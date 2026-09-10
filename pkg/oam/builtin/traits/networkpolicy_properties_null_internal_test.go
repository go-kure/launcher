package traits

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// The top-level `ingress`/`egress` keys, one level above the peers pinned in
// networkpolicy_peer_null_internal_test.go. They are optional individually and
// required jointly, which makes this the one guard in the file a null could
// SATISFY while contributing nothing: `at least one of 'ingress' or 'egress' must
// be specified` is keyed on key presence, and a typed nil is present.
//
// The two nil shapes used to disagree here in a way no document can express. A
// typed nil satisfied `.([]any)` with ok=true and a nil slice whose range body
// never runs, so the trait emitted a NetworkPolicy selecting the component's pods
// with no rules at all — a default-deny nobody authored. An untyped nil failed the
// same assertion and reported `'ingress' must be an array`, which is a mistyped-key
// diagnostic for a key that is absent.

func npProps(t *testing.T, props map[string]any) (*NetworkPolicyConfig, error) {
	t.Helper()
	h := &NetworkPolicyHandler{}
	return h.parseProperties(props, &stack.Application{Name: "web"})
}

// Both keys, every time. The two guards are separate statements, so a test that
// only nulls `ingress` pins only half the fix; each key's subtests were confirmed
// to fail when that key's guard alone is disabled, and the other key's do not.
var npNullShapes = map[string]any{"untyped nil": nil, "typed nil": []any(nil)}

func TestParseProperties_NullRuleKeyDoesNotSatisfyTheJointRequirement(t *testing.T) {
	// The defect: `ingress:` with no value is not a NetworkPolicy with zero rules,
	// it is a document that specified neither key. Both shapes must reach the joint
	// requirement rather than passing it.
	for _, key := range []string{"ingress", "egress"} {
		for shape, value := range npNullShapes {
			t.Run(key+"/"+shape, func(t *testing.T) {
				_, err := npProps(t, map[string]any{key: value})
				if err == nil {
					t.Fatalf("a null '%s' alone must be rejected; it emitted a policy with no rules, which denies all traffic for the component", key)
				}
				if got := err.Error(); got != "at least one of 'ingress' or 'egress' must be specified" {
					t.Errorf("wrong diagnostic for an absent key: %q", got)
				}
			})
		}
	}
}

func TestParseProperties_NullRuleKeyIsAbsentBesideARealOne(t *testing.T) {
	// The other half of "null is absence": a null next to a populated sibling is
	// simply not there, and must not turn into an error or into an empty rule list
	// that changes what the sibling means.
	for _, pair := range []struct{ null, real string }{{"ingress", "egress"}, {"egress", "ingress"}} {
		for shape, value := range npNullShapes {
			t.Run(pair.null+"/"+shape, func(t *testing.T) {
				config, err := npProps(t, map[string]any{
					pair.null: value,
					pair.real: []any{map[string]any{}},
				})
				if err != nil {
					t.Fatalf("a null '%s' beside a real '%s' must parse, got: %v", pair.null, pair.real, err)
				}
				// Ingress and Egress are distinct rule types, so compare through
				// nil-ness and length rather than a shared variable.
				nullIsNil, nullLen := config.Ingress == nil, len(config.Ingress)
				realLen := len(config.Egress)
				if pair.null == "egress" {
					nullIsNil, nullLen = config.Egress == nil, len(config.Egress)
					realLen = len(config.Ingress)
				}
				if !nullIsNil {
					t.Errorf("null '%s' produced %d rules, want none", pair.null, nullLen)
				}
				if realLen != 1 {
					t.Errorf("'%s' lost rules to the null sibling: got %d, want 1", pair.real, realLen)
				}
			})
		}
	}
}

func TestParseProperties_EmptyRuleListStaysPresent(t *testing.T) {
	// The control, and the reason the fix is keyed on nil-ness rather than on
	// emptiness. An authored `ingress: []` is a present, empty rule list — a real,
	// expressible value meaning "select these pods and permit no ingress". It must
	// still satisfy the joint requirement, which a length check would break.
	config, err := npProps(t, map[string]any{"ingress": []any{}})
	if err != nil {
		t.Fatalf("an authored empty 'ingress' list must satisfy the joint requirement, got: %v", err)
	}
	if len(config.Ingress) != 0 {
		t.Errorf("empty 'ingress' produced %d rules, want 0", len(config.Ingress))
	}
}

func TestParseProperties_MistypedRuleKeyStillReportsAsMistyped(t *testing.T) {
	// The second control: null became absence, but a non-null value of the wrong
	// type must keep its own diagnostic rather than being folded into the joint
	// requirement's message.
	_, err := npProps(t, map[string]any{"ingress": "not-a-list"})
	if err == nil {
		t.Fatal("a string 'ingress' must be rejected")
	}
	if got := err.Error(); got != "'ingress' must be an array" {
		t.Errorf("mistyped 'ingress' reported %q, want the mistyped-key diagnostic", got)
	}
}
