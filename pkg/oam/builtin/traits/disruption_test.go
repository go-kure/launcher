package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The disruption trait generates a PodDisruptionBudget for a workload without an
// autoscaler (go-kure/launcher#1266). These tests run the whole transformer with
// every built-in registered, the way the CLI does, because the trait finds the
// scaler of its component in the bundle at Generate and every application's
// config is wrapped (ownership) by then.

// opaqueScalerWrapperTrait is a consumer's trait that wraps the scaler's config in
// a wrapper that is no oam.ConfigWrapper, so oam.UnwrapConfig stops at it.
type opaqueScalerWrapperTrait struct{}

func (opaqueScalerWrapperTrait) CanHandle(traitType string) bool {
	return traitType == "opaque-wrap-scaler"
}

func (opaqueScalerWrapperTrait) DecoratesSubApplications() bool { return true }

func (opaqueScalerWrapperTrait) Apply(_ *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	if _, ok := oam.UnwrapConfig(app.Config).(interface{ ScaledReplicas() (string, int32) }); ok {
		app.Config = &opaqueScalerConfig{inner: app.Config}
	}
	return nil
}

type opaqueScalerConfig struct{ inner stack.ApplicationConfig }

func (o *opaqueScalerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return o.inner.Generate(app)
}

func disruptionTransformer() *oam.Transformer {
	tr := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		tr.RegisterBuiltinComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		tr.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		tr.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		tr.RegisterBuiltinTraitLowering(r)
	}
	tr.RegisterBuiltinTrait("wrap-scaler", scalerWrapperTrait{})
	tr.RegisterBuiltinTrait("opaque-wrap-scaler", opaqueScalerWrapperTrait{})
	return tr
}

func disruptionComponent(name, kind string, trs ...oam.Trait) oam.Component {
	return oam.Component{Name: name, Type: kind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: trs}
}

func disruptionTrait(props map[string]any) oam.Trait {
	return oam.Trait{Type: "disruption", Properties: props}
}

// disruptionBudgets transforms the components and returns every PodDisruptionBudget
// they generate, or the first error from the transform or from a Generate.
func disruptionBudgets(t *testing.T, comps ...oam.Component) ([]*policyv1.PodDisruptionBudget, error) {
	t.Helper()
	return budgetsWith(t, disruptionTransformer(), oam.TransformContext{}, comps...)
}

// budgetsWith is disruptionBudgets on a transformer and a transform context the
// caller built: a profile's capabilities, or a lowering rule added to the registry.
func budgetsWith(t *testing.T, tr *oam.Transformer, ctx oam.TransformContext, comps ...oam.Component) ([]*policyv1.PodDisruptionBudget, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	cluster, err := tr.Transform(app, ctx)
	if err != nil {
		return nil, err
	}
	var pdbs []*policyv1.PodDisruptionBudget
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					return err
				}
				for _, o := range objs {
					if pdb, ok := (*o).(*policyv1.PodDisruptionBudget); ok {
						pdbs = append(pdbs, pdb)
					}
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(cluster.Node); err != nil {
		return nil, err
	}
	return pdbs, nil
}

func wantRefused(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("accepted, want an error containing %q", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("error %q does not contain %q", err, p)
		}
	}
}

// TestDisruption_GeneratesABudgetOnEveryAdmittedKind: one PodDisruptionBudget
// named <component>-pdb, selecting the component's pods, with the authored budget.
func TestDisruption_GeneratesABudgetOnEveryAdmittedKind(t *testing.T) {
	for _, kind := range []string{"webservice", "worker", "deployment", "statefulset"} {
		t.Run(kind+"/minAvailable", func(t *testing.T) {
			pdbs, err := disruptionBudgets(t, disruptionComponent("app", kind, disruptionTrait(map[string]any{"minAvailable": 1})))
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(pdbs) != 1 {
				t.Fatalf("generated %d PodDisruptionBudgets, want 1", len(pdbs))
			}
			pdb := pdbs[0]
			if pdb.Name != "app-pdb" || pdb.Namespace != "default" {
				t.Errorf("budget is %s/%s, want default/app-pdb", pdb.Namespace, pdb.Name)
			}
			if got := pdb.Spec.MinAvailable; got == nil || *got != intstr.FromInt32(1) {
				t.Errorf("minAvailable = %v, want 1", got)
			}
			if pdb.Spec.MaxUnavailable != nil {
				t.Errorf("maxUnavailable = %v, want unset", pdb.Spec.MaxUnavailable)
			}
			if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels["app"] != "app" {
				t.Errorf("selector = %v, want app=app", pdb.Spec.Selector)
			}
		})
		t.Run(kind+"/maxUnavailable", func(t *testing.T) {
			pdbs, err := disruptionBudgets(t, disruptionComponent("app", kind, disruptionTrait(map[string]any{
				"maxUnavailable": "25%", "unhealthyPodEvictionPolicy": "AlwaysAllow", "pdbName": "app-budget",
			})))
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(pdbs) != 1 {
				t.Fatalf("generated %d PodDisruptionBudgets, want 1", len(pdbs))
			}
			pdb := pdbs[0]
			if pdb.Name != "app-budget" {
				t.Errorf("name = %q, want app-budget", pdb.Name)
			}
			if got := pdb.Spec.MaxUnavailable; got == nil || *got != intstr.FromString("25%") {
				t.Errorf("maxUnavailable = %v, want 25%%", got)
			}
			if pdb.Spec.MinAvailable != nil {
				t.Errorf("minAvailable = %v, want unset", pdb.Spec.MinAvailable)
			}
			if got := pdb.Spec.UnhealthyPodEvictionPolicy; got == nil || *got != policyv1.AlwaysAllow {
				t.Errorf("unhealthyPodEvictionPolicy = %v, want AlwaysAllow", got)
			}
		})
	}
}

