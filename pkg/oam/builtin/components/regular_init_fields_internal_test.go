package components

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestRefuseRegularInitFields holds the helper to upstream's rule: a lifecycle
// or one of the three probes is refused on an init container whose
// restartPolicy is not Always, whatever else it carries, and read on no
// regular container. The label names the pod spec's prefix, the index and the
// container; the first offending container and field are reported.
func TestRefuseRegularInitFields(t *testing.T) {
	alwaysPolicy := corev1.ContainerRestartPolicyAlways
	always := &alwaysPolicy
	neverPolicy := corev1.ContainerRestartPolicy("Never")
	never := &neverPolicy
	hook := &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"true"}}}}
	probe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"true"}}}}

	cases := []struct {
		name string
		init []corev1.Container
		// want is the refusal's text after the label, "" for a spec the rule accepts.
		want string
	}{
		{name: "no init container"},
		{name: "regular init with none of the fields", init: []corev1.Container{{Name: "i"}}},
		{name: "regular init with a port is not this rule's", init: []corev1.Container{{Name: "i", Ports: []corev1.ContainerPort{{ContainerPort: 80}}}}},
		{name: "restartable init with every field", init: []corev1.Container{{Name: "i", RestartPolicy: always, Lifecycle: hook, LivenessProbe: probe, ReadinessProbe: probe, StartupProbe: probe}}},
		{name: "lifecycle, nil restartPolicy", init: []corev1.Container{{Name: "i", Lifecycle: hook}}, want: "lifecycle: not supported on an init container that is not restartable — " + regularInitLifecycleReason},
		{name: "lifecycle, Never", init: []corev1.Container{{Name: "i", RestartPolicy: never, Lifecycle: hook}}, want: "lifecycle: not supported on an init container that is not restartable — " + regularInitLifecycleReason},
		{name: "liveness", init: []corev1.Container{{Name: "i", LivenessProbe: probe}}, want: "livenessProbe: not supported on an init container that is not restartable — " + regularInitProbesReason},
		{name: "readiness", init: []corev1.Container{{Name: "i", ReadinessProbe: probe}}, want: "readinessProbe: not supported on an init container that is not restartable — " + regularInitProbesReason},
		{name: "startup", init: []corev1.Container{{Name: "i", StartupProbe: probe}}, want: "startupProbe: not supported on an init container that is not restartable — " + regularInitProbesReason},
		{name: "lifecycle is named before a probe", init: []corev1.Container{{Name: "i", Lifecycle: hook, LivenessProbe: probe}}, want: "lifecycle: not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := &corev1.PodSpec{InitContainers: tc.init, Containers: []corev1.Container{{Name: "app", Lifecycle: hook, LivenessProbe: probe, ReadinessProbe: probe, StartupProbe: probe}}}
			err := refuseRegularInitFields("spec.template.spec.", ps)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			label := `spec.template.spec.initContainers[0] "i": `
			if err == nil || !strings.HasPrefix(err.Error(), label+tc.want) {
				t.Fatalf("err = %v\nwant one starting %q", err, label+tc.want)
			}
		})
	}

	t.Run("second init container is reported by its index", func(t *testing.T) {
		ps := &corev1.PodSpec{InitContainers: []corev1.Container{{Name: "a", RestartPolicy: always, Lifecycle: hook}, {Name: "b", StartupProbe: probe}}}
		err := refuseRegularInitFields("", ps)
		if err == nil || !strings.HasPrefix(err.Error(), `initContainers[1] "b": startupProbe: not supported`) {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestRestartableOnlyInitKeys_TextsUnchanged pins the app.yaml refusal texts:
// the typed rule shares their sentences (regularInitProbesReason,
// regularInitLifecycleReason), and the app.yaml tail stays on them.
func TestRestartableOnlyInitKeys_TextsUnchanged(t *testing.T) {
	want := map[string]string{
		"probes":    "Kubernetes forbids probes on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar, or author them on a sidecar instead",
		"lifecycle": "Kubernetes forbids lifecycle hooks on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar, or author them on a sidecar instead",
	}
	for k, w := range want {
		if got := restartableOnlyInitKeys[k]; got != w {
			t.Errorf("restartableOnlyInitKeys[%q] =\n%s\nwant\n%s", k, got, w)
		}
	}
}
