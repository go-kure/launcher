package traits_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The topology-spread trait applies launcher's default spread opinion — the
// one webservice and worker apply from their `topologySpread` property — to
// any Deployment a component generates. These tests pin the three properties
// that make it usable as the target of a lowering rule: it reads the replica
// count the Deployment actually carries (so after the environment policy), it
// produces exactly what the role kinds produce, and it never merges with
// constraints that are already there.

func topologySpreadTrait() *oam.Trait {
	return &oam.Trait{Type: "topology-spread"}
}

// deploymentComponent returns a `deployment` component named api with the
// given extra properties on top of its image.
func deploymentComponent(extra map[string]any) *oam.Component {
	props := map[string]any{"image": "ghcr.io/org/api:v1"}
	for k, v := range extra {
		props[k] = v
	}
	return &oam.Component{Name: "api", Type: "deployment", Properties: props}
}

// generateWithTopologySpread runs the production order for one component —
// parse, policy, trait, generate — and returns the generated objects or the
// first error.
func generateWithTopologySpread(t *testing.T, h oam.ComponentHandler, comp *oam.Component, p oam.Policy) ([]*appsv1.Deployment, error) {
	t.Helper()
	cfg, err := h.ToApplicationConfig(comp, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if p != nil {
		enforceable, ok := cfg.(oam.Enforceable)
		if !ok {
			t.Fatalf("%s config does not implement oam.Enforceable", comp.Type)
		}
		if err := enforceable.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
	}
	app := stack.NewApplication(comp.Name, "default", cfg)
	if err := (&traits.TopologySpreadHandler{}).Apply(topologySpreadTrait(), app, newBundle()); err != nil {
		return nil, err
	}
	objects, err := app.Config.Generate(app)
	if err != nil {
		return nil, err
	}
	var deps []*appsv1.Deployment
	for _, o := range objects {
		if o == nil {
			continue
		}
		if dep, ok := (*o).(*appsv1.Deployment); ok && dep != nil {
			deps = append(deps, dep)
		}
	}
	if len(deps) == 0 {
		t.Fatal("no Deployment among the generated objects")
	}
	return deps, nil
}

func spreadKeys(tscs []corev1.TopologySpreadConstraint) []string {
	keys := make([]string, len(tscs))
	for i, c := range tscs {
		keys[i] = c.TopologyKey + "/" + string(c.WhenUnsatisfiable)
	}
	return keys
}

func TestTopologySpreadHandler_CanHandle(t *testing.T) {
	h := &traits.TopologySpreadHandler{}
	for typ, want := range map[string]bool{
		"topology-spread":  true,
		"prune-protection": false,
		"scaler":           false,
	} {
		if got := h.CanHandle(typ); got != want {
			t.Errorf("CanHandle(%q) = %v, want %v", typ, got, want)
		}
	}
}

// The trait's tiers are the role kinds' tiers: nothing at one replica, a hard
// hostname spread from two, a soft zone spread added from three. The tiers,
// maxSkew and selector are spelled out, so a change to any of them in the
// shared opinion fails here. The comparison against
// components.BuildTopologySpreadConstraints pins something else: the trait
// emits exactly what the role kinds' helper returns for the same replicas and
// selector, whatever that helper currently returns.
func TestTopologySpread_StampsDefaultConstraintsByReplicas(t *testing.T) {
	cases := []struct {
		replicas int
		want     []string
	}{
		{1, []string{}},
		{2, []string{"kubernetes.io/hostname/DoNotSchedule"}},
		{3, []string{"kubernetes.io/hostname/DoNotSchedule", "topology.kubernetes.io/zone/ScheduleAnyway"}},
		{5, []string{"kubernetes.io/hostname/DoNotSchedule", "topology.kubernetes.io/zone/ScheduleAnyway"}},
	}
	for _, tc := range cases {
		deps, err := generateWithTopologySpread(t, &components.DeploymentHandler{},
			deploymentComponent(map[string]any{"replicas": tc.replicas}), nil)
		if err != nil {
			t.Fatalf("replicas=%d: %v", tc.replicas, err)
		}
		got := deps[0].Spec.Template.Spec.TopologySpreadConstraints
		if !reflect.DeepEqual(spreadKeys(got), tc.want) {
			t.Errorf("replicas=%d: constraints %v, want %v", tc.replicas, spreadKeys(got), tc.want)
		}
		want := components.BuildTopologySpreadConstraints(int32(tc.replicas), map[string]string{"app": "api"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("replicas=%d: constraints differ from the shared opinion\n got: %+v\nwant: %+v", tc.replicas, got, want)
		}
		for i, c := range got {
			if c.MaxSkew != 1 {
				t.Errorf("replicas=%d constraint %d: maxSkew = %d, want 1", tc.replicas, i, c.MaxSkew)
			}
			if c.LabelSelector == nil || !reflect.DeepEqual(c.LabelSelector.MatchLabels, map[string]string{"app": "api"}) {
				t.Errorf("replicas=%d constraint %d: labelSelector = %+v, want matchLabels app=api", tc.replicas, i, c.LabelSelector)
			}
		}
	}
}

// A role kind with its own opinion switched off gets, through the trait, the
// same constraints it produces with the opinion on. This is the equivalence a
// lowering rule that forwards `topologySpread` as this trait relies on.
func TestTopologySpread_MatchesWorkerOpinion(t *testing.T) {
	withOpinion, err := generateWithTopologySpreadless(t, workerViaRule{},
		&oam.Component{Name: "api", Type: "worker", Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3}})
	if err != nil {
		t.Fatalf("worker with its own opinion: %v", err)
	}
	deps, err := generateWithTopologySpread(t, workerViaRule{},
		&oam.Component{Name: "api", Type: "worker", Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3, "topologySpread": false}}, nil)
	if err != nil {
		t.Fatalf("worker with topologySpread false plus the trait: %v", err)
	}
	got := deps[0].Spec.Template.Spec.TopologySpreadConstraints
	if len(got) != 2 || !reflect.DeepEqual(got, withOpinion) {
		t.Errorf("trait on worker gives %+v, want the worker opinion %+v", got, withOpinion)
	}
}

