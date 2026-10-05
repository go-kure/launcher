package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// go-kure/launcher#790: the service kind reads the ServiceSpec fields beyond
// type, clusterIP, selector and ports, and the two remaining port fields.

// A LoadBalancer Service with every field that type accepts, written through
// to the emitted object exactly as authored.
func TestServiceHandler_Generate_LoadBalancerFields(t *testing.T) {
	svc := generateService(t, "api", map[string]any{
		"type": "LoadBalancer",
		"ports": []any{
			map[string]any{"name": "http", "port": 80, "targetPort": 8080, "nodePort": 30080, "appProtocol": "http"},
			map[string]any{"name": "grpc", "port": 9090, "appProtocol": "kubernetes.io/h2c"},
		},
		"externalTrafficPolicy":         "Local",
		"internalTrafficPolicy":         "Cluster",
		"trafficDistribution":           "PreferSameZone",
		"sessionAffinity":               "ClientIP",
		"sessionAffinityConfig":         map[string]any{"clientIP": map[string]any{"timeoutSeconds": 600}},
		"publishNotReadyAddresses":      true,
		"ipFamilies":                    []any{"IPv6", "IPv4"},
		"ipFamilyPolicy":                "PreferDualStack",
		"loadBalancerClass":             "example.com/internal",
		"loadBalancerSourceRanges":      []any{"10.0.0.0/8", "2001:db8::/32"},
		"loadBalancerIP":                "192.0.2.10",
		"allocateLoadBalancerNodePorts": false,
		"healthCheckNodePort":           32000,
	})
	want := corev1.ServiceSpec{
		Type:     corev1.ServiceTypeLoadBalancer,
		Selector: map[string]string{"app": "api"},
		Ports: []corev1.ServicePort{
			{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP, NodePort: 30080, AppProtocol: new("http")},
			{Name: "grpc", Port: 9090, TargetPort: intstr.FromInt32(9090), Protocol: corev1.ProtocolTCP, AppProtocol: new("kubernetes.io/h2c")},
		},
		ExternalTrafficPolicy:         corev1.ServiceExternalTrafficPolicyLocal,
		InternalTrafficPolicy:         new(corev1.ServiceInternalTrafficPolicyCluster),
		TrafficDistribution:           new("PreferSameZone"),
		SessionAffinity:               corev1.ServiceAffinityClientIP,
		SessionAffinityConfig:         &corev1.SessionAffinityConfig{ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(600))}},
		PublishNotReadyAddresses:      true,
		IPFamilies:                    []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol},
		IPFamilyPolicy:                new(corev1.IPFamilyPolicyPreferDualStack),
		LoadBalancerClass:             new("example.com/internal"),
		LoadBalancerSourceRanges:      []string{"10.0.0.0/8", "2001:db8::/32"},
		LoadBalancerIP:                "192.0.2.10",
		AllocateLoadBalancerNodePorts: new(false),
		HealthCheckNodePort:           32000,
	}
	if !reflect.DeepEqual(svc.Spec, want) {
		t.Errorf("spec = %+v\nwant %+v", svc.Spec, want)
	}
}

// Nothing is defaulted: a Service that authors none of the new keys carries
// exactly the four fields it carried before.
func TestServiceHandler_Generate_NewFieldsUnsetByDefault(t *testing.T) {
	svc := generateService(t, "api", map[string]any{"ports": []any{map[string]any{"port": 80}}})
	want := corev1.ServiceSpec{
		Type:     corev1.ServiceTypeClusterIP,
		Selector: map[string]string{"app": "api"},
		Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(80), Protocol: corev1.ProtocolTCP}},
	}
	if !reflect.DeepEqual(svc.Spec, want) {
		t.Errorf("spec = %+v\nwant %+v", svc.Spec, want)
	}
}

