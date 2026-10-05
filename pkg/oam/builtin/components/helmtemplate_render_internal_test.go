package components

// Tests of the client-side Helm render and hook-group partition
// (helmtemplate_render.go), driven through the helmtemplate terminal.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

func TestDecodeChartManifests_ErrorOnMalformedYAML(t *testing.T) {
	_, err := decodeChartManifests([]byte("key: [unclosed"))
	assertErrorMentions(t, err, "decoding rendered manifests")
}

func TestDecodeChartManifests_ErrorOnMappingWithoutAPIVersion(t *testing.T) {
	_, err := decodeChartManifests([]byte("kind: ConfigMap\nmetadata:\n  name: cm"))
	assertErrorMentions(t, err, "decoding rendered manifests")
}

// TestDecodeChartManifests_SkipsEmptyDoc: a document that holds nothing (null,
// ~, a comment alone, nothing at all) carries no object and is skipped; the
// document after it still decodes.
func TestDecodeChartManifests_SkipsEmptyDoc(t *testing.T) {
	for _, skipped := range []string{
		"null\n",
		"~\n",
		"# a comment alone\n",
		"",
	} {
		t.Run(fmt.Sprintf("%q", skipped), func(t *testing.T) {
			objects, err := decodeChartManifests([]byte(skipped + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm"))
			if err != nil {
				t.Fatalf("decodeChartManifests: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, []string{"cm"}) {
				t.Fatalf("decoded %v, want [cm] (the first document skipped)", names)
			}
		})
	}
}

// TestDecodeChartManifests_NonObjectDocIsAnError: a document that holds
// something other than Kubernetes objects — a scalar, a sequence, an empty
// mapping, a list with an item that does not decode — is an error, not skipped
// and not emitted.
func TestDecodeChartManifests_NonObjectDocIsAnError(t *testing.T) {
	for _, doc := range []string{
		"just a string\n",
		"42\n",
		"- a\n- b\n",
		"{}\n",
		"apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: inner\n- null\n",
		"apiVersion: apps/v1\nkind: DeploymentList\nitems:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: inner\n",
	} {
		t.Run(fmt.Sprintf("%q", doc), func(t *testing.T) {
			_, err := decodeChartManifests([]byte(doc + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm"))
			assertErrorMentions(t, err, "decoding rendered manifests")
		})
	}
}

// TestDecodeChartManifests_TypedOrUnstructured: an object of a kind kure's
// scheme registers decodes to its Go type, any other to unstructured, and the
// items of a list of an unregistered kind replace the list.
func TestDecodeChartManifests_TypedOrUnstructured(t *testing.T) {
	objects, err := decodeChartManifests([]byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 2
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
---
apiVersion: example.com/v1
kind: Thing
metadata:
  name: thing
---
apiVersion: example.com/v1
kind: ThingList
items:
- apiVersion: example.com/v1
  kind: Thing
  metadata:
    name: item
`))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	if names := resourceNames(objects); !slices.Equal(names, []string{"web", "migrate", "thing", "item"}) {
		t.Fatalf("decoded %v, want [web migrate thing item]", names)
	}
	dep, ok := objects[0].(*appsv1.Deployment)
	if !ok {
		t.Fatalf("web is %T, want *appsv1.Deployment", objects[0])
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Errorf("Deployment replicas = %v, want 2", dep.Spec.Replicas)
	}
	if _, ok := objects[1].(*batchv1.Job); !ok {
		t.Errorf("migrate is %T, want *batchv1.Job", objects[1])
	}
	for _, o := range objects[2:] {
		if _, ok := o.(*unstructured.Unstructured); !ok {
			t.Errorf("%s is %T, want *unstructured.Unstructured", o.GetName(), o)
		}
	}
	assertDeepCopyable(t, objects)
}

// TestDecodeChartManifests_FlattensRegisteredLists: a `v1` List and a typed
// list are replaced by their items, in the list's order. An item of a `v1`
// List is decoded as a document of its own: typed when its kind is registered,
// unstructured when not, and flattened in turn when it is itself a list. An
// item of a typed list that leaves apiVersion and kind out is the kind the
// list holds.
func TestDecodeChartManifests_FlattensRegisteredLists(t *testing.T) {
	objects, err := decodeChartManifests([]byte(`apiVersion: v1
kind: List
items:
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: settings
- apiVersion: example.com/v1
  kind: Thing
  metadata:
    name: thing
- apiVersion: v1
  kind: List
  items:
  - apiVersion: batch/v1
    kind: Job
    metadata:
      name: migrate
---
apiVersion: apps/v1
kind: DeploymentList
items:
- metadata:
    name: web
- apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: api
`))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	if names := resourceNames(objects); !slices.Equal(names, []string{"settings", "thing", "migrate", "web", "api"}) {
		t.Fatalf("decoded %v, want [settings thing migrate web api]", names)
	}
	if _, ok := objects[0].(*corev1.ConfigMap); !ok {
		t.Errorf("settings is %T, want *corev1.ConfigMap", objects[0])
	}
	if _, ok := objects[1].(*unstructured.Unstructured); !ok {
		t.Errorf("thing is %T, want *unstructured.Unstructured", objects[1])
	}
	if _, ok := objects[2].(*batchv1.Job); !ok {
		t.Errorf("migrate is %T, want *batchv1.Job", objects[2])
	}
	for _, o := range objects[3:] {
		if _, ok := o.(*appsv1.Deployment); !ok {
			t.Errorf("%s is %T, want *appsv1.Deployment", o.GetName(), o)
		}
		if kind := o.GetObjectKind().GroupVersionKind().Kind; kind != "Deployment" {
			t.Errorf("%s has kind %q, want Deployment", o.GetName(), kind)
		}
	}
	assertDeepCopyable(t, objects)
}

// TestDecodeChartManifests_HookInAListIsAnError: a list document where a
// helm.sh/hook annotation is involved is refused, naming the list and the item:
// the annotation on the list's own metadata (Helm's hook, which the items that
// replace the list would not carry), or on an item (which Helm does not read).
// That holds for a `v1` List, a typed list, a list nested in a `v1` List and a
// list of a kind the scheme does not register, wherever the document sits in
// the stream. The same document with another annotation in the hook's place
// builds.
func TestDecodeChartManifests_HookInAListIsAnError(t *testing.T) {
	const leading = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: first\n---\n"
	cases := []struct {
		name  string
		doc   string
		wants []string
		// objects is what the control, without the hook, decodes to.
		objects []string
	}{
		{
			name: "v1 List, hook on the list",
			doc: `apiVersion: v1
kind: List
metadata:
  name: cleanup-hook
  annotations:
    ANNOTATION
items:
- apiVersion: v1
  kind: Pod
  metadata:
    name: cleanup
`,
			wants:   []string{`list List "cleanup-hook" carries a helm.sh/hook annotation on its own metadata`},
			objects: []string{"cleanup"},
		},
		{
			name: "typed list, hook on the list",
			doc: `apiVersion: v1
kind: PodList
metadata:
  annotations:
    ANNOTATION
items:
- metadata:
    name: cleanup
`,
			wants:   []string{`list PodList "" carries a helm.sh/hook annotation on its own metadata`},
			objects: []string{"cleanup"},
		},
		{
			name: "v1 List, hook on an item",
			doc: `apiVersion: v1
kind: List
items:
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: settings
- apiVersion: v1
  kind: Pod
  metadata:
    name: cleanup
    annotations:
      ANNOTATION
`,
			wants:   []string{`item 1 (Pod "cleanup") of list List "" carries a helm.sh/hook annotation`},
			objects: []string{"settings", "cleanup"},
		},
		{
			name: "typed list, hook on an item",
			doc: `apiVersion: apps/v1
kind: DeploymentList
items:
- metadata:
    name: web
    annotations:
      ANNOTATION
`,
			wants:   []string{`item 0 ("web") of list DeploymentList "" carries a helm.sh/hook annotation`},
			objects: []string{"web"},
		},
		{
			name: "nested list, hook on the inner list",
			doc: `apiVersion: v1
kind: List
metadata:
  name: outer
items:
- apiVersion: v1
  kind: List
  metadata:
    name: inner
    annotations:
      ANNOTATION
  items:
  - apiVersion: batch/v1
    kind: Job
    metadata:
      name: migrate
`,
			wants:   []string{`item 0 (List "inner") of list List "outer" carries a helm.sh/hook annotation`},
			objects: []string{"migrate"},
		},
		{
			name: "nested list, hook on an item of the inner list",
			doc: `apiVersion: v1
kind: List
metadata:
  name: outer
items:
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: settings
- apiVersion: v1
  kind: List
  metadata:
    name: inner
  items:
  - apiVersion: batch/v1
    kind: Job
    metadata:
      name: migrate
      annotations:
        ANNOTATION
`,
			wants: []string{
				`item 1 of list List "outer"`,
				`item 0 (Job "migrate") of list List "inner" carries a helm.sh/hook annotation`,
			},
			objects: []string{"settings", "migrate"},
		},
		{
			name: "list of an unregistered kind, hook on the list",
			doc: `apiVersion: example.com/v1
kind: ThingList
metadata:
  annotations:
    ANNOTATION
items:
- apiVersion: example.com/v1
  kind: Thing
  metadata:
    name: thing
`,
			wants:   []string{`list ThingList "" carries a helm.sh/hook annotation on its own metadata`},
			objects: []string{"thing"},
		},
		{
			name: "list of an unregistered kind, hook on an item",
			doc: `apiVersion: example.com/v1
kind: ThingList
items:
- apiVersion: example.com/v1
  kind: Thing
  metadata:
    name: thing
    annotations:
      ANNOTATION
`,
			wants:   []string{`item 0 (Thing "thing") of list ThingList "" carries a helm.sh/hook annotation`},
			objects: []string{"thing"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.doc, "ANNOTATION") {
				t.Fatal("test premise: the document has no ANNOTATION placeholder")
			}
			hooked := strings.Replace(tc.doc, "ANNOTATION", "helm.sh/hook: pre-delete", 1)
			for _, stream := range []string{hooked, leading + hooked} {
				_, err := decodeChartManifests([]byte(stream))
				assertErrorMentions(t, err, append([]string{"decoding rendered manifests"}, tc.wants...)...)
			}
			if _, err := parseChartManifests([]byte(hooked)); err == nil {
				t.Error("parseChartManifests: got no error, want the refusal")
			}

			control := strings.Replace(tc.doc, "ANNOTATION", "example.com/note: kept", 1)
			objects, err := decodeChartManifests([]byte(control))
			if err != nil {
				t.Fatalf("control without the hook: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, tc.objects) {
				t.Errorf("control without the hook decoded %v, want %v", names, tc.objects)
			}
		})
	}
}

// TestDecodeChartManifests_HookInAListIsReadAsWritten: the check reads a list
// document as the parser does, in a render that is a JSON stream too, where a
// document can state what YAML cannot. A number no Go type holds does not make
// the check skip the document, and a key stated twice is read in every
// statement: the typed decode merges a repeated metadata, so an annotation in
// the first one is on the decoded object. Each document builds without the
// hook, which is what makes the parser's reading the one to match.
func TestDecodeChartManifests_HookInAListIsReadAsWritten(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "a number beyond float64 beside the list's hook",
			doc: `{"apiVersion": "v1", "kind": "List",
 "metadata": {"annotations": {ANNOTATION}},
 "extra": 1e1000,
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm"}}]}`,
			want: `list List "" carries a helm.sh/hook annotation on its own metadata`,
		},
		{
			name: "an item's hook in the first of two metadata",
			doc: `{"apiVersion": "v1", "kind": "List",
 "items": [{"apiVersion": "v1", "kind": "ConfigMap",
   "metadata": {"annotations": {ANNOTATION}},
   "metadata": {"name": "cm"}}]}`,
			want: `item 0 (ConfigMap "cm") of list List "" carries a helm.sh/hook annotation`,
		},
		{
			name: "an item's hook in the first of two annotations",
			doc: `{"apiVersion": "v1", "kind": "List",
 "items": [{"apiVersion": "v1", "kind": "ConfigMap",
   "metadata": {"name": "cm", "annotations": {ANNOTATION}, "annotations": {"example.com/other": "x"}}}]}`,
			want: `item 0 (ConfigMap "cm") of list List "" carries a helm.sh/hook annotation`,
		},
		{
			name: "the list's hook in the first of two metadata",
			doc: `{"apiVersion": "v1", "kind": "List",
 "metadata": {"annotations": {ANNOTATION}},
 "metadata": {"name": "later"},
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm"}}]}`,
			want: `list List "later" carries a helm.sh/hook annotation on its own metadata`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hooked := strings.Replace(tc.doc, "ANNOTATION", `"helm.sh/hook": "pre-delete"`, 1)
			_, err := decodeChartManifests([]byte(hooked))
			assertErrorMentions(t, err, "decoding rendered manifests", tc.want)

			control := strings.Replace(tc.doc, "ANNOTATION", `"example.com/note": "kept"`, 1)
			objects, err := decodeChartManifests([]byte(control))
			if err != nil {
				t.Fatalf("control without the hook: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, []string{"cm"}) {
				t.Errorf("control without the hook decoded %v, want [cm]", names)
			}
		})
	}
}

// TestDecodeChartManifests_HookInARepeatedItems: of an items key stated twice
// both of the parser's readers keep the last statement, so the check reads
// that one. An item with a hook in the earlier statement is in no object the
// parser returns: the document builds, to the last statement's items alone. A
// hook in the last statement is refused.
func TestDecodeChartManifests_HookInARepeatedItems(t *testing.T) {
	const (
		hooked = `{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "hooked", "annotations": {"helm.sh/hook": "pre-delete"}}}`
		plain  = `{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "plain"}}`
	)
	heads := map[string]string{
		"v1 List":                      `"apiVersion": "v1", "kind": "List"`,
		"typed list":                   `"apiVersion": "v1", "kind": "ConfigMapList"`,
		"list of an unregistered kind": `"apiVersion": "example.com/v1", "kind": "ThingList"`,
	}
	for name, head := range heads {
		t.Run(name, func(t *testing.T) {
			discarded := `{` + head + `, "items": [` + hooked + `], "items": [` + plain + `]}`
			objects, err := decodeChartManifests([]byte(discarded))
			if err != nil {
				t.Fatalf("a hook in the discarded statement: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, []string{"plain"}) {
				t.Errorf("decoded %v, want [plain]: the last statement's items alone", names)
			}

			for _, last := range []string{`[]`, `null`} {
				emptied := `{` + head + `, "items": [` + hooked + `], "items": ` + last + `}`
				objects, err = decodeChartManifests([]byte(emptied))
				if err != nil || len(objects) != 0 {
					t.Errorf("a hook in a statement that %s replaces: %v, %v; want no object and no error", last, resourceNames(objects), err)
				}
			}

			kept := `{` + head + `, "items": [` + plain + `], "items": [` + hooked + `]}`
			_, err = decodeChartManifests([]byte(kept))
			assertErrorMentions(t, err, "decoding rendered manifests", `item 0 (ConfigMap "hooked") of list`, "carries a helm.sh/hook annotation")
		})
	}
}

// TestDecodeChartManifests_HookInAListUnderAKeyOfAnotherCase: the parser's list
// detection reads apiVersion and kind under exactly those keys, the decoder it
// falls back to reads them whatever their case and takes the last, and Helm
// reads metadata whatever its case. A document that is a single object to the
// first and a list of an unregistered kind to the second is replaced by its
// items, so the check reads it as a list too; the same document without the
// hook builds to its item.
func TestDecodeChartManifests_HookInAListUnderAKeyOfAnotherCase(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "a later Kind makes a registered object an unregistered list, hook on the list",
			doc: `{"apiVersion": "v1", "kind": "ConfigMap", "Kind": "ThingList",
 "metadata": {"annotations": {ANNOTATION}},
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm"}}]}`,
			want: `list ThingList "" carries a helm.sh/hook annotation on its own metadata`,
		},
		{
			name: "a later Kind makes a registered object an unregistered list, hook on an item",
			doc: `{"apiVersion": "v1", "kind": "ConfigMap", "Kind": "ThingList",
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm", "annotations": {ANNOTATION}}}]}`,
			want: `item 0 (ConfigMap "cm") of list ThingList "" carries a helm.sh/hook annotation`,
		},
		{
			name: "a later APIVersion makes a registered object an unregistered one that states items",
			doc: `{"apiVersion": "v1", "kind": "ConfigMap", "APIVersion": "example.com/v1",
 "metadata": {"annotations": {ANNOTATION}},
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm"}}]}`,
			want: `list ConfigMap "" carries a helm.sh/hook annotation on its own metadata`,
		},
		{
			name: "the list's hook under Metadata, which Helm reads",
			doc: `{"apiVersion": "v1", "kind": "List",
 "Metadata": {"Annotations": {ANNOTATION}},
 "items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "cm"}}]}`,
			want: `list List "" carries a helm.sh/hook annotation on its own metadata`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hooked := strings.Replace(tc.doc, "ANNOTATION", `"helm.sh/hook": "pre-delete"`, 1)
			_, err := decodeChartManifests([]byte(hooked))
			assertErrorMentions(t, err, "decoding rendered manifests", tc.want)

			control := strings.Replace(tc.doc, "ANNOTATION", `"example.com/note": "kept"`, 1)
			objects, err := decodeChartManifests([]byte(control))
			if err != nil {
				t.Fatalf("control without the hook: %v", err)
			}
			if names := resourceNames(objects); !slices.Equal(names, []string{"cm"}) {
				t.Errorf("control without the hook decoded %v, want [cm]", names)
			}
		})
	}
}

// TestDecodeChartManifests_HookInAListBelowTheNestingBound: the parser's
// nesting bound is on registered lists. A list of an unregistered kind nine
// `v1` Lists deep, one level below what the parser flattens a registered list
// to, is still replaced by its items, so the check still reads it; the same
// document without the hook builds.
func TestDecodeChartManifests_HookInAListBelowTheNestingBound(t *testing.T) {
	nest := func(annotation string) string {
		raw := `{"apiVersion":"example.com/v1","kind":"ThingList","items":[` +
			`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm","annotations":{` + annotation + `}}}]}`
		for range maxCheckedListNesting + 1 {
			raw = `{"apiVersion":"v1","kind":"List","items":[` + raw + `]}`
		}
		return raw
	}
	_, err := decodeChartManifests([]byte(nest(`"helm.sh/hook":"pre-delete"`)))
	assertErrorMentions(t, err, "decoding rendered manifests",
		`item 0 (ConfigMap "cm") of list ThingList "" carries a helm.sh/hook annotation`)

	objects, err := decodeChartManifests([]byte(nest(`"example.com/note":"kept"`)))
	if err != nil {
		t.Fatalf("control without the hook: %v", err)
	}
	if names := resourceNames(objects); !slices.Equal(names, []string{"cm"}) {
		t.Errorf("control without the hook decoded %v, want [cm]", names)
	}
}

// TestDecodeChartManifests_HookOnASingleObjectIsNotRefused: the refusal is for
// lists. An object that is not one keeps its helm.sh/hook annotation and
// decodes. A registered kind that states an items array of its own is no list
// to the parser, which does not replace it by anything, so the hook refusal is
// not its error: it is refused as any object written with an items array its
// type does not declare is.
func TestDecodeChartManifests_HookOnASingleObjectIsNotRefused(t *testing.T) {
	const (
		hook = "  annotations:\n    helm.sh/hook: pre-install\n"
		cm   = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n" + hook
	)
	objects, err := decodeChartManifests([]byte(
		cm + "---\napiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: thing\n" + hook))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	if names := resourceNames(objects); !slices.Equal(names, []string{"cm", "thing"}) {
		t.Fatalf("decoded %v, want [cm thing]", names)
	}
	for _, o := range objects {
		if got := o.GetAnnotations()["helm.sh/hook"]; got != "pre-install" {
			t.Errorf("%s: helm.sh/hook = %q, want pre-install", o.GetName(), got)
		}
	}

	_, err = decodeChartManifests([]byte(cm + "items:\n- metadata:\n    annotations:\n      helm.sh/hook: test\n"))
	assertErrorMentions(t, err, `ConfigMap "cm"`, "declares no `items` field")
	if err != nil && strings.Contains(err.Error(), "carries a helm.sh/hook annotation") {
		t.Errorf("a single object that states items was refused as a hook list: %v", err)
	}
}

// TestDecodeChartManifests_UndeclaredFieldIsKeptOrRefused: a field the API
// type of a registered kind does not declare is not dropped. An object that is
// neither a workload nor a claim comes back unstructured with the field; a
// workload is refused, the error naming the object and the field's path. A
// field whose value has the wrong type is an error, as it was.
func TestDecodeChartManifests_UndeclaredFieldIsKeptOrRefused(t *testing.T) {
	const head = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"
	objects, err := decodeChartManifests([]byte(head + "notAField: x\ndata:\n  k: v\n"))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	u, ok := objects[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("cm is %T, want *unstructured.Unstructured: the document as rendered", objects[0])
	}
	if u.Object["notAField"] != "x" {
		t.Errorf("notAField = %#v, want it kept", u.Object["notAField"])
	}
	if got, _, _ := unstructured.NestedString(u.Object, "data", "k"); got != "v" {
		t.Errorf("data.k = %q, want %q", got, "v")
	}
	assertDeepCopyable(t, objects)

	objects, err = decodeChartManifests([]byte(head + "data:\n  k: v\n"))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	if _, ok := objects[0].(*corev1.ConfigMap); !ok {
		t.Errorf("control: a ConfigMap with declared fields only is %T, want *corev1.ConfigMap", objects[0])
	}

	_, err = decodeChartManifests([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n" +
		"spec:\n  template:\n    spec:\n      fieldOfALaterVersion: true\n"))
	assertErrorMentions(t, err, "decoding rendered manifests", `Deployment "shop/web"`,
		"undeclared field spec.template.spec.fieldOfALaterVersion", "apps/v1 Deployment")

	_, err = decodeChartManifests([]byte(head + "data: not-a-mapping\n"))
	assertErrorMentions(t, err, "decoding rendered manifests")
}

// TestDecodeChartManifests_NonStringMappingKeyBecomesAString: the parser
// converts a document to JSON before it decodes it, so a mapping key that is
// not a string becomes its string form, in a typed object and in an
// unstructured one alike.
func TestDecodeChartManifests_NonStringMappingKeyBecomesAString(t *testing.T) {
	objects, err := decodeChartManifests([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  1: one\n" +
		"---\napiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\n1: x\ntrue: z\nspec:\n  2: two\n"))
	if err != nil {
		t.Fatalf("decodeChartManifests: %v", err)
	}
	if got := objects[0].(*corev1.ConfigMap).Data["1"]; got != "one" {
		t.Errorf(`ConfigMap data["1"] = %q, want "one"`, got)
	}
	u := objects[1].(*unstructured.Unstructured)
	if u.Object["1"] != "x" || u.Object["true"] != "z" {
		t.Errorf(`top-level "1" = %#v, "true" = %#v, want "x" and "z"`, u.Object["1"], u.Object["true"])
	}
	if got, _, _ := unstructured.NestedString(u.Object, "spec", "2"); got != "two" {
		t.Errorf(`spec["2"] = %q, want "two"`, got)
	}
	assertDeepCopyable(t, objects)
}

// TestDecodeChartManifests_UnstructuredScalarsAreJSONTyped covers the scalars
// a chart can render into an object of an unregistered kind: the decoded
// value, at the top of spec, in a list and in a nested mapping, is the type a
// JSON decode gives it, which runtime.DeepCopyJSONValue accepts. An unquoted
// timestamp stays the string the chart wrote, in or out of RFC 3339's range;
// an integer beyond int64 becomes a float64.
func TestDecodeChartManifests_UnstructuredScalarsAreJSONTyped(t *testing.T) {
	cases := []struct {
		scalar string
		want   any
	}{
		{"0", int64(0)},
		{"8080", int64(8080)},
		{"-1", int64(-1)},
		{"0x1F", int64(31)},
		{"9223372036854775807", int64(math.MaxInt64)},
		{"9223372036854775808", 9.223372036854775808e18},
		{"1.5", 1.5},
		{"2001-12-14", "2001-12-14"},
		{"2001-12-14t21:59:43.10-05:00", "2001-12-14t21:59:43.10-05:00"},
		{"2001-12-14T21:59:43+24:00", "2001-12-14T21:59:43+24:00"},
		{"!!binary aGVsbG8=", "hello"},
		{"true", true},
		{"null", nil},
		{"text", "text"},
	}
	for _, tc := range cases {
		t.Run(tc.scalar, func(t *testing.T) {
			doc := "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nspec:\n  v: " + tc.scalar +
				"\n  list:\n  - " + tc.scalar + "\n  nested:\n    v: " + tc.scalar + "\n"
			objects, err := decodeChartManifests([]byte(doc))
			if err != nil {
				t.Fatalf("decodeChartManifests: %v", err)
			}
			if len(objects) != 1 {
				t.Fatalf("got %d objects, want 1", len(objects))
			}
			u := objects[0].(*unstructured.Unstructured)
			spec := u.Object["spec"].(map[string]any)
			for where, got := range map[string]any{
				"spec.v":        spec["v"],
				"spec.list[0]":  spec["list"].([]any)[0],
				"spec.nested.v": spec["nested"].(map[string]any)["v"],
			} {
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("%s = %#v (%T), want %#v (%T)", where, got, got, tc.want, tc.want)
				}
			}
			assertDeepCopyable(t, objects)
		})
	}
}

// TestDecodeChartManifests_LargeIntegerAsWritten: what kure's manifest writer
// writes for a large integer a chart rendered into an object of an unregistered
// kind. One an int64 holds is decoded as an int64 and written with its own
// digits (go-kure/kure#1006). One above that range is a float64 from the decode
// on, so it is written as that float, with the shortest digits that read back
// as it: not the chart's digits, also where the float holds the integer (2^63).
func TestDecodeChartManifests_LargeIntegerAsWritten(t *testing.T) {
	for rendered, written := range map[string]string{
		"9007199254740993":     "9007199254740993",
		"9223372036854775807":  "9223372036854775807",
		"9223372036854775808":  "9223372036854776000",
		"9223372036854775809":  "9223372036854776000",
		"18446744073709551615": "1.8446744073709552e+19",
	} {
		t.Run(rendered, func(t *testing.T) {
			doc := "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nspec:\n  v: " + rendered + "\n"
			objects, err := decodeChartManifests([]byte(doc))
			if err != nil {
				t.Fatalf("decodeChartManifests: %v", err)
			}
			if len(objects) != 1 {
				t.Fatalf("got %d objects, want 1", len(objects))
			}
			out, err := kureio.EncodeObjectsToYAML([]*client.Object{&objects[0]})
			if err != nil {
				t.Fatalf("EncodeObjectsToYAML: %v", err)
			}
			if want := "  v: " + written + "\n"; !strings.Contains(string(out), want) {
				t.Errorf("the written object lacks %q:\n%s", want, out)
			}
		})
	}
}

// assertErrorMentions fails t unless err is non-nil and its text contains
// every one of wants.
func assertErrorMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestDecodeChartManifests_UndecodableDocumentOfADroppedHookIsAnError: every
// document is decoded before hook grouping drops any, so one that does not
// decode fails the build even when its helm.sh/hook annotation would have had
// it dropped unwritten.
func TestDecodeChartManifests_UndecodableDocumentOfADroppedHookIsAnError(t *testing.T) {
	const hooked = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: hooked\n  annotations:\n    helm.sh/hook: test\n"
	const mainDoc = "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: main\n"
	groups, err := parseChartManifests([]byte(hooked + mainDoc))
	if err != nil {
		t.Fatalf("parseChartManifests: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Resources) != 1 || groups[0].Resources[0].GetName() != "main" {
		t.Fatalf("control: a decodable test hook is dropped and main kept; got %d group(s)", len(groups))
	}
	_, err = parseChartManifests([]byte(hooked + "data: not-a-mapping\n" + mainDoc))
	assertErrorMentions(t, err, "decoding rendered manifests")
}

func TestGenerate_FlattensHookGroupsInExecutionOrder(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	var names []string
	for _, o := range objects {
		names = append(names, (*o).GetName())
	}
	want := []string{"pre", "main", "post"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("execution order = %v, want %v", names, want)
			break
		}
	}
}

func TestAugmentLayout_SingleGroup_NoChildren(t *testing.T) {
	raw := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n")
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 0 {
		t.Errorf("ml.Children has %d entries, want 0 (a single hook group is a no-op)", len(ml.Children))
	}
	if ml.ApplicationFileMode != layout.AppFileUnset {
		t.Errorf("ml.ApplicationFileMode = %v, want AppFileUnset (the directory pin applies only when partitioning)", ml.ApplicationFileMode)
	}
}

func TestAugmentLayout_MultiGroup_PartitionsAndChains(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{
		Name:                "myapp",
		Namespace:           "team", // the enclosing layout's path, as kure's walker sets it
		Resources:           []client.Object{&unstructured.Unstructured{}},
		Mode:                layout.KustomizationExplicit,
		FluxPlacement:       layout.FluxIntegratedPerLayout,
		FileNaming:          layout.FileNamingKindName,
		FilePer:             layout.FilePerKind,
		ApplicationFileMode: layout.AppFileSingle, // must NOT propagate to children
	}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if ml.Resources != nil {
		t.Errorf("ml.Resources = %v, want nil after partitioning", ml.Resources)
	}
	if ml.ApplicationFileMode != layout.AppFileSingle {
		t.Errorf("ml.ApplicationFileMode = %v, want the caller's explicit AppFileSingle kept", ml.ApplicationFileMode)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}

	wantNames := []string{"myapp-00-pre-install", "myapp-01-main", "myapp-02-post-install"}
	var prevName string
	for i, child := range ml.Children {
		if child.Name != wantNames[i] {
			t.Errorf("Children[%d].Name = %q, want %q", i, child.Name, wantNames[i])
		}
		if child.Namespace != "team/myapp" {
			t.Errorf("Children[%d].Namespace = %q, want the parent path %q", i, child.Namespace, "team/myapp")
		}
		// One level below the component's directory, never nested twice.
		if wantPath := "team/myapp/" + wantNames[i]; child.FullRepoPath() != wantPath {
			t.Errorf("Children[%d].FullRepoPath() = %q, want %q", i, child.FullRepoPath(), wantPath)
		}
		if child.Mode != ml.Mode {
			t.Errorf("Children[%d].Mode = %v, want %v", i, child.Mode, ml.Mode)
		}
		if child.FluxPlacement != ml.FluxPlacement {
			t.Errorf("Children[%d].FluxPlacement = %v, want %v", i, child.FluxPlacement, ml.FluxPlacement)
		}
		if child.FileNaming != ml.FileNaming {
			t.Errorf("Children[%d].FileNaming = %v, want %v", i, child.FileNaming, ml.FileNaming)
		}
		if child.FilePer != ml.FilePer {
			t.Errorf("Children[%d].FilePer = %v, want %v", i, child.FilePer, ml.FilePer)
		}
		if child.ApplicationFileMode != layout.AppFileUnset {
			t.Errorf("Children[%d].ApplicationFileMode = %v, want AppFileUnset (must not inherit ml's AppFileSingle)", i, child.ApplicationFileMode)
		}
		if i == 0 {
			if len(child.DependsOn) != 0 {
				t.Errorf("Children[0].DependsOn = %v, want empty", child.DependsOn)
			}
		} else if len(child.DependsOn) != 1 || child.DependsOn[0] != prevName {
			t.Errorf("Children[%d].DependsOn = %v, want [%q]", i, child.DependsOn, prevName)
		}
		prevName = child.Name
	}
}

// findLayoutByName returns the first layout named name in ml's tree, or nil.
func findLayoutByName(ml *layout.ManifestLayout, name string) *layout.ManifestLayout {
	if ml == nil {
		return nil
	}
	if ml.Name == name {
		return ml
	}
	for _, c := range ml.Children {
		if found := findLayoutByName(c, name); found != nil {
			return found
		}
	}
	return nil
}

// kustomizationResources returns the resources entries of dir/kustomization.yaml.
func kustomizationResources(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read kustomization.yaml: %v", err)
	}
	var k struct {
		Resources []string `yaml:"resources"`
	}
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("parse %s/kustomization.yaml: %v", dir, err)
	}
	return k.Resources
}

