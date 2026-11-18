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
// The rich fields (Enum, Properties, Items, AdditionalProperties,
// AdditionalPropertiesSchema) express nested
// and constrained schemas. They are only meaningful for handler properties: the
// two flat call sites (kurel parameters, capability rendering) reject them at
// decode time so unifying the type does not silently widen accepted behavior
// (see rejectUnsupportedSchemaKeys and flatschema.go).
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
	// AdditionalPropertiesSchema types the values of the keys a Type==object node
	// leaves open: every key not named in Properties holds a value of this schema,
	// the way Items types the elements of an array. A non-nil schema opens the object
	// (undeclared keys are accepted) whatever AdditionalProperties says, so the bool
	// stays the shorthand for "any value" and this field is the typed form. Properties
	// and a value schema together are legal: a declared key uses its own schema, every
	// other key this one. A null under such a key is stripped, as a declared optional
	// key's null is, and the value that remains is validated. Meaningful only on a
	// Type==object node: elsewhere it is a schema error, as Exclusive is. Like
	// Enum/Properties/Items, only meaningful for handler properties — the two flat call
	// sites reject the key at decode time (flatschema.go's key allow-sets).
	//
	// A consumer that predates it reads the key as an object that accepts any value,
	// as it did before the field existed; the field is additive for it.
	AdditionalPropertiesSchema *PropertySchema `json:"additionalPropertiesSchema,omitempty" yaml:"additionalPropertiesSchema,omitempty"`
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
	// Exclusive names groups of this object's Properties keys of which a value sets
	// exactly one (ExclusiveGroup.Required) or at most one: the published form of an
	// API's "exactly one of" (go-kure/launcher#790). Meaningful only on a Type==object
	// node; a handler's top-level keys take their groups from
	// ExclusivePropertiesProvider, since the top level is a map with no node of its
	// own. Like Enum/Properties/Items, Exclusive is only meaningful for handler
	// properties — the two flat call sites reject the key at decode time
	// (flatschema.go's key allow-sets).
	//
	// A consumer that predates Exclusive reads each key as the optional key it is
	// declared as, so it accepts a value setting both or neither, as it did before
	// the field existed; the field is additive for it. A malformed group (fewer than
	// two keys, a key Properties does not declare, a key listed twice or declared
	// Required) is a schema error validatePropertyValue reports as soon as a value
	// reaches the object.
	Exclusive []ExclusiveGroup `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
	// Gate names the Kubernetes feature gate (a row of the lifecycle table in
	// featuregates.go) the property needs on the cluster. When the ClusterProfile
	// states a target version (spec.kubernetesVersion) below the version where the
	// gate is on by default, an authored value is refused
	// (go-kure/launcher#1183), unless the profile's spec.featureGates sets the gate on
	// (go-kure/launcher#1231); a gate set off there refuses it at any version. With no
	// target version nothing is checked. Like
	// PlatformReserved, only meaningful for handler properties: the two flat call
	// sites reject the key at decode time (flatschema.go's key allow-sets).
	//
	// A consumer that predates Gate ignores it and accepts the value, as it did
	// before the field existed; the field is additive for it.
	Gate string `json:"gate,omitempty" yaml:"gate,omitempty"`
	// GatedPaths names the feature gates of fields below this node that its schema
	// does not declare: a node left open (AdditionalProperties with no Properties)
	// takes its content from a strict decode into a Kubernetes type, so the gate
	// check cannot find a Gate there (go-kure/launcher#1245). A key is a path
	// relative to the node, dotted, with "[]" after the key of an array whose every
	// element is walked (`env[].valueFrom.fileKeyRef`) and which matches the authored
	// keys case-insensitively, as the decode does; the value is the gate, a row
	// of the lifecycle table in featuregates.go, with Gate's meaning. Only the gate
	// check reads it: property validation does not, so what a document is accepted
	// as, and the text of its other refusals, are the same with or without it. Like
	// Gate, only meaningful for handler properties: the two flat call sites reject
	// the key at decode time (flatschema.go's key allow-sets).
	//
	// A consumer that predates GatedPaths ignores it and accepts the value, as it did
	// before the field existed; the field is additive for it.
	GatedPaths map[string]string `json:"gatedPaths,omitempty" yaml:"gatedPaths,omitempty"`
	// ValueGatedPaths is GatedPaths for a field of which only some values need a gate
	// (go-kure/launcher#1330): an open node's schema cannot declare ValueGates on a field
	// below it, so it names them here. The outer key is a path, written as in GatedPaths
	// ("[]" after the key of an array whose every element is walked, matched
	// case-insensitively against the authored keys); the inner key is a value of the
	// field and the inner value is its gate, a row of the lifecycle table in
	// featuregates.go. An authored string value listed there is refused below the release
	// where its gate is on by default, with the path, the value, the gate, the release and
	// the target, as ValueGates refuses it. The value match is exact. Any other value of
	// the field, a null and a value that is not a string are accepted. Like GatedPaths,
	// only the gate check reads it, so what a document is accepted as is the same with or
	// without it, and it is only meaningful for handler properties: the two flat call
	// sites reject the key at decode time (flatschema.go's key allow-sets leave it out).
	//
	// A consumer that predates ValueGatedPaths ignores it and accepts the value, as it did
	// before the field existed; the field is additive for it.
	ValueGatedPaths map[string]map[string]string `json:"valueGatedPaths,omitempty" yaml:"valueGatedPaths,omitempty"`

	// ValueGates names, per Enum value, the Kubernetes feature gate (a row of the
	// lifecycle table in featuregates.go) that value alone needs on the cluster: the
	// property is accepted at every release, but an authored value listed here is
	// refused below the release where its gate is on by default, exactly as Gate
	// refuses the whole property (go-kure/launcher#1230). A key must be an Enum
	// value and a gate must name a row of the table; a handler's schema that breaks
	// either is a launcher defect. The schema tests report both; at run time the
	// gate pass reports only an unknown gate, and only when an authored value
	// reaches that key, and never checks a key against Enum.
	// Meaningful on a Type==string node with an Enum, and only for handler
	// properties: the two flat call sites reject the key at decode time, as they do
	// Gate (flatschema.go's key allow-sets leave it out).
	//
	// A consumer that predates ValueGates ignores it and accepts the value, as it
	// did before the field existed; the field is additive for it.
	ValueGates map[string]string `json:"valueGates,omitempty" yaml:"valueGates,omitempty"`
	// BoundedBy names the classes of refusal by the environment policy that an
	// authored value at this property can cause (go-kure/launcher#1580): a
	// consumer that builds an input form reads here which properties the
	// environment limits. It names the class and never the limit, which is the
	// policy's. On an open node (an object that takes keys the schema does not
	// declare, or an array of such items) it lists every class a value at an
	// undeclared key below the node can cause, with no path; a property the node
	// does declare carries its own. For an array of open items the list is on the
	// array where its items declare no property, and on the items where they do.
	//
	// It is a published fact, not a rule: nothing reads it to accept or refuse a
	// value, so a document is accepted as it is with or without it. A built-in
	// type declares only what a test holds, in one direction: a declared class is
	// shown to refuse a value at that property, and a property that declares none
	// is not shown to be unbounded. A class that is about a type as a whole, not
	// about one property (RefusalTraitCapability, RefusalObjectKind), is declared
	// nowhere. The classes are listed in RefusalClasses order.
	//
	// Like PlatformReserved, it belongs to each schema declaration and never to a
	// shared schema fragment (DeclareBounds), and it is only meaningful for
	// handler properties: the two flat call sites reject the key at decode time
	// (flatschema.go's key allow-sets leave it out), and a model input may not
	// declare it (pkg/oam/model).
	//
	// A consumer that predates BoundedBy ignores it; the field is additive for it.
	BoundedBy []RefusalClass `json:"boundedBy,omitempty" yaml:"boundedBy,omitempty"`
}

// ExclusiveGroup is one group of PropertySchema.Exclusive: keys of one object, at
// most one of which a value sets, and exactly one when Required. A key counts as set
// when the Required check would count it present — not absent and not a null.
//
// Required follows PropertySchema.Required wherever that one is relaxed: it is not
// enforced on an authored document's top level, and a capability-bound trait's
// nested groups are checked on the properties merged with the capability rendering.
// At most one is enforced at every level.
type ExclusiveGroup struct {
	// Keys are the group's keys, at least two.
	Keys []string `json:"keys" yaml:"keys"`
	// Required makes the group exactly one of Keys rather than at most one.
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
}
