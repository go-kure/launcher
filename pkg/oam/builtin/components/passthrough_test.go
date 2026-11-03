package components_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func passthroughComponent(props map[string]any) *oam.Component {
	return &oam.Component{Name: "my-res", Type: "passthrough", Properties: props}
}

// generate runs ToApplicationConfig + Generate and returns the single emitted object.
func generatePassthrough(t *testing.T, props map[string]any, namespace string) *unstructured.Unstructured {
	t.Helper()
	h := &components.PassthroughHandler{}
	cfg, err := h.ToApplicationConfig(passthroughComponent(props), namespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	u, ok := (*objs[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("expected *unstructured.Unstructured, got %T", *objs[0])
	}
	return u
}

func TestPassthroughHandler_CanHandle(t *testing.T) {
	h := &components.PassthroughHandler{}
	if !h.CanHandle("passthrough") {
		t.Error("expected true for passthrough")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

func TestPassthroughHandler_NamespacedSpecObject(t *testing.T) {
	u := generatePassthrough(t, map[string]any{
		"object": map[string]any{
			"apiVersion": "sparkoperator.k8s.io/v1beta2",
			"kind":       "SparkApplication",
			"spec":       map[string]any{"mode": "cluster"},
		},
	}, "data")

	if u.GetAPIVersion() != "sparkoperator.k8s.io/v1beta2" {
		t.Errorf("apiVersion = %q", u.GetAPIVersion())
	}
	if u.GetKind() != "SparkApplication" {
		t.Errorf("kind = %q", u.GetKind())
	}
	if u.GetName() != "my-res" {
		t.Errorf("name = %q, want defaulted to component name", u.GetName())
	}
	if u.GetNamespace() != "data" {
		t.Errorf("namespace = %q, want build namespace", u.GetNamespace())
	}
	spec, _ := u.Object["spec"].(map[string]any)
	if spec["mode"] != "cluster" {
		t.Errorf("spec passthrough lost: %#v", u.Object["spec"])
	}
}

func TestPassthroughHandler_PreservesUserMetadata(t *testing.T) {
	u := generatePassthrough(t, map[string]any{
		"object": map[string]any{
			"apiVersion": "example.com/v1",
			"kind":       "Widget",
			"metadata": map[string]any{
				"name":   "explicit-name",
				"labels": map[string]any{"team": "data"},
			},
			"spec": map[string]any{},
		},
	}, "ns1")

	if u.GetName() != "explicit-name" {
		t.Errorf("name = %q, want user-set name preserved", u.GetName())
	}
	if u.GetLabels()["team"] != "data" {
		t.Errorf("labels lost: %#v", u.GetLabels())
	}
	if u.GetNamespace() != "ns1" {
		t.Errorf("namespace = %q, want build namespace", u.GetNamespace())
	}
}

func TestPassthroughHandler_NonSpecObject(t *testing.T) {
	u := generatePassthrough(t, map[string]any{
		"object": map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"data":       map[string]any{"key": "value"},
		},
	}, "ns1")

	data, _ := u.Object["data"].(map[string]any)
	if data["key"] != "value" {
		t.Errorf("data passthrough lost: %#v", u.Object["data"])
	}
	if u.GetName() != "my-res" || u.GetNamespace() != "ns1" {
		t.Errorf("metadata fixup wrong: name=%q ns=%q", u.GetName(), u.GetNamespace())
	}
}

func TestPassthroughHandler_ClusterScoped(t *testing.T) {
	u := generatePassthrough(t, map[string]any{
		"clusterScoped": true,
		"object": map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"rules":      []any{map[string]any{"verbs": []any{"get"}}},
		},
	}, "data")

	if u.GetNamespace() != "" {
		t.Errorf("cluster-scoped object must not get a namespace, got %q", u.GetNamespace())
	}
	if u.GetName() != "my-res" {
		t.Errorf("name = %q", u.GetName())
	}
	if _, ok := u.Object["rules"].([]any); !ok {
		t.Errorf("rules passthrough lost: %#v", u.Object["rules"])
	}
}

func TestPassthroughHandler_Errors(t *testing.T) {
	h := &components.PassthroughHandler{}
	cases := map[string]map[string]any{
		"missing object":       {},
		"object not a map":     {"object": "nope"},
		"missing apiVersion":   {"object": map[string]any{"kind": "Widget"}},
		"missing kind":         {"object": map[string]any{"apiVersion": "example.com/v1"}},
		"empty apiVersion":     {"object": map[string]any{"apiVersion": "", "kind": "Widget"}},
		"unknown top key":      {"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap"}, "extra": 1},
		"clusterScoped string": {"clusterScoped": "yes", "object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap"}},
		"metadata not a map":   {"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": "nope"}},
		"clusterScoped with namespace": {
			"clusterScoped": true,
			"object": map[string]any{
				"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole",
				"metadata": map[string]any{"namespace": "x"},
			},
		},
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.ToApplicationConfig(passthroughComponent(props), "ns1"); err == nil {
				t.Errorf("expected error for %q", name)
			}
		})
	}
}

