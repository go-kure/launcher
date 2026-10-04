package policies_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

// TestPolicySchemas_Surface pins each built-in policy's property surface: the
// exact top-level keys plus the nested shapes, so an accidental schema regression
// (wrong type, dropped required flag, missing nested field) fails loudly.
func TestPolicySchemas_Surface(t *testing.T) {
	handlers := map[string]oam.PropertySchemaProvider{
		"dependency": &policies.DependencyHandler{},
		"placement":  &policies.PlacementHandler{},
	}
	wantKeys := map[string][]string{
		"dependency": {"rules"},
		"placement":  {"component", "tier"},
	}
	for policy, keys := range wantKeys {
		s := handlers[policy].PropertySchema()
		got := make([]string, 0, len(s))
		for k := range s {
			got = append(got, k)
		}
		sort.Strings(got)
		want := append([]string(nil), keys...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("policy %q keys: got %v, want %v", policy, got, want)
		}
	}

	// dependency.rules[] is a required array of objects whose component is a
	// required string and whose dependsOn is a required array of strings.
	rules := handlers["dependency"].PropertySchema()["rules"]
	if rules.Type != oam.PropertyTypeArray || !rules.Required {
		t.Errorf("dependency.rules: got type=%v required=%v, want array/required", rules.Type, rules.Required)
	}
	if rules.Items == nil || rules.Items.Type != oam.PropertyTypeObject {
		t.Fatalf("dependency.rules.Items: want object, got %+v", rules.Items)
	}
	if c := rules.Items.Properties["component"]; c.Type != oam.PropertyTypeString || !c.Required {
		t.Errorf("dependency.rules[].component: want required string, got type=%v required=%v", c.Type, c.Required)
	}
	dep := rules.Items.Properties["dependsOn"]
	if dep.Type != oam.PropertyTypeArray || !dep.Required || dep.Items == nil || dep.Items.Type != oam.PropertyTypeString {
		t.Errorf("dependency.rules[].dependsOn: want required array-of-string, got type=%v required=%v items=%+v", dep.Type, dep.Required, dep.Items)
	}

	// placement.tier enumerates exactly the launcher tiers and is required.
	tier := handlers["placement"].PropertySchema()["tier"]
	wantEnum := make([]any, 0, len(oam.TierOrder))
	for _, tr := range oam.TierOrder {
		wantEnum = append(wantEnum, string(tr))
	}
	if !tier.Required || !reflect.DeepEqual(tier.Enum, wantEnum) {
		t.Errorf("placement.tier: want required with enum %v, got required=%v enum=%v", wantEnum, tier.Required, tier.Enum)
	}
}
