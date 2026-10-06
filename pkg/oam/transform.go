package oam

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// ValidateAndApplyDefaults is implemented by built-in TraitHandlers that accept
// rendering keys from ClusterProfile. Called at ClusterProfile evaluation time,
// before any Application is processed. See design-capability-schema.md §2.2.
type ValidateAndApplyDefaults interface {
	ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error)
}

// TransformContext carries per-transform state passed to the pipeline.
type TransformContext struct {
	ClusterID     string
	TenantID      string
	Environment   string
	AppVersion    string
	TeamID        string
	Namespace     string // overrides OAM metadata.namespace when set; a DNS-1123 label
	FluxNamespace string // Flux control-plane namespace; "" means use component namespace; a DNS-1123 label
	Policy        Policy
	Capabilities  map[string]CapabilityBinding
	// EgressPeers carries downstream-supplied, graph-derived egress destinations keyed
	// by OAM component name. Non-authorable: never sourced from OAM YAML or
	// capability rendering, and never merged into trait properties. nil on the
	// kurel path, where egress synthesis is a no-op.
	EgressPeers map[string][]netpol.EgressPeer
	// ComponentLabelKey overrides the pod-label key that synthesized NetworkPolicies
	// target (the component's own pods: ingress recipients / egress sources). Empty
	// => the domain-derived key ComponentLabelKeyForDomain(Domain). Takes precedence over
	// Domain. Validated as a Kubernetes qualified label key. Non-authorable platform input,
	// like EgressPeers. It is also the key of the label the transform puts on every
	// object a component owns and on its pod templates, valued ComponentLabelValue(component)
	// as the selector is, so the selector matches the component's pods with no caller
	// labelling anything (go-kure/launcher#788).
	//
	// The label is authoritative (go-kure/launcher#790): the key must be one nothing
	// else writes. On the objects launcher generates, a value already under the key
	// is the component's own, or generation fails, wherever the object holds labels
	// that reach pods: its own, a pod template's, and the metadata an operator puts
	// on its pods (a CloudNativePG Cluster's spec.inheritedMetadata, a Pooler's
	// template, spec.podMetadata of the Prometheus operator's kinds, the
	// moverPodLabels of a VolSync mover, the pod template of a cert-manager
	// issuer's HTTP01 solvers, a Gateway's spec.infrastructure). A workload
	// whose selector requires another value for the key is refused too. Where the
	// key is absent launcher writes it, on the object and its pod template. One
	// value besides the component's passes, under the key "app" only: the one the
	// kinds write for an entry a lowering rule emitted under a name of its own,
	// whose pods then carry that entry's value and are not selected by the
	// component's policies. Under that key the transform refuses a document in
	// which such an entry's value is another component's. Each of these refusals,
	// and the one of a kind component's `labels` property that holds another
	// value under the key, is a *ComponentLabelError and answers to
	// ErrComponentLabelValue.
	//
	// A workload whose own selector rules the
	// label out (DoesNotExist on the key, NotIn with the component's value) keeps its pod
	// template as written: its pods carry no component label, and a synthesized policy
	// does not select them. A HelmRelease's post-renderer overwrites instead: with a
	// ComponentLabelKey that a Flux-installed chart's own selectors use ("app",
	// "app.kubernetes.io/name"), the post-renderer replaces the value the chart set under
	// it. Where a chart's selector does not accept the component's value, that parts the
	// selector from the chart's pods and the cluster refuses the workload. Use a key no
	// chart sets, such as the default.
	ComponentLabelKey string
	// Domain is the label/annotation domain for derived platform keys (<domain>/tier,
	// <domain>/component). Empty => DefaultDomain ("gokure.dev"). Non-authorable platform
	// input; a downstream platform embedding launcher sets its own domain. Validated as a
	// DNS-1123 subdomain — an invalid value fails the transform.
	Domain string
	// IngressPeers carries platform-supplied, graph-derived target-side allows keyed by OAM
	// component name (the endpoint owner). Each IngressPeer names an endpoint (selector +
	// ports) and the sources allowed to reach it. Non-authorable, like EgressPeers. nil on the
	// kurel path, where endpoint-ingress synthesis is a no-op.
	IngressPeers map[string][]netpol.IngressPeer
	// Naming is the consumer's say in the names launcher generates
	// (go-kure/launcher#787). It is asked once for each name of a role in
	// NameRoles that the author did not set: return a name and true to use it in
	// place of NameRequest.Default, or false to keep the default. A returned name
	// must be a DNS-1123 subdomain (a DNS-1035 label for the pooler role); it is
	// used as returned or refused, never shortened. Two names that end up naming one object, or one bundle, fail the
	// transform with both named. nil asks nothing: every name is the author's or
	// the default. Non-authorable platform input.
	//
	// It must be a pure function of its request: the same answer for the same
	// NameRequest, whenever and however often it is asked. One name is asked for
	// more than once: in the transform, and again by ComponentEndpointsNamed,
	// which builds a selector from the answer.
	Naming func(NameRequest) (string, bool)
	// ReservedMetadataKeys names the label and annotation keys the consumer keeps
	// to itself (go-kure/launcher#790). An entry is a key ("example.com/owner",
	// "owner"), or a key prefix followed by "/" ("example.com/"), which reserves
	// every key under that prefix. An entry that is neither fails the transform.
	// Non-authorable platform input.
	//
	// A reserved key is refused on every object an application generates, whatever
	// put it there: a component's or a trait's property, an object written out in
	// full (passthrough, manifests), a chart launcher renders. The refusal comes
	// from generation (Application.Generate, GenerateApplications), with
	// ErrReservedMetadataKey, since a rendered chart's objects exist only then. It
	// is read on an object's own labels and annotations, on the pod template's of
	// a workload, of a PodTemplate and of a CloudNativePG Pooler, on
	// spec.inheritedMetadata of a CloudNativePG Cluster, on spec.podMetadata
	// of a Prometheus, a PrometheusAgent, an Alertmanager and a ThanosRuler, on
	// the moverPodLabels of a VolSync mover, on the pod template of a
	// cert-manager issuer's HTTP01 solvers, and on a Gateway's
	// spec.infrastructure.
	//
	// The keys launcher writes itself are not refused: the `app` label, the
	// component label (ComponentLabelKey), and an annotation the platform sets on
	// an Ingress (the `ingress` trait's platform-reserved platformAnnotations,
	// which the `expose` trait fills), with the value it set. What is
	// added after generation is not read: the labels and annotations of a bundle,
	// what a config the consumer wraps around an application's after the transform
	// adds, and what a delivery workflow or the cluster adds. Neither is what a Flux
	// object hands on to the objects it applies, nor what a chart that Flux
	// installs renders in the cluster.
	ReservedMetadataKeys []string
	// names resolves and claims every name of this transform. Internal only: nil
	// on a caller-constructed ctx; TransformWithPolicy sets it. A pointer, so
	// every by-value ctx copy shares the one claim space.
	names *nameResolver
	// nameClaims is the transform's one name allocator: the lowering fixpoint
	// allocates from it, and names then claims into it, so a name a lowering rule
	// resolved and one resolved after lowering are held against each other.
	// Internal only: nil on a caller-constructed ctx; TransformWithPolicy sets it.
	nameClaims *NameAllocator
	// consumedCapabilities accumulates keys traits actually resolved against
	// Capabilities (go-kure/launcher#290) — populated by resolveCapability's call sites,
	// and by LoweringContext.Capability for a key a lowering rule reads
	// (go-kure/launcher#686) — read back into PolicyResult.ConsumedCapabilities at the
	// end of TransformWithPolicy.
	// Internal only: nil on a caller-constructed ctx; TransformWithPolicy inits it.
	// Reference type, so every by-value ctx copy through the pipeline shares one
	// map — same sharing pattern Capabilities/EgressPeers already rely on.
	consumedCapabilities map[string]struct{}
	// subAppDecorations accumulates, per component, each decorating trait
	// (SubApplicationDecorator) and the trait sub-applications it covers, recorded by
	// applyEntryTraits and applied by decorateSubApplications as the last step of
	// TransformWithPolicy. Internal only, initialized like consumedCapabilities; a
	// pointer so every by-value ctx copy appends to one slice.
	subAppDecorations *[]subAppDecoration
	// traitSubApps accumulates each application a trait ran on (a sibling
	// group's member, not the group) and the sub-applications that trait
	// appended, recorded by applyEntryTraits and read
	// by postProcessFluxNamespace. Internal only, shared like subAppDecorations.
	traitSubApps *[]traitSubApps
}

// subAppDecoration is one decorating trait of one component and the
// sub-applications that component's traits appended to bundle. entries are the
// entries bundle was built from, whose sibling group members the decoration must
// not rename.
type subAppDecoration struct {
	component string
	trait     Trait
	handler   TraitHandler
	bundle    *stack.Bundle
	entries   []componentEntry
	subApps   []*stack.Application
}

// fluxNamespaceSettable is implemented by ApplicationConfig types that emit
// Flux CRDs (HelmRelease, Kustomization, and the HelmRepository, OCIRepository,
// GitRepository and Bucket sources) and support per-request
// namespace re-stamping. Decorators that wrap such configs must also implement
// this interface and forward the call.
type fluxNamespaceSettable interface {
	SetFluxNamespace(string)
}

// deliveryPolicyTypes and deliveryTraitTypes are the policy and trait types that
// say how an application is delivered rather than what it consists of: health
// checks and reconciliation settings of the delivering object, and patches and
// post-build substitution it applies. Launcher has no handler for them and sets
// none of those fields on a bundle (go-kure/launcher#781); a consumer that
// delivers through Flux registers its own. They are named here only so the "no
// handler" error can say that.
var (
	deliveryPolicyTypes = map[string]bool{"health-checks": true, "reconciliation": true}
	deliveryTraitTypes  = map[string]bool{"fluxcd-patches": true, "fluxcd-postbuild": true}
)

// noHandlerMessage is the "no handler" error for a policy or trait type, with a
// hint when the type is one of delivery. where names the element: the policy, or
// the component the trait is on, and the lowering rule that emitted it, if one
// did (emittedBy).
func noHandlerMessage(position, typ, where string, delivery map[string]bool) string {
	msg := fmt.Sprintf("no handler for %s type %q (%s)", position, typ, where)
	if delivery[typ] {
		msg += ": it configures delivery, which launcher leaves to the consumer that delivers the application; a consumer that delivers through Flux registers its own handler"
	}
	return msg
}

