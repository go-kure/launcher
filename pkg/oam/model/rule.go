package model

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

const (
	// MaxParts is the number of components one render may give. It counts after when and
	// forEach, so a part that is skipped counts none.
	MaxParts = 50
	// MaxNestingDepth is the length of the longest chain of models, each naming the next as
	// the type of a part. A model with no model part has depth 1.
	MaxNestingDepth = 4
)

// ErrLimit is wrapped by the failure of a render or a registration that breaks a limit.
var ErrLimit = errors.New("model: limit exceeded")

// Rule is the component lowering rule of one model: a component of the model's type lowers to
// the components of its parts. A Registry makes it. It is safe for concurrent use, and the
// model it was made from must not change.
type Rule struct {
	m *Model
	c *Compiled
	t *oam.Transformer
}

// ComponentType is the registry key of the model.
func (r *Rule) ComponentType() string { return r.m.Key() }

// PropertySchema publishes the inputs of the model as the schema of its component.
func (r *Rule) PropertySchema() map[string]oam.PropertySchema { return r.m.Spec.Inputs }

// LowerComponent expands one component of the model's type into the components of its parts.
// An input the component does not set takes its default. A part is named
// "<component>-<part>" by the allocator of the run, so a name that two parts want fails the
// build, and `parts.<part>.<key>` reads the name the build gives, under the Naming hook of the
// run where one is set. Part types and trait types stay as the model wrote them: the engine
// lowers them in the next round.
func (r *Rule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	if lctx.Namer == nil {
		return oam.LoweringResult{}, errors.Errorf("model: %s: no name allocator in the lowering context", r.m.Key())
	}
	inputs := make(map[string]any, len(r.m.Spec.Inputs)+len(comp.Properties))
	for name, s := range r.m.Spec.Inputs {
		if s.Default != nil {
			inputs[name] = s.Default
		}
	}
	for k, v := range comp.Properties {
		inputs[k] = v
	}
	application := lctx.Origin.Document
	if application == "" && lctx.Document != nil {
		application = lctx.Document.Metadata.Name
	}
	sc := Scope{Inputs: inputs, Self: Self{Name: comp.Name, Application: application}}
	name := func(part string) (string, error) { return lctx.Namer.Name(comp.Name, part, lctx.Origin) }
	comps, err := r.c.Expand(r.t, sc, lctx.NamingHook(), name)
	if err != nil {
		return oam.LoweringResult{}, errors.Wrapf(err, "model: %s", r.m.Key())
	}
	return oam.LoweringResult{Components: comps}, nil
}
