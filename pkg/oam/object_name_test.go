package oam

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	widgetKind = schema.GroupKind{Group: "example.com", Kind: "Widget"}
	gadgetKind = schema.GroupKind{Group: "example.com", Kind: "Gadget"}
)

// kindStubHandler is a kind component's handler: it declares its object and a
// schema of its own without `objectName`, and records, per component, the
// object name it was handed and whether the property was still there.
type kindStubHandler struct {
	typ      string
	kind     schema.GroupKind
	scope    ObjectScope
	names    map[string]string
	property map[string]bool
}

func kindStub(typ string, kind schema.GroupKind, scope ObjectScope) *kindStubHandler {
	return &kindStubHandler{typ: typ, kind: kind, scope: scope, names: map[string]string{}, property: map[string]bool{}}
}

func (h *kindStubHandler) CanHandle(t string) bool { return t == h.typ }
func (h *kindStubHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"size": {Type: PropertyTypeString}}
}
func (h *kindStubHandler) ComponentObject() (schema.GroupKind, ObjectScope) { return h.kind, h.scope }
func (h *kindStubHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.names[c.Name] = c.ObjectName()
	_, h.property[c.Name] = c.Properties[ObjectNameProperty]
	return &siblingStub{}, nil
}

// objectHook answers the "object" request of the components in answers, and
// records every "object" request it was asked.
func objectHook(answers map[string]string, asked *[]NameRequest) func(NameRequest) (string, bool) {
	return func(req NameRequest) (string, bool) {
		if req.Role != NameRoleObject {
			return "", false
		}
		*asked = append(*asked, req)
		name, ok := answers[req.Component]
		return name, ok
	}
}

func widget(name string, props map[string]any) Component {
	if props == nil {
		props = map[string]any{}
	}
	return Component{Name: name, Type: "widget", Properties: props}
}

// A kind component's object is named by the author's `objectName`, else by the
// Naming hook under role "object", else after the component. The handler reads
// the result off the component and never sees the property.
func TestObjectName_Order(t *testing.T) {
	request := NameRequest{Application: "app", Component: "web", Role: NameRoleObject, Kind: "Widget.example.com", Default: "web"}
	tests := []struct {
		name      string
		props     map[string]any
		answers   map[string]string
		want      string
		wantAsked []NameRequest
	}{
		{name: "the component name", want: "web", wantAsked: []NameRequest{request}},
		{name: "the hook's", answers: map[string]string{"web": "hooked"}, want: "hooked", wantAsked: []NameRequest{request}},
		{name: "the author's, and the hook is not asked", props: map[string]any{"objectName": "authored"},
			answers: map[string]string{"web": "hooked"}, want: "authored"},
		{name: "an explicit null is no name", props: map[string]any{"objectName": nil}, want: "web", wantAsked: []NameRequest{request}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := kindStub("widget", widgetKind, ObjectScopeNamespaced)
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			var asked []NameRequest
			_, _, err := tr.TransformWithPolicy(siblingDoc(widget("web", tt.props)), TransformContext{Naming: objectHook(tt.answers, &asked)})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := h.names["web"]; got != tt.want {
				t.Errorf("the handler was handed object name %q, want %q", got, tt.want)
			}
			if h.property["web"] {
				t.Error("the handler was handed the objectName property; the engine takes it out")
			}
			if !slices.Equal(asked, tt.wantAsked) {
				t.Errorf("the hook was asked %+v, want %+v", asked, tt.wantAsked)
			}
		})
	}
}

// The author's properties are not changed by the engine taking `objectName`
// out: the document can be transformed again.
func TestObjectName_LeavesTheAuthoredPropertiesAlone(t *testing.T) {
	h := kindStub("widget", widgetKind, ObjectScopeNamespaced)
	tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
	doc := siblingDoc(widget("web", map[string]any{"objectName": "authored"}))
	for range 2 {
		if _, _, err := tr.TransformWithPolicy(doc, TransformContext{}); err != nil {
			t.Fatalf("transform: %v", err)
		}
		if got := h.names["web"]; got != "authored" {
			t.Fatalf("object name %q, want the authored one", got)
		}
	}
	if got := doc.Spec.Components[0].Properties["objectName"]; got != "authored" {
		t.Errorf("the document's objectName is %v after two transforms, want it as authored", got)
	}
}

