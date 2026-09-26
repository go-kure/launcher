package oam

import (
	"strings"
	"testing"
)

func adoptOrigin(component, namespace string) Origin {
	return Origin{Document: "app", DocumentKind: "Application", Namespace: namespace, Component: component, ComponentType: "source-user"}
}

func TestEmitOrAdopt(t *testing.T) {
	a, b := adoptOrigin("a", ""), adoptOrigin("b", "")

	t.Run("first claim emits, same identity adopts", func(t *testing.T) {
		n := NewNameAllocator()
		adopted, err := n.EmitOrAdopt("src", "helmrepository|https://charts.example", a)
		if err != nil || adopted {
			t.Fatalf("first claim = (%v, %v), want (false, nil)", adopted, err)
		}
		adopted, err = n.EmitOrAdopt("src", "helmrepository|https://charts.example", b)
		if err != nil || !adopted {
			t.Fatalf("same-identity claim from another origin = (%v, %v), want (true, nil)", adopted, err)
		}
		adopted, err = n.EmitOrAdopt("src", "helmrepository|https://charts.example", a)
		if err != nil || !adopted {
			t.Fatalf("same-identity repeat from the first origin = (%v, %v), want (true, nil)", adopted, err)
		}
	})

	t.Run("different identity hard-fails naming both origins", func(t *testing.T) {
		n := NewNameAllocator()
		if _, err := n.EmitOrAdopt("src", "helmrepository|https://one.example", a); err != nil {
			t.Fatal(err)
		}
		_, err := n.EmitOrAdopt("src", "helmrepository|https://two.example", b)
		if err == nil {
			t.Fatal("different content at the same name was adopted")
		}
		for _, want := range []string{`"src"`, `component "a"`, `component "b"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %s", err, want)
			}
		}
	})

	t.Run("a Reserve claim is never adoptable, in either order", func(t *testing.T) {
		n := NewNameAllocator()
		if err := n.Reserve("taken", a); err != nil {
			t.Fatal(err)
		}
		if _, err := n.EmitOrAdopt("taken", "x", b); err == nil {
			t.Error("EmitOrAdopt adopted a name Reserve had claimed")
		}

		n = NewNameAllocator()
		if _, err := n.EmitOrAdopt("shared", "x", a); err != nil {
			t.Fatal(err)
		}
		if err := n.Reserve("shared", b); err == nil {
			t.Error("Reserve accepted a name EmitOrAdopt had claimed")
		}
		if err := n.Reserve("shared", a); err == nil {
			t.Error("Reserve from the same origin accepted a name EmitOrAdopt had claimed")
		}
	})

	t.Run("empty identity is refused", func(t *testing.T) {
		n := NewNameAllocator()
		if _, err := n.EmitOrAdopt("src", "", a); err == nil {
			t.Error("empty identity accepted; every such claim would adopt every other")
		}
		if _, err := n.EmitOrAdopt("src", "x", a); err != nil {
			t.Errorf("a refused empty-identity claim still took the name: %v", err)
		}
	})

	t.Run("namespaces are disjoint", func(t *testing.T) {
		n := NewNameAllocator()
		if _, err := n.EmitOrAdopt("src", "one", adoptOrigin("a", "ns1")); err != nil {
			t.Fatal(err)
		}
		adopted, err := n.EmitOrAdopt("src", "two", adoptOrigin("b", "ns2"))
		if err != nil || adopted {
			t.Errorf("same name in another namespace = (%v, %v), want (false, nil)", adopted, err)
		}
	})
}

func TestNameOrAdopt(t *testing.T) {
	n := NewNameAllocator()
	name, adopted, err := n.NameOrAdopt("podinfo", "helmrepo", "id", adoptOrigin("a", ""))
	if err != nil || adopted || name != "podinfo-helmrepo" {
		t.Fatalf("NameOrAdopt = (%q, %v, %v), want (podinfo-helmrepo, false, nil)", name, adopted, err)
	}
	name, adopted, err = n.NameOrAdopt("podinfo", "helmrepo", "id", adoptOrigin("b", ""))
	if err != nil || !adopted || name != "podinfo-helmrepo" {
		t.Fatalf("second NameOrAdopt = (%q, %v, %v), want (podinfo-helmrepo, true, nil)", name, adopted, err)
	}
	if _, _, err := n.NameOrAdopt("Bad_Name", "x", "id", adoptOrigin("a", "")); err == nil {
		t.Error("a non-DNS-1123 name was accepted")
	}
}

// sharedSourceRule is the synthetic stand-in for a rule whose components share a
// derived object: every "source-user" component emits its own leaf plus a source
// component under the fixed name "shared-source". The url property is the claim's
// identity, not part of the name, so two components with the same url adopt one
// source and two with different urls collide on the name deliberately.
type sharedSourceRule struct{}

func (sharedSourceRule) ComponentType() string { return "source-user" }

func (sharedSourceRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	url, _ := comp.Properties["url"].(string)
	leaf := Component{Name: comp.Name, Type: "webservice", Properties: map[string]any{"image": "nginx"}}
	adopted, err := lctx.Namer.EmitOrAdopt("shared-source", "source|"+url, lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	if adopted {
		return LoweringResult{Components: []Component{leaf}}, nil
	}
	src := Component{Name: "shared-source", Type: "worker", Properties: map[string]any{"image": url}}
	return LoweringResult{Components: []Component{leaf, src}}, nil
}

func lowerSharedSource(t *testing.T, urlA, urlB string) ([]*Application, error) {
	t.Helper()
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(sharedSourceRule{})
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{Components: []Component{
			{Name: "a", Type: "source-user", Properties: map[string]any{"url": urlA}},
			{Name: "b", Type: "source-user", Properties: map[string]any{"url": urlB}},
		}},
	}
	return tr.lower(app, TransformContext{})
}

// TestLower_EmitOrAdopt_SharedSourceEmittedOnce: two same-round sibling components
// that cannot see each other's output share one derived component instead of
// colliding on its name.
func TestLower_EmitOrAdopt_SharedSourceEmittedOnce(t *testing.T) {
	out, err := lowerSharedSource(t, "nginx:1", "nginx:1")
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("lower returned %d documents, want 1", len(out))
	}
	var names []string
	sources := 0
	for _, c := range out[0].Spec.Components {
		names = append(names, c.Name)
		if c.Name == "shared-source" {
			sources++
		}
	}
	if sources != 1 || len(names) != 3 {
		t.Errorf("components = %v, want a, b and exactly one shared-source", names)
	}
}

func TestLower_EmitOrAdopt_DifferentContentCollides(t *testing.T) {
	_, err := lowerSharedSource(t, "nginx:1", "nginx:2")
	if err == nil {
		t.Fatal("two different sources under one name were merged")
	}
	if !strings.Contains(err.Error(), `component "a"`) || !strings.Contains(err.Error(), `component "b"`) {
		t.Errorf("error does not name both origins: %v", err)
	}
}
