package traits

import (
	"fmt"
	"time"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// HTTPRouteHandler handles OAM httproute traits.
type HTTPRouteHandler struct{}

// CanHandle returns true for httproute trait type.
func (h *HTTPRouteHandler) CanHandle(traitType string) bool {
	return traitType == "httproute"
}

// Apply creates an HTTPRoute resource for the component's service.
// If the optional 'name' property is set, that value is used as the sub-application
// name, enabling multiple httproute traits on the same component without collision.
func (h *HTTPRouteHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	subAppName := app.Name + "-httproute"
	if config.Name != "" {
		subAppName = config.Name
	} else if config.Scope != "" {
		subAppName = app.Name + "-httproute-" + config.Scope
	}
	routeApp := stack.NewApplication(
		subAppName,
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, routeApp)
	return nil
}

// synthesizeParentRef builds a Gateway API parentRef map from a gateway name and
// (optional) namespace, applying the "gateway-system" default. Shared by the
// httproute trait's capability synthesis and the expose trait's gateway path.
func synthesizeParentRef(name, namespace string) map[string]any {
	if namespace == "" {
		namespace = "gateway-system"
	}
	return map[string]any{"name": name, "namespace": namespace}
}

func (h *HTTPRouteHandler) parseProperties(props map[string]any, app *stack.Application) (*HTTPRouteConfig, error) {
	defaultPort := resolveDefaultPort(app)
	config := &HTTPRouteConfig{
		ComponentName: app.Name,
	}

	// Trait-level explicit backend — same semantics as ingress: servicePort first,
	// then serviceName; both guarded against components with a known service port.
	traitPortProvided := false
	defaultServiceName := resolveServiceName(app)
	if _, hasServicePort := props["servicePort"]; hasServicePort {
		sp, ok := toIngressPort(props["servicePort"])
		if !ok {
			return nil, errors.Errorf("servicePort must be a valid port number (1–65535)")
		}
		if existingPort := resolveDefaultPort(app); existingPort > 0 {
			return nil, errors.Errorf(
				"servicePort may not be set on a component that already exposes service port %d; "+
					"use backendRef-level 'port' or an explicit 'name' instead",
				existingPort)
		}
		defaultPort = sp
		traitPortProvided = true
	}
	if sn, ok := props["serviceName"].(string); ok && sn != "" {
		if !traitPortProvided {
			return nil, errors.Errorf("serviceName requires a valid servicePort to also be set on the trait")
		}
		defaultServiceName = sn
	}

	// Optional: name (for multiple httproute traits on the same component)
	if name, ok := props["name"].(string); ok && name != "" {
		config.Name = name
	}

	// Optional: scope — sub-app name becomes {component}-httproute-{scope} when
	// set and name is empty, enabling multiple httproute traits per component.
	if scope, ok := props["scope"].(string); ok && scope != "" {
		config.Scope = scope
	}

	// parentRefs: user-authored take precedence; otherwise synthesize a single ref
	// from the gatewayName/gatewayNamespace capability fields (crane#235 D4). This
	// is optional-capability: a plain httproute with an explicit parentRefs still
	// works with no capability.
	rawParentRefs, ok := props["parentRefs"].([]any)
	if !ok || len(rawParentRefs) == 0 {
		gatewayName, _ := props["gatewayName"].(string)
		if gatewayName == "" {
			return nil, errors.New("required property 'parentRefs' missing or empty (and no gatewayName capability to synthesize from)")
		}
		gatewayNamespace, _ := props["gatewayNamespace"].(string)
		rawParentRefs = []any{synthesizeParentRef(gatewayName, gatewayNamespace)}
	}
	for i, rawRef := range rawParentRefs {
		refMap, ok := rawRef.(map[string]any)
		if !ok {
			return nil, errors.Errorf("parentRefs[%d]: expected object", i)
		}
		name, ok := refMap["name"].(string)
		if !ok || name == "" {
			return nil, errors.Errorf("parentRefs[%d]: required field 'name' missing or not a string", i)
		}
		ref := ParentRef{Name: name}
		if ns, ok := refMap["namespace"].(string); ok {
			ref.Namespace = ns
		}
		config.ParentRefs = append(config.ParentRefs, ref)
	}

	// Optional: hostnames
	if rawHostnames, ok := props["hostnames"].([]any); ok {
		for _, rawHost := range rawHostnames {
			s, ok := rawHost.(string)
			if !ok {
				return nil, errors.New("hostnames entries must be strings")
			}
			config.Hostnames = append(config.Hostnames, s)
		}
	}

	// Required: rules
	rawRules, ok := props["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		return nil, errors.New("required property 'rules' missing or empty")
	}
	for i, rawRule := range rawRules {
		ruleMap, ok := rawRule.(map[string]any)
		if !ok {
			return nil, errors.Errorf("rules[%d]: expected object", i)
		}

		rule := HTTPRouteRule{}

		// Optional: matches
		if rawMatches, ok := ruleMap["matches"].([]any); ok {
			for j, rawMatch := range rawMatches {
				matchMap, ok := rawMatch.(map[string]any)
				if !ok {
					return nil, errors.Errorf("rules[%d].matches[%d]: expected object", i, j)
				}

				match := HTTPRouteMatch{}

				// Optional: path
				if rawPath, ok := matchMap["path"].(map[string]any); ok {
					pm := &PathMatch{
						Type:  "PathPrefix",
						Value: "/",
					}
					if t, ok := rawPath["type"].(string); ok {
						pm.Type = t
					}
					if v, ok := rawPath["value"].(string); ok {
						pm.Value = v
					}
					match.Path = pm
				}

				// Optional: headers
				if rawHeaders, ok := matchMap["headers"].([]any); ok {
					for k, rawHeader := range rawHeaders {
						headerMap, ok := rawHeader.(map[string]any)
						if !ok {
							return nil, errors.Errorf("rules[%d].matches[%d].headers[%d]: expected object", i, j, k)
						}
						hm := HeaderMatch{
							Type: "Exact",
						}
						if t, ok := headerMap["type"].(string); ok {
							hm.Type = t
						}
						name, ok := headerMap["name"].(string)
						if !ok || name == "" {
							return nil, errors.Errorf("rules[%d].matches[%d].headers[%d]: required field 'name' missing", i, j, k)
						}
						hm.Name = name
						value, ok := headerMap["value"].(string)
						if !ok {
							return nil, errors.Errorf("rules[%d].matches[%d].headers[%d]: required field 'value' missing", i, j, k)
						}
						hm.Value = value
						match.Headers = append(match.Headers, hm)
					}
				}

				rule.Matches = append(rule.Matches, match)
			}
		}

		// Optional: backendRefs
		if rawBackends, ok := ruleMap["backendRefs"].([]any); ok {
			for j, rawBackend := range rawBackends {
				backendMap, ok := rawBackend.(map[string]any)
				if !ok {
					return nil, errors.Errorf("rules[%d].backendRefs[%d]: expected object", i, j)
				}
				// nameExplicit is true only when the backendRef names a DIFFERENT service
				// than the component's own. A self-reference is treated as implicit so
				// the same port-mismatch guard applies.
				selfServiceName := defaultServiceName
				nameExplicit := false
				br := BackendRef{
					Name: selfServiceName,
					Port: defaultPort,
				}
				if name, ok := backendMap["name"].(string); ok {
					br.Name = name
					if name != selfServiceName {
						nameExplicit = true
					}
				}
				portExplicit := false
				if port, ok := backendMap["port"].(float64); ok {
					br.Port = int32(port) //nolint:gosec
					portExplicit = true
				} else if port, ok := backendMap["port"].(int); ok {
					br.Port = int32(port) //nolint:gosec
					portExplicit = true
				}
				if nameExplicit && !portExplicit {
					br.Port = 0
				}
				if !nameExplicit {
					if !traitPortProvided {
						if err := checkImplicitBackend(app, fmt.Sprintf("rules[%d].backendRefs[%d]", i, j)); err != nil {
							return nil, err
						}
					}
					if portExplicit && br.Port != defaultPort {
						return nil, errors.Errorf(
							"rules[%d].backendRefs[%d]: cannot route implicit backend to port %d — component service exposes port %d; specify an explicit backend name or match the component port",
							i, j, br.Port, defaultPort)
					}
				}
				if br.Port == 0 {
					return nil, errors.Errorf(
						"rules[%d].backendRefs[%d]: cannot determine backend port — specify 'port' in the backendRef",
						i, j)
				}
				rule.BackendRefs = append(rule.BackendRefs, br)
			}
		} else {
			// Default: single backend pointing to the component's service
			if !traitPortProvided {
				if err := checkImplicitBackend(app, fmt.Sprintf("rules[%d]", i)); err != nil {
					return nil, err
				}
			}
			if defaultPort == 0 {
				return nil, errors.Errorf(
					"rules[%d]: cannot determine backend port for component %q — configure the component port or specify backendRefs[].port",
					i, app.Name)
			}
			rule.BackendRefs = []BackendRef{{Name: defaultServiceName, Port: defaultPort}}
		}

		// Optional: filters
		filters, err := parseRuleFilters(ruleMap, i)
		if err != nil {
			return nil, err
		}
		rule.Filters = filters

		// Optional: timeouts
		timeouts, err := parseRuleTimeouts(ruleMap, i)
		if err != nil {
			return nil, err
		}
		rule.Timeouts = timeouts

		config.Rules = append(config.Rules, rule)
	}

	// Platform-reserved auto-NetworkPolicy inputs (populated by capability rendering).
	sources, err := parseTrafficSources(props, app.Name, "httproute")
	if err != nil {
		return nil, err
	}
	config.sources = sources
	config.ports = collectHTTPRoutePorts(config, defaultServiceName)

	return config, nil
}

// parseRuleFilters parses `rules[i].filters[]`, returning nil when the
// key is absent. Each filter is a discriminated union on `type`; the
// matching nested object must be present and non-empty. ExtensionRef
// is not implemented and returns a structured error.
func parseRuleFilters(ruleMap map[string]any, ruleIdx int) ([]HTTPRouteFilter, error) {
	raw, ok := ruleMap["filters"].([]any)
	if !ok {
		return nil, nil
	}
	var out []HTTPRouteFilter
	for j, rawFilter := range raw {
		filterMap, ok := rawFilter.(map[string]any)
		if !ok {
			return nil, errors.Errorf("rules[%d].filters[%d]: expected object", ruleIdx, j)
		}
		filterType, _ := filterMap["type"].(string)
		if filterType == "" {
			return nil, errors.Errorf("rules[%d].filters[%d]: type is required", ruleIdx, j)
		}
		filter := HTTPRouteFilter{Type: filterType}
		scope := errors.Errorf("rules[%d].filters[%d] %q", ruleIdx, j, filterType).Error()
		switch filterType {
		case "RequestRedirect":
			cfg, err := parseRequestRedirect(filterMap, scope)
			if err != nil {
				return nil, err
			}
			filter.RequestRedirect = cfg
		case "RequestHeaderModifier":
			cfg, err := parseHeaderModifier(filterMap, "requestHeaderModifier", scope)
			if err != nil {
				return nil, err
			}
			filter.RequestHeaderModifier = cfg
		case "ResponseHeaderModifier":
			cfg, err := parseHeaderModifier(filterMap, "responseHeaderModifier", scope)
			if err != nil {
				return nil, err
			}
			filter.ResponseHeaderModifier = cfg
		case "URLRewrite":
			cfg, err := parseURLRewrite(filterMap, scope)
			if err != nil {
				return nil, err
			}
			filter.URLRewrite = cfg
		case "RequestMirror":
			cfg, err := parseRequestMirror(filterMap, scope)
			if err != nil {
				return nil, err
			}
			filter.RequestMirror = cfg
		case "CORS":
			cfg, err := parseCORS(filterMap, scope)
			if err != nil {
				return nil, err
			}
			filter.CORS = cfg
		case "ExternalAuth":
			cfg, err := parseExternalAuth(filterMap, scope)
			if err != nil {
				return nil, err
			}
			filter.ExternalAuth = cfg
		case "ExtensionRef":
			return nil, errors.Errorf("%s: filter type %q is not implemented", scope, filterType)
		default:
			return nil, errors.Errorf("%s: unknown filter type %q", scope, filterType)
		}
		out = append(out, filter)
	}
	return out, nil
}

// parseRequestRedirect parses the `requestRedirect` nested object.
// Gateway API marks all fields optional, but a filter with zero
// populated fields is a no-op — we reject it.
func parseRequestRedirect(filterMap map[string]any, scope string) (*HTTPRequestRedirect, error) {
	raw, ok := filterMap["requestRedirect"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: requestRedirect block is required", scope)
	}
	rr := &HTTPRequestRedirect{}
	if scheme, ok := raw["scheme"].(string); ok && scheme != "" {
		if scheme != "http" && scheme != "https" {
			return nil, errors.Errorf("%s: scheme %q must be \"http\" or \"https\"", scope, scheme)
		}
		rr.Scheme = scheme
	}
	if hostname, ok := raw["hostname"].(string); ok {
		rr.Hostname = hostname
	}
	if rawPort, ok := raw["port"]; ok {
		port, err := coerceInt32(rawPort)
		if err != nil {
			return nil, errors.Errorf("%s: port: %w", scope, err)
		}
		if port < 1 || port > 65535 {
			return nil, errors.Errorf("%s: port %d out of range [1,65535]", scope, port)
		}
		rr.Port = &port
	}
	if rawCode, ok := raw["statusCode"]; ok {
		code, err := coerceInt(rawCode)
		if err != nil {
			return nil, errors.Errorf("%s: statusCode: %w", scope, err)
		}
		if !isAllowedRedirectStatus(code) {
			return nil, errors.Errorf("%s: statusCode %d must be one of [301,302,303,307,308]", scope, code)
		}
		rr.StatusCode = &code
	}
	if rawPath, ok := raw["path"].(map[string]any); ok {
		pm, err := parsePathModifier(rawPath, scope)
		if err != nil {
			return nil, err
		}
		rr.Path = pm
	}
	if rr.Scheme == "" && rr.Hostname == "" && rr.Port == nil && rr.StatusCode == nil && rr.Path == nil {
		return nil, errors.Errorf("%s: at least one of scheme/hostname/port/statusCode/path must be set", scope)
	}
	return rr, nil
}

// parseURLRewrite parses the `urlRewrite` nested object. At least one
// of hostname or path must be set.
func parseURLRewrite(filterMap map[string]any, scope string) (*HTTPURLRewrite, error) {
	raw, ok := filterMap["urlRewrite"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: urlRewrite block is required", scope)
	}
	rw := &HTTPURLRewrite{}
	if hostname, ok := raw["hostname"].(string); ok {
		rw.Hostname = hostname
	}
	if rawPath, ok := raw["path"].(map[string]any); ok {
		pm, err := parsePathModifier(rawPath, scope)
		if err != nil {
			return nil, err
		}
		rw.Path = pm
	}
	if rw.Hostname == "" && rw.Path == nil {
		return nil, errors.Errorf("%s: at least one of hostname or path must be set", scope)
	}
	return rw, nil
}

// parseRequestMirror parses the `requestMirror` nested object.
// backendRef.name and backendRef.port are required. Only one of
// percent or fraction may be set.
func parseRequestMirror(filterMap map[string]any, scope string) (*HTTPRequestMirror, error) {
	raw, ok := filterMap["requestMirror"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: requestMirror block is required", scope)
	}
	rawBR, ok := raw["backendRef"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: requestMirror.backendRef is required", scope)
	}
	name, _ := rawBR["name"].(string)
	if name == "" {
		return nil, errors.Errorf("%s: requestMirror.backendRef.name is required", scope)
	}
	portRaw, ok := rawBR["port"]
	if !ok {
		return nil, errors.Errorf("%s: requestMirror.backendRef.port is required", scope)
	}
	port, err := coerceInt32(portRaw)
	if err != nil {
		return nil, errors.Errorf("%s: requestMirror.backendRef.port: %w", scope, err)
	}
	if port < 1 || port > 65535 {
		return nil, errors.Errorf("%s: requestMirror.backendRef.port %d out of range [1,65535]", scope, port)
	}
	m := &HTTPRequestMirror{BackendRef: MirrorBackendRef{Name: name, Port: port}}

	hasPercent := false
	hasFraction := false
	if rawPct, ok := raw["percent"]; ok {
		hasPercent = true
		pct, err := coerceInt32(rawPct)
		if err != nil {
			return nil, errors.Errorf("%s: requestMirror.percent: %w", scope, err)
		}
		if pct < 0 || pct > 100 {
			return nil, errors.Errorf("%s: requestMirror.percent %d must be in [0,100]", scope, pct)
		}
		m.Percent = &pct
	}
	if rawFrac, ok := raw["fraction"].(map[string]any); ok {
		hasFraction = true
		rawNum, ok := rawFrac["numerator"]
		if !ok {
			return nil, errors.Errorf("%s: requestMirror.fraction.numerator is required", scope)
		}
		num, err := coerceInt32(rawNum)
		if err != nil {
			return nil, errors.Errorf("%s: requestMirror.fraction.numerator: %w", scope, err)
		}
		if num < 0 {
			return nil, errors.Errorf("%s: requestMirror.fraction.numerator must be >= 0", scope)
		}
		frac := &HTTPFraction{Numerator: num}
		if rawDen, ok := rawFrac["denominator"]; ok {
			den, err := coerceInt32(rawDen)
			if err != nil {
				return nil, errors.Errorf("%s: requestMirror.fraction.denominator: %w", scope, err)
			}
			if den < 1 {
				return nil, errors.Errorf("%s: requestMirror.fraction.denominator must be >= 1", scope)
			}
			frac.Denominator = &den
		}
		m.Fraction = frac
	}
	if hasPercent && hasFraction {
		return nil, errors.Errorf("%s: only one of percent or fraction may be specified", scope)
	}
	return m, nil
}

// parseCORS parses the `cors` nested object. All fields are optional per
// Gateway API, but an empty cors block is rejected as a no-op.
func parseCORS(filterMap map[string]any, scope string) (*HTTPCORS, error) {
	raw, ok := filterMap["cors"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: cors block is required", scope)
	}
	c := &HTTPCORS{}
	if rawOrigins, ok := raw["allowOrigins"].([]any); ok {
		for i, v := range rawOrigins {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, errors.Errorf("%s: cors.allowOrigins[%d]: expected non-empty string", scope, i)
			}
			c.AllowOrigins = append(c.AllowOrigins, s)
		}
	}
	if rawCred, ok := raw["allowCredentials"].(bool); ok {
		c.AllowCredentials = &rawCred
	}
	if rawMethods, ok := raw["allowMethods"].([]any); ok {
		for i, v := range rawMethods {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, errors.Errorf("%s: cors.allowMethods[%d]: expected non-empty string", scope, i)
			}
			c.AllowMethods = append(c.AllowMethods, s)
		}
	}
	if rawHeaders, ok := raw["allowHeaders"].([]any); ok {
		for i, v := range rawHeaders {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, errors.Errorf("%s: cors.allowHeaders[%d]: expected non-empty string", scope, i)
			}
			c.AllowHeaders = append(c.AllowHeaders, s)
		}
	}
	if rawExpose, ok := raw["exposeHeaders"].([]any); ok {
		for i, v := range rawExpose {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, errors.Errorf("%s: cors.exposeHeaders[%d]: expected non-empty string", scope, i)
			}
			c.ExposeHeaders = append(c.ExposeHeaders, s)
		}
	}
	if rawAge, ok := raw["maxAge"]; ok {
		age, err := coerceInt32(rawAge)
		if err != nil {
			return nil, errors.Errorf("%s: cors.maxAge: %w", scope, err)
		}
		if age < 1 {
			return nil, errors.Errorf("%s: cors.maxAge must be >= 1", scope)
		}
		c.MaxAge = &age
	}
	if len(c.AllowOrigins) == 0 && c.AllowCredentials == nil && len(c.AllowMethods) == 0 &&
		len(c.AllowHeaders) == 0 && len(c.ExposeHeaders) == 0 && c.MaxAge == nil {
		return nil, errors.Errorf("%s: cors block must set at least one field", scope)
	}
	return c, nil
}

