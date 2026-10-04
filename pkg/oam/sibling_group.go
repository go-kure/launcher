package oam

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// siblingGroup identifies one same-name sibling group: the components a single
// ComponentLoweringRule invocation emitted under one name. Members hold the same
// pointer, which is the group's whole identity (go-kure/launcher#280).
type siblingGroup struct {
	name string
}

// stampSiblingGroups marks every set of components in one rule invocation's output
// that shares a name as one sibling group. A group's members must have distinct
// types, each with a registered ComponentHandler: a member a later round would
// lower again could not keep the group's single identity through that rewrite.
// A name emitted once is left alone, so a rule emitting no repeated name changes
// nothing. A marker the rule copied in with a component (a by-value copy of a
// member of an existing group) is cleared first: only this invocation's own
// repeated names form its groups.
func (t *Transformer) stampSiblingGroups(components []Component) error {
	byName := make(map[string][]int, len(components))
	var repeated []string
	for i := range components {
		components[i].siblingGroup = nil
		name := components[i].Name
		byName[name] = append(byName[name], i)
		if len(byName[name]) == 2 {
			repeated = append(repeated, name)
		}
	}
	for _, name := range repeated {
		group := &siblingGroup{name: name}
		types := make(map[string]bool, len(byName[name]))
		for _, i := range byName[name] {
			c := &components[i]
			if types[c.Type] {
				return errors.Errorf("sibling group %q: more than one member of type %q", name, c.Type)
			}
			types[c.Type] = true
			if _, terminal := t.componentHandlers[c.Type]; !terminal {
				return errors.Errorf("sibling group %q: member type %q has no component handler; every member must be a terminal component type", name, c.Type)
			}
			c.siblingGroup = group
		}
	}
	return nil
}

// collapseSiblingGroups folds each sibling group's entries into one entry, placed
// where its first member stood. The collapsed entry carries the first member's
// component (the primary), the primary's
// tier (checkSiblingTiers settles it once placement has run), and ONE
// stack.Application named after the group whose config is a
// siblingGroupConfig over the members' own applications. Every name-keyed step
// after this — tier overrides, policies, bundles, dependsOn, the component map,
// the layout — therefore sees one component, while each member keeps its own
// config, policy enforcement (already applied in createApplications) and traits
// (applyTraits iterates members).
func collapseSiblingGroups(entries []componentEntry, namespace string) ([]componentEntry, error) {
	out := make([]componentEntry, 0, len(entries))
	at := make(map[*siblingGroup]int)
	for _, e := range entries {
		g := e.component.siblingGroup
		if g == nil {
			out = append(out, e)
			continue
		}
		i, seen := at[g]
		if !seen {
			at[g] = len(out)
			out = append(out, componentEntry{
				index:     e.index,
				component: e.component,
				app:       stack.NewApplication(g.name, namespace, &siblingGroupConfig{}),
				tier:      e.tier,
				members:   []componentEntry{e},
			})
			continue
		}
		out[i].members = append(out[i].members, e)
	}
	for _, e := range out {
		if len(e.members) == 0 {
			continue
		}
		cfg := e.app.Config.(*siblingGroupConfig)
		for _, m := range e.members {
			cfg.members = append(cfg.members, m.app)
			cfg.types = append(cfg.types, m.component.Type)
		}
	}
	return out, nil
}

// checkSiblingTiers refuses a sibling group whose members' annotations disagree
// on the tier (one of them in no tier included), unless a placement policy
// places the group's name: placement replaces the annotations, so it gives the
// group its one tier (the override loop in Transform has already set it). A
// group deploys as one unit and needs one tier.
func checkSiblingTiers(entries []componentEntry, overrides map[string]Tier) error {
	for _, e := range entries {
		if len(e.members) == 0 {
			continue
		}
		if _, placed := overrides[e.component.Name]; placed {
			continue
		}
		first := e.members[0]
		for _, m := range e.members[1:] {
			if m.tier != first.tier {
				return &TransformError{Message: fmt.Sprintf(
					"sibling group %q: member %q is %s but member %q is %s; a group deploys as one unit and needs one tier — place the group with a placement policy",
					e.component.Name, first.component.Type, tierPhrase(first.tier), m.component.Type, tierPhrase(m.tier))}
			}
		}
	}
	return nil
}

// tierPhrase reads "in tier "infra"", or "in no tier" for a component nothing
// placed.
func tierPhrase(tier Tier) string {
	if tier == "" {
		return "in no tier"
	}
	return fmt.Sprintf("in tier %q", tier)
}

// traitStep is traits applyEntryTraits applies, in order, on one entry's
// application: the entry itself, or one member of a collapsed sibling group, each
// with its own traits and its own application.
type traitStep struct {
	entry  componentEntry
	traits []Trait
}

