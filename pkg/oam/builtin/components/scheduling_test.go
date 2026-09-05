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

// TestDeploymentScheduling_AffinityRoundTrip authors every arm of
// corev1.Affinity and asserts each reaches the emitted pod spec unchanged.
// Nothing here is inferred from the component — that is the difference from the
// four-key shorthand, which fills the selector in from the component's own app
// label.
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
						"topologyKey":   "kubernetes.io/hostname",
						"labelSelector": map[string]any{"matchLabels": map[string]any{"tier": "cache"}},
						"namespaces":    []any{"other"},
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

	terms := af.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 {
		t.Fatalf("nodeSelectorTerms = %d, want 1", len(terms))
	}
	if got := terms[0].MatchExpressions[0].Key; got != "kubernetes.io/arch" {
		t.Errorf("matchExpressions[0].Key = %q, want %q", got, "kubernetes.io/arch")
	}
	// matchFields keys are field paths, not qualified names — a distinction the
	// parser has to make, since `metadata.name` is not a valid qualified name.
	if got := terms[0].MatchFields[0].Key; got != "metadata.name" {
		t.Errorf("matchFields[0].Key = %q, want %q", got, "metadata.name")
	}

	preferred := af.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(preferred) != 1 || preferred[0].Weight != 40 {
		t.Fatalf("nodeAffinity preferred = %+v, want one term of weight 40", preferred)
	}
	if got := preferred[0].Preference.MatchExpressions[0].Operator; got != corev1.NodeSelectorOpExists {
		t.Errorf("preference operator = %q, want Exists", got)
	}

	podReq := af.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(podReq) != 1 {
		t.Fatalf("podAffinity required = %d terms, want 1", len(podReq))
	}
	if got := podReq[0].TopologyKey; got != "kubernetes.io/hostname" {
		t.Errorf("podAffinity topologyKey = %q", got)
	}
	if got := podReq[0].LabelSelector.MatchLabels["tier"]; got != "cache" {
		t.Errorf("podAffinity labelSelector.matchLabels[tier] = %q, want cache", got)
	}
	if got := podReq[0].Namespaces; len(got) != 1 || got[0] != "other" {
		t.Errorf("podAffinity namespaces = %v, want [other]", got)
	}

	antiPref := af.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(antiPref) != 1 || antiPref[0].Weight != 100 {
		t.Fatalf("podAntiAffinity preferred = %+v, want one term of weight 100", antiPref)
	}
	if got := antiPref[0].PodAffinityTerm.TopologyKey; got != "topology.kubernetes.io/zone" {
		t.Errorf("podAntiAffinity topologyKey = %q", got)
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
	sel := dep.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].LabelSelector
	if sel == nil {
		t.Fatal("an authored empty labelSelector parsed to nil, collapsing the upstream empty/null distinction")
	}
	if len(sel.MatchLabels) != 0 || len(sel.MatchExpressions) != 0 {
		t.Errorf("labelSelector = %+v, want empty", sel)
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
	if tscs[1].MinDomains != nil || tscs[1].NodeAffinityPolicy != nil || tscs[1].NodeTaintsPolicy != nil || tscs[1].LabelSelector != nil {
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