// parseExternalAuth parses the `externalAuth` nested object.
// protocol ("HTTP" or "GRPC") and backendRef are required.
func parseExternalAuth(filterMap map[string]any, scope string) (*HTTPExternalAuth, error) {
	raw, ok := filterMap["externalAuth"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: externalAuth block is required", scope)
	}
	protocol, _ := raw["protocol"].(string)
	if protocol != "HTTP" && protocol != "GRPC" {
		return nil, errors.Errorf("%s: externalAuth.protocol must be \"HTTP\" or \"GRPC\", got %q", scope, protocol)
	}
	rawBR, ok := raw["backendRef"].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: externalAuth.backendRef is required", scope)
	}
	name, _ := rawBR["name"].(string)
	if name == "" {
		return nil, errors.Errorf("%s: externalAuth.backendRef.name is required", scope)
	}
	portRaw, ok := rawBR["port"]
	if !ok {
		return nil, errors.Errorf("%s: externalAuth.backendRef.port is required", scope)
	}
	port, err := coerceInt32(portRaw)
	if err != nil {
		return nil, errors.Errorf("%s: externalAuth.backendRef.port: %w", scope, err)
	}
	if port < 1 || port > 65535 {
		return nil, errors.Errorf("%s: externalAuth.backendRef.port %d out of range [1,65535]", scope, port)
	}
	ea := &HTTPExternalAuth{
		Protocol:   protocol,
		BackendRef: MirrorBackendRef{Name: name, Port: port},
	}
	if rawGRPC, ok := raw["grpc"].(map[string]any); ok {
		g := &HTTPGRPCAuth{}
		if rawHdrs, ok := rawGRPC["allowedHeaders"].([]any); ok {
			for i, v := range rawHdrs {
				s, ok := v.(string)
				if !ok || s == "" {
					return nil, errors.Errorf("%s: externalAuth.grpc.allowedHeaders[%d]: expected non-empty string", scope, i)
				}
				g.AllowedHeaders = append(g.AllowedHeaders, s)
			}
		}
		ea.GRPC = g
	}
	if rawHTTP, ok := raw["http"].(map[string]any); ok {
		h := &HTTPHTTPAuth{}
		if path, ok := rawHTTP["path"].(string); ok {
			h.Path = path
		}
		if rawHdrs, ok := rawHTTP["allowedHeaders"].([]any); ok {
			for i, v := range rawHdrs {
				s, ok := v.(string)
				if !ok || s == "" {
					return nil, errors.Errorf("%s: externalAuth.http.allowedHeaders[%d]: expected non-empty string", scope, i)
				}
				h.AllowedHeaders = append(h.AllowedHeaders, s)
			}
		}
		if rawResp, ok := rawHTTP["allowedResponseHeaders"].([]any); ok {
			for i, v := range rawResp {
				s, ok := v.(string)
				if !ok || s == "" {
					return nil, errors.Errorf("%s: externalAuth.http.allowedResponseHeaders[%d]: expected non-empty string", scope, i)
				}
				h.AllowedResponseHeaders = append(h.AllowedResponseHeaders, s)
			}
		}
		ea.HTTP = h
	}
	if rawFwd, ok := raw["forwardBody"].(map[string]any); ok {
		fb := &HTTPForwardBody{}
		if rawSize, ok := rawFwd["maxSize"]; ok {
			size, err := coerceInt32(rawSize)
			if err != nil {
				return nil, errors.Errorf("%s: externalAuth.forwardBody.maxSize: %w", scope, err)
			}
			if size < 0 {
				return nil, errors.Errorf("%s: externalAuth.forwardBody.maxSize must be >= 0", scope)
			}
			fb.MaxSize = uint16(size) //nolint:gosec
		}
		ea.ForwardBody = fb
	}
	return ea, nil
}

