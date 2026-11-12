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
	cache *Cache
	mu    sync.Mutex
	rules map[string]*Rule
}

// NewRegistry returns a registry for t.
func NewRegistry(t *oam.Transformer) *Registry {
	return &Registry{t: t, cache: CreateCache(), rules: map[string]*Rule{}}
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
	c, err := g.cache.Compile(m)
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

// Rule returns the rule registered under key, or nil.
func (g *Registry) Rule(key string) *Rule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules[key]
}