// Transformer is the core OAM runtime. Handlers are registered at startup;
// Transform/TransformWithPolicy (added in go-kure/launcher#53) execute the pipeline.
// Internal storage uses maps keyed by typeName for O(1) dispatch.
//
// Handlers registered via RegisterTrait are treated as custom for
// CapabilityDefinition purposes. Use RegisterBuiltinTrait for launcher's own
// built-in handlers; built-in types are never checked against CapabilityDefinition files.
type Transformer struct {
	componentHandlers  map[string]ComponentHandler
	traitHandlers      map[string]TraitHandler
	policyHandlers     map[string]PolicyHandler
	builtinTraitTypes  map[string]bool
	capabilityDefs     map[string]*CapabilityDefinition
	strictCapabilities bool
	warnHandler        func(string)

	// Lowering rule registries (lowering.go). Kept separate from the dispatchable
	// handler maps above: a lowerable type must never also be dispatchable (see
	// RegisterComponentLowering et al.), so the two must not collide silently by
	// sharing one map.
	docLoweringRules       map[string]DocumentLoweringRule
	componentLoweringRules map[string]ComponentLoweringRule
	traitLoweringRules     map[string]TraitLoweringRule
	policyLoweringRules    map[string]PolicyLoweringRule
	// rawDocLoweringRules is the raw-document entry point's own registry
	// (lowering_raw.go). Deliberately a second map rather than an entry in
	// docLoweringRules: the two rule flavours have different LowerDocument
	// signatures, are reachable from different entry points, and a kind may be
	// claimed by at most one of them. Keyed on the (apiVersion, kind) pair a rule
	// claims — see RawDocumentAPIVersioner (lowering.go) — because a raw-entered
	// document may be authored under a consumer's own API group, which the
	// in-transform path (single-group by construction) never sees.
	rawDocLoweringRules map[rawDocRuleKey]RawDocumentLoweringRule
}

// NewTransformer creates a Transformer pre-loaded with component and trait handlers.
// Inputs are maps keyed by typeName — the same key used by Register* and find* methods.
// Each entry is routed through RegisterComponent/RegisterTrait so the startup assertion
// (design-capability-schema.md §2.5) applies to pre-loaded handlers too.
// Panics if any trait handler implements CapabilityAware but not ValidateAndApplyDefaults.
// Nil maps are treated as empty.
// Handlers registered through this constructor are treated as custom for
// CapabilityDefinition purposes; use RegisterBuiltinTrait for launcher built-ins.
func NewTransformer(componentHandlers map[string]ComponentHandler, traitHandlers map[string]TraitHandler) *Transformer {
	t := &Transformer{
		componentHandlers:      make(map[string]ComponentHandler),
		traitHandlers:          make(map[string]TraitHandler),
		policyHandlers:         make(map[string]PolicyHandler),
		builtinTraitTypes:      make(map[string]bool),
		docLoweringRules:       make(map[string]DocumentLoweringRule),
		componentLoweringRules: make(map[string]ComponentLoweringRule),
		traitLoweringRules:     make(map[string]TraitLoweringRule),
		policyLoweringRules:    make(map[string]PolicyLoweringRule),
		rawDocLoweringRules:    make(map[rawDocRuleKey]RawDocumentLoweringRule),
	}
	for typeName, h := range componentHandlers {
		t.RegisterComponent(typeName, h)
	}
	for typeName, h := range traitHandlers {
		t.RegisterTrait(typeName, h)
	}
	return t
}

// RegisterComponent registers a component handler under the given type name.
// Panics if typeName is already registered or if h.CanHandle(typeName) returns false.
func (t *Transformer) RegisterComponent(typeName string, h ComponentHandler) {
	if _, exists := t.componentHandlers[typeName]; exists {
		panic("oam: component handler already registered for type " + typeName)
	}
	if _, exists := t.componentLoweringRules[typeName]; exists {
		panic("oam: type " + typeName + " is already registered via RegisterComponentLowering; a lowerable type must not also be a dispatchable component handler")
	}
	if !h.CanHandle(typeName) {
		panic("oam: component handler does not claim type " + typeName)
	}
	t.componentHandlers[typeName] = h
}

// RegisterTrait registers a trait handler under the given type name.
// Panics if typeName is already registered, if h.CanHandle(typeName) returns false,
// or if h implements CapabilityAware but not ValidateAndApplyDefaults.
// See design-capability-schema.md §2.5.
func (t *Transformer) RegisterTrait(typeName string, h TraitHandler) {
	if _, exists := t.traitHandlers[typeName]; exists {
		panic("oam: trait handler already registered for type " + typeName)
	}
	if _, exists := t.traitLoweringRules[typeName]; exists {
		panic("oam: type " + typeName + " is already registered via RegisterTraitLowering; a lowerable type must not also be a dispatchable trait handler")
	}
	if !h.CanHandle(typeName) {
		panic("oam: trait handler does not claim type " + typeName)
	}
	if _, ok := h.(CapabilityAware); ok {
		if _, ok := h.(ValidateAndApplyDefaults); !ok {
			panic("oam: trait handler for type " + typeName + " implements CapabilityAware but not ValidateAndApplyDefaults")
		}
	}
	t.traitHandlers[typeName] = h
}

// HandlerSchemaSet is the set of property schemas declared by registered handlers,
// keyed by handler type name. Component, trait and policy schemas are kept
// separate so types that share a name across registries do not collide, and so
// consumers (the downstream runtime's validator) know which registry a schema
// came from.
type HandlerSchemaSet struct {
	Components map[string]map[string]PropertySchema
	Traits     map[string]map[string]PropertySchema
	Policies   map[string]map[string]PropertySchema
}

// HandlerSchemas returns the property schemas of every registered component,
// trait and policy handler, and every component, trait and policy lowering rule,
// that implements PropertySchemaProvider. Handlers and rules that do not
// implement it are omitted. The maps are always non-nil.
func (t *Transformer) HandlerSchemas() HandlerSchemaSet {
	set := HandlerSchemaSet{
		Components: make(map[string]map[string]PropertySchema),
		Traits:     make(map[string]map[string]PropertySchema),
		Policies:   make(map[string]map[string]PropertySchema),
	}
	// A kind component's schema is published with `objectName`, `labels` and
	// `annotations`, which the engine reads off it (ComponentObjectProvider).
	for name, h := range t.componentHandlers {
		if p, ok := h.(PropertySchemaProvider); ok {
			set.Components[name] = withObjectProperties(h, p.PropertySchema())
		}
	}
	for name, h := range t.traitHandlers {
		if p, ok := h.(PropertySchemaProvider); ok {
			set.Traits[name] = p.PropertySchema()
		}
	}
	// A trait type reachable only through a TraitLoweringRule (e.g. "expose", which
	// RegisterTraitLowering claims instead of RegisterBuiltinTrait) must still publish
	// its schema here — HandlerSchemas is the one place a caller discovers property
	// schemas, regardless of which position registry actually claims the type.
	for name, r := range t.traitLoweringRules {
		if p, ok := r.(PropertySchemaProvider); ok {
			set.Traits[name] = p.PropertySchema()
		}
	}
	// Mirror the trait-lowering-rule loop above for component-lowering rules: a
	// component type reachable only through a ComponentLoweringRule must still
	// publish its schema here, for the identical reason — HandlerSchemas is the one
	// place a caller discovers property schemas, regardless of which position
	// registry actually claims the type. Omitting this left a downstream validator
	// unable to check a higher-level component's user-facing properties before its
	// lowering rule runs.
	for name, r := range t.componentLoweringRules {
		if p, ok := r.(PropertySchemaProvider); ok {
			set.Components[name] = p.PropertySchema()
		}
	}
	// Policies publish from both of their registries for the same reason:
	// ValidateAuthoredProperties checks an authored policy against a handler's
	// schema or, failing that, a policy lowering rule's, so a caller that
	// validates or documents policies must be able to discover either.
	for name, h := range t.policyHandlers {
		if p, ok := h.(PropertySchemaProvider); ok {
			set.Policies[name] = p.PropertySchema()
		}
	}
	for name, r := range t.policyLoweringRules {
		if p, ok := r.(PropertySchemaProvider); ok {
			set.Policies[name] = p.PropertySchema()
		}
	}
	return set
}

// HandlerContractSet is the set of ContractMetadata declared by registered
// component/trait/policy handlers and component/trait/policy lowering rules, keyed
// by type name. Mirrors HandlerSchemaSet's Components/Traits/Policies split for the
// identical reason: two positions that share a type name must not collide, and a
// consumer needs to know which registry a contract came from.
type HandlerContractSet struct {
	Components map[string]ContractMetadata
	Traits     map[string]ContractMetadata
	Policies   map[string]ContractMetadata
}

// HandlerContracts returns the ContractMetadata of every registered component,
// trait and policy handler, and every component/trait/policy lowering rule, that
// implements ContractDescriber. Entries that do not implement it are omitted. The maps are
// always non-nil. Covers the six registries HandlerSchemas covers, for the same
// reason: a type reachable only through a lowering rule (e.g. "expose", claimed via
// RegisterTraitLowering rather than RegisterBuiltinTrait) must still publish its
// contract metadata here — otherwise a caller discovering contracts would see a gap
// for exactly the types HandlerSchemas already had to stop omitting (see that
// method's comments above).
func (t *Transformer) HandlerContracts() HandlerContractSet {
	set := HandlerContractSet{
		Components: make(map[string]ContractMetadata),
		Traits:     make(map[string]ContractMetadata),
		Policies:   make(map[string]ContractMetadata),
	}
	for name, h := range t.policyHandlers {
		if p, ok := h.(ContractDescriber); ok {
			set.Policies[name] = p.ContractMetadata()
		}
	}
	for name, r := range t.policyLoweringRules {
		if p, ok := r.(ContractDescriber); ok {
			set.Policies[name] = p.ContractMetadata()
		}
	}
	for name, h := range t.componentHandlers {
		if p, ok := h.(ContractDescriber); ok {
			set.Components[name] = p.ContractMetadata()
		}
	}
	for name, h := range t.traitHandlers {
		if p, ok := h.(ContractDescriber); ok {
			set.Traits[name] = p.ContractMetadata()
		}
	}
	for name, r := range t.traitLoweringRules {
		if p, ok := r.(ContractDescriber); ok {
			set.Traits[name] = p.ContractMetadata()
		}
	}
	for name, r := range t.componentLoweringRules {
		if p, ok := r.(ContractDescriber); ok {
			set.Components[name] = p.ContractMetadata()
		}
	}
	return set
}

// RegisterPolicy registers a policy handler under the given type name.
// Panics if typeName is already registered or if h.CanHandle(typeName) returns false.
func (t *Transformer) RegisterPolicy(typeName string, h PolicyHandler) {
	if _, exists := t.policyHandlers[typeName]; exists {
		panic("oam: policy handler already registered for type " + typeName)
	}
	if _, exists := t.policyLoweringRules[typeName]; exists {
		panic("oam: type " + typeName + " is already registered via RegisterPolicyLowering; a lowerable type must not also be a dispatchable policy handler")
	}
	if !h.CanHandle(typeName) {
		panic("oam: policy handler does not claim type " + typeName)
	}
	t.policyHandlers[typeName] = h
}

