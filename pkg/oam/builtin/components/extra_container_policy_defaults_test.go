package components_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin go-kure/launcher#1581: the policy's default requests and
// limits reach every entry of `initContainers` and `sidecars`, with the rules
// the main container has. An authored value stays, a default request is held to
// an authored limit, `unlimited` opts out of a default limit, and the value the
// policy supplied is the one the policy maximum is checked against.

// extraContainerList is one list of non-main containers a kind publishes.
type extraContainerList struct {
	property string
	rendered func(ps corev1.PodSpec) []corev1.Container
}

func extraContainerLists(kind string) []extraContainerList {
	lists := []extraContainerList{{
		property: "initContainers",
		rendered: func(ps corev1.PodSpec) []corev1.Container { return ps.InitContainers },
	}}
	if hasSidecars[kind] {
		lists = append(lists, extraContainerList{
			property: "sidecars",
			rendered: func(ps corev1.PodSpec) []corev1.Container { return ps.Containers },
		})
	}
	return lists
}

func extraEntry(name string, resources map[string]any) map[string]any {
	e := map[string]any{"name": name, "image": "ghcr.io/org/" + name + ":v1"}
	if resources != nil {
		e["resources"] = resources
	}
	return e
}

// extraDefaults returns the policy defaults a config reports under one list.
func extraDefaults(t *testing.T, cfg stack.ApplicationConfig, property string) []oam.PolicyDefault {
	t.Helper()
	reporter, ok := cfg.(oam.PolicyDefaultReporter)
	if !ok {
		t.Fatalf("%T does not implement oam.PolicyDefaultReporter", cfg)
	}
	var out []oam.PolicyDefault
	for _, d := range reporter.PolicyDefaults() {
		if strings.HasPrefix(d.Path, property+"[") {
			out = append(out, d)
		}
	}
	return out
}

func TestExtraContainerPolicyDefaults_FillAnEntryWithoutResources(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi", defaultCPULimit: "500m", defaultMemoryLimit: "512Mi"}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				props := withProps(kind.props, map[string]any{list.property: []any{extraEntry("extra", nil)}})
				ps, err := generateKindWithPolicy(t, kind, props, policy)
				if err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				res := containerNamed(t, list.rendered(ps), "extra").Resources
				assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "256Mi"})
				assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{corev1.ResourceCPU: "500m", corev1.ResourceMemory: "512Mi"})
			})
		}
	}
}

func TestExtraContainerPolicyDefaults_AuthoredValuesStay(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi", defaultCPULimit: "500m", defaultMemoryLimit: "512Mi"}
	authored := map[string]any{
		"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
		"limits":   map[string]any{"cpu": "100m", "memory": "128Mi"},
	}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				props := withProps(kind.props, map[string]any{list.property: []any{extraEntry("extra", authored)}})
				cfg := objects2Config(t, kind.handler, kind.name, props)
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				res := containerNamed(t, list.rendered(podTemplateSpec(t, objects)), "extra").Resources
				assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"})
				assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"})
				if got := extraDefaults(t, cfg, list.property); len(got) != 0 {
					t.Errorf("authored values reported as policy defaults: %+v", got)
				}
			})
		}
	}
}

// Without a policy default the built-in fallback still fills the requests, and
// the memory limit still mirrors the memory request.
func TestExtraContainerPolicyDefaults_NoPolicyDefaultKeepsTheBuiltInFallback(t *testing.T) {
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				props := withProps(kind.props, map[string]any{list.property: []any{extraEntry("extra", nil)}})
				ps, err := generateKindWithPolicy(t, kind, props, &stubPolicy{})
				if err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				res := containerNamed(t, list.rendered(ps), "extra").Resources
				assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"})
				assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"})
			})
		}
	}
}

// The value the policy supplied is the one its maximum is checked against: a
// default above the maximum is refused on the entry, as it is on the main
// container. The main container authors its request so that the entry is the
// only container over the maximum.
func TestExtraContainerPolicyDefaults_DefaultAboveTheMaximumIsRefused(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "2", maxCPU: "1"}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				props := withProps(kind.props, map[string]any{
					"resources":   map[string]any{"requests": map[string]any{"cpu": "100m"}},
					list.property: []any{extraEntry("extra", nil)},
				})
				cfg := objects2Config(t, kind.handler, kind.name, props)
				err := cfg.(oam.Enforceable).ApplyPolicy(policy)
				if err == nil {
					t.Fatal("ApplyPolicy accepted a policy default request above the policy maximum")
				}
				var refusal *oam.PolicyRefusal
				if !errors.As(err, &refusal) || refusal.Class != oam.RefusalResourceMaximum {
					t.Errorf("error = %v, want a %s refusal", err, oam.RefusalResourceMaximum)
				}
				assertErrorContains(t, err, list.property+`[0] "extra"`, `cpu request "2"`, "enforced maximum")
			})
		}
	}
}