// reachableObjectNames follows dir's kustomization.yaml resources entries the
// way a kustomize build does — a file entry adds its objects, a directory
// entry adds that directory's own build — and returns every object name
// reached. An entry that does not resolve on disk fails the test.
func reachableObjectNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	var build func(d string)
	build = func(d string) {
		for _, e := range kustomizationResources(t, d) {
			p := filepath.Join(d, e)
			fi, err := os.Stat(p)
			if err != nil {
				t.Errorf("%s/kustomization.yaml: resources entry %q does not resolve (%v)", d, e, err)
				continue
			}
			if fi.IsDir() {
				build(p)
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			objs, err := decodeChartManifests(data)
			if err != nil {
				t.Fatalf("decode %s: %v", p, err)
			}
			for _, o := range objs {
				names[o.GetName()] = true
			}
		}
	}
	build(dir)
	return names
}

func TestExcludedHookPhasesAreDropped(t *testing.T) {
	excludedPhases := []string{"pre-delete", "post-delete", "pre-rollback", "post-rollback", "test"}
	var raw strings.Builder
	for i, phase := range excludedPhases {
		if i > 0 {
			raw.WriteString("---\n")
		}
		fmt.Fprintf(&raw, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: %s-pod\n  annotations:\n    helm.sh/hook: %s\n", phase, phase)
	}
	raw.WriteString("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kept\n")

	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return []byte(raw.String()), nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 surviving object (the 5 excluded-phase objects dropped), got %d", len(objects))
	}
	if got := (*objects[0]).GetName(); got != "kept" {
		t.Errorf("surviving object name = %q, want %q", got, "kept")
	}
}

