package components_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#660: a sidecar's ports were read without
// the main container's checks, and no check looked across containers, so a
// port name repeated between the main container and a sidecar, or between two
// sidecars, deployed with only an API server warning while a Service selecting
// the name reached just the first container.

type sidecarKind struct {
	handler oam.ComponentHandler
	props   map[string]any
}

// sidecarKindProps returns each sidecar-bearing kind's handler and minimum
// properties, keyed by kind.
func sidecarKindProps() map[string]sidecarKind {
	out := map[string]sidecarKind{}
	for _, k := range workloadKinds {
		if sidecarKinds[k.name] {
			out[k.name] = sidecarKind{k.handler, k.props}
		}
	}
	return out
}

func kindConvertErr(h oam.ComponentHandler, kind string, props map[string]any) error {
	_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	return err
}

func sidecarWithPorts(name string, ports ...any) map[string]any {
	return map[string]any{"name": name, "image": "ghcr.io/org/" + name + ":v1", "ports": ports}
}

// TestSidecarPorts_Refused pins each refusal of a sidecar's own port list, on
// every kind with sidecars: the sidecar now reads its ports as the deployment
// kind reads its main container's.
func TestSidecarPorts_Refused(t *testing.T) {
	cases := []struct {
		name    string
		sidecar map[string]any
		want    string
	}{
		{"not a list", map[string]any{"name": "s", "image": "ghcr.io/org/s:v1", "ports": "admin"}, `sidecars[0] "s": ports: must be an array`},
		{"entry not an object", sidecarWithPorts("s", 9901), `sidecars[0] "s": ports[0]: must be an object`},
		{"unknown key", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "hostPort": 9901}), `sidecars[0] "s": ports[0]: unrecognized key "hostPort"`},
		{"missing containerPort", sidecarWithPorts("s", map[string]any{"name": "admin"}), `sidecars[0] "s": ports[0].containerPort: required`},
		{"name not a string", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "name": 7}), `sidecars[0] "s": ports[0].name`},
		{"invalid name", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "name": "ADMIN"}), `sidecars[0] "s": ports[0].name: invalid port name "ADMIN"`},
		{"duplicate name", sidecarWithPorts("s",
			map[string]any{"containerPort": 9901, "name": "admin"},
			map[string]any{"containerPort": 9902, "name": "admin"},
		), `sidecars[0] "s": ports[1].name: duplicate port name "admin"`},
		{"invalid protocol", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "protocol": "udp"}), `sidecars[0] "s": ports[0].protocol: must be one of TCP, UDP, SCTP, got "udp"`},
		// A non-string protocol used to read as TCP.
		{"protocol not a string", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "protocol": 7}), `sidecars[0] "s": ports[0].protocol: must be a string, got int`},
		{"empty protocol", sidecarWithPorts("s", map[string]any{"containerPort": 9901, "protocol": ""}), `sidecars[0] "s": ports[0].protocol: must be one of TCP, UDP, SCTP, got ""`},
		{"duplicate port and protocol", sidecarWithPorts("s",
			map[string]any{"containerPort": 9901},
			map[string]any{"containerPort": 9901, "protocol": "TCP", "name": "admin"},
		), `sidecars[0] "s": ports[1]: duplicate port 9901/TCP`},
	}
	for kind, k := range sidecarKindProps() {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				err := kindConvertErr(k.handler, kind, withProps(k.props, map[string]any{"sidecars": []any{tc.sidecar}}))
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want one containing %q", err, tc.want)
				}
			})
		}
	}
}

// TestSidecarPorts_Rendered keeps the accepted shapes: protocol defaulting to
// TCP, an explicit UDP, and a nameless port.
func TestSidecarPorts_Rendered(t *testing.T) {
	want := []corev1.ContainerPort{
		{Name: "admin", ContainerPort: 9901, Protocol: corev1.ProtocolTCP},
		{ContainerPort: 9902, Protocol: corev1.ProtocolUDP},
	}
	for kind, k := range sidecarKindProps() {
		t.Run(kind, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"sidecars": []any{sidecarWithPorts("s",
				map[string]any{"name": "admin", "containerPort": 9901},
				map[string]any{"containerPort": 9902, "protocol": "UDP"},
			)}})
			ps := podTemplateSpec(t, generateKind(t, k.handler, kind, props))
			if got := containerNamed(t, ps.Containers, "s").Ports; !reflect.DeepEqual(got, want) {
				t.Errorf("sidecar ports = %#v, want %#v", got, want)
			}
		})
	}
}