func TestObjectName_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		answers map[string]string
		want    []string
	}{
		{name: "an authored name that is no DNS-1123 subdomain", props: map[string]any{"objectName": "Not_Valid"},
			want: []string{"properties.objectName", `"Not_Valid"`}},
		{name: "an empty authored name", props: map[string]any{"objectName": ""}, want: []string{"properties.objectName"}},
		{name: "not a string", props: map[string]any{"objectName": 3}, want: []string{"objectName"}},
		{name: "a hook's answer that is no DNS-1123 subdomain", answers: map[string]string{"web": "Not_Valid"},
			want: []string{`the Naming hook returned "Not_Valid" for role "object" in place of "web"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"widget": kindStub("widget", widgetKind, ObjectScopeNamespaced)}, nil)
			var asked []NameRequest
			_, _, err := tr.TransformWithPolicy(siblingDoc(widget("web", tt.props)), TransformContext{Naming: objectHook(tt.answers, &asked)})
			if err == nil {
				t.Fatal("transform succeeded, want a refusal")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v\nwant it to contain %q", err, want)
				}
			}
		})
	}
}

// A member a component lowering rule emitted is named by its rule: the hook is
// not asked for its object, and `objectName` on it is refused, in words that
// name the rule kinds that emit a member.
func TestObjectName_EmittedMember(t *testing.T) {
	emit := func(props map[string]any) *Transformer {
		tr := NewTransformer(map[string]ComponentHandler{"widget": kindStub("widget", widgetKind, ObjectScopeNamespaced)}, nil)
		tr.RegisterComponentLowering(emitRule{typ: "role", fn: func(c *Component) []Component {
			return []Component{{Name: c.Name, Type: "widget", Properties: props}}
		}})
		return tr
	}
	doc := func() *Application {
		return siblingDoc(Component{Name: "web", Type: "role", Properties: map[string]any{}})
	}

	var asked []NameRequest
	if _, _, err := emit(map[string]any{}).TransformWithPolicy(doc(), TransformContext{Naming: objectHook(map[string]string{"web": "hooked"}, &asked)}); err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(asked) != 0 {
		t.Errorf("the hook was asked %+v for an emitted member, want no request of role object", asked)
	}

	_, _, err := emit(map[string]any{"objectName": "renamed"}).TransformWithPolicy(doc(), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), "objectName is set on a component a component or trait lowering rule emitted") {
		t.Fatalf("err = %v\nwant objectName refused on a member a component rule emitted", err)
	}
}

// copyingDocRule is testDocRule building its components anew, by value, where
// testDocRule forwards the ones it was given.
type copyingDocRule struct{ testDocRule }

func (r copyingDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	res, err := r.testDocRule.LowerDocument(doc, lctx)
	if err == nil {
		res.Documents[0].Spec.Components = append([]Component(nil), doc.Spec.Components...)
	}
	return res, err
}

// What a document rule returns is authored input, the components it forwards
// and the ones it builds alike: `objectName` names the object of a kind
// component there, and without one the hook is asked.
func TestObjectName_ThroughADocumentRule(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule DocumentLoweringRule
	}{
		{name: "a forwarded component", rule: testDocRule{kind: "Wrapper"}},
		{name: "a component the rule built", rule: copyingDocRule{testDocRule{kind: "Wrapper"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transform := func(props map[string]any, asked *[]NameRequest) string {
				t.Helper()
				h := kindStub("widget", widgetKind, ObjectScopeNamespaced)
				tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
				tr.RegisterDocumentLowering(tt.rule)
				doc := siblingDoc(widget("web", props))
				doc.Kind = "Wrapper"
				if _, _, err := tr.TransformWithPolicy(doc, TransformContext{Naming: objectHook(map[string]string{"web": "hooked"}, asked)}); err != nil {
					t.Fatalf("transform: %v", err)
				}
				return h.names["web"]
			}

			var asked []NameRequest
			if got := transform(map[string]any{"objectName": "renamed"}, &asked); got != "renamed" {
				t.Errorf("object name %q, want the authored one", got)
			}
			if len(asked) != 0 {
				t.Errorf("the hook was asked %+v though the author named the object", asked)
			}
			if got := transform(nil, &asked); got != "hooked" {
				t.Errorf("object name %q, want the hook's", got)
			}
			if len(asked) != 1 {
				t.Errorf("the hook was asked %d times for the object, want once", len(asked))
			}
		})
	}
}

// widgetRawRule is testRawRule writing one widget component, with the
// `objectName` its document's author gave it, if any.
type widgetRawRule struct {
	testRawRule
	objectName string
}

func (r widgetRawRule) LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error) {
	res, err := r.testRawRule.LowerDocument(doc, lctx)
	if err == nil {
		props := map[string]any{}
		if r.objectName != "" {
			props["objectName"] = r.objectName
		}
		res.Documents[0].Spec.Components[0].Properties = props
	}
	return res, err
}

// What a raw document rule writes is authored input too: LowerRaws keeps an
// `objectName` on a kind component, and the transform of what it returns names
// the object by it, or asks the hook without one.
func TestObjectName_ThroughARawDocumentRule(t *testing.T) {
	transform := func(objectName string, asked *[]NameRequest) string {
		t.Helper()
		// Registered under a type name the parser knows, since what LowerRaws
		// returns is parsed before it is transformed.
		h := kindStub("configmap", widgetKind, ObjectScopeNamespaced)
		tr := NewTransformer(map[string]ComponentHandler{"configmap": h}, nil)
		tr.RegisterRawDocumentLowering(widgetRawRule{testRawRule: testRawRule{kind: "WebApplication", compType: "configmap"}, objectName: objectName})
		out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
		if err != nil || len(out) != 1 {
			t.Fatalf("LowerRaws = %d documents, %v; want one", len(out), err)
		}
		doc, err := Parse(out[0])
		if err != nil {
			t.Fatalf("parse the lowered document: %v\n%s", err, out[0])
		}
		if _, _, err := tr.TransformWithPolicy(doc, TransformContext{Naming: objectHook(map[string]string{"web": "hooked"}, asked)}); err != nil {
			t.Fatalf("transform: %v", err)
		}
		return h.names["web"]
	}

	var asked []NameRequest
	if got := transform("renamed", &asked); got != "renamed" {
		t.Errorf("object name %q, want the authored one", got)
	}
	if len(asked) != 0 {
		t.Errorf("the hook was asked %+v though the author named the object", asked)
	}
	if got := transform("", &asked); got != "hooked" {
		t.Errorf("object name %q, want the hook's", got)
	}
	if len(asked) != 1 {
		t.Errorf("the hook was asked %d times for the object, want once", len(asked))
	}
}

// A type that declares a schema and no object refuses `objectName`.
func TestObjectName_NotAKindComponent(t *testing.T) {
	h := &plainHandler{}
	tr := NewTransformer(map[string]ComponentHandler{"plain": h}, nil)
	doc := siblingDoc(Component{Name: "web", Type: "plain", Properties: map[string]any{"objectName": "renamed"}})
	_, _, err := tr.TransformWithPolicy(doc, TransformContext{})
	if err == nil || !strings.Contains(err.Error(), "objectName") {
		t.Fatalf("err = %v\nwant objectName refused on a type that declares no object", err)
	}
}

// plainHandler declares a schema of its own and no object.
type plainHandler struct{}

func (*plainHandler) CanHandle(t string) bool { return t == "plain" }
func (*plainHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"size": {Type: PropertyTypeString}}
}
func (*plainHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &siblingStub{}, nil
}

// The object's name joins the one claim space under role "object": two
// components naming one object of one kind and scope are refused with both
// named, whichever of them the name is the default of.
func TestObjectName_Collisions(t *testing.T) {
	handlers := func() map[string]ComponentHandler {
		return map[string]ComponentHandler{
			"widget":  kindStub("widget", widgetKind, ObjectScopeNamespaced),
			"gadget":  kindStub("gadget", gadgetKind, ObjectScopeNamespaced),
			"cluster": kindStub("cluster", widgetKind, ObjectScopeCluster),
			"flux":    kindStub("flux", gadgetKind, ObjectScopeFlux),
		}
	}
	named := func(name, typ, objectName string) Component {
		props := map[string]any{}
		if objectName != "" {
			props["objectName"] = objectName
		}
		return Component{Name: name, Type: typ, Properties: props}
	}
	tests := []struct {
		name       string
		components []Component
		ctx        TransformContext
		want       string // "" for no collision
	}{
		{name: "two authored names", components: []Component{named("a", "widget", "shared"), named("b", "widget", "shared")},
			want: `name collision: Widget.example.com "default/shared" is named by component "a" (role "object", set by properties.objectName) and by component "b" (role "object", set by properties.objectName)`},
		{name: "an authored name and another component's own", components: []Component{named("a", "widget", "b"), named("b", "widget", "")},
			want: `name collision: Widget.example.com "default/b" is named by component "a" (role "object", set by properties.objectName) and by component "b" (role "object", its default)`},
		{name: "two kinds", components: []Component{named("a", "widget", "shared"), named("b", "gadget", "shared")}},
		{name: "a cluster-scoped object has no namespace", components: []Component{named("a", "cluster", "shared"), named("b", "cluster", "shared")},
			want: `name collision: Widget.example.com "shared" is named by component "a"`},
		{name: "a cluster-scoped object and a namespaced one of its kind", components: []Component{named("a", "cluster", "shared"), named("b", "widget", "shared")}},
		{name: "a Flux object is of the Flux namespace", components: []Component{named("a", "flux", "shared"), named("b", "flux", "shared")},
			ctx:  TransformContext{FluxNamespace: "flux-system"},
			want: `name collision: Gadget.example.com "flux-system/shared" is named by component "a"`},
		{name: "a Flux object and a namespaced one under a Flux namespace", components: []Component{named("a", "flux", "shared"), named("b", "gadget", "shared")},
			ctx: TransformContext{FluxNamespace: "flux-system"}},
		{name: "a Flux object without a Flux namespace is of the document's", components: []Component{named("a", "flux", "shared"), named("b", "gadget", "shared")},
			want: `name collision: Gadget.example.com "default/shared" is named by component "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTransformer(handlers(), nil)
			_, _, err := tr.TransformWithPolicy(siblingDoc(tt.components...), tt.ctx)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("transform: %v\nwant no collision", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("err = %v\nwant one containing %s", err, tt.want)
			}
		})
	}
}

