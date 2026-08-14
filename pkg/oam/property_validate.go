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
// call-site-specific messages.

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
		// Required would let a required field through empty.
		if v, present := props[key]; !present || v == nil {
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
func validatePropertyValue(schema PropertySchema, value any, path string) (any, error) {
	if value == nil {
		// Absent/null. Presence is enforced by Required at the caller; a nil under
		// an optional field constrains nothing.
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
				normalized, err := validatePropertyValue(*schema.Items, item, fmt.Sprintf("%s[%d]", path, i))
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

	if len(schema.Enum) > 0 && !enumContainsValue(schema.Enum, value) {
		return value, errors.Errorf("%s: value %v not in allowed set %v", path, value, schema.Enum)
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

// validateEmittedPolicy is validateEmittedComponent for the policy position.
func (t *Transformer) validateEmittedPolicy(pol *ApplicationPolicy) error {
	h, ok := t.policyHandlers[pol.Type]
	if !ok {
		return nil
	}
	return validateEmittedProperties(h, pol.Properties,
		fmt.Sprintf("emitted policy %q (type %q): properties", pol.Name, pol.Type))
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