func TestHookGroupDir_EmptyPhaseIsMain(t *testing.T) {
	if got := hookGroupDir(helm.HookGroup{Phase: ""}); got != "main" {
		t.Errorf("hookGroupDir(empty phase) = %q, want %q", got, "main")
	}
}

func TestHookGroupDir_SanitizesUnsafePhase(t *testing.T) {
	cases := []struct{ phase, want string }{
		{"pre-install,post-install", "pre-install-post-install"},
		{"PRE-INSTALL", "pre-install"},
		{"weird/phase", "weird-phase"},
		{"../../etc", "etc"},
		{"!!!", "unknown"}, // punctuation-only phase strips to an empty slug — pins the "unknown" fallback
	}
	for _, c := range cases {
		if got := hookGroupDir(helm.HookGroup{Phase: c.phase}); got != c.want {
			t.Errorf("hookGroupDir(%q) = %q, want %q", c.phase, got, c.want)
		}
	}
}

func TestHookGroupDir_TruncatesLongPhase(t *testing.T) {
	long := strings.Repeat("a", 80)
	got := hookGroupDir(helm.HookGroup{Phase: long})
	if len(got) > 40 {
		t.Errorf("len(hookGroupDir(80-char phase)) = %d, want <= 40", len(got))
	}
	if got != strings.Repeat("a", 40) {
		t.Errorf("hookGroupDir(80-char phase) = %q, want 40 a's", got)
	}
}

