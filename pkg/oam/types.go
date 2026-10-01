package oam

// Application represents an OAM Application resource.
type Application struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   Metadata        `yaml:"metadata"`
	Spec       ApplicationSpec `yaml:"spec"`

	// origin is the authored top-level document this element traces back to, set by
	// the lowering engine (lowering.go) on every document it emits. Never encoded to
	// or decoded from YAML (unexported; yaml.v3 ignores it in both directions), and
	// nil for a document that was never touched by the fixpoint. See Origin.
	origin *Origin
}

// Origin returns the element's authored provenance and whether the lowering engine
// ever stamped one. A document parsed and never lowered returns (Origin{}, false).
func (a Application) Origin() (Origin, bool) {
	if a.origin == nil {
		return Origin{}, false
	}
	return *a.origin, true
}

// Metadata contains standard Kubernetes-style metadata fields.
type Metadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// ApplicationSpec defines the components and policies of an OAM application.
type ApplicationSpec struct {
	Components []Component         `yaml:"components"`
	Policies   []ApplicationPolicy `yaml:"policies,omitempty"`
}

// Component represents a single component within an OAM application.
type Component struct {
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`
	Properties  map[string]any    `yaml:"properties"`
	Traits      []Trait           `yaml:"traits,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`

	origin *Origin
	// synthesized marks a component a lowering rule emitted rather than forwarded:
	// its properties are the rule's own output, which may carry a PlatformReserved
	// value the rule rendered from LoweringContext.Capabilities, so the D3 check on
	// AUTHORED values (enforcePlatformReserved) does not apply to it — the component
	// counterpart of Trait.synthesized. The engine sets it at each emission site
	// (lowering.go); a rule in another package cannot, and an authored component is
	// never synthesized. A rule's output is marked only when the rule's input was
	// checked: the rule declares a schema (PropertySchemaProvider), or its input
	// component or trait is itself synthesized; any other rule's output is checked as
	// authored. A trait rule's output over a sealed trait that is not synthesized
	// stays authored, schema or not: the check its schema runs there covers the
	// trait's own reserved keys, not those of what the rule emits. A
	// document rule's output is never synthesized, since nothing checks its whole
	// input (go-kure/launcher#612); a component it forwards (pointer-identical,
	// isForwardedComponent) keeps the value it arrived with. What a user wrote is still
	// checked before any rule can rewrite it: before a ComponentLoweringRule
	// (lowerDocumentBody) and before a DocumentLoweringRule
	// (enforceAuthoredReservations).
	synthesized bool
}

// Origin returns the component's authored provenance and whether the lowering engine
// ever stamped one.
func (c Component) Origin() (Origin, bool) {
	if c.origin == nil {
		return Origin{}, false
	}
	return *c.origin, true
}

// Trait represents an operational behavior attached to a component.
type Trait struct {
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties"`

	origin *Origin
	// sealed marks a trait emitted by a lowering rule (D5): its properties are the
	// rule's own deterministic output, so applyTraits must not merge a second
	// ClusterProfile capability rendering into it — that would make the trait's
	// output depend on a fifth input the information-closure rule does not allow.
	// An authored trait is never sealed. Sealing says nothing about whether the
	// trait's content was checked; Trait.synthesized does.
	sealed bool
	// synthesized marks a sealed trait whose emitting rule's input was checked, the
	// trait counterpart of Component.synthesized and set under the same rule at each
	// emission site (lowering.go): its properties are the rule's own output, so the
	// D3 check on authored values (enforcePlatformReserved) does not apply to it.
	// Any other trait — authored, forwarded, or sealed by a rule whose input was not
	// checked — is checked by D3 before a schema-declaring TraitLoweringRule
	// (lowerDocumentBody) and in applyTraits. Every trait a document rule builds is
	// unsynthesized (go-kure/launcher#612). Only a sealed trait is ever synthesized.
	synthesized bool
	// forwardedFrom is set only while a ComponentLoweringRule runs: the engine
	// hands the rule a marked copy of the component's traits (forwardableTraits,
	// lowering.go), so a by-value copy the rule forwards is still recognised as
	// forwarded rather than synthesized (isForwardedTrait). The engine clears it
	// again once it has classified what the rule emitted.
	forwardedFrom *Trait
	// authoredIndex is the index a forwarded trait held in the traits slice the
	// component rule was handed, recorded when the engine classifies it as forwarded
	// (sealEmittedNestedTraits). A rule that adds a trait of its own ahead of the
	// forwarded ones shifts their position in its output; the origin fallback in
	// lowerDocumentBody reads this instead, so Origin.Index still names the authored
	// slot. nil for a trait never forwarded that way. It is a hint for that
	// fallback only: the trait stays unstamped, exactly as authored.
	authoredIndex *int
}

// Origin returns the trait's authored provenance and whether the lowering engine ever
// stamped one.
func (t Trait) Origin() (Origin, bool) {
	if t.origin == nil {
		return Origin{}, false
	}
	return *t.origin, true
}

// ApplicationPolicy defines an application-level policy entry. The transform
// dispatches it by Type to the PolicyHandler registered for that type (see
// Transformer.RegisterPolicy), which records its effect on the PolicyResult; a
// type with no registered handler is a transform error. kurel registers the
// built-in handlers in pkg/oam/builtin/policies.
type ApplicationPolicy struct {
	Name       string         `yaml:"name"`
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties,omitempty"`

	origin *Origin
}

// Origin returns the policy's authored provenance and whether the lowering engine
// ever stamped one.
func (p ApplicationPolicy) Origin() (Origin, bool) {
	if p.origin == nil {
		return Origin{}, false
	}
	return *p.origin, true
}

// CapabilityDefinition declares the rendering schema for a custom capability trait type.
// metadata.name is the trait type. Scope: platform-facing rendering schema only
// (what keys a ClusterProfile capability binding may contain).
// See docs/oam/design-capability-schema.md §3.2.
type CapabilityDefinition struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   Metadata          `yaml:"metadata"` // metadata.name = trait type
	Spec       CapabilityDefSpec `yaml:"spec"`
}

// CapabilityDefSpec holds the rendering schema declaration.
type CapabilityDefSpec struct {
	Description string                    `yaml:"description,omitempty"`
	Rendering   CapabilityRenderingSchema `yaml:"rendering,omitempty"`
}

// CapabilityRenderingSchema lists the accepted rendering properties. Each
// property is a PropertySchema restricted to the flat vocabulary
// (type/required/default/description) — the rich fields are rejected at decode
// time by UnmarshalYAML (flatschema.go). Accepted types: "string", "integer",
// "boolean" (enforced by LoadCapabilityDefinitions).
type CapabilityRenderingSchema struct {
	Properties map[string]PropertySchema `yaml:"properties,omitempty"`
}
