package traits

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// servicePortProvider is implemented by component configs that expose a service port.
// Trait handlers use it to resolve the default backend port from the component's service.
type servicePortProvider interface {
	ServicePort() int32
}

// serviceBackendNamer is implemented by component configs whose Kubernetes Service
// name differs from the application name (e.g. StatefulsetConfig.ServiceName).
type serviceBackendNamer interface {
	BackendServiceName() string
}

// resolveDefaultPort returns the component's service port, or 0 if the component
// does not expose a service port.
func resolveDefaultPort(app *stack.Application) int32 {
	if pp, ok := app.Config.(servicePortProvider); ok && pp.ServicePort() > 0 {
		return pp.ServicePort()
	}
	return 0
}

// resolveServiceName returns the name of the Kubernetes Service the component exposes.
// Falls back to app.Name when the config does not implement serviceBackendNamer.
func resolveServiceName(app *stack.Application) string {
	if sn, ok := app.Config.(serviceBackendNamer); ok {
		return sn.BackendServiceName()
	}
	return app.Name
}

// checkImplicitBackend validates that the component can serve as an implicit backend
// (the trait path/backendRef does not name an explicit target service).
func checkImplicitBackend(app *stack.Application, location string) error {
	pp, ok := app.Config.(servicePortProvider)
	if !ok || pp.ServicePort() == 0 {
		return errors.Errorf(
			"%s: component %q has no service port; configure a service port or specify an explicit backend name"+
				" (for helmchart components, set servicePort and optionally serviceName on the trait)",
			location, app.Name)
	}
	return nil
}

// validPathTypes is the set of path types accepted by the Kubernetes Ingress API.
var validPathTypes = map[string]bool{
	string(networkingv1.PathTypePrefix):                 true,
	string(networkingv1.PathTypeExact):                  true,
	string(networkingv1.PathTypeImplementationSpecific): true,
}

// IngressHandler handles OAM ingress traits.
type IngressHandler struct{}

// CanHandle returns true for ingress trait type.
func (h *IngressHandler) CanHandle(traitType string) bool {
	return traitType == "ingress"
}

// PropertySchema declares the ingress trait's user-facing properties.
// `allowedHostnameWildcard` and `networkPolicy` are platform-reserved keys
// populated by capability rendering.
func (h *IngressHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"rules": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Host-based routing rules for the Ingress.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A single host rule with its paths.",
				Properties: map[string]oam.PropertySchema{
					"host": {Type: oam.PropertyTypeString, Required: true, Description: "Hostname this rule matches."},
					"paths": {
						Type:        oam.PropertyTypeArray,
						Required:    true,
						Description: "Paths under the host and the service backend each routes to.",
						Items: &oam.PropertySchema{
							Type:        oam.PropertyTypeObject,
							Description: "A single path mapping to a service backend.",
							Properties: map[string]oam.PropertySchema{
								"path":            {Type: oam.PropertyTypeString, Default: "/", Description: "URL path to match."},
								"pathType":        {Type: oam.PropertyTypeString, Default: "Prefix", Enum: []any{"Prefix", "Exact", "ImplementationSpecific"}, Description: "How the path is matched (Prefix, Exact, or ImplementationSpecific)."},
								"backend":         {Type: oam.PropertyTypeString, Description: "Service name to route to (defaults to the component's service)."},
								"port":            {Type: oam.PropertyTypeInteger, Description: "Service port number to route to."},
								"portName":        {Type: oam.PropertyTypeString, Description: "Named service port to route to (alternative to port)."},
								"backendSelector": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Explicit pod selector (matchLabels only) for an external backend Service; enables ingress NetworkPolicy synthesis onto the backend's pods."},
							},
						},
					},
				},
			},
		},
		"tls": {
			Type:        oam.PropertyTypeArray,
			Description: "TLS configuration for the Ingress hosts.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A single TLS entry pairing hosts with a secret.",
				Properties: map[string]oam.PropertySchema{
					"hosts":      {Type: oam.PropertyTypeArray, Description: "Hostnames covered by this TLS certificate.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname covered by the certificate."}},
					"secretName": {Type: oam.PropertyTypeString, Description: "Name of the Secret holding the TLS certificate."},
				},
			},
		},
		"annotations":             {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Additional annotations to set on the Ingress resource."},
		"ingressClassName":        {Type: oam.PropertyTypeString, Description: "IngressClass that should handle this Ingress."},
		"servicePort":             {Type: oam.PropertyTypeInteger, Description: "Service port to route to when the component does not expose one (e.g. helmchart)."},
		"serviceName":             {Type: oam.PropertyTypeString, Description: "Service name to route to; requires servicePort to also be set."},
		"name":                    {Type: oam.PropertyTypeString, Description: "Overrides the sub-application name, allowing multiple ingress traits per component."},
		"scope":                   {Type: oam.PropertyTypeString, Description: "Suffix appended to the sub-application name to disambiguate multiple ingress traits."},
		"allowedHostnameWildcard": {Type: oam.PropertyTypeString, Description: "Platform-reserved wildcard the rule hostnames must fall under."},
		"networkPolicy":           schemaNetworkPolicy(),
	}
}

