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
// component (the primary: its type picks the auto health check), the members'
// shared tier, and ONE stack.Application named after the group whose config is a
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
		if out[i].tier != e.tier {
			return nil, &TransformError{Message: fmt.Sprintf(
				"sibling group %q: member %q is in tier %q but member %q is in tier %q; a group deploys as one unit and needs one tier",
				g.name, out[i].component.Type, out[i].tier, e.component.Type, e.tier)}
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

// traitTargets returns the entries whose traits applyTraits dispatches: the
// members of a collapsed sibling group, each with its own traits and its own
// application, or the entry itself.
func (e componentEntry) traitTargets() []componentEntry {
	if len(e.members) > 0 {
		return e.members
	}
	return []componentEntry{e}
}

// healthCheckConfig is the config whose object the auto health check names: the
// primary member's for a collapsed sibling group, else the entry's own.
func (e componentEntry) healthCheckConfig() stack.ApplicationConfig {
	if len(e.members) > 0 {
		return e.members[0].app.Config
	}
	return e.app.Config
}

// siblingGroupConfig is the one ApplicationConfig a sibling group deploys as. It
// generates each member's application in emission order and answers every
// optional config interface the engine type-asserts on a deployed component's
// config after traits ran, by asking the members. Traits themselves never see it:
// applyTraits hands each trait its own member's application, so the rule places a
// trait on the member whose contracts it reads (a routing trait on the member
// owning the Service).
//
//   - fluxNamespaceSettable (transform.go applyAutoHealthChecks and
//     postProcessFluxNamespace): set on every member.
//   - autoHealthCheckEmitter (applyAutoHealthChecks): the primary member's
//     answer, since the health check names the primary's kind.
//   - servicePortProvider, serviceBackendNamer, serviceRoutingTargeter
//     (netpol_synthesis.go componentServiceName and the routing registry),
//     ServiceAccountNamer, and the servicePortNamer and nonRWXClaimer contracts
//     the builtin traits assert: the one member that answers a non-zero value.
//     checkSiblingGroups refuses a group in which two members do, so the answer
//     is never a choice.
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

// Generate returns the members' objects, member by member in emission order. Two
// members generating the same Kubernetes object (API group, kind, namespace and
// name) is refused: the group would deploy one object twice with two contents —
// for example a statefulset member's headless Service and a service member's
// Service, both named after the group. An object without a kind cannot be
// compared and is refused, as CheckCrossDocumentCollisions refuses one.
func (g *siblingGroupConfig) Generate(*stack.Application) ([]*client.Object, error) {
	var objs []*client.Object
	owner := make(map[objectIdentity]string)
	for i, m := range g.members {
		out, err := m.Generate()
		if err != nil {
			return nil, err
		}
		for _, p := range out {
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
		objs = append(objs, out...)
	}
	return objs, nil
}

// SetFluxNamespace sets the per-request Flux namespace on every member that takes one.
func (g *siblingGroupConfig) SetFluxNamespace(ns string) {
	for _, m := range g.members {
		if s, ok := m.Config.(fluxNamespaceSettable); ok {
			s.SetFluxNamespace(ns)
		}
	}
}

// EmitsAutoHealthCheck answers for the primary member, whose kind the health check names.
func (g *siblingGroupConfig) EmitsAutoHealthCheck() bool {
	if e, ok := g.members[0].Config.(autoHealthCheckEmitter); ok {
		return e.EmitsAutoHealthCheck()
	}
	return true
}

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

// ServiceAccountName is the one member's non-empty ServiceAccount name, or "".
func (g *siblingGroupConfig) ServiceAccountName() string {
	for _, m := range g.members {
		if n, ok := m.Config.(ServiceAccountNamer); ok && n.ServiceAccountName() != "" {
			return n.ServiceAccountName()
		}
	}
	return ""
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

// siblingAnswers names each value-forwarded contract cfg answers with a non-zero value.
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
	if n, ok := cfg.(ServiceAccountNamer); ok && n.ServiceAccountName() != "" {
		out = append(out, "ServiceAccountName")
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
