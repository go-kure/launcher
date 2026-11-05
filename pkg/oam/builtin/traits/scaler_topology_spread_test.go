package traits_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// Under a scaler the HorizontalPodAutoscaler owns the replica count, so the
// Deployment has no spec.replicas and the topology-spread trait found no count
// to spread by (go-kure/launcher#1196). The trait takes the scaler's effective
// minReplicas instead. These tests run the whole transformer, because the
// scaler is a sub-application the trait finds in the bundle it was applied to,
// and because every application's config is wrapped (ownership) by the time
// Generate runs.

// spreadScalerTransformer registers what the tests below need. spread is the
// handler registered for "topology-spread".
func spreadScalerTransformer(spread oam.TraitHandler) *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment":     &components.DeploymentHandler{},
		"service":        &components.ServiceHandler{},
		"serviceaccount": &components.ServiceAccountHandler{},
		"manifests":      &components.ManifestsHandler{},
	}, nil)
	tr.RegisterComponentLowering(components.WebserviceRule{})
	tr.RegisterComponentLowering(components.WorkerRule{})
	tr.RegisterBuiltinTrait("topology-spread", spread)
	tr.RegisterBuiltinTrait("pvc", &traits.PVCHandler{})
	tr.RegisterBuiltinTrait("scaler", &traits.ScalerHandler{})
	tr.RegisterBuiltinTrait("force-replace", &traits.ForceReplaceHandler{})
	tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
	tr.RegisterBuiltinTrait("wrap-scaler", scalerWrapperTrait{})
	return tr
}

func transformSpreadScaler(spread oam.TraitHandler, kind string, trs ...oam.Trait) (*stack.Cluster, error) {
	return transformSpreadScalerIn(spread, nil, []oam.Component{{
		Name: "app", Type: kind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: trs,
	}})
}

func transformSpreadScalerIn(spread oam.TraitHandler, policy oam.Policy, comps []oam.Component) (*stack.Cluster, error) {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	return spreadScalerTransformer(spread).Transform(app, oam.TransformContext{Policy: policy})
}

// scalerWrapperTrait is a trait of a consumer's own that wraps the config of the
// scaler sub-application, saying so (oam.ConfigWrapper). No built-in trait does
// that, so it stands for one that decorates a scaler's config.
type scalerWrapperTrait struct{}

func (scalerWrapperTrait) CanHandle(traitType string) bool { return traitType == "wrap-scaler" }

func (scalerWrapperTrait) DecoratesSubApplications() bool { return true }

func (scalerWrapperTrait) Apply(_ *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	if _, ok := app.Config.(*traits.ScalerConfig); ok {
		app.Config = &wrappedScalerConfig{inner: app.Config}
	}
	return nil
}

type wrappedScalerConfig struct{ inner stack.ApplicationConfig }

func (w *wrappedScalerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return w.inner.Generate(app)
}

func (w *wrappedScalerConfig) WrappedApplicationConfig() stack.ApplicationConfig { return w.inner }

// wrapDepth is the number of config wrappers around cfg.
func wrapDepth(cfg stack.ApplicationConfig) int {
	n := 0
	for {
		w, ok := cfg.(oam.ConfigWrapper)
		if !ok {
			return n
		}
		n++
		cfg = w.WrappedApplicationConfig()
	}
}

// scalerApplication returns the application whose config, unwrapped, is the
// scaler's.
func scalerApplication(t *testing.T, cluster *stack.Cluster) *stack.Application {
	t.Helper()
	var found *stack.Application
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				if _, ok := oam.UnwrapConfig(a.Config).(*traits.ScalerConfig); ok {
					found = a
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	if found == nil {
		t.Fatal("no application holds a ScalerConfig under its wrappers")
	}
	return found
}

// wantSpread is the constraints for the requested pod count: BuildTopologySpreadConstraints,
// reduced to what the assertion compares.
func wantSpread(pods int32) []string {
	switch {
	case pods <= 1:
		return nil
	case pods == 2:
		return []string{"kubernetes.io/hostname/" + string(corev1.DoNotSchedule)}
	default:
		return []string{
			"kubernetes.io/hostname/" + string(corev1.DoNotSchedule),
			"topology.kubernetes.io/zone/" + string(corev1.ScheduleAnyway),
		}
	}
}

func gotSpread(cs []corev1.TopologySpreadConstraint) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.TopologyKey+"/"+string(c.WhenUnsatisfiable))
	}
	return out
}

