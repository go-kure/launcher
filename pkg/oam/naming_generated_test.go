package oam

import (
	"slices"
	"strings"
	"testing"
)

// The generated role puts the object a rule or a handler outside launcher's own
// generates to the hook, with its kind, and claims it as under any other role.
func TestNameRoleGenerated(t *testing.T) {
	widget := NameSpec{Role: NameRoleGenerated, Kind: widgetKind}

	t.Run("a lowering rule's object is put to the hook and claimed", func(t *testing.T) {
		h := newLoweringHarness(map[string]string{"db-widget": "theirs"})
		lctx := h.lctx("db")
		got, err := lctx.ResolveName("db", "widget", widget)
		if err != nil || got != "theirs" {
			t.Fatalf("ResolveName = %q, %v; want the hook's name", got, err)
		}
		want := []NameRequest{{Application: "shop", Component: "db", Role: NameRoleGenerated, Kind: "Widget.example.com", Default: "db-widget"}}
		if !slices.Equal(h.asked, want) {
			t.Errorf("the hook was asked %+v, want %+v", h.asked, want)
		}
		_, err = lctx.ResolveName("db", "gadget", NameSpec{Role: NameRoleGenerated, Kind: widgetKind, Property: "widgetName", Authored: "theirs"})
		if err == nil || !strings.Contains(err.Error(), `name collision: Widget.example.com "theirs"`) ||
			!strings.Contains(err.Error(), `role "generated"`) {
			t.Fatalf("err = %v, want the collision on the generated Widget", err)
		}
	})

	t.Run("a trait's object is put to the hook and claimed", func(t *testing.T) {
		h := newNamingHarness(map[string]string{"web-widget": "theirs"})
		spec := NameSpec{Role: NameRoleGenerated, Kind: widgetKind, Namespace: "default", Default: "web-widget"}
		got, err := h.trait("web", "acme-widget", 0).ResolveName(spec)
		if err != nil || got != "theirs" {
			t.Fatalf("ResolveName = %q, %v; want the hook's name", got, err)
		}
		want := []NameRequest{{Application: "shop", Component: "web", Role: NameRoleGenerated, Kind: "Widget.example.com", Default: "web-widget"}}
		if !slices.Equal(h.asked, want) {
			t.Errorf("the hook was asked %+v, want %+v", h.asked, want)
		}
		_, err = h.trait("api", "acme-widget", 0).ResolveName(NameSpec{Role: NameRoleGenerated, Kind: widgetKind, Namespace: "default", Default: "api-widget", Property: "name", Authored: "theirs"})
		if err == nil || !strings.Contains(err.Error(), `name collision: Widget.example.com "default/theirs"`) {
			t.Fatalf("err = %v, want the collision on the generated Widget", err)
		}
	})

	t.Run("the role names an object, so the spec needs its kind", func(t *testing.T) {
		h := newLoweringHarness(nil)
		_, err := h.lctx("db").ResolveName("db", "widget", NameSpec{Role: NameRoleGenerated})
		if err == nil || !strings.Contains(err.Error(), `role "generated" names an object, and its NameSpec has no Kind`) {
			t.Fatalf("err = %v, want the missing Kind refused", err)
		}
	})

	t.Run("a hook answer is held to a subdomain", func(t *testing.T) {
		h := newLoweringHarness(map[string]string{"db-widget": "Not_A_Name"})
		_, err := h.lctx("db").ResolveName("db", "widget", widget)
		if err == nil || !strings.Contains(err.Error(), `the Naming hook returned "Not_A_Name" for role "generated"`) {
			t.Fatalf("err = %v, want the hook's answer refused", err)
		}
	})
}
