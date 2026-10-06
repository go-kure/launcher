package oam

import (
	"strings"
	"testing"
)

// legacyHint is what the hinting fakes below say about the key "legacy", the
// one key they have a hint for.
const legacyHint = `"legacy" was replaced by "modern"`

func legacyOnlyHint(key string) string {
	if key == "legacy" {
		return legacyHint
	}
	return ""
}

// The three fakes add a hint to a schema fake that has none, so each position
// has a control of the same schema.

type hintingComponentRule struct{ schemaComponentLoweringRule }

func (hintingComponentRule) UnsupportedFieldHint(key string) string { return legacyOnlyHint(key) }

type hintingTrait struct{ schemaTrait }

func (hintingTrait) UnsupportedFieldHint(key string) string { return legacyOnlyHint(key) }

type hintingTraitRule struct{ schemaTraitLoweringRule }

func (hintingTraitRule) UnsupportedFieldHint(key string) string { return legacyOnlyHint(key) }

// TestValidateAuthoredProperties_UnsupportedFieldHintPositions pins where the
// document check appends a handler's hint to its refusal of an undeclared
// top-level key (go-kure/launcher#790): at the component lowering rule, and at
// the trait position for a handler and for a lowering rule, on both of that
// position's paths (with a ClusterProfile's capabilities and without). The
// terminal component handler position is the helmchart's, pinned beside it.
//
// The refusal itself is unchanged: the hint follows it after "; ". A key the
// hinter has no hint for, a type that is no hinter, and an error that refuses
// no undeclared key all read as they did.
func TestValidateAuthoredProperties_UnsupportedFieldHintPositions(t *testing.T) {
	newTransformer := func() *Transformer {
		tr := newSchemaTransformer()
		tr.RegisterComponentLowering(hintingComponentRule{schemaComponentLoweringRule{typ: "widget"}})
		tr.RegisterComponentLowering(schemaComponentLoweringRule{typ: "gadget"})
		tr.RegisterTrait("claim", hintingTrait{schemaTrait{typ: "claim"}})
		tr.RegisterTraitLowering(hintingTraitRule{schemaTraitLoweringRule{typ: "route"}})
		tr.RegisterTraitLowering(schemaTraitLoweringRule{typ: "plainroute"})
		return tr
	}
	onTrait := func(traitType string, props map[string]any) *Application {
		return authoredApp("webservice", map[string]any{"image": "nginx"}, Trait{Type: traitType, Properties: props})
	}
	// Any binding sends the trait position down its capability path; none of
	// these matches a trait below.
	bindings := map[string]CapabilityBinding{"unrelated": {}}

	cases := []struct {
		name string
		app  *Application
		// refusal is the generic text up to its allowed list; "" means the
		// document is accepted or refused for another reason (other).
		refusal  string
		wantHint bool
		other    string
	}{
		{
			name:     "component lowering rule, hinted key",
			app:      authoredApp("widget", map[string]any{"legacy": 1}),
			refusal:  `component "web" (type "widget"): properties: unsupported field "legacy" (allowed: replicas)`,
			wantHint: true,
		},
		{
			name:    "component lowering rule, a key it has no hint for",
			app:     authoredApp("widget", map[string]any{"replicaz": 1}),
			refusal: `component "web" (type "widget"): properties: unsupported field "replicaz" (allowed: replicas)`,
		},
		{
			name:    "component lowering rule that is no hinter",
			app:     authoredApp("gadget", map[string]any{"legacy": 1}),
			refusal: `component "web" (type "gadget"): properties: unsupported field "legacy" (allowed: replicas)`,
		},
		{
			name:  "component lowering rule, a declared key of the wrong type",
			app:   authoredApp("widget", map[string]any{"replicas": "three"}),
			other: `properties.replicas: expected integer`,
		},
		{
			name:     "trait handler, hinted key",
			app:      onTrait("claim", map[string]any{"size": "1Gi", "legacy": 1}),
			refusal:  `component "web": trait "claim": properties: unsupported field "legacy" (allowed: `,
			wantHint: true,
		},
		{
			name:    "trait handler, a key it has no hint for",
			app:     onTrait("claim", map[string]any{"size": "1Gi", "sizes": 1}),
			refusal: `component "web": trait "claim": properties: unsupported field "sizes" (allowed: `,
		},
		{
			name:    "trait handler that is no hinter",
			app:     onTrait("pvc", map[string]any{"size": "1Gi", "legacy": 1}),
			refusal: `component "web": trait "pvc": properties: unsupported field "legacy" (allowed: `,
		},
		{
			name:  "trait handler, a declared key of the wrong type",
			app:   onTrait("claim", map[string]any{"size": 1}),
			other: `properties.size: expected string`,
		},
		{
			name:     "trait lowering rule, hinted key",
			app:      onTrait("route", map[string]any{"hostnames": []any{"a"}, "legacy": 1}),
			refusal:  `component "web": trait "route": properties: unsupported field "legacy" (allowed: `,
			wantHint: true,
		},
		{
			name:    "trait lowering rule that is no hinter",
			app:     onTrait("plainroute", map[string]any{"hostnames": []any{"a"}, "legacy": 1}),
			refusal: `component "web": trait "plainroute": properties: unsupported field "legacy" (allowed: `,
		},
	}
	for _, tc := range cases {
		for _, path := range []struct {
			name     string
			bindings map[string]CapabilityBinding
		}{
			{"without capabilities", nil},
			{"with capabilities", bindings},
		} {
			t.Run(tc.name+", "+path.name, func(t *testing.T) {
				err := newTransformer().ValidateAuthoredPropertiesWithCapabilities(tc.app, path.bindings)
				if err == nil {
					t.Fatal("the document was accepted, want it refused")
				}
				got := err.Error()
				if tc.other != "" {
					if !strings.Contains(got, tc.other) || strings.Contains(got, legacyHint) {
						t.Fatalf("error = %q, want it to contain %q and no hint", got, tc.other)
					}
					return
				}
				if !strings.HasPrefix(got, tc.refusal) {
					t.Fatalf("error = %q, want it to begin with the refusal %q", got, tc.refusal)
				}
				hinted := strings.HasSuffix(got, "; "+legacyHint)
				if hinted != tc.wantHint || strings.Count(got, legacyHint) > 1 {
					t.Errorf("error = %q, hint appended = %v, want %v and at most once", got, hinted, tc.wantHint)
				}
				if !tc.wantHint && !strings.HasSuffix(got, ")") {
					t.Errorf("error = %q, want the refusal to end with its allowed list, as before", got)
				}
			})
		}
	}
}
