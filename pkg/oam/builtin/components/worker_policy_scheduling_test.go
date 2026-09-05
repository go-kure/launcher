package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Pod-level scheduling on `worker`, exercised under a NON-NOOP environment
// policy. The golden fixtures cannot reach this behaviour, so it is pinned here
// or nowhere.
//
// worker's topology-spread opinion is evaluated inside Generate, from
// c.Replicas (worker.go, createDeployment -> buildTopologySpreadConstraints).
// By then ApplyPolicy has already run — transform.go calls it on the config
// ToApplicationConfig returned — and ApplyPolicy substitutes the environment
// policy's DefaultReplicas for a document that authored none (worker.go ->
// applyDefaultReplicas, enforce.go). The opinion is therefore evaluated against
// the EFFECTIVE replica count, and the number of constraints a worker gets is a
// function of the policy, not of the document alone.
//
// No fixture covers that. `kurel build` passes no policy, TransformWithPolicy
// normalizes a nil Policy to &NoopPolicy{}, and NoopPolicy.DefaultReplicas()
// returns nil (policy.go), so every golden runs with no default replicas — the
// one case where the policy cannot change the count and the coupling is
// invisible. A change that broke the coupling would leave all of them passing.
//
// Hence the pair below. TestWorkerTopologySpread_FollowsPolicyDefaultedReplicas
// asserts the constraints a policy-defaulted count produces, and
// TestWorkerTopologySpread_NoPolicyMeansNoSpread asserts the same document
// produces none without a policy. Either alone is an assertion that happens to
// hold; together they isolate the policy step as the only difference between
// spread and no spread, which is the property actually worth pinning.

// workerSchedulingDoc is a worker component that authors neither `replicas` nor
// `topologySpread`, so the topology-spread opinion is on by default and the
// replica count is whatever the policy makes it — the shape in which the
// coupling is observable.
func workerSchedulingDoc() *oam.Component {
	return &oam.Component{
		Name: "backend",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/backend:v1.0.0",
		},
	}
}

// buildWorkerDeployment runs the production sequence — parse, apply policy,
// generate — and returns the emitted Deployment. A nil p uses oam.NoopPolicy,
// the same value TransformWithPolicy substitutes.
func buildWorkerDeployment(t *testing.T, comp *oam.Component, p oam.Policy) *appsv1.Deployment {
	t.Helper()
	if p == nil {
		p = &oam.NoopPolicy{}
	}

	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(comp, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	// Asserted rather than assumed: if the config ever stops implementing
	// Enforceable, ApplyPolicy is skipped and every test in this file quietly
	// becomes a no-policy test — the exact case the goldens already cover.
	enforceable, ok := cfg.(oam.Enforceable)
	if !ok {
		t.Fatalf("worker config does not implement oam.Enforceable; the policy step these tests exist to exercise would be silently skipped")
	}
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}

	app := stack.NewApplication(comp.Name, "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			return dep
		}
	}
	t.Fatal("no Deployment among the generated objects")
	return nil
}

// topologyKeys returns the constraints' topology keys in order, so a failure
// where the count is right but the tiers are wrong reads directly.
func topologyKeys(tscs []corev1.TopologySpreadConstraint) []string {
	keys := make([]string, len(tscs))
	for i, tsc := range tscs {
		keys[i] = tsc.TopologyKey
	}
	return keys
}

