package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// trafficSourceCollector is implemented by routing trait configs (IngressConfig,
// HTTPRouteConfig) that expose collected traffic-source data so the cluster
// post-build stage can synthesize per-component NetworkPolicy allow rules.
type trafficSourceCollector interface {
	TrafficSources() []netpol.TrafficSource
	TargetComponentName() string
	BackendPorts() []intstr.IntOrString
}

type trafficRule struct {
	Sources []netpol.TrafficSource
	Ports   []intstr.IntOrString
}

// componentAllowPolicyConfig is the ApplicationConfig for an auto-generated
// NetworkPolicy that allows ingress from routing controller traffic sources to
// the component's pods. The resource name is {component}-allow-ingress-traffic,
// distinct from the name emitted by the explicit networkpolicy trait.
type componentAllowPolicyConfig struct {
	ComponentName string
	Rules         []trafficRule // one per collector with non-empty ports; deduplicated
	// PodSelectorKey is the label key selecting the component's own pods (the ingress
	// recipients). Empty => the library default key (ComponentLabelKeyForDomain(DefaultDomain)).
	// The transform path always passes the resolved, domain-aware key; the default here only
	// applies to directly-constructed configs (tests), which cannot know the transform domain.
	PodSelectorKey string
}

// podSelectorKey returns the configured selector key, defaulting to the library default so a
// directly-built config never emits an empty-key selector.
func (c *componentAllowPolicyConfig) podSelectorKey() string {
	if c.PodSelectorKey == "" {
		return ComponentLabelKeyForDomain(DefaultDomain)
	}
	return c.PodSelectorKey
}

// ApplyPolicy is a no-op: a synthesized NetworkPolicy has no enforceable policy
// fields. Synthesis runs after the trait Enforceable.ApplyPolicy pass, so these
// configs are intentionally never policy-checked (matching the downstream runtime).
func (c *componentAllowPolicyConfig) ApplyPolicy(_ Policy) error { return nil }

func (c *componentAllowPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	np := kubernetes.CreateNetworkPolicy(c.ComponentName+"-allow-ingress-traffic", app.Namespace)
	np.Labels = nil
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, metav1.LabelSelector{
		MatchLabels: map[string]string{c.podSelectorKey(): c.ComponentName},
	})
	kubernetes.SetNetworkPolicyPolicyTypes(np, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress})

	proto := corev1.ProtocolTCP
	for _, tr := range c.Rules {
		rule := networkingv1.NetworkPolicyIngressRule{}
		for _, src := range tr.Sources {
			peer := networkingv1.NetworkPolicyPeer{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"kubernetes.io/metadata.name": src.Namespace,
					},
				},
			}
			if src.PodSelector != nil {
				peer.PodSelector = src.PodSelector
			}
			kubernetes.AddNetworkPolicyIngressPeer(&rule, peer)
		}
		for _, p := range tr.Ports {
			port := p
			kubernetes.AddNetworkPolicyIngressPort(&rule, networkingv1.NetworkPolicyPort{
				Port:     &port,
				Protocol: &proto,
			})
		}
		kubernetes.AddNetworkPolicyIngressRule(np, rule)
	}

	obj := client.Object(np)
	return []*client.Object{&obj}, nil
}

// synthesizeNetworkPolicies traverses all leaf bundles in the cluster and, for
// each bundle, scans application configs implementing trafficSourceCollector. For
// each component with at least one collector providing non-empty BackendPorts and
// TrafficSources, it appends a componentAllowPolicyConfig application.
//
// Runs as a cluster post-build stage (Phase 4), after trait application — so the
// synthesized policies are not subject to Enforceable.ApplyPolicy (intentional).
func synthesizeNetworkPolicies(cluster *stack.Cluster, labelKey string) {
	if cluster == nil {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		synthesizeForBundle(bundle, labelKey)
	})
}

