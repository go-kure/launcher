package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func deploymentConvertErr(props map[string]any) error {
	_, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{
		Name: "web", Type: "deployment", Properties: props,
	}, "default")
	return err
}

func portProps(ports ...any) map[string]any {
	return map[string]any{"image": "nginx:1.27", "ports": ports}
}

// TestDeploymentHandler_PortsProjected pins the emitted ContainerPort list:
// authored order, protocol defaulting to TCP, and no Service.
func TestDeploymentHandler_PortsProjected(t *testing.T) {
	dep, objs := generateDeployment(t, "web", portProps(
		map[string]any{"name": "http", "containerPort": 8080},
		map[string]any{"containerPort": 9090, "protocol": "UDP"},
	))
	want := []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
		{ContainerPort: 9090, Protocol: corev1.ProtocolUDP},
	}
	if got := dep.Spec.Template.Spec.Containers[0].Ports; !reflect.DeepEqual(got, want) {
		t.Errorf("main container ports = %#v, want %#v", got, want)
	}
	for _, o := range objs {
		if _, ok := (*o).(*corev1.Service); ok {
			t.Error("deployment emitted a Service; ports declare container ports only")
		}
	}
}

// TestDeploymentHandler_NoPortsDeclaresNone keeps the portless main container:
// absent and empty `ports` both declare nothing.
func TestDeploymentHandler_NoPortsDeclaresNone(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"absent": {"image": "nginx:1.27"},
		"empty":  portProps(),
		"null":   {"image": "nginx:1.27", "ports": nil},
	} {
		t.Run(name, func(t *testing.T) {
			dep, _ := generateDeployment(t, "web", props)
			if got := dep.Spec.Template.Spec.Containers[0].Ports; got != nil {
				t.Errorf("main container ports = %#v, want none", got)
			}
		})
	}
}

// TestDeploymentHandler_PortMatchesWebservice pins that a deployment declaring
// webservice's port gets webservice's exact main container port, which the
// webservice lowering onto deployment relies on for identical output.
func TestDeploymentHandler_PortMatchesWebservice(t *testing.T) {
	dep, _ := generateDeployment(t, "web", portProps(map[string]any{"name": "http", "containerPort": 8080}))

	cfg, err := webserviceViaRule{}.ToApplicationConfig(&oam.Component{
		Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.27", "port": 8080},
	}, "default")
	if err != nil {
		t.Fatalf("webservice ToApplicationConfig: %v", err)
	}
	objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err != nil {
		t.Fatalf("webservice Generate: %v", err)
	}
	var ws *appsv1.Deployment
	for _, o := range objs {
		if d, ok := (*o).(*appsv1.Deployment); ok {
			ws = d
		}
	}
	if ws == nil {
		t.Fatal("webservice generated no Deployment")
	}
	got, want := dep.Spec.Template.Spec.Containers[0].Ports, ws.Spec.Template.Spec.Containers[0].Ports
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deployment ports = %#v, webservice ports = %#v", got, want)
	}
}

