package oam

import (
	"strings"
	"testing"
)

// identityParseTransformer declares the identities Parse is asked to accept:
// webservice and configmap are in the component allowlist, scaler is restricted
// to webservice, worker and deployment.
func identityParseTransformer(t *testing.T) *Transformer {
	t.Helper()
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice": &stubComponentHandler{typ: "webservice"},
		"configmap":  &stubComponentHandler{typ: "configmap"},
	}, map[string]TraitHandler{"scaler": &stubTraitHandler{typ: "scaler"}})
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"webservice": mustID(t, "webservice.launcher.gokure.dev@v1"),
		"configmap":  mustID(t, "configmap@v1"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.DeclareTraitTypes(map[string]TypeID{"scaler": mustID(t, "scaler.launcher.gokure.dev@v1")}); err != nil {
		t.Fatal(err)
	}
	return tr
}

func identityDoc(componentType, traitType string) []byte {
	doc := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: ` + componentType + `
      properties: {image: nginx}
`
	if traitType != "" {
		doc += `      traits:
        - type: ` + traitType + `
          properties: {}
`
	}
	return []byte(doc)
}

func TestParseWithExtraTypes_AcceptsAnAuthoredIdentity(t *testing.T) {
	lowerable := identityParseTransformer(t).LowerableTypes()
	for _, ref := range []string{"webservice", "webservice@v1", "webservice.launcher.gokure.dev", "webservice.launcher.gokure.dev@v1.0.0"} {
		t.Run(ref, func(t *testing.T) {
			if ref != "webservice" {
				if _, err := Parse(identityDoc(ref, "")); err == nil {
					t.Error("Parse, which has no declared identity, accepted it")
				}
				if _, err := ParseWithExtraTypes(identityDoc(ref, ""), nil, LowerableTypes{}); err == nil {
					t.Error("an empty LowerableTypes accepted it")
				}
			}
			app, err := ParseWithExtraTypes(identityDoc(ref, ""), nil, lowerable)
			if err != nil {
				t.Fatalf("ParseWithExtraTypes: %v", err)
			}
			if got := app.Spec.Components[0].Type; got != ref {
				t.Errorf("the parsed type is %q, want the authored %q", got, ref)
			}
		})
	}
}

// The trait-component restriction is checked against the type the reference
// resolves to, not against the text the author wrote.
func TestParseWithExtraTypes_ChecksTheRestrictionOnTheResolvedType(t *testing.T) {
	lowerable := identityParseTransformer(t).LowerableTypes()
	if _, err := ParseWithExtraTypes(identityDoc("webservice@v1", "scaler@v1"), nil, lowerable); err != nil {
		t.Errorf("scaler on webservice@v1 refused: %v", err)
	}
	_, err := ParseWithExtraTypes(identityDoc("configmap@v1", "scaler@v1"), nil, lowerable)
	if err == nil || !strings.Contains(err.Error(), `component type "configmap"`) {
		t.Errorf("scaler on configmap@v1: error %v, want a refusal naming component type \"configmap\"", err)
	}
}

func TestParseWithExtraTypes_RefusesAnIdentityThatDoesNotResolve(t *testing.T) {
	lowerable := identityParseTransformer(t).LowerableTypes()
	for name, doc := range map[string][]byte{
		"component version": identityDoc("webservice@v9", ""),
		"trait version":     identityDoc("webservice", "scaler@v9"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseWithExtraTypes(doc, nil, lowerable)
			if err == nil || !strings.Contains(err.Error(), "v1") {
				t.Fatalf("error %v, want a refusal naming the registered version v1", err)
			}
		})
	}
}

// The identities LowerableTypes carries are the ones declared when it was taken.
func TestLowerableTypes_IdentitiesAreASnapshot(t *testing.T) {
	tr := identityParseTransformer(t)
	lowerable := tr.LowerableTypes()
	tr.RegisterComponent("worker", &stubComponentHandler{typ: "worker"})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"worker": mustID(t, "worker.launcher.gokure.dev@v1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseWithExtraTypes(identityDoc("worker@v1", ""), nil, lowerable); err == nil {
		t.Error("a type declared after LowerableTypes was taken was accepted")
	}
	if _, err := ParseWithExtraTypes(identityDoc("worker@v1", ""), nil, tr.LowerableTypes()); err != nil {
		t.Errorf("a fresh LowerableTypes refused it: %v", err)
	}
}

// With nothing declared, LowerableTypes is the strict set it always was.
func TestLowerableTypes_NoIdentitiesIsTheStrictSet(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"webservice": &stubComponentHandler{typ: "webservice"}}, nil)
	if tr.LowerableTypes().identities != nil {
		t.Error("a transformer with no declaration carries identities")
	}
	if _, err := ParseWithExtraTypes(identityDoc("webservice@v1", ""), nil, tr.LowerableTypes()); err == nil {
		t.Fatal("webservice@v1 was accepted with no identity declared")
	}
}