// RegisterBuiltinTrait is like RegisterTrait but marks the type as built-in.
// Built-in types are never checked against CapabilityDefinition files.
func (t *Transformer) RegisterBuiltinTrait(typeName string, h TraitHandler) {
	t.RegisterTrait(typeName, h)
	t.builtinTraitTypes[typeName] = true
}

// SetCapabilityDefs replaces the set of loaded CapabilityDefinition schemas.
// Typically populated from LoadCapabilityDefinitions before calling EvaluateProfile.
func (t *Transformer) SetCapabilityDefs(defs map[string]*CapabilityDefinition) {
	t.capabilityDefs = defs
}

// SetStrictCapabilities controls whether a missing CapabilityDefinition for a custom
// trait is a hard error (true) or a warning (false, default).
func (t *Transformer) SetStrictCapabilities(strict bool) {
	t.strictCapabilities = strict
}

// SetWarningHandler sets the callback invoked when a non-fatal warning is emitted: a
// capability warning, or an authored component or trait whose type is deprecated
// (ContractMetadata.Deprecated). If nil, warnings are silently dropped.
func (t *Transformer) SetWarningHandler(h func(string)) {
	t.warnHandler = h
}

// EvaluateProfile validates and applies defaults to all capability renderings in
// the ClusterProfile. For each capability key whose trait type matches a registered
// handler that implements ValidateAndApplyDefaults, the rendering map is passed
// through that handler's VAD method. Returns a new ClusterProfile with updated
// renderings, or a TransformError wrapping the first validation failure.
//
// Capability keys follow the "<type>" or "<type>.<scope>" convention. EvaluateProfile
// must be called before Transform so that malformed profile renderings are caught at
// load time rather than silently propagated into the pipeline.
func (t *Transformer) EvaluateProfile(profile *ClusterProfile) (*ClusterProfile, error) {
	if profile == nil {
		return nil, nil
	}
	if len(profile.Spec.Capabilities) == 0 {
		return profile, nil
	}
	evaluated := make(map[string]CapabilityBinding, len(profile.Spec.Capabilities))
	for key, binding := range profile.Spec.Capabilities {
		typeName, _, _ := strings.Cut(key, ".")
		// capabilityValidated mirrors which of the branches below validate the
		// rendering; a change here is a change there.
		handler, ok := t.traitHandlers[typeName]
		if !ok {
			// The type may be a trait-position lowering rule instead of a dispatchable
			// handler (e.g. "expose") — a rule never reaches applyTraits, so this is
			// the only place its ValidateAndApplyDefaults ever runs. A lowering-rule
			// type CAN still have a loaded CapabilityDefinition, and must get the
			// identical schema-defaults step the handler branch below applies before
			// its own VAD runs — otherwise a TraitLoweringRule's VAD sees undefaulted,
			// unchecked-against-declared-schema values purely because its type lowers
			// instead of dispatching. But a BUILT-IN lowering rule (registered via
			// RegisterBuiltinTraitLowering, e.g. "expose") is exempt from
			// CapabilityDefinition application exactly like a built-in TraitHandler is
			// (t.builtinTraitTypes gates both registries identically) — otherwise a
			// caller loading a definition that happens to share a built-in lowering
			// rule's type name would have it silently applied to that rule's rendering.
			if rule, ok := t.traitLoweringRules[typeName]; ok {
				currentRendering := binding.Rendering
				if !t.builtinTraitTypes[typeName] {
					if def, hasDef := t.capabilityDefs[typeName]; hasDef {
						withDefaults, err := applyDefinitionSchema(currentRendering, def)
						if err != nil {
							return nil, &TransformError{
								Message: fmt.Sprintf("capability %q definition schema", key),
								Cause:   err,
							}
						}
						currentRendering = withDefaults
					}
				}
				vad, ok := rule.(ValidateAndApplyDefaults)
				if !ok {
					evaluated[key] = CapabilityBinding{Rendering: currentRendering}
					continue
				}
				validated, err := vad.ValidateAndApplyDefaults(currentRendering)
				if err != nil {
					return nil, &TransformError{Message: fmt.Sprintf("capability %q", key), Cause: err}
				}
				evaluated[key] = CapabilityBinding{Rendering: validated}
				continue
			}
			evaluated[key] = binding
			continue
		}

		currentRendering := binding.Rendering

		// For custom (non-built-in) trait types, apply CapabilityDefinition schema
		// defaults before handler VAD so that VAD sees schema-defaulted values.
		if !t.builtinTraitTypes[typeName] {
			if def, hasDef := t.capabilityDefs[typeName]; hasDef {
				withDefaults, err := applyDefinitionSchema(currentRendering, def)
				if err != nil {
					return nil, &TransformError{
						Message: fmt.Sprintf("capability %q definition schema", key),
						Cause:   err,
					}
				}
				currentRendering = withDefaults
			}
		}

		vad, ok := handler.(ValidateAndApplyDefaults)
		if !ok {
			evaluated[key] = CapabilityBinding{Rendering: currentRendering}
			continue
		}
		validated, err := vad.ValidateAndApplyDefaults(currentRendering)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("capability %q", key), Cause: err}
		}
		evaluated[key] = CapabilityBinding{Rendering: validated}
	}
	result := *profile
	result.Spec = profile.Spec           // copy all fields (future-proof: new fields survive automatically)
	result.Spec.Capabilities = evaluated // overwrite only the evaluated capabilities
	return &result, nil
}

// ComponentEndpoints returns the endpoints declared for comp.Type by its
// ComponentLoweringRule, or by its handler when no rule claims the type, or (nil, nil)
// if comp is nil, neither is registered, or the one registered is not an
// EndpointProvider. It returns an error if a registered provider yields a malformed endpoint
// (fail-fast: a broken handler surfaces early, not as a silent connectivity outage). Consumed
// by a downstream platform to learn endpoint selectors when building its dependency graph.
//
// It consults no TransformContext.Naming hook: an endpoint whose selector carries a
// generated name (the pooler endpoint of a postgresql component) carries the author's
// name or the default. A consumer that sets Naming calls ComponentEndpointsNamed.
func (t *Transformer) ComponentEndpoints(comp *Component) ([]netpol.Endpoint, error) {
	return t.componentEndpoints(comp, "", nil)
}

// ComponentEndpointsNamed is ComponentEndpoints for a consumer that sets
// TransformContext.Naming: an endpoint whose selector carries a generated name carries
// the name the transform gives the object, the author's, else the one naming returns,
// else the default. naming is asked the NameRequest the transform asks for that name,
// and an answer that is no valid name for its role is refused here with the transform's
// message.
//
// An endpoint answered here, or by ComponentEndpoints, is not the build's verdict on
// the document. Both are given one component and hold to one rule: an endpoint entry
// answers only for a component its type's own parse accepts, and refuses another in the
// parse's words. Four things it does not see: neither runs the schema check
// (ValidateAuthoredProperties); neither sees what the transform refuses of the
// component after its type's own parse where the entry does not run that step (what
// webservice's lowering refuses, what a kind postgresql lowers into refuses of a
// member); neither is given the policy or what generation refuses after it; and
// neither sees what only the document shows: a name that is already another component
// of the document, a name another component generates in the same run, or two objects
// that collide. The package README states the rule and the four limits ("Name roles
// and the Naming hook").
//
// application is the name of the document comp is authored in, as the transform puts
// it in that request (NameRequest.Application): the document's metadata.name, or,
// where a DocumentLoweringRule renames the document, the name it has after lowering.
// A nil naming makes this ComponentEndpoints.
func (t *Transformer) ComponentEndpointsNamed(application string, comp *Component, naming func(NameRequest) (string, bool)) ([]netpol.Endpoint, error) {
	return t.componentEndpoints(comp, application, naming)
}

func (t *Transformer) componentEndpoints(comp *Component, application string, naming func(NameRequest) (string, bool)) ([]netpol.Endpoint, error) {
	if comp == nil {
		return nil, nil
	}
	// A type is a rule or a handler, never both (RegisterComponent,
	// RegisterComponentLowering), so this is never a choice.
	handler := t.findComponentHandler(comp.Type)
	var provider any = handler
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		provider = rule
	} else if declaresEndpoints(handler) {
		// As the transform does before ToApplicationConfig: a kind component's
		// endpoint that selects by its object's name selects by the name the object
		// gets (Component.ObjectName). Nothing is claimed. A handler that declares
		// no endpoints is not asked for a name: the answer is (nil, nil) whatever
		// its component holds.
		named, err := withObjectName(*comp, handler, "", "", &nameResolver{hook: naming, application: application})
		if err != nil {
			return nil, errors.Wrapf(err, "component %q", comp.Name)
		}
		// Its `labels` and `annotations` are read off it too, so the handler is
		// handed the properties it is handed in the transform. There is no
		// component label key here to hold the labels to.
		named, err = withObjectMetadata(named, handler, "")
		if err != nil {
			return nil, errors.Wrapf(err, "component %q", comp.Name)
		}
		comp = &named
	}
	var eps []netpol.Endpoint
	var err error
	if named, ok := provider.(NamedEndpointProvider); ok {
		// A fresh allocator per call: what a name is resolved to is asked here,
		// nothing is claimed.
		namer := NewNameAllocator()
		namer.hook = naming
		eps, err = named.EndpointsNamed(comp, LoweringContext{Component: comp, Namer: namer, application: application})
	} else if ep, ok := provider.(EndpointProvider); ok {
		eps, err = ep.Endpoints(comp)
	} else {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i, e := range eps {
		if err := validateEndpoint(e); err != nil {
			return nil, errors.Wrapf(err, "component %q endpoint[%d]", comp.Name, i)
		}
	}
	return eps, nil
}

// declaresEndpoints reports whether handler is one componentEndpoints reads
// endpoints from. A nil handler is not.
func declaresEndpoints(handler ComponentHandler) bool {
	switch handler.(type) {
	case NamedEndpointProvider, EndpointProvider:
		return true
	}
	return false
}

func (t *Transformer) findComponentHandler(componentType string) ComponentHandler {
	return t.componentHandlers[componentType]
}

func (t *Transformer) findTraitHandler(traitType string) TraitHandler {
	return t.traitHandlers[traitType]
}

func (t *Transformer) findPolicyHandler(policyType string) PolicyHandler {
	return t.policyHandlers[policyType]
}

// --- Pipeline entry points ---

// componentEntry holds a component with its corresponding stack application and tier.
type componentEntry struct {
	index     int
	component Component
	app       *stack.Application
	tier      Tier
	// members is set only on a collapsed sibling group (collapseSiblingGroups):
	// the group's own entries, in emission order. index and component are then
	// the first member's, and app is the group's one application.
	members []componentEntry
}

