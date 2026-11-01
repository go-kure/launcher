package components_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// chartEndpointKind is one chart-release component type and how it is built: a valid
// component of the type without a declaration, and the provider that reports endpoints.
type chartEndpointKind struct {
	name     string
	typ      string
	provider oam.EndpointProvider
	// props is a fresh valid property map of the type, no `endpoints`.
	props func() map[string]any
	// invalid is a fresh property map the type's own parse refuses, and the text of the refusal.
	invalid func() map[string]any
}

func chartEndpointKinds() []chartEndpointKind {
	helmFlux := func() map[string]any {
		return map[string]any{"chart": "podinfo", "version": "6.0.0", "source": map[string]any{"url": "https://charts.example.com"}}
	}
	helmTemplate := func() map[string]any {
		p := helmFlux()
		p["delivery"] = "template"
		return p
	}
	return []chartEndpointKind{
		{"helm/flux", "helm", components.HelmRule{}, helmFlux, func() map[string]any { return map[string]any{"chart": "podinfo"} }},
		{"helm/template", "helm", components.HelmRule{}, helmTemplate, func() map[string]any { return map[string]any{"chart": "podinfo", "delivery": "template"} }},
		{"helmrelease", "helmrelease", &components.HelmReleaseHandler{}, func() map[string]any { return map[string]any{"chart": hrChart()} }, func() map[string]any { return map[string]any{"nonsense": true} }},
		{"helmtemplate", "helmtemplate", &components.HelmTemplateHandler{}, func() map[string]any {
			return map[string]any{"chart": "podinfo", "version": "6.0.0", "source": map[string]any{"url": "https://charts.example.com"}}
		}, func() map[string]any { return map[string]any{"chart": "podinfo"} }},
	}
}

func chartSelector(labels map[string]any) map[string]any {
	return map[string]any{"matchLabels": labels}
}

func chartEntry(name, protocol string, port any) map[string]any {
	e := map[string]any{"port": port, "selector": chartSelector(map[string]any{"app.kubernetes.io/name": "shop"})}
	if name != "" {
		e["name"] = name
	}
	if protocol != "" {
		e["protocol"] = protocol
	}
	return e
}

func chartComponent(k chartEndpointKind, props map[string]any) *oam.Component {
	return &oam.Component{Name: "shop", Type: k.typ, Properties: props}
}

// A declaration is reported as written: in order, with the name, the protocol (empty for an
// unset one), the port as a number or a name, and a matchLabels-only selector.
func TestChartEndpoints_Declared(t *testing.T) {
	for _, k := range chartEndpointKinds() {
		t.Run(k.name, func(t *testing.T) {
			props := k.props()
			props["endpoints"] = []any{
				chartEntry("http", "", 8080),
				chartEntry("dns", "UDP", "dns"),
				chartEntry("", "SCTP", int64(9000)),
				chartEntry("", "", 7000.0),
			}
			eps, err := k.provider.Endpoints(chartComponent(k, props))
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			want := []netpol.Endpoint{
				{Name: "http", Ports: []intstr.IntOrString{intstr.FromInt32(8080)}},
				{Name: "dns", Protocol: corev1.ProtocolUDP, Ports: []intstr.IntOrString{intstr.FromString("dns")}},
				{Protocol: corev1.ProtocolSCTP, Ports: []intstr.IntOrString{intstr.FromInt32(9000)}},
				{Ports: []intstr.IntOrString{intstr.FromInt32(7000)}},
			}
			if len(eps) != len(want) {
				t.Fatalf("got %d endpoints, want %d: %+v", len(eps), len(want), eps)
			}
			for i, ep := range eps {
				if ep.Name != want[i].Name || ep.Protocol != want[i].Protocol || !reflect.DeepEqual(ep.Ports, want[i].Ports) {
					t.Errorf("endpoint[%d] = %+v, want name %q protocol %q ports %v", i, ep, want[i].Name, want[i].Protocol, want[i].Ports)
				}
				sel := ep.PodSelector
				if sel == nil || len(sel.MatchExpressions) != 0 || len(sel.MatchLabels) != 1 || sel.MatchLabels["app.kubernetes.io/name"] != "shop" {
					t.Errorf("endpoint[%d] selector = %v, want app.kubernetes.io/name=shop only", i, sel)
				}
			}
		})
	}
}