func TestDeploymentHandler_PortsRefused(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"not a list", map[string]any{"image": "nginx:1.27", "ports": "http"}, "ports: must be an array"},
		{"entry not an object", portProps(8080), "ports[0]: must be an object"},
		{"null entry", portProps(nil), "ports[0]: must be an object"},
		{"unknown key", portProps(map[string]any{"containerPort": 80, "hostPort": 80}), `ports[0]: unrecognized key "hostPort"`},
		{"missing containerPort", portProps(map[string]any{"name": "http"}), "ports[0].containerPort: required"},
		{"containerPort zero", portProps(map[string]any{"containerPort": 0}), "ports[0].containerPort"},
		{"containerPort too large", portProps(map[string]any{"containerPort": 65536}), "ports[0].containerPort"},
		{"containerPort as string", portProps(map[string]any{"containerPort": "80"}), "ports[0].containerPort"},
		{"invalid name", portProps(map[string]any{"containerPort": 80, "name": "HTTP"}), `ports[0].name: invalid port name "HTTP"`},
		{"name too long", portProps(map[string]any{"containerPort": 80, "name": "a-very-long-port-name"}), "ports[0].name: invalid port name"},
		{"duplicate name", portProps(
			map[string]any{"containerPort": 80, "name": "http"},
			map[string]any{"containerPort": 81, "name": "http"},
		), `ports[1].name: duplicate port name "http"`},
		{"invalid protocol", portProps(map[string]any{"containerPort": 80, "protocol": "tcp"}), `ports[0].protocol: must be one of TCP, UDP, SCTP, got "tcp"`},
		// The schema's enum refuses "", so the parser must too.
		{"empty protocol", portProps(map[string]any{"containerPort": 80, "protocol": ""}), `ports[0].protocol: must be one of TCP, UDP, SCTP, got ""`},
		{"duplicate port and protocol", portProps(
			map[string]any{"containerPort": 80},
			map[string]any{"containerPort": 80, "protocol": "TCP", "name": "web"},
		), "ports[1]: duplicate port 80/TCP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := deploymentConvertErr(tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestDeploymentHandler_SamePortOtherProtocol accepts one port number on two
// protocols, which is two distinct ports.
func TestDeploymentHandler_SamePortOtherProtocol(t *testing.T) {
	if err := deploymentConvertErr(portProps(
		map[string]any{"containerPort": 53, "name": "dns-tcp"},
		map[string]any{"containerPort": 53, "name": "dns-udp", "protocol": "UDP"},
	)); err != nil {
		t.Fatalf("53/TCP and 53/UDP refused: %v", err)
	}
}

// TestDeploymentHandler_NamedProbePorts resolves a named probe or lifecycle
// port against the main container's declared names.
func TestDeploymentHandler_NamedProbePorts(t *testing.T) {
	readiness := func(port any) map[string]any {
		return map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": port}}}
	}
	preStop := func(port any) map[string]any {
		return map[string]any{"preStop": map[string]any{"httpGet": map[string]any{"path": "/quit", "port": port}}}
	}
	declared := []any{
		map[string]any{"containerPort": 8080, "name": "http"},
		map[string]any{"containerPort": 9090, "name": "metrics"},
	}
	with := func(key string, v map[string]any, ports []any) map[string]any {
		p := map[string]any{"image": "nginx:1.27", key: v}
		if ports != nil {
			p["ports"] = ports
		}
		return p
	}

	t.Run("declared names resolve", func(t *testing.T) {
		dep, _ := generateDeployment(t, "web", with("probes", readiness("metrics"), declared))
		if got := dep.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Port.StrVal; got != "metrics" {
			t.Errorf("readiness port = %q, want metrics", got)
		}
		if err := deploymentConvertErr(with("lifecycle", preStop("http"), declared)); err != nil {
			t.Errorf("preStop on declared port refused: %v", err)
		}
	})

	refusals := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"probe names an undeclared port", with("probes", readiness("admin"), declared),
			`readiness probe: httpGet handler: named port "admin" does not match any port the main container declares (http, metrics)`},
		{"hook names an undeclared port", with("lifecycle", preStop("admin"), declared),
			`lifecycle.preStop: httpGet handler: named port "admin" does not match any port the main container declares (http, metrics)`},
		{"ports declared but none named", with("probes", readiness("http"), []any{map[string]any{"containerPort": 8080}}),
			`named port "http" is not supported here: the main container declares no named ports`},
		// Without `ports`, the refusal every portless kind gives, unchanged.
		{"no ports at all", with("probes", readiness("http"), nil),
			`named port "http" is not supported here: this component declares no container ports for the kubelet to resolve the name against — use a numeric port instead`},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			err := deploymentConvertErr(tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestDeploymentHandler_PortsSchemaAgreesWithParser runs each entry through
// the published schema (as kurel build and a lowering rule's emission are
// checked) and through the parser, and requires the same verdict from both.
func TestDeploymentHandler_PortsSchemaAgreesWithParser(t *testing.T) {
	h := &components.DeploymentHandler{}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": h}, nil)
	for name, entry := range map[string]map[string]any{
		"minimal":        {"containerPort": 8080},
		"full":           {"containerPort": 8080, "name": "http", "protocol": "SCTP"},
		"empty protocol": {"containerPort": 8080, "protocol": ""},
		"bad protocol":   {"containerPort": 8080, "protocol": "tcp"},
		"unknown key":    {"containerPort": 8080, "hostPort": 8080},
		"no port":        {"name": "http"},
	} {
		t.Run(name, func(t *testing.T) {
			props := portProps(entry)
			app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
				{Name: "web", Type: "deployment", Properties: props},
			}}}
			schemaErr := tr.ValidateAuthoredProperties(app)
			parseErr := deploymentConvertErr(props)
			if (schemaErr == nil) != (parseErr == nil) {
				t.Errorf("schema error = %v, parser error = %v; they must agree", schemaErr, parseErr)
			}
		})
	}
}

// TestDeploymentHandler_PortsSchema pins the published `ports` shape.
func TestDeploymentHandler_PortsSchema(t *testing.T) {
	s, ok := (&components.DeploymentHandler{}).PropertySchema()["ports"]
	if !ok {
		t.Fatal("deployment publishes no ports property")
	}
	if s.Type != oam.PropertyTypeArray || s.Items == nil || s.Items.Type != oam.PropertyTypeObject {
		t.Fatalf("ports schema = %+v, want an array of objects", s)
	}
	if s.Items.AdditionalProperties {
		t.Error("ports entries admit additional properties; the parser refuses unknown keys")
	}
	props := s.Items.Properties
	if len(props) != 3 || !props["containerPort"].Required || props["name"].Required || props["protocol"].Default != "TCP" {
		t.Errorf("ports entry schema = %+v", props)
	}
	if got := props["protocol"].Enum; !reflect.DeepEqual(got, []any{"TCP", "UDP", "SCTP"}) {
		t.Errorf("protocol enum = %v", got)
	}
}
