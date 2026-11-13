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
	m     *Model
	c     *Compiled
	t     *oam.Transformer
	depth int // 1 plus the depth of the deepest part that is a model
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
	if err := refuseAuthoredTraits(comp, r.m.Key()); err != nil {
		return oam.LoweringResult{}, err
	}
	inputs := make(map[string]any, len(r.m.Spec.Inputs))
	for name, s := range r.m.Spec.Inputs {
		if s.Default != nil {
			inputs[name] = s.Default
		}
	}
	for k, v := range comp.Properties {
		// An explicit null reads as absent (the authored path keeps the key, the emitted path
		// deletes it), so it never overwrites a default.
		if oam.IsNullValue(v) {
			continue
		}
		inputs[k] = v
	}
	// The name a Naming hook is asked in the build: the document as it stands at this round
	// (a document rule may have renamed it), else the one the origin names.
	application := lctx.Origin.Document
	if lctx.Document != nil {
		application = lctx.Document.Metadata.Name
	}
	sc := Scope{Inputs: inputs, Self: Self{Name: comp.Name, Application: application}}
	name := func(part string) (string, error) { return lctx.Namer.Name(comp.Name, part, lctx.Origin) }
	comps, parts, err := r.c.expand(r.t, sc, objectNameUnhooked(lctx.NamingHook()), name)
	if err != nil {
		return oam.LoweringResult{}, errors.Wrapf(err, "model: %s", r.m.Key())
	}
	// The consumer hook sees the components only of a render that succeeded, in part order.
	for i, c := range comps {
		if err := lctx.EmitModelComponent(r.m.Key(), parts[i], c); err != nil {
			return oam.LoweringResult{}, err
		}
	}
	return oam.LoweringResult{Components: comps}, nil
}

// objectNameUnhooked is naming without its answers for the role object. The build names the
// object of a kind part (a Secret, a ConfigMap) after its component and never asks the hook,
// because this rule emitted it, so the read of the name must not ask either. A direct Expand
// keeps the hook: its components are not emitted members, and a Transform asks it for them.
func objectNameUnhooked(naming func(oam.NameRequest) (string, bool)) func(oam.NameRequest) (string, bool) {
	if naming == nil {
		return nil
	}
	return func(r oam.NameRequest) (string, bool) {
		if r.Role == oam.NameRoleObject {
			return "", false
		}
		return naming(r)
	}
}