func synthesizeForBundle(bundle *stack.Bundle, labelKey string) {
	if bundle == nil {
		return
	}

	// Group collectors by component name, preserving insertion order for determinism.
	type groupEntry struct {
		collectors []trafficSourceCollector
		namespace  string
	}
	byComponent := map[string]*groupEntry{}
	var componentOrder []string

	for _, appPtr := range bundle.Applications {
		col, ok := appPtr.Config.(trafficSourceCollector)
		if !ok {
			continue
		}
		name := col.TargetComponentName()
		if _, exists := byComponent[name]; !exists {
			byComponent[name] = &groupEntry{}
			componentOrder = append(componentOrder, name)
		}
		byComponent[name].collectors = append(byComponent[name].collectors, col)
		byComponent[name].namespace = appPtr.Namespace
	}

	for _, compName := range componentOrder {
		entry := byComponent[compName]
		rules := buildTrafficRules(entry.collectors)
		if len(rules) == 0 {
			continue
		}
		autoApp := stack.NewApplication(
			compName+"-allow-ingress-traffic",
			entry.namespace,
			&componentAllowPolicyConfig{ComponentName: compName, Rules: rules, PodSelectorKey: labelKey},
		)
		bundle.Applications = append(bundle.Applications, autoApp)
	}
}

// buildTrafficRules returns one trafficRule per collector with non-empty ports
// and non-empty sources, deduplicating identical rules.
func buildTrafficRules(collectors []trafficSourceCollector) []trafficRule {
	seen := map[string]struct{}{}
	var rules []trafficRule
	for _, col := range collectors {
		ports := col.BackendPorts()
		if len(ports) == 0 {
			continue // external-only backend; no component-local ports to protect
		}
		sources := col.TrafficSources()
		if len(sources) == 0 {
			continue // no traffic sources configured; synthesis suppressed
		}
		key := trafficRuleKey(sources, ports)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, trafficRule{Sources: sources, Ports: ports})
	}
	return rules
}

// trafficRuleKey returns a canonical deduplication key for a (sources, ports) pair.
// Sources are sorted by namespace then by full key=value serialization of matchLabels.
// Ports use the order from the caller (numeric ascending, then named lexical).
func trafficRuleKey(sources []netpol.TrafficSource, ports []intstr.IntOrString) string {
	srcKeys := make([]string, 0, len(sources))
	for _, src := range sources {
		k := src.Namespace
		if src.PodSelector != nil && len(src.PodSelector.MatchLabels) > 0 {
			labelParts := make([]string, 0, len(src.PodSelector.MatchLabels))
			for lk, lv := range src.PodSelector.MatchLabels {
				labelParts = append(labelParts, lk+"="+lv)
			}
			sort.Strings(labelParts)
			k += "/" + strings.Join(labelParts, ",")
		}
		srcKeys = append(srcKeys, k)
	}
	sort.Strings(srcKeys)

	portKeys := make([]string, 0, len(ports))
	for _, p := range ports {
		portKeys = append(portKeys, p.String())
	}

	return fmt.Sprintf("src:%s;ports:%s", strings.Join(srcKeys, "|"), strings.Join(portKeys, "|"))
}

// --- Egress synthesis (downstream-supplied, non-authorable peers) ---

// componentEgressPolicyConfig is the ApplicationConfig for an auto-generated
// NetworkPolicy that allows egress from a component's pods to downstream-supplied
// dependency-graph peers. The resource name is {component}-allow-egress-traffic,
// distinct from the inbound synthesis and the explicit networkpolicy trait, so it
// is purely additive.
type componentEgressPolicyConfig struct {
	ComponentName string
	Peers         []netpol.EgressPeer
	// PodSelectorKey is the label key selecting the component's own pods (the egress
	// source pods this policy allows out). Empty => the library default key
	// (ComponentLabelKeyForDomain(DefaultDomain)); the transform path always passes the
	// resolved, domain-aware key, so this default only applies to directly-built configs.
	PodSelectorKey string
}

// podSelectorKey returns the configured selector key, defaulting to the library default so a
// directly-built config never emits an empty-key selector.
func (c *componentEgressPolicyConfig) podSelectorKey() string {
	if c.PodSelectorKey == "" {
		return ComponentLabelKeyForDomain(DefaultDomain)
	}
	return c.PodSelectorKey
}

// ApplyPolicy is a no-op: a synthesized NetworkPolicy has no enforceable policy
// fields. Synthesis runs after the trait Enforceable.ApplyPolicy pass, so these
// configs are intentionally never policy-checked (matching the inbound path).
func (c *componentEgressPolicyConfig) ApplyPolicy(_ Policy) error { return nil }

