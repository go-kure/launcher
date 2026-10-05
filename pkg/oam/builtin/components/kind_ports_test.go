package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// portKindHandler is a kind handler with its published schema.
type portKindHandler interface {
	oam.ComponentHandler
	PropertySchema() map[string]oam.PropertySchema
}

// portKind is one kind-named workload component that publishes the main
// container's `ports` beside deployment (go-kure/launcher#334).
type portKind struct {
	typ     string
	handler portKindHandler
	base    map[string]any
}

var portKinds = []portKind{
	{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "nginx:1.27"}},
	{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "nginx:1.27"}},
	{"job", &components.JobHandler{}, map[string]any{"image": "busybox:1.36"}},
	{"cronjob", &components.CronjobHandler{}, map[string]any{"image": "busybox:1.36", "schedule": "0 2 * * *"}},
}

// props is the kind's minimal properties plus extra.
func (k portKind) props(extra map[string]any) map[string]any {
	p := maps.Clone(k.base)
	maps.Copy(p, extra)
	return p
}

func (k portKind) convertErr(props map[string]any) error {
	_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.typ, Properties: props}, "default")
	return err
}

// generate builds the component and returns its pod spec and every Service it
// emitted.
func (k portKind) generate(t *testing.T, props map[string]any) (corev1.PodSpec, []*corev1.Service) {
	t.Helper()
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objs, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var spec *corev1.PodSpec
	var svcs []*corev1.Service
	for _, o := range objs {
		switch v := (*o).(type) {
		case *appsv1.DaemonSet:
			spec = &v.Spec.Template.Spec
		case *appsv1.StatefulSet:
			spec = &v.Spec.Template.Spec
		case *batchv1.Job:
			spec = &v.Spec.Template.Spec
		case *batchv1.CronJob:
			spec = &v.Spec.JobTemplate.Spec.Template.Spec
		case *corev1.Service:
			svcs = append(svcs, v)
		}
	}
	if spec == nil {
		t.Fatalf("%s generated no workload", k.typ)
	}
	return *spec, svcs
}

func extraPorts() []any {
	return []any{
		map[string]any{"name": "metrics", "containerPort": 9090},
		map[string]any{"containerPort": 53, "protocol": "UDP"},
	}
}

var wantExtraPorts = []corev1.ContainerPort{
	{Name: "metrics", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
	{ContainerPort: 53, Protocol: corev1.ProtocolUDP},
}

// TestKindPorts_Projected pins the emitted ContainerPort list: authored order
// and protocol defaulting to TCP, and no kind emits a Service
// (go-kure/launcher#690).
func TestKindPorts_Projected(t *testing.T) {
	for _, k := range portKinds {
		t.Run(k.typ, func(t *testing.T) {
			spec, svcs := k.generate(t, k.props(map[string]any{"ports": extraPorts()}))
			if got := spec.Containers[0].Ports; !reflect.DeepEqual(got, wantExtraPorts) {
				t.Errorf("main container ports = %#v, want %#v", got, wantExtraPorts)
			}
			for _, s := range svcs {
				t.Errorf("Service %s emitted; a kind component emits no Service", s.Name)
			}
		})
	}
}

// TestKindPorts_NoPortProperty pins `port`'s removal from daemonset and
// statefulset (go-kure/launcher#690), as deployment dropped it
// (go-kure/launcher#343): no kind publishes it, and an authored `port` is
// refused as an unknown property.
func TestKindPorts_NoPortProperty(t *testing.T) {
	for _, k := range portKinds {
		t.Run(k.typ, func(t *testing.T) {
			if _, ok := k.handler.PropertySchema()["port"]; ok {
				t.Fatalf("%s publishes `port`", k.typ)
			}
			tr := oam.NewTransformer(map[string]oam.ComponentHandler{k.typ: k.handler}, nil)
			app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
				{Name: "app", Type: k.typ, Properties: k.props(map[string]any{"port": 8080})},
			}}}
			if err := tr.ValidateAuthoredProperties(app); err == nil || !strings.Contains(err.Error(), `"port"`) {
				t.Errorf("error = %v, want an unknown-property refusal naming \"port\"", err)
			}
		})
	}
}

// TestKindPorts_NoneLeavesContainerAsBefore requires absent, empty and null
// `ports` to leave the main container portless.
func TestKindPorts_NoneLeavesContainerAsBefore(t *testing.T) {
	for _, k := range portKinds {
		for name, ports := range map[string]map[string]any{
			"absent": {},
			"empty":  {"ports": []any{}},
			"null":   {"ports": nil},
		} {
			t.Run(k.typ+"/"+name, func(t *testing.T) {
				spec, _ := k.generate(t, k.props(ports))
				if got := spec.Containers[0].Ports; got != nil {
					t.Errorf("main container ports = %#v, want none", got)
				}
			})
		}
	}
}

func TestKindPorts_Refused(t *testing.T) {
	for _, k := range portKinds {
		cases := []struct {
			name  string
			extra map[string]any
			want  string
		}{
			// One parse refusal per kind shows parseContainerPorts is wired;
			// deployment_ports_test.go covers its whole table.
			{"unknown key", map[string]any{"ports": []any{map[string]any{"containerPort": 80, "hostPort": 80}}}, `ports[0]: unrecognized key "hostPort"`},
			{"duplicate name", map[string]any{"ports": []any{
				map[string]any{"containerPort": 80, "name": "web"},
				map[string]any{"containerPort": 81, "name": "web"},
			}}, `ports[1].name: duplicate port name "web"`},
		}
		for _, tc := range cases {
			t.Run(k.typ+"/"+tc.name, func(t *testing.T) {
				err := k.convertErr(k.props(tc.extra))
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want one containing %q", err, tc.want)
				}
			})
		}
	}
}

