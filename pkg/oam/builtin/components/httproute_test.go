package components_test

import (
	"reflect"
	"strings"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestHTTPRouteHandler_CanHandle(t *testing.T) {
	h := &components.HTTPRouteHandler{}
	if !h.CanHandle("httproute") {
		t.Error("CanHandle(httproute) = false")
	}
	if h.CanHandle("ingress") {
		t.Error("CanHandle(ingress) = true")
	}
}

// TestHTTPRouteHandler_EmitsIdentityOnly: no field of the spec is required by
// the decode, and none is filled: no parent is defaulted.
func TestHTTPRouteHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"parentRefs": nil, "useDefaultGateways": nil, "hostnames": nil, "rules": nil},
	} {
		t.Run(name, func(t *testing.T) {
			route := generateCoreKind(t, &components.HTTPRouteHandler{}, "httproute", "web", props).(*gatewayv1.HTTPRoute)
			if route.APIVersion != "gateway.networking.k8s.io/v1" || route.Kind != "HTTPRoute" {
				t.Errorf("GVK = %s %s, want gateway.networking.k8s.io/v1 HTTPRoute", route.APIVersion, route.Kind)
			}
			if route.Namespace != coreKindNamespace {
				t.Errorf("namespace = %q, want the build namespace %q", route.Namespace, coreKindNamespace)
			}
			if !reflect.DeepEqual(route.Spec, gatewayv1.HTTPRouteSpec{}) {
				t.Errorf("spec = %+v, want empty", route.Spec)
			}
		})
	}
}

// TestHTTPRouteHandler_EmitsAuthoredSpec: the parents, the hostnames and the
// rules as written. A backendRef is a reference, not a component: its name
// and namespace are carried, not resolved.
func TestHTTPRouteHandler_EmitsAuthoredSpec(t *testing.T) {
	route := generateCoreKind(t, &components.HTTPRouteHandler{}, "httproute", "web", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "gateways", "sectionName": "https"}},
		"hostnames":  []any{"app.example.com", "*.example.org"},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/api"}}},
			"filters": []any{map[string]any{
				"type":            "RequestRedirect",
				"requestRedirect": map[string]any{"scheme": "https", "statusCode": 301},
			}},
			"backendRefs": []any{map[string]any{"name": "api", "namespace": "backend", "port": 8080, "weight": 3}},
			"timeouts":    map[string]any{"request": "10s"},
		}},
	}).(*gatewayv1.HTTPRoute)

	gateways, section := gatewayv1.Namespace("gateways"), gatewayv1.SectionName("https")
	prefix, value := gatewayv1.PathMatchPathPrefix, "/api"
	scheme, code := "https", 301
	backend, port, weight := gatewayv1.Namespace("backend"), gatewayv1.PortNumber(8080), int32(3)
	request := gatewayv1.Duration("10s")
	want := gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{
			Name: "public", Namespace: &gateways, SectionName: &section,
		}}},
		Hostnames: []gatewayv1.Hostname{"app.example.com", "*.example.org"},
		Rules: []gatewayv1.HTTPRouteRule{{
			Matches: []gatewayv1.HTTPRouteMatch{{Path: &gatewayv1.HTTPPathMatch{Type: &prefix, Value: &value}}},
			Filters: []gatewayv1.HTTPRouteFilter{{
				Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
				RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{Scheme: &scheme, StatusCode: &code},
			}},
			BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{Name: "api", Namespace: &backend, Port: &port},
				Weight:                 &weight,
			}}},
			Timeouts: &gatewayv1.HTTPRouteTimeouts{Request: &request},
		}},
	}
	if !reflect.DeepEqual(route.Spec, want) {
		t.Errorf("spec = %+v, want %+v", route.Spec, want)
	}
}

// TestHTTPRouteConfig_ReportsNoTraffic: the config is an authored object. It
// implements none of the methods the NetworkPolicy synthesis reads a routing
// trait's config through, so the synthesis allows nothing for it.
func TestHTTPRouteConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.HTTPRouteConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("HTTPRouteConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("HTTPRouteConfig names an owning component; it is a component's own config, not a trait's")
	}
}

func TestHTTPRouteHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a gateway.networking.k8s.io/v1 HTTPRouteSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":      {map[string]any{"gateway": "public"}, notASpec},
		"trait property":   {map[string]any{"hostname": "app.example.com"}, notASpec},
		"status":           {map[string]any{"status": map[string]any{}}, notASpec},
		"metadata":         {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, notASpec},
		"hostnames a text": {map[string]any{"hostnames": "app.example.com"}, notASpec},
		"rule sub-key":     {map[string]any{"rules": []any{map[string]any{"backends": []any{}}}}, notASpec},
		"filter sub-key":   {map[string]any{"rules": []any{map[string]any{"filters": []any{map[string]any{"type": "RequestRedirect", "redirect": map[string]any{}}}}}}, notASpec},
		"port a text":      {map[string]any{"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": "http"}}}}}, notASpec},
		"null rule":        {map[string]any{"rules": []any{nil}}, "rules[0]"},
		"null backendRef":  {map[string]any{"rules": []any{map[string]any{"backendRefs": []any{nil}}}}, "rules[0].backendRefs[0]"},
		"null parentRef":   {map[string]any{"parentRefs": []any{map[string]any{"name": "a"}, nil}}, "parentRefs[1]"},
		"two spellings":    {map[string]any{"hostnames": []any{}, "Hostnames": []any{}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.HTTPRouteHandler{}, "httproute", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestHTTPRouteHandler_TypedNilElement: a list element that is a typed nil is
// refused as a null one is, not decoded to an empty rule, which matches every
// request and has no backend.
func TestHTTPRouteHandler_TypedNilElement(t *testing.T) {
	err := coreKindErr(&components.HTTPRouteHandler{}, "httproute", "web", map[string]any{
		"rules": []any{map[string]any(nil)},
	})
	if err == nil || !strings.Contains(err.Error(), "rules[0]") {
		t.Fatalf("err = %v, want one naming rules[0]", err)
	}
}
