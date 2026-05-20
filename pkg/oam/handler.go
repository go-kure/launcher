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