// objectNameTrait records the object name of the component it is applied on.
type objectNameTrait struct{ got *string }

func (objectNameTrait) CanHandle(t string) bool { return t == "reads-object-name" }
func (o objectNameTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*o.got = trait.ComponentObjectName()
	return nil
}

// A trait reads the name of its component's object, for a reference it writes
// to that object.
func TestObjectName_TraitReadsIt(t *testing.T) {
	for _, tt := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{name: "the authored name", props: map[string]any{"objectName": "authored"}, want: "authored"},
		{name: "the component name without one", want: "web"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			tr := NewTransformer(
				map[string]ComponentHandler{"widget": kindStub("widget", widgetKind, ObjectScopeNamespaced)},
				map[string]TraitHandler{"reads-object-name": objectNameTrait{got: &got}})
			comp := widget("web", tt.props)
			comp.Traits = []Trait{{Type: "reads-object-name"}}
			if _, _, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{}); err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got != tt.want {
				t.Errorf("Trait.ComponentObjectName() = %q, want %q", got, tt.want)
			}
		})
	}

	// Outside a transform there is no resolved name: the caller falls back.
	if got := (&Trait{Type: "reads-object-name"}).ComponentObjectName(); got != "" {
		t.Errorf("ComponentObjectName() on a trait built outside a transform = %q, want \"\"", got)
	}
}

// HandlerSchemas publishes `objectName` for a kind component and for no other
// type, without changing the handler's own schema.
func TestObjectName_HandlerSchemas(t *testing.T) {
	h := kindStub("widget", widgetKind, ObjectScopeNamespaced)
	tr := NewTransformer(map[string]ComponentHandler{"widget": h, "plain": &plainHandler{}}, nil)
	set := tr.HandlerSchemas()
	if p, ok := set.Components["widget"][ObjectNameProperty]; !ok || p.Type != PropertyTypeString {
		t.Errorf("the kind component's published schema has objectName = %+v (present %t), want a string property", p, ok)
	}
	if _, ok := set.Components["widget"]["size"]; !ok {
		t.Error("the kind component's published schema lost its own property")
	}
	if _, ok := set.Components["plain"][ObjectNameProperty]; ok {
		t.Error("a type that declares no object is published with objectName")
	}
	if _, ok := h.PropertySchema()[ObjectNameProperty]; ok {
		t.Error("the handler's own schema was changed")
	}
}
