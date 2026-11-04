package components_test

import (
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

func hasQuantity(rl corev1.ResourceList, name corev1.ResourceName) bool {
	_, ok := rl[name]
	return ok
}

// TestUnlimitedLimit_OmitsTheLimit pins go-kure/launcher#1181: the string
// `unlimited` under limits.cpu or limits.memory opts the container out of the
// built-in memory-limit mirror, on the main container, init containers and
// sidecars of every workload kind, while leaving the request defaults alone.
func TestUnlimitedLimit_OmitsTheLimit(t *testing.T) {
	cases := []struct {
		name         string
		limits       map[string]any
		wantRequests map[corev1.ResourceName]string
		wantLimits   map[corev1.ResourceName]string
	}{
		{
			name:         "memory",
			limits:       map[string]any{"memory": "unlimited"},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{},
		},
		{
			name:         "cpu leaves the memory mirror in place",
			limits:       map[string]any{"cpu": "unlimited"},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"},
		},
		{
			name:         "both",
			limits:       map[string]any{"cpu": "unlimited", "memory": "unlimited"},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{},
		},
		{
			name:         "memory unlimited beside a cpu limit",
			limits:       map[string]any{"cpu": "2", "memory": "unlimited"},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "2"},
		},
	}
	for _, kind := range workloadKinds {
		for _, holder := range resourceHolders() {
			if holder.name == "sidecar" && !hasSidecars[kind.name] {
				continue
			}
			for _, tc := range cases {
				t.Run(kind.name+"/"+holder.name+"/"+tc.name, func(t *testing.T) {
					props := withProps(kind.props, holder.props(map[string]any{"limits": tc.limits}))
					res := holder.pick(podTemplateSpec(t, generateKind(t, kind.handler, kind.name, props))).Resources
					assertQuantities(t, "requests", res.Requests, tc.wantRequests)
					assertQuantities(t, "limits", res.Limits, tc.wantLimits)
				})
			}
		}
	}
}

// TestUnlimitedLimit_AuthoredRequestKept: the opt-out drops the limit only.
// An authored request, and a policy default request, still apply.
func TestUnlimitedLimit_AuthoredRequestKept(t *testing.T) {
	for _, kind := range workloadKinds {
		t.Run(kind.name+"/authored request", func(t *testing.T) {
			props := withProps(kind.props, map[string]any{"resources": map[string]any{
				"requests": map[string]any{"memory": "512Mi"},
				"limits":   map[string]any{"memory": "unlimited"},
			}})
			res := podTemplateSpec(t, generateKind(t, kind.handler, kind.name, props)).Containers[0].Resources
			assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "512Mi"})
			assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{})
		})
		t.Run(kind.name+"/policy default request", func(t *testing.T) {
			props := withProps(kind.props, map[string]any{"resources": map[string]any{
				"limits": map[string]any{"memory": "unlimited"},
			}})
			ps, err := generateKindWithPolicy(t, kind, props, &stubPolicy{defaultMemoryRequest: "256Mi"})
			if err != nil {
				t.Fatalf("ApplyPolicy: %v", err)
			}
			res := ps.Containers[0].Resources
			assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "256Mi"})
			assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{})
		})
	}
}

// TestUnlimitedLimit_StopsPolicyDefaultLimit: tier 1 beats tier 2. A policy
// default limit does not land on a resource the author opted out of, and still
// lands on the one they did not.
func TestUnlimitedLimit_StopsPolicyDefaultLimit(t *testing.T) {
	policy := &stubPolicy{defaultCPULimit: "500m", defaultMemoryLimit: "1Gi"}
	for _, kind := range workloadKinds {
		for _, tc := range []struct {
			name       string
			limits     map[string]any
			wantLimits map[corev1.ResourceName]string
		}{
			{"nothing authored", nil, map[corev1.ResourceName]string{corev1.ResourceCPU: "500m", corev1.ResourceMemory: "1Gi"}},
			{"cpu unlimited", map[string]any{"cpu": "unlimited"}, map[corev1.ResourceName]string{corev1.ResourceMemory: "1Gi"}},
			{"memory unlimited", map[string]any{"memory": "unlimited"}, map[corev1.ResourceName]string{corev1.ResourceCPU: "500m"}},
			{"both unlimited", map[string]any{"cpu": "unlimited", "memory": "unlimited"}, map[corev1.ResourceName]string{}},
		} {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				props := kind.props
				if tc.limits != nil {
					props = withProps(kind.props, map[string]any{"resources": map[string]any{"limits": tc.limits}})
				}
				ps, err := generateKindWithPolicy(t, kind, props, policy)
				if err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				assertQuantities(t, "limits", ps.Containers[0].Resources.Limits, tc.wantLimits)
			})
		}
	}
}

