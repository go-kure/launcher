// Package lowering holds the spike's toy lowering rules (C4): a document-position
// rule ("WebApplication"), a component-position rule ("web-and-cache") that also
// emits a policy, and a policy-position rule ("ordered") that lowers that emitted
// policy into terminal "dependency" policies (pkg/oam/builtin/policies). Together
// they exercise all four D1 positions and D2's document-level 1→N through the real
// oam.Transformer fixpoint — not something these rules implement themselves. This
// package is spike-only: it is never registered by the CLI's production
// builtinComponentHandlers/builtinTraitHandlers (pkg/cmd/kurel/build.go).
package lowering

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// WebApplicationRule is a toy document-position lowering rule proving D1's document
// position and D2's document-level 1→N: it splits a "WebApplication" document into
// two disjoint "Application" documents (documented friction: this split does not
// span cluster-wide passes like netpol synthesis or source dedup — deliberate for
// the toy, noted as a production constraint). The first component goes to
// "<name>-primary" (already terminal); the rest go to "<name>-secondary", which
// still carries a "web-and-cache" component — its own lowering continues on the
// fixpoint's next round, proving recursion is the engine's doing, not this rule's.
type WebApplicationRule struct{}

func (WebApplicationRule) Kind() string { return "WebApplication" }

func (WebApplicationRule) LowerDocument(doc *oam.Application, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	if len(doc.Spec.Components) < 2 {
		return oam.LoweringResult{}, errors.Errorf(
			"WebApplication %q: spec.components must have at least 2 entries to split into primary/secondary",
			doc.Metadata.Name)
	}

	primary := *doc
	primary.Kind = "Application"
	primary.Metadata.Name = doc.Metadata.Name + "-primary"
	primary.Spec = oam.ApplicationSpec{Components: append([]oam.Component{}, doc.Spec.Components[:1]...)}

	secondary := *doc
	secondary.Kind = "Application"
	secondary.Metadata.Name = doc.Metadata.Name + "-secondary"
	secondary.Spec = oam.ApplicationSpec{Components: append([]oam.Component{}, doc.Spec.Components[1:]...)}

	return oam.LoweringResult{Documents: []oam.Application{primary, secondary}}, nil
}
