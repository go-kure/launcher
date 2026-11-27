package model

import (
	"strconv"
	"sync"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// Registry registers models on one Transformer. A model names its parts by full identity and
// every part type and trait type must already be declared on the Transformer, so a model can
// name only a model registered before it, and no cycle exists. It is safe for concurrent use.
type Registry struct {
	t     *oam.Transformer
	mu    sync.Mutex
	rules map[string]*Rule
}

// NewRegistry returns a registry for t.
func NewRegistry(t *oam.Transformer) *Registry {
	return &Registry{t: t, rules: map[string]*Rule{}}
}

// Register compiles m, checks that its type is not declared yet, that every part type and trait
// type is declared (Load already pins each to a version), and that the models nest at most MaxNestingDepth deep.
// It then registers the rule and declares the type. A failure names the path of the part, and
// nothing is registered unless every check passed.
func (g *Registry) Register(m *Model) (err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := m.Key()
	if _, ok := g.rules[key]; ok {
		return errors.Errorf("model: %s is already registered", key)
	}
	if _, err := g.t.ResolveComponentType(key); err == nil {
		return errors.Errorf("model: the type %s is already declared", key)
	}
	// An identity resolves before a plain name, and a model is known by its qualified name and
	// by its short name, so a model of the name of a plain key that no declared type claims
	// would be built in the place of the type that holds the key (go-kure/launcher#1716).
	ident := m.Identity()
	names := []string{ident.Name}
	if short := ident.ShortName(); short != ident.Name {
		names = append(names, short)
	}
	for _, name := range names {
		if g.holdsUnclaimedPlainKey(name) {
			return errors.Errorf("model: %s: the name %q, which the model answers to, is the plain name of a registered component type, and an identity resolves before a plain name, so the model would be built in that type's place; rename one of them", key, name)
		}
	}
	// The key is mandatory so that a model says what it publishes, even when it publishes
	// nothing: a model written before the outputs existed is refused, not read as silent.
	if m.Spec.Outputs == nil {
		return errors.Errorf("model: %s: spec.outputs is required: name the records the model publishes (${ parts.<id>.name } or ${ parts.<id>.<key>.name }), or write `outputs: {}` for none", key)
	}
	// A cache of its own for each model: a refused registration leaves no compiled site for a
	// later definition of the same identity to read.
	c, err := Compile(m)
	if err != nil {
		return err
	}
	depth := 1
	for i, p := range m.Spec.Parts {
		base := "spec.parts[" + strconv.Itoa(i) + "]"
		id, err := g.t.ResolveComponentType(p.Type)
		if err != nil {
			return errors.Errorf("model: %s.type: %w", base, err)
		}
		// The depth is read from the rule the Transformer holds, so a model registered through
		// another Registry of the same Transformer counts too.
		if sub, ok := g.t.ComponentLoweringRuleFor(id.String()); ok {
			if mr, isModel := sub.(*Rule); isModel && mr.depth+1 > depth {
				depth = mr.depth + 1
			}
		}
		for j, t := range p.Traits {
			if _, err := g.t.ResolveTraitType(t.Type); err != nil {
				return errors.Errorf("model: %s.traits[%d].type: %w", base, j, err)
			}
		}
	}
	if depth > MaxNestingDepth {
		return errors.Errorf("%w: %s nests models %d deep, the limit is %d", ErrLimit, key, depth, MaxNestingDepth)
	}
	// The Transformer cannot unregister a rule, so the declaration is checked before the rule
	// is registered, and it is the only step that can refuse after it.
	ids := map[string]oam.TypeID{key: m.Identity()}
	if err := g.t.CheckComponentTypes(ids); err != nil {
		return errors.Wrapf(err, "model: declare %s", key)
	}
	r := &Rule{m: m, c: c, t: g.t, depth: depth}
	// The Transformer panics on a type it cannot take, before it holds any of it.
	defer func() {
		if p := recover(); p != nil {
			err = errors.Errorf("model: register %s: %v", key, p)
		}
	}()
	g.t.RegisterComponentLowering(r)
	if err := g.t.DeclareComponentTypes(ids); err != nil {
		return errors.Wrapf(err, "model: declare %s", key)
	}
	g.rules[key] = r
	return nil
}

// holdsUnclaimedPlainKey reports whether a handler or a lowering rule is registered under name
// and no declared type claims name, by its qualified name or its short name. A name a declared
// type claims resolves to that type, or is reported ambiguous, and so never reads as another's.
func (g *Registry) holdsUnclaimedPlainKey(name string) bool {
	_, handler := g.t.ComponentHandlerFor(name)
	_, rule := g.t.ComponentLoweringRuleFor(name)
	if !handler && !rule {
		return false
	}
	_, err := g.t.ResolveComponentType(name)
	return errors.Is(err, oam.ErrTypeReferenceUnknown)
}

// Rule returns the rule registered under key, or nil.
func (g *Registry) Rule(key string) *Rule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules[key]
}
