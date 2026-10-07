package components_test

import (
	"reflect"
	"strings"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestGRPCRouteHandler_CanHandle(t *testing.T) {
	h := &components.GRPCRouteHandler{}
	if !h.CanHandle("grpcroute") {
		t.Error("CanHandle(grpcroute) = false")
	}
	if h.CanHandle("httproute") {
		t.Error("CanHandle(httproute) = true")
	}
}

// TestGRPCRouteHandler_EmitsIdentityOnly: no field of the spec is required by
// the decode, and none is filled: no parent is defaulted.
func TestGRPCRouteHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"parentRefs": nil, "useDefaultGateways": nil, "hostnames": nil, "rules": nil},
	} {
		t.Run(name, func(t *testing.T) {
			route := generateCoreKind(t, &components.GRPCRouteHandler{}, "grpcroute", "api", props).(*gatewayv1.GRPCRoute)
			if route.APIVersion != "gateway.networking.k8s.io/v1" || route.Kind != "GRPCRoute" {
				t.Errorf("GVK = %s %s, want gateway.networking.k8s.io/v1 GRPCRoute", route.APIVersion, route.Kind)
			}
			if route.Namespace != coreKindNamespace {
				t.Errorf("namespace = %q, want the build namespace %q", route.Namespace, coreKindNamespace)
			}
			if !reflect.DeepEqual(route.Spec, gatewayv1.GRPCRouteSpec{}) {
				t.Errorf("spec = %+v, want empty", route.Spec)
			}
		})
	}
}

// TestGRPCRouteHandler_EmitsAuthoredSpec: the parents, the hostnames and the
// rules as written. A parentRef or a backendRef in another namespace is
// carried as authored, not resolved and not refused.
func TestGRPCRouteHandler_EmitsAuthoredSpec(t *testing.T) {
	route := generateCoreKind(t, &components.GRPCRouteHandler{}, "grpcroute", "api", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "gateways", "sectionName": "grpc"}},
		"hostnames":  []any{"api.example.com"},
		"rules": []any{map[string]any{
			"matches":     []any{map[string]any{"method": map[string]any{"type": "Exact", "service": "shop.Orders", "method": "Get"}}},
			"backendRefs": []any{map[string]any{"name": "orders", "namespace": "backend", "port": 9090, "weight": 3}},
		}},
	}).(*gatewayv1.GRPCRoute)

	gateways, section := gatewayv1.Namespace("gateways"), gatewayv1.SectionName("grpc")
	exact, service, method := gatewayv1.GRPCMethodMatchExact, "shop.Orders", "Get"
	backend, port, weight := gatewayv1.Namespace("backend"), gatewayv1.PortNumber(9090), int32(3)
	want := gatewayv1.GRPCRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{
			Name: "public", Namespace: &gateways, SectionName: &section,
		}}},
		Hostnames: []gatewayv1.Hostname{"api.example.com"},
		Rules: []gatewayv1.GRPCRouteRule{{
			Matches: []gatewayv1.GRPCRouteMatch{{Method: &gatewayv1.GRPCMethodMatch{Type: &exact, Service: &service, Method: &method}}},
			BackendRefs: []gatewayv1.GRPCBackendRef{{BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{Name: "orders", Namespace: &backend, Port: &port},
				Weight:                 &weight,
			}}},
		}},
	}
	if !reflect.DeepEqual(route.Spec, want) {
		t.Errorf("spec = %+v, want %+v", route.Spec, want)
	}
}

// TestGRPCRouteConfig_ReportsNoTraffic: the config is an authored object. It
// implements none of the methods the NetworkPolicy synthesis reads a routing
// trait's config through, so the synthesis allows nothing for it.
func TestGRPCRouteConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.GRPCRouteConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("GRPCRouteConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("GRPCRouteConfig names an owning component; it is a component's own config, not a trait's")
	}
}

func TestGRPCRouteHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a gateway.networking.k8s.io/v1 GRPCRouteSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":      {map[string]any{"gateway": "public"}, notASpec},
		"status":           {map[string]any{"status": map[string]any{}}, notASpec},
		"metadata":         {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, notASpec},
		"hostnames a text": {map[string]any{"hostnames": "api.example.com"}, notASpec},
		"rule sub-key":     {map[string]any{"rules": []any{map[string]any{"backends": []any{}}}}, notASpec},
		"HTTP match":       {map[string]any{"rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"value": "/"}}}}}}, notASpec},
		"port a text":      {map[string]any{"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "orders", "port": "grpc"}}}}}, notASpec},
		"null rule":        {map[string]any{"rules": []any{nil}}, "rules[0]"},
		"null backendRef":  {map[string]any{"rules": []any{map[string]any{"backendRefs": []any{nil}}}}, "rules[0].backendRefs[0]"},
		"null parentRef":   {map[string]any{"parentRefs": []any{map[string]any{"name": "a"}, nil}}, "parentRefs[1]"},
		"two spellings":    {map[string]any{"hostnames": []any{}, "Hostnames": []any{}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.GRPCRouteHandler{}, "grpcroute", "api", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