// With no declaration, a null or an empty list the type reports no endpoint, as it did
// before the property existed.
func TestChartEndpoints_NoDeclarationNoEndpoint(t *testing.T) {
	for _, k := range chartEndpointKinds() {
		t.Run(k.name, func(t *testing.T) {
			for name, decl := range map[string]any{"absent": nil, "empty list": []any{}} {
				props := k.props()
				if name != "absent" {
					props["endpoints"] = decl
				}
				eps, err := k.provider.Endpoints(chartComponent(k, props))
				if err != nil || len(eps) != 0 {
					t.Errorf("%s: endpoints = %+v, err = %v; want none", name, eps, err)
				}
			}
			props := k.props()
			props["endpoints"] = nil
			eps, err := k.provider.Endpoints(chartComponent(k, props))
			if err != nil || len(eps) != 0 {
				t.Errorf("null: endpoints = %+v, err = %v; want none", eps, err)
			}
		})
	}
}

// Every malformed declaration is refused with the property path, through a direct Endpoints
// call and through the type's build or lowering.
func TestChartEndpoints_Refusals(t *testing.T) {
	good := func() map[string]any { return chartEntry("http", "", 8080) }
	without := func(key string) map[string]any {
		e := good()
		delete(e, key)
		return e
	}
	with := func(key string, value any) map[string]any {
		e := good()
		e[key] = value
		return e
	}
	for _, tc := range []struct {
		name string
		decl any
		want string
	}{
		{"not a list", map[string]any{"port": 80}, "endpoints: must be a list"},
		{"entry not an object", []any{"http"}, "endpoints[0]: must be an object"},
		{"unknown key", []any{with("targetPort", 80)}, `endpoints[0]: unknown property "targetPort"`},
		{"no selector", []any{without("selector")}, "endpoints[0].selector: is required"},
		{"selector with expressions", []any{with("selector", map[string]any{"matchLabels": map[string]any{"a": "b"}, "matchExpressions": []any{}})}, `endpoints[0].selector: unknown property "matchExpressions"`},
		{"selector without matchLabels", []any{with("selector", map[string]any{})}, "endpoints[0].selector.matchLabels: is required and not empty"},
		{"empty matchLabels", []any{with("selector", chartSelector(map[string]any{}))}, "endpoints[0].selector.matchLabels: is required and not empty"},
		{"non-string label value", []any{with("selector", chartSelector(map[string]any{"a": 1}))}, `endpoints[0].selector.matchLabels["a"]: must be a string`},
		{"bad label key", []any{with("selector", chartSelector(map[string]any{"-bad": "x"}))}, `endpoints[0].selector.matchLabels: key "-bad"`},
		{"bad label value", []any{with("selector", chartSelector(map[string]any{"a": "-x"}))}, `endpoints[0].selector.matchLabels["a"]: "-x"`},
		{"no port", []any{without("port")}, "endpoints[0].port: is required"},
		{"port 0", []any{with("port", 0)}, "endpoints[0].port: must be an integer between 1 and 65535"},
		{"port 65536", []any{with("port", 65536)}, "endpoints[0].port: must be an integer between 1 and 65535"},
		{"port a fraction", []any{with("port", 80.5)}, "endpoints[0].port: must be an integer"},
		{"port digits as a string", []any{with("port", "8080")}, `endpoints[0].port: "8080" is neither a port number nor a valid port name`},
		{"port name uppercase", []any{with("port", "Http")}, `endpoints[0].port: "Http"`},
		{"bad protocol", []any{with("protocol", "ICMP")}, `endpoints[0].protocol: must be TCP, UDP or SCTP, got "ICMP"`},
		{"lowercase protocol", []any{with("protocol", "tcp")}, `endpoints[0].protocol: must be TCP, UDP or SCTP, got "tcp"`},
		{"empty protocol", []any{with("protocol", "")}, `endpoints[0].protocol: must be TCP, UDP or SCTP, got ""`},
		{"name uppercase", []any{with("name", "Http")}, `endpoints[0].name: "Http" is not a valid port name`},
		{"name too long", []any{with("name", "abcdefghijklmnop")}, `endpoints[0].name: "abcdefghijklmnop" is not a valid port name`},
		{"repeated name", []any{chartEntry("http", "", 80), chartEntry("web", "", 81), chartEntry("http", "UDP", 82)}, `endpoints[2].name: repeats the name "http" of endpoints[0]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range chartEndpointKinds() {
				props := k.props()
				props["endpoints"] = tc.decl
				comp := chartComponent(k, props)
				if _, err := k.provider.Endpoints(comp); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s: Endpoints err = %v, want it to contain %q", k.name, err, tc.want)
				}
				var buildErr error
				switch h := k.provider.(type) {
				case components.HelmRule:
					_, buildErr = h.LowerComponent(comp, helmLowering("doc"))
				case oam.ComponentHandler:
					_, buildErr = h.ToApplicationConfig(comp, "demo")
				}
				if buildErr == nil || !strings.Contains(buildErr.Error(), tc.want) {
					t.Errorf("%s: build/lowering err = %v, want it to contain %q", k.name, buildErr, tc.want)
				}
			}
		})
	}
}

// An endpoint's NetworkPolicy is created in the application namespace, so a declaration beside
// an authored targetNamespace is refused, on a direct Endpoints call and on a build or
// lowering alike. Without a declaration the same targetNamespace is accepted, and an empty one
// reads as absent.
func TestChartEndpoints_RefusedBesideTargetNamespace(t *testing.T) {
	// A named string type decodes as the string it holds, so it is refused like one.
	type namespaceName string
	for _, k := range chartEndpointKinds() {
		if k.name != "helm/flux" && k.name != "helmrelease" {
			continue // the other types take no targetNamespace
		}
		t.Run(k.name, func(t *testing.T) {
			const want = `endpoints: cannot be declared beside targetNamespace "data"`
			// The decode matches keys whatever their case, so the guard must too.
			data := "data"
			for _, key := range []string{"targetNamespace", "TargetNamespace", "TARGETNAMESPACE", "named string", "pointer", "raw message"} {
				props := k.props()
				switch key {
				case "named string":
					props["targetNamespace"] = namespaceName("data")
				case "pointer":
					props["targetNamespace"] = &data
				case "raw message":
					props["targetNamespace"] = json.RawMessage(`"data"`)
				default:
					props[key] = "data"
				}
				props["endpoints"] = []any{chartEntry("http", "", 8080)}
				comp := chartComponent(k, props)
				if _, err := k.provider.Endpoints(comp); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("%s: Endpoints err = %v, want it to contain %q", key, err, want)
				}
				var buildErr error
				switch h := k.provider.(type) {
				case components.HelmRule:
					_, buildErr = h.LowerComponent(comp, helmLowering("doc"))
				case oam.ComponentHandler:
					_, buildErr = h.ToApplicationConfig(comp, "demo")
				}
				if buildErr == nil || !strings.Contains(buildErr.Error(), want) {
					t.Errorf("%s: build/lowering err = %v, want it to contain %q", key, buildErr, want)
				}
			}

			plain := k.props()
			plain["targetNamespace"] = "data"
			if eps, err := k.provider.Endpoints(chartComponent(k, plain)); err != nil || eps != nil {
				t.Errorf("no declaration: Endpoints = %+v, %v; want none and no error", eps, err)
			}
			empty := k.props()
			empty["targetNamespace"] = ""
			empty["endpoints"] = []any{chartEntry("http", "", 8080)}
			if eps, err := k.provider.Endpoints(chartComponent(k, empty)); err != nil || len(eps) != 1 {
				t.Errorf("empty targetNamespace: Endpoints = %+v, %v; want one endpoint", eps, err)
			}
		})
	}
}

// A declaration on a component the type itself refuses changes nothing: no endpoint, and the
// refusal is the type's own, word for word, as it is without the declaration.
func TestChartEndpoints_InvalidChartIsRefusedInTheParsesWords(t *testing.T) {
	for _, k := range chartEndpointKinds() {
		t.Run(k.name, func(t *testing.T) {
			bare := chartComponent(k, k.invalid())
			var bareErr error
			switch h := k.provider.(type) {
			case components.HelmRule:
				_, bareErr = h.LowerComponent(bare, helmLowering("doc"))
			case oam.ComponentHandler:
				_, bareErr = h.ToApplicationConfig(bare, "demo")
			}
			if bareErr == nil {
				t.Fatalf("the invalid fixture was accepted")
			}
			props := k.invalid()
			props["endpoints"] = []any{chartEntry("http", "", 8080)}
			eps, err := k.provider.Endpoints(chartComponent(k, props))
			if err == nil || eps != nil {
				t.Fatalf("Endpoints = %+v, %v; want no endpoint and a refusal", eps, err)
			}
			if err.Error() != bareErr.Error() {
				t.Errorf("Endpoints err = %v\nwant the parse's words: %v", err, bareErr)
			}
		})
	}
}

// helmrelease: `endpoints` never reaches the emitted HelmReleaseSpec.
func TestHelmRelease_EndpointsNeverReachTheSpec(t *testing.T) {
	plain := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "values": map[string]any{"a": 1}}), "")
	declared := hrGenerate(t, hrConfig(t, "web", map[string]any{
		"chart": hrChart(), "values": map[string]any{"a": 1},
		"endpoints": []any{chartEntry("http", "", 8080)},
	}), "")
	if !reflect.DeepEqual(plain.Spec, declared.Spec) || !reflect.DeepEqual(plain.ObjectMeta, declared.ObjectMeta) {
		t.Errorf("a declaration changed the HelmRelease:\nplain:    %+v\ndeclared: %+v", plain, declared)
	}
}

// helm: the declaration is split off and never forwarded, under either delivery: the
// lowered members are the same as without it.
func TestHelmRule_EndpointsNeverForwarded(t *testing.T) {
	for _, delivery := range []string{"", "template"} {
		t.Run("delivery="+delivery, func(t *testing.T) {
			build := func(withEndpoints bool) []byte {
				props := map[string]any{"chart": "podinfo", "version": "6.0.0", "source": map[string]any{"url": "https://charts.example.com"}, "values": map[string]any{"a": 1}}
				if delivery != "" {
					props["delivery"] = delivery
				}
				if withEndpoints {
					props["endpoints"] = []any{chartEntry("http", "", 8080), chartEntry("dns", "UDP", "dns")}
				}
				out, err := json.Marshal(lowerHelm(t, helmLowering("doc"), "shop", props))
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			if plain, declared := build(false), build(true); string(plain) != string(declared) {
				t.Errorf("a declaration changed the lowered output:\nplain:    %s\ndeclared: %s", plain, declared)
			}
		})
	}
}

// Asked through the transformer, which gives a handler the object name and the metadata the
// build gives it, and checks the endpoints it gets back.
func TestChartEndpoints_ThroughTheTransformer(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponentLowering(components.HelmRule{})
	tr.RegisterComponent("helmrelease", &components.HelmReleaseHandler{})
	tr.RegisterComponent("helmtemplate", &components.HelmTemplateHandler{})
	for _, k := range chartEndpointKinds() {
		t.Run(k.name, func(t *testing.T) {
			props := k.props()
			props["endpoints"] = []any{chartEntry("http", "", 8080), chartEntry("metrics", "", 9090)}
			if k.typ == "helmrelease" {
				props["objectName"] = "shop-primary"
			}
			eps, err := tr.ComponentEndpoints(chartComponent(k, props))
			if err != nil || len(eps) != 2 || eps[0].Name != "http" || eps[1].Name != "metrics" {
				t.Fatalf("endpoints = %+v, err = %v; want http and metrics", eps, err)
			}
			props["endpoints"] = []any{chartEntry("http", "", 8080), chartEntry("http", "", 8081)}
			if _, err := tr.ComponentEndpoints(chartComponent(k, props)); err == nil || !strings.Contains(err.Error(), "repeats the name") {
				t.Errorf("repeated name: err = %v, want a refusal", err)
			}
		})
	}
}
