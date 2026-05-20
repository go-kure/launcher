package traits

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
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
			"%s: component %q has no service port; configure a service port or specify an explicit backend name",
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
	}
	ingressApp := stack.NewApplication(subAppName, app.Namespace, config)
	bundle.Applications = append(bundle.Applications, ingressApp)
	return nil
}

func (h *IngressHandler) parseProperties(props map[string]any, app *stack.Application) (*IngressConfig, error) {
	defaultPort := resolveDefaultPort(app)
	config := &IngressConfig{
		ComponentName: app.Name,
		ServiceName:   resolveServiceName(app),
	}

	if name, ok := props["name"].(string); ok && name != "" {
		config.Name = name
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
				if err := checkImplicitBackend(app, fmt.Sprintf("rules[%d].paths[%d]", i, j)); err != nil {
					return nil, err
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

	return config, nil
}

// IngressConfig implements stack.ApplicationConfig for ingress traits.
type IngressConfig struct {
	Name             string
	ComponentName    string // label value — always the OAM component name, not the K8s Service name
	Annotations      map[string]string
	IngressClassName string
	Rules            []IngressRule
	TLS              []IngressTLS
	ServiceName      string
}

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
}

// IngressTLS represents a TLS entry.
type IngressTLS struct {
	Hosts      []string
	SecretName string
}

// Generate creates a Kubernetes Ingress resource.
func (c *IngressConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{
		"app": c.ComponentName,
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
		_ = kubernetes.AddIngressRule(ingress, ingressRule)
	}

	for _, tls := range c.TLS {
		_ = kubernetes.AddIngressTLS(ingress, networkingv1.IngressTLS{
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
