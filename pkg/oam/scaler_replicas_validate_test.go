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

// wantScalerReplicasRefusalText is the whole refusal as the Deployment kinds
// have always worded it (validateScalerReplicas, go-kure/launcher#1179). A
// statefulset names its own workload; the other kinds keep these bytes.
func wantScalerReplicasRefusalText(component string) string {
	return `component "` + component + `" sets replicas and has a scaler trait: the HorizontalPodAutoscaler owns the replica count, so the Deployment is built without spec.replicas; remove replicas and set minReplicas and maxReplicas on the scaler`
}

// TestValidate_ScalerReplicasMessageUnchangedOnDeploymentKinds: the refusal
// text of a webservice, worker or deployment equals the literal above.
func TestValidate_ScalerReplicasMessageUnchangedOnDeploymentKinds(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	for _, typ := range []string{"webservice", "worker", "deployment"} {
		t.Run(typ, func(t *testing.T) {
			app := scalerReplicasApp(map[string]any{"image": "x", "replicas": 3}, scaler)
			app.Spec.Components[0].Type = typ
			err := validate(app)
			if err == nil {
				t.Fatalf("a %s with replicas and a scaler validated; want it refused", typ)
			}
			if !strings.Contains(err.Error(), wantScalerReplicasRefusalText("web")) {
				t.Errorf("refusal %q does not contain the unchanged text %q", err, wantScalerReplicasRefusalText("web"))
			}
		})
	}
}

// TestValidate_ScalerRefusesAuthoredReplicasOnStatefulset: the rule of
// go-kure/launcher#1179 holds for a statefulset (go-kure/launcher#1268), and the
// refusal names the StatefulSet, not a Deployment the component does not build.
func TestValidate_ScalerRefusesAuthoredReplicasOnStatefulset(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	app := scalerReplicasApp(map[string]any{"image": "x", "replicas": 3}, scaler)
	app.Spec.Components[0].Type = "statefulset"
	err := validate(app)
	if err == nil {
		t.Fatal("a statefulset with replicas and a scaler validated; want it refused")
	}
	for _, want := range []string{wantScalerReplicasRefusal, `component "web"`, "the StatefulSet is built without spec.replicas", "minReplicas and maxReplicas"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "the Deployment is built") {
		t.Errorf("refusal %q names a Deployment the statefulset does not build", err)
	}
}

// TestValidate_ScalerReplicasControlsOnStatefulset: a scaler without replicas,
// with an authored null, and replicas without a scaler all validate on a
// statefulset.
func TestValidate_ScalerReplicasControlsOnStatefulset(t *testing.T) {
	scaler := Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 4}}
	cases := []struct {
		name   string
		props  map[string]any
		traits []Trait
	}{
		{"scaler without replicas", map[string]any{"image": "x"}, []Trait{scaler}},
		{"scaler with replicas null", map[string]any{"image": "x", "replicas": nil}, []Trait{scaler}},
		{"replicas without a scaler", map[string]any{"image": "x", "replicas": 3}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := scalerReplicasApp(tc.props, tc.traits...)
			app.Spec.Components[0].Type = "statefulset"
			if err := validate(app); err != nil {
				t.Fatalf("validate refused %s: %v", tc.name, err)
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
