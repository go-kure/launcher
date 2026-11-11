package traits_test

import (
	"fmt"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The tests below hold one invariant that the scaler and topology-spread
// traits share: under a scaler, the Deployment spreads by the scaler's
// minReplicas, after the environment policy ran. The same count holds in either
// trait order, an adopted Deployment's own count is ignored, and a scaler that
// scales another Deployment does not count. The README of this package states
// it (the scaler and topology-spread sections). Each test runs the invariant
// through every public entry point of the transformer. Transform and
// TransformWithPolicy share one body, and TransformWithProvenance wraps
// TransformWithPolicy with a recorder, so the rows guard that the wrappers do
// not change the spread. They do not test three separate code paths.

// spreadEntry is one public way into the transformer.
type spreadEntry struct {
	name string
	run  func(*oam.Transformer, *oam.Application, oam.TransformContext) (*stack.Cluster, error)
}

func spreadEntryPoints() []spreadEntry {
	return []spreadEntry{
		{"Transform", func(tr *oam.Transformer, app *oam.Application, ctx oam.TransformContext) (*stack.Cluster, error) {
			return tr.Transform(app, ctx)
		}},
		{"TransformWithPolicy", func(tr *oam.Transformer, app *oam.Application, ctx oam.TransformContext) (*stack.Cluster, error) {
			cluster, _, err := tr.TransformWithPolicy(app, ctx)
			return cluster, err
		}},
		{"TransformWithProvenance", func(tr *oam.Transformer, app *oam.Application, ctx oam.TransformContext) (*stack.Cluster, error) {
			cluster, _, _, err := tr.TransformWithProvenance(app, ctx)
			return cluster, err
		}},
	}
}

func interactionApp(comps []oam.Component) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
}

// adoptedDeployment returns the Deployment that the application named "adopted"
// generates, which is the one a manifests component carries.
func adoptedDeployment(t *testing.T, cluster *stack.Cluster) *appsv1.Deployment {
	t.Helper()
	var found *appsv1.Deployment
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				if a.Name != "adopted" {
					continue
				}
				objs, err := a.Generate()
				if err != nil {
					t.Fatalf("Generate %s: %v", a.Name, err)
				}
				for _, o := range objs {
					if dep, ok := (*o).(*appsv1.Deployment); ok {
						found = dep
					}
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	if found == nil {
		t.Fatal("the adopted application generated no Deployment")
	}
	return found
}

// TestInteraction_SpreadFollowsTheScalersMinimumOnEveryEntryPoint: a scaled
// Deployment spreads by the scaler's minReplicas and has no spec.replicas, for
// each kind that builds a Deployment and, for the deployment kind, in either
// trait order. A webservice and a worker synthesize their own topology-spread
// trait, so they do not author one.
func TestInteraction_SpreadFollowsTheScalersMinimumOnEveryEntryPoint(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	for _, entry := range spreadEntryPoints() {
		for _, kind := range []string{"deployment", "webservice", "worker"} {
			for _, minReplicas := range []int32{1, 2, 3} {
				for _, spreadFirst := range []bool{false, true} {
					if kind != "deployment" && spreadFirst {
						continue
					}
					name := fmt.Sprintf("%s/%s/min%d/spreadFirst=%v", entry.name, kind, minReplicas, spreadFirst)
					t.Run(name, func(t *testing.T) {
						trs := []oam.Trait{scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})}
						switch {
						case kind != "deployment":
						case spreadFirst:
							trs = append([]oam.Trait{spread}, trs...)
						default:
							trs = append(trs, spread)
						}
						app := interactionApp([]oam.Component{{
							Name: "app", Type: kind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: trs,
						}})
						cluster, err := entry.run(spreadScalerTransformer(&traits.TopologySpreadHandler{}), app, oam.TransformContext{})
						if err != nil {
							t.Fatalf("transform: %v", err)
						}
						dep := oneDeployment(t, cluster)
						if dep.Spec.Replicas != nil {
							t.Fatalf("spec.replicas = %d under a scaler, want it unset", *dep.Spec.Replicas)
						}
						got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints)
						if want := wantSpread(minReplicas); fmt.Sprint(got) != fmt.Sprint(want) {
							t.Errorf("spread constraints = %v, want %v (scaler minReplicas %d)", got, want, minReplicas)
						}
					})
				}
			}
		}
	}
}

