package traits

import (
	"fmt"
	"maps"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// ExposeHandler handles OAM expose traits, dispatching to IngressHandler based
// on the controllerType field injected by capability rendering.
type ExposeHandler struct{}

// CanHandle returns true for expose trait type.
func (h *ExposeHandler) CanHandle(traitType string) bool {
	return traitType == "expose"
}

// CapabilityRequired returns true: the expose trait needs controllerType from
// a ClusterProfile capability and cannot produce valid output without it.
func (h *ExposeHandler) CapabilityRequired() bool { return true }

// ValidateAndApplyDefaults validates the capability rendering for the expose trait.
// It accepts only controllerType "ingress" in this release; "gateway" is deferred
// until the httproute handler lands.
func (h *ExposeHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	r, err := builtin.DecodeStrict[builtin.ExposeRendering](rendering)
	if err != nil {
		return nil, errors.Wrap(err, "expose rendering")
	}
	if r.ControllerType == "" {
		return nil, errors.New("expose rendering: controllerType is required")
	}
	if r.ControllerType != "ingress" {
		return nil, errors.Errorf("expose rendering: controllerType %q is not yet implemented; only \"ingress\" is supported", r.ControllerType)
	}
	if r.IngressClassName == "" {
		return nil, errors.New("expose rendering: ingressClassName is required when controllerType is \"ingress\"")
	}
	if r.GatewayName != "" || r.GatewayNamespace != "" {
		return nil, errors.New("expose rendering: gatewayName and gatewayNamespace are only valid when controllerType is \"gateway\"")
	}
	return rendering, nil
}

// Apply dispatches to IngressHandler based on controllerType in the trait properties.
func (h *ExposeHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	controllerType, _ := trait.Properties["controllerType"].(string)
	props := maps.Clone(trait.Properties)
	delete(props, "controllerType")
	modified := &oam.Trait{Type: "expose", Properties: props}
	switch controllerType {
	case "ingress":
		return (&IngressHandler{}).Apply(modified, app, bundle)
	default:
		return fmt.Errorf("expose trait: unsupported controllerType %q", controllerType)
	}
}