// The fields that need no particular type are accepted on a plain ClusterIP
// Service, a headless one included.
func TestServiceHandler_Generate_TypeIndependentFields(t *testing.T) {
	svc := generateService(t, "db", map[string]any{
		"clusterIP":                "None",
		"publishNotReadyAddresses": true,
		"internalTrafficPolicy":    "Local",
		"sessionAffinity":          "None",
		"ipFamilies":               []any{"IPv4"},
		"ipFamilyPolicy":           "SingleStack",
		"ports":                    []any{map[string]any{"port": 5432, "appProtocol": "postgresql"}},
	})
	if !svc.Spec.PublishNotReadyAddresses || svc.Spec.ClusterIP != "None" {
		t.Errorf("publishNotReadyAddresses/clusterIP = %v/%q, want true/None", svc.Spec.PublishNotReadyAddresses, svc.Spec.ClusterIP)
	}
	if p := svc.Spec.InternalTrafficPolicy; p == nil || *p != corev1.ServiceInternalTrafficPolicyLocal {
		t.Errorf("internalTrafficPolicy = %v, want Local", p)
	}
	if svc.Spec.SessionAffinity != corev1.ServiceAffinityNone {
		t.Errorf("sessionAffinity = %q, want None as authored", svc.Spec.SessionAffinity)
	}
	if p := svc.Spec.IPFamilyPolicy; p == nil || *p != corev1.IPFamilyPolicySingleStack {
		t.Errorf("ipFamilyPolicy = %v, want SingleStack", p)
	}
	if ap := svc.Spec.Ports[0].AppProtocol; ap == nil || *ap != "postgresql" {
		t.Errorf("appProtocol = %v, want postgresql", ap)
	}
}

// One render must not share a pointer or slice with the next.
func TestServiceHandler_Generate_NewFieldsNotAliased(t *testing.T) {
	cfg := serviceConfig(t, "api", map[string]any{
		"type":                     "LoadBalancer",
		"ports":                    []any{map[string]any{"port": 80, "appProtocol": "http"}},
		"loadBalancerSourceRanges": []any{"10.0.0.0/8"},
		"ipFamilies":               []any{"IPv4"},
	})
	app := stack.NewApplication("api", "default", cfg)
	render := func() *corev1.Service {
		t.Helper()
		objs, err := cfg.Generate(app)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return (*objs[0]).(*corev1.Service)
	}
	first := render()
	*first.Spec.Ports[0].AppProtocol = "changed"
	first.Spec.LoadBalancerSourceRanges[0] = "192.0.2.0/24"
	first.Spec.IPFamilies[0] = corev1.IPv6Protocol

	second := render()
	if got := *second.Spec.Ports[0].AppProtocol; got != "http" {
		t.Errorf("second render appProtocol = %q, want http", got)
	}
	if got := second.Spec.LoadBalancerSourceRanges[0]; got != "10.0.0.0/8" {
		t.Errorf("second render loadBalancerSourceRanges[0] = %q, want 10.0.0.0/8", got)
	}
	if got := second.Spec.IPFamilies[0]; got != corev1.IPv4Protocol {
		t.Errorf("second render ipFamilies[0] = %q, want IPv4", got)
	}
}

