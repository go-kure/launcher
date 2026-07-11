package oam

import (
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
}

// ApplyPolicy is a no-op: a synthesized NetworkPolicy has no enforceable policy
// fields. Synthesis runs after the trait Enforceable.ApplyPolicy pass, so these
// configs are intentionally never policy-checked (matching crane).
func (c *componentAllowPolicyConfig) ApplyPolicy(_ Policy) error { return nil }

func (c *componentAllowPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	np := kubernetes.CreateNetworkPolicy(c.ComponentName+"-allow-ingress-traffic", app.Namespace)
	np.Labels = nil
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, metav1.LabelSelector{
		MatchLabels: map[string]string{"app": c.ComponentName},
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
func synthesizeNetworkPolicies(cluster *stack.Cluster) {
	if cluster == nil {
		return
	}
	walkLeafBundles(cluster.Node, synthesizeForBundle)
}

func synthesizeForBundle(bundle *stack.Bundle) {
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
			&componentAllowPolicyConfig{ComponentName: compName, Rules: rules},
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

// --- Egress synthesis (crane-supplied, non-authorable peers) ---

// componentEgressPolicyConfig is the ApplicationConfig for an auto-generated
// NetworkPolicy that allows egress from a component's pods to crane-supplied
// dependency-graph peers. The resource name is {component}-allow-egress-traffic,
// distinct from the inbound synthesis and the explicit networkpolicy trait, so it
// is purely additive.
type componentEgressPolicyConfig struct {
	ComponentName string
	Peers         []netpol.EgressPeer
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
		MatchLabels: map[string]string{"app": c.ComponentName},
	})
	kubernetes.SetNetworkPolicyPolicyTypes(np, []networkingv1.PolicyType{networkingv1.PolicyTypeEgress})

	// Protocol is a deliberate TCP constant: the per-peer signal carries no
	// protocol, and UDP/DNS egress is owned by crane's namespace-level
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
// component application with crane-supplied egress peers, appends a
// componentEgressPolicyConfig application emitting a per-component egress
// NetworkPolicy. The signal is non-authorable (TransformContext.EgressPeers), so
// this is a no-op on the kurel path where no peers are supplied.
//
// Runs as a cluster post-build stage (Phase 4), after trait application — so the
// synthesized policies are not subject to Enforceable.ApplyPolicy (intentional).
func synthesizeEgressNetworkPolicies(cluster *stack.Cluster, componentMap map[string]componentEntry, egressPeers map[string][]netpol.EgressPeer) {
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
				&componentEgressPolicyConfig{ComponentName: app.Name, Peers: peers},
			))
		}
		bundle.Applications = append(bundle.Applications, autoApps...)
	})
}

// buildEgressPeers normalizes crane-supplied egress peers for deterministic output:
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
