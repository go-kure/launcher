package oam

import "testing"

// ResolveNameDefaulted resolves as ResolveName does, and says whether launcher's
// default stood: not for the author's name, and not for one the Naming hook
// gave, the default word for word included.
func TestTraitResolveNameDefaulted(t *testing.T) {
	spec := func(def string) NameSpec {
		return NameSpec{Role: NameRoleGenerated, Kind: widgetKind, Namespace: "default", Default: def}
	}
	authored := spec("web-widget")
	authored.Property, authored.Authored = "name", "own"

	inTransform := func(answers map[string]string) *Trait {
		return newNamingHarness(answers).trait("web", "acme-widget", 0)
	}
	outside := func(map[string]string) *Trait { return &Trait{Type: "acme-widget"} }

	for _, tc := range []struct {
		name      string
		trait     func(map[string]string) *Trait
		answers   map[string]string
		spec      NameSpec
		want      string
		defaulted bool
	}{
		{"the default", inTransform, nil, spec("web-widget"), "web-widget", true},
		{"the author's name", inTransform, nil, authored, "own", false},
		{"the hook's name", inTransform, map[string]string{"web-widget": "theirs"}, spec("web-widget"), "theirs", false},
		{"the hook answers with the default", inTransform, map[string]string{"web-widget": "web-widget"}, spec("web-widget"), "web-widget", false},
		{"outside a transform, the default", outside, nil, spec("web-widget"), "web-widget", true},
		{"outside a transform, the author's name", outside, nil, authored, "own", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, defaulted, err := tc.trait(tc.answers).ResolveNameDefaulted(tc.spec)
			if err != nil || got != tc.want || defaulted != tc.defaulted {
				t.Errorf("ResolveNameDefaulted = %q, %v, %v; want %q, %v", got, defaulted, err, tc.want, tc.defaulted)
			}
			// ResolveName is unchanged: the same name from a fresh claim space.
			plain, err := tc.trait(tc.answers).ResolveName(tc.spec)
			if err != nil || plain != tc.want {
				t.Errorf("ResolveName = %q, %v; want %q", plain, err, tc.want)
			}
		})
	}
}