// parsePathModifier parses the shared path-rewrite shape used by
// RequestRedirect.path and URLRewrite.path.
func parsePathModifier(raw map[string]any, scope string) (*HTTPPathModifier, error) {
	typ, _ := raw["type"].(string)
	if typ == "" {
		return nil, errors.Errorf("%s: path.type is required", scope)
	}
	pm := &HTTPPathModifier{Type: typ}
	switch typ {
	case "ReplaceFullPath":
		v, ok := raw["replaceFullPath"].(string)
		if !ok || v == "" {
			return nil, errors.Errorf("%s: path.replaceFullPath is required when type=ReplaceFullPath", scope)
		}
		pm.ReplaceFullPath = v
	case "ReplacePrefixMatch":
		v, ok := raw["replacePrefixMatch"].(string)
		if !ok || v == "" {
			return nil, errors.Errorf("%s: path.replacePrefixMatch is required when type=ReplacePrefixMatch", scope)
		}
		pm.ReplacePrefixMatch = v
	default:
		return nil, errors.Errorf("%s: path.type %q must be one of [ReplaceFullPath,ReplacePrefixMatch]", scope, typ)
	}
	return pm, nil
}

// parseHeaderModifier parses the shared shape used by both
// RequestHeaderModifier and ResponseHeaderModifier filters.
func parseHeaderModifier(filterMap map[string]any, key, scope string) (*HTTPHeaderModifier, error) {
	raw, ok := filterMap[key].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: %s block is required", scope, key)
	}
	hm := &HTTPHeaderModifier{}
	if rawSet, ok := raw["set"].([]any); ok {
		kvs, err := parseHeaderKVList(rawSet, scope+".set")
		if err != nil {
			return nil, err
		}
		hm.Set = kvs
	}
	if rawAdd, ok := raw["add"].([]any); ok {
		kvs, err := parseHeaderKVList(rawAdd, scope+".add")
		if err != nil {
			return nil, err
		}
		hm.Add = kvs
	}
	if rawRemove, ok := raw["remove"].([]any); ok {
		for i, r := range rawRemove {
			s, ok := r.(string)
			if !ok {
				return nil, errors.Errorf("%s.remove[%d]: expected string", scope, i)
			}
			if s == "" {
				return nil, errors.Errorf("%s.remove[%d]: empty header name", scope, i)
			}
			hm.Remove = append(hm.Remove, s)
		}
	}
	if len(hm.Set) == 0 && len(hm.Add) == 0 && len(hm.Remove) == 0 {
		return nil, errors.Errorf("%s: at least one of set/add/remove must be populated", scope)
	}
	return hm, nil
}

