package components

// Tests of what the readers of rendered and authored documents do with a
// field the Go type of a registered kind does not declare
// (undeclared_fields.go).

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/manifest"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// registeredWorkloadGVKs is every group, version and kind kure's scheme
// registers that isWorkloadGVK calls a workload or a claim, sorted. Lists of
// those kinds are left out: a list is not one object, and the parser refuses it.
func registeredWorkloadGVKs(t *testing.T) []schema.GroupVersionKind {
	t.Helper()
	if err := kubernetes.RegisterSchemes(); err != nil {
		t.Fatalf("RegisterSchemes: %v", err)
	}
	var out []schema.GroupVersionKind
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		if isWorkloadGVK(gvk) {
			out = append(out, gvk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// TestUndeclaredFields_EveryWorkloadKindIsRefused: a document of every
// workload and claim kind the build registers is refused when it sets a field
// its Go type does not declare, and decodes to that Go type when it does not.
// The kinds are read from the tables the policy check keys on, so a kind added
// there is covered here without an edit.
func TestUndeclaredFields_EveryWorkloadKindIsRefused(t *testing.T) {
	gvks := registeredWorkloadGVKs(t)
	if len(gvks) < 10 {
		t.Fatalf("found %d registered workload and claim kinds, want at least the 10 the check reads: %v", len(gvks), gvks)
	}
	for _, gvk := range gvks {
		t.Run(gvk.String(), func(t *testing.T) {
			head := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: thing\n  namespace: shop\n", gvk.GroupVersion(), gvk.Kind)

			objs, err := decodeManifestDocuments([]byte(head))
			if err != nil {
				t.Fatalf("control: the document without the field: %v", err)
			}
			if _, isUnstructured := objs[0].(*unstructured.Unstructured); len(objs) != 1 || isUnstructured {
				t.Fatalf("control: the document without the field decoded to %d objects, the first a %T; want one object of its Go type", len(objs), objs[0])
			}

			_, err = decodeManifestDocuments([]byte(head + "fieldOfALaterVersion: x\n"))
			assertErrorMentions(t, err, fmt.Sprintf("%s %q", gvk.Kind, "shop/thing"),
				"undeclared field fieldOfALaterVersion:", fmt.Sprintf("the %s %s type", gvk.GroupVersion(), gvk.Kind),
				"cannot be checked against environment policy")
		})
	}
}

// TestUndeclaredFields_RefuseSetIsWhatThePolicyReads: every registered kind
// whose pod spec, claims or volume source the policy check reads as a Go type
// is one whose documents are refused over an undeclared field. A kind added to
// renderedPodSpec or enforceRenderedClaims and not to workloadKinds fails
// here: the check would read its Go type and miss what that type drops.
func TestUndeclaredFields_RefuseSetIsWhatThePolicyReads(t *testing.T) {
	if err := kubernetes.RegisterSchemes(); err != nil {
		t.Fatalf("RegisterSchemes: %v", err)
	}
	read := 0
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		made, err := kubernetes.Scheme.New(gvk)
		if err != nil {
			t.Fatalf("Scheme.New(%s): %v", gvk, err)
		}
		obj, ok := made.(client.Object)
		if !ok {
			continue
		}
		// A ReplicationController's template is a pointer, nil in a new object.
		if rc, ok := obj.(*corev1.ReplicationController); ok {
			rc.Spec.Template = &corev1.PodTemplateSpec{}
		}
		_, podSpec := renderedPodSpec(obj)
		_, isClaim := obj.(*corev1.PersistentVolumeClaim)
		_, hasClaimTemplates := obj.(*appsv1.StatefulSet)
		_, isVolume := obj.(*corev1.PersistentVolume)
		if podSpec == nil && !isClaim && !hasClaimTemplates && !isVolume {
			continue
		}
		read++
		if !isWorkloadGVK(gvk) {
			t.Errorf("%s is read as its Go type by the policy check and is not in workloadGroups/workloadKinds, so a field its type does not declare would be dropped unchecked", gvk)
		}
	}
	if read < 10 {
		t.Errorf("the policy check read %d registered kinds as Go types, want at least 10: the probe above no longer sees them", read)
	}
}

// keptDocuments are documents of registered kinds that are neither workloads
// nor claims, each with one field its Go type does not declare, and the path
// of that field.
var keptDocuments = []struct {
	name string
	doc  string
	path []string
}{
	{
		name: "ConfigMap, top level",
		doc:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\nfieldOfALaterVersion: kept\ndata:\n  k: v\n",
		path: []string{"fieldOfALaterVersion"},
	},
	{
		name: "Service, in spec",
		doc:  "apiVersion: v1\nkind: Service\nmetadata:\n  name: thing\nspec:\n  fieldOfALaterVersion: kept\n  ports:\n    - port: 80\n",
		path: []string{"spec", "fieldOfALaterVersion"},
	},
	{
		name: "ServiceMonitor, a registered custom resource",
		doc:  "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: thing\nspec:\n  fieldOfALaterVersion: kept\n  selector: {}\n  endpoints:\n    - port: http\n",
		path: []string{"spec", "fieldOfALaterVersion"},
	},
	{
		name: "HorizontalPodAutoscaler",
		doc: "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n  fieldOfALaterVersion: kept\n" +
			"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: 9\n",
		path: []string{"spec", "fieldOfALaterVersion"},
	},
}

// TestDecodeManifestDocuments_OtherKindsKeepTheField: a document of a
// registered kind that is neither a workload nor a claim comes back
// unstructured, as written, the undeclared field kept; without that field it
// is its Go type.
func TestDecodeManifestDocuments_OtherKindsKeepTheField(t *testing.T) {
	for _, tc := range keptDocuments {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := decodeManifestDocuments([]byte(tc.doc))
			if err != nil {
				t.Fatalf("decodeManifestDocuments: %v", err)
			}
			if len(objs) != 1 {
				t.Fatalf("decoded %d objects, want 1", len(objs))
			}
			u, ok := objs[0].(*unstructured.Unstructured)
			if !ok {
				t.Fatalf("decoded a %T, want *unstructured.Unstructured: the document as written", objs[0])
			}
			if got, _, _ := unstructured.NestedString(u.Object, tc.path...); got != "kept" {
				t.Errorf("%s = %q, want %q", strings.Join(tc.path, "."), got, "kept")
			}
			if u.GetName() != "thing" {
				t.Errorf("name = %q, want %q", u.GetName(), "thing")
			}
			assertDeepCopyable(t, objs)
			if written := writtenYAML(t, u); !strings.Contains(written, "fieldOfALaterVersion: kept\n") {
				t.Errorf("written object lacks the field:\n%s", written)
			}

			clean := strings.Replace(tc.doc, "  fieldOfALaterVersion: kept\n", "", 1)
			clean = strings.Replace(clean, "fieldOfALaterVersion: kept\n", "", 1)
			objs, err = decodeManifestDocuments([]byte(clean))
			if err != nil {
				t.Fatalf("control: without the field: %v", err)
			}
			if _, isUnstructured := objs[0].(*unstructured.Unstructured); isUnstructured {
				t.Errorf("control: without the field the document is still unstructured, so the field is not what made it so")
			}
		})
	}
}

