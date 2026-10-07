package oam

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// The component label is authoritative (go-kure/launcher#790). The ownership
// wrapper holds every object its config generates to it before it writes the
// label itself: a value the object already carries under the label's key is the
// owning component's, or generation fails. The NetworkPolicies synthesized for
// a component select by the label, so a pod that carried another component's
// value would be let in wherever that component's pods are.
//
// One table says where an object holds metadata (metadataHolders), and both
// checks of the wrapper read through it: this one and the reserved metadata
// keys (reserved_metadata.go).

// monitoringGroup is the API group of the Prometheus operator's kinds,
// volsyncGroup that of VolSync's, certManagerGroup that of cert-manager's
// kinds, gatewayGroup that of the Gateway API, externalSecretsGroup that of
// External Secrets and helmGroup that of Flux's HelmRelease; cnpgPoolerKind is
// the CloudNativePG Pooler.
const (
	monitoringGroup      = "monitoring.coreos.com"
	volsyncGroup         = "volsync.backube"
	certManagerGroup     = "cert-manager.io"
	gatewayGroup         = "gateway.networking.k8s.io"
	externalSecretsGroup = "external-secrets.io"
	helmGroup            = "helm.toolkit.fluxcd.io"
	cnpgPoolerKind       = "Pooler"
)

// listStep, as a step of a holder's path, stands for every element of the list
// the step before it names.
const listStep = "[]"

// metadataHolder is one place an object holds labels and annotations in: its
// own metadata, metadata that becomes that of pods the cluster runs for it, or
// metadata an operator copies onto other objects it creates for it.
type metadataHolder struct {
	// path is where the object holds it: the object with the `labels` and the
	// `annotations`, or, of a label map (labelMap) or an annotation map
	// (annotationMap), the map itself. A listStep in it reads every element of
	// a list.
	path []string
	// in is the holder as a reserved-key refusal names it.
	in ReservedKeyHolder
	// labelMap says that what path holds is a map of labels and nothing else:
	// there are no annotations beside it. annotationMap says the same of a map
	// of annotations.
	labelMap, annotationMap bool
	// noPods says the metadata reaches objects that are no pods. The reserved
	// keys are read there; the component label, which selects pods, is not.
	noPods bool
}

// heldMetadata is what a holder holds on one object: once, or once per element
// of the lists its path goes through.
type heldMetadata struct {
	// metadata is the object with the `labels` and the `annotations`: of a
	// label map, the map as its labels, and of an annotation map, the map as
	// its annotations.
	metadata map[string]any
	// path is where it is, with the index of each list element
	// ("spec.acme.solvers[1].http01.ingress.podTemplate.metadata"), and labels
	// and annotations the paths of its labels and its annotations, as a refusal
	// prints them.
	path, labels, annotations string
}

// held returns what h holds in content. An absent or null step of the path
// holds nothing, as does a null element of a list; a step that is no object,
// or no list where the path has a listStep, is an error.
func (h metadataHolder) held(content map[string]any) ([]heldMetadata, error) {
	var all []heldMetadata
	if err := h.collect(content, h.path, "", &all); err != nil {
		return nil, err
	}
	return all, nil
}

// collect appends to all what h holds below m at the steps left, where at is
// the path of m.
func (h metadataHolder) collect(m map[string]any, steps []string, at string, all *[]heldMetadata) error {
	for i, field := range steps {
		at = strings.TrimPrefix(at+"."+field, ".")
		if i+1 < len(steps) && steps[i+1] == listStep {
			raw, named := m[field]
			if !named || raw == nil {
				return nil
			}
			list, isList := raw.([]any)
			if !isList {
				return errors.Errorf("%s is a %T, not a list", field, raw)
			}
			for n, element := range list {
				if element == nil {
					continue
				}
				object, isObject := element.(map[string]any)
				if !isObject {
					return errors.Errorf("%s[%d] is a %T, not an object", field, n, element)
				}
				if err := h.collect(object, steps[i+2:], fmt.Sprintf("%s[%d]", at, n), all); err != nil {
					return err
				}
			}
			return nil
		}
		next, found, err := objectField(m, field)
		if err != nil || !found {
			return err
		}
		m = next
	}
	switch {
	case h.labelMap:
		*all = append(*all, heldMetadata{metadata: map[string]any{"labels": m}, path: at, labels: at})
	case h.annotationMap:
		*all = append(*all, heldMetadata{metadata: map[string]any{"annotations": m}, path: at, annotations: at})
	default:
		*all = append(*all, heldMetadata{metadata: m, path: at, labels: at + ".labels", annotations: at + ".annotations"})
	}
	return nil
}

