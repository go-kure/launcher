package components

// Tests of the undeclared-field rule on the items of a list of a registered
// kind, which kure's parser replaces by its items (undeclared_fields.go,
// keepListItemFields).

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestDecodeManifestDocuments_WorkloadInAListIsRefused: a workload that sets a
// field its Go type does not declare is refused inside a list as it is alone,
// the error naming its position: in a `v1` List, whose items the strict decode
// of the list itself does not look into, in a typed list whose item leaves its
// kind out, and in a list nested in a `v1` List. Without the field each list
// decodes to its items, as their Go types.
func TestDecodeManifestDocuments_WorkloadInAListIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		wants   []string
		objects []string
	}{
		{
			name: "v1 List",
			doc: "apiVersion: v1\nkind: List\nitems:\n" +
				"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: settings\n" +
				"- apiVersion: apps/v1\n  kind: Deployment\n  metadata:\n    name: web\n  spec:\n    template:\n      spec:\n        FIELD\n        hostNetwork: false\n",
			wants: []string{"item 1 of List", `Deployment "web"`, "undeclared field spec.template.spec.fieldOfALaterVersion:",
				"cannot be checked against environment policy"},
			objects: []string{"settings", "web"},
		},
		{
			name: "typed list, the item leaves its kind out",
			doc: "apiVersion: apps/v1\nkind: DeploymentList\nitems:\n" +
				"- metadata:\n    name: web\n  spec:\n    template:\n      spec:\n        FIELD\n        hostNetwork: false\n",
			wants: []string{"item 0 of DeploymentList", `Deployment "web"`, "undeclared field spec.template.spec.fieldOfALaterVersion:",
				"the apps/v1 Deployment type"},
			objects: []string{"web"},
		},
		{
			name: "a list nested in a v1 List",
			doc: "apiVersion: v1\nkind: List\nitems:\n" +
				"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: settings\n" +
				"- apiVersion: v1\n  kind: PodList\n  items:\n  - metadata:\n      name: cleanup\n    spec:\n      FIELD\n      hostNetwork: false\n",
			wants:   []string{"item 1 of List: item 0 of PodList", `Pod "cleanup"`, "undeclared field spec.fieldOfALaterVersion:"},
			objects: []string{"settings", "cleanup"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.doc, "FIELD\n") {
				t.Fatal("test premise: the document has no FIELD placeholder")
			}
			_, err := decodeManifestDocuments([]byte(strings.Replace(tc.doc, "FIELD\n", "fieldOfALaterVersion: x\n", 1)))
			assertErrorMentions(t, err, tc.wants...)

			objs, err := decodeManifestDocuments([]byte(strings.Replace(tc.doc, "FIELD\n", "nodeName: \"\"\n", 1)))
			if err != nil {
				t.Fatalf("control: with a declared field in its place: %v", err)
			}
			if names := resourceNames(objs); !slices.Equal(names, tc.objects) {
				t.Errorf("control decoded %v, want %v", names, tc.objects)
			}
			for _, obj := range objs {
				if _, untyped := obj.(*unstructured.Unstructured); untyped {
					t.Errorf("control: %s is unstructured, want its Go type", obj.GetName())
				}
			}
		})
	}
}

// TestDecodeManifestDocuments_ListWithSeveralItems: a list of several items is
// settled item by item. The item that sets an undeclared field, of a kind that
// is neither a workload nor a claim, comes back unstructured with the field;
// the items beside it are their Go types; the list's order stands.
func TestDecodeManifestDocuments_ListWithSeveralItems(t *testing.T) {
	objs, err := decodeManifestDocuments([]byte("apiVersion: v1\nkind: List\nitems:\n" +
		"- apiVersion: apps/v1\n  kind: Deployment\n  metadata:\n    name: web\n" +
		"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: settings\n  fieldOfALaterVersion: kept\n" +
		"- apiVersion: example.com/v1\n  kind: Thing\n  metadata:\n    name: thing\n  anything: x\n" +
		"- apiVersion: v1\n  kind: Service\n  metadata:\n    name: api\n"))
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	if names := resourceNames(objs); !slices.Equal(names, []string{"web", "settings", "thing", "api"}) {
		t.Fatalf("decoded %v, want [web settings thing api]", names)
	}
	if _, ok := objs[0].(*appsv1.Deployment); !ok {
		t.Errorf("web is %T, want *appsv1.Deployment", objs[0])
	}
	kept, ok := objs[1].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("settings is %T, want *unstructured.Unstructured: the item as written", objs[1])
	}
	if written := writtenYAML(t, kept); !strings.Contains(written, "fieldOfALaterVersion: kept\n") {
		t.Errorf("written item lacks the field:\n%s", written)
	}
	if _, ok := objs[3].(*corev1.Service); !ok {
		t.Errorf("api is %T, want *corev1.Service", objs[3])
	}
	assertDeepCopyable(t, objs)
}

