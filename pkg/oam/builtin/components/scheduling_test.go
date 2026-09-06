package components_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// schedulingError runs the deployment handler over props and returns the error,
// for the rejection tables below.
func schedulingError(t *testing.T, props map[string]any) error {
	t.Helper()
	props["image"] = "nginx:1.27"
	h := &components.DeploymentHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "app",
		Type:       "deployment",
		Properties: props,
	}, "default")
	return err
}

// TestDeploymentScheduling_Unauthored is the acceptance criterion for
// go-kure/launcher#412 stated as a test: publishing a property nobody has
// authored must change no output. Every existing golden covers this too, but
// only implicitly — this says it directly.
func TestDeploymentScheduling_Unauthored(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{"image": "nginx:1.27"})
	ps := dep.Spec.Template.Spec
	if ps.Affinity != nil {
		t.Errorf("Affinity = %+v, want nil for an unauthored document", ps.Affinity)
	}
	if ps.Tolerations != nil {
		t.Errorf("Tolerations = %+v, want nil for an unauthored document", ps.Tolerations)
	}
	if ps.TopologySpreadConstraints != nil {
		t.Errorf("TopologySpreadConstraints = %+v, want nil for an unauthored document", ps.TopologySpreadConstraints)
	}
}

// TestDeploymentScheduling_ExplicitNullIsOmission pins the null-as-omission
// convention on all three keys: pkg/oam's property validator reads an explicit
// null under an optional property as absent, so these documents are
// schema-valid and must parse to nothing rather than failing a type check.
func TestDeploymentScheduling_ExplicitNullIsOmission(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image":                     "nginx:1.27",
		"affinity":                  nil,
		"tolerations":               nil,
		"topologySpreadConstraints": nil,
	})
	ps := dep.Spec.Template.Spec
	if ps.Affinity != nil || ps.Tolerations != nil || ps.TopologySpreadConstraints != nil {
		t.Errorf("an explicit null produced scheduling state: affinity=%+v tolerations=%+v tscs=%+v",
			ps.Affinity, ps.Tolerations, ps.TopologySpreadConstraints)
	}
}

