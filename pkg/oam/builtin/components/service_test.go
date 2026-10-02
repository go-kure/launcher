package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// serviceRoutingTargeter is the optional interface pkg/oam's inbound NetworkPolicy
// synthesis reads off a component config (unexported there, so restated here).
type serviceRoutingTargeter interface {
	ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString)
}

// serviceFrontingOtherWorkload is the shape the kind exists for (go-kure/launcher#411): a
// Service named "api" in front of a workload component named "api-server", with a port whose
// targetPort differs from its port and a second, UDP port.
func serviceFrontingOtherWorkload() map[string]any {
	return map[string]any{
		"selector": map[string]any{"app": "api-server"},
		"ports": []any{
			map[string]any{"name": "http", "port": 80, "targetPort": 8080},
			map[string]any{"name": "dns", "port": 53, "protocol": "UDP"},
		},
	}
}

func serviceConfig(t *testing.T, name string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := (&components.ServiceHandler{}).ToApplicationConfig(
		&oam.Component{Name: name, Type: "service", Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

func generateService(t *testing.T, name string, props map[string]any) *corev1.Service {
	t.Helper()
	cfg := serviceConfig(t, name, props)
	objs, err := cfg.Generate(stack.NewApplication(name, "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected exactly one object (the Service), got %d", len(objs))
	}
	svc, ok := (*objs[0]).(*corev1.Service)
	if !ok {
		t.Fatalf("expected *corev1.Service, got %T", *objs[0])
	}
	return svc
}

func TestServiceHandler_CanHandle(t *testing.T) {
	h := &components.ServiceHandler{}
	if !h.CanHandle("service") {
		t.Error("expected true for service")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

// A Service in front of a differently named workload: the selector is the authored one,
// never the component's own labels; targetPort is kept apart from port; the UDP port keeps
// its protocol. Only the Service is emitted — the workload owns its ServiceAccount.
func TestServiceHandler_Generate_FrontsDifferentlyNamedWorkload(t *testing.T) {
	svc := generateService(t, "api", serviceFrontingOtherWorkload())

	if svc.Name != "api" || svc.Namespace != "default" {
		t.Errorf("metadata = %s/%s, want default/api", svc.Namespace, svc.Name)
	}
	if len(svc.Labels) != 1 || svc.Labels["app"] != "api" {
		t.Errorf("labels = %v, want app=api", svc.Labels)
	}
	if len(svc.Spec.Selector) != 1 || svc.Spec.Selector["app"] != "api-server" {
		t.Errorf("selector = %v, want app=api-server", svc.Spec.Selector)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %q, want ClusterIP", svc.Spec.Type)
	}
	want := []corev1.ServicePort{
		{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP},
		{Name: "dns", Port: 53, TargetPort: intstr.FromInt32(53), Protocol: corev1.ProtocolUDP},
	}
	if len(svc.Spec.Ports) != len(want) {
		t.Fatalf("ports = %+v, want %+v", svc.Spec.Ports, want)
	}
	for i := range want {
		if svc.Spec.Ports[i] != want[i] {
			t.Errorf("ports[%d] = %+v, want %+v", i, svc.Spec.Ports[i], want[i])
		}
	}
}

func TestServiceHandler_Generate_Defaults(t *testing.T) {
	svc := generateService(t, "web", map[string]any{
		"ports": []any{map[string]any{"port": 8080}},
	})
	if len(svc.Spec.Selector) != 1 || svc.Spec.Selector["app"] != "web" {
		t.Errorf("default selector = %v, want app=web", svc.Spec.Selector)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("ports = %+v, want one", svc.Spec.Ports)
	}
	p := svc.Spec.Ports[0]
	if p.Name != "" || p.Port != 8080 || p.TargetPort != intstr.FromInt32(8080) || p.Protocol != corev1.ProtocolTCP {
		t.Errorf("port = %+v, want unnamed 8080->8080/TCP", p)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %q, want ClusterIP", svc.Spec.Type)
	}
}

func TestServiceHandler_Generate_NamedTargetPortAndType(t *testing.T) {
	svc := generateService(t, "web", map[string]any{
		"type":  "LoadBalancer",
		"ports": []any{map[string]any{"port": 443, "targetPort": "https"}},
	})
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Errorf("type = %q, want LoadBalancer", svc.Spec.Type)
	}
	if got := svc.Spec.Ports[0].TargetPort; got != intstr.FromString("https") {
		t.Errorf("targetPort = %v, want the named port https", got)
	}
}

// The selector must not alias a map a caller can reach: stamping a label onto the emitted
// Service's selector must not change what a second Generate emits.
func TestServiceHandler_Generate_SelectorNotAliased(t *testing.T) {
	cfg := serviceConfig(t, "api", serviceFrontingOtherWorkload())
	app := stack.NewApplication("api", "default", cfg)
	first, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	(*first[0]).(*corev1.Service).Spec.Selector["extra"] = "x"
	second, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if sel := (*second[0]).(*corev1.Service).Spec.Selector; len(sel) != 1 {
		t.Errorf("second Generate selector = %v, want only app=api-server", sel)
	}
}

func TestServiceHandler_Rejects(t *testing.T) {
	port := func(m map[string]any) map[string]any { return map[string]any{"ports": []any{m}} }
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"ports missing", map[string]any{}, "ports: at least one port is required"},
		{"ports empty", map[string]any{"ports": []any{}}, "ports: at least one port is required"},
		{"ports not an array", map[string]any{"ports": 80}, "ports: must be an array"},
		{"port missing", port(map[string]any{"name": "http"}), "ports[0].port: required"},
		{"port wrong type", port(map[string]any{"port": "80"}), "ports[0].port: must be an integer"},
		{"port out of range", port(map[string]any{"port": 70000}), "ports[0].port: must be between 1 and 65535"},
		{"targetPort out of range", port(map[string]any{"port": 80, "targetPort": 0}), "ports[0].targetPort: must be between 1 and 65535"},
		{"targetPort invalid name", port(map[string]any{"port": 80, "targetPort": "not_a_port"}), "ports[0].targetPort: invalid port name"},
		// Property validation refuses this first on a built document (union_leaves_test.go);
		// the parser keeps its own check for callers that skip it, such as Endpoints.
		{"targetPort wrong type", port(map[string]any{"port": 80, "targetPort": true}), "ports[0].targetPort: must be an integer or a port name"},
		{"protocol invalid", port(map[string]any{"port": 80, "protocol": "HTTP"}), "ports[0].protocol: must be one of TCP, UDP, SCTP"},
		{"name invalid", port(map[string]any{"port": 80, "name": "HTTP"}), "ports[0].name: invalid port name"},
		{"unknown key", port(map[string]any{"port": 80, "nodePort": 30080}), `ports[0]: unrecognized key "nodePort"`},
		{"unnamed port among several", map[string]any{"ports": []any{
			map[string]any{"name": "http", "port": 80},
			map[string]any{"port": 443},
		}}, "ports[1].name: required when the Service has more than one port"},
		{"duplicate name", map[string]any{"ports": []any{
			map[string]any{"name": "http", "port": 80},
			map[string]any{"name": "http", "port": 443},
		}}, `ports[1].name: duplicate port name "http"`},
		{"duplicate port and protocol", map[string]any{"ports": []any{
			map[string]any{"name": "a", "port": 80},
			map[string]any{"name": "b", "port": 80, "protocol": "TCP"},
		}}, "ports[1]: duplicate port 80/TCP"},
		{"selector empty", map[string]any{"selector": map[string]any{}, "ports": []any{map[string]any{"port": 80}}}, "selector: must name at least one label"},
		{"selector non-string value", map[string]any{"selector": map[string]any{"app": 1}, "ports": []any{map[string]any{"port": 80}}}, "selector.app: must be a string"},
		{"type invalid", map[string]any{"type": "ExternalName", "ports": []any{map[string]any{"port": 80}}}, "type: must be one of ClusterIP, NodePort, LoadBalancer"},
		// go-kure/launcher#690: only None, and only on a ClusterIP Service. A set-but-empty
		// value is refused, not read as absent.
		{"clusterIP empty", map[string]any{"clusterIP": "", "ports": []any{map[string]any{"port": 80}}}, `clusterIP: must be "None", got ""`},
		{"clusterIP address", map[string]any{"clusterIP": "10.0.0.10", "ports": []any{map[string]any{"port": 80}}}, `clusterIP: must be "None", got "10.0.0.10"`},
		{"clusterIP lowercase none", map[string]any{"clusterIP": "none"}, `clusterIP: must be "None", got "none"`},
		{"clusterIP wrong type", map[string]any{"clusterIP": true}, "clusterIP: must be a string"},
		{"headless NodePort", map[string]any{"clusterIP": "None", "type": "NodePort", "ports": []any{map[string]any{"port": 80}}}, `clusterIP: "None" requires type ClusterIP, got NodePort`},
		{"headless LoadBalancer", map[string]any{"clusterIP": "None", "type": "LoadBalancer"}, `clusterIP: "None" requires type ClusterIP, got LoadBalancer`},
		{"ports missing, clusterIP null", map[string]any{"clusterIP": nil}, "ports: at least one port is required"},
	}
	h := &components.ServiceHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{Name: "api", Type: "service", Properties: tt.props}, "default")
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The component name becomes the Service's name, and the API server validates a Service name as a
// DNS-1035 label: stricter than the DNS-1123 subdomain every component name already passes. A name
// that is a valid subdomain but not a valid Service name is refused here, on both entry points, rather
// than emitted as a Service the cluster rejects on apply.
func TestServiceHandler_RejectsInvalidServiceName(t *testing.T) {
	props := func() map[string]any { return map[string]any{"ports": []any{map[string]any{"port": 80}}} }
	tests := []struct {
		name      string
		component string
	}{
		{"dot", "api.v1"},
		{"longer than 63 characters", strings.Repeat("a", 64)},
		{"leading digit", "1api"},
	}
	h := &components.ServiceHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &oam.Component{Name: tt.component, Type: "service", Properties: props()}
			want := "is not a valid Service name"
			if _, err := h.ToApplicationConfig(comp, "default"); err == nil || !strings.Contains(err.Error(), want) ||
				!strings.Contains(err.Error(), "DNS-1035 label") {
				t.Errorf("ToApplicationConfig(%q) error = %v, want one containing %q and the DNS-1035 rule", tt.component, err, want)
			}
			if _, err := h.Endpoints(comp); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Endpoints(%q) error = %v, want one containing %q", tt.component, err, want)
			}
		})
	}
}