// parseHeaderKVList parses a set[]/add[] list of {name, value} maps.
func parseHeaderKVList(raw []any, scope string) ([]HTTPHeaderKV, error) {
	var out []HTTPHeaderKV
	for i, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.Errorf("%s[%d]: expected object", scope, i)
		}
		name, _ := m["name"].(string)
		if name == "" {
			return nil, errors.Errorf("%s[%d]: name is required", scope, i)
		}
		value, _ := m["value"].(string)
		out = append(out, HTTPHeaderKV{Name: name, Value: value})
	}
	return out, nil
}

// parseRuleTimeouts parses `rules[i].timeouts`, returning nil when the
// block is absent.
func parseRuleTimeouts(ruleMap map[string]any, ruleIdx int) (*HTTPRouteTimeouts, error) {
	raw, ok := ruleMap["timeouts"].(map[string]any)
	if !ok {
		return nil, nil
	}
	t := &HTTPRouteTimeouts{}
	if rawReq, ok := raw["request"].(string); ok && rawReq != "" {
		dur, err := time.ParseDuration(rawReq)
		if err != nil {
			return nil, errors.Errorf("rules[%d].timeouts.request: invalid duration %q: %w", ruleIdx, rawReq, err)
		}
		if dur < 0 {
			return nil, errors.Errorf("rules[%d].timeouts.request: duration %q must be non-negative", ruleIdx, rawReq)
		}
		t.Request = dur
	}
	if rawBR, ok := raw["backendRequest"].(string); ok && rawBR != "" {
		dur, err := time.ParseDuration(rawBR)
		if err != nil {
			return nil, errors.Errorf("rules[%d].timeouts.backendRequest: invalid duration %q: %w", ruleIdx, rawBR, err)
		}
		if dur < 0 {
			return nil, errors.Errorf("rules[%d].timeouts.backendRequest: duration %q must be non-negative", ruleIdx, rawBR)
		}
		t.BackendRequest = dur
	}
	if t.Request == 0 && t.BackendRequest == 0 {
		return nil, errors.Errorf("rules[%d].timeouts: at least one of request or backendRequest must be set", ruleIdx)
	}
	if t.Request > 0 && t.BackendRequest > 0 && t.BackendRequest > t.Request {
		return nil, errors.Errorf("rules[%d].timeouts: backendRequest (%s) must be <= request (%s)", ruleIdx, t.BackendRequest, t.Request)
	}
	return t, nil
}

