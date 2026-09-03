package traits_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// exposeGateway builds a gateway-path expose trait, mirroring exposeIngress
// (expose_shorthand_sslredirect_test.go) for the other controllerType. The
// capability-supplied fields are inline because applyExpose feeds LowerTrait
// directly, exactly as the pre-lowering-merge caller did.
func exposeGateway(props map[string]any) *oam.Trait {
	base := map[string]any{"controllerType": "gateway", "gatewayName": "public-gw"}
	for k, v := range props {
		base[k] = v
	}
	return &oam.Trait{Type: "expose", Properties: base}
}

// assertShorthandRoute checks the HTTPRoute a gateway expose produced from the
// hostnames shorthand alone: the hostname survives on the route (a native
// HTTPRoute field, unlike the ingress path where hostnames is consumed), and the
// single synthesized rule matches everything and backs onto the component's own
// service and port.
func assertShorthandRoute(t *testing.T, route *gatewayv1.HTTPRoute) {
	t.Helper()
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "shop.example.com" {
		t.Fatalf("hostnames = %v, want [shop.example.com]", route.Spec.Hostnames)
	}
	if len(route.Spec.Rules) != 1 {
		t.Fatalf("rules = %+v, want exactly one synthesized rule", route.Spec.Rules)
	}
	rule := route.Spec.Rules[0]
	if len(rule.Matches) != 0 {
		t.Errorf("matches = %+v, want none (a catch-all rule)", rule.Matches)
	}
	if len(rule.BackendRefs) != 1 {
		t.Fatalf("backendRefs = %+v, want the component's own service", rule.BackendRefs)
	}
	br := rule.BackendRefs[0]
	if string(br.Name) != "web" {
		t.Errorf("backendRef name = %q, want web", br.Name)
	}
	if br.Port == nil || *br.Port != 80 {
		t.Errorf("backendRef port = %v, want 80 (component service port)", br.Port)
	}
}

// The gateway analogue of TestExposeRule_Ingress_HostnamesShorthand: hostnames
// without rules must synthesize a rule, or the emitted httproute trait fails its
// own required-rules check.
func TestExposeRule_Gateway_HostnamesShorthand(t *testing.T) {
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeGateway(map[string]any{"hostnames": []any{"shop.example.com"}})
	if err := applyExpose(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertShorthandRoute(t, httprouteFromBundle(t, bundle))
}

// hostnames + rules together: the authored rules drive routing and are not
// replaced, matching TestExposeRule_Ingress_HostnamesAndRules. hostnames still
// survives here, since on this path it selects which requests the route serves
// rather than being folded into the rules.
func TestExposeRule_Gateway_HostnamesAndRules(t *testing.T) {
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeGateway(map[string]any{
		"hostnames": []any{"shop.example.com"},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{
				"path": map[string]any{"type": "PathPrefix", "value": "/api"},
			}},
		}},
	})
	if err := applyExpose(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	route := httprouteFromBundle(t, bundle)
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "shop.example.com" {
		t.Errorf("hostnames = %v, want [shop.example.com] preserved alongside authored rules", route.Spec.Hostnames)
	}
	if len(route.Spec.Rules) != 1 || len(route.Spec.Rules[0].Matches) != 1 {
		t.Fatalf("rules = %+v, want the single authored rule", route.Spec.Rules)
	}
	path := route.Spec.Rules[0].Matches[0].Path
	if path == nil || path.Value == nil || *path.Value != "/api" {
		t.Errorf("match path = %v, want the authored /api", path)
	}
}

// gatewayShorthandTransformer builds the engine as the real pipeline assembles it
// for this trait: the webservice component handler, the httproute trait handler the
// lowered expose trait dispatches to, and ExposeRule itself.
func gatewayShorthandTransformer() *oam.Transformer {
	tr := oam.NewTransformer(
		map[string]oam.ComponentHandler{"webservice": &components.WebserviceHandler{}},
		nil,
	)
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	tr.RegisterBuiltinTraitLowering(traits.ExposeRule{})
	return tr
}

// gatewayCapability is the platform's expose rendering: the gateway fields are
// platform-reserved, so a document may only author hostnames.
func gatewayCapability() map[string]oam.CapabilityBinding {
	return map[string]oam.CapabilityBinding{
		"expose": {Rendering: map[string]any{
			"controllerType": "gateway",
			"gatewayName":    "public-gw",
		}},
	}
}

