package oam

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/stack"

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
	Namespace     string // overrides OAM metadata.namespace when set
	FluxNamespace string // Flux control-plane namespace; "" means use component namespace
	Policy        Policy
	Capabilities  map[string]CapabilityBinding
	// EgressPeers carries crane-supplied, graph-derived egress destinations keyed
	// by OAM component name. Non-authorable: never sourced from OAM YAML or
	// capability rendering, and never merged into trait properties. nil on the
	// kurel path, where egress synthesis is a no-op.
	EgressPeers map[string][]netpol.EgressPeer
}

// fluxNamespaceSettable is implemented by ApplicationConfig types that emit
// Flux CRDs (HelmRelease, HelmRepository, OCIRepository) and support per-request
// namespace re-stamping. Decorators that wrap such configs must also implement
// this interface and forward the call.
type fluxNamespaceSettable interface {
	SetFluxNamespace(string)
}

// autoHealthCheckEmitter is implemented by ApplicationConfig types whose
// auto health-check (from componentHealthCheckGVK) is only valid when the
// config actually emits the referenced object. Helmchart returns false for
// delivery=template (it renders manifests and emits no HelmRelease), so no
// HelmRelease health check should be synthesized. Decorators that wrap such
// configs must forward this call (mirroring fluxNamespaceSettable). Configs
// that do not implement it are assumed to emit their object.
type autoHealthCheckEmitter interface {
	EmitsAutoHealthCheck() bool
}

// Transformer is the core OAM runtime. Handlers are registered at startup;
// Transform/TransformWithPolicy (added in #53) execute the pipeline.
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
		componentHandlers: make(map[string]ComponentHandler),
		traitHandlers:     make(map[string]TraitHandler),
		policyHandlers:    make(map[string]PolicyHandler),
		builtinTraitTypes: make(map[string]bool),
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
// keyed by handler type name. Component and trait schemas are kept separate so a
// component and a trait that share a type name do not collide, and so consumers
// (crane's validator) know which registry a schema came from.
type HandlerSchemaSet struct {
	Components map[string]map[string]PropertySchema
	Traits     map[string]map[string]PropertySchema
}