// TestPassthrough_ListShapedObjectIsRejected pins BOTH directions of the list
// predicate, because a wrong predicate passes a one-directional suite.
//
// Generate emits the authored map as a single unstructured and stamps a name and a
// namespace onto it, so a list arrives downstream as one NAMED envelope whose items
// never see per-object label mutation, namespace stamping or ownership checks — while
// Flux unwraps it at apply time into N objects that do reach the cluster.
//
// The rejection is keyed on apimachinery's Unstructured.IsList ("items is present AND
// is a []interface{}"), never on the kind name. The accept cases below are what
// distinguishes that predicate from `strings.HasSuffix(kind, "List")` or from a bare
// `items` presence check: without them, the wrong predicate also goes green.
func TestPassthrough_ListShapedObjectIsRejected(t *testing.T) {
	h := &components.PassthroughHandler{}

	item := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "a"},
	}

	rejected := map[string]map[string]any{
		"core v1 List": {
			"apiVersion": "v1", "kind": "List",
			"items": []any{item},
		},
		// A typed list carries items and would slip past `kind == "List"`.
		"typed ConfigMapList": {
			"apiVersion": "v1", "kind": "ConfigMapList",
			"items": []any{item},
		},
		// Empty is still list-shaped: it unwraps to zero objects rather than to the
		// one object the contract promises.
		"empty items": {
			"apiVersion": "v1", "kind": "List",
			"items": []any{},
		},
		// A kind that does NOT end in "List" but carries a top-level items ARRAY.
		// Deliberately stricter than Kustomize, whose inlineAnyEmbeddedLists keeps
		// this as one ordinary resource because it checks the kind suffix first. The
		// stage that decides what reaches the cluster is Flux's, not Kustomize's:
		// kustomize-controller decodes the build output with fluxcd/pkg/ssa's
		// utils.ReadObjects, which expands any object for which Unstructured.IsList
		// holds, kind unchecked. So the Widget below is never applied and the
		// ConfigMap is, in whatever namespace it names — the smuggle this arm exists
		// to stop. Narrowing this arm to the kind suffix reopens it.
		"non-List kind with a top-level items array of objects": {
			"apiVersion": "example.com/v1", "kind": "Widget",
			"items": []any{item},
		},
		// The same shape with scalar members is still refused: Flux's ReadObjects
		// fails the whole apply on it ("items member is not an object").
		"non-List kind with a top-level items array of scalars": {
			"apiVersion": "example.com/v1", "kind": "Widget",
			"items": []any{"a", "b"},
		},
	}
	for name, object := range rejected {
		t.Run("rejected/"+name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": object}), "ns1")
			if err == nil {
				t.Fatal("a list-shaped object was accepted; its items would bypass every per-object rule downstream")
			}
			if !strings.Contains(err.Error(), "is a list") {
				t.Errorf("error %q does not identify the failure as a list; a caller cannot tell it from the apiVersion/kind checks", err)
			}
			if kind, _ := object["kind"].(string); !strings.Contains(err.Error(), kind) {
				t.Errorf("error %q does not name the offending kind %q", err, kind)
			}
		})
	}

	accepted := map[string]map[string]any{
		// THE CASE THAT MATTERS. A kind ending in "List" with no items is not a list;
		// a suffix-based predicate would reject it and this test is what says so.
		"CRD kind ending in List, no items": {
			"apiVersion": "example.com/v1", "kind": "ShoppingList",
			"spec": map[string]any{"entries": []any{"milk"}},
		},
		// `items` present but not a sequence. IsList is items-IS-A-SLICE, not
		// items-IS-PRESENT, and a presence check would reject this.
		"items is an object, not a sequence": {
			"apiVersion": "example.com/v1", "kind": "Widget",
			"items": map[string]any{"count": 3},
		},
		// A nested items array is not the object's own shape.
		"items nested under spec": {
			"apiVersion": "example.com/v1", "kind": "Widget",
			"spec": map[string]any{"items": []any{item}},
		},
		// A top-level `items` set to null on a kind that does NOT end in "List".
		// Kustomize's own list expansion (resource/factory.go,
		// inlineAnyEmbeddedLists) checks strings.HasSuffix(kind, "List") before it
		// ever looks at `items`, so this is one ordinary resource to Kustomize —
		// rejecting it here would be stricter than the tool this check exists to
		// match. A CRD whose spec happens to define a top-level `items` field is
		// the concrete collision this guards against.
		"items authored as null, kind does not end in List": {
			"apiVersion": "example.com/v1", "kind": "Widget",
			"items": nil,
		},
	}
	for name, object := range accepted {
		t.Run("accepted/"+name, func(t *testing.T) {
			if _, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": object}), "ns1"); err != nil {
				t.Fatalf("a non-list object was rejected: %v", err)
			}
		})
	}

	// The null arm is separated from the table above because its diagnostic is a
	// different one on purpose — "expands to zero objects", not "is a list" — and
	// folding it into the loop would either weaken that loop's message assertion or
	// assert the wrong sentence here. IsList cannot reach these: it requires
	// exactly []interface{}, and neither an authored null nor a typed nil map is
	// one. `[]any(nil)` is deliberately NOT in this table — it satisfies the
	// .([]interface{}) assertion with ok=true and is already caught by IsList
	// above, so listing it here would look like coverage while discriminating
	// nothing. Every case below uses kind "List", the one case where the arm's
	// kind-suffix guard does not itself change the outcome — the guard's own
	// discriminating case ("items authored as null, kind does not end in List")
	// lives in the accepted table above, not here.
	nullItems := map[string]any{
		"untyped nil, as authored YAML `items:` writes it": nil,
		"typed nil map": map[string]any(nil),
	}
	for name, itemsValue := range nullItems {
		t.Run("rejected/items authored as null/"+name, func(t *testing.T) {
			object := map[string]any{"apiVersion": "v1", "kind": "List", "items": itemsValue}
			_, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": object}), "ns1")
			if err == nil {
				t.Fatal("an empty list envelope was accepted; Kustomize expands it to zero objects with no error, and under pruning that also removes whatever the previous inventory held")
			}
			if !strings.Contains(err.Error(), "zero objects") {
				t.Errorf("error %q does not say what actually happens; a caller cannot tell this from the apiVersion/kind checks", err)
			}
			if !strings.Contains(err.Error(), "List") {
				t.Errorf("error %q does not name the offending kind", err)
			}
		})
	}
}

