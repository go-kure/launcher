package components

import (
	"fmt"
	"maps"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// ServiceHandler handles OAM service components: the kind-named projection of
// corev1.Service (go-kure/launcher#411), alongside the kind-named workload
// kinds.
//
// It is an independent kind, not a half of a workload. It emits the Service
// and nothing else: the pods it selects belong to some other component (by
// default one named like this one, via `app: <component name>`), and that
// workload component owns their ServiceAccount. `selector` names those pods
// explicitly when the workload is named differently.
//
// The component name is the Service's name, so it must be a DNS-1035 label,
// as the API server requires of a Service (validateServiceName).
//
// `ports` is the full corev1.ServicePort list, each entry with its own
// `targetPort` (default: the entry's `port`) and `protocol` (default TCP),
// rather than webservice's single port that drives both sides.
type ServiceHandler struct{}

// CanHandle returns true for the service component type.
func (h *ServiceHandler) CanHandle(componentType string) bool {
	return componentType == "service"
}

// serviceTypes is the set of spec.type values this kind emits. ExternalName is
// left out: it carries no selector and no ports, so it is not a projection of
// the same object.
var serviceTypes = []corev1.ServiceType{
	corev1.ServiceTypeClusterIP,
	corev1.ServiceTypeNodePort,
	corev1.ServiceTypeLoadBalancer,
}

// serviceProtocols is the set of port protocols the API accepts.
var serviceProtocols = []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP, corev1.ProtocolSCTP}

// PropertySchema declares the service component's user-facing properties.
func (h *ServiceHandler) PropertySchema() map[string]oam.PropertySchema {
	typeEnum := make([]any, 0, len(serviceTypes))
	for _, t := range serviceTypes {
		typeEnum = append(typeEnum, string(t))
	}
	protoEnum := make([]any, 0, len(serviceProtocols))
	for _, p := range serviceProtocols {
		protoEnum = append(protoEnum, string(p))
	}
	return map[string]oam.PropertySchema{
		"type": {
			Type:        oam.PropertyTypeString,
			Default:     string(corev1.ServiceTypeClusterIP),
			Enum:        typeEnum,
			Description: "Service type: ClusterIP, NodePort or LoadBalancer. ExternalName is not supported.",
		},
		"selector": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "Labels of the pods the Service routes to, as label key to string value. Defaults to app: <component name>, the label every launcher workload kind puts on its own pods; set it when the Service fronts a workload component with a different name. Must name at least one label.",
		},
		"ports": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "The Service's ports; at least one. Routing traits on this component default to, and accept only, the first port.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One Service port. Every port needs a name when there is more than one; names and port/protocol pairs must be unique.",
				Properties: map[string]oam.PropertySchema{
					"name": {Type: oam.PropertyTypeString, Description: "Port name (a DNS-1123 label). Required when the Service has more than one port."},
					"port": {Type: oam.PropertyTypeInteger, Required: true, Description: "Port the Service exposes, 1-65535."},
					// An int-or-string leaf, published as the integer/string union
					// like deployment's maxUnavailable (go-kure/launcher#383).
					// parseTargetPort reads the integer through toInt32, which
					// refuses a fractional number, so integer (which admits an
					// integral float) narrows nothing it accepts.
					"targetPort": {Types: intOrStringTypes(), Description: "Port on the selected pods: a number (1-65535) or a container port name. Defaults to port."},
					"protocol":   {Type: oam.PropertyTypeString, Default: string(corev1.ProtocolTCP), Enum: protoEnum, Description: "TCP, UDP or SCTP. Defaults to TCP. Only TCP ports receive a synthesized NetworkPolicy allow."},
				},
			},
		},
	}
}

// ToApplicationConfig converts an OAM service component to a ServiceConfig.
func (h *ServiceHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	c, err := parseService(component)
	if err != nil {
		return nil, err
	}
	c.Namespace = namespace
	return c, nil
}

