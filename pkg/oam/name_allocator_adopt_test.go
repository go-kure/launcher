package oam

import (
	"encoding/json"
	"strings"
	"testing"
)

func adoptOrigin(component, namespace string) Origin {
	return Origin{Document: "app", DocumentKind: "Application", Namespace: namespace, Component: component, ComponentType: "source-user"}
}

// assertIdentityDigests checks that a conflicting-identity error names each identity
// by its digest and never echoes the identity text itself, which may carry
// sensitive inputs.
func assertIdentityDigests(t *testing.T, err error, identities ...string) {
	t.Helper()
	for _, id := range identities {
		if !strings.Contains(err.Error(), identityDigest(id)) {
			t.Errorf("error %q does not contain the digest %s of %q", err, identityDigest(id), id)
		}
		if strings.Contains(err.Error(), id) {
			t.Errorf("error %q echoes the raw identity %q", err, id)
		}
	}
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

	t.Run("a claim from an earlier round is adopted in a later round", func(t *testing.T) {
		n := NewNameAllocator()
		n.round = 0
		if adopted, err := n.EmitOrAdopt("src", "helmrepository|https://charts.example", a); err != nil || adopted {
			t.Fatalf("round-0 claim = (%v, %v), want (false, nil)", adopted, err)
		}
		n.round = 1
		adopted, err := n.EmitOrAdopt("src", "helmrepository|https://charts.example", b)
		if err != nil || !adopted {
			t.Fatalf("round-1 same-identity claim = (%v, %v), want (true, nil)", adopted, err)
		}
		n.round = 2
		if _, err := n.EmitOrAdopt("src", "helmrepository|https://other.example", b); err == nil {
			t.Error("a later-round claim with different content was adopted")
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
		assertIdentityDigests(t, err, "helmrepository|https://one.example", "helmrepository|https://two.example")
	})

	// The first claimant gets no pass on its own name: changed content from the
	// same origin is as much a collision as changed content from a sibling.
	t.Run("different identity from the first claimant's own origin hard-fails", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			round int
		}{
			{"same round", 0},
			{"later round", 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n := NewNameAllocator()
				if _, err := n.EmitOrAdopt("src", "helmrepository|https://one.example", a); err != nil {
					t.Fatal(err)
				}
				n.round = tc.round
				adopted, err := n.EmitOrAdopt("src", "helmrepository|https://two.example", a)
				if err == nil {
					t.Fatalf("same-origin claim with different content = (%v, nil), want an error", adopted)
				}
				assertIdentityDigests(t, err, "helmrepository|https://one.example", "helmrepository|https://two.example")
				if !strings.Contains(err.Error(), `component "a"`) {
					t.Errorf("error %q does not name the origin", err)
				}
			})
		}
	})

	// Adoption stays inside one authored document: each settled document is
	// transformed on its own, so adopting another document's element would leave it
	// missing from the adopter's output.
	t.Run("same identity from another document in the namespace hard-fails", func(t *testing.T) {
		for _, other := range []Origin{
			{Document: "other", DocumentKind: "Application", Component: "b", ComponentType: "source-user"},
			{Document: "app", DocumentKind: "WebApplication", Component: "b", ComponentType: "source-user"},
		} {
			n := NewNameAllocator()
			if _, err := n.EmitOrAdopt("src", "helmrepository|https://charts.example", a); err != nil {
				t.Fatal(err)
			}
			adopted, err := n.EmitOrAdopt("src", "helmrepository|https://charts.example", other)
			if err == nil {
				t.Fatalf("claim from %s = (%v, nil), want a cross-document collision", other, adopted)
			}
			for _, want := range []string{`"src"`, a.String(), other.String()} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %s", err, want)
				}
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

// TestNameOrAdopt_Errors: every EmitOrAdopt refusal surfaces through the wrapper
// as an error with no name and adopted=false, never as a silent claim.
func TestNameOrAdopt_Errors(t *testing.T) {
	a, b := adoptOrigin("a", ""), adoptOrigin("b", "")
	tests := []struct {
		name     string
		setup    func(t *testing.T, n *NameAllocator)
		identity string
	}{
		{
			name: "conflicting identity",
			setup: func(t *testing.T, n *NameAllocator) {
				if _, _, err := n.NameOrAdopt("podinfo", "helmrepo", "helmrepository|https://one.example", a); err != nil {
					t.Fatal(err)
				}
			},
			identity: "helmrepository|https://two.example",
		},
		{
			name:     "empty identity",
			setup:    func(*testing.T, *NameAllocator) {},
			identity: "",
		},
		{
			name: "name first taken by Reserve",
			setup: func(t *testing.T, n *NameAllocator) {
				if err := n.Reserve("podinfo-helmrepo", a); err != nil {
					t.Fatal(err)
				}
			},
			identity: "helmrepository|https://one.example",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNameAllocator()
			tt.setup(t, n)
			name, adopted, err := n.NameOrAdopt("podinfo", "helmrepo", tt.identity, b)
			if err == nil {
				t.Fatalf("NameOrAdopt = (%q, %v, nil), want an error", name, adopted)
			}
			if name != "" || adopted {
				t.Errorf("NameOrAdopt returned (%q, %v) alongside error %v, want (\"\", false)", name, adopted, err)
			}
		})
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
	return lowerWithSharedSource(comp, url, lctx)
}

// imageSourceRule is sharedSourceRule keyed on the image property, which is what a
// testRawRule-emitted component carries.
type imageSourceRule struct{}

func (imageSourceRule) ComponentType() string { return "image-source-user" }

func (imageSourceRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	image, _ := comp.Properties["image"].(string)
	return lowerWithSharedSource(comp, image, lctx)
}

func lowerWithSharedSource(comp *Component, url string, lctx LoweringContext) (LoweringResult, error) {
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

// delayedSourceUserRule re-emits its component unchanged except for the type,
// "source-user", so sharedSourceRule sees it one round later than an authored
// source-user sibling.
type delayedSourceUserRule struct{}

func (delayedSourceUserRule) ComponentType() string { return "delayed-source-user" }

func (delayedSourceUserRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "source-user", Properties: comp.Properties}}}, nil
}