func TestServiceHandler_RejectsSpecFields(t *testing.T) {
	ports := []any{map[string]any{"port": 80}}
	with := func(serviceType string, extra map[string]any) map[string]any {
		props := map[string]any{"ports": ports}
		if serviceType != "" {
			props["type"] = serviceType
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		// Refused by name, with the reason.
		{"externalIPs", with("", map[string]any{"externalIPs": []any{"192.0.2.1"}}), "externalIPs: not supported"},
		{"clusterIPs", with("", map[string]any{"clusterIPs": []any{"10.0.0.1"}}), "clusterIPs: not supported"},

		{"externalName on ClusterIP", with("", map[string]any{"externalName": "db.example.com"}), "externalName: may only be set with type ExternalName, got ClusterIP"},
		{"externalName empty on ClusterIP", with("", map[string]any{"externalName": ""}), "externalName: may only be set with type ExternalName, got ClusterIP"},
		{"externalName wrong type", with("ExternalName", map[string]any{"externalName": 1}), "externalName: must be a string"},

		{"externalTrafficPolicy on ClusterIP", with("", map[string]any{"externalTrafficPolicy": "Local"}), "externalTrafficPolicy: may only be set with type NodePort or LoadBalancer, got ClusterIP"},
		{"externalTrafficPolicy invalid", with("NodePort", map[string]any{"externalTrafficPolicy": "Node"}), `externalTrafficPolicy: must be one of Cluster, Local, got "Node"`},
		{"externalTrafficPolicy empty", with("NodePort", map[string]any{"externalTrafficPolicy": ""}), `externalTrafficPolicy: must be one of Cluster, Local, got ""`},
		{"internalTrafficPolicy invalid", with("", map[string]any{"internalTrafficPolicy": "local"}), `internalTrafficPolicy: must be one of Cluster, Local, got "local"`},
		{"trafficDistribution invalid", with("", map[string]any{"trafficDistribution": "PreferLocal"}), `trafficDistribution: must be one of PreferClose, PreferSameZone, PreferSameNode, got "PreferLocal"`},
		{"trafficDistribution wrong type", with("", map[string]any{"trafficDistribution": true}), "trafficDistribution: must be a string"},

		{"sessionAffinity invalid", with("", map[string]any{"sessionAffinity": "Cookie"}), `sessionAffinity: must be one of None, ClientIP, got "Cookie"`},
		{"sessionAffinityConfig without ClientIP", with("", map[string]any{
			"sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeoutSeconds": 600}},
		}), "sessionAffinityConfig: requires sessionAffinity ClientIP"},
		{"sessionAffinityConfig with None", with("", map[string]any{
			"sessionAffinity":       "None",
			"sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeoutSeconds": 600}},
		}), "sessionAffinityConfig: requires sessionAffinity ClientIP"},
		{"sessionAffinityConfig empty", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{},
		}), "sessionAffinityConfig.clientIP: required"},
		{"sessionAffinityConfig unknown key", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{"cookie": map[string]any{}},
		}), `sessionAffinityConfig: unrecognized key "cookie"`},
		{"clientIP without timeout", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{"clientIP": map[string]any{}},
		}), "sessionAffinityConfig.clientIP.timeoutSeconds: required"},
		{"clientIP unknown key", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeout": 1}},
		}), `sessionAffinityConfig.clientIP: unrecognized key "timeout"`},
		{"timeout zero", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeoutSeconds": 0}},
		}), "sessionAffinityConfig.clientIP.timeoutSeconds: must be between 1 and 86400, got 0"},
		{"timeout above a day", with("", map[string]any{
			"sessionAffinity": "ClientIP", "sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeoutSeconds": 86401}},
		}), "sessionAffinityConfig.clientIP.timeoutSeconds: must be between 1 and 86400, got 86401"},

		{"publishNotReadyAddresses wrong type", with("", map[string]any{"publishNotReadyAddresses": "true"}), "publishNotReadyAddresses: must be a boolean"},

		{"ipFamilies invalid", with("", map[string]any{"ipFamilies": []any{"IPv5"}}), `ipFamilies[0]: must be one of IPv4, IPv6, got "IPv5"`},
		{"ipFamilies duplicate", with("", map[string]any{"ipFamilies": []any{"IPv4", "IPv4"}}), `ipFamilies[1]: duplicate family "IPv4"`},
		{"ipFamilies three", with("", map[string]any{"ipFamilies": []any{"IPv4", "IPv6", "IPv4"}}), "ipFamilies: at most 2 families, got 3"},
		{"ipFamilyPolicy invalid", with("", map[string]any{"ipFamilyPolicy": "DualStack"}), `ipFamilyPolicy: must be one of SingleStack, PreferDualStack, RequireDualStack, got "DualStack"`},
		{"SingleStack with two families", with("", map[string]any{
			"ipFamilies": []any{"IPv4", "IPv6"}, "ipFamilyPolicy": "SingleStack",
		}), "ipFamilyPolicy: must be PreferDualStack or RequireDualStack when two ipFamilies are set, got SingleStack"},

		{"loadBalancerClass on NodePort", with("NodePort", map[string]any{"loadBalancerClass": "example.com/internal"}), "loadBalancerClass: may only be set with type LoadBalancer, got NodePort"},
		{"loadBalancerClass invalid", with("LoadBalancer", map[string]any{"loadBalancerClass": "not a name"}), `loadBalancerClass: "not a name" is not a qualified name`},
		{"loadBalancerSourceRanges on ClusterIP", with("", map[string]any{"loadBalancerSourceRanges": []any{"10.0.0.0/8"}}), "loadBalancerSourceRanges: may only be set with type LoadBalancer, got ClusterIP"},
		{"loadBalancerSourceRanges not a CIDR", with("LoadBalancer", map[string]any{"loadBalancerSourceRanges": []any{"10.0.0.0/8", "10.0.0.1"}}), `loadBalancerSourceRanges[1]: "10.0.0.1" is not a valid CIDR`},
		// Narrower than the API, which trims the value before it checks it.
		{"loadBalancerSourceRanges padded", with("LoadBalancer", map[string]any{"loadBalancerSourceRanges": []any{" 10.0.0.0/8"}}), `loadBalancerSourceRanges[0]: " 10.0.0.0/8" is not a valid CIDR`},
		{"loadBalancerSourceRanges host bits set", with("LoadBalancer", map[string]any{"loadBalancerSourceRanges": []any{"10.1.2.3/8"}}), `loadBalancerSourceRanges[0]: "10.1.2.3/8" is not a valid CIDR`},
		{"loadBalancerIP on ClusterIP", with("", map[string]any{"loadBalancerIP": "192.0.2.10"}), "loadBalancerIP: may only be set with type LoadBalancer, got ClusterIP"},
		{"allocateLoadBalancerNodePorts on NodePort", with("NodePort", map[string]any{"allocateLoadBalancerNodePorts": false}), "allocateLoadBalancerNodePorts: may only be set with type LoadBalancer, got NodePort"},
		{"healthCheckNodePort without Local", with("LoadBalancer", map[string]any{"healthCheckNodePort": 32000}), "healthCheckNodePort: may only be set with type LoadBalancer and externalTrafficPolicy Local"},
		{"healthCheckNodePort on NodePort", with("NodePort", map[string]any{"externalTrafficPolicy": "Local", "healthCheckNodePort": 32000}), "healthCheckNodePort: may only be set with type LoadBalancer and externalTrafficPolicy Local"},
		{"healthCheckNodePort zero", with("LoadBalancer", map[string]any{"externalTrafficPolicy": "Local", "healthCheckNodePort": 0}), "healthCheckNodePort: "},

		{"nodePort on ClusterIP", map[string]any{"ports": []any{map[string]any{"port": 80, "nodePort": 30080}}}, "ports[0].nodePort: may only be set with type NodePort or LoadBalancer, got ClusterIP"},
		{"nodePort out of range", map[string]any{"type": "NodePort", "ports": []any{map[string]any{"port": 80, "nodePort": 70000}}}, "ports[0].nodePort: "},
		{"nodePort zero", map[string]any{"type": "NodePort", "ports": []any{map[string]any{"port": 80, "nodePort": 0}}}, "ports[0].nodePort: "},
		{"nodePort duplicate", map[string]any{"type": "NodePort", "ports": []any{
			map[string]any{"name": "a", "port": 80, "nodePort": 30080},
			map[string]any{"name": "b", "port": 81, "nodePort": 30080},
		}}, "ports[1].nodePort: duplicate node port 30080/TCP"},
		{"appProtocol invalid", map[string]any{"ports": []any{map[string]any{"port": 80, "appProtocol": "not a name"}}}, `ports[0].appProtocol: "not a name" is not a qualified name`},
		{"appProtocol empty", map[string]any{"ports": []any{map[string]any{"port": 80, "appProtocol": ""}}}, `ports[0].appProtocol: "" is not a qualified name`},
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