func (c *componentEgressPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	np := kubernetes.CreateNetworkPolicy(c.ComponentName+"-allow-egress-traffic", app.Namespace)
	np.Labels = nil
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, metav1.LabelSelector{
		MatchLabels: map[string]string{c.podSelectorKey(): c.ComponentName},
	})
	kubernetes.SetNetworkPolicyPolicyTypes(np, []networkingv1.PolicyType{networkingv1.PolicyTypeEgress})

	// Protocol is a deliberate TCP constant: the per-peer signal carries no
	// protocol, and UDP/DNS egress is owned by the downstream runtime's namespace-level
	// allow-dns-egress baseline, not component-scoped synthesis.
	proto := corev1.ProtocolTCP
	// Normalize here too (not just on the synthesis path) so a config built
	// directly can never emit an all-ports rule from an empty-port peer.
	for _, peer := range buildEgressPeers(c.Peers) {
		// One egress rule per peer: a NetworkPolicyEgressRule is (any To) AND (any
		// Ports), so grouping peers with differing ports would cross-product them
		// (one peer reachable on another's ports).
		rule := networkingv1.NetworkPolicyEgressRule{}
		to := networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": peer.Namespace,
				},
			},
		}
		if peer.PodSelector != nil {
			to.PodSelector = peer.PodSelector
		}
		kubernetes.AddNetworkPolicyEgressPeer(&rule, to)
		for _, p := range peer.Ports {
			port := p
			kubernetes.AddNetworkPolicyEgressPort(&rule, networkingv1.NetworkPolicyPort{
				Port:     &port,
				Protocol: &proto,
			})
		}
		kubernetes.AddNetworkPolicyEgressRule(np, rule)
	}

	obj := client.Object(np)
	return []*client.Object{&obj}, nil
}

// synthesizeEgressNetworkPolicies traverses all leaf bundles and, for each primary
// component application with downstream-supplied egress peers, appends a
// componentEgressPolicyConfig application emitting a per-component egress
// NetworkPolicy. The signal is non-authorable (TransformContext.EgressPeers), so
// this is a no-op on the kurel path where no peers are supplied.
//
// Runs as a cluster post-build stage (Phase 4), after trait application — so the
// synthesized policies are not subject to Enforceable.ApplyPolicy (intentional).
func synthesizeEgressNetworkPolicies(cluster *stack.Cluster, componentMap map[string]componentEntry, egressPeers map[string][]netpol.EgressPeer, labelKey string) {
	if cluster == nil || len(egressPeers) == 0 {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		var autoApps []*stack.Application
		for _, app := range bundle.Applications {
			// Match the primary component app by identity, not just by name: a
			// trait sub-app whose name collides with a component name would match
			// componentMap[app.Name] and could otherwise attach the policy beside
			// the wrong app in the wrong bundle.
			entry, ok := componentMap[app.Name]
			if !ok || app != entry.app {
				continue
			}
			peers := buildEgressPeers(egressPeers[app.Name])
			if len(peers) == 0 {
				continue
			}
			autoApps = append(autoApps, stack.NewApplication(
				app.Name+"-allow-egress-traffic",
				app.Namespace,
				&componentEgressPolicyConfig{ComponentName: app.Name, Peers: peers, PodSelectorKey: labelKey},
			))
		}
		bundle.Applications = append(bundle.Applications, autoApps...)
	})
}

// buildEgressPeers normalizes downstream-supplied egress peers for deterministic output:
// it drops peers with no ports (an all-ports egress rule would over-permit; an
// underivable-port peer stays authored via the escape hatch), copies and sorts each
// surviving peer's ports, then deduplicates and sorts peers by a canonical key so
// the emitted NetworkPolicy is byte-stable regardless of the graph's iteration order.
func buildEgressPeers(peers []netpol.EgressPeer) []netpol.EgressPeer {
	seen := map[string]struct{}{}
	out := make([]netpol.EgressPeer, 0, len(peers))
	for _, p := range peers {
		if len(p.Ports) == 0 {
			continue // no derivable destination ports; peer stays authored
		}
		ports := make([]intstr.IntOrString, len(p.Ports))
		copy(ports, p.Ports)
		sortIntOrStringPorts(ports)
		peer := netpol.EgressPeer{Namespace: p.Namespace, PodSelector: p.PodSelector, Ports: ports}
		key := egressPeerKey(peer)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, peer)
	}
	sort.SliceStable(out, func(i, j int) bool { return egressPeerKey(out[i]) < egressPeerKey(out[j]) })
	return out
}

