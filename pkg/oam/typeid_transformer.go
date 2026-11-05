package oam

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// typeIndex is the identity side of one position (components, traits or
// policies) of a Transformer: the registry of declared identities, and the plain
// type name each one is registered under. The handler and the rule stay in the
// Transformer maps under that name; an authored reference that is an identity
// is resolved to the name before anything dispatches on it.
type typeIndex struct {
	reg   *TypeRegistry
	keys  map[TypeID]string
	byKey map[string]TypeID
}

func newTypeIndex() *typeIndex {
	return &typeIndex{reg: NewTypeRegistry(), keys: map[TypeID]string{}, byKey: map[string]TypeID{}}
}

// with returns a copy of ix (a nil ix is empty) that also declares ids, keyed by
// plain type name, or the first refusal. ix itself is never changed, so a refused
// declaration leaves the Transformer as it was. registered says whether a
// handler or a rule is registered under a name.
func (ix *typeIndex) with(position string, ids map[string]TypeID, registered func(string) bool) (*typeIndex, error) {
	out := newTypeIndex()
	if ix != nil {
		for _, key := range sortedTypeKeys(ix.byKey) {
			if err := out.add(key, ix.byKey[key]); err != nil {
				return nil, err
			}
		}
	}
	for _, key := range sortedTypeKeys(ids) {
		if !registered(key) {
			return nil, fmt.Errorf("%w: no %s handler or lowering rule is registered for %q", ErrTypeRegistration, position, key)
		}
		if prev, dup := out.byKey[key]; dup {
			return nil, fmt.Errorf("%w: %s type %q is already declared as %s", ErrTypeRegistration, position, key, prev)
		}
		if err := out.add(key, ids[key]); err != nil {
			return nil, fmt.Errorf("%s type %q: %w", position, key, err)
		}
	}
	return out, nil
}

func (ix *typeIndex) add(key string, id TypeID) error {
	if err := ix.reg.Register(id); err != nil {
		return err
	}
	ix.keys[id] = key
	ix.byKey[key] = id
	return nil
}

func sortedTypeKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// resolve resolves ref to a declared identity. A nil index declares nothing.
func (ix *typeIndex) resolve(ref string) (TypeID, error) {
	if ix == nil {
		return TypeID{}, fmt.Errorf("%w %q: no types are declared", ErrTypeReferenceUnknown, ref)
	}
	return ix.reg.Resolve(ref)
}

func (t *Transformer) isComponentKey(name string) bool {
	_, h := t.componentHandlers[name]
	_, r := t.componentLoweringRules[name]
	return h || r
}

func (t *Transformer) isTraitKey(name string) bool {
	_, h := t.traitHandlers[name]
	_, r := t.traitLoweringRules[name]
	return h || r
}

func (t *Transformer) isPolicyKey(name string) bool {
	_, h := t.policyHandlers[name]
	_, r := t.policyLoweringRules[name]
	return h || r
}

// DeclareComponentTypes declares the identity of component types already
// registered, a handler or a lowering rule, keyed by the plain type name each is
// registered under. registry.ComponentTypeIDs returns the built-ins'. It is
// refused as a whole, and the Transformer left as it was, for a name with no
// handler or rule, a name declared twice, an invalid identity, or one that
// conflicts with another (a version declared twice, a short name that equals
// another type's qualified name).
func (t *Transformer) DeclareComponentTypes(ids map[string]TypeID) error {
	ix, err := t.componentTypes.with("component", ids, t.isComponentKey)
	if err != nil {
		return err
	}
	t.componentTypes = ix
	return nil
}

// DeclareTraitTypes is DeclareComponentTypes for trait types.
func (t *Transformer) DeclareTraitTypes(ids map[string]TypeID) error {
	ix, err := t.traitTypes.with("trait", ids, t.isTraitKey)
	if err != nil {
		return err
	}
	t.traitTypes = ix
	return nil
}

// DeclarePolicyTypes is DeclareComponentTypes for policy types.
func (t *Transformer) DeclarePolicyTypes(ids map[string]TypeID) error {
	ix, err := t.policyTypes.with("policy", ids, t.isPolicyKey)
	if err != nil {
		return err
	}
	t.policyTypes = ix
	return nil
}

// ResolveComponentType resolves an authored component type, ref, to the declared
// identity it names: `name`, or `name@version`, with a short or a qualified name
// (README.md "Type identity"). The result carries the exact version resolved to.
func (t *Transformer) ResolveComponentType(ref string) (TypeID, error) {
	return t.componentTypes.resolve(ref)
}

// ResolveTraitType is ResolveComponentType for trait types.
func (t *Transformer) ResolveTraitType(ref string) (TypeID, error) {
	return t.traitTypes.resolve(ref)
}

