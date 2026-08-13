package oam

import (
	"fmt"
	"reflect"

	"github.com/go-kure/launcher/pkg/errors"
)

// validateProperties checks props against schema — a handler's top-level declared
// property set — enforcing every Required field's presence and every present
// field's Type/Enum/nested Properties/Items/AdditionalProperties (D4: an emitted
// element is validated against its target schema exactly as a user-authored one
// would be). The top level itself never tolerates an undeclared key: handlers
// declare their complete accepted property set, so there is no top-level
// AdditionalProperties escape — that only applies within a nested object field.
func validateProperties(schema map[string]PropertySchema, props map[string]any, path string) error {
	return validateObjectProperties(schema, false, props, path)
}

// validateObjectProperties is validateProperties widened with additionalAllowed, for
// recursing into a nested object field whose schema declares
// PropertySchema.AdditionalProperties.
func validateObjectProperties(schema map[string]PropertySchema, additionalAllowed bool, props map[string]any, path string) error {
	for key, field := range schema {
		if !field.Required {
			continue
		}
		if _, present := props[key]; !present {
			return errors.Errorf("%s: %q is required", path, key)
		}
	}
	for key, value := range props {
		field, ok := schema[key]
		if !ok {
			if additionalAllowed {
				continue
			}
			return errors.Errorf("%s: unsupported field %q", path, key)
		}
		if err := validatePropertyValue(field, value, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

// validatePropertyValue checks one value against its declared PropertySchema: the
// value's Go type matches schema.Type, an array's elements match schema.Items, an
// object's fields recurse through validateObjectProperties, and — after the type
// check — the value is a member of schema.Enum when one is declared.
func validatePropertyValue(schema PropertySchema, value any, path string) error {
	if value == nil {
		// Absent/null: presence is enforced by Required at the caller, not here.
		return nil
	}

	switch schema.Type {
	case PropertyTypeString:
		if _, ok := value.(string); !ok {
			return errors.Errorf("%s: expected string, got %T", path, value)
		}
	case PropertyTypeBoolean:
		if _, ok := value.(bool); !ok {
			return errors.Errorf("%s: expected boolean, got %T", path, value)
		}
	case PropertyTypeInteger:
		if !isIntegerValue(value) {
			return errors.Errorf("%s: expected integer, got %T", path, value)
		}
	case PropertyTypeNumber:
		if !isNumberValue(value) {
			return errors.Errorf("%s: expected number, got %T", path, value)
		}
	case PropertyTypeArray:
		items, ok := value.([]any)
		if !ok {
			return errors.Errorf("%s: expected array, got %T", path, value)
		}
		if schema.Items != nil {
			for i, item := range items {
				if err := validatePropertyValue(*schema.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case PropertyTypeObject:
		obj, ok := value.(map[string]any)
		if !ok {
			return errors.Errorf("%s: expected object, got %T", path, value)
		}
		if schema.Properties != nil {
			if err := validateObjectProperties(schema.Properties, schema.AdditionalProperties, obj, path); err != nil {
				return err
			}
		}
	}

	if len(schema.Enum) > 0 && !enumContainsValue(schema.Enum, value) {
		return errors.Errorf("%s: value %v not in allowed set %v", path, value, schema.Enum)
	}
	return nil
}

// isIntegerValue accepts any Go integer kind, plus a float64/float32 with no
// fractional part — YAML numeric literals decode to float64 when the surrounding
// value came through interface{}, and a lowering rule may construct either.
func isIntegerValue(value any) bool {
	switch v := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return v == float64(int64(v))
	case float32:
		return v == float32(int64(v))
	}
	return false
}

// isNumberValue accepts any Go integer or floating-point kind.
func isNumberValue(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	}
	return false
}

func enumContainsValue(enum []any, value any) bool {
	for _, e := range enum {
		if reflect.DeepEqual(e, value) {
			return true
		}
	}
	return false
}

// validateEmittedComponent checks an emitted component's Properties against its
// target ComponentHandler's declared schema (D4), when that handler declares one.
// A component whose type has no registered handler — because it is itself
// lowerable, or the fixpoint has not settled — is left for the post-fixpoint
// whole-document validation pass; there is no target schema to check against yet.
func (t *Transformer) validateEmittedComponent(comp *Component) error {
	h, ok := t.componentHandlers[comp.Type]
	if !ok {
		return nil
	}
	p, ok := h.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateProperties(p.PropertySchema(), comp.Properties, "properties")
}

// validateEmittedTrait is validateEmittedComponent for the trait position.
func (t *Transformer) validateEmittedTrait(trait *Trait) error {
	h, ok := t.traitHandlers[trait.Type]
	if !ok {
		return nil
	}
	p, ok := h.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateProperties(p.PropertySchema(), trait.Properties, "properties")
}

// validateEmittedPolicy is validateEmittedComponent for the policy position.
func (t *Transformer) validateEmittedPolicy(pol *ApplicationPolicy) error {
	h, ok := t.policyHandlers[pol.Type]
	if !ok {
		return nil
	}
	p, ok := h.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateProperties(p.PropertySchema(), pol.Properties, "properties")
}