// TestRed1196_SpreadFollowsTheScalersMinimum: a Deployment a scaler scales gets
// the constraints of the scaler's minReplicas, whichever way the scaler is
// authored and in whichever order the traits are declared. "wrapped" has a
// consumer's trait wrap the scaler's config; "delivery" has force-replace and
// prune-protection decorate it (they set a delivery intent and leave the config
// as it is). The ownership wrapper is around every case, so no case hands the
// trait a bare ScalerConfig.
func TestRed1196_SpreadFollowsTheScalersMinimum(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	variants := []struct {
		name  string
		extra []oam.Trait
		depth int // config wrappers around the scaler's application config, at least
	}{
		{"plain", nil, 1},
		{"wrapped", []oam.Trait{{Type: "wrap-scaler"}}, 2},
		{"delivery", []oam.Trait{{Type: "force-replace"}, {Type: "prune-protection"}}, 1},
	}
	for _, kind := range []string{"deployment", "webservice", "worker"} {
		for _, minReplicas := range []int32{2, 3} {
			for _, v := range variants {
				for _, spreadFirst := range []bool{false, true} {
					// webservice and worker synthesize their own topology-spread
					// trait, and a second one is refused (the Deployment would carry
					// constraints twice), so only the deployment kind authors it.
					if kind != "deployment" && spreadFirst {
						continue
					}
					name := fmt.Sprintf("%s/min%d/%s/spreadFirst=%v", kind, minReplicas, v.name, spreadFirst)
					t.Run(name, func(t *testing.T) {
						scaler := scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})
						trs := append([]oam.Trait{scaler}, v.extra...)
						switch {
						case kind != "deployment":
						case spreadFirst:
							trs = append([]oam.Trait{spread}, trs...)
						default:
							trs = append(trs, spread)
						}
						cluster, err := transformSpreadScaler(&traits.TopologySpreadHandler{}, kind, trs...)
						if err != nil {
							t.Fatalf("transform: %v", err)
						}

						// The fixture is what it says: the scaler's application config is wrapped.
						if got := wrapDepth(scalerApplication(t, cluster).Config); got < v.depth {
							t.Fatalf("scaler application has %d config wrappers, want at least %d: the fixture does not hand the trait a wrapped scaler", got, v.depth)
						}

						dep := oneDeployment(t, cluster)
						if dep.Spec.Replicas != nil {
							t.Fatalf("spec.replicas = %d under a scaler, want it unset", *dep.Spec.Replicas)
						}
						got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints)
						want := wantSpread(minReplicas)
						if fmt.Sprint(got) != fmt.Sprint(want) {
							t.Errorf("spread constraints = %v, want %v (scaler minReplicas %d)", got, want, minReplicas)
						}
					})
				}
			}
		}
	}
}

// TestScalerSpread_MinimumAndPolicy: the constraints follow the scaler's minimum
// whatever the environment policy default is (the policy default no longer
// reaches a scaled Deployment). A minimum of 1 gives none.
func TestScalerSpread_MinimumAndPolicy(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	three := int32(3)
	for policyName, policy := range map[string]oam.Policy{"no policy": nil, "policy default 3": &replicasPolicy{defaultReplicas: &three}} {
		for _, minReplicas := range []int32{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/min%d", policyName, minReplicas), func(t *testing.T) {
				scaler := scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})
				cluster, err := transformSpreadScalerIn(&traits.TopologySpreadHandler{}, policy, []oam.Component{{
					Name: "app", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
					Traits: []oam.Trait{scaler, spread},
				}})
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				got := gotSpread(oneDeployment(t, cluster).Spec.Template.Spec.TopologySpreadConstraints)
				if want := wantSpread(minReplicas); fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("spread constraints = %v, want %v", got, want)
				}
			})
		}
	}
}

