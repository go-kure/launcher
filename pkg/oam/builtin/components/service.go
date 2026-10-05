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
//
// `clusterIP: None` makes the Service headless (go-kure/launcher#690); a
// headless Service may have no ports at all, as a StatefulSet's governing
// Service often has.
type ServiceHandler struct{}

// CanHandle returns true for the service component type.
func (h *ServiceHandler) CanHandle(componentType string) bool {
	return componentType == "service"
}

// serviceTypes is the set of spec.type values this kind emits: all four the
// API has. An ExternalName Service selects no pods, so it is no backend of a
// routing trait on its own component, no NetworkPolicy endpoint, and a route
// naming it gets no synthesized inbound policy (go-kure/launcher#790); see
// isExternalName.
var serviceTypes = []corev1.ServiceType{
	corev1.ServiceTypeClusterIP,
	corev1.ServiceTypeNodePort,
	corev1.ServiceTypeLoadBalancer,
	corev1.ServiceTypeExternalName,
}

// serviceClusterIPNone is the one spec.clusterIP value this kind emits: a
// headless Service. A literal address is not offered.
const serviceClusterIPNone = "None"

// serviceProtocols is the set of port protocols the API accepts.
var serviceProtocols = []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP, corev1.ProtocolSCTP}

// servicePortKeys are the keys of one `ports` entry: what parseServicePort
// reads and the item schema publishes, pinned to each other by
// TestServiceSpecSchemaMatchesParser.
var servicePortKeys = []string{"name", "port", "targetPort", "protocol", "nodePort", "appProtocol"}

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
	schema := map[string]oam.PropertySchema{
		"type": {
			Type:        oam.PropertyTypeString,
			Default:     string(corev1.ServiceTypeClusterIP),
			Enum:        typeEnum,
			Description: "Service type: ClusterIP, NodePort, LoadBalancer or ExternalName. An ExternalName Service is a DNS alias for externalName: it selects no pods, so selector is refused and ports are optional.",
		},
		"clusterIP": {
			Type:        oam.PropertyTypeString,
			Enum:        []any{serviceClusterIPNone},
			Description: "None makes the Service headless: no virtual IP, DNS resolves to the selected pods. Only with type ClusterIP. A headless Service may have no ports. A literal address is not supported.",
		},
		"selector": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "Labels of the pods the Service routes to, as label key to string value. Defaults to app: <component name>, the label every launcher workload kind puts on its own pods; set it when the Service fronts a workload component with a different name. Must name at least one label. Refused with type ExternalName.",
		},
		"ports": {
			Type:        oam.PropertyTypeArray,
			Description: "The Service's ports; at least one, unless clusterIP is None or type is ExternalName. Routing traits on this component default to, and accept only, the first port.",
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
					// The two below: go-kure/launcher#790.
					"nodePort":    {Type: oam.PropertyTypeInteger, Description: "Port opened on every node for this port, 1-65535 (the cluster's node port range is narrower, 30000-32767 by default). Only with type NodePort or LoadBalancer; unset, the cluster allocates one. Unique per protocol."},
					"appProtocol": {Type: oam.PropertyTypeString, Description: "Application protocol of the port, a qualified name: an IANA service name (http) or a prefixed one (kubernetes.io/h2c)."},
				},
			},
		},
	}
	// The ServiceSpec fields beyond the four above (service_spec.go).
	maps.Copy(schema, schemaServiceSpec())
	return schema
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
// Service with no TCP port declares no endpoint, and neither does an
// ExternalName Service, which selects no pods. It parses the component
// itself, since ComponentEndpoints calls it with no schema validation first.
func (h *ServiceHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	c, err := parseService(component)
	if err != nil {
		return nil, err
	}
	var ports []intstr.IntOrString
	for _, p := range c.routedPorts() {
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
	Name string
	// ObjectName names the Service (oam.Component.ObjectName); its labels and
	// default selector keep Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Service
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Type      corev1.ServiceType
	// ClusterIP is "" (a virtual IP is allocated) or "None" (headless).
	ClusterIP string
	// Selector is the pod selector: authored, or app: <component name>.
	Selector map[string]string
	// Ports carries every port with its defaults applied (targetPort, protocol).
	Ports []corev1.ServicePort

	// Spec carries the authored ServiceSpec fields beyond the four above
	// (go-kure/launcher#790); see service_spec.go.
	Spec ServiceSpecFields
}

// isExternalName reports whether the Service is a DNS alias. Such a Service
// selects no pods (Selector is nil), so it has no endpoint, no pods a routed
// port may be opened on (ServiceRoutingTarget) and no port a routing trait may
// route to, whatever ports it lists.
func (c *ServiceConfig) isExternalName() bool {
	return c.Type == corev1.ServiceTypeExternalName
}

// routedPorts are the ports that lead to pods: every port, or none on an
// ExternalName Service.
func (c *ServiceConfig) routedPorts() []corev1.ServicePort {
	if c.isExternalName() {
		return nil
	}
	return c.Ports
}

// ServicePort returns the first port: routing traits resolve an implicit
// backend from it, and reject an implicit backend on any other port. It is 0
// on an ExternalName Service, as on a port-less headless one: a routing trait
// that takes the component as its backend is refused rather than pointed at a
// name outside the cluster.
func (c *ServiceConfig) ServicePort() int32 {
	ports := c.routedPorts()
	if len(ports) == 0 {
		return 0
	}
	return ports[0].Port
}

// ServicePortName returns the first port's name ("" when it is unnamed) and
// true: this config knows its port names, so routing traits refuse an implicit
// backend addressed by any other port name — a later port's, or one the
// Service does not have — just as they refuse any other port number. A
// port-less headless Service also knows its ports, all none of them: it
// returns "" and true, so routing traits refuse a trait-level servicePort on
// it rather than route to a port the Service lacks (go-kure/launcher#690).
// An ExternalName Service answers the same way, with or without ports.
func (c *ServiceConfig) ServicePortName() (string, bool) {
	ports := c.routedPorts()
	if len(ports) == 0 {
		return "", true
	}
	return ports[0].Name, true
}

// BackendServiceName names the Service this component owns when it has no
// ports (go-kure/launcher#690). A Service with ports is already known as its
// component's own by its first port (ServicePort); a port-less one would
// otherwise read as no Service at all, and NetworkPolicy synthesis would then
// treat a route naming it as an external backend and trust that route's
// backendSelector. It returns "" when the Service has ports, leaving that
// path unchanged. An ExternalName Service is named here with or without
// ports, since its ServicePort is always 0.
//
// A Service named apart from its component (`objectName`) is named here with
// or without ports: a route reaches it by the Service's name, and one that
// names the component's instead names no Service of this document.
func (c *ServiceConfig) BackendServiceName() string {
	if c.ObjectName != "" && c.ObjectName != c.Name {
		return c.ObjectName
	}
	if len(c.routedPorts()) > 0 {
		return ""
	}
	return c.Name
}

// ServiceRoutingTarget tells pkg/oam's inbound NetworkPolicy synthesis where
// traffic routed to this Service actually lands: the selector's pods, not the
// pods carrying this component's label (it owns none), on the target ports
// matching the routed Service ports. A routed port is matched by number or by
// port name; one that matches no TCP port of this Service is dropped, because
// the synthesized rules are TCP.
//
// An ExternalName Service has no such pods. It returns a non-nil selector
// without labels and never a port, whatever ports it lists. That selector is a
// marker, read as "owns its Service name and selects no pods": the synthesis
// then drops every rule routed to the component and writes no policy for it,
// instead of opening the routed port on pods carrying the component's label
// (go-kure/launcher#790). The marker is never written into an object: in an
// object a selector without labels selects every pod of the namespace.
func (c *ServiceConfig) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	if c.isExternalName() {
		return &metav1.LabelSelector{}, nil
	}
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

// IdentityTargetPorts reports whether every port, whatever its protocol, targets
// its own port number. A sibling group reads it (pkg/oam identityPortMapper):
// only then is a policy opening the routed Service ports on the selected pods
// the same as one opening their target ports. A named targetPort is never one,
// and an ExternalName Service, which selects no pods, never is either.
func (c *ServiceConfig) IdentityTargetPorts() bool {
	ports := c.routedPorts()
	for _, p := range ports {
		if p.TargetPort.Type != intstr.Int || p.TargetPort.IntVal != p.Port {
			return false
		}
	}
	return len(ports) > 0
}

// Generate creates the Service. Nothing else: the selected pods' workload
// component owns their ServiceAccount.
func (c *ServiceConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.ObjectName, app.Name)
	if err := validateServiceName(serviceNameField(name, app.Name), name); err != nil {
		return nil, err
	}
	svc := kubernetes.CreateService(name, app.Namespace)
	svc.Labels = appLabels(app.Name)
	svc.Annotations = nil
	svc.Spec.Type = c.Type
	svc.Spec.ClusterIP = c.ClusterIP
	svc.Spec.Selector = maps.Clone(c.Selector)
	for _, p := range c.Ports {
		// Copied: appProtocol is a pointer, and one render must not share it
		// with the next.
		kubernetes.AddServicePort(svc, *p.DeepCopy())
	}
	c.Spec.apply(&svc.Spec)
	return kindObject(svc, c.Metadata)
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
// field names where the value came from ("name" for the component name,
// "serviceName" for statefulset's authored property), so the author can find
// it. Every kind that emits a Service calls it, service and webservice
// (go-kure/launcher#546), and so does statefulset for the governing Service its
// serviceName names.
func validateServiceName(field, name string) error {
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
		return errors.Errorf("%s: %q is not a valid Service name, which must be a DNS-1035 label: %s", field, name, strings.Join(errs, "; "))
	}
	return nil
}