// TestPodPortNames_SidecarVsSidecar refuses a name two sidecars both declare,
// on every kind with sidecars, naming both containers.
func TestPodPortNames_SidecarVsSidecar(t *testing.T) {
	const want = `sidecars[1] "b": ports[0].name: port name "metrics" is also declared by sidecars[0] "a"`
	for kind, k := range sidecarKindProps() {
		t.Run(kind, func(t *testing.T) {
			err := kindConvertErr(k.handler, kind, withProps(k.props, map[string]any{"sidecars": []any{
				sidecarWithPorts("a", map[string]any{"name": "metrics", "containerPort": 9090}),
				sidecarWithPorts("b", map[string]any{"name": "metrics", "containerPort": 9091}),
			}}))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
	}
}

// TestPodPortNames_MainVsSidecar refuses a sidecar port named like one of the
// main container's, for each kind's own main-container port names.
func TestPodPortNames_MainVsSidecar(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		props map[string]any
		port  string
	}{
		{"webservice http", "webservice", map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}, "http"},
		// webservice's main port is named "http" even when `port` is left to its default.
		{"webservice default port", "webservice", map[string]any{"image": "ghcr.io/org/app:v1"}, "http"},
		{"statefulset ports list", "statefulset", map[string]any{"image": "ghcr.io/org/app:v1", "ports": []any{
			map[string]any{"name": "tcp", "containerPort": 5432},
		}}, "tcp"},
		{"deployment ports list", "deployment", map[string]any{"image": "ghcr.io/org/app:v1", "ports": []any{
			map[string]any{"name": "grpc", "containerPort": 9000},
		}}, "grpc"},
	}
	kinds := sidecarKindProps()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := withProps(tc.props, map[string]any{"sidecars": []any{
				sidecarWithPorts("proxy", map[string]any{"name": tc.port, "containerPort": 15000}),
			}})
			want := `sidecars[0] "proxy": ports[0].name: port name "` + tc.port + `" is also declared by the main container`
			err := kindConvertErr(kinds[tc.kind].handler, tc.kind, props)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
	}
}

// TestPodPortNames_Accepted keeps what the check must not refuse: distinct
// names across containers, nameless ports sharing a number across containers
// (each container has its own network view of declared ports), a statefulset
// sidecar named "tcp" when the main container declares no ports, and a worker
// sidecar named "http" (worker's main container declares none).
func TestPodPortNames_Accepted(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		props map[string]any
	}{
		{"distinct names", "webservice", map[string]any{"image": "ghcr.io/org/app:v1", "sidecars": []any{
			sidecarWithPorts("a", map[string]any{"name": "metrics", "containerPort": 9090}),
			sidecarWithPorts("b", map[string]any{"name": "admin", "containerPort": 9091}),
		}}},
		{"nameless ports", "deployment", map[string]any{"image": "ghcr.io/org/app:v1",
			"ports": []any{map[string]any{"containerPort": 8080}},
			"sidecars": []any{
				sidecarWithPorts("a", map[string]any{"containerPort": 9090}),
				sidecarWithPorts("b", map[string]any{"containerPort": 9091}),
			}}},
		{"statefulset without port", "statefulset", map[string]any{"image": "ghcr.io/org/app:v1", "sidecars": []any{
			sidecarWithPorts("proxy", map[string]any{"name": "tcp", "containerPort": 15000}),
		}}},
		{"worker without main ports", "worker", map[string]any{"image": "ghcr.io/org/app:v1", "sidecars": []any{
			sidecarWithPorts("proxy", map[string]any{"name": "http", "containerPort": 15000}),
		}}},
	}
	kinds := sidecarKindProps()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			generateKind(t, kinds[tc.kind].handler, tc.kind, tc.props)
		})
	}
}