// TestPassthrough_ValidatedObjectIsFrozen pins that the checks in
// ToApplicationConfig are not merely advisory. The handler used to retain the
// caller's map, and Generate copies its contents at CALL time — so a programmatic
// caller could hand in a validated single object, add an `items` array afterwards,
// and get exactly the list envelope ToApplicationConfig had just refused. No
// in-repo production caller does this (every LowerComponent implementation in this
// module is in a _test.go), which is why it is reachable through the public Go API
// rather than through authored YAML; that makes it a contract this test has to
// hold, not a scenario the current call graph rules out.
func TestPassthrough_ValidatedObjectIsFrozen(t *testing.T) {
	h := &components.PassthroughHandler{}

	object := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}}
	cfg, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": object}), "ns1")
	if err != nil {
		t.Fatalf("a plain ConfigMap was rejected: %v", err)
	}

	// The mutation the frozen copy exists to defeat.
	object["kind"] = "List"
	object["items"] = []any{
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "b"}},
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c"}},
	}

	out, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Generate emitted %d objects, want 1", len(out))
	}
	u, ok := (*out[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Generate emitted %T, want *unstructured.Unstructured", *out[0])
	}
	if got := u.GetKind(); got != "ConfigMap" {
		t.Errorf("emitted kind = %q, want ConfigMap — the post-validation mutation reached the output", got)
	}
	if _, present := u.Object["items"]; present {
		t.Error("emitted object carries 'items'; the validated bytes and the emitted bytes are not the same bytes")
	}
}

func TestPassthrough_TransformWithPolicy(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"passthrough": &components.PassthroughHandler{},
	}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "app", Namespace: "ns1"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name: "my-cr",
				Type: "passthrough",
				Properties: map[string]any{
					"object": map[string]any{
						"apiVersion": "example.com/v1",
						"kind":       "Widget",
						"spec":       map[string]any{"size": int64(3)},
					},
				},
			}},
		},
	}

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "ns1"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}

	found := false
	walkBundles(cluster.Node, func(b *stack.Bundle) {
		for _, a := range b.Applications {
			objs, err := a.Generate()
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, o := range objs {
				u, ok := (*o).(*unstructured.Unstructured)
				if !ok || u.GetKind() != "Widget" {
					continue
				}
				found = true
				if u.GetName() != "my-cr" || u.GetNamespace() != "ns1" {
					t.Errorf("Widget metadata: name=%q ns=%q", u.GetName(), u.GetNamespace())
				}
			}
		}
	})
	if !found {
		t.Error("passthrough Widget was not emitted by the transform")
	}
}

func walkBundles(n *stack.Node, fn func(*stack.Bundle)) {
	if n == nil {
		return
	}
	var visit func(b *stack.Bundle)
	visit = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		fn(b)
		for _, ch := range b.Children {
			visit(ch)
		}
	}
	visit(n.Bundle)
	for _, ch := range n.Children {
		walkBundles(ch, fn)
	}
}

func TestPassthroughHandler_RespectsInlineNamespace(t *testing.T) {
	// clusterScoped:false respects a user-supplied namespace (intentional
	// cross-namespace escape hatch); the build namespace is only a fallback.
	u := generatePassthrough(t, map[string]any{
		"object": map[string]any{
			"apiVersion": "example.com/v1",
			"kind":       "Widget",
			"metadata":   map[string]any{"namespace": "other"},
		},
	}, "ns1")
	if u.GetNamespace() != "other" {
		t.Errorf("inline namespace not respected: got %q, want \"other\"", u.GetNamespace())
	}
}

