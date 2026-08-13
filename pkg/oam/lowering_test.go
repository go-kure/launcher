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