// TestAugmentLayout_ChildNameStaysWithinDNS1123Limit exercises
// hookGroupChildName directly with near-253-char ml.Names (validate.go's
// DNS-1123 subdomain max), including one whose truncation boundary lands
// right after a '.', and pins both the within-name and cross-name uniqueness
// guarantees hookGroupChildName's doc comment claims.
func TestAugmentLayout_ChildNameStaysWithinDNS1123Limit(t *testing.T) {
	groups := []helm.HookGroup{
		{Phase: "pre-install"},
		{Phase: strings.Repeat("x", 80)}, // slugs+truncates to 40 x's via hookGroupDir
	}

	// mlNameA's truncation boundary (prefixLen=227 for group 0's suffix
	// "-00-pre-install", len 15: maxPrefix=253-15=238, prefixLen=238-10-1=227)
	// lands right after a literal '.': mlNameA[:227] ends in ".", exercising
	// the trailing-separator trim of the shortening rule (oam.ShortenName).
	mlNameA := strings.Repeat("a", 226) + "." + strings.Repeat("b", 26)
	if len(mlNameA) != 253 {
		t.Fatalf("test setup: len(mlNameA) = %d, want 253", len(mlNameA))
	}
	if mlNameA[226] != '.' {
		t.Fatalf("test setup: mlNameA[226] = %q, want '.'", mlNameA[226])
	}

	namesA := make([]string, len(groups))
	for i, g := range groups {
		dn := hookGroupChildName("", mlNameA, i, g)
		if len(dn) > 253 {
			t.Errorf("group %d: len(%q) = %d, want <= 253", i, dn, len(dn))
		}
		if errs := validation.IsDNS1123Subdomain(dn); len(errs) != 0 {
			t.Errorf("group %d: IsDNS1123Subdomain(%q) = %v, want no errors", i, dn, errs)
		}
		if strings.HasSuffix(dn, ".") || strings.HasSuffix(dn, "-") {
			t.Errorf("group %d: %q has a dangling '-'/'.' artifact from truncation", i, dn)
		}
		namesA[i] = dn
	}
	if namesA[0] == namesA[1] {
		t.Fatalf("hookGroupChildName collided across groups for one ml.Name: both produced %q", namesA[0])
	}

	// A second near-253-char ml.Name sharing mlNameA's truncated prefix must
	// still yield a distinct dirName set — the sha256 prefix, not just the
	// group index, is what prevents cross-name collision (as
	// TestShortenName_GeneratingSites shows for every site).
	mlNameB := strings.Repeat("a", 226) + "." + strings.Repeat("c", 26)
	if len(mlNameB) != 253 {
		t.Fatalf("test setup: len(mlNameB) = %d, want 253", len(mlNameB))
	}
	if mlNameA == mlNameB {
		t.Fatal("test setup: mlNameA and mlNameB must differ")
	}
	for i, g := range groups {
		dnA := hookGroupChildName("", mlNameA, i, g)
		dnB := hookGroupChildName("", mlNameB, i, g)
		if dnA == dnB {
			t.Errorf("group %d: hookGroupChildName collided across ml.Names: mlNameA=%q mlNameB=%q both produced %q", i, mlNameA, mlNameB, dnA)
		}
	}
}

