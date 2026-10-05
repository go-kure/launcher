package oam

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// memberNamingRule lowers a "probe" component to one member of memberType under
// the component's name, after resolving the name of the member's object under
// role. The component's `memberName` property is the authored name; calls is
// how often the rule resolves it (0 counts as once).
type memberNamingRule struct {
	memberType string
	role       NameRole
	scope      ObjectScope
	calls      int
	// props are the member's properties, nil for none.
	props map[string]any
}

func (memberNamingRule) ComponentType() string { return "probe" }

func (r memberNamingRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	member := Component{Name: comp.Name, Type: r.memberType, Properties: map[string]any{}}
	if r.props != nil {
		member.Properties = r.props
	}
	spec := NameSpec{Role: r.role, Kind: widgetKind, ClusterScoped: r.scope == ObjectScopeCluster, FluxScoped: r.scope == ObjectScopeFlux}
	if raw, ok := comp.Properties["memberName"]; ok {
		spec.Property, spec.Authored = "memberName", raw.(string)
	}
	for range max(r.calls, 1) {
		if err := lctx.ResolveMemberName(&member, spec); err != nil {
			return LoweringResult{}, err
		}
	}
	return LoweringResult{Components: []Component{member}}, nil
}

func probe(name string, props map[string]any) Component {
	if props == nil {
		props = map[string]any{}
	}
	return Component{Name: name, Type: "probe", Properties: props}
}

// memberTransformer is a transformer whose "probe" components are lowered by
// rule to a "widget" member, a kind component.
func memberTransformer(rule memberNamingRule) (*Transformer, *kindStubHandler) {
	h := kindStub("widget", widgetKind, rule.scope)
	tr := NewTransformer(map[string]ComponentHandler{"widget": h, "a": stubHandler("a", 0)}, nil)
	tr.RegisterComponentLowering(rule)
	return tr, h
}

// roleHook records each request and answers the ones of role from answers, by
// component.
func roleHook(role NameRole, answers map[string]string, asked *[]NameRequest) func(NameRequest) (string, bool) {
	return func(req NameRequest) (string, bool) {
		*asked = append(*asked, req)
		if req.Role != role {
			return "", false
		}
		name, ok := answers[req.Component]
		return name, ok
	}
}

// The object of a member a rule emits under the component's name is named by the
// author's property, else by the Naming hook under the rule's own role, else
// after the component. The member keeps the component's name, and the hook is
// not asked under role "object" for it.
func TestResolveMemberName_Order(t *testing.T) {
	rule := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization}
	request := NameRequest{Application: "app", Component: "web", Role: NameRoleOCIKustomization, Kind: "Widget.example.com", Default: "web"}
	for _, tt := range []struct {
		name      string
		props     map[string]any
		answers   map[string]string
		want      string
		wantAsked []NameRequest
	}{
		{name: "the component name", want: "web", wantAsked: []NameRequest{request}},
		{name: "the hook's", answers: map[string]string{"web": "hooked"}, want: "hooked", wantAsked: []NameRequest{request}},
		{name: "the author's, and the hook is not asked", props: map[string]any{"memberName": "authored"},
			answers: map[string]string{"web": "hooked"}, want: "authored"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr, h := memberTransformer(rule)
			var asked []NameRequest
			_, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", tt.props)), TransformContext{Naming: roleHook(NameRoleOCIKustomization, tt.answers, &asked)})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			// Keyed by the member's component name: the member kept it.
			if got := h.names["web"]; got != tt.want {
				t.Errorf("the handler was handed object name %q for component web, want %q", got, tt.want)
			}
			var object, own []NameRequest
			for _, req := range asked {
				if req.Role == NameRoleObject {
					object = append(object, req)
				}
				if req.Role == NameRoleOCIKustomization {
					own = append(own, req)
				}
			}
			if len(object) != 0 {
				t.Errorf("the hook was asked under role object for a member a rule emitted: %+v", object)
			}
			if !slices.Equal(own, tt.wantAsked) {
				t.Errorf("the hook was asked %+v, want %+v", own, tt.wantAsked)
			}
		})
	}
}

// A hook that declines leaves the member as the rule built it: the default sets
// nothing on it.
func TestResolveMemberName_DefaultLeavesTheMemberAlone(t *testing.T) {
	spec := NameSpec{Role: NameRoleOCIKustomization, Kind: widgetKind}
	for name, lctx := range map[string]LoweringContext{
		"in a transform":  newLoweringHarness(nil).lctx("web"),
		"without a Namer": {},
	} {
		t.Run(name, func(t *testing.T) {
			member := Component{Name: "web", Type: "widget", Properties: map[string]any{}}
			want := member
			if err := lctx.ResolveMemberName(&member, spec); err != nil {
				t.Fatalf("ResolveMemberName: %v", err)
			}
			if !reflect.DeepEqual(member, want) {
				t.Errorf("the member is %+v after the default, want it unchanged: %+v", member, want)
			}
			if got := member.ObjectName(); got != "web" {
				t.Errorf("ObjectName() = %q, want the component name", got)
			}
		})
	}
}