// Endpoints implements oam.EndpointProvider: the pods the Service selects, on
// every TCP targetPort. A UDP or SCTP port is not declared, because the
// endpoint-ingress NetworkPolicy it feeds is TCP-only (netpol.Endpoint); a
// Service with no TCP port declares no endpoint. It parses the component
// itself, since ComponentEndpoints calls it with no schema validation first.
func (h *ServiceHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	c, err := parseService(component)
	if err != nil {
		return nil, err
	}
	var ports []intstr.IntOrString
	for _, p := range c.Ports {
		if p.Protocol == corev1.ProtocolTCP {
			ports = appendUniquePort(ports, p.TargetPort)
		}
	}
	if len(ports) == 0 {
		return nil, nil
	}
	return []netpol.Endpoint{{PodSelector: selectorFrom(c.Selector), Ports: ports}}, nil
}

// ServiceConfig implements stack.ApplicationConfig for service components.
type ServiceConfig struct {
	Name      string
	Namespace string
	Type      corev1.ServiceType
	// Selector is the pod selector: authored, or app: <component name>.
	Selector map[string]string
	// Ports carries every port with its defaults applied (targetPort, protocol).
	Ports []corev1.ServicePort
}

// ServicePort returns the first port: routing traits resolve an implicit
// backend from it, and reject an implicit backend on any other port.
func (c *ServiceConfig) ServicePort() int32 {
	if len(c.Ports) == 0 {
		return 0
	}
	return c.Ports[0].Port
}

// ServicePortName returns the first port's name ("" when it is unnamed) and
// true: this config knows its port names, so routing traits refuse an implicit
// backend addressed by any other port name — a later port's, or one the
// Service does not have — just as they refuse any other port number.
func (c *ServiceConfig) ServicePortName() (string, bool) {
	if len(c.Ports) == 0 {
		return "", false
	}
	return c.Ports[0].Name, true
}

// ServiceRoutingTarget tells pkg/oam's inbound NetworkPolicy synthesis where
// traffic routed to this Service actually lands: the selector's pods, not the
// pods carrying this component's label (it owns none), on the target ports
// matching the routed Service ports. A routed port is matched by number or by
// port name; one that matches no TCP port of this Service is dropped, because
// the synthesized rules are TCP.
func (c *ServiceConfig) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	var out []intstr.IntOrString
	for _, routed := range servicePorts {
		for _, p := range c.Ports {
			if p.Protocol != corev1.ProtocolTCP {
				continue
			}
			if (routed.Type == intstr.Int && routed.IntVal == p.Port) ||
				(routed.Type == intstr.String && p.Name != "" && routed.StrVal == p.Name) {
				out = appendUniquePort(out, p.TargetPort)
			}
		}
	}
	return selectorFrom(c.Selector), out
}

// Generate creates the Service. Nothing else: the selected pods' workload
// component owns their ServiceAccount.
func (c *ServiceConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := validateServiceName(app.Name); err != nil {
		return nil, err
	}
	svc := kubernetes.CreateService(app.Name, app.Namespace)
	svc.Labels = appLabels(app.Name)
	svc.Annotations = nil
	svc.Spec.Type = c.Type
	svc.Spec.Selector = maps.Clone(c.Selector)
	for _, p := range c.Ports {
		kubernetes.AddServicePort(svc, p)
	}
	obj := client.Object(svc)
	return []*client.Object{&obj}, nil
}

func appendUniquePort(ports []intstr.IntOrString, p intstr.IntOrString) []intstr.IntOrString {
	for _, existing := range ports {
		if existing == p {
			return ports
		}
	}
	return append(ports, p)
}

