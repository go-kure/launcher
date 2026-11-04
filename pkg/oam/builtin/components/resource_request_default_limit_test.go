package components_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// resourceHolders names every container of a workload that gets the built-in
// request defaults, through the one props key that authors it. The main
// container takes `resources` at the top level; an init container and a
// sidecar take it in their own entry.
type resourceHolder struct {
	name  string
	props func(res map[string]any) map[string]any
	pick  func(corev1.PodSpec) corev1.Container
}

func resourceHolders() []resourceHolder {
	entry := func(name string, res map[string]any) []any {
		return []any{map[string]any{"name": name, "image": "ghcr.io/org/side:v1", "resources": res}}
	}
	return []resourceHolder{
		{
			name:  "main",
			props: func(res map[string]any) map[string]any { return map[string]any{"resources": res} },
			pick:  func(ps corev1.PodSpec) corev1.Container { return ps.Containers[0] },
		},
		{
			name:  "init",
			props: func(res map[string]any) map[string]any { return map[string]any{"initContainers": entry("setup", res)} },
			pick:  func(ps corev1.PodSpec) corev1.Container { return ps.InitContainers[0] },
		},
		{
			name:  "sidecar",
			props: func(res map[string]any) map[string]any { return map[string]any{"sidecars": entry("side", res)} },
			pick: func(ps corev1.PodSpec) corev1.Container {
				for _, c := range ps.Containers {
					if c.Name == "side" {
						return c
					}
				}
				return corev1.Container{}
			},
		},
	}
}

// hasSidecars lists the kinds whose schema publishes `sidecars`.
var hasSidecars = map[string]bool{
	"webservice": true, "worker": true, "deployment": true, "statefulset": true, "daemonset": true,
}

// TestBuildResourceRequirements_RequestNeverAboveAuthoredLimit pins
// go-kure/launcher#1217: an authored limit below the built-in request default,
// with no request authored for that resource, must not render a request above
// the limit, which the apiserver refuses at admission. It holds on the main
// container, init containers and sidecars of every workload kind.
func TestBuildResourceRequirements_RequestNeverAboveAuthoredLimit(t *testing.T) {
	cases := []struct {
		name     string
		limits   map[string]any
		resource corev1.ResourceName
	}{
		{"memory limit below the 128Mi default", map[string]any{"memory": "64Mi"}, corev1.ResourceMemory},
		{"cpu limit below the 100m default", map[string]any{"cpu": "50m"}, corev1.ResourceCPU},
		{"bare number cpu limit below the default", map[string]any{"cpu": 0.05}, corev1.ResourceCPU},
	}
	for _, kind := range workloadKinds {
		for _, holder := range resourceHolders() {
			if holder.name == "sidecar" && !hasSidecars[kind.name] {
				continue
			}
			for _, tc := range cases {
				t.Run(kind.name+"/"+holder.name+"/"+tc.name, func(t *testing.T) {
					props := withProps(kind.props, holder.props(map[string]any{"limits": tc.limits}))
					ps := podTemplateSpec(t, generateKind(t, kind.handler, kind.name, props))
					res := holder.pick(ps).Resources
					limit, ok := res.Limits[tc.resource]
					if !ok {
						t.Fatalf("limits[%s] is absent, want the authored limit: %v", tc.resource, res.Limits)
					}
					if req, ok := res.Requests[tc.resource]; ok && req.Cmp(limit) > 0 {
						t.Errorf("requests[%s] = %s is above the authored limit %s", tc.resource, req.String(), limit.String())
					}
				})
			}
		}
	}
}