// TestDeploymentScheduling_AffinityRoundTrip authors five of corev1.Affinity's
// six scheduling arms and asserts each reaches the emitted pod spec unchanged.
// Nothing here is inferred from the component — that is the difference from the
// four-key shorthand, which fills the selector in from the component's own app
// label.
//
// Five, not six, and the count is stated rather than rounded up: the sixth arm,
// podAntiAffinity's requiredDuringSchedulingIgnoredDuringExecution, is pinned by
// TestDeploymentScheduling_EmptyLabelSelectorAccepted and by
// TestDeploymentScheduling_MismatchLabelKeysMayOverlapSelector, both of which
// build their document on that arm and both of which fail if parseRawAffinity
// stops assigning it — verified by deleting that assignment. PodAffinityTerm's
// mismatchLabelKeys is likewise pinned by the second of those, verified the same
// way. So both of the things this test leaves out are pinned elsewhere; the
// count is stated here only so a reader checking the claim against the input
// below does not have to discover the gap for themselves. It is not a claim
// that every field of every affinity struct is pinned somewhere — that would be
// the same unverified completeness assertion this comment exists to retract.
//
// "Asserts" is meant literally: a round-trip test that authors a field without
// asserting it is indistinguishable from one that does not author it at all,
// because the assignment can be deleted and the test stays green. Two
// PodAffinityTerm fields were in exactly that state —
// namespaceSelector and matchLabelKeys were parsed and assigned
// (scheduling.go, parsePodAffinityTerm) with no assertion anywhere in the
// package or in the golden fixture, so both could be silently dropped. That is
// the same accept-and-drop failure tolerationSeconds had. Every field this test
// authors must therefore also be read back below.
func TestDeploymentScheduling_AffinityRoundTrip(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"affinity": map[string]any{
			"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
					"nodeSelectorTerms": []any{
						map[string]any{
							"matchExpressions": []any{
								map[string]any{"key": "kubernetes.io/arch", "operator": "In", "values": []any{"amd64"}},
							},
							"matchFields": []any{
								map[string]any{"key": "metadata.name", "operator": "In", "values": []any{"node-1"}},
							},
						},
					},
				},
				"preferredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"weight": 40,
						"preference": map[string]any{
							"matchExpressions": []any{
								map[string]any{"key": "disk", "operator": "Exists"},
							},
						},
					},
				},
			},
			"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"topologyKey":       "kubernetes.io/hostname",
						"labelSelector":     map[string]any{"matchLabels": map[string]any{"tier": "cache"}},
						"namespaces":        []any{"other"},
						"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": "prod"}},
						"matchLabelKeys":    []any{"pod-template-hash"},
					},
				},
				// Both arms of podAffinity are authored, not just the required
				// one: parseRawAffinity assigns them on separate lines
				// (scheduling.go, the corev1.PodAffinity literal), so a test that
				// authors only `required` lets the `preferred` assignment be
				// deleted with the suite still green. Proven by mutation.
				"preferredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"weight": 25,
						"podAffinityTerm": map[string]any{
							"topologyKey":   "topology.kubernetes.io/region",
							"labelSelector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
						},
					},
				},
			},
			"podAntiAffinity": map[string]any{
				"preferredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"weight": 100,
						"podAffinityTerm": map[string]any{
							"topologyKey":   "topology.kubernetes.io/zone",
							"labelSelector": map[string]any{"matchLabels": map[string]any{"app": "app"}},
						},
					},
				},
			},
		},
	})

	af := dep.Spec.Template.Spec.Affinity
	if af == nil {
		t.Fatal("Affinity is nil")
	}
	// Each arm is guarded before it is dereferenced. A dropped arm is a
	// realistic regression here (that is what the ordering guards exist for),
	// and an unguarded chain would turn it into a nil-pointer panic whose stack
	// names the test rather than the field that went missing.
	if af.NodeAffinity == nil {
		t.Fatal("Affinity.NodeAffinity is nil — the authored nodeAffinity was dropped")
	}
	if af.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatal("NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution is nil")
	}

	terms := af.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 {
		t.Fatalf("nodeSelectorTerms = %d, want 1", len(terms))
	}
	if len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("matchExpressions = %d, want 1", len(terms[0].MatchExpressions))
	}
	expr := terms[0].MatchExpressions[0]
	if expr.Key != "kubernetes.io/arch" {
		t.Errorf("matchExpressions[0].Key = %q, want %q", expr.Key, "kubernetes.io/arch")
	}
	if expr.Operator != corev1.NodeSelectorOpIn {
		t.Errorf("matchExpressions[0].Operator = %q, want In", expr.Operator)
	}
	if got := expr.Values; len(got) != 1 || got[0] != "amd64" {
		t.Errorf("matchExpressions[0].Values = %v, want [amd64]", got)
	}
	// matchFields keys do not take the qualified-name rule matchExpressions keys
	// take — they take a narrower one. `metadata.name` is the only key upstream's
	// nodeFieldSelectorValidators registers, so this is both the happy path and the
	// only key that has one; the rejections are in
	// TestDeploymentScheduling_AffinityRejections.
	if len(terms[0].MatchFields) != 1 {
		t.Fatalf("matchFields = %d, want 1", len(terms[0].MatchFields))
	}
	field := terms[0].MatchFields[0]
	if field.Key != "metadata.name" {
		t.Errorf("matchFields[0].Key = %q, want %q", field.Key, "metadata.name")
	}
	if field.Operator != corev1.NodeSelectorOpIn {
		t.Errorf("matchFields[0].Operator = %q, want In", field.Operator)
	}
	if got := field.Values; len(got) != 1 || got[0] != "node-1" {
		t.Errorf("matchFields[0].Values = %v, want [node-1]", got)
	}

	preferred := af.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(preferred) != 1 || preferred[0].Weight != 40 {
		t.Fatalf("nodeAffinity preferred = %+v, want one term of weight 40", preferred)
	}
	if len(preferred[0].Preference.MatchExpressions) != 1 {
		t.Fatalf("preference matchExpressions = %d, want 1", len(preferred[0].Preference.MatchExpressions))
	}
	if got := preferred[0].Preference.MatchExpressions[0].Key; got != "disk" {
		t.Errorf("preference key = %q, want disk", got)
	}
	if got := preferred[0].Preference.MatchExpressions[0].Operator; got != corev1.NodeSelectorOpExists {
		t.Errorf("preference operator = %q, want Exists", got)
	}

	if af.PodAffinity == nil {
		t.Fatal("Affinity.PodAffinity is nil — the authored podAffinity was dropped")
	}
	podReq := af.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(podReq) != 1 {
		t.Fatalf("podAffinity required = %d terms, want 1", len(podReq))
	}
	if got := podReq[0].TopologyKey; got != "kubernetes.io/hostname" {
		t.Errorf("podAffinity topologyKey = %q", got)
	}
	if podReq[0].LabelSelector == nil {
		t.Fatal("podAffinity labelSelector is nil")
	}
	if got := podReq[0].LabelSelector.MatchLabels["tier"]; got != "cache" {
		t.Errorf("podAffinity labelSelector.matchLabels[tier] = %q, want cache", got)
	}
	if got := podReq[0].Namespaces; len(got) != 1 || got[0] != "other" {
		t.Errorf("podAffinity namespaces = %v, want [other]", got)
	}
	// namespaceSelector and matchLabelKeys are separately nil-able fields of the
	// same term, and each is assigned by its own line in parsePodAffinityTerm.
	// Until this round nothing read either one back, so either assignment could
	// be deleted with the whole suite still green — proven by mutation, both
	// times.
	if podReq[0].NamespaceSelector == nil {
		t.Fatal("podAffinity namespaceSelector is nil — an authored namespaceSelector was dropped")
	}
	if got := podReq[0].NamespaceSelector.MatchLabels["env"]; got != "prod" {
		t.Errorf("podAffinity namespaceSelector.matchLabels[env] = %q, want prod", got)
	}
	if got := podReq[0].MatchLabelKeys; len(got) != 1 || got[0] != "pod-template-hash" {
		t.Errorf("podAffinity matchLabelKeys = %v, want [pod-template-hash]", got)
	}

	podPref := af.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(podPref) != 1 || podPref[0].Weight != 25 {
		t.Fatalf("podAffinity preferred = %+v, want one term of weight 25", podPref)
	}
	if got := podPref[0].PodAffinityTerm.TopologyKey; got != "topology.kubernetes.io/region" {
		t.Errorf("podAffinity preferred topologyKey = %q", got)
	}
	if podPref[0].PodAffinityTerm.LabelSelector == nil {
		t.Fatal("podAffinity preferred labelSelector is nil")
	}
	if got := podPref[0].PodAffinityTerm.LabelSelector.MatchLabels["tier"]; got != "web" {
		t.Errorf("podAffinity preferred labelSelector.matchLabels[tier] = %q, want web", got)
	}

	if af.PodAntiAffinity == nil {
		t.Fatal("Affinity.PodAntiAffinity is nil — the authored podAntiAffinity was dropped")
	}
	antiPref := af.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(antiPref) != 1 || antiPref[0].Weight != 100 {
		t.Fatalf("podAntiAffinity preferred = %+v, want one term of weight 100", antiPref)
	}
	if got := antiPref[0].PodAffinityTerm.TopologyKey; got != "topology.kubernetes.io/zone" {
		t.Errorf("podAntiAffinity topologyKey = %q", got)
	}
	if antiPref[0].PodAffinityTerm.LabelSelector == nil {
		t.Fatal("podAntiAffinity labelSelector is nil")
	}
	if got := antiPref[0].PodAffinityTerm.LabelSelector.MatchLabels["app"]; got != "app" {
		t.Errorf("podAntiAffinity labelSelector.matchLabels[app] = %q, want app", got)
	}
}

