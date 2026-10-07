package kurel

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// labelReach is one field of a kind's API type through which metadata reaches
// what the cluster creates from the object: pods, or other objects.
type labelReach struct {
	// path is the field under its json names, a list as "[]" and a map of
	// objects as "{}".
	path string
	// shape is what the field holds: reachMetadata, reachLabels or
	// reachAnnotations.
	shape string
	// field is the Go field that declares it: package path, type and field.
	field string
}

const (
	// reachMetadata is an object with `labels` and `annotations`.
	reachMetadata = "metadata"
	// reachLabels is a map of labels on its own, reachAnnotations one of
	// annotations.
	reachLabels      = "labels"
	reachAnnotations = "annotations"
)

// jsonFieldName returns the json name of f, whether it is inlined into its
// parent, and whether it is serialized at all.
func jsonFieldName(f reflect.StructField) (name string, inline, ok bool) {
	tag, tagged := f.Tag.Lookup("json")
	name, options, _ := strings.Cut(tag, ",")
	if name == "-" || !f.IsExported() {
		return "", false, false
	}
	if strings.Contains(options, "inline") || (f.Anonymous && (!tagged || name == "")) {
		return "", true, true
	}
	if name == "" {
		name = f.Name
	}
	return name, false, true
}

// isStringMap reports whether t is a map of strings to strings, whatever the
// two string types are named.
func isStringMap(t reflect.Type) bool {
	return t.Kind() == reflect.Map && t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String
}

func indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// structJSONFields returns the fields of t by json name, inlined ones included.
func structJSONFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, inline, ok := jsonFieldName(f)
		switch {
		case !ok:
		case inline:
			if inner := indirect(f.Type); inner.Kind() == reflect.Struct {
				maps.Copy(out, structJSONFields(inner))
			}
		default:
			out[name] = f
		}
	}
	return out
}

// isMetadataShaped reports whether t holds `labels` and `annotations`, both
// maps of strings: metav1.ObjectMeta, and every type an API embeds in its place.
func isMetadataShaped(t reflect.Type) bool {
	fields := structJSONFields(t)
	labels, hasLabels := fields["labels"]
	annotations, hasAnnotations := fields["annotations"]
	return hasLabels && hasAnnotations && isStringMap(indirect(labels.Type)) && isStringMap(indirect(annotations.Type))
}

// isSelectorShaped reports whether t is a label selector: its matchLabels
// select by labels and put none on anything.
func isSelectorShaped(t reflect.Type) bool {
	fields := structJSONFields(t)
	_, labels := fields["matchLabels"]
	_, expressions := fields["matchExpressions"]
	return labels && expressions
}

// walkLabelReach finds, below t at path, every field that hands metadata on:
// a field that is metadata-shaped, and a map of strings whose name says it
// holds labels or annotations. A selector, and a field named matchLabels, select
// by labels and are none. on is the stack of struct types being walked, which
// ends a type that holds itself.
func walkLabelReach(t reflect.Type, path, field string, on map[reflect.Type]bool, found *[]labelReach) {
	t = indirect(t)
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		walkLabelReach(t.Elem(), path+"[]", field, on, found)
	case reflect.Map:
		if indirect(t.Elem()).Kind() == reflect.Struct {
			walkLabelReach(t.Elem(), path+"{}", field, on, found)
		}
	case reflect.Struct:
		if on[t] || isSelectorShaped(t) {
			return
		}
		on[t] = true
		defer delete(on, t)
		metadata := path != "" && isMetadataShaped(t)
		if metadata {
			*found = append(*found, labelReach{path: path, shape: reachMetadata, field: field})
		}
		for i := range t.NumField() {
			f := t.Field(i)
			name, inline, ok := jsonFieldName(f)
			if !ok || name == "matchLabels" {
				continue
			}
			at, declared := path, t.PkgPath()+"."+t.Name()+"."+f.Name
			if !inline {
				if path == "" && (name == "metadata" || name == "status") {
					// The object's own metadata, and what the cluster writes.
					continue
				}
				at = strings.TrimPrefix(path+"."+name, ".")
			}
			if isStringMap(indirect(f.Type)) {
				lower := strings.ToLower(name)
				switch {
				case metadata && (name == "labels" || name == "annotations"):
				case strings.HasSuffix(lower, "labels"):
					*found = append(*found, labelReach{path: at, shape: reachLabels, field: declared})
				case strings.HasSuffix(lower, "annotations"):
					*found = append(*found, labelReach{path: at, shape: reachAnnotations, field: declared})
				}
				continue
			}
			walkLabelReach(f.Type, at, declared, on, found)
		}
	default:
		// A scalar, an interface, a channel or a function holds no metadata.
	}
}

