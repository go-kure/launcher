package components_test

import (
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin go-kure/launcher#1582: a policy default limit that lands under
// the container's request is refused when the policy is applied. The apiserver
// refuses a container whose request is above its limit, so rendering it would
// only move the failure from build to apply. The check covers every container
// that takes the policy's defaults: the main container, and each init container
// and sidecar (go-kure/launcher#1581).

// requestAboveDefaultLimit is one resource with a request above the limit the
// policy defaults, and a request at that limit.
type requestAboveDefaultLimit struct {
	name    string
	request string
	atLimit string
	policy  *stubPolicy
	limit   string
}

func requestAboveDefaultLimitCases() []requestAboveDefaultLimit {
	return []requestAboveDefaultLimit{
		{name: "cpu", request: "1", atLimit: "500m", policy: &stubPolicy{defaultCPULimit: "500m"}, limit: "500m"},
		{name: "memory", request: "1Gi", atLimit: "512Mi", policy: &stubPolicy{defaultMemoryLimit: "512Mi"}, limit: "512Mi"},
	}
}

// defaultedContainer is one container of a kind that takes the policy's
// defaults: where its resources are authored, and how it is found once rendered.
type defaultedContainer struct {
	label  string
	prefix string
	props  func(resources map[string]any) map[string]any
	find   func(t *testing.T, ps corev1.PodSpec) corev1.Container
}

func defaultedContainers(kind string) []defaultedContainer {
	out := []defaultedContainer{{
		label: "main",
		props: func(resources map[string]any) map[string]any {
			return map[string]any{"resources": resources}
		},
		find: func(t *testing.T, ps corev1.PodSpec) corev1.Container {
			t.Helper()
			return containerNamed(t, ps.Containers, "app")
		},
	}}
	for _, list := range extraContainerLists(kind) {
		out = append(out, defaultedContainer{
			label:  list.property,
			prefix: list.property + "[0].",
			props: func(resources map[string]any) map[string]any {
				return map[string]any{list.property: []any{extraEntry("extra", resources)}}
			},
			find: func(t *testing.T, ps corev1.PodSpec) corev1.Container {
				t.Helper()
				return containerNamed(t, list.rendered(ps), "extra")
			},
		})
	}
	return out
}

// wantDefaultLimitRefusal reads the policy refusal in err's cause chain. Only a
// request the author wrote is one, with the class
// oam.RefusalResourceDefaultLimit (go-kure/launcher#1580); want false asserts
// that the chain holds none.
func wantDefaultLimitRefusal(t *testing.T, err error, want bool) {
	t.Helper()
	var r *oam.PolicyRefusal
	switch found := errors.As(err, &r); {
	case want && !found:
		t.Errorf("the cause chain holds no *oam.PolicyRefusal: %v", err)
	case want && r.Class != oam.RefusalResourceDefaultLimit:
		t.Errorf("refusal class = %q, want %q: %v", r.Class, oam.RefusalResourceDefaultLimit, err)
	case !want && found:
		t.Errorf("an error no authored value caused carries a refusal of class %q: %v", r.Class, err)
	}
}

// The author wrote the request and left the limit to the policy: the message
// names the request, the policy's default limit, and what the author can write.
// It is a policy refusal with a class, on every container that takes the
// defaults.
func TestPolicyDefaultLimit_AuthoredRequestAboveItIsRefused(t *testing.T) {
	for _, kind := range workloadKinds {
		for _, c := range defaultedContainers(kind.name) {
			for _, rc := range requestAboveDefaultLimitCases() {
				t.Run(kind.name+"/"+c.label+"/"+rc.name, func(t *testing.T) {
					props := withProps(kind.props, c.props(map[string]any{"requests": map[string]any{rc.name: rc.request}}))
					_, err := generateKindWithPolicy(t, kind, props, rc.policy)
					if err == nil {
						t.Fatalf("ApplyPolicy accepted a %s request %s above the policy default limit %s", rc.name, rc.request, rc.limit)
					}
					assertErrorContains(t, err,
						c.prefix+"resources.requests."+rc.name+` "`+rc.request+`"`,
						`the policy's default limit "`+rc.limit+`"`,
						"write "+c.prefix+"resources.limits."+rc.name,
						"or lower the request",
					)
					wantDefaultLimitRefusal(t, err, true)
				})
			}
		}
	}
}

// The author wrote neither: the policy's default request is above its own
// default limit, and the message names the policy as the source.
func TestPolicyDefaultLimit_DefaultRequestAboveItNamesThePolicy(t *testing.T) {
	policies := map[string]*stubPolicy{
		"cpu":    {defaultCPURequest: "1", defaultCPULimit: "500m"},
		"memory": {defaultMemoryRequest: "1Gi", defaultMemoryLimit: "512Mi"},
	}
	values := map[string][2]string{"cpu": {"1", "500m"}, "memory": {"1Gi", "512Mi"}}
	// A container that authors a request and a limit takes neither default.
	authored := map[string]any{
		"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
		"limits":   map[string]any{"cpu": "100m", "memory": "128Mi"},
	}
	for _, kind := range workloadKinds {
		for _, c := range defaultedContainers(kind.name) {
			for name, policy := range policies {
				t.Run(kind.name+"/"+c.label+"/"+name, func(t *testing.T) {
					// Only the container under test takes the defaults: the main
					// container authors its values when an entry is under test.
					props := withProps(kind.props, map[string]any{"resources": authored})
					props = withProps(props, c.props(nil))
					if c.prefix == "" {
						delete(props, "resources")
					}
					_, err := generateKindWithPolicy(t, kind, props, policy)
					if err == nil {
						t.Fatalf("ApplyPolicy accepted a policy default %s request above the policy default limit", name)
					}
					assertErrorContains(t, err,
						c.prefix+"resources: the policy's default "+name+` request "`+values[name][0]+`"`,
						`its default limit "`+values[name][1]+`"`,
						"the policy must set",
					)
					wantDefaultLimitRefusal(t, err, false)
				})
			}
		}
	}
}

// A second ApplyPolicy on a config the first one refused gives the same answer:
// the request the policy filled on the first run is still the policy's, not the
// author's.
func TestPolicyDefaultLimit_ARefusedConfigIsRefusedTheSameWayAgain(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "1", defaultCPULimit: "500m"}
	for _, kind := range workloadKinds {
		t.Run(kind.name, func(t *testing.T) {
			cfg := objects2Config(t, kind.handler, kind.name, kind.props)
			first := cfg.(oam.Enforceable).ApplyPolicy(policy)
			second := cfg.(oam.Enforceable).ApplyPolicy(policy)
			assertErrorContains(t, first, `resources: the policy's default cpu request "1"`)
			assertErrorContains(t, second, `resources: the policy's default cpu request "1"`)
			if first.Error() != second.Error() {
				t.Errorf("second refusal = %q, want the first one, %q", second, first)
			}
		})
	}
}