// TestHookGroupChildName_IncludesApplication pins go-kure/launcher#792 on the
// name itself: the application leads it, an empty application leaves the name
// as it was, and an application and a component that together exceed the
// limit are shortened as one prefix, with the suffix whole and the digest
// taken over both — so two applications whose long names differ only past the
// cut, each with the same component, still get different child names.
func TestHookGroupChildName_IncludesApplication(t *testing.T) {
	pre := helm.HookGroup{Phase: "pre-install"}
	if got, want := hookGroupChildName("shop", "db", 0, pre), "shop-db-00-pre-install"; got != want {
		t.Errorf("with an application = %q, want %q", got, want)
	}
	if got, want := hookGroupChildName("", "db", 0, pre), "db-00-pre-install"; got != want {
		t.Errorf("without an application = %q, want %q", got, want)
	}
	if a, b := hookGroupChildName("shop", "db", 0, pre), hookGroupChildName("billing", "db", 0, pre); a == b {
		t.Errorf("two applications with a component db both gave %q", a)
	}

	shared := strings.Repeat("a", 240)
	appA := shared + "." + strings.Repeat("b", 12) // 253 characters, a valid application name
	appB := shared + "." + strings.Repeat("c", 12)
	component := strings.Repeat("d", 253)
	const suffix = "-00-pre-install"
	gotA, gotB := hookGroupChildName(appA, component, 0, pre), hookGroupChildName(appB, component, 0, pre)
	for app, got := range map[string]string{appA: gotA, appB: gotB} {
		if want := wantShortened(app+"-"+component, suffix, oam.ShortenLimitSubdomain); got != want {
			t.Errorf("shortened = %q, want %q", got, want)
		}
		if len(got) > oam.ShortenLimitSubdomain {
			t.Errorf("len(%q) = %d, over 253", got, len(got))
		}
		if !strings.HasSuffix(got, suffix) {
			t.Errorf("%q lost its suffix %q", got, suffix)
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
			t.Errorf("IsDNS1123Subdomain(%q) = %v, want no errors", got, errs)
		}
	}
	if gotA == gotB {
		t.Errorf("two applications sharing their first 240 characters both gave %q", gotA)
	}
}

