package traits

import (
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
func (h *ExposeHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	r, err := builtin.DecodeStrict[builtin.ExposeRendering](rendering)
	if err != nil {
		return nil, errors.Wrap(err, "expose rendering")
	}
	if r.ControllerType == "" {
		return nil, errors.New("expose rendering: controllerType is required")
	}
	switch r.ControllerType {
	case "ingress":
		if r.IngressClassName == "" {
			return nil, errors.New("expose rendering: ingressClassName is required when controllerType is \"ingress\"")
		}
		if r.GatewayName != "" || r.GatewayNamespace != "" {
			return nil, errors.New("expose rendering: gatewayName and gatewayNamespace are only valid when controllerType is \"gateway\"")
		}
	case "gateway":
		if r.GatewayName == "" {
			return nil, errors.New("expose rendering: gatewayName is required when controllerType is \"gateway\"")
		}
		if r.IngressClassName != "" {
			return nil, errors.New("expose rendering: ingressClassName is only valid when controllerType is \"ingress\"")
		}
		if r.GatewayNamespace == "" {
			rendering["gatewayNamespace"] = "gateway-system"
		}
	default:
		return nil, errors.Errorf("expose rendering: controllerType %q is not supported (want \"ingress\" or \"gateway\")", r.ControllerType)
	}
	return rendering, nil
}

// Apply dispatches to IngressHandler or HTTPRouteHandler based on controllerType.
func (h *ExposeHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	controllerType, _ := trait.Properties["controllerType"].(string)
	props := maps.Clone(trait.Properties)
	delete(props, "controllerType")
	modified := &oam.Trait{Type: "expose", Properties: props}
	switch controllerType {
	case "ingress":
		return (&IngressHandler{}).Apply(modified, app, bundle)
	case "gateway":
		gatewayName, _ := props["gatewayName"].(string)
		gatewayNamespace, _ := props["gatewayNamespace"].(string)
		if gatewayNamespace == "" {
			gatewayNamespace = "gateway-system"
		}
		delete(props, "gatewayName")
		delete(props, "gatewayNamespace")
		ref := map[string]any{"name": gatewayName}
		if gatewayNamespace != "" {
			ref["namespace"] = gatewayNamespace
		}
		props["parentRefs"] = []any{ref}
		modified = &oam.Trait{Type: "expose", Properties: props}
		return (&HTTPRouteHandler{}).Apply(modified, app, bundle)
	default:
		return errors.Errorf("expose trait: unsupported controllerType %q", controllerType)
	}
}
