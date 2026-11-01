package components

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// chartEndpointsKey is the property a chart-release component (helm, helmrelease,
// helmtemplate) declares its endpoints under (go-kure/launcher#1132). A chart renders
// pods this package cannot see, so the author names the pods a consumer connects to.
const chartEndpointsKey = "endpoints"

// chartEndpointsSchema is the one schema of the `endpoints` property, shared by the three
// chart types so they cannot drift. It cannot say that non-empty names are unique or that
// a port is 1-65535 or an IANA name: parseChartEndpoints refuses both, with the path.
func chartEndpointsSchema() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Description: "The pods a consumer connects to, as the chart renders them: one entry per port. " +
			"The chart's pods are not visible to launcher, so they are declared here and not derived. " +
			"A non-empty name is unique within the component. The pods run in the application namespace of the application's cluster: refused beside targetNamespace and kubeConfig. A launcher property: it is not a chart value " +
			"and is never forwarded to Flux or to the render.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "One endpoint: a port of the pods the selector matches.",
			Properties: map[string]oam.PropertySchema{
				"name":     {Type: oam.PropertyTypeString, Description: "Optional. Empty, or an IANA service name as a container port name is: at most 15 characters of a-z, 0-9 and '-', at least one letter, no leading, trailing or doubled hyphen."},
				"protocol": {Type: oam.PropertyTypeString, Enum: []any{"TCP", "UDP", "SCTP"}, Description: "Defaults to TCP."},
				"port":     {Types: []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString}, Required: true, Description: "The port number (1-65535) or the IANA name of a container port of the matched pods."},
				"selector": {
					Type:        oam.PropertyTypeObject,
					Required:    true,
					Description: "The pods the endpoint is on. matchLabels only, and not empty: an empty selector would match every pod of the namespace.",
					Properties: map[string]oam.PropertySchema{
						"matchLabels": {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "Label key to value, every entry a string."},
					},
				},
			},
		},
	}
}

// splitChartEndpoints returns props without the `endpoints` key, and the authored value of
// it: ok is false when it is absent or an explicit null (a null reads as omission). The
// input map is not modified. The key is split off before any strict decode, so a chart
// type's own parse never sees it and what it forwards or decodes does not change.
func splitChartEndpoints(props map[string]any) (rest map[string]any, raw any, ok bool) {
	if _, present := props[chartEndpointsKey]; !present {
		return props, nil, false
	}
	rest = make(map[string]any, len(props))
	for k, v := range props {
		if k != chartEndpointsKey {
			rest[k] = v
		}
	}
	raw, ok = authoredValue(props, chartEndpointsKey)
	return rest, raw, ok
}

// parseChartEndpoints reads the value of the `endpoints` property into endpoints. Every
// refusal names its path (`endpoints[1].selector.matchLabels`). An entry takes name,
// protocol, port and selector, and nothing else; the selector is required and has
// matchLabels alone. A repeated non-empty name is refused here, so a build, a lowering
// and a direct Endpoints call all refuse it.
func parseChartEndpoints(raw any) ([]netpol.Endpoint, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.Errorf("%s: must be a list of endpoint objects, got %T", chartEndpointsKey, raw)
	}
	eps := make([]netpol.Endpoint, 0, len(list))
	names := map[string]int{}
	for i, item := range list {
		label := fmt.Sprintf("%s[%d]", chartEndpointsKey, i)
		ep, err := parseChartEndpoint(nullElem(item), label)
		if err != nil {
			return nil, err
		}
		if ep.Name != "" {
			if first, dup := names[ep.Name]; dup {
				return nil, errors.Errorf("%s.name: repeats the name %q of %s[%d]", label, ep.Name, chartEndpointsKey, first)
			}
			names[ep.Name] = i
		}
		eps = append(eps, ep)
	}
	return eps, nil
}

func parseChartEndpoint(item any, label string) (netpol.Endpoint, error) {
	entry, ok := item.(map[string]any)
	if !ok {
		return netpol.Endpoint{}, errors.Errorf("%s: must be an object, got %T", label, item)
	}
	for _, k := range slices.Sorted(maps.Keys(entry)) {
		switch k {
		case "name", "protocol", "port", "selector":
		default:
			return netpol.Endpoint{}, errors.Errorf("%s: unknown property %q", label, k)
		}
	}
	var ep netpol.Endpoint
	name, _, err := parseStringField(entry, "name", label+".name")
	if err != nil {
		return ep, err
	}
	if errs := validation.IsValidPortName(name); name != "" && len(errs) > 0 {
		return ep, errors.Errorf("%s.name: %q is not a valid port name: %s", label, name, strings.Join(errs, "; "))
	}
	ep.Name = name

	protocol, present, err := parseRawStringField(entry, "protocol", label+".protocol")
	if err != nil {
		return ep, err
	}
	switch p := corev1.Protocol(protocol); {
	case !present:
	case p == corev1.ProtocolTCP || p == corev1.ProtocolUDP || p == corev1.ProtocolSCTP:
		ep.Protocol = p
	default:
		return ep, errors.Errorf("%s.protocol: must be TCP, UDP or SCTP, got %q", label, protocol)
	}

	port, err := parseChartEndpointPort(entry, label+".port")
	if err != nil {
		return ep, err
	}
	ep.Ports = []intstr.IntOrString{port}

	sel, err := parseChartEndpointSelector(entry, label+".selector")
	if err != nil {
		return ep, err
	}
	ep.PodSelector = sel
	return ep, nil
}

