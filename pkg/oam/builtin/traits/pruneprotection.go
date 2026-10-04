package traits

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// PruneProtectionHandler handles OAM prune-protection traits.
//
// When applied, the component's application records the delivery intent
// stack.DeliveryIntent.PruneProtection: its objects stay in the cluster when
// they are removed from the source. The trait takes no properties.
//
// The trait writes nothing onto the generated objects. The workflow that
// delivers the application turns the intent into its engine's mechanism: kure's
// Flux workflow sets kustomize.toolkit.fluxcd.io/prune: disabled on the
// application's objects (go-kure/launcher#782).
type PruneProtectionHandler struct{}

// CanHandle returns true for the "prune-protection" trait type.
func (h *PruneProtectionHandler) CanHandle(traitType string) bool {
	return traitType == "prune-protection"
}

// PropertySchema declares the prune-protection trait's properties. It accepts no
// user-facing properties, so the schema is empty.
func (h *PruneProtectionHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

// Apply sets the PruneProtection delivery intent on the application it is
// handed, and leaves its config and its objects as they are.
//
// The intent covers what the delivering workflow takes for that application's
// objects. For kure's Flux layout integration that is everything the
// application generates, wherever a layout.LayoutAugmenter config places it (a
// helmtemplate component's hook groups move into child layouts), what such a
// config adds to its layout and the layouts below it, and the
// configMapGenerators of those layouts.
//
// Apply itself sets the intent only on the application it is handed. The
// sub-applications the component's other traits append to the bundle (e.g. pvc,
// rbac, ingress) are reached through DecoratesSubApplications: the engine calls
// Apply on each of them once every trait has run. A sibling group's application
// takes the intent of its members from the engine (go-kure/launcher#782).
func (h *PruneProtectionHandler) Apply(_ *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	app.Delivery.PruneProtection = true
	return nil
}

// DecoratesSubApplications reports that the trait also covers the component's
// trait sub-applications (oam.SubApplicationDecorator).
func (h *PruneProtectionHandler) DecoratesSubApplications() bool { return true }

var _ oam.SubApplicationDecorator = (*PruneProtectionHandler)(nil)
