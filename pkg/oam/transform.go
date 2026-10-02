package oam

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
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
	// like EgressPeers: a caller that injects trafficSources/EgressPeers must ensure its
	// pods carry this label (the platform stamps the derived component label) or set this
	// to a key its pods do carry (e.g. "app"). The selector value is always
	// ComponentLabelValue(component), so a platform stamping the label uses that function
	// too, never the raw component name.
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
// sub-applications that component's traits appended to bundle.
type subAppDecoration struct {
	component string
	trait     Trait
	handler   TraitHandler
	bundle    *stack.Bundle
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

// autoHealthCheckEmitter is implemented by ApplicationConfig types whose
// auto health-check (from componentHealthCheckGVK) is only meaningful for some
// of the documents they accept. Two shapes qualify, and both return false:
//
//   - the config emits no object for the check to reference for some
//     documents. No built-in config takes this shape today.
//   - the config emits the object, but the document instructs the workload not
//     to progress, so a readiness gate on it is not a health signal. Deployment
//     returns false for paused: true, job and helmrelease for suspend: true.
//
// Decorators that wrap such configs must forward this call (mirroring
// fluxNamespaceSettable). Configs that do not implement it are assumed to emit
// an object that can reach a ready state.
type autoHealthCheckEmitter interface {
	EmitsAutoHealthCheck() bool
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
	for name, h := range t.componentHandlers {
		if p, ok := h.(PropertySchemaProvider); ok {
			set.Components[name] = p.PropertySchema()
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
// component/trait handlers and component/trait lowering rules, keyed by type name.
// Mirrors HandlerSchemaSet's Components/Traits split for the identical reason: a
// component and a trait that share a type name must not collide, and a consumer
// needs to know which registry a contract came from.
type HandlerContractSet struct {
	Components map[string]ContractMetadata
	Traits     map[string]ContractMetadata
}

// HandlerContracts returns the ContractMetadata of every registered component and
// trait handler, and every component/trait lowering rule, that implements
// ContractDescriber. Entries that do not implement it are omitted. The maps are
// always non-nil. Covers the four component/trait registries HandlerSchemas covers, for the same
// reason: a type reachable only through a lowering rule (e.g. "expose", claimed via
// RegisterTraitLowering rather than RegisterBuiltinTrait) must still publish its
// contract metadata here — otherwise a caller discovering contracts would see a gap
// for exactly the types HandlerSchemas already had to stop omitting (see that
// method's comments above).
func (t *Transformer) HandlerContracts() HandlerContractSet {
	set := HandlerContractSet{
		Components: make(map[string]ContractMetadata),
		Traits:     make(map[string]ContractMetadata),
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
func (t *Transformer) ComponentEndpoints(comp *Component) ([]netpol.Endpoint, error) {
	if comp == nil {
		return nil, nil
	}
	// A type is a rule or a handler, never both (RegisterComponent,
	// RegisterComponentLowering), so this is never a choice.
	var provider any = t.findComponentHandler(comp.Type)
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		provider = rule
	}
	ep, ok := provider.(EndpointProvider)
	if !ok {
		return nil, nil
	}
	eps, err := ep.Endpoints(comp)
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
func (t *Transformer) Transform(app *Application, ctx TransformContext) (*stack.Cluster, error) {
	cluster, _, err := t.TransformWithPolicy(app, ctx)
	return cluster, err
}

// TransformWithPolicy converts an OAM Application to a kure Cluster and
// returns the accumulated PolicyResult. ctx.Policy is normalized to NoopPolicy
// if nil so that all pipeline stages always receive a non-nil Policy value.
func (t *Transformer) TransformWithPolicy(app *Application, ctx TransformContext) (*stack.Cluster, *PolicyResult, error) {
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
	// The namespace override (kurel build --namespace) and the Flux namespace are stamped
	// onto metadata.namespace as given, so both must be DNS-1123 labels. The messages name
	// the value as a CLI reader knows it, not the field.
	if err := checkNamespaceLabel("namespace", ctx.Namespace); err != nil {
		return nil, nil, err
	}
	if err := checkNamespaceLabel("flux namespace", ctx.FluxNamespace); err != nil {
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

	namespace := ctx.Namespace
	if namespace == "" {
		namespace = app.Metadata.Namespace
	}
	if namespace == "" {
		namespace = "default"
	}

	// Phase 1: create applications, apply Enforceable policy, classify tiers.
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
		return nil, nil, &ViolationError{Component: app.Metadata.Name, Cause: err}
	}

	// Phase 2: apply OAM policies (placement overrides, dependency graph).
	policyResult, err := t.applyPolicies(app, entries)
	if err != nil {
		return nil, nil, err
	}

	// Apply placement tier overrides before grouping. A generated source
	// (isGeneratedSource) deploys first and waits on nothing: placed in a later
	// tier, or made to depend on another component, it could follow one of its
	// own consumers, which would then never become ready and hold it back forever.
	for i, entry := range entries {
		tier, overridden := policyResult.TierOverrides[entry.component.Name]
		if isGeneratedSource(&entry.component) {
			if overridden && tier != TierInfra {
				return nil, nil, errors.Errorf("placement cannot move %s %q to tier %s: a source a lowering rule generates deploys in infra, before every consumer", entry.component.Type, entry.component.Name, tier)
			}
			if deps := policyResult.Dependencies[entry.component.Name]; len(deps) > 0 {
				return nil, nil, errors.Errorf("dependency cannot make %s %q wait on %s: a source a lowering rule generates deploys first, before every consumer", entry.component.Type, entry.component.Name, strings.Join(deps, ", "))
			}
		}
		if overridden {
			entries[i].tier = tier
		}
	}
	if err := checkSiblingTiers(entries, policyResult.TierOverrides); err != nil {
		return nil, nil, err
	}

	// A shared source is emitted by the consumer that deploys first. Deciding this
	// only now, with final tiers and dependencies known, keeps the owner from
	// waiting on another consumer of its source, which would deadlock.
	deduplicateSourceRefs(deploymentOrder(entries, policyResult.Dependencies))

	// Phase 3: group by tier and build cluster.
	tierGroups := groupByTier(entries)

	var cluster *stack.Cluster
	if policyResult.HasDependencies() {
		cluster, err = t.buildDependencyAwareCluster(app, entries, policyResult.Dependencies, ctx)
	} else if len(tierGroups) <= 1 {
		cluster, err = t.buildFlatCluster(app, entries, ctx)
	} else {
		cluster, err = t.buildHierarchicalCluster(app, entries, tierGroups, ctx)
	}
	if err != nil {
		return nil, nil, err
	}
	// Traits have now wrapped what they wrap, so a sibling group's members are
	// checked as phase 4 will read them through the group's config.
	if err := checkSiblingGroups(entries); err != nil {
		return nil, nil, err
	}

	// Phase 4: post-build bundle decorations.
	componentMap := make(map[string]componentEntry, len(entries))
	for _, e := range entries {
		componentMap[e.component.Name] = e
	}
	applyAutoHealthChecks(cluster, componentMap, policyResult.HealthCheckOverrides, ctx.FluxNamespace)
	applyReconciliationSettings(cluster, componentMap, policyResult.ReconciliationSettings)
	labelKey := ctx.ComponentLabelKey
	if labelKey == "" {
		labelKey = ComponentLabelKeyForDomain(ctx.Domain)
	}
	if err := synthesizeNetworkPolicies(cluster, componentMap, labelKey); err != nil {
		return nil, nil, err
	}
	// Egress synthesis fails fast on a malformed non-authorable peer (ported but selector-less):
	// a producer bug should fail the build, not silently emit a namespace-wide egress allow.
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, ctx.EgressPeers, labelKey); err != nil {
		return nil, nil, err
	}
	synthesizeEndpointIngressNetworkPolicies(cluster, componentMap, ctx.IngressPeers)
	postProcessFluxNamespace(cluster, *ctx.traitSubApps, ctx.FluxNamespace)
	// Last: a decorator hides the interfaces the steps above read on a trait
	// sub-application (the NetworkPolicy synthesis collectors among them).
	if err := decorateSubApplications(*ctx.subAppDecorations); err != nil {
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

// createApplications converts OAM components to stack applications, applies
// Enforceable policy and then each component's post-policy steps, and classifies
// each component into a deployment tier.
func (t *Transformer) createApplications(app *Application, namespace string, ctx TransformContext) ([]componentEntry, error) {
	entries := make([]componentEntry, 0, len(app.Spec.Components))
	for i, component := range app.Spec.Components {
		handler := t.findComponentHandler(component.Type)
		if handler == nil {
			return nil, &TransformError{Message: fmt.Sprintf("no handler for component type %q", component.Type)}
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

		// After D3, which checks only what was authored: the capability defaults a
		// ComponentCapabilityDefaults handler names fill the keys left unauthored.
		if d, ok := handler.(ComponentCapabilityDefaults); ok && !component.synthesized {
			component.Properties = applyComponentCapabilityDefaults(d, component.Properties, ctx)
		}

		config, err := handler.ToApplicationConfig(&component, namespace)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("component %q", component.Name), Cause: err}
		}

		if enforceable, ok := config.(Enforceable); ok {
			if err := enforceable.ApplyPolicy(ctx.Policy); err != nil {
				return nil, &ViolationError{Component: component.Name, Cause: err}
			}
		}

		// The steps a lowering rule attached (Component.AfterPolicy) run on the
		// config the policy has just decided, before any trait of the component.
		for _, step := range component.afterPolicy {
			if err := step(config); err != nil {
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
			return nil, &TransformError{Message: fmt.Sprintf("no handler for policy type %q", p.Type)}
		}
		if err := handler.Apply(&p, componentNames, result); err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("policy %q", p.Name), Cause: err}
		}
	}

	return result, nil
}

// buildFlatCluster creates a single-bundle cluster when all components belong to one tier.
func (t *Transformer) buildFlatCluster(app *Application, entries []componentEntry, ctx TransformContext) (*stack.Cluster, error) {
	apps := make([]*stack.Application, 0, len(entries))
	for _, e := range entries {
		apps = append(apps, e.app)
	}

	bundle, err := stack.NewBundle(app.Metadata.Name, apps, nil)
	if err != nil {
		return nil, &TransformError{Message: "failed to create bundle", Cause: err}
	}

	if err := t.applyTraits(app, entries, bundle, ctx); err != nil {
		return nil, err
	}

	node := &stack.Node{Name: "", Bundle: bundle}
	return stack.NewCluster(ctx.ClusterID, node), nil
}

// buildHierarchicalCluster creates an umbrella bundle with one tier-child per populated tier.
func (t *Transformer) buildHierarchicalCluster(app *Application, entries []componentEntry, tierGroups map[Tier][]componentEntry, ctx TransformContext) (*stack.Cluster, error) {
	tierBundles := make([]*stack.Bundle, 0, len(tierGroups))
	for _, tier := range TierOrder {
		group, ok := tierGroups[tier]
		if !ok {
			continue
		}

		apps := make([]*stack.Application, 0, len(group))
		for _, e := range group {
			apps = append(apps, e.app)
		}

		bundleName := fmt.Sprintf("%s-%s", app.Metadata.Name, tier)
		bundle, err := stack.NewBundle(bundleName, apps, nil)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("failed to create %s bundle", tier), Cause: err}
		}

		if err := t.applyTraits(app, group, bundle, ctx); err != nil {
			return nil, err
		}

		tierBundles = append(tierBundles, bundle)
	}

	// Deploy the tiers in order: each tier bundle depends on the populated tier
	// before it, the same edge the dependency-aware path wires per component.
	for i := 1; i < len(tierBundles); i++ {
		tierBundles[i].DependsOn = append(tierBundles[i].DependsOn, tierBundles[i-1])
	}

	// No Wait: kure gives an umbrella one health check per child Kustomization,
	// and Flux ignores health checks when wait is enabled.
	umbrella := &stack.Bundle{
		Name:     app.Metadata.Name,
		Children: tierBundles,
	}
	umbrella.InitializeUmbrella()
	if err := umbrella.Validate(); err != nil {
		return nil, &TransformError{Message: "failed to validate umbrella bundle", Cause: err}
	}

	rootNode := &stack.Node{Name: umbrella.Name, Bundle: umbrella}
	rootNode.InitializePathMap()
	return stack.NewCluster(ctx.ClusterID, rootNode), nil
}

// buildDependencyAwareCluster creates per-component bundles when explicit dependency
// policies are present. Each component gets its own Node and Bundle, enabling arbitrary
// DependsOn relationships.
func (t *Transformer) buildDependencyAwareCluster(app *Application, entries []componentEntry, deps map[string][]string, ctx TransformContext) (*stack.Cluster, error) {
	rootNode := &stack.Node{
		Name:     "",
		Children: make([]*stack.Node, 0, len(entries)),
	}

	bundleMap := make(map[string]*stack.Bundle, len(entries))
	tierBundles := make(map[Tier][]*stack.Bundle)

	for _, entry := range entries {
		bundleName := fmt.Sprintf("%s-%s", app.Metadata.Name, entry.component.Name)
		bundle, err := stack.NewBundle(bundleName, []*stack.Application{entry.app}, nil)
		if err != nil {
			return nil, &TransformError{Message: fmt.Sprintf("failed to create bundle for component %q", entry.component.Name), Cause: err}
		}

		if err := t.applyTraits(app, []componentEntry{entry}, bundle, ctx); err != nil {
			return nil, err
		}

		bundleMap[entry.component.Name] = bundle
		tierBundles[entry.tier] = append(tierBundles[entry.tier], bundle)

		childNode := &stack.Node{Name: entry.component.Name, Bundle: bundle}
		childNode.SetParent(rootNode)
		rootNode.Children = append(rootNode.Children, childNode)
	}

	// Wire explicit dependencies from policies.
	for component, depNames := range deps {
		bundle := bundleMap[component]
		for _, depName := range depNames {
			if depBundle := bundleMap[depName]; depBundle != nil {
				bundle.DependsOn = append(bundle.DependsOn, depBundle)
			}
		}
	}

	// Wire automatic cross-tier dependencies: each bundle depends on all bundles in the
	// immediately preceding populated tier.
	var prevTierBundles []*stack.Bundle
	for _, tier := range TierOrder {
		current := tierBundles[tier]
		if len(prevTierBundles) > 0 {
			for _, b := range current {
				for _, ptb := range prevTierBundles {
					if !slices.Contains(b.DependsOn, ptb) {
						b.DependsOn = append(b.DependsOn, ptb)
					}
				}
			}
		}
		if len(current) > 0 {
			prevTierBundles = current
		}
	}

	if err := detectBundleCycles(entries, bundleMap); err != nil {
		return nil, &TransformError{Message: "dependency cycle after applying cross-tier edges", Cause: err}
	}

	rootNode.InitializePathMap()
	return stack.NewCluster(ctx.ClusterID, rootNode), nil
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
		// For a sibling group: each trait-created sub-application's name and the
		// member type whose trait created it (see the check after Apply).
		var groupTraitApps map[string]string
		if len(e.members) > 0 {
			groupTraitApps = make(map[string]string)
		}
		subApps, err := t.applyEntryTraits(app, e, entries, groupTraitApps, bundle, ctx)
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

// checkEntryApplications fails when trait traitType of component component
// left a bundle entry's application out of the bundle, by pointer, or renamed
// it from names[i]. The Phase-4 automatic health check and NetworkPolicy
// synthesis find a component's application by its name and then its pointer,
// so a replaced, removed or renamed one would silently get neither
// (go-kure/launcher#734).
func checkEntryApplications(entries []componentEntry, names []string, bundle *stack.Bundle, component, traitType string) error {
	inBundle := make(map[*stack.Application]bool, len(bundle.Applications))
	for _, a := range bundle.Applications {
		inBundle[a] = true
	}
	for i, c := range entries {
		if !inBundle[c.app] {
			return &TransformError{Message: fmt.Sprintf(
				"component %q trait %q replaced or removed the application of component %q; %s",
				component, traitType, c.component.Name, entryAppContract)}
		}
		if c.app.Name != names[i] {
			return &TransformError{Message: fmt.Sprintf(
				"component %q trait %q renamed the application of component %q from %q to %q; %s",
				component, traitType, c.component.Name, names[i], c.app.Name, entryAppContract)}
		}
	}
	return nil
}

// applyEntryTraits applies the traits of one entry — each member's own, on that
// member's application and in authored order, for a collapsed sibling group
// (traitSteps). groupTraitApps is non-nil exactly for a group. It returns the
// sub-applications the entry's traits appended to the bundle, in creation order,
// and records each decorating trait (SubApplicationDecorator) with them in
// ctx.subAppDecorations.
//
// bundleEntries are the entries whose applications the bundle was built from.
// Each must still be in the bundle, by pointer and under the name it had when
// this entry's traits began, after every trait (checkEntryApplications).
func (t *Transformer) applyEntryTraits(app *Application, e componentEntry, bundleEntries []componentEntry, groupTraitApps map[string]string, bundle *stack.Bundle, ctx TransformContext) ([]*stack.Application, error) {
	var subApps []*stack.Application
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
	var entryNames []string
	if slices.ContainsFunc(steps, func(s traitStep) bool { return len(s.traits) > 0 }) {
		entryNames = make([]string, len(bundleEntries))
		for i, c := range bundleEntries {
			entryNames[i] = c.app.Name
		}
	}
	for _, step := range steps {
		entry := step.entry
		for _, trait := range step.traits {
			handler := t.findTraitHandler(trait.Type)
			if handler == nil {
				return nil, &TransformError{Message: fmt.Sprintf("no handler for trait type %q", trait.Type)}
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
			prev := slices.Clone(bundle.Applications)
			if err := handler.Apply(&resolved, entry.app, bundle); err != nil {
				return nil, &TransformError{
					Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
					Cause:   err,
				}
			}
			if err := checkEntryApplications(bundleEntries, entryNames, bundle, entry.component.Name, trait.Type); err != nil {
				return nil, err
			}

			added := addedApplications(prev, bundle.Applications)
			for _, newApp := range added {
				// Members share the group's name, so traits on two members that
				// derive a sub-application name from it (web-rbac, web-ingress)
				// would deploy one name twice.
				if groupTraitApps != nil {
					if prev, dup := groupTraitApps[newApp.Name]; dup && prev != entry.component.Type {
						return nil, &TransformError{Message: fmt.Sprintf(
							"sibling group %q: traits on members %q and %q both create sub-application %q; carry the trait on one member",
							entry.component.Name, prev, entry.component.Type, newApp.Name)}
					}
					groupTraitApps[newApp.Name] = entry.component.Type
				}
				if enforceable, ok := newApp.Config.(Enforceable); ok {
					if err := enforceable.ApplyPolicy(ctx.Policy); err != nil {
						return nil, &ViolationError{Component: entry.component.Name, Cause: err}
					}
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
					component: entry.component.Name, trait: resolved, handler: handler, bundle: bundle,
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
// by name: the automatic health check and NetworkPolicy synthesis have already
// named their objects after the applications (go-kure/launcher#734). The
// snapshot is a copy because a removal shifts the shared backing array in
// place.
func decorateSubApplications(decorations []subAppDecoration) error {
	for _, d := range decorations {
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
					"component %q trait %q changed the bundle's applications while decorating sub-application %q; a SubApplicationDecorator must not add, remove, replace, rename or reorder applications",
					d.component, d.trait.Type, subAppName)}
			}
		}
	}
	return nil
}

// --- Helpers ---

// deduplicateSourceRefs suppresses duplicate source CRD generation when multiple
// components share the same source key (URL for HelmRepository, URL+version for
// OCIRepository); the first component in the given order wins. Callers pass
// deploymentOrder's result so the owner never waits on another consumer.
func deduplicateSourceRefs(entries []componentEntry) {
	seen := make(map[string]string) // sourceKey → sourceRefName
	for _, entry := range entries {
		dedup, ok := entry.app.Config.(SourceDeduplicatable)
		if !ok {
			continue
		}
		key := dedup.GetSourceKey()
		if key == "" {
			continue
		}
		if existingName, found := seen[key]; found {
			dedup.SuppressSourceGeneration(existingName)
		} else {
			seen[key] = dedup.GetSourceRefName()
		}
	}
}

// deploymentOrder returns entries in an order every Flux dependency the cluster
// builders wire respects: a component follows the components it explicitly
// depends on and every component of an earlier tier. Ties keep document order,
// so an application with one tier and no dependencies comes back unchanged. On a
// dependency cycle it returns document order; the cluster builder reports the
// cycle.
func deploymentOrder(entries []componentEntry, deps map[string][]string) []componentEntry {
	pos := make(map[string]int, len(entries))
	for i, e := range entries {
		pos[e.component.Name] = i
	}
	rank := make(map[Tier]int, len(TierOrder))
	for i, tier := range TierOrder {
		rank[tier] = i
	}

	// Kahn's algorithm, always taking the earliest placeable entry in document order.
	pending := make([]int, len(entries)) // predecessors not yet placed
	successors := make([][]int, len(entries))
	for i, e := range entries {
		for _, name := range deps[e.component.Name] {
			if j, ok := pos[name]; ok {
				successors[j] = append(successors[j], i)
				pending[i]++
			}
		}
		for j, other := range entries {
			if rank[other.tier] < rank[e.tier] {
				successors[j] = append(successors[j], i)
				pending[i]++
			}
		}
	}

	ordered := make([]componentEntry, 0, len(entries))
	placed := make([]bool, len(entries))
	for len(ordered) < len(entries) {
		next := -1
		for i := range entries {
			if !placed[i] && pending[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			return entries
		}
		placed[next] = true
		ordered = append(ordered, entries[next])
		for _, s := range successors[next] {
			pending[s]--
		}
	}
	return ordered
}

// resolveCapability merges capability rendering into trait properties (rendering as
// defaults, OAM inline values win). An authored null, typed nil included, is absent
// (the null contract, isNullValue), so it does not displace a rendering value for its key
// (go-kure/launcher#742). Tries the scoped key, falls back to the bare
// type key. Returns (trait, "", false) on no match; otherwise (possibly merged
// trait, matched key, true) — a match with empty Rendering still counts as consumed.
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

	rendering, err := deepCopyMap(cap.Rendering)
	if err != nil {
		rendering = cap.Rendering
	}

	merged := make(map[string]any, len(rendering)+len(trait.Properties))
	maps.Copy(merged, rendering)
	for k, v := range trait.Properties {
		if _, rendered := rendering[k]; rendered && isNullValue(v) {
			continue
		}
		merged[k] = v
	}

	result := trait
	result.Properties = merged
	return result, matchedKey, true
}

// applyComponentCapabilityDefaults returns props with each key d lists that props
// leaves unauthored (absent, or a null as isNullValue reads it) taken from the rendering of the
// capability d names: the component counterpart of resolveCapability's "rendering
// as defaults, inline wins", restricted to the listed keys. The key is recorded as
// consumed when the profile binds it, whether or not a value was copied, as a trait's
// matched key is. props is never mutated: a copy is returned when a value is filled,
// and the filled values are deep copies, as resolveCapability's are, so no component
// shares a value with the profile.
func applyComponentCapabilityDefaults(d ComponentCapabilityDefaults, props map[string]any, ctx TransformContext) map[string]any {
	key, keys := d.CapabilityDefaults()
	binding, ok := ctx.Capabilities[key]
	if !ok {
		return props
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
		return props
	}
	if copied, err := deepCopyMap(fill); err == nil {
		fill = copied
	}
	out := maps.Clone(props)
	if out == nil {
		out = make(map[string]any, len(fill))
	}
	maps.Copy(out, fill)
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

// detectBundleCycles builds a name-keyed dependency graph from bundle DependsOn
// pointers and checks for cycles.
func detectBundleCycles(entries []componentEntry, bundleMap map[string]*stack.Bundle) error {
	bundleToName := make(map[*stack.Bundle]string, len(entries))
	for _, e := range entries {
		bundleToName[bundleMap[e.component.Name]] = e.component.Name
	}

	graph := make(map[string][]string)
	for _, e := range entries {
		bundle := bundleMap[e.component.Name]
		for _, dep := range bundle.DependsOn {
			if depName, ok := bundleToName[dep]; ok {
				graph[e.component.Name] = append(graph[e.component.Name], depName)
			}
		}
	}

	return detectCycles(graph)
}

// detectCycles checks for circular dependencies in a string-keyed graph using DFS.
func detectCycles(deps map[string][]string) error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int)

	var visit func(node string, path []string) error
	visit = func(node string, path []string) error {
		if state[node] == visited {
			return nil
		}
		if state[node] == visiting {
			return fmt.Errorf("circular dependency: %v -> %s", path, node)
		}
		state[node] = visiting
		path = append(path, node)
		for _, dep := range deps[node] {
			if err := visit(dep, path); err != nil {
				return err
			}
		}
		state[node] = visited
		return nil
	}

	for node := range deps {
		if state[node] == unvisited {
			if err := visit(node, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// componentHealthCheckGVK maps OAM component types to their primary workload GVK.
// Types not listed are skipped.
//
// `job` is listed and `cronjob` is not, and the difference is not that one is
// ephemeral: it is that a Job reaches a terminal state a health check can wait
// on and a CronJob never does. kstatus reads a Job's `Complete` condition as
// Current and its `Failed` condition as failed (fluxcd/cli-utils
// pkg/kstatus/status/core.go, jobConditions), so a Kustomization that depends on
// a migration job waits for the migration to finish and fails when it fails —
// which is the whole point of declaring the dependency. A CronJob owns no pods
// between schedules and has no such condition, so a check on one would wait for
// something that never arrives.
//
// The obvious objection — that a job with ttlSecondsAfterFinished is garbage
// collected and the health check then names a missing object — does not hold:
// kustomize-controller extracts jobs carrying a TTL and passes them to the
// waiter as JobsWithTTL so their disappearance is not a failure
// (fluxcd/kustomize-controller, checkHealth). The case that does need handling
// is `suspend: true`, and JobConfig vetoes its own check there via
// autoHealthCheckEmitter, exactly as deployment does for `paused: true`.
// HelmReleaseConfig vetoes for its own `suspend: true` the same way: a
// suspended HelmRelease is not reconciled, so its Ready condition cannot report
// on it.
//
// The kind-named Flux source components (go-kure/launcher#347, and helmchart,
// go-kure/launcher#351) are listed: each
// emits exactly one source CR whose Ready condition kstatus reads, so a
// Kustomization that depends on a source waits until the source is ready. Each
// vetoes its check for `suspend: true`, the same shape as job's veto: the
// document tells source-controller not to reconcile. helmrepository also vetoes
// for `type: oci`, which Flux treats as a static object with no artifact, so
// there is no reconcile to wait on. The GVK is a *.toolkit.fluxcd.io kind, so
// the check follows the CR to the Flux namespace when one is set.
//
// cnpg-pooler, cnpg-database and cnpg-objectstore (go-kure/launcher#573) are
// not listed: the Pooler, Database and ObjectStore statuses carry no condition
// kstatus reads, so a check would report Current without waiting on anything.
// postgresql, which emits the same kinds, checks only its Cluster.
var componentHealthCheckGVK = map[string]struct{ APIVersion, Kind string }{
	"webservice":   {"apps/v1", "Deployment"},
	"worker":       {"apps/v1", "Deployment"},
	"deployment":   {"apps/v1", "Deployment"},
	"statefulset":  {"apps/v1", "StatefulSet"},
	"daemonset":    {"apps/v1", "DaemonSet"},
	"job":          {"batch/v1", "Job"},
	"helmrelease":  {"helm.toolkit.fluxcd.io/v2", "HelmRelease"},
	"postgresql":   {"postgresql.cnpg.io/v1", "Cluster"},
	"cnpg-cluster": {"postgresql.cnpg.io/v1", "Cluster"},
	"oci":          {"kustomize.toolkit.fluxcd.io/v1", "Kustomization"},

	"helmrepository": {"source.toolkit.fluxcd.io/v1", "HelmRepository"},
	"ocirepository":  {"source.toolkit.fluxcd.io/v1", "OCIRepository"},
	"gitrepository":  {"source.toolkit.fluxcd.io/v1", "GitRepository"},
	"bucket":         {"source.toolkit.fluxcd.io/v1", "Bucket"},
	"helmchart":      {"source.toolkit.fluxcd.io/v1", "HelmChart"},
}

// postProcessFluxNamespace walks all leaf bundle applications and calls
// SetFluxNamespace on any config that satisfies fluxNamespaceSettable, then
// moves the trait sub-applications those Flux objects read from their own
// namespace (moveFluxNamespaceInputs).
func postProcessFluxNamespace(cluster *stack.Cluster, owned []traitSubApps, ns string) {
	if cluster == nil || ns == "" {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			if setter, ok := app.Config.(fluxNamespaceSettable); ok {
				setter.SetFluxNamespace(ns)
			}
		}
	})
	moveFluxNamespaceInputs(owned, ns)
}

// isFluxControlPlaneGVK reports whether an auto health-check GVK targets a Flux
// control-plane CR (a *.toolkit.fluxcd.io kind, e.g. HelmRelease) that
// postProcessFluxNamespace relocates to the flux namespace. Workload kinds
// (Deployment/StatefulSet/…) and app-namespace CRs (CNPG Cluster) stay in the
// app namespace even when their config is wrapped by a fluxNamespaceSettable
// decorator, so the namespace switch must be gated on this.
func isFluxControlPlaneGVK(apiVersion string) bool {
	group, _, _ := strings.Cut(apiVersion, "/")
	return strings.HasSuffix(group, ".toolkit.fluxcd.io")
}

// applyAutoHealthChecks walks all leaf bundles and appends inferred health check
// references based on each component's type, followed by any explicit overrides.
//
// The synthesized check's namespace must point at the namespace where the
// referenced object actually lands. For Flux-CR configs (helmrelease →
// HelmRelease) the object is relocated to the flux namespace by
// postProcessFluxNamespace, so the check must carry the same flux namespace —
// this mirrors that function's predicate exactly (fluxNamespaceSettable +
// non-empty fluxNamespace) so the check always follows its object. Configs that
// veto their check (helmrelease with suspend: true) are skipped via
// autoHealthCheckEmitter.
func applyAutoHealthChecks(cluster *stack.Cluster, componentMap map[string]componentEntry, overrides []stack.HealthCheck, fluxNamespace string) {
	if cluster == nil {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			// Match the component's own application by identity, not by name: a
			// trait's sub-application (a `pvc` trait's claim `<component>-<volume>`)
			// can share its name with another component, and must not get that
			// component's check (go-kure/launcher#702).
			entry, ok := componentMap[app.Name]
			if !ok || app != entry.app {
				continue
			}
			gvk, ok := componentHealthCheckGVK[entry.component.Type]
			if !ok {
				continue
			}
			// Skip when the config vetoes its check (autoHealthCheckEmitter).
			if e, ok := app.Config.(autoHealthCheckEmitter); ok && !e.EmitsAutoHealthCheck() {
				continue
			}
			// Mirror postProcessFluxNamespace: a config that re-stamps the flux
			// namespace on its object emits that object in the flux namespace, so
			// its health check must reference the flux namespace too. Gate on the
			// target being a Flux control-plane CR (e.g. HelmRelease): wrappers
			// (configmap/prune-protection) implement fluxNamespaceSettable even
			// when wrapping a workload whose Deployment stays in the app namespace,
			// so the settable check alone is too broad.
			// A sibling group's config takes the namespace for every member, so
			// whether the checked object follows it is the primary member's answer:
			// the check names the primary's kind.
			ns := app.Namespace
			if fluxNamespace != "" && isFluxControlPlaneGVK(gvk.APIVersion) {
				if _, settable := entry.healthCheckConfig().(fluxNamespaceSettable); settable {
					ns = fluxNamespace
				}
			}
			bundle.HealthChecks = append(bundle.HealthChecks, stack.HealthCheck{
				APIVersion: gvk.APIVersion,
				Kind:       gvk.Kind,
				Name:       app.Name,
				Namespace:  ns,
			})
		}
		bundle.HealthChecks = append(bundle.HealthChecks, overrides...)
	})
}

// applyReconciliationSettings applies Flux reconciliation overrides from a
// reconciliation policy to all leaf bundles.
func applyReconciliationSettings(cluster *stack.Cluster, _ map[string]componentEntry, settings *ReconciliationSettings) {
	if cluster == nil || settings == nil {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		if settings.Interval != "" {
			bundle.Interval = settings.Interval
		}
		if settings.RetryInterval != "" {
			bundle.RetryInterval = settings.RetryInterval
		}
		if settings.Timeout != "" {
			bundle.Timeout = settings.Timeout
		}
		if settings.Prune != nil {
			bundle.Prune = settings.Prune
		}
		if settings.Wait != nil {
			bundle.Wait = settings.Wait
		}
		if settings.Force != nil {
			bundle.Force = settings.Force
		}
		if settings.Suspend != nil {
			bundle.Suspend = settings.Suspend
		}
	})
}

// walkLeafBundles calls fn for every leaf bundle reachable from node.
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
				return fmt.Errorf("capability %q is forbidden by environment policy", t)
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
				return fmt.Errorf("capability %q is not in the allowed list", t)
			}
		}
	}

	for _, r := range required {
		if !used[r] {
			return fmt.Errorf("required capability %q is missing", r)
		}
	}

	return nil
}

// deepCopyMap returns a deep copy of a map[string]any via JSON round-trip.
func deepCopyMap(src map[string]any) (map[string]any, error) {
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var dst map[string]any
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil, err
	}
	return dst, nil
}
