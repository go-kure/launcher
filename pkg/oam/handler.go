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
// Application and Bundle. Apply mutates the application it is given and may
// append sub-applications to the bundle; it must not replace, remove or rename
// a component's application there, which the transform refuses: the
// NetworkPolicy synthesis finds a component's application by its name and then
// its pointer, so such a one would silently get no policy.
type TraitHandler interface {
	CanHandle(traitType string) bool
	Apply(trait *Trait, app *stack.Application, bundle *stack.Bundle) error
}

// SubApplicationDecorator is an optional interface for TraitHandlers whose Apply
// decorates an application's objects (prune-protection, force-replace). When
// DecoratesSubApplications returns true, the engine also applies the trait to
// every sub-application the component's other traits appended to the bundle
// (a pvc trait's claim, an ingress trait's Ingress), whichever order the traits
// were authored in. That pass runs last in the build, after every trait of every
// component and the NetworkPolicy synthesis, and calls Apply with each
// sub-application in turn; Apply must not add, remove, replace, rename or
// reorder the bundle's applications there (the transform fails). Synthesized
// NetworkPolicies belong to no component's traits and are not decorated.
type SubApplicationDecorator interface {
	DecoratesSubApplications() bool
}

// CapabilityAware is an optional interface for TraitHandlers that require a
// matching ClusterProfile capability to produce correct output. If
// CapabilityRequired returns true and no capability resolves for the trait,
// the runtime returns ErrMissingCapability.
type CapabilityAware interface {
	CapabilityRequired() bool
}

// ComponentCapabilityDefaults is an optional interface for ComponentHandlers whose
// properties take defaults from a ClusterProfile capability. CapabilityDefaults names
// the capability key and the property keys it supplies. Before ToApplicationConfig,
// the engine copies each listed key the component does not author from that
// binding's rendering, so an authored value wins ("" included; an explicit null is
// absent). It reads only the listed keys, never the rest of the rendering, and
// records the key as consumed when the profile binds it. A component a lowering rule
// synthesized is skipped: its properties are the rule's own output, as a sealed
// trait's are. Each copied value is validated against the component's own
// PropertySchema, which must declare every listed key. A handler that declares no
// schema relies on EvaluateProfile, which validates the binding only through the
// trait handler or trait lowering rule of the key's type. Its fill is refused unless
// that handler or rule validates the rendering: it implements
// ValidateAndApplyDefaults, or the type is not built in and has a
// CapabilityDefinition, whose schema EvaluateProfile applies (go-kure/launcher#772).
type ComponentCapabilityDefaults interface {
	CapabilityDefaults() (key string, properties []string)
}

// ComponentCapabilityFiller is an optional interface for ComponentHandlers whose
// capability defaults land below the top level of their properties, where
// ComponentCapabilityDefaults cannot reach: the statefulset kind's
// volumeClaimTemplates entries take the `pvc` capability's storageClassName
// (go-kure/launcher#761). FillCapabilityDefaults runs right after
// ComponentCapabilityDefaults, on unsynthesized components only, and returns the
// properties ToApplicationConfig receives. It must not mutate props: it returns
// props itself when it fills nothing, and copies what it changes. It reads a
// binding only through lctx.Capability, which records the key as consumed; lctx
// carries nothing else. An error fails the component.
type ComponentCapabilityFiller interface {
	FillCapabilityDefaults(props map[string]any, lctx LoweringContext) (map[string]any, error)
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
// RequiredCapabilityKeys here is declarative, for a consumer to introspect). Two
// fields are read: Version, which loweringRuleIdentity (lowering.go) uses to compose the
// "@<version>" suffix on Origin.Rule for a lowering rule that also implements
// ContractDescriber; and Deprecated with DeprecationMessage, which the transform turns
// into one warning per authored component or trait of that type, through the
// Transformer's warning handler (never an error, never a change to the output).
// Nothing else on this struct is read or enforced by the engine.
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
// same source key (URL for HelmRepository, URL+version for OCIRepository).
// The component deployed first (earliest tier, then dependency order, then
// document order) emits the source; every other one references it.
//
// No builtin config implements it since go-kure/launcher#784: the oci rule
// shares its source the way the helm rule does, as a generated component
// (NameAllocator.NameOrAdopt). The engine still honours an implementer;
// go-kure/launcher#783 removes the interface.
type SourceDeduplicatable interface {
	GetSourceKey() string
	GetSourceRefName() string
	SuppressSourceGeneration(refName string)
}

// ComponentNamed is an optional interface for trait/component sub-app
// ApplicationConfig types that expose the OAM component they were emitted for.
// Consumers use it to attribute each emitted resource to its owning component
// (e.g. a provenance label) without re-deriving the component from sub-app names.
// ComponentName returns the raw component name, which may exceed the 63-character
// label-value limit; a consumer writing it into a label or selector passes it
// through ComponentLabelValue.
type ComponentNamed interface {
	ComponentName() string
}

// ServiceAccountNamer is an optional interface for component ApplicationConfig
// types. name is the ServiceAccount the component's pods run as: the authored
// `serviceAccountName`, or "" when none was authored — no pod kind generates a
// per-component account (go-kure/launcher#702), so such pods run as the
// namespace's `default` account. A role rule that generates one hands its pod
// member that account's name, so the member answers it. runsPods reports
// whether the config runs pods at all; a wrapper that forwards the interface (a
// trait decorator, a sibling group) reports false when nothing it wraps does. Traits that bind RBAC or
// other identity to the workload (the rbac trait's RoleBinding subject) read
// this instead of assuming the component name, so an authored
// serviceAccountName is honoured end to end and a pod without one is refused.
type ServiceAccountNamer interface {
	ServiceAccountName() (name string, runsPods bool)
}

// LayoutAugmentationCoverage is an optional interface for ApplicationConfig
// types that also implement kure's layout.LayoutAugmenter. It answers, for a
// consumer that never constructs or walks a layout.ManifestLayout (e.g.
// pkg/cmd/kurel's flat-YAML build path): would skipping AugmentLayout lose
// anything? true only when Generate's own output is already a complete
// superset of what AugmentLayout adds — e.g. a helmtemplate component, whose
// AugmentLayout only repartitions Generate's flat union into hook-ordered
// child layouts and adds nothing new. false, or this interface being absent
// altogether, is the fail-closed default: it means AugmentLayout adds
// something Generate's own output does not already contain — e.g. an object
// that Generate's output references but never emits itself. A
// LayoutAugmenter implementation that does not also implement this interface
// is always treated as false: an augmenter a consumer doesn't know the
// coverage of must never be assumed safe to skip.
type LayoutAugmentationCoverage interface {
	GenerateCoversAugmentLayout() bool
}

// EndpointProvider is an optional ComponentHandler or ComponentLoweringRule interface: it
// declares the component's in-cluster data-plane endpoints (selector + ports) that launcher
// knows deterministically (e.g. an operator-managed database's instance pods). A downstream platform consumer calls
// Transformer.ComponentEndpoints to learn these — to build its dependency graph and the
// target-side allows it feeds back via TransformContext.IngressPeers — without hardcoding the
// operator selector. It is not read by synthesis (synthesis emits from IngressPeers).
type EndpointProvider interface {
	Endpoints(component *Component) ([]netpol.Endpoint, error)
}