// The same node port on two protocols is legal, as the same port is.
func TestServiceHandler_SameNodePortDifferentProtocols(t *testing.T) {
	svc := generateService(t, "dns", map[string]any{"type": "NodePort", "ports": []any{
		map[string]any{"name": "dns-tcp", "port": 53, "nodePort": 30053},
		map[string]any{"name": "dns-udp", "port": 53, "protocol": "UDP", "nodePort": 30053},
	}})
	if len(svc.Spec.Ports) != 2 || svc.Spec.Ports[0].NodePort != 30053 || svc.Spec.Ports[1].NodePort != 30053 {
		t.Errorf("ports = %+v, want node port 30053 on both", svc.Spec.Ports)
	}
}

// type ExternalName: a DNS alias. No selector, no cluster IP, ports optional.
func TestServiceHandler_ExternalName(t *testing.T) {
	bare := generateService(t, "db", map[string]any{"type": "ExternalName", "externalName": "db.example.com"})
	want := corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "db.example.com"}
	if !reflect.DeepEqual(bare.Spec, want) {
		t.Errorf("spec = %+v\nwant %+v", bare.Spec, want)
	}
	if len(bare.Labels) != 1 || bare.Labels["app"] != "db" {
		t.Errorf("labels = %v, want app=db", bare.Labels)
	}

	// A trailing dot marks the name fully qualified and is kept as authored.
	// Ports are allowed: some consumers of an ExternalName Service read them.
	withPorts := generateService(t, "db", map[string]any{
		"type":                "ExternalName",
		"externalName":        "db.example.com.",
		"ports":               []any{map[string]any{"name": "pg", "port": 5432}},
		"sessionAffinity":     "None",
		"trafficDistribution": nil,
	})
	if withPorts.Spec.ExternalName != "db.example.com." {
		t.Errorf("externalName = %q, want the trailing dot kept", withPorts.Spec.ExternalName)
	}
	if withPorts.Spec.Selector != nil {
		t.Errorf("selector = %v, want none", withPorts.Spec.Selector)
	}
	if len(withPorts.Spec.Ports) != 1 || withPorts.Spec.Ports[0].Port != 5432 {
		t.Errorf("ports = %+v, want the one authored port", withPorts.Spec.Ports)
	}
}

