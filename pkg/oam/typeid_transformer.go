package oam

import (
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
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
			return nil, errors.Errorf("%w: no %s handler or lowering rule is registered for %q", ErrTypeRegistration, position, key)
		}
		if prev, dup := out.byKey[key]; dup {
			return nil, errors.Errorf("%w: %s type %q is already declared as %s", ErrTypeRegistration, position, key, prev)
		}
		if err := out.add(key, ids[key]); err != nil {
			return nil, errors.Errorf("%s type %q: %w", position, key, err)
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

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func sortedTypeKeys[V any](m map[string]V) []string {
	keys := mapKeys(m)
	slices.Sort(keys)
	return keys
}

// declares reports whether any type is declared in this position. A nil index, and
// one built from an empty declaration, declare nothing: the position behaves as it
// did before type identities, a type is its plain registered name.
func (ix *typeIndex) declares() bool {
	return ix != nil && len(ix.byKey) > 0
}

// resolve resolves ref to a declared identity. A nil index declares nothing.
func (ix *typeIndex) resolve(ref string) (TypeID, error) {
	if ix == nil {
		return TypeID{}, errors.Errorf("%w %q: no types are declared", ErrTypeReferenceUnknown, ref)
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

// CheckComponentTypes returns the refusal DeclareComponentTypes would give ids, and nil when it
// would take them, for names whose handler or rule is not registered yet. It declares nothing.
func (t *Transformer) CheckComponentTypes(ids map[string]TypeID) error {
	_, err := t.componentTypes.with("component", ids, func(string) bool { return true })
	return err
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
// registered under, and whether there is one. A declared identity or short name
// resolves first, so a name that more than one type or version claims has no key
// even when a handler is registered under it. A plain name nobody declared an
// identity for, registered as it is, stays itself and behaves as it always did.
func (t *Transformer) componentKey(ref string) (string, bool) {
	id, err := t.componentTypes.resolve(ref)
	if err == nil {
		return t.componentTypes.keys[id], true
	}
	if errors.Is(err, ErrTypeReferenceAmbiguous) {
		return "", false
	}
	// Where a type is declared, a reference with a version can only be an identity,
	// and Transform refuses one that does not resolve, so it never falls back to a
	// handler registered under that text. Where nothing is declared a type is its
	// registered name, `@` included, and Transform dispatches on it as it always did.
	if t.isComponentKey(ref) && (!t.componentTypes.declares() || !strings.Contains(ref, "@")) {
		return ref, true
	}
	return "", false
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

// typeForm is the text a resolved type reference is rewritten to.
type typeForm int

const (
	// byKey is the plain name the handler or rule is registered under: what the
	// engine dispatches on.
	byKey typeForm = iota
	// byIdentity is the declared identity, `name@version`: text Transform resolves
	// again to the same type, so a result fed back to it still resolves.
	byIdentity
)

// resolveRef returns the text an authored reference is rewritten to (the plain
// name it is registered under, or its identity, per form), and whether that is a
// change. A reference that resolves is resolved
// first, so a name more than one type or version claims is refused as ambiguous
// even when a handler is registered under it. An empty type, any type in a position
// where nothing is declared (its registered name stays what it is, `@` included),
// and a plain name, one
// without a version, that no declared identity names or that is not a valid
// identity (a custom trait such as custom_metrics, or an unknown one) pass through
// unchanged, so the refusal the later stage gives them (the allowlist, "no handler")
// stays what it was. A reference with a version, which can only be an identity,
// that does not resolve is refused here in the resolver's words.
func resolveRef(ix *typeIndex, ref string, form typeForm) (string, bool, error) {
	if ref == "" || !ix.declares() {
		return ref, false, nil
	}
	id, err := ix.resolve(ref)
	if err != nil {
		plain := !strings.Contains(ref, "@")
		if plain && (errors.Is(err, ErrTypeReferenceUnknown) || errors.Is(err, ErrTypeIdentityInvalid)) {
			return ref, false, nil
		}
		return "", false, err
	}
	out := ix.keys[id]
	if form == byIdentity {
		out = id.String()
	}
	return out, out != ref, nil
}

// typeIdentities is what resolving an application's authored type references
// reads: the declared identities of each position. A Transformer reads its own
// (live), and LowerableTypes carries a snapshot of them to Parse, which has no
// Transformer.
type typeIdentities struct {
	components, traits, policies *typeIndex
	// keep, when set, names a reference that stays as written: a plain name a handler or a
	// rule is registered under, per position. Resolving what a lowering rule emitted sets it,
	// because dispatch has always taken such a name as it stands, where an authored reference
	// is refused when more than one declared type claims it.
	keep *keptTypes
}

// keptTypes says, for each position, whether a reference is registered as it is written.
type keptTypes struct{ component, trait, policy func(string) bool }

func (t *Transformer) identities() *typeIdentities {
	return &typeIdentities{components: t.componentTypes, traits: t.traitTypes, policies: t.policyTypes}
}

// snapshotIdentities is identities frozen at this call: a type declared on t later
// does not reach the result (a declaration replaces the index, it does not change
// it). It is nil when nothing is declared.
func (t *Transformer) snapshotIdentities() *typeIdentities {
	if t.componentTypes == nil && t.traitTypes == nil && t.policyTypes == nil {
		return nil
	}
	return t.identities()
}

// resolveTypes returns app with every authored type reference resolved to the
// plain type name its handler or rule is registered under, so that the engine
// dispatches on one name. It returns app itself, not a copy, when nothing needs
// resolving, and always when no type identity was declared; otherwise a copy that
// shares everything but the types it rewrote, and never changes app.
func (t *Transformer) resolveTypes(app *Application) (*Application, error) {
	return t.identities().resolve(app, byKey)
}

// resolveEmitted is resolveTypes for the output of a lowering round: a plain name that a
// handler or a rule is registered under stays as written, so only an identity a rule wrote
// changes, and a name that dispatch took before still dispatches.
func (t *Transformer) resolveEmitted(app *Application) (*Application, error) {
	ids := t.identities()
	ids.keep = &keptTypes{component: t.isComponentKey, trait: t.isTraitKey, policy: t.isPolicyKey}
	return ids.resolve(app, byKey)
}

func (ids *typeIdentities) resolve(app *Application, form typeForm) (*Application, error) {
	if ids == nil || (ids.components == nil && ids.traits == nil && ids.policies == nil) {
		return app, nil
	}
	resolveRef := func(ix *typeIndex, keep func(string) bool, ref string, form typeForm) (string, bool, error) {
		if keep != nil && keep(ref) {
			return ref, false, nil
		}
		return resolveRef(ix, ref, form)
	}
	var keepComponent, keepTrait, keepPolicy func(string) bool
	if ids.keep != nil {
		keepComponent, keepTrait, keepPolicy = ids.keep.component, ids.keep.trait, ids.keep.policy
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
		key, changed, err := resolveRef(ids.components, keepComponent, c.Type, form)
		if err != nil {
			return nil, errors.Errorf("component %q: %w", c.Name, err)
		}
		if changed {
			own()
			out.Spec.Components[i].Type = key
		}
		for j := range c.Traits {
			key, changed, err := resolveRef(ids.traits, keepTrait, c.Traits[j].Type, form)
			if err != nil {
				return nil, errors.Errorf("component %q trait[%d]: %w", c.Name, j, err)
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
		key, changed, err := resolveRef(ids.policies, keepPolicy, p.Type, form)
		if err != nil {
			return nil, errors.Errorf("policy %q: %w", p.Name, err)
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
// first, with each declared type written as its identity (`name@version`, the
// version as declared) rather than the plain name the engine dispatches on: app
// with every component, trait and policy type that a declaration names resolved,
// as a copy when anything changed. A type nobody declared an identity for stays
// as written. A caller that reads Component.Type itself, rather than going through
// a handler lookup, resolves first. The result is valid input to Transform, which
// resolves an identity again to the same type.
//
// A reference that does not resolve is a ValidationError whose Cause is the
// resolver's error, as Parse and Transform return it, so errors.Is still matches
// ErrTypeReferenceUnknown and ErrTypeReferenceAmbiguous.
func (t *Transformer) ResolveTypes(app *Application) (*Application, error) {
	resolved, err := t.identities().resolve(app, byIdentity)
	if err != nil {
		return nil, typeValidationError(err)
	}
	return resolved, nil
}