// kindObject returns the object the kind component typ generates from its
// registry fixture: the one typed object of the kind the handler declares.
func kindObject(t *testing.T, typ string, handler oam.ComponentHandler) client.Object {
	t.Helper()
	fx := componentLabelFixtures[typ]
	props := fx.props
	if fx.propsFor != nil {
		props = fx.propsFor(t)
	}
	cfg, err := handler.ToApplicationConfig(&oam.Component{Name: "c", Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig: %v", typ, err)
	}
	objs, err := cfg.Generate(stack.NewApplication("c", "default", cfg))
	if err != nil {
		t.Fatalf("%s: Generate: %v", typ, err)
	}
	kind, _ := handler.(oam.ComponentObjectProvider).ComponentObject()
	var typed []client.Object
	for _, o := range objs {
		if _, bare := (*o).(*unstructured.Unstructured); bare {
			continue
		}
		if (*o).GetObjectKind().GroupVersionKind().GroupKind() == kind {
			typed = append(typed, *o)
		}
	}
	if len(typed) != 1 {
		t.Fatalf("%s: %d typed objects that state the kind %s among the %d generated; the walk needs the kind's Go type, and the checks tell a kind by the group and kind its object states",
			typ, len(typed), kind, len(objs))
	}
	return typed[0]
}

// labelReachUnread is a field the walk finds that neither check reads.
type labelReachUnread struct {
	// field is the Go field that declares it, as labelReach.field has it.
	field string
	// reason is why it is not read.
	reason string
}

const (
	// unreadFlux: decided with the rest of what Flux hands on.
	unreadFlux = "what a Flux object hands on to the objects it applies: the triage of go-kure/launcher#790"
	// unreadTemplate: these become the metadata of a PersistentVolumeClaim, not
	// of a pod.
	unreadTemplate = "a volume claim template's metadata: its own item of go-kure/launcher#790"
)

// labelReachReservedOnly names every field the walk finds that hands metadata
// on to objects an operator creates that are no pods. The reserved keys are
// held there (a row of the holders table that reaches no pods, pkg/oam
// operatorMetadataKinds); the component label, which selects pods, is not.
// says is the field's own comment in its API type, which says what it reaches.
var labelReachReservedOnly = []struct {
	field, says string
}{
	{
		field: "github.com/cert-manager/cert-manager/pkg/apis/acme/v1.ACMEChallengeSolverHTTP01IngressTemplate.ACMEChallengeSolverHTTP01IngressObjectMeta",
		says:  "Labels that should be added to the created ACME HTTP01 solver ingress.",
	},
	{
		field: "github.com/cert-manager/cert-manager/pkg/apis/acme/v1.ACMEChallengeSolverHTTP01GatewayHTTPRoute.Labels",
		says:  "Custom labels that will be applied to HTTPRoutes created by cert-manager while solving HTTP-01 challenges.",
	},
	{
		field: "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1.CertificateSpec.SecretTemplate",
		says:  "Defines annotations and labels to be copied to the Certificate's Secret.",
	},
	{
		field: "github.com/cloudnative-pg/cloudnative-pg/api/v1.ServiceTemplateSpec.ObjectMeta",
		says:  "ServiceTemplateSpec is a structure allowing the user to set a template for Service generation.",
	},
	{
		field: "github.com/cloudnative-pg/cloudnative-pg/api/v1.ServiceAccountTemplate.Metadata",
		says:  "Metadata are the metadata to be used for the generated service account",
	},
	{
		field: "github.com/cloudnative-pg/cloudnative-pg/api/v1.BackupConfiguration.VolumeSnapshot",
		says:  "Labels are key-value pairs that will be added to .metadata.labels snapshot resources.",
	},
	{
		field: "github.com/external-secrets/external-secrets/apis/externalsecrets/v1.ExternalSecretTemplate.Metadata",
		says:  "ExternalSecretTemplate defines a blueprint for the created Secret resource.",
	},
	{
		field: "github.com/external-secrets/external-secrets/apis/externalsecrets/v1.ClusterExternalSecretSpec.ExternalSecretMetadata",
		says:  "The metadata of the external secrets to be created",
	},
	{
		field: "github.com/fluxcd/helm-controller/api/v2.HelmChartTemplate.ObjectMeta",
		says:  "HelmChartTemplate defines the template from which the controller will generate a v1.HelmChart object",
	},
	{
		field: "github.com/backube/volsync/api/v1alpha1.ReplicationDestinationRsyncSpec.ServiceAnnotations",
		says:  "serviceAnnotations defines annotations that will be added to the service created for incoming SSH connections.",
	},
	{
		field: "github.com/backube/volsync/api/v1alpha1.ReplicationDestinationRsyncTLSSpec.ServiceAnnotations",
		says:  "serviceAnnotations defines annotations that will be added to the service created for incoming SSH connections.",
	},
}

// labelReachNotRead names every field the walk finds that the checks do not
// read, with the reason. A field of a kind's API type that hands metadata on is
// either refused through (a row of the holders table, pkg/oam
// operatorMetadataKinds and podTemplateKinds), by both checks or, listed in
// labelReachReservedOnly, by the reserved keys alone, or stands here.
var labelReachNotRead = []labelReachUnread{
	// What a Flux object hands on.
	{field: "github.com/fluxcd/kustomize-controller/api/v1.KustomizationSpec.CommonMetadata", reason: unreadFlux},
	{field: "github.com/fluxcd/helm-controller/api/v2.HelmReleaseSpec.CommonMetadata", reason: unreadFlux},
	{field: "github.com/fluxcd/source-watcher/api/v2/v1beta1.ArtifactGeneratorSpec.CommonMetadata", reason: unreadFlux},

	// A volume claim template's: a StatefulSet's spec.volumeClaimTemplates, and
	// the claim template of an ephemeral volume, in every pod spec and in a
	// CloudNativePG Cluster's spec.ephemeralVolumeSource. A CronJob's job
	// template is held (pkg/oam podTemplateKinds).
	{field: "k8s.io/api/core/v1.PersistentVolumeClaim.ObjectMeta", reason: unreadTemplate},
	{field: "k8s.io/api/core/v1.PersistentVolumeClaimTemplate.ObjectMeta", reason: unreadTemplate},

	// Maps that are named like metadata and are the metadata of no object.
	{
		field:  "k8s.io/api/core/v1.PodCertificateProjection.UserAnnotations",
		reason: "no metadata of any object: the kubelet passes them to the certificate's signer in the request it makes",
	},
	{
		field:  "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1.RuleGroup.Labels",
		reason: "no metadata of any object: labels of the alerts and series the group's rules produce",
	},
	{
		field:  "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1.RuleGroup.Rules",
		reason: "no metadata of any object: the labels and annotations of the alert or series a rule produces",
	},
	{
		field:  "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1.ProbeTargetStaticConfig.Labels",
		reason: "no metadata of any object: labels of the series scraped from the targets",
	},
	{
		field:  "github.com/cilium/cilium/pkg/policy/api.AWSGroup.Labels",
		reason: "no metadata of any object: the tags a rule selects cloud instances by",
	},
}

// labelReachUnwalked are fields that hand metadata on and that the walk cannot
// find, because no name or type of theirs says so. They are written here by
// hand, so the limit of the walk is visible; the test only holds that the
// field is still there.
var labelReachUnwalked = []struct {
	typ, path, reason string
}{
	{"helmrelease", "spec.postRenderers", unreadFlux + "; a post-renderer's kustomize patches may write any label onto what the chart renders"},
}

// labelReachProbe is a passthrough object of a kind that holds one label or
// annotation in the field a labelReach names.
func labelReachProbe(gvk schema.GroupVersionKind, reach labelReach, annotation bool, key, value string) map[string]any {
	var held any = map[string]any{key: value}
	if reach.shape == reachMetadata {
		in := "labels"
		if annotation {
			in = "annotations"
		}
		held = map[string]any{in: held}
	}
	steps := strings.Split(reach.path, ".")
	for _, step := range slices.Backward(steps) {
		name, list := strings.CutSuffix(step, "[]")
		name, keyed := strings.CutSuffix(name, "{}")
		if list {
			held = []any{held}
		}
		if keyed {
			held = map[string]any{"k": held}
		}
		held = map[string]any{name: held}
	}
	object := map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]any{"name": "probe"},
	}
	maps.Copy(object, held.(map[string]any))
	return object
}

