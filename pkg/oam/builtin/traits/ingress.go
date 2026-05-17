package traits

import (
	"fmt"
	"log/slog"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// validPathTypes is the set of path types accepted by the Kubernetes Ingress API.
var validPathTypes = map[string]bool{
	string(networkingv1.PathTypePrefix):                 true,
	string(networkingv1.PathTypeExact):                  true,
	string(networkingv1.PathTypeImplementationSpecific): true,
}

// IngressHandler handles OAM ingress traits.
//
// Deprecated: the ingress trait is deprecated; migrate to the expose trait.
// This handler is retained for direct use and emits a deprecation warning on every invocation.
type IngressHandler struct{}

// CanHandle returns true for ingress trait type.
func (h *IngressHandler) CanHandle(traitType string) bool {
	return traitType == "ingress"
}

// Apply creates an Ingress resource for the component's service.
// If the optional 'name' property is set, that value is used as the sub-application
// name, enabling multiple ingress traits on the same component without collision.
func (h *IngressHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	slog.Warn("ingress trait is deprecated; migrate to the expose trait or httproute",
		slog.String("component", app.Name),
	)
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
	config := &IngressConfig{
		ServiceName: app.Name,
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
				Port:     80,
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

			if port, ok := toIngressPort(pathMap["port"]); ok {
				p.Port = port
			}

			if portName, ok := pathMap["portName"].(string); ok && portName != "" {
				if _, hasPort := pathMap["port"]; !hasPort {
					p.Port = 0
					p.PortName = portName
				}
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
	Path     string
	PathType string
	Port     int32
	PortName string
}

// IngressTLS represents a TLS entry.
type IngressTLS struct {
	Hosts      []string
	SecretName string
}

// Generate creates a Kubernetes Ingress resource.
func (c *IngressConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{
		"app": c.ServiceName,
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
			} else if p.PortName != "" {
				servicePort = networkingv1.ServiceBackendPort{Name: p.PortName}
			} else {
				servicePort = networkingv1.ServiceBackendPort{Number: 80}
			}
			kubernetes.AddIngressRulePath(ingressRule, networkingv1.HTTPIngressPath{
				Path:     p.Path,
				PathType: &pathType,
				Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{
						Name: c.ServiceName,
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