func TestServiceHandler_ExternalName_Rejects(t *testing.T) {
	ext := func(extra map[string]any) map[string]any {
		props := map[string]any{"type": "ExternalName", "externalName": "db.example.com"}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"externalName missing", map[string]any{"type": "ExternalName"}, "externalName: required with type ExternalName"},
		{"externalName null", ext(map[string]any{"externalName": nil}), "externalName: required with type ExternalName"},
		{"externalName empty", ext(map[string]any{"externalName": ""}), "externalName: required with type ExternalName"},
		{"externalName only a dot", ext(map[string]any{"externalName": "."}), "externalName: required with type ExternalName"},
		{"externalName two trailing dots", ext(map[string]any{"externalName": "db.example.com.."}), `externalName: "db.example.com.." is not a valid DNS name`},
		{"externalName uppercase", ext(map[string]any{"externalName": "DB.example.com"}), `externalName: "DB.example.com" is not a valid DNS name`},
		{"externalName an URL", ext(map[string]any{"externalName": "https://db.example.com"}), "is not a valid DNS name"},
		// This parser's own: the API accepts and ignores a selector here.
		{"selector", ext(map[string]any{"selector": map[string]any{"app": "db"}}), "selector: may not be set with type ExternalName, which selects no pods"},
		{"selector empty", ext(map[string]any{"selector": map[string]any{}}), "selector: may not be set with type ExternalName"},
		{"headless", ext(map[string]any{"clusterIP": "None"}), `clusterIP: "None" requires type ClusterIP, got ExternalName`},
		{"ipFamilies", ext(map[string]any{"ipFamilies": []any{"IPv4"}}), "ipFamilies: may not be set with type ExternalName"},
		{"ipFamilyPolicy", ext(map[string]any{"ipFamilyPolicy": "SingleStack"}), "ipFamilyPolicy: may not be set with type ExternalName"},
		{"externalTrafficPolicy", ext(map[string]any{"externalTrafficPolicy": "Local"}), "externalTrafficPolicy: may only be set with type NodePort or LoadBalancer, got ExternalName"},
		{"loadBalancerIP", ext(map[string]any{"loadBalancerIP": "192.0.2.10"}), "loadBalancerIP: may only be set with type LoadBalancer, got ExternalName"},
		{"nodePort", ext(map[string]any{"ports": []any{map[string]any{"port": 80, "nodePort": 30080}}}), "ports[0].nodePort: may only be set with type NodePort or LoadBalancer, got ExternalName"},
	}
	h := &components.ServiceHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{Name: "db", Type: "service", Properties: tt.props}, "default")
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// An ExternalName Service selects no pods, so everything that would point
// traffic or a NetworkPolicy at pods answers "none", with or without ports:
// no service port and a known-empty port name (routing traits refuse an
// implicit backend and a trait-level servicePort), its own name as the backend
// Service name (a route naming it is not an external backend), a routing target
// that is the selector without labels and never a port (the marker "selects no
// pods", for which the synthesis writes no policy; a port beside it would fail
// the synthesis), no identity port mapping, no endpoint.
func TestServiceConfig_ExternalName_PointsAtNoPods(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no ports":   {"type": "ExternalName", "externalName": "db.example.com"},
		"with ports": {"type": "ExternalName", "externalName": "db.example.com", "ports": []any{map[string]any{"name": "pg", "port": 5432}}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := serviceConfig(t, "db", props)
			if got := cfg.(interface{ ServicePort() int32 }).ServicePort(); got != 0 {
				t.Errorf("ServicePort() = %d, want 0", got)
			}
			if got, known := cfg.(interface{ ServicePortName() (string, bool) }).ServicePortName(); got != "" || !known {
				t.Errorf("ServicePortName() = %q, %v; want \"\", true", got, known)
			}
			if got := cfg.(interface{ BackendServiceName() string }).BackendServiceName(); got != "db" {
				t.Errorf("BackendServiceName() = %q, want db", got)
			}
			for _, routed := range [][]intstr.IntOrString{nil, {intstr.FromInt32(5432)}, {intstr.FromString("pg")}} {
				sel, ports := cfg.(serviceRoutingTargeter).ServiceRoutingTarget(routed)
				if sel == nil || len(sel.MatchLabels) != 0 || len(sel.MatchExpressions) != 0 {
					t.Errorf("ServiceRoutingTarget(%v) selector = %v, want a non-nil one without labels", routed, sel)
				}
				if len(ports) != 0 {
					t.Errorf("ServiceRoutingTarget(%v) ports = %v, want none", routed, ports)
				}
			}
			if cfg.(interface{ IdentityTargetPorts() bool }).IdentityTargetPorts() {
				t.Error("IdentityTargetPorts() = true, want false")
			}
			eps, err := (&components.ServiceHandler{}).Endpoints(&oam.Component{Name: "db", Type: "service", Properties: props})
			if err != nil || len(eps) != 0 {
				t.Errorf("Endpoints = %+v, %v; want none", eps, err)
			}
		})
	}
}
