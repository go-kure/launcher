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
		{"webservice", &components.WebserviceHandler{}, "webservice", map[string]any{"image": "nginx:1.25", "port": 8080}, "http", true},
		{"webservice default port", &components.WebserviceHandler{}, "webservice", map[string]any{"image": "nginx:1.25"}, "http", true},
		{"statefulset", &components.StatefulsetHandler{}, "statefulset", map[string]any{"image": "postgres:16", "port": 5432}, "tcp", true},
		{"statefulset without port", &components.StatefulsetHandler{}, "statefulset", map[string]any{"image": "postgres:16"}, "", false},
		{"daemonset", &components.DaemonsetHandler{}, "daemonset", map[string]any{"image": "prom/node-exporter:v1.0.0", "port": 9100}, "http", true},
		{"daemonset without port", &components.DaemonsetHandler{}, "daemonset", map[string]any{"image": "prom/node-exporter:v1.0.0"}, "", false},
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