// TestWorkerTopologySpread_FollowsPolicyDefaultedReplicas pins that the
// topology-spread opinion is evaluated against the policy-defaulted replica
// count. A document authoring no replicas, under a policy defaulting them to 3,
// gets both spread tiers — because the count the opinion reads is the one
// ApplyPolicy left behind, not the one the document authored.
//
// Read the count before the policy applies and this fails with zero
// constraints: buildTopologySpreadConstraints returns nil at replicas <= 1
// (common.go) and the authored count is absent, so 1. The emitted object would
// then be a three-replica Deployment with no anti-collocation at all.
func TestWorkerTopologySpread_FollowsPolicyDefaultedReplicas(t *testing.T) {
	dep := buildWorkerDeployment(t, workerSchedulingDoc(), &stubPolicy{defaultReplicas: int32ptr(3)})

	if dep.Spec.Replicas == nil {
		t.Fatal("Replicas is nil; the policy default was not applied")
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("Replicas = %d, want 3 (the policy default for an unauthored count)", *dep.Spec.Replicas)
	}

	tscs := dep.Spec.Template.Spec.TopologySpreadConstraints
	if len(tscs) != 2 {
		t.Fatalf("got %d topology spread constraints %v, want 2 — the opinion must be evaluated against the policy-defaulted replica count (3), not the authored one (absent, so 1)",
			len(tscs), topologyKeys(tscs))
	}

	if got, want := tscs[0].TopologyKey, "kubernetes.io/hostname"; got != want {
		t.Errorf("constraint[0].TopologyKey = %q, want %q", got, want)
	}
	if got, want := tscs[0].WhenUnsatisfiable, corev1.DoNotSchedule; got != want {
		t.Errorf("constraint[0].WhenUnsatisfiable = %q, want %q", got, want)
	}
	if got, want := tscs[1].TopologyKey, "topology.kubernetes.io/zone"; got != want {
		t.Errorf("constraint[1].TopologyKey = %q, want %q", got, want)
	}
	if got, want := tscs[1].WhenUnsatisfiable, corev1.ScheduleAnyway; got != want {
		t.Errorf("constraint[1].WhenUnsatisfiable = %q, want %q", got, want)
	}
	for i, tsc := range tscs {
		if tsc.MaxSkew != 1 {
			t.Errorf("constraint[%d].MaxSkew = %d, want 1", i, tsc.MaxSkew)
		}
		if tsc.LabelSelector == nil {
			t.Fatalf("constraint[%d].LabelSelector is nil; a constraint with no selector spreads every pod in the namespace", i)
		}
		if got := tsc.LabelSelector.MatchLabels["app"]; got != "backend" {
			t.Errorf("constraint[%d].LabelSelector matches app=%q, want app=backend", i, got)
		}
	}
}

// TestWorkerTopologySpread_NoPolicyMeansNoSpread pins the other half of the
// pair: the identical document under NoopPolicy gets one replica and no
// constraints.
//
// This is the case every golden already exercises, and on its own it says
// nothing about the coupling — it is only informative next to the test above,
// where the same document under a policy gets two constraints. Together they
// make "the policy step is what produces the spread" a red/green fact.
func TestWorkerTopologySpread_NoPolicyMeansNoSpread(t *testing.T) {
	dep := buildWorkerDeployment(t, workerSchedulingDoc(), nil)

	if dep.Spec.Replicas == nil {
		t.Fatal("Replicas is nil")
	}
	if *dep.Spec.Replicas != 1 {
		t.Fatalf("Replicas = %d, want 1 (no policy default, no authored value)", *dep.Spec.Replicas)
	}
	if tscs := dep.Spec.Template.Spec.TopologySpreadConstraints; len(tscs) != 0 {
		t.Fatalf("got %d topology spread constraints %v, want 0 at a single replica", len(tscs), topologyKeys(tscs))
	}
}

// TestWorkerTopologySpread_AuthoredReplicasIgnorePolicyDefault pins the
// precondition the pair above rests on: applyDefaultReplicas substitutes the
// policy default ONLY for a document that authored no count (enforce.go). With
// replicas authored, the default is inert.
//
// It also pins that the count drives WHICH tiers appear rather than the opinion
// being all-or-nothing: two replicas get the hostname tier alone, since the
// zone tier needs three (buildTopologySpreadConstraints, common.go). Without
// this case, reading the policy default unconditionally would satisfy the test
// above while being wrong for every document that sets its own count.
func TestWorkerTopologySpread_AuthoredReplicasIgnorePolicyDefault(t *testing.T) {
	comp := workerSchedulingDoc()
	comp.Properties["replicas"] = 2

	dep := buildWorkerDeployment(t, comp, &stubPolicy{defaultReplicas: int32ptr(9)})

	if dep.Spec.Replicas == nil {
		t.Fatal("Replicas is nil")
	}
	if *dep.Spec.Replicas != 2 {
		t.Fatalf("Replicas = %d, want 2 — an authored count must win over the policy default", *dep.Spec.Replicas)
	}
	tscs := dep.Spec.Template.Spec.TopologySpreadConstraints
	if len(tscs) != 1 {
		t.Fatalf("got %d topology spread constraints %v, want 1 at two replicas", len(tscs), topologyKeys(tscs))
	}
	if got, want := tscs[0].TopologyKey, "kubernetes.io/hostname"; got != want {
		t.Errorf("constraint[0].TopologyKey = %q, want %q", got, want)
	}
}

