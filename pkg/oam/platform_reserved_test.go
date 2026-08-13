package oam

import (
	stderrors "errors"
	"strings"
	"testing"
)

// --- C5 unit proofs: enforcePlatformReserved ------------------------------

func reservedSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"hostname":      {Type: PropertyTypeString, Description: "Authored freely."},
		"networkPolicy": {Type: PropertyTypeObject, PlatformReserved: true, AdditionalProperties: true, Description: "Platform-supplied."},
		"tls": {
			Type:        PropertyTypeObject,
			Description: "Nested declaration carrying its own reservation.",
			Properties: map[string]PropertySchema{
				"hosts":      {Type: PropertyTypeArray, Description: "Authored freely."},
				"secretName": {Type: PropertyTypeString, PlatformReserved: true, Description: "Platform-supplied."},
			},
		},
	}
}

// TestEnforcePlatformReserved covers what the check does and — as importantly — what
// it deliberately leaves to validateProperties.
func TestEnforcePlatformReserved(t *testing.T) {
	tests := []struct {
		name     string
		props    map[string]any
		wantKey  string // "" means the props must be accepted
		wantPath string
	}{
		{
			name:  "nothing authored",
			props: nil,
		},
		{
			name:  "only unreserved properties authored",
			props: map[string]any{"hostname": "shop.example.com"},
		},
		{
			name:    "reserved property authored",
			props:   map[string]any{"networkPolicy": map[string]any{"trafficSources": []any{}}},
			wantKey: "networkPolicy", wantPath: "properties",
		},
		{
			// Presence is the violation, not the value: this is the inverse of the
			// Required rule, where an explicit null counts as absent.
			name:    "reserved property authored as an explicit null",
			props:   map[string]any{"networkPolicy": nil},
			wantKey: "networkPolicy", wantPath: "properties",
		},
		{
			name:    "reserved property nested in a declared object",
			props:   map[string]any{"tls": map[string]any{"secretName": "shop-tls"}},
			wantKey: "secretName", wantPath: "properties.tls",
		},
		{
			// A rule assembling properties in Go writes map[string]string; the walk
			// must see through it exactly as validateObjectProperties does.
			name:    "reserved property nested in a Go-typed map",
			props:   map[string]any{"tls": map[string]string{"secretName": "shop-tls"}},
			wantKey: "secretName", wantPath: "properties.tls",
		},
		{
			name:  "unreserved sibling of a nested reserved property",
			props: map[string]any{"tls": map[string]any{"hosts": []any{"shop.example.com"}}},
		},
		{
			// Undeclared keys belong to validateProperties, which names the accepted
			// set. Reporting them here would give the right rejection for the wrong
			// reason.
			name:  "undeclared key is not this check's business",
			props: map[string]any{"nosuchkey": true},
		},
		{
			// Likewise a type mismatch: the nested walk steps over it rather than
			// duplicating validatePropertyValue's message.
			name:  "declared object authored as a scalar",
			props: map[string]any{"tls": "not-an-object"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := enforcePlatformReserved(reservedSchema(), tc.props, "properties")
			if tc.wantKey == "" {
				if err != nil {
					t.Fatalf("expected the properties to be accepted, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %q to be rejected as platform-reserved", tc.wantKey)
			}
			if !stderrors.Is(err, ErrPlatformReserved) {
				t.Errorf("expected the error to wrap ErrPlatformReserved, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("expected the error to name %q, got: %v", tc.wantKey, err)
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("expected the error to cite path %q, got: %v", tc.wantPath, err)
			}
		})
	}
}

// TestEnforcePlatformReserved_EmptySchemaAcceptsAnything pins the "a handler that
// declares nothing reserves nothing" latitude — the same latitude
// validateEmittedProperties gives a handler with no PropertySchemaProvider.
func TestEnforcePlatformReserved_EmptySchemaAcceptsAnything(t *testing.T) {
	if err := enforcePlatformReserved(nil, map[string]any{"networkPolicy": map[string]any{}}, "properties"); err != nil {
		t.Fatalf("an empty schema must reserve nothing, got: %v", err)
	}
}

// TestEnforcePlatformReserved_ReportsTheSameViolationEveryRun guards the sorted walk:
// with two reserved keys authored at once, Go's randomized map iteration must not make
// the reported key vary between runs.
func TestEnforcePlatformReserved_ReportsTheSameViolationEveryRun(t *testing.T) {
	schema := map[string]PropertySchema{
		"alpha": {Type: PropertyTypeString, PlatformReserved: true},
		"omega": {Type: PropertyTypeString, PlatformReserved: true},
	}
	props := map[string]any{"alpha": "a", "omega": "o"}

	for i := range 50 {
		err := enforcePlatformReserved(schema, props, "properties")
		if err == nil {
			t.Fatalf("run %d: expected a rejection", i)
		}
		if !strings.Contains(err.Error(), "alpha") {
			t.Fatalf("run %d: expected the first sorted violation (\"alpha\"), got: %v", i, err)
		}
	}
}

// --- C5 engine proofs: the trait-lowering call site ------------------------

// reservingRule is a trait-position lowering rule that declares one platform-reserved
// property, so the engine's D3 check has something to enforce. It records the
// properties it was handed, which is how the capability-supplied case proves the value
// still arrives — enforcement runs on what was AUTHORED, before the merge.
type reservingRule struct{ seen map[string]any }

func (r *reservingRule) TraitType() string { return "reserving-trait" }

func (r *reservingRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"hostname":      {Type: PropertyTypeString, Description: "Authored freely."},
		"networkPolicy": {Type: PropertyTypeObject, PlatformReserved: true, AdditionalProperties: true, Description: "Platform-supplied."},
	}
}

func (r *reservingRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	r.seen = trait.Properties
	return LoweringResult{Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}, nil
}

func reservingApp(props map[string]any) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
				Traits:     []Trait{{Type: "reserving-trait", Properties: props}},
			}},
		},
	}
}