// coerceInt32 handles both int and float64 JSON number types.
func coerceInt32(v any) (int32, error) {
	switch n := v.(type) {
	case int:
		return int32(n), nil //nolint:gosec
	case int32:
		return n, nil
	case int64:
		return int32(n), nil //nolint:gosec
	case float64:
		return int32(n), nil //nolint:gosec
	default:
		return 0, errors.Errorf("expected number, got %T", v)
	}
}

// coerceInt is the untyped counterpart of coerceInt32.
func coerceInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, errors.Errorf("expected number, got %T", v)
	}
}

// isAllowedRedirectStatus matches the Gateway API enum for
// HTTPRequestRedirectFilter.statusCode.
func isAllowedRedirectStatus(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// HTTPRouteConfig implements stack.ApplicationConfig for httproute traits.
type HTTPRouteConfig struct {
	Name          string // optional, overrides sub-app name for multi-httproute components
	Scope         string // optional; sub-app name becomes {component}-httproute-{scope} when set and Name is empty
	ComponentName string
	ParentRefs    []ParentRef
	Hostnames     []string
	Rules         []HTTPRouteRule

	// sources/ports are populated in parseProperties from the platform-reserved
	// networkPolicy.trafficSources rendering; they drive auto-NetworkPolicy synthesis.
	sources []netpol.TrafficSource
	ports   []intstr.IntOrString
}

// TrafficSources implements the cluster-level trafficSourceCollector contract.
func (c *HTTPRouteConfig) TrafficSources() []netpol.TrafficSource { return c.sources }

// TargetComponentName returns the OAM component label (not the K8s Service name),
// so the synthesized NetworkPolicy selects the component's pods via {app: <name>}.
func (c *HTTPRouteConfig) TargetComponentName() string { return c.ComponentName }

// BackendPorts implements the cluster-level trafficSourceCollector contract.
func (c *HTTPRouteConfig) BackendPorts() []intstr.IntOrString { return c.ports }

// ParentRef represents a gateway parent reference.
type ParentRef struct {
	Name      string
	Namespace string
}

// HTTPRouteRule represents a single routing rule.
type HTTPRouteRule struct {
	Matches     []HTTPRouteMatch
	BackendRefs []BackendRef
	// Filters preserves OAM declaration order; Gateway API output stays stable.
	Filters []HTTPRouteFilter
	// Timeouts is nil when the rule has no timeout block.
	Timeouts *HTTPRouteTimeouts
}

// HTTPRouteFilter is the parsed form of one entry in `rules[].filters[]`.
// It is a discriminated union on Type.
type HTTPRouteFilter struct {
	Type                   string
	RequestRedirect        *HTTPRequestRedirect
	RequestHeaderModifier  *HTTPHeaderModifier
	ResponseHeaderModifier *HTTPHeaderModifier
	URLRewrite             *HTTPURLRewrite
	RequestMirror          *HTTPRequestMirror
	CORS                   *HTTPCORS
	ExternalAuth           *HTTPExternalAuth
}

// HTTPRequestRedirect is the parsed form of the RequestRedirect filter type.
type HTTPRequestRedirect struct {
	Scheme     string
	Hostname   string
	Port       *int32
	StatusCode *int
	Path       *HTTPPathModifier
}

// HTTPURLRewrite is the parsed form of the URLRewrite filter type.
type HTTPURLRewrite struct {
	Hostname string
	Path     *HTTPPathModifier
}

// HTTPPathModifier is the shared shape used by RequestRedirect and URLRewrite.
type HTTPPathModifier struct {
	Type               string // "ReplaceFullPath" or "ReplacePrefixMatch"
	ReplaceFullPath    string
	ReplacePrefixMatch string
}

// HTTPHeaderModifier is the parsed form of RequestHeaderModifier/ResponseHeaderModifier.
type HTTPHeaderModifier struct {
	Set    []HTTPHeaderKV
	Add    []HTTPHeaderKV
	Remove []string
}

// HTTPHeaderKV is a single {name, value} entry in a header-modifier list.
type HTTPHeaderKV struct {
	Name  string
	Value string
}

// HTTPRequestMirror is the parsed form of the RequestMirror filter type.
type HTTPRequestMirror struct {
	BackendRef MirrorBackendRef
	Percent    *int32
	Fraction   *HTTPFraction
}

// MirrorBackendRef is the backend reference for RequestMirror and ExternalAuth.
type MirrorBackendRef struct {
	Name string
	Port int32
}

// HTTPFraction is the fractional mirror weight.
type HTTPFraction struct {
	Numerator   int32
	Denominator *int32
}

// HTTPCORS is the parsed form of the CORS filter type.
type HTTPCORS struct {
	AllowOrigins     []string
	AllowCredentials *bool
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	MaxAge           *int32
}

// HTTPExternalAuth is the parsed form of the ExternalAuth filter type.
type HTTPExternalAuth struct {
	Protocol    string // "HTTP" or "GRPC"
	BackendRef  MirrorBackendRef
	GRPC        *HTTPGRPCAuth
	HTTP        *HTTPHTTPAuth
	ForwardBody *HTTPForwardBody
}

// HTTPGRPCAuth is the optional grpc sub-object for ExternalAuth.
type HTTPGRPCAuth struct {
	AllowedHeaders []string
}

// HTTPHTTPAuth is the optional http sub-object for ExternalAuth.
type HTTPHTTPAuth struct {
	Path                   string
	AllowedHeaders         []string
	AllowedResponseHeaders []string
}

// HTTPForwardBody is the optional forwardBody sub-object for ExternalAuth.
type HTTPForwardBody struct {
	MaxSize uint16
}

// HTTPRouteTimeouts is the parsed form of `rules[].timeouts`.
type HTTPRouteTimeouts struct {
	Request        time.Duration
	BackendRequest time.Duration
}

// HTTPRouteMatch represents match conditions for a rule.
type HTTPRouteMatch struct {
	Path    *PathMatch
	Headers []HeaderMatch
}

// PathMatch represents a path-based match condition.
type PathMatch struct {
	Type  string
	Value string
}

// HeaderMatch represents a header-based match condition.
type HeaderMatch struct {
	Type  string
	Name  string
	Value string
}

// BackendRef represents a backend service reference.
type BackendRef struct {
	Name string
	Port int32
}

// Generate creates a Gateway API HTTPRoute resource.
func (c *HTTPRouteConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	route := kubernetes.CreateHTTPRoute(app.Name, app.Namespace)
	route.Labels = map[string]string{"app": c.ComponentName}
	route.Annotations = nil

	for _, ref := range c.ParentRefs {
		pr := gatewayv1.ParentReference{
			Name: gatewayv1.ObjectName(ref.Name),
		}
		if ref.Namespace != "" {
			ns := gatewayv1.Namespace(ref.Namespace)
			pr.Namespace = &ns
		}
		kubernetes.AddHTTPRouteParentRef(route, pr)
	}

	for _, h := range c.Hostnames {
		kubernetes.AddHTTPRouteHostname(route, gatewayv1.Hostname(h))
	}

	for _, rule := range c.Rules {
		httpRule := gatewayv1.HTTPRouteRule{}

		for _, match := range rule.Matches {
			httpMatch := gatewayv1.HTTPRouteMatch{}

			if match.Path != nil {
				pathType := toGatewayPathType(match.Path.Type)
				value := match.Path.Value
				httpMatch.Path = &gatewayv1.HTTPPathMatch{
					Type:  &pathType,
					Value: &value,
				}
			}

			for _, header := range match.Headers {
				headerType := toGatewayHeaderType(header.Type)
				httpMatch.Headers = append(httpMatch.Headers, gatewayv1.HTTPHeaderMatch{
					Type:  &headerType,
					Name:  gatewayv1.HTTPHeaderName(header.Name),
					Value: header.Value,
				})
			}

			kubernetes.AddHTTPRouteRuleMatch(&httpRule, httpMatch)
		}

		for _, br := range rule.BackendRefs {
			port := br.Port
			kubernetes.AddHTTPRouteRuleBackendRef(&httpRule, gatewayv1.HTTPBackendRef{
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: gatewayv1.BackendObjectReference{
						Name: gatewayv1.ObjectName(br.Name),
						Port: &port,
					},
				},
			})
		}

		for _, f := range rule.Filters {
			gwFilter := buildGatewayFilter(f)
			kubernetes.AddHTTPRouteRuleFilter(&httpRule, gwFilter)
		}
		if rule.Timeouts != nil {
			httpRule.Timeouts = buildGatewayTimeouts(rule.Timeouts)
		}

		kubernetes.AddHTTPRouteRule(route, httpRule)
	}

	obj := client.Object(route)
	return []*client.Object{&obj}, nil
}