// A name at the DNS-1035 limit still builds: the check is the Service-name rule, not a tighter one.
func TestServiceHandler_AcceptsLongestValidServiceName(t *testing.T) {
	name := "a" + strings.Repeat("b-", 30) + "cd" // 63 characters
	if len(name) != 63 {
		t.Fatalf("test name is %d characters, want 63", len(name))
	}
	if svc := generateService(t, name, map[string]any{"ports": []any{map[string]any{"port": 80}}}); svc.Name != name {
		t.Errorf("Service name = %q, want %q", svc.Name, name)
	}
}

// Generate names the Service after the Application, which a library caller builds itself, so it
// applies the same rule to the name it actually emits.
func TestServiceConfig_GenerateRejectsInvalidServiceName(t *testing.T) {
	cfg := serviceConfig(t, "api", map[string]any{"ports": []any{map[string]any{"port": 80}}})
	if _, err := cfg.Generate(stack.NewApplication("api.v1", "default", cfg)); err == nil ||
		!strings.Contains(err.Error(), "is not a valid Service name") {
		t.Errorf("Generate(api.v1) error = %v, want an invalid Service name error", err)
	}
}

// The same port number on two protocols is legal (a DNS Service on 53/TCP and 53/UDP).
func TestServiceHandler_SamePortDifferentProtocols(t *testing.T) {
	svc := generateService(t, "dns", map[string]any{"ports": []any{
		map[string]any{"name": "dns-tcp", "port": 53},
		map[string]any{"name": "dns-udp", "port": 53, "protocol": "UDP"},
	}})
	want := []corev1.ServicePort{
		{Name: "dns-tcp", Port: 53, TargetPort: intstr.FromInt32(53), Protocol: corev1.ProtocolTCP},
		{Name: "dns-udp", Port: 53, TargetPort: intstr.FromInt32(53), Protocol: corev1.ProtocolUDP},
	}
	if len(svc.Spec.Ports) != len(want) {
		t.Fatalf("ports = %+v, want %+v", svc.Spec.Ports, want)
	}
	for i := range want {
		if svc.Spec.Ports[i] != want[i] {
			t.Errorf("ports[%d] = %+v, want %+v", i, svc.Spec.Ports[i], want[i])
		}
	}
}