// TestLower_RejectsAuthoredPlatformReservedTraitProperty is the end-to-end D3 proof at
// the trait-lowering position: an authored value for a reserved property fails the
// build, citing the authored origin first (D7).
func TestLower_RejectsAuthoredPlatformReservedTraitProperty(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(&reservingRule{})

	app := reservingApp(map[string]any{
		"networkPolicy": map[string]any{"trafficSources": []any{}},
	})

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected an authored platform-reserved property to be rejected")
	}
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Errorf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{`trait "reserving-trait"`, `component "web"`, "networkPolicy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected the error to contain %q, got: %v", want, msg)
		}
	}
}

// TestLower_AllowsPlatformSuppliedReservedTraitProperty is the other half, and the
// reason the check runs where it does: the platform's own value for the very same
// property is accepted, because it arrives through capability rendering AFTER the
// authored document has been checked.
func TestLower_AllowsPlatformSuppliedReservedTraitProperty(t *testing.T) {
	rule := &reservingRule{}
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(rule)

	ctx := TransformContext{Capabilities: map[string]CapabilityBinding{
		"reserving-trait": {Rendering: map[string]any{
			"networkPolicy": map[string]any{
				"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
			},
		}},
	}}

	if _, err := tr.lower(reservingApp(map[string]any{"hostname": "shop.example.com"}), ctx); err != nil {
		t.Fatalf("a platform-supplied reserved property must be accepted, got: %v", err)
	}
	if rule.seen["networkPolicy"] == nil {
		t.Fatalf("expected the capability-supplied reserved value to reach the rule, got properties: %#v", rule.seen)
	}
	if rule.seen["hostname"] != "shop.example.com" {
		t.Errorf("expected the authored unreserved property to survive the merge, got: %#v", rule.seen)
	}
}

// TestLower_UnreservedTraitPropertiesStayAuthorable pins the scope of the check: only
// properties a schema marks reserved are refused, not every property a capability
// could also supply.
func TestLower_UnreservedTraitPropertiesStayAuthorable(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(&reservingRule{})

	if _, err := tr.lower(reservingApp(map[string]any{"hostname": "shop.example.com"}), TransformContext{}); err != nil {
		t.Fatalf("an unreserved authored property must be accepted, got: %v", err)
	}
}

// TestLower_RuleWithoutSchemaReservesNothing documents that PropertySchemaProvider is
// optional at this call site too: a lowering rule that declares no schema accepts
// whatever was authored, exactly as an unschema'd handler does.
func TestLower_RuleWithoutSchemaReservesNothing(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(extraTypesTraitRule{typeName: "reserving-trait"})

	app := reservingApp(map[string]any{"networkPolicy": map[string]any{"trafficSources": []any{}}})
	if _, err := tr.lower(app, TransformContext{}); err != nil {
		t.Fatalf("a rule declaring no schema must reserve nothing, got: %v", err)
	}
}
