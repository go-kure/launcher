package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

type authoredTestHandler struct{ typ string }

func (h authoredTestHandler) CanHandle(t string) bool { return t == h.typ }

func (h authoredTestHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}

func authoredTestApp(componentType, traits string) []byte {
	return []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
  - name: job
    type: ` + componentType + "\n" + traits)
}

// A type registered with RegisterAuthoredComponent parses; one registered with
// RegisterComponent stays refused.
func TestAuthoredComponent_ParseAdmitsOnlyOptedInTypes(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterAuthoredComponent("spark", authoredTestHandler{"spark"})
	tr.RegisterComponent("internal", authoredTestHandler{"internal"})

	if _, err := ParseWithExtraTypes(authoredTestApp("spark", ""), nil, tr.LowerableTypes()); err != nil {
		t.Errorf("opted-in type refused at parse: %v", err)
	}
	_, err := ParseWithExtraTypes(authoredTestApp("internal", ""), nil, tr.LowerableTypes())
	if want := `invalid value "internal" for "type"`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("plain handler type: got %v, want an error holding %q", err, want)
	}
}

// An opted-in type is terminal, so a trait restricted to other types is refused on
// it at parse, not deferred as it is for a type a rule will rewrite.
func TestAuthoredComponent_TraitRestrictionStillApplies(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterAuthoredComponent("spark", authoredTestHandler{"spark"})

	_, err := ParseWithExtraTypes(authoredTestApp("spark", "    traits:\n    - type: scaler\n"), nil, tr.LowerableTypes())
	if want := `trait "scaler" is not supported on component type "spark"`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error holding %q", err, want)
	}
}
