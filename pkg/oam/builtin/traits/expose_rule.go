package traits

import (
	"maps"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// ExposeRule lowers an "expose" trait (D5, D1 trait position) into a resolved,
// terminal "ingress" or "httproute" trait, dispatching on the controllerType
// capability value. It is the C6 port of the former ExposeHandler.Apply
// (expose.go): the engine merges capability rendering into the trait before
// calling LowerTrait (lowering.go), so this rule reads trait.Properties exactly as
// ExposeHandler.Apply once read them post-merge — only the two direct handler
// calls at the end of each branch changed, from invoking
// IngressHandler/HTTPRouteHandler.Apply directly to emitting a Trait for the
// engine's fixpoint to dispatch on the next round.
//
// This type deliberately does NOT redeclare the helper functions ExposeHandler's
// Apply already uses (ruleHosts, hostnameList, setClusterIssuerAnnotation,
// expandHostnamesToIngressRules, setSSLRedirectAnnotations, boolProp,
// setAuthAnnotations, stringList, synthesizedIngressTLS — all package-level in
// expose.go): ExposeHandler stays registered as a dispatchable TraitHandler for
// the four pre-existing direct-construction test files
// (expose_ext_auth_test.go, expose_managed_tls_test.go,
// expose_secretname_override_test.go, expose_shorthand_sslredirect_test.go), so
// those helpers already exist in this package and ExposeRule.LowerTrait calls them
// directly rather than duplicating them.
type ExposeRule struct{}

// TraitType claims the "expose" trait type at the trait lowering position
// (oam.TraitLoweringRule, lowering.go). Removing "expose" from build.go's
// dispatchable trait-handler map (builtinTraitHandlers) and registering this rule
// via RegisterTraitLowering means "expose" is now reachable only here.
func (ExposeRule) TraitType() string { return "expose" }

// CapabilityRequired returns true: the expose trait needs controllerType from a
// ClusterProfile capability and cannot produce valid output without it. The engine
// enforces this (lowering.go, via the CapabilityAware optional interface) exactly
// as applyTraits enforces it for a dispatchable TraitHandler.
func (ExposeRule) CapabilityRequired() bool { return true }

// ValidateAndApplyDefaults validates the capability rendering for the expose
// trait. Identical to the former ExposeHandler.ValidateAndApplyDefaults;
// EvaluateProfile (transform.go) calls it via the trait-lowering-rule registry
// fallback instead of the trait-handler registry.
func (ExposeRule) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
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
		if strings.Contains(r.AuthURL, "?") {
			return nil, errors.New("expose rendering: authURL must be a base URL without a query string")
		}
		if (r.AuthSigninURL != "" || r.AuthResponseHeaders != "") && r.AuthURL == "" {
			return nil, errors.New("expose rendering: authSigninURL and authResponseHeaders require authURL")
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
		if r.AuthURL != "" || r.AuthSigninURL != "" || r.AuthResponseHeaders != "" {
			return nil, errors.New("expose rendering: authURL, authSigninURL and authResponseHeaders are only valid when controllerType is \"ingress\"")
		}
		if r.GatewayNamespace == "" {
			rendering["gatewayNamespace"] = "gateway-system"
		}
	default:
		return nil, errors.Errorf("expose rendering: controllerType %q is not supported (want \"ingress\" or \"gateway\")", r.ControllerType)
	}
	return rendering, nil
}