// ResolvePolicyType is ResolveComponentType for policy types.
func (t *Transformer) ResolvePolicyType(ref string) (TypeID, error) {
	return t.policyTypes.resolve(ref)
}

// componentKey returns the plain type name an authored component type is
// registered under, and whether there is one. A plain name registered as it is
// stays itself, so a type nobody declared an identity for behaves as it always
// did.
func (t *Transformer) componentKey(ref string) (string, bool) {
	if t.isComponentKey(ref) {
		return ref, true
	}
	id, err := t.componentTypes.resolve(ref)
	if err != nil {
		return "", false
	}
	return t.componentTypes.keys[id], true
}

// ComponentHandlerFor returns the handler of the component type ref names, an
// authored plain name or identity (ResolveComponentType), and false when the
// type has none: unknown, ambiguous, or one a lowering rule claims.
func (t *Transformer) ComponentHandlerFor(ref string) (ComponentHandler, bool) {
	key, ok := t.componentKey(ref)
	if !ok {
		return nil, false
	}
	h, ok := t.componentHandlers[key]
	return h, ok
}

// ComponentLoweringRuleFor returns the lowering rule of the component type ref
// names, and false when the type has none. A type is a handler or a rule, never
// both, so exactly one of this and ComponentHandlerFor answers for a type.
func (t *Transformer) ComponentLoweringRuleFor(ref string) (ComponentLoweringRule, bool) {
	key, ok := t.componentKey(ref)
	if !ok {
		return nil, false
	}
	r, ok := t.componentLoweringRules[key]
	return r, ok
}

// resolveRef returns the plain type name an authored reference is registered
// under, and whether that is a change. A plain name registered as it is, an
// empty type, and a plain unknown name pass through unchanged, so the refusal the
// later stage gives them (the allowlist, "no handler") stays what it was. A
// reference with a version, which can only be an identity, that does not resolve
// is refused here in the resolver's words, as is an ambiguous one.
func resolveRef(ix *typeIndex, registered func(string) bool, ref string) (string, bool, error) {
	if ref == "" || registered(ref) {
		return ref, false, nil
	}
	id, err := ix.resolve(ref)
	if err != nil {
		if errors.Is(err, ErrTypeReferenceUnknown) && !strings.Contains(ref, "@") {
			return ref, false, nil
		}
		return "", false, err
	}
	key := ix.keys[id]
	return key, key != ref, nil
}

// resolveTypes returns app with every authored type reference resolved to the
// plain type name its handler or rule is registered under, so that the engine
// dispatches on one name. It returns app itself, not a copy, when nothing needs
// resolving, and always when no type identity was declared; otherwise a copy that
// shares everything but the types it rewrote, and never changes app.
func (t *Transformer) resolveTypes(app *Application) (*Application, error) {
	if t.componentTypes == nil && t.traitTypes == nil && t.policyTypes == nil {
		return app, nil
	}
	var out *Application
	own := func() {
		if out == nil {
			cp := *app
			cp.Spec.Components = slices.Clone(app.Spec.Components)
			cp.Spec.Policies = slices.Clone(app.Spec.Policies)
			out = &cp
		}
	}
	for i := range app.Spec.Components {
		c := &app.Spec.Components[i]
		key, changed, err := resolveRef(t.componentTypes, t.isComponentKey, c.Type)
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", c.Name, err)
		}
		if changed {
			own()
			out.Spec.Components[i].Type = key
		}
		for j := range c.Traits {
			key, changed, err := resolveRef(t.traitTypes, t.isTraitKey, c.Traits[j].Type)
			if err != nil {
				return nil, fmt.Errorf("component %q trait[%d]: %w", c.Name, j, err)
			}
			if changed {
				own()
				traits := slices.Clone(out.Spec.Components[i].Traits)
				traits[j].Type = key
				out.Spec.Components[i].Traits = traits
			}
		}
	}
	for i := range app.Spec.Policies {
		p := &app.Spec.Policies[i]
		key, changed, err := resolveRef(t.policyTypes, t.isPolicyKey, p.Type)
		if err != nil {
			return nil, fmt.Errorf("policy %q: %w", p.Name, err)
		}
		if changed {
			own()
			out.Spec.Policies[i].Type = key
		}
	}
	if out == nil {
		return app, nil
	}
	return out, nil
}

// ResolveTypes is the type resolution Transform and TransformWithPolicy run
// first: app with every component, trait and policy type resolved to the plain
// name it is registered under, as a copy when anything changed. A caller that
// reads Component.Type itself, rather than going through a handler lookup,
// resolves first.
func (t *Transformer) ResolveTypes(app *Application) (*Application, error) {
	return t.resolveTypes(app)
}
