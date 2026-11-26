package oam

import (
	"strings"
	"testing"
)

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
// RegisterComponent stays refused (go-kure/launcher#1703).
func TestAuthoredComponent_ParseAdmitsOnlyOptedInTypes(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterAuthoredComponent("spark", &stubComponentHandler{typ: "spark"})
	tr.RegisterComponent("internal", &stubComponentHandler{typ: "internal"})

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
	tr.RegisterAuthoredComponent("spark", &stubComponentHandler{typ: "spark"})

	_, err := ParseWithExtraTypes(authoredTestApp("spark", "    traits:\n    - type: scaler\n"), nil, tr.LowerableTypes())
	if want := `trait "scaler" is not supported on component type "spark"`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error holding %q", err, want)
	}
}

// A parsed document naming an opted-in type goes through a transform. The lowering
// rule claims another type; it is registered so that the lowering pass and its
// check of the settled document run.
func TestAuthoredComponent_ParsesAndTransforms(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterAuthoredComponent("spark", &pipelineComponentHandler{typ: "spark"})
	tr.RegisterComponentLowering(forwardingComponentRule{fromType: "unused", toType: "spark"})

	app, err := ParseWithExtraTypes(authoredTestApp("spark", ""), nil, tr.LowerableTypes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if cluster == nil || cluster.Node == nil || cluster.Node.Bundle == nil {
		t.Fatalf("transform returned no root bundle: %+v", cluster)
	}
	apps := cluster.Node.Bundle.Applications
	if len(apps) != 1 || apps[0].Name != "job" {
		t.Errorf("got %d applications %+v, want the one of component %q", len(apps), apps, "job")
	}
}
