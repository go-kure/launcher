package components_test

import (
	"fmt"
	"testing"
)

// These tests cover go-kure/launcher#1228: a lifecycle hook whose
// sleep.seconds exceeds the pod's terminationGracePeriodSeconds (30 when the
// pod sets none) is refused at Generate. Kubernetes refuses it at admission
// (validateSleepAction compares `sleep.Seconds > gracePeriod`), so a sleep equal
// to the grace period is admitted and stays accepted here.

func sleepHook(hook string, seconds int) map[string]any {
	return map[string]any{hook: map[string]any{"sleep": map[string]any{"seconds": seconds}}}
}

// sleepPaths are the three places a hook is authored: the main container, a
// sidecars entry and a native sidecar. Each returns the properties that carry
// the hook and the container list and name the refusal must carry.
type sleepPath struct {
	name      string
	list      string
	index     int
	container string
	// sidecarOnly marks a path only the kinds with a `sidecars` property take.
	sidecarOnly bool
	props       func(base map[string]any, lifecycle map[string]any) map[string]any
}

var sleepPaths = []sleepPath{
	{
		name: "main", list: "containers", container: "app",
		props: func(base, lifecycle map[string]any) map[string]any {
			return withProps(base, map[string]any{"lifecycle": lifecycle})
		},
	},
	{
		name: "sidecar", list: "containers", index: 1, container: "side", sidecarOnly: true,
		props: func(base, lifecycle map[string]any) map[string]any {
			return withProps(base, map[string]any{"sidecars": []any{
				map[string]any{"name": "side", "image": "ghcr.io/org/side:v1", "lifecycle": lifecycle},
			}})
		},
	},
	{
		name: "native", list: "initContainers", container: "nat",
		props: func(base, lifecycle map[string]any) map[string]any {
			return withProps(base, map[string]any{"initContainers": []any{
				map[string]any{"name": "nat", "image": "ghcr.io/org/nat:v1", "restartPolicy": "Always", "lifecycle": lifecycle},
			}})
		},
	},
}

func TestLifecycleSleep_RefusedAboveGracePeriod(t *testing.T) {
	cases := []struct {
		name  string
		grace any // nil leaves terminationGracePeriodSeconds unauthored
		sleep int
		limit int
	}{
		{"default grace period is 30", nil, 31, 30},
		{"authored grace period", 10, 11, 10},
		{"a large sleep", 120, 600, 120},
		{"grace period zero admits no positive sleep", 0, 1, 0},
	}
	for _, k := range workloadKinds {
		for _, p := range sleepPaths {
			if p.sidecarOnly && !sidecarKinds[k.name] {
				continue
			}
			for _, hook := range []string{"postStart", "preStop"} {
				for _, tc := range cases {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", k.name, p.name, hook, tc.name), func(t *testing.T) {
						base := k.props
						if tc.grace != nil {
							base = withProps(base, map[string]any{"terminationGracePeriodSeconds": tc.grace})
						}
						err := generateErr(t, k.handler, k.name, p.props(base, sleepHook(hook, tc.sleep)))
						want := fmt.Sprintf(`%s[%d] %q: lifecycle.%s.sleep.seconds: %d must not exceed terminationGracePeriodSeconds (%d)`,
							p.list, p.index, p.container, hook, tc.sleep, tc.limit)
						assertErrorContains(t, err, want)
					})
				}
			}
		}
	}
}

func TestLifecycleSleep_AcceptedWithinGracePeriod(t *testing.T) {
	cases := []struct {
		name  string
		grace any
		sleep int
	}{
		{"default grace period, shorter", nil, 29},
		{"default grace period, equal", nil, 30},
		{"authored grace period, shorter", 10, 9},
		{"authored grace period, equal", 10, 10},
		{"an authored grace period replaces the default", 120, 60},
		{"zero sleep with grace period zero", 0, 0},
		{"zero sleep", nil, 0},
	}
	for _, k := range workloadKinds {
		for _, p := range sleepPaths {
			if p.sidecarOnly && !sidecarKinds[k.name] {
				continue
			}
			for _, hook := range []string{"postStart", "preStop"} {
				for _, tc := range cases {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", k.name, p.name, hook, tc.name), func(t *testing.T) {
						base := k.props
						if tc.grace != nil {
							base = withProps(base, map[string]any{"terminationGracePeriodSeconds": tc.grace})
						}
						if err := generateErr(t, k.handler, k.name, p.props(base, sleepHook(hook, tc.sleep))); err != nil {
							t.Fatalf("a sleep of %d must build, got: %v", tc.sleep, err)
						}
					})
				}
			}
		}
	}
}

// TestLifecycleSleep_OtherHandlersUnaffected: only a sleep handler is compared
// to the grace period; an exec hook beside a short grace period still builds.
func TestLifecycleSleep_OtherHandlersUnaffected(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"terminationGracePeriodSeconds": 1,
				"lifecycle": map[string]any{
					"preStop": map[string]any{"exec": map[string]any{"command": []any{"sleep", "60"}}},
				},
			})
			if err := generateErr(t, k.handler, k.name, props); err != nil {
				t.Fatalf("an exec hook must build, got: %v", err)
			}
		})
	}
}