// Transform converts an OAM Application to a kure Cluster.
//
// It refuses two names it resolved under a name role (NameRoles) that name one
// object or one bundle (go-kure/launcher#787). It does not compare the other
// objects two applications of the document generate, two traits of one
// component included (go-kure/launcher#757): a caller runs GenerateApplications
// and CheckInDocumentCollisions on the result.
func (t *Transformer) Transform(app *Application, ctx TransformContext) (*stack.Cluster, error) {
	cluster, _, err := t.TransformWithPolicy(app, ctx)
	return cluster, err
}

// TransformWithPolicy converts an OAM Application to a kure Cluster and
// returns the accumulated PolicyResult. ctx.Policy is normalized to NoopPolicy
// if nil so that all pipeline stages always receive a non-nil Policy value.
func (t *Transformer) TransformWithPolicy(app *Application, ctx TransformContext) (*stack.Cluster, *PolicyResult, error) {
	// A lowering rule whose declared targets are not registered is refused here,
	// whether or not this document uses it (Seal).
	if err := t.Seal(); err != nil {
		return nil, nil, err
	}
	if ctx.Policy == nil {
		ctx.Policy = &NoopPolicy{}
	}
	ctx.consumedCapabilities = make(map[string]struct{})
	ctx.subAppDecorations = &[]subAppDecoration{}
	ctx.traitSubApps = &[]traitSubApps{}

	// Validate + normalize the platform domain (and the optional full-key override) once,
	// fail-fast before building anything. ComponentLabelKey takes precedence over Domain,
	// so it is validated here too — a behavioral tightening (previously any string was
	// accepted). ctx is a value copy, so normalizing ctx.Domain here is local to this call.
	ctx.Domain = domainOrDefault(ctx.Domain)
	if errs := validation.IsDNS1123Subdomain(ctx.Domain); len(errs) > 0 {
		return nil, nil, errors.Errorf("invalid TransformContext.Domain %q: %s", ctx.Domain, strings.Join(errs, "; "))
	}
	if ctx.ComponentLabelKey != "" {
		if errs := validation.IsQualifiedName(ctx.ComponentLabelKey); len(errs) > 0 {
			return nil, nil, errors.Errorf("invalid TransformContext.ComponentLabelKey %q: %s", ctx.ComponentLabelKey, strings.Join(errs, "; "))
		}
	}
	reservedKeys, err := parseReservedMetadataKeys(ctx.ReservedMetadataKeys)
	if err != nil {
		return nil, nil, err
	}
	// The namespace override (kurel build --namespace) and the Flux namespace are stamped
	// onto metadata.namespace as given, so both must be DNS-1123 labels. The messages name
	// the value as a CLI reader knows it, not the field.
	if err := checkNamespaceLabel("namespace", ctx.Namespace); err != nil {
		return nil, nil, err
	}
	if err := checkNamespaceLabel("flux namespace", ctx.FluxNamespace); err != nil {
		return nil, nil, err
	}
	// Every binding's rendering is checked once here, used or not: both merge sites
	// copy it keeping Go types and have no way to refuse a value they cannot copy.
	if err := checkCapabilityRenderings(ctx.Capabilities); err != nil {
		return nil, nil, err
	}

	// authoredTraitTypes is captured BEFORE t.lower() runs (F7): a Policy that
	// constrains trait types (e.g. "expose must not be used") must see what the
	// human wrote, not what a lowering rule renamed it to. ExposeRule lowers an
	// authored "expose" trait into a terminal "ingress"/"httproute" trait, so
	// collecting trait types AFTER lowering would silently evaluate the Policy
	// against the wrong input — a policy control deciding on synthesized detail
	// instead of the authored line it is meant to police.
	authoredTraitTypes := collectTraitTypes(app)

	// Deprecation is read from the authored document for the same reason: a
	// component a lowering rule synthesizes was not written by anyone, so it never
	// warns.
	t.warnDeprecatedTypes(app)

	// Run the lowering fixpoint (D1/D2, lowering.go) before anything else: a
	// registered TraitLoweringRule (e.g. "expose", pkg/oam/builtin/traits) must
	// settle into its terminal trait type before createApplications/applyTraits
	// ever dispatch on trait.Type. With no lowering rules registered, t.lower
	// returns []*Application{app} unchanged (the same pointer) — the bit-identity
	// guarantee documented on lower() — so this is a no-op for every caller that
	// registers none.
	//
	// TransformWithPolicy's single-cluster signature cannot express a document-
	// position rule's 1->N fan-out (only a DocumentLoweringRule can produce that,
	// and none is registered anywhere on this branch); the length check below
	// exists so a future misuse fails loudly here instead of silently dropping
	// documents.
	ctx.nameClaims = NewNameAllocator()
	ctx.nameClaims.hook = ctx.Naming
	docs, err := t.lower(app, ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(docs) != 1 {
		return nil, nil, errors.Errorf(
			"lowering expanded application %q into %d documents; TransformWithPolicy's single-cluster signature cannot express this fan-out",
			app.Metadata.Name, len(docs))
	}
	app = docs[0]
	// Created after lowering: a document rule may rename the document, and
	// NameRequest.Application is the name the defaults are built from. The only
	// names resolved before this point are the ones lowering rules resolved
	// (LoweringContext.ResolveName), each with the document's name as it stood.
	ctx.names = &nameResolver{hook: ctx.Naming, application: app.Metadata.Name, claims: ctx.nameClaims}

	namespace := ctx.Namespace
	if namespace == "" {
		namespace = app.Metadata.Namespace
	}
	if namespace == "" {
		namespace = defaultNamespace
	}
	// The names lowering rules resolved are claimed first, now that the namespace
	// their objects land in is known (the Flux namespace for a Flux-scoped one,
	// when there is one): a name resolved from here on that names one of those
	// objects is refused with both named.
	if err := ctx.nameClaims.claimLowered(namespace, ctx.FluxNamespace); err != nil {
		return nil, nil, &TransformError{Message: "generated name", Cause: err}
	}

	// Phase 1: create applications, apply Enforceable policy, read tier annotations.
	entries, err := t.createApplications(app, namespace, ctx)
	if err != nil {
		return nil, nil, err
	}
	// A same-name sibling group a lowering rule emitted becomes one entry here,
	// before anything keyed by component name runs (go-kure/launcher#280).
	entries, err = collapseSiblingGroups(entries, namespace)
	if err != nil {
		return nil, nil, err
	}

	// Phase 1.5: validate capability constraints declared by the Policy. Uses
	// authoredTraitTypes (captured before t.lower() ran), not a fresh
	// collectTraitTypes(app) call here — see the comment at that capture site.
	if err := enforceCapabilityConstraints(authoredTraitTypes, ctx.Policy); err != nil {
		return nil, nil, NewViolationError(app.Metadata.Name, err)
	}

	// Phase 2: apply OAM policies (placement overrides, dependency graph).
	policyResult, err := t.applyPolicies(app, entries)
	if err != nil {
		return nil, nil, err
	}

	// A placement replaces the tier a component's annotation gave it.
	for i, entry := range entries {
		if tier, placed := policyResult.TierOverrides[entry.component.Name]; placed {
			entries[i].tier = tier
		}
	}
	if err := checkSiblingTiers(entries, policyResult.TierOverrides); err != nil {
		return nil, nil, err
	}

	// Phase 3: order the components by what the document declares (placement,
	// dependency rules, a lowering rule's own order) and build the cluster.
	// Launcher orders nothing by itself (go-kure/launcher#783).
	order, err := orderComponents(entries, policyResult.Dependencies)
	if err != nil {
		return nil, nil, err
	}

	cluster, err := t.buildCluster(app, order, ctx)
	if err != nil {
		return nil, nil, err
	}
	// Traits have now wrapped what they wrap, so a sibling group's members are
	// checked as phase 4 will read them through the group's config.
	if err := checkSiblingGroups(entries); err != nil {
		return nil, nil, err
	}
	// A trait sets a delivery intent on the member it ran on; the group's one
	// application, which is what is delivered, takes it (go-kure/launcher#782).
	adoptMemberDeliveryIntents(entries)

	// Phase 4: post-build bundle decorations. None of them sets a Flux delivery
	// field of a bundle (health checks, interval, prune, wait, timeout, retry
	// interval, force, suspend, patches, post-build): those belong to the consumer
	// that delivers the application (go-kure/launcher#781).
	componentMap := make(map[string]componentEntry, len(entries))
	for _, e := range entries {
		componentMap[e.component.Name] = e
	}
	labelKey := ctx.componentLabelKey()
	if err := synthesizeNetworkPolicies(cluster, componentMap, labelKey); err != nil {
		return nil, nil, err
	}
	// Egress synthesis fails fast on a malformed non-authorable peer (ported but selector-less):
	// a producer bug should fail the build, not silently emit a namespace-wide egress allow.
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, ctx.EgressPeers, labelKey); err != nil {
		return nil, nil, err
	}
	synthesizeEndpointIngressNetworkPolicies(cluster, componentMap, ctx.IngressPeers)
	if err := ctx.names.resolveSynthesizedPolicyNames(cluster); err != nil {
		return nil, nil, err
	}
	postProcessFluxNamespace(cluster, *ctx.traitSubApps, ctx.FluxNamespace)
	// Last: a decorator hides the interfaces the steps above read on a trait
	// sub-application (the NetworkPolicy synthesis collectors among them).
	if err := decorateSubApplications(*ctx.subAppDecorations); err != nil {
		return nil, nil, err
	}
	// After every step that reads a config: from here on each application's
	// config is its ownership wrapper, which labels what the application
	// generates with its component (go-kure/launcher#788) and holds it to the
	// consumer's reserved metadata keys (go-kure/launcher#790).
	if err := markComponentOwnership(cluster, order, *ctx.traitSubApps, labelKey, reservedKeys); err != nil {
		return nil, nil, err
	}

	if len(ctx.consumedCapabilities) > 0 {
		keys := make([]string, 0, len(ctx.consumedCapabilities))
		for k := range ctx.consumedCapabilities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		policyResult.ConsumedCapabilities = keys
	}

	return cluster, policyResult, nil
}

// componentLabelKey returns the key of the component label: ComponentLabelKey,
// else the one derived from Domain, which the transform has normalized by the
// time it reads this.
func (ctx TransformContext) componentLabelKey() string {
	if ctx.ComponentLabelKey != "" {
		return ctx.ComponentLabelKey
	}
	return ComponentLabelKeyForDomain(ctx.Domain)
}