// egressPeerKey returns a canonical dedup/ordering key for an egress peer. Ports
// include their Type so Int(80) and String("80") do not collide.
func egressPeerKey(p netpol.EgressPeer) string {
	k := p.Namespace
	if p.PodSelector != nil && len(p.PodSelector.MatchLabels) > 0 {
		labelParts := make([]string, 0, len(p.PodSelector.MatchLabels))
		for lk, lv := range p.PodSelector.MatchLabels {
			labelParts = append(labelParts, lk+"="+lv)
		}
		sort.Strings(labelParts)
		k += "/" + strings.Join(labelParts, ",")
	}
	portKeys := make([]string, 0, len(p.Ports))
	for _, pt := range p.Ports {
		portKeys = append(portKeys, fmt.Sprintf("%d:%s", pt.Type, pt.String()))
	}
	return fmt.Sprintf("ns:%s;ports:%s", k, strings.Join(portKeys, "|"))
}

// sortIntOrStringPorts orders ports numeric-ascending then named-lexical, matching
// the inbound synthesis convention.
func sortIntOrStringPorts(ports []intstr.IntOrString) {
	sort.SliceStable(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.Type != b.Type {
			return a.Type == intstr.Int // numeric ports before named ports
		}
		if a.Type == intstr.Int {
			return a.IntVal < b.IntVal
		}
		return a.StrVal < b.StrVal
	})
}

// --- Endpoint-ingress synthesis (platform-supplied, non-authorable target-side allows) ---

// validateEndpoint fails closed on a malformed endpoint: the pod selector is required and
// matchLabels-only, and at least one port is required. Used to reject bad EndpointProvider
// output (a launcher-internal bug) and to guard directly-built endpoint-ingress configs.
func validateEndpoint(e netpol.Endpoint) error {
	if e.PodSelector == nil || len(e.PodSelector.MatchLabels) == 0 {
		return errors.New("endpoint pod selector must have non-empty matchLabels")
	}
	if len(e.PodSelector.MatchExpressions) > 0 {
		return errors.New("endpoint pod selector must be matchLabels-only (no matchExpressions)")
	}
	if len(e.Ports) == 0 {
		return errors.New("endpoint must declare at least one port")
	}
	return nil
}

// endpointValid reports whether e passes validateEndpoint — a boolean form for fail-closed
// drop/skip paths (Generate, grouping) that must not surface a nil error.
func endpointValid(e netpol.Endpoint) bool { return validateEndpoint(e) == nil }

// validSources keeps only fail-closed traffic sources: each must carry a namespace and a
// non-empty matchLabels pod selector (matchLabels-only). Namespace-wide sources are dropped —
// unlike the inbound family, endpoint-ingress never allows "all pods in a namespace". The
// survivors are sorted by canonical key so the emitted rule's From order (and the dedup key)
// is byte-stable regardless of the caller's source order.
func validSources(sources []netpol.TrafficSource) []netpol.TrafficSource {
	out := make([]netpol.TrafficSource, 0, len(sources))
	for _, s := range sources {
		if s.Namespace == "" || s.PodSelector == nil || len(s.PodSelector.MatchLabels) == 0 {
			continue
		}
		if len(s.PodSelector.MatchExpressions) > 0 {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return trafficSourceKey(out[i]) < trafficSourceKey(out[j]) })
	return out
}

// trafficSourceKey returns a canonical key for one (validated) traffic source: namespace plus
// sorted key=value matchLabels.
func trafficSourceKey(s netpol.TrafficSource) string {
	labelParts := make([]string, 0, len(s.PodSelector.MatchLabels))
	for lk, lv := range s.PodSelector.MatchLabels {
		labelParts = append(labelParts, lk+"="+lv)
	}
	sort.Strings(labelParts)
	return s.Namespace + "/" + strings.Join(labelParts, ",")
}

type endpointIngressRule struct {
	Sources []netpol.TrafficSource // fail-closed: each carries a namespace + matchLabels pod selector
}