func TestPassthroughHandler_DeepCopyIsolatesSource(t *testing.T) {
	srcSpec := map[string]any{"replicas": int64(1)}
	srcLabels := map[string]any{"team": "data"}
	object := map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"labels": srcLabels},
		"spec":       srcSpec,
	}

	u := generatePassthrough(t, map[string]any{"object": object}, "ns1")

	// Simulate downstream in-place edits to nested maps of the emitted object.
	u.Object["spec"].(map[string]any)["replicas"] = int64(99)
	u.Object["metadata"].(map[string]any)["labels"].(map[string]any)["team"] = "ops"

	if srcSpec["replicas"] != int64(1) {
		t.Errorf("source spec mutated through emitted object: %#v", srcSpec)
	}
	if srcLabels["team"] != "data" {
		t.Errorf("source labels mutated through emitted object: %#v", srcLabels)
	}
}

// phase and tags are named types a Go-assembled body can hold.
type (
	phase string
	tags  map[string]int
)

// TestPassthrough_EmittedObjectIsJSONShaped: the emitted object holds only the types an
// object decoded from JSON holds (map[string]any, []any, string, bool, int64, float64),
// whatever the authored map carries: the YAML decoder produces int, a Go caller can add
// any integer or float type, named scalars and typed maps, slices and arrays. A caller
// that deep-copies the emitted object, through DeepCopyObject, panics on any other
// (go-kure/launcher#1162). The document it serializes to is the authored one.
func TestPassthrough_EmittedObjectIsJSONShaped(t *testing.T) {
	bigUnsigned := uint64(9223372036854775809)
	port := map[string]any{
		"port":     8080,
		"weight":   float32(0.5),
		"small":    int8(3),
		"unsigned": uint32(7),
		"huge":     uint64(1 << 63),
		"stamp":    time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC),
		"raw":      json.RawMessage(`{"a":1}`),
		"exact":    int64(9),
		"ratio":    1.5,
		"name":     "http",
		"on":       true,
		"phase":    phase("up"),
		"tags":     tags{"a": 1},
		"counts":   []int32{1, 2},
		"pair":     [2]uint8{3, 4},
		"blob":     []byte("hi"),
		"labels":   map[string]string{"k": "v"},
		"nilTyped": []string(nil),
		// A key encoding/json writes as a string, and numbers it must not round.
		"intKeys":   map[int]string{1: "x"},
		"rawBig":    json.RawMessage("9223372036854775809"),
		"ptrBig":    &bigUnsigned,
		"marshaled": []markedByte{1},
		"viaPtr":    []ptrMarkedByte{1},
		"nestedPtr": [][1]ptrMarkedByte{{1}},
		"fieldPtr":  []struct{ F ptrMarkedByte }{{1}},
		"rawFrac":   json.RawMessage("1.5"),
		"rawExp":    json.RawMessage("1e3"),
		"rawNegBig": json.RawMessage("-9223372036854775809"),
		"f32":       float32(0.1),
	}
	object := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"spec": map[string]any{
			"type":         "ExternalName",
			"externalName": "example.invalid",
			"ports":        []any{port},
		},
	}

	u := generatePassthrough(t, map[string]any{"object": object}, "ns1")

	// The assertion that matters: DeepCopyObject panics ("cannot deep copy int") on a plain int.
	var copied *unstructured.Unstructured
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DeepCopyObject of the emitted object panicked: %v", r)
			}
		}()
		copied = u.DeepCopyObject().(*unstructured.Unstructured)
	}()
	got := copied.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)
	want := map[string]any{
		"port":      int64(8080),
		"weight":    float64(0.5),
		"small":     int64(3),
		"unsigned":  int64(7),
		"huge":      json.Number("9223372036854775808"), // beyond int64: its own digits, as authored
		"stamp":     "2026-11-02T21:00:00Z",
		"raw":       map[string]any{"a": int64(1)},
		"exact":     int64(9),
		"ratio":     1.5,
		"name":      "http",
		"on":        true,
		"phase":     "up",
		"tags":      map[string]any{"a": int64(1)},
		"counts":    []any{int64(1), int64(2)},
		"pair":      []any{int64(3), int64(4)},
		"blob":      "aGk=", // what encoding/json writes for a []byte
		"labels":    map[string]any{"k": "v"},
		"nilTyped":  nil,
		"intKeys":   map[string]any{"1": "x"},
		"rawBig":    json.Number("9223372036854775809"),
		"ptrBig":    json.Number("9223372036854775809"),
		"marshaled": []any{"x"}, // the element's own form, not base64
		"viaPtr":    []any{"y"}, // a marshaler on the pointer, which a slice element has
		"nestedPtr": []any{[]any{"y"}},
		"fieldPtr":  []any{map[string]any{"F": "y"}},
		"rawFrac":   1.5,
		"rawExp":    float64(1000),
		"rawNegBig": json.Number("-9223372036854775809"),
		"f32":       0.1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emitted port = %#v, want %#v", got, want)
	}

	// The authored map is not touched: the normalization is of the emitted copy.
	if port["port"] != 8080 {
		t.Errorf("authored port changed to %#v", port["port"])
	}
}

// markedByte is a byte that serializes itself, so a slice of it is an array and not base64.
type markedByte byte

func (markedByte) MarshalJSON() ([]byte, error) { return []byte(`"x"`), nil }

