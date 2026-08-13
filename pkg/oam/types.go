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
	// An authored trait is never sealed.
	sealed bool
}

// Origin returns the trait's authored provenance and whether the lowering engine ever
// stamped one.
func (t Trait) Origin() (Origin, bool) {
	if t.origin == nil {
		return Origin{}, false
	}
	return *t.origin, true
}

// ApplicationPolicy defines an application-level policy entry passed through to the runtime unchanged.
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