// HandlerSchemas returns the property schemas of every registered component and
// trait handler that implements PropertySchemaProvider. Handlers that do not
// implement it are omitted. The maps are always non-nil.
func (t *Transformer) HandlerSchemas() HandlerSchemaSet {
	set := HandlerSchemaSet{
		Components: make(map[string]map[string]PropertySchema),
		Traits:     make(map[string]map[string]PropertySchema),
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
	return set
}

// RegisterPolicy registers a policy handler under the given type name.
// Panics if typeName is already registered or if h.CanHandle(typeName) returns false.
func (t *Transformer) RegisterPolicy(typeName string, h PolicyHandler) {
	if _, exists := t.policyHandlers[typeName]; exists {
		panic("oam: policy handler already registered for type " + typeName)
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

// SetWarningHandler sets the callback invoked when a non-fatal capability warning is emitted.
// If nil, warnings are silently dropped.
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

	// Phase 1.5: validate capability constraints declared by the Policy.
	if err := enforceCapabilityConstraints(collectTraitTypes(app), ctx.Policy); err != nil {
		return nil, nil, &ViolationError{Component: app.Metadata.Name, Cause: err}
	}

	// Phase 2: apply OAM policies (placement overrides, dependency graph).
	policyResult, err := t.applyPolicies(app, entries)
	if err != nil {
		return nil, nil, err
	}

	// Apply placement tier overrides before grouping.
	for i, entry := range entries {
		if tier, ok := policyResult.TierOverrides[entry.component.Name]; ok {
			entries[i].tier = tier
		}
	}

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

	// Phase 4: post-build bundle decorations.
	componentMap := make(map[string]componentEntry, len(entries))
	for _, e := range entries {
		componentMap[e.component.Name] = e
	}
	applyAutoHealthChecks(cluster, componentMap, policyResult.HealthCheckOverrides, ctx.FluxNamespace)
	applyReconciliationSettings(cluster, componentMap, policyResult.ReconciliationSettings)
	synthesizeNetworkPolicies(cluster)
	synthesizeEgressNetworkPolicies(cluster, componentMap, ctx.EgressPeers)
	postProcessFluxNamespace(cluster, ctx.FluxNamespace)

	return cluster, policyResult, nil
}

// createApplications converts OAM components to stack applications, applies
// Enforceable policy, and classifies each component into a deployment tier.
func (t *Transformer) createApplications(app *Application, namespace string, ctx TransformContext) ([]componentEntry, error) {
	entries := make([]componentEntry, 0, len(app.Spec.Components))
	for i, component := range app.Spec.Components {
		handler := t.findComponentHandler(component.Type)
		if handler == nil {
			return nil, &TransformError{Message: fmt.Sprintf("no handler for component type %q", component.Type)}
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

		tier, err := ClassifyComponent(&component)
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

	deduplicateSourceRefs(entries)
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

	waitTrue := true
	umbrella := &stack.Bundle{
		Name:     app.Metadata.Name,
		Children: tierBundles,
		Wait:     &waitTrue,
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
func (t *Transformer) applyTraits(app *Application, entries []componentEntry, bundle *stack.Bundle, ctx TransformContext) error {
	for _, entry := range entries {
		for _, trait := range app.Spec.Components[entry.index].Traits {
			handler := t.findTraitHandler(trait.Type)
			if handler == nil {
				return &TransformError{Message: fmt.Sprintf("no handler for trait type %q", trait.Type)}
			}

			if aware, ok := handler.(CapabilityAware); ok && aware.CapabilityRequired() {
				key := buildCapabilityKey(trait)
				_, foundScoped := ctx.Capabilities[key]
				_, foundBare := ctx.Capabilities[trait.Type]
				if !foundScoped && !foundBare {
					return &TransformError{
						Message: fmt.Sprintf("component %q trait %q: capability %q not found in ClusterProfile",
							entry.component.Name, trait.Type, key),
						Cause: ErrMissingCapability,
					}
				}
			}

			// For custom (non-built-in) traits whose capability rendering resolved in the
			// profile, warn or error when no CapabilityDefinition was loaded for the type.
			if !t.builtinTraitTypes[trait.Type] {
				key := buildCapabilityKey(trait)
				_, foundScoped := ctx.Capabilities[key]
				_, foundBare := ctx.Capabilities[trait.Type]
				if foundScoped || foundBare {
					if _, hasDef := t.capabilityDefs[trait.Type]; !hasDef {
						msg := fmt.Sprintf("no CapabilityDefinition found for custom trait %q", trait.Type)
						if t.strictCapabilities {
							return &TransformError{Message: msg}
						}
						if t.warnHandler != nil {
							t.warnHandler(msg)
						}
					}
				}
			}

			resolved := resolveCapability(trait, ctx.Capabilities)
			prevLen := len(bundle.Applications)
			if err := handler.Apply(&resolved, entry.app, bundle); err != nil {
				return &TransformError{
					Message: fmt.Sprintf("component %q trait %q", entry.component.Name, trait.Type),
					Cause:   err,
				}
			}

			for _, newApp := range bundle.Applications[prevLen:] {
				if enforceable, ok := newApp.Config.(Enforceable); ok {
					if err := enforceable.ApplyPolicy(ctx.Policy); err != nil {
						return &ViolationError{Component: entry.component.Name, Cause: err}
					}
				}
			}
		}
	}
	return nil
}

// --- Helpers ---

// deduplicateSourceRefs suppresses duplicate source CRD generation when multiple
// components share the same source key (URL for HelmRepository, URL+version for
// OCIRepository); first component wins.
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

// resolveCapability merges capability rendering values into trait properties.
// Rendering values act as platform-provided defaults; OAM inline values take precedence.
// Key resolution: tries the scoped key ("<type>.<scope>"), then falls back to the bare
// "<type>" key. Returns the original trait unchanged when no matching capability exists.
func resolveCapability(trait Trait, capabilities map[string]CapabilityBinding) Trait {
	if len(capabilities) == 0 {
		return trait
	}
	key := buildCapabilityKey(trait)
	cap, ok := capabilities[key]
	if !ok {
		cap, ok = capabilities[trait.Type]
	}
	if !ok || len(cap.Rendering) == 0 {
		return trait
	}

	rendering, err := deepCopyMap(cap.Rendering)
	if err != nil {
		rendering = cap.Rendering
	}

	merged := make(map[string]any, len(rendering)+len(trait.Properties))
	maps.Copy(merged, rendering)
	maps.Copy(merged, trait.Properties)

	result := trait
	result.Properties = merged
	return result
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
// Types not listed (e.g. cronjob) are skipped — their resources are ephemeral.
var componentHealthCheckGVK = map[string]struct{ APIVersion, Kind string }{
	"webservice":  {"apps/v1", "Deployment"},
	"worker":      {"apps/v1", "Deployment"},
	"statefulset": {"apps/v1", "StatefulSet"},
	"daemonset":   {"apps/v1", "DaemonSet"},
	"helmchart":   {"helm.toolkit.fluxcd.io/v2", "HelmRelease"},
	"postgresql":  {"postgresql.cnpg.io/v1", "Cluster"},
	"oci":         {"kustomize.toolkit.fluxcd.io/v1", "Kustomization"},
}

// postProcessFluxNamespace walks all leaf bundle applications and calls
// SetFluxNamespace on any config that satisfies fluxNamespaceSettable.
func postProcessFluxNamespace(cluster *stack.Cluster, ns string) {
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
// referenced object actually lands. For Flux-CR configs (helmchart →
// HelmRelease) the object is relocated to the flux namespace by
// postProcessFluxNamespace, so the check must carry the same flux namespace —
// this mirrors that function's predicate exactly (fluxNamespaceSettable +
// non-empty fluxNamespace) so the check always follows its object. Configs that
// will not emit the referenced object (helmchart delivery=template) are skipped
// via autoHealthCheckEmitter.
func applyAutoHealthChecks(cluster *stack.Cluster, componentMap map[string]componentEntry, overrides []stack.HealthCheck, fluxNamespace string) {
	if cluster == nil {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, app := range bundle.Applications {
			entry, ok := componentMap[app.Name]
			if !ok {
				continue
			}
			gvk, ok := componentHealthCheckGVK[entry.component.Type]
			if !ok {
				continue
			}
			// Skip when the config will not emit the referenced object
			// (e.g. helmchart delivery=template emits manifests, no HelmRelease).
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
			ns := app.Namespace
			if fluxNamespace != "" && isFluxControlPlaneGVK(gvk.APIVersion) {
				if _, settable := app.Config.(fluxNamespaceSettable); settable {
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