// ptrMarkedByte is the same with the marshaler on the pointer.
type ptrMarkedByte byte

func (*ptrMarkedByte) MarshalJSON() ([]byte, error) { return []byte(`"y"`), nil }

// TestPassthrough_FrozenCopyDetachesTypedContainers covers the half of the freeze the
// authored path cannot reach. yaml.v3 decoding into `any` only ever produces
// map[string]any and []any, so the two fast paths in deepCopyValue looked complete;
// a Go-assembled body can hold map[string]string, []string, named collection types
// and arrays, and every one of those aliased straight through the old
// `default: return v`. The freeze is the whole basis for calling the list rejection
// binding, so a partial one is not a smaller version of the guarantee — it is the
// guarantee holding for the shapes nobody would attack and failing for the rest.
func TestPassthrough_FrozenCopyDetachesTypedContainers(t *testing.T) {
	h := &components.PassthroughHandler{}

	args := []string{"before"}
	data := map[string]string{"k": "before"}
	nested := []any{[]string{"before"}}

	object := map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "a"},
		"args":       args,
		"data":       data,
		"nested":     nested,
		// Nullness must survive the copy: turning either of these into an empty
		// {} or [] changes the emitted document, and it is the same predicate the
		// null-items arm keys on, so the two must agree on what a null is.
		"nilMap":   map[string]any(nil),
		"nilSlice": []string(nil),
	}

	cfg, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": object}), "ns1")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	// Mutate the caller's containers after validation returned.
	args[0] = "after"
	data["k"] = "after"
	nested[0].([]string)[0] = "after"

	out, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u, ok := (*out[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Generate emitted %T, want *unstructured.Unstructured", *out[0])
	}

	// The frozen object stays as authored, typed containers included; what is emitted from
	// it is JSON-shaped (TestPassthrough_EmittedObjectIsJSONShaped), still detached.
	frozen := cfg.(*components.PassthroughConfig).Object
	if got := frozen["args"].([]string); !reflect.DeepEqual(got, []string{"before"}) {
		t.Errorf("frozen args = %#v, want [before] — the []string aliased the caller's slice", got)
	}
	if got := frozen["data"].(map[string]string)["k"]; got != "before" {
		t.Errorf("frozen data[k] = %q, want before — the map[string]string aliased the caller's map", got)
	}
	if got := frozen["nested"].([]any)[0].([]string); !reflect.DeepEqual(got, []string{"before"}) {
		t.Errorf("frozen nested[0] = %#v, want [before] — a typed slice inside an []any aliased through", got)
	}

	if got := u.Object["args"]; !reflect.DeepEqual(got, []any{"before"}) {
		t.Errorf("emitted args = %#v, want [before] — the []string aliased the caller's slice", got)
	}
	if got := u.Object["data"]; !reflect.DeepEqual(got, map[string]any{"k": "before"}) {
		t.Errorf("emitted data = %#v, want k: before — the map[string]string aliased the caller's map", got)
	}
	if got := u.Object["nested"]; !reflect.DeepEqual(got, []any{[]any{"before"}}) {
		t.Errorf("emitted nested = %#v, want [[before]] — a typed slice inside an []any aliased through", got)
	}

	nilMap, present := u.Object["nilMap"]
	if !present {
		t.Error("emitted object dropped nilMap entirely")
	} else if nilMap != nil && !reflect.ValueOf(nilMap).IsNil() {
		t.Errorf("emitted nilMap = %#v, want null — the copy turned a null into an empty map", nilMap)
	}
	nilSlice, present := u.Object["nilSlice"]
	if !present {
		t.Error("emitted object dropped nilSlice entirely")
	} else if nilSlice != nil && !reflect.ValueOf(nilSlice).IsNil() {
		t.Errorf("emitted nilSlice = %#v, want null — the copy turned a null into an empty slice", nilSlice)
	}
}

// namedAnyMap and namedAnySlice are `any`-valued collections under their own named
// types, so they miss deepCopyValue's map[string]any / []any fast paths and land in
// the reflection arm. A nil element in one of those is the case where reflect has no
// Value to copy, and the two reflect writers fail differently on it: Set panics,
// SetMapIndex reads a zero Value as "delete this key".
type namedAnyMap map[string]any

type namedAnySlice []any

func TestPassthrough_FrozenCopyKeepsNilsInsideNamedCollections(t *testing.T) {
	object := map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "a"},
		"m":          namedAnyMap{"present": "x", "null": nil},
		"s":          namedAnySlice{"x", nil},
	}

	// generatePassthrough t.Fatal's on an error, and a panic here fails the test
	// outright — both are the point: before copiedValue existed, the slice case
	// panicked on the nil element.
	u := generatePassthrough(t, map[string]any{"object": object}, "ns1")

	// The copy keeps the named types, and so the nils inside them; what is emitted from
	// it is the JSON-shaped form (TestPassthrough_EmittedObjectIsJSONShaped), the nils
	// still in place.
	m, ok := u.Object["m"].(map[string]any)
	if !ok {
		t.Fatalf("emitted m = %T, want map[string]any", u.Object["m"])
	}
	if v, present := m["null"]; !present {
		t.Error(`emitted m dropped the "null" key — SetMapIndex took the zero Value as a delete`)
	} else if v != nil {
		t.Errorf(`emitted m["null"] = %#v, want nil`, v)
	}
	if got := m["present"]; got != "x" {
		t.Errorf(`emitted m["present"] = %#v, want "x"`, got)
	}

	s, ok := u.Object["s"].([]any)
	if !ok {
		t.Fatalf("emitted s = %T, want []any", u.Object["s"])
	}
	if len(s) != 2 {
		t.Fatalf("emitted s has %d elements, want 2", len(s))
	}
	if s[0] != "x" || s[1] != nil {
		t.Errorf("emitted s = %#v, want [x <nil>]", s)
	}
}