// generateNames runs Generate and returns the resulting objects' names in
// order, failing the test on any error.
func generateNames(t *testing.T, cfg *HelmTemplateConfig) []string {
	t.Helper()
	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	names := make([]string, len(objects))
	for i, o := range objects {
		names[i] = (*o).GetName()
	}
	return names
}

// TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition covers a
// distinct branch untested by every other multi-event test: those all list
// their tokens in ascending priority order already
// ("pre-install,pre-upgrade"), so a naive
// implementation that simply picked the FIRST recognized token (rather than
// the earliest by kure's own hookPhaseOrder priority) would pass every one
// of them. "pre-upgrade,pre-install" (tokens in descending priority order)
// distinguishes the two: it must still resolve to the pre-install group.
func TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-upgrade,pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "pre-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (earliest by priority, not by listed position)", got, "pre-install")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase covers a
// multi-value annotation mixing a recognized ordered phase with an excluded
// one ("pre-install,pre-delete"): the excluded token must not suppress the
// whole object, and the object must still land in the pre-install group.
func TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-install,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"multi", "main"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("execution order = %v, want %v (multi must survive, ordered ahead of main)", got, want)
		}
	}
}

// TestGenerate_MultiEventHookAllExcludedIsDropped covers a multi-value
// annotation whose tokens are entirely excluded phases ("test,pre-delete"):
// kure's exact-string-match excludedHookPhases lookup (hooks.go:20-26,49)
// never excludes the combined string, so without normalization this object
// would wrongly survive into the mis-sorted unknown bucket instead of being
// dropped, same as a single excluded phase is today.
func TestGenerate_MultiEventHookAllExcludedIsDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kept
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"kept"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only the non-hook object to survive, got %v", got)
	}
}