// TestLower_EmitOrAdopt_StaggeredSiblingsAdopt: siblings that reach the shared
// source in different lowering rounds still converge on one adopted element,
// whichever of them claims it first.
func TestLower_EmitOrAdopt_StaggeredSiblingsAdopt(t *testing.T) {
	for _, tc := range []struct{ name, typeA, typeB string }{
		{"later sibling delayed", "source-user", "delayed-source-user"},
		{"earlier sibling delayed", "delayed-source-user", "source-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tr.RegisterComponentLowering(sharedSourceRule{})
			tr.RegisterComponentLowering(delayedSourceUserRule{})
			app := &Application{
				APIVersion: SupportedAPIVersion,
				Kind:       terminalDocumentKind,
				Metadata:   Metadata{Name: "myapp"},
				Spec: ApplicationSpec{Components: []Component{
					{Name: "a", Type: tc.typeA, Properties: map[string]any{"url": "nginx:1"}},
					{Name: "b", Type: tc.typeB, Properties: map[string]any{"url": "nginx:1"}},
				}},
			}
			out, err := tr.lower(app, TransformContext{})
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
		})
	}
}

// sharedSourceOnlyRule emits nothing but the shared source, so an adopting sibling
// is left with an empty result — outside EmitOrAdopt's contract.
type sharedSourceOnlyRule struct{}

func (sharedSourceOnlyRule) ComponentType() string { return "source-only" }

func (sharedSourceOnlyRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	url, _ := comp.Properties["url"].(string)
	adopted, err := lctx.Namer.EmitOrAdopt("shared-source", "source|"+url, lctx.Origin)
	if err != nil || adopted {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: "shared-source", Type: "worker", Properties: map[string]any{"image": url}}}}, nil
}

// TestLower_EmitOrAdopt_SharedOnlyExpansionFails pins the contract boundary: a rule
// whose whole expansion is the shared element leaves the adopting sibling with
// nothing to emit, and the engine rejects that as a deletion rather than converging.
func TestLower_EmitOrAdopt_SharedOnlyExpansionFails(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(sharedSourceOnlyRule{})
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{Components: []Component{
			{Name: "a", Type: "source-only", Properties: map[string]any{"url": "nginx:1"}},
			{Name: "b", Type: "source-only", Properties: map[string]any{"url": "nginx:1"}},
		}},
	}
	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("an adopting sibling with an empty expansion was accepted")
	}
	for _, want := range []string{"emitted nothing", `component "b"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %s", err, want)
		}
	}
}

// TestLowerRaws_InTransformAdoptionRunsPerDocument: LowerRaws runs raw rules only, so
// an in-transform rule's EmitOrAdopt claim never meets another document's in the
// LowerRaws allocator. Two same-namespace documents whose rules would both emit
// "shared-source" leave LowerRaws with that component unlowered, and each document's
// own Transform, with a fresh allocator, emits its own shared element. The
// cross-document refusal itself is covered at allocator level by TestEmitOrAdopt.
func TestLowerRaws_InTransformAdoptionRunsPerDocument(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice": &pipelineComponentHandler{typ: "webservice"},
		"worker":     &pipelineComponentHandler{typ: "worker"},
	}, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", compType: "image-source-user"})
	tr.RegisterComponentLowering(imageSourceRule{})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop"), rawWebApplication("cart")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("LowerRaws returned %d documents, want 2", len(out))
	}
	for _, raw := range out {
		app := parseLoweredOutput(t, tr, raw)
		if c := app.Spec.Components; len(c) != 1 || c[0].Type != "image-source-user" {
			t.Fatalf("document %q components = %+v, want the one image-source-user component unlowered", app.Metadata.Name, c)
		}
		// Transform returns a cluster, not the lowered document, so the shared element
		// is observed as the stack application its component became.
		cluster, err := tr.Transform(app, TransformContext{})
		if err != nil {
			t.Fatalf("Transform %q: %v", app.Metadata.Name, err)
		}
		names := bundleAppNames(cluster.Node.Bundle)
		sources := 0
		for _, n := range names {
			if n == "shared-source" {
				sources++
			}
		}
		if sources != 1 {
			t.Errorf("document %q transformed to applications %v, want exactly one shared-source", app.Metadata.Name, names)
		}
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