// TestPassthrough_FrozenCopyKeepsNilPlainSlices pins the one shape
// TestPassthrough_FrozenCopyKeepsNilsInsideNamedCollections does not reach: a
// typed-nil []any, supplied programmatically here (not via a YAML decode) but
// matching what yaml.v3 itself produces for a null-valued list field — it
// decodes a YAML null into a plain interface nil, and a null under a
// sequence-typed key decodes to []any(nil), not a named slice type.
// deepCopyValue's fast paths used to run before the null check, and
// []any(nil) matches the `case []any:` arm of a type switch (nil of that dynamic
// type) — so `make([]any, len(nil))` silently produced a non-nil empty slice,
// turning an authored null into `[]` on the way out. The README's "a null
// survives as a null" claim was false for exactly this shape until this test
// existed to hold it.
func TestPassthrough_FrozenCopyKeepsNilPlainSlices(t *testing.T) {
	object := map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "a"},
		"spec": map[string]any{
			"values": []any(nil),
		},
	}

	u := generatePassthrough(t, map[string]any{"object": object}, "ns1")

	spec, ok := u.Object["spec"].(map[string]any)
	if !ok {
		t.Fatalf("emitted spec = %T, want map[string]any", u.Object["spec"])
	}
	values, present := spec["values"]
	if !present {
		t.Fatal(`emitted spec dropped the "values" key`)
	}
	// values != nil would always be true here even on success: a correctly-
	// preserved null is a typed-nil []any wrapped in an any, and comparing that
	// interface directly against the untyped nil literal never reports equal —
	// the standard Go "nil interface vs interface holding nil" trap. Assert on
	// the concrete slice's nil-ness instead of the interface's.
	slice, ok := values.([]any)
	if !ok {
		t.Fatalf(`emitted spec["values"] = %#v (%T), want a []any (possibly nil)`, values, values)
	}
	if slice != nil {
		t.Errorf(`emitted spec["values"] = %#v, want a nil []any (got a non-nil []any — the null became "[]")`, slice)
	}
}

// TestPassthrough_GenerateRevalidatesTheEmittedObject pins the routes that never run
// ToApplicationConfig at all. Freezing the map only defeats a caller mutating the map
// it passed in; Object is an exported field, so assigning a fresh map to it, building
// the struct by literal, or decoding one from JSON all reach Generate with a body the
// rejection arms never saw. Generate re-runs them on the map it is about to emit.
func TestPassthrough_GenerateRevalidatesTheEmittedObject(t *testing.T) {
	listBody := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items": []any{
			map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "b"}},
		},
	}
	nullItemsBody := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMapList",
		"items":      nil,
	}

	t.Run("assigned over the exported Object field", func(t *testing.T) {
		h := &components.PassthroughHandler{}
		raw, err := h.ToApplicationConfig(passthroughComponent(map[string]any{
			"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}},
		}), "ns1")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		cfg, ok := raw.(*components.PassthroughConfig)
		if !ok {
			t.Fatalf("ToApplicationConfig returned %T, want *components.PassthroughConfig", raw)
		}
		cfg.Object = listBody

		_, err = cfg.Generate(nil)
		if err == nil {
			t.Fatal("Generate accepted a list assigned over the validated object")
		}
		if !strings.Contains(err.Error(), "is a list") {
			t.Errorf("error = %q, want the list diagnostic", err)
		}
	})

	t.Run("struct literal that never called the constructor", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "ns1", Object: listBody}
		if _, err := cfg.Generate(nil); err == nil {
			t.Fatal("Generate accepted a list from a struct literal")
		}
	})

	t.Run("items assigned as null over the exported field", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "ns1", Object: nullItemsBody}
		_, err := cfg.Generate(nil)
		if err == nil {
			t.Fatal("Generate accepted an items: null envelope")
		}
		// Asserting the diagnostic, not merely that an error happened: the two arms
		// are order-coupled, and a first arm widened to swallow the null cases would
		// still error here while reporting the wrong cause.
		if !strings.Contains(err.Error(), "zero objects") {
			t.Errorf("error = %q, want the zero-objects diagnostic", err)
		}
	})

	t.Run("no object at all", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "ns1"}
		_, err := cfg.Generate(nil)
		if err == nil {
			t.Fatal("Generate accepted a config with no object")
		}
		if !strings.Contains(err.Error(), "no 'object'") {
			t.Errorf("error = %q, want the uninitialized-config diagnostic", err)
		}
	})

	// The nil guard alone was not enough, and the gap is the one the guard's own
	// comment claimed to close. An empty map is non-nil, so it cleared the guard, and
	// it carries no items, so it cleared both list arms — leaving Generate to emit a
	// document consisting of nothing but the metadata it had just stamped on. Every
	// check that must hold of the emitted map now lives in one function, so the
	// constructor and Generate cannot disagree about what a valid body is.
	for name, object := range map[string]map[string]any{
		"empty map":         {},
		"apiVersion only":   {"apiVersion": "v1"},
		"kind only":         {"kind": "ConfigMap"},
		"empty kind string": {"apiVersion": "v1", "kind": ""},
		"non-string kind":   {"apiVersion": "v1", "kind": 17},
	} {
		t.Run("body with no identity: "+name, func(t *testing.T) {
			cfg := &components.PassthroughConfig{Namespace: "ns1", Object: object}
			_, err := cfg.Generate(nil)
			if err == nil {
				t.Fatal("Generate emitted a body carrying no apiVersion/kind")
			}
			if !strings.Contains(err.Error(), "is required and must be a non-empty string") {
				t.Errorf("error = %q, want the required-identity diagnostic", err)
			}
		})
	}

	t.Run("a plain object still generates", func(t *testing.T) {
		// The control: re-validation must not reject what the constructor accepts,
		// or every assertion above passes for the wrong reason.
		u := generatePassthrough(t, map[string]any{
			"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}},
		}, "ns1")
		if got := u.GetKind(); got != "ConfigMap" {
			t.Errorf("emitted kind = %q, want ConfigMap", got)
		}
	})
}