// componentEndpointIngressPolicyConfig is the ApplicationConfig for an auto-generated
// NetworkPolicy that allows ingress to one of a component's declared endpoints (e.g. an
// operator-managed database's instance pods) from platform-supplied sources. The resource
// name is {component}-allow-endpoint-ingress, distinct from the other synthesized families and
// the explicit networkpolicy trait, so it is purely additive. The podSelector is the endpoint's
// own selector — deliberately not the component-label key — because it protects operator pods
// that carry no component-provenance label.
type componentEndpointIngressPolicyConfig struct {
	ComponentName string
	Endpoint      netpol.Endpoint       // this policy's single endpoint (podSelector + ports)
	Rules         []endpointIngressRule // one per distinct source set, deduplicated
}

// ApplyPolicy is a no-op: a synthesized NetworkPolicy has no enforceable policy fields
// (matching the other synthesized families).
func (c *componentEndpointIngressPolicyConfig) ApplyPolicy(_ Policy) error { return nil }

func (c *componentEndpointIngressPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Fail closed: never emit a deny-all (no rules) or allow-from-all (bad selectors) policy,
	// even for a directly-built config. Filter sources with the same strict rule as synthesis.
	if !endpointValid(c.Endpoint) {
		return nil, nil // invalid endpoint: emit nothing (not an error at the Generate boundary)
	}
	var rules []endpointIngressRule
	for _, r := range c.Rules {
		if srcs := validSources(r.Sources); len(srcs) > 0 {
			rules = append(rules, endpointIngressRule{Sources: srcs})
		}
	}
	if len(rules) == 0 {
		return nil, nil // no valid sources; an Ingress NP with zero rules would deny all ingress
	}

	np := kubernetes.CreateNetworkPolicy(c.ComponentName+"-allow-endpoint-ingress", app.Namespace)
	np.Labels = nil
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, *c.Endpoint.PodSelector)
	kubernetes.SetNetworkPolicyPolicyTypes(np, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress})

	proto := corev1.ProtocolTCP
	for _, r := range rules {
		rule := networkingv1.NetworkPolicyIngressRule{}
		for _, src := range r.Sources {
			peer := networkingv1.NetworkPolicyPeer{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": src.Namespace},
				},
				PodSelector: src.PodSelector,
			}
			kubernetes.AddNetworkPolicyIngressPeer(&rule, peer)
		}
		for _, p := range c.Endpoint.Ports {
			port := p
			kubernetes.AddNetworkPolicyIngressPort(&rule, networkingv1.NetworkPolicyPort{
				Port:     &port,
				Protocol: &proto,
			})
		}
		kubernetes.AddNetworkPolicyIngressRule(np, rule)
	}

	obj := client.Object(np)
	return []*client.Object{&obj}, nil
}

// synthesizeEndpointIngressNetworkPolicies traverses all leaf bundles and, for each primary
// component application with platform-supplied ingress peers, appends
// componentEndpointIngressPolicyConfig applications emitting per-endpoint ingress
// NetworkPolicies. The signal is non-authorable (TransformContext.IngressPeers), so this is a
// no-op on the kurel path where no peers are supplied.
//
// Runs as a cluster post-build stage (Phase 4), after trait application — so the synthesized
// policies are not subject to Enforceable.ApplyPolicy (intentional).
func synthesizeEndpointIngressNetworkPolicies(cluster *stack.Cluster, componentMap map[string]componentEntry, ingressPeers map[string][]netpol.IngressPeer) {
	if cluster == nil || len(ingressPeers) == 0 {
		return
	}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		var autoApps []*stack.Application
		for _, app := range bundle.Applications {
			// Match the primary component app by identity, not just by name (same guard as
			// the egress path): a trait sub-app whose name collides with a component name
			// could otherwise attach the policy beside the wrong app in the wrong bundle.
			entry, ok := componentMap[app.Name]
			if !ok || app != entry.app {
				continue
			}
			groups := groupIngressPeers(ingressPeers[app.Name])
			// One NP per distinct endpoint. A single-endpoint component keeps the bare name
			// (back-compat); a multi-endpoint component (e.g. a postgresql cluster + its pooler)
			// gets a per-endpoint suffix so the names don't collide. The suffix is a content
			// hash of the endpoint, not a positional index, so each endpoint's NP name is stable
			// across unrelated endpoint additions (an index would renumber, causing spurious
			// downstream prune+create).
			multi := len(groups) > 1
			for _, g := range groups {
				autoApps = append(autoApps, stack.NewApplication(
					endpointIngressPolicyName(app.Name, g.Endpoint, multi),
					app.Namespace,
					&componentEndpointIngressPolicyConfig{
						ComponentName: app.Name,
						Endpoint:      g.Endpoint,
						Rules:         g.Rules,
					},
				))
			}
		}
		bundle.Applications = append(bundle.Applications, autoApps...)
	})
}

