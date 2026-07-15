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

// serviceBackendNamer is optionally implemented by a component config whose Kubernetes Service
// name differs from its component name (e.g. a statefulset's headless service). Used to resolve
// an expose backendRef (a Service name) back to the sibling component that owns it (#227).
type serviceBackendNamer interface {
	BackendServiceName() string
}

// servicePortProvider is optionally implemented by a component config that exposes a Service port
// (the webservice/statefulset convention: Service name == component name). Used to decide whether a
// component actually owns a routable Service.
type servicePortProvider interface {
	ServicePort() int32
}

// componentServiceName returns the Kubernetes Service name a component owns and whether it owns one.
// A component is a valid backendRef target only if it declares an explicit BackendServiceName or a
// positive ServicePort; a Service-less component (e.g. a worker, or a daemonset with no port) owns
// no Service, so its name must NOT enter serviceToComponent — otherwise it would shadow a bare
// external Service of the same name (misrouting a #239 backendSelector target) or fabricate a false
// R3 ambiguity against another component's real Service name.
func componentServiceName(app *stack.Application) (string, bool) {
	if sn, ok := app.Config.(serviceBackendNamer); ok && sn.BackendServiceName() != "" {
		return sn.BackendServiceName(), true
	}
	if pp, ok := app.Config.(servicePortProvider); ok && pp.ServicePort() > 0 {
		return app.Name, true
	}
	return "", false
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

	appendIngressTrafficRules(np, c.Rules)

	obj := client.Object(np)
	return []*client.Object{&obj}, nil
}

// appendIngressTrafficRules emits one NetworkPolicy ingress rule per trafficRule: a namespace peer
// (optionally narrowed by the source's matchLabels pod selector, else namespace-wide) for each
// source, and the rule's ports (TCP). Shared by the inbound component family and the external
// backend family so their From/Ports emission cannot drift.
func appendIngressTrafficRules(np *networkingv1.NetworkPolicy, rules []trafficRule) {
	proto := corev1.ProtocolTCP
	for _, tr := range rules {
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
}

// backendIngressAllowPolicyConfig is the ApplicationConfig for an auto-generated NetworkPolicy that
// allows ingress to an EXTERNAL routing backend — a bare Service with no owning OAM component (#239).
// Unlike componentAllowPolicyConfig it selects an explicit, authored pod selector (the backend's
// pods) rather than a component-label key, and takes an explicit resource name. Like the inbound
// family (and unlike endpoint-ingress), it permits namespace-wide sources, since routing
// trafficSources are typically namespace-scoped.
type backendIngressAllowPolicyConfig struct {
	PolicyName  string
	PodSelector *metav1.LabelSelector // authored backend selector (matchLabels-only)
	Rules       []trafficRule
}

// ApplyPolicy is a no-op: a synthesized NetworkPolicy has no enforceable policy fields.
func (c *backendIngressAllowPolicyConfig) ApplyPolicy(_ Policy) error { return nil }

func (c *backendIngressAllowPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Residual fail-closed: never emit a policy that selects every pod in the namespace, even for a
	// directly-built config (the synthesis path already validates the selector before constructing).
	// A directly-built invalid config emits nothing, not an error.
	if !matchLabelsSelectorValid(c.PodSelector) {
		return nil, nil
	}
	np := kubernetes.CreateNetworkPolicy(c.PolicyName, app.Namespace)
	np.Labels = nil
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, *c.PodSelector)
	kubernetes.SetNetworkPolicyPolicyTypes(np, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress})

	appendIngressTrafficRules(np, c.Rules)

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
func synthesizeNetworkPolicies(cluster *stack.Cluster, componentMap map[string]componentEntry, labelKey string) error {
	if cluster == nil {
		return nil
	}
	// Synthesis is entirely CLUSTER-wide (not per-bundle): components of one Application share a
	// namespace but are split across leaf bundles (dependency-aware: one per component; hierarchical:
	// one per tier). Resolving a backendRef to its sibling component, and merging a router's injected
	// allow onto that component's own bundle, therefore requires cluster-wide lookups (#242) — the
	// same model #239 uses for external backends.
	reg := newNPSynthesisRegistry()
	if err := reg.buildLookups(cluster, componentMap); err != nil {
		return err
	}
	// walkLeafBundles cannot abort traversal, so capture the first error and guard the callback.
	var firstErr error
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		if firstErr != nil {
			return
		}
		if err := synthesizeForBundle(bundle, reg); err != nil {
			firstErr = err
		}
	})
	if firstErr != nil {
		return firstErr
	}
	// Emit component policies first so the external-vs-component collision check (emitExternalBackends)
	// sees every component name; both only queue apps.
	if err := reg.emitComponents(labelKey); err != nil {
		return err
	}
	if err := reg.emitExternalBackends(); err != nil {
		return err
	}
	// Every append is deferred to here, so an error at any point above leaves the cluster
	// completely unmutated (no partially-decorated bundle observable by a direct caller).
	reg.flush()
	return nil
}

