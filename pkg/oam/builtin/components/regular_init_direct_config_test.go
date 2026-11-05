package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A config built directly in Go does not pass through the parser, which refuses
// a lifecycle or probe on an init container that is not restartable
// (go-kure/launcher#1237). buildInitContainer copies both whatever the restart
// policy, so Generate holds the pod spec it assembles to the same rule, and a
// sleep above the grace period on such a container is refused as the parser
// would refuse it, not passed on to the API server.
func TestDeploymentConfig_RegularInitLifecycleOrProbeRefusedOnADirectConfig(t *testing.T) {
	sleep := func(seconds int64) *corev1.Lifecycle {
		return &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: seconds}}}
	}
	probe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"true"}}}}
	cases := []struct {
		name string
		init components.InitContainerConfig
		want string
	}{
		{"lifecycle sleep above the grace period",
			components.InitContainerConfig{Lifecycle: sleep(31)}, `initContainers[0] "i": lifecycle: not supported on an init container that is not restartable`},
		{"lifecycle sleep within the grace period",
			components.InitContainerConfig{Lifecycle: sleep(0)}, `initContainers[0] "i": lifecycle: not supported on an init container that is not restartable`},
		{"readiness probe",
			components.InitContainerConfig{Probes: components.ProbeConfig{Readiness: probe}}, `initContainers[0] "i": readinessProbe: not supported on an init container that is not restartable`},
		{"liveness probe",
			components.InitContainerConfig{Probes: components.ProbeConfig{Liveness: probe}}, `initContainers[0] "i": livenessProbe: not supported on an init container that is not restartable`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.init.Name, tc.init.Image = "i", "ghcr.io/example/init:v1.0.0"
			cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
				InitContainers: []components.InitContainerConfig{tc.init}}
			_, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one holding %q", err, tc.want)
			}
		})
	}

	t.Run("a restartable init container keeps its lifecycle", func(t *testing.T) {
		always := corev1.ContainerRestartPolicyAlways
		cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
			InitContainers: []components.InitContainerConfig{{
				Name: "nat", Image: "ghcr.io/example/nat:v1.0.0", RestartPolicy: &always, Lifecycle: sleep(30),
			}}}
		if _, err := cfg.Generate(stack.NewApplication("web", "default", cfg)); err != nil {
			t.Fatalf("a native sidecar with a sleep equal to the grace period must build, got: %v", err)
		}
	})

	t.Run("a restartable init container above the grace period is still refused", func(t *testing.T) {
		always := corev1.ContainerRestartPolicyAlways
		cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
			InitContainers: []components.InitContainerConfig{{
				Name: "nat", Image: "ghcr.io/example/nat:v1.0.0", RestartPolicy: &always, Lifecycle: sleep(31),
			}}}
		_, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
		if err == nil || !strings.Contains(err.Error(), `initContainers[0] "nat": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`) {
			t.Fatalf("err = %v\nwant the sleep refused", err)
		}
	})
}