// parseChartEndpointPort reads a required port: a whole number 1-65535, or an IANA port
// name. A string of digits is not a name (a name needs a letter) and is refused as one.
func parseChartEndpointPort(entry map[string]any, label string) (intstr.IntOrString, error) {
	v, present := authoredValue(entry, "port")
	if !present {
		return intstr.IntOrString{}, errors.Errorf("%s: is required", label)
	}
	if s, isString := v.(string); isString {
		if errs := validation.IsValidPortName(s); len(errs) > 0 {
			return intstr.IntOrString{}, errors.Errorf("%s: %q is neither a port number nor a valid port name: %s", label, s, strings.Join(errs, "; "))
		}
		return intstr.FromString(s), nil
	}
	n, err := oam.IntegerInRange(v, 1, 65535)
	if err != nil {
		return intstr.IntOrString{}, errors.Errorf("%s: %w", label, err)
	}
	return intstr.FromInt32(int32(n)), nil //nolint:gosec // bounded to 1..65535 by IntegerInRange
}

// parseChartEndpointSelector reads the required selector: matchLabels alone, not empty,
// every key a qualified name and every value a valid label value.
func parseChartEndpointSelector(entry map[string]any, label string) (*metav1.LabelSelector, error) {
	sel, present, err := parseObjectField(entry, "selector", label)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.Errorf("%s: is required", label)
	}
	for _, k := range slices.Sorted(maps.Keys(sel)) {
		if k != "matchLabels" {
			return nil, errors.Errorf("%s: unknown property %q; only matchLabels is taken", label, k)
		}
	}
	labels, present, err := parseObjectField(sel, "matchLabels", label+".matchLabels")
	if err != nil {
		return nil, err
	}
	if !present || len(labels) == 0 {
		return nil, errors.Errorf("%s.matchLabels: is required and not empty; an empty selector would match every pod of the namespace", label)
	}
	out := make(map[string]string, len(labels))
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		s, ok := labels[k].(string)
		if !ok {
			return nil, errors.Errorf("%s.matchLabels[%q]: must be a string, got %T", label, k, labels[k])
		}
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, errors.Errorf("%s.matchLabels: key %q is not a valid label key: %s", label, k, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(s); len(errs) > 0 {
			return nil, errors.Errorf("%s.matchLabels[%q]: %q is not a valid label value: %s", label, k, s, strings.Join(errs, "; "))
		}
		out[k] = s
	}
	return &metav1.LabelSelector{MatchLabels: out}, nil
}

// declaredChartEndpoints reads the `endpoints` declaration of a chart-release component's
// properties: none when it is absent, null or an empty list. The caller runs the type's own
// parse first, so a component the type refuses has no endpoint and is refused in the
// parse's words, and prefixes a refusal of the declaration with its type, as the type's own
// refusals are prefixed.
func declaredChartEndpoints(props map[string]any) ([]netpol.Endpoint, error) {
	_, raw, declared := splitChartEndpoints(props)
	if !declared {
		return nil, nil
	}
	eps, err := parseChartEndpoints(raw)
	if err != nil || len(eps) == 0 {
		return nil, err
	}
	if err := refuseEndpointsBesideRelocation(props); err != nil {
		return nil, err
	}
	return eps, nil
}

// refuseEndpointsBesideRelocation refuses a declaration beside a property that puts the
// chart's pods somewhere the endpoint's NetworkPolicy does not select them. That policy is
// created in the application namespace of the application's own cluster, and launcher
// cannot compare it with another place, so a declaration is refused beside each property
// that moves the pods: targetNamespace (another namespace) and kubeConfig (another
// cluster). A HelmRelease has no other field that places its pods; the others considered
// are listed in the components README. The key matches as the decode does, whatever its
// case, and so does the value: it is read the way builtin.DecodeStrictJSON reads it,
// through JSON, so a named string, a pointer to a string and a json.RawMessage count like
// a plain string (the type's own parse has already refused a value of the wrong type).
func refuseEndpointsBesideRelocation(props map[string]any) error {
	for _, k := range slices.Sorted(maps.Keys(props)) {
		data, err := json.Marshal(props[k])
		if err != nil {
			continue // the type's own decode has already refused a value that is not JSON
		}
		switch {
		case strings.EqualFold(k, "targetNamespace"):
			var ns string
			_ = json.Unmarshal(data, &ns) // null and any other shape leave ns empty
			if ns != "" {
				return errors.Errorf("%s: cannot be declared beside targetNamespace %q: an endpoint's NetworkPolicy is created in the application namespace, so the chart's pods must run there", chartEndpointsKey, ns)
			}
		case strings.EqualFold(k, "kubeConfig"):
			// A null reads as omission, as in the decode; any other value is a remote cluster.
			if string(data) != "null" {
				return errors.Errorf("%s: cannot be declared beside kubeConfig: the chart's pods run on another cluster, and an endpoint's NetworkPolicy is created in the application's own cluster, where it selects none of them", chartEndpointsKey)
			}
		}
	}
	return nil
}
