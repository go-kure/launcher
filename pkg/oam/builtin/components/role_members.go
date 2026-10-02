package components

import (
	"maps"

	"github.com/go-kure/launcher/pkg/oam"
)

// roleObjectTraits are the authored trait types a role rule forwards to every
// member, because they decorate every object a component generates and each
// member generates its own (see WebserviceRule).
var roleObjectTraits = map[string]bool{"prune-protection": true, "force-replace": true}

// roleServiceAccount moves a role component's per-component ServiceAccount out
// of its deployment member into a `serviceaccount` sibling member
// (go-kure/launcher#702). When serviceAccountName is authored, the pod runs as
// that existing account and nothing is emitted, as before. Otherwise the member
// is the account deployment generated itself — the component's name, its
// labels, automountServiceAccountToken false — and the deployment member is
// handed that name, so it emits none of its own. traits are the authored
// traits; the object decorators among them are forwarded to the member, as
// they acted on the account when deployment generated it. depProps must be the
// rule's own copy of the properties.
func roleServiceAccount(comp *oam.Component, depProps map[string]any, traits []oam.Trait) *oam.Component {
	if _, authored := authoredValue(depProps, "serviceAccountName"); authored {
		return nil
	}
	depProps["serviceAccountName"] = comp.Name
	var saTraits []oam.Trait
	for _, t := range traits {
		if roleObjectTraits[t.Type] {
			saTraits = append(saTraits, t)
		}
	}
	return &oam.Component{
		Name:        comp.Name,
		Type:        "serviceaccount",
		Properties:  map[string]any{"automountServiceAccountToken": false},
		Traits:      saTraits,
		Annotations: maps.Clone(comp.Annotations),
	}
}