// traitSteps returns the traits applyEntryTraits applies, in order: an entry's own
// traits in slice order, or, for a collapsed sibling group, every member's traits
// merged into authored order. The rule's own traits (no authored slot) come first,
// in member order, then the traits it forwarded, ascending by the slot each held in
// the component the rule was handed (Trait.authoredIndex); a trait forwarded to two
// members applies on each, in member order. Applied member by member instead, a
// group's trait sub-applications would follow its members rather than the authored
// traits, and a component re-expressed as a group would not stay byte-identical.
func (e componentEntry) traitSteps(app *Application) []traitStep {
	if len(e.members) == 0 {
		return []traitStep{{entry: e, traits: app.Spec.Components[e.index].Traits}}
	}
	var steps []traitStep
	for _, m := range e.members {
		for _, trait := range app.Spec.Components[m.index].Traits {
			steps = append(steps, traitStep{entry: m, traits: []Trait{trait}})
		}
	}
	sort.SliceStable(steps, func(i, j int) bool {
		a, b := steps[i].traits[0].authoredIndex, steps[j].traits[0].authoredIndex
		if a == nil || b == nil {
			return a == nil && b != nil
		}
		return *a < *b
	})
	return steps
}

// siblingGroupConfig is the one ApplicationConfig a sibling group deploys as. It
// generates its members' objects primary objects first (Generate) and answers every
// optional config interface the engine type-asserts on a deployed component's
// config after traits ran, by asking the members. Traits themselves never see it:
// applyTraits hands each trait its own member's application, so the rule places a
// trait on the member whose contracts it reads (a routing trait on the member
// owning the Service).
//
//   - fluxNamespaceSettable (transform.go postProcessFluxNamespace): set on
//     every member.
//   - fluxNamespaceReader: every member's reads. moveFluxNamespaceInputs asks
//     the member a trait ran on instead, so the union never moves one member's
//     trait object for another member's read.
//   - ComponentNamed (for consumers attributing objects to their component):
//     the group's name, which every member shares.
//   - servicePortProvider, serviceBackendNamer, serviceRoutingTargeter
//     (netpol_synthesis.go componentServiceName and the routing registry),
//     ServiceAccountNamer, and the servicePortNamer and nonRWXClaimer contracts
//     the builtin traits assert: the one member that answers a non-zero value.
//     checkSiblingGroups refuses a group in which two members do, so the answer
//     is never a choice. Synthesis asks the group itself (routesToOwnPods)
//     whether a routing target selects another member's pods on ports mapped
//     to themselves, and then keeps the component label as the pod selector.
//
// Not forwarded, on purpose: Enforceable and SourceDeduplicatable run per member
// in createApplications, before the group exists; trafficSourceCollector and
// backendRefTargetCollector are trait sub-application contracts, never a
// component's; stack.Validator runs inside each member's own Generate. A member
// that is a kure layout augmenter is refused (checkSiblingGroups): its layout is
// keyed by its own application, which the group replaces.
//
// Members are read through their *stack.Application at call time, never through a
// config captured earlier, so a trait that wraps a member's config in a decorator
// (stack.Application.SetConfig) is seen.
type siblingGroupConfig struct {
	members []*stack.Application
	// types holds each member's component type, parallel to members, for errors.
	types []string
}

// Generate returns each member's first object, member by member in emission
// order, then every member's remaining objects, member by member in the same
// order. A member's first object is its primary one (the workload, the Service),
// so a group of a deployment and a service generates Deployment, Service, then the
// Deployment's ServiceAccount and claims — the order one component generating all
// of them uses, which keeps a component re-expressed as a group byte-identical.
// Two members generating the same Kubernetes object (API group, kind, namespace
// and name) is refused: the group would deploy one object twice with two contents
// — for example two members that each generate a Service named after the
// group. An object without a kind cannot be
// compared and is refused, as CheckCrossDocumentCollisions refuses one.
func (g *siblingGroupConfig) Generate(*stack.Application) ([]*client.Object, error) {
	var heads, tails []*client.Object
	owner := make(map[objectIdentity]string)
	for i, m := range g.members {
		out, err := m.Generate()
		if err != nil {
			return nil, err
		}
		split := len(out)
		for j, p := range out {
			if p == nil || isNullValue(*p) {
				continue
			}
			if split == len(out) {
				split = j + 1
			}
			if p == nil || isNullValue(*p) {
				continue
			}
			obj := *p
			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.Kind == "" {
				return nil, errors.Errorf("sibling group %q: member %q generates object %q with no kind; set its apiVersion and kind so the group can compare its members' objects",
					m.Name, g.types[i], qualifiedName(obj.GetNamespace(), obj.GetName()))
			}
			id := objectIdentity{group: gvk.Group, kind: gvk.Kind, namespace: obj.GetNamespace(), name: obj.GetName()}
			if prev, dup := owner[id]; dup && prev != g.types[i] {
				return nil, errors.Errorf("sibling group %q: members %q and %q both generate %s; exactly one member may",
					m.Name, prev, g.types[i], id)
			}
			owner[id] = g.types[i]
		}
		heads = append(heads, out[:split]...)
		tails = append(tails, out[split:]...)
	}
	return append(heads, tails...), nil
}