// labelReachBuild builds a document whose one component is a passthrough of
// object, with two metadata keys reserved.
func labelReachBuild(t *testing.T, object map[string]any) error {
	t.Helper()
	doc, err := yaml.Marshal(map[string]any{
		"apiVersion": "launcher.gokure.dev/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": "shop", "namespace": "default"},
		"spec": map[string]any{"components": []any{map[string]any{
			"name": "probe", "type": "passthrough", "properties": map[string]any{"object": object},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes(doc, nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v\n%s", err, doc)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v\n%s", err, doc)
	}
	cluster, err := transformer.Transform(app, oam.TransformContext{
		Domain:               kurelDomain,
		ReservedMetadataKeys: []string{"platform.example/", "example.org/tenant"},
	})
	if err != nil {
		return err
	}
	_, err = oam.GenerateApplications(cluster)
	return err
}

// labelReachHold is which of the two checks hold a field.
type labelReachHold struct {
	// componentProbed says the component label was probed there: the field
	// holds labels. componentLabel says another component's value is refused
	// there, and reservedKeys that a reserved key is.
	componentProbed, componentLabel, reservedKeys bool
}

// is reports whether h is the hold of a field the component label is held at
// or not (componentLabel) and the reserved keys (reservedKeys). The component
// label is not asked of a field that holds no labels.
func (h labelReachHold) is(componentLabel, reservedKeys bool) bool {
	return h.reservedKeys == reservedKeys && (!h.componentProbed || h.componentLabel == componentLabel)
}

func (h labelReachHold) String() string {
	says := func(refused bool) string {
		if refused {
			return "refused"
		}
		return "built"
	}
	component := "not probed"
	if h.componentProbed {
		component = says(h.componentLabel)
	}
	return "the component label " + component + ", a reserved key " + says(h.reservedKeys)
}

// labelReachHeld says which checks hold the field reach names on an object of
// gvk: whether a passthrough object that holds another component's value for
// the component label there is refused, and whether one that holds a reserved
// key there, as a label and as an annotation, is, each by the typed refusal
// that names the field. The same object with a key that is neither builds
// first, so a refusal is the key's.
func labelReachHeld(t *testing.T, gvk schema.GroupVersionKind, reach labelReach) labelReachHold {
	t.Helper()
	labels, annotations := reach.shape != reachAnnotations, reach.shape != reachLabels
	// mapPath is the path of the labels or the annotations, as a refusal names
	// it: the probe holds one element of each list.
	mapPath := func(annotation bool) string {
		at := strings.ReplaceAll(reach.path, "[]", "[0]")
		switch {
		case reach.shape != reachMetadata:
			return at
		case annotation:
			return at + ".annotations"
		default:
			return at + ".labels"
		}
	}
	componentKey := oam.ComponentLabelKeyForDomain(kurelDomain)

	// probe reports whether the object that holds key there is refused.
	probe := func(what string, annotation bool, key, value string, isRefusal func(error) bool) bool {
		t.Helper()
		if err := labelReachBuild(t, labelReachProbe(gvk, reach, annotation, "example.com/owner", "a")); err != nil {
			t.Fatalf("%s at %s: the probe does not build with a key nobody holds: %v", gvk.Kind, reach.path, err)
		}
		err := labelReachBuild(t, labelReachProbe(gvk, reach, annotation, key, value))
		switch {
		case err == nil:
			return false
		case isRefusal(err):
			return true
		default:
			t.Fatalf("%s at %s, %s: %v, which is neither a build nor the refusal of the key there", gvk.Kind, reach.path, what, err)
			return false
		}
	}
	reserved := func(annotation bool) func(error) bool {
		return func(err error) bool {
			var got *oam.ReservedMetadataKeyError
			return errors.As(err, &got) && got.Kind == gvk.GroupKind() && got.Name == "probe" &&
				got.Holder != oam.ReservedKeyInObjectMetadata && got.Path == mapPath(annotation) &&
				got.Annotation == annotation && got.Key == "platform.example/zone"
		}
	}
	var hold labelReachHold
	var refused, built []string
	reservedProbe := func(what string, annotation bool) {
		t.Helper()
		if probe(what, annotation, "platform.example/zone", "a", reserved(annotation)) {
			refused = append(refused, what)
		} else {
			built = append(built, what)
		}
	}
	if labels {
		hold.componentProbed = true
		hold.componentLabel = probe("another component's label", false, componentKey, "another", func(err error) bool {
			var got *oam.ComponentLabelError
			return errors.As(err, &got) && got.Refused == oam.ComponentLabelForeignValue &&
				got.Kind == gvk.GroupKind() && got.Path == mapPath(false) && got.Value == "another"
		})
		reservedProbe("a reserved label", false)
	}
	if annotations {
		reservedProbe("a reserved annotation", true)
	}
	if len(refused) > 0 && len(built) > 0 {
		t.Errorf("%s at %s is held by half for the reserved keys: refused with %s, built with %s",
			gvk.Kind, reach.path, strings.Join(refused, ", "), strings.Join(built, ", "))
	}
	hold.reservedKeys = len(built) == 0
	return hold
}

// TestLabelReach_EveryFieldIsHeldOrListed derives, from the API types of the
// kind components, every field through which labels and annotations reach what
// the cluster creates from an object, and holds each to one of three: the
// checks of go-kure/launcher#790 refuse through it (the component label's value
// and the reserved metadata keys, pkg/oam metadataHolders), which a passthrough
// object of the kind shows; the reserved keys alone do, and it stands in
// labelReachReservedOnly; or neither does, and it stands in labelReachNotRead
// with its reason. A field a dependency bump adds fails the test until it is
// one of them, and so does an entry of a list that names a field no kind has
// any more.
//
// The Go type of a kind is that of the object the kind generates from its
// fixture in the registry, so a new kind is walked with no row to remember.
// The walk finds a field whose type holds `labels` and `annotations` (an
// ObjectMeta, and every type an API puts in its place) and a map of strings
// whose name ends in labels or annotations, below the object's own metadata and
// outside its status.
//
// Its limits: it walks registered kinds only, so a row for a kind that has no
// kind component (the Prometheus operator's) is held by the tests of the table
// alone; and a field that hands labels on under a name and a type that say
// neither is not found (labelReachUnwalked).
func TestLabelReach_EveryFieldIsHeldOrListed(t *testing.T) {
	listed := map[string]labelReachUnread{}
	for _, entry := range labelReachNotRead {
		if _, twice := listed[entry.field]; twice {
			t.Errorf("labelReachNotRead names %s twice", entry.field)
		}
		if entry.reason == "" {
			t.Errorf("labelReachNotRead: %s has no reason", entry.field)
		}
		listed[entry.field] = entry
	}
	reservedOnly := map[string]bool{}
	for _, entry := range labelReachReservedOnly {
		if reservedOnly[entry.field] {
			t.Errorf("labelReachReservedOnly names %s twice", entry.field)
		}
		if _, unread := listed[entry.field]; unread {
			t.Errorf("labelReachReservedOnly and labelReachNotRead both name %s", entry.field)
		}
		if entry.says == "" {
			t.Errorf("labelReachReservedOnly: %s does not say what the field reaches, by its type's own comment", entry.field)
		}
		reservedOnly[entry.field] = true
	}
	walked := map[string]bool{}
	types := map[string]reflect.Type{}

	handlers := builtinComponentHandlers()
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		if _, declares := handlers[typ].(oam.ComponentObjectProvider); !declares {
			continue
		}
		obj := kindObject(t, typ, handlers[typ])
		gvk := obj.GetObjectKind().GroupVersionKind()
		types[typ] = reflect.TypeOf(obj).Elem()
		var found []labelReach
		walkLabelReach(types[typ], "", "", map[reflect.Type]bool{}, &found)
		for _, reach := range found {
			hold := labelReachHeld(t, gvk, reach)
			_, unread := listed[reach.field]
			walked[reach.field] = true
			switch {
			case unread && !hold.is(false, false):
				t.Errorf("%s: %s (%s) is refused through (%s), and labelReachNotRead lists it as not read: take the entry out", typ, reach.path, reach.field, hold)
			case reservedOnly[reach.field] && !hold.is(false, true):
				t.Errorf("%s: %s (%s) is not held as labelReachReservedOnly lists it, by the reserved keys alone: %s", typ, reach.path, reach.field, hold)
			case !unread && !reservedOnly[reach.field] && !hold.is(true, true):
				t.Errorf("%s: %s (%s, %s) hands metadata on and is neither held (%s) nor listed: add a row to the holders table (pkg/oam operatorMetadataKinds), or an entry to labelReachReservedOnly or labelReachNotRead",
					typ, reach.path, reach.field, reach.shape, hold)
			}
		}
	}

	for _, entry := range labelReachNotRead {
		if !walked[entry.field] {
			t.Errorf("labelReachNotRead names %s, which no kind component's type holds any more: take the entry out", entry.field)
		}
	}
	for _, entry := range labelReachReservedOnly {
		if !walked[entry.field] {
			t.Errorf("labelReachReservedOnly names %s, which no kind component's type holds any more: take the entry out", entry.field)
		}
	}
	for _, entry := range labelReachUnwalked {
		at, there := types[entry.typ]
		if !there {
			t.Errorf("labelReachUnwalked names %s, which is no kind component's type any more: take the entry out", entry.typ)
			continue
		}
		for _, step := range strings.Split(entry.path, ".") {
			at = indirect(at)
			for at != nil && (at.Kind() == reflect.Slice || at.Kind() == reflect.Map) {
				at = indirect(at.Elem())
			}
			var f reflect.StructField
			if at == nil || at.Kind() != reflect.Struct {
				there = false
				break
			}
			if f, there = structJSONFields(at)[step]; !there {
				break
			}
			at = f.Type
		}
		if !there || entry.reason == "" {
			t.Errorf("labelReachUnwalked names %s of %s, which its type does not hold, or gives no reason", entry.path, entry.typ)
		}
	}
}