func TestDisruption_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		props map[string]any
		want  string
	}{
		{"neither budget", "deployment", map[string]any{}, "minAvailable"},
		{"both budgets", "deployment", map[string]any{"minAvailable": 1, "maxUnavailable": 1}, "minAvailable"},
		{"percentage over 100", "deployment", map[string]any{"minAvailable": "150%"}, "150%"},
		{"not a percentage", "deployment", map[string]any{"minAvailable": "half"}, "half"},
		{"negative count", "deployment", map[string]any{"maxUnavailable": -1}, "maxUnavailable"},
		{"policy value", "deployment", map[string]any{"minAvailable": 1, "unhealthyPodEvictionPolicy": "Never"}, "unhealthyPodEvictionPolicy"},
		{"unknown key", "deployment", map[string]any{"minAvailable": 1, "selector": map[string]any{}}, "selector"},
		{"empty pdbName", "deployment", map[string]any{"minAvailable": 1, "pdbName": ""}, "pdbName"},
		{"daemonset", "daemonset", map[string]any{"minAvailable": 1}, "not supported"},
		{"job", "job", map[string]any{"minAvailable": 1}, "not supported"},
		{"cronjob", "cronjob", map[string]any{"minAvailable": 1}, "not supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := disruptionBudgets(t, disruptionComponent("app", c.kind, disruptionTrait(c.props)))
			wantRefused(t, err, c.want)
		})
	}
}

// TestDisruption_AndScalerPDB: the scaler's enablePDB and the disruption
// trait on one component are refused in either trait order, with distinct names
// so the refusal is the conflict rule and not the name allocator's.
func TestDisruption_AndScalerPDB(t *testing.T) {
	scaler := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4, "enablePDB": true, "pdbName": "app-scaler-pdb"})
	budget := disruptionTrait(map[string]any{"minAvailable": 1, "pdbName": "app-budget"})
	for _, order := range []struct {
		name string
		trs  []oam.Trait
	}{
		{"scaler first", []oam.Trait{scaler, budget}},
		{"disruption first", []oam.Trait{budget, scaler}},
		{"scaler wrapped", []oam.Trait{scaler, {Type: "wrap-scaler"}, budget}},
		{"disruption first, scaler wrapped", []oam.Trait{budget, scaler, {Type: "wrap-scaler"}}},
	} {
		t.Run(order.name, func(t *testing.T) {
			_, err := disruptionBudgets(t, disruptionComponent("app", "deployment", order.trs...))
			// "enablePDB" is in the conflict message only: the unknown-type error
			// of a trait that does not exist lists "scaler" and "disruption" too.
			wantRefused(t, err, "enablePDB", "disruption", "drop one")
		})
	}

	t.Run("scaler without a budget is allowed", func(t *testing.T) {
		plain := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4})
		pdbs, err := disruptionBudgets(t, disruptionComponent("app", "deployment", plain, budget))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 1 || pdbs[0].Name != "app-budget" {
			t.Fatalf("budgets = %v, want the one app-budget", pdbs)
		}
	})

	t.Run("a scaler budget on another component does not count", func(t *testing.T) {
		pdbs, err := disruptionBudgets(t,
			disruptionComponent("a", "deployment", budget),
			disruptionComponent("b", "deployment", scaler),
		)
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 2 {
			t.Fatalf("generated %d budgets, want 2 (one per component)", len(pdbs))
		}
	})

	// A consumer wrapper that is no oam.ConfigWrapper hides the scaler's config
	// from oam.UnwrapConfig, so the conflict is not seen: a stated limit of the
	// check, pinned here so a change of behaviour shows.
	t.Run("opaque wrapper is not detected", func(t *testing.T) {
		pdbs, err := disruptionBudgets(t, disruptionComponent("app", "deployment", scaler, budget, oam.Trait{Type: "opaque-wrap-scaler"}))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(pdbs) != 2 {
			t.Fatalf("generated %d budgets, want 2: the limit changed", len(pdbs))
		}
	})
}