// validateServiceName refuses a name the API server would refuse for a
// Service. Kubernetes validates a Service name as a DNS-1035 label (at most 63
// characters, lowercase alphanumerics and '-', starting with a letter), which
// is stricter than the DNS-1123 subdomain every component name already
// passes: "api.v1", a 64-character name and "1api" all pass that check.
func validateServiceName(name string) error {
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
		return errors.Errorf("name: %q is not a valid Service name, which must be a DNS-1035 label: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

// parseService reads a service component's properties, applying the defaults
// and the checks ValidateService applies to the same fields. The component
// name is the Service's name, so it is checked against the Service-name rule
// first.
func parseService(component *oam.Component) (*ServiceConfig, error) {
	if err := validateServiceName(component.Name); err != nil {
		return nil, err
	}
	props := component.Properties
	c := &ServiceConfig{Name: component.Name, Type: corev1.ServiceTypeClusterIP}

	if t, present, err := parseStringField(props, "type", "type"); err != nil {
		return nil, err
	} else if present {
		st := corev1.ServiceType(t)
		if !containsValue(serviceTypes, st) {
			return nil, errors.Errorf("type: must be one of %s, got %q", joinValues(serviceTypes), t)
		}
		c.Type = st
	}

	c.Selector = appLabels(component.Name)
	if raw, present, err := parseObjectField(props, "selector", "selector"); err != nil {
		return nil, err
	} else if present {
		if len(raw) == 0 {
			return nil, errors.New("selector: must name at least one label; a selector-less Service is not supported")
		}
		sel, err := parseLabelMap(raw, "selector")
		if err != nil {
			return nil, err
		}
		c.Selector = sel
	}

	entries, present, err := parseObjectList(props, "ports")
	if err != nil {
		return nil, err
	}
	if !present || len(entries) == 0 {
		return nil, errors.New("ports: at least one port is required")
	}
	names := map[string]bool{}
	pairs := map[string]bool{}
	for i, m := range entries {
		label := indexedLabel("ports", i)
		p, err := parseServicePort(m, label)
		if err != nil {
			return nil, err
		}
		if p.Name == "" && len(entries) > 1 {
			return nil, errors.Errorf("%s.name: required when the Service has more than one port", label)
		}
		if p.Name != "" {
			if names[p.Name] {
				return nil, errors.Errorf("%s.name: duplicate port name %q", label, p.Name)
			}
			names[p.Name] = true
		}
		pair := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
		if pairs[pair] {
			return nil, errors.Errorf("%s: duplicate port %s", label, pair)
		}
		pairs[pair] = true
		c.Ports = append(c.Ports, p)
	}
	return c, nil
}

func parseServicePort(m map[string]any, label string) (corev1.ServicePort, error) {
	var p corev1.ServicePort
	if err := rejectUnknownKeys(m, []string{"name", "port", "targetPort", "protocol"}, label); err != nil {
		return p, err
	}

	if name, present, err := parseStringField(m, "name", label+".name"); err != nil {
		return p, err
	} else if present {
		if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
			return p, errors.Errorf("%s.name: invalid port name %q: %s", label, name, strings.Join(errs, "; "))
		}
		p.Name = name
	}

	port, present, err := parseInt32Field(m, "port", label+".port")
	if err != nil {
		return p, err
	}
	if !present {
		return p, errors.Errorf("%s.port: required", label)
	}
	if errs := validation.IsValidPortNum(int(port)); len(errs) > 0 {
		return p, errors.Errorf("%s.port: must be between 1 and 65535, got %d", label, port)
	}
	p.Port = port

	p.TargetPort = intstr.FromInt32(port)
	if v, present := authoredValue(m, "targetPort"); present {
		tp, err := parseTargetPort(v, label+".targetPort")
		if err != nil {
			return p, err
		}
		p.TargetPort = tp
	}

	p.Protocol = corev1.ProtocolTCP
	if proto, present, err := parseStringField(m, "protocol", label+".protocol"); err != nil {
		return p, err
	} else if present {
		if !containsValue(serviceProtocols, corev1.Protocol(proto)) {
			return p, errors.Errorf("%s.protocol: must be one of %s, got %q", label, joinValues(serviceProtocols), proto)
		}
		p.Protocol = corev1.Protocol(proto)
	}
	return p, nil
}

// parseTargetPort reads an int-or-string targetPort: a port number, or a
// container port name (IANA_SVC_NAME), as ValidateService checks it.
func parseTargetPort(v any, label string) (intstr.IntOrString, error) {
	if s, ok := v.(string); ok {
		if errs := validation.IsValidPortName(s); len(errs) > 0 {
			return intstr.IntOrString{}, errors.Errorf("%s: invalid port name %q: %s", label, s, strings.Join(errs, "; "))
		}
		return intstr.FromString(s), nil
	}
	n, ok := toInt32(v)
	if !ok {
		return intstr.IntOrString{}, errors.Errorf("%s: must be an integer or a port name, got %T", label, v)
	}
	if errs := validation.IsValidPortNum(int(n)); len(errs) > 0 {
		return intstr.IntOrString{}, errors.Errorf("%s: must be between 1 and 65535, got %d", label, n)
	}
	return intstr.FromInt32(n), nil
}

func containsValue[T ~string](set []T, v T) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func joinValues[T ~string](set []T) string {
	parts := make([]string, len(set))
	for i, s := range set {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}