// TestInteraction_PolicyDefaultDoesNotReachAScaledDeployment: an environment
// policy that defaults replicas to 3 does not change the spread of a scaled
// Deployment. A minimum of 1 gives no constraints, and a minimum of 2 gives the
// hostname constraint only.
func TestInteraction_PolicyDefaultDoesNotReachAScaledDeployment(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	three := int32(3)
	// Every kind that builds a Deployment and, for the deployment kind, both
	// trait orders: a webservice and a worker synthesize their own spread trait.
	for _, entry := range spreadEntryPoints() {
		for _, kind := range []string{"deployment", "webservice", "worker"} {
			for _, spreadFirst := range []bool{false, true} {
				if kind != "deployment" && spreadFirst {
					continue
				}
				for _, minReplicas := range []int32{1, 2} {
					name := fmt.Sprintf("%s/%s/spreadFirst=%v/policy3/min%d", entry.name, kind, spreadFirst, minReplicas)
					t.Run(name, func(t *testing.T) {
						trs := []oam.Trait{scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})}
						switch {
						case kind != "deployment":
						case spreadFirst:
							trs = append([]oam.Trait{spread}, trs...)
						default:
							trs = append(trs, spread)
						}
						app := interactionApp([]oam.Component{{
							Name: "app", Type: kind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: trs,
						}})
						ctx := oam.TransformContext{Policy: &replicasPolicy{defaultReplicas: &three}}
						cluster, err := entry.run(spreadScalerTransformer(&traits.TopologySpreadHandler{}), app, ctx)
						if err != nil {
							t.Fatalf("transform: %v", err)
						}
						got := gotSpread(oneDeployment(t, cluster).Spec.Template.Spec.TopologySpreadConstraints)
						if want := wantSpread(minReplicas); fmt.Sprint(got) != fmt.Sprint(want) {
							t.Errorf("spread constraints = %v, want %v (policy default 3, scaler minReplicas %d)", got, want, minReplicas)
						}
					})
				}
			}
		}
	}
}

// TestInteraction_PolicyScalerMinimumDecidesTheSpread: when the scaler omits
// minReplicas, the policy's scaler minimum fills it in ApplyPolicy, and the
// Deployment spreads by that value. This is the row that pins the order, because
// a spread that ran before the scaler's ApplyPolicy would see no minimum. The
// last row is the control: an authored minimum wins over the policy's.
func TestInteraction_PolicyScalerMinimumDecidesTheSpread(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	rows := []struct {
		name       string
		authoredIn map[string]any
		policyMin  int32
		want       []string
	}{
		{"policy minimum 3 fills the omitted minimum", map[string]any{"maxReplicas": 5}, 3, wantSpread(3)},
		{"policy minimum 2 fills the omitted minimum", map[string]any{"maxReplicas": 5}, 2, wantSpread(2)},
		{"authored minimum 1 wins over policy minimum 3", map[string]any{"minReplicas": 1, "maxReplicas": 5}, 3, wantSpread(1)},
	}
	// Every kind that builds a Deployment and, for the deployment kind, both
	// trait orders: a webservice and a worker synthesize their own spread trait.
	for _, entry := range spreadEntryPoints() {
		for _, kind := range []string{"deployment", "webservice", "worker"} {
			for _, spreadFirst := range []bool{false, true} {
				if kind != "deployment" && spreadFirst {
					continue
				}
				for _, row := range rows {
					name := fmt.Sprintf("%s/%s/spreadFirst=%v/%s", entry.name, kind, spreadFirst, row.name)
					t.Run(name, func(t *testing.T) {
						trs := []oam.Trait{scalerTrait(row.authoredIn)}
						switch {
						case kind != "deployment":
						case spreadFirst:
							trs = append([]oam.Trait{spread}, trs...)
						default:
							trs = append(trs, spread)
						}
						app := interactionApp([]oam.Component{{
							Name: "app", Type: kind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: trs,
						}})
						ctx := oam.TransformContext{Policy: &stubScalerPolicy{defaultScalerMin: int32ptr32(row.policyMin)}}
						cluster, err := entry.run(spreadScalerTransformer(&traits.TopologySpreadHandler{}), app, ctx)
						if err != nil {
							t.Fatalf("transform: %v", err)
						}
						got := gotSpread(oneDeployment(t, cluster).Spec.Template.Spec.TopologySpreadConstraints)
						if fmt.Sprint(got) != fmt.Sprint(row.want) {
							t.Errorf("spread constraints = %v, want %v (policy scaler minimum %d)", got, row.want, row.policyMin)
						}
					})
				}
			}
		}
	}
}

