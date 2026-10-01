package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// siblingStub is a component config answering every value-forwarded contract and
// taking a Flux namespace, so a test can see which member the group's config
// consulted. A zero field answers with the zero value, as a trait decorator does
// for a contract its inner config lacks.
type siblingStub struct {
	port     int32
	backend  string
	portName string
	sa       string
	claim    string
	selector *metav1.LabelSelector
	fluxNS   string
}

func (s *siblingStub) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *siblingStub) ServicePort() int32                                    { return s.port }
func (s *siblingStub) BackendServiceName() string                            { return s.backend }
func (s *siblingStub) ServicePortName() (string, bool)                       { return s.portName, s.portName != "" }
func (s *siblingStub) ServiceAccountName() string                            { return s.sa }
func (s *siblingStub) NonRWXClaim() string                                   { return s.claim }
func (s *siblingStub) SetFluxNamespace(ns string)                            { s.fluxNS = ns }
func (s *siblingStub) ServiceRoutingTarget(p []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	if s.selector == nil {
		return nil, nil
	}
	return s.selector, p
}

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

// TestSiblingGroup_TraitCopyDoesNotJoinGroup: a trait rule on a member that emits
// a by-value copy of that member (marker included) under another type does not
// enlarge the group; the copy is a duplicate name.
func TestSiblingGroup_TraitCopyDoesNotJoinGroup(t *testing.T) {
	tr := siblingTransformer(stubHandler("a", 0), stubHandler("b", 0), stubHandler("c", 0))
	tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
		return []Component{
			{Name: c.Name, Type: "a", Properties: map[string]any{}, Traits: []Trait{{Type: "copy", Properties: map[string]any{}}}},
			{Name: c.Name, Type: "b", Properties: map[string]any{}},
		}
	}})
	tr.RegisterTraitLowering(copyTraitRule{})
	_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `duplicate component name "web"`) {
		t.Fatalf("err = %v, want a duplicate component name refusal", err)
	}
}

// copyTraitRule lowers a "copy" trait to a copy of its component retyped "c".
type copyTraitRule struct{}

func (copyTraitRule) TraitType() string { return "copy" }
func (copyTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	c := *lctx.Component
	c.Type = "c"
	c.Traits = nil
	return LoweringResult{Components: []Component{c}}, nil
}

// TestSiblingGroup_ForwardingAcrossMembers: a member answering a contract with a
// zero value (as every trait decorator does for one it does not carry) leaves the
// answer to the member that has one, and the Flux namespace reaches every member.
func TestSiblingGroup_ForwardingAcrossMembers(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	a := stubHandler("a", 0)
	b := &siblingStubHandler{typ: "b", build: func() stack.ApplicationConfig {
		return &siblingStub{port: 8080, backend: "web-svc", portName: "http", sa: "web-sa", claim: "data", selector: sel}
	}}
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
	cfg := apps[0].Config
	if got := cfg.(servicePortProvider).ServicePort(); got != 8080 {
		t.Errorf("ServicePort = %d, want member b's 8080", got)
	}
	if got := cfg.(serviceBackendNamer).BackendServiceName(); got != "web-svc" {
		t.Errorf("BackendServiceName = %q, want member b's web-svc", got)
	}
	if got, known := cfg.(siblingServicePortNamer).ServicePortName(); got != "http" || !known {
		t.Errorf("ServicePortName = (%q, %v), want member b's (http, true)", got, known)
	}
	if got := cfg.(ServiceAccountNamer).ServiceAccountName(); got != "web-sa" {
		t.Errorf("ServiceAccountName = %q, want member b's web-sa", got)
	}
	if got := cfg.(siblingNonRWXClaimer).NonRWXClaim(); got != "data" {
		t.Errorf("NonRWXClaim = %q, want member b's data", got)
	}
	if got, _ := cfg.(serviceRoutingTargeter).ServiceRoutingTarget(nil); got != sel {
		t.Errorf("ServiceRoutingTarget selector = %v, want member b's %v", got, sel)
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
