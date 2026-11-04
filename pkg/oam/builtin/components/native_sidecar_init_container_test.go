package components_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover go-kure/launcher#1180: an `initContainers` entry with
// `restartPolicy: Always` is a native sidecar (a restartable init container).
// It starts in list order, keeps running beside the main container, and may
// carry probes and lifecycle hooks, which Kubernetes forbids on every other
// init container. The same entry on a `job` or `cronjob` is the one way to
// run a helper beside the job's container without keeping its pod from
// completing.

// nativeSidecarEntry is one restartable init container with every field only
// it may carry.
func nativeSidecarEntry() map[string]any {
	return map[string]any{
		"name":          "proxy",
		"image":         "ghcr.io/org/proxy:v1",
		"restartPolicy": "Always",
		"probes": map[string]any{
			"readiness": map[string]any{"httpGet": map[string]any{"port": 9901, "path": "/ready"}},
			"liveness":  map[string]any{"tcpSocket": map[string]any{"port": 9901}},
			"startup":   map[string]any{"exec": map[string]any{"command": []any{"true"}}, "failureThreshold": 30},
		},
		"lifecycle": map[string]any{
			"preStop": map[string]any{"sleep": map[string]any{"seconds": 5}},
		},
	}
}

func regularInitEntry(name string) map[string]any {
	return map[string]any{"name": name, "image": "ghcr.io/org/" + name + ":v1"}
}

// TestNativeSidecar_RendersInInitContainersInListOrder: on every kind the
// restartable entry renders in spec.initContainers where it was authored, with
// its policy, probes and hook; the regular init containers around it carry
// none of them; and an authored `sidecars` entry stays after the main container.
func TestNativeSidecar_RendersInInitContainersInListOrder(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			extra := map[string]any{
				"initContainers": []any{regularInitEntry("setup"), nativeSidecarEntry(), regularInitEntry("migrate")},
			}
			if sidecarKinds[k.name] {
				extra["sidecars"] = []any{regularInitEntry("side")}
			}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, extra)))

			var names []string
			for _, c := range ps.InitContainers {
				names = append(names, c.Name)
			}
			if got := strings.Join(names, ","); got != "setup,proxy,migrate" {
				t.Fatalf("initContainers = %s, want setup,proxy,migrate", got)
			}
			proxy := ps.InitContainers[1]
			if proxy.RestartPolicy == nil || *proxy.RestartPolicy != corev1.ContainerRestartPolicyAlways {
				t.Errorf("proxy RestartPolicy = %v, want Always", proxy.RestartPolicy)
			}
			if proxy.ReadinessProbe == nil || proxy.ReadinessProbe.HTTPGet == nil ||
				proxy.ReadinessProbe.HTTPGet.Port != intstr.FromInt32(9901) || proxy.ReadinessProbe.HTTPGet.Path != "/ready" {
				t.Errorf("proxy ReadinessProbe = %+v, want httpGet 9901 /ready", proxy.ReadinessProbe)
			}
			if proxy.LivenessProbe == nil || proxy.LivenessProbe.TCPSocket == nil || proxy.LivenessProbe.TCPSocket.Port != intstr.FromInt32(9901) {
				t.Errorf("proxy LivenessProbe = %+v, want tcpSocket 9901", proxy.LivenessProbe)
			}
			if proxy.StartupProbe == nil || proxy.StartupProbe.Exec == nil || proxy.StartupProbe.FailureThreshold != 30 {
				t.Errorf("proxy StartupProbe = %+v, want exec with failureThreshold 30", proxy.StartupProbe)
			}
			if proxy.Lifecycle == nil || proxy.Lifecycle.PreStop == nil || proxy.Lifecycle.PreStop.Sleep == nil || proxy.Lifecycle.PreStop.Sleep.Seconds != 5 {
				t.Errorf("proxy Lifecycle = %+v, want a preStop sleep of 5 seconds", proxy.Lifecycle)
			}
			for _, i := range []int{0, 2} {
				c := ps.InitContainers[i]
				if c.RestartPolicy != nil || c.ReadinessProbe != nil || c.LivenessProbe != nil || c.StartupProbe != nil || c.Lifecycle != nil {
					t.Errorf("regular init container %q carries restart policy, probe or hook: %+v", c.Name, c)
				}
			}
			for _, c := range ps.Containers {
				if c.Name == "proxy" {
					t.Errorf("native sidecar rendered in spec.containers")
				}
			}
			if sidecarKinds[k.name] {
				if len(ps.Containers) < 2 || ps.Containers[len(ps.Containers)-1].Name != "side" {
					t.Errorf("containers = %v, want the sidecars entry last, after the main container", containerNames(ps.Containers))
				}
			}
			if k.name == "job" || k.name == "cronjob" {
				if ps.RestartPolicy != corev1.RestartPolicyOnFailure {
					t.Errorf("pod RestartPolicy = %q, want OnFailure unchanged by a native sidecar", ps.RestartPolicy)
				}
			}
		})
	}
}

