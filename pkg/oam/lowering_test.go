package oam

import "testing"

// TestLower_EmptyRegistry_ReturnsSamePointer is the bit-identity proof for the
// lowering engine (C1): with no rules registered at any position, lower must not
// copy, mutate, or re-validate app — it returns the exact same pointer it was given.
func TestLower_EmptyRegistry_ReturnsSamePointer(t *testing.T) {
	tr := NewTransformer(nil, nil)
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower returned error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly 1 document, got %d", len(docs))
	}
	if docs[0] != app {
		t.Fatal("lower did not return the same pointer with an empty registry")
	}
}

func TestHasLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	if tr.hasLoweringRules() {
		t.Fatal("expected hasLoweringRules to be false on a fresh Transformer")
	}
}

const nonTerminalKindYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: WebApplication
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
`

const nonTerminalComponentTypeYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: shop
      type: web-and-cache
      properties: {}
`

const nonTerminalTraitTypeYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
      traits:
        - type: expose-plus
          properties: {}
`

// TestParseWithExtraTypes_Kind is the C2 proof: a non-terminal kind is rejected by
// plain Parse/ParseWithExtraTraitTypes, and accepted by ParseWithExtraTypes only when
// LowerableTypes.Kinds claims it.
func TestParseWithExtraTypes_Kind(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalKindYAML)); err == nil {
		t.Fatal("expected Parse to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTraitTypes([]byte(nonTerminalKindYAML), nil); err == nil {
		t.Fatal("expected ParseWithExtraTraitTypes to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalKindYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalKindYAML), nil, LowerableTypes{Kinds: []string{"WebApplication"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with Kinds:[WebApplication] to accept it, got: %v", err)
	}
}

// TestParseWithExtraTypes_ComponentType is the C2 proof for the component position.
func TestParseWithExtraTypes_ComponentType(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalComponentTypeYAML)); err == nil {
		t.Fatal("expected Parse to reject component type web-and-cache")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalComponentTypeYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject component type web-and-cache")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalComponentTypeYAML), nil, LowerableTypes{ComponentTypes: []string{"web-and-cache"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with ComponentTypes:[web-and-cache] to accept it, got: %v", err)
	}
}

// TestParseWithExtraTypes_TraitType is the C2 proof for the trait position.
func TestParseWithExtraTypes_TraitType(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalTraitTypeYAML)); err == nil {
		t.Fatal("expected Parse to reject trait type expose-plus")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalTraitTypeYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject trait type expose-plus")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalTraitTypeYAML), nil, LowerableTypes{TraitTypes: []string{"expose-plus"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with TraitTypes:[expose-plus] to accept it, got: %v", err)
	}
}

// TestTransformer_LowerableTypes proves the accessor reflects exactly what is
// registered, on all four positions.
func TestTransformer_LowerableTypes(t *testing.T) {
	tr := NewTransformer(nil, nil)
	lt := tr.LowerableTypes()
	if len(lt.Kinds) != 0 || len(lt.ComponentTypes) != 0 || len(lt.TraitTypes) != 0 || len(lt.PolicyTypes) != 0 {
		t.Fatalf("expected all-empty LowerableTypes on a fresh Transformer, got %+v", lt)
	}
}