// TestGenerate_MultiEventCustomHooksStayUnknown proves the fix does not
// overcorrect: a multi-value annotation made entirely of unrecognized custom
// hook names ("crd-install,some-custom-hook" — no member of the excluded or
// four-ordered sets) has no defined ordering priority among its tokens, so it
// must be left exactly as kure's own unknown-bucket fallback already handles
// it — sorted alphabetically after post-upgrade, annotation untouched.
func TestGenerate_MultiEventCustomHooksStayUnknown(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: crd-install,some-custom-hook
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
	u := *objects[1]
	if u.GetName() != "multi" {
		t.Fatalf("execution order: objects[1].Name = %q, want %q (unrecognized custom hook must sort last, unchanged)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != "crd-install,some-custom-hook" {
		t.Errorf("multi's helm.sh/hook annotation = %q, want unchanged %q", got, "crd-install,some-custom-hook")
	}
	// Pin the grouping key itself, not just execution order and the emitted
	// annotation: a broken normalizer could rewrite the grouping key (e.g. to
	// just "crd-install", dropping "some-custom-hook") while still leaving
	// the emitted object's own annotation untouched and this object sorting
	// last purely by chance of the two orderings comparing equal here — the
	// Phase assertion is the one check that would catch that.
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[1].Phase; got != "crd-install,some-custom-hook" {
		t.Errorf("hookGroups[1].Phase = %q, want unchanged %q (grouping key for an all-custom annotation must not be rewritten)", got, "crd-install,some-custom-hook")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks covers a
// branch not handled by an earlier version of this fix: a
// multi-value annotation mixing an excluded phase with an unrecognized
// custom hook name ("test,crd-install") has no recognized ordered phase
// among its tokens, so it does not take the earliest-phase branch — but it
// is not "every token unrecognized" either (one token, "test", IS a member
// of excludedHookPhases), so it must not take the leave-unchanged branch
// either. The excluded token must be dropped from the grouping key, leaving
// just "crd-install" — an excluded phase must never influence the unknown-
// bucket grouping decision for an object that survives (is not entirely
// excluded).
func TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,crd-install
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group (multi survives, not all tokens excluded), got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "crd-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (excluded token \"test\" dropped from grouping key)", got, "crd-install")
	}
	if len(cfg.hookGroups[0].Resources) != 1 {
		t.Fatalf("hookGroups[0].Resources has %d entries, want 1", len(cfg.hookGroups[0].Resources))
	}
	if got := cfg.hookGroups[0].Resources[0].GetAnnotations()["helm.sh/hook"]; got != "test,crd-install" {
		t.Errorf("emitted object's helm.sh/hook annotation = %q, want unchanged %q", got, "test,crd-install")
	}
}

