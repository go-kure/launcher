package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// go-kure/launcher#545: every workload kind that generates a Service reports the name of that
// Service's port (ServicePortName), so routing traits can hold an implicit backend addressed by
// portName to it. The reported name must be the one the generated Service actually carries; a
// kind configured without a port reports none.
func TestWorkloadConfigs_ServicePortNameMatchesGeneratedService(t *testing.T) {
	tests := []struct {
		name      string
		handler   oam.ComponentHandler
		kind      string
		props     map[string]any
		wantName  string
		wantKnown bool
	}{
		{"webservice", webserviceViaRule{}, "webservice", map[string]any{"image": "nginx:1.25", "port": 8080}, "http", true},
		{"webservice default port", webserviceViaRule{}, "webservice", map[string]any{"image": "nginx:1.25"}, "http", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := tt.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: tt.kind, Properties: tt.props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			pn, ok := cfg.(interface{ ServicePortName() (string, bool) })
			if !ok {
				t.Fatalf("%T does not implement ServicePortName", cfg)
			}
			name, known := pn.ServicePortName()
			if name != tt.wantName || known != tt.wantKnown {
				t.Fatalf("ServicePortName() = %q, %v; want %q, %v", name, known, tt.wantName, tt.wantKnown)
			}

			objects, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			var ports []corev1.ServicePort
			for _, obj := range objects {
				if svc, ok := (*obj).(*corev1.Service); ok {
					ports = append(ports, svc.Spec.Ports...)
				}
			}
			if !known {
				if len(ports) != 0 {
					t.Fatalf("no port name reported, but the generated Service carries ports %+v", ports)
				}
				return
			}
			if len(ports) != 1 || ports[0].Name != name {
				t.Fatalf("generated Service ports = %+v, want exactly one named %q", ports, name)
			}
		})
	}
}

// daemonset and statefulset emit no Service since go-kure/launcher#690, so neither is an implicit
// backend: their configs report no service port, port name or backend Service name, and a
// routing trait on one must name an authored `service`.
func TestWorkloadConfigs_NoServiceKindsAreNotBackends(t *testing.T) {
	ports := []any{map[string]any{"name": "http", "containerPort": 9100}}
	for _, tc := range []struct {
		kind    string
		handler oam.ComponentHandler
		props   map[string]any
	}{
		{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "prom/node-exporter:v1.0.0", "ports": ports}},
		{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "postgres:16", "ports": ports, "serviceName": "db"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: tc.kind, Properties: tc.props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if _, ok := cfg.(interface{ ServicePort() int32 }); ok {
				t.Errorf("%T implements ServicePort", cfg)
			}
			if _, ok := cfg.(interface{ ServicePortName() (string, bool) }); ok {
				t.Errorf("%T implements ServicePortName", cfg)
			}
			if _, ok := cfg.(interface{ BackendServiceName() string }); ok {
				t.Errorf("%T implements BackendServiceName", cfg)
			}
		})
	}
}