// ServicePort is the first port: routing traits resolve their implicit backend from it.
func TestServiceConfig_ServicePortIsFirstPort(t *testing.T) {
	cfg := serviceConfig(t, "api", serviceFrontingOtherWorkload())
	pp, ok := cfg.(interface{ ServicePort() int32 })
	if !ok {
		t.Fatal("ServiceConfig does not implement ServicePort")
	}
	if got := pp.ServicePort(); got != 80 {
		t.Errorf("ServicePort() = %d, want 80 (the first port)", got)
	}
}

// ServicePortName is the first port's name, reported as known even when that port is unnamed:
// routing traits use it to refuse an implicit backend addressed by any other port name.
func TestServiceConfig_ServicePortNameIsFirstPortName(t *testing.T) {
	tests := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"named first port", serviceFrontingOtherWorkload(), "http"},
		{"unnamed single port", map[string]any{"ports": []any{map[string]any{"port": 8080}}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := serviceConfig(t, "api", tt.props)
			pn, ok := cfg.(interface{ ServicePortName() (string, bool) })
			if !ok {
				t.Fatal("ServiceConfig does not implement ServicePortName")
			}
			if got, known := pn.ServicePortName(); got != tt.want || !known {
				t.Errorf("ServicePortName() = %q, %v; want %q, true", got, known, tt.want)
			}
		})
	}
}