// TestDeploymentScheduling_EmptyLabelSelectorAccepted pins the one place this
// parser deliberately diverges from parseLabelSelector's volume-claim rule.
// Upstream distinguishes a null labelSelector (matches no pods) from an empty
// one (matches every pod in scope), so refusing `labelSelector: {}` would make
// a real upstream shape unexpressible.
func TestDeploymentScheduling_EmptyLabelSelectorAccepted(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"affinity": map[string]any{
			"podAntiAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"topologyKey":   "kubernetes.io/hostname",
						"labelSelector": map[string]any{},
					},
				},
			},
		},
	})
	af := dep.Spec.Template.Spec.Affinity
	if af == nil || af.PodAntiAffinity == nil || len(af.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Fatalf("want one podAntiAffinity required term, got affinity %+v", af)
	}
	sel := af.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].LabelSelector
	if sel == nil {
		t.Fatal("an authored empty labelSelector parsed to nil, collapsing the upstream empty/null distinction")
	}
	if len(sel.MatchLabels) != 0 || len(sel.MatchExpressions) != 0 {
		t.Errorf("labelSelector = %+v, want empty", sel)
	}
}

// TestDeploymentScheduling_MismatchLabelKeysMayOverlapSelector is the control for
// the asymmetry in parsePodAffinityTerm: matchLabelKeys may not name a key the
// selector already constrains, mismatchLabelKeys may. It looks like an oversight
// against the field docs — MismatchLabelKeys' own doc (k8s.io/api@v0.36.3
// core/v1/types.go:3999) says the key "is forbidden to exist in both
// mismatchLabelKeys and labelSelector" — so without this test the next reader
// closes the gap and makes launcher refuse a document the apiserver accepts.
//
// Upstream's validation, not its field doc, is the contract:
// ValidateMatchLabelKeysAndMismatchLabelKeys builds the forbidden-key map from
// matchLabelKeys alone (pkg/apis/core/validation/validation.go, release-1.36:8989),
// and :8983 states the reason — a mismatchLabelKey is merged as `NotIn`, so
// filtering further on the same key is a legitimate thing to want.
func TestDeploymentScheduling_MismatchLabelKeysMayOverlapSelector(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"affinity": map[string]any{
			"podAntiAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{
					map[string]any{
						"topologyKey": "kubernetes.io/hostname",
						"labelSelector": map[string]any{
							"matchLabels": map[string]any{"tier": "cache"},
							"matchExpressions": []any{
								map[string]any{"key": "zone", "operator": "Exists"},
							},
						},
						"mismatchLabelKeys": []any{"tier", "zone"},
					},
				},
			},
		},
	})
	af := dep.Spec.Template.Spec.Affinity
	if af == nil || af.PodAntiAffinity == nil || len(af.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Fatalf("want one podAntiAffinity required term, got affinity %+v", af)
	}
	term := af.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	if got := term.MismatchLabelKeys; len(got) != 2 || got[0] != "tier" || got[1] != "zone" {
		t.Errorf("MismatchLabelKeys = %v, want [tier zone] carried through unchanged", got)
	}
}

