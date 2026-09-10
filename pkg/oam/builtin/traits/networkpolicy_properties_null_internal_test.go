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
// only nulls `ingress` pins only half the fix.
//
// Measured, by deleting one guard at a time and running this package: with the
// `ingress` guard gone, both ingress subtests of the joint-requirement test below
// fail and no egress subtest does; the `egress` guard is symmetric. The
// companion test after it is weaker, and deliberately said so
// rather than being credited with discrimination it does not have: only its
// UNTYPED-nil rows fail, because a typed nil still satisfies the `[]any` assertion
// and ranges to nothing, leaving the same nil config field the guard produces.
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

func TestParseProperties_NullRuleElementIsRejected(t *testing.T) {
	// One level BELOW the keys above and one ABOVE the peers: an element of the
	// rule list itself. A typed nil satisfied `.(map[string]any)` with a nil map,
	// whose `from` and `ports` reads then both miss — producing a rule with no
	// peers and no ports, which networking.k8s.io/v1 defines as matching ALL
	// sources on ALL ports. An untyped nil failed the same assertion and was
	// rejected. Same divergence as the peer envelope, one level up, and this one
	// fails OPEN (go-kure/launcher#430).
	for _, key := range []string{"ingress", "egress"} {
		for shape, value := range map[string]any{"untyped nil": nil, "typed nil": map[string]any(nil)} {
			t.Run(key+"/"+shape, func(t *testing.T) {
				_, err := npProps(t, map[string]any{key: []any{value}})
				if err == nil {
					t.Fatalf("a null '%s' rule element must be rejected; an empty rule matches all sources on all ports", key)
				}
				if got, want := err.Error(), key+"[0]: expected object"; got != want {
					t.Errorf("diagnostic = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestParseProperties_NullPortElementIsRejected(t *testing.T) {
	// The third list this trait parses. Both shapes were already rejected here, so
	// this pins the DIAGNOSTIC rather than a behaviour change: every list element
	// in this file now reports "expected object" for a null instead of one of them
	// complaining about a missing 'port'.
	for shape, value := range map[string]any{"untyped nil": nil, "typed nil": map[string]any(nil)} {
		t.Run(shape, func(t *testing.T) {
			_, err := npProps(t, map[string]any{
				"ingress": []any{map[string]any{"ports": []any{value}}},
			})
			if err == nil {
				t.Fatal("a null port element must be rejected")
			}
			if got, want := err.Error(), "ingress[0].ports[0]: expected object"; got != want {
				t.Errorf("diagnostic = %q, want %q", got, want)
			}
		})
	}
}

func TestParseProperties_EmptyRuleObjectStillParses(t *testing.T) {
	// The control for the test above, and the reason the guard is keyed on
	// nil-ness rather than on emptiness. An authored `- {}` rule is a present,
	// empty rule — a real, expressible allow-all this parser has always accepted.
	config, err := npProps(t, map[string]any{"ingress": []any{map[string]any{}}})
	if err != nil {
		t.Fatalf("an authored empty ingress rule must still parse, got: %v", err)
	}
	if len(config.Ingress) != 1 {
		t.Fatalf("authored empty rule produced %d rules, want 1", len(config.Ingress))
	}
	if config.Ingress[0].From != nil || config.Ingress[0].Ports != nil {
		t.Errorf("authored empty rule produced %+v, want both fields nil", config.Ingress[0])
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

func TestParseProperties_MistypedPeerOrPortListIsRejected(t *testing.T) {
	// The rule's two list-valued keys, read with a bare comma-ok until now: a
	// wrong-typed value was DISCARDED, and both keys are constraints, so the
	// discard rendered a rule wider than the document authored. A rule with no
	// `from` matches all sources and one with no `ports` matches all ports
	// (k8s.io/api networking/v1/types.go:112-130), so `from: "web"` — a plausible
	// mistake — used to parse into allow-all.
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"string from", map[string]any{"ingress": []any{map[string]any{"from": "web"}}},
			"ingress[0].from: expected array, got string"},
		{"object from", map[string]any{"ingress": []any{map[string]any{"from": map[string]any{"podSelector": map[string]any{}}}}},
			"ingress[0].from: expected array, got map[string]interface {}"},
		{"string to", map[string]any{"egress": []any{map[string]any{"to": "db"}}},
			"egress[0].to: expected array, got string"},
		{"numeric ingress ports", map[string]any{"ingress": []any{map[string]any{"ports": 8080}}},
			"ingress[0].ports: expected array, got int"},
		{"numeric egress ports", map[string]any{"egress": []any{map[string]any{"ports": 8080}}},
			"egress[0].ports: expected array, got int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := npProps(t, tc.props)
			if err == nil {
				t.Fatalf("a mistyped rule list must be rejected, got %+v", config)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseProperties_UnknownRuleKeyIsRejected(t *testing.T) {
	// The rule key set. `from`/`to` is the rule's only peer constraint, so a
	// misspelt one was dropped in silence and left a rule matching ALL sources —
	// the worst instance in this file of the class the key checks close. The
	// direction keys are deliberately not interchangeable: `to` inside an ingress
	// rule is a document that meant something else.
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"misspelt from", map[string]any{"ingress": []any{map[string]any{"frm": []any{}}}},
			`ingress[0]: unsupported key "frm"`},
		{"egress key in an ingress rule", map[string]any{"ingress": []any{map[string]any{"to": []any{}}}},
			`ingress[0]: unsupported key "to"`},
		{"ingress key in an egress rule", map[string]any{"egress": []any{map[string]any{"from": []any{}}}},
			`egress[0]: unsupported key "from"`},
		{"misspelt ports beside a real from", map[string]any{"ingress": []any{map[string]any{
			"from":  []any{map[string]any{"podSelector": map[string]any{}}},
			"portz": []any{},
		}}}, `ingress[0]: unsupported key "portz"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := npProps(t, tc.props)
			if err == nil {
				t.Fatalf("an unrecognized rule key must be rejected, got %+v", config)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseProperties_NullAndEmptyRuleListsStillParse(t *testing.T) {
	// The control for the two tests above. Absence of `from`/`ports` is legal and
	// meaningful — it is exactly the authored allow-all — so the new guards must
	// reject only the wrong-typed and unrecognized shapes. A null reads as absent
	// here as it does everywhere else, and an authored empty list is a present
	// value that must survive.
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"untyped nil", nil},
		{"typed nil", []any(nil)},
		{"authored empty list", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := npProps(t, map[string]any{
				"ingress": []any{map[string]any{"from": tc.value, "ports": tc.value}},
			})
			if err != nil {
				t.Fatalf("an absent-or-empty from/ports must parse, got: %v", err)
			}
			if len(config.Ingress) != 1 {
				t.Fatalf("produced %d rules, want 1", len(config.Ingress))
			}
			if config.Ingress[0].From != nil || config.Ingress[0].Ports != nil {
				t.Errorf("produced %+v, want both fields nil", config.Ingress[0])
			}
		})
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
