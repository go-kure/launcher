package oam

import (
	"strings"
	"testing"
)

// authoredApp builds a one-component Application around the supplied properties and
// traits, so each case below reads as the document an author would have written.
func authoredApp(compType string, props map[string]any, traits ...Trait) *Application {
	return &Application{
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: compType, Properties: props, Traits: traits},
			},
		},
	}
}

// TestValidateAuthoredProperties is the table for go-kure/launcher#408: an authored
// property no handler declares must be a build error rather than a silent drop.
// Cases are grouped by the decision each one pins, because several of them look like
// holes until the reason is stated.
func TestValidateAuthoredProperties(t *testing.T) {
	tests := []struct {
		name    string
		app     *Application
		wantErr string // "" means accept
	}{
		// The check itself.
		{
			name: "undeclared component property is rejected",
			app:  authoredApp("webservice", map[string]any{"image": "nginx", "replicaz": 3}),
			// The allowed list is part of the contract: a typo's fix is in the message.
			wantErr: `component "web" (type "webservice"): properties: unsupported field "replicaz" (allowed: image)`,
		},
		{
			name:    "declared component property of the wrong type is rejected",
			app:     authoredApp("webservice", map[string]any{"image": 8080}),
			wantErr: `properties.image: expected string, got int`,
		},
		{
			name: "undeclared trait property is rejected, and the path names the component",
			app: authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: "pvc", Properties: map[string]any{"size": "1Gi", "storageclass": "fast"}}),
			wantErr: `component "web": trait "pvc": properties: unsupported field "storageclass"`,
		},
		{
			name: "undeclared key nested inside a declared object is rejected",
			app: authoredApp("rich", map[string]any{
				"resources": map[string]any{"cpu": "100m", "memmory": "1Gi"},
			}),
			wantErr: `unsupported field "memmory"`,
		},

		// Required: enforced nested, not at the top level. See the doc comment on
		// validateAuthoredProperties — ClusterProfile capability rendering merges into
		// a trait's TOP-LEVEL property map after this runs, so a required property the
		// platform supplies is legitimately absent from what the author wrote.
		{
			name: "omitted top-level Required is accepted on a component",
			app:  authoredApp("webservice", map[string]any{}),
		},
		{
			name: "omitted top-level Required is accepted on a trait",
			app: authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: "pvc", Properties: map[string]any{"accessModes": []any{"ReadWriteOnce"}}}),
		},
		{
			name: "omitted Required inside a declared object IS rejected",
			app: authoredApp("rich", map[string]any{
				"resources": map[string]any{"memory": "1Gi"},
			}),
			wantErr: `"cpu" is required`,
		},

		// Positions with no schema to check against. Each is a deliberate pass-over,
		// not an unchecked hole — an unknown type is rejected elsewhere (validate()'s
		// type allowlists, and validateSettled after the lowering fixpoint).
		{
			name: "a handler declaring no schema accepts anything",
			app:  authoredApp("plain", map[string]any{"anything": "at all"}),
		},
		{
			name: "a component type no handler and no rule claims is passed over",
			app:  authoredApp("no-such-type", map[string]any{"anything": "at all"}),
		},
		{
			name: "a trait type no handler and no rule claims is passed over",
			app: authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: "custom-capability-trait", Properties: map[string]any{"whatever": true}}),
		},

		// Ordering: components in document order, each component's own properties
		// before its traits, so a document with several problems always reports the
		// same one.
		{
			name: "a component's own property is reported before its trait's",
			app: authoredApp("webservice", map[string]any{"image": "nginx", "badcomp": 1},
				Trait{Type: "pvc", Properties: map[string]any{"badtrait": 1}}),
			wantErr: `unsupported field "badcomp"`,
		},
		{
			name: "nil application is accepted",
			app:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := newSchemaTransformer().ValidateAuthoredProperties(tc.app)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateAuthoredProperties_ConsultsLoweringRuleSchemas pins the fallback that
// makes the check reach higher-level kinds: when no terminal handler claims an
// authored type, the ComponentLoweringRule / TraitLoweringRule that will consume it
// supplies the schema. Without this the whole D5 lowerable surface would be exempt —
// exactly the kinds whose properties are most likely to be mistyped, since they are
// the newest.
func TestValidateAuthoredProperties_ConsultsLoweringRuleSchemas(t *testing.T) {
	tr := newSchemaTransformer()
	tr.RegisterComponentLowering(schemaComponentLoweringRule{typ: "widget"})
	tr.RegisterTraitLowering(schemaTraitLoweringRule{typ: "route"})

	err := tr.ValidateAuthoredProperties(authoredApp("widget", map[string]any{"replicaz": 1}))
	if err == nil || !strings.Contains(err.Error(), `unsupported field "replicaz"`) {
		t.Errorf("component lowering rule schema not consulted: err = %v", err)
	}

	err = tr.ValidateAuthoredProperties(authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "route", Properties: map[string]any{"hostnamez": []any{"a"}}}))
	if err == nil || !strings.Contains(err.Error(), `unsupported field "hostnamez"`) {
		t.Errorf("trait lowering rule schema not consulted: err = %v", err)
	}
}

// TestValidateAuthoredProperties_PoliciesArePassedOver is the deliberate carve-out,
// pinned so a later reader does not "fix" it. ApplicationPolicy is documented
// pass-through (types.go) and no production code registers a PolicyHandler, so
// launcher declares no schema for any policy type. Checking policies here would
// reject every policy ever written.
//
// The transformer used here DOES register a schema-carrying policy handler
// (newSchemaTransformer registers schemaPolicy for "dependency"), so this asserts the
// pass-over is by position, not by an accidental absence of a schema to find.
func TestValidateAuthoredProperties_PoliciesArePassedOver(t *testing.T) {
	app := &Application{
		Spec: ApplicationSpec{
			Policies: []ApplicationPolicy{
				{Name: "p", Type: "dependency", Properties: map[string]any{"not-in-the-schema": true}},
			},
		},
	}

	if err := newSchemaTransformer().ValidateAuthoredProperties(app); err != nil {
		t.Errorf("policies must be passed over, got %v", err)
	}
}

// TestValidateAuthoredProperties_WritesNormalizedValuesBack is the reason the loop
// assigns props[key] rather than discarding validatePropertyValue's return: the
// handler downstream must see the shape validation actually checked. A rule or a
// decoder can hand over a typed slice, and asArrayValue normalizes it to []any —
// dropping that would leave the handler reading the un-normalized original.
func TestValidateAuthoredProperties_WritesNormalizedValuesBack(t *testing.T) {
	props := map[string]any{
		"env": []map[string]any{{"name": "LOG_LEVEL", "value": "info"}},
	}

	if err := newSchemaTransformer().ValidateAuthoredProperties(authoredApp("rich", props)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := props["env"].([]any); !ok {
		t.Errorf("env = %T, want []any — the normalized value was not written back", props["env"])
	}
}
