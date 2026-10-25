package components

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TestRefuseRenamedProbedPort pins refuseRenamedProbedPort to the
// operator's merge of a patch (MergePatchContainers and sanitizeProbeHandlers,
// pkg/k8s/merge.go:27-121 at prometheus-operator v0.94.1): a probe of the
// patch that sets no handler type keeps the operator's, which names portName;
// one that sets any gives the patch's (go-kure/launcher#963).
func TestRefuseRenamedProbedPort(t *testing.T) {
	generated := []corev1.ContainerPort{{Name: "web", ContainerPort: 9090, Protocol: corev1.ProtocolTCP}}
	rename := []corev1.ContainerPort{{Name: "http", ContainerPort: 9090}}
	http := func(port intstr.IntOrString) *corev1.Probe {
		return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: port}}}
	}
	tcp := func(port intstr.IntOrString) *corev1.Probe {
		return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: port}}}
	}
	exec := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"true"}}}}
	grpc := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: 9090}}}
	timed := &corev1.Probe{PeriodSeconds: 10}
	both := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: exec.Exec, HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString("web")}}}
	// split names the port by number in one handler type and by portName in
	// another: the patch's whole handler is kept, and the number does not
	// hide the name.
	split := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9090)}, HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString("web")}}}
	patch := func(ports []corev1.ContainerPort, startup, liveness, readiness *corev1.Probe) []corev1.Container {
		return []corev1.Container{{Name: "prometheus", Ports: ports, StartupProbe: startup, LivenessProbe: liveness, ReadinessProbe: readiness}}
	}
	all := []string{"startup", "liveness", "readiness"}
	cases := []struct {
		name       string
		containers []corev1.Container
		portName   string
		generated  []corev1.ContainerPort
		want       string // the probes the refusal lists; "" admits
	}{
		{"no patch", nil, "", generated, ""},
		{"a patch of another container", []corev1.Container{{Name: "config-reloader", Ports: rename}}, "", generated, ""},
		{"a patch without the web port", patch([]corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}, nil, nil, nil), "", generated, ""},
		{"a patch that keeps portName's name", patch([]corev1.ContainerPort{{Name: "web", ContainerPort: 9090}}, nil, nil, nil), "", generated, ""},
		{"a patch of the web port without a name", patch([]corev1.ContainerPort{{ContainerPort: 9090}}, nil, nil, nil), "", generated, ""},
		{"a patch that names the port as an authored portName does", patch(rename, nil, nil, nil), "http", []corev1.ContainerPort{{Name: "http", ContainerPort: 9090}}, ""},
		{"no generated web port (listenLocal)", patch(rename, nil, nil, nil), "", nil, ""},
		// The probes name the port by name, so a port the patch adds under
		// portName's name serves them.
		{"a rename and a port the patch adds of portName's name", patch(append(slices.Clone(rename), corev1.ContainerPort{Name: "web", ContainerPort: 9091}), nil, nil, nil), "", generated, ""},
		{"a rename and no probe of the patch", patch(rename, nil, nil, nil), "", generated, "startup, liveness and readiness probes"},
		{"a rename of an authored portName", patch(rename, nil, nil, nil), "metrics", []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9090}}, `portName "metrics" the Prometheus operator's startup, liveness and readiness probes`},
		{"probes by number", patch(rename, http(intstr.FromInt32(9090)), http(intstr.FromInt32(9090)), http(intstr.FromInt32(9090))), "", generated, ""},
		{"probes by the new name", patch(rename, http(intstr.FromString("http")), http(intstr.FromString("http")), http(intstr.FromString("http"))), "", generated, ""},
		{"probes over TCP", patch(rename, tcp(intstr.FromInt32(9090)), tcp(intstr.FromInt32(9090)), tcp(intstr.FromInt32(9090))), "", generated, ""},
		{"probes with a command", patch(rename, exec, exec, exec), "", generated, ""},
		{"probes over gRPC", patch(rename, grpc, grpc, grpc), "", generated, ""},
		{"liveness and readiness re-pointed only", patch(rename, nil, exec, exec), "", generated, "startup probe names"},
		{"startup and liveness re-pointed only", patch(rename, exec, exec, nil), "", generated, "readiness probe names"},
		{"a probe of timing fields only", patch(rename, exec, timed, exec), "", generated, "liveness probe names"},
		{"a probe by portName", patch(rename, exec, exec, http(intstr.FromString("web"))), "", generated, "readiness probe names"},
		{"a probe over TCP by portName", patch(rename, tcp(intstr.FromString("web")), exec, exec), "", generated, "startup probe names"},
		{"a probe of two handler types, one by portName", patch(rename, exec, both, exec), "", generated, "liveness probe names"},
		{"a probe of two handler types, one by portName, one by number", patch(rename, exec, split, exec), "", generated, "liveness probe names"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refuseRenamedProbedPort(tc.containers, "prometheus", 9090, tc.portName, tc.generated, all)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tc.want != "" && err == nil:
				t.Fatalf("err = nil, want one naming %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}
