package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// siblingStub is a component config answering one forwarded value contract
// (ServicePort) and taking a Flux namespace, so a test can see which member the
// group's config consulted.
type siblingStub struct {
	port   int32
	fluxNS string
}

func (s *siblingStub) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *siblingStub) ServicePort() int32                                    { return s.port }
func (s *siblingStub) SetFluxNamespace(ns string)                            { s.fluxNS = ns }

type siblingAugmenterStub struct{ siblingStub }

func (*siblingAugmenterStub) AugmentLayout(*layout.ManifestLayout) error { return nil }

// siblingStubHandler builds a fresh config per component and keeps every one it built.
type siblingStubHandler struct {
	typ   string
	build func() stack.ApplicationConfig
	made  []stack.ApplicationConfig
}

func (h *siblingStubHandler) CanHandle(t string) bool { return t == h.typ }
func (h *siblingStubHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	cfg := h.build()
	h.made = append(h.made, cfg)
	return cfg, nil
}

// emitRule is a ComponentLoweringRule whose output is fn's.
type emitRule struct {
	typ string
	fn  func(*Component) []Component
}

func (r emitRule) ComponentType() string { return r.typ }
func (r emitRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: r.fn(c)}, nil
}

func stubHandler(typ string, port int32) *siblingStubHandler {
	return &siblingStubHandler{typ: typ, build: func() stack.ApplicationConfig { return &siblingStub{port: port} }}
}

func siblingTransformer(handlers ...*siblingStubHandler) *Transformer {
	m := make(map[string]ComponentHandler, len(handlers))
	for _, h := range handlers {
		m[h.typ] = h
	}
	return NewTransformer(m, nil)
}

func siblingDoc(components ...Component) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "app"},
		Spec:       ApplicationSpec{Components: components},
	}
}

func pair(types ...string) func(*Component) []Component {
	return func(c *Component) []Component {
		out := make([]Component, 0, len(types))
		for _, typ := range types {
			out = append(out, Component{Name: c.Name, Type: typ, Properties: map[string]any{}})
		}
		return out
	}
}

// TestSiblingGroup_AuthoredDuplicateStillRefused: no author can write a group;
// two authored components sharing a name stay a duplicate, whatever their types.
func TestSiblingGroup_AuthoredDuplicateStillRefused(t *testing.T) {
	doc := siblingDoc(
		Component{Name: "web", Type: "a", Properties: map[string]any{}},
		Component{Name: "web", Type: "b", Properties: map[string]any{}},
	)
	err := validateWithExtraTypes(doc, nil, map[string]bool{"a": true, "b": true}, LowerableTypes{})
	if err == nil || !strings.Contains(err.Error(), `duplicate component name "web"`) {
		t.Fatalf("authored duplicate: err = %v, want a duplicate component name refusal", err)
	}
}

// TestSiblingGroup_Refusals covers every shape a group may not take.
func TestSiblingGroup_Refusals(t *testing.T) {
	infra := map[string]string{TierAnnotationKey(DefaultDomain): string(TierInfra)}
	cases := []struct {
		name     string
		handlers []*siblingStubHandler
		rules    []emitRule
		doc      *Application
		want     string
	}{{
		name:     "two members of one type",
		handlers: []*siblingStubHandler{stubHandler("a", 0)},
		rules:    []emitRule{{"pair", pair("a", "a")}},
		doc:      siblingDoc(Component{Name: "web", Type: "pair"}),
		want:     `sibling group "web": more than one member of type "a"`,
	}, {
		name:     "a member a later round would lower again",
		handlers: []*siblingStubHandler{stubHandler("a", 0)},
		rules:    []emitRule{{"pair", pair("a", "inner")}, {"inner", pair("a")}},
		doc:      siblingDoc(Component{Name: "web", Type: "pair"}),
		want:     `member type "inner" has no component handler`,
	}, {
		name:     "one name from two invocations",
		handlers: []*siblingStubHandler{stubHandler("a", 0)},
		rules: []emitRule{{"one", func(*Component) []Component {
			return []Component{{Name: "web", Type: "a", Properties: map[string]any{}}}
		}}},
		doc:  siblingDoc(Component{Name: "x", Type: "one"}, Component{Name: "y", Type: "one"}),
		want: `duplicate component name "web"`,
	}, {
		name:     "a group sharing its name with another component",
		handlers: []*siblingStubHandler{stubHandler("a", 0), stubHandler("b", 0), stubHandler("c", 0)},
		rules:    []emitRule{{"pair", pair("a", "b")}},
		doc:      siblingDoc(Component{Name: "web", Type: "pair"}, Component{Name: "web", Type: "c", Properties: map[string]any{}}),
		want:     `duplicate component name "web"`,
	}, {
		name:     "members in different tiers",
		handlers: []*siblingStubHandler{stubHandler("a", 0), stubHandler("b", 0)},
		rules: []emitRule{{"pair", func(c *Component) []Component {
			out := pair("a", "b")(c)
			out[1].Annotations = infra
			return out
		}}},
		doc:  siblingDoc(Component{Name: "web", Type: "pair"}),
		want: `a group deploys as one unit and needs one tier`,
	}, {
		name:     "two members answering one contract",
		handlers: []*siblingStubHandler{stubHandler("a", 80), stubHandler("b", 8080)},
		rules:    []emitRule{{"pair", pair("a", "b")}},
		doc:      siblingDoc(Component{Name: "web", Type: "pair"}),
		want:     `members a and b both answer ServicePort`,
	}, {
		name: "a layout augmenter member",
		handlers: []*siblingStubHandler{stubHandler("a", 0), {typ: "aug", build: func() stack.ApplicationConfig {
			return &siblingAugmenterStub{}
		}}},
		rules: []emitRule{{"pair", pair("a", "aug")}},
		doc:   siblingDoc(Component{Name: "web", Type: "pair"}),
		want:  `member "aug" needs layout-level resources`,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := siblingTransformer(tc.handlers...)
			for _, r := range tc.rules {
				tr.RegisterComponentLowering(r)
			}
			_, _, err := tr.TransformWithPolicy(tc.doc, TransformContext{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestSiblingGroup_ForwardingAcrossMembers: a member answering a contract with a
// zero value (as every trait decorator does for one it does not carry) leaves the
// answer to the member that has one, and the Flux namespace reaches every member.
func TestSiblingGroup_ForwardingAcrossMembers(t *testing.T) {
	a, b := stubHandler("a", 0), stubHandler("b", 8080)
	tr := siblingTransformer(a, b)
	tr.RegisterComponentLowering(emitRule{"pair", pair("a", "b")})

	cluster, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	apps := cluster.Node.Bundle.Applications
	if len(apps) != 1 || apps[0].Name != "web" {
		t.Fatalf("bundle applications = %d, want the group's one application", len(apps))
	}
	if got := apps[0].Config.(servicePortProvider).ServicePort(); got != 8080 {
		t.Errorf("ServicePort = %d, want member b's 8080", got)
	}
	for _, h := range []*siblingStubHandler{a, b} {
		if len(h.made) != 1 {
			t.Fatalf("handler %s built %d configs, want 1", h.typ, len(h.made))
		}
		if ns := h.made[0].(*siblingStub).fluxNS; ns != "flux-system" {
			t.Errorf("member %s Flux namespace = %q, want flux-system", h.typ, ns)
		}
	}
}