// TestDecodeManifestDocuments_KeptTypedListItemStatesItsKind: an item of a
// typed list that leaves apiVersion and kind out and sets an undeclared field
// comes back unstructured, as written, with the kind the list holds: written
// out without one it would not be an object.
func TestDecodeManifestDocuments_KeptTypedListItemStatesItsKind(t *testing.T) {
	objs, err := decodeManifestDocuments([]byte("apiVersion: v1\nkind: ConfigMapList\nitems:\n" +
		"- metadata:\n    name: settings\n  fieldOfALaterVersion: kept\n"))
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("decoded %d objects, want 1", len(objs))
	}
	u, ok := objs[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("decoded a %T, want *unstructured.Unstructured", objs[0])
	}
	if u.GetAPIVersion() != "v1" || u.GetKind() != "ConfigMap" || u.GetName() != "settings" {
		t.Errorf("kept item is %s %s %q, want v1 ConfigMap \"settings\"", u.GetAPIVersion(), u.GetKind(), u.GetName())
	}
	if written := writtenYAML(t, u); !strings.Contains(written, "fieldOfALaterVersion: kept\n") {
		t.Errorf("written item lacks the field:\n%s", written)
	}
}

// TestDecodeManifestDocuments_ListsOwnFieldsAreNotTheItems: a field the list
// states on itself is not a field of an item. The list's metadata, a key its
// type does not declare and a number no Go type holds leave the items as their
// Go types, the workload among them built, not refused; the list is never
// emitted, so nothing of it is lost.
func TestDecodeManifestDocuments_ListsOwnFieldsAreNotTheItems(t *testing.T) {
	for name, raw := range map[string]string{
		"v1 List": `{"apiVersion": "v1", "kind": "List",
 "metadata": {"name": "bundle", "annotations": {"example.com/note": "x"}},
 "fieldOfALaterVersion": 1e1000,
 "items": [{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "cleanup"}},
           {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings"}}]}`,
		"typed list": `{"apiVersion": "v1", "kind": "PodList",
 "metadata": {"name": "bundle", "annotations": {"example.com/note": "x"}},
 "fieldOfALaterVersion": 1e1000,
 "items": [{"metadata": {"name": "cleanup"}}, {"metadata": {"name": "settings"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			objs, err := decodeManifestDocuments([]byte(raw))
			if err != nil {
				t.Fatalf("decodeManifestDocuments: %v", err)
			}
			if names := resourceNames(objs); !slices.Equal(names, []string{"cleanup", "settings"}) {
				t.Fatalf("decoded %v, want [cleanup settings]", names)
			}
			if _, ok := objs[0].(*corev1.Pod); !ok {
				t.Errorf("cleanup is %T, want *corev1.Pod", objs[0])
			}
			for _, obj := range objs {
				if _, untyped := obj.(*unstructured.Unstructured); untyped {
					t.Errorf("%s is unstructured: a field of the list was read as the item's", obj.GetName())
				}
			}
		})
	}
}

// TestDecodeManifestDocuments_ListItemIsHeldToEveryRefusal: the refusals of a
// document that is not a workload's undeclared field hold for a list item too:
// an item of a kind that declares no `items` and states an items array, and a
// workload whose repeated keys fill the strict decode's record.
func TestDecodeManifestDocuments_ListItemIsHeldToEveryRefusal(t *testing.T) {
	_, err := decodeManifestDocuments([]byte("apiVersion: v1\nkind: List\nitems:\n" +
		"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: thing\n  items:\n  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: inner\n"))
	assertErrorMentions(t, err, "item 0 of List", `ConfigMap "thing"`, "declares no `items` field")

	full := fmt.Sprintf(`{"apiVersion": "v1", "kind": "List", "items": [%s]}`,
		documentWithRepeatedKeys("v1", "Pod", strictErrorLimit, ""))
	_, err = decodeManifestDocuments([]byte(full))
	if !errors.Is(err, errStrictRecordFull) {
		t.Errorf("a workload item with a full record of strict errors: error = %v, want errStrictRecordFull", err)
	}
	assertErrorMentions(t, err, "item 0 of List")
}

// TestRegisteredListDocument_IsTheParsersDetection holds registeredListDocument
// to kure's parser, whose list detection it repeats: over documents that state
// their type in each way the two readers of it differ on, it holds a document
// for a list of a registered kind exactly when the parser replaces the document
// by its typed items. A document the parser refuses is never settled, so only
// those it accepts are compared; the test fails if it accepts too few of them
// to compare anything.
func TestRegisteredListDocument_IsTheParsersDetection(t *testing.T) {
	const items = `"items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "a"}},
                 {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "b"}}]`
	cases := map[string]struct {
		doc      string
		wantList bool
	}{
		"v1 List":                             {`{"apiVersion": "v1", "kind": "List", ` + items + `}`, true},
		"typed list":                          {`{"apiVersion": "v1", "kind": "ConfigMapList", ` + items + `}`, true},
		"kind stated last under another case": {`{"apiVersion": "v1", "kind": "List", "Kind": "ThingList", ` + items + `}`, true},
		"items stated twice":                  {`{"apiVersion": "v1", "kind": "List", "items": [], ` + items + `}`, true},
		"a list of an unregistered kind":      {`{"apiVersion": "example.com/v1", "kind": "ThingList", ` + items + `}`, false},
		"a list kind stated under Kind only":  {`{"apiVersion": "example.com/v1", "kind": "Thing", "Kind": "ThingList", ` + items + `}`, false},
		"a single object":                     {`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "a"}}`, false},
		"an unregistered single object":       {`{"apiVersion": "example.com/v1", "kind": "Thing", "metadata": {"name": "a"}}`, false},
	}
	compared := 0
	for name, tc := range cases {
		objs, err := kureio.ParseYAMLWithOptions([]byte(tc.doc), manifestParseOptions)
		if err != nil {
			t.Errorf("%s: test premise: the parser refuses the document: %v", name, err)
			continue
		}
		typedItems := len(objs) == 2
		for _, obj := range objs {
			if _, ok := obj.(*corev1.ConfigMap); !ok {
				typedItems = false
			}
		}
		if typedItems != tc.wantList {
			t.Errorf("%s: test premise: the parser returned typed items = %v, want %v (%v)", name, typedItems, tc.wantList, resourceNames(objs))
			continue
		}
		compared++
		list, isList := registeredListDocument([]byte(tc.doc))
		if isList != tc.wantList {
			t.Errorf("%s: registeredListDocument holds it for a list = %v, the parser = %v", name, isList, tc.wantList)
			continue
		}
		if isList && len(list.items) != len(objs) {
			t.Errorf("%s: read %d items, the parser returned %d objects", name, len(list.items), len(objs))
		}
	}
	if compared < len(cases) {
		t.Errorf("compared %d of %d documents", compared, len(cases))
	}

	// Every list kind the scheme registers is recognised, and no other kind is;
	// the generic list is the one whose items are documents of any kind.
	lists, generic := 0, 0
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		made, err := kubernetes.Scheme.New(gvk)
		if err != nil {
			t.Fatalf("Scheme.New(%s): %v", gvk, err)
		}
		_, isObject := made.(client.Object)
		want := !isObject && meta.IsListType(made)
		doc := fmt.Sprintf(`{"apiVersion": %q, "kind": %q, "items": []}`, gvk.GroupVersion().String(), gvk.Kind)
		list, got := registeredListDocument([]byte(doc))
		if got != want {
			t.Errorf("%s: registeredListDocument = %v, want %v", gvk, got, want)
		}
		if got {
			lists++
			if list.generic {
				generic++
				if gvk.Kind != "List" {
					t.Errorf("%s is read as the generic list", gvk)
				}
			}
		}
	}
	if lists < 20 || generic == 0 {
		t.Errorf("recognised %d list kinds, %d of them generic; want the scheme's list kinds and the v1 List among them", lists, generic)
	}
}

// TestKeepDocumentFields_RefusesAReadingThatIsNotTheParsers: when the items
// read from a list do not account for the objects the parser returned, or a
// document that is no registered list decoded to several objects with a typed
// one among them, some object's fields would go unread. Each is refused.
func TestKeepDocumentFields_RefusesAReadingThatIsNotTheParsers(t *testing.T) {
	pods := []client.Object{&corev1.Pod{}, &corev1.Pod{}}
	one := []byte(`{"apiVersion": "v1", "kind": "PodList", "items": [{"metadata": {"name": "a"}}]}`)
	_, err := keepDocumentFields(one, pods)
	assertErrorMentions(t, err, "the items of PodList were read as 1 objects and decoded to 2", "defect of this build")

	generic := []byte(`{"apiVersion": "v1", "kind": "List", "items": [{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "a"}}]}`)
	_, err = keepDocumentFields(generic, pods)
	assertErrorMentions(t, err, "the items of List were read as 1 objects and decoded to 2", "defect of this build")

	single := []byte(`{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "a"}}`)
	_, err = keepDocumentFields(single, pods)
	assertErrorMentions(t, err, "a document that is no list of a registered kind decoded to 2 objects", "defect of this build")

	untyped := []client.Object{&unstructured.Unstructured{}, &unstructured.Unstructured{}}
	thingList := []byte(`{"apiVersion": "example.com/v1", "kind": "ThingList", "items": [{}, {}]}`)
	if objs, err := keepDocumentFields(thingList, untyped); err != nil || len(objs) != 2 {
		t.Errorf("control: the untyped items of an unregistered list: %d objects, %v; want them back", len(objs), err)
	}
}