// SetFluxNamespace sets the per-request Flux namespace on every member that takes one.
func (g *siblingGroupConfig) SetFluxNamespace(ns string) {
	for _, m := range g.members {
		if s, ok := m.Config.(fluxNamespaceSettable); ok {
			s.SetFluxNamespace(ns)
		}
	}
}

// FluxNamespaceReads is every member's reads (fluxNamespaceReader): each member
// that moves to the Flux namespace reads its own inputs from there.
func (g *siblingGroupConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	for _, m := range g.members {
		if r, ok := m.Config.(fluxNamespaceReader); ok {
			cms, secs := r.FluxNamespaceReads()
			configMaps = append(configMaps, cms...)
			secrets = append(secrets, secs...)
		}
	}
	return configMaps, secrets
}

// ComponentName is the group's name, which every member shares (ComponentNamed).
func (g *siblingGroupConfig) ComponentName() string { return g.members[0].Name }

// ServicePort is the one member's non-zero Service port, or 0.
func (g *siblingGroupConfig) ServicePort() int32 {
	for _, m := range g.members {
		if p, ok := m.Config.(servicePortProvider); ok && p.ServicePort() != 0 {
			return p.ServicePort()
		}
	}
	return 0
}

// BackendServiceName is the one member's non-empty Service name, or "".
func (g *siblingGroupConfig) BackendServiceName() string {
	for _, m := range g.members {
		if n, ok := m.Config.(serviceBackendNamer); ok && n.BackendServiceName() != "" {
			return n.BackendServiceName()
		}
	}
	return ""
}

// ServicePortName is the one member's known Service port name, or "" and false.
func (g *siblingGroupConfig) ServicePortName() (string, bool) {
	for _, m := range g.members {
		if n, ok := m.Config.(siblingServicePortNamer); ok {
			if name, known := n.ServicePortName(); known {
				return name, true
			}
		}
	}
	return "", false
}

// ServiceAccountName is the one member's non-empty ServiceAccount name, or "";
// runsPods is whether any member runs pods.
func (g *siblingGroupConfig) ServiceAccountName() (string, bool) {
	var name string
	runsPods := false
	for _, m := range g.members {
		if n, ok := m.Config.(ServiceAccountNamer); ok {
			memberName, pods := n.ServiceAccountName()
			if name == "" {
				name = memberName
			}
			runsPods = runsPods || pods
		}
	}
	return name, runsPods
}

// NonRWXClaim is the one member's non-empty single-pod claim, or "".
func (g *siblingGroupConfig) NonRWXClaim() string {
	for _, m := range g.members {
		if n, ok := m.Config.(siblingNonRWXClaimer); ok && n.NonRWXClaim() != "" {
			return n.NonRWXClaim()
		}
	}
	return ""
}

// ServiceRoutingTarget is the one member's routing target, or a nil selector.
func (g *siblingGroupConfig) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	for _, m := range g.members {
		if rt, ok := m.Config.(serviceRoutingTargeter); ok {
			if sel, ports := rt.ServiceRoutingTarget(servicePorts); sel != nil {
				return sel, ports
			}
		}
	}
	return nil, nil
}

// routesToOwnPods reports whether the group's routing target selects another
// member's pods (selectsSibling) and its member maps every port to itself by
// number (identityPortMapper). The routed traffic then lands on the group's own
// pods, on the ports it was routed to, so the synthesized inbound policy keeps the
// group's component label (netpol_synthesis.go emitComponents) — the policy one
// component deploying them all synthesizes — while its ports still go through
// ServiceRoutingTarget: a named Service port becomes its number and a non-TCP one
// is dropped. A member remapping any port, or naming a targetPort, does not: that
// policy would open the Service port, not the one the pods listen on.
func (g *siblingGroupConfig) routesToOwnPods() bool {
	for i, m := range g.members {
		if rt, ok := m.Config.(serviceRoutingTargeter); ok {
			if sel, _ := rt.ServiceRoutingTarget(nil); sel != nil {
				id, ok := m.Config.(identityPortMapper)
				return ok && id.IdentityTargetPorts() && g.selectsSibling(i, sel)
			}
		}
	}
	return false
}

