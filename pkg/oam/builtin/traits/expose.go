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
		if r.CertManagerClusterIssuer != "" {
			return nil, errors.New("expose rendering: certManagerClusterIssuer is only valid when controllerType is \"ingress\"")
		}
		if r.GatewayNamespace == "" {
			rendering["gatewayNamespace"] = "gateway-system"
		}
	default:
		return nil, errors.Errorf("expose rendering: controllerType %q is not supported (want \"ingress\" or \"gateway\")", r.ControllerType)
	}
	return rendering, nil
}

// PropertySchema declares the expose trait's user-facing properties. expose is a
// dispatcher, so its surface is the effective union it passes through to the
// ingress (rules) or gateway (hostnames) handler, minus `tls` (platform-managed).
// controllerType and the ingressClassName/gateway*/certManager* keys are supplied
// by capability rendering.
func (h *ExposeHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		// controllerType is capability-injected, not user-set (see doc above), so it is
		// NOT user-required here; it is validated in ValidateAndApplyDefaults. Kept in the
		// schema as an optional enum so a value, if present, is type/enum-checked.
		"controllerType":           {Type: oam.PropertyTypeString, Enum: []any{"ingress", "gateway"}},
		"certManagerClusterIssuer": {Type: oam.PropertyTypeString},
		"allowedHostnameWildcard":  {Type: oam.PropertyTypeString},
		"gatewayName":              {Type: oam.PropertyTypeString},
		"gatewayNamespace":         {Type: oam.PropertyTypeString, Default: "gateway-system"},
		"annotations":              {Type: oam.PropertyTypeObject, AdditionalProperties: true},
		"rules":                    {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true}},
		"hostnames":                {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeString}},
		"ingressClassName":         {Type: oam.PropertyTypeString},
		"servicePort":              {Type: oam.PropertyTypeInteger},
		"serviceName":              {Type: oam.PropertyTypeString},
		"name":                     {Type: oam.PropertyTypeString},
		"scope":                    {Type: oam.PropertyTypeString},
		"networkPolicy":            schemaNetworkPolicy(),
	}
}

// Apply dispatches to IngressHandler or HTTPRouteHandler based on controllerType.
// It also implements platform-managed TLS (ingress path) and hostname validation
// (both paths), consuming the certManagerClusterIssuer/allowedHostnameWildcard
// capability keys so they never leak into the low-level handlers.
func (h *ExposeHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	controllerType, _ := trait.Properties["controllerType"].(string)
	props := maps.Clone(trait.Properties)
	delete(props, "controllerType")

	// Consume the platform capability keys; they are handled here, not downstream.
	issuer, _ := props["certManagerClusterIssuer"].(string)
	wildcard, _ := props["allowedHostnameWildcard"].(string)
	delete(props, "certManagerClusterIssuer")
	delete(props, "allowedHostnameWildcard")

	switch controllerType {
	case "ingress":
		hosts := uniqueStrings(ruleHosts(props))
		if err := validateHostnames(hosts, wildcard, app.Name); err != nil {
			return err
		}
		// expose is platform-managed: the user does not author TLS.
		delete(props, "tls")
		if issuer != "" {
			if err := setClusterIssuerAnnotation(props, issuer, app.Name); err != nil {
				return err
			}
			if len(hosts) > 0 {
				props["tls"] = synthesizedIngressTLS(hosts, app.Name)
			}
		}
		return (&IngressHandler{}).Apply(&oam.Trait{Type: "expose", Properties: props}, app, bundle)
	case "gateway":
		if err := validateHostnames(hostnameList(props), wildcard, app.Name); err != nil {
			return err
		}
		gatewayName, _ := props["gatewayName"].(string)
		gatewayNamespace, _ := props["gatewayNamespace"].(string)
		delete(props, "gatewayName")
		delete(props, "gatewayNamespace")
		props["parentRefs"] = []any{synthesizeParentRef(gatewayName, gatewayNamespace)}
		return (&HTTPRouteHandler{}).Apply(&oam.Trait{Type: "expose", Properties: props}, app, bundle)
	default:
		return errors.Errorf("expose trait: unsupported controllerType %q", controllerType)
	}
}

// ruleHosts extracts the host of every entry in the ingress-style rules[] property.
func ruleHosts(props map[string]any) []string {
	var hosts []string
	if rawRules, ok := props["rules"].([]any); ok {
		for _, r := range rawRules {
			if rm, ok := r.(map[string]any); ok {
				if host, ok := rm["host"].(string); ok && host != "" {
					hosts = append(hosts, host)
				}
			}
		}
	}
	return hosts
}

// hostnameList extracts the gateway-style hostnames[] property.
func hostnameList(props map[string]any) []string {
	var hosts []string
	if raw, ok := props["hostnames"].([]any); ok {
		for _, h := range raw {
			if s, ok := h.(string); ok && s != "" {
				hosts = append(hosts, s)
			}
		}
	}
	return hosts
}

// setClusterIssuerAnnotation adds the platform cert-manager cluster-issuer
// annotation. The platform value is authoritative: a conflicting user-supplied
// value is rejected as a ValidationError.
func setClusterIssuerAnnotation(props map[string]any, issuer, component string) error {
	anns, _ := props["annotations"].(map[string]any)
	if anns == nil {
		anns = map[string]any{}
	}
	if existing, ok := anns[clusterIssuerAnnotation].(string); ok && existing != issuer {
		return &errors.ValidationError{
			Field:     "annotations." + clusterIssuerAnnotation,
			Value:     existing,
			Component: component,
			Message: "annotation " + clusterIssuerAnnotation +
				" is platform-managed by the expose trait and cannot be overridden",
		}
	}
	anns[clusterIssuerAnnotation] = issuer
	props["annotations"] = anns
	return nil
}

// synthesizedIngressTLS builds the single managed TLS entry: all hosts under one
// deterministic secretName (<component>-tls), for cert-manager's ingress-shim.
func synthesizedIngressTLS(hosts []string, component string) []any {
	anyHosts := make([]any, len(hosts))
	for i, h := range hosts {
		anyHosts[i] = h
	}
	return []any{map[string]any{
		"hosts":      anyHosts,
		"secretName": component + "-tls",
	}}
}