// generateWithTopologySpreadless is the no-trait baseline for
// TestTopologySpread_MatchesWorkerOpinion.
func generateWithTopologySpreadless(t *testing.T, h oam.ComponentHandler, comp *oam.Component) ([]corev1.TopologySpreadConstraint, error) {
	t.Helper()
	cfg, err := h.ToApplicationConfig(comp, "default")
	if err != nil {
		return nil, err
	}
	app := stack.NewApplication(comp.Name, "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		return nil, err
	}
	for _, o := range objects {
		if o == nil {
			continue
		}
		if dep, ok := (*o).(*appsv1.Deployment); ok && dep != nil {
			return dep.Spec.Template.Spec.TopologySpreadConstraints, nil
		}
	}
	t.Fatal("no Deployment among the generated objects")
	return nil, nil
}

// replicasPolicy is a policy that only supplies a default replica count.
type replicasPolicy struct {
	oam.NoopPolicy
	defaultReplicas *int32
}

func (p *replicasPolicy) DefaultReplicas() *int32 { return p.defaultReplicas }

// topologySpreadTransformer registers the kinds and the trait the transform
// tests need, exactly as kurel build does.
func topologySpreadTransformer() *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment": &components.DeploymentHandler{},
	}, nil)
	tr.RegisterComponentLowering(components.WorkerRule{})
	tr.RegisterBuiltinTrait("topology-spread", &traits.TopologySpreadHandler{})
	return tr
}

// transformedDeployment runs the full transformer and returns the Deployment
// generated for the component named name.
func transformedDeployment(t *testing.T, comp oam.Component, p oam.Policy) (*appsv1.Deployment, error) {
	t.Helper()
	dep, _, err := transformedDeploymentCtx(t, comp, oam.TransformContext{Namespace: "default", Policy: p})
	return dep, err
}