// operatorMetadataKind is one place an object of a group and kind holds
// metadata in that an operator puts on what it creates.
type operatorMetadataKind struct {
	group, kind string
	holder      metadataHolder
}

// operatorMetadataKinds are the kinds whose object holds metadata an operator
// puts on what it creates: pods, or objects that are no pods (noPods). The
// wrapper reads it and writes nothing there: the pods of such an object carry
// the operator's labels, and the component label only where the document or a
// kind puts it.
//
// Metadata that reaches pods is held to both checks:
//
//   - a CloudNativePG Cluster's spec.inheritedMetadata goes onto every object
//     the operator creates for the cluster, its pods among them;
//   - a Pooler's spec.template is the pod template of its pods;
//   - spec.podMetadata of a Prometheus, a PrometheusAgent, an Alertmanager and a
//     ThanosRuler is the metadata of its pods;
//   - moverPodLabels of each mover of a VolSync ReplicationSource and
//     ReplicationDestination are labels of the pods that move its data: a map
//     of labels, with no annotations beside it;
//   - podTemplate.metadata of each HTTP01 solver of a cert-manager Issuer and
//     ClusterIssuer (spec.acme.solvers[].http01.ingress and .gatewayHTTPRoute)
//     is the metadata of the pods that answer its challenges;
//   - a Gateway's spec.infrastructure holds labels and annotations for what
//     the controller creates for the Gateway, which may be pods.
//
// Metadata an operator copies onto objects it creates that are no pods is held
// to the consumer's reserved keys alone (go-kure/launcher#790): the component
// label selects pods, and these objects are none.
//
//   - the ingress template's metadata of each HTTP01 solver of an Issuer and a
//     ClusterIssuer (spec.acme.solvers[].http01.ingress.ingressTemplate), which
//     goes onto the solver's Ingress, and the labels of a solver on a Gateway
//     (spec.acme.solvers[].http01.gatewayHTTPRoute.labels), which go onto its
//     HTTPRoute;
//   - a Certificate's spec.secretTemplate, which goes onto its Secret;
//   - of a CloudNativePG Cluster, the service template of each additional
//     Service (spec.managed.services.additional[].serviceTemplate.metadata),
//     the service account template (spec.serviceAccountTemplate.metadata) and
//     spec.backup.volumeSnapshot, which go onto those Services, the
//     ServiceAccount and the VolumeSnapshots; a Pooler's service template
//     (spec.serviceTemplate.metadata);
//   - the template of the Secret an ExternalSecret creates
//     (spec.target.template.metadata); of a ClusterExternalSecret, the metadata
//     of the ExternalSecrets it creates (spec.externalSecretMetadata) and the
//     template of their Secrets
//     (spec.externalSecretSpec.target.template.metadata);
//   - a HelmRelease's spec.chart.metadata, which goes onto the HelmChart the
//     controller creates;
//   - serviceAnnotations of the rsync and rsyncTLS movers of a VolSync
//     ReplicationDestination, which go onto the Service of the mover: a map of
//     annotations, with no labels beside it.
//
// A kind is told by the group and kind the object states. Of the typed objects
// that state none, only the ones statedOrTypedKind names are recognized.
//
// The table is held to the API types of the kind components by
// TestLabelReach_EveryFieldIsHeldOrListed (pkg/cmd/kurel), which finds every
// field of them that hands metadata on and names each one that is not read.
var operatorMetadataKinds = slices.Concat(
	[]operatorMetadataKind{
		{cnpgGroup, cnpgClusterKind, metadataHolder{path: []string{"spec", "inheritedMetadata"}, in: ReservedKeyInInheritedMetadata}},
		{cnpgGroup, cnpgPoolerKind, metadataHolder{path: []string{"spec", "template", "metadata"}, in: ReservedKeyInPodTemplate}},
		{monitoringGroup, "Prometheus", metadataHolder{path: []string{"spec", "podMetadata"}, in: ReservedKeyInPodMetadata}},
		{monitoringGroup, "PrometheusAgent", metadataHolder{path: []string{"spec", "podMetadata"}, in: ReservedKeyInPodMetadata}},
		{monitoringGroup, "Alertmanager", metadataHolder{path: []string{"spec", "podMetadata"}, in: ReservedKeyInPodMetadata}},
		{monitoringGroup, "ThanosRuler", metadataHolder{path: []string{"spec", "podMetadata"}, in: ReservedKeyInPodMetadata}},
		{gatewayGroup, "Gateway", metadataHolder{path: []string{"spec", "infrastructure"}, in: ReservedKeyInInfrastructure}},

		// Metadata that reaches objects that are no pods.
		{certManagerGroup, "Certificate", metadataHolder{path: []string{"spec", "secretTemplate"}, in: ReservedKeyInSecretTemplate, noPods: true}},
		{cnpgGroup, cnpgClusterKind, metadataHolder{
			path: []string{"spec", "managed", "services", "additional", listStep, "serviceTemplate", "metadata"}, in: ReservedKeyInServiceTemplate, noPods: true,
		}},
		{cnpgGroup, cnpgClusterKind, metadataHolder{path: []string{"spec", "serviceAccountTemplate", "metadata"}, in: ReservedKeyInServiceAccountTemplate, noPods: true}},
		{cnpgGroup, cnpgClusterKind, metadataHolder{path: []string{"spec", "backup", "volumeSnapshot"}, in: ReservedKeyInVolumeSnapshot, noPods: true}},
		{cnpgGroup, cnpgPoolerKind, metadataHolder{path: []string{"spec", "serviceTemplate", "metadata"}, in: ReservedKeyInServiceTemplate, noPods: true}},
		{externalSecretsGroup, "ExternalSecret", metadataHolder{path: []string{"spec", "target", "template", "metadata"}, in: ReservedKeyInSecretTemplate, noPods: true}},
		{externalSecretsGroup, "ClusterExternalSecret", metadataHolder{path: []string{"spec", "externalSecretMetadata"}, in: ReservedKeyInExternalSecretMetadata, noPods: true}},
		{externalSecretsGroup, "ClusterExternalSecret", metadataHolder{
			path: []string{"spec", "externalSecretSpec", "target", "template", "metadata"}, in: ReservedKeyInSecretTemplate, noPods: true,
		}},
		{helmGroup, "HelmRelease", metadataHolder{path: []string{"spec", "chart", "metadata"}, in: ReservedKeyInChartTemplate, noPods: true}},
	},
	moverPodLabels("ReplicationSource", "rsync", "rsyncTLS", "rclone", "restic", "syncthing"),
	moverPodLabels("ReplicationDestination", "rsync", "rsyncTLS", "rclone", "restic"),
	moverServiceAnnotations("ReplicationDestination", "rsync", "rsyncTLS"),
	solverHolders("Issuer"),
	solverHolders("ClusterIssuer"),
)