// validateComponentServiceName is validateServiceName at conversion, for a
// workload kind that names its Service after the component. A nameless config
// (converted without a component name, as a library caller may) is let
// through: its Generate checks the name it actually emits, the Application's.
func validateComponentServiceName(name string) error {
	if name == "" {
		return nil
	}
	return validateServiceName("name", name)
}

// serviceNameField names where a service component's Service name came from,
// for validateServiceName: the component name, or, when the Service is named
// apart from it, `objectName` or the Naming hook (objectNameField).
func serviceNameField(serviceName, componentName string) string {
	if serviceName != componentName {
		return objectNameField
	}
	return "name"
}

// parseService reads a service component's properties, applying the defaults
// and the checks ValidateService applies to the same fields. The Service is
// named after the component, or by its `objectName`: that name is checked
// against the Service-name rule first, and the component name is then held to
// no more than every component name is.
func parseService(component *oam.Component) (*ServiceConfig, error) {
	name := component.ObjectName()
	if err := validateServiceName(serviceNameField(name, component.Name), name); err != nil {
		return nil, err
	}
	props := component.Properties
	c := &ServiceConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Type: corev1.ServiceTypeClusterIP}

	if t, present, err := parseStringField(props, "type", "type"); err != nil {
		return nil, err
	} else if present {
		st := corev1.ServiceType(t)
		if !containsValue(serviceTypes, st) {
			return nil, errors.Errorf("type: must be one of %s, got %q", joinValues(serviceTypes), t)
		}
		c.Type = st
	}

	if ip, present, err := parseRawStringField(props, "clusterIP", "clusterIP"); err != nil {
		return nil, err
	} else if present {
		if ip != serviceClusterIPNone {
			return nil, errors.Errorf("clusterIP: must be %q, got %q; a literal address is not supported", serviceClusterIPNone, ip)
		}
		// The API server refuses a headless NodePort or LoadBalancer Service.
		if c.Type != corev1.ServiceTypeClusterIP {
			return nil, errors.Errorf("clusterIP: %q requires type %s, got %s", serviceClusterIPNone, corev1.ServiceTypeClusterIP, c.Type)
		}
		c.ClusterIP = ip
	}

	if err := parseServiceSpec(props, c); err != nil {
		return nil, err
	}

	// An ExternalName Service selects no pods, so it gets no default selector.
	// An authored one is this parser's own refusal: the API accepts and
	// ignores it.
	if c.isExternalName() {
		if _, authored := authoredValue(props, "selector"); authored {
			return nil, errors.Errorf("selector: may not be set with type %s, which selects no pods", corev1.ServiceTypeExternalName)
		}
	} else {
		c.Selector = appLabels(component.Name)
	}
	if raw, present, err := parseObjectField(props, "selector", "selector"); err != nil {
		return nil, err
	} else if present {
		sel, err := parseLabelMap(raw, "selector")
		if err != nil {
			return nil, err
		}
		// Checked on the parsed map: parseLabelMap drops a null entry, so
		// `selector: {app: null}` must meet the same refusal as `selector: {}`.
		if len(sel) == 0 {
			return nil, errors.New("selector: must name at least one label; a selector-less Service is not supported")
		}
		c.Selector = sel
	}

	entries, present, err := parseObjectList(props, "ports")
	if err != nil {
		return nil, err
	}
	if (!present || len(entries) == 0) && c.ClusterIP != serviceClusterIPNone && !c.isExternalName() {
		return nil, errors.New("ports: at least one port is required")
	}
	names := map[string]bool{}
	pairs := map[string]bool{}
	nodePorts := map[string]bool{}
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
		if p.NodePort != 0 {
			// The API refuses a node port on a ClusterIP Service; on an
			// ExternalName one the refusal is this parser's own.
			if c.Type != corev1.ServiceTypeNodePort && c.Type != corev1.ServiceTypeLoadBalancer {
				return nil, errors.Errorf("%s.nodePort: may only be set with type %s or %s, got %s",
					label, corev1.ServiceTypeNodePort, corev1.ServiceTypeLoadBalancer, c.Type)
			}
			nodePort := fmt.Sprintf("%d/%s", p.NodePort, p.Protocol)
			if nodePorts[nodePort] {
				return nil, errors.Errorf("%s.nodePort: duplicate node port %s", label, nodePort)
			}
			nodePorts[nodePort] = true
		}
		c.Ports = append(c.Ports, p)
	}
	return c, nil
}

func parseServicePort(m map[string]any, label string) (corev1.ServicePort, error) {
	var p corev1.ServicePort
	if err := rejectUnknownKeys(m, servicePortKeys, label); err != nil {
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

	if nodePort, present, err := parsePortField(m, "nodePort", label+".nodePort", 1); err != nil {
		return p, err
	} else if present {
		p.NodePort = nodePort
	}

	// "" is kept as a value, so it meets the refusal below rather than read
	// as an omitted key.
	if proto, present, err := parseRawStringField(m, "appProtocol", label+".appProtocol"); err != nil {
		return p, err
	} else if present {
		if errs := validation.IsQualifiedName(proto); len(errs) > 0 {
			return p, errors.Errorf("%s.appProtocol: %q is not a qualified name: %s", label, proto, strings.Join(errs, "; "))
		}
		p.AppProtocol = &proto
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