// createApplications converts OAM components to stack applications, applies
// Enforceable policy and then each component's post-policy steps, and classifies
// each component into a deployment tier.
func (t *Transformer) createApplications(app *Application, namespace string, ctx TransformContext) ([]componentEntry, error) {
	entries := make([]componentEntry, 0, len(app.Spec.Components))
	for i, component := range app.Spec.Components {
		handler := t.findComponentHandler(component.Type)
		if handler == nil {
			return nil, &TransformError{Message: fmt.Sprintf("no handler for component type %q (component %q%s)", component.Type, component.Name, emittedBy(component.origin))}
		}

		// D3: an authored value for a platform-reserved property is rejected before
		// the handler ever sees it, symmetric with the trait-position enforcement in
		// applyTraits/lowerDocumentBody. A component a lowering rule synthesized
		// (Component.synthesized) is exempt, as a synthesized trait is in applyTraits:
		// its properties are the rule's own output, which may carry a reserved value the
		// rule rendered from LoweringContext.Capability. A rule's output is marked
		// synthesized only when its input was already checked before that rule ran: a
		// component rule that declares a schema or receives a synthesized component,
		// or a trait rule that receives a synthesized trait or declares a schema and
		// receives an unsealed one. A component a document rule forwards
		// unchanged keeps its classification. Any other rule output, including every
		// component a document rule builds, is checked here like an authored component,
		// except a reserved value the rule recorded with Component.RenderReserved while
		// it still holds the recorded value.
		if p, ok := handler.(PropertySchemaProvider); ok && !component.synthesized {
			if err := enforcePlatformReserved(p.PropertySchema(), component.Properties, component.rendered, "properties"); err != nil {
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
		}

		// Before any of the handler's own code runs on the properties, a capability
		// filler included: the handler reads the object's name off the component and
		// never sees the property.
		named, err := withObjectName(component, handler, namespace, ctx.FluxNamespace, ctx.names)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
		}
		component = named
		// The same for the object's labels and annotations (go-kure/launcher#790).
		labelled, err := withObjectMetadata(component, handler, ctx.componentLabelKey())
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
		}
		component = labelled

		// After D3, which checks only what was authored: the capability defaults a
		// ComponentCapabilityDefaults handler names fill the keys left unauthored.
		if d, ok := handler.(ComponentCapabilityDefaults); ok && !component.synthesized {
			filled, err := t.applyComponentCapabilityDefaults(d, component.Properties, ctx)
			if err != nil {
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
			component.Properties = filled
		}
		if f, ok := handler.(ComponentCapabilityFiller); ok && !component.synthesized {
			filled, err := f.FillCapabilityDefaults(component.Properties, LoweringContext{capabilities: ctx.Capabilities, consumed: ctx.consumedCapabilities})
			if err != nil {
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
			component.Properties = filled
		}

		config, err := handler.ToApplicationConfig(&component, namespace)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
		}

		// Here, on the handler's own config: a trait decorator wraps it later.
		if named, ok := config.(ApplicationNameSetter); ok {
			named.SetApplicationName(app.Metadata.Name)
		}
		if prefixed, ok := config.(HookGroupNamePrefixSetter); ok {
			if err := ctx.names.resolveHookGroupNamePrefix(component.Name, prefixed); err != nil {
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
		}

		if enforceable, ok := config.(Enforceable); ok {
			if err := enforceable.ApplyPolicy(ctx.Policy); err != nil {
				return nil, NewViolationError(component.Name, err)
			}
		}

		// The steps a lowering rule attached (Component.AfterPolicy) run on the
		// config the policy has just decided, before any trait of the component.
		for _, step := range component.afterPolicy {
			if err := step(config); err != nil {
				// A step that holds the config to the policy refuses as ApplyPolicy
				// does: a refusal by the policy is a violation, any other error is not.
				var refusal *PolicyRefusal
				if errors.As(err, &refusal) {
					return nil, NewViolationError(component.Name, err)
				}
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
		}

		// A config that rendered its chart for the policy knows its hook-group
		// names now, and one the prefix resolved above makes too long is refused
		// here instead of when the layout is built (HookGroupNameChecker). The
		// policy is never nil here, so a helmtemplate config has rendered.
		if checked, ok := config.(HookGroupNameChecker); ok {
			if err := checked.CheckHookGroupNames(); err != nil {
				return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
			}
		}

		// ctx.Domain was validated + normalized at the top of TransformWithPolicy; the
		// per-component re-validation inside ClassifyComponentWithDomain is idempotent.
		tier, err := ClassifyComponentWithDomain(&component, ctx.Domain)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
		}

		stackApp := stack.NewApplication(component.Name, namespace, config)
		entries = append(entries, componentEntry{
			index:     i,
			component: component,
			app:       stackApp,
			tier:      tier,
		})
	}

	return entries, nil
}

// applyPolicies runs all registered policy handlers for the application's OAM policies.
func (t *Transformer) applyPolicies(app *Application, entries []componentEntry) (*PolicyResult, error) {
	result := NewPolicyResult()
	if len(app.Spec.Policies) == 0 {
		return result, nil
	}

	componentNames := make([]string, len(entries))
	for i, e := range entries {
		componentNames[i] = e.component.Name
	}

	for _, p := range app.Spec.Policies {
		handler := t.findPolicyHandler(p.Type)
		if handler == nil {
			where := fmt.Sprintf("policy %q%s", p.Name, emittedBy(p.origin))
			return nil, &TransformError{Message: noHandlerMessage("policy", p.Type, where, deliveryPolicyTypes)}
		}
		if err := handler.Apply(&p, componentNames, result); err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("policy %q", p.Name), Cause: err}
		}
	}

	return result, nil
}

// buildCluster builds the application's one bundle, named after the
// application.
//
// When nothing orders the components, the bundle is flat: it holds every
// component's application, in document order.
//
// Otherwise it holds the generated sources (orderComponents) as its own
// applications and one child bundle per group, each depending on the group
// before it. A bundle's own applications are applied before its children: that
// is what puts a generated source ahead of every consumer, and a consumer that
// delivers the bundle keeps to it. Nothing here sets a delivery field of a
// bundle: how it is delivered is the consumer's (go-kure/launcher#781).
func (t *Transformer) buildCluster(app *Application, order *componentOrder, ctx TransformContext) (*stack.Cluster, error) {
	bundleName, err := ctx.names.resolveBundleName(NameRoleBundle, app.Metadata.Name)
	if err != nil {
		return nil, err
	}
	if !order.ordered() {
		bundle, err := t.buildBundle(app, bundleName, order.sequence(), ctx)
		if err != nil {
			return nil, err
		}
		return stack.NewCluster(ctx.ClusterID, &stack.Node{Name: "", Bundle: bundle}), nil
	}

	root, err := t.buildBundle(app, bundleName, order.sources, ctx)
	if err != nil {
		return nil, err
	}
	for i, entries := range order.groups {
		groupName, err := ctx.names.resolveBundleName(NameRoleGroup, order.groupName(app.Metadata.Name, i))
		if err != nil {
			return nil, err
		}
		group, err := t.buildBundle(app, groupName, entries, ctx)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			group.DependsOn = append(group.DependsOn, root.Children[i-1])
		}
		root.Children = append(root.Children, group)
	}
	root.InitializeUmbrella()
	if err := root.Validate(); err != nil {
		return nil, &TransformError{Message: "failed to validate application bundle", Cause: err}
	}

	return stack.NewCluster(ctx.ClusterID, &stack.Node{Name: "", Bundle: root}), nil
}

// buildBundle creates the bundle name holding entries' applications and applies
// the entries' traits to it.
func (t *Transformer) buildBundle(app *Application, name string, entries []componentEntry, ctx TransformContext) (*stack.Bundle, error) {
	apps := make([]*stack.Application, 0, len(entries))
	for _, e := range entries {
		apps = append(apps, e.app)
	}
	bundle, err := stack.NewBundle(name, apps, nil)
	if err != nil {
		return nil, &TransformError{Message: fmt.Sprintf("failed to create bundle %q", name), Cause: err}
	}
	if err := t.applyTraits(app, entries, bundle, ctx); err != nil {
		return nil, err
	}
	return bundle, nil
}

// applyTraits applies all traits for the given component entries to the bundle.
// Capability rendering values are merged into trait properties before dispatch;
// OAM inline values take precedence. Policy enforcement is applied to configs
// added by each trait.
//
// The bundle's applications are then ordered component by component: each
// entry's application followed by the sub-applications its traits created, in
// creation order. Traits append to the bundle, which would otherwise put every
// sub-application after every component of the bundle. This applies only when
// the traits did nothing but append: the bundle must read exactly as it did
// before the traits, followed by the sub-applications in creation order. A
// trait handler that moved an application or removed a sub-application leaves
// an order of its own, which is kept as it left it. One that replaced, removed
// or renamed a component's application fails the transform
// (checkEntryApplications).
func (t *Transformer) applyTraits(app *Application, entries []componentEntry, bundle *stack.Bundle, ctx TransformContext) error {
	before := slices.Clone(bundle.Applications)
	var created []*stack.Application
	ordered := make([]*stack.Application, 0, len(bundle.Applications))
	for _, e := range entries {
		subApps, err := t.applyEntryTraits(app, e, entries, bundle, ctx)
		if err != nil {
			return err
		}
		ordered = append(ordered, e.app)
		ordered = append(ordered, subApps...)
		created = append(created, subApps...)
	}
	// The membership check also holds the bundle to the entries: an application
	// no entry accounts for keeps the bundle as it is.
	if slices.Equal(bundle.Applications, append(before, created...)) && sameApplications(ordered, bundle.Applications) {
		bundle.Applications = ordered
	}
	return nil
}

// addedApplications returns the applications in after that are not in before,
// by pointer, in after's order. A trait handler may move or remove applications
// as well as append, so what it added is not simply the tail past before's
// length: a removal shifts the tail left and hides what follows it.
func addedApplications(before, after []*stack.Application) []*stack.Application {
	had := make(map[*stack.Application]bool, len(before))
	for _, a := range before {
		had[a] = true
	}
	var added []*stack.Application
	for _, a := range after {
		if !had[a] {
			added = append(added, a)
		}
	}
	return added
}

// sameApplications reports whether a and b hold the same applications, each
// exactly once, in any order.
func sameApplications(a, b []*stack.Application) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[*stack.Application]int, len(a))
	for _, x := range a {
		count[x]++
	}
	for _, x := range b {
		if count[x] != 1 {
			return false
		}
		count[x]--
	}
	return true
}

// entryAppContract is the TraitHandler contract a refused trait broke.
const entryAppContract = "a TraitHandler mutates the application it is given and appends sub-applications, it must not replace, remove or rename a component's application"

// entryAppPolicyContract is the Enforceable contract a refused trait
// sub-application's ApplyPolicy broke.
const entryAppPolicyContract = "an Enforceable's ApplyPolicy enforces policy on its own configuration, it must not replace, remove or rename a component's application"

