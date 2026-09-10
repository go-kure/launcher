package oam

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// schemaPolicy is a stub PolicyHandler that declares a property schema. The
// existing policy stubs (transform_test.go) declare none, and the policy position
// is one of the three emission points, so it needs its own schema-carrying fixture.
type schemaPolicy struct{ typ string }

func (h schemaPolicy) CanHandle(t string) bool { return t == h.typ }
func (h schemaPolicy) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}
func (h schemaPolicy) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"components": {Type: PropertyTypeArray, Required: true, Items: &PropertySchema{Type: PropertyTypeString}},
	}
}

// richComponent exercises the parts of the vocabulary the two existing fixtures do
// not reach: enum, number/boolean/integer scalars, a nested closed object, a nested
// open object (AdditionalProperties), and an array of objects.
type richComponent struct{ typ string }

func (h richComponent) CanHandle(t string) bool { return t == h.typ }
func (h richComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (h richComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"strategy": {Type: PropertyTypeString, Enum: []any{"rolling", "recreate"}},
		"replicas": {Type: PropertyTypeInteger},
		"weight":   {Type: PropertyTypeNumber},
		"enabled":  {Type: PropertyTypeBoolean},
		"port":     {Type: PropertyTypeInteger, Enum: []any{80, 443}},
		"resources": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"cpu":    {Type: PropertyTypeString, Required: true},
				"memory": {Type: PropertyTypeString},
			},
		},
		"labels": {
			Type:                 PropertyTypeObject,
			Properties:           map[string]PropertySchema{"tier": {Type: PropertyTypeString}},
			AdditionalProperties: true,
		},
		"env": {
			Type: PropertyTypeArray,
			Items: &PropertySchema{
				Type: PropertyTypeObject,
				Properties: map[string]PropertySchema{
					"name":  {Type: PropertyTypeString, Required: true},
					"value": {Type: PropertyTypeString},
				},
			},
		},
	}
}

func richSchema() map[string]PropertySchema { return richComponent{typ: "rich"}.PropertySchema() }