// buildLookups populates the registry's cluster-wide read-only maps: appToBundle (via a leaf-bundle
// walk), componentPlacement (each component's own bundle + namespace), and serviceToComponent (each
// component's Service name → its name). It fails fast (R3) when two components resolve to the same
// Service name — an ambiguous #227/#242 routing target must error, not silently misroute.
func (r *npSynthesisRegistry) buildLookups(cluster *stack.Cluster, componentMap map[string]componentEntry) error {
	appToBundle := map[*stack.Application]*stack.Bundle{}
	walkLeafBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, a := range bundle.Applications {
			appToBundle[a] = bundle
		}
	})
	// Sort component names so the R3 error (and any map build) is deterministic.
	names := make([]string, 0, len(componentMap))
	for name := range componentMap {
		names = append(names, name)
	}
	sort.Strings(names)
	svcOwner := map[string]string{} // svc name → first component that claimed it
	for _, name := range names {
		entry := componentMap[name]
		r.componentPlacement[name] = componentPlacement{bundle: appToBundle[entry.app], namespace: entry.app.Namespace}
		svc, owns := componentServiceName(entry.app)
		if !owns {
			continue // Service-less component: not a backendRef target, must not claim a Service name
		}
		if prev, dup := svcOwner[svc]; dup {
			a, b := prev, name
			if a > b {
				a, b = b, a
			}
			return errors.Errorf(
				"components %q and %q resolve to the same Service name %q — ambiguous backendRef target",
				a, b, svc)
		}
		svcOwner[svc] = name
		r.serviceToComponent[svc] = name
	}
	return nil
}

// emitComponents queues one {comp}-allow-ingress-traffic policy per accumulated component, in its own
// bundle, merging the component's own routing collectors with any injected backendRef rules. Runs
// after the walk; queued apps are appended only by flush().
func (r *npSynthesisRegistry) emitComponents(labelKey string) error {
	for _, compName := range r.componentOrder {
		ce := r.components[compName]
		rules := buildTrafficRules(ce.collectors)
		rules = appendDedupTrafficRules(rules, ce.injected)
		if len(rules) == 0 {
			continue
		}
		policyName := compName + "-allow-ingress-traffic"
		// Key by namespace/name (not bare name) to preserve the #239 external-vs-component collision
		// check and avoid future cross-namespace false positives.
		r.emitted[ce.namespace+"/"+policyName] = struct{}{}
		r.queue(ce.bundle, stack.NewApplication(
			policyName,
			ce.namespace,
			&componentAllowPolicyConfig{ComponentName: compName, Rules: rules, PodSelectorKey: labelKey},
		))
	}
	return nil
}

// externalBackendEntry accumulates one external routing backend (a bare Service with no owning
// component) cluster-wide, keyed namespace-qualified. rules merge across every router that names it;
// bundle is the leaf bundle its synthesized policy attaches to (the first router's, by walk order).
type externalBackendEntry struct {
	selector  *metav1.LabelSelector
	namespace string
	service   string
	bundle    *stack.Bundle
	rules     []trafficRule
}

// pendingSynthApp is a synthesized application queued for append to its bundle. All appends are
// deferred until every bundle has been validated, so a later error leaves no partial mutation.
type pendingSynthApp struct {
	bundle *stack.Bundle
	app    *stack.Application
}

// componentPlacement records where a component's synthesized inbound policy must land: its own leaf
// bundle and namespace. Built cluster-wide so a router in one bundle can inject an allow onto a
// backend component in another bundle (#242).
type componentPlacement struct {
	bundle    *stack.Bundle
	namespace string
}

// componentInboundEntry accumulates one component's inbound-policy inputs CLUSTER-wide: its own
// routing collectors plus rules injected by backendRefs from routers in any bundle (#227/#242).
// bundle/namespace are the component's own (set once), so the merged {comp}-allow-ingress-traffic
// policy is emitted exactly once in the component's bundle regardless of walk order.
type componentInboundEntry struct {
	collectors []trafficSourceCollector
	injected   []trafficRule
	namespace  string
	bundle     *stack.Bundle
}

