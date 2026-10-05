package components

// Tests of the undeclared-field rule on the items of a list, which kure's
// parser replaces by its items (undeclared_fields.go, keepListItemFields), and
// of what is a list to that parser (listDocumentOf).

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

	"github.com/go-kure/launcher/pkg/oam"
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
// states on itself is not a field of an item. The list's name and
// resourceVersion, a key its type does not declare and a number no Go type
// holds leave the items as their Go types, the workload among them built, not
// refused; the list is never emitted, and nothing an item could have kept is
// lost with it. What an item cannot keep, a label or an annotation of the
// list, is the parser's refusal, in its own words.
func TestDecodeManifestDocuments_ListsOwnFieldsAreNotTheItems(t *testing.T) {
	for name, raw := range map[string]string{
		"v1 List": `{"apiVersion": "v1", "kind": "List",
 "metadata": {"name": "bundle", "resourceVersion": "7"NOTE},
 "fieldOfALaterVersion": 1e1000,
 "items": [{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "cleanup"}},
           {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings"}}]}`,
		"typed list": `{"apiVersion": "v1", "kind": "PodList",
 "metadata": {"name": "bundle", "resourceVersion": "7"NOTE},
 "fieldOfALaterVersion": 1e1000,
 "items": [{"metadata": {"name": "cleanup"}}, {"metadata": {"name": "settings"}}]}`,
		"list of an unregistered kind": `{"apiVersion": "example.com/v1", "kind": "ThingList",
 "metadata": {"name": "bundle", "resourceVersion": "7"NOTE},
 "fieldOfALaterVersion": 1e1000,
 "items": [{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "cleanup"}},
           {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(raw, "NOTE") {
				t.Fatal("test premise: the document has no NOTE placeholder")
			}
			objs, err := decodeManifestDocuments([]byte(strings.Replace(raw, "NOTE", "", 1)))
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

			for field, note := range map[string]string{
				"annotations example.com/note": `, "annotations": {"example.com/note": "x"}`,
				"labels app":                   `, "labels": {"app": "x"}`,
			} {
				_, err := decodeManifestDocuments([]byte(strings.Replace(raw, "NOTE", note, 1)))
				assertErrorMentions(t, err, "has metadata of its own that its items cannot keep: "+field)
			}
		})
	}
}

// TestDecodeManifestDocuments_UnregisteredListItemsAreDocuments: an item of a
// list of a kind the scheme does not register is a document of its own to the
// parser, so one of a registered kind is its Go type and is held to the
// undeclared-fields rule as a document of the stream is: a workload that sets a
// field its type does not declare is refused, the error naming the item's
// position, and an object of another registered kind comes back unstructured,
// as written, the field kept. The parser alone returns both as their Go types,
// the field dropped unseen.
func TestDecodeManifestDocuments_UnregisteredListItemsAreDocuments(t *testing.T) {
	const head = "apiVersion: example.com/v1\nkind: WidgetList\nitems:\n"

	t.Run("two items of a registered kind", func(t *testing.T) {
		objs, err := decodeManifestDocuments([]byte(head +
			"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: a\n" +
			"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: b\n"))
		if err != nil {
			t.Fatalf("decodeManifestDocuments: %v", err)
		}
		if names := resourceNames(objs); !slices.Equal(names, []string{"a", "b"}) {
			t.Fatalf("decoded %v, want [a b]", names)
		}
		for _, obj := range objs {
			if _, ok := obj.(*corev1.ConfigMap); !ok {
				t.Errorf("%s is %T, want *corev1.ConfigMap", obj.GetName(), obj)
			}
		}
		assertDeepCopyable(t, objs)
	})

	t.Run("a workload item with an undeclared pod spec field is refused", func(t *testing.T) {
		const deployment = "- apiVersion: apps/v1\n  kind: Deployment\n  metadata:\n    name: web\n  spec:\n    template:\n      spec:\n        FIELD\n"
		doc := head + deployment
		// The premise: the parser alone accepts the item and drops the field.
		parsed, err := kureio.ParseYAMLWithOptions([]byte(strings.Replace(doc, "FIELD", "fieldOfALaterVersion: x", 1)), manifestParseOptions)
		if err != nil || len(parsed) != 1 {
			t.Fatalf("test premise: the parser returned %d objects, %v; want the one Deployment", len(parsed), err)
		}
		if _, typed := parsed[0].(*appsv1.Deployment); !typed {
			t.Fatalf("test premise: the parser returned a %T, want *appsv1.Deployment", parsed[0])
		}

		_, err = decodeManifestDocuments([]byte(strings.Replace(doc, "FIELD", "fieldOfALaterVersion: x", 1)))
		assertErrorMentions(t, err, "item 0 of WidgetList", `Deployment "web"`,
			"undeclared field spec.template.spec.fieldOfALaterVersion:", "cannot be checked against environment policy")

		objs, err := decodeManifestDocuments([]byte(strings.Replace(doc, "FIELD", "hostNetwork: false", 1)))
		if err != nil {
			t.Fatalf("control: with a declared field in its place: %v", err)
		}
		if _, ok := objs[0].(*appsv1.Deployment); !ok || len(objs) != 1 {
			t.Errorf("control decoded %v, the first a %T; want the one *appsv1.Deployment", resourceNames(objs), objs[0])
		}

		// Inside a `v1` List inside the list, the item is named by both positions.
		nested := head + "- apiVersion: v1\n  kind: List\n  items:\n" + nestedItems(deployment)
		_, err = decodeManifestDocuments([]byte(strings.Replace(nested, "FIELD", "fieldOfALaterVersion: x", 1)))
		assertErrorMentions(t, err, "item 0 of WidgetList: item 0 of List", `Deployment "web"`, "undeclared field spec.template.spec.fieldOfALaterVersion:")
	})

	t.Run("an item of another registered kind keeps its undeclared field", func(t *testing.T) {
		objs, err := decodeManifestDocuments([]byte(head +
			"- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: settings\n  fieldOfALaterVersion: kept\n"))
		if err != nil {
			t.Fatalf("decodeManifestDocuments: %v", err)
		}
		if len(objs) != 1 {
			t.Fatalf("decoded %d objects, want 1", len(objs))
		}
		u, ok := objs[0].(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("settings is %T, want *unstructured.Unstructured: the item as written", objs[0])
		}
		if written := writtenYAML(t, u); !strings.Contains(written, "fieldOfALaterVersion: kept\n") {
			t.Errorf("written item lacks the field:\n%s", written)
		}
	})

	t.Run("an item is read with what its list gives it", func(t *testing.T) {
		// The list's apiVersion and its kind without the List, where the item
		// leaves one out. A kept item is written with both.
		objs, err := decodeManifestDocuments([]byte("apiVersion: v1\nkind: FooList\nitems:\n" +
			"- kind: ConfigMap\n  metadata:\n    name: settings\n  fieldOfALaterVersion: kept\n" +
			"- metadata:\n    name: foo\n" +
			"- kind: ConfigMapList\n  items:\n  - metadata:\n      name: inner\n"))
		if err != nil {
			t.Fatalf("decodeManifestDocuments: %v", err)
		}
		if names := resourceNames(objs); !slices.Equal(names, []string{"settings", "foo", "inner"}) {
			t.Fatalf("decoded %v, want [settings foo inner]", names)
		}
		kept, ok := objs[0].(*unstructured.Unstructured)
		if !ok || kept.GetAPIVersion() != "v1" || kept.GetKind() != "ConfigMap" {
			t.Errorf("settings is %T %s %s, want the item as written, unstructured, a v1 ConfigMap", objs[0], objs[0].GetObjectKind().GroupVersionKind().GroupVersion(), objs[0].GetObjectKind().GroupVersionKind().Kind)
		} else if written := writtenYAML(t, kept); !strings.Contains(written, "fieldOfALaterVersion: kept\n") {
			t.Errorf("written item lacks the field:\n%s", written)
		}
		if gvk := objs[1].GetObjectKind().GroupVersionKind(); gvk.Kind != "Foo" || gvk.Version != "v1" {
			t.Errorf("foo is a %s, want a v1 Foo", gvk)
		}
		if _, ok := objs[2].(*corev1.ConfigMap); !ok {
			t.Errorf("inner is %T, want *corev1.ConfigMap: the item of a typed list its list's apiVersion made one", objs[2])
		}

		// A list of lists of an unregistered kind: the inner list has its kind
		// from the outer one.
		objs, err = decodeManifestDocuments([]byte("apiVersion: example.com/v1\nkind: WidgetListList\nitems:\n" +
			"- items:\n  - apiVersion: apps/v1\n    kind: Deployment\n    metadata:\n      name: web\n  - metadata:\n      name: widget\n"))
		if err != nil {
			t.Fatalf("a list of lists: %v", err)
		}
		if names := resourceNames(objs); !slices.Equal(names, []string{"web", "widget"}) {
			t.Fatalf("a list of lists decoded %v, want [web widget]", names)
		}
		if _, ok := objs[0].(*appsv1.Deployment); !ok {
			t.Errorf("web is %T, want *appsv1.Deployment", objs[0])
		}
		if kind := objs[1].GetObjectKind().GroupVersionKind().Kind; kind != "Widget" {
			t.Errorf("widget has kind %q, want Widget", kind)
		}
	})

	t.Run("an item the strict decode reads as another kind is refused", func(t *testing.T) {
		// The parser reads the last statement of apiVersion, a null, as left
		// out, and gives the item its list's. The Kubernetes decoder keeps the
		// string a null follows. Checked as that one, the item is of no
		// registered kind and its fields pass unread, while the object that is
		// emitted is the Go type, the field dropped.
		const list = `{"apiVersion":"APIVERSION","kind":"FooList","items":[{"apiVersion":"example.com/v1"NULL,` +
			`"kind":"KIND","metadata":{"name":"web"},FIELD}]}`
		doc := func(apiVersion, null, kind, field string) []byte {
			return []byte(strings.NewReplacer("APIVERSION", apiVersion, "NULL", null, "KIND", kind, "FIELD", field).Replace(list))
		}
		const twice = `,"apiVersion":null`

		for name, tc := range map[string]struct{ apiVersion, kind, field string }{
			"a workload with an undeclared field":    {"apps/v1", "Deployment", `"spec":{"template":{"spec":{"fieldOfALaterVersion":"x"}}}`},
			"another kind with an undeclared field":  {"v1", "ConfigMap", `"fieldOfALaterVersion":"kept"`},
			"a workload that sets no undeclared one": {"apps/v1", "Deployment", `"spec":{"replicas":1}`},
		} {
			t.Run(name, func(t *testing.T) {
				input := doc(tc.apiVersion, twice, tc.kind, tc.field)
				// The premise: the parser alone returns the Go type.
				parsed, err := kureio.ParseYAMLWithOptions(input, manifestParseOptions)
				if err != nil || len(parsed) != 1 {
					t.Fatalf("test premise: the parser returned %d objects, %v; want one", len(parsed), err)
				}
				if _, untyped := parsed[0].(*unstructured.Unstructured); untyped {
					t.Fatalf("test premise: the parser returned an unstructured object, want the Go type of %s", tc.kind)
				}

				_, err = decodeManifestDocuments(input)
				assertErrorMentions(t, err, "item 0 of FooList", tc.kind+` "web"`,
					"the object was read as "+tc.apiVersion+" "+tc.kind,
					"the decode that checks its fields reads the document as example.com/v1 "+tc.kind,
					"so its fields cannot be checked", "state each once")
			})
		}

		// Control: an item that states one apiVersion, of no registered kind, is
		// one unstructured object, and nothing is refused.
		objs, err := decodeManifestDocuments(doc("apps/v1", "", "Deployment", `"spec":{"template":{"spec":{"fieldOfALaterVersion":"x"}}}`))
		if err != nil {
			t.Fatalf("control: %v", err)
		}
		if _, untyped := objs[0].(*unstructured.Unstructured); !untyped || len(objs) != 1 {
			t.Errorf("control decoded %d objects, the first a %T; want the one unstructured example.com/v1 Deployment", len(objs), objs[0])
		}
	})
}

// nestedItems indents the items of a list two columns, to nest them in an item
// of another list.
func nestedItems(items string) string {
	var b strings.Builder
	for line := range strings.Lines(items) {
		b.WriteString("  " + line)
	}
	return b.String()
}

// TestDecodeManifestDocuments_ItemsOnAKindThatIsNoListIsRefused: an object of a
// kind the scheme does not register and that does not end in List is one object
// to the parser, whatever it holds. With a top-level `items` array it is a list
// to what applies the output, which would apply entries no check has read, so
// it is refused, at the top of the stream and as an item of a list, the error
// naming the kind and what to write instead. An `items` that is no array, one
// below the top level and one under a key of another case are the object's own
// content and are kept. A kind ending in List that states no `items` is one
// object too.
func TestDecodeManifestDocuments_ItemsOnAKindThatIsNoListIsRefused(t *testing.T) {
	const head = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n"
	wants := []string{`Widget "w"`, "an `items` array on an object of a kind that is no list (example.com/v1 Widget)",
		"is read as a list by what applies the output", "write the entries as documents of their own, or give the object a kind ending in List"}
	for name, items := range map[string]string{
		"objects": "items:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: a\n    namespace: elsewhere\n",
		"scalars": "items: [blue, green]\n",
		"empty":   "items: []\n",
	} {
		for _, decode := range []func([]byte) ([]client.Object, error){decodeManifestDocuments, decodeChartManifests} {
			_, err := decode([]byte(head + items))
			assertErrorMentions(t, err, wants...)
			if err != nil && strings.Contains(err.Error(), "ConfigMap \"a\"") {
				t.Errorf("items of %s: the refusal names an entry, which is no object of the stream: %v", name, err)
			}
		}
		// The parser alone returns the one object: the refusal is launcher's.
		parsed, err := kureio.ParseYAMLWithOptions([]byte(head+items), manifestParseOptions)
		if err != nil || len(parsed) != 1 {
			t.Errorf("items of %s: test premise: the parser returned %d objects, %v; want the one Widget", name, len(parsed), err)
		}
	}

	for list, listHead := range map[string]string{
		"List":       "apiVersion: v1\nkind: List\nitems:\n",
		"WidgetList": "apiVersion: example.com/v1\nkind: WidgetList\nitems:\n",
	} {
		_, err := decodeManifestDocuments([]byte(listHead + "- " + strings.ReplaceAll(head+"items: [blue]\n", "\n", "\n  ")))
		assertErrorMentions(t, err, append([]string{"item 0 of " + list}, wants...)...)
	}

	for name, doc := range map[string]string{
		"items is an object":              head + "items:\n  blue: 1\n",
		"items is null":                   head + "items: null\n",
		"items below the top level":       head + "spec:\n  items: [blue, green]\n",
		"items under a key of other case": head + "Items: [blue, green]\n",
		"a kind ending in List, no items": "apiVersion: example.com/v1\nkind: WidgetList\nmetadata:\n  name: w\n",
	} {
		objs, err := decodeManifestDocuments([]byte(doc))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(objs) != 1 || objs[0].GetName() != "w" {
			t.Errorf("%s: decoded %v, want the one object", name, resourceNames(objs))
		}
	}
}

// TestEnforceRenderedObjectPolicy_ObjectWithItemsIsUnreadable: the decode of a
// chart's render and of a manifests source returns no object with a top-level
// `items` array, so the policy check's arm for one holds only an object that
// reaches it another way. Such an object is refused as unreadable, in words that
// say what it is to what applies it. The text no longer places it inside a list
// of an unregistered kind, where the decode once left it.
func TestEnforceRenderedObjectPolicy_ObjectWithItemsIsUnreadable(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "w"},
		"items":    []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "inner"}}},
	}}
	err := enforceRenderedObjectPolicy(u, &maxReplicasPolicy{max: 3})
	assertErrorMentions(t, err, "the object has a top-level items list, so what applies it reads it as a list, whose objects cannot be checked against environment policy")
	var refusal *oam.PolicyRefusal
	if !errors.As(err, &refusal) || refusal.Class != oam.RefusalUnreadableObject {
		t.Errorf("error = %v, want a refusal of class %q", err, oam.RefusalUnreadableObject)
	}
	if err != nil && strings.Contains(err.Error(), "unregistered kind") {
		t.Errorf("the refusal still places the object inside a list of an unregistered kind: %v", err)
	}

	delete(u.Object, "items")
	if err := enforceRenderedObjectPolicy(u, &maxReplicasPolicy{max: 3}); err != nil {
		t.Errorf("control: the same object without items: %v", err)
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

// TestListDocumentOf_IsTheParsersDetection holds listDocumentOf to kure's
// parser, whose list detection it repeats: over documents of each shape, it
// holds a document for a list exactly when the parser replaces the document by
// its items, here two ConfigMaps as their Go type. A document the parser
// refuses is never settled, so only those it accepts are compared; the test
// fails if it accepts too few of them to compare anything. A document that
// states apiVersion, kind or items under a key of another case, which the two
// readers of a document's type once read differently, is one the parser
// refuses.
func TestListDocumentOf_IsTheParsersDetection(t *testing.T) {
	const items = `"items": [{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "a"}},
                 {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "b"}}]`
	cases := map[string]struct {
		doc      string
		wantList bool
		// unregistered is the shape a list of an unregistered kind is read as.
		unregistered bool
	}{
		"v1 List":                               {doc: `{"apiVersion": "v1", "kind": "List", ` + items + `}`, wantList: true},
		"typed list":                            {doc: `{"apiVersion": "v1", "kind": "ConfigMapList", ` + items + `}`, wantList: true},
		"items stated twice":                    {doc: `{"apiVersion": "v1", "kind": "List", "items": [], ` + items + `}`, wantList: true},
		"a list of an unregistered kind":        {doc: `{"apiVersion": "example.com/v1", "kind": "ThingList", ` + items + `}`, wantList: true, unregistered: true},
		"an unregistered version of a list":     {doc: `{"apiVersion": "v2", "kind": "ConfigMapList", ` + items + `}`, wantList: true, unregistered: true},
		"an unregistered kind that is no list":  {doc: `{"apiVersion": "example.com/v1", "kind": "Thing", ` + items + `}`},
		"an unregistered list kind, no items":   {doc: `{"apiVersion": "example.com/v1", "kind": "ThingList", "metadata": {"name": "a"}}`},
		"a single object":                       {doc: `{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "a"}}`},
		"an unregistered single object":         {doc: `{"apiVersion": "example.com/v1", "kind": "Thing", "metadata": {"name": "a"}}`},
		"a registered kind whose name ends so":  {doc: `{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "ThingList"}}`},
		"an unregistered kind ending in a List": {doc: `{"apiVersion": "example.com/v1", "kind": "Playlist", ` + items + `}`},
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
		list, isList := listDocumentOf([]byte(tc.doc), listIdentity{})
		if isList != tc.wantList {
			t.Errorf("%s: listDocumentOf holds it for a list = %v, the parser = %v", name, isList, tc.wantList)
			continue
		}
		if isList && len(list.items) != len(objs) {
			t.Errorf("%s: read %d items, the parser returned %d objects", name, len(list.items), len(objs))
		}
		if isList && list.unregistered != tc.unregistered {
			t.Errorf("%s: read as a list of an unregistered kind = %v, want %v", name, list.unregistered, tc.unregistered)
		}
	}
	if compared < len(cases) {
		t.Errorf("compared %d of %d documents", compared, len(cases))
	}

	// A key of another case is the parser's refusal, so no reading of such a
	// document is ever settled against another.
	for name, doc := range map[string]string{
		"Kind beside kind":             `{"apiVersion": "v1", "kind": "List", "Kind": "ThingList", ` + items + `}`,
		"apiversion beside apiVersion": `{"apiVersion": "v1", "apiversion": "example.com/v1", "kind": "List", ` + items + `}`,
		"Items on a registered list":   `{"apiVersion": "v1", "kind": "List", "Items": []}`,
		"Items on an unregistered one": `{"apiVersion": "example.com/v1", "kind": "ThingList", "Items": []}`,
	} {
		_, err := kureio.ParseYAMLWithOptions([]byte(doc), manifestParseOptions)
		assertErrorMentions(t, err, "only after case folding")
		if _, err := decodeManifestDocuments([]byte(doc)); err == nil {
			t.Errorf("%s: decodeManifestDocuments accepts a document the parser refuses", name)
		}
	}

	// Every list kind the scheme registers is recognised, and no other kind of
	// the scheme is; the generic list is the one whose items are documents of
	// any kind.
	lists, generic := 0, 0
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		made, err := kubernetes.Scheme.New(gvk)
		if err != nil {
			t.Fatalf("Scheme.New(%s): %v", gvk, err)
		}
		_, isObject := made.(client.Object)
		want := !isObject && meta.IsListType(made)
		doc := fmt.Sprintf(`{"apiVersion": %q, "kind": %q, "items": []}`, gvk.GroupVersion().String(), gvk.Kind)
		list, got := listDocumentOf([]byte(doc), listIdentity{})
		if got != want {
			t.Errorf("%s: listDocumentOf = %v, want %v", gvk, got, want)
		}
		if got {
			lists++
			if list.unregistered {
				t.Errorf("%s is read as a list of an unregistered kind", gvk)
			}
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
// document that is no list decoded to more than one object, some object's
// fields would go unread. Each is refused.
func TestKeepDocumentFields_RefusesAReadingThatIsNotTheParsers(t *testing.T) {
	// The objects here are made by hand, so no parse has registered the scheme
	// the list detection reads.
	if err := kubernetes.RegisterSchemes(); err != nil {
		t.Fatalf("RegisterSchemes: %v", err)
	}
	pods := []client.Object{&corev1.Pod{}, &corev1.Pod{}}
	one := []byte(`{"apiVersion": "v1", "kind": "PodList", "items": [{"metadata": {"name": "a"}}]}`)
	_, err := keepDocumentFields(one, pods)
	assertErrorMentions(t, err, "the items of PodList were read as 1 objects and decoded to 2", "defect of this build")

	generic := []byte(`{"apiVersion": "v1", "kind": "List", "items": [{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "a"}}]}`)
	_, err = keepDocumentFields(generic, pods)
	assertErrorMentions(t, err, "the items of List were read as 1 objects and decoded to 2", "defect of this build")

	single := []byte(`{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "a"}}`)
	_, err = keepDocumentFields(single, pods)
	assertErrorMentions(t, err, "a document that is no list decoded to 2 objects", "defect of this build")

	// An unregistered kind that is no list to the parser, whatever it holds.
	untyped := []client.Object{&unstructured.Unstructured{}, &unstructured.Unstructured{}}
	thing := []byte(`{"apiVersion": "example.com/v1", "kind": "Thing", "items": [{}, {}]}`)
	_, err = keepDocumentFields(thing, untyped)
	assertErrorMentions(t, err, "a document that is no list decoded to 2 objects", "defect of this build")

	oneThing := []byte(`{"apiVersion": "example.com/v1", "kind": "ThingList", "items": [{}]}`)
	_, err = keepDocumentFields(oneThing, untyped)
	assertErrorMentions(t, err, "the items of ThingList were read as 1 objects and decoded to 2", "defect of this build")

	thingList := []byte(`{"apiVersion": "example.com/v1", "kind": "ThingList", "items": [{}, {}]}`)
	if objs, err := keepDocumentFields(thingList, untyped); err != nil || len(objs) != 2 {
		t.Errorf("control: the untyped items of an unregistered list: %d objects, %v; want them back", len(objs), err)
	}
}