// TestDeploymentScheduling_TolerationSecondsSurvives is the regression test for
// the one field the raw toleration projection used to drop. Before this, an
// authored tolerationSeconds parsed to nothing and vanished from the pod
// template: the build SUCCEEDED and emitted a toleration with no eviction
// deadline, silently turning a time-bounded NoExecute toleration into an
// unbounded one. A pointer test, not a value test — nil (tolerate forever) and
// 0 (evict immediately) are different documents (k8s.io/api@v0.36.3
// core/v1/types.go:4111-4116).
func TestDeploymentScheduling_TolerationSecondsSurvives(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"tolerations": []any{
			map[string]any{
				"key": "node.kubernetes.io/unreachable", "operator": "Exists",
				"effect": "NoExecute", "tolerationSeconds": 300,
			},
			map[string]any{
				"key": "spot", "operator": "Exists",
				"effect": "NoExecute", "tolerationSeconds": 0,
			},
			map[string]any{"key": "plain", "operator": "Exists", "effect": "NoExecute"},
		},
	})
	tols := dep.Spec.Template.Spec.Tolerations
	if len(tols) != 3 {
		t.Fatalf("Tolerations = %d, want 3", len(tols))
	}
	if tols[0].TolerationSeconds == nil {
		t.Fatal("tolerations[0].TolerationSeconds is nil — an authored tolerationSeconds was dropped")
	}
	if got := *tols[0].TolerationSeconds; got != 300 {
		t.Errorf("tolerations[0].TolerationSeconds = %d, want 300", got)
	}
	// 0 is authorable and distinct from unset: it means evict immediately.
	if tols[1].TolerationSeconds == nil {
		t.Fatal("tolerations[1].TolerationSeconds is nil — an authored 0 was read as unset")
	}
	if got := *tols[1].TolerationSeconds; got != 0 {
		t.Errorf("tolerations[1].TolerationSeconds = %d, want 0", got)
	}
	if tols[2].TolerationSeconds != nil {
		t.Errorf("tolerations[2].TolerationSeconds = %d, want nil for an unauthored field", *tols[2].TolerationSeconds)
	}
}

// TestDeploymentScheduling_TolerationComparisonOperators pins the two operators
// the projection used to refuse outright. Lt and Gt are feature-gated upstream
// (TaintTolerationComparisonOperators, core/v1/types.go:4100) but are valid API
// values, and this package leaves other gated fields to the cluster too.
func TestDeploymentScheduling_TolerationComparisonOperators(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"tolerations": []any{
			map[string]any{"key": "capacity", "operator": "Gt", "value": "5"},
			map[string]any{"key": "capacity", "operator": "Lt", "value": "20"},
			// A canonical negative and a canonical zero, so the syntax check
			// added for the leading-zero/plus-sign/"-0" cases cannot quietly
			// become "reject anything that is not a positive integer".
			map[string]any{"key": "drift", "operator": "Gt", "value": "-5"},
			map[string]any{"key": "drift", "operator": "Lt", "value": "0"},
		},
	})
	tols := dep.Spec.Template.Spec.Tolerations
	if len(tols) != 4 {
		t.Fatalf("Tolerations = %d, want 4", len(tols))
	}
	if tols[0].Operator != corev1.TolerationOpGt || tols[0].Value != "5" {
		t.Errorf("tolerations[0] = %+v, want operator Gt value 5", tols[0])
	}
	if tols[1].Operator != corev1.TolerationOpLt || tols[1].Value != "20" {
		t.Errorf("tolerations[1] = %+v, want operator Lt value 20", tols[1])
	}
	if tols[2].Value != "-5" {
		t.Errorf("tolerations[2].Value = %q, want -5 accepted", tols[2].Value)
	}
	if tols[3].Value != "0" {
		t.Errorf("tolerations[3].Value = %q, want 0 accepted", tols[3].Value)
	}
}

// TestDeploymentScheduling_TolerationNullScalarIsOmission pins null-as-omission
// one level down, inside a toleration entry. The null cannot be filtered out
// before it reaches the parser: withoutExplicitNulls (deployment_spec.go) strips
// only top-level properties, and emission validation accepts a null under any
// optional field (property_validate.go's validatePropertyValue), so a nested null
// arrives intact and a direct type assertion on it made a schema-valid document
// fail conversion.
//
// The assertions below are behavioural, not just "no error": a null `key` must
// leave the entry keyless, which in turn is what makes the operator default to
// Exists rather than Equal.
func TestDeploymentScheduling_TolerationNullScalarIsOmission(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"tolerations": []any{map[string]any{
			"key":      nil,
			"operator": nil,
			"value":    nil,
			"effect":   "NoSchedule",
		}},
	})
	got := dep.Spec.Template.Spec.Tolerations
	if len(got) != 1 {
		t.Fatalf("Tolerations length = %d, want 1", len(got))
	}
	if got[0].Key != "" {
		t.Errorf("Key = %q, want empty — a null key is an omitted key", got[0].Key)
	}
	if got[0].Value != "" {
		t.Errorf("Value = %q, want empty", got[0].Value)
	}
	if got[0].Operator != corev1.TolerationOpExists {
		t.Errorf("Operator = %q, want Exists — the keyless default, reached only if the null key read as absent", got[0].Operator)
	}
	if got[0].Effect != corev1.TaintEffectNoSchedule {
		t.Errorf("Effect = %q, want NoSchedule", got[0].Effect)
	}
}

