package components_test

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#1225: a native sidecar (an
// `initContainers` entry with `restartPolicy: Always`) may declare `ports`.
// Kubernetes resolves a Service's named targetPort against the ports of a
// restartable init container, so the port has a consumer. A regular init
// container has exited before anything could reach it, so `ports` stays
// refused there, with the reason.

func nativeSidecarWithPorts(name string, ports ...any) map[string]any {
	return map[string]any{"name": name, "image": "ghcr.io/org/" + name + ":v1", "restartPolicy": "Always", "ports": ports}
}

// TestNativeSidecarPorts_Rendered: on every kind the entry's ports render on
// the init container, protocol defaulting to TCP, and the entry's own probe and
// hook may address a declared name.
func TestNativeSidecarPorts_Rendered(t *testing.T) {
	want := []corev1.ContainerPort{
		{Name: "admin", ContainerPort: 9901, Protocol: corev1.ProtocolTCP},
		{ContainerPort: 9902, Protocol: corev1.ProtocolUDP},
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			entry := nativeSidecarWithPorts("proxy",
				map[string]any{"name": "admin", "containerPort": 9901},
				map[string]any{"containerPort": 9902, "protocol": "UDP"},
			)
			entry["probes"] = map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"port": "admin", "path": "/ready"}}}
			entry["lifecycle"] = map[string]any{"preStop": map[string]any{"httpGet": map[string]any{"port": "admin", "path": "/drain"}}}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, map[string]any{"initContainers": []any{entry}})))
			proxy := containerNamed(t, ps.InitContainers, "proxy")
			if !reflect.DeepEqual(proxy.Ports, want) {
				t.Errorf("native sidecar ports = %#v, want %#v", proxy.Ports, want)
			}
			if proxy.ReadinessProbe == nil || proxy.ReadinessProbe.HTTPGet == nil || proxy.ReadinessProbe.HTTPGet.Port != intstr.FromString("admin") {
				t.Errorf("readiness probe = %+v, want httpGet on the named port admin", proxy.ReadinessProbe)
			}
		})
	}
}

// TestNativeSidecarPorts_UndeclaredNameStillRefused: a probe or hook may name
// only a port the same entry declares.
func TestNativeSidecarPorts_UndeclaredNameStillRefused(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			entry := nativeSidecarWithPorts("proxy", map[string]any{"name": "admin", "containerPort": 9901})
			entry["probes"] = map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"port": "metrics"}}}
			_, err := k.handler.ToApplicationConfig(&oam.Component{
				Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{entry}}),
			}, "default")
			assertErrorContains(t, err, `initContainers[0] "proxy"`, `named port "metrics" does not match any port this init container declares (admin)`)
		})
	}
}

// TestNativeSidecarPorts_RefusedWithoutAlways: `ports` on an init container
// that is not restartable names the rule and the reason, for every policy but
// Always, on every kind.
func TestNativeSidecarPorts_RefusedWithoutAlways(t *testing.T) {
	for _, k := range workloadKinds {
		for _, policy := range []string{"", "Never", "OnFailure"} {
			t.Run(k.name+"/policy="+policy, func(t *testing.T) {
				entry := regularInitEntry("c")
				entry["ports"] = []any{map[string]any{"containerPort": 80}}
				if policy != "" {
					entry["restartPolicy"] = policy
				}
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{entry}}),
				}, "default")
				assertErrorContains(t, err, `initContainers[0] "c"`, "ports", "not restartable", "restartPolicy: Always", "exited")
			})
		}
	}
}

// TestNativeSidecarPorts_Refused: the entry's port list is read as a
// sidecar's is, with the same refusals, on every kind.
func TestNativeSidecarPorts_Refused(t *testing.T) {
	cases := []struct {
		name  string
		ports any
		want  string
	}{
		{"not a list", "admin", `initContainers[0] "p": ports: must be an array`},
		{"unknown key", []any{map[string]any{"containerPort": 9901, "hostPort": 9901}}, `initContainers[0] "p": ports[0]: unrecognized key "hostPort"`},
		{"missing containerPort", []any{map[string]any{"name": "admin"}}, `initContainers[0] "p": ports[0].containerPort: required`},
		{"invalid name", []any{map[string]any{"containerPort": 9901, "name": "ADMIN"}}, `initContainers[0] "p": ports[0].name: invalid port name "ADMIN"`},
		{"duplicate name", []any{
			map[string]any{"containerPort": 9901, "name": "admin"},
			map[string]any{"containerPort": 9902, "name": "admin"},
		}, `initContainers[0] "p": ports[1].name: duplicate port name "admin"`},
		{"duplicate port and protocol", []any{
			map[string]any{"containerPort": 9901},
			map[string]any{"containerPort": 9901, "protocol": "TCP"},
		}, `initContainers[0] "p": ports[1]: duplicate port 9901/TCP`},
		{"invalid protocol", []any{map[string]any{"containerPort": 9901, "protocol": "udp"}}, `initContainers[0] "p": ports[0].protocol: must be one of TCP, UDP, SCTP, got "udp"`},
	}
	for _, k := range workloadKinds {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				entry := nativeSidecarWithPorts("p")
				entry["ports"] = tc.ports
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{entry}}),
				}, "default")
				assertErrorContains(t, err, tc.want)
			})
		}
	}
}