// transformedDeploymentCtx is transformedDeployment with the caller's whole
// transform context. It runs the authored-property check first, exactly as
// kurel build does, and also returns the policy result so a test can read
// which capability keys the traits resolved.
func transformedDeploymentCtx(t *testing.T, comp oam.Component, ctx oam.TransformContext) (*appsv1.Deployment, *oam.PolicyResult, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	tr := topologySpreadTransformer()
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return nil, nil, err
	}
	cluster, result, err := tr.TransformWithPolicy(app, ctx)
	if err != nil {
		return nil, nil, err
	}
	var found *stack.Application
	var visitBundle func(b *stack.Bundle)
	visitBundle = func(b *stack.Bundle) {
		if b == nil || found != nil {
			return
		}
		for _, a := range b.Applications {
			if a.Name == comp.Name {
				found = a
				return
			}
		}
		for _, ch := range b.Children {
			visitBundle(ch)
		}
	}
	var visitNode func(n *stack.Node)
	visitNode = func(n *stack.Node) {
		if n == nil || found != nil {
			return
		}
		visitBundle(n.Bundle)
		for _, ch := range n.Children {
			visitNode(ch)
		}
	}
	visitNode(cluster.Node)
	if found == nil {
		t.Fatalf("no application %q in the transformed cluster", comp.Name)
	}
	objects, err := found.Config.Generate(found)
	if err != nil {
		return nil, nil, err
	}
	for _, o := range objects {
		if o == nil {
			continue
		}
		if dep, ok := (*o).(*appsv1.Deployment); ok && dep != nil {
			return dep, result, nil
		}
	}
	t.Fatalf("application %q generated no Deployment", comp.Name)
	return nil, nil, nil
}

// The replica count the trait reads is the one the Deployment carries after
// the environment policy ran, not the authored one. A document that authors no
// replicas gets no spread without a policy and the full spread under a policy
// defaulting to three: the policy step is the only difference between the two
// runs, so this pair is what pins the ordering (policy at component
// conversion, traits after, Generate last).
func TestTopologySpread_FollowsPolicyDefaultedReplicas(t *testing.T) {
	comp := oam.Component{
		Name: "api", Type: "deployment",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1"},
		Traits:     []oam.Trait{*topologySpreadTrait()},
	}

	three := int32(3)
	withPolicy, err := transformedDeployment(t, comp, &replicasPolicy{defaultReplicas: &three})
	if err != nil {
		t.Fatalf("with policy: %v", err)
	}
	if withPolicy.Spec.Replicas == nil || *withPolicy.Spec.Replicas != 3 {
		t.Fatalf("precondition: policy did not default replicas to 3, got %v", withPolicy.Spec.Replicas)
	}
	want := []string{"kubernetes.io/hostname/DoNotSchedule", "topology.kubernetes.io/zone/ScheduleAnyway"}
	if got := spreadKeys(withPolicy.Spec.Template.Spec.TopologySpreadConstraints); !reflect.DeepEqual(got, want) {
		t.Errorf("policy-defaulted replicas=3: constraints %v, want %v", got, want)
	}

	withoutPolicy, err := transformedDeployment(t, comp, nil)
	if err != nil {
		t.Fatalf("without policy: %v", err)
	}
	if got := withoutPolicy.Spec.Template.Spec.TopologySpreadConstraints; len(got) != 0 {
		t.Errorf("no policy, replicas default 1: constraints %v, want none", spreadKeys(got))
	}
}

// Constraints already on the Deployment are never merged with the trait's:
// two sources for the same field would leave the output depending on which
// one a reader assumed won. Both routes to an existing constraint are refused
// — authored on `deployment`, and the role kinds' own default opinion.
func TestTopologySpread_RefusesExistingConstraints(t *testing.T) {
	authored := deploymentComponent(map[string]any{
		"replicas": 3,
		"topologySpreadConstraints": []any{map[string]any{
			"maxSkew": 2, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "ScheduleAnyway",
		}},
	})
	if _, err := generateWithTopologySpread(t, &components.DeploymentHandler{}, authored, nil); err == nil {
		t.Error("deployment authoring topologySpreadConstraints: expected an error, got none")
	} else if !strings.Contains(err.Error(), "topology-spread") || !strings.Contains(err.Error(), "topologySpreadConstraints") {
		t.Errorf("error should name the trait and the conflicting field, got: %v", err)
	}

	workerDefault := &oam.Component{Name: "api", Type: "worker",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 2}}
	if _, err := generateWithTopologySpread(t, workerViaRule{}, workerDefault, nil); err == nil {
		t.Error("worker with its default topologySpread: expected an error, got none")
	} else if !strings.Contains(err.Error(), "topologySpread") {
		t.Errorf("error should point at the component's own topologySpread, got: %v", err)
	}
}