// Apply creates an Ingress resource for the component's service.
// If the optional 'name' property is set, that value is used as the sub-application
// name, enabling multiple ingress traits on the same component without collision.
func (h *IngressHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	subAppName := app.Name + "-ingress"
	if config.Name != "" {
		subAppName = config.Name
	} else if config.Scope != "" {
		subAppName = app.Name + "-ingress-" + config.Scope
	}
	ingressApp := stack.NewApplication(subAppName, app.Namespace, config)
	bundle.Applications = append(bundle.Applications, ingressApp)
	return nil
}

func (h *IngressHandler) parseProperties(props map[string]any, app *stack.Application) (*IngressConfig, error) {
	defaultPort := resolveDefaultPort(app)
	config := &IngressConfig{
		componentName: app.Name,
		ServiceName:   resolveServiceName(app),
	}

	// Trait-level explicit backend — allows routing traits on component types that do not
	// implement servicePortProvider (e.g. helmchart). servicePort is parsed first so that
	// an invalid value is rejected before serviceName can be honoured.
	traitPortProvided := false
	if _, hasServicePort := props["servicePort"]; hasServicePort {
		sp, ok := toIngressPort(props["servicePort"])
		if !ok {
			return nil, errors.Errorf("servicePort must be a valid port number (1–65535)")
		}
		if existingPort := resolveDefaultPort(app); existingPort > 0 {
			return nil, errors.Errorf(
				"servicePort may not be set on a component that already exposes service port %d; "+
					"use path-level 'port' or 'backend' overrides instead",
				existingPort)
		}
		defaultPort = sp
		traitPortProvided = true
	}
	if sn, ok := props["serviceName"].(string); ok && sn != "" {
		if !traitPortProvided {
			return nil, errors.Errorf("serviceName requires a valid servicePort to also be set on the trait")
		}
		config.ServiceName = sn
	}

	if name, ok := props["name"].(string); ok && name != "" {
		config.Name = name
	}

	if scope, ok := props["scope"].(string); ok && scope != "" {
		config.Scope = scope
	}

	if rawAnnotations, ok := props["annotations"].(map[string]any); ok {
		config.Annotations = make(map[string]string, len(rawAnnotations))
		for k, v := range rawAnnotations {
			config.Annotations[k] = fmt.Sprintf("%v", v)
		}
	}

	if className, ok := props["ingressClassName"].(string); ok {
		config.IngressClassName = className
	}

	rawRules, ok := props["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		return nil, errors.Errorf("required property 'rules' missing or empty")
	}
	for i, rawRule := range rawRules {
		ruleMap, ok := rawRule.(map[string]any)
		if !ok {
			return nil, errors.Errorf("rules[%d]: expected object", i)
		}

		host, ok := ruleMap["host"].(string)
		if !ok || host == "" {
			return nil, errors.Errorf("rules[%d]: required field 'host' missing or not a string", i)
		}

		rawPaths, ok := ruleMap["paths"].([]any)
		if !ok || len(rawPaths) == 0 {
			return nil, errors.Errorf("rules[%d]: required field 'paths' missing or empty", i)
		}

		rule := IngressRule{Host: host}
		for j, rawPath := range rawPaths {
			pathMap, ok := rawPath.(map[string]any)
			if !ok {
				return nil, errors.Errorf("rules[%d].paths[%d]: expected object", i, j)
			}

			p := IngressPath{
				Path:     "/",
				PathType: "Prefix",
				Port:     defaultPort,
			}

			if path, ok := pathMap["path"].(string); ok {
				p.Path = path
			}

			if pathType, ok := pathMap["pathType"].(string); ok {
				if !validPathTypes[pathType] {
					return nil, errors.Errorf("rules[%d].paths[%d]: pathType must be 'Prefix', 'Exact', or 'ImplementationSpecific', got %q", i, j, pathType)
				}
				p.PathType = pathType
			}

			// backendExplicit is true only when the backend names a DIFFERENT service than
			// the component's own. A self-reference (backend == component service name) is
			// treated as implicit so that the same port-mismatch guard applies.
			backendExplicit := false
			if backend, ok := pathMap["backend"].(string); ok && backend != "" {
				p.ServiceName = backend
				if backend != config.ServiceName {
					backendExplicit = true
				}
			}

			// backendSelector is honored only for an explicit external backend: a self/implicit
			// backend is retargeted onto the component's own pods, so a selector there can never
			// take effect — reject it loudly rather than silently ignore.
			if rawSel, ok := pathMap["backendSelector"]; ok {
				selMap, ok := rawSel.(map[string]any)
				if !ok {
					return nil, errors.Errorf("rules[%d].paths[%d].backendSelector: expected object, got %T", i, j, rawSel)
				}
				if !backendExplicit {
					return nil, errors.Errorf("rules[%d].paths[%d]: backendSelector is only valid with an explicit external 'backend'", i, j)
				}
				sel, err := parseMatchLabelsSelector(selMap, fmt.Sprintf("rules[%d].paths[%d].backendSelector", i, j))
				if err != nil {
					return nil, err
				}
				if len(sel.MatchLabels) == 0 {
					return nil, errors.Errorf("rules[%d].paths[%d].backendSelector.matchLabels: must be non-empty", i, j)
				}
				p.BackendSelector = sel
			}

			portExplicit := false
			if port, ok := toIngressPort(pathMap["port"]); ok {
				p.Port = port
				portExplicit = true
			}

			if portName, ok := pathMap["portName"].(string); ok && portName != "" {
				if _, hasPort := pathMap["port"]; !hasPort {
					p.Port = 0
					p.PortName = portName
					portExplicit = true
				}
			}

			// Zero the inherited port only for truly external explicit backends.
			if backendExplicit && !portExplicit {
				p.Port = 0
			}

			// Implicit backend: empty name OR explicit name that resolves to the component's
			// own service — both are subject to the same port constraints.
			if p.ServiceName == "" || p.ServiceName == config.ServiceName {
				if !traitPortProvided {
					if err := checkImplicitBackend(app, fmt.Sprintf("rules[%d].paths[%d]", i, j)); err != nil {
						return nil, err
					}
				}
				if portExplicit && p.Port > 0 && p.Port != defaultPort {
					return nil, errors.Errorf(
						"rules[%d].paths[%d]: cannot route implicit backend to port %d — component service exposes port %d; specify an explicit backend name or match the component port",
						i, j, p.Port, defaultPort)
				}
			}
			if p.Port == 0 && p.PortName == "" {
				return nil, errors.Errorf(
					"rules[%d].paths[%d]: cannot determine backend port — configure the component port or specify 'port'/'portName' in the path",
					i, j)
			}

			rule.Paths = append(rule.Paths, p)
		}
		config.Rules = append(config.Rules, rule)
	}

	// Optional platform constraint (from capability rendering when used directly;
	// the expose trait validates and strips this before delegating here).
	if wildcard, ok := props["allowedHostnameWildcard"].(string); ok && wildcard != "" {
		hosts := make([]string, 0, len(config.Rules))
		for _, r := range config.Rules {
			hosts = append(hosts, r.Host)
		}
		if err := validateHostnames(hosts, wildcard, app.Name); err != nil {
			return nil, err
		}
	}

	if rawTLS, ok := props["tls"].([]any); ok {
		for i, rawEntry := range rawTLS {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return nil, errors.Errorf("tls[%d]: expected object", i)
			}
			tlsEntry := IngressTLS{}
			if rawHosts, ok := entry["hosts"].([]any); ok {
				for _, h := range rawHosts {
					s, ok := h.(string)
					if !ok {
						return nil, errors.Errorf("tls[%d].hosts entries must be strings", i)
					}
					tlsEntry.Hosts = append(tlsEntry.Hosts, s)
				}
			}
			if secretName, ok := entry["secretName"].(string); ok {
				tlsEntry.SecretName = secretName
			}
			config.TLS = append(config.TLS, tlsEntry)
		}
	}

	// Platform-reserved auto-NetworkPolicy inputs (populated by capability rendering).
	sources, err := parseTrafficSources(props, app.Name, "ingress")
	if err != nil {
		return nil, err
	}
	config.sources = sources
	config.ports = collectIngressPorts(config)
	backendTargets, err := collectIngressBackendTargets(config)
	if err != nil {
		return nil, err
	}
	config.backendTargets = backendTargets

	return config, nil
}

