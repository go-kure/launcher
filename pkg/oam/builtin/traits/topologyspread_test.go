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
// hostname spread from two, a soft zone spread added from three. The expected
// value is spelled out rather than only compared against
// components.BuildTopologySpreadConstraints, so a change to the shared opinion
// shows up here as a decision and not as a silently moving target.
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
	withOpinion, err := generateWithTopologySpreadless(t, &components.WorkerHandler{},
		&oam.Component{Name: "api", Type: "worker", Properties: map[string]any{"image": "ghcr.io/org/api:v1", "replicas": 3}})
	if err != nil {
		t.Fatalf("worker with its own opinion: %v", err)
	}
	deps, err := generateWithTopologySpread(t, &components.WorkerHandler{},
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
		if dep, ok := (*o).(*appsv1.Deployment); ok {
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
		"worker":     &components.WorkerHandler{},
	}, nil)
	tr.RegisterBuiltinTrait("topology-spread", &traits.TopologySpreadHandler{})
	return tr
}

// transformedDeployment runs the full transformer and returns the Deployment
// generated for the component named name.
func transformedDeployment(t *testing.T, comp oam.Component, p oam.Policy) (*appsv1.Deployment, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, _, err := topologySpreadTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: p})
	if err != nil {
		return nil, err
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
		return nil, err
	}
	for _, o := range objects {
		if dep, ok := (*o).(*appsv1.Deployment); ok {
			return dep, nil
		}
	}
	t.Fatalf("application %q generated no Deployment", comp.Name)
	return nil, nil
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
	if _, err := generateWithTopologySpread(t, &components.WorkerHandler{}, workerDefault, nil); err == nil {
		t.Error("worker with its default topologySpread: expected an error, got none")
	} else if !strings.Contains(err.Error(), "topologySpread") {
		t.Errorf("error should point at the component's own topologySpread, got: %v", err)
	}
}

// At one replica the role kinds' opinion produces no constraints, so there is
// nothing to conflict with and the trait is a no-op rather than an error.
func TestTopologySpread_SingleReplicaWorkerDefaultIsNoConflict(t *testing.T) {
	deps, err := generateWithTopologySpread(t, &components.WorkerHandler{},
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

// The constraints' selectors are copies: a later edit to the Deployment's own
// selector map must not reach them (the selectorFrom rule in components).
func TestTopologySpread_SelectorsDoNotAliasTheDeployment(t *testing.T) {
	deps, err := generateWithTopologySpread(t, &components.DeploymentHandler{},
		deploymentComponent(map[string]any{"replicas": 3}), nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dep := deps[0]
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
