package traits_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// npPortRule emits a webservice carrying a networkpolicy trait with the given
// properties, so the trait reaches its handler through emission validation.
type npPortRule struct{ traitProps map[string]any }

func (npPortRule) ComponentType() string { return "np-port-app" }

func (r npPortRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{
		Name:       comp.Name,
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
		Traits:     []oam.Trait{{Type: "networkpolicy", Properties: r.traitProps}},
	}}}, nil
}

func npPortTraitProps(port any) map[string]any {
	return map[string]any{
		"ingress": []any{map[string]any{
			"from":  []any{map[string]any{"podSelector": map[string]any{}}},
			"ports": []any{map[string]any{"port": port}},
		}},
	}
}

func npPortTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil,
		map[string]oam.TraitHandler{"networkpolicy": &traits.NetworkPolicyHandler{}},
	)
	registerWebservice(tr)
	return tr
}

func npPortApp(comp oam.Component) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
}

func npPortAuthored(port any) ([]client.Object, error) {
	tr := npPortTransformer()
	app := npPortApp(oam.Component{
		Name:       "web",
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
		Traits:     []oam.Trait{{Type: "networkpolicy", Properties: npPortTraitProps(port)}},
	})
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		return nil, err
	}
	return npPortGenerate(tr, app)
}

func npPortEmitted(port any) ([]client.Object, error) {
	tr := npPortTransformer()
	tr.RegisterComponentLowering(npPortRule{traitProps: npPortTraitProps(port)})
	return npPortGenerate(tr, npPortApp(oam.Component{Name: "web", Type: "np-port-app", Properties: map[string]any{}}))
}

func npPortGenerate(tr *oam.Transformer, app *oam.Application) ([]client.Object, error) {
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		return nil, err
	}
	var out []client.Object
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					return err
				}
				for _, o := range objs {
					out = append(out, *o)
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(cluster.Node)
}

var npPortPaths = []struct {
	name string
	run  func(any) ([]client.Object, error)
}{
	{"authored", npPortAuthored},
	{"emitted", npPortEmitted},
}

// TestNetworkPolicyPort_IntegerAndNamedPortPassBothPaths: the networkpolicy
// `port` leaf is published as an integer/string union (go-kure/launcher#383), and
// both forms pass the authored and the emitted path and render into the
// NetworkPolicy unchanged.
func TestNetworkPolicyPort_IntegerAndNamedPortPassBothPaths(t *testing.T) {
	for _, p := range npPortPaths {
		for _, tc := range []struct {
			v    any
			want string
		}{{2, "2"}, {"http", "http"}} {
			t.Run(p.name+"/"+tc.want, func(t *testing.T) {
				objs, err := p.run(tc.v)
				if err != nil {
					t.Fatalf("%T(%v) rejected: %v", tc.v, tc.v, err)
				}
				var np *networkingv1.NetworkPolicy
				for _, o := range objs {
					if v, ok := o.(*networkingv1.NetworkPolicy); ok {
						np = v
					}
				}
				if np == nil || len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 {
					t.Fatalf("no NetworkPolicy with one ingress port generated: %+v", np)
				}
				if got := np.Spec.Ingress[0].Ports[0].Port.String(); got != tc.want {
					t.Errorf("rendered port %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// TestNetworkPolicyPort_EveryParserKindStillAccepted: publishing `port` as a union
// must not narrow what parseNPPort accepted while the leaf was untyped.
// npPortNumber classifies by reflect.Kind — every integer kind, uintptr and named
// types included, and a whole number of any float kind — and npStringValue takes a
// named string type, so each still passes both paths and renders unchanged.
func TestNetworkPolicyPort_EveryParserKindStillAccepted(t *testing.T) {
	type namedInt int32
	type namedUint uint16
	type namedPtr uintptr
	type namedFloat float64
	type namedString string
	for _, p := range npPortPaths {
		for _, tc := range []struct {
			v    any
			want string
		}{
			{int8(80), "80"}, {int16(80), "80"}, {int32(80), "80"}, {int64(80), "80"}, {namedInt(80), "80"},
			{uint(80), "80"}, {uint8(80), "80"}, {uint16(80), "80"}, {uint32(80), "80"}, {uint64(80), "80"},
			{uintptr(80), "80"}, {namedUint(80), "80"}, {namedPtr(80), "80"},
			{float32(80), "80"}, {float64(80), "80"}, {namedFloat(80), "80"},
			{namedString("http"), "http"},
		} {
			t.Run(fmt.Sprintf("%s/%T", p.name, tc.v), func(t *testing.T) {
				objs, err := p.run(tc.v)
				if err != nil {
					t.Fatalf("%T(%v) rejected: %v", tc.v, tc.v, err)
				}
				var np *networkingv1.NetworkPolicy
				for _, o := range objs {
					if v, ok := o.(*networkingv1.NetworkPolicy); ok {
						np = v
					}
				}
				if np == nil || len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 {
					t.Fatalf("no NetworkPolicy with one ingress port generated: %+v", np)
				}
				if got := np.Spec.Ingress[0].Ports[0].Port.String(); got != tc.want {
					t.Errorf("rendered port %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// TestNetworkPolicyPort_NonMemberRejectedAtSchemaLayer: a boolean port is refused
// by property validation on both paths, before parseNPPort sees it.
func TestNetworkPolicyPort_NonMemberRejectedAtSchemaLayer(t *testing.T) {
	for _, p := range npPortPaths {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.run(true)
			if err == nil {
				t.Fatal("a boolean port was accepted")
			}
			if !strings.Contains(err.Error(), "expected one of") {
				t.Errorf("error = %v, want the schema layer's union type error", err)
			}
		})
	}
}