// Worker's topologySpread default reaches the Deployment through the whole
// transformer — WorkerRule lowering worker into deployment plus a synthesized
// topology-spread trait — exactly as the former worker handler applied it, and
// an authored topology-spread trait on a worker behaves as it did: refused
// with the same message once the default produced constraints, a no-op when it
// produced none, and the one source of constraints when the default is off.
func TestTopologySpread_WorkerThroughTheTransformer(t *testing.T) {
	worker := func(replicas int, topologySpread any, traits ...oam.Trait) oam.Component {
		props := map[string]any{"image": "ghcr.io/org/api:v1", "replicas": replicas}
		if topologySpread != nil {
			props["topologySpread"] = topologySpread
		}
		return oam.Component{Name: "api", Type: "worker", Properties: props, Traits: traits}
	}
	want := components.BuildTopologySpreadConstraints(3, map[string]string{"app": "api"})

	for name, comp := range map[string]oam.Component{
		"default":                      worker(3, nil),
		"topologySpread false + trait": worker(3, false, *topologySpreadTrait()),
	} {
		dep, err := transformedDeployment(t, comp, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := dep.Spec.Template.Spec.TopologySpreadConstraints; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: constraints %v, want %v", name, spreadKeys(got), spreadKeys(want))
		}
	}

	dep, err := transformedDeployment(t, worker(3, false), nil)
	if err != nil {
		t.Fatalf("topologySpread false: %v", err)
	}
	if got := dep.Spec.Template.Spec.TopologySpreadConstraints; len(got) != 0 {
		t.Errorf("topologySpread false: constraints %v, want none", spreadKeys(got))
	}

	dep, err = transformedDeployment(t, worker(1, nil, *topologySpreadTrait()), nil)
	if err != nil {
		t.Fatalf("one replica + trait: %v", err)
	}
	if got := dep.Spec.Template.Spec.TopologySpreadConstraints; len(got) != 0 {
		t.Errorf("one replica + trait: constraints %v, want none", spreadKeys(got))
	}

	_, err = transformedDeployment(t, worker(3, nil, *topologySpreadTrait()), nil)
	const refusal = `topology-spread: Deployment "api" already carries 2 topologySpreadConstraints (authored on the component, or from its own topologySpread default); remove them, or set topologySpread: false, or drop the trait`
	if err == nil || !strings.Contains(err.Error(), refusal) {
		t.Errorf("default + trait at three replicas: err = %v, want the refusal %q", err, refusal)
	}
}

// At one replica the role kinds' opinion produces no constraints, so there is
// nothing to conflict with and the trait is a no-op rather than an error.
func TestTopologySpread_SingleReplicaWorkerDefaultIsNoConflict(t *testing.T) {
	deps, err := generateWithTopologySpread(t, workerViaRule{},
		&oam.Component{Name: "api", Type: "worker", Properties: map[string]any{"image": "ghcr.io/org/api:v1"}}, nil)
	if err != nil {
		t.Fatalf("worker at one replica: %v", err)
	}
	if got := deps[0].Spec.Template.Spec.TopologySpreadConstraints; len(got) != 0 {
		t.Errorf("constraints %v, want none", spreadKeys(got))
	}
}

// The trait takes no properties; anything authored under it is a mistake the
// author should hear about, not a knob that silently does nothing.
func TestTopologySpread_RejectsProperties(t *testing.T) {
	app := stack.NewApplication("api", "default", &cmStub{name: "api", namespace: "default"})
	err := (&traits.TopologySpreadHandler{}).Apply(
		&oam.Trait{Type: "topology-spread", Properties: map[string]any{"maxSkew": 2}}, app, newBundle())
	if err == nil {
		t.Fatal("expected an error for an authored property, got none")
	}
	if !strings.Contains(err.Error(), "maxSkew") {
		t.Errorf("error should name the property, got: %v", err)
	}
}

