package oam

// PropertyType is the constrained set of value types a handler property may
// declare. It mirrors the JSON-schema scalar/compound vocabulary that the
// downstream runtime's validator understands.
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
// the downstream runtime can validate a component/trait's user-facing properties before invoking
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
	// Type is the value type. Required unless Types is set; the two are mutually
	// exclusive.
	Type PropertyType `json:"type" yaml:"type"`
	// Types declares a union: the value is accepted when any one of the listed
	// types accepts it as a single Type would, whatever the order they are listed
	// in, and is normalized by the first listed type that accepts it. It is
	// how a Kubernetes int-or-string field (intstr.IntOrString, e.g. a rolling
	// update's maxUnavailable or a port) or a quantity (a string or a bare number)
	// is published (go-kure/launcher#383).
	//
	// Members must be at least two distinct scalar types (string, integer, number,
	// boolean); a union with an array or object member would need to say which
	// member's Properties/Items apply, which this vocabulary does not express.
	// Setting both Type and Types, or a malformed union, is a schema error that
	// validatePropertyValue reports as soon as a value reaches the leaf.
	//
	// Type stays empty on a union leaf, deliberately: a consumer that predates Types
	// reads an empty Type as "no declared type" and keeps accepting every member,
	// so the field is additive for it. Like Enum/Properties/Items, Types is only
	// meaningful for handler properties — the two flat call sites reject the key
	// at decode time (flatschema.go's key allow-sets).
	Types []PropertyType `json:"types,omitempty" yaml:"types,omitempty"`
	// Description is human-facing prose for the property, surfaced in generated
	// API references (e.g. the downstream runtime's Handler API Reference). Optional.
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
	// PlatformReserved marks a property as platform-supplied only (D3): its value
	// may arrive solely via ClusterProfile capability rendering, never authored
	// inline. enforcePlatformReserved (property_validate.go) rejects an authored
	// value before capability rendering is merged in. Meaningful only for handler
	// properties, like Enum/Properties/Items/AdditionalProperties above — the two
	// flat call sites (kurel parameters, capability rendering) reject it at decode
	// time (flatschema.go's key allow-sets), since a rendering schema describing what
	// the platform may set cannot itself be platform-reserved.
	//
	// Reservation belongs to each SCHEMA DECLARATION, never to a shared schema
	// fragment: a fragment shared by several handler schemas takes reservation as an
	// explicit parameter — schemaNetworkPolicy(reserved bool), builtin/traits — so
	// every sharing schema states its own answer at its own call site. A default
	// baked into the shared fragment would reserve the property for callers that
	// deliberately do not want it reserved, and nothing would keep the sharers from
	// silently diverging as they evolve independently.
	PlatformReserved bool `json:"platformReserved,omitempty" yaml:"platformReserved,omitempty"`
}