// maxReplicasPolicy is the policy that checks nothing, with a replica maximum.
type maxReplicasPolicy struct {
	oam.NoopPolicy
	max int32
}

func (p *maxReplicasPolicy) MaxReplicas() *int32 { return &p.max }

// TestDecodeManifestDocuments_KeptAutoscalerIsStillChecked: the policy check
// reads a HorizontalPodAutoscaler's replica maximum from its Go type, and from
// the unstructured object a document with an undeclared field becomes.
func TestDecodeManifestDocuments_KeptAutoscalerIsStillChecked(t *testing.T) {
	const doc = "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
		"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: 9\n"
	policy := &maxReplicasPolicy{max: 3}
	for name, tc := range map[string]struct {
		doc       string
		wantTyped bool
	}{
		"declared fields only": {doc, true},
		"an undeclared field":  {strings.Replace(doc, "spec:\n", "spec:\n  fieldOfALaterVersion: kept\n", 1), false},
	} {
		objs, err := decodeManifestDocuments([]byte(tc.doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, typed := objs[0].(*autoscalingv2.HorizontalPodAutoscaler); typed != tc.wantTyped {
			t.Fatalf("%s: decoded a %T, want typed = %v", name, objs[0], tc.wantTyped)
		}
		err = enforceRenderedObjectPolicy(objs[0], policy)
		assertErrorMentions(t, err, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3")
	}
}

// TestDecodeManifestDocuments_KeptCRDStillDefinesScope: a
// CustomResourceDefinition with an undeclared schema keyword comes back
// unstructured and is still read as the definition it is: IsCRD holds, and the
// scope it declares still reaches a custom resource beside it.
func TestDecodeManifestDocuments_KeptCRDStillDefinesScope(t *testing.T) {
	objs, err := decodeManifestDocuments([]byte(undeclaredSchemaKeyCRD("          futureSchemaKey: x\n", "")))
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	u, ok := objs[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("decoded a %T, want *unstructured.Unstructured", objs[0])
	}
	if got, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions"); len(got) != 1 ||
		got[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)["futureSchemaKey"] != "x" {
		t.Errorf("the schema keyword was not kept: %v", got)
	}
	if !manifest.IsCRD(u) {
		t.Error("IsCRD is false for the kept CustomResourceDefinition")
	}
	gk, scope, ok := manifest.CRDScope(u)
	if !ok || gk != (schema.GroupKind{Group: "example.com", Kind: "Widget"}) || scope != apiextv1.ClusterScoped {
		t.Errorf("CRDScope = %v, %v, %v; want example.com Widget, Cluster", gk, scope, ok)
	}
}

// undeclaredSchemaKeyCRD is a cluster-scoped Widget CustomResourceDefinition
// with besideProperties inserted in its schema next to `properties`, and
// insideItems inserted inside the `items` schema of an array property.
func undeclaredSchemaKeyCRD(besideProperties, insideItems string) string {
	return "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\n" +
		"spec:\n  group: example.com\n  names:\n    kind: Widget\n    plural: widgets\n  scope: Cluster\n" +
		"  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n" +
		besideProperties +
		"          properties:\n            a:\n              type: array\n              items:\n                type: string\n" +
		insideItems
}

// TestUndeclaredFields_KnownLimit_SelfUnmarshallingType pins the limit the
// README states: a type that unmarshals itself decodes its own keys, so the
// strict decode does not report an undeclared key inside it. A
// CustomResourceDefinition's `items` schema is one: a key next to `properties`
// is reported and kept, the same key inside `items` is not reported, and the
// typed decode drops it. A release of the API types that closes this fails
// here, and the README sentence goes with it.
func TestUndeclaredFields_KnownLimit_SelfUnmarshallingType(t *testing.T) {
	doc := undeclaredSchemaKeyCRD("", "                futureItemsKey: x\n")
	objs, err := decodeManifestDocuments([]byte(doc))
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	crd, ok := objs[0].(*apiextv1.CustomResourceDefinition)
	if !ok {
		t.Fatalf("decoded a %T: the key inside `items` is now reported, so the limit in the README no longer holds", objs[0])
	}
	if written := writtenYAML(t, crd); strings.Contains(written, "futureItemsKey") {
		t.Errorf("the typed object carries the key, so nothing is lost and the limit in the README no longer holds:\n%s", written)
	}
}

// TestDecodeManifestDocuments_UndeclaredItemsArrayIsRefused: a registered
// kind that declares no `items` and is written with an items array would be
// emitted as a list, whose items are applied in its place. It is refused. A
// top-level `items` that is not an array is an ordinary undeclared field and
// is kept.
func TestDecodeManifestDocuments_UndeclaredItemsArrayIsRefused(t *testing.T) {
	const head = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\n"
	_, err := decodeManifestDocuments([]byte(head +
		"items:\n  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: inner\n      namespace: elsewhere\n"))
	assertErrorMentions(t, err, `ConfigMap "thing"`, "declares no `items` field", "list")

	for name, items := range map[string]string{"an object": "items:\n  blue: 1\n", "null": "items: null\n"} {
		objs, err := decodeManifestDocuments([]byte(head + items))
		if err != nil {
			t.Fatalf("items is %s: %v", name, err)
		}
		if len(objs) != 1 || objs[0].GetName() != "thing" {
			t.Errorf("items is %s: decoded %v, want the one ConfigMap", name, resourceNames(objs))
		}
		if u, ok := objs[0].(*unstructured.Unstructured); ok && u.IsList() {
			t.Errorf("items is %s: the kept object reads as a list", name)
		}
	}
}

// TestDecodeManifestDocuments_ParseErrorsAreTheParsersOwn: input the parser
// refuses gives the parser's own error, every bad document in it, whether or
// not a workload with an undeclared field stands before them.
func TestDecodeManifestDocuments_ParseErrorsAreTheParsersOwn(t *testing.T) {
	const (
		undeclared = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  fieldOfALaterVersion: 1\n"
		wrongType  = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\ndata: not-a-mapping\n"
		noKind     = "metadata:\n  name: b\n"
		good       = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n"
	)
	for name, raw := range map[string]string{
		"two bad documents":                        wrongType + "---\n" + good + "---\n" + noKind,
		"an undeclared field before bad documents": undeclared + "---\n" + wrongType + "---\n" + noKind,
		"invalid YAML":                             "key: [unclosed",
		"a scalar document":                        good + "---\njust a string\n",
		"a v1 List":                                "apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: inner\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, want := kureio.ParseYAMLWithOptions([]byte(raw), kureio.ParseOptions{AllowUnstructured: true})
			if want == nil {
				t.Fatal("control: the parser accepts the input")
			}
			_, got := decodeManifestDocuments([]byte(raw))
			if got == nil || got.Error() != want.Error() {
				t.Errorf("error = %v\nwant the parser's own: %v", got, want)
			}
		})
	}
}

// TestDecodeManifestDocuments_AsTheParserDecodes: input with no undeclared
// field decodes to what the parser alone makes of it: the same objects, in the
// same order, of the same types, empty documents skipped and an unregistered
// list replaced by its items.
func TestDecodeManifestDocuments_AsTheParserDecodes(t *testing.T) {
	raw := []byte("# a comment alone\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 2\n" +
		"---\nnull\n---\napiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: thing\nspec:\n  big: 9223372036854775808\n" +
		"---\napiVersion: example.com/v1\nkind: ThingList\nitems:\n- apiVersion: example.com/v1\n  kind: Thing\n  metadata:\n    name: item\n" +
		"---\n{\"apiVersion\": \"v1\", \"kind\": \"ConfigMap\", \"metadata\": {\"name\": \"json\"}}\n")
	want, err := kureio.ParseYAMLWithOptions(raw, kureio.ParseOptions{AllowUnstructured: true})
	if err != nil {
		t.Fatalf("control: the parser: %v", err)
	}
	got, err := decodeManifestDocuments(raw)
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	if names := resourceNames(got); !slices.Equal(names, []string{"web", "thing", "item", "json"}) {
		t.Fatalf("decoded %v, want [web thing item json]", names)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded objects differ from the parser's:\n got %#v\nwant %#v", got, want)
	}
}

// TestDecodeManifestDocuments_DuplicateKeyIsNotAnUndeclaredField: a key
// written twice is not a field the type does not declare. In YAML the last
// value stands, as it did; in a JSON document the strict decode reports the
// repeat with its own message, which is not a refusal here.
func TestDecodeManifestDocuments_DuplicateKeyIsNotAnUndeclaredField(t *testing.T) {
	for name, raw := range map[string]string{
		"YAML": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  labels:\n    a: \"1\"\n    a: \"2\"\n",
		"JSON": `{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": {"name": "web", "labels": {"a": "1", "a": "2"}}}`,
	} {
		objs, err := decodeManifestDocuments([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		dep, ok := objs[0].(*appsv1.Deployment)
		if !ok {
			t.Fatalf("%s: decoded a %T, want *appsv1.Deployment", name, objs[0])
		}
		if got := dep.Labels["a"]; got != "2" {
			t.Errorf("%s: label a = %q, want the last value, %q", name, got, "2")
		}
	}
}

// documentWithRepeatedKeys is a JSON document of the given kind that writes n
// keys of metadata.labels twice each, with rest spliced in after metadata as
// further top-level members.
func documentWithRepeatedKeys(apiVersion, kind string, n int, rest string) string {
	labels := make([]string, 0, n)
	for i := range n {
		labels = append(labels, fmt.Sprintf(`"k%d": "a", "k%d": "b"`, i, i))
	}
	return fmt.Sprintf(`{"apiVersion": %q, "kind": %q, "metadata": {"name": "web", "labels": {%s}}%s}`,
		apiVersion, kind, strings.Join(labels, ", "), rest)
}

// TestUndeclaredFields_StrictErrorLimit holds strictErrorLimit to the
// decoder's own: a document with more repeated keys than that comes back with
// exactly that many strict errors. A decoder that records more, or all of
// them, fails here, and the limit and its refusal go or move with it.
func TestUndeclaredFields_StrictErrorLimit(t *testing.T) {
	decoder, err := strictDecoder()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = decoder.Decode([]byte(documentWithRepeatedKeys("v1", "Pod", strictErrorLimit+50, "")), nil, nil)
	strict, ok := runtime.AsStrictDecodingError(err)
	if !ok {
		t.Fatalf("decoding gave %v, want a strict decoding error", err)
	}
	if got := len(strict.Errors()); got != strictErrorLimit {
		t.Errorf("the decoder recorded %d strict errors for %d repeated keys, want strictErrorLimit (%d)", got, strictErrorLimit+50, strictErrorLimit)
	}
}

// TestDecodeManifestDocuments_RepeatedKeysCannotHideAnUndeclaredField: the
// decoder records a limited number of strict errors, and a repeated key is
// one. Below the limit an undeclared field after the repeats is still named
// and the workload refused; at the limit the record is full without naming a
// field, so the document is refused, of any kind and whether or not it sets
// one, since that cannot be told. It was decoded with the field dropped.
func TestDecodeManifestDocuments_RepeatedKeysCannotHideAnUndeclaredField(t *testing.T) {
	const undeclared = `, "spec": {"futureField": true}`

	t.Run("below the limit the field is named", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(documentWithRepeatedKeys("v1", "Pod", strictErrorLimit-1, undeclared)))
		if err == nil {
			t.Fatal("a Pod with an undeclared field decoded")
		}
		for _, want := range []string{`Pod "web"`, "undeclared field spec.futureField"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	})

	t.Run("YAML resolves its repeated keys before the decode", func(t *testing.T) {
		var doc strings.Builder
		doc.WriteString("apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\n  labels:\n")
		for i := range 2 * strictErrorLimit {
			fmt.Fprintf(&doc, "    k%d: a\n    k%d: b\n", i, i)
		}
		objs, err := decodeManifestDocuments([]byte(doc.String()))
		if err != nil {
			t.Fatalf("a YAML Pod with repeated keys and no undeclared field: %v", err)
		}
		if got := objs[0].GetLabels()["k0"]; got != "b" {
			t.Errorf("label k0 = %q, want the last value, %q", got, "b")
		}
		doc.WriteString("spec:\n  futureField: true\n")
		_, err = decodeManifestDocuments([]byte(doc.String()))
		if err == nil || !strings.Contains(err.Error(), "undeclared field spec.futureField") {
			t.Errorf("a YAML Pod with repeated keys and an undeclared field gave %v, want the field named", err)
		}
	})

	t.Run("below the limit a document without one decodes", func(t *testing.T) {
		objs, err := decodeManifestDocuments([]byte(documentWithRepeatedKeys("v1", "Pod", strictErrorLimit-1, "")))
		if err != nil {
			t.Fatal(err)
		}
		pod, ok := objs[0].(*corev1.Pod)
		if !ok {
			t.Fatalf("decoded a %T, want *corev1.Pod", objs[0])
		}
		if got := pod.Labels["k0"]; got != "b" {
			t.Errorf("label k0 = %q, want the last value, %q", got, "b")
		}
	})

	for name, doc := range map[string]string{
		"a workload that sets a field":    documentWithRepeatedKeys("v1", "Pod", strictErrorLimit, undeclared),
		"a workload that sets none":       documentWithRepeatedKeys("v1", "Pod", strictErrorLimit, ""),
		"another kind that sets a field":  documentWithRepeatedKeys("v1", "ConfigMap", strictErrorLimit, `, "futureField": true`),
		"another kind that sets none":     documentWithRepeatedKeys("v1", "ConfigMap", strictErrorLimit, ""),
		"a workload far beyond the limit": documentWithRepeatedKeys("apps/v1", "Deployment", 3*strictErrorLimit, undeclared),
		"a workload, the field written first": `{"apiVersion": "v1", "kind": "Pod", "spec": {"futureField": true}, ` +
			strings.TrimPrefix(documentWithRepeatedKeys("v1", "Pod", strictErrorLimit, ""), `{"apiVersion": "v1", "kind": "Pod", `),
	} {
		t.Run("at the limit: "+name, func(t *testing.T) {
			_, err := decodeManifestDocuments([]byte(doc))
			if err == nil {
				t.Fatal("the document decoded, and an undeclared field in it would have been dropped unseen")
			}
			if !strings.Contains(err.Error(), `"web"`) {
				t.Errorf("error %q does not name the object", err)
			}
			if name == "a workload, the field written first" {
				// The field is recorded before the repeats fill the record.
				if !strings.Contains(err.Error(), "undeclared field spec.futureField") {
					t.Errorf("error %q does not name the field", err)
				}
				return
			}
			if !strings.Contains(err.Error(), "keys twice") {
				t.Errorf("error %q does not say why the document cannot be read for undeclared fields", err)
			}
		})
	}
}

// TestDecodeManifestDocuments_FieldRecordedBeforeTheRepeatsIsKept: the
// full-record refusal is for a record that names no undeclared field. A
// document of a kept kind whose undeclared field is recorded before the
// repeats fill the record is kept, as any other with such a field.
func TestDecodeManifestDocuments_FieldRecordedBeforeTheRepeatsIsKept(t *testing.T) {
	doc := `{"apiVersion": "v1", "kind": "ConfigMap", "futureField": true, ` +
		strings.TrimPrefix(documentWithRepeatedKeys("v1", "ConfigMap", strictErrorLimit, ""), `{"apiVersion": "v1", "kind": "ConfigMap", `)
	objs, err := decodeManifestDocuments([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := objs[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("decoded a %T, want the document as written", objs[0])
	}
	if got, found, _ := unstructured.NestedBool(u.Object, "futureField"); !found || !got {
		t.Errorf("futureField = %v (found %v), want it kept", got, found)
	}
}

// TestPassthrough_RepeatedKeysCannotHideAnUndeclaredField: an authored map
// holds no key twice, but a value in it that serializes itself can. A Go
// caller's json.RawMessage with enough repeated keys fills the strict decode's
// record as a JSON document's do, and the workload is refused, with a policy
// and with none, where the undeclared field after it would have been emitted
// unchecked. Below the limit the field is named.
func TestPassthrough_RepeatedKeysCannotHideAnUndeclaredField(t *testing.T) {
	config := func(repeats int) *PassthroughConfig {
		labels := make([]string, 0, repeats)
		for i := range repeats {
			labels = append(labels, fmt.Sprintf(`"k%d": "a", "k%d": "b"`, i, i))
		}
		return &PassthroughConfig{
			componentName: "web",
			Namespace:     "demo",
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]any{
					"name":   "web",
					"labels": json.RawMessage("{" + strings.Join(labels, ", ") + "}"),
				},
				"spec": map[string]any{"futureField": true},
			},
		}
	}
	// The refusal is for what the policy check reads as a Go type. An object
	// of any other kind is emitted as authored, whatever the strict decode
	// says of it: nothing it sets is dropped.
	t.Run("another kind is emitted as authored", func(t *testing.T) {
		cfg := config(strictErrorLimit)
		cfg.Object["kind"] = "ConfigMap"
		delete(cfg.Object, "spec")
		// A map is serialized with its keys sorted, so the field has to sort
		// after metadata for the repeats to fill the record before it.
		cfg.Object["zzFutureField"] = true
		serialized, err := json.Marshal(cfg.Object)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := undeclaredFields(serialized); !errors.Is(err, errStrictRecordFull) {
			t.Fatalf("the fixture gave %v, want a full record: it does not test the case", err)
		}
		if _, err := decodeManifestDocuments(serialized); !errors.Is(err, errStrictRecordFull) {
			t.Fatalf("the same document through the manifests decode gave %v, want it refused", err)
		}
		if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		objs, err := cfg.Generate(nil)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		u, ok := (*objs[0]).(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("generated a %T, want the authored map", *objs[0])
		}
		if got, found, _ := unstructured.NestedBool(u.Object, "zzFutureField"); !found || !got {
			t.Errorf("zzFutureField = %v (found %v), want it emitted", got, found)
		}
	})

	for name, tc := range map[string]struct {
		repeats int
		want    string
	}{
		"below the limit": {strictErrorLimit - 1, "undeclared field spec.futureField"},
		"at the limit":    {strictErrorLimit, "keys twice"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config(tc.repeats)
			if _, err := cfg.Generate(nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Generate with no policy gave %v, want an error containing %q", err, tc.want)
			}
			if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ApplyPolicy gave %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestUndeclaredFields_Paths: undeclaredFields names every undeclared field
// by its path, list indices included, and nothing else: none for a document
// that loses nothing or whose kind is not registered, and an error for one
// that does not decode. It pins the message form the paths are read from.
func TestUndeclaredFields_Paths(t *testing.T) {
	asJSON := func(doc string) []byte {
		t.Helper()
		docs, err := splitManifestDocuments([]byte(doc))
		if err != nil || len(docs) != 1 {
			t.Fatalf("splitting the test document: %d documents, %v", len(docs), err)
		}
		return docs[0]
	}
	const deployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  futureMeta: x\nspec:\n  futureTop: 1\n" +
		"  template:\n    spec:\n      futurePod: true\n      volumes:\n        - name: v\n          csi:\n            driver: d\n            futureCSI: x\n" +
		"      containers:\n        - name: c\n          image: registry.example/c:1\n          futureCtr: x\n" +
		"          ports:\n            - containerPort: 80\n              futurePort: x\n"

	got, err := undeclaredFields(asJSON(deployment))
	if err != nil {
		t.Fatalf("undeclaredFields: %v", err)
	}
	want := []string{
		"metadata.futureMeta",
		"spec.futureTop",
		"spec.template.spec.containers[0].futureCtr",
		"spec.template.spec.containers[0].ports[0].futurePort",
		"spec.template.spec.futurePod",
		"spec.template.spec.volumes[0].csi.futureCSI",
	}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %q\n  want %q", got, want)
	}

	// The paths are cut out of messages of this form; a change of form would
	// make every document read as lossless.
	decoder, err := strictDecoder()
	if err != nil {
		t.Fatalf("strictDecoder: %v", err)
	}
	_, _, err = decoder.Decode(asJSON("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\nstray: x\n"), nil, nil)
	if err == nil || err.Error() != `strict decoding error: unknown field "stray"` {
		t.Errorf("strict decode error = %v, want exactly: strict decoding error: unknown field \"stray\"", err)
	}

	for name, doc := range map[string]string{
		"declared fields only": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 0\n  template:\n    spec:\n      hostNetwork: false\n      nodeName: \"\"\n",
		"unregistered kind":    "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nanything: x\n",
	} {
		if got, err := undeclaredFields(asJSON(doc)); err != nil || len(got) != 0 {
			t.Errorf("%s: undeclaredFields = %q, %v; want none", name, got, err)
		}
	}
	if _, err := undeclaredFields(asJSON("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: three\n")); err == nil {
		t.Error("a document that does not decode gave no error")
	}
}

// selfUnmarshallingLeaves are the types under a workload or a claim that
// decode themselves, each with why no undeclared key can hide in it.
var selfUnmarshallingLeaves = map[string]string{
	"k8s.io/apimachinery/pkg/api/resource.Quantity":   "a scalar: a string or a number",
	"k8s.io/apimachinery/pkg/util/intstr.IntOrString": "a scalar: a string or a number",
	"k8s.io/apimachinery/pkg/apis/meta/v1.Time":       "a scalar: a string",
	"k8s.io/apimachinery/pkg/apis/meta/v1.FieldsV1":   "metadata.managedFields' field set, kept as the raw JSON it is: it declares no fields and drops none",
}

// TestUndeclaredFields_NoSelfUnmarshallingStructUnderAWorkload holds the
// premise of the refusal: under the Go type of a workload or a claim, nothing
// decodes itself but the leaves listed above, so the strict decode sees every
// key of a pod spec. A type that starts to unmarshal itself would hide its
// undeclared keys from the strict decode, as a CustomResourceDefinition's
// `items` schema does, and the policy check would again read an object that
// lost a field. Such a type fails here until it is shown to be a leaf.
func TestUndeclaredFields_NoSelfUnmarshallingStructUnderAWorkload(t *testing.T) {
	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	found := map[string][]string{}
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, root string)
	walk = func(typ reflect.Type, root string) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		if typ.Name() != "" && reflect.PointerTo(typ).Implements(unmarshaler) {
			name := typ.PkgPath() + "." + typ.Name()
			found[name] = append(found[name], root)
			return
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(typ.Elem(), root)
		case reflect.Map:
			walk(typ.Key(), root)
			walk(typ.Elem(), root)
		case reflect.Struct:
			for i := range typ.NumField() {
				walk(typ.Field(i).Type, root)
			}
		case reflect.Interface:
			t.Errorf("%s holds an interface value (%s), which the strict decode cannot check for undeclared keys", root, typ)
		default:
			// A scalar: it holds no keys.
		}
	}

	types := kubernetes.Scheme.AllKnownTypes()
	for _, gvk := range registeredWorkloadGVKs(t) {
		walk(types[gvk], gvk.String())
	}
	if len(seen) < 100 {
		t.Fatalf("walked %d types, want the pod spec's type tree: the walk no longer reaches it", len(seen))
	}
	if !seen[reflect.TypeFor[corev1.PodSpec]()] || !seen[reflect.TypeFor[corev1.Container]()] {
		t.Fatal("the walk did not reach corev1.PodSpec and corev1.Container")
	}
	for name, roots := range found {
		if _, leaf := selfUnmarshallingLeaves[name]; !leaf {
			t.Errorf("%s unmarshals itself (first reached from %s): an undeclared key inside it is not reported, so a workload that sets one is not refused. Show it holds no keys and list it, or refuse the kind another way", name, roots[0])
		}
	}
	for name := range selfUnmarshallingLeaves {
		if _, reached := found[name]; !reached {
			t.Errorf("%s is listed as a leaf and is no longer under any workload or claim type: drop it from the list", name)
		}
	}
}