// `scope` is not the trait's property but the transform engine's: it is legal
// on every authored trait and selects the "<type>.<scope>" ClusterProfile
// capability binding. The trait must let it through — refusing it would let a
// document pass the authored check and then fail at Apply — while any other
// key next to it is still refused by name.
func TestTopologySpread_AcceptsEngineOwnedScope(t *testing.T) {
	app := stack.NewApplication("api", "default", &cmStub{name: "api", namespace: "default"})
	if err := (&traits.TopologySpreadHandler{}).Apply(
		&oam.Trait{Type: "topology-spread", Properties: map[string]any{"scope": "zone-a"}}, app, newBundle()); err != nil {
		t.Fatalf("Apply with only the engine-owned scope: %v", err)
	}

	app = stack.NewApplication("api", "default", &cmStub{name: "api", namespace: "default"})
	err := (&traits.TopologySpreadHandler{}).Apply(
		&oam.Trait{Type: "topology-spread", Properties: map[string]any{"scope": "zone-a", "maxSkew": 2}}, app, newBundle())
	if err == nil {
		t.Fatal("expected an error for maxSkew next to scope, got none")
	}
	if !strings.Contains(err.Error(), `"maxSkew"`) {
		t.Errorf("error should name maxSkew, got: %v", err)
	}
}

// End to end: an authored `scope` passes the authored check, selects the
// scoped capability binding, and the Deployment is still decorated.
func TestTopologySpread_ScopeSelectsScopedCapability(t *testing.T) {
	comp := oam.Component{
		Name: "api", Type: "deployment",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3},
		Traits:     []oam.Trait{{Type: "topology-spread", Properties: map[string]any{"scope": "zone-a"}}},
	}
	dep, result, err := transformedDeploymentCtx(t, comp, oam.TransformContext{
		Namespace: "default",
		Capabilities: map[string]oam.CapabilityBinding{
			"topology-spread":        {},
			"topology-spread.zone-a": {},
		},
	})
	if err != nil {
		t.Fatalf("transform with scope: %v", err)
	}
	if got, want := result.ConsumedCapabilities, []string{"topology-spread.zone-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("consumed capabilities %v, want %v", got, want)
	}
	want := []string{"kubernetes.io/hostname/DoNotSchedule", "topology.kubernetes.io/zone/ScheduleAnyway"}
	if got := spreadKeys(dep.Spec.Template.Spec.TopologySpreadConstraints); !reflect.DeepEqual(got, want) {
		t.Errorf("constraints %v, want %v", got, want)
	}
}

// A ClusterProfile capability rendering is merged into the trait's properties
// before Apply. The trait reads none, so a rendering key other than an
// engine-owned one is refused by name like an authored one: it would
// otherwise be a platform setting that silently does nothing.
func TestTopologySpread_RefusesCapabilityRenderingKey(t *testing.T) {
	comp := oam.Component{
		Name: "api", Type: "deployment",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1"},
		Traits:     []oam.Trait{*topologySpreadTrait()},
	}
	_, _, err := transformedDeploymentCtx(t, comp, oam.TransformContext{
		Namespace: "default",
		Capabilities: map[string]oam.CapabilityBinding{
			"topology-spread": {Rendering: map[string]any{"maxSkew": 2}},
		},
	})
	if err == nil {
		t.Fatal("expected an error for a capability rendering key, got none")
	}
	if !strings.Contains(err.Error(), `"maxSkew"`) {
		t.Errorf("error should name maxSkew, got: %v", err)
	}
}

// buildWithProfile runs the kurel build order for one component: the
// ClusterProfile is evaluated first, then its evaluated capabilities feed the
// transform (pkg/cmd/kurel build.go, EvaluateProfile then Transform).
func buildWithProfile(t *testing.T, comp oam.Component, capabilities map[string]oam.CapabilityBinding) (*appsv1.Deployment, *oam.PolicyResult, error) {
	t.Helper()
	profile := &oam.ClusterProfile{Spec: oam.ClusterProfileSpec{Capabilities: capabilities}}
	evaluated, err := topologySpreadTransformer().EvaluateProfile(profile)
	if err != nil {
		return nil, nil, err
	}
	return transformedDeploymentCtx(t, comp, oam.TransformContext{
		Namespace:    "default",
		Capabilities: evaluated.Spec.Capabilities,
	})
}

