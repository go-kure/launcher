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