// A limit the policy did not fill is not called the policy's default. A config
// built through the Go API can hold a request above its limit without the
// parser having seen it; it is refused with the parser's own words.
func TestPolicyDefaultLimit_ALimitThePolicyDidNotFillIsNotBlamedOnIt(t *testing.T) {
	cfg := objects2Config(t, &components.DeploymentHandler{}, "deployment", map[string]any{
		"image":     "ghcr.io/org/app:v1",
		"resources": map[string]any{"requests": map[string]any{"cpu": "100m"}, "limits": map[string]any{"cpu": "500m"}},
	})
	dc, ok := cfg.(*components.DeploymentConfig)
	if !ok {
		t.Fatalf("config is %T, want *components.DeploymentConfig", cfg)
	}
	dc.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("1")
	err := dc.ApplyPolicy(&stubPolicy{defaultCPULimit: "250m"})
	assertErrorContains(t, err, "resources: cpu: request 1 must not exceed limit 500m")
	wantDefaultLimitRefusal(t, err, false)
	if strings.Contains(err.Error(), "policy") {
		t.Errorf("error %q names the policy for a limit it did not fill", err)
	}
}

// The built-in request (100m cpu, 128Mi memory) is filled when the workload is
// generated, after the policy. It is held to the limit the policy filled, as it
// is to an authored one (go-kure/launcher#1217): a default limit under the
// built-in request renders a request at that limit, never above it.
func TestPolicyDefaultLimit_BuiltinRequestIsHeldToIt(t *testing.T) {
	cases := []struct {
		name   string
		limit  string
		policy *stubPolicy
	}{
		{"cpu", "50m", &stubPolicy{defaultCPULimit: "50m"}},
		{"memory", "64Mi", &stubPolicy{defaultMemoryLimit: "64Mi"}},
	}
	for _, kind := range workloadKinds {
		for _, c := range defaultedContainers(kind.name) {
			for _, tc := range cases {
				t.Run(kind.name+"/"+c.label+"/"+tc.name, func(t *testing.T) {
					props := withProps(kind.props, c.props(nil))
					if c.prefix == "" {
						delete(props, "resources")
					}
					ps, err := generateKindWithPolicy(t, kind, props, tc.policy)
					if err != nil {
						t.Fatalf("ApplyPolicy: %v", err)
					}
					res := c.find(t, ps).Resources
					name := corev1.ResourceName(tc.name)
					limit, ok := res.Limits[name]
					if !ok || limit.String() != tc.limit {
						t.Fatalf("%s limit = %s (set: %t), want the policy default %s", tc.name, limit.String(), ok, tc.limit)
					}
					request, ok := res.Requests[name]
					if !ok || request.String() != tc.limit {
						t.Errorf("%s request = %s (set: %t), want it held to the limit %s", tc.name, request.String(), ok, tc.limit)
					}
				})
			}
		}
	}
}