// solverHolders returns the rows of a cert-manager issuer kind, of every solver
// in the list: the pod template's metadata of each HTTP01 solver, the ingress
// template's of a solver by Ingress and the HTTPRoute labels of a solver on a
// Gateway.
func solverHolders(kind string) []operatorMetadataKind {
	solver := func(steps ...string) []string {
		return append([]string{"spec", "acme", "solvers", listStep, "http01"}, steps...)
	}
	var rows []operatorMetadataKind
	for _, s := range []string{"ingress", "gatewayHTTPRoute"} {
		rows = append(rows, operatorMetadataKind{certManagerGroup, kind, metadataHolder{
			path: solver(s, "podTemplate", "metadata"),
			in:   ReservedKeyInSolverPodTemplate,
		}})
	}
	return append(rows,
		operatorMetadataKind{certManagerGroup, kind, metadataHolder{
			path: solver("ingress", "ingressTemplate", "metadata"), in: ReservedKeyInSolverIngressTemplate, noPods: true,
		}},
		operatorMetadataKind{certManagerGroup, kind, metadataHolder{
			path: solver("gatewayHTTPRoute", "labels"), in: ReservedKeyInSolverHTTPRoute, labelMap: true, noPods: true,
		}},
	)
}

// moverServiceAnnotations returns the rows of a VolSync kind:
// spec.<mover>.serviceAnnotations of each of its movers, which go onto the
// Service of the mover.
func moverServiceAnnotations(kind string, movers ...string) []operatorMetadataKind {
	rows := make([]operatorMetadataKind, 0, len(movers))
	for _, mover := range movers {
		rows = append(rows, operatorMetadataKind{volsyncGroup, kind, metadataHolder{
			path: []string{"spec", mover, "serviceAnnotations"}, in: ReservedKeyInMoverService, annotationMap: true, noPods: true,
		}})
	}
	return rows
}

