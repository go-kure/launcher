package oam

import (
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"

	"github.com/go-kure/launcher/pkg/errors"
)

// This file is the enforcement half of PropertySchema (schema.go). Until now the
// package only PUBLISHED handler schemas (Transformer.HandlerSchemas) for an
// out-of-process validator to consume; nothing inside launcher checked a property
// map against one. The lowering engine needs that check in-process (D4): an element
// a lowering rule EMITS was never seen by whatever validated the authored document,
// so without a check here a rule could silently produce properties its own target
// handler cannot accept, and the failure would surface far downstream — or not at
// all, as a silently missing field.
//
// THE NULL CONTRACT, stated once because "null" has seven readers this contract binds
// and each round so far aligned one and left the next round to find the one it had not
// touched:
//
//	A value that serializes to JSON null is absent, at every depth, on every path;
//	a null is never a member of any Items type, so a null array element is a type
//	error; reservation is about the KEY being written.
//
// Reservation is checked on the authored surface; the component-side checks
// (transform.go:649, lowering.go:1083) also run on rule-produced components, where a
// reserved value is wrongly rejected as authored (KNOWN LIMITATION, transform.go:637;
// see go-kure/launcher#429) and a reserved null has already been stripped.
//
// Two boundaries the sentence deliberately does not cross. An empty object is NOT a
// null and is NOT absent — an empty metav1.LabelSelector selects everything where an
// absent one selects nothing, so collapsing them would change what a NetworkPolicy
// admits (builtin/traits/networkpolicy.go). And an UNDECLARED key is outside every
// rule here: the strip reaches only keys a schema declares, the same horizon
// validation itself has.
//
// Scope note: validateProperties and its object walk run on EMITTED elements. The
// authored path has its own entry point, ValidateAuthoredProperties
// (property_validate_authored.go), which reaches the same validatePropertyValue for
// every authored key — so a declared string property written as `123` is a type
// error on either path (go-kure/launcher#325). It is a Transformer method rather
// than a call inside validate() because it needs the registered handlers' schemas,
// which the parser does not have; kurel build calls it right after parsing. A
// caller that invokes Transform without it gets only the handlers' own reads,
// which do not type-check every property.
//
// checkCapabilityValueType (capability.go) is NOT this: it validates a
// ClusterProfile capability rendering against the FLAT schema subset those call
// sites accept (string/integer/boolean, no enum/nested/items — see flatschema.go).
// This file covers the full handler vocabulary. The two are kept apart on purpose:
// merging them would either widen what the flat call sites accept or lose their
// call-site-specific messages. It is also outside the null contract above, by the
// scope sentence rather than by the vocabulary split: it reads a PRESENT null as a
// type error ("expected string, got <nil>") where the contract would read it as
// omission — loud rather than silent, so nothing is wrongly accepted, but it is a
// divergence and it is tracked, with the missing switch default, as
// go-kure/launcher#431.

// validateProperties checks props against schema — a handler's top-level declared
// property set — enforcing every Required field's presence and every present
// field's Type/Enum/nested Properties/Items/AdditionalProperties.
//
// The top level never tolerates an undeclared key: a handler declares its complete
// accepted property set, so there is no top-level AdditionalProperties escape —
// that applies only within a nested object field that declares it.
func validateProperties(schema map[string]PropertySchema, props map[string]any, path string) error {
	return validateObjectProperties(schema, false, props, path)
}