func containerNames(list []corev1.Container) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Name)
	}
	return out
}

// TestNativeSidecar_ProbesAndHooksStayRefusedWithoutAlways: the explained
// refusal of probes and lifecycle holds for a regular init container, named
// with the rule that lifts it, on every kind and for every policy but Always.
func TestNativeSidecar_ProbesAndHooksStayRefusedWithoutAlways(t *testing.T) {
	keys := map[string]any{
		"probes":    map[string]any{"liveness": map[string]any{"exec": map[string]any{"command": []any{"true"}}}},
		"lifecycle": map[string]any{"preStop": map[string]any{"sleep": map[string]any{"seconds": 5}}},
	}
	for _, k := range workloadKinds {
		for key, value := range keys {
			for _, policy := range []string{"", "Never", "OnFailure"} {
				name := k.name + "/" + key + "/policy=" + policy
				t.Run(name, func(t *testing.T) {
					entry := regularInitEntry("c")
					entry[key] = value
					if policy != "" {
						entry["restartPolicy"] = policy
					}
					_, err := k.handler.ToApplicationConfig(&oam.Component{
						Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{entry}}),
					}, "default")
					assertErrorContains(t, err, `initContainers[0] "c"`, key, "init container", "restartPolicy: Always")
				})
			}
		}
	}
}

func assertErrorContains(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not contain %q", err.Error(), w)
		}
	}
}

// TestNativeSidecar_Refusals: what a restartable entry still may not carry.
func TestNativeSidecar_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
		want  []string
	}{
		{
			name: "restart rules are refused beside Always",
			entry: map[string]any{
				"restartPolicy": "Always",
				"restartPolicyRules": []any{map[string]any{
					"action": "Restart", "exitCodes": map[string]any{"operator": "In", "values": []any{1}},
				}},
			},
			want: []string{`initContainers[0] "c"`, "restartPolicyRules", "Always"},
		},
		{
			name:  "ports are not a native sidecar key",
			entry: map[string]any{"restartPolicy": "Always", "ports": []any{map[string]any{"containerPort": 80}}},
			want:  []string{`initContainers[0] "c"`, `unrecognized key "ports"`},
		},
		{
			name: "a probe cannot name a port nothing declares",
			entry: map[string]any{
				"restartPolicy": "Always",
				"probes":        map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"port": "admin"}}},
			},
			want: []string{`initContainers[0] "c"`, `named port "admin"`},
		},
		{
			name:  "an unknown restart policy",
			entry: map[string]any{"restartPolicy": "Sometimes"},
			want:  []string{`initContainers[0] "c"`, `invalid value "Sometimes"`, "Always"},
		},
		{
			name:  "RestartContainer resize is still refused on a regular init container",
			entry: map[string]any{"restartPolicy": "Never", "resizePolicy": []any{map[string]any{"resourceName": "cpu", "restartPolicy": "RestartContainer"}}},
			want:  []string{`initContainers[0] "c"`, "RestartContainer", "restartable"},
		},
	}
	for _, k := range workloadKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				entry := regularInitEntry("c")
				for key, v := range tc.entry {
					entry[key] = v
				}
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{entry}}),
				}, "default")
				assertErrorContains(t, err, tc.want...)
			})
		}
	}
}