// TestKindPorts_NamedProbePorts resolves a named probe or lifecycle port
// against the main container's `ports`, and refuses one when none are
// declared.
func TestKindPorts_NamedProbePorts(t *testing.T) {
	readiness := func(port any) map[string]any {
		return map[string]any{"readiness": map[string]any{"tcpSocket": map[string]any{"port": port}}}
	}
	preStop := func(port any) map[string]any {
		return map[string]any{"preStop": map[string]any{"httpGet": map[string]any{"path": "/quit", "port": port}}}
	}
	for _, k := range portKinds {
		t.Run(k.typ, func(t *testing.T) {
			spec, _ := k.generate(t, k.props(map[string]any{"ports": extraPorts(), "probes": readiness("metrics")}))
			if got := spec.Containers[0].ReadinessProbe.TCPSocket.Port.StrVal; got != "metrics" {
				t.Errorf("readiness port = %q, want metrics", got)
			}
			if err := k.convertErr(k.props(map[string]any{"ports": extraPorts(), "lifecycle": preStop("metrics")})); err != nil {
				t.Errorf("preStop on a declared port refused: %v", err)
			}
			refusals := []struct {
				name  string
				props map[string]any
				want  string
			}{
				{"probe names an undeclared port", k.props(map[string]any{"ports": extraPorts(), "probes": readiness("admin")}),
					`readiness probe: tcpSocket handler: named port "admin" does not match any port the main container declares (metrics)`},
				{"hook names an undeclared port", k.props(map[string]any{"ports": extraPorts(), "lifecycle": preStop("admin")}),
					`lifecycle.preStop: httpGet handler: named port "admin" does not match any port the main container declares (metrics)`},
				{"no ports at all", k.props(map[string]any{"probes": readiness("metrics")}),
					`named port "metrics" is not supported here: this component declares no container ports for the kubelet to resolve the name against — use a numeric port instead`},
				// The names `port` once gave daemonset ("http") and statefulset
				// ("tcp") are no longer declared implicitly.
				{"former port name", k.props(map[string]any{"probes": readiness("http")}),
					`named port "http" is not supported here`},
			}
			for _, tc := range refusals {
				t.Run(tc.name, func(t *testing.T) {
					err := k.convertErr(tc.props)
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("error = %v, want one containing %q", err, tc.want)
					}
				})
			}
		})
	}
}

// TestKindPorts_SidecarNameCollision refuses a sidecar port named like one of
// the main container's `ports` (checkPodPortNames sees the whole list), on the
// two of these kinds with sidecars: statefulset and, since
// go-kure/launcher#790, daemonset.
func TestKindPorts_SidecarNameCollision(t *testing.T) {
	want := `sidecars[0] "exporter": ports[0].name: port name "metrics" is also declared by the main container`
	for _, k := range portKinds[:2] {
		t.Run(k.typ, func(t *testing.T) {
			props := k.props(map[string]any{
				"ports": extraPorts(),
				"sidecars": []any{map[string]any{
					"name": "exporter", "image": "busybox:1.36",
					"ports": []any{map[string]any{"name": "metrics", "containerPort": 9100}},
				}},
			})
			if err := k.convertErr(props); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
	}
}

// TestKindPorts_SchemaAgreesWithParser runs each entry through the published
// schema and through the parser, and requires the same verdict from both.
func TestKindPorts_SchemaAgreesWithParser(t *testing.T) {
	for _, k := range portKinds {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{k.typ: k.handler}, nil)
		for name, entry := range map[string]map[string]any{
			"minimal":        {"containerPort": 8080},
			"full":           {"containerPort": 8080, "name": "web", "protocol": "SCTP"},
			"empty protocol": {"containerPort": 8080, "protocol": ""},
			"bad protocol":   {"containerPort": 8080, "protocol": "tcp"},
			"unknown key":    {"containerPort": 8080, "hostPort": 8080},
			"no port":        {"name": "web"},
		} {
			t.Run(k.typ+"/"+name, func(t *testing.T) {
				props := k.props(map[string]any{"ports": []any{entry}})
				app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
					{Name: "app", Type: k.typ, Properties: props},
				}}}
				schemaErr := tr.ValidateAuthoredProperties(app)
				parseErr := k.convertErr(props)
				if (schemaErr == nil) != (parseErr == nil) {
					t.Errorf("schema error = %v, parser error = %v; they must agree", schemaErr, parseErr)
				}
			})
		}
	}
}

// TestKindPorts_Schema pins the published `ports` shape: deployment's entry
// schema on every kind.
func TestKindPorts_Schema(t *testing.T) {
	want := (&components.DeploymentHandler{}).PropertySchema()["ports"].Items
	for _, k := range portKinds {
		t.Run(k.typ, func(t *testing.T) {
			s, ok := k.handler.PropertySchema()["ports"]
			if !ok {
				t.Fatalf("%s publishes no ports property", k.typ)
			}
			if s.Type != oam.PropertyTypeArray || s.Required || !reflect.DeepEqual(s.Items, want) {
				t.Errorf("ports schema = %+v, want an optional array of deployment's entries", s)
			}
		})
	}
}
