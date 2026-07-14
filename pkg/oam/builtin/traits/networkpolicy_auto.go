package traits

import (
	"fmt"
	"sort"
	"strings"

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

// collectIngressBackendTargets returns the external backend targets of an ingress trait: paths
// naming a Service other than the component's own, grouped by (service name, backendSelector) with
// their ports. These drive ingress-synthesis retargeting (#227 for in-bundle components, #239 for
// external Services carrying an explicit selector). Returns an error when one Service name is given
// two different non-nil selectors — a Service has a single selector, so that is an authoring
// conflict.
func collectIngressBackendTargets(config *IngressConfig) ([]netpol.BackendTarget, error) {
	var g backendTargetGroups
	for _, rule := range config.Rules {
		for _, path := range rule.Paths {
			if path.ServiceName == "" || path.ServiceName == config.ServiceName {
				continue // self backend; covered by BackendPorts
			}
			var port intstr.IntOrString
			hasPort := false
			if path.Port > 0 {
				port, hasPort = intstr.FromInt32(path.Port), true
			} else if path.PortName != "" {
				port, hasPort = intstr.FromString(path.PortName), true
			}
			if err := g.add(path.ServiceName, path.BackendSelector, port, hasPort); err != nil {
				return nil, err
			}
		}
	}
	return g.build(), nil
}

// collectHTTPRouteBackendTargets returns the external backend targets of an httproute trait:
// backendRefs naming a Service other than the component's own, grouped by (service name,
// backendSelector) with their ports. See collectIngressBackendTargets.
func collectHTTPRouteBackendTargets(config *HTTPRouteConfig, selfServiceName string) ([]netpol.BackendTarget, error) {
	var g backendTargetGroups
	for _, rule := range config.Rules {
		for _, ref := range rule.BackendRefs {
			if ref.Name == "" || ref.Name == selfServiceName {
				continue // self backend; covered by BackendPorts
			}
			var port intstr.IntOrString
			hasPort := false
			if ref.Port > 0 {
				port, hasPort = intstr.FromInt32(ref.Port), true
			}
			if err := g.add(ref.Name, ref.BackendSelector, port, hasPort); err != nil {
				return nil, err
			}
		}
	}
	return g.build(), nil
}

// backendTargetGroups accumulates external backend refs into (service name, selector) groups with
// deduped ports. A selectorless occurrence forms its own nil-selector group so its ports never
// widen a group that carries a selector. A single Service name may carry at most one distinct
// non-nil selector (a Kubernetes Service has one selector) — a second, different one is a conflict.
type backendTargetGroups struct {
	ports  map[string]map[string]intstr.IntOrString // composite key -> port dedup map
	sel    map[string]*metav1.LabelSelector         // composite key -> group selector
	svcSel map[string]*metav1.LabelSelector         // service name -> its single non-nil selector
	order  []string                                 // composite keys, insertion order
}

func (g *backendTargetGroups) add(service string, sel *metav1.LabelSelector, port intstr.IntOrString, hasPort bool) error {
	if g.ports == nil {
		g.ports = map[string]map[string]intstr.IntOrString{}
		g.sel = map[string]*metav1.LabelSelector{}
		g.svcSel = map[string]*metav1.LabelSelector{}
	}
	if sel != nil && len(sel.MatchLabels) > 0 {
		if prev, ok := g.svcSel[service]; ok && !matchLabelsEqual(prev, sel) {
			return errors.Errorf("backend service %q given conflicting backendSelector values", service)
		}
		g.svcSel[service] = sel
	}
	key := service + "\x00" + backendSelectorKey(sel)
	pm, ok := g.ports[key]
	if !ok {
		pm = map[string]intstr.IntOrString{}
		g.ports[key] = pm
		g.sel[key] = sel
		g.order = append(g.order, key)
	}
	if hasPort {
		if port.Type == intstr.Int {
			pm[fmt.Sprintf("n:%d", port.IntVal)] = port
		} else {
			pm["s:"+port.StrVal] = port
		}
	}
	return nil
}

// build converts the groups into a deterministically ordered slice: composite keys sorted (service
// name then selector key), ports via sortedIntOrStringPorts. Groups with no ports are dropped.
func (g *backendTargetGroups) build() []netpol.BackendTarget {
	sort.Strings(g.order)
	var out []netpol.BackendTarget
	for _, key := range g.order {
		ports := sortedIntOrStringPorts(g.ports[key])
		if len(ports) == 0 {
			continue
		}
		service := key[:strings.IndexByte(key, '\x00')]
		out = append(out, netpol.BackendTarget{ServiceName: service, Ports: ports, PodSelector: g.sel[key]})
	}
	return out
}

// backendSelectorKey is a canonical key for a matchLabels-only selector; "" for nil/empty.
func backendSelectorKey(sel *metav1.LabelSelector) string {
	if sel == nil || len(sel.MatchLabels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sel.MatchLabels))
	for k, v := range sel.MatchLabels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// matchLabelsEqual reports whether two matchLabels-only selectors are equal (both nil counts as
// equal). Used to detect conflicting backendSelector values for one Service name.
func matchLabelsEqual(a, b *metav1.LabelSelector) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a.MatchLabels) != len(b.MatchLabels) {
		return false
	}
	for k, v := range a.MatchLabels {
		if b.MatchLabels[k] != v {
			return false
		}
	}
	return true
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