// The workload component owns the ServiceAccount; a service component names none.
func TestServiceConfig_NotServiceAccountNamer(t *testing.T) {
	cfg := serviceConfig(t, "api", serviceFrontingOtherWorkload())
	if _, ok := cfg.(oam.ServiceAccountNamer); ok {
		t.Error("ServiceConfig must not implement oam.ServiceAccountNamer: the workload owns the ServiceAccount")
	}
}

// Routing retargeting: the pods are the selector's, and each routed Service port maps to the
// target port the pods listen on. A UDP or unmatched port gets nothing — the synthesized
// NetworkPolicy rules are TCP.
func TestServiceConfig_ServiceRoutingTarget(t *testing.T) {
	cfg := serviceConfig(t, "api", serviceFrontingOtherWorkload())
	rt, ok := cfg.(serviceRoutingTargeter)
	if !ok {
		t.Fatal("ServiceConfig does not implement ServiceRoutingTarget")
	}
	tests := []struct {
		name string
		in   []intstr.IntOrString
		want []intstr.IntOrString
	}{
		{"numeric port maps to targetPort", []intstr.IntOrString{intstr.FromInt32(80)}, []intstr.IntOrString{intstr.FromInt32(8080)}},
		{"named port maps to targetPort", []intstr.IntOrString{intstr.FromString("http")}, []intstr.IntOrString{intstr.FromInt32(8080)}},
		{"both collapse to one", []intstr.IntOrString{intstr.FromInt32(80), intstr.FromString("http")}, []intstr.IntOrString{intstr.FromInt32(8080)}},
		{"UDP port dropped", []intstr.IntOrString{intstr.FromInt32(53)}, nil},
		{"unknown port dropped", []intstr.IntOrString{intstr.FromInt32(9999)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel, got := rt.ServiceRoutingTarget(tt.in)
			if sel == nil || len(sel.MatchLabels) != 1 || sel.MatchLabels["app"] != "api-server" {
				t.Errorf("selector = %v, want app=api-server", sel)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ports = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("ports[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Endpoints: the selector plus every TCP targetPort. The UDP port is not declared, since the
// endpoint-ingress NetworkPolicy it feeds is TCP-only.
func TestServiceHandler_Endpoints(t *testing.T) {
	h := &components.ServiceHandler{}
	props := serviceFrontingOtherWorkload()
	props["ports"] = append(props["ports"].([]any),
		// The Service port name and the container port name differ on purpose, so a declared
		// endpoint port taken from the wrong one of the two is caught.
		map[string]any{"name": "admin", "port": 9000, "targetPort": "mgmt"},
		map[string]any{"name": "alt", "port": 8081, "targetPort": 8080})
	eps, err := h.Endpoints(&oam.Component{Name: "api", Type: "service", Properties: props})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if sel := eps[0].PodSelector; sel == nil || len(sel.MatchLabels) != 1 || sel.MatchLabels["app"] != "api-server" {
		t.Errorf("selector = %v, want app=api-server", eps[0].PodSelector)
	}
	want := []intstr.IntOrString{intstr.FromInt32(8080), intstr.FromString("mgmt")}
	if len(eps[0].Ports) != len(want) {
		t.Fatalf("ports = %v, want %v", eps[0].Ports, want)
	}
	for i := range want {
		if eps[0].Ports[i] != want[i] {
			t.Errorf("ports[%d] = %v, want %v", i, eps[0].Ports[i], want[i])
		}
	}
}

func TestServiceHandler_Endpoints_UDPOnlyDeclaresNone(t *testing.T) {
	h := &components.ServiceHandler{}
	eps, err := h.Endpoints(&oam.Component{Name: "dns", Type: "service", Properties: map[string]any{
		"ports": []any{map[string]any{"port": 53, "protocol": "UDP"}},
	}})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 0 {
		t.Errorf("expected no endpoint for a UDP-only Service, got %+v", eps)
	}
}

// Endpoints is called with no schema validation first, so a malformed document is refused here
// rather than declared.
func TestServiceHandler_Endpoints_RejectsMalformed(t *testing.T) {
	h := &components.ServiceHandler{}
	if _, err := h.Endpoints(&oam.Component{Name: "api", Type: "service", Properties: map[string]any{
		"ports": []any{map[string]any{"port": "80"}},
	}}); err == nil {
		t.Fatal("expected an error for a wrongly typed port")
	}
}

func TestServiceHandler_PropertySchema(t *testing.T) {
	s := (&components.ServiceHandler{}).PropertySchema()
	ports, ok := s["ports"]
	// Not Required: a headless Service may have none, so the parser enforces "at least one"
	// for the other Services (TestServiceHandler_Rejects).
	if !ok || ports.Type != oam.PropertyTypeArray || ports.Required || ports.Items == nil {
		t.Fatalf("ports schema = %+v, want an optional array", ports)
	}
	if ip := s["clusterIP"]; ip.Type != oam.PropertyTypeString || ip.Required || ip.Default != nil || !slices.Equal(ip.Enum, []any{"None"}) {
		t.Errorf("clusterIP schema = %+v, want an optional string with enum [None] and no default", ip)
	}
	item := ports.Items.Properties
	// A missing entry reads as the zero PropertySchema (Type "", not Required), so each key's
	// presence is asserted before its fields.
	for _, key := range []string{"name", "port", "targetPort", "protocol"} {
		if _, ok := item[key]; !ok {
			t.Errorf("ports[] schema has no %q property", key)
		}
	}
	if !item["port"].Required {
		t.Error("ports[].port must be required")
	}
	// targetPort is int-or-name: the integer/string Types union, with Type left empty so a
	// consumer that does not read Types still accepts both forms (go-kure/launcher#383).
	if tp := item["targetPort"]; tp.Type != "" || !slices.Equal(tp.Types, []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString}) {
		t.Errorf("ports[].targetPort = Type %q, Types %v; want no Type and Types [integer string]", tp.Type, tp.Types)
	}
	if item["protocol"].Default != "TCP" {
		t.Errorf("ports[].protocol default = %v, want TCP", item["protocol"].Default)
	}
	if s["type"].Default != "ClusterIP" {
		t.Errorf("type default = %v, want ClusterIP", s["type"].Default)
	}
	if s["selector"].Type != oam.PropertyTypeObject || !s["selector"].AdditionalProperties {
		t.Errorf("selector schema = %+v, want an open object", s["selector"])
	}
}

// go-kure/launcher#690: clusterIP None emits a headless Service, with or without ports; without
// the key the Service carries no clusterIP, as before.
func TestServiceHandler_Headless(t *testing.T) {
	withPorts := generateService(t, "db", map[string]any{
		"clusterIP": "None",
		"ports":     []any{map[string]any{"name": "tcp", "port": 5432}},
	})
	if withPorts.Spec.ClusterIP != "None" || withPorts.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("clusterIP/type = %q/%q, want None/ClusterIP", withPorts.Spec.ClusterIP, withPorts.Spec.Type)
	}
	if len(withPorts.Spec.Ports) != 1 || withPorts.Spec.Ports[0].Port != 5432 {
		t.Errorf("ports = %+v, want the one authored port", withPorts.Spec.Ports)
	}

	for name, props := range map[string]map[string]any{
		"ports absent": {"clusterIP": "None"},
		"ports empty":  {"clusterIP": "None", "ports": []any{}},
		"ports null":   {"clusterIP": "None", "ports": nil},
	} {
		t.Run(name, func(t *testing.T) {
			svc := generateService(t, "db", props)
			if svc.Spec.ClusterIP != "None" || len(svc.Spec.Ports) != 0 {
				t.Errorf("clusterIP/ports = %q/%+v, want None and no ports", svc.Spec.ClusterIP, svc.Spec.Ports)
			}
			if got := svc.Spec.Selector; len(got) != 1 || got["app"] != "db" {
				t.Errorf("selector = %v, want the default app: db", got)
			}
			cfg := serviceConfig(t, "db", props)
			if pp, ok := cfg.(interface{ ServicePort() int32 }); !ok || pp.ServicePort() != 0 {
				t.Error("a port-less Service must report no service port, so routing traits refuse an implicit backend")
			}
			eps, err := (&components.ServiceHandler{}).Endpoints(&oam.Component{Name: "db", Type: "service", Properties: props})
			if err != nil || len(eps) != 0 {
				t.Errorf("Endpoints = %+v, %v; want none", eps, err)
			}
		})
	}

	plain := generateService(t, "db", map[string]any{"ports": []any{map[string]any{"port": 5432}}})
	if plain.Spec.ClusterIP != "" {
		t.Errorf("clusterIP without the key = %q, want unset", plain.Spec.ClusterIP)
	}
}
