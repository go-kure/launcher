package oam

// PropertyType is the constrained set of value types a handler property may
// declare. It mirrors the JSON-schema scalar/compound vocabulary that crane's
// validator understands.
type PropertyType string

const (
	PropertyTypeString  PropertyType = "string"
	PropertyTypeInteger PropertyType = "integer"
	PropertyTypeBoolean PropertyType = "boolean"
	PropertyTypeNumber  PropertyType = "number"
	PropertyTypeArray   PropertyType = "array"
	PropertyTypeObject  PropertyType = "object"
)

// PropertySchema is the constrained schema vocabulary describing one handler
// property. It is the richer sibling of CapabilityPropertySchema (types.go),
// which models only the custom-trait YAML rendering fields (type/required/
// default/description). Launcher-origin handlers expose PropertySchema via the
// PropertySchemaProvider interface (handler.go) so crane can validate a
// component/trait's user-facing properties before invoking the handler.
//
// AdditionalProperties defaults to false: a handler that accepts arbitrary keys
// (an escape hatch, e.g. the passthrough component's `object`) sets it true.
type PropertySchema struct {
	// Type is the value type. Required.
	Type PropertyType `json:"type"`
	// Description is human-facing prose for the property, surfaced in generated
	// API references (e.g. crane's Handler API Reference). Optional.
	Description string `json:"description,omitempty"`
	// Required marks the property as mandatory.
	Required bool `json:"required,omitempty"`
	// Default is the value applied when the property is absent.
	Default any `json:"default,omitempty"`
	// Enum, when non-empty, constrains the value to this set.
	Enum []any `json:"enum,omitempty"`
	// Properties describes the fields of a Type==object value.
	Properties map[string]PropertySchema `json:"properties,omitempty"`
	// Items describes the element schema of a Type==array value.
	Items *PropertySchema `json:"items,omitempty"`
	// AdditionalProperties allows keys beyond those in Properties (object types).
	// Defaults to false.
	AdditionalProperties bool `json:"additionalProperties,omitempty"`
}
