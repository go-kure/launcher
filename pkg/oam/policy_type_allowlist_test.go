package oam

import (
	"errors"
	"strings"
	"testing"

	kureerrors "github.com/go-kure/launcher/pkg/errors"
)

// policyDoc is a one-component document that carries one policy of type typ.
func policyDoc(typ string) []byte {
	return []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
  policies:
    - name: pol
      type: ` + typ + `
`)
}

// A policy type is checked against a known set at parse time, the way a component
// type and a trait type are, so a typo is refused where it is written and not at
// build, after the document has been accepted.
func TestParse_RefusesAnUnknownPolicyType(t *testing.T) {
	for _, typ := range []string{"staged-rollout", "placment", "Placement"} {
		t.Run(typ, func(t *testing.T) {
			_, err := Parse(policyDoc(typ))
			if err == nil {
				t.Fatalf("Parse accepted the unknown policy type %q", typ)
			}
			var ve *kureerrors.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error %T is not a *ValidationError: %v", err, err)
			}
			if ve.Field != "type" {
				t.Errorf("the refusal names field %q, want \"type\"", ve.Field)
			}
			if !strings.Contains(err.Error(), typ) {
				t.Errorf("the refusal does not name the type %q: %v", typ, err)
			}
			if !strings.Contains(err.Error(), "placement") {
				t.Errorf("the refusal does not list the supported types: %v", err)
			}
		})
	}
}

// An identity that nothing declared cannot resolve, so plain Parse, which knows no
// identity, refuses it as it refuses any other unknown policy type.
func TestParse_RefusesAnUnresolvablePolicyIdentity(t *testing.T) {
	for _, typ := range []string{"placement@v1", "placement.acme.example@v1"} {
		t.Run(typ, func(t *testing.T) {
			if _, err := Parse(policyDoc(typ)); err == nil {
				t.Fatalf("Parse accepted the policy identity %q that nothing declared", typ)
			}
		})
	}
}

// Every type a document could name before and still reach a handler, or the
// refusal a consumer relies on at build, keeps parsing as a plain name.
func TestParse_AcceptsTheKnownPolicyTypes(t *testing.T) {
	for _, typ := range []string{"dependency", "placement", "health-checks", "reconciliation", "app-dependency"} {
		t.Run(typ, func(t *testing.T) {
			if _, err := Parse(policyDoc(typ)); err != nil {
				t.Fatalf("Parse refused the known policy type %q: %v", typ, err)
			}
		})
	}
}

// A declared policy identity resolves to the plain name its handler is registered
// under, and that name is what the check sees. One that does not resolve is
// refused, whichever way.
func TestParseWithExtraTypes_PolicyIdentityResolves(t *testing.T) {
	tr, _ := declared(t)
	lt := tr.LowerableTypes()
	for _, typ := range []string{"placement.acme.example@v1", "placement@v1", "placement.acme.example", "placement"} {
		t.Run("accepts "+typ, func(t *testing.T) {
			if _, err := ParseWithExtraTypes(policyDoc(typ), nil, lt); err != nil {
				t.Fatalf("the declared policy identity %q was refused: %v", typ, err)
			}
		})
	}
	for _, typ := range []string{"placement@v9", "nothing.acme.example@v1"} {
		t.Run("refuses "+typ, func(t *testing.T) {
			if _, err := ParseWithExtraTypes(policyDoc(typ), nil, lt); err == nil {
				t.Fatalf("the policy identity %q that does not resolve was accepted", typ)
			}
		})
	}
}

// A policy type outside the known set parses when the caller claims it, and only then.
func TestParseWithExtraTypes_PolicyTypeIsOpenOnlyWhenClaimed(t *testing.T) {
	if _, err := ParseWithExtraTypes(policyDoc("staged-rollout"), nil, LowerableTypes{}); err == nil {
		t.Fatal("an unclaimed policy type was accepted")
	}
	if _, err := ParseWithExtraTypes(policyDoc("staged-rollout"), nil, LowerableTypes{PolicyTypes: []string{"staged-rollout"}}); err != nil {
		t.Fatalf("a claimed policy type was refused: %v", err)
	}
	if _, err := ParseWithExtraTypes(policyDoc("other"), nil, LowerableTypes{PolicyTypes: []string{"staged-rollout"}}); err == nil {
		t.Fatal("claiming one policy type opened another")
	}
}

// A policy handler registered on the Transformer is a type the Transformer
// accepts: its LowerableTypes carry it, so a caller that parses with them needs
// nothing more.
func TestTransformerLowerableTypes_CarryARegisteredPolicyHandler(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicy("staged-rollout", &stubPolicyHandler{typ: "staged-rollout"})
	if _, err := ParseWithExtraTypes(policyDoc("staged-rollout"), nil, tr.LowerableTypes()); err != nil {
		t.Fatalf("a policy type with a registered handler was refused: %v", err)
	}
	if _, err := ParseWithExtraTypes(policyDoc("staged-rolloutt"), nil, tr.LowerableTypes()); err == nil {
		t.Fatal("a policy type with no handler was accepted")
	}
}

// Once a lowering rule routes a document through the settled-document check, the
// check still judges a policy type: a registered handler makes it terminal, no
// handler leaves it refused.
func TestValidateSettled_JudgesAPolicyType(t *testing.T) {
	doc := func() *Application {
		return &Application{
			APIVersion: SupportedAPIVersion,
			Kind:       "Wrapper",
			Metadata:   Metadata{Name: "myapp"},
			Spec: ApplicationSpec{
				Components: []Component{{Name: "shop", Type: "webservice", Properties: map[string]any{}}},
				Policies:   []ApplicationPolicy{{Name: "pol", Type: "staged-rollout", Properties: map[string]any{}}},
			},
		}
	}

	with := NewTransformer(nil, nil)
	with.RegisterDocumentLowering(forwardingPoliciesDocRule{kind: "Wrapper"})
	with.RegisterPolicy("staged-rollout", &stubPolicyHandler{typ: "staged-rollout"})
	if _, err := with.lower(doc(), TransformContext{}); err != nil {
		t.Fatalf("a policy type with a registered handler was refused after lowering: %v", err)
	}

	without := NewTransformer(nil, nil)
	without.RegisterDocumentLowering(forwardingPoliciesDocRule{kind: "Wrapper"})
	_, err := without.lower(doc(), TransformContext{})
	if err == nil {
		t.Fatal("a policy type with no handler was accepted after lowering")
	}
	if !strings.Contains(err.Error(), "staged-rollout") {
		t.Errorf("the refusal does not name the policy type: %v", err)
	}
}
