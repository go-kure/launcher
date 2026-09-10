package components_test

import (
	"reflect"
	"strings"
	"testing"

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
// Flux's kustomize unwraps it at apply time into N objects that do reach the cluster.
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
	// nothing.
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

	if got := u.Object["args"].([]string); !reflect.DeepEqual(got, []string{"before"}) {
		t.Errorf("emitted args = %#v, want [before] — the []string aliased the caller's slice", got)
	}
	if got := u.Object["data"].(map[string]string)["k"]; got != "before" {
		t.Errorf("emitted data[k] = %q, want before — the map[string]string aliased the caller's map", got)
	}
	if got := u.Object["nested"].([]any)[0].([]string); !reflect.DeepEqual(got, []string{"before"}) {
		t.Errorf("emitted nested[0] = %#v, want [before] — a typed slice inside an []any aliased through", got)
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

	m, ok := u.Object["m"].(namedAnyMap)
	if !ok {
		t.Fatalf("emitted m = %T, want namedAnyMap", u.Object["m"])
	}
	if v, present := m["null"]; !present {
		t.Error(`emitted m dropped the "null" key — SetMapIndex took the zero Value as a delete`)
	} else if v != nil {
		t.Errorf(`emitted m["null"] = %#v, want nil`, v)
	}
	if got := m["present"]; got != "x" {
		t.Errorf(`emitted m["present"] = %#v, want "x"`, got)
	}

	s, ok := u.Object["s"].(namedAnySlice)
	if !ok {
		t.Fatalf("emitted s = %T, want namedAnySlice", u.Object["s"])
	}
	if len(s) != 2 {
		t.Fatalf("emitted s has %d elements, want 2", len(s))
	}
	if s[0] != "x" || s[1] != nil {
		t.Errorf("emitted s = %#v, want [x <nil>]", s)
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
