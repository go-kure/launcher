package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// Named scalar types a lowering rule writes in Go. Hand-written YAML cannot produce
// any of these; only a rule (or a library caller) can.
type (
	namedImage     string
	namedDNSPolicy string
	namedToggle    bool
	namedInt       int
	namedInt32     int32
	namedInt64     int64
	namedFloat64   float64
	namedArg       string
)

// namedScalarRule is a ComponentLoweringRule that emits one component of compType,
// with an httproute trait nested on it, carrying exactly the properties it was
// built with — the place each case puts a named Go scalar type.
type namedScalarRule struct {
	compType   string
	props      map[string]any
	routeProps map[string]any
}

func (namedScalarRule) ComponentType() string { return "named-scalar-app" }

func (r namedScalarRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{
		Name:       comp.Name,
		Type:       r.compType,
		Properties: r.props,
		Traits:     []oam.Trait{{Type: "httproute", Properties: r.routeProps}},
	}}}, nil
}

// transformNamedScalars runs the whole pipeline — lowering, emission validation, the
// real webservice and httproute handlers — and returns every generated object.
func transformNamedScalars(t *testing.T, rule namedScalarRule) []client.Object {
	t.Helper()
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"webservice": &components.WebserviceHandler{},
		"worker":     &components.WorkerHandler{},
	}, nil)
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	tr.RegisterComponentLowering(rule)
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "named-scalar-app", Properties: map[string]any{},
		}}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var out []client.Object
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, o := range objs {
					out = append(out, *o)
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	return out
}

func namedScalarDeployment(t *testing.T, objs []client.Object) *appsv1.Deployment {
	t.Helper()
	for _, o := range objs {
		if d, ok := o.(*appsv1.Deployment); ok {
			return d
		}
	}
	t.Fatal("no Deployment generated")
	return nil
}

func namedScalarRoute(t *testing.T, objs []client.Object) *gatewayv1.HTTPRoute {
	t.Helper()
	for _, o := range objs {
		if r, ok := o.(*gatewayv1.HTTPRoute); ok {
			return r
		}
	}
	t.Fatal("no HTTPRoute generated")
	return nil
}

// TestLowering_NamedScalarReachesTheHandler is go-kure/launcher#428. Emission
// validation matched a scalar by reflect.Kind and returned it unchanged, so a named
// Go scalar type a lowering rule wrote passed validation and then failed the
// handler's concrete type assertion — an error for a required string, and a silent
// default for an optional one. Every assertion here is on the rendered objects, after
// the real handlers read the value; validation's own return proves nothing.
func TestLowering_NamedScalarReachesTheHandler(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"image": "nginx:1.25", "port": 8080}
	}
	route := map[string]any{
		"parentRefs": []any{map[string]any{"name": "gw"}},
		"rules":      []any{map[string]any{}},
	}

	cases := []struct {
		name  string
		props map[string]any
		check func(t *testing.T, objs []client.Object)
	}{
		{
			name:  "named string image",
			props: map[string]any{"image": namedImage("ghcr.io/org/app:v2")},
			check: func(t *testing.T, objs []client.Object) {
				if got := namedScalarDeployment(t, objs).Spec.Template.Spec.Containers[0].Image; got != "ghcr.io/org/app:v2" {
					t.Errorf("image = %q, want ghcr.io/org/app:v2", got)
				}
			},
		},
		{
			name:  "named string under an Enum",
			props: map[string]any{"dnsPolicy": namedDNSPolicy("Default")},
			check: func(t *testing.T, objs []client.Object) {
				if got := namedScalarDeployment(t, objs).Spec.Template.Spec.DNSPolicy; got != corev1.DNSDefault {
					t.Errorf("dnsPolicy = %q, want Default", got)
				}
			},
		},
		{
			name: "named bool",
			// Constraints are rendered only above one replica.
			props: map[string]any{"topologySpread": namedToggle(false), "replicas": 3},
			check: func(t *testing.T, objs []client.Object) {
				if got := namedScalarDeployment(t, objs).Spec.Template.Spec.TopologySpreadConstraints; len(got) != 0 {
					t.Errorf("topologySpread: false rendered %d constraints, want none — the value was dropped", len(got))
				}
			},
		},
		{
			name:  "named string array elements",
			props: map[string]any{"command": []namedArg{"serve", "--fast"}},
			check: func(t *testing.T, objs []client.Object) {
				got := namedScalarDeployment(t, objs).Spec.Template.Spec.Containers[0].Command
				if len(got) != 2 || got[0] != "serve" || got[1] != "--fast" {
					t.Errorf("command = %v, want [serve --fast]", got)
				}
			},
		},
		{
			name:  "named int replicas",
			props: map[string]any{"replicas": namedInt(3)},
			check: replicasAre(3),
		},
		{
			name:  "named int32 replicas",
			props: map[string]any{"replicas": namedInt32(3)},
			check: replicasAre(3),
		},
		{
			name:  "named int64 replicas",
			props: map[string]any{"replicas": namedInt64(3)},
			check: replicasAre(3),
		},
		{
			name:  "named integral float64 replicas",
			props: map[string]any{"replicas": namedFloat64(3)},
			check: replicasAre(3),
		},
		{
			name:  "named int32 container port",
			props: map[string]any{"port": namedInt32(8080)},
			check: func(t *testing.T, objs []client.Object) {
				ports := namedScalarDeployment(t, objs).Spec.Template.Spec.Containers[0].Ports
				if len(ports) == 0 || ports[0].ContainerPort != 8080 {
					t.Errorf("port = %v, want containerPort 8080", ports)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := base()
			for k, v := range tc.props {
				props[k] = v
			}
			tc.check(t, transformNamedScalars(t, namedScalarRule{compType: "webservice", props: props, routeProps: route}))
		})
	}
}