// moverPodLabels returns the rows of a VolSync kind: spec.<mover>.moverPodLabels
// of each of its movers.
func moverPodLabels(kind string, movers ...string) []operatorMetadataKind {
	rows := make([]operatorMetadataKind, 0, len(movers))
	for _, mover := range movers {
		rows = append(rows, operatorMetadataKind{volsyncGroup, kind, metadataHolder{
			path: []string{"spec", mover, "moverPodLabels"}, in: ReservedKeyInMoverPodLabels, labelMap: true,
		}})
	}
	return rows
}

// metadataHolders returns every place an object of group and kind holds
// metadata in: its own first, then a CronJob's job template's and its pod
// template's on a kind that has one (podTemplateKinds), then what an operator
// hands on (operatorMetadataKinds). The reserved keys are read in every one of
// them, the component label in all but those that reach no pods (noPods).
// Neither check reads the metadata a Flux object hands on to what it applies
// (spec.commonMetadata), or a volume claim template's.
func metadataHolders(group, kind string) []metadataHolder {
	holders := []metadataHolder{{path: []string{"metadata"}, in: ReservedKeyInObjectMetadata}}
	for _, k := range podTemplateKinds {
		if group == k.group && kind == k.kind {
			if k.jobTemplate != nil {
				holders = append(holders, metadataHolder{path: append(slices.Clone(k.jobTemplate), "metadata"), in: ReservedKeyInJobTemplate})
			}
			holders = append(holders, metadataHolder{path: append(slices.Clone(k.spec), "template", "metadata"), in: ReservedKeyInPodTemplate})
		}
	}
	for _, k := range operatorMetadataKinds {
		if group == k.group && kind == k.kind {
			holders = append(holders, k.holder)
		}
	}
	return holders
}

// generatedObject is an object an application generated, read once for both
// checks.
type generatedObject struct {
	obj         client.Object
	group, kind string
	// where names the object in a refusal: `Deployment "web"`, with the Go type
	// in place of the kind for a typed object that states none.
	where string
	// content is the object as the cluster would be sent it. The checks only
	// read it.
	content map[string]any
}

func readGeneratedObject(obj client.Object) (generatedObject, error) {
	g := generatedObject{obj: obj}
	g.group, g.kind = statedOrTypedKind(obj)
	g.where = fmt.Sprintf("%s %q", g.kind, obj.GetName())
	if g.kind == "" {
		g.where = fmt.Sprintf("%T %q", obj, obj.GetName())
	}
	content, err := objectContent(obj)
	if err != nil {
		return g, err
	}
	g.content = content
	return g, nil
}

// check holds obj to the consumer's reserved metadata keys (checkReserved) and
// to the component label (checkComponentLabel), and with it every object obj
// stands for when Flux applies it (appliedObjects, a list envelope's members).
// An object of an application the document as a whole owns has no component
// label to be held to.
//
// The reserved keys are checked on obj and all it stands for before the
// component label is checked on any of them, so a reserved key is refused as
// one (ErrReservedMetadataKey) whatever the labels beside it hold.
func (o *ownedConfig) check(obj client.Object) error {
	if o.reserved == nil && o.component == "" {
		return nil
	}
	checked := []client.Object{obj}
	if u, ok := obj.(*unstructured.Unstructured); ok {
		for _, applied := range appliedObjects(u) {
			if applied != obj {
				checked = append(checked, applied)
			}
		}
	}
	// An object that cannot be read is refused by the first check that reads it.
	unreadable := "component label"
	if o.reserved != nil {
		unreadable = "reserved metadata keys"
	}
	generated := make([]generatedObject, 0, len(checked))
	for _, c := range checked {
		g, err := readGeneratedObject(c)
		if err != nil {
			return errors.Errorf("%s: %s: %w", unreadable, g.where, err)
		}
		if o.reserved != nil {
			if err := o.checkReserved(g); err != nil {
				return err
			}
		}
		generated = append(generated, g)
	}
	if o.component == "" {
		return nil
	}
	for _, g := range generated {
		if err := o.checkComponentLabel(g); err != nil {
			return err
		}
	}
	return nil
}

