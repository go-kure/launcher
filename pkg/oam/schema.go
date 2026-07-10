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

// PropertySchema is the single constrained schema vocabulary describing one
// declared property across launcher: handler properties (via the
// PropertySchemaProvider interface, handler.go), kurel package parameters
// (ParameterDecl, package.go), and capability rendering properties
// (CapabilityRenderingSchema, types.go). Launcher-origin handlers expose it so
// crane can validate a component/trait's user-facing properties before invoking
// the handler.
//
// The rich fields (Enum, Properties, Items, AdditionalProperties) express nested
// and constrained schemas. They are only meaningful for handler properties: the
// two flat call sites (kurel parameters, capability rendering) reject them at
// decode time so unifying the type does not silently widen accepted behavior
// (see rejectUnsupportedSchemaKeys, flatschema.go, and adr#33).
//
// AdditionalProperties defaults to false: a handler that accepts arbitrary keys
// (an escape hatch, e.g. the passthrough component's `object`) sets it true.
type PropertySchema struct {
	// Type is the value type. Required.
	Type PropertyType `json:"type" yaml:"type"`
	// Description is human-facing prose for the property, surfaced in generated
	// API references (e.g. crane's Handler API Reference). Optional.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Required marks the property as mandatory.
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
	// Default is the value applied when the property is absent.
	Default any `json:"default,omitempty" yaml:"default,omitempty"`
	// Enum, when non-empty, constrains the value to this set.
	Enum []any `json:"enum,omitempty" yaml:"enum,omitempty"`
	// Properties describes the fields of a Type==object value.
	Properties map[string]PropertySchema `json:"properties,omitempty" yaml:"properties,omitempty"`
	// Items describes the element schema of a Type==array value.
	Items *PropertySchema `json:"items,omitempty" yaml:"items,omitempty"`
	// AdditionalProperties allows keys beyond those in Properties (object types).
	// Defaults to false.
	AdditionalProperties bool `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
}
