package traits

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestSchemaNetworkPolicy_ReservationComesFromTheCallSite is the C5/D3 correction in
// one assertion: the shared fragment carries whatever its caller states and nothing
// else. Everything except the flag is identical between the two answers, so a caller
// choosing `false` still gets the same property description — reservation is the only
// thing the argument decides.
func TestSchemaNetworkPolicy_ReservationComesFromTheCallSite(t *testing.T) {
	reserved := schemaNetworkPolicy(true)
	if !reserved.PlatformReserved {
		t.Error("schemaNetworkPolicy(true) must produce a platform-reserved property")
	}
	open := schemaNetworkPolicy(false)
	if open.PlatformReserved {
		t.Error("schemaNetworkPolicy(false) must produce an unreserved property")
	}

	open.PlatformReserved = true
	if got, want := len(open.Properties), len(reserved.Properties); got != want {
		t.Fatalf("the two answers must differ only in the flag: %d nested properties vs %d", got, want)
	}
	if open.Type != reserved.Type || open.Description != reserved.Description {
		t.Errorf("the two answers must differ only in the flag: %+v vs %+v", open, reserved)
	}
}

// TestNetworkPolicy_ReservedAtEveryDeclaration pins the state ADR-035 ratified: every
// schema that shares the fragment states its own reservation, and today all three say
// true. A new sharer that forgets to say anything cannot exist — the parameter is
// required — but one that says `false` would land here as a deliberate, visible diff.
func TestNetworkPolicy_ReservedAtEveryDeclaration(t *testing.T) {
	declarations := map[string]oam.PropertySchemaProvider{
		"ingress":   &IngressHandler{},
		"httproute": &HTTPRouteHandler{},
		"expose":    &ExposeHandler{},
	}

	for name, provider := range declarations {
		t.Run(name, func(t *testing.T) {
			schema, ok := provider.PropertySchema()["networkPolicy"]
			if !ok {
				t.Fatalf("%s declares no networkPolicy property", name)
			}
			if !schema.PlatformReserved {
				t.Errorf("%s must declare networkPolicy platform-reserved", name)
			}
		})
	}
}

// The regression guard for the ~24 tests that author networkPolicy directly lives in
// platform_reserved_apply_test.go, which is in the external traits_test package where
// those tests' fixtures already are.