func TestValidateProperties_TopLevel(t *testing.T) {
	schema := schemaComponent{typ: "webservice"}.PropertySchema() // image: string, required

	tests := []struct {
		name    string
		props   map[string]any
		wantErr string // "" means accept
	}{
		{name: "valid", props: map[string]any{"image": "nginx"}},
		{name: "missing required", props: map[string]any{}, wantErr: `"image" is required`},
		{name: "nil required is absent", props: map[string]any{"image": nil}, wantErr: `"image" is required`},
		{name: "wrong type", props: map[string]any{"image": 7}, wantErr: "expected string, got int"},
		{
			name:    "undeclared key rejected at top level",
			props:   map[string]any{"image": "nginx", "imagee": "typo"},
			wantErr: `unsupported field "imagee" (allowed: image)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProperties(schema, tc.props, "properties")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected acceptance, got error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateProperties_RichVocabulary(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name: "fully populated document-shaped value",
			props: map[string]any{
				"strategy":  "rolling",
				"replicas":  3,
				"weight":    1.5,
				"enabled":   true,
				"port":      443,
				"resources": map[string]any{"cpu": "100m", "memory": "128Mi"},
				"labels":    map[string]any{"tier": "web", "anything": "goes"},
				"env":       []any{map[string]any{"name": "PORT", "value": "8080"}},
			},
		},
		{
			name:  "go-native slice and map, not decoder-shaped",
			props: map[string]any{"labels": map[string]string{"tier": "web"}},
		},
		{
			name:  "yaml float for an integer field",
			props: map[string]any{"replicas": float64(3)},
		},
		{
			name:    "fractional float is not an integer",
			props:   map[string]any{"replicas": 3.5},
			wantErr: "expected integer, got float64",
		},
		{
			name:    "enum member not allowed",
			props:   map[string]any{"strategy": "bluegreen"},
			wantErr: "not in allowed set",
		},
		{
			name:  "numeric enum matches across int and float",
			props: map[string]any{"port": float64(80)},
		},
		{
			name:    "numeric enum still rejects a non-member",
			props:   map[string]any{"port": 8080},
			wantErr: "not in allowed set",
		},
		{
			name:    "nested required field missing",
			props:   map[string]any{"resources": map[string]any{"memory": "128Mi"}},
			wantErr: `properties.resources: "cpu" is required`,
		},
		{
			name:    "nested undeclared key rejected when object is closed",
			props:   map[string]any{"resources": map[string]any{"cpu": "100m", "gpu": "1"}},
			wantErr: `properties.resources: unsupported field "gpu"`,
		},
		{
			name:    "array element validated against Items",
			props:   map[string]any{"env": []any{map[string]any{"value": "8080"}}},
			wantErr: `properties.env[0]: "name" is required`,
		},
		{
			name:    "array element path is indexed",
			props:   map[string]any{"env": []any{map[string]any{"name": "A"}, map[string]any{"name": 2}}},
			wantErr: "properties.env[1].name: expected string, got int",
		},
		{
			name:    "object where array expected",
			props:   map[string]any{"env": map[string]any{"name": "A"}},
			wantErr: "expected array, got map[string]interface {}",
		},
		{
			name:    "scalar where object expected",
			props:   map[string]any{"resources": "100m"},
			wantErr: "expected object, got string",
		},
		{
			name:    "boolean type enforced",
			props:   map[string]any{"enabled": "true"},
			wantErr: "expected boolean, got string",
		},
		{
			name:  "number accepts an integer",
			props: map[string]any{"weight": 2},
		},
		{
			name:    "number rejects a string",
			props:   map[string]any{"weight": "2"},
			wantErr: "expected number, got string",
		},
		{
			name:  "nil under an optional field is tolerated",
			props: map[string]any{"replicas": nil},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProperties(richSchema(), tc.props, "properties")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected acceptance, got error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidatePropertyValue_UnsupportedSchemaType proves a handler bug (a type
// outside the PropertyType vocabulary) fails loudly instead of silently leaving the
// field unvalidated forever.
func TestValidatePropertyValue_UnsupportedSchemaType(t *testing.T) {
	schema := map[string]PropertySchema{"weird": {Type: PropertyType("strng")}}
	err := validateProperties(schema, map[string]any{"weird": "x"}, "properties")
	if err == nil || !strings.Contains(err.Error(), "unsupported property type") {
		t.Fatalf("expected an unsupported-property-type error, got: %v", err)
	}
}

// TestValidatePropertyValue_EmptyObjectSchema is the regression test for the Codex
// review finding F6: when a PropertyTypeObject field declares no sub-schema
// (schema.Properties == nil), validation was skipped entirely, so every key was
// silently accepted regardless of AdditionalProperties — including its documented
// default of false (closed). A nil Properties map has no declared keys, so every key
// in the value is "not declared" by definition; AdditionalProperties alone must decide
// whether that is accepted, exactly as it already does for a non-nil-but-empty
// Properties map.
func TestValidatePropertyValue_EmptyObjectSchema(t *testing.T) {
	value := map[string]any{"extra": "x"}

	t.Run("AdditionalProperties false (the default) rejects an undeclared key", func(t *testing.T) {
		schema := PropertySchema{Type: PropertyTypeObject}
		_, err := validatePropertyValue(schema, value, "properties.field")
		if err == nil {
			t.Fatal("expected an undeclared-key error, got nil")
		}
		if !strings.Contains(err.Error(), `unsupported field "extra"`) {
			t.Fatalf("expected an unsupported-field error naming %q, got: %v", "extra", err)
		}
	})

	t.Run("AdditionalProperties true still accepts an undeclared key", func(t *testing.T) {
		schema := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true}
		if _, err := validatePropertyValue(schema, value, "properties.field"); err != nil {
			t.Fatalf("expected acceptance with AdditionalProperties:true, got: %v", err)
		}
	})
}

// TestValidateProperties_UntypedSchemaChecksEnumOnly covers a schema built by a call
// site that leaves Type empty (the flat capability vocabulary permits that).
func TestValidateProperties_UntypedSchemaChecksEnumOnly(t *testing.T) {
	schema := map[string]PropertySchema{"mode": {Enum: []any{"a", "b"}}}
	if err := validateProperties(schema, map[string]any{"mode": "a"}, "properties"); err != nil {
		t.Fatalf("expected acceptance of an enum member, got: %v", err)
	}
	if err := validateProperties(schema, map[string]any{"mode": "c"}, "properties"); err == nil {
		t.Fatal("expected rejection of a non-member")
	}
}

// TestValidateProperties_DeterministicMessage proves the reported problem does not
// depend on Go's map iteration order: two required fields are missing and the same
// one is always named.
func TestValidateProperties_DeterministicMessage(t *testing.T) {
	schema := map[string]PropertySchema{
		"alpha": {Type: PropertyTypeString, Required: true},
		"zulu":  {Type: PropertyTypeString, Required: true},
	}
	for i := range 50 {
		err := validateProperties(schema, map[string]any{}, "properties")
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), `"alpha" is required`) {
			t.Fatalf("run %d: expected the first sorted required field, got: %v", i, err)
		}
	}
}

// reservedComponent declares one optional PlatformReserved property, to pin the
// carve-out in the null normalisation below. No production schema declares a
// reserved property that is also optional-and-nullable, which is exactly why the
// behaviour needs a fixture: without one, changing the carve-out breaks nothing.
type reservedComponent struct{ typ string }

func (h reservedComponent) CanHandle(t string) bool { return t == h.typ }
func (h reservedComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (h reservedComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"registry": {Type: PropertyTypeString, PlatformReserved: true},
		"image":    {Type: PropertyTypeString},
	}
}

// TestValidateProperties_NullUnderOptionalKeyIsStripped is the presence half of the
// null contract. The assertion is deliberately on the two-value map lookup rather
// than on the value: a handler parser decides presence with exactly that lookup
// (builtin/components/common.go:1718-1721), so checking props["x"] == nil would pass
// against the very defect this closes.
func TestValidateProperties_NullUnderOptionalKeyIsStripped(t *testing.T) {
	cases := map[string]any{
		"untyped nil": nil,
		"nil slice":   []any(nil),
		"nil map":     map[string]any(nil),
	}
	for name, null := range cases {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"replicas": 2, "strategy": null}
			if err := validateProperties(richSchema(), props, "properties"); err != nil {
				t.Fatalf("expected acceptance, got: %v", err)
			}
			if _, present := props["strategy"]; present {
				t.Fatalf("expected the null key to be deleted, still present as %#v", props["strategy"])
			}
			if props["replicas"] != 2 {
				t.Fatalf("a non-null sibling was disturbed: %#v", props["replicas"])
			}
		})
	}
}

// The loop order in validateObjectProperties is load bearing: the Required check
// runs before the strip, so a required null keeps reporting the empty value rather
// than being deleted and then reported as a missing key.
func TestValidateProperties_NullUnderRequiredKeyStillFails(t *testing.T) {
	schema := map[string]PropertySchema{"cpu": {Type: PropertyTypeString, Required: true}}
	props := map[string]any{"cpu": nil}
	err := validateProperties(schema, props, "properties")
	if err == nil {
		t.Fatal("expected a required-field error")
	}
	if !strings.Contains(err.Error(), `"cpu" is required`) {
		t.Fatalf("expected the required message, got: %v", err)
	}
	if _, present := props["cpu"]; !present {
		t.Fatal("a required null must not be stripped out from under its own check")
	}
}

// TestNullAndPlatformReserved_DoNotMeet pins the boundary between the two rules,
// which is the reason the strip needs no PlatformReserved exception. They are
// asserted SEPARATELY and never composed: enforcePlatformReserved runs on a rule's
// INPUT (lowering.go:1083, :1206; transform.go:649, :891) and emission validation on
// its OUTPUT (lowering.go:1104, :1230), so running one after the other would assert a
// pipeline that does not exist — the mistake this file's scope note at :22-27 warns
// about.
//
// Reservation is a rule about what a user WROTE, so it keeps treating an explicit
// null as present; the strip is a rule about what a lowering rule EMITTED, so it
// treats one as absent. Exempting reserved keys from the strip would not have
// preserved the authored rule — it would only have handed a reserved null to the type
// switch, producing a loud rejection with the wrong reason, which is precisely what
// this file's "would duplicate that message with a misleading reason" note exists to
// avoid.
func TestNullAndPlatformReserved_DoNotMeet(t *testing.T) {
	schema := reservedComponent{typ: "reserved"}.PropertySchema()

	t.Run("emitted null is stripped, reserved or not", func(t *testing.T) {
		props := map[string]any{"registry": nil, "image": nil}
		if err := validateProperties(schema, props, "properties"); err != nil {
			t.Fatalf("expected acceptance, got: %v", err)
		}
		for _, key := range []string{"registry", "image"} {
			if _, present := props[key]; present {
				t.Errorf("expected %q to be deleted, still present as %#v", key, props[key])
			}
		}
	})

	t.Run("authored null is still refused", func(t *testing.T) {
		err := enforcePlatformReserved(schema, map[string]any{"registry": nil}, "properties")
		if err == nil {
			t.Fatal("expected an authored reserved null to be refused")
		}
		if !stderrors.Is(err, ErrPlatformReserved) {
			t.Fatalf("expected ErrPlatformReserved, got: %v", err)
		}
		if err := enforcePlatformReserved(schema, map[string]any{"image": nil}, "properties"); err != nil {
			t.Fatalf("an unreserved key is not this function's business, got: %v", err)
		}
	})
}

// TestValidateProperties_NullArrayElementIsRejected is the ruling's other half, and
// the divergence that prompted it: `values: [null]` cleared emission validation and
// then failed conversion in the handler parser. Stripping cannot apply to an element
// — deleting one would renumber its siblings — so it must fail, and it does so
// through the ordinary type switch rather than an element special case.
func TestValidateProperties_NullArrayElementIsRejected(t *testing.T) {
	// Every case asserts the SAME message, because the guard runs ahead of the type
	// switch and the reason is the contract, not the declared element type: a null is
	// never a member of any Items type. The index must be named either way — a message
	// pointing at `values` alone would send an author to a list that is itself well
	// formed.
	const want = "null is not a valid array element"

	t.Run("string items", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"values": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}},
		}
		err := validateProperties(schema, map[string]any{"values": []any{"node-1", nil}}, "properties")
		if err == nil {
			t.Fatal("expected a null element to be rejected")
		}
		if !strings.Contains(err.Error(), "values[1]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})
	t.Run("object items", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"env": {Type: PropertyTypeArray, Items: &PropertySchema{
				Type:       PropertyTypeObject,
				Properties: map[string]PropertySchema{"name": {Type: PropertyTypeString, Required: true}},
			}},
		}
		err := validateProperties(schema, map[string]any{"env": []any{nil}}, "properties")
		if err == nil {
			t.Fatal("expected a null element to be rejected")
		}
		if !strings.Contains(err.Error(), "env[0]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})

	// The case the guard's old placement could not reach at all: it sat inside
	// `if schema.Items != nil`, so an array declared without an element schema
	// accepted a null element silently and handed a nil inside a []any to the handler
	// parser. Declaring no Items says nothing about the members; it does not license
	// the one member no Items type could ever have matched.
	t.Run("no items schema", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"values": {Type: PropertyTypeArray},
		}
		err := validateProperties(schema, map[string]any{"values": []any{"a", nil}}, "properties")
		if err == nil {
			t.Fatal("an array with no Items schema still must not accept a null element")
		}
		if !strings.Contains(err.Error(), "values[1]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})

	// Same placement, the typed-nil shape: this one the type switch could not have
	// caught either, since there is no Items type to switch on.
	t.Run("no items schema, typed nil element", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"values": {Type: PropertyTypeArray},
		}
		err := validateProperties(schema, map[string]any{"values": []any{[]any(nil)}}, "properties")
		if err == nil {
			t.Fatal("an array with no Items schema still must not accept a typed nil element")
		}
		if !strings.Contains(err.Error(), "values[0]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})

	// The positive half, so the two subtests above cannot pass by rejecting every
	// element: an array with no Items schema and no null members is still accepted.
	t.Run("no items schema, null-free", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"values": {Type: PropertyTypeArray},
		}
		if err := validateProperties(schema, map[string]any{"values": []any{"a", 1, map[string]any{"k": "v"}}}, "properties"); err != nil {
			t.Fatalf("a null-free array with no Items schema must still pass: %v", err)
		}
	})

	// The three cases the type switch alone could NOT reject. A typed nil satisfies the
	// plain type assertion asObjectValue/asArrayValue try first — both return
	// (nil, true) — and iterating the resulting empty collection rejects nothing, so
	// each of these was accepted before the guard existed. The untyped-schema case has
	// no type check at all.
	t.Run("typed nil map under object items", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"env": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject}},
		}
		err := validateProperties(schema, map[string]any{"env": []any{map[string]any(nil)}}, "properties")
		if err == nil {
			t.Fatal("a typed nil map passed asObjectValue and was accepted as an element")
		}
		if !strings.Contains(err.Error(), "env[0]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})
	t.Run("typed nil slice under array items", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"groups": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeArray}},
		}
		err := validateProperties(schema, map[string]any{"groups": []any{[]any(nil)}}, "properties")
		if err == nil {
			t.Fatal("a typed nil slice passed asArrayValue and was accepted as an element")
		}
		if !strings.Contains(err.Error(), "groups[0]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})
	t.Run("untyped items schema", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"anything": {Type: PropertyTypeArray, Items: &PropertySchema{}},
		}
		err := validateProperties(schema, map[string]any{"anything": []any{nil}}, "properties")
		if err == nil {
			t.Fatal("an untyped Items schema checks nothing, so only the guard can reject a null element")
		}
		if !strings.Contains(err.Error(), "anything[0]") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected an indexed null-element error, got: %v", err)
		}
	})
}

// The Enum comparison reads a value this package has already normalized while the
// declared members stay as written, so a member holding a null at any depth could never
// match. Enum is restricted to scalars rather than normalizing members, which would have
// made it a further reader of "null" to keep aligned. Enum on a scalar still works, and
// an untyped schema still accepts one.
// TestValidatePropertyValue_EnumMemberHoldingNullIsRejected pins the narrowed rule.
// The check used to refuse EVERY Enum declared on an array or object type, which also
// refused the null-free compound enums that match perfectly well. PropertySchema is
// exported, so that landed on out-of-tree handlers as a break in schemas this validator
// had accepted. Only a member that can never match is refused now — the discriminating
// case is the null_free subtest below, which the old rule failed.
func TestValidatePropertyValue_EnumMemberHoldingNullIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  PropertyType
	}{
		{"object", PropertyTypeObject},
		{"array", PropertyTypeArray},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]PropertySchema{
				"options": {Type: tc.typ, Enum: []any{map[string]any{"x": nil}}},
			}
			// Empty collections deliberately: a populated object would be rejected
			// for its undeclared key by the object recursion before the Enum check
			// was reached, and the assertion would pass on the wrong error.
			var value any = map[string]any{}
			if tc.typ == PropertyTypeArray {
				value = []any{}
			}
			err := validateProperties(schema, map[string]any{"options": value}, "properties")
			if err == nil {
				t.Fatal("expected a schema error for an Enum member holding a null")
			}
			if !strings.Contains(err.Error(), "Enum member 0 holding a null") {
				t.Fatalf("expected the schema-level message, got: %v", err)
			}
		})
	}

	// The case the old per-type rule got wrong. Nothing in this schema is unmatchable:
	// the declared member is null-free, and the value equals it after normalization.
	t.Run("null_free compound enum still matches", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"options": {
				Type:                 PropertyTypeObject,
				AdditionalProperties: true,
				Enum:                 []any{map[string]any{"mode": "fast"}, map[string]any{"mode": "slow"}},
			},
		}
		props := map[string]any{"options": map[string]any{"mode": "fast"}}
		if err := validateProperties(schema, props, "properties"); err != nil {
			t.Fatalf("a null-free compound enum must still be usable: %v", err)
		}
	})

	// And it must still REJECT a value outside that null-free set, so the subtest
	// above is not passing because compound enums stopped being enforced.
	t.Run("null_free compound enum still rejects a non-member", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"options": {
				Type:                 PropertyTypeObject,
				AdditionalProperties: true,
				Enum:                 []any{map[string]any{"mode": "fast"}},
			},
		}
		props := map[string]any{"options": map[string]any{"mode": "sideways"}}
		err := validateProperties(schema, props, "properties")
		if err == nil || !strings.Contains(err.Error(), "not in allowed set") {
			t.Fatalf("expected the ordinary enum rejection, got: %v", err)
		}
	})

	// A null nested below the top level of a member is just as unmatchable, and the
	// walk has to reach it.
	t.Run("null nested inside a member", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"options": {
				Type:                 PropertyTypeObject,
				AdditionalProperties: true,
				Enum:                 []any{map[string]any{"a": []any{map[string]any{"b": nil}}}},
			},
		}
		err := validateProperties(schema, map[string]any{"options": map[string]any{}}, "properties")
		if err == nil || !strings.Contains(err.Error(), "Enum member 0 holding a null") {
			t.Fatalf("expected the nested null to be found, got: %v", err)
		}
	})

	t.Run("scalar enum still enforced", func(t *testing.T) {
		schema := map[string]PropertySchema{
			"mode": {Type: PropertyTypeString, Enum: []any{"a", "b"}},
		}
		if err := validateProperties(schema, map[string]any{"mode": "a"}, "properties"); err != nil {
			t.Fatalf("a valid scalar enum value must still pass: %v", err)
		}
		err := validateProperties(schema, map[string]any{"mode": "c"}, "properties")
		if err == nil || !strings.Contains(err.Error(), "not in allowed set") {
			t.Fatalf("expected the ordinary enum rejection, got: %v", err)
		}
	})
}

// A nested declared object inherits the strip through the object recursion, so the
// contract does not stop at the top level.
func TestValidateProperties_NullInsideNestedObjectIsStripped(t *testing.T) {
	props := map[string]any{"resources": map[string]any{"cpu": "100m", "memory": nil}}
	if err := validateProperties(richSchema(), props, "properties"); err != nil {
		t.Fatalf("expected acceptance, got: %v", err)
	}
	nested, ok := props["resources"].(map[string]any)
	if !ok {
		t.Fatalf("expected the nested object to survive as map[string]any, got %T", props["resources"])
	}
	if _, present := nested["memory"]; present {
		t.Fatal("expected the nested null to be deleted")
	}
}

// Disclosed residual, pinned rather than fixed: an undeclared key under
// AdditionalProperties has no schema to consult, so it is passed over untouched and
// a null there still reaches a parser. This test exists to make that boundary
// visible and to fail loudly if someone later widens the strip to undeclared keys
// without deciding to — the same horizon as validation itself, since an opaque
// object is precisely the thing this package does not model.
func TestValidateProperties_NullUnderUndeclaredKeyIsNotStripped(t *testing.T) {
	props := map[string]any{"labels": map[string]any{"tier": "web", "opaque": nil}}
	if err := validateProperties(richSchema(), props, "properties"); err != nil {
		t.Fatalf("expected acceptance, got: %v", err)
	}
	nested := props["labels"].(map[string]any)
	if _, present := nested["opaque"]; !present {
		t.Fatal("an undeclared null is outside the strip's reach; if this now passes, the scope note in property_validate.go is stale")
	}
}

func newSchemaTransformer() *Transformer {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": schemaComponent{typ: "webservice"},
			"plain":      plainComponent{typ: "plain"},
			"rich":       richComponent{typ: "rich"},
		},
		map[string]TraitHandler{
			"pvc": schemaTrait{typ: "pvc"},
		},
	)
	tr.RegisterPolicy("dependency", schemaPolicy{typ: "dependency"})
	return tr
}

func TestValidateEmittedComponent(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted component to be accepted, got: %v", err)
	}

	// No registered handler: lowerable or not yet settled, nothing to check against.
	if err := tr.validateEmittedComponent(&Component{
		Name: "shop", Type: "web-and-cache", Properties: map[string]any{"anything": 1},
	}); err != nil {
		t.Fatalf("expected an unregistered component type to be passed over, got: %v", err)
	}

	// Registered handler that declares no schema: accepts anything.
	if err := tr.validateEmittedComponent(&Component{
		Name: "p", Type: "plain", Properties: map[string]any{"whatever": 1},
	}); err != nil {
		t.Fatalf("expected a schema-less handler to accept anything, got: %v", err)
	}

	err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "webservice", Properties: map[string]any{"image": 7},
	})
	if err == nil {
		t.Fatal("expected a type violation in an emitted component to be rejected")
	}
	// The message names the emitted element, since the caller supplies only the
	// AUTHORED origin around it.
	for _, want := range []string{`emitted component "web"`, `(type "webservice")`, "properties.image", "expected string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestValidateEmittedTrait(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteOnce"}},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted trait to be accepted, got: %v", err)
	}

	// A rule assembling its output in Go writes []string, not []any.
	if err := tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []string{"ReadWriteOnce"}},
	}); err != nil {
		t.Fatalf("expected a Go-native []string to be accepted for an array field, got: %v", err)
	}

	if err := tr.validateEmittedTrait(&Trait{
		Type: "expose-plus", Properties: map[string]any{"anything": 1},
	}); err != nil {
		t.Fatalf("expected an unregistered trait type to be passed over, got: %v", err)
	}

	err := tr.validateEmittedTrait(&Trait{Type: "pvc", Properties: map[string]any{"accessModes": []any{"ReadWriteOnce"}}})
	if err == nil {
		t.Fatal("expected a missing required trait property to be rejected")
	}
	if !strings.Contains(err.Error(), `emitted trait "pvc"`) || !strings.Contains(err.Error(), `"size" is required`) {
		t.Errorf("unexpected message: %v", err)
	}

	err = tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []any{1}},
	})
	if err == nil || !strings.Contains(err.Error(), "properties.accessModes[0]: expected string") {
		t.Fatalf("expected an indexed item-type error, got: %v", err)
	}
}

// requiredSchemaComponentLoweringRule is a ComponentLoweringRule that also
// declares a PropertySchema with a required field — the round-8 Codex
// regression fixture: HandlerSchemas (transform.go) already publishes a
// lowering rule's schema, but before this fix validateEmittedComponent never
// consulted it, checking only t.componentHandlers. Named distinctly from
// schema_test.go's schemaComponentLoweringRule (used by
// TestHandlerSchemas_IncludesComponentLoweringRules), whose schema has no
// required field and so cannot exercise the rejection path this test needs.
type requiredSchemaComponentLoweringRule struct{ typ string }

func (r requiredSchemaComponentLoweringRule) ComponentType() string { return r.typ }
func (r requiredSchemaComponentLoweringRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "webservice", Properties: map[string]any{"image": "nginx"}}}}, nil
}
func (r requiredSchemaComponentLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"image": {Type: PropertyTypeString, Required: true}}
}

// schemaTraitLoweringRule is the trait-position counterpart.
type schemaTraitLoweringRule struct{ typ string }

func (r schemaTraitLoweringRule) TraitType() string { return r.typ }
func (r schemaTraitLoweringRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "ingress", Properties: map[string]any{}}}}, nil
}
func (r schemaTraitLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"hostnames": {Type: PropertyTypeArray, Required: true, Items: &PropertySchema{Type: PropertyTypeString}}}
}

// requiredSchemaPolicyLoweringRule is requiredSchemaComponentLoweringRule's
// policy-position counterpart.
type requiredSchemaPolicyLoweringRule struct{ typ string }

func (r requiredSchemaPolicyLoweringRule) PolicyType() string { return r.typ }
func (r requiredSchemaPolicyLoweringRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "dependency", Properties: map[string]any{}}}}, nil
}
func (r requiredSchemaPolicyLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"owner": {Type: PropertyTypeString, Required: true}}
}

// TestValidateEmittedPolicy_ConsultsPolicyLoweringRuleSchema is the
// round-9-batch-2 Codex regression test (property_validate.go:429): unlike
// validateEmittedComponent/validateEmittedTrait, validateEmittedPolicy never fell
// back to policyLoweringRules when no terminal policyHandler claimed the emitted
// type — so a PolicyLoweringRule's own declared PropertySchema went completely
// unenforced at emission time, even though HandlerSchemas (transform.go) already
// publishes it as a discoverable schema (same gap round-8's component/trait fix
// closed for those two positions, left open here).
func TestValidateEmittedPolicy_ConsultsPolicyLoweringRuleSchema(t *testing.T) {
	tr := newSchemaTransformer()
	tr.RegisterPolicyLowering(requiredSchemaPolicyLoweringRule{typ: "higher-dependency"})

	if err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "order", Type: "higher-dependency", Properties: map[string]any{"owner": "team-a"},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted policy to be accepted, got: %v", err)
	}

	err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "order", Type: "higher-dependency", Properties: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected a missing required property to be rejected against the lowering rule's own schema")
	}
	if !strings.Contains(err.Error(), `emitted policy "order"`) || !strings.Contains(err.Error(), `"owner" is required`) {
		t.Errorf("unexpected message: %v", err)
	}
}

// TestValidateEmittedComponent_ConsultsComponentLoweringRuleSchema is the round-8
// Codex regression test (property_validate.go:379-386): when a rule emits an
// intermediate component whose target type is claimed by a ComponentLoweringRule
// (not a terminal ComponentHandler), validateEmittedComponent silently accepted
// anything instead of checking that rule's own declared PropertySchema — even
// though HandlerSchemas (transform.go) already publishes it as a discoverable
// schema, so the gap was enforcement, not availability.
func TestValidateEmittedComponent_ConsultsComponentLoweringRuleSchema(t *testing.T) {
	tr := newSchemaTransformer()
	tr.RegisterComponentLowering(requiredSchemaComponentLoweringRule{typ: "higher-webservice"})

	if err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "higher-webservice", Properties: map[string]any{"image": "nginx"},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted component to be accepted, got: %v", err)
	}

	err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "higher-webservice", Properties: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected a missing required property to be rejected against the lowering rule's own schema")
	}
	if !strings.Contains(err.Error(), `emitted component "web"`) || !strings.Contains(err.Error(), `"image" is required`) {
		t.Errorf("unexpected message: %v", err)
	}
}

// TestValidateEmittedTrait_ConsultsTraitLoweringRuleSchema is the trait-position
// counterpart of TestValidateEmittedComponent_ConsultsComponentLoweringRuleSchema.
func TestValidateEmittedTrait_ConsultsTraitLoweringRuleSchema(t *testing.T) {
	tr := newSchemaTransformer()
	tr.RegisterTraitLowering(schemaTraitLoweringRule{typ: "higher-expose"})

	if err := tr.validateEmittedTrait(&Trait{
		Type: "higher-expose", Properties: map[string]any{"hostnames": []any{"shop.example.com"}},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted trait to be accepted, got: %v", err)
	}

	err := tr.validateEmittedTrait(&Trait{Type: "higher-expose", Properties: map[string]any{}})
	if err == nil {
		t.Fatal("expected a missing required property to be rejected against the lowering rule's own schema")
	}
	if !strings.Contains(err.Error(), `emitted trait "higher-expose"`) || !strings.Contains(err.Error(), `"hostnames" is required`) {
		t.Errorf("unexpected message: %v", err)
	}
}

// TestValidateEmittedTrait_NormalizesArrayWriteBack is the regression test for G4
// (Codex-bot wave 2): asArrayValue built a fresh []any copy purely to validate a
// Go-native []string, then discarded the copy — trait.Properties kept the original,
// still-typed []string. A downstream handler asserting .([]any) on it (the shape a
// YAML-decoded document would have produced) silently failed and dropped the field
// with no error. validateEmittedTrait must write the normalized []any back.
func TestValidateEmittedTrait_NormalizesArrayWriteBack(t *testing.T) {
	tr := newSchemaTransformer()
	trait := &Trait{
		Type:       "pvc",
		Properties: map[string]any{"size": "1Gi", "accessModes": []string{"ReadWriteOnce"}},
	}
	if err := tr.validateEmittedTrait(trait); err != nil {
		t.Fatalf("validateEmittedTrait: %v", err)
	}
	modes, ok := trait.Properties["accessModes"].([]any)
	if !ok {
		t.Fatalf("trait.Properties[%q] = %T, want []any (write-back did not normalize the original []string)", "accessModes", trait.Properties["accessModes"])
	}
	if len(modes) != 1 || modes[0] != "ReadWriteOnce" {
		t.Fatalf("normalized accessModes lost content: %+v", modes)
	}
}

// TestValidateEmittedComponent_NormalizesObjectWriteBack is the object-position
// counterpart of the array test above: asObjectValue built a fresh map[string]any
// copy from a Go-native map[string]string purely to validate it, then discarded the
// copy. Confirmed real downstream consumer: expose_rule.go does
// `anns, _ := props["annotations"].(map[string]any)` — comma-ok, so it doesn't panic,
// but a map[string]string there silently fails the assertion and drops the field.
func TestValidateEmittedComponent_NormalizesObjectWriteBack(t *testing.T) {
	tr := newSchemaTransformer()
	comp := &Component{
		Name:       "web",
		Type:       "rich",
		Properties: map[string]any{"labels": map[string]string{"tier": "gold", "team": "x"}},
	}
	if err := tr.validateEmittedComponent(comp); err != nil {
		t.Fatalf("validateEmittedComponent: %v", err)
	}
	labels, ok := comp.Properties["labels"].(map[string]any)
	if !ok {
		t.Fatalf("comp.Properties[%q] = %T, want map[string]any (write-back did not normalize the original map[string]string)", "labels", comp.Properties["labels"])
	}
	if labels["tier"] != "gold" || labels["team"] != "x" {
		t.Fatalf("normalized labels lost content: %+v", labels)
	}
}

func TestValidateEmittedPolicy(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "db-first", Type: "dependency", Properties: map[string]any{"components": []any{"db"}},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted policy to be accepted, got: %v", err)
	}

	if err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "anything", Type: "placement", Properties: map[string]any{"zone": "eu"},
	}); err != nil {
		t.Fatalf("expected an unregistered policy type to be passed over, got: %v", err)
	}

	err := tr.validateEmittedPolicy(&ApplicationPolicy{Name: "db-first", Type: "dependency"})
	if err == nil {
		t.Fatal("expected a missing required policy property to be rejected")
	}
	if !strings.Contains(err.Error(), `emitted policy "db-first" (type "dependency")`) {
		t.Errorf("unexpected message: %v", err)
	}
}

// badComponentRule emits one component of a registered, schema-carrying type with
// properties the schema rejects — the case emission-time validation exists for.
type badComponentRule struct{ props map[string]any }

func (badComponentRule) ComponentType() string { return "web-and-cache" }

func (r badComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name: comp.Name + "-web", Type: "webservice", Properties: r.props,
	}}}, nil
}

// TestLower_EmittedComponentIsValidated is the integration proof: a rule's bad
// output fails the run at emission time, and the error leads with the AUTHORED
// origin (D7) with the emitted detail second.
func TestLower_EmittedComponentIsValidated(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "shop", Type: "web-and-cache", Properties: map[string]any{}}},
		},
	}

	// Accepted output settles normally.
	trOK := newSchemaTransformer()
	trOK.RegisterComponentLowering(badComponentRule{props: map[string]any{"image": "nginx"}})
	if _, err := trOK.lower(app, TransformContext{}); err != nil {
		t.Fatalf("expected conformant rule output to lower cleanly, got: %v", err)
	}

	// Rejected output fails the run.
	trBad := newSchemaTransformer()
	trBad.RegisterComponentLowering(badComponentRule{props: map[string]any{"image": "nginx", "imagee": "typo"}})
	_, err := trBad.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected lower to reject a rule emitting schema-violating properties")
	}
	var lerr *LoweringError
	if !stderrors.As(err, &lerr) {
		t.Fatalf("expected a *LoweringError, got %T: %v", err, err)
	}
	// runLowering attributes a LoweringError to the document being expanded; the
	// failing element's own authored origin is carried in the wrapped cause, which
	// Error() prints right after it.
	if lerr.Origin.Document != "myapp" || lerr.Origin.DocumentKind != "Application" {
		t.Errorf("expected the authored document origin, got %+v", lerr.Origin)
	}
	msg := err.Error()
	for _, want := range []string{`component "shop" (type "web-and-cache")`, `emitted component "shop-web"`, `unsupported field "imagee"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

// TestValidatePropertyValue_NumberRejectsNaNAndInf is the regression test for the
// round-5 Codex finding at property_validate.go:325 (F8): asFloatValue's float branch
// returned rv.Float() unconditionally, so isNumberValue (its only type-check caller)
// accepted NaN and +/-Inf as valid PropertyTypeNumber values. Neither round-trips
// through the YAML/JSON a validated property eventually serializes to — isIntegerValue,
// just above asFloatValue in this same file, already excludes both for the integer
// path (math.IsInf check, plus NaN failing its own Trunc equality), so the number path
// was the one left unguarded.
func TestValidatePropertyValue_NumberRejectsNaNAndInf(t *testing.T) {
	schema := PropertySchema{Type: PropertyTypeNumber}
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePropertyValue(schema, tc.value, "properties.field")
			if err == nil {
				t.Fatalf("expected %v to be rejected as a PropertyTypeNumber value, got nil error", tc.value)
			}
			if !strings.Contains(err.Error(), "expected number") {
				t.Fatalf("expected an \"expected number\" error, got: %v", err)
			}
		})
	}
}

// TestValidatePropertyValue_NumberAcceptsFiniteFloat guards against an overcorrection:
// an ordinary finite float must still validate as PropertyTypeNumber.
func TestValidatePropertyValue_NumberAcceptsFiniteFloat(t *testing.T) {
	schema := PropertySchema{Type: PropertyTypeNumber}
	if _, err := validatePropertyValue(schema, 3.5, "properties.field"); err != nil {
		t.Fatalf("expected a finite float to validate, got: %v", err)
	}
}
