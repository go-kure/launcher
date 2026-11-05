package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover where a scaler's budget, or a disruption trait, can come from
// besides an authored trait on the component: a ClusterProfile capability rendering,
// a trait lowering rule, a workload name override, and a custom type that claims the
// name (go-kure/launcher#1266).

// A scaler's enablePDB set by the profile reaches the scaler's properties before
// Apply, so only the Generate-time check sees it.
func TestDisruption_ProfileRenderedEnablePDB(t *testing.T) {
	scaler := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4})
	budget := disruptionTrait(map[string]any{"minAvailable": 1, "pdbName": "app-budget"})
	rendered := map[string]oam.CapabilityBinding{
		"scaler": {Rendering: map[string]any{"enablePDB": true}},
	}
	ctx := oam.TransformContext{Capabilities: rendered}

	t.Run("rendered true conflicts", func(t *testing.T) {
		_, err := budgetsWith(t, disruptionTransformer(), ctx, disruptionComponent("app", "deployment", scaler, budget))
		wantRefused(t, err, "enablePDB", "disruption", "drop one")
	})

	t.Run("authored false over rendered true is accepted", func(t *testing.T) {
		off := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4, "enablePDB": false})
		pdbs, err := budgetsWith(t, disruptionTransformer(), ctx, disruptionComponent("app", "deployment", off, budget))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 1 || pdbs[0].Name != "app-budget" {
			t.Fatalf("budgets = %v, want the one app-budget", pdbs)
		}
	})
}

// scaleLowering is a trait lowering rule that emits a scaler trait, as a
// consumer's own rule would. The scaler it emits reaches the bundle like an
// authored one.
type scaleLowering struct{ enablePDB bool }

func (scaleLowering) TraitType() string { return "scale-lowered" }

func (r scaleLowering) LowerTrait(_ *oam.Trait, _ oam.LoweringContext) (oam.LoweringResult, error) {
	props := map[string]any{"minReplicas": 2, "maxReplicas": 4}
	if r.enablePDB {
		props["enablePDB"] = true
		props["pdbName"] = "app-scaler-pdb"
	}
	return oam.LoweringResult{Traits: []oam.Trait{{Type: "scaler", Properties: props}}}, nil
}

func TestDisruption_LoweringEmittedScaler(t *testing.T) {
	budget := disruptionTrait(map[string]any{"minAvailable": 1, "pdbName": "app-budget"})
	lowered := oam.Trait{Type: "scale-lowered"}

	t.Run("a lowered scaler with a budget conflicts", func(t *testing.T) {
		tr := disruptionTransformer()
		tr.RegisterTraitLowering(scaleLowering{enablePDB: true})
		_, err := budgetsWith(t, tr, oam.TransformContext{}, disruptionComponent("app", "deployment", lowered, budget))
		wantRefused(t, err, "enablePDB", "disruption", "drop one")
	})

	t.Run("a lowered scaler without a budget does not", func(t *testing.T) {
		tr := disruptionTransformer()
		tr.RegisterTraitLowering(scaleLowering{})
		pdbs, err := budgetsWith(t, tr, oam.TransformContext{}, disruptionComponent("app", "deployment", lowered, budget))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 1 || pdbs[0].Name != "app-budget" {
			t.Fatalf("budgets = %v, want the one app-budget", pdbs)
		}
	})
}

// The scaler's target is the Deployment's name, which an author can override
// (deploymentObjectName); the component the budget selects is still "app". The check
// keys on the component, so the override must not hide the conflict.
func TestDisruption_ConflictSurvivesAWorkloadNameOverride(t *testing.T) {
	scaler := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4, "enablePDB": true, "pdbName": "app-scaler-pdb"})
	budget := disruptionTrait(map[string]any{"minAvailable": 1, "pdbName": "app-budget"})
	comp := disruptionComponent("app", "webservice", scaler, budget)
	comp.Properties["deploymentObjectName"] = "app-v2"

	_, err := disruptionBudgets(t, comp)
	wantRefused(t, err, "enablePDB", "disruption", "drop one")
}

// rivalDisruption is a consumer's own trait handler claiming the built-in's name.
type rivalDisruption struct{}

func (rivalDisruption) CanHandle(traitType string) bool { return traitType == "disruption" }

func (rivalDisruption) Apply(_ *oam.Trait, _ *stack.Application, _ *stack.Bundle) error { return nil }

// rivalDisruptionLowering is a consumer's own trait lowering rule claiming it.
type rivalDisruptionLowering struct{}

func (rivalDisruptionLowering) TraitType() string { return "disruption" }

func (rivalDisruptionLowering) LowerTrait(_ *oam.Trait, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{}, nil
}

func wantPanic(t *testing.T, part string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want one containing %q", part)
		}
		if msg, _ := r.(string); !strings.Contains(msg, part) {
			t.Fatalf("panic %v does not contain %q", r, part)
		}
	}()
	f()
}

// The name is the built-in's: a consumer handler or lowering rule with the same
// type is refused at registration, and a CapabilityDefinition with the same name is
// not applied to the built-in's rendering.
func TestDisruption_NameIsTheBuiltins(t *testing.T) {
	t.Run("a consumer trait handler", func(t *testing.T) {
		wantPanic(t, "already registered", func() { disruptionTransformer().RegisterTrait("disruption", rivalDisruption{}) })
	})

	t.Run("a consumer trait lowering rule", func(t *testing.T) {
		wantPanic(t, "already a dispatchable trait handler", func() { disruptionTransformer().RegisterTraitLowering(rivalDisruptionLowering{}) })
	})

	t.Run("a capability definition is not applied", func(t *testing.T) {
		tr := disruptionTransformer()
		tr.SetCapabilityDefs(map[string]*oam.CapabilityDefinition{
			"disruption": {
				Metadata: oam.Metadata{Name: "disruption"},
				Spec: oam.CapabilityDefSpec{Rendering: oam.CapabilityRenderingSchema{
					Properties: map[string]oam.PropertySchema{"timeout": {Type: "integer", Required: true}},
				}},
			},
		})
		profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: map[string]oam.CapabilityBinding{
			"disruption": {Rendering: map[string]any{"unhealthyPodEvictionPolicy": "AlwaysAllow"}},
		}}}
		evaluated, err := tr.EvaluateProfile(profile)
		if err != nil {
			t.Fatalf("EvaluateProfile applied the definition to the built-in: %v", err)
		}
		pdbs, err := budgetsWith(t, tr, oam.TransformContext{Capabilities: evaluated.Spec.Capabilities},
			disruptionComponent("app", "deployment", disruptionTrait(map[string]any{"minAvailable": 1})))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 1 {
			t.Fatalf("generated %d budgets, want 1", len(pdbs))
		}
		if got := pdbs[0].Spec.UnhealthyPodEvictionPolicy; got == nil || string(*got) != "AlwaysAllow" {
			t.Errorf("unhealthyPodEvictionPolicy = %v, want the rendered AlwaysAllow", got)
		}
	})
}