// npSynthesisRegistry holds cluster-wide synthesis state: read-only lookups (Service name →
// component, component → placement), the per-component and external-backend accumulators, every
// emitted policy's namespace/name (to detect collisions), and the deferred append queue.
type npSynthesisRegistry struct {
	serviceToComponent map[string]string             // svc name → component name (cluster-wide, ambiguity-checked)
	componentPlacement map[string]componentPlacement // component name → its own bundle + namespace
	components         map[string]*componentInboundEntry
	componentOrder     []string
	emitted            map[string]struct{}              // namespace/name of every synthesized policy
	externalBackends   map[string]*externalBackendEntry // key: namespace\x00service
	externalOrder      []string
	pending            []pendingSynthApp
}

func newNPSynthesisRegistry() *npSynthesisRegistry {
	return &npSynthesisRegistry{
		serviceToComponent: map[string]string{},
		componentPlacement: map[string]componentPlacement{},
		components:         map[string]*componentInboundEntry{},
		emitted:            map[string]struct{}{},
		externalBackends:   map[string]*externalBackendEntry{},
	}
}

// ensureComponent returns the accumulator for a component, creating it (and recording insertion
// order) on first use. bundle/namespace are set only on creation. A later call that disagrees on
// bundle/namespace is a producer bug (a real component has one placement) — error rather than
// silently queue the policy into the wrong bundle.
func (r *npSynthesisRegistry) ensureComponent(name string, bundle *stack.Bundle, namespace string) (*componentInboundEntry, error) {
	ce, ok := r.components[name]
	if !ok {
		ce = &componentInboundEntry{namespace: namespace, bundle: bundle}
		r.components[name] = ce
		r.componentOrder = append(r.componentOrder, name)
		return ce, nil
	}
	if ce.bundle != bundle || ce.namespace != namespace {
		return nil, errors.Errorf(
			"component %q resolved to inconsistent placement (namespace %q vs %q)",
			name, ce.namespace, namespace)
	}
	return ce, nil
}

// queue records a synthesized app for deferred append to its bundle.
func (r *npSynthesisRegistry) queue(bundle *stack.Bundle, app *stack.Application) {
	r.pending = append(r.pending, pendingSynthApp{bundle: bundle, app: app})
}

// flush appends every queued app to its bundle. Called once, after all validation succeeds — the
// only step that mutates a bundle.
func (r *npSynthesisRegistry) flush() {
	for _, p := range r.pending {
		p.bundle.Applications = append(p.bundle.Applications, p.app)
	}
}

// emitExternalBackends queues one {service}-allow-ingress-traffic policy per accumulated external
// backend, after every bundle has contributed. It fails the transform when an external policy name
// collides with a policy already emitted this pass (a component whose Service name differs, leaving
// a bare external Service that shares the component's name). Runs after the walk so the collision
// check sees every component policy cluster-wide; the queued apps are appended only by flush().
func (r *npSynthesisRegistry) emitExternalBackends() error {
	sort.Strings(r.externalOrder)
	for _, key := range r.externalOrder {
		eb := r.externalBackends[key]
		rules := appendDedupTrafficRules(nil, eb.rules)
		if len(rules) == 0 {
			continue
		}
		policyName := eb.service + "-allow-ingress-traffic"
		nsName := eb.namespace + "/" + policyName
		if _, dup := r.emitted[nsName]; dup {
			return errors.Errorf(
				"external backend Service %q in namespace %q collides with a synthesized policy named %q",
				eb.service, eb.namespace, policyName)
		}
		r.emitted[nsName] = struct{}{}
		r.queue(eb.bundle, stack.NewApplication(
			policyName,
			eb.namespace,
			&backendIngressAllowPolicyConfig{PolicyName: policyName, PodSelector: eb.selector, Rules: rules},
		))
	}
	return nil
}

// backendRefTargetCollector is optionally implemented by routing trait configs (IngressConfig,
// HTTPRouteConfig) that can route to a separate backend Service via expose backendRefs. It
// surfaces those external targets so ingress synthesis can land the allow on the backend's own
// pods instead of the exposing component's (#227).
type backendRefTargetCollector interface {
	BackendTargets() []netpol.BackendTarget
}