// TestGenerate_CommaOnlyHookAnnotationIsNotDropped covers a degenerate
// annotation with no actual token content at all ("," — every split token is
// empty after trimming). Before this multi-event fix existed, kure's
// SplitByHookWeight would have treated the literal string "," as one opaque
// unknown-phase string — not excluded (excludedHookPhases has no "," entry)
// and not dropped. normalizeHookAnnotationForGrouping must reach the same
// outcome: "no non-empty token was ever excluded" is a different condition
// from "every non-empty token was excluded", and only the latter should
// route to the drop-via-"test" branch.
func TestGenerate_CommaOnlyHookAnnotationIsNotDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ","
`)
	cfg := helmTemplateFixture(t, func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	var total int
	for _, g := range cfg.hookGroups {
		total += len(g.Resources)
	}
	if total != 1 {
		t.Fatalf("expected the comma-only-hook object to survive (not all tokens excluded — there were no tokens at all), got %d resources across %d groups", total, len(cfg.hookGroups))
	}
	// Pin the grouping key itself: a broken normalizer could rewrite "," to
	// "" (or some other value) while still leaving the object present, which
	// the resource-count check above alone would not catch.
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "," {
		t.Errorf("hookGroups[0].Phase = %q, want unchanged %q (a degenerate annotation must not be rewritten)", got, ",")
	}
}

// TestAugmentLayout_MultiEventHookAnnotationUnchangedInOutput is the
// test called for by the fix's own hazard: the grouping-key rewrite
// (normalizeHookAnnotationForGrouping) must never leak into the object that
// ends up in emitted output. Exercises both Generate (flattened union) and
// AugmentLayout (repartitioned into
// child layouts) — a no-op implementation that simply left the multi-event
// annotation untouched would satisfy the "annotation unchanged" half of this
// test but fail its "correct group placement" half, and a broken
// implementation that mutated the object in place would fail the reverse —
// only a correct fix (copy-for-grouping, restore-original-for-output)
// satisfies both halves at once.
func TestAugmentLayout_MultiEventHookAnnotationUnchangedInOutput(t *testing.T) {
	const wantHook = "pre-install,pre-upgrade"
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ` + wantHook + `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	renderChart := func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	}

	// Generate path: correct placement (first) and unchanged annotation.
	genCfg := helmTemplateFixture(t, renderChart)
	objects, err := genCfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	u := *objects[0]
	if u.GetName() != "multi" {
		t.Fatalf("Generate execution order: objects[0].Name = %q, want %q (earliest-phase placement)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("Generate: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}

	// AugmentLayout path: correct child group placement (dirName derived from
	// the earliest phase, "pre-install") and unchanged annotation on the
	// resource inside that child.
	augCfg := helmTemplateFixture(t, renderChart)
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := augCfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}
	if ml.Children[0].Name != "myapp-00-pre-install" {
		t.Fatalf("Children[0].Name = %q, want %q (multi's group keyed by its earliest phase)", ml.Children[0].Name, "myapp-00-pre-install")
	}
	if len(ml.Children[0].Resources) != 1 {
		t.Fatalf("Children[0].Resources has %d entries, want 1", len(ml.Children[0].Resources))
	}
	child := ml.Children[0].Resources[0]
	if child.GetName() != "multi" {
		t.Fatalf("Children[0].Resources[0].Name = %q, want %q", child.GetName(), "multi")
	}
	if got := child.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("AugmentLayout: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}
}
