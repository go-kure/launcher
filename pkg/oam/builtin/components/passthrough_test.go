package components_test

import (
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
