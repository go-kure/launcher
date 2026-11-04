package oam

import (
	"strings"
	"testing"
)

// A scaler on a component that authors `replicas` is refused (go-kure/launcher#1179):
// the HorizontalPodAutoscaler owns the replica count, so the Deployment is built
// without spec.replicas and an authored count would be accepted and applied to
// nothing.

// wantScalerReplicasRefusal is the part of the refusal that names both what is
// wrong and the fix; a test that only matched "replicas" would pass on any
// unrelated mention.
const wantScalerReplicasRefusal = "the HorizontalPodAutoscaler owns the replica count"

func scalerReplicasApp(props map[string]any, traits ...Trait) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{Components: []Component{{
			Name: "web", Type: "webservice", Properties: props, Traits: traits,
		}}},
	}
}

func TestValidate_ScalerRefusesAuthoredReplicas(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	for _, typ := range []string{"webservice", "worker", "deployment"} {
		t.Run(typ, func(t *testing.T) {
			app := scalerReplicasApp(map[string]any{"image": "x", "replicas": 3}, scaler)
			app.Spec.Components[0].Type = typ
			err := validate(app)
			if err == nil {
				t.Fatalf("a %s with replicas and a scaler validated; want it refused", typ)
			}
			for _, want := range []string{wantScalerReplicasRefusal, `component "web"`, "minReplicas and maxReplicas"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestValidate_ScalerReplicasControls(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	cases := []struct {
		name   string
		props  map[string]any
		traits []Trait
	}{
		{"scaler without replicas", map[string]any{"image": "x"}, []Trait{scaler}},
		{"replicas without a scaler", map[string]any{"image": "x", "replicas": 3}, nil},
		{"replicas beside another trait", map[string]any{"image": "x", "replicas": 3}, []Trait{{Type: "ingress", Properties: map[string]any{}}}},
		// An authored null is no count (the null contract), as for every property.
		{"scaler with replicas null", map[string]any{"image": "x", "replicas": nil}, []Trait{scaler}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate(scalerReplicasApp(tc.props, tc.traits...)); err != nil {
				t.Fatalf("validate refused %s: %v", tc.name, err)
			}
		})
	}
}

// replicasRule lowers its component type to a webservice, forwarding the traits
// and writing replicas when set: the properties a rule emits are the ones the
// settled pass sees (validateSettled), not the ones the document authored.
type replicasRule struct {
	replicas any
}

func (r replicasRule) ComponentType() string { return "scaled-web" }

func (r replicasRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	props := map[string]any{"image": "x"}
	if r.replicas != nil {
		props["replicas"] = r.replicas
	}
	return LoweringResult{Components: []Component{{
		Name: comp.Name, Type: "webservice", Properties: props, Traits: comp.Traits,
	}}}, nil
}

// TestLower_ValidateSettled_ScalerRefusesReplicasALoweringRuleEmits: the
// authored component type is not terminal, so validateTrait defers the check
// (componentIsLowerable) and validateSettled runs it on the lowered component.
func TestLower_ValidateSettled_ScalerRefusesReplicasALoweringRuleEmits(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	cases := []struct {
		name     string
		replicas any
		wantErr  bool
	}{
		{"rule emits replicas", 3, true},
		{"rule emits none (control)", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, map[string]TraitHandler{"scaler": &stubTraitHandler{typ: "scaler"}})
			tr.RegisterComponentLowering(replicasRule{replicas: tc.replicas})
			app := scalerReplicasApp(map[string]any{}, scaler)
			app.Spec.Components[0].Type = "scaled-web"
			_, err := tr.lower(app, TransformContext{})
			switch {
			case tc.wantErr && err == nil:
				t.Fatal("a lowered webservice with replicas and a scaler settled; want it refused")
			case tc.wantErr && !strings.Contains(err.Error(), wantScalerReplicasRefusal):
				t.Fatalf("refusal %q does not contain %q", err, wantScalerReplicasRefusal)
			case !tc.wantErr && err != nil:
				t.Fatalf("control refused: %v", err)
			}
		})
	}
}
