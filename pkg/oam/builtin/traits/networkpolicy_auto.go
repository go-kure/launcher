package traits

import (
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// parseTrafficSources reads networkPolicy.trafficSources[] from already-merged
// trait properties. The key is platform-reserved and populated via capability
// rendering, never by OAM authors.
//
// Returns nil, nil when the key is absent (no auto-synthesis for this trait).
// Returns an error when the key is present but malformed.
// An explicit trafficSources: [] is the mechanism for intentionally disabling
// auto-synthesis without removing the networkPolicy key.
func parseTrafficSources(props map[string]any, component, traitType string) ([]netpol.TrafficSource, error) {
	ve := func(field, msg string, args ...any) error {
		return &errors.ValidationError{
			Field:     field,
			Component: component,
			Message: fmt.Sprintf("component %q trait %q field %q: %s",
				component, traitType, field, fmt.Sprintf(msg, args...)),
		}
	}

	rawNP, hasNP := props["networkPolicy"]
	if !hasNP {
		return nil, nil
	}
	np, ok := rawNP.(map[string]any)
	if !ok {
		return nil, ve("networkPolicy", "expected object, got %T", rawNP)
	}
	for key := range np {
		if key != "trafficSources" {
			return nil, ve("networkPolicy", "unsupported key %q", key)
		}
	}
	rawSources, hasSources := np["trafficSources"]
	if !hasSources {
		return nil, ve("networkPolicy.trafficSources",
			"required field missing; use trafficSources: [] to explicitly disable auto-generation")
	}
	rawList, ok := rawSources.([]any)
	if !ok {
		return nil, ve("networkPolicy.trafficSources", "expected array, got %T", rawSources)
	}
	// Empty list is the intentional disable mechanism.
	if len(rawList) == 0 {
		return nil, nil
	}

	out := make([]netpol.TrafficSource, 0, len(rawList))
	for i, raw := range rawList {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d]", i), "expected object, got %T", raw)
		}
		for key := range m {
			if key != "namespace" && key != "podSelector" {
				return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d]", i), "unsupported key %q", key)
			}
		}
		rawNS, hasNS := m["namespace"]
		if !hasNS {
			return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d].namespace", i), "required field missing")
		}
		ns, ok := rawNS.(string)
		if !ok || ns == "" {
			return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d].namespace", i),
				"expected non-empty string, got %T", rawNS)
		}
		src := netpol.TrafficSource{Namespace: ns}
		if rawSel, hasSel := m["podSelector"]; hasSel {
			selMap, ok := rawSel.(map[string]any)
			if !ok {
				return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d].podSelector", i),
					"expected object, got %T", rawSel)
			}
			sel, err := parseMatchLabelsSelector(selMap, fmt.Sprintf("networkPolicy.trafficSources[%d].podSelector", i))
			if err != nil {
				return nil, ve(fmt.Sprintf("networkPolicy.trafficSources[%d].podSelector", i), "%s", err)
			}
			src.PodSelector = sel
		}
		out = append(out, src)
	}
	return out, nil
}

// parseMatchLabelsSelector parses a podSelector object supporting only matchLabels.
// Returns plain errors; callers wrap in a structured ValidationError.
func parseMatchLabelsSelector(raw map[string]any, path string) (*metav1.LabelSelector, error) {
	for key := range raw {
		if key != "matchLabels" {
			return nil, fmt.Errorf("%s: unsupported key %q (only matchLabels is supported)", path, key)
		}
	}
	rawML, hasML := raw["matchLabels"]
	if !hasML {
		return nil, fmt.Errorf("%s: missing required 'matchLabels'", path)
	}
	ml, ok := rawML.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.matchLabels: expected object, got %T", path, rawML)
	}
	labels := make(map[string]string, len(ml))
	for k, v := range ml {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s.matchLabels.%s: expected string value, got %T", path, k, v)
		}
		labels[k] = s
	}
	return &metav1.LabelSelector{MatchLabels: labels}, nil
}

// collectIngressPorts returns the ports for IngressPath entries that target the
// component's own Service (path.ServiceName empty or equal to the component
// service). Paths naming an external backend are skipped. Numeric Port and named
// PortName are both collected; duplicates are de-duplicated.
func collectIngressPorts(config *IngressConfig) []intstr.IntOrString {
	seen := map[string]intstr.IntOrString{}
	for _, rule := range config.Rules {
		for _, path := range rule.Paths {
			if path.ServiceName != "" && path.ServiceName != config.ServiceName {
				continue // external backend; skip
			}
			if path.Port > 0 {
				seen[fmt.Sprintf("n:%d", path.Port)] = intstr.FromInt32(path.Port)
			} else if path.PortName != "" {
				seen["s:"+path.PortName] = intstr.FromString(path.PortName)
			}
		}
	}
	return sortedIntOrStringPorts(seen)
}

// collectHTTPRoutePorts returns numeric ports for BackendRef entries targeting the
// component's own Service (ref.Name equal to selfServiceName). Refs naming an
// external backend are skipped.
func collectHTTPRoutePorts(config *HTTPRouteConfig, selfServiceName string) []intstr.IntOrString {
	seen := map[string]intstr.IntOrString{}
	for _, rule := range config.Rules {
		for _, ref := range rule.BackendRefs {
			if ref.Name != "" && ref.Name != selfServiceName {
				continue // external backend; skip
			}
			seen[fmt.Sprintf("n:%d", ref.Port)] = intstr.FromInt32(ref.Port)
		}
	}
	return sortedIntOrStringPorts(seen)
}

// sortedIntOrStringPorts converts the dedup map to a deterministically ordered
// slice: numeric ports ascending, then named ports lexically.
func sortedIntOrStringPorts(seen map[string]intstr.IntOrString) []intstr.IntOrString {
	var numeric []intstr.IntOrString
	var named []intstr.IntOrString
	for _, p := range seen {
		if p.Type == intstr.Int {
			numeric = append(numeric, p)
		} else {
			named = append(named, p)
		}
	}
	sort.Slice(numeric, func(i, j int) bool { return numeric[i].IntVal < numeric[j].IntVal })
	sort.Slice(named, func(i, j int) bool { return named[i].StrVal < named[j].StrVal })
	return append(numeric, named...)
}
