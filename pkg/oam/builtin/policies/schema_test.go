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
		"dependency":     &policies.DependencyHandler{},
		"placement":      &policies.PlacementHandler{},
		"health-checks":  &policies.HealthChecksHandler{},
		"reconciliation": &policies.ReconciliationSettingsHandler{},
	}
	wantKeys := map[string][]string{
		"dependency":     {"rules"},
		"placement":      {"component", "tier"},
		"health-checks":  {"checks"},
		"reconciliation": {"interval", "retryInterval", "timeout", "prune", "wait", "force", "suspend"},
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

	// health-checks.checks[]: apiVersion/kind/name required strings, namespace optional.
	checks := handlers["health-checks"].PropertySchema()["checks"]
	if checks.Type != oam.PropertyTypeArray || !checks.Required || checks.Items == nil {
		t.Fatalf("health-checks.checks: want required array with Items, got %+v", checks)
	}
	for _, k := range []string{"apiVersion", "kind", "name"} {
		if p := checks.Items.Properties[k]; p.Type != oam.PropertyTypeString || !p.Required {
			t.Errorf("health-checks.checks[].%s: want required string, got type=%v required=%v", k, p.Type, p.Required)
		}
	}
	if ns := checks.Items.Properties["namespace"]; ns.Type != oam.PropertyTypeString || ns.Required {
		t.Errorf("health-checks.checks[].namespace: want optional string, got type=%v required=%v", ns.Type, ns.Required)
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

	// reconciliation: every key optional; durations are strings, the rest booleans.
	for k, p := range handlers["reconciliation"].PropertySchema() {
		if p.Required {
			t.Errorf("reconciliation.%s: want optional", k)
		}
		want := oam.PropertyTypeBoolean
		if k == "interval" || k == "retryInterval" || k == "timeout" {
			want = oam.PropertyTypeString
		}
		if p.Type != want {
			t.Errorf("reconciliation.%s: type %v, want %v", k, p.Type, want)
		}
	}
}
