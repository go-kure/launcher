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
// Scope note, deliberate: these functions run on EMITTED elements only. Authored
// documents still pass through validate.go, which checks type names and identity
// but not property shape. Wiring the authored path through here as well is a
// behaviour change for existing users' documents and is out of scope for the
// lowering engine; it is why the emission-time check exists as its own entry point
// rather than as a call inside validate().
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
// original, still-typed value silently surviving unassertable.
//
// A null never reaches here as a whole property value: validateObjectProperties
// strips an optional one and rejects a required one before calling. The one
// remaining way a nil arrives is as an ARRAY ELEMENT, through the per-element
// recursion below — and it must fail there, because the handler parsers assert
// concrete element types (parseStringList's item.(string),
// builtin/components/podspec.go:644-646). An earlier early return here accepted such
// an element and let it reach a parser that then rejected it — a schema/parser
// divergence the schema itself could not express.
//
// The ordinary type switch is NOT sufficient to reject one, which is why the array
// case guards its elements explicitly rather than relying on the switch. The switch
// does handle an UNTYPED nil: isStringValue(nil) is false, so `values: [null]` under
// Items{Type: string} reports "expected string, got <nil>". But a TYPED nil
// collection satisfies the plain type assertion each coercer tries first —
// asArrayValue([]any(nil)) and asObjectValue(map[string]any(nil)) both return
// (nil, true) — and iterating the resulting empty collection rejects nothing, so
// `items: [null]` under Items{Type: object} passed on exactly that path. The
// `case "":` branch checks nothing at all, so an untyped Items schema accepted a null
// element too. Both are why the guard is a guard and not a comment.
func validatePropertyValue(schema PropertySchema, value any, path string) (any, error) {
	switch schema.Type {
	case "":
		// No declared type: nothing to check beyond Enum below. Reachable for a
		// schema built for a call site that leaves Type empty (flatschema.go).
	case PropertyTypeString:
		if !isStringValue(value) {
			return value, errors.Errorf("%s: expected string, got %T", path, value)
		}
	case PropertyTypeBoolean:
		if !isBooleanValue(value) {
			return value, errors.Errorf("%s: expected boolean, got %T", path, value)
		}
	case PropertyTypeInteger:
		if !isIntegerValue(value) {
			return value, errors.Errorf("%s: expected integer, got %T (%v)", path, value, value)
		}
	case PropertyTypeNumber:
		if !isNumberValue(value) {
			return value, errors.Errorf("%s: expected number, got %T", path, value)
		}
	case PropertyTypeArray:
		items, ok := asArrayValue(value)
		if !ok {
			return value, errors.Errorf("%s: expected array, got %T", path, value)
		}
		if schema.Items != nil {
			for i, item := range items {
				elemPath := fmt.Sprintf("%s[%d]", path, i)
				// A null is never a member of any Items type. An element cannot be
				// "absent" the way an object key can — it is present by being in the
				// list, and deleting it would renumber its siblings — so the contract
				// makes it a type error instead. Guarded here rather than left to the
				// type switch below because a typed nil survives that switch; see the
				// doc comment above.
				if isNullValue(item) {
					return value, errors.Errorf("%s: null is not a valid array element", elemPath)
				}
				normalized, err := validatePropertyValue(*schema.Items, item, elemPath)
				if err != nil {
					return value, err
				}
				items[i] = normalized
			}
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
		// written. So a member holding a null at any depth can never match a value that
		// reached this line: Enum{{"x": nil}} stopped matching `{x: null}` the moment
		// the strip existed. Normalizing members instead would make Enum an eighth
		// reader of "null" to keep aligned forever; restricting Enum to the scalar
		// types removes the reader instead, and no built-in schema declares one on a
		// non-scalar today (asserted by TestBuiltinHandlerSchemaEnumsAreScalar).
		if schema.Type == PropertyTypeArray || schema.Type == PropertyTypeObject {
			return value, errors.Errorf("%s: schema declares Enum on non-scalar type %q", path, schema.Type)
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
// and a reserved null is never flagged. Latent today: all eight PlatformReserved
// declarations are on trait schemas, none on a component schema — which also means
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

// isNumberValue accepts any Go integer or floating-point kind.
func isNumberValue(value any) bool {
	_, ok := asFloatValue(value)
	return ok
}

// isNullValue reports whether value is a bare nil interface OR a typed nil map,
// slice, pointer, channel or function — round-9 Codex regression (property_validate.go:63):
// asArrayValue/asObjectValue's type assertions succeed on a typed nil
// ([]any(nil), map[string]any(nil)) with ok=true, so a plain `value == nil` check
// lets one through as a present, validly-typed empty collection even though it
// serializes to JSON/YAML `null`, not `[]`/`{}`. A rule that assigns an
// uninitialized Go slice or map to a Properties entry hits this by construction,
// with no unusual authoring required.
func isNullValue(value any) bool {
	if value == nil {
		return true
	}
	switch rv := reflect.ValueOf(value); rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
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
// float64(80) and string vs named-string-type comparisons are routine and are the
// same value by every meaning a user has. Anything not string-kinded or
// numeric-kinded falls back to DeepEqual.
func enumContainsValue(enum []any, value any) bool {
	for _, e := range enum {
		if equalPropertyValues(e, value) {
			return true
		}
	}
	return false
}

func equalPropertyValues(a, b any) bool {
	if sa, ok := asStringValue(a); ok {
		sb, ok := asStringValue(b)
		return ok && sa == sb
	}
	if fa, ok := asFloatValue(a); ok {
		// Bool is neither string- nor numeric-kinded, so it never reaches here.
		fb, ok := asFloatValue(b)
		return ok && fa == fb
	}
	return reflect.DeepEqual(a, b)
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
