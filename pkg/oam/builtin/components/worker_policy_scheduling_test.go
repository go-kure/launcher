package components_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	// Asserted rather than assumed. If the config ever stops implementing
	// Enforceable, ApplyPolicy is skipped — but not silently: the three tests
	// that assert a policy-defaulted count would fail anyway. What they would
	// NOT do is say why; each would report "Replicas = 1, want 3" and send the
	// reader after the replica logic rather than the type assertion that
	// actually broke. This names the seam. The tests asserting no policy effect
	// would go on passing, so the failure would also look narrower than it is.
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
		// Generate returns []*client.Object — a slice of pointers TO an
		// interface — so both the pointer and the interface it holds are
		// separately nil-able. Neither is reachable from the worker path today;
		// the guards are here so a future one fails as a named assertion rather
		// than a panic stack, which is the whole point of asserting in a test.
		if obj == nil {
			continue
		}
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			if dep == nil {
				t.Fatal("the generated objects carry a typed-nil *appsv1.Deployment; every assertion below would panic instead of failing")
			}
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

// assertAppSelector asserts sel is EXACTLY the selector this package builds for
// a scheduling position — selectorFrom(appLabels(name)) (common.go), i.e. the
// single label app=<name> and no match expressions.
//
// Exact, not "contains app=<name>": the generated pods carry only that one
// label (worker.go, dep.Spec.Template.Labels = appLabels(app.Name)), so a
// selector that additionally required, say, tier=<name> would select no pods at
// all — the constraint silently stops constraining anything — while a
// membership check on app alone still reads green.
func assertAppSelector(t *testing.T, sel *metav1.LabelSelector, name, where string) {
	t.Helper()
	if want := map[string]string{"app": name}; !maps.Equal(sel.MatchLabels, want) {
		t.Errorf("%s: LabelSelector.MatchLabels = %v, want exactly %v", where, sel.MatchLabels, want)
	}
	if len(sel.MatchExpressions) != 0 {
		// Not "an expression matches nothing" — `app In [backend]` would match
		// the same pods. The reason to reject is narrower and firmer: every
		// scheduling selector in this package comes from selectorFrom, which
		// sets MatchLabels and nothing else (common.go), so ANY expression here
		// is unauthored, and an unauthored requirement can only narrow — down to
		// selecting no pods at all, which silently disarms the constraint.
		t.Errorf("%s: LabelSelector.MatchExpressions = %v, want none — selectorFrom builds MatchLabels only, so any expression here is unauthored and can only narrow past app=%s",
			where, sel.MatchExpressions, name)
	}
}

