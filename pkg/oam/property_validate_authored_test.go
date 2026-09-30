package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
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
		{
			// This function calls validatePropertyValue directly, for every
			// authored key, with no strip of its own first (unlike
			// validateObjectProperties, whose own callers never see a null
			// property value). validatePropertyValue's own top-level null check
			// is what makes this pass rather than fail with "expected integer,
			// got <nil>" — regression test for that early return.
			name: "authored optional scalar explicit null is accepted, not a type error",
			app: authoredApp("rich", map[string]any{
				"replicas": nil,
			}),
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

// TestValidateAuthoredProperties_Policies pins the policy position: an authored
// policy is checked against the schema its registered PolicyHandler (or the
// PolicyLoweringRule claiming its type) declares, so a misspelt or wrongly typed key
// is a build error instead of a setting the handler never reads. newSchemaTransformer
// registers schemaPolicy for "dependency"; the other types are registered per case.
func TestValidateAuthoredProperties_Policies(t *testing.T) {
	policyApp := func(pols ...ApplicationPolicy) *Application {
		return &Application{Spec: ApplicationSpec{Policies: pols}}
	}
	tests := []struct {
		name    string
		app     *Application
		wantErr string // "" means accept
	}{
		{
			name: "undeclared policy property is rejected, with the allowed list",
			app: policyApp(ApplicationPolicy{Name: "order", Type: "dependency",
				Properties: map[string]any{"components": []any{"api"}, "componentz": []any{"web"}}}),
			wantErr: `policy "order" (type "dependency"): properties: unsupported field "componentz" (allowed: components)`,
		},
		{
			name: "declared policy property of the wrong type is rejected",
			app: policyApp(ApplicationPolicy{Name: "order", Type: "dependency",
				Properties: map[string]any{"components": "api"}}),
			wantErr: `policy "order" (type "dependency"): properties.components: expected array, got string`,
		},
		{
			name: "a policy matching its schema is accepted",
			app: policyApp(ApplicationPolicy{Name: "order", Type: "dependency",
				Properties: map[string]any{"components": []any{"api", "web"}}}),
		},
		{
			// Same top-level rule as components: the handler reports a missing
			// property it needs with its own message, at transform time.
			name: "omitted top-level Required is accepted on a policy",
			app:  policyApp(ApplicationPolicy{Name: "order", Type: "dependency", Properties: map[string]any{}}),
		},
		{
			name: "a policy handler declaring no schema accepts anything",
			app: policyApp(ApplicationPolicy{Name: "p", Type: "schemaless",
				Properties: map[string]any{"anything": "at all"}}),
		},
		{
			// The transform rejects it ("no handler for policy type"); there is no
			// schema to check here.
			name: "a policy type nothing is registered for is passed over",
			app: policyApp(ApplicationPolicy{Name: "p", Type: "no-such-policy",
				Properties: map[string]any{"anything": "at all"}}),
		},
		{
			name: "a PolicyLoweringRule's schema is consulted when no handler claims the type",
			app: policyApp(ApplicationPolicy{Name: "p", Type: "higher-dependency",
				Properties: map[string]any{"ownr": "team-a"}}),
			wantErr: `policy "p" (type "higher-dependency"): properties: unsupported field "ownr" (allowed: owner)`,
		},
		{
			name: "policies are checked in document order",
			app: policyApp(
				ApplicationPolicy{Name: "first", Type: "dependency", Properties: map[string]any{"bad1": true}},
				ApplicationPolicy{Name: "second", Type: "dependency", Properties: map[string]any{"bad2": true}},
			),
			wantErr: `policy "first"`,
		},
		{
			name: "a component's error is reported before a policy's",
			app: &Application{Spec: ApplicationSpec{
				Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"badcomp": 1}}},
				Policies:   []ApplicationPolicy{{Name: "order", Type: "dependency", Properties: map[string]any{"badpol": 1}}},
			}},
			wantErr: `unsupported field "badcomp"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := newSchemaTransformer()
			tr.RegisterPolicy("schemaless", &stubPolicyHandler{typ: "schemaless"})
			tr.RegisterPolicyLowering(requiredSchemaPolicyLoweringRule{typ: "higher-dependency"})
			err := tr.ValidateAuthoredProperties(tc.app)
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

// sharedSchemaTrait returns the SAME schema map on every call, the way a handler
// caching or returning a package-level map would. schemaTrait builds a fresh map
// each call, so it cannot detect the engine-property merge writing into a handler's
// own schema; this one can. Used only by
// TestValidateAuthoredProperties_EngineScopeDoesNotMutateHandlerSchema.
type sharedSchemaTrait struct {
	typ    string
	schema map[string]PropertySchema
}

func (h sharedSchemaTrait) CanHandle(t string) bool                               { return t == h.typ }
func (h sharedSchemaTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }
func (h sharedSchemaTrait) PropertySchema() map[string]PropertySchema             { return h.schema }

// TestValidateAuthoredProperties_EngineScopeIsAcceptedOnAnyTrait is the regression
// test for the defect this check introduced and then had to fix: `scope` is read by
// the transform ENGINE off every authored trait (buildCapabilityKey builds
// "<type>.<scope>" for every trait type), not by the trait's handler, so validating a
// trait against its handler's schema alone rejected documents that build correctly.
//
// The gap was invisible in the existing tests because the only traits exercised with
// a `scope` (transform_test.go's ingress cases) belong to the three handlers —
// expose, ingress, httproute — that happen to declare `scope` themselves, for the
// unrelated purpose of disambiguating sub-application names. Every other trait type
// was broken. See testdata/pvc-trait-scoped in pkg/cmd/kurel for the end-to-end half:
// it pins that the SCOPED capability binding is the one that renders.
func TestValidateAuthoredProperties_EngineScopeIsAcceptedOnAnyTrait(t *testing.T) {
	// The pvc handler declares size and accessModes, and no `scope`.
	app := authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "pvc", Properties: map[string]any{"size": "1Gi", "scope": "fast"}})
	if err := newSchemaTransformer().ValidateAuthoredProperties(app); err != nil {
		t.Errorf("engine-read `scope` must be accepted on a trait whose handler does not declare it, got: %v", err)
	}

	// Same for a trait claimed by a lowering rule rather than a terminal handler:
	// resolveCapability runs in the lowering fixpoint too, so the property is just as
	// legal there.
	tr := newSchemaTransformer()
	tr.RegisterTraitLowering(schemaTraitLoweringRule{typ: "route"})
	app = authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "route", Properties: map[string]any{"hostnames": []any{"a"}, "scope": "public"}})
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		t.Errorf("engine-read `scope` must be accepted on a lowering-rule trait, got: %v", err)
	}

	// Typed, not merely tolerated. buildCapabilityKey type-asserts to string, so a
	// non-string `scope` is silently ignored today — the exact class of silent drop
	// this whole check exists to eliminate.
	app = authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "pvc", Properties: map[string]any{"size": "1Gi", "scope": 3}})
	err := tr.ValidateAuthoredProperties(app)
	if err == nil || !strings.Contains(err.Error(), "expected string, got int") {
		t.Errorf("a non-string `scope` must be rejected, got: %v", err)
	}

	// The allowed-list in the rejection message must name it, or the message tells an
	// author to delete a property that is in fact legal.
	app = authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "pvc", Properties: map[string]any{"scop": "fast"}})
	err = tr.ValidateAuthoredProperties(app)
	if err == nil || !strings.Contains(err.Error(), "allowed: accessModes, scope, size") {
		t.Errorf("the allowed list must name `scope`, got: %v", err)
	}

	// Trait position only. Nothing reads a `scope` off a COMPONENT — the two
	// resolveCapability call sites both take a Trait — so accepting it there would
	// re-open the silent drop for a property that does nothing.
	err = tr.ValidateAuthoredProperties(authoredApp("webservice", map[string]any{"image": "nginx", "scope": "fast"}))
	if err == nil || !strings.Contains(err.Error(), `unsupported field "scope"`) {
		t.Errorf("`scope` is a trait-level property and must not be accepted on a component, got: %v", err)
	}
}

// TestValidateAuthoredProperties_EngineScopeDoesNotMutateHandlerSchema pins the copy
// in withEngineTraitProperties. PropertySchema() may return a shared or cached map,
// and writing `scope` into it would leak the addition into HandlerSchemas() and every
// other consumer of that handler's schema — a `kurel schema`-style listing would then
// advertise `scope` as a property the handler itself declares.
func TestValidateAuthoredProperties_EngineScopeDoesNotMutateHandlerSchema(t *testing.T) {
	shared := map[string]PropertySchema{"size": {Type: PropertyTypeString}}
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": schemaComponent{typ: "webservice"}},
		map[string]TraitHandler{"pvc": sharedSchemaTrait{typ: "pvc", schema: shared}},
	)

	app := authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "pvc", Properties: map[string]any{"size": "1Gi", "scope": "fast"}})
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, leaked := shared["scope"]; leaked {
		t.Error("withEngineTraitProperties wrote into the handler's own schema map")
	}
}

// TestIsEngineTraitProperty pins the exported view of engineTraitProperties that a
// handler outside this package uses to let engine-owned keys through: every key of
// the map, and nothing else.
func TestIsEngineTraitProperty(t *testing.T) {
	for key := range engineTraitProperties {
		if !IsEngineTraitProperty(key) {
			t.Errorf("IsEngineTraitProperty(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "Scope", "size", "maxSkew"} {
		if IsEngineTraitProperty(key) {
			t.Errorf("IsEngineTraitProperty(%q) = true, want false", key)
		}
	}
}

// TestWithEngineTraitProperties_HandlerDeclarationWins covers the three handlers that
// declare `scope` themselves (expose, ingress, httproute): their own description and
// constraints must survive the merge, so the engine default never silently overrides
// a handler that documented the property for its own purpose.
func TestWithEngineTraitProperties_HandlerDeclarationWins(t *testing.T) {
	own := PropertySchema{Type: PropertyTypeString, Description: "the handler's own wording"}
	in := map[string]PropertySchema{"scope": own}

	out := withEngineTraitProperties(in)

	if out["scope"].Description != own.Description {
		t.Errorf("scope = %+v, want the handler's own declaration preserved", out["scope"])
	}
	// Nothing to add, so the input map is returned as-is rather than copied.
	if len(out) != len(in) {
		t.Errorf("len(out) = %d, want %d — no key should have been added", len(out), len(in))
	}
}

// TestValidateAuthoredProperties_EngineScopeCheckedWithoutHandlerSchema is
// go-kure/launcher#426. PropertySchemaProvider is optional, and a trait whose handler
// or lowering rule declares no schema used to skip the authored check entirely —
// including `scope`, which the ENGINE reads off every trait (buildCapabilityKey)
// whatever the handler declares. A non-string `scope` there was silently ignored and
// the trait resolved against the unscoped capability binding.
//
// No built-in reaches this (TestNewBuiltinTransformer_HandlerSchemaParity in
// pkg/cmd/kurel fails on a built-in without a schema), so both registrations go through
// the exported API an external consumer uses: RegisterTrait and RegisterTraitLowering.
func TestValidateAuthoredProperties_EngineScopeCheckedWithoutHandlerSchema(t *testing.T) {
	tr := newSchemaTransformer()
	tr.RegisterTrait("custom", &stubTraitHandler{typ: "custom"})
	tr.RegisterTraitLowering(stubTraitLoweringRule{typ: "custom-rule"})

	for _, traitType := range []string{"custom", "custom-rule"} {
		t.Run(traitType, func(t *testing.T) {
			// Half one: the engine-owned key is still type-checked.
			app := authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: traitType, Properties: map[string]any{"scope": 3}})
			err := tr.ValidateAuthoredProperties(app)
			want := `component "web": trait "` + traitType + `": properties.scope: expected string, got int`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("a non-string `scope` on a schema-less trait must be rejected with %q, got: %v", want, err)
			}

			// Half two: handler-owned keys stay unchecked — there is no schema to
			// check them against, so an undeclared key must not become an error here.
			app = authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: traitType, Properties: map[string]any{"scope": "fast", "anything": 1, "nested": map[string]any{"x": true}}})
			if err := tr.ValidateAuthoredProperties(app); err != nil {
				t.Errorf("handler-owned keys on a schema-less trait must stay accepted, got: %v", err)
			}

			// An explicit null is absent, exactly as on a trait whose handler declares
			// a schema — it resolves the unscoped binding like omitting the key does.
			app = authoredApp("webservice", map[string]any{"image": "nginx"},
				Trait{Type: traitType, Properties: map[string]any{"scope": nil}})
			if err := tr.ValidateAuthoredProperties(app); err != nil {
				t.Errorf("an explicit null `scope` must stay accepted, got: %v", err)
			}
		})
	}
}
