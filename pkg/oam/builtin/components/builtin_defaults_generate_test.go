package components_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin go-kure/launcher#1580 for the values a workload kind fills
// when it generates its object: the cpu and memory requests, the memory limit
// that mirrors the memory request, and the token automount. Each is reported as
// a built-in default at the path the author would have written, by a config
// that answers from its own state when asked. What is expected is never taken
// from the code that decides the fill: it is derived from what the author
// wrote, what the policy reports, and what the generated object holds.

// builtinFillKinds are the kinds whose own handler builds the pod: the kinds
// that lower to one of them are not held here.
func builtinFillKinds() []struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
} {
	var out []struct {
		name    string
		handler oam.ComponentHandler
		props   map[string]any
	}
	for _, k := range workloadKinds {
		if k.name == "webservice" || k.name == "worker" {
			continue
		}
		out = append(out, k)
	}
	return out
}

// builtinReport returns cfg's built-in report as a sorted list of paths.
func builtinReport(t *testing.T, cfg stack.ApplicationConfig) []string {
	t.Helper()
	reporter, ok := cfg.(oam.BuiltinDefaultReporter)
	if !ok {
		t.Fatalf("%T does not implement oam.BuiltinDefaultReporter", cfg)
	}
	var paths []string
	for _, d := range reporter.BuiltinDefaults() {
		if d.Ref == "" {
			t.Errorf("the built-in default at %q names no fill", d.Path)
		}
		paths = append(paths, d.Path)
	}
	sort.Strings(paths)
	return paths
}

// policyReportedPaths returns the paths cfg reports a policy default at.
func policyReportedPaths(t *testing.T, cfg stack.ApplicationConfig) map[string]bool {
	t.Helper()
	reporter, ok := cfg.(oam.PolicyDefaultReporter)
	if !ok {
		t.Fatalf("%T does not implement oam.PolicyDefaultReporter", cfg)
	}
	out := map[string]bool{}
	for _, d := range reporter.PolicyDefaults() {
		out[d.Path] = true
	}
	return out
}

// resourcePaths are the three values a container can get from the kind itself.
var resourcePaths = []struct {
	path string
	list string
	name corev1.ResourceName
}{
	{"resources.requests.cpu", "requests", corev1.ResourceCPU},
	{"resources.requests.memory", "requests", corev1.ResourceMemory},
	{"resources.limits.memory", "limits", corev1.ResourceMemory},
}

// addedResourcePaths derives what generation added to one container: every
// value the rendered container holds that the author did not write and the
// policy does not report. authored is the container's authored resources.
func addedResourcePaths(prefix string, rendered corev1.ResourceRequirements, authored map[string]any, policy map[string]bool) []string {
	var out []string
	for _, rp := range resourcePaths {
		list := rendered.Requests
		if rp.list == "limits" {
			list = rendered.Limits
		}
		if _, ok := list[rp.name]; !ok {
			continue
		}
		if m, ok := authored[rp.list].(map[string]any); ok {
			if _, ok := m[string(rp.name)]; ok {
				continue
			}
		}
		if policy[prefix+rp.path] {
			continue
		}
		out = append(out, prefix+rp.path)
	}
	sort.Strings(out)
	return out
}

// withPrefix returns the paths of all that start with prefix. The main
// container's prefix is empty: its paths are those that start with "resources.".
func withPrefix(all []string, prefix string) []string {
	var out []string
	for _, p := range all {
		if strings.HasPrefix(p, prefix+"resources.") {
			out = append(out, p)
		}
	}
	return out
}

// TestBuiltinDefaults_ResourceFillsAreWhatGenerationAdds: on every container
// that takes the fills, the report names exactly the values generation adds.
func TestBuiltinDefaults_ResourceFillsAreWhatGenerationAdds(t *testing.T) {
	cases := []struct {
		name      string
		resources map[string]any
		policy    *stubPolicy
		want      int
	}{
		{name: "nothing authored", want: 3},
		{name: "everything authored", resources: map[string]any{
			"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]any{"memory": "128Mi"},
		}},
		{name: "requests authored", resources: map[string]any{
			"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
		}, want: 1},
		{name: "memory limit authored under the built-in request", resources: map[string]any{
			"limits": map[string]any{"memory": "64Mi"},
		}, want: 2},
		{name: "memory limit unlimited", resources: map[string]any{
			"limits": map[string]any{"memory": "unlimited"},
		}, want: 2},
		{name: "policy fills the requests", policy: &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi"}, want: 1},
		{name: "policy fills requests and limits", policy: &stubPolicy{
			defaultCPURequest: "200m", defaultMemoryRequest: "256Mi", defaultCPULimit: "1", defaultMemoryLimit: "512Mi",
		}},
		{name: "policy fills the memory limit", policy: &stubPolicy{defaultMemoryLimit: "512Mi"}, want: 2},
	}
	for _, kind := range builtinFillKinds() {
		for _, c := range defaultedContainers(kind.name) {
			for _, tc := range cases {
				t.Run(kind.name+"/"+c.label+"/"+tc.name, func(t *testing.T) {
					policy := tc.policy
					if policy == nil {
						policy = &stubPolicy{}
					}
					props := withProps(kind.props, c.props(tc.resources))
					cfg := objects2Config(t, kind.handler, kind.name, props)
					if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
						t.Fatalf("ApplyPolicy: %v", err)
					}
					got := withPrefix(builtinReport(t, cfg), c.prefix)

					objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
					if err != nil {
						t.Fatalf("Generate: %v", err)
					}
					rendered := c.find(t, podTemplateSpec(t, objects)).Resources
					want := addedResourcePaths(c.prefix, rendered, tc.resources, policyReportedPaths(t, cfg))

					if !reflect.DeepEqual(got, want) {
						t.Errorf("reported %v, generation added %v", got, want)
					}
					if len(want) != tc.want {
						t.Errorf("generation added %d values (%v), the case expects %d: the case no longer builds what it names", len(want), want, tc.want)
					}
					for _, p := range got {
						if policyReportedPaths(t, cfg)[p] {
							t.Errorf("%q is reported as built-in and as a policy default", p)
						}
					}
				})
			}
		}
	}
}

