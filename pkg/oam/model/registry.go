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
	depth map[string]int
}

// NewRegistry returns a registry for t.
func NewRegistry(t *oam.Transformer) *Registry {
	return &Registry{t: t, cache: CreateCache(), rules: map[string]*Rule{}, depth: map[string]int{}}
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
		if d := g.depth[id.Canonical().String()] + 1; d > depth {
			depth = d
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
	r := &Rule{m: m, c: c, t: g.t}
	// The Transformer panics on a type it cannot take, before it holds any of it.
	defer func() {
		if p := recover(); p != nil {
			err = errors.Errorf("model: register %s: %v", key, p)
		}
	}()
	g.t.RegisterComponentLowering(r)
	if err := g.t.DeclareComponentTypes(map[string]oam.TypeID{key: m.Identity()}); err != nil {
		return errors.Wrapf(err, "model: declare %s", key)
	}
	g.rules[key] = r
	g.depth[key] = depth
	return nil
}

// Rule returns the rule registered under key, or nil.
func (g *Registry) Rule(key string) *Rule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules[key]
}
