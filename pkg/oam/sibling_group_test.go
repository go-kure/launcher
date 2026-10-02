package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
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
func (s *siblingStub) ServiceAccountName() (string, bool)                    { return s.sa, s.sa != "" }
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

// kindlessStub generates one ConfigMap without apiVersion and kind.
type kindlessStub struct{ siblingStub }

func (*kindlessStub) Generate(*stack.Application) ([]*client.Object, error) {
	var obj client.Object = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}}
	return []*client.Object{&obj}, nil
}

// objectsStub generates one ConfigMap per name, a nil entry for an empty name.
type objectsStub struct {
	siblingStub
	names []string
}

func (s *objectsStub) Generate(*stack.Application) ([]*client.Object, error) {
	out := make([]*client.Object, 0, len(s.names))
	for _, n := range s.names {
		if n == "" {
			out = append(out, nil)
			continue
		}
		var obj client.Object = &corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: "default"},
		}
		out = append(out, &obj)
	}
	return out, nil
}

// TestSiblingGroup_PrimaryObjectsFirst: the group generates each member's first
// object in member order, then the members' remaining objects — the order one
// component generating all of them uses (a Deployment, its Service, then the
// ServiceAccount and claims). A leading nil entry does not count as a member's
// first object.
func TestSiblingGroup_PrimaryObjectsFirst(t *testing.T) {
	objects := func(typ string, names ...string) *siblingStubHandler {
		return &siblingStubHandler{typ: typ, build: func() stack.ApplicationConfig { return &objectsStub{names: names} }}
	}
	tr := siblingTransformer(objects("a", "a1", "a2"), objects("b", "", "b1", "b2"))
	tr.RegisterComponentLowering(emitRule{"pair", pair("a", "b")})
	cluster, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	objs, err := cluster.Node.Bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var got []string
	for _, p := range objs {
		if p != nil {
			got = append(got, (*p).GetName())
		}
	}
	if want := []string{"a1", "b1", "a2", "b2"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("group objects = %v, want %v", got, want)
	}
}

// TestSiblingGroup_KindlessObjectRefused: a member object with no kind cannot be
// compared against the other members' objects, so the group refuses it.
func TestSiblingGroup_KindlessObjectRefused(t *testing.T) {
	tr := siblingTransformer(stubHandler("a", 0), &siblingStubHandler{typ: "k", build: func() stack.ApplicationConfig { return &kindlessStub{} }})
	tr.RegisterComponentLowering(emitRule{"pair", pair("a", "k")})
	cluster, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	_, err = cluster.Node.Bundle.Applications[0].Generate()
	if want := `member "k" generates object "default/web" with no kind`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Generate err = %v, want it to contain %q", err, want)
	}
}

// subAppTrait appends a sub-application named after its component, as builtin
// traits do (<name>-rbac, <name>-ingress).
type subAppTrait struct{}

func (subAppTrait) CanHandle(t string) bool { return t == "sub" }
func (subAppTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-sub", app.Namespace, &siblingStub{}))
	return nil
}