func TestPassthroughHandler_DoesNotMutateSource(t *testing.T) {
	objMeta := map[string]any{"name": ""}
	object := map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   objMeta,
	}
	props := map[string]any{"object": object}

	_ = generatePassthrough(t, props, "ns1")

	// Source metadata must be untouched (no injected name/namespace).
	if _, ok := objMeta["namespace"]; ok {
		t.Errorf("source metadata mutated: namespace injected into %#v", objMeta)
	}
	if objMeta["name"] != "" {
		t.Errorf("source metadata mutated: name set to %q", objMeta["name"])
	}
}

// TestPassthrough_RepeatedKeyWalkTakesBigInteger: the check for a repeated key reads
// numbers as text, so an authored integer beyond float64 range is accepted and emitted
// with its own digits, and a real repeated key next to it is still refused
// (go-kure/launcher#1176).
func TestPassthrough_RepeatedKeyWalkTakesBigInteger(t *testing.T) {
	digits := "1" + strings.Repeat("0", 399)
	object := func(raw string) map[string]any {
		return map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"data":       map[string]any{"big": json.RawMessage(raw)},
		}
	}

	t.Run("an integer beyond float64 range is accepted", func(t *testing.T) {
		u := generatePassthrough(t, map[string]any{"object": object(digits)}, "ns1")
		got := u.Object["data"].(map[string]any)["big"]
		if got != json.Number(digits) {
			t.Errorf("big = %#v, want json.Number with its 400 digits", got)
		}
	})

	t.Run("a repeated key next to it is still refused", func(t *testing.T) {
		h := &components.PassthroughHandler{}
		_, err := h.ToApplicationConfig(passthroughComponent(map[string]any{
			"object": object(`{"n":` + digits + `,"n":2}`),
		}), "ns1")
		if err == nil || !strings.Contains(err.Error(), "more than once") {
			t.Errorf("error = %v, want the repeated-key refusal", err)
		}
	})
}

// TestPassthrough_DecimalBeyondFloat64KeepsItsDigits: a fraction or an exponent becomes a
// float64 only where that is the same number. Otherwise it stays a json.Number with its
// own digits, as an integer beyond int64 does, so the emitted object does not round what
// was authored (go-kure/launcher#1170).
func TestPassthrough_DecimalBeyondFloat64KeepsItsDigits(t *testing.T) {
	cases := []struct {
		raw  string
		want any
	}{
		{"9.223372036854775809e18", json.Number("9.223372036854775809e18")},
		{"1.0000000000000000000001", json.Number("1.0000000000000000000001")},
		{"-0.30000000000000000000000001", json.Number("-0.30000000000000000000000001")},
		{"1e400", json.Number("1e400")},
		{"1e-9999999", json.Number("1e-9999999")},
		{"1" + strings.Repeat("0", 1200) + ".5", json.Number("1" + strings.Repeat("0", 1200) + ".5")},
		{"0.1", 0.1},
		{"1.50", 1.5},
		{"1e3", float64(1000)},
		{"2.5E-3", 0.0025},
		{"0.0", float64(0)},
		{"1e-320", 1e-320},
	}
	for _, c := range cases {
		name := c.raw
		if len(name) > 30 {
			name = name[:30] + "..."
		}
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			u := generatePassthrough(t, map[string]any{"object": map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"data":       map[string]any{"n": json.RawMessage(c.raw)},
			}}, "ns1")
			if d := time.Since(start); d > time.Second {
				t.Errorf("took %v, the input must be bounded before it is compared", d)
			}
			if got := u.Object["data"].(map[string]any)["n"]; got != c.want {
				t.Errorf("n = %#v, want %#v", got, c.want)
			}
		})
	}
}