// TestUnlimitedLimit_RefusedUnderAMaximum: a policy maximum for a resource is
// a bound on its limit, and an absent limit is no bound. The refusal is of
// class resource-maximum and names the reason.
func TestUnlimitedLimit_RefusedUnderAMaximum(t *testing.T) {
	cases := []struct {
		name   string
		limits map[string]any
		policy *stubPolicy
		want   string
	}{
		{"cpu under a cpu maximum", map[string]any{"cpu": "unlimited"}, &stubPolicy{maxCPU: "2"}, `cpu limit "unlimited"`},
		{"memory under a memory maximum", map[string]any{"memory": "unlimited"}, &stubPolicy{maxMemory: "4Gi"}, `memory limit "unlimited"`},
	}
	pass := []struct {
		name   string
		limits map[string]any
		policy *stubPolicy
	}{
		{"cpu unlimited under only a memory maximum", map[string]any{"cpu": "unlimited"}, &stubPolicy{maxMemory: "4Gi"}},
		{"memory unlimited under only a cpu maximum", map[string]any{"memory": "unlimited"}, &stubPolicy{maxCPU: "2"}},
		{"both unlimited under no maximum", map[string]any{"cpu": "unlimited", "memory": "unlimited"}, &stubPolicy{}},
	}
	for _, kind := range workloadKinds {
		for _, holder := range resourceHolders() {
			if holder.name == "sidecar" && !hasSidecars[kind.name] {
				continue
			}
			for _, tc := range cases {
				t.Run(kind.name+"/"+holder.name+"/"+tc.name, func(t *testing.T) {
					props := withProps(kind.props, holder.props(map[string]any{"limits": tc.limits}))
					cfg := objects2Config(t, kind.handler, kind.name, props)
					err := cfg.(oam.Enforceable).ApplyPolicy(tc.policy)
					if err == nil {
						t.Fatal("ApplyPolicy accepted an unlimited limit under a maximum")
					}
					var refusal *oam.PolicyRefusal
					if !errors.As(err, &refusal) || refusal.Class != oam.RefusalResourceMaximum {
						t.Errorf("error = %v, want a %s refusal", err, oam.RefusalResourceMaximum)
					}
					if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "enforced maximum") {
						t.Errorf("error = %q, want it to name %q and the enforced maximum", err.Error(), tc.want)
					}
				})
			}
			for _, tc := range pass {
				t.Run(kind.name+"/"+holder.name+"/"+tc.name, func(t *testing.T) {
					props := withProps(kind.props, holder.props(map[string]any{"limits": tc.limits}))
					cfg := objects2Config(t, kind.handler, kind.name, props)
					if err := cfg.(oam.Enforceable).ApplyPolicy(tc.policy); err != nil {
						t.Fatalf("ApplyPolicy: %v", err)
					}
				})
			}
		}
	}
}

// TestUnlimitedLimit_RefusedWhereThereIsNothingToOptOutOf: the marker is
// refused by name wherever it has no meaning.
func TestUnlimitedLimit_RefusedWhereThereIsNothingToOptOutOf(t *testing.T) {
	deployment := workloadKinds[5]
	if deployment.name != "deployment" {
		t.Fatalf("workloadKinds[5] is %s, want deployment", deployment.name)
	}
	cases := []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{
			"under requests",
			map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "unlimited"}}},
			[]string{"resources.requests.memory", "unlimited"},
		},
		{
			"another resource name",
			map[string]any{"resources": map[string]any{"limits": map[string]any{"ephemeral-storage": "unlimited"}}},
			[]string{"resources.limits.ephemeral-storage", "unlimited", "cpu and memory"},
		},
		{
			"podResources limits",
			map[string]any{"podResources": map[string]any{"limits": map[string]any{"memory": "unlimited"}}},
			[]string{"podResources", "resources.limits.memory", "no intrinsic default to opt out of"},
		},
		{
			"podResources requests",
			map[string]any{"podResources": map[string]any{"requests": map[string]any{"cpu": "unlimited"}}},
			[]string{"podResources", "resources.requests.cpu"},
		},
		{
			"a string that is not the marker",
			map[string]any{"resources": map[string]any{"limits": map[string]any{"memory": "Unlimited"}}},
			[]string{"memory"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := deployment.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: withProps(deployment.props, tc.props)}, "default")
			if err == nil {
				t.Fatal("ToApplicationConfig accepted the marker")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), w)
				}
			}
		})
	}

	t.Run("postgresql", func(t *testing.T) {
		for _, section := range []string{"limits", "requests"} {
			props := map[string]any{"resources": map[string]any{section: map[string]any{"memory": "unlimited"}}}
			_, err := postgresqlViaRule{}.ToApplicationConfig(&oam.Component{Name: "db", Type: "postgresql", Properties: props}, "default")
			if err == nil {
				t.Fatalf("postgresql accepted the marker under %s", section)
			}
			if !strings.Contains(err.Error(), "resources."+section+".memory") || !strings.Contains(err.Error(), "unlimited") {
				t.Errorf("%s: error = %q, want it to name the key and the marker", section, err.Error())
			}
		}
	})
}

// TestUnlimitedLimit_NullStaysAbsence: the `null` rule is unchanged, and an
// unlimited marker beside it does not disturb it.
func TestUnlimitedLimit_NullStaysAbsence(t *testing.T) {
	deployment := workloadKinds[5]
	props := withProps(deployment.props, map[string]any{"resources": map[string]any{
		"limits": map[string]any{"cpu": nil, "memory": nil},
	}})
	res := podTemplateSpec(t, generateKind(t, deployment.handler, "deployment", props)).Containers[0].Resources
	assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"})
	assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"})
	if hasQuantity(res.Limits, corev1.ResourceCPU) {
		t.Errorf("null cpu limit produced a limit: %v", res.Limits)
	}
}
