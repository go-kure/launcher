package components_test

import (
	"reflect"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestIngressHandler_CanHandle(t *testing.T) {
	h := &components.IngressHandler{}
	if !h.CanHandle("ingress") {
		t.Error("CanHandle(ingress) = false")
	}
	if h.CanHandle("httproute") {
		t.Error("CanHandle(httproute) = true")
	}
}

// TestIngressHandler_EmitsIdentityOnly: no field of the spec is required by the
// decode, and none is filled. An Ingress with neither a default backend nor a
// rule is the API server's to refuse.
func TestIngressHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"ingressClassName": nil, "defaultBackend": nil, "tls": nil, "rules": nil},
	} {
		t.Run(name, func(t *testing.T) {
			ing := generateCoreKind(t, &components.IngressHandler{}, "ingress", "web", props).(*networkingv1.Ingress)
			if ing.APIVersion != "networking.k8s.io/v1" || ing.Kind != "Ingress" {
				t.Errorf("GVK = %s %s, want networking.k8s.io/v1 Ingress", ing.APIVersion, ing.Kind)
			}
			if ing.Namespace != coreKindNamespace {
				t.Errorf("namespace = %q, want the build namespace %q", ing.Namespace, coreKindNamespace)
			}
			if !reflect.DeepEqual(ing.Spec, networkingv1.IngressSpec{}) {
				t.Errorf("spec = %+v, want empty", ing.Spec)
			}
		})
	}
}

// TestIngressHandler_EmitsAuthoredSpec: the class, the default backend, the
// TLS entries and the rules as written. A backend is a Service reference, not a
// component: its name is carried, not resolved.
func TestIngressHandler_EmitsAuthoredSpec(t *testing.T) {
	ing := generateCoreKind(t, &components.IngressHandler{}, "ingress", "web", map[string]any{
		"ingressClassName": "nginx",
		"defaultBackend":   map[string]any{"service": map[string]any{"name": "fallback", "port": map[string]any{"number": 8080}}},
		"tls":              []any{map[string]any{"hosts": []any{"app.example.com"}, "secretName": "app-tls"}},
		"rules": []any{map[string]any{
			"host": "app.example.com",
			"http": map[string]any{"paths": []any{map[string]any{
				"path": "/api", "pathType": "Prefix",
				"backend": map[string]any{"service": map[string]any{"name": "api", "port": map[string]any{"name": "http"}}},
			}}},
		}},
	}).(*networkingv1.Ingress)

	class, prefix := "nginx", networkingv1.PathTypePrefix
	want := networkingv1.IngressSpec{
		IngressClassName: &class,
		DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
			Name: "fallback", Port: networkingv1.ServiceBackendPort{Number: 8080}}},
		TLS: []networkingv1.IngressTLS{{Hosts: []string{"app.example.com"}, SecretName: "app-tls"}},
		Rules: []networkingv1.IngressRule{{
			Host: "app.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/api", PathType: &prefix,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: "api", Port: networkingv1.ServiceBackendPort{Name: "http"}}},
				}},
			}},
		}},
	}
	if !reflect.DeepEqual(ing.Spec, want) {
		t.Errorf("spec = %+v, want %+v", ing.Spec, want)
	}
}

// TestIngressConfig_ReportsNoTraffic: the config is an authored object. It
// implements none of the methods the NetworkPolicy synthesis reads a routing
// trait's config through, so the synthesis allows nothing for it.
func TestIngressConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.IngressConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("IngressConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("IngressConfig names an owning component; it is a component's own config, not a trait's")
	}
}

func TestIngressHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a networking.k8s.io/v1 IngressSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":        {map[string]any{"host": "app.example.com"}, notASpec},
		"trait property":     {map[string]any{"hostname": "app.example.com", "paths": []any{}}, notASpec},
		"status":             {map[string]any{"status": map[string]any{}}, notASpec},
		"metadata":           {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, notASpec},
		"rules a map":        {map[string]any{"rules": map[string]any{"host": "a"}}, notASpec},
		"backend sub-key":    {map[string]any{"defaultBackend": map[string]any{"serviceName": "api"}}, notASpec},
		"port number text":   {map[string]any{"defaultBackend": map[string]any{"service": map[string]any{"name": "api", "port": map[string]any{"number": "http"}}}}, notASpec},
		"path sub-key":       {map[string]any{"rules": []any{map[string]any{"http": map[string]any{"paths": []any{map[string]any{"pathtype": "Prefix", "pathType": "Exact"}}}}}}, "sets the same field as"},
		"null rule":          {map[string]any{"rules": []any{nil}}, "rules[0]"},
		"null path":          {map[string]any{"rules": []any{map[string]any{"http": map[string]any{"paths": []any{nil}}}}}, "rules[0].http.paths[0]"},
		"null tls entry":     {map[string]any{"tls": []any{map[string]any{"secretName": "a"}, nil}}, "tls[1]"},
		"two spellings":      {map[string]any{"rules": []any{}, "Rules": []any{}}, "sets the same field as"},
		"class not a string": {map[string]any{"ingressClassName": 3}, notASpec},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.IngressHandler{}, "ingress", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestIngressHandler_TypedNilElement: a list element that is a typed nil (a
// document built in Go, not parsed from YAML) is refused as a null one is, not
// decoded to an empty rule.
func TestIngressHandler_TypedNilElement(t *testing.T) {
	err := coreKindErr(&components.IngressHandler{}, "ingress", "web", map[string]any{
		"rules": []any{map[string]any(nil)},
	})
	if err == nil || !strings.Contains(err.Error(), "rules[0]") {
		t.Fatalf("err = %v, want one naming rules[0]", err)
	}
}