// TestPassthrough_AuthoredJSONNumberIsShaped: a json.Number a Go caller put in the object
// is given the type its text has, as the same text in a document is, so that the
// unstructured readers find it (go-kure/launcher#1167).
func TestPassthrough_AuthoredJSONNumberIsShaped(t *testing.T) {
	u := generatePassthrough(t, map[string]any{"object": map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"data": map[string]any{
			"int":   json.Number("12"),
			"frac":  json.Number("1.5"),
			"big":   json.Number("9223372036854775808"),
			"round": json.Number("9.223372036854775809e18"),
			"list":  []any{json.Number("7")},
		},
	}}, "ns1")

	want := map[string]any{
		"int":   int64(12),
		"frac":  1.5,
		"big":   json.Number("9223372036854775808"),
		"round": json.Number("9.223372036854775809e18"),
		"list":  []any{int64(7)},
	}
	if got := u.Object["data"]; !reflect.DeepEqual(got, want) {
		t.Errorf("data = %#v, want %#v", got, want)
	}
	if n, found, err := unstructured.NestedInt64(u.Object, "data", "int"); err != nil || !found || n != 12 {
		t.Errorf("NestedInt64 = %d, %v, %v; want 12, true, nil", n, found, err)
	}
}

// TestPassthrough_UnserializableValueIsRefused: a value that does not serialize cannot be
// given the JSON types, and an Unstructured holding it panics when it is deep-copied. With
// no policy nothing else refuses it, so Generate does, and names the field
// (go-kure/launcher#1166).
func TestPassthrough_UnserializableValueIsRefused(t *testing.T) {
	type holdsFunc struct{ F func() }
	cases := []struct {
		name  string
		value any
	}{
		{"func", func() {}},
		{"chan", make(chan int)},
		{"complex", complex(1, 2)},
		{"NaN", math.NaN()},
		{"infinity", math.Inf(1)},
		{"text number", json.Number("abc")},
		{"JSON value that is not a number", json.Number("true")},
		{"plus sign", json.Number("+12")},
		{"leading zero", json.Number("012")},
		{"negative leading zero", json.Number("-01")},
		{"struct holding a func", holdsFunc{F: func() {}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &components.PassthroughHandler{}
			cfg, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": map[string]any{
				"apiVersion": "v1",
				"kind":       "Service",
				"spec": map[string]any{
					"ports": []any{map[string]any{"x": c.value}},
				},
			}}), "ns1")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			objs, err := cfg.Generate(nil)
			if err == nil {
				// Today the object is emitted, and the deep copy panics.
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("Generate emitted the object, and DeepCopyObject panicked: %v", r)
						}
					}()
					(*objs[0]).DeepCopyObject()
				}()
				t.Fatal("Generate returned no error")
			}
			if !strings.Contains(err.Error(), "spec.ports[0].x") {
				t.Errorf("error = %q, want it to name spec.ports[0].x", err)
			}
		})
	}
}

// TestPassthrough_RefusalPathKeepsLiteralKeys: the path in the refusal tells two fields
// apart. A key with a dot, a bracket or a quote, or an empty key, is written quoted, so
// spec["a.b"] is not confused with spec.a.b (go-kure/launcher#1166).
func TestPassthrough_RefusalPathKeepsLiteralKeys(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"dotted key", map[string]any{"a.b": func() {}}, `spec["a.b"]`},
		{"nested under a dotted key", map[string]any{"a.b": map[string]any{"c": func() {}}}, `spec["a.b"].c`},
		{"bracket key", map[string]any{"a[0]": func() {}}, `spec["a[0]"]`},
		{"empty key", map[string]any{"": func() {}}, `spec[""]`},
		{"plain key", map[string]any{"a": func() {}}, `spec.a`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &components.PassthroughHandler{}
			cfg, err := h.ToApplicationConfig(passthroughComponent(map[string]any{"object": map[string]any{
				"apiVersion": "v1",
				"kind":       "Service",
				"spec":       c.spec,
			}}), "ns1")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			_, err = cfg.Generate(nil)
			if err == nil || !strings.Contains(err.Error(), c.want+" holds") {
				t.Errorf("error = %v, want it to name %s", err, c.want)
			}
		})
	}
}

// TestPassthrough_EmptyJSONNumberIsZero: encoding/json writes the zero value of json.Number
// as 0, so the object it came in serialized before and is emitted with an int64 0, not
// refused.
func TestPassthrough_EmptyJSONNumberIsZero(t *testing.T) {
	u := generatePassthrough(t, map[string]any{"object": map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"data":       map[string]any{"n": json.Number(""), "list": []any{json.Number("")}},
	}}, "ns1")
	want := map[string]any{"n": int64(0), "list": []any{int64(0)}}
	if got := u.Object["data"]; !reflect.DeepEqual(got, want) {
		t.Errorf("data = %#v, want %#v", got, want)
	}
}