// selectsSibling reports whether sel, member i's routing target, selects the pods
// of another member: its matchLabels are a non-empty subset of that member's pod
// template labels (podTemplateLabeler) and it has no matchExpressions. An empty
// selector selects every pod in the namespace, not the group's, so it never does.
func (g *siblingGroupConfig) selectsSibling(i int, sel *metav1.LabelSelector) bool {
	if len(sel.MatchLabels) == 0 || len(sel.MatchExpressions) > 0 {
		return false
	}
	for j, m := range g.members {
		if j == i {
			continue
		}
		l, ok := m.Config.(podTemplateLabeler)
		if !ok {
			continue
		}
		labels := l.PodTemplateLabels()
		subset := len(labels) > 0
		for k, v := range sel.MatchLabels {
			if got, ok := labels[k]; !ok || got != v {
				subset = false
				break
			}
		}
		if subset {
			return true
		}
	}
	return false
}

// podTemplateLabeler is optionally implemented by a component config whose
// workload runs pods: it returns the labels its pod template carries. A sibling
// group reads it to tell a member's routing target that selects its own sibling's
// pods (selectsSibling). A member without it counts as running no pods. Of the
// built-in pod kinds only deployment implements it, the one pod kind a lowering
// rule emits into a group; a rule that emits another pod kind into a group must
// add the method to that kind's config.
type podTemplateLabeler interface {
	PodTemplateLabels() map[string]string
}

// identityPortMapper is optionally implemented by a routing targeter: it reports
// whether every port, whatever its protocol, targets its own port number. A
// sibling group reads it to tell a routing target that selects its own sibling's
// pods on the ports it was routed to (routesToOwnPods).
type identityPortMapper interface {
	IdentityTargetPorts() bool
}

// siblingServicePortNamer and siblingNonRWXClaimer mirror the contracts the
// builtin ingress and scaler traits assert (pkg/oam/builtin/traits), which this
// package cannot import.
type siblingServicePortNamer interface {
	ServicePortName() (string, bool)
}

type siblingNonRWXClaimer interface {
	NonRWXClaim() string
}

// checkSiblingGroups refuses, once traits have run, a sibling group the
// siblingGroupConfig could not answer for unambiguously: two members answering
// the same forwarded contract with a value, or a member that is a kure layout
// augmenter. It tests values, not interface presence, because every trait
// decorator satisfies every contract unconditionally (traits.decoratorBase).
func checkSiblingGroups(entries []componentEntry) error {
	for _, e := range entries {
		if len(e.members) == 0 {
			continue
		}
		answered := make(map[string][]string)
		for _, m := range e.members {
			cfg := m.app.Config
			if _, ok := cfg.(layout.LayoutAugmenter); ok {
				return &TransformError{Message: fmt.Sprintf(
					"sibling group %q: member %q needs layout-level resources, which a sibling group cannot carry", e.component.Name, m.component.Type)}
			}
			for _, contract := range siblingAnswers(cfg) {
				answered[contract] = append(answered[contract], m.component.Type)
			}
		}
		contracts := make([]string, 0, len(answered))
		for contract := range answered {
			contracts = append(contracts, contract)
		}
		sort.Strings(contracts)
		for _, contract := range contracts {
			if types := answered[contract]; len(types) > 1 {
				return &TransformError{Message: fmt.Sprintf(
					"sibling group %q: members %s both answer %s; exactly one member may",
					e.component.Name, strings.Join(types, " and "), contract)}
			}
		}
	}
	return nil
}

// siblingAnswers names each value-forwarded contract cfg answers with a non-zero
// value, or, for ServiceAccountName, for pods it runs.
func siblingAnswers(cfg stack.ApplicationConfig) []string {
	var out []string
	if p, ok := cfg.(servicePortProvider); ok && p.ServicePort() != 0 {
		out = append(out, "ServicePort")
	}
	if n, ok := cfg.(serviceBackendNamer); ok && n.BackendServiceName() != "" {
		out = append(out, "BackendServiceName")
	}
	if n, ok := cfg.(siblingServicePortNamer); ok {
		if _, known := n.ServicePortName(); known {
			out = append(out, "ServicePortName")
		}
	}
	// A pod-running member answers even with no name: its pods run as the
	// namespace's default account, which the group could not report for both.
	if n, ok := cfg.(ServiceAccountNamer); ok {
		if name, runsPods := n.ServiceAccountName(); name != "" || runsPods {
			out = append(out, "ServiceAccountName")
		}
	}
	if n, ok := cfg.(siblingNonRWXClaimer); ok && n.NonRWXClaim() != "" {
		out = append(out, "NonRWXClaim")
	}
	if rt, ok := cfg.(serviceRoutingTargeter); ok {
		if sel, _ := rt.ServiceRoutingTarget(nil); sel != nil {
			out = append(out, "ServiceRoutingTarget")
		}
	}
	return out
}