// What renders today still renders: a request at or under the default limit,
// an authored limit with its request, and `unlimited` on the limit.
func TestPolicyDefaultLimit_ControlsRenderAsToday(t *testing.T) {
	for _, kind := range workloadKinds {
		for _, c := range defaultedContainers(kind.name) {
			for _, rc := range requestAboveDefaultLimitCases() {
				name := corev1.ResourceName(rc.name)
				cases := []struct {
					label     string
					resources map[string]any
					request   string
					limit     string
				}{
					{"request at the default limit", map[string]any{"requests": map[string]any{rc.name: rc.atLimit}}, rc.atLimit, rc.limit},
					{"authored limit with its request", map[string]any{
						"requests": map[string]any{rc.name: rc.request},
						"limits":   map[string]any{rc.name: rc.request},
					}, rc.request, rc.request},
					{"unlimited", map[string]any{
						"requests": map[string]any{rc.name: rc.request},
						"limits":   map[string]any{rc.name: "unlimited"},
					}, rc.request, ""},
				}
				for _, tc := range cases {
					t.Run(kind.name+"/"+c.label+"/"+rc.name+"/"+tc.label, func(t *testing.T) {
						props := withProps(kind.props, c.props(tc.resources))
						ps, err := generateKindWithPolicy(t, kind, props, rc.policy)
						if err != nil {
							t.Fatalf("ApplyPolicy: %v", err)
						}
						res := c.find(t, ps).Resources
						if got := res.Requests[name]; got.String() != tc.request {
							t.Errorf("%s request = %s, want %s", rc.name, got.String(), tc.request)
						}
						got, ok := res.Limits[name]
						if tc.limit == "" {
							if ok {
								t.Errorf("%s limit = %s, want none", rc.name, got.String())
							}
							return
						}
						if got.String() != tc.limit {
							t.Errorf("%s limit = %s, want %s", rc.name, got.String(), tc.limit)
						}
					})
				}
			}
		}
	}
}