// httprouteFromCluster finds the single HTTPRoute a transformed cluster generates.
func httprouteFromCluster(t *testing.T, cluster *stack.Cluster) *gatewayv1.HTTPRoute {
	t.Helper()
	var found *gatewayv1.HTTPRoute
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil || node.Bundle == nil {
			return
		}
		for _, app := range node.Bundle.Applications {
			objs, err := app.Generate()
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, o := range objs {
				if r, ok := (*o).(*gatewayv1.HTTPRoute); ok {
					found = r
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	if found == nil {
		t.Fatal("no HTTPRoute generated")
	}
	return found
}

// The authored-trait path end to end: unlike applyExpose, Transform also validates
// the emitted httproute trait against HTTPRouteHandler's property schema, where
// "rules" is required — so this is the shape a document actually fails in.
func TestExposeRule_Gateway_HostnamesShorthand_Transform(t *testing.T) {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 80},
				Traits: []oam.Trait{{
					Type:       "expose",
					Properties: map[string]any{"hostnames": []any{"shop.example.com"}},
				}},
			}},
		},
	}

	cluster, err := gatewayShorthandTransformer().Transform(app, oam.TransformContext{
		Namespace:    "default",
		Capabilities: gatewayCapability(),
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertShorthandRoute(t, httprouteFromCluster(t, cluster))
}

// --- the raw-document seam -------------------------------------------------

// shorthandRawDoc is a whole-noun higher-level kind whose spec carries a hostname
// with no ApplicationSpec home, so it is reachable only through LowerRaws.
type shorthandRawDoc struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   oam.Metadata `yaml:"metadata"`
	Spec       struct {
		Image    string `yaml:"image"`
		Hostname string `yaml:"hostname"`
	} `yaml:"spec"`
}

// shorthandRawRule is the launcher-side stand-in for an intent-tier lowering rule:
// it turns spec.hostname into a gateway expose trait. Because a raw rule's emitted
// traits are sealed — the engine skips its own capability merge for them — the rule
// must fold the capability rendering in itself, which is exactly why the shorthand
// has to work with the rendering already inline.
type shorthandRawRule struct{}

var errUnexpectedRawDoc = stderrors.New("shorthandRawRule: unexpected decode target")

func (shorthandRawRule) Kind() string { return "ShorthandApp" }

func (shorthandRawRule) DecodeDocument(raw []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var doc shorthandRawDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (shorthandRawRule) LowerDocument(doc any, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	d, ok := doc.(*shorthandRawDoc)
	if !ok {
		return oam.LoweringResult{}, errUnexpectedRawDoc
	}
	rendering := lctx.Capabilities["expose"].Rendering
	props := map[string]any{"hostnames": []any{d.Spec.Hostname}}
	for k, v := range rendering {
		props[k] = v
	}
	return oam.LoweringResult{Documents: []oam.Application{{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: d.Metadata.Name, Namespace: d.Metadata.Namespace},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": d.Spec.Image, "port": 80},
				Traits:     []oam.Trait{{Type: "expose", Properties: props}},
			}},
		},
	}}}, nil
}

// The raw seam end to end: lower the authored bytes, parse what comes back, and
// transform it. This is the path a consumer's intent-tier document takes, and it
// reaches ExposeRule with the rendering already sealed into the trait.
func TestExposeRule_Gateway_HostnamesShorthand_RawSeam(t *testing.T) {
	raw := []byte(`apiVersion: ` + oam.SupportedAPIVersion + `
kind: ShorthandApp
metadata:
  name: myapp
  namespace: default
spec:
  image: nginx:1.25
  hostname: shop.example.com
`)

	tr := gatewayShorthandTransformer()
	tr.RegisterRawDocumentLowering(shorthandRawRule{})

	ctx := oam.TransformContext{Namespace: "default", Capabilities: gatewayCapability()}
	lowered, err := tr.LowerRaws([]json.RawMessage{raw}, ctx)
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(lowered) != 1 {
		t.Fatalf("lowered %d documents, want 1", len(lowered))
	}

	app, err := oam.Parse(lowered[0])
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cluster, err := tr.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertShorthandRoute(t, httprouteFromCluster(t, cluster))
}