type endpointIngressGroup struct {
	Endpoint netpol.Endpoint
	Rules    []endpointIngressRule
}

// groupIngressPeers normalizes platform-supplied ingress peers into per-endpoint groups for
// deterministic, fail-closed output: it drops peers whose endpoint is malformed or whose sources
// are all namespace-wide, groups the survivors by endpoint (selector + ports), deduplicates
// identical source sets within a group, and sorts groups + rules by canonical key.
func groupIngressPeers(peers []netpol.IngressPeer) []endpointIngressGroup {
	byEndpoint := map[string]*endpointIngressGroup{}
	var order []string
	for _, p := range peers {
		if !endpointValid(p.Endpoint) {
			continue
		}
		srcs := validSources(p.Sources)
		if len(srcs) == 0 {
			continue
		}
		ports := make([]intstr.IntOrString, len(p.Endpoint.Ports))
		copy(ports, p.Endpoint.Ports)
		sortIntOrStringPorts(ports)
		ep := netpol.Endpoint{PodSelector: p.Endpoint.PodSelector, Ports: ports}
		ek := endpointKey(ep)
		g, ok := byEndpoint[ek]
		if !ok {
			g = &endpointIngressGroup{Endpoint: ep}
			byEndpoint[ek] = g
			order = append(order, ek)
		}
		sk := sourcesKey(srcs)
		dup := false
		for _, r := range g.Rules {
			if sourcesKey(r.Sources) == sk {
				dup = true
				break
			}
		}
		if !dup {
			g.Rules = append(g.Rules, endpointIngressRule{Sources: srcs})
		}
	}
	out := make([]endpointIngressGroup, 0, len(order))
	for _, ek := range order {
		g := byEndpoint[ek]
		sort.SliceStable(g.Rules, func(i, j int) bool { return sourcesKey(g.Rules[i].Sources) < sourcesKey(g.Rules[j].Sources) })
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool { return endpointKey(out[i].Endpoint) < endpointKey(out[j].Endpoint) })
	return out
}

// endpointKey returns a canonical dedup/ordering key for an endpoint (matchLabels + Type-tagged
// ports, mirroring egressPeerKey).
func endpointKey(e netpol.Endpoint) string {
	labelParts := make([]string, 0, len(e.PodSelector.MatchLabels))
	for lk, lv := range e.PodSelector.MatchLabels {
		labelParts = append(labelParts, lk+"="+lv)
	}
	sort.Strings(labelParts)
	portKeys := make([]string, 0, len(e.Ports))
	for _, pt := range e.Ports {
		portKeys = append(portKeys, fmt.Sprintf("%d:%s", pt.Type, pt.String()))
	}
	return fmt.Sprintf("sel:%s;ports:%s", strings.Join(labelParts, ","), strings.Join(portKeys, "|"))
}

// endpointIngressPolicyName returns the NetworkPolicy name for a component's endpoint-ingress
// policy. A single-endpoint component keeps the bare "{comp}-allow-endpoint-ingress" name
// (back-compat with #213). When a component exposes more than one distinct endpoint (multi=true,
// e.g. a postgresql cluster plus its pooler), every policy is suffixed with a short content hash
// of the endpoint so the names are distinct and — because the hash is derived from the endpoint's
// own selector+ports, not its position — stable across unrelated endpoint additions.
func endpointIngressPolicyName(comp string, e netpol.Endpoint, multi bool) string {
	base := comp + "-allow-endpoint-ingress"
	if !multi {
		return base
	}
	sum := sha256.Sum256([]byte(endpointKey(e)))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

// sourcesKey returns a canonical dedup/ordering key for a source set — per-source keys sorted
// so order does not affect the key. (validSources already sorts, but sort defensively.)
func sourcesKey(sources []netpol.TrafficSource) string {
	srcKeys := make([]string, 0, len(sources))
	for _, s := range sources {
		srcKeys = append(srcKeys, trafficSourceKey(s))
	}
	sort.Strings(srcKeys)
	return strings.Join(srcKeys, "|")
}
