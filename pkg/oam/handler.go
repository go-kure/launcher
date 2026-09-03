package oam

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

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

// ContractMetadata is optional registration metadata describing the contract a
// handler or lowering rule implements: its family, version, the ClusterProfile
// capability keys it requires, and deprecation status. It is primarily a discovery/
// documentation surface — the engine does not enforce any of these fields
// (CapabilityAware.CapabilityRequired is what the engine actually enforces;
// RequiredCapabilityKeys here is declarative, for a consumer to introspect). The one
// exception is Version: loweringRuleIdentity (lowering.go) reads it to compose the
// "@<version>" suffix on Origin.Rule for a lowering rule that also implements
// ContractDescriber — nothing else on this struct is read or enforced by the engine.
type ContractMetadata struct {
	// Family is the contract family this handler/rule belongs to, e.g. "webservice".
	Family string
	// Version is the contract version within Family, e.g. "v1".
	Version string
	// RequiredCapabilityKeys lists the ClusterProfile capability keys ("<type>" or
	// "<type>.<scope>", see buildCapabilityKey) an entity of this contract needs in
	// the profile to produce correct output.
	RequiredCapabilityKeys []string
	// Deprecated marks the contract as deprecated.
	Deprecated bool
	// DeprecationMessage is guidance shown when Deprecated is true (e.g. pointing at
	// a replacement contract). May be "" even when Deprecated is true.
	DeprecationMessage string
}

// ContractDescriber is an optional interface implemented by component/trait handlers
// and component/trait lowering rules that declare contract metadata: family,
// version, required capability keys, and deprecation info. Queryable at
// registration time alongside PropertySchema(), through
// Transformer.HandlerContracts() — the HandlerSchemas-shaped accessor covering all
// four registries a component/trait type can be claimed by (componentHandlers,
// traitHandlers, componentLoweringRules, traitLoweringRules; see HandlerSchemas'
// own comments for why the lowering-rule registries must be included, not just the
// two dispatchable maps). Metadata rides the existing registration mechanism —
// there is no separate contract registry. Consumers: schema publication, artifact
// provenance in a downstream consumer, deprecation tooling. A lowering rule that
// also implements ContractDescriber has its Version folded into the lowering-rule
// identity Origin.Rule records (lowering.go), e.g. "trait/expose@v1".
type ContractDescriber interface {
	ContractMetadata() ContractMetadata
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

// ServiceAccountNamer is an optional interface for component ApplicationConfig
// types whose pods run as a ServiceAccount. It returns the effective name: the
// authored `serviceAccountName` when the component set one, else the
// per-component ServiceAccount the handler generates (named after the
// component). Traits that bind RBAC or other identity to the workload (the rbac
// trait's RoleBinding subject) read this instead of assuming the component name,
// so an authored serviceAccountName is honoured end to end.
type ServiceAccountNamer interface {
	ServiceAccountName() string
}

// LayoutAugmentationCoverage is an optional interface for ApplicationConfig
// types that also implement kure's layout.LayoutAugmenter. It answers, for a
// consumer that never constructs or walks a layout.ManifestLayout (e.g.
// pkg/cmd/kurel's flat-YAML build path): would skipping AugmentLayout lose
// anything? true only when Generate's own output is already a complete
// superset of what AugmentLayout adds — e.g. a helmchart component with
// delivery: template, whose AugmentLayout only repartitions Generate's flat
// union into hook-ordered child layouts and adds nothing new. false, or this
// interface being absent altogether, is the fail-closed default: it means
// AugmentLayout adds something Generate's own output does not already
// contain — e.g. a helmchart component with valuesMode: configMap, whose
// AugmentLayout emits a values ConfigMap that Generate's HelmRelease
// references but never emits itself. A LayoutAugmenter implementation that
// does not also implement this interface is always treated as false: an
// augmenter a consumer doesn't know the coverage of must never be assumed
// safe to skip.
type LayoutAugmentationCoverage interface {
	GenerateCoversAugmentLayout() bool
}

// EndpointProvider is an optional ComponentHandler interface: it declares the component's
// in-cluster data-plane endpoints (selector + ports) that launcher knows deterministically
// (e.g. an operator-managed database's instance pods). A downstream platform consumer calls
// Transformer.ComponentEndpoints to learn these — to build its dependency graph and the
// target-side allows it feeds back via TransformContext.IngressPeers — without hardcoding the
// operator selector. It is not read by synthesis (synthesis emits from IngressPeers).
type EndpointProvider interface {
	Endpoints(component *Component) ([]netpol.Endpoint, error)
}
