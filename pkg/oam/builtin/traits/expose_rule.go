package traits

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// ExposeRule lowers an "expose" trait (D5, D1 trait position) into a resolved,
// terminal "ingress" or "httproute" trait, dispatching on the controllerType
// capability value. It is the C6 port of the former ExposeHandler.Apply
// (deleted, expose.go): the engine merges capability rendering into the trait
// before calling LowerTrait (lowering.go), so this rule reads trait.Properties
// exactly as ExposeHandler.Apply once read them post-merge — only the two direct
// handler calls at the end of each branch changed, from invoking
// IngressHandler/HTTPRouteHandler.Apply directly to emitting a Trait for the
// engine's fixpoint to dispatch on the next round.
//
// The package-level helpers ExposeHandler.Apply used (ruleHosts, hostnameList,
// exposeAnnotations (once setClusterIssuerAnnotation), expandHostnamesToIngressRules,
// setSSLRedirectAnnotations, boolProp, setAuthAnnotations, stringList,
// managedIngressTLS (once synthesizedIngressTLS), below) moved here from expose.go
// when ExposeHandler was deleted — this file is now their only caller. The five pre-existing
// direct-construction test files (expose_ext_auth_test.go,
// expose_managed_tls_test.go, expose_secretname_override_test.go,
// expose_shorthand_sslredirect_test.go, traits_test.go) are re-pointed through the
// applyExpose test helper (traits_test.go), which calls ExposeRule.LowerTrait and
// feeds the emitted trait to the real IngressHandler/HTTPRouteHandler.Apply.
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
// the former ExposeHandler.PropertySchema, plus PlatformReserved: true on
// controllerType, certManagerClusterIssuer, allowedHostnameWildcard, authURL,
// authResponseHeaders, gatewayName and gatewayNamespace — these seven are
// capability-injected only and must never be authored inline (D3). This closes the
// enforcement gap the D3 call sites exist to catch: createApplications and
// applyTraits (transform.go) now run enforcePlatformReserved exactly like
// lowering.go's trait-position check already did, so a schema that marks a field
// reserved is actually enforced everywhere capability rendering merges in, not
// just here.
// testdata/webservice-expose-ingress/app.yaml previously authored
// `controllerType: ingress` inline; that line moved out to the fixture's
// cluster.yaml capability rendering (which already supplied it) so the fixture
// itself proves the enforcement is live without changing expected.yaml.
//
// gatewayName/gatewayNamespace were NOT reserved until round-11-batch-2
// (pullrequestreview-4937433461): resolveCapability gives an authored inline
// property precedence over the platform's ClusterProfile rendering (D5), so
// without PlatformReserved an application could author gatewayName inline under
// the gateway controllerType and attach its route to a different Gateway than the
// platform selected — the exact bypass PlatformReserved exists to close for
// controllerType/certManagerClusterIssuer/etc. above.
func (ExposeRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		// controllerType is capability-injected, not user-set (see doc above), so it is
		// NOT user-required here; it is validated in ValidateAndApplyDefaults. Kept in the
		// schema as an optional enum so a value, if present, is type/enum-checked.
		"controllerType":           {Type: oam.PropertyTypeString, Enum: []any{"ingress", "gateway"}, PlatformReserved: true, Description: "Capability-injected controller kind (ingress or gateway) this expose dispatches to."},
		"certManagerClusterIssuer": {Type: oam.PropertyTypeString, PlatformReserved: true, Description: "cert-manager ClusterIssuer used to synthesize TLS (ingress controllerType only)."},
		"secretName":               {Type: oam.PropertyTypeString, Description: "Overrides the synthesized <component>-tls secret name for platform-managed TLS (ingress controllerType only; requires a cert-manager cluster-issuer capability)."},
		"allowedHostnameWildcard":  {Type: oam.PropertyTypeString, PlatformReserved: true, Description: "Platform-reserved wildcard the hostnames must fall under."},
		"gatewayName":              {Type: oam.PropertyTypeString, PlatformReserved: true, Description: "Capability-injected Gateway name used to synthesize parentRefs (gateway controllerType only)."},
		"gatewayNamespace":         {Type: oam.PropertyTypeString, PlatformReserved: true, Default: "gateway-system", Description: "Capability-injected namespace of the Gateway (gateway controllerType only)."},
		"annotations":              {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Additional annotations to set on the generated resource."},
		"rules":                    {Type: oam.PropertyTypeArray, Description: "Ingress-style host rules passed through to the ingress handler.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single ingress-style host rule."}},
		"hostnames":                {Type: oam.PropertyTypeArray, Description: "Hostnames: gateway routes, or an ingress shorthand that expands to one rule per host when rules is absent.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname to route."}},
		"ingressClassName":         {Type: oam.PropertyTypeString, Description: "IngressClass to use (ingress controllerType only)."},
		"sslRedirect":              {Type: oam.PropertyTypeBoolean, Description: "nginx ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"forceSslRedirect":         {Type: oam.PropertyTypeBoolean, Description: "nginx force-ssl-redirect annotation (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"allowedGroups":            {Type: oam.PropertyTypeArray, Description: "oauth2-proxy allowed groups; enables external-auth on the route (ingress controllerType only). Order is preserved.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An allowed oauth2-proxy group."}},
		"authURL":                  {Type: oam.PropertyTypeString, PlatformReserved: true, Description: "Capability-injected nginx external-auth endpoint base (ingress controllerType only)."},
		"authSigninURL":            {Type: oam.PropertyTypeString, Description: "nginx auth-signin URL (ingress controllerType only); platform default via capability rendering, override-able inline."},
		"authResponseHeaders":      {Type: oam.PropertyTypeString, PlatformReserved: true, Description: "Capability-injected nginx auth-response-headers value (ingress controllerType only)."},
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

	// The annotations the rule writes itself reach the ingress trait apart from
	// the authored ones, as its platformAnnotations, and the TLS entry it manages
	// as its managedTLS: only what the rule writes may arrive under those keys,
	// on either rendering.
	for _, reserved := range []string{platformAnnotationsProperty, managedTLSProperty} {
		if _, authored := props[reserved]; authored {
			return oam.LoweringResult{}, &errors.ValidationError{
				Field:     reserved,
				Component: componentName,
				Message:   reserved + " is not a property of the expose trait: the trait writes it on the ingress trait itself",
			}
		}
	}

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
			if err := checkAuthoredObjectName("secretName", "the managed TLS Secret", s); err != nil {
				return oam.LoweringResult{}, &errors.ValidationError{
					Field:     "secretName",
					Component: componentName,
					Message:   err.Error(),
				}
			}
			secretName = s
		}
		delete(props, "secretName")
		delete(props, "tls")
		authoredAnnotations, _ := props["annotations"].(map[string]any)
		platform := &exposeAnnotations{component: componentName, authored: authoredAnnotations}
		var managed map[string]any
		if issuer != "" {
			if err := platform.set(clusterIssuerAnnotation, issuer, "the certManagerClusterIssuer capability value"); err != nil {
				return oam.LoweringResult{}, err
			}
			// TLS covers the effective routing hosts only. When both `rules` and
			// `hostnames` are supplied, `rules` drives routing, so a hostnames entry
			// that is not routed must not get a synthesized certificate.
			if routingHosts := uniqueStrings(ruleHosts(props)); len(routingHosts) > 0 {
				managed = managedIngressTLS(routingHosts, secretName)
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
		// ssl-redirect / force-ssl-redirect: written from the typed property
		// (capability default or inline override). A raw annotation of the same key
		// with another value is refused.
		if err := setSSLRedirectAnnotations(props, platform); err != nil {
			return oam.LoweringResult{}, err
		}
		// external-auth: when the trait authors allowedGroups, inject the nginx
		// auth-* annotations from the capability rendering.
		if err := setAuthAnnotations(props, platform); err != nil {
			return oam.LoweringResult{}, err
		}
		emitted := oam.Trait{Type: "ingress", Properties: props}
		if len(platform.written) > 0 {
			// Recorded as rendered, so the ingress trait's D3 check accepts the
			// reserved key whether or not the engine marks this rule's output
			// synthesized.
			if err := emitted.RenderReserved(platformAnnotationsProperty, platform.written); err != nil {
				return oam.LoweringResult{}, errors.Wrap(err, "expose trait")
			}
		}
		if managed != nil {
			// The managed TLS entry reaches the ingress trait on the same guarded
			// channel: its handler resolves the Secret's name (NameRoleTLSSecret).
			if err := emitted.RenderReserved(managedTLSProperty, managed); err != nil {
				return oam.LoweringResult{}, errors.Wrap(err, "expose trait")
			}
		}
		return oam.LoweringResult{Traits: []oam.Trait{emitted}}, nil
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
		gatewayHostnames := hostnameList(props)
		if err := validateHostnames(gatewayHostnames, wildcard, componentName); err != nil {
			return oam.LoweringResult{}, err
		}
		// hostnames shorthand, gateway half: when hostnames is set and rules is not,
		// synthesize a catch-all rule. HTTPRouteHandler declares rules required, so
		// without this the shorthand emits an httproute trait that cannot validate.
		// Two deliberate differences from the ingress branch above: hostnames is KEPT,
		// because it is a native HTTPRoute field the handler consumes rather than
		// something folded into the rules; and one rule covers every hostname, since a
		// route matches its hostnames at the route level, not per rule. The rule needs
		// no keys — HTTPRouteHandler defaults a rule with no matches to match-all and
		// one with no backendRefs to the component's own service and port.
		if len(gatewayHostnames) > 0 {
			if _, hasRules := props["rules"]; !hasRules {
				props["rules"] = []any{map[string]any{}}
			}
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

// exposeAnnotations collects the annotations the expose rule writes on the
// Ingress itself: the cert-manager cluster-issuer, the nginx ssl-redirect pair
// and the nginx external-auth three, each from the platform's capability
// rendering or from a typed property of the trait. They reach the emitted
// ingress trait as its platformAnnotations, apart from the authored annotations,
// so the check of the consumer's reserved metadata keys tells them from what an
// author wrote (go-kure/launcher#790).
type exposeAnnotations struct {
	component string
	// authored is the trait's own annotations property. It is read, never
	// written: the map is the authored trait's.
	authored map[string]any
	// written holds what the rule writes, nil while it has written nothing.
	written map[string]any
}

// set records key: value as an annotation the rule writes; source names what it
// writes it from. The rule's value is authoritative: an authored annotation of
// the same key holding another value is refused, naming both, and never
// overridden silently. One holding the same value says the same thing and
// stays.
func (a *exposeAnnotations) set(key, value, source string) error {
	if existing, ok := a.authored[key]; ok && !oam.IsNullValue(existing) {
		// Compared as the ingress trait reads an authored value.
		if got := fmt.Sprintf("%v", existing); got != value {
			return &errors.ValidationError{
				Field:     "annotations." + key,
				Value:     got,
				Component: a.component,
				Message: fmt.Sprintf("annotation %s is platform-managed by the expose trait and cannot be overridden: "+
					"the trait writes %q from %s; remove the annotation", key, value, source),
			}
		}
	}
	if a.written == nil {
		a.written = map[string]any{}
	}
	a.written[key] = value
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
// is authoritative: a raw annotation of the same key with another value is refused
// (exposeAnnotations.set). When the property is absent, an existing raw annotation
// is left untouched. The property keys are consumed here so they never reach
// IngressHandler.
func setSSLRedirectAnnotations(props map[string]any, platform *exposeAnnotations) error {
	if v, ok := boolProp(props, "sslRedirect"); ok {
		if err := platform.set(sslRedirectAnnotation, strconv.FormatBool(v), "the sslRedirect property (its own, else the capability's default)"); err != nil {
			return err
		}
	}
	if v, ok := boolProp(props, "forceSslRedirect"); ok {
		if err := platform.set(forceSSLRedirectAnnotation, strconv.FormatBool(v), "the forceSslRedirect property (its own, else the capability's default)"); err != nil {
			return err
		}
	}
	delete(props, "sslRedirect")
	delete(props, "forceSslRedirect")
	return nil
}

// boolProp reads a boolean property; ok is false when absent or not a bool.
func boolProp(props map[string]any, key string) (val, ok bool) {
	val, ok = props[key].(bool)
	return val, ok
}

// setAuthAnnotations writes the nginx external-auth annotations (auth-url, auth-signin,
// auth-response-headers) when the expose trait authors allowedGroups. The auth-url base,
// signin default, and response-headers come from the capability rendering (authURL and
// authResponseHeaders are platform-reserved; authSigninURL is override-able inline);
// allowedGroups is authored, order preserved. The typed values are authoritative: a
// raw annotation of the same key with another value is refused
// (exposeAnnotations.set). All auth-* keys are consumed here so they never reach
// IngressHandler.
func setAuthAnnotations(props map[string]any, platform *exposeAnnotations) error {
	component := platform.component
	rawGroups, hasGroups := props["allowedGroups"]
	authURL, _ := props["authURL"].(string)
	signin, _ := props["authSigninURL"].(string)
	respHeaders, _ := props["authResponseHeaders"].(string)
	delete(props, "allowedGroups")
	delete(props, "authURL")
	delete(props, "authSigninURL")
	delete(props, "authResponseHeaders")

	if !hasGroups {
		return nil // no ext-auth requested; discard any capability-injected auth-* keys
	}
	groups := stringList(rawGroups)
	if len(groups) == 0 {
		return &errors.ValidationError{
			Field:     "allowedGroups",
			Component: component,
			Message:   "allowedGroups must not be empty (ext-auth requires at least one group)",
		}
	}
	if authURL == "" {
		return &errors.ValidationError{
			Field:     "allowedGroups",
			Component: component,
			Message:   "ext-auth is not offered on this platform (the expose capability has no authURL)",
		}
	}

	if err := platform.set(authURLAnnotation, authURL+"?allowed_groups="+strings.Join(groups, ","),
		"its allowedGroups property and the authURL capability value"); err != nil {
		return err
	}
	if signin != "" {
		if err := platform.set(authSigninAnnotation, signin, "the authSigninURL property (its own, else the capability's default)"); err != nil {
			return err
		}
	}
	if respHeaders != "" {
		if err := platform.set(authResponseHeadersAnnotation, respHeaders, "the authResponseHeaders capability value"); err != nil {
			return err
		}
	}
	return nil
}

// stringList converts an OAM array value ([]any of strings) to []string, preserving
// order and dropping empty entries.
func stringList(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// managedIngressTLS builds the value of the ingress trait's managedTLS property:
// the single managed TLS entry, all hosts under one Secret, for cert-manager's
// ingress-shim. secretName is the trait's authored override, left out when the
// author wrote none: the ingress trait's handler resolves the name
// (managedTLSSecretName).
func managedIngressTLS(hosts []string, secretName string) map[string]any {
	anyHosts := make([]any, len(hosts))
	for i, h := range hosts {
		anyHosts[i] = h
	}
	managed := map[string]any{"hosts": anyHosts}
	if secretName != "" {
		managed["secretName"] = secretName
	}
	return managed
}