// TestSiblingGroup_TraitSubApplications: the same trait on two members would
// create one sub-application name twice and is refused; on one member it passes.
func TestSiblingGroup_TraitSubApplications(t *testing.T) {
	sub := []Trait{{Type: "sub", Properties: map[string]any{}}}
	for _, tc := range []struct {
		name      string
		onB, want string
	}{
		{name: "one member", want: ""},
		{name: "two members", onB: "x", want: `traits on members "a" and "b" both create sub-application "web-sub"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0), "b": stubHandler("b", 0)},
				map[string]TraitHandler{"sub": subAppTrait{}})
			tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
				b := Component{Name: c.Name, Type: "b", Properties: map[string]any{}}
				if tc.onB != "" {
					b.Traits = sub
				}
				return []Component{{Name: c.Name, Type: "a", Properties: map[string]any{}, Traits: sub}, b}
			}})
			_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{})
			if tc.want == "" && err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// namedSubTrait appends a sub-application named <component>-<trait type>, so the
// bundle's application order shows the order its traits were applied in.
type namedSubTrait struct{}

func (namedSubTrait) CanHandle(string) bool { return true }
func (namedSubTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	b.Applications = append(b.Applications, stack.NewApplication(app.Name+"-"+trait.Type, app.Namespace, &siblingStub{}))
	return nil
}

// hopTraitRule lowers a "hop" trait to one "t-hop" trait of its own.
type hopTraitRule struct{}

func (hopTraitRule) TraitType() string { return "hop" }
func (hopTraitRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "t-hop", Properties: map[string]any{}}}}, nil
}

// traitOrderApps transforms one authored "web" of type typ carrying traits of the
// given types under rule, and returns the names of the applications its bundle
// carries after the component's own.
func traitOrderApps(t *testing.T, rule emitRule, traitTypes ...string) []string {
	t.Helper()
	handlers := map[string]TraitHandler{}
	for _, typ := range []string{"t0", "t1", "own", "t-hop"} {
		handlers[typ] = namedSubTrait{}
	}
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0), "b": stubHandler("b", 0)}, handlers)
	tr.RegisterComponentLowering(rule)
	tr.RegisterTraitLowering(hopTraitRule{})
	authored := Component{Name: "web", Type: rule.typ}
	for _, typ := range traitTypes {
		authored.Traits = append(authored.Traits, Trait{Type: typ, Properties: map[string]any{}})
	}
	cluster, _, err := tr.TransformWithPolicy(siblingDoc(authored), TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	var names []string
	for _, a := range cluster.Node.Bundle.Applications[1:] {
		names = append(names, a.Name)
	}
	return names
}

// crossRule emits members a and b, handing a the authored trait at slot 1 and b
// its own trait "own" followed by the authored trait at slot 0.
var crossRule = emitRule{"cross", func(c *Component) []Component {
	return []Component{
		{Name: c.Name, Type: "a", Properties: map[string]any{}, Traits: []Trait{c.Traits[1]}},
		{Name: c.Name, Type: "b", Properties: map[string]any{}, Traits: []Trait{{Type: "own", Properties: map[string]any{}}, c.Traits[0]}},
	}
}}

// TestSiblingGroup_TraitsApplyInAuthoredOrder: a group applies its members'
// traits as authored, not member by member — the rule's own trait first, then the
// forwarded ones by authored slot — so trait sub-applications are ordered as one
// component carrying the same traits orders them.
func TestSiblingGroup_TraitsApplyInAuthoredOrder(t *testing.T) {
	got := traitOrderApps(t, crossRule, "t0", "t1")
	if want := []string{"web-own", "web-t0", "web-t1"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trait sub-applications = %v, want %v", got, want)
	}
}

// TestSiblingGroup_LoweredTraitKeepsAuthoredSlot: a forwarded trait a trait rule
// lowers in a later round keeps the authored slot of the trait it replaced.
func TestSiblingGroup_LoweredTraitKeepsAuthoredSlot(t *testing.T) {
	got := traitOrderApps(t, crossRule, "t0", "hop")
	if want := []string{"web-own", "web-t0", "web-t-hop"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trait sub-applications = %v, want %v", got, want)
	}
}

// TestSiblingGroup_SingleComponentTraitOrderUnchanged: the authored-order merge is
// for groups only. A rule emitting one component applies its traits in the order
// it placed them, its own trait after a forwarded one included.
func TestSiblingGroup_SingleComponentTraitOrderUnchanged(t *testing.T) {
	single := emitRule{"single", func(c *Component) []Component {
		return []Component{{Name: c.Name, Type: "a", Properties: map[string]any{},
			Traits: []Trait{c.Traits[0], {Type: "own", Properties: map[string]any{}}}}}
	}}
	got := traitOrderApps(t, single, "t0")
	if want := []string{"web-t0", "web-own"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trait sub-applications = %v, want %v", got, want)
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
	if got := cfg.(ComponentNamed).ComponentName(); got != "web" {
		t.Errorf("ComponentName = %q, want the group's web", got)
	}
	if got := cfg.(servicePortProvider).ServicePort(); got != 8080 {
		t.Errorf("ServicePort = %d, want member b's 8080", got)
	}
	if got := cfg.(serviceBackendNamer).BackendServiceName(); got != "web-svc" {
		t.Errorf("BackendServiceName = %q, want member b's web-svc", got)
	}
	if got, known := cfg.(siblingServicePortNamer).ServicePortName(); got != "http" || !known {
		t.Errorf("ServicePortName = (%q, %v), want member b's (http, true)", got, known)
	}
	if got, pods := cfg.(ServiceAccountNamer).ServiceAccountName(); got != "web-sa" || !pods {
		t.Errorf("ServiceAccountName = (%q, %v), want member b's (web-sa, true)", got, pods)
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

// podsStub is a member running pods that carry labels (podTemplateLabeler).
type podsStub struct {
	siblingStub
	labels map[string]string
}

func (s *podsStub) PodTemplateLabels() map[string]string { return s.labels }

// identityStub is a routing member whose ports either all target themselves or
// not (identityPortMapper).
type identityStub struct {
	siblingStub
	identity bool
}

func (s *identityStub) IdentityTargetPorts() bool { return s.identity }

// TestSiblingGroup_RoutingTargetOnOwnPods: a member routing to another member's
// pods on ports mapped to themselves routes to the group's own pods, so synthesis
// keeps its component-label policy; every other routing target does not. Either
// way the group forwards the member's routing target, whose ports synthesis still
// translates and filters.
func TestSiblingGroup_RoutingTargetOnOwnPods(t *testing.T) {
	pods := &podsStub{labels: map[string]string{"app": "web", "tier": "front"}}
	ports := []intstr.IntOrString{intstr.FromInt32(8080)}
	cases := []struct {
		name     string
		selector *metav1.LabelSelector
		identity bool
		ownPods  bool
	}{
		{"subset on identity ports", &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, true, true},
		{"whole label set", &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web", "tier": "front"}}, true, true},
		{"remapped port", &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, false, false},
		{"not a subset", &metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}}, true, false},
		{"extra label", &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web", "x": "y"}}, true, false},
		{"empty matchLabels", &metav1.LabelSelector{}, true, false},
		{"matchExpressions", &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "web"},
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "tier", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"front"}},
			},
		}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route := &identityStub{siblingStub: siblingStub{selector: tc.selector}, identity: tc.identity}
			g := &siblingGroupConfig{members: []*stack.Application{
				stack.NewApplication("web", "ns", pods),
				stack.NewApplication("web", "ns", route),
			}}
			if got := g.routesToOwnPods(); got != tc.ownPods {
				t.Errorf("routesToOwnPods = %v, want %v", got, tc.ownPods)
			}
			sel, got := g.ServiceRoutingTarget(ports)
			if sel != tc.selector || len(got) != 1 || got[0] != ports[0] {
				t.Errorf("ServiceRoutingTarget = (%v, %v), want the member's (%v, %v)", sel, got, tc.selector, ports)
			}
		})
	}
}

// TestSiblingGroup_RoutingTargetWithoutPodLabeler: a routing target selecting pods
// no member declares — its own sibling runs none — does not route to the group's
// own pods.
func TestSiblingGroup_RoutingTargetWithoutPodLabeler(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	route := &identityStub{siblingStub: siblingStub{selector: sel}, identity: true}
	g := &siblingGroupConfig{members: []*stack.Application{
		stack.NewApplication("web", "ns", &siblingStub{}),
		stack.NewApplication("web", "ns", route),
	}}
	if g.routesToOwnPods() {
		t.Error("routesToOwnPods = true, want false")
	}
	if got, _ := g.ServiceRoutingTarget(nil); got != sel {
		t.Errorf("ServiceRoutingTarget selector = %v, want the member's %v", got, sel)
	}
}