// A default request above the limit the author set is fixed at that limit, and
// the report says so.
func TestExtraContainerPolicyDefaults_RequestHeldToTheAuthoredLimit(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m"}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				entry := extraEntry("extra", map[string]any{"limits": map[string]any{"cpu": "150m"}})
				props := withProps(kind.props, map[string]any{list.property: []any{entry}})
				cfg := objects2Config(t, kind.handler, kind.name, props)
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				res := containerNamed(t, list.rendered(podTemplateSpec(t, objects)), "extra").Resources
				assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "150m", corev1.ResourceMemory: "128Mi"})
				want := []oam.PolicyDefault{{Path: list.property + "[0].resources.requests.cpu", Accessor: "DefaultCPURequest", Detail: oam.DetailCappedToLimit}}
				if got := extraDefaults(t, cfg, list.property); !slices.Equal(got, want) {
					t.Errorf("reported defaults = %+v, want %+v", got, want)
				}
			})
		}
	}
}

// `unlimited` is the author's opt-out of a limit: no default limit lands on the
// entry, and none is reported.
func TestExtraContainerPolicyDefaults_UnlimitedStopsTheDefaultLimit(t *testing.T) {
	policy := &stubPolicy{defaultCPULimit: "500m", defaultMemoryLimit: "1Gi"}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				entry := extraEntry("extra", map[string]any{"limits": map[string]any{"cpu": "unlimited", "memory": "unlimited"}})
				props := withProps(kind.props, map[string]any{list.property: []any{entry}})
				cfg := objects2Config(t, kind.handler, kind.name, props)
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				res := containerNamed(t, list.rendered(podTemplateSpec(t, objects)), "extra").Resources
				assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{})
				if got := extraDefaults(t, cfg, list.property); len(got) != 0 {
					t.Errorf("an unlimited limit reported as a policy default: %+v", got)
				}
			})
		}
	}
}

// Every entry takes the defaults at its own position, and applying the policy a
// second time changes neither the generated containers nor the report.
func TestExtraContainerPolicyDefaults_EachEntryByIndexAndTwiceIsTheSame(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi"}
	for _, kind := range workloadKinds {
		for _, list := range extraContainerLists(kind.name) {
			t.Run(kind.name+"/"+list.property, func(t *testing.T) {
				props := withProps(kind.props, map[string]any{list.property: []any{
					extraEntry("first", nil),
					extraEntry("second", map[string]any{"requests": map[string]any{"cpu": "50m"}}),
				}})
				cfg := objects2Config(t, kind.handler, kind.name, props)
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				once := extraDefaults(t, cfg, list.property)
				want := []oam.PolicyDefault{
					{Path: list.property + "[0].resources.requests.cpu", Accessor: "DefaultCPURequest"},
					{Path: list.property + "[0].resources.requests.memory", Accessor: "DefaultMemoryRequest"},
					{Path: list.property + "[1].resources.requests.memory", Accessor: "DefaultMemoryRequest"},
				}
				if !slices.Equal(once, want) {
					t.Errorf("reported defaults = %+v, want %+v", once, want)
				}
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("second ApplyPolicy: %v", err)
				}
				if twice := extraDefaults(t, cfg, list.property); !slices.Equal(twice, once) {
					t.Errorf("reported defaults after a second ApplyPolicy = %+v, want %+v", twice, once)
				}
				objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				rendered := list.rendered(podTemplateSpec(t, objects))
				first := containerNamed(t, rendered, "first").Resources
				assertQuantities(t, "first requests", first.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "200m", corev1.ResourceMemory: "256Mi"})
				assertQuantities(t, "first limits", first.Limits, map[corev1.ResourceName]string{corev1.ResourceMemory: "256Mi"})
				second := containerNamed(t, rendered, "second").Resources
				assertQuantities(t, "second requests", second.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "256Mi"})
				assertQuantities(t, "second limits", second.Limits, map[corev1.ResourceName]string{corev1.ResourceMemory: "256Mi"})
			})
		}
	}
}