// synthesizeForBundle accumulates one leaf bundle's routing collectors into the cluster-wide
// registry (reg): each component's own collectors, plus rules injected onto a backend component
// (resolved cluster-wide, landing on the backend's OWN bundle — #242) or onto an external bare
// Service (#239). It never emits — emitComponents/emitExternalBackends do that after the whole
// cluster is walked, so nothing is appended to any bundle until every bundle validates.
func synthesizeForBundle(bundle *stack.Bundle, reg *npSynthesisRegistry) error {
	if bundle == nil {
		return nil
	}

	for _, appPtr := range bundle.Applications {
		col, ok := appPtr.Config.(trafficSourceCollector)
		if !ok {
			continue
		}
		routerComp := col.TargetComponentName()
		// The router's own collectors accumulate under its component, in this (the router's) bundle.
		if _, err := reg.ensureComponent(routerComp, bundle, appPtr.Namespace); err != nil {
			return err
		}
		reg.components[routerComp].collectors = append(reg.components[routerComp].collectors, col)

		// #227/#242: an external backendRef routes to a separate backend. Retarget its allow onto the
		// backend component's pods, resolved cluster-wide so the backend may live in another bundle.
		bt, ok := appPtr.Config.(backendRefTargetCollector)
		if !ok {
			continue
		}
		sources := col.TrafficSources()
		if len(sources) == 0 {
			continue // no traffic sources → nothing to allow
		}
		for _, target := range bt.BackendTargets() {
			backendComp, resolved := reg.serviceToComponent[target.ServiceName]
			if resolved {
				if backendComp == routerComp {
					continue // self (already handled above)
				}
				// Retarget onto the backend component's pods, in the backend's OWN bundle. An authored
				// backendSelector on a resolvable ref is ignored — component-label targeting wins.
				pl, ok := reg.componentPlacement[backendComp]
				if !ok {
					continue // backend has no known placement (defensive) → leave authored
				}
				bce, err := reg.ensureComponent(backendComp, pl.bundle, pl.namespace)
				if err != nil {
					return err
				}
				bce.injected = append(bce.injected, trafficRule{Sources: sources, Ports: target.Ports})
				continue
			}
			// #239: external bare Service. Synthesize only with an explicit, valid authored selector;
			// otherwise leave authored (no name-based inference). Accumulate cluster-wide so a Service
			// named across bundles is deduped/merged and a cross-bundle selector conflict is caught.
			if target.PodSelector == nil || !matchLabelsSelectorValid(target.PodSelector) {
				continue
			}
			ebKey := appPtr.Namespace + "\x00" + target.ServiceName
			eb, ok := reg.externalBackends[ebKey]
			if !ok {
				eb = &externalBackendEntry{
					selector:  target.PodSelector,
					namespace: appPtr.Namespace,
					service:   target.ServiceName,
					bundle:    bundle,
				}
				reg.externalBackends[ebKey] = eb
				reg.externalOrder = append(reg.externalOrder, ebKey)
			} else if !matchLabelsSelectorEqual(eb.selector, target.PodSelector) {
				return errors.Errorf(
					"external backend Service %q in namespace %q given conflicting backendSelector values",
					target.ServiceName, appPtr.Namespace)
			}
			eb.rules = append(eb.rules, trafficRule{Sources: sources, Ports: target.Ports})
		}
	}
	return nil
}

// matchLabelsSelectorValid is the bool form of validateMatchLabelsSelector, for fail-closed
// drop paths that must not surface a nil error (avoids a nilerr lint on Generate).
func matchLabelsSelectorValid(sel *metav1.LabelSelector) bool {
	return validateMatchLabelsSelector(sel) == nil
}