// TestLowering_NamedScalarReachesHTTPRouteServicePort pins the reader the issue
// singles out: httproute reads servicePort through toIngressPort, which accepts
// float64 and int only. A named integer must reach it as int — normalizing to its
// underlying int32/int64 would leave the port unreadable — and a plain int must stay
// an int. The component is a worker, which exposes no Service port of its own, so
// the trait-level serviceName/servicePort pair is what names the backend.
func TestLowering_NamedScalarReachesHTTPRouteServicePort(t *testing.T) {
	for _, tc := range []struct {
		name string
		port any
	}{
		{"named int", namedInt(8080)},
		{"named int32", namedInt32(8080)},
		{"named int64", namedInt64(8080)},
		{"named float64", namedFloat64(8080)},
		{"plain int", 8080},
		{"plain float64", float64(8080)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := transformNamedScalars(t, namedScalarRule{
				compType: "worker",
				props:    map[string]any{"image": "nginx:1.25"},
				routeProps: map[string]any{
					"parentRefs":  []any{map[string]any{"name": "gw"}},
					"rules":       []any{map[string]any{}},
					"serviceName": "other-svc",
					"servicePort": tc.port,
				},
			})
			backendIs(t, objs, "other-svc", 8080)
		})
	}
}

func replicasAre(want int32) func(t *testing.T, objs []client.Object) {
	return func(t *testing.T, objs []client.Object) {
		t.Helper()
		got := namedScalarDeployment(t, objs).Spec.Replicas
		if got == nil || *got != want {
			t.Errorf("replicas = %v, want %d — the supplied value was dropped", got, want)
		}
	}
}

func backendIs(t *testing.T, objs []client.Object, name string, port gatewayv1.PortNumber) {
	t.Helper()
	route := namedScalarRoute(t, objs)
	if len(route.Spec.Rules) != 1 || len(route.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("rules = %+v, want one rule with one backendRef", route.Spec.Rules)
	}
	ref := route.Spec.Rules[0].BackendRefs[0]
	if string(ref.Name) != name || ref.Port == nil || *ref.Port != port {
		t.Errorf("backendRef = %s:%v, want %s:%d", ref.Name, ref.Port, name, port)
	}
}