// TestInteraction_AnAdoptedDeploymentIgnoresItsOwnReplicasUnderAScaler: a
// Deployment that a manifests component carries keeps its own spec.replicas, and
// the trait ignores that count when a scaler scales the Deployment. The scaler
// matches the adopted Deployment by name, namespace and kind. The third row is
// the control: the scaler scales another Deployment, so the adopted one spreads
// by its own count.
func TestInteraction_AnAdoptedDeploymentIgnoresItsOwnReplicasUnderAScaler(t *testing.T) {
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
	rows := []struct {
		name        string
		adoptedName string // the Deployment's name in the manifest; the scaler scales "app"
		replicas    int
		minReplicas int32
		want        []string
	}{
		{"own count 5, scaler minimum 1", "app", 5, 1, nil},
		{"own count 1, scaler minimum 3", "app", 1, 3, wantSpread(3)},
		{"own count 3, scaler scales another Deployment", "elsewhere", 3, 1, wantSpread(3)},
	}
	// The adopted Deployment comes from a manifests component by design, and it carries
	// the spread trait alone, so it has no trait order. The scaled component is the part
	// that varies: it builds a Deployment in each of the three kinds.
	for _, entry := range spreadEntryPoints() {
		for _, scaledKind := range []string{"deployment", "webservice", "worker"} {
			for _, row := range rows {
				t.Run(entry.name+"/"+scaledKind+"/"+row.name, func(t *testing.T) {
					runAdoptedRow(t, entry, scaledKind, row.adoptedName, row.replicas, row.minReplicas, row.want, spread)
				})
			}
		}
	}
}

func runAdoptedRow(t *testing.T, entry spreadEntry, scaledKind, adoptedName string, replicas int, minReplicas int32, want []string, spread oam.Trait) {
	t.Helper()
	inline := fmt.Sprintf("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: %s\nspec:\n  replicas: %d\n", adoptedName, replicas) +
		"  selector:\n    matchLabels:\n      app: web\n" +
		"  template:\n    metadata:\n      labels:\n        app: web\n" +
		"    spec:\n      containers:\n      - name: web\n        image: ghcr.io/org/web:v1\n"
	app := interactionApp([]oam.Component{
		{Name: "app", Type: scaledKind, Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
			Traits: []oam.Trait{scalerTrait(map[string]any{"minReplicas": int(minReplicas), "maxReplicas": 5})}},
		{Name: "adopted", Type: "manifests", Properties: map[string]any{"inline": inline},
			Traits: []oam.Trait{spread}},
	})
	cluster, err := entry.run(spreadScalerTransformer(&traits.TopologySpreadHandler{}), app, oam.TransformContext{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	dep := adoptedDeployment(t, cluster)
	got := gotSpread(dep.Spec.Template.Spec.TopologySpreadConstraints)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("adopted Deployment %s (own replicas %d): spread constraints = %v, want %v", adoptedName, replicas, got, want)
	}
}