// TestBuildResourceRequirements_DefaultsKeptWhereTheyFit pins that the fix for
// go-kure/launcher#1217 moves nothing else: a limit at or above the default,
// or a request authored beside the limit, still gets the same output.
func TestBuildResourceRequirements_DefaultsKeptWhereTheyFit(t *testing.T) {
	handler := &components.DeploymentHandler{}
	cases := []struct {
		name         string
		resources    map[string]any
		wantRequests map[corev1.ResourceName]string
		wantLimits   map[corev1.ResourceName]string
	}{
		{
			name:         "nothing authored",
			resources:    nil,
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"},
		},
		{
			name:         "limit above the default",
			resources:    map[string]any{"limits": map[string]any{"memory": "1Gi", "cpu": "1"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "1Gi"},
		},
		{
			name:         "limit equal to the default",
			resources:    map[string]any{"limits": map[string]any{"memory": "128Mi", "cpu": "100m"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"},
		},
		{
			name:         "limit below the default",
			resources:    map[string]any{"limits": map[string]any{"memory": "64Mi", "cpu": "50m"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"},
		},
		{
			name:         "limit below the default for one resource only",
			resources:    map[string]any{"limits": map[string]any{"memory": "64Mi"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "64Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "64Mi"},
		},
		{
			name: "memory request authored, no limit",
			resources: map[string]any{
				"requests": map[string]any{"memory": "32Mi"},
			},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "32Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "32Mi"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"image": "ghcr.io/org/app:v1"}
			if tc.resources != nil {
				props["resources"] = tc.resources
			}
			res := podTemplateSpec(t, generateKind(t, handler, "deployment", props)).Containers[0].Resources
			assertQuantities(t, "requests", res.Requests, tc.wantRequests)
			assertQuantities(t, "limits", res.Limits, tc.wantLimits)
		})
	}
}

// generateKindWithPolicy is generateKind with the environment policy applied
// between ToApplicationConfig and Generate, as the transform does.
func generateKindWithPolicy(t *testing.T, kind struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
}, props map[string]any, p oam.Policy) (corev1.PodSpec, error) {
	t.Helper()
	cfg := objects2Config(t, kind.handler, kind.name, props)
	if err := cfg.(oam.Enforceable).ApplyPolicy(p); err != nil {
		return corev1.PodSpec{}, err
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return podTemplateSpec(t, objects), nil
}

// TestPolicyRequestDefault_NeverAboveAuthoredLimit pins the policy half of
// go-kure/launcher#1217: a policy default request is held to the limit the
// author set for the same resource, as the built-in default is. An authored
// request is never touched, and a default that fits is kept.
func TestPolicyRequestDefault_NeverAboveAuthoredLimit(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi"}
	cases := []struct {
		name         string
		resources    map[string]any
		wantRequests map[corev1.ResourceName]string
		wantLimits   map[corev1.ResourceName]string
	}{
		{
			name:         "limits below the policy defaults",
			resources:    map[string]any{"limits": map[string]any{"cpu": "50m", "memory": "64Mi"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"},
		},
		{
			name:         "memory limit below the policy default only",
			resources:    map[string]any{"limits": map[string]any{"memory": "64Mi"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "64Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "64Mi"},
		},
		{
			name:         "limits above the policy defaults",
			resources:    map[string]any{"limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "256Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "1Gi"},
		},
		{
			name: "authored request below the limit is kept",
			resources: map[string]any{
				"requests": map[string]any{"memory": "32Mi"},
				"limits":   map[string]any{"memory": "64Mi"},
			},
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "32Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "64Mi"},
		},
		{
			name:         "no limit authored",
			resources:    nil,
			wantRequests: map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "256Mi"},
			wantLimits:   map[corev1.ResourceName]string{corev1.ResourceMemory: "256Mi"},
		},
	}
	for _, kind := range workloadKinds {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				props := kind.props
				if tc.resources != nil {
					props = withProps(kind.props, map[string]any{"resources": tc.resources})
				}
				ps, err := generateKindWithPolicy(t, kind, props, policy)
				if err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				res := ps.Containers[0].Resources
				assertQuantities(t, "requests", res.Requests, tc.wantRequests)
				assertQuantities(t, "limits", res.Limits, tc.wantLimits)
			})
		}
	}
}

func assertQuantities(t *testing.T, label string, got corev1.ResourceList, want map[corev1.ResourceName]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", label, got, want)
		return
	}
	for name, w := range want {
		q, ok := got[name]
		if !ok || q.String() != w {
			t.Errorf("%s[%s] = %q (present %v), want %q", label, name, q.String(), ok, w)
		}
	}
}
