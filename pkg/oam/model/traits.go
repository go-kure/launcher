package model

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ErrTraitsUnsupported is wrapped by the refusal of a trait on a component of a model type. A
// model has many parts, so no part is the one such a trait belongs to, and the rule does not
// forward it. Refusing it is what keeps the build from ignoring a trait that somebody wrote
// (go-kure/launcher#1528).
var ErrTraitsUnsupported = errors.New("model: traits on a model component are not supported")

// refuseTraits refuses the first trait of comp. A trait that carries an origin was written by a
// rule, which for a model type is an outer model, and the error says so.
func refuseTraits(comp *oam.Component, model string) error {
	if len(comp.Traits) == 0 {
		return nil
	}
	first := comp.Traits[0]
	by := "written in the document"
	if _, emitted := first.Origin(); emitted {
		by = "written by an outer model"
	}
	return errors.Errorf("component %q (type %q), trait %q, %s: %w", comp.Name, model, first.Type, by, ErrTraitsUnsupported)
}