// IngressConfig implements stack.ApplicationConfig for ingress traits.
type IngressConfig struct {
	Name             string
	Scope            string // optional; sub-app name becomes {component}-ingress-{scope} when set and Name is empty
	componentName    string
	Annotations      map[string]string
	IngressClassName string
	Rules            []IngressRule
	TLS              []IngressTLS
	ServiceName      string

	// sources/ports are populated in parseProperties from the platform-reserved
	// networkPolicy.trafficSources rendering; they drive auto-NetworkPolicy synthesis.
	sources []netpol.TrafficSource
	ports   []intstr.IntOrString
	// backendTargets are external backendRef targets (paths naming a separate Service); they
	// drive ingress-synthesis retargeting onto the backend's pods (#227).
	backendTargets []netpol.BackendTarget
}

// TrafficSources implements the cluster-level trafficSourceCollector contract.
func (c *IngressConfig) TrafficSources() []netpol.TrafficSource { return c.sources }

// BackendTargets implements the cluster-level backendRefTargetCollector contract: external
// backendRef targets whose ingress allow should land on the backend's pods, not this component's.
func (c *IngressConfig) BackendTargets() []netpol.BackendTarget { return c.backendTargets }

// ComponentName returns the OAM component this sub-app belongs to — always the OAM
// component name, not the K8s Service name — for resource provenance attribution.
func (c *IngressConfig) ComponentName() string { return c.componentName }