// TestBuiltinDefaults_TokenAutomount: the automount the kind switches off is
// reported, and only when the kind did it.
func TestBuiltinDefaults_TokenAutomount(t *testing.T) {
	const path = "automountServiceAccountToken"
	cases := []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{name: "nothing authored", want: true},
		{name: "service account authored", extra: map[string]any{"serviceAccountName": "shared"}},
		{name: "automount authored true", extra: map[string]any{path: true}},
		{name: "automount authored false", extra: map[string]any{path: false}},
	}
	for _, kind := range builtinFillKinds() {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				cfg := objects2Config(t, kind.handler, kind.name, withProps(kind.props, tc.extra))
				if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{}); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
				reported := false
				for _, p := range builtinReport(t, cfg) {
					reported = reported || p == path
				}
				objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				_, authored := tc.extra[path]
				added := podTemplateSpec(t, objects).AutomountServiceAccountToken != nil && !authored
				if reported != added {
					t.Errorf("reported = %t, generation added it = %t", reported, added)
				}
				if added != tc.want {
					t.Errorf("generation added it = %t, the case expects %t", added, tc.want)
				}
			})
		}
	}
}

// TestBuiltinDefaults_ReadingChangesNothing: the report is read, not made. It
// is the same on every read, before and after the policy runs again, and the
// objects of a config that was asked equal those of one that never was.
func TestBuiltinDefaults_ReadingChangesNothing(t *testing.T) {
	render := func(t *testing.T, cfg stack.ApplicationConfig) string {
		t.Helper()
		objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		out, err := yaml.Marshal(objects)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(out)
	}
	policy := &stubPolicy{defaultCPURequest: "200m"}
	for _, kind := range builtinFillKinds() {
		t.Run(kind.name, func(t *testing.T) {
			props := kind.props
			for _, list := range extraContainerLists(kind.name) {
				// One entry per list, each under its own name: a pod's containers share none.
				props = withProps(props, map[string]any{list.property: []any{extraEntry("extra-"+strings.ToLower(list.property), nil)}})
			}
			asked := objects2Config(t, kind.handler, kind.name, props)
			never := objects2Config(t, kind.handler, kind.name, props)

			beforePolicy := builtinReport(t, asked)
			if again := builtinReport(t, asked); !reflect.DeepEqual(again, beforePolicy) {
				t.Errorf("second read before the policy = %v, first = %v", again, beforePolicy)
			}
			for _, cfg := range []stack.ApplicationConfig{asked, never} {
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("ApplyPolicy: %v", err)
				}
			}
			first := builtinReport(t, asked)
			if again := builtinReport(t, asked); !reflect.DeepEqual(again, first) {
				t.Errorf("second read after the policy = %v, first = %v", again, first)
			}
			for _, cfg := range []stack.ApplicationConfig{asked, never} {
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatalf("second ApplyPolicy: %v", err)
				}
			}
			if again := builtinReport(t, asked); !reflect.DeepEqual(again, first) {
				t.Errorf("read after the policy ran twice = %v, after it ran once = %v", again, first)
			}
			// The cpu request is the policy's after it ran, and was the kind's before.
			for _, p := range first {
				if strings.HasSuffix(p, "resources.requests.cpu") {
					t.Errorf("%q is reported as built-in where the policy filled it", p)
				}
			}
			if len(beforePolicy) <= len(first) {
				t.Errorf("the report before the policy (%v) does not hold the cpu requests the policy then filled (%v)", beforePolicy, first)
			}
			if got, want := render(t, asked), render(t, never); got != want {
				t.Errorf("a config whose report was read renders differently from one never asked:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