// A capability rendering is merged into the trait's properties, and the trait
// cannot tell a merged key from an authored one at Apply. For an engine-owned
// key that matters: a `scope` in a rendering arrives after the engine already
// chose the binding, so it selects nothing, and letting it through would build
// with a platform value that silently does nothing. The rendering is therefore
// refused where it is evaluated, naming the key and the capability it came
// from — under the bare key and under a scoped one alike.
func TestTopologySpread_RefusesRenderingKeysAtProfileEvaluation(t *testing.T) {
	cases := []struct {
		name, capability, key string
	}{
		{"engine-owned scope under the bare key", "topology-spread", "scope"},
		{"engine-owned scope under a scoped key", "topology-spread.zone-b", "scope"},
		{"non-engine key", "topology-spread", "maxSkew"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{
				Name: "api", Type: "deployment",
				Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3},
				Traits:     []oam.Trait{*topologySpreadTrait()},
			}
			_, _, err := buildWithProfile(t, comp, map[string]oam.CapabilityBinding{
				tc.capability: {Rendering: map[string]any{tc.key: "zone-a"}},
			})
			if err == nil {
				t.Fatalf("expected the build to fail for rendering key %q on capability %q, got none", tc.key, tc.capability)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error should name the key %q, got: %v", tc.key, err)
			}
			if want := `capability "` + tc.capability + `"`; !strings.Contains(err.Error(), want) {
				t.Errorf("error should name its source %s, got: %v", want, err)
			}
		})
	}
}