// Every name a rule gives a member's object is claimed: the default as much as
// an authored one or the hook's, so another owner of that object is refused with
// both named.
func TestResolveMemberName_Claimed(t *testing.T) {
	rule := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization}
	other := func(name string) Component { return widget("other", map[string]any{"objectName": name}) }
	for _, tt := range []struct {
		name    string
		doc     *Application
		answers map[string]string
		want    string
	}{
		{
			name: "the default",
			doc:  siblingDoc(probe("web", nil), other("web")),
			want: `name collision: Widget.example.com "default/web" is named by ` +
				`component "web" (role "oci-kustomization", its default) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored name",
			doc:  siblingDoc(probe("web", map[string]any{"memberName": "shared"}), other("shared")),
			want: `name collision: Widget.example.com "default/shared" is named by ` +
				`component "web" (role "oci-kustomization", set by memberName) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name:    "the hook's answer",
			doc:     siblingDoc(probe("web", nil), other("shared")),
			answers: map[string]string{"web": "shared"},
			want: `name collision: Widget.example.com "default/shared" is named by ` +
				`component "web" (role "oci-kustomization", returned by the Naming hook in place of "web") and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "two members of two components",
			doc:  siblingDoc(probe("web", map[string]any{"memberName": "shared"}), probe("api", map[string]any{"memberName": "shared"})),
			want: `name collision: Widget.example.com "shared" is named by ` +
				`component "web" (role "oci-kustomization", set by memberName) and by ` +
				`component "api" (role "oci-kustomization", set by memberName); give one of them another name`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr, _ := memberTransformer(rule)
			var asked []NameRequest
			_, _, err := tr.TransformWithPolicy(tt.doc, TransformContext{Naming: roleHook(NameRoleOCIKustomization, tt.answers, &asked)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tt.want)
			}
		})
	}

	// A member's object is one object: a rule that resolves its name twice is
	// refused as every second resolution of one name is.
	t.Run("resolved twice", func(t *testing.T) {
		twice := rule
		twice.calls = 2
		tr, _ := memberTransformer(twice)
		_, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", nil)), TransformContext{})
		const want = `name collision: Widget.example.com "web" is named twice by component "web" (role "oci-kustomization", its default); give one of them another name`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
}

// A Flux-scoped member's name is claimed in the Flux namespace when the
// transform has one, where an object of the application namespace is another.
func TestResolveMemberName_FluxScoped(t *testing.T) {
	rule := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization, scope: ObjectScopeFlux}
	gadget := kindStub("gadget", widgetKind, ObjectScopeNamespaced)
	doc := func() *Application {
		return siblingDoc(probe("web", map[string]any{"memberName": "shared"}),
			Component{Name: "other", Type: "gadget", Properties: map[string]any{"objectName": "shared"}})
	}
	transformer := func() *Transformer {
		tr, _ := memberTransformer(rule)
		tr.componentHandlers["gadget"] = gadget
		return tr
	}

	if _, _, err := transformer().TransformWithPolicy(doc(), TransformContext{FluxNamespace: "flux-system"}); err != nil {
		t.Fatalf("with a Flux namespace the two are two objects: %v", err)
	}
	_, _, err := transformer().TransformWithPolicy(doc(), TransformContext{})
	const want = `name collision: Widget.example.com "default/shared" is named by component "web" (role "oci-kustomization", set by memberName) and by component "other" (role "object", set by properties.objectName)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("without a Flux namespace: err = %v\nwant one containing %s", err, want)
	}
}