// TestNativeSidecar_ResizeRestartContainer: RestartContainer is accepted on a
// restartable entry, as upstream's validateInitContainers does, and refused
// where the pod's own restart policy is Never.
func TestNativeSidecar_ResizeRestartContainer(t *testing.T) {
	entry := func() map[string]any {
		e := nativeSidecarEntry()
		e["resizePolicy"] = []any{map[string]any{"resourceName": "memory", "restartPolicy": "RestartContainer"}}
		return e
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"initContainers": []any{entry()}})
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			rp := ps.InitContainers[0].ResizePolicy
			if len(rp) != 1 || rp[0].RestartPolicy != corev1.RestartContainer {
				t.Errorf("proxy ResizePolicy = %+v, want one RestartContainer entry", rp)
			}
		})
	}
	for _, k := range workloadKinds {
		if k.name != "job" && k.name != "cronjob" {
			continue
		}
		t.Run(k.name+"/pod restartPolicy Never", func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"restartPolicy":  "Never",
				"initContainers": []any{entry()},
			})
			cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			if err == nil {
				_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
			}
			assertErrorContains(t, err, "RestartContainer", "Never")
		})
	}
}

// TestNativeSidecar_PolicyAndResourceDefaults: a restartable entry is held to
// the same environment policy as any init container, and gets the built-in
// request defaults and memory limit, not the policy's defaults, which only
// the main container takes.
func TestNativeSidecar_PolicyAndResourceDefaults(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name+"/defaults", func(t *testing.T) {
			props := withProps(k.props, map[string]any{"initContainers": []any{nativeSidecarEntry()}})
			ps, err := generateKindWithPolicy(t, k, props, &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi"})
			if err != nil {
				t.Fatalf("ApplyPolicy: %v", err)
			}
			res := ps.InitContainers[0].Resources
			assertQuantities(t, "requests", res.Requests, map[corev1.ResourceName]string{corev1.ResourceCPU: "100m", corev1.ResourceMemory: "128Mi"})
			assertQuantities(t, "limits", res.Limits, map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"})
		})
		t.Run(k.name+"/maximum", func(t *testing.T) {
			entry := nativeSidecarEntry()
			entry["resources"] = map[string]any{"limits": map[string]any{"cpu": "4"}}
			props := withProps(k.props, map[string]any{"initContainers": []any{entry}})
			_, err := generateKindWithPolicy(t, k, props, &stubPolicy{maxCPU: "1"})
			assertErrorContains(t, err, `initContainers[0] "proxy"`)
		})
		t.Run(k.name+"/privileged", func(t *testing.T) {
			entry := nativeSidecarEntry()
			entry["securityContext"] = map[string]any{"privileged": true}
			props := withProps(k.props, map[string]any{"initContainers": []any{entry}})
			_, err := generateKindWithPolicy(t, k, props, &stubPolicy{})
			assertErrorContains(t, err, `initContainers[0] "proxy"`)
		})
	}
}

// TestNativeSidecar_PodFailurePolicyMayNameIt: a job's podFailurePolicy may
// address a native sidecar by name, as it may any init container.
func TestNativeSidecar_PodFailurePolicyMayNameIt(t *testing.T) {
	props := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"restartPolicy":  "Never",
		"initContainers": []any{nativeSidecarEntry()},
		"podFailurePolicy": map[string]any{"rules": []any{map[string]any{
			"action":      "Ignore",
			"onExitCodes": map[string]any{"containerName": "proxy", "operator": "In", "values": []any{42}},
		}}},
	}
	ps := podTemplateSpec(t, generateKind(t, &components.JobHandler{}, "job", props))
	if len(ps.InitContainers) != 1 || ps.InitContainers[0].Name != "proxy" {
		t.Errorf("initContainers = %v, want proxy", containerNames(ps.InitContainers))
	}
}