// PropertySchema declares the expose trait's user-facing properties. Identical to
// ExposeHandler.PropertySchema (expose.go) — deliberately NOT tightened with the
// extra PlatformReserved tags the spike's version carries on controllerType,
// certManagerClusterIssuer, allowedHostnameWildcard, authURL and
// authResponseHeaders. testdata/webservice-expose-ingress/app.yaml (this branch's
// existing golden fixture, unlike the spike's rewritten one) authors
// `controllerType: ingress` inline; marking it PlatformReserved makes
// enforcePlatformReserved (lowering.go's trait-position check, wired before this
// task, and now also createApplications/applyTraits) reject that fixture outright,
// breaking the byte-identity acceptance bar. The D3 gap this task closes is the
// enforcement call sites (lowering.go already had it; createApplications and
// applyTraits did not, until this change) actually running for every schema that
// DOES mark a field reserved — e.g. the shared networkPolicy fragment below,
// already reserved via schemaNetworkPolicy(true) — not adding new reservations to
// this particular schema.
func (ExposeRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		// controllerType is capability-injected, not user-set (see doc above), so it is
		// NOT user-required here; it is validated in ValidateAndApplyDefaults. Kept in the
		// schema as an optional enum so a value, if present, is type/enum-checked.
		"controllerType":           {Type: oam.PropertyTypeString, Enum: []any{"ingress", "gateway"}, Description: "Capability-injected controller kind (ingress or gateway) this expose dispatches to."},
		"certManagerClusterIssuer": {Type: oam.PropertyTypeString, Description: "cert-manager ClusterIssuer used to synthesize TLS (ingress controllerType only)."},
		"secretName":               {Type: oam.PropertyTypeString, Description: "Overrides the synthesized <component>-tls secret name for platform-managed TLS (ingress controllerType only; requires a cert-manager cluster-issuer capability)."},
		"allowedHostnameWildcard":  {Type: oam.PropertyTypeString, Description: "Platform-reserved wildcard the hostnames must fall under."},
		"gatewayName":              {Type: oam.PropertyTypeString, Description: "Gateway name used to synthesize parentRefs (gateway controllerType only)."},
		"gatewayNamespace":         {Type: oam.PropertyTypeString, Default: "gateway-system", Description: "Namespace of the Gateway (gateway controllerType only)."},
		"annotations":              {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Additional annotations to set on the generated resource."},
		"rules":                    {Type: oam.PropertyTypeArray, Description: "Ingress-style host rules passed through to the ingress handler.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single ingress-style host rule."}},
		"hostnames":                {Type: oam.PropertyTypeArray, Description: "Hostnames: gateway routes, or an ingress shorthand that expands to one rule per host when rules is absent.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname to route."}},
		"ingressClassName":         {Type: oam.PropertyTypeString, Description: "IngressClass to use (ingress controllerType only)."},
		"sslRedirect":              {Type: oam.PropertyTypeBoolean, Description: "nginx ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"forceSslRedirect":         {Type: oam.PropertyTypeBoolean, Description: "nginx force-ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"allowedGroups":            {Type: oam.PropertyTypeArray, Description: "oauth2-proxy allowed groups; enables external-auth on the route (ingress controllerType only). Order is preserved.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An allowed oauth2-proxy group."}},
		"authURL":                  {Type: oam.PropertyTypeString, Description: "Capability-injected nginx external-auth endpoint base (ingress controllerType only)."},
		"authSigninURL":            {Type: oam.PropertyTypeString, Description: "nginx auth-signin URL (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"authResponseHeaders":      {Type: oam.PropertyTypeString, Description: "Capability-injected nginx auth-response-headers value (ingress controllerType only)."},
		"servicePort":              {Type: oam.PropertyTypeInteger, Description: "Service port to route to when the component does not expose one."},
		"serviceName":              {Type: oam.PropertyTypeString, Description: "Service name to route to; requires servicePort to also be set."},
		"name":                     {Type: oam.PropertyTypeString, Description: "Overrides the sub-application name, allowing multiple expose traits per component."},
		"scope":                    {Type: oam.PropertyTypeString, Description: "Suffix appended to the sub-application name to disambiguate multiple expose traits."},
		"networkPolicy":            schemaNetworkPolicy(true),
	}
}

