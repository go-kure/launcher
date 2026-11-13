package model

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ErrTraitsUnsupported is wrapped by the refusal of a trait that the author wrote on a
// component of a model type. A model has many parts, so no part is the one such a trait
// belongs to, and the rule does not forward it. Refusing it is what keeps the build from
// ignoring a trait the author wrote (go-kure/launcher#1528).
var ErrTraitsUnsupported = errors.New("model: traits on a model component are not supported")

// refuseAuthoredTraits refuses the first trait of comp that the author wrote. A trait that an
// outer model wrote in its own `traits:` is on the component that the outer rule emitted, and
// the engine marks it as synthesized, so it passes.
func refuseAuthoredTraits(comp *oam.Component, model string) error {
	for i := range comp.Traits {
		if comp.Traits[i].Synthesized() {
			continue
		}
		return errors.Errorf("component %q (type %q), trait %q: %w", comp.Name, model, comp.Traits[i].Type, ErrTraitsUnsupported)
	}
	return nil
}