// entryAppNames holds the name of each bundle entry's application and, for a
// collapsed sibling group, of each member's, as they were when an entry's
// traits began.
type entryAppNames struct {
	entries []string
	// members is parallel to entries and nil until an entry is a group.
	members [][]string
}

// snapshotEntryAppNames takes entryAppNames for entries.
func snapshotEntryAppNames(entries []componentEntry) entryAppNames {
	s := entryAppNames{entries: make([]string, len(entries))}
	for i, c := range entries {
		s.entries[i] = c.app.Name
		if len(c.members) == 0 {
			continue
		}
		if s.members == nil {
			s.members = make([][]string, len(entries))
		}
		s.members[i] = make([]string, len(c.members))
		for j, m := range c.members {
			s.members[i][j] = m.app.Name
		}
	}
	return s
}

// subAppDecoratorContract is the SubApplicationDecorator contract a refused
// decorating trait broke.
const subAppDecoratorContract = "a SubApplicationDecorator must not add, remove, replace, rename or reorder applications"

// entryAppStep names the step checkEntryApplications checks after: a trait's
// Apply (the zero value), the ApplyPolicy of the sub-application policyOf the
// trait added, or the trait's decoration of sub-application decorating.
type entryAppStep struct {
	policyOf, decorating string
}

// checkEntryApplications fails when trait traitType of component component, at
// step, left a bundle entry's application out of the bundle, by pointer, or
// renamed it or a sibling group member's from names. The Phase-4 NetworkPolicy
// synthesis finds a component's application by its name and then its pointer,
// so a replaced, removed or renamed one would silently get no policy; a renamed
// member would generate its objects under a name the group does not carry
// (go-kure/launcher#734, go-kure/launcher#752, go-kure/launcher#763). A member
// is checked by name only:
// the bundle holds the group's application, and a trait is handed the member's
// application, never the group's list of them.
func checkEntryApplications(entries []componentEntry, names entryAppNames, bundle *stack.Bundle, component, traitType string, step entryAppStep) error {
	inBundle := make(map[*stack.Application]bool, len(bundle.Applications))
	for _, a := range bundle.Applications {
		inBundle[a] = true
	}
	for i, c := range entries {
		if !inBundle[c.app] {
			by, contract := entryAppRefusal(component, traitType, step)
			return &TransformError{Message: fmt.Sprintf(
				"%s replaced or removed the application of component %q; %s",
				by, c.component.Name, contract)}
		}
		if c.app.Name != names.entries[i] {
			by, contract := entryAppRefusal(component, traitType, step)
			return &TransformError{Message: fmt.Sprintf(
				"%s renamed the application of component %q from %q to %q; %s",
				by, c.component.Name, names.entries[i], c.app.Name, contract)}
		}
		for j, m := range c.members {
			if m.app.Name != names.members[i][j] {
				by, contract := entryAppRefusal(component, traitType, step)
				return &TransformError{Message: fmt.Sprintf(
					"%s renamed the application of member %q of sibling group %q from %q to %q; %s",
					by, m.component.Type, c.component.Name, names.members[i][j], m.app.Name, contract)}
			}
		}
	}
	return nil
}

// entryAppRefusal returns the subject of a checkEntryApplications error and the
// contract it broke.
func entryAppRefusal(component, traitType string, step entryAppStep) (by, contract string) {
	by = fmt.Sprintf("component %q trait %q", component, traitType)
	switch {
	case step.policyOf != "":
		return fmt.Sprintf("%s: the ApplyPolicy of sub-application %q", by, step.policyOf), entryAppPolicyContract
	case step.decorating != "":
		return fmt.Sprintf("%s while decorating sub-application %q", by, step.decorating), subAppDecoratorContract
	}
	return by, entryAppContract
}

// groupSubApp is a sub-application a trait on a sibling group member created,
// with that member's type.
type groupSubApp struct {
	app    *stack.Application
	member string
	// hookName is the name the Naming hook gave the sub-application, and defs the
	// defaults it gave that name in place of, for this trait: one, unless the
	// trait created several sub-applications of that name (hookDefaults). Both
	// are empty when the name is not the hook's.
	hookName string
	defs     []string
}

// checkGroupSubApplications fails when traits on two members of sibling group
// group created sub-applications of one name: members share the group's name,
// so traits that derive a sub-application name from it (web-rbac, web-ingress)
// would deploy one name twice. The names are read as they are now, after the
// ApplyPolicy of every sub-application created so far, since a policy may rename
// its own sub-application or an earlier one (go-kure/launcher#755).
//
// Two sub-applications that are both still under the name the Naming hook gave
// them are compared by their defaults instead: the hook is asked the same
// question for the same trait on two members, so their defaults meet whatever it
// answers, while two different sub-applications it gave one name are accepted as
// they are outside a group. Every other pair is compared by name as before: one
// the hook named against one it did not name, or against one a policy renamed
// since (go-kure/launcher#787).
//
// A trait that created several sub-applications of one name has them compared
// by all the defaults of that name (hookDefaults): any shared with the other
// member's refuses the pair.
func checkGroupSubApplications(group string, subApps []groupSubApp) error {
	for i, s := range subApps {
		for _, prior := range subApps[:i] {
			if prior.member == s.member {
				continue
			}
			switch {
			case prior.hookNamed() && s.hookNamed():
				for _, def := range s.defs {
					if slices.Contains(prior.defs, def) {
						return &TransformError{Message: fmt.Sprintf(
							"sibling group %q: traits on members %q and %q both create sub-application %q (named %q by the Naming hook); carry the trait on one member",
							group, prior.member, s.member, def, s.hookName)}
					}
				}
			case prior.app.Name == s.app.Name:
				hooked := prior
				if s.hookNamed() {
					hooked = s
				}
				if !hooked.hookNamed() {
					return &TransformError{Message: fmt.Sprintf(
						"sibling group %q: traits on members %q and %q both create sub-application %q; carry the trait on one member",
						group, prior.member, s.member, s.app.Name)}
				}
				defs := make([]string, len(hooked.defs))
				for j, def := range hooked.defs {
					defs[j] = strconv.Quote(def)
				}
				return &TransformError{Message: fmt.Sprintf(
					"sibling group %q: traits on members %q and %q both create sub-application %q (the Naming hook's name for %s on member %q); carry the trait on one member, or return another name from the hook",
					group, prior.member, s.member, s.app.Name, strings.Join(defs, " and "), hooked.member)}
			}
		}
	}
	return nil
}

// hookNamed reports whether the sub-application still carries the name the
// Naming hook gave it: no policy renamed it since.
func (s groupSubApp) hookNamed() bool {
	return s.hookName != "" && s.app.Name == s.hookName
}

