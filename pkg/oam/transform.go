package oam

// ValidateAndApplyDefaults is implemented by built-in TraitHandlers that accept
// rendering keys from ClusterProfile. Called at ClusterProfile evaluation time,
// before any Application is processed. See design-capability-schema.md §2.2.
type ValidateAndApplyDefaults interface {
	ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error)
}

// TransformContext carries per-transform state passed to the pipeline.
type TransformContext struct {
	ClusterID    string
	TenantID     string
	Environment  string
	AppVersion   string
	TeamID       string
	Namespace    string // overrides OAM metadata.namespace when set
	Policy       Policy
	Capabilities map[string]CapabilityBinding
}

// Transformer is the core OAM runtime. Handlers are registered at startup;
// Transform/TransformWithPolicy (added in #53) execute the pipeline.
// Internal storage uses maps keyed by typeName for O(1) dispatch.
type Transformer struct {
	componentHandlers map[string]ComponentHandler
	traitHandlers     map[string]TraitHandler
	policyHandlers    map[string]PolicyHandler
}

// NewTransformer creates a Transformer pre-loaded with component and trait handlers.
// Inputs are maps keyed by typeName — the same key used by Register* and find* methods.
// Each entry is routed through RegisterComponent/RegisterTrait so the startup assertion
// (design-capability-schema.md §2.5) applies to pre-loaded handlers too.
// Panics if any trait handler implements CapabilityAware but not ValidateAndApplyDefaults.
// Nil maps are treated as empty.
func NewTransformer(componentHandlers map[string]ComponentHandler, traitHandlers map[string]TraitHandler) *Transformer {
	t := &Transformer{
		componentHandlers: make(map[string]ComponentHandler),
		traitHandlers:     make(map[string]TraitHandler),
		policyHandlers:    make(map[string]PolicyHandler),
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

func (t *Transformer) findComponentHandler(componentType string) ComponentHandler {
	return t.componentHandlers[componentType]
}

func (t *Transformer) findTraitHandler(traitType string) TraitHandler {
	return t.traitHandlers[traitType]
}

func (t *Transformer) findPolicyHandler(policyType string) PolicyHandler {
	return t.policyHandlers[policyType]
}
