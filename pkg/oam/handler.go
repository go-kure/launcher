package oam

import "github.com/go-kure/kure/pkg/stack"

// ComponentHandler handles transformation of a specific OAM component type
// into a kure ApplicationConfig.
type ComponentHandler interface {
	CanHandle(componentType string) bool
	ToApplicationConfig(component *Component, namespace string) (stack.ApplicationConfig, error)
}

// TraitHandler handles application of a specific OAM trait type to a kure
// Application and Bundle.
type TraitHandler interface {
	CanHandle(traitType string) bool
	Apply(trait *Trait, app *stack.Application, bundle *stack.Bundle) error
}

// CapabilityAware is an optional interface for TraitHandlers that require a
// matching ClusterProfile capability to produce correct output. If
// CapabilityRequired returns true and no capability resolves for the trait,
// the runtime returns ErrMissingCapability.
type CapabilityAware interface {
	CapabilityRequired() bool
}

// PropertySchemaProvider is an optional interface implemented by component and
// trait handlers that declare a schema for their user-facing properties. The
// downstream runtime's validator consumes these schemas (via Transformer.HandlerSchemas) to validate a
// component/trait's properties before the handler is invoked. Handlers that accept
// arbitrary keys declare an open field with PropertySchema.AdditionalProperties.
type PropertySchemaProvider interface {
	PropertySchema() map[string]PropertySchema
}

// SourceDeduplicatable is an optional interface for ApplicationConfig types
// that generate source CRDs (e.g. HelmRepository). The runtime uses it to
// suppress duplicate source generation when multiple components share the
// same source key (URL for HelmRepository, URL+version for OCIRepository);
// first component wins.
type SourceDeduplicatable interface {
	GetSourceKey() string
	GetSourceRefName() string
	SuppressSourceGeneration(refName string)
}

// ComponentNamed is an optional interface for trait/component sub-app
// ApplicationConfig types that expose the OAM component they were emitted for.
// Consumers use it to attribute each emitted resource to its owning component
// (e.g. a provenance label) without re-deriving the component from sub-app names.
type ComponentNamed interface {
	ComponentName() string
}