// applyEntryTraits applies the traits of one entry — each member's own, on that
// member's application and in authored order, for a collapsed sibling group
// (traitSteps). It returns the sub-applications the entry's traits appended to
// the bundle, in creation order, and records each decorating trait
// (SubApplicationDecorator) with them in ctx.subAppDecorations.
//
// bundleEntries are the entries whose applications the bundle was built from.
// Each must still be in the bundle, by pointer and under the name it had when
// this entry's traits began, and each sibling group member's application under
// its name then, after every trait and after the ApplyPolicy of every
// sub-application one added (checkEntryApplications).
func (t *Transformer) applyEntryTraits(app *Application, e componentEntry, bundleEntries []componentEntry, bundle *stack.Bundle, ctx TransformContext) ([]*stack.Application, error) {
	var subApps []*stack.Application
	// For a sibling group: each trait-created sub-application and the member type
	// whose trait created it (checkGroupSubApplications).
	var groupSubApps []groupSubApp
	var decorators []subAppDecoration
	// A sibling group applies a trait forwarded to two members on each; its
	// sub-applications are decorated once per authored trait. The slot alone does
	// not name the trait — a trait rule's output keeps its input's slot — so the
	// key adds the type, and only another member's copy is skipped.
	type slotTrait struct {
		index int
		typ   string
	}
	decorated := make(map[slotTrait]string)
	steps := e.traitSteps(app)
	// The names are taken only for an entry with a trait to check: a traitless
	// one would pay for every entry's name and use none (go-kure/launcher#747).
	var entryNames entryAppNames
	if slices.ContainsFunc(steps, func(s traitStep) bool { return len(s.traits) > 0 }) {
		entryNames = snapshotEntryAppNames(bundleEntries)
	}
	for _, step := range steps {
		entry := step.entry
		for position, trait := range step.traits {
			handler := t.findTraitHandler(trait.Type)
			if handler == nil {
				where := traitLocation(&entry.component, trait.origin)
				return nil, &TransformError{Message: noHandlerMessage("trait", trait.Type, where, deliveryTraitTypes)}
			}
			// A sealed trait was emitted by a lowering rule, which already merged
			// capability rendering into it (D5) before the fixpoint settled — the
			// information-closure rule does not allow a second, different-key merge
			// here (a fifth input). So every capability-processing step below is
			// skipped entirely for a sealed trait; the trait's Properties are final.
			// Sealing does not exempt it from D3: only a synthesized one is exempt.
			resolved := trait
			matched := false
			matchedKey := ""
			if !trait.sealed {
				resolved, matchedKey, matched = resolveCapability(trait, ctx.Capabilities)

				if aware, ok := handler.(CapabilityAware); ok && aware.CapabilityRequired() && !matched {
					return nil, &TransformError{
						Message: fmt.Sprintf("component %q trait %q: capability %q not found in ClusterProfile",
							entry.component.Name, trait.Type, buildCapabilityKey(trait)),
						Cause: ErrMissingCapability,
					}
				}

				// For custom (non-built-in) traits whose capability rendering resolved in the
				// profile, warn or error when no CapabilityDefinition was loaded for the type.
				if !t.builtinTraitTypes[trait.Type] && matched {
					if _, hasDef := t.capabilityDefs[trait.Type]; !hasDef {
						msg := fmt.Sprintf("no CapabilityDefinition found for custom trait %q", trait.Type)
						if t.strictCapabilities {
							return nil, &TransformError{Message: msg}
						}
						if t.warnHandler != nil {
							t.warnHandler(msg)
						}
					}
				}

				// D3: an authored value for a platform-reserved property is rejected
				// before capability rendering is merged in. Checked against trait.Properties
				// (the pre-merge original) — resolveCapability's merged rendering must stay
				// invisible to this check, and cannot make an authored value exempt: only
				// a value the trait itself recorded (Trait.RenderReserved) is.
				if p, ok := handler.(PropertySchemaProvider); ok {
					if err := enforcePlatformReserved(p.PropertySchema(), trait.Properties, trait.rendered, "properties"); err != nil {
						return nil, &TransformError{
							Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
							Cause:   err,
						}
					}
					// Nested Required is checked on the merged properties, where a
					// rendering may have supplied a required key the author left out
					// (go-kure/launcher#765). It runs whether or not a binding matched.
					if err := checkNestedRequired(p.PropertySchema(), resolved.Properties, "properties", boundCapability(matched, matchedKey)); err != nil {
						return nil, &TransformError{
							Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
							Cause:   err,
						}
					}
				}

				if matched && ctx.consumedCapabilities != nil {
					ctx.consumedCapabilities[matchedKey] = struct{}{}
				}
			} else if p, ok := handler.(PropertySchemaProvider); ok && !trait.synthesized {
				// D3 on a sealed trait no checked rule emitted (Trait.synthesized): a
				// schema-less rule may have copied an authored reserved value into it.
				// Its Properties are final, so they are checked as they stand, a value
				// the rule recorded with Trait.RenderReserved exempt.
				if err := enforcePlatformReserved(p.PropertySchema(), trait.Properties, trait.rendered, "properties"); err != nil {
					return nil, &TransformError{
						Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
						Cause:   err,
					}
				}
			}
			// What the trait's handler resolves its names through. A decorating
			// trait keeps it for its later Apply calls (decorateSubApplications),
			// which then resolve the same names for the same owner.
			member := ""
			if len(e.members) > 0 {
				member = entry.component.Type
			}
			resolved.naming = ctx.names.forTrait(entry.component.Name, member, trait, step.first+position)
			if resolved.naming != nil {
				resolved.naming.objectName = entry.component.ObjectName()
			}
			prev := slices.Clone(bundle.Applications)
			if err := handler.Apply(&resolved, entry.app, bundle); err != nil {
				return nil, &TransformError{
					Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
					Cause:   err,
				}
			}
			if err := checkEntryApplications(bundleEntries, entryNames, bundle, entry.component.Name, trait.Type, entryAppStep{}); err != nil {
				return nil, err
			}

			added := addedApplications(prev, bundle.Applications)
			// Which of them the Naming hook named is read now, under the names the
			// trait gave them: a policy may rename one below.
			var created []groupSubApp
			if len(e.members) > 0 {
				names := resolved.naming.takeSubAppNames()
				ofName := make(map[string]int, len(added))
				for _, newApp := range added {
					ofName[newApp.Name]++
				}
				for _, newApp := range added {
					s := groupSubApp{app: newApp, member: entry.component.Type}
					if defs := hookDefaults(names[newApp.Name], ofName[newApp.Name]); len(defs) > 0 {
						s.hookName, s.defs = newApp.Name, defs
					}
					created = append(created, s)
				}
			}
			for _, newApp := range added {
				if enforceable, ok := newApp.Config.(Enforceable); ok {
					if err := enforceable.ApplyPolicy(ctx.Policy); err != nil {
						return nil, NewViolationError(entry.component.Name, err)
					}
					// The policy runs after the trait's check, so it is held to the
					// same names (go-kure/launcher#752).
					if err := checkEntryApplications(bundleEntries, entryNames, bundle, entry.component.Name, trait.Type, entryAppStep{policyOf: newApp.Name}); err != nil {
						return nil, err
					}
				}
			}
			if len(e.members) > 0 {
				groupSubApps = append(groupSubApps, created...)
				if err := checkGroupSubApplications(entry.component.Name, groupSubApps); err != nil {
					return nil, err
				}
			}
			subApps = append(subApps, added...)
			// Recorded under the member the trait ran on, not the group: only
			// that member's Flux object reading one moves it.
			if len(added) > 0 && ctx.traitSubApps != nil {
				*ctx.traitSubApps = append(*ctx.traitSubApps, traitSubApps{owner: entry.app, subApps: added})
			}

			if d, ok := handler.(SubApplicationDecorator); ok && d.DecoratesSubApplications() {
				if trait.authoredIndex != nil {
					key := slotTrait{*trait.authoredIndex, trait.Type}
					if member, seen := decorated[key]; seen && member != entry.component.Type {
						continue
					}
					decorated[key] = entry.component.Type
				}
				decorators = append(decorators, subAppDecoration{
					component: entry.component.Name, trait: resolved, handler: handler, bundle: bundle, entries: bundleEntries,
				})
			}
		}
	}
	if len(subApps) > 0 && ctx.subAppDecorations != nil {
		for _, d := range decorators {
			d.subApps = subApps
			*ctx.subAppDecorations = append(*ctx.subAppDecorations, d)
		}
	}
	return subApps, nil
}

// decorateSubApplications applies each recorded decorating trait to the trait
// sub-applications of its component, as the last step of the build. It runs
// after every trait of every component, so a decorator authored before the trait
// that creates a sub-application covers it as well as one authored after.
//
// The bundle's order is final by then, so a decorator must leave its
// applications exactly as they are. They are compared by pointer and position
// rather than by count, so a removal followed by an append is caught too, and
// by name: the NetworkPolicy synthesis has already named its objects after
// the applications (go-kure/launcher#734). The snapshot is a copy because a
// removal shifts the shared backing array in place. A sibling group member's
// application is not in the bundle, so its name is checked against the
// entries' (checkEntryApplications, go-kure/launcher#763).
func decorateSubApplications(decorations []subAppDecoration) error {
	for _, d := range decorations {
		names := snapshotEntryAppNames(d.entries)
		for _, subApp := range d.subApps {
			prev := slices.Clone(d.bundle.Applications)
			prevNames := make([]string, len(prev))
			for i, a := range prev {
				prevNames[i] = a.Name
			}
			subAppName := subApp.Name
			if err := d.handler.Apply(&d.trait, subApp, d.bundle); err != nil {
				return &TransformError{
					Message: fmt.Sprintf("component %q trait %q on sub-application %q", d.component, d.trait.Type, subAppName),
					Cause:   err,
				}
			}
			if !slices.Equal(prev, d.bundle.Applications) || !slices.EqualFunc(prev, prevNames, func(a *stack.Application, name string) bool { return a.Name == name }) {
				return &TransformError{Message: fmt.Sprintf(
					"component %q trait %q changed the bundle's applications while decorating sub-application %q; %s",
					d.component, d.trait.Type, subAppName, subAppDecoratorContract)}
			}
			if err := checkEntryApplications(d.entries, names, d.bundle, d.component, d.trait.Type, entryAppStep{decorating: subAppName}); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- Helpers ---

// resolveCapability merges capability rendering into trait properties (rendering as
// defaults, OAM inline values win), recursively into nested objects
// (mergeRenderedProperties, go-kure/launcher#750). An authored null, typed nil
// included, is absent (the null contract, isNullValue), so it does not displace a
// rendering value for its key (go-kure/launcher#742). Tries the scoped key, falls back to the bare
// type key. Returns (trait, "", false) on no match; otherwise (possibly merged
// trait, matched key, true) — a match with empty Rendering still counts as consumed.
// The rendering is copied with its Go types kept (copyRendering), which relies on
// TransformWithPolicy having checked it (checkCapabilityRenderings).
func resolveCapability(trait Trait, capabilities map[string]CapabilityBinding) (Trait, string, bool) {
	if len(capabilities) == 0 {
		return trait, "", false
	}
	key := buildCapabilityKey(trait)
	cap, ok := capabilities[key]
	matchedKey := key
	if !ok {
		cap, ok = capabilities[trait.Type]
		matchedKey = trait.Type
	}
	if !ok {
		return trait, "", false
	}
	if len(cap.Rendering) == 0 {
		return trait, matchedKey, true
	}

	result := trait
	result.Properties = mergeRenderedProperties(copyRendering(cap.Rendering), trait.Properties)
	return result, matchedKey, true
}

// mergeRenderedProperties merges authored over rendered key by key, recursing where
// both hold an object (map[string]any), so an authored nested key overrides only
// that key and the rendering's sibling keys are kept (go-kure/launcher#750). An
// authored null, typed nil included (isNullValue), over a rendered key is absent at
// any depth and keeps the rendered value; under a key the rendering lacks it is left
// as authored. Any other authored value, a list included, replaces the rendered one
// whole, as does an object over a rendered null (a typed nil map included) or over a
// rendered object of another Go type (map[string]string). rendered must be the
// caller's own copy: its top level is written into and returned, and each nested
// object is cloned before it is written into. authored is never mutated.
func mergeRenderedProperties(rendered, authored map[string]any) map[string]any {
	for k, v := range authored {
		r, has := rendered[k]
		if has && isNullValue(v) {
			continue
		}
		if rm, ok := r.(map[string]any); ok && rm != nil {
			if am, ok := v.(map[string]any); ok {
				// Cloned before it is written into: copyRendering keeps an object the
				// rendering shares between two keys shared, and an override of one
				// must not reach the other.
				rendered[k] = mergeRenderedProperties(maps.Clone(rm), am)
				continue
			}
		}
		rendered[k] = v
	}
	return rendered
}

// applyComponentCapabilityDefaults returns props with each key d lists that props
// leaves unauthored (absent, or a null as isNullValue reads it) taken from the rendering of the
// capability d names: the component counterpart of resolveCapability's "rendering
// as defaults, inline wins", restricted to the listed keys. The key is recorded as
// consumed when the profile binds it, whether or not a value was copied, as a trait's
// matched key is. props is never mutated: a copy is returned when a value is filled,
// and the filled values are deep copies that keep their Go types, so no component
// shares a value with the profile and an integer stays an integer. A filled value
// that is not a property value (checkRenderedValue) is an error, never shared
// instead; TransformWithPolicy refuses one before this runs (checkCapabilityRenderings).
// Each filled value is then validated against the component's schema
// (validateCapabilityFill).
func (t *Transformer) applyComponentCapabilityDefaults(d ComponentCapabilityDefaults, props map[string]any, ctx TransformContext) (map[string]any, error) {
	key, keys := d.CapabilityDefaults()
	binding, ok := ctx.Capabilities[key]
	if !ok {
		return props, nil
	}
	if ctx.consumedCapabilities != nil {
		ctx.consumedCapabilities[key] = struct{}{}
	}
	fill := map[string]any{}
	for _, k := range keys {
		if v, authored := props[k]; authored && !isNullValue(v) {
			continue
		}
		if v, has := binding.Rendering[k]; has && !isNullValue(v) {
			fill[k] = v
		}
	}
	if len(fill) == 0 {
		return props, nil
	}
	for _, k := range slices.Sorted(maps.Keys(fill)) {
		if err := checkRenderedValue(fill[k], map[propertyCopyKey]bool{}); err != nil {
			return nil, errors.Wrapf(err, "capability %q defaults: rendering key %q", key, k)
		}
	}
	copied := copyRendering(fill)
	if err := t.validateCapabilityFill(d, key, copied); err != nil {
		return nil, err
	}
	out := maps.Clone(props)
	if out == nil {
		out = make(map[string]any, len(fill))
	}
	maps.Copy(out, copied)
	return out, nil
}

// capabilityValidated reports whether EvaluateProfile validates a rendering of
// capability type typeName: the trait handler registered for it, else the trait
// lowering rule, implements ValidateAndApplyDefaults, or the type is not built in
// and has a CapabilityDefinition, whose schema EvaluateProfile applies. With neither
// a handler nor a rule, EvaluateProfile passes the binding through unchecked. It
// mirrors EvaluateProfile's branches; a change to one is a change to both.
func (t *Transformer) capabilityValidated(typeName string) bool {
	var registered any
	if h, ok := t.traitHandlers[typeName]; ok {
		registered = h
	} else if rule, ok := t.traitLoweringRules[typeName]; ok {
		registered = rule
	} else {
		return false
	}
	if _, ok := registered.(ValidateAndApplyDefaults); ok {
		return true
	}
	_, hasDef := t.capabilityDefs[typeName]
	return hasDef && !t.builtinTraitTypes[typeName]
}

// validateCapabilityFill checks the values applyComponentCapabilityDefaults is about
// to fill against the component's own PropertySchema, and writes back each value
// as validation normalized it, as authored validation does (go-kure/launcher#751).
// EvaluateProfile validates a binding only through the trait handler or trait
// lowering rule of its type, whose schema can differ from the component's, so the
// component's schema decides. A component that declares no schema relies on that
// trait-side check alone, and the fill is refused when it does not validate the
// rendering (capabilityValidated, go-kure/launcher#772).
func (t *Transformer) validateCapabilityFill(d ComponentCapabilityDefaults, key string, fill map[string]any) error {
	p, ok := d.(PropertySchemaProvider)
	if !ok {
		typeName, _, _ := strings.Cut(key, ".")
		if t.capabilityValidated(typeName) {
			return nil
		}
		return errors.Errorf("capability %q defaults: nothing validates rendering keys %q: the component handler declares no PropertySchema, and no trait handler or trait lowering rule for type %q validates the rendering (none implements ValidateAndApplyDefaults, and no CapabilityDefinition applies to it); implement PropertySchemaProvider on the component handler, or ValidateAndApplyDefaults on the trait",
			key, slices.Sorted(maps.Keys(fill)), typeName)
	}
	schema := p.PropertySchema()
	for _, k := range slices.Sorted(maps.Keys(fill)) {
		field, declared := schema[k]
		if !declared {
			return errors.Errorf("capability %q defaults: rendering key %q is not a property the component declares (allowed: %s)", key, k, declaredFields(schema))
		}
		normalized, err := validatePropertyValue(field, fill[k], "properties."+k)
		if err != nil {
			return errors.Wrapf(err, "capability %q defaults", key)
		}
		fill[k] = normalized
	}
	return nil
}

// checkCapabilityRenderings refuses a capability binding whose rendering holds a value
// that is not a property value (checkRenderedValue): a null below the top level,
// NaN or ±Inf, or a Go type other than a string, boolean, number, list or
// string-keyed object. Both merge sites (resolveCapability and
// applyComponentCapabilityDefaults) copy a rendering with copyRendering, which keeps
// Go types but cannot copy such a value, so the check runs once, for every binding,
// before anything is built (go-kure/launcher#756). A null top-level value is not
// refused: it reads as absent (isNullValue).
func checkCapabilityRenderings(capabilities map[string]CapabilityBinding) error {
	for _, key := range slices.Sorted(maps.Keys(capabilities)) {
		rendering := capabilities[key].Rendering
		for _, k := range slices.Sorted(maps.Keys(rendering)) {
			if isNullValue(rendering[k]) {
				continue
			}
			if err := checkRenderedValue(rendering[k], map[propertyCopyKey]bool{}); err != nil {
				return errors.Wrapf(err, "capability %q rendering key %q", key, k)
			}
		}
	}
	return nil
}

// copyRendering returns a deep copy of a rendering checkCapabilityRenderings
// accepted, keeping each value's Go type (copyRenderedValue): an int stays an int,
// and an int64 above 2^53 keeps its value. A nil value is kept as nil.
func copyRendering(rendering map[string]any) map[string]any {
	out := make(map[string]any, len(rendering))
	for k, v := range rendering {
		if v == nil {
			out[k] = nil
			continue
		}
		out[k] = copyRenderedValue(v)
	}
	return out
}

// buildCapabilityKey returns "<type>.<scope>" when the trait carries a non-empty
// scope property, or "<type>" otherwise. Resolution falls back to the bare type key.
func buildCapabilityKey(trait Trait) string {
	if scope, ok := trait.Properties["scope"].(string); ok && scope != "" {
		return trait.Type + "." + scope
	}
	return trait.Type
}

// postProcessFluxNamespace walks every bundle's own applications and calls
// SetFluxNamespace on any config that satisfies fluxNamespaceSettable, then
// moves the trait sub-applications those Flux objects read from their own
// namespace (moveFluxNamespaceInputs). It walks every bundle, not only the
// leaves: an ordered application's own bundle holds its generated sources,
// which are Flux objects too.
func postProcessFluxNamespace(cluster *stack.Cluster, owned []traitSubApps, ns string) {
	if cluster == nil || ns == "" {
		return
	}
	walkBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			if setter, ok := app.Config.(fluxNamespaceSettable); ok {
				setter.SetFluxNamespace(ns)
			}
		}
	})
	moveFluxNamespaceInputs(owned, ns)
}