// matchLabelsSelectorEqual reports whether two matchLabels-only selectors are equal (both nil counts
// as equal). Used to detect an external Service given two different backendSelector values.
func matchLabelsSelectorEqual(a, b *metav1.LabelSelector) bool {
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

// appendDedupTrafficRules appends extra rules to base, skipping any whose (sources, ports) key
// already appears — so a backendRef-retargeted rule can't duplicate a rule the backend already
// has from its own routing traits.
func appendDedupTrafficRules(base, extra []trafficRule) []trafficRule {
	seen := map[string]struct{}{}
	for _, r := range base {
		seen[trafficRuleKey(r.Sources, r.Ports)] = struct{}{}
	}
	for _, r := range extra {
		if len(r.Ports) == 0 || len(r.Sources) == 0 {
			continue
		}
		key := trafficRuleKey(r.Sources, r.Ports)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		base = append(base, r)
	}
	return base
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
		// Residual defense behind the synthesizeEgressNetworkPolicies boundary check: reject a
		// ported peer with an invalid selector here too, so a directly-built config can never
		// emit a namespace-wide egress allow if the loud layer is ever bypassed.
		if err := validateMatchLabelsSelector(peer.PodSelector); err != nil {
			return nil, errors.Wrapf(err, "component %q egress peer", c.ComponentName)
		}
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
//
// Loud layer (fail-fast): it returns an error for any peer that carries ports but a nil, empty,
// or expression-bearing pod selector — which would otherwise emit a namespace-wide egress allow
// (to.PodSelector nil/empty = all pods in the namespace). EgressPeers is non-authorable
// (platform-supplied), so a malformed peer is a producer bug; erroring surfaces it at build time
// rather than silently widening egress — or, under default-deny, silently emitting a too-narrow
// rule. The len(Ports)==0 skip stays a documented escape hatch, not an invariant violation.
func synthesizeEgressNetworkPolicies(cluster *stack.Cluster, componentMap map[string]componentEntry, egressPeers map[string][]netpol.EgressPeer, labelKey string) error {
	if cluster == nil || len(egressPeers) == 0 {
		return nil
	}
	// Validate the whole non-authorable input up front (sorted keys → deterministic first error),
	// independent of which components happen to appear in a bundle.
	comps := make([]string, 0, len(egressPeers))
	for name := range egressPeers {
		comps = append(comps, name)
	}
	sort.Strings(comps)
	for _, name := range comps {
		for i, p := range egressPeers[name] {
			if len(p.Ports) == 0 {
				continue // documented escape hatch: underivable ports → peer stays authored
			}
			if err := validateMatchLabelsSelector(p.PodSelector); err != nil {
				return errors.Wrapf(err, "component %q egress peer[%d]", name, i)
			}
		}
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
	return nil
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

// validateMatchLabelsSelector is the single shared selector validator for the synthesis paths:
// a pod selector must be non-nil, carry non-empty matchLabels, and use matchLabels only (no
// matchExpressions). Both the egress fail-fast path and endpoint validation route through it so
// the "matchLabels-only, non-empty" rule cannot drift between the two synthesis families.
func validateMatchLabelsSelector(sel *metav1.LabelSelector) error {
	if sel == nil || len(sel.MatchLabels) == 0 {
		return errors.New("pod selector must have non-empty matchLabels")
	}
	if len(sel.MatchExpressions) > 0 {
		return errors.New("pod selector must be matchLabels-only (no matchExpressions)")
	}
	return nil
}

// validateEndpoint fails closed on a malformed endpoint: the pod selector is required and
// matchLabels-only, and at least one port is required. Used to reject bad EndpointProvider
// output (a launcher-internal bug) and to guard directly-built endpoint-ingress configs.
func validateEndpoint(e netpol.Endpoint) error {
	if err := validateMatchLabelsSelector(e.PodSelector); err != nil {
		return errors.Wrap(err, "endpoint")
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
	// PolicyName is the resource name for the emitted NetworkPolicy. Empty => the bare
	// {ComponentName}-allow-endpoint-ingress default (directly-built/test configs); the synthesis
	// path always sets the per-endpoint suffixed name so multi-endpoint resource names don't collide.
	PolicyName string
}

// policyName returns the configured resource name, defaulting to the bare
// {ComponentName}-allow-endpoint-ingress so a directly-built config still emits a valid single-
// endpoint name. Mirrors the podSelectorKey() default-fallback pattern of the other families.
func (c *componentEndpointIngressPolicyConfig) policyName() string {
	if c.PolicyName == "" {
		return c.ComponentName + "-allow-endpoint-ingress"
	}
	return c.PolicyName
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

	np := kubernetes.CreateNetworkPolicy(c.policyName(), app.Namespace)
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
				// Compute the per-endpoint name once and bind it to both the layout Application and
				// the emitted resource, so a multi-endpoint component's NetworkPolicy resource names
				// are distinct (not just its layout names).
				name := endpointIngressPolicyName(app.Name, g.Endpoint, multi)
				autoApps = append(autoApps, stack.NewApplication(
					name,
					app.Namespace,
					&componentEndpointIngressPolicyConfig{
						ComponentName: app.Name,
						Endpoint:      g.Endpoint,
						Rules:         g.Rules,
						PolicyName:    name,
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