// TestNativeSidecarPorts_NameUniqueAcrossThePod: a port name declared by a
// native sidecar and by any other container of the pod is refused, because a
// Service selecting the name reaches only the first container that declares it
// (go-kure/launcher#660). Every kind is covered for two native sidecars; the
// kinds with a main-container port list or sidecars cover those too.
func TestNativeSidecarPorts_NameUniqueAcrossThePod(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name+"/native-vs-native", func(t *testing.T) {
			_, err := k.handler.ToApplicationConfig(&oam.Component{
				Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{"initContainers": []any{
					nativeSidecarWithPorts("a", map[string]any{"name": "metrics", "containerPort": 9090}),
					nativeSidecarWithPorts("b", map[string]any{"name": "metrics", "containerPort": 9091}),
				}}),
			}, "default")
			assertErrorContains(t, err, `initContainers[1] "b": ports[0].name: port name "metrics" is also declared by initContainers[0] "a"`)
		})
	}
	for kind, k := range sidecarKindProps() {
		t.Run(kind+"/native-vs-sidecar", func(t *testing.T) {
			err := kindConvertErr(k.handler, kind, withProps(k.props, map[string]any{
				"initContainers": []any{nativeSidecarWithPorts("a", map[string]any{"name": "metrics", "containerPort": 9090})},
				"sidecars":       []any{sidecarWithPorts("b", map[string]any{"name": "metrics", "containerPort": 9091})},
			}))
			assertErrorContains(t, err, `sidecars[0] "b": ports[0].name: port name "metrics" is also declared by initContainers[0] "a"`)
		})
	}
	mainCases := []struct {
		kind  string
		props map[string]any
		port  string
	}{
		{"webservice", map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}, "http"},
		{"deployment", map[string]any{"image": "ghcr.io/org/app:v1", "ports": []any{map[string]any{"name": "grpc", "containerPort": 9000}}}, "grpc"},
		{"statefulset", map[string]any{"image": "ghcr.io/org/app:v1", "ports": []any{map[string]any{"name": "tcp", "containerPort": 5432}}}, "tcp"},
		{"daemonset", map[string]any{"image": "ghcr.io/org/app:v1", "ports": []any{map[string]any{"name": "metrics", "containerPort": 9100}}}, "metrics"},
	}
	kinds := sidecarKindProps()
	for _, tc := range mainCases {
		t.Run(tc.kind+"/native-vs-main", func(t *testing.T) {
			props := withProps(tc.props, map[string]any{"initContainers": []any{
				nativeSidecarWithPorts("proxy", map[string]any{"name": tc.port, "containerPort": 15000}),
			}})
			err := kindConvertErr(kinds[tc.kind].handler, tc.kind, props)
			assertErrorContains(t, err, `initContainers[0] "proxy": ports[0].name: port name "`+tc.port+`" is also declared by the main container`)
		})
	}
}

// TestNativeSidecarPorts_NameUniqueWithJobMainPorts: job and cronjob take
// `ports` on the main container, so a native sidecar port named like one of
// them is refused there too.
func TestNativeSidecarPorts_NameUniqueWithJobMainPorts(t *testing.T) {
	found := 0
	for _, k := range workloadKinds {
		if k.name != "job" && k.name != "cronjob" {
			continue
		}
		found++
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"ports": []any{map[string]any{"name": "metrics", "containerPort": 9100}},
				"initContainers": []any{
					nativeSidecarWithPorts("proxy", map[string]any{"name": "metrics", "containerPort": 15000}),
				},
			})
			_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			assertErrorContains(t, err, `initContainers[0] "proxy": ports[0].name: port name "metrics" is also declared by the main container`)
		})
	}
	if found != 2 {
		t.Fatalf("found %d of job and cronjob in workloadKinds, want 2", found)
	}
}

// TestNativeSidecarPorts_DistinctNamesAccepted keeps what the check must not
// refuse: distinct names across containers, and the same number in different
// containers when unnamed.
func TestNativeSidecarPorts_DistinctNamesAccepted(t *testing.T) {
	for kind, k := range sidecarKindProps() {
		t.Run(kind, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"initContainers": []any{nativeSidecarWithPorts("a", map[string]any{"name": "admin", "containerPort": 9901}, map[string]any{"containerPort": 9903})},
				"sidecars":       []any{sidecarWithPorts("b", map[string]any{"name": "metrics", "containerPort": 9090}, map[string]any{"containerPort": 9903})},
			})
			if err := kindConvertErr(k.handler, kind, props); err != nil {
				t.Fatalf("distinct names refused: %v", err)
			}
		})
	}
}

// TestNativeSidecarPorts_RenderTwice: the rendered ports are the container's
// own copy, so mutating one render of a config leaves a second Generate of the
// same parsed config untouched. The config is parsed once; reparsing between
// the renders would hide a slice shared with it.
func TestNativeSidecarPorts_RenderTwice(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"initContainers": []any{
				nativeSidecarWithPorts("proxy", map[string]any{"name": "admin", "containerPort": 9901}),
			}})
			cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			render := func() corev1.PodSpec {
				t.Helper()
				out, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				return podTemplateSpec(t, out)
			}
			first := render()
			first.InitContainers[0].Ports[0].ContainerPort = 1
			if got := render().InitContainers[0].Ports[0].ContainerPort; got != 9901 {
				t.Errorf("second render port = %d, want 9901", got)
			}
		})
	}
}