// TestDeploymentScheduling_TolerationRejections covers the cross-field rules
// that previously let launcher emit a toleration the apiserver refuses, plus
// the unknown-key rejection that is how tolerationSeconds stayed missing.
func TestDeploymentScheduling_TolerationRejections(t *testing.T) {
	cases := []struct {
		name       string
		toleration map[string]any
		want       string
	}{
		{
			"empty key with Equal",
			map[string]any{"operator": "Equal", "effect": "NoSchedule"},
			"must be 'Exists' when key is empty",
		},
		{
			// An omitted operator with an empty key defaults to Exists, not
			// Equal, so this is refused by the value rule rather than the key
			// rule — either way an authored value that matches nothing is
			// reported instead of emitted.
			"empty key with a value and no operator",
			map[string]any{"value": "batch", "effect": "NoSchedule"},
			"must be empty when operator is 'Exists'",
		},
		{
			"Exists with a value",
			map[string]any{"key": "dedicated", "operator": "Exists", "value": "batch"},
			"must be empty when operator is 'Exists'",
		},
		{
			"Gt with a non-integer value",
			map[string]any{"key": "capacity", "operator": "Gt", "value": "many"},
			"requires an integer",
		},
		// The three forms strconv.ParseInt accepts and the scheduler's own
		// matcher does not: Toleration.ToleratesTaint runs
		// content.IsDecimalInteger first and returns "no match" — not an error —
		// on any of them, so accepting these would emit a toleration that
		// silently tolerates nothing. See the check in parseTolerations for the
		// citation chain.
		{
			"Gt with a leading zero",
			map[string]any{"key": "capacity", "operator": "Gt", "value": "05"},
			"canonical form",
		},
		{
			"Lt with a plus sign",
			map[string]any{"key": "capacity", "operator": "Lt", "value": "+5"},
			"canonical form",
		},
		{
			"Gt with negative zero",
			map[string]any{"key": "capacity", "operator": "Gt", "value": "-0"},
			"canonical form",
		},
		// The other half of the same check, and it needs its own case because
		// the two halves reject disjoint sets. content.IsDecimalInteger
		// constrains the SYNTAX and says nothing about magnitude
		// (k8s.io/apimachinery@v0.36.3 pkg/api/validate/content/decimal_int.go:
		// 30-61 walks characters and never converts), so 2^63 — canonical in
		// every respect, just one past int64 — passes it and only
		// strconv.ParseInt refuses. Without this case the ParseInt arm is
		// unreachable from any test: the three cases above fail the syntax
		// check first, and "many" and the missing value do too, so deleting
		// ParseInt entirely left the whole repository suite green. The failure
		// it prevents is the same silent one: compareNumericValues parses with
		// ParseInt after IsDecimalInteger passes and returns false — no match,
		// no error — on overflow (k8s.io/api@v0.36.3 core/v1/toleration.go:
		// 87-90), so an out-of-range value would build and tolerate nothing.
		//
		// The wanted substring is deliberately the one the canonical-form
		// message cannot contain: that message reads "requires an integer in
		// canonical form", so matching on the comma pins which arm fired.
		{
			"Gt with a canonical value past int64",
			map[string]any{"key": "capacity", "operator": "Gt", "value": "9223372036854775808"},
			`requires an integer, got "9223372036854775808"`,
		},
		{
			"Lt with no value",
			map[string]any{"key": "capacity", "operator": "Lt"},
			"requires an integer",
		},
		{
			"unknown operator",
			map[string]any{"key": "dedicated", "operator": "Nope"},
			"must be 'Exists', 'Equal', 'Lt' or 'Gt'",
		},
		{
			"unknown key",
			map[string]any{"key": "dedicated", "operator": "Exists", "tolerationSecond": 30},
			`unrecognized key "tolerationSecond"`,
		},
		{
			"tolerationSeconds is not an integer",
			map[string]any{"key": "dedicated", "operator": "Exists", "tolerationSeconds": "300"},
			"must be an integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schedulingError(t, map[string]any{"tolerations": []any{tc.toleration}})
			if err == nil {
				t.Fatalf("got nil error, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestDeploymentScheduling_TolerationsRoundTrip reuses the pre-existing
// parseTolerations, so this asserts the wiring rather than the parsing.
func TestDeploymentScheduling_TolerationsRoundTrip(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"tolerations": []any{
			map[string]any{"key": "dedicated", "operator": "Equal", "value": "batch", "effect": "NoSchedule"},
			map[string]any{"operator": "Exists"},
		},
	})
	tols := dep.Spec.Template.Spec.Tolerations
	if len(tols) != 2 {
		t.Fatalf("Tolerations = %d, want 2", len(tols))
	}
	if tols[0].Key != "dedicated" || tols[0].Effect != corev1.TaintEffectNoSchedule {
		t.Errorf("tolerations[0] = %+v", tols[0])
	}
	if tols[0].Operator != corev1.TolerationOpEqual || tols[0].Value != "batch" {
		t.Errorf("tolerations[0] = %+v, want operator Equal value batch", tols[0])
	}
	if tols[1].Operator != corev1.TolerationOpExists {
		t.Errorf("tolerations[1].Operator = %q, want Exists", tols[1].Operator)
	}
}

func TestDeploymentScheduling_TopologySpreadConstraintsRoundTrip(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "nginx:1.27",
		"topologySpreadConstraints": []any{
			map[string]any{
				"maxSkew":            2,
				"topologyKey":        "topology.kubernetes.io/zone",
				"whenUnsatisfiable":  "DoNotSchedule",
				"labelSelector":      map[string]any{"matchLabels": map[string]any{"app": "app"}},
				"minDomains":         3,
				"nodeAffinityPolicy": "Honor",
				"nodeTaintsPolicy":   "Ignore",
				"matchLabelKeys":     []any{"pod-template-hash"},
			},
			map[string]any{
				"maxSkew":           1,
				"topologyKey":       "kubernetes.io/hostname",
				"whenUnsatisfiable": "ScheduleAnyway",
			},
		},
	})
	tscs := dep.Spec.Template.Spec.TopologySpreadConstraints
	if len(tscs) != 2 {
		t.Fatalf("TopologySpreadConstraints = %d, want 2", len(tscs))
	}
	first := tscs[0]
	if first.MaxSkew != 2 || first.TopologyKey != "topology.kubernetes.io/zone" {
		t.Errorf("tscs[0] = %+v", first)
	}
	if first.WhenUnsatisfiable != corev1.DoNotSchedule {
		t.Errorf("tscs[0].WhenUnsatisfiable = %q, want DoNotSchedule", first.WhenUnsatisfiable)
	}
	if first.LabelSelector == nil {
		t.Fatal("tscs[0].LabelSelector is nil — an authored labelSelector was dropped")
	}
	if got := first.LabelSelector.MatchLabels["app"]; got != "app" {
		t.Errorf("tscs[0].LabelSelector.MatchLabels[app] = %q, want app", got)
	}
	if first.MinDomains == nil || *first.MinDomains != 3 {
		t.Errorf("tscs[0].MinDomains = %v, want 3", first.MinDomains)
	}
	if first.NodeAffinityPolicy == nil || *first.NodeAffinityPolicy != corev1.NodeInclusionPolicyHonor {
		t.Errorf("tscs[0].NodeAffinityPolicy = %v, want Honor", first.NodeAffinityPolicy)
	}
	if first.NodeTaintsPolicy == nil || *first.NodeTaintsPolicy != corev1.NodeInclusionPolicyIgnore {
		t.Errorf("tscs[0].NodeTaintsPolicy = %v, want Ignore", first.NodeTaintsPolicy)
	}
	if got := first.MatchLabelKeys; len(got) != 1 || got[0] != "pod-template-hash" {
		t.Errorf("tscs[0].MatchLabelKeys = %v", got)
	}
	// The second constraint authors none of the optional fields: they must stay
	// nil rather than being defaulted here, since defaulting them would emit
	// values the author did not write.
	if tscs[1].MinDomains != nil || tscs[1].NodeAffinityPolicy != nil || tscs[1].NodeTaintsPolicy != nil ||
		tscs[1].LabelSelector != nil || len(tscs[1].MatchLabelKeys) != 0 {
		t.Errorf("tscs[1] = %+v, want every optional field unset", tscs[1])
	}
}

// TestDeploymentScheduling_AffinityRejections covers the constraints taken from
// the pinned k8s.io/api field docs, plus the two rejections that are launcher's
// own (an affinity with no arm set, and an empty node selector term).
func TestDeploymentScheduling_AffinityRejections(t *testing.T) {
	nodeTerm := func(expr map[string]any) map[string]any {
		return map[string]any{
			"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
					"nodeSelectorTerms": []any{map[string]any{"matchExpressions": []any{expr}}},
				},
			},
		}
	}
	nodeFields := func(req map[string]any) map[string]any {
		return map[string]any{
			"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
					"nodeSelectorTerms": []any{map[string]any{"matchFields": []any{req}}},
				},
			},
		}
	}
	cases := []struct {
		name     string
		affinity map[string]any
		want     string
	}{
		{"no arm set", map[string]any{}, "set nodeAffinity, podAffinity or podAntiAffinity"},
		{"unknown key", map[string]any{"nodeAffinityy": map[string]any{}}, `unrecognized key "nodeAffinityy"`},
		{"nodeAffinity with no arm", map[string]any{"nodeAffinity": map[string]any{}}, "affinity.nodeAffinity: set requiredDuring"},
		{"podAffinity with no arm", map[string]any{"podAffinity": map[string]any{}}, "affinity.podAffinity: set requiredDuring"},
		{
			"empty nodeSelectorTerms",
			map[string]any{"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{"nodeSelectorTerms": []any{}},
			}},
			"at least one term is required",
		},
		{
			"empty node selector term",
			map[string]any{"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{"nodeSelectorTerms": []any{map[string]any{}}},
			}},
			"an empty node selector term matches no nodes",
		},
		{"In with no values", nodeTerm(map[string]any{"key": "k", "operator": "In"}), "at least one value is required for operator In"},
		{"Exists with values", nodeTerm(map[string]any{"key": "k", "operator": "Exists", "values": []any{"v"}}), "must be empty for operator Exists"},
		{"Gt with two values", nodeTerm(map[string]any{"key": "k", "operator": "Gt", "values": []any{"1", "2"}}), "exactly one value is required for operator Gt"},
		{"Gt with a non-integer", nodeTerm(map[string]any{"key": "k", "operator": "Gt", "values": []any{"big"}}), `operator Gt requires an integer, got "big"`},
		{"unknown operator", nodeTerm(map[string]any{"key": "k", "operator": "Matches", "values": []any{"v"}}), `invalid value "Matches"`},
		{
			"weight out of range",
			map[string]any{"nodeAffinity": map[string]any{
				"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"weight":     101,
					"preference": map[string]any{"matchExpressions": []any{map[string]any{"key": "k", "operator": "Exists"}}},
				}},
			}},
			"must be between 1 and 100, got 101",
		},
		{
			"pod affinity term without topologyKey",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"labelSelector": map[string]any{"matchLabels": map[string]any{"a": "b"}},
				}},
			}},
			"topologyKey: required",
		},
		{
			"matchLabelKeys without a labelSelector",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey":    "kubernetes.io/hostname",
					"matchLabelKeys": []any{"a"},
				}},
			}},
			"cannot be set without labelSelector",
		},
		{
			"matchLabelKeys clashing with labelSelector",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey":    "kubernetes.io/hostname",
					"labelSelector":  map[string]any{"matchLabels": map[string]any{"a": "b"}},
					"matchLabelKeys": []any{"a"},
				}},
			}},
			"already constrained by labelSelector.matchLabels",
		},
		{
			// The half the matchLabels-only check missed. Upstream rejects this too:
			// PrepareForCreate merges each matchLabelKey into the selector as its own
			// `In` requirement, so the authored expression becomes the second
			// occurrence of "a" and ValidateMatchLabelKeysAndMismatchLabelKeys flags
			// it. See checkMatchLabelKeysAgainstSelector for the full citation chain.
			"matchLabelKeys clashing with labelSelector matchExpressions",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey": "kubernetes.io/hostname",
					"labelSelector": map[string]any{"matchExpressions": []any{
						map[string]any{"key": "a", "operator": "Exists"},
					}},
					"matchLabelKeys": []any{"a"},
				}},
			}},
			"already constrained by labelSelector.matchExpressions",
		},

		// Admission parity: everything below builds a document the parser used to
		// accept and the apiserver then rejects. Each cites the upstream validator
		// that refuses it, so the rule can be checked rather than trusted.

		{
			// nodeFieldSelectorValidators has exactly one entry, metav1.ObjectNameField
			// (validation.go, release-1.36:4993-4995).
			"matchFields with a key other than metadata.name",
			nodeFields(map[string]any{"key": "spec.nodeName", "operator": "In", "values": []any{"n1"}}),
			`invalid value "spec.nodeName", want "metadata.name"`,
		},
		{
			// ValidateNodeFieldSelectorRequirement's switch (validation.go:5001-5009)
			// has only In and NotIn; Exists falls to its default branch.
			"matchFields with an operator matchExpressions allows",
			nodeFields(map[string]any{"key": "metadata.name", "operator": "Exists"}),
			`invalid value "Exists" for matchFields, want In or NotIn`,
		},
		{
			// Same switch: In and NotIn require len(Values) == 1, not "at least one".
			"matchFields In with two values",
			nodeFields(map[string]any{"key": "metadata.name", "operator": "In", "values": []any{"n1", "n2"}}),
			"exactly one value is required for operator In in matchFields, got 2",
		},
		{
			// Ordering, not just rejection: Gt is valid on matchExpressions, so the
			// shared rules would have reported it as an arity problem and offered an
			// operator list that does not apply to this field. The matchFields entry
			// must be judged by the field-selector rules alone.
			"matchFields Gt is reported against matchFields, not the shared operator list",
			nodeFields(map[string]any{"key": "metadata.name", "operator": "Gt", "values": []any{"1"}}),
			`invalid value "Gt" for matchFields, want In or NotIn`,
		},
		{
			// validatePodAffinityTerm runs every entry through ValidateNamespaceName
			// (validation.go:5162-5163), which is NameIsDNSLabel.
			"pod affinity namespaces entry that is not a DNS label",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey": "kubernetes.io/hostname",
					"namespaces":  []any{"Bad NS"},
				}},
			}},
			`invalid namespace name "Bad NS"`,
		},
		{
			// validateLabelKeys -> ValidateLabelName per entry (validation.go:9063-9064).
			"matchLabelKeys entry that is not a qualified name",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey":    "kubernetes.io/hostname",
					"labelSelector":  map[string]any{"matchLabels": map[string]any{"a": "b"}},
					"matchLabelKeys": []any{"not a key"},
				}},
			}},
			`invalid label key "not a key"`,
		},
		{
			// Same validator, other list — the selector-overlap asymmetry above does
			// not extend to the qualified-name rule.
			"mismatchLabelKeys entry that is not a qualified name",
			map[string]any{"podAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey":       "kubernetes.io/hostname",
					"labelSelector":     map[string]any{"matchLabels": map[string]any{"a": "b"}},
					"mismatchLabelKeys": []any{"not a key"},
				}},
			}},
			`invalid label key "not a key"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schedulingError(t, map[string]any{"affinity": tc.affinity})
			if err == nil {
				t.Fatalf("got nil error, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestDeploymentScheduling_TopologySpreadConstraintRejections(t *testing.T) {
	valid := func(overrides map[string]any) map[string]any {
		m := map[string]any{
			"maxSkew":           1,
			"topologyKey":       "kubernetes.io/hostname",
			"whenUnsatisfiable": "DoNotSchedule",
		}
		for k, v := range overrides {
			if v == nil {
				delete(m, k)
				continue
			}
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name       string
		constraint map[string]any
		want       string
	}{
		{"missing maxSkew", valid(map[string]any{"maxSkew": nil}), "maxSkew: required"},
		{"zero maxSkew", valid(map[string]any{"maxSkew": 0}), "maxSkew: must be greater than 0"},
		{"missing topologyKey", valid(map[string]any{"topologyKey": nil}), "topologyKey: required"},
		{"missing whenUnsatisfiable", valid(map[string]any{"whenUnsatisfiable": nil}), "whenUnsatisfiable: required"},
		{"bad whenUnsatisfiable", valid(map[string]any{"whenUnsatisfiable": "Maybe"}), `invalid value "Maybe"`},
		{"unknown key", valid(map[string]any{"maxSkewww": 1}), `unrecognized key "maxSkewww"`},
		{"zero minDomains", valid(map[string]any{"minDomains": 0}), "minDomains: must be greater than 0"},
		{
			"minDomains with ScheduleAnyway",
			valid(map[string]any{"minDomains": 2, "whenUnsatisfiable": "ScheduleAnyway"}),
			"requires whenUnsatisfiable DoNotSchedule",
		},
		{"bad nodeAffinityPolicy", valid(map[string]any{"nodeAffinityPolicy": "Maybe"}), "want Honor or Ignore"},
		{"bad nodeTaintsPolicy", valid(map[string]any{"nodeTaintsPolicy": "Maybe"}), "want Honor or Ignore"},
		{"matchLabelKeys without a labelSelector", valid(map[string]any{"matchLabelKeys": []any{"a"}}), "cannot be set without labelSelector"},
		{
			"matchLabelKeys clashing with labelSelector matchLabels",
			valid(map[string]any{
				"labelSelector":  map[string]any{"matchLabels": map[string]any{"a": "b"}},
				"matchLabelKeys": []any{"a"},
			}),
			"already constrained by labelSelector.matchLabels",
		},
		{
			// Rejected on this path in every upstream configuration, not only after
			// the PrepareForCreate merge: with
			// MatchLabelKeysInPodTopologySpreadSelectorMerge off, the legacy
			// ValidateMatchLabelKeysInTopologySpread seeds its forbidden set from
			// matchExpressions keys directly and rejects unconditionally.
			"matchLabelKeys clashing with labelSelector matchExpressions",
			valid(map[string]any{
				"labelSelector": map[string]any{"matchExpressions": []any{
					map[string]any{"key": "a", "operator": "Exists"},
				}},
				"matchLabelKeys": []any{"a"},
			}),
			"already constrained by labelSelector.matchExpressions",
		},
		{
			// Admission parity, same validator as the PodAffinityTerm lists:
			// validateLabelKeys -> ValidateLabelName (validation.go,
			// release-1.36:9063-9064) applies to this field too.
			"matchLabelKeys entry that is not a qualified name",
			valid(map[string]any{
				"labelSelector":  map[string]any{"matchLabels": map[string]any{"a": "b"}},
				"matchLabelKeys": []any{"not a key"},
			}),
			`invalid label key "not a key"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schedulingError(t, map[string]any{"topologySpreadConstraints": []any{tc.constraint}})
			if err == nil {
				t.Fatalf("got nil error, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestDeploymentScheduling_TopologySpreadDuplicatePair pins the duplicate rule to
// the PAIR (topologyKey, whenUnsatisfiable), which is what
// ValidateSpreadConstraintNotRepeat actually checks (pkg/apis/core/validation/
// validation.go, release-1.36:8943-8951). The accepted case is the load-bearing
// half: two constraints sharing a topologyKey with different whenUnsatisfiable
// values are legal, so a topologyKey-uniqueness check here would be stricter than
// the API — the same over-reach an earlier round already rejected once.
func TestDeploymentScheduling_TopologySpreadDuplicatePair(t *testing.T) {
	constraint := func(action string) map[string]any {
		return map[string]any{
			"maxSkew":           1,
			"topologyKey":       "kubernetes.io/hostname",
			"whenUnsatisfiable": action,
		}
	}

	t.Run("identical pair is rejected", func(t *testing.T) {
		err := schedulingError(t, map[string]any{"topologySpreadConstraints": []any{
			constraint("DoNotSchedule"), constraint("DoNotSchedule"),
		}})
		if err == nil {
			t.Fatal("got nil error, want a duplicate-constraint rejection")
		}
		want := "duplicate constraint {kubernetes.io/hostname, DoNotSchedule}"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	})

	t.Run("same topologyKey with a different action is accepted", func(t *testing.T) {
		dep, _ := generateDeployment(t, "app", map[string]any{
			"image": "nginx:1.27",
			"topologySpreadConstraints": []any{
				constraint("DoNotSchedule"), constraint("ScheduleAnyway"),
			},
		})
		got := dep.Spec.Template.Spec.TopologySpreadConstraints
		if len(got) != 2 {
			t.Fatalf("TopologySpreadConstraints length = %d, want 2", len(got))
		}
		if got[0].WhenUnsatisfiable != corev1.DoNotSchedule || got[1].WhenUnsatisfiable != corev1.ScheduleAnyway {
			t.Errorf("whenUnsatisfiable = %q, %q; want DoNotSchedule, ScheduleAnyway",
				got[0].WhenUnsatisfiable, got[1].WhenUnsatisfiable)
		}
		if got[0].TopologyKey != got[1].TopologyKey {
			t.Errorf("topologyKey = %q, %q; want both to be the shared key",
				got[0].TopologyKey, got[1].TopologyKey)
		}
	})
}