// Profile evaluation accepts a topology-spread capability with no rendering,
// and an authored `scope` still selects the scoped binding after it.
func TestTopologySpread_ProfileEvaluationKeepsAuthoredScope(t *testing.T) {
	comp := oam.Component{
		Name: "api", Type: "deployment",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3},
		Traits:     []oam.Trait{{Type: "topology-spread", Properties: map[string]any{"scope": "zone-a"}}},
	}
	dep, result, err := buildWithProfile(t, comp, map[string]oam.CapabilityBinding{
		"topology-spread":        {},
		"topology-spread.zone-a": {Rendering: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("build with an authored scope: %v", err)
	}
	if got, want := result.ConsumedCapabilities, []string{"topology-spread.zone-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("consumed capabilities %v, want %v", got, want)
	}
	want := []string{"kubernetes.io/hostname/DoNotSchedule", "topology.kubernetes.io/zone/ScheduleAnyway"}
	if got := spreadKeys(dep.Spec.Template.Spec.TopologySpreadConstraints); !reflect.DeepEqual(got, want) {
		t.Errorf("constraints %v, want %v", got, want)
	}
}

// A component that generates no Deployment has nothing for the trait to act
// on; that is an error rather than a silent no-op.
func TestTopologySpread_NoDeploymentFails(t *testing.T) {
	app := stack.NewApplication("cfg", "default", &cmStub{name: "cfg", namespace: "default"})
	if err := (&traits.TopologySpreadHandler{}).Apply(topologySpreadTrait(), app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	_, err := app.Config.Generate(app)
	if err == nil {
		t.Fatal("expected an error for a component that generates no Deployment, got none")
	}
	if !strings.Contains(err.Error(), "Deployment") {
		t.Errorf("error should say a Deployment is required, got: %v", err)
	}
}

// manifestsComponent returns a `manifests` component named api whose inline
// source is the given YAML.
func manifestsComponent(inline string) *oam.Component {
	return &oam.Component{Name: "api", Type: "manifests", Properties: map[string]any{"inline": inline}}
}

// manifestsDeployment is an inline apps/v1 Deployment named web; replicas and
// selector are spliced in verbatim (each a complete, indented YAML block, or
// empty to leave the field unset).
func manifestsDeployment(replicas, selector string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n" +
		replicas + selector +
		"  template:\n    metadata:\n      labels:\n        app: web\n        tier: front\n" +
		"    spec:\n      containers:\n      - name: web\n        image: ghcr.io/org/web:v1\n"
}

// A Deployment in a `manifests` source is decoded into the typed Deployment,
// so the trait decorates it like one a launcher kind builds, using its own
// selector rather than any component-derived label.
func TestTopologySpread_DecoratesManifestsDeployment(t *testing.T) {
	deps, err := generateWithTopologySpread(t, &components.ManifestsHandler{},
		manifestsComponent(manifestsDeployment("  replicas: 2\n", "  selector:\n    matchLabels:\n      app: web\n")), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := deps[0].Spec.Template.Spec.TopologySpreadConstraints
	want := components.BuildTopologySpreadConstraints(2, map[string]string{"app": "web"})
	if len(got) != 1 || !reflect.DeepEqual(got, want) {
		t.Errorf("manifests Deployment at replicas=2: constraints %+v, want %+v", got, want)
	}
}

// A Deployment whose selector the trait cannot copy into a spread selector is
// refused at every replica count, not only once the count reaches the tier
// that emits constraints. The environment policy sets the count, so a check
// gated on it would let one document build in one environment and fail in
// another.
func TestTopologySpread_RefusesNonMatchLabelsSelectorAtAnyReplicas(t *testing.T) {
	selectors := map[string]string{
		"matchLabels plus matchExpressions": "  selector:\n    matchLabels:\n      app: web\n    matchExpressions:\n    - {key: tier, operator: In, values: [front]}\n",
		"matchExpressions only":             "  selector:\n    matchExpressions:\n    - {key: app, operator: In, values: [web]}\n",
		"no selector":                       "",
	}
	replicaCounts := map[string]string{
		"unset":      "",
		"replicas=1": "  replicas: 1\n",
		"replicas=3": "  replicas: 3\n",
	}
	for selName, sel := range selectors {
		for repName, rep := range replicaCounts {
			_, err := generateWithTopologySpread(t, &components.ManifestsHandler{},
				manifestsComponent(manifestsDeployment(rep, sel)), nil)
			if err == nil {
				t.Errorf("%s, %s: expected an error, got none", selName, repName)
				continue
			}
			if !strings.Contains(err.Error(), "matchLabels") {
				t.Errorf("%s, %s: error should name the matchLabels requirement, got: %v", selName, repName, err)
			}
		}
	}
}

// A Deployment emitted as raw, unstructured output (here a passthrough
// object) is not inspected, and the error says so rather than claiming the
// component has no Deployment at all.
func TestTopologySpread_UnstructuredDeploymentIsNotInspected(t *testing.T) {
	comp := &oam.Component{Name: "api", Type: "passthrough", Properties: map[string]any{
		"object": map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "api"},
			"spec":     map[string]any{"replicas": 3},
		},
	}}
	cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(comp, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication(comp.Name, "default", cfg)
	if err := (&traits.TopologySpreadHandler{}).Apply(topologySpreadTrait(), app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	_, err = app.Config.Generate(app)
	if err == nil {
		t.Fatal("expected an error for a passthrough Deployment, got none")
	}
	if !strings.Contains(err.Error(), "unstructured") || !strings.Contains(err.Error(), "not inspected") {
		t.Errorf("error should say an unstructured Deployment is not inspected, got: %v", err)
	}
}

// The constraints' selectors are copies: a later edit to the Deployment's own
// selector map must not reach them (the selectorFrom rule in components).
func TestTopologySpread_SelectorsDoNotAliasTheDeployment(t *testing.T) {
	deps, err := generateWithTopologySpread(t, &components.DeploymentHandler{},
		deploymentComponent(map[string]any{"replicas": 3}), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dep := deps[0]
	// Without constraints the loop below would pass vacuously.
	if n := len(dep.Spec.Template.Spec.TopologySpreadConstraints); n != 2 {
		t.Fatalf("replicas=3: %d constraints, want 2", n)
	}
	dep.Spec.Selector.MatchLabels["example.test/added-after"] = "yes"
	dep.Spec.Template.Labels["example.test/added-after"] = "yes"
	for i, c := range dep.Spec.Template.Spec.TopologySpreadConstraints {
		if _, leaked := c.LabelSelector.MatchLabels["example.test/added-after"]; leaked {
			t.Errorf("constraint %d selector aliases a Deployment label map: %v", i, c.LabelSelector.MatchLabels)
		}
	}
}

// The decorator keeps the wrapped config's optional interfaces reachable, like
// every other trait decorator.
func TestTopologySpread_ForwardsServiceAccountName(t *testing.T) {
	cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(
		deploymentComponent(map[string]any{"serviceAccountName": "shared-sa"}), "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("api", "default", cfg)
	if err := (&traits.TopologySpreadHandler{}).Apply(topologySpreadTrait(), app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	namer, ok := app.Config.(oam.ServiceAccountNamer)
	if !ok {
		t.Fatal("decorated config does not implement oam.ServiceAccountNamer")
	}
	if got := namer.ServiceAccountName(); got != "shared-sa" {
		t.Errorf("ServiceAccountName() = %q, want shared-sa", got)
	}
}