func buildGatewayFilter(f HTTPRouteFilter) gatewayv1.HTTPRouteFilter {
	gwFilter := gatewayv1.HTTPRouteFilter{
		Type: toGatewayFilterType(f.Type),
	}
	switch f.Type {
	case "RequestRedirect":
		if f.RequestRedirect != nil {
			gwFilter.RequestRedirect = buildGatewayRequestRedirect(f.RequestRedirect)
		}
	case "RequestHeaderModifier":
		if f.RequestHeaderModifier != nil {
			gwFilter.RequestHeaderModifier = buildGatewayHeaderFilter(f.RequestHeaderModifier)
		}
	case "ResponseHeaderModifier":
		if f.ResponseHeaderModifier != nil {
			gwFilter.ResponseHeaderModifier = buildGatewayHeaderFilter(f.ResponseHeaderModifier)
		}
	case "URLRewrite":
		if f.URLRewrite != nil {
			gwFilter.URLRewrite = buildGatewayURLRewrite(f.URLRewrite)
		}
	case "RequestMirror":
		if f.RequestMirror != nil {
			gwFilter.RequestMirror = buildGatewayRequestMirror(f.RequestMirror)
		}
	case "CORS":
		if f.CORS != nil {
			gwFilter.CORS = buildGatewayCORS(f.CORS)
		}
	case "ExternalAuth":
		if f.ExternalAuth != nil {
			gwFilter.ExternalAuth = buildGatewayExternalAuth(f.ExternalAuth)
		}
	}
	return gwFilter
}