// A name that is not the default is validated for its role and used as given
// or refused, never shortened.
func TestResolveMemberName_Refusals(t *testing.T) {
	long := strings.Repeat("a", 254)
	kustomization := NameSpec{Role: NameRoleOCIKustomization, Kind: widgetKind}
	authored := func(name string) NameSpec {
		spec := kustomization
		spec.Property, spec.Authored = "memberName", name
		return spec
	}
	member := func() *Component { return &Component{Name: "web", Type: "widget"} }
	for _, tt := range []struct {
		name    string
		member  *Component
		spec    NameSpec
		answers map[string]string
		want    string
	}{
		{name: "no member", spec: kustomization, want: "ResolveMemberName needs a member component with a name"},
		{name: "a member with no name", member: &Component{Type: "widget"}, spec: kustomization, want: "ResolveMemberName needs a member component with a name"},
		{name: "a role that names no object", member: member(), spec: NameSpec{Role: NameRoleBundle},
			want: `role "bundle" names no object; a lowering rule resolves object names only`},
		{name: "an unknown role", member: member(), spec: NameSpec{Role: "service", Kind: widgetKind}, want: `"service" is not a name role`},
		{name: "an object role with no kind", member: member(), spec: NameSpec{Role: NameRoleOCIKustomization},
			want: `role "oci-kustomization" names an object, and its NameSpec has no Kind`},
		{name: "cluster-scoped beside a namespace", member: member(),
			spec: NameSpec{Role: NameRoleOCIKustomization, Kind: widgetKind, ClusterScoped: true, Namespace: "prod"}, want: "ClusterScoped"},
		{name: "an authored name that is no subdomain", member: member(), spec: authored("Bad_Name"),
			want: `memberName "Bad_Name" cannot be the name for role "oci-kustomization": not a valid DNS-1123 subdomain: `},
		{name: "an empty authored name", member: member(), spec: authored(""),
			want: `memberName "" cannot be the name for role "oci-kustomization": it is empty; write a valid name, or leave the property out for the default "web"`},
		{name: "an authored name over 253 characters is not shortened", member: member(), spec: authored(long),
			want: `cannot be the name for role "oci-kustomization": not a valid DNS-1123 subdomain: must be no more than 253`},
		{name: "a hook answer that is no subdomain", member: member(), spec: kustomization, answers: map[string]string{"web": "Bad_Name"},
			want: `the Naming hook returned "Bad_Name" for role "oci-kustomization" in place of "web": not a valid DNS-1123 subdomain: `},
		{name: "a hook answer over 253 characters is not shortened", member: member(), spec: kustomization, answers: map[string]string{"web": long},
			want: `for role "oci-kustomization" in place of "web": not a valid DNS-1123 subdomain: must be no more than 253`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := newLoweringHarness(tt.answers).lctx("web").ResolveMemberName(tt.member, tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tt.want)
			}
			if tt.member != nil && tt.member.ObjectName() != tt.member.Name {
				t.Errorf("a refused name was set on the member: %q", tt.member.ObjectName())
			}
		})
	}
}

// On a context with no Namer (a rule driven directly) an authored name is
// validated and set, no hook is consulted and nothing is claimed.
func TestResolveMemberName_WithoutANamer(t *testing.T) {
	spec := NameSpec{Role: NameRoleOCIKustomization, Kind: widgetKind, Property: "memberName", Authored: "mine"}
	for range 2 {
		// Twice: nothing is recorded, so nothing refuses the second.
		member := Component{Name: "web", Type: "widget"}
		if err := (LoweringContext{}).ResolveMemberName(&member, spec); err != nil {
			t.Fatalf("ResolveMemberName: %v", err)
		}
		if member.ObjectName() != "mine" || member.Name != "web" {
			t.Fatalf("member %q names its object %q, want web and mine", member.Name, member.ObjectName())
		}
	}
	spec.Authored = "Bad_Name"
	member := Component{Name: "web", Type: "widget"}
	err := (LoweringContext{}).ResolveMemberName(&member, spec)
	if err == nil || !strings.Contains(err.Error(), `memberName "Bad_Name" cannot be the name for role "oci-kustomization"`) {
		t.Fatalf("err = %v, want the authored name refused", err)
	}
}

// A name a rule set on a member whose type declares no object is refused by the
// transform: no single object would take it. The default, which sets nothing,
// passes.
func TestResolveMemberName_TypeWithNoObject(t *testing.T) {
	rule := memberNamingRule{memberType: "a", role: NameRoleOCIKustomization}
	tr, _ := memberTransformer(rule)
	if _, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", nil)), TransformContext{}); err != nil {
		t.Fatalf("the default: %v", err)
	}
	tr, _ = memberTransformer(rule)
	_, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", map[string]any{"memberName": "mine"})), TransformContext{})
	const want = `a lowering rule named the object of this component "mine"`
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), `component type "a" generates no single object named after the component`) {
		t.Fatalf("err = %v\nwant one containing %s", err, want)
	}
}

// `objectName` stays refused on a member whose object the rule named: the rule's
// call is the one way to name it.
func TestResolveMemberName_ObjectNamePropertyStillRefused(t *testing.T) {
	rule := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization, props: map[string]any{"objectName": "theirs"}}
	tr, _ := memberTransformer(rule)
	_, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", map[string]any{"memberName": "mine"})), TransformContext{})
	const want = "objectName is set on a component a component or trait lowering rule emitted"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %s", err, want)
	}
}