// walkBundles calls fn for every bundle reachable from node, a bundle before
// its children: the order their own applications are applied in.
func walkBundles(node *stack.Node, fn func(*stack.Bundle)) {
	if node == nil {
		return
	}
	if node.Bundle != nil {
		walkBundle(node.Bundle, fn)
	}
	for _, child := range node.Children {
		walkBundles(child, fn)
	}
}

func walkBundle(bundle *stack.Bundle, fn func(*stack.Bundle)) {
	if bundle == nil {
		return
	}
	fn(bundle)
	for _, child := range bundle.Children {
		walkBundle(child, fn)
	}
}

// walkLeafBundles calls fn for every leaf bundle reachable from node. It skips
// the own applications of a bundle that has children: the NetworkPolicy
// synthesis that uses it reads workloads, and an ordered application's own
// bundle holds only its generated sources (buildCluster).
func walkLeafBundles(node *stack.Node, fn func(*stack.Bundle)) {
	if node == nil {
		return
	}
	if node.Bundle != nil {
		walkLeafBundle(node.Bundle, fn)
	}
	for _, child := range node.Children {
		walkLeafBundles(child, fn)
	}
}

func walkLeafBundle(bundle *stack.Bundle, fn func(*stack.Bundle)) {
	if bundle == nil {
		return
	}
	if !bundle.IsUmbrella() {
		fn(bundle)
		return
	}
	for _, child := range bundle.Children {
		walkLeafBundle(child, fn)
	}
}

// warnDeprecatedTypes passes one warning per authored component, and per authored
// trait, whose type's registered handler or lowering rule declares
// ContractMetadata.Deprecated, in document order. It never fails the build and never
// changes the output.
func (t *Transformer) warnDeprecatedTypes(app *Application) {
	if t.warnHandler == nil {
		return
	}
	for _, comp := range app.Spec.Components {
		if md, ok := t.componentContract(comp.Type); ok && md.Deprecated {
			t.warnHandler(deprecationWarning(fmt.Sprintf("component %q: type %s", comp.Name, comp.Type), md.DeprecationMessage))
		}
		for _, trait := range comp.Traits {
			if md, ok := t.traitContract(trait.Type); ok && md.Deprecated {
				t.warnHandler(deprecationWarning(fmt.Sprintf("component %q: trait type %s", comp.Name, trait.Type), md.DeprecationMessage))
			}
		}
	}
}

// componentContract returns the ContractMetadata of the handler or lowering rule
// registered for a component type; a type is registered as one or the other.
func (t *Transformer) componentContract(typeName string) (ContractMetadata, bool) {
	if h, ok := t.componentHandlers[typeName].(ContractDescriber); ok {
		return h.ContractMetadata(), true
	}
	if r, ok := t.componentLoweringRules[typeName].(ContractDescriber); ok {
		return r.ContractMetadata(), true
	}
	return ContractMetadata{}, false
}

// traitContract is componentContract for a trait type.
func (t *Transformer) traitContract(typeName string) (ContractMetadata, bool) {
	if h, ok := t.traitHandlers[typeName].(ContractDescriber); ok {
		return h.ContractMetadata(), true
	}
	if r, ok := t.traitLoweringRules[typeName].(ContractDescriber); ok {
		return r.ContractMetadata(), true
	}
	return ContractMetadata{}, false
}

func deprecationWarning(subject, message string) string {
	if message == "" {
		return subject + " is deprecated"
	}
	return subject + " is deprecated: " + message
}

// collectTraitTypes returns the unique trait types used across all components.
func collectTraitTypes(app *Application) []string {
	seen := make(map[string]bool)
	var types []string
	for _, comp := range app.Spec.Components {
		for _, trait := range comp.Traits {
			if !seen[trait.Type] {
				seen[trait.Type] = true
				types = append(types, trait.Type)
			}
		}
	}
	return types
}

// enforceCapabilityConstraints checks that the application's trait types satisfy the
// capability constraints from the active Policy. Returns nil when all constraint slices
// are empty (which is always true for NoopPolicy).
func enforceCapabilityConstraints(traitTypes []string, policy Policy) error {
	forbidden := policy.ForbiddenCapabilities()
	allowed := policy.AllowedCapabilities()
	required := policy.RequiredCapabilities()

	if len(forbidden) == 0 && len(allowed) == 0 && len(required) == 0 {
		return nil
	}

	used := make(map[string]bool, len(traitTypes))
	for _, t := range traitTypes {
		used[t] = true
	}

	if len(forbidden) > 0 {
		forbiddenSet := make(map[string]bool, len(forbidden))
		for _, f := range forbidden {
			forbiddenSet[f] = true
		}
		for _, t := range traitTypes {
			if forbiddenSet[t] {
				return NewPolicyRefusal(RefusalTraitCapability, fmt.Sprintf("capability %q is forbidden by environment policy", t))
			}
		}
	}

	if len(allowed) > 0 {
		allowedSet := make(map[string]bool, len(allowed))
		for _, a := range allowed {
			allowedSet[a] = true
		}
		for _, t := range traitTypes {
			if !allowedSet[t] {
				return NewPolicyRefusal(RefusalTraitCapability, fmt.Sprintf("capability %q is not in the allowed list", t))
			}
		}
	}

	for _, r := range required {
		if !used[r] {
			return NewPolicyRefusal(RefusalTraitCapability, fmt.Sprintf("required capability %q is missing", r))
		}
	}

	return nil
}