func toGatewayFilterType(s string) gatewayv1.HTTPRouteFilterType {
	switch s {
	case "RequestRedirect":
		return gatewayv1.HTTPRouteFilterRequestRedirect
	case "RequestHeaderModifier":
		return gatewayv1.HTTPRouteFilterRequestHeaderModifier
	case "ResponseHeaderModifier":
		return gatewayv1.HTTPRouteFilterResponseHeaderModifier
	case "URLRewrite":
		return gatewayv1.HTTPRouteFilterURLRewrite
	case "RequestMirror":
		return gatewayv1.HTTPRouteFilterRequestMirror
	case "CORS":
		return gatewayv1.HTTPRouteFilterCORS
	case "ExternalAuth":
		return gatewayv1.HTTPRouteFilterExternalAuth
	}
	return gatewayv1.HTTPRouteFilterType(s)
}

func buildGatewayRequestRedirect(rr *HTTPRequestRedirect) *gatewayv1.HTTPRequestRedirectFilter {
	gwRR := &gatewayv1.HTTPRequestRedirectFilter{}
	if rr.Scheme != "" {
		scheme := rr.Scheme
		gwRR.Scheme = &scheme
	}
	if rr.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(rr.Hostname)
		gwRR.Hostname = &hostname
	}
	if rr.Port != nil {
		port := *rr.Port
		gwRR.Port = &port
	}
	if rr.StatusCode != nil {
		code := *rr.StatusCode
		gwRR.StatusCode = &code
	}
	if rr.Path != nil {
		gwRR.Path = buildGatewayPathModifier(rr.Path)
	}
	return gwRR
}

func buildGatewayURLRewrite(rw *HTTPURLRewrite) *gatewayv1.HTTPURLRewriteFilter {
	gwRW := &gatewayv1.HTTPURLRewriteFilter{}
	if rw.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(rw.Hostname)
		gwRW.Hostname = &hostname
	}
	if rw.Path != nil {
		gwRW.Path = buildGatewayPathModifier(rw.Path)
	}
	return gwRW
}