// A config built by hand, or changed after it was parsed, is checked at Generate with
// the parser's own refusals, as the scaler's is (go-kure/launcher#1278): without it
// the trait emits a PodDisruptionBudget the API server then refuses, or one that
// protects nothing.
func TestDisruptionConfig_Generate_RefusesWhatTheParserRefuses(t *testing.T) {
	budget := func(v intstr.IntOrString) *intstr.IntOrString { return &v }
	policy := func(s string) *policyv1.UnhealthyPodEvictionPolicyType {
		p := policyv1.UnhealthyPodEvictionPolicyType(s)
		return &p
	}
	cases := []struct {
		name string
		cfg  traits.DisruptionConfig
		want []string
	}{
		{"neither budget", traits.DisruptionConfig{}, []string{"disruption: set minAvailable or maxUnavailable; the trait has no default budget"}},
		{"both budgets", traits.DisruptionConfig{MinAvailable: budget(intstr.FromInt32(1)), MaxUnavailable: budget(intstr.FromInt32(1))},
			[]string{"disruption: set minAvailable or maxUnavailable, not both"}},
		{"percentage over 100", traits.DisruptionConfig{MinAvailable: budget(intstr.FromString("150%"))}, []string{"minAvailable", "150%"}},
		{"not a percentage", traits.DisruptionConfig{MaxUnavailable: budget(intstr.FromString("half"))}, []string{"maxUnavailable", "half"}},
		{"negative count", traits.DisruptionConfig{MaxUnavailable: budget(intstr.FromInt32(-1))}, []string{"maxUnavailable"}},
		{"minAvailable type tag", traits.DisruptionConfig{MinAvailable: budget(intstr.IntOrString{Type: 2, IntVal: 1})}, []string{"minAvailable", "invalid IntOrString type"}},
		{"maxUnavailable type tag", traits.DisruptionConfig{MaxUnavailable: budget(intstr.IntOrString{Type: 2, IntVal: 1})}, []string{"maxUnavailable", "invalid IntOrString type"}},
		{"eviction policy", traits.DisruptionConfig{MinAvailable: budget(intstr.FromInt32(1)), UnhealthyPodEvictionPolicy: policy("Never")},
			[]string{"unhealthyPodEvictionPolicy must be", "Never"}},
		{"pdb name", traits.DisruptionConfig{MinAvailable: budget(intstr.FromInt32(1)), PDBName: "Not_A_Name"}, []string{"pdbName", "Not_A_Name"}},
		// A config built by hand has no component name, so the default name it falls
		// back to is "-pdb", which the API server refuses.
		{"pdb name omitted, no component name", traits.DisruptionConfig{MinAvailable: budget(intstr.FromInt32(1))}, []string{"pdbName", "-pdb"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			_, err := cfg.Generate(newApp("app", "default"))
			wantRefused(t, err, c.want...)
		})
	}

	t.Run("a valid hand-built config generates", func(t *testing.T) {
		cfg := traits.DisruptionConfig{
			MinAvailable:               budget(intstr.FromString("50%")),
			UnhealthyPodEvictionPolicy: policy(string(policyv1.AlwaysAllow)),
			PDBName:                    "app-budget",
		}
		objs, err := cfg.Generate(newApp("app", "default"))
		if err != nil || len(objs) != 1 {
			t.Fatalf("Generate = %d objects, %v; want one PodDisruptionBudget", len(objs), err)
		}
	})

	t.Run("a parsed config changed afterwards", func(t *testing.T) {
		bundle := newBundle()
		trait := &oam.Trait{Type: "disruption", Properties: map[string]any{"minAvailable": 1}}
		if err := (&traits.DisruptionHandler{}).Apply(trait, newApp("app", "default"), bundle); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		cfg, ok := bundle.Applications[0].Config.(*traits.DisruptionConfig)
		if !ok {
			t.Fatalf("the sub-application config is %T, want *DisruptionConfig", bundle.Applications[0].Config)
		}
		cfg.MaxUnavailable = budget(intstr.FromInt32(1))
		_, err := bundle.Applications[0].Generate()
		wantRefused(t, err, "not both")
	})
}
