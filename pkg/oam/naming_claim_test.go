package oam

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ingressKind = schema.GroupKind{Group: "networking.k8s.io", Kind: "Ingress"}

// objectSpec is the spec the engine resolves a kind component's object under.
func objectSpec(kind schema.GroupKind, component string) (nameOwner, NameSpec) {
	return nameOwner{component: component, role: NameRoleObject, def: component},
		NameSpec{Role: NameRoleObject, Kind: kind, Namespace: "default", Default: component}
}

// TestClaimObjectName_HeldAgainstAResolvedName: a name a trait claimed and a
// name a role resolved are one claim space. Whichever comes second is refused,
// and the error names both in the order they came.
func TestClaimObjectName_HeldAgainstAResolvedName(t *testing.T) {
	const (
		byTrait     = `component "web" traits[0] "ingress" (its own object, its default name)`
		byComponent = `component "web-ingress" (role "object", its default)`
	)
	t.Run("trait first", func(t *testing.T) {
		h := newNamingHarness(nil)
		if err := h.trait("web", "ingress", 0).ClaimObjectName(ingressKind, "default", "web-ingress", ""); err != nil {
			t.Fatalf("ClaimObjectName: %v", err)
		}
		owner, spec := objectSpec(ingressKind, "web-ingress")
		_, err := h.resolver.resolve(owner, spec)
		want := `name collision: Ingress.networking.k8s.io "default/web-ingress" is named by ` + byTrait + ` and by ` + byComponent + `; give one of them another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant %s", err, want)
		}
	})
	t.Run("component first", func(t *testing.T) {
		h := newNamingHarness(nil)
		owner, spec := objectSpec(ingressKind, "web-ingress")
		if _, err := h.resolver.resolve(owner, spec); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		err := h.trait("web", "ingress", 0).ClaimObjectName(ingressKind, "default", "web-ingress", "")
		want := `name collision: Ingress.networking.k8s.io "default/web-ingress" is named by ` + byComponent + ` and by ` + byTrait + `; give one of them another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant %s", err, want)
		}
	})
}

// TestClaimObjectName_ClaimsWhatItIsGiven: the claim is of one kind, namespace
// and name. Another of any of the three is another object, and nothing about
// the name is asked of the hook.
func TestClaimObjectName_ClaimsWhatItIsGiven(t *testing.T) {
	h := newNamingHarness(map[string]string{"web-ingress": "renamed"})
	web := h.trait("web", "ingress", 0)
	if err := web.ClaimObjectName(ingressKind, "default", "web-ingress", ""); err != nil {
		t.Fatalf("ClaimObjectName: %v", err)
	}
	if len(h.asked) != 0 {
		t.Errorf("the Naming hook was asked %v; a claim resolves nothing", h.asked)
	}
	// The same trait, the same name: nothing new.
	if err := web.ClaimObjectName(ingressKind, "default", "web-ingress", ""); err != nil {
		t.Errorf("the same trait claiming its name again: %v", err)
	}
	api := h.trait("api", "ingress", 0)
	for name, claim := range map[string]func() error{
		"another name":      func() error { return api.ClaimObjectName(ingressKind, "default", "api-ingress", "") },
		"another namespace": func() error { return api.ClaimObjectName(ingressKind, "edge", "web-ingress", "") },
		"another kind":      func() error { return api.ClaimObjectName(hpaKind, "default", "web-ingress", "") },
	} {
		if err := claim(); err != nil {
			t.Errorf("%s: %v, want it accepted", name, err)
		}
	}
	err := api.ClaimObjectName(ingressKind, "default", "web-ingress", "name")
	const want = `name collision: Ingress.networking.k8s.io "default/web-ingress" is named by ` +
		`component "web" traits[0] "ingress" (its own object, its default name) and by ` +
		`component "api" traits[0] "ingress" (its own object, set by name); give one of them another name`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}

// TestClaimObjectName_OutsideATransform: a trait the engine did not apply has
// no claim space, and claims nothing, as ResolveName claims nothing there. A
// claim that names no kind or no name is a caller error either way.
func TestClaimObjectName_OutsideATransform(t *testing.T) {
	bare := &Trait{Type: "ingress"}
	for range 2 {
		if err := bare.ClaimObjectName(ingressKind, "default", "web-ingress", ""); err != nil {
			t.Fatalf("ClaimObjectName on a trait built outside a transform: %v", err)
		}
	}
	for _, tr := range []*Trait{bare, newNamingHarness(nil).trait("web", "ingress", 0)} {
		if err := tr.ClaimObjectName(schema.GroupKind{}, "default", "web-ingress", ""); err == nil || !strings.Contains(err.Error(), "has no Kind") {
			t.Errorf("a claim with no kind: %v, want it refused", err)
		}
		if err := tr.ClaimObjectName(ingressKind, "default", "", ""); err == nil || !strings.Contains(err.Error(), "has no name") {
			t.Errorf("a claim with no name: %v, want it refused", err)
		}
	}
}