// appSelector returns the selector this package builds for a scheduling
// position, for use as the expected value in a whole-object comparison. It
// mirrors selectorFrom(appLabels(name)) (common.go) and is deliberately a
// separate literal rather than a call into the production helper: a test whose
// expected value is computed by the code under test asserts nothing.
func appSelector(name string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}
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
			// Nothing, not everything: LabelSelectorAsSelector maps a nil
			// selector to labels.Nothing() and an EMPTY one to
			// labels.Everything() (apimachinery, meta/v1/helpers.go). So a nil
			// selector makes the constraint count no pods and spread nothing —
			// it disarms the constraint rather than widening it.
			t.Fatalf("constraint[%d].LabelSelector is nil; apimachinery maps nil to labels.Nothing(), so the constraint would count no pods and spread nothing", i)
		}
		assertAppSelector(t, tsc.LabelSelector, "backend", fmt.Sprintf("constraint[%d]", i))
	}

	// The assertions above are a SUBSET of the constraint, and a subset oracle
	// is the same weakness assertAppSelector exists to avoid one level down.
	// TopologySpreadConstraint also carries MinDomains,
	// NodeAffinityPolicy, NodeTaintsPolicy and MatchLabelKeys; a regression
	// setting any of them leaves every assertion above green while changing
	// where the scheduler puts the pods. Measured, not assumed: adding
	// MinDomains: 5 to the hostname constraint in buildTopologySpreadConstraints
	// (common.go) leaves every assertion above green, and within this package
	// only the exact-object comparison below fails.
	//
	// Repository-wide it is also caught by TestFixtures/params-scalar, because
	// buildTopologySpreadConstraints is shared with webservice and that golden
	// renders the hostname constraint verbatim. That is a golden diff on an
	// unrelated kind, not an oracle for the worker path — it names no field and
	// would not survive the fixture being retired, so it is not what this test
	// relies on.
	//
	// So the exact object below is the oracle; the assertions above are the
	// diagnostic that names which field moved.
	want := []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     appSelector("backend"),
		},
		{
			MaxSkew:           1,
			TopologyKey:       "topology.kubernetes.io/zone",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     appSelector("backend"),
		},
	}
	if !reflect.DeepEqual(tscs, want) {
		t.Errorf("topology spread constraints are not exactly what the opinion should emit at 3 replicas\n  got:  %+v\n  want: %+v", tscs, want)
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
	for _, tc := range []struct {
		name         string
		authored     int
		wantReplicas int32
		wantKeys     []string
	}{
		{
			name:         "two authored replicas take the hostname tier only",
			authored:     2,
			wantReplicas: 2,
			wantKeys:     []string{"kubernetes.io/hostname"},
		},
		{
			// The boundary, and the only count at which "authored" is
			// distinguishable from "absent" at all: an unauthored document also
			// arrives at 1, because that is parseReplicas' fallback (common.go).
			// So a defaulting rule keyed on the VALUE rather than on
			// explicitness — `current != 1` substituted for
			// applyDefaultReplicas' `explicit` (enforce.go) — is invisible to
			// every other case in this file, yet it would raise a deliberate
			// single-replica worker to the policy default and hand it spread
			// constraints the document asked not to have.
			name:         "one authored replica is explicit, not the intrinsic default",
			authored:     1,
			wantReplicas: 1,
			wantKeys:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := workerSchedulingDoc()
			comp.Properties["replicas"] = tc.authored

			dep := buildWorkerDeployment(t, comp, &stubPolicy{defaultReplicas: int32ptr(9)})

			if dep.Spec.Replicas == nil {
				t.Fatal("Replicas is nil")
			}
			if *dep.Spec.Replicas != tc.wantReplicas {
				t.Fatalf("Replicas = %d, want %d — an authored count must win over the policy default (9)",
					*dep.Spec.Replicas, tc.wantReplicas)
			}
			tscs := dep.Spec.Template.Spec.TopologySpreadConstraints
			if got := topologyKeys(tscs); !slices.Equal(got, tc.wantKeys) {
				t.Fatalf("topology spread constraint keys = %v, want %v (at %d replicas)",
					got, tc.wantKeys, tc.wantReplicas)
			}
		})
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
// are compared WHOLE.
//
// Whole, not field-by-field: a subset comparison only refutes the policy moving
// the fields the subset happens to name, so a regression that (say) appended a
// preferred anti-affinity term once the effective replica count exceeded one
// would leave every named assertion green while changing how the scheduler
// places the pods. reflect.DeepEqual over *corev1.Affinity covers the fields
// below, the ones buildAffinity writes on the other branch, and any field added
// later.
//
// The cross-build comparison alone is only half an oracle, though: two builds
// that moved IDENTICALLY are still equal to each other. It was tempting to say
// the literal assertions close that half, and they do not — they are a subset
// too. Measured, not assumed, and measured against the oracle as it stood
// BEFORE tc.want below was added — at HEAD both mutations fail, which is the
// point of the fix, so do not read these as claims about the current tree:
// adding Namespaces: []string{"other"} to the anti-affinity term in
// buildAffinity (common.go), which stops anti-affinity considering sibling pods
// in the workload's own namespace, kept this entire repository's tests passing;
// so did adding a MatchFields requirement to the node selector term, which pins
// the pods to a named node nobody asked for.
//
// So this test now carries THREE assertions, and each closes a different hole:
// tc.want pins what the shorthand must produce (catches an identical move in
// any field), the cross-build DeepEqual pins that the policy did not move it
// (catches a divergent move), and tc.assert names which field broke in the
// failure output (diagnostic only — it proves nothing the first two do not).
//
// Both shorthand shapes are exercised, since they take different branches of
// buildAffinity (common.go): required-vs-preferred anti-affinity, and the
// nodeSelector branch that emits NodeAffinity at all.
func TestWorkerAffinity_IndependentOfPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		shorthand func() map[string]any
		// want is the COMPLETE affinity the shorthand must produce — every
		// field of corev1.Affinity, not the ones the assertions below happen to
		// name. Written as a literal rather than built by calling buildAffinity:
		// an expected value computed by the code under test asserts nothing.
		want   func() *corev1.Affinity
		assert func(t *testing.T, aff *corev1.Affinity, where string)
	}{
		{
			name: "required anti-affinity, no node selector",
			shorthand: func() map[string]any {
				return map[string]any{
					"enablePodAntiAffinity": true,
					"podAntiAffinityType":   "required",
					"topologyKey":           "topology.kubernetes.io/zone",
				}
			},
			want: func() *corev1.Affinity {
				return &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
							LabelSelector: appSelector("backend"),
							TopologyKey:   "topology.kubernetes.io/zone",
						}},
					},
				}
			},
			assert: func(t *testing.T, aff *corev1.Affinity, where string) {
				t.Helper()
				required := aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				if len(required) != 1 {
					t.Fatalf("%s: got %d required anti-affinity terms, want 1", where, len(required))
				}
				if got, want := required[0].TopologyKey, "topology.kubernetes.io/zone"; got != want {
					t.Errorf("%s: TopologyKey = %q, want %q", where, got, want)
				}
				if required[0].LabelSelector == nil {
					t.Fatalf("%s: LabelSelector is nil", where)
				}
				assertAppSelector(t, required[0].LabelSelector, "backend", where)
				if n := len(aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution); n != 0 {
					t.Errorf("%s: got %d preferred anti-affinity terms, want 0 for podAntiAffinityType: required", where, n)
				}
				if aff.NodeAffinity != nil {
					t.Errorf("%s: NodeAffinity emitted without a nodeSelector in the shorthand", where)
				}
			},
		},
		{
			name: "preferred anti-affinity with a node selector",
			shorthand: func() map[string]any {
				return map[string]any{
					"enablePodAntiAffinity": true,
					"podAntiAffinityType":   "preferred",
					"nodeSelector":          map[string]any{"disktype": "ssd"},
				}
			},
			want: func() *corev1.Affinity {
				return &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
							Weight: 100,
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: appSelector("backend"),
								// parseAffinity's default, not authored above.
								TopologyKey: "kubernetes.io/hostname",
							},
						}},
					},
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{{
								MatchExpressions: []corev1.NodeSelectorRequirement{{
									Key:      "disktype",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"ssd"},
								}},
							}},
						},
					},
				}
			},
			assert: func(t *testing.T, aff *corev1.Affinity, where string) {
				t.Helper()
				preferred := aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
				if len(preferred) != 1 {
					t.Fatalf("%s: got %d preferred anti-affinity terms, want 1", where, len(preferred))
				}
				if got, want := preferred[0].Weight, int32(100); got != want {
					t.Errorf("%s: preferred[0].Weight = %d, want %d", where, got, want)
				}
				if got, want := preferred[0].PodAffinityTerm.TopologyKey, "kubernetes.io/hostname"; got != want {
					t.Errorf("%s: preferred[0].TopologyKey = %q, want %q (parseAffinity's default)", where, got, want)
				}
				if preferred[0].PodAffinityTerm.LabelSelector == nil {
					t.Fatalf("%s: preferred[0].LabelSelector is nil", where)
				}
				assertAppSelector(t, preferred[0].PodAffinityTerm.LabelSelector, "backend", where)
				if n := len(aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution); n != 0 {
					t.Errorf("%s: got %d required anti-affinity terms, want 0 for podAntiAffinityType: preferred", where, n)
				}
				if aff.NodeAffinity == nil {
					t.Fatalf("%s: no node affinity emitted for a shorthand nodeSelector", where)
				}
				// RequiredDuringScheduling... is a *corev1.NodeSelector, nil-able
				// independently of NodeAffinity itself. buildAffinity always
				// populates it in the branch that sets NodeAffinity at all
				// (common.go), so this is unreachable today — but a regression
				// moving the shorthand's nodeSelector to the preferred arm would
				// otherwise panic on .NodeSelectorTerms instead of failing here.
				if aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
					t.Fatalf("%s: node affinity carries no required node selector; the shorthand nodeSelector must land on the required arm", where)
				}
				terms := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
				if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
					t.Fatalf("%s: node selector terms = %+v, want one term carrying one match expression", where, terms)
				}
				req := terms[0].MatchExpressions[0]
				if req.Key != "disktype" || req.Operator != corev1.NodeSelectorOpIn || !slices.Equal(req.Values, []string{"ssd"}) {
					t.Errorf("%s: node selector requirement = %+v, want disktype In [ssd]", where, req)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withPolicy := workerSchedulingDoc()
			withPolicy.Properties["affinity"] = tc.shorthand()
			withoutPolicy := workerSchedulingDoc()
			withoutPolicy.Properties["affinity"] = tc.shorthand()

			depWith := buildWorkerDeployment(t, withPolicy, &stubPolicy{defaultReplicas: int32ptr(3)})
			depWithout := buildWorkerDeployment(t, withoutPolicy, nil)

			// The policy must actually have bitten on one of the two builds.
			// Without this the invariance below is satisfied trivially by a
			// policy step that did nothing at all — including one deleted
			// outright — and the test would assert nothing.
			if depWith.Spec.Replicas == nil || *depWith.Spec.Replicas != 3 {
				t.Fatal("policy build: replicas were not defaulted to 3, so the two builds below differ in nothing and the comparison proves nothing")
			}
			if depWithout.Spec.Replicas == nil || *depWithout.Spec.Replicas != 1 {
				t.Fatal("no-policy build: replicas is not 1")
			}

			affWith := depWith.Spec.Template.Spec.Affinity
			affWithout := depWithout.Spec.Template.Spec.Affinity
			if affWith == nil || affWith.PodAntiAffinity == nil {
				t.Fatal("policy defaulting replicas to 3: no pod anti-affinity emitted")
			}
			if affWithout == nil || affWithout.PodAntiAffinity == nil {
				t.Fatal("no policy: no pod anti-affinity emitted")
			}

			if !reflect.DeepEqual(affWith, affWithout) {
				t.Errorf("affinity moved with the policy; it reads no replica count and must not\n  with policy (3 replicas): %+v\n  no policy (1 replica):    %+v",
					affWith, affWithout)
			}

			// The other half of the oracle: what the shorthand must produce, in
			// full. Applied to both builds rather than relying on the comparison
			// above to carry it across — if that comparison is the thing that
			// broke, a want-check on one build alone would report only half the
			// story.
			want := tc.want()
			if !reflect.DeepEqual(affWith, want) {
				t.Errorf("policy build: affinity is not exactly what the shorthand asks for\n  got:  %+v\n  want: %+v", affWith, want)
			}
			if !reflect.DeepEqual(affWithout, want) {
				t.Errorf("no-policy build: affinity is not exactly what the shorthand asks for\n  got:  %+v\n  want: %+v", affWithout, want)
			}

			tc.assert(t, affWith, "policy defaulting replicas to 3")
			tc.assert(t, affWithout, "no policy")
		})
	}
}