// componentLabelValues returns the values the component label's key may hold on
// what the config generates: the owning component's.
//
// With ComponentLabelKey "app" there is a second one. The kinds write the `app`
// label themselves, valued with the name of the component after lowering they
// generate for, which is the owner's except for an entry a lowering rule
// emitted under a name of its own. That value is launcher's own output, and no
// document can change it, so it is accepted on that entry's objects. It is no
// other component's value: the transform refuses a document in which it would
// be (checkEntryLabelValues). Such an entry's pods then carry the entry's value,
// and the component's NetworkPolicies, which select the owner's, do not select
// them.
func (o *ownedConfig) componentLabelValues() []string {
	values := []string{ComponentLabelValue(o.component)}
	if o.labelKey == appLabelKey && o.entry != "" && o.entry != o.component {
		values = append(values, ComponentLabelValue(o.entry))
	}
	return values
}

// checkEntryLabelValues refuses, under the component label key `app`, a document
// in which a lowering rule emitted an entry for one component under a name
// whose label value is another component's. The kinds label such an entry's
// objects with the entry's value (componentLabelValues), which would be the
// value the other component's NetworkPolicies select by. Lowering's own name
// checks do not see it where the other component was itself lowered into
// entries under other names. owners holds the authored component of every
// entry after lowering, by the entry's name; "" is the document as a whole.
//
// It compares label values, not names: a name over 63 characters is projected
// onto a shorter value (ComponentLabelValue), and an entry named as that
// projection carries the same label as the component.
func checkEntryLabelValues(owners map[string]string, labelKey string) error {
	if labelKey != appLabelKey {
		return nil
	}
	byValue := map[string]string{}
	for _, component := range owners {
		if component != "" {
			byValue[ComponentLabelValue(component)] = component
		}
	}
	for _, entry := range slices.Sorted(maps.Keys(owners)) {
		component := owners[entry]
		if component == "" || entry == component {
			continue
		}
		if other, taken := byValue[ComponentLabelValue(entry)]; taken && other != component {
			return &ComponentLabelError{
				Refused:   ComponentLabelOfAnotherComponent,
				Component: component,
				Key:       labelKey,
				Value:     ComponentLabelValue(entry),
				Want:      ComponentLabelValue(component),
				Entry:     entry,
				Other:     other,
			}
		}
	}
	return nil
}

// checkComponentLabel refuses g when it carries the component label's key with
// a value that is not the owning component's (componentLabelValues), in any
// place it holds metadata in (metadataHolders) but metadata that reaches no
// pods, whoever wrote it there: a passthrough or manifests object, a rendered
// chart, a kind's own property. An absent key is not refused: the wrapper
// writes the label where it writes one at all (stampComponentLabel). A null
// value is the empty string the cluster reads it as, and a value that is no
// string is refused as such.
//
// A workload whose own selector requires another value for the key is refused
// too (checkWorkloadSelector).
func (o *ownedConfig) checkComponentLabel(g generatedObject) error {
	accepted := o.componentLabelValues()
	for _, h := range metadataHolders(g.group, g.kind) {
		if h.noPods {
			continue
		}
		all, err := h.held(g.content)
		if err != nil {
			return errors.Errorf("component label: %s: %w", g.where, err)
		}
		for _, held := range all {
			labels, _, err := objectField(held.metadata, "labels")
			if err != nil {
				return errors.Errorf("component label: %s: %s: %w", g.where, held.path, err)
			}
			raw, carried := labels[o.labelKey]
			if !carried {
				continue
			}
			got, isString := raw.(string)
			if raw != nil && !isString {
				return errors.Errorf("component label: %s: %s[%q] is a %T, not a string", g.where, held.labels, o.labelKey, raw)
			}
			if !slices.Contains(accepted, got) {
				refusal := o.componentLabelRefusal(g, ComponentLabelForeignValue, held.labels)
				refusal.Value = got
				return refusal
			}
		}
	}
	return o.checkWorkloadSelector(g, accepted)
}