// TestWorkerTopologySpread_DisabledStaysDisabledUnderPolicy pins that
// `topologySpread: false` suppresses the opinion at any replica count, so a
// policy raising the count cannot resurrect the constraints.
func TestWorkerTopologySpread_DisabledStaysDisabledUnderPolicy(t *testing.T) {
	comp := workerSchedulingDoc()
	comp.Properties["topologySpread"] = false

	dep := buildWorkerDeployment(t, comp, &stubPolicy{defaultReplicas: int32ptr(3)})

	if dep.Spec.Replicas == nil {
		t.Fatal("Replicas is nil")
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("Replicas = %d, want 3", *dep.Spec.Replicas)
	}
	if tscs := dep.Spec.Template.Spec.TopologySpreadConstraints; len(tscs) != 0 {
		t.Fatalf("got %d topology spread constraints %v, want 0 — topologySpread: false must hold at any replica count", len(tscs), topologyKeys(tscs))
	}
}

// TestWorkerAffinity_IndependentOfPolicy pins the boundary of the coupling: the
// four-key `affinity` shorthand is evaluated from the shorthand and the
// component's own labels (buildAffinity, common.go) and reads no replica count,
// so a policy cannot move it.
//
// It is here so that "only topology spread depends on the policy" is a measured
// property of this kind rather than an inference from reading buildAffinity —
// the same document is built with and without a policy and the two affinities
// are asserted identical in every field the shorthand controls.
func TestWorkerAffinity_IndependentOfPolicy(t *testing.T) {
	shorthand := func() map[string]any {
		return map[string]any{
			"enablePodAntiAffinity": true,
			"podAntiAffinityType":   "required",
			"topologyKey":           "topology.kubernetes.io/zone",
		}
	}

	withPolicy := workerSchedulingDoc()
	withPolicy.Properties["affinity"] = shorthand()
	withoutPolicy := workerSchedulingDoc()
	withoutPolicy.Properties["affinity"] = shorthand()

	depWith := buildWorkerDeployment(t, withPolicy, &stubPolicy{defaultReplicas: int32ptr(3)})
	depWithout := buildWorkerDeployment(t, withoutPolicy, nil)

	for _, tc := range []struct {
		name string
		dep  *appsv1.Deployment
	}{
		{"policy defaulting replicas to 3", depWith},
		{"no policy", depWithout},
	} {
		aff := tc.dep.Spec.Template.Spec.Affinity
		if aff == nil || aff.PodAntiAffinity == nil {
			t.Fatalf("%s: no pod anti-affinity emitted", tc.name)
		}
		required := aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		if len(required) != 1 {
			t.Fatalf("%s: got %d required anti-affinity terms, want 1", tc.name, len(required))
		}
		if got, want := required[0].TopologyKey, "topology.kubernetes.io/zone"; got != want {
			t.Errorf("%s: TopologyKey = %q, want %q", tc.name, got, want)
		}
		if required[0].LabelSelector == nil {
			t.Fatalf("%s: LabelSelector is nil", tc.name)
		}
		if got := required[0].LabelSelector.MatchLabels["app"]; got != "backend" {
			t.Errorf("%s: LabelSelector matches app=%q, want app=backend", tc.name, got)
		}
		if aff.NodeAffinity != nil {
			t.Errorf("%s: NodeAffinity emitted without a nodeSelector in the shorthand", tc.name)
		}
	}
}