// TestScalerSpread_AScalerScalesOnlyItsOwnDeployment: another component's
// Deployment keeps the spread of its own count; a Deployment no scaler scales
// is unchanged.
func TestScalerSpread_AScalerScalesOnlyItsOwnDeployment(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	props := func(extra map[string]any) map[string]any {
		out := map[string]any{"image": "ghcr.io/org/app:v1"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cluster, err := transformSpreadScalerIn(&traits.TopologySpreadHandler{}, nil, []oam.Component{
		{Name: "app", Type: "deployment", Properties: props(nil),
			Traits: []oam.Trait{scalerTrait(map[string]any{"minReplicas": 3, "maxReplicas": 5}), spread}},
		{Name: "other", Type: "deployment", Properties: props(nil), Traits: []oam.Trait{spread}},
		{Name: "plain", Type: "deployment", Properties: props(map[string]any{"replicas": 2}), Traits: []oam.Trait{spread}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	want := map[string][]string{
		"app":   wantSpread(3), // the scaler's minimum
		"other": nil,           // its own unset count reads as 1
		"plain": wantSpread(2), // its own count
	}
	deps := generatedDeployments(t, cluster)
	if len(deps) != len(want) {
		t.Fatalf("generated %d Deployments, want %d", len(deps), len(want))
	}
	for _, dep := range deps {
		w, ok := want[dep.Name]
		if !ok {
			t.Errorf("unexpected Deployment %q", dep.Name)
			continue
		}
		if got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints); fmt.Sprint(got) != fmt.Sprint(w) {
			t.Errorf("Deployment %s: spread constraints = %v, want %v", dep.Name, got, w)
		}
	}
}

// TestScalerSpread_ASameNamedDeploymentInAnotherNamespaceIsNotScaled: a
// manifests source keeps the namespace its object names, and a scaler's
// HorizontalPodAutoscaler finds its target in the scaler's own namespace. A
// Deployment of the same name in another namespace is therefore not scaled, and
// spreads by its own replicas.
func TestScalerSpread_ASameNamedDeploymentInAnotherNamespaceIsNotScaled(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	inline := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: other\nspec:\n  replicas: 1\n" +
		"  selector:\n    matchLabels:\n      app: web\n" +
		"  template:\n    metadata:\n      labels:\n        app: web\n" +
		"    spec:\n      containers:\n      - name: web\n        image: ghcr.io/org/web:v1\n"
	cluster, err := transformSpreadScalerIn(&traits.TopologySpreadHandler{}, nil, []oam.Component{
		{Name: "web", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/web:v1"},
			Traits: []oam.Trait{scalerTrait(map[string]any{"minReplicas": 3, "maxReplicas": 5}), spread}},
		{Name: "elsewhere", Type: "manifests", Properties: map[string]any{"inline": inline},
			Traits: []oam.Trait{spread}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	want := map[string][]string{
		"default": wantSpread(3), // the scaled Deployment: the scaler's minimum
		"other":   nil,           // same name, other namespace: its own count of 1
	}
	deps := generatedDeployments(t, cluster)
	if len(deps) != len(want) {
		t.Fatalf("generated %d Deployments, want %d", len(deps), len(want))
	}
	for _, dep := range deps {
		w, ok := want[dep.Namespace]
		if !ok {
			t.Errorf("unexpected Deployment %s/%s", dep.Namespace, dep.Name)
			continue
		}
		if got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints); fmt.Sprint(got) != fmt.Sprint(w) {
			t.Errorf("Deployment %s/%s: spread constraints = %v, want %v", dep.Namespace, dep.Name, got, w)
		}
	}
}

// TestScalerSpread_RefusesNonMatchLabelsSelectorUnderAScalerAtMinOne: the
// selector check runs before the count is consulted, also when the count is the
// scaler's minReplicas. A scaler with a minimum of 1 makes the trait emit no
// constraints, so a check gated on the count would let this document build
// where a minimum of 2 or more refuses it. The manifests Deployment is named
// after the scaled component and sits in its namespace, so the scaler matches
// it. Two controls make the refusal the selector's: the same Deployment with a
// matchLabels selector spreads by the scaler's minimum, which shows the scaler
// matches, and builds without constraints at a minimum of 1.
func TestScalerSpread_RefusesNonMatchLabelsSelectorUnderAScalerAtMinOne(t *testing.T) {
	const matchLabels = "  selector:\n    matchLabels:\n      app: web\n"
	const matchExpressions = "  selector:\n    matchExpressions:\n    - {key: app, operator: In, values: [web]}\n"
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	// generate transforms the document and runs Generate on every application,
	// which is where the trait refuses; it returns the first error.
	generate := func(selector string, minReplicas int) (*appsv1.Deployment, error) {
		inline := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n" + selector +
			"  template:\n    metadata:\n      labels:\n        app: web\n" +
			"    spec:\n      containers:\n      - name: web\n        image: ghcr.io/org/web:v1\n"
		cluster, err := transformSpreadScalerIn(&traits.TopologySpreadHandler{}, nil, []oam.Component{
			{Name: "web", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/web:v1"},
				Traits: []oam.Trait{scalerTrait(map[string]any{"minReplicas": minReplicas, "maxReplicas": 5})}},
			{Name: "adopted", Type: "manifests", Properties: map[string]any{"inline": inline},
				Traits: []oam.Trait{spread}},
		})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		var adopted *appsv1.Deployment
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
						if dep, ok := (*o).(*appsv1.Deployment); ok && a.Name == "adopted" {
							adopted = dep
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
		// walk fills adopted, so it runs before adopted is read.
		err = walk(cluster.Node)
		return adopted, err
	}

	for _, minReplicas := range []int{1, 3} {
		dep, err := generate(matchLabels, minReplicas)
		if err != nil {
			t.Fatalf("control (matchLabels selector, minimum %d): %v", minReplicas, err)
		}
		if dep == nil {
			t.Fatalf("control (minimum %d): the adopted application generated no Deployment", minReplicas)
		}
		want := wantSpread(int32(minReplicas))
		if got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("control (minimum %d): spread constraints = %v, want %v", minReplicas, got, want)
		}
	}

	_, err := generate(matchExpressions, 1)
	if err == nil {
		t.Fatal("matchExpressions selector under a scaler with minReplicas 1: expected an error, got none")
	}
	if !strings.Contains(err.Error(), "matchLabels") {
		t.Errorf("error should name the matchLabels requirement, got: %v", err)
	}
}

// TestScalerSpread_TwoScalersOnOneComponentAreRefused pins what the trait relies
// on: it takes the one scaler of a Deployment, because the transformer refuses a
// second scaler on the component. Both default names collide, the scaler's
// sub-application ("app-scaler") and its HorizontalPodAutoscaler ("app-hpa"),
// and the scaler has no property to name the sub-application.
func TestScalerSpread_TwoScalersOnOneComponentAreRefused(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	first := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 5, "hpaName": "app-hpa-two"})
	second := scalerTrait(map[string]any{"minReplicas": 3, "maxReplicas": 6, "hpaName": "app-hpa-three"})
	_, err := transformSpreadScaler(&traits.TopologySpreadHandler{}, "deployment", first, second, spread)
	if err == nil || !strings.Contains(err.Error(), "name collision") || !strings.Contains(err.Error(), `"app-scaler"`) {
		t.Errorf("two scalers on one component: err = %v, want a name collision on the sub-application \"app-scaler\"", err)
	}
}

// TestScalerSpread_AuthoredSpreadBesideAScalerOnARoleKind: webservice and worker
// synthesize their own topology-spread trait, which now spreads by the scaler's
// minimum. An authored topology-spread trait beside it is therefore refused at a
// minimum of 2 or more, with the message it gives without a scaler at 2 or more
// replicas. Before go-kure/launcher#1196 the synthesized trait applied nothing
// under a scaler, so this combination was admitted. At a minimum of 1 nothing is
// carried and the authored trait is still admitted.
func TestScalerSpread_AuthoredSpreadBesideAScalerOnARoleKind(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	for _, kind := range []string{"webservice", "worker"} {
		for _, minReplicas := range []int32{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/min%d", kind, minReplicas), func(t *testing.T) {
				scaler := scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})
				cluster, err := transformSpreadScaler(&traits.TopologySpreadHandler{}, kind, scaler, spread)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				err = generateErr(cluster)
				if minReplicas == 1 {
					if err != nil {
						t.Errorf("Generate at minReplicas 1: %v, want the authored trait admitted", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "already carries") {
					t.Errorf("Generate at minReplicas %d: err = %v, want the \"already carries\" refusal", minReplicas, err)
				}
			})
		}
	}
}

// recordingSpread is the topology-spread handler with a record of the bundle it
// was applied to.
type recordingSpread struct {
	*traits.TopologySpreadHandler
	bundle *stack.Bundle
}

func (r *recordingSpread) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	r.bundle = bundle
	return r.TopologySpreadHandler.Apply(trait, app, bundle)
}

// TestScalerSpread_BundleHeldAtApplyIsTheBundleAtGenerate guards the trait's
// reliance on the bundle it is handed in Apply: that pointer must be the bundle
// of the cluster node, not a copy, and must hold the scaler's application once
// the transform is done, whichever trait is declared first.
func TestScalerSpread_BundleHeldAtApplyIsTheBundleAtGenerate(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	scaler := scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 5})
	for name, trs := range map[string][]oam.Trait{
		"scaler first": {scaler, spread},
		"spread first": {spread, scaler},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordingSpread{TopologySpreadHandler: &traits.TopologySpreadHandler{}}
			cluster, err := transformSpreadScaler(rec, "deployment", trs...)
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if rec.bundle == nil {
				t.Fatal("Apply was not called")
			}
			var node *stack.Bundle
			var walk func(n *stack.Node)
			walk = func(n *stack.Node) {
				if n == nil {
					return
				}
				if n.Bundle == rec.bundle {
					node = n.Bundle
				}
				for _, c := range n.Children {
					walk(c)
				}
			}
			walk(cluster.Node)
			if node == nil {
				t.Fatal("the bundle handed to Apply is not a bundle of the cluster: it was copied before Generate")
			}
			found := false
			for _, a := range rec.bundle.Applications {
				if _, ok := oam.UnwrapConfig(a.Config).(*traits.ScalerConfig); ok {
					found = true
				}
			}
			if !found {
				t.Errorf("the bundle handed to Apply holds no scaler application at Generate (%d applications)", len(rec.bundle.Applications))
			}
		})
	}
}