// checkWorkloadSelector refuses a workload whose own selector requires, for the
// component label's key, values of which none is accepted: its pods could not
// carry the component's value, and the cluster refuses a workload whose
// selector does not match its pod template.
//
// A selector that rules the key out, or the component's value, is not refused:
// its pod template stays as written (withComponentLabel).
func (o *ownedConfig) checkWorkloadSelector(g generatedObject, accepted []string) error {
	for _, k := range podTemplateKinds {
		// A PodTemplate has no spec around its template and no selector.
		if g.group != k.group || g.kind != k.kind || len(k.spec) == 0 {
			continue
		}
		spec, found, err := nestedObject(g.content, k.spec...)
		if err != nil {
			return errors.Errorf("component label: %s: %w", g.where, err)
		}
		if !found {
			continue
		}
		selector, isObject := spec["selector"].(map[string]any)
		if !isObject {
			continue
		}
		for _, required := range requiredLabelValues(selector, o.labelKey, k.labelMapSelector) {
			if slices.ContainsFunc(required, func(v string) bool { return slices.Contains(accepted, v) }) {
				continue
			}
			refusal := o.componentLabelRefusal(g, ComponentLabelSelectorRequiresAnother, strings.Join(k.spec, ".")+".selector")
			refusal.Required = slices.Clone(required)
			return refusal
		}
	}
	return nil
}

// componentLabelRefusal is the refusal of g for the owning component, with what
// every refusal of an object says. The caller adds the value, or the values a
// selector requires.
func (o *ownedConfig) componentLabelRefusal(g generatedObject, refused ComponentLabelRefusal, path string) *ComponentLabelError {
	return &ComponentLabelError{
		Refused:   refused,
		Component: o.component,
		Kind:      schema.GroupKind{Group: g.group, Kind: g.kind},
		Namespace: g.obj.GetNamespace(),
		Name:      g.obj.GetName(),
		Object:    g.where,
		Path:      path,
		Key:       o.labelKey,
		Want:      ComponentLabelValue(o.component),
	}
}

// ComponentLabelError is the refusal of a component label value that is not the
// component's (go-kure/launcher#790). Refused says which of four it is: a value
// an object holds, the values a workload's selector requires, the value a kind
// component's `labels` property holds, or the value the kinds would write for an
// entry a lowering rule emitted.
//
// Generation returns the first two and the transform the other two, each on its
// own or wrapped, so it is found with errors.As. It answers to
// ErrComponentLabelValue under errors.Is, which it unwraps to.
type ComponentLabelError struct {
	// Refused says what is refused.
	Refused ComponentLabelRefusal
	// Component is the component that owns the object, the property or the
	// entry. For ComponentLabelInLabelsProperty under the key `app`, the
	// property is held first to the `app` value the kinds write, that of the
	// name of the component after lowering, then to the owner's: Component and
	// Want are those of the one the value fails.
	Component string
	// Kind is the object's group and kind: the ones it states, else, for a typed
	// object of a kind the check reads more than the metadata of, its Go type's.
	// It is zero for any other typed object that states no kind, and where no
	// object is refused: for a property and for an entry.
	Kind schema.GroupKind
	// Namespace is the object's namespace as it was generated, empty when the
	// object states none. The text does not print it.
	Namespace string
	// Name is the object's name. On a member of a list envelope, Kind, Namespace
	// and Name are the member's. It is empty for a property and for an entry.
	Name string
	// Object is the object as the text names it: `Deployment "web"`, with the Go
	// type in place of the kind when Kind is zero. It is empty for a property
	// and for an entry.
	Object string
	// Path is where what is refused is held: an object's labels
	// ("spec.template.metadata.labels", and with the element's index where
	// they are in a list, "spec.acme.solvers[0].http01.ingress.podTemplate.metadata.labels")
	// or selector ("spec.selector"), or the property ("labels"). It is empty
	// for an entry.
	Path string
	// Key is the component label's key.
	Key string
	// Value is the value refused: the one the object or the property holds under
	// Key, or the one the kinds would write for the entry. It is empty for a
	// selector.
	Value string
	// Required is the values a selector requires Key to have one of, none of
	// which is the component's. It is nil otherwise.
	Required []string
	// Want is the component label's value for Component.
	Want string
	// Entry is the name of the component after lowering whose value is refused,
	// for an entry; empty otherwise.
	Entry string
	// Other is the component whose label value the entry's is, for an entry;
	// empty otherwise.
	Other string
}

