package traits

import (
	"maps"
	"strconv"

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
		if r.SSLRedirect != nil || r.ForceSSLRedirect != nil {
			return nil, errors.New("expose rendering: sslRedirect and forceSslRedirect are only valid when controllerType is \"ingress\"")
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
		"controllerType":           {Type: oam.PropertyTypeString, Enum: []any{"ingress", "gateway"}, Description: "Capability-injected controller kind (ingress or gateway) this expose dispatches to."},
		"certManagerClusterIssuer": {Type: oam.PropertyTypeString, Description: "cert-manager ClusterIssuer used to synthesize TLS (ingress controllerType only)."},
		"allowedHostnameWildcard":  {Type: oam.PropertyTypeString, Description: "Platform-reserved wildcard the hostnames must fall under."},
		"gatewayName":              {Type: oam.PropertyTypeString, Description: "Gateway name used to synthesize parentRefs (gateway controllerType only)."},
		"gatewayNamespace":         {Type: oam.PropertyTypeString, Default: "gateway-system", Description: "Namespace of the Gateway (gateway controllerType only)."},
		"annotations":              {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Additional annotations to set on the generated resource."},
		"rules":                    {Type: oam.PropertyTypeArray, Description: "Ingress-style host rules passed through to the ingress handler.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single ingress-style host rule."}},
		"hostnames":                {Type: oam.PropertyTypeArray, Description: "Hostnames: gateway routes, or an ingress shorthand that expands to one rule per host when rules is absent.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname to route."}},
		"ingressClassName":         {Type: oam.PropertyTypeString, Description: "IngressClass to use (ingress controllerType only)."},
		"sslRedirect":              {Type: oam.PropertyTypeBoolean, Description: "nginx ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"forceSslRedirect":         {Type: oam.PropertyTypeBoolean, Description: "nginx force-ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"servicePort":              {Type: oam.PropertyTypeInteger, Description: "Service port to route to when the component does not expose one."},
		"serviceName":              {Type: oam.PropertyTypeString, Description: "Service name to route to; requires servicePort to also be set."},
		"name":                     {Type: oam.PropertyTypeString, Description: "Overrides the sub-application name, allowing multiple expose traits per component."},
		"scope":                    {Type: oam.PropertyTypeString, Description: "Suffix appended to the sub-application name to disambiguate multiple expose traits."},
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
		// hostnames shorthand: when hostnames is set and rules is not, synthesize
		// one rule per host (path "/" + the component service port are defaulted by
		// IngressHandler). hostnames is never an IngressHandler input on this path.
		shorthand := hostnameList(props)
		if len(shorthand) > 0 {
			if _, hasRules := props["rules"]; !hasRules {
				props["rules"] = expandHostnamesToIngressRules(shorthand)
			}
		}
		delete(props, "hostnames")
		// Validate every host that appears — the rules' hosts and any shorthand
		// hostnames — against the platform wildcard, even when both are present.
		if err := validateHostnames(uniqueStrings(append(ruleHosts(props), shorthand...)), wildcard, app.Name); err != nil {
			return err
		}
		// expose is platform-managed: the user does not author TLS.
		delete(props, "tls")
		if issuer != "" {
			if err := setClusterIssuerAnnotation(props, issuer, app.Name); err != nil {
				return err
			}
			// TLS covers the effective routing hosts only. When both `rules` and
			// `hostnames` are supplied, `rules` drives routing, so a hostnames entry
			// that is not routed must not get a synthesized certificate.
			if routingHosts := uniqueStrings(ruleHosts(props)); len(routingHosts) > 0 {
				props["tls"] = synthesizedIngressTLS(routingHosts, app.Name)
			}
		}
		// ssl-redirect / force-ssl-redirect: typed property (capability default or
		// inline override) wins over a same-key raw annotation.
		setSSLRedirectAnnotations(props)
		return (&IngressHandler{}).Apply(&oam.Trait{Type: "expose", Properties: props}, app, bundle)
	case "gateway":
		// ssl-redirect is nginx-ingress-specific; reject the inline properties on the
		// gateway path (the rendering guard only covers the capability-supplied form).
		for _, k := range []string{"sslRedirect", "forceSslRedirect"} {
			if _, ok := props[k]; ok {
				return &errors.ValidationError{
					Field:     k,
					Component: app.Name,
					Message:   k + " is only valid when controllerType is \"ingress\"",
				}
			}
		}
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

// expandHostnamesToIngressRules turns the hostnames shorthand into ingress rules,
// one host per rule with a single empty path object. IngressHandler defaults the
// path to "/" and the backend port to the component service port.
func expandHostnamesToIngressRules(hostnames []string) []any {
	rules := make([]any, len(hostnames))
	for i, h := range hostnames {
		rules[i] = map[string]any{
			"host":  h,
			"paths": []any{map[string]any{}},
		}
	}
	return rules
}

// setSSLRedirectAnnotations writes the nginx ssl-redirect / force-ssl-redirect
// annotations from the typed sslRedirect / forceSslRedirect properties, which carry
// the capability-rendered platform default (override-able inline). The typed value
// is authoritative: it is written last and wins over a same-key raw annotation. When
// the property is absent, an existing raw annotation is left untouched. The property
// keys are consumed here so they never reach IngressHandler.
func setSSLRedirectAnnotations(props map[string]any) {
	var anns map[string]any
	set := func(key string, val bool) {
		if anns == nil {
			if existing, ok := props["annotations"].(map[string]any); ok {
				anns = existing
			} else {
				anns = map[string]any{}
			}
		}
		anns[key] = strconv.FormatBool(val)
	}
	if v, ok := boolProp(props, "sslRedirect"); ok {
		set(sslRedirectAnnotation, v)
	}
	if v, ok := boolProp(props, "forceSslRedirect"); ok {
		set(forceSSLRedirectAnnotation, v)
	}
	if anns != nil {
		props["annotations"] = anns
	}
	delete(props, "sslRedirect")
	delete(props, "forceSslRedirect")
}

// boolProp reads a boolean property; ok is false when absent or not a bool.
func boolProp(props map[string]any, key string) (val, ok bool) {
	val, ok = props[key].(bool)
	return val, ok
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