// TargetComponentName returns the OAM component label (not the K8s Service name), so the
// synthesized NetworkPolicy selects the component's pods via the configured component
// label key (default {<domain>/component: <name>}, domain from TransformContext.Domain).
func (c *IngressConfig) TargetComponentName() string { return c.ComponentName() }

// BackendPorts implements the cluster-level trafficSourceCollector contract.
func (c *IngressConfig) BackendPorts() []intstr.IntOrString { return c.ports }

// IngressRule represents a single host rule with its paths.
type IngressRule struct {
	Host  string
	Paths []IngressPath
}

// IngressPath represents a single path within a rule.
type IngressPath struct {
	Path        string
	PathType    string
	ServiceName string // empty means use IngressConfig.ServiceName (= component service)
	Port        int32
	PortName    string
	// BackendSelector is the authored pod selector (matchLabels only) for an external backend
	// Service — only valid when ServiceName names a service other than the component's own. It
	// drives ingress-synthesis onto the backend's pods when the Service has no owning component.
	BackendSelector *metav1.LabelSelector
}

// IngressTLS represents a TLS entry.
type IngressTLS struct {
	Hosts      []string
	SecretName string
}

// Generate creates a Kubernetes Ingress resource.
func (c *IngressConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{
		"app": c.componentName,
	}

	ingress := kubernetes.CreateIngress(app.Name, app.Namespace, c.IngressClassName)
	ingress.Labels = labels
	ingress.Annotations = c.Annotations
	if c.IngressClassName == "" {
		ingress.Spec.IngressClassName = nil
	}

	for _, rule := range c.Rules {
		ingressRule := kubernetes.CreateIngressRule(rule.Host)
		for _, p := range rule.Paths {
			pathType := toPathType(p.PathType)
			var servicePort networkingv1.ServiceBackendPort
			if p.Port > 0 {
				servicePort = networkingv1.ServiceBackendPort{Number: p.Port}
			} else {
				servicePort = networkingv1.ServiceBackendPort{Name: p.PortName}
			}
			serviceName := p.ServiceName
			if serviceName == "" {
				serviceName = c.ServiceName
			}
			kubernetes.AddIngressRulePath(ingressRule, networkingv1.HTTPIngressPath{
				Path:     p.Path,
				PathType: &pathType,
				Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: serviceName,
						Port: servicePort,
					},
				},
			})
		}
		kubernetes.AddIngressRule(ingress, ingressRule)
	}

	for _, tls := range c.TLS {
		kubernetes.AddIngressTLS(ingress, networkingv1.IngressTLS{
			Hosts:      tls.Hosts,
			SecretName: tls.SecretName,
		})
	}

	obj := client.Object(ingress)
	return []*client.Object{&obj}, nil
}

func toIngressPort(v any) (int32, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n <= 65535 {
			return int32(n), true //nolint:gosec // validated above
		}
	case int:
		if n > 0 && n <= 65535 {
			return int32(n), true //nolint:gosec // validated above
		}
	}
	return 0, false
}

func toPathType(s string) networkingv1.PathType {
	switch s {
	case "Exact":
		return networkingv1.PathTypeExact
	case "ImplementationSpecific":
		return networkingv1.PathTypeImplementationSpecific
	default:
		return networkingv1.PathTypePrefix
	}
}
