package traits

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// ForceReplaceHandler handles OAM force-replace traits.
//
// When applied, the component's application records the delivery intent
// stack.DeliveryIntent.ForceReplace: its objects may be deleted and recreated
// when an update would otherwise fail on an immutable field — a Job's pod
// template being the case the trait exists for. Replacing a Job re-runs it,
// stopping any run in progress. The trait takes no properties and is opt-in.
//
// The trait writes nothing onto the generated objects. The workflow that
// delivers the application turns the intent into its engine's mechanism: kure's
// Flux workflow sets kustomize.toolkit.fluxcd.io/force: enabled on the
// application's objects (go-kure/launcher#782).
type ForceReplaceHandler struct{}

// CanHandle returns true for the "force-replace" trait type.
func (h *ForceReplaceHandler) CanHandle(traitType string) bool {
	return traitType == "force-replace"
}

// PropertySchema declares the force-replace trait's properties. It accepts no
// user-facing properties, so the schema is empty.
func (h *ForceReplaceHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

// Apply sets the ForceReplace delivery intent on the application it is handed,
// and leaves its config and its objects as they are.
//
// Coverage is prune-protection's (see PruneProtectionHandler.Apply): the
// component's own application and, through DecoratesSubApplications, the
// sub-applications the component's other traits append to the bundle.
func (h *ForceReplaceHandler) Apply(_ *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	app.Delivery.ForceReplace = true
	return nil
}

// DecoratesSubApplications reports that the trait also covers the component's
// trait sub-applications (oam.SubApplicationDecorator).
func (h *ForceReplaceHandler) DecoratesSubApplications() bool { return true }

var _ oam.SubApplicationDecorator = (*ForceReplaceHandler)(nil)