// LowerTrait dispatches to an emitted "ingress" or "httproute" trait based on
// controllerType. It also implements platform-managed TLS (ingress path) and
// hostname validation (both paths), consuming the certManagerClusterIssuer/
// allowedHostnameWildcard capability keys so they never leak into the low-level
// handlers — the same responsibilities ExposeHandler.Apply had, ported to
// lowering: lctx.Component.Name replaces app.Name (identical value,
// transform.go's former ExposeHandler.Apply call site), and the emitted Trait
// replaces the direct IngressHandler/HTTPRouteHandler.Apply call — the engine
// dispatches to those handlers itself once the emitted trait settles.
func (ExposeRule) LowerTrait(trait *oam.Trait, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	componentName := lctx.Component.Name

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
		if err := validateHostnames(uniqueStrings(append(ruleHosts(props), shorthand...)), wildcard, componentName); err != nil {
			return oam.LoweringResult{}, err
		}
		// expose is platform-managed: the user does not author the TLS block, only
		// (optionally) the managed secret's name. Present-but-wrong-typed/empty is an
		// error, not a silent fallback to <component>-tls (which would name-collide).
		var secretName string
		if raw, present := props["secretName"]; present {
			s, ok := raw.(string)
			if !ok || s == "" {
				return oam.LoweringResult{}, &errors.ValidationError{
					Field:     "secretName",
					Component: componentName,
					Message:   "secretName must be a non-empty string",
				}
			}
			secretName = s
		}
		delete(props, "secretName")
		delete(props, "tls")
		if issuer != "" {
			if err := setClusterIssuerAnnotation(props, issuer, componentName); err != nil {
				return oam.LoweringResult{}, err
			}
			// TLS covers the effective routing hosts only. When both `rules` and
			// `hostnames` are supplied, `rules` drives routing, so a hostnames entry
			// that is not routed must not get a synthesized certificate.
			if routingHosts := uniqueStrings(ruleHosts(props)); len(routingHosts) > 0 {
				props["tls"] = synthesizedIngressTLS(routingHosts, componentName, secretName)
			}
		} else if secretName != "" {
			// No cluster-issuer capability → no synthesized TLS, so an authored
			// secretName would be silently dropped. Reject instead.
			return oam.LoweringResult{}, &errors.ValidationError{
				Field:     "secretName",
				Component: componentName,
				Message:   "secretName requires platform-managed TLS (no cert-manager cluster-issuer capability)",
			}
		}
		// ssl-redirect / force-ssl-redirect: typed property (capability default or
		// inline override) wins over a same-key raw annotation.
		setSSLRedirectAnnotations(props)
		// external-auth: when the trait authors allowedGroups, inject the nginx
		// auth-* annotations from the capability rendering.
		if err := setAuthAnnotations(props, componentName); err != nil {
			return oam.LoweringResult{}, err
		}
		return oam.LoweringResult{Traits: []oam.Trait{{Type: "ingress", Properties: props}}}, nil
	case "gateway":
		// These properties are nginx-ingress-specific; reject them inline on the
		// gateway path (the rendering guard only covers the capability-supplied form).
		for _, k := range []string{"sslRedirect", "forceSslRedirect", "allowedGroups", "authSigninURL", "secretName"} {
			if _, ok := props[k]; ok {
				return oam.LoweringResult{}, &errors.ValidationError{
					Field:     k,
					Component: componentName,
					Message:   k + " is only valid when controllerType is \"ingress\"",
				}
			}
		}
		if err := validateHostnames(hostnameList(props), wildcard, componentName); err != nil {
			return oam.LoweringResult{}, err
		}
		gatewayName, _ := props["gatewayName"].(string)
		gatewayNamespace, _ := props["gatewayNamespace"].(string)
		delete(props, "gatewayName")
		delete(props, "gatewayNamespace")
		props["parentRefs"] = []any{synthesizeParentRef(gatewayName, gatewayNamespace)}
		return oam.LoweringResult{Traits: []oam.Trait{{Type: "httproute", Properties: props}}}, nil
	default:
		return oam.LoweringResult{}, errors.Errorf("expose trait: unsupported controllerType %q", controllerType)
	}
}