// ComponentLabelRefusal says which refusal a ComponentLabelError is.
type ComponentLabelRefusal string

const (
	// ComponentLabelForeignValue is an object that holds the component label's
	// key with a value that is not its component's, in its own labels, a pod
	// template's, or the metadata an operator hands on to its pods.
	ComponentLabelForeignValue ComponentLabelRefusal = "foreign value"
	// ComponentLabelSelectorRequiresAnother is a workload whose own selector
	// requires, for the component label's key, values none of which is its
	// component's.
	ComponentLabelSelectorRequiresAnother ComponentLabelRefusal = "selector requires another value"
	// ComponentLabelInLabelsProperty is a kind component whose `labels`
	// property holds the component label's key with a value that is not its
	// component's. The transform refuses it (withObjectMetadata), before there
	// is an object to name.
	ComponentLabelInLabelsProperty ComponentLabelRefusal = "labels property holds a foreign value"
	// ComponentLabelOfAnotherComponent is an entry a lowering rule emitted for
	// one component whose `app` label value is another component's, under the
	// component label key `app`. The transform refuses it.
	ComponentLabelOfAnotherComponent ComponentLabelRefusal = "entry carries another component's value"
)

// Error returns the refusal's text, which the document's author reads: what
// holds the value, whose label it is not, and what to do.
func (e *ComponentLabelError) Error() string {
	switch e.Refused {
	case ComponentLabelSelectorRequiresAnother:
		return fmt.Sprintf("%s: %s requires %s for the label %q, not the component label of component %q (%q): launcher sets that label on the pod template, and the NetworkPolicies generated for the component select by it; take the label out of the selector, or require that value",
			e.Object, e.Path, quotedValues(e.Required), e.Key, e.Component, e.Want)
	case ComponentLabelOfAnotherComponent:
		return fmt.Sprintf("component %q: its lowering emitted %q, whose `app` label value %q is the component label of component %q: with the component label key %q the objects of %q would carry the label the NetworkPolicies generated for %q select by; rename one of the two components",
			e.Component, e.Entry, e.Value, e.Other, e.Key, e.Entry, e.Other)
	default:
		// A value an object or the property holds: ComponentLabelForeignValue
		// and ComponentLabelInLabelsProperty.
		text := fmt.Sprintf("%s[%q]: %q is not the component label of component %q (%q): launcher sets that label on everything the component generates, and the NetworkPolicies generated for the component select by it; remove the label, or write that value",
			e.Path, e.Key, e.Value, e.Component, e.Want)
		if e.Refused == ComponentLabelInLabelsProperty {
			// The property is the component's: there is no object to name.
			return text
		}
		return e.Object + ": " + text
	}
}

// Unwrap makes the error answer to ErrComponentLabelValue under errors.Is.
func (e *ComponentLabelError) Unwrap() error { return ErrComponentLabelValue }

// requiredLabelValues returns the values selector requires the label key to
// have one of, one set per requirement: a matchLabels entry and each In
// expression of a label selector, or the entry of a plain label map
// (labelMap, a ReplicationController's). A requirement that names no values the
// key must have (Exists, DoesNotExist, NotIn) is none, and a selector that does
// not decode requires nothing, as it holds no label back (withComponentLabel).
func requiredLabelValues(selector map[string]any, key string, labelMap bool) [][]string {
	if labelMap {
		switch v := selector[key].(type) {
		case string:
			return [][]string{{v}}
		case nil:
			if _, named := selector[key]; named {
				return [][]string{{""}}
			}
		}
		return nil
	}
	decoded := &metav1.LabelSelector{}
	if runtime.DefaultUnstructuredConverter.FromUnstructured(selector, decoded) != nil {
		return nil
	}
	var required [][]string
	if v, named := decoded.MatchLabels[key]; named {
		required = append(required, []string{v})
	}
	for _, e := range decoded.MatchExpressions {
		// An In that names no value is no selector the cluster accepts, and not
		// this check's to refuse.
		if e.Key == key && e.Operator == metav1.LabelSelectorOpIn && len(e.Values) > 0 {
			required = append(required, e.Values)
		}
	}
	return required
}

// quotedValues prints the values a selector requires one of.
func quotedValues(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return "one of " + strings.Join(quoted, ", ")
}