func buildGatewayPathModifier(pm *HTTPPathModifier) *gatewayv1.HTTPPathModifier {
	gwPM := &gatewayv1.HTTPPathModifier{}
	switch pm.Type {
	case "ReplaceFullPath":
		gwPM.Type = gatewayv1.FullPathHTTPPathModifier
		v := pm.ReplaceFullPath
		gwPM.ReplaceFullPath = &v
	case "ReplacePrefixMatch":
		gwPM.Type = gatewayv1.PrefixMatchHTTPPathModifier
		v := pm.ReplacePrefixMatch
		gwPM.ReplacePrefixMatch = &v
	}
	return gwPM
}

func buildGatewayHeaderFilter(hm *HTTPHeaderModifier) *gatewayv1.HTTPHeaderFilter {
	gwHF := &gatewayv1.HTTPHeaderFilter{}
	for _, kv := range hm.Set {
		gwHF.Set = append(gwHF.Set, gatewayv1.HTTPHeader{
			Name:  gatewayv1.HTTPHeaderName(kv.Name),
			Value: kv.Value,
		})
	}
	for _, kv := range hm.Add {
		gwHF.Add = append(gwHF.Add, gatewayv1.HTTPHeader{
			Name:  gatewayv1.HTTPHeaderName(kv.Name),
			Value: kv.Value,
		})
	}
	if len(hm.Remove) > 0 {
		gwHF.Remove = append([]string(nil), hm.Remove...)
	}
	return gwHF
}

func buildGatewayTimeouts(t *HTTPRouteTimeouts) *gatewayv1.HTTPRouteTimeouts {
	gwT := &gatewayv1.HTTPRouteTimeouts{}
	if t.Request > 0 {
		gwDur := gatewayv1.Duration(t.Request.String())
		gwT.Request = &gwDur
	}
	if t.BackendRequest > 0 {
		gwDur := gatewayv1.Duration(t.BackendRequest.String())
		gwT.BackendRequest = &gwDur
	}
	return gwT
}

func buildGatewayRequestMirror(m *HTTPRequestMirror) *gatewayv1.HTTPRequestMirrorFilter {
	port := m.BackendRef.Port
	gw := &gatewayv1.HTTPRequestMirrorFilter{
		BackendRef: gatewayv1.BackendObjectReference{
			Name: gatewayv1.ObjectName(m.BackendRef.Name),
			Port: &port,
		},
	}
	if m.Percent != nil {
		pct := *m.Percent
		gw.Percent = &pct
	}
	if m.Fraction != nil {
		f := &gatewayv1.Fraction{Numerator: m.Fraction.Numerator}
		if m.Fraction.Denominator != nil {
			den := *m.Fraction.Denominator
			f.Denominator = &den
		}
		gw.Fraction = f
	}
	return gw
}

func buildGatewayCORS(c *HTTPCORS) *gatewayv1.HTTPCORSFilter {
	gw := &gatewayv1.HTTPCORSFilter{}
	for _, o := range c.AllowOrigins {
		gw.AllowOrigins = append(gw.AllowOrigins, gatewayv1.CORSOrigin(o))
	}
	if c.AllowCredentials != nil {
		v := *c.AllowCredentials
		gw.AllowCredentials = &v
	}
	for _, m := range c.AllowMethods {
		gw.AllowMethods = append(gw.AllowMethods, gatewayv1.HTTPMethodWithWildcard(m))
	}
	for _, h := range c.AllowHeaders {
		gw.AllowHeaders = append(gw.AllowHeaders, gatewayv1.HTTPHeaderName(h))
	}
	for _, h := range c.ExposeHeaders {
		gw.ExposeHeaders = append(gw.ExposeHeaders, gatewayv1.HTTPHeaderName(h))
	}
	if c.MaxAge != nil {
		gw.MaxAge = *c.MaxAge
	}
	return gw
}

func buildGatewayExternalAuth(ea *HTTPExternalAuth) *gatewayv1.HTTPExternalAuthFilter {
	port := ea.BackendRef.Port
	gw := &gatewayv1.HTTPExternalAuthFilter{
		ExternalAuthProtocol: gatewayv1.HTTPRouteExternalAuthProtocol(ea.Protocol),
		BackendRef: gatewayv1.BackendObjectReference{
			Name: gatewayv1.ObjectName(ea.BackendRef.Name),
			Port: &port,
		},
	}
	if ea.GRPC != nil {
		gc := &gatewayv1.GRPCAuthConfig{}
		if len(ea.GRPC.AllowedHeaders) > 0 {
			gc.AllowedRequestHeaders = append([]string(nil), ea.GRPC.AllowedHeaders...)
		}
		gw.GRPCAuthConfig = gc
	}
	if ea.HTTP != nil {
		hc := &gatewayv1.HTTPAuthConfig{Path: ea.HTTP.Path}
		if len(ea.HTTP.AllowedHeaders) > 0 {
			hc.AllowedRequestHeaders = append([]string(nil), ea.HTTP.AllowedHeaders...)
		}
		if len(ea.HTTP.AllowedResponseHeaders) > 0 {
			hc.AllowedResponseHeaders = append([]string(nil), ea.HTTP.AllowedResponseHeaders...)
		}
		gw.HTTPAuthConfig = hc
	}
	if ea.ForwardBody != nil {
		gw.ForwardBody = &gatewayv1.ForwardBodyConfig{MaxSize: ea.ForwardBody.MaxSize}
	}
	return gw
}

func toGatewayPathType(s string) gatewayv1.PathMatchType {
	switch s {
	case "Exact":
		return gatewayv1.PathMatchExact
	case "RegularExpression":
		return gatewayv1.PathMatchRegularExpression
	default:
		return gatewayv1.PathMatchPathPrefix
	}
}

func toGatewayHeaderType(s string) gatewayv1.HeaderMatchType {
	switch s {
	case "RegularExpression":
		return gatewayv1.HeaderMatchRegularExpression
	default:
		return gatewayv1.HeaderMatchExact
	}
}