// validateObjectProperties is validateProperties widened with additionalAllowed, for
// recursing into a nested object field whose schema sets
// PropertySchema.AdditionalProperties.
//
// Keys are visited in sorted order at both stages so a props map with several
// problems always reports the same one, rather than a different error per run.
func validateObjectProperties(schema map[string]PropertySchema, additionalAllowed bool, props map[string]any, path string) error {
	for _, key := range slices.Sorted(maps.Keys(schema)) {
		if !schema[key].Required {
			continue
		}
		// An explicit nil counts as absent, not as a present null: `size:` with no
		// value decodes to a nil entry, and a rule that assembles properties in Go
		// can leave a key mapped to nil the same way. Treating that as satisfying
		// Required would let a required field through empty. A bare `v == nil` only
		// catches the untyped case — a rule that assigns an uninitialized Go slice
		// or map (`[]any(nil)`, `map[string]any(nil)`) produces an `any` whose
		// interface value is non-nil even though the data it holds is, and that
		// value still serializes to JSON `null` — so isNullValue is needed here too.
		if v, present := props[key]; !present || isNullValue(v) {
			return errors.Errorf("%s: %q is required", path, key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(props)) {
		field, ok := schema[key]
		if !ok {
			if additionalAllowed {
				continue
			}
			return errors.Errorf("%s: unsupported field %q (allowed: %s)", path, key, declaredFields(schema))
		}
		// Normalize an explicit null under an optional declared key to absence, so
		// the classification the Required loop above already makes is what a
		// downstream consumer actually sees. validatePropertyValue used to agree
		// that a null was absent and return it unchanged, but the write-back below
		// then materialized that "absent" value as a PRESENT key — and a handler
		// parser answers presence with a bare map lookup (`v, ok := props[key]`,
		// common.go:1718-1721), so it received a key validation had already decided
		// was not there. Deleting it is not a new rule; it is the existing rule
		// taking effect.
		//
		// Required keys cannot reach here holding a null: the loop above returns
		// %q is required for exactly that case, which is why this needs no Required
		// test of its own and why the order of the two loops is load bearing —
		// reversing them would strip a required null out from under its own check
		// and report a missing key instead of an empty one.
		//
		// No PlatformReserved exception, deliberately. Reservation governs what a user
		// WROTE, so reporting it names the line an author actually typed.
		//
		// On the AUTHORED surface the two rules never meet: enforcePlatformReserved
		// runs upstream of any emission validation, so what it sees is what a user
		// wrote. On the COMPONENT surface they do meet, and saying otherwise would be
		// false — transform.go:649 and lowering.go:1083 run it on comp.Properties, and
		// the lowering round loop (lowering.go:759) feeds each round's emitted
		// documents back as the next round's input, so a rule-produced component
		// reaches that check with its properties already stripped here and a reserved
		// key set to null is never flagged. Latent rather than live: every
		// PlatformReserved field declared today is on a trait schema, none on a
		// component schema. Tracked as go-kure/launcher#429.
		if isNullValue(props[key]) {
			delete(props, key)
			continue
		}
		normalized, err := validatePropertyValue(field, props[key], path+"."+key)
		if err != nil {
			return err
		}
		props[key] = normalized
	}
	return nil
}

// validatePropertyValue checks one value against its declared PropertySchema: the
// value matches schema.Type, an array's elements match schema.Items, an object's
// fields recurse through validateObjectProperties, and — after the type check — the
// value is a member of schema.Enum when one is declared.
//
// Type matching is by reflect.Kind, not by concrete Go type, because the values
// reaching here are not only YAML-decoded (string/bool/int/float64/[]any/
// map[string]any). A lowering rule builds its emitted element's properties in Go,
// where []string, map[string]string and named scalar types are the natural things
// to write; rejecting those would fail correct rule output on its Go
// representation alone.
//
// It returns the normalized value alongside the error: for an array or object,
// asArrayValue/asObjectValue may have built a fresh []any/map[string]any copy from a
// typed Go collection (a rule's own []string or map[string]string), and that copy —
// not the original typed value — is what nested validation actually checked. The
// caller (validateObjectProperties) writes the returned value back into the props
// map it holds, so a downstream consumer's type assertion (e.g. .(map[string]any))
// sees the same normalized shape validation itself checked, instead of the
// original, still-typed value silently surviving unassertable. Scalars are
// normalized the same way (go-kure/launcher#428): a named string, boolean or number
// type (`type Mode string`) is written back as its predeclared type (unnamedScalar),
// and an integer-typed value whose kind or name no reader asserts becomes int (see
// normalizeIntegerValue). An untyped schema (`case "":`) checks no type, so its
// value is left as supplied.
//
// A null reaches here as a whole property value on one path: validateAuthoredProperties
// (property_validate_authored.go) calls this function directly over every authored
// key, without the null-strip validateObjectProperties applies to its own callers —
// that strip is what makes "absent" and "explicit null" the same case for the
// EMITTED path (D4), but the AUTHORED path never goes through it. The early return
// below is what makes an optional field's authored null read as "constrains
// nothing" there too, matching Required's own treatment of it at the caller.
//
// It must NOT apply to an ARRAY ELEMENT, though: the handler parsers assert
// concrete element types (parseStringList's item.(string),
// builtin/components/podspec.go:644-646), so a null element has to fail rather than
// pass through. That case never reaches this early return — the PropertyTypeArray
// case below guards each element explicitly, before the recursive call into this
// function, so a null item is rejected at the array level and this function is
// never invoked on it. An earlier version of this early return had no such guard
// and let a null element through to a parser that then rejected it — a
// schema/parser divergence the schema itself could not express; the array-level
// guard is what closed that without reopening this one.
//
// The ordinary type switch is NOT sufficient to reject a null array element on its
// own, which is why the array case guards explicitly rather than relying on it. The
// switch does handle an UNTYPED nil reaching it directly: isStringValue(nil) is
// false, so `values: [null]` under Items{Type: string} reports "expected string,
// got <nil>". But a TYPED nil collection satisfies the plain type assertion each
// coercer tries first — asArrayValue([]any(nil)) and asObjectValue(map[string]any(nil))
// both return (nil, true) — and iterating the resulting empty collection rejects
// nothing, so `items: [null]` under Items{Type: object} passed on exactly that
// path. The `case "":` branch checks nothing at all, so an untyped Items schema
// accepted a null element too. Both are why the array-level guard is a guard and
// not a comment.
func validatePropertyValue(schema PropertySchema, value any, path string) (any, error) {
	if isNullValue(value) {
		// Absent/null. Presence is enforced by Required at the caller; a nil under
		// an optional field constrains nothing. Never reached for an array element
		// — see the doc comment above.
		return value, nil
	}

	switch schema.Type {
	case "":
		// No declared type: nothing to check beyond Enum below. Reachable for a
		// schema built for a call site that leaves Type empty (flatschema.go).
	case PropertyTypeString:
		if !isStringValue(value) {
			return value, errors.Errorf("%s: expected string, got %T", path, value)
		}
		value = unnamedScalar(value)
	case PropertyTypeBoolean:
		if !isBooleanValue(value) {
			return value, errors.Errorf("%s: expected boolean, got %T", path, value)
		}
		value = unnamedScalar(value)
	case PropertyTypeInteger:
		if !isIntegerValue(value) {
			return value, errors.Errorf("%s: expected integer, got %T (%v)", path, value, value)
		}
		normalized, err := normalizeIntegerValue(value, path)
		if err != nil {
			return value, err
		}
		value = normalized
	case PropertyTypeNumber:
		if !isNumberValue(value) {
			return value, errors.Errorf("%s: expected number, got %T", path, value)
		}
		value = unnamedScalar(value)
	case PropertyTypeArray:
		items, ok := asArrayValue(value)
		if !ok {
			return value, errors.Errorf("%s: expected array, got %T", path, value)
		}
		for i, item := range items {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			// A null is never a member of any Items type. An element cannot be
			// "absent" the way an object key can — it is present by being in the
			// list, and deleting it would renumber its siblings — so the contract
			// makes it a type error instead. Guarded here rather than left to the
			// type switch below because a typed nil survives that switch; see the
			// doc comment above.
			//
			// Outside the schema.Items guard, deliberately: "no null elements" is a
			// property of the array contract itself, not of a declared element type.
			// An array schema with no Items says nothing about its members, but it
			// does not thereby license a member that no Items type could ever match —
			// and unlike an object's undeclared key, whose null this validator leaves
			// alone because deleting it would exceed what the schema describes, an
			// array element cannot be left alone in that sense: it still reaches the
			// handler parser as a nil in a []any, which is the shape the contract
			// exists to keep out.
			if isNullValue(item) {
				return value, errors.Errorf("%s: null is not a valid array element", elemPath)
			}
			if schema.Items == nil {
				continue
			}
			normalized, err := validatePropertyValue(*schema.Items, item, elemPath)
			if err != nil {
				return value, err
			}
			items[i] = normalized
		}
		value = items
	case PropertyTypeObject:
		obj, ok := asObjectValue(value)
		if !ok {
			return value, errors.Errorf("%s: expected object, got %T", path, value)
		}
		// Run unconditionally, even when schema.Properties is nil: a nil map has no
		// declared keys, so every key in obj is "not declared" by definition, and
		// validateObjectProperties/AdditionalProperties (defaulting to false, i.e.
		// closed) is exactly what decides whether that is accepted. Skipping the call
		// here previously let AdditionalProperties:false silently accept anything when
		// a handler declared an object-typed field with no sub-schema at all.
		if err := validateObjectProperties(schema.Properties, schema.AdditionalProperties, obj, path); err != nil {
			return value, err
		}
		value = obj
	default:
		// A schema, not a document, is wrong here: some handler declared a type
		// outside the PropertyType vocabulary. Silently accepting it would make the
		// field unvalidated forever, so it fails loudly at the one place that reads
		// the schema.
		return value, errors.Errorf("%s: schema declares unsupported property type %q", path, schema.Type)
	}

	if len(schema.Enum) > 0 {
		// A schema, not a document, is wrong here — same shape as the unsupported-type
		// failure above, and for the same reason: it fails at the one place that reads
		// the schema rather than silently mis-comparing forever.
		//
		// Enum members are compared against a value this function has already
		// NORMALIZED — an object's explicit nulls stripped, a typed collection copied
		// into []any/map[string]any — while the declared members are left exactly as
		// written. So a member holding a null where the value's own null would have
		// been stripped or rejected can never match a value that reached this line:
		// Enum{{"x": nil}} under a declared "x" stopped matching `{x: null}` the moment
		// the strip existed.
		//
		// Only THERE, though. The strip reaches exactly the keys a schema declares:
		// a key an object leaves to AdditionalProperties: true is skipped by
		// validateObjectProperties untouched, as is everything inside an array element
		// with no Items schema and everything below a schema with no declared Type. A
		// value holding a null in one of those places reaches the comparison as
		// written, so a member holding the same null is matchable and refusing it
		// would refuse a working schema (go-kure/launcher#481). The member is
		// therefore walked alongside this schema, by enumMemberHoldsStrippedNull,
		// rather than scanned for a null anywhere.
		//
		// Refused per MEMBER, not per schema type. Refusing every Enum declared on an
		// array or object type would be simpler to state, but it also refuses the
		// null-free compound enums that keep matching perfectly well — and
		// PropertySchema is exported, so a handler outside this repo would see a schema
		// this validator used to accept start failing for a reason that does not apply
		// to it. Only the members that can never match are refused.
		//
		// Normalizing members instead was the other option, and is the expensive one:
		// it would make Enum one more reader of "null" that has to reproduce the strip
		// exactly, forever. enumMemberHoldsStrippedNull is still a null reader, but a
		// far cheaper one — it only answers "is there a null in this literal where the
		// strip would have removed one", and never rewrites the member.
		//
		// The comment this replaces cited TestBuiltinHandlerSchemaEnumsAreScalar as
		// asserting that no built-in schema declares an Enum on a non-scalar type. That
		// test existed — in package kurel, not here, which is why a grep of this package
		// missed it — and it asserted the rule this arm no longer implements. It has
		// been retargeted to the rule below and renamed
		// TestBuiltinHandlerSchemaEnumMembersHoldNoNull, walking the schemas that ship
		// for a member holding a null. Nothing asserts the per-TYPE property any more,
		// and this arm does not need it to be true.
		for i, member := range schema.Enum {
			if enumMemberHoldsStrippedNull(schema, member, 0) {
				return value, errors.Errorf(
					"%s: schema declares Enum member %d holding a null, which no validated value can match",
					path, i)
			}
		}
		if !enumContainsValue(schema.Enum, value) {
			return value, errors.Errorf("%s: value %v not in allowed set %v", path, value, schema.Enum)
		}
	}
	return value, nil
}

// enforcePlatformReserved rejects an AUTHORED value for any property the schema marks
// PropertySchema.PlatformReserved (D3). Callers run it on a props map before capability
// rendering is merged into it (before resolveCapability), so what it sees is exactly
// what a user wrote: a value the platform itself supplies through capability rendering
// arrives afterwards and is never visible here.
//
// Presence, not value, is the violation — including a key authored with an explicit
// null. This is deliberately the inverse of the Required rule above, where null counts
// as absent: there, an empty value fails to supply something mandatory; here, writing
// the key at all is the authorship attempt being refused, and reporting it names the
// line the user actually wrote instead of silently ignoring it.
//
// Emitted-property validation normalizes an explicit null to absence
// (validateObjectProperties, above) and does NOT exempt reserved keys from that.
//
// On the AUTHORED surface the two rules do not meet: this runs upstream of any
// emission validation, so what it sees is what a user wrote. On the COMPONENT surface
// they do — transform.go:649 and lowering.go:1083 call this on comp.Properties, and
// the lowering round loop (lowering.go:759) returns each round's emitted documents as
// the next round's input, so a rule-produced component arrives here already stripped
// and a reserved null is never flagged. Latent today: all 11 PlatformReserved
// declarations are on trait schemas — 8 written literally (builtin/traits/expose_rule.go
// and ingress.go) plus the 3 schemaNetworkPolicy(true) calls at expose_rule.go:145,
// ingress.go:132 and httproute.go:112 — and none on a component schema, since every
// components-side schema*(reserved) call passes false. Which also means
// the component-side calls cannot currently fire at all, so their agreement with this
// rule is vacuous rather than demonstrated. Tracked as go-kure/launcher#429, together
// with the KNOWN LIMITATION at transform.go:637 that a rule-written reserved value is
// rejected here as if a user had authored it.
//
// A key the schema does not declare is passed over: it is validateProperties' business,
// and reporting it here would duplicate that message with a misleading reason.
//
// Nested declared objects are walked as well, so a reservation on an inner field is
// enforced wherever it is declared rather than only at the top level. No schema
// declares a nested reserved field today; the walk exists so declaring one later is
// enforcement, not documentation — the exact gap D3 was written to close.
func enforcePlatformReserved(schema map[string]PropertySchema, props map[string]any, path string) error {
	if len(schema) == 0 || len(props) == 0 {
		return nil
	}
	// Sorted, like validateObjectProperties: a props map with several violations must
	// always report the same one rather than a different one per run.
	for _, key := range slices.Sorted(maps.Keys(props)) {
		field, ok := schema[key]
		if !ok {
			continue
		}
		if field.PlatformReserved {
			return errors.Wrapf(ErrPlatformReserved,
				"%s: %q is platform-reserved and may only be set via ClusterProfile capability rendering", path, key)
		}
		if field.Type != PropertyTypeObject || len(field.Properties) == 0 {
			continue
		}
		obj, ok := asObjectValue(props[key])
		if !ok {
			// Not an object: validatePropertyValue reports the type mismatch.
			continue
		}
		if err := enforcePlatformReserved(field.Properties, obj, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

// declaredFields renders a schema's key set for an unsupported-field message, so the
// error names what the handler does accept. Sorted for a stable message.
func declaredFields(schema map[string]PropertySchema) string {
	if len(schema) == 0 {
		return "(none)"
	}
	keys := slices.Sorted(maps.Keys(schema))
	out := keys[0]
	for _, k := range keys[1:] {
		out += ", " + k
	}
	return out
}

// isStringValue accepts any string-kinded value, including a named string type such
// as a rule's own enum-ish constant type.
func isStringValue(value any) bool {
	_, ok := asStringValue(value)
	return ok
}

func isBooleanValue(value any) bool {
	return reflect.ValueOf(value).Kind() == reflect.Bool
}

// isIntegerValue accepts any Go integer kind, plus a float with no fractional part —
// a YAML numeric literal decodes to float64 through interface{}, and a rule may
// construct either. NaN and ±Inf fail, since neither equals its own truncation.
func isIntegerValue(value any) bool {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		return f == math.Trunc(f) && !math.IsInf(f, 0)
	default:
		return false
	}
}

// normalizeIntegerValue rewrites an accepted integer whose Go type the property
// readers cannot assert into a plain int, which every reader accepts
// (go-kure/launcher#418, go-kure/launcher#428). isIntegerValue matches by
// reflect.Kind, but the readers downstream type-switch on concrete types —
// toInt32/toInt64 in builtin/components/common.go on float64/int/int32/int64, and at
// least one trait reader (toIngressPort, which reads servicePort) on float64/int
// only — and treat anything else as "not an integer", so a uint32 replicas passed
// validation and then rendered the schema default. Writing the value back as int,
// the type gopkg.in/yaml.v3 decodes an integer literal to, gives every reader the
// shape it already handles.
//
// The decoder set is returned unchanged: a value whose type is exactly int, int32,
// int64 or float64. So is an integral value of the predeclared float32, which no
// reader accepts and which this function has never rewritten. Everything else that
// passed isIntegerValue is rewritten:
//
//   - int8, int16 and every unsigned kind, named or not, become int.
//   - A NAMED type of kind int, int32 or int64 (`type Replicas int32`) becomes int
//     too, not its underlying type: an int32 or int64 is not a port to
//     toIngressPort (the ingress and httproute servicePort reader), which accepts
//     float64 and int only, and int is the one integer type every reader accepts.
//   - A named float type becomes its underlying float64/float32 (unnamedScalar); an
//     integral float64 is already in the decoder set.
//
// A value with no int representation is an error rather than a truncation: an
// unsigned value above math.MaxInt, or (on a 32-bit platform) a named int64 outside
// the int range. The wrapped value would read as a real one.
func normalizeIntegerValue(value any, path string) (any, error) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		if !isNamedScalar(rv) {
			return value, nil
		}
		i := rv.Int()
		if int64(int(i)) != i {
			return value, errors.Errorf("%s: integer %d out of range (min %d, max %d)", path, i, math.MinInt, math.MaxInt)
		}
		return int(i), nil
	case reflect.Int8, reflect.Int16:
		return int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > math.MaxInt {
			return value, errors.Errorf("%s: integer %d out of range (max %d)", path, u, math.MaxInt)
		}
		return int(u), nil
	default:
		return unnamedScalar(value), nil
	}
}

// predeclaredScalarTypes maps each scalar reflect.Kind to the predeclared Go type of
// that kind — the type a named scalar type (`type Mode string`) is defined over.
var predeclaredScalarTypes = map[reflect.Kind]reflect.Type{
	reflect.String:  reflect.TypeFor[string](),
	reflect.Bool:    reflect.TypeFor[bool](),
	reflect.Int:     reflect.TypeFor[int](),
	reflect.Int8:    reflect.TypeFor[int8](),
	reflect.Int16:   reflect.TypeFor[int16](),
	reflect.Int32:   reflect.TypeFor[int32](),
	reflect.Int64:   reflect.TypeFor[int64](),
	reflect.Uint:    reflect.TypeFor[uint](),
	reflect.Uint8:   reflect.TypeFor[uint8](),
	reflect.Uint16:  reflect.TypeFor[uint16](),
	reflect.Uint32:  reflect.TypeFor[uint32](),
	reflect.Uint64:  reflect.TypeFor[uint64](),
	reflect.Float32: reflect.TypeFor[float32](),
	reflect.Float64: reflect.TypeFor[float64](),
}

// isNamedScalar reports whether rv holds a scalar of a named (defined) type rather
// than the predeclared type of its kind.
func isNamedScalar(rv reflect.Value) bool {
	t, ok := predeclaredScalarTypes[rv.Kind()]
	return ok && rv.Type() != t
}

// unnamedScalar converts a value of a named scalar type to the predeclared type of
// its kind — `Mode("rolling")` to `"rolling"` — so a reader's concrete type
// assertion (`.(string)`, `.(bool)`) accepts it (go-kure/launcher#428). Any other
// value, including one already of a predeclared type, is returned unchanged.
func unnamedScalar(value any) any {
	rv := reflect.ValueOf(value)
	if !isNamedScalar(rv) {
		return value
	}
	return rv.Convert(predeclaredScalarTypes[rv.Kind()]).Interface()
}

// isNumberValue accepts any Go integer or floating-point kind.
func isNumberValue(value any) bool {
	_, ok := asFloatValue(value)
	return ok
}

// isNullValue reports whether value is a bare nil interface OR a typed nil map,
// slice, pointer, channel or function — round-9 Codex regression (property_validate.go:63):
// asArrayValue/asObjectValue's type assertions succeed on a typed nil
// ([]any(nil), map[string]any(nil)) with ok=true, so a plain `value == nil` check
// lets one through as a present, validly-typed empty collection even though
// encoding/json marshals it as `null`, not `[]`/`{}` (see IsNullValue below on
// why the encoder has to be named). A rule that assigns an
// uninitialized Go slice or map to a Properties entry hits this by construction,
// with no unusual authoring required.
func isNullValue(value any) bool {
	if value == nil {
		return true
	}
	switch rv := reflect.ValueOf(value); rv.Kind() {
	// reflect.Interface is unreachable through this signature — reflect.ValueOf
	// unwraps the interface, so a nil one has already returned above and a non-nil
	// one reports the dynamic type's kind. It is listed for completeness against a
	// future caller that passes a reflect.Value through, not because it fires.
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// IsNullValue reports whether value is nil — a bare nil interface, or a non-nil
// interface holding a nil map, slice, pointer, channel or function. It is the
// exported form of isNullValue above, and with it the null contract this package
// enforces: a value that is null is ABSENT, not present-and-empty.
//
// It is a NIL predicate, not a serialization oracle, and the difference is worth
// stating because the contract it serves is phrased in serialization terms — a
// phrasing that is ENCODER-SPECIFIC. Under encoding/json, and so under
// sigs.k8s.io/yaml which routes through it, a nil map or slice marshals to `null`
// where an allocated empty one marshals to `{}`/`[]`; that is the pairing the
// contract's wording comes from. gopkg.in/yaml.v3 — this package's own YAML
// library — does not agree: its encoder dispatches on reflect.Kind and sends a nil
// map to the mapping emitter and a nil slice to the sequence emitter
// (gopkg.in/yaml.v3@v3.0.1 encode.go:160-176), rendering `{}` and `[]`, so under
// that encoder the two shapes are indistinguishable in the output. Nil-ness is
// what this predicate reports either way, which is precisely why it is the right
// check: the encoder varies, the value does not.
//
// It also diverges from every encoder outside the shapes a decoded document
// produces: a nil channel or func is reported null here although encoding/json
// cannot marshal either at all, and a non-nil value with a custom MarshalJSON that
// emits `null` is reported not-null. Neither shape survives a round trip through a
// document, so neither reaches a property map by decoding.
//
// It exists because that contract has to hold in packages that cannot see
// isNullValue. A parser reading an optional property must classify a null with
// this rather than with `value == nil`: a TYPED nil (map[string]any(nil),
// []any(nil)) is a non-nil interface holding a nil value, so `== nil` is false
// and a `.(map[string]any)` assertion on it SUCCEEDS with ok=true and a nil map.
// The key then reads as an authored empty collection, which for a selector-shaped
// field is the widest possible value where the same input everywhere else means
// the narrowest. An uninitialized Go map or slice in a lowering rule produces
// that shape by construction, with no unusual authoring required.
func IsNullValue(value any) bool {
	return isNullValue(value)
}

// enumMemberMaxDepth bounds the walk over a declared Enum member
// (enumMemberHoldsStrippedNull and the helpers it falls back to). An Enum member is a
// schema literal, so nothing legitimate comes close; the cap exists only so a self-
// referential map handed in by a buggy handler cannot hang the validator — here, or
// in equalPropertyValues, whose recursion over a member only this cap bounds.
const enumMemberMaxDepth = 32

// enumMemberHoldsStrippedNull reports whether an Enum member declared on schema holds
// a null where validatePropertyValue would have stripped or rejected the value's own,
// so the member can never match anything that reaches the Enum comparison. It walks
// the member alongside schema, mirroring what validatePropertyValue normalizes:
//
//   - The member itself null: a null value returns before the comparison.
//   - An object schema: a DECLARED key holding a null is stripped from the value, and
//     a declared key's non-null value is walked against its own field schema. A key
//     left to AdditionalProperties: true is not normalized at all, so a null anywhere
//     under it is matchable and only the depth cap applies.
//   - An array schema: a null element is rejected whatever Items says; a non-null
//     element is walked against Items, or — with no Items — not normalized.
//   - No declared Type: nothing is normalized, so only the depth cap applies.
//
// Where the member cannot match regardless of nulls — a shape that is not the schema's
// type, or a key a closed object would refuse — containsNullValue's schema-less
// answer is kept, so those members are refused exactly as they were before the walk
// learned the schema (go-kure/launcher#481).
//
// Exceeding enumMemberMaxDepth counts as holding a null, for the reason
// containsNullValue gives.
func enumMemberHoldsStrippedNull(schema PropertySchema, member any, depth int) bool {
	if isNullValue(member) || depth >= enumMemberMaxDepth {
		return true
	}
	switch schema.Type {
	case "":
		return exceedsEnumMemberDepth(member, depth)
	case PropertyTypeArray:
		items, ok := asArrayValue(member)
		if !ok {
			return containsNullValue(member, depth)
		}
		for _, item := range items {
			if schema.Items == nil {
				if isNullValue(item) || exceedsEnumMemberDepth(item, depth+1) {
					return true
				}
				continue
			}
			if enumMemberHoldsStrippedNull(*schema.Items, item, depth+1) {
				return true
			}
		}
		return false
	case PropertyTypeObject:
		obj, ok := asObjectValue(member)
		if !ok {
			return containsNullValue(member, depth)
		}
		for key, val := range obj {
			field, declared := schema.Properties[key]
			var holds bool
			switch {
			case declared:
				holds = enumMemberHoldsStrippedNull(field, val, depth+1)
			case schema.AdditionalProperties:
				holds = exceedsEnumMemberDepth(val, depth+1)
			default:
				holds = containsNullValue(val, depth+1)
			}
			if holds {
				return true
			}
		}
		return false
	default:
		return containsNullValue(member, depth)
	}
}

// exceedsEnumMemberDepth reports whether v nests past enumMemberMaxDepth, walking the
// same slices/arrays and string-keyed maps containsNullValue does but passing over a
// null: it is used on the parts of an Enum member the value's normalization leaves
// alone, where a null is matchable but the member's depth must still be bounded.
func exceedsEnumMemberDepth(v any, depth int) bool {
	if isNullValue(v) {
		return false
	}
	if depth >= enumMemberMaxDepth {
		return true
	}
	if items, ok := asArrayValue(v); ok {
		for _, item := range items {
			if exceedsEnumMemberDepth(item, depth+1) {
				return true
			}
		}
		return false
	}
	if obj, ok := asObjectValue(v); ok {
		for _, val := range obj {
			if exceedsEnumMemberDepth(val, depth+1) {
				return true
			}
		}
		return false
	}
	return false
}

// containsNullValue reports whether v is a null, or holds one at any depth, walking
// slices/arrays and string-keyed maps through the same coercions validatePropertyValue
// applies to a document value. It is the schema-less answer enumMemberHoldsStrippedNull
// falls back to where a member cannot match whatever its nulls — see the Enum arm above
// for why a member holding a null is a schema defect rather than a value that merely
// fails to match.
//
// Exceeding enumMemberMaxDepth counts as "contains a null", not as clean: at that point
// the member cannot be shown null-free, and the two failure modes are a loud schema
// rejection versus an Enum that silently never matches. The loud one is the right side
// to fail on, and it is the same call the unsupported-type arm above makes.
func containsNullValue(v any, depth int) bool {
	if isNullValue(v) {
		return true
	}
	if depth >= enumMemberMaxDepth {
		return true
	}
	if items, ok := asArrayValue(v); ok {
		for _, item := range items {
			if containsNullValue(item, depth+1) {
				return true
			}
		}
		return false
	}
	if obj, ok := asObjectValue(v); ok {
		for _, val := range obj {
			if containsNullValue(val, depth+1) {
				return true
			}
		}
		return false
	}
	return false
}

// asArrayValue normalises any slice or array value to []any. A string is never an
// array here even though it is indexable, and neither is a map.
func asArrayValue(value any) ([]any, bool) {
	if s, ok := value.([]any); ok {
		return s, true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out, true
	default:
		return nil, false
	}
}

// asObjectValue normalises any string-keyed map to map[string]any, so a rule that
// builds a nested field as map[string]string validates like the map[string]any a
// decoder would have produced.
func asObjectValue(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}

func asStringValue(value any) (string, bool) {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.String {
		return "", false
	}
	return rv.String(), true
}

// asFloatValue rejects NaN and ±Inf for a float-kinded value: neither is a valid
// PropertyTypeNumber value (isNumberValue is this function's only type-check caller),
// since both fail to round-trip through the YAML/JSON a validated property eventually
// serializes to. This mirrors isIntegerValue's existing !math.IsInf/NaN-via-Trunc
// checks just above, which only ever applied to the integer path.
func asFloatValue(value any) (float64, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// enumContainsValue reports whether value is one of enum's members.
//
// A plain reflect.DeepEqual is not enough: the enum literals come from a handler's
// Go schema while the value comes from a decoder or a rule, so int(80) vs
// float64(80), string vs named-string-type and bool vs named-bool-type comparisons
// are routine and are the same value by every meaning a user has. Arrays and
// objects are compared element by element under the same rule; anything else falls
// back to DeepEqual.
func enumContainsValue(enum []any, value any) bool {
	for _, e := range enum {
		if equalPropertyValues(e, value) {
			return true
		}
	}
	return false
}

func equalPropertyValues(a, b any) bool {
	// A null equals a null and nothing else, by the null contract rather than by Go
	// type. It reaches here only where the value's normalization leaves a null in
	// place (enumMemberHoldsStrippedNull admits a member holding one there), and a
	// typed nil would otherwise compare as the empty collection its type assertion
	// yields: a member's []any(nil) matching an authored `[]`, or an authored null
	// failing to match a rule's map[string]any(nil).
	if an, bn := isNullValue(a), isNullValue(b); an || bn {
		return an && bn
	}
	if sa, ok := asStringValue(a); ok {
		sb, ok := asStringValue(b)
		return ok && sa == sb
	}
	// A boolean is compared by kind like a string, so a member declared with a named
	// boolean type still matches a value validation has already unnamed.
	if ba := reflect.ValueOf(a); ba.Kind() == reflect.Bool {
		bb := reflect.ValueOf(b)
		return bb.Kind() == reflect.Bool && ba.Bool() == bb.Bool()
	}
	if na, ok := asExactNumber(a); ok {
		nb, ok := asExactNumber(b)
		return ok && na.equal(nb)
	}
	// Compound members are compared element by element with this same function
	// rather than by reflect.DeepEqual: validatePropertyValue normalizes the VALUE's
	// nested integers (normalizeIntegerValue, go-kure/launcher#418) and collections
	// before the Enum check, while members stay exactly as declared, so a member
	// holding uint16(80) must still equal a value whose 80 is now an int. The walk is
	// bounded by the member's own depth, which the Enum arm has already capped at
	// enumMemberMaxDepth through enumMemberHoldsStrippedNull.
	if aa, ok := asArrayValue(a); ok {
		ba, ok := asArrayValue(b)
		if !ok || len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if !equalPropertyValues(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	if ao, ok := asObjectValue(a); ok {
		bo, ok := asObjectValue(b)
		if !ok || len(ao) != len(bo) {
			return false
		}
		for k, av := range ao {
			bv, present := bo[k]
			if !present || !equalPropertyValues(av, bv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

// exactNumber is a numeric value in a form that compares without loss: an integer
// of any Go kind as sign and magnitude, or a float as itself. Enum comparison used
// to go through float64, which makes distinct integers above 2^53 compare equal —
// harmless while only scalars were compared that way and a live admission of an
// out-of-Enum value once compound members are compared element by element (review
// of go-kure/launcher#418).
type exactNumber struct {
	isInt bool
	neg   bool   // integer only: value < 0
	mag   uint64 // integer only: |value|
	f     float64
}

// asExactNumber accepts any Go integer or floating-point kind. NaN and ±Inf are
// rejected, as asFloatValue rejects them.
func asExactNumber(value any) (exactNumber, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := rv.Int()
		if i < 0 {
			// -(i+1) cannot overflow, even for math.MinInt64.
			return exactNumber{isInt: true, neg: true, mag: uint64(-(i + 1)) + 1}, true //nolint:gosec // i < 0, so -(i+1) >= 0
		}
		return exactNumber{isInt: true, mag: uint64(i)}, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return exactNumber{isInt: true, mag: rv.Uint()}, true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return exactNumber{}, false
		}
		return exactNumber{f: f}, true
	default:
		return exactNumber{}, false
	}
}

// equal compares two numbers exactly: integers by sign and magnitude, floats by
// value, and a float against an integer only when the float is that integer
// exactly — an integral float within the uint64 magnitude range.
func (a exactNumber) equal(b exactNumber) bool {
	switch {
	case a.isInt && b.isInt:
		return a.neg == b.neg && a.mag == b.mag
	case !a.isInt && !b.isInt:
		return a.f == b.f
	case a.isInt:
		a, b = b, a
	}
	// a is the float, b the integer.
	f := a.f
	if f != math.Trunc(f) {
		return false
	}
	neg := f < 0
	if neg {
		f = -f
	}
	// 2^64 is exactly representable; every integral float below it converts to
	// uint64 without loss.
	if f >= 18446744073709551616.0 {
		return false
	}
	mag := uint64(f)
	if mag == 0 {
		return b.mag == 0
	}
	return neg == b.neg && mag == b.mag
}

// validateEmittedComponent checks an emitted component's Properties against its
// target's declared schema (D4): a terminal ComponentHandler when one is registered
// for its type, or else a ComponentLoweringRule claiming that type (an intermediate
// emission a later round will expand further) — whichever one declares a schema.
//
// A component whose type is claimed by neither is passed over on purpose: it is an
// unknown type, and there is no target schema to check it against. That case is not
// silently accepted overall — the post-fixpoint whole-document pass
// (Transformer.validateSettled) rejects a type that is still unclaimed once the
// fixpoint has settled. Checking the lowering-rule registry here, rather than only
// the terminal handler, closes the gap HandlerSchemas already closed for schema
// *publication* (transform.go): a rule-claimed type's schema was discoverable
// through HandlerSchemas but was never actually enforced against what a rule emits.
func (t *Transformer) validateEmittedComponent(comp *Component) error {
	path := fmt.Sprintf("emitted component %q (type %q): properties", comp.Name, comp.Type)
	if h, ok := t.componentHandlers[comp.Type]; ok {
		return validateEmittedProperties(h, comp.Properties, path)
	}
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		return validateEmittedProperties(rule, comp.Properties, path)
	}
	return nil
}

// validateEmittedTrait is validateEmittedComponent for the trait position.
func (t *Transformer) validateEmittedTrait(trait *Trait) error {
	path := fmt.Sprintf("emitted trait %q: properties", trait.Type)
	if h, ok := t.traitHandlers[trait.Type]; ok {
		return validateEmittedProperties(h, trait.Properties, path)
	}
	if rule, ok := t.traitLoweringRules[trait.Type]; ok {
		return validateEmittedProperties(rule, trait.Properties, path)
	}
	return nil
}

// validateEmittedPolicy is validateEmittedComponent for the policy position: falls
// back to policyLoweringRules when no terminal handler claims pol.Type, exactly as
// validateEmittedComponent/validateEmittedTrait already fall back to their own
// *LoweringRule registries. Round-9-batch-2 Codex finding: this fallback was missing
// here, so a PolicyLoweringRule implementing PropertySchemaProvider went unenforced —
// a component/trait/document-position rule emitting a malformed intermediate policy
// (one still claimed by another registered PolicyLoweringRule) bypassed emission-time
// validation and reached that rule next round, where an assertion on the malformed
// properties could mis-lower or panic.
func (t *Transformer) validateEmittedPolicy(pol *ApplicationPolicy) error {
	path := fmt.Sprintf("emitted policy %q (type %q): properties", pol.Name, pol.Type)
	if h, ok := t.policyHandlers[pol.Type]; ok {
		return validateEmittedProperties(h, pol.Properties, path)
	}
	if rule, ok := t.policyLoweringRules[pol.Type]; ok {
		return validateEmittedProperties(rule, pol.Properties, path)
	}
	return nil
}

// validateEmittedProperties validates props against handler's schema, if handler
// declares one. PropertySchemaProvider is optional at every position, so a handler
// that declares nothing accepts anything — the same latitude it has on the authored
// path.
func validateEmittedProperties(handler any, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateProperties(p.PropertySchema(), props, path)
}

// validateEmittedDocument applies validateEmittedComponent/validateEmittedPolicy to
// every nested component and policy of a document a DocumentLoweringRule or
// RawDocumentLoweringRule just emitted — round-9 Codex regression (lowering.go:710):
// those rules hand back a whole *Application, and the only checks ever run against
// it are validatePositionResult (arity) and, once the fixpoint settles,
// validateSettled (identity/type allowlists only, no property schemas). A component
// that arrives already terminal-handler-typed inside such a document — rather than
// being emitted at its own component position — never passes through
// validateEmittedComponent at all: lowerDocumentBody's per-round component loop only
// calls it at the emission sites reached via componentLoweringRules dispatch, and a
// component whose type matches neither is carried forward via newComponents
// unchanged, at every round, forever. Calling this once, right after a document rule
// emits, closes that gap the same way emission-time validation already does at the
// component position itself.
//
// Deliberately does NOT validate traits (round-12-batch-2 Codex finding,
// lowering.go:717 as reviewed): at the point this runs, a document-rule branch has
// not yet determined which of comp.Traits are freshly synthesized versus forwarded
// unchanged from an authored component (sealNestedTraitsInDocument, called
// immediately after this in lowerDocumentOnce, is what tells the two apart). A
// forwarded trait has not gone through capability rendering yet and is not meant to
// look complete against its handler's full PropertySchema — validating it here,
// before forwarding is known, rejected a capability-aware trait whose
// capability-supplied required property was still pending, with a spurious "is
// required" error. Trait validation instead happens exclusively in the
// forwarding-aware pass every caller runs immediately after this: sealNestedTraits
// (via sealNestedTraitsInDocument here, sealEmittedNestedTraits in lowerRawOnce)
// calls validateEmittedTrait on every trait it does NOT skip as forwarded, so a
// freshly synthesized trait is still validated — just after forwarding is known,
// not before.
func (t *Transformer) validateEmittedDocument(app *Application) error {
	for i := range app.Spec.Components {
		if err := t.validateEmittedComponent(&app.Spec.Components[i]); err != nil {
			return err
		}
	}
	for i := range app.Spec.Policies {
		if err := t.validateEmittedPolicy(&app.Spec.Policies[i]); err != nil {
			return err
		}
	}
	return nil
}
