package oam

import (
	"fmt"
	"slices"

	"github.com/go-kure/kure/pkg/stack"
)

// fluxNamespaceReader is implemented by a config that moves to the Flux
// namespace (fluxNamespaceSettable) and reports the ConfigMaps and Secrets its
// Flux object reads by name from the namespace it lands in: a HelmRelease's
// valuesFrom and kubeConfig, a source's secretRef, certSecretRef and the like.
// Every built-in fluxNamespaceSettable config implements it, with empty lists
// when it reads none, and decorators forward it.
type fluxNamespaceReader interface {
	FluxNamespaceReads() (configMaps, secrets []string)
}

// fluxNamespaceInput is implemented by a trait sub-application's config whose
// object is a ConfigMap or Secret a Flux object can read by name: the configmap
// trait's ConfigMap, or the Secret an external-secret trait's ExternalSecret or
// a certificate trait's Certificate produces in its own namespace. kind is
// "ConfigMap" or "Secret".
type fluxNamespaceInput interface {
	FluxNamespaceInput() (kind, name string)
}

// fluxNamespaceProducer is implemented by a fluxNamespaceInput config that
// reports whether its trait makes the object it names.
// ProducesFluxNamespaceInput is true only when applying this trait creates the
// named object; a reference to an object that exists apart from the trait
// returns false: an external-secret trait under creationPolicy Merge or None.
// checkFluxObjectMakerOrder refuses only a producer, so a config without it is
// never refused (go-kure/launcher#1039).
type fluxNamespaceProducer interface {
	ProducesFluxNamespaceInput() bool
}

// fluxSourceRequiredReader is implemented by a Flux source config that reports
// the Secrets a consumer of the source waits for, in the source's namespace:
// an OCIRepository's secretRef, and its verify.trustedRootSecretRef only under
// the cosign provider. It is narrower than its fluxNamespaceReader reads, which
// move a trait object the source may read. checkFluxObjectMakerOrder refuses
// only a Secret a generated source requires, so a source without it is never
// refused (go-kure/launcher#1039).
type fluxSourceRequiredReader interface {
	FluxSourceRequiredSecrets() []string
}

// fluxRequiredReader is implemented by a Flux config that reports the
// ConfigMaps and Secrets its object itself cannot become Ready without, in the
// namespace it lands in: a subset of its fluxNamespaceReader reads, empty when
// the object is suspended or reads nothing it requires. It differs from
// fluxSourceRequiredReader, what a consumer of a generated source waits for,
// for a suspended source and for a HelmRepository of type oci, which never
// sets Ready but whose Secrets an OCI chart pulled through it reads. Every built-in fluxNamespaceSettable
// config implements it. checkFluxObjectMakerOrder refuses only what an object
// requires, so a config without it is never refused (go-kure/launcher#1079).
type fluxRequiredReader interface {
	FluxRequiredReads() (configMaps, secrets []string)
}

// traitSubApps is the application a trait ran on and the sub-applications it
// appended, recorded by applyEntryTraits for postProcessFluxNamespace and for
// component attribution (markComponentOwnership). In a sibling group owner is
// the member carrying the trait, not the group. readers are the Flux objects
// whose reads move those sub-applications (fluxInputReaders): the owner alone,
// or in a sibling group every Flux member when the owner is one, and none when
// it is not; with a Flux object, the generated sources a lowering rule ordered
// it after (generatedSourceReaders).
type traitSubApps struct {
	owner   *stack.Application
	readers []*stack.Application
	subApps []*stack.Application
}

// moveFluxNamespaceInputs moves, for each component whose Flux object moved to
// ns, the trait sub-applications that object reads from its own namespace
// (go-kure/launcher#740): a configmap trait's ConfigMap a HelmRelease names in
// valuesFrom, the Secret of an external-secret trait a HelmRepository names in
// secretRef, the Secret of a certificate trait one names in certSecretRef. A
// sub-application the object does not read stays in the
// application namespace with the workloads, which may read it too. One the
// object reads moves even when the workloads read it as well: the Flux object
// needs it to reconcile at all, while a workload's reference through the
// chart's values cannot be seen here. In a sibling group the object is any of
// the group's Flux members (go-kure/launcher#1012): they land together in the
// one Flux namespace, so a trait object one of them reads must be there,
// whichever member's trait made it. The object is also a generated source the
// component is ordered after (go-kure/launcher#1016): an OCIRepository the oci
// rule shares between consumers reads the Secret one consumer's trait makes.
func moveFluxNamespaceInputs(owned []traitSubApps, ns string) {
	for _, o := range owned {
		for _, sub := range o.subApps {
			in, ok := sub.Config.(fluxNamespaceInput)
			if !ok {
				continue
			}
			if kind, name := in.FluxNamespaceInput(); fluxObjectReads(o.readers, kind, name) {
				sub.Namespace = ns
			}
		}
	}
}

// fluxObjectReads reports whether the Flux object of one of readers reads the
// ConfigMap or Secret (kind) of name from its own namespace
// (fluxNamespaceReader), so that a trait object of that name moves with it to
// the Flux namespace. It is the one test of that move: moveFluxNamespaceInputs
// moves by it, and a trait's claim of such an object (NameSpec.FluxInput,
// Trait.ClaimFluxInputName) is held in the namespace it gives, so the claim and
// the move cannot disagree. Each reader is asked through its application at
// call time, so a decorated config's forwarded reads are seen.
func fluxObjectReads(readers []*stack.Application, kind, name string) bool {
	for _, r := range readers {
		if r == nil {
			continue
		}
		reader, ok := r.Config.(fluxNamespaceReader)
		if !ok {
			continue
		}
		configMaps, secrets := reader.FluxNamespaceReads()
		switch kind {
		case "ConfigMap":
			if slices.Contains(configMaps, name) {
				return true
			}
		case "Secret":
			if slices.Contains(secrets, name) {
				return true
			}
		}
	}
	return false
}

// groupFluxMembers returns the members of a sibling group entry that are Flux
// objects, in member order: those whose config takes the Flux namespace
// (fluxNamespaceSettable). applyEntryTraits takes them before the entry's first
// trait runs, because a trait decorator satisfies fluxNamespaceSettable
// unconditionally: a workload member a trait decorated would pass the test
// afterwards. It is nil for an entry that is not a sibling group.
func groupFluxMembers(e componentEntry) []*stack.Application {
	var out []*stack.Application
	for _, m := range e.members {
		if _, ok := m.app.Config.(fluxNamespaceSettable); ok {
			out = append(out, m.app)
		}
	}
	return out
}

// fluxInputReaders returns the Flux objects whose reads decide where the trait
// objects of member, the application a trait of entry e ran on, land: member
// itself for an entry that is not a sibling group; in a sibling group every
// Flux member (fluxMembers, groupFluxMembers) when member is one of them, and
// none when it is not, so a member that is no Flux object keeps its trait
// objects with its workload, where it reads them. To a Flux object's readers
// are added, once each and in order, the generated sources a lowering rule
// ordered it after (sources, generatedSourceReaders; go-kure/launcher#1016).
func fluxInputReaders(e componentEntry, fluxMembers []*stack.Application, member *stack.Application, sources map[*stack.Application][]*stack.Application) []*stack.Application {
	var readers []*stack.Application
	switch {
	case len(e.members) == 0:
		readers = []*stack.Application{member}
	case slices.Contains(fluxMembers, member):
		readers = fluxMembers
	default:
		return nil
	}
	extra := sources[member]
	if len(extra) == 0 {
		return readers
	}
	readers = slices.Clone(readers)
	for _, s := range extra {
		if !slices.Contains(readers, s) {
			readers = append(readers, s)
		}
	}
	return readers
}

// generatedSourceReaders maps each Flux object a lowering rule ordered after a
// generated source (componentOrder.sources) to those sources, in its rule's
// order and once each: an oci component's Kustomization to the OCIRepository
// it shares with the other consumers of its artifact (go-kure/launcher#1016).
// Such a source reads, by its secretRef and the like, a Secret a consumer's
// trait makes, so it is one of the Flux objects whose reads decide where that
// trait object lands (fluxInputReaders). The Flux objects are taken as
// orderComponents left them, before any trait runs: a trait decorator
// satisfies fluxNamespaceSettable unconditionally (groupFluxMembers). A
// workload ordered after a source is no key, so its trait objects stay with
// it. Nil when the document has no generated source.
func generatedSourceReaders(order *componentOrder) map[*stack.Application][]*stack.Application {
	if len(order.sources) == 0 {
		return nil
	}
	byName := make(map[string]*stack.Application, len(order.sources))
	for _, s := range order.sources {
		byName[s.component.Name] = s.app
	}
	out := make(map[*stack.Application][]*stack.Application)
	for _, group := range order.groups {
		for _, e := range group {
			var sources []*stack.Application
			for _, name := range e.ruleOrder() {
				if s, ok := byName[name]; ok && !slices.Contains(sources, s) {
					sources = append(sources, s)
				}
			}
			if len(sources) == 0 {
				continue
			}
			objects := groupFluxMembers(e)
			if len(e.members) == 0 {
				if _, ok := e.app.Config.(fluxNamespaceSettable); ok {
					objects = []*stack.Application{e.app}
				}
			}
			for _, o := range objects {
				out[o] = sources
			}
		}
	}
	return out
}

// fluxWaiter is a Flux object a group waits for under a delivery whose groups
// wait for their Flux objects (TransformContext.GroupsWaitForFluxObjects), and
// what it cannot become Ready without: a Flux object of a group entry or of a
// sibling group's member, waiting in its own group, or a generated source,
// waiting in the group of its earliest Flux consumer (consumer).
type fluxWaiter struct {
	app *stack.Application
	// reads reports, when the check runs, the ConfigMaps and Secrets the
	// object cannot become Ready without, in the namespace it lands in. It is
	// asked of the config as orderComponents left it, before a trait wrapped
	// it; SetFluxNamespace still reaches that config through a decorator, so
	// it knows its Flux namespace by then.
	reads func() (configMaps, secrets []string)
	group int
	// component and typ name the waiter in a refusal: the group entry's
	// component (a sibling group's, for a member) and the object's type.
	component, typ string
	// consumer is a generated source's earliest Flux consumer; empty for an
	// object of a group.
	consumer string
}

// fluxWaiters returns the Flux objects of order that report what they cannot
// become Ready without, in the order a refusal is looked for: the generated
// sources first, then the groups in order, each entry's object, or its sibling
// group's Flux members, in document order. A generated source requires what
// it reports as a source (fluxSourceRequiredReader), Secrets only, and waits
// in the group of its earliest consumer: the first Flux object, by group, a
// lowering rule ordered after it (sources, generatedSourceReaders), by that
// object's own component's order, so in a sibling group the member's, not the
// group's. A source no Flux object consumes waits in no group. A Flux object
// of a group requires what it reports as one (fluxRequiredReader). Configs are
// asserted here, before any trait runs: a trait decorator forwards neither
// interface (groupFluxMembers).
func fluxWaiters(order *componentOrder, sources map[*stack.Application][]*stack.Application) []fluxWaiter {
	source := make(map[string]componentEntry, len(order.sources))
	for _, s := range order.sources {
		source[s.component.Name] = s
	}
	type consumer struct {
		group int
		name  string
	}
	earliest := make(map[*stack.Application]consumer)
	var grouped []fluxWaiter
	for i, group := range order.groups {
		for _, e := range group {
			objects := []componentEntry{e}
			if len(e.members) > 0 {
				objects = e.members
			}
			for _, o := range objects {
				if req, ok := o.app.Config.(fluxRequiredReader); ok {
					grouped = append(grouped, fluxWaiter{app: o.app, reads: req.FluxRequiredReads, group: i, component: e.component.Name, typ: o.component.Type})
				}
				if _, ok := sources[o.app]; !ok {
					continue
				}
				for _, name := range o.component.orderAfter {
					if s, ok := source[name]; ok {
						if _, seen := earliest[s.app]; !seen {
							earliest[s.app] = consumer{group: i, name: e.component.Name}
						}
					}
				}
			}
		}
	}
	var out []fluxWaiter
	for _, s := range order.sources {
		first, ok := earliest[s.app]
		if !ok {
			continue
		}
		req, ok := s.app.Config.(fluxSourceRequiredReader)
		if !ok {
			continue
		}
		reads := func() (configMaps, secrets []string) { return nil, req.FluxSourceRequiredSecrets() }
		out = append(out, fluxWaiter{app: s.app, reads: reads, group: first.group, component: s.component.Name, typ: s.component.Type, consumer: first.name})
	}
	return append(out, grouped...)
}

// checkFluxObjectMakerOrder refuses a ConfigMap or Secret a waiter
// (fluxWaiters) cannot become Ready without that a trait makes
// (fluxNamespaceProducer) on a component in a later group than the waiter's,
// in the namespace the waiter lands in, for a delivery that makes a group
// Ready only once its Flux objects are (TransformContext.GroupsWaitForFluxObjects;
// go-kure/launcher#1039, go-kure/launcher#1079). Each group depends on the one
// before it (buildCluster), so the waiter's group waits for the waiter, the
// waiter for the object, and the object's group for the waiter's: the
// delivery never converges. Under the default delivery a group is Ready once
// applied, and it does.
//
// Any trait of a later group is looked at, a workload's too: its object lands
// in the application namespace, where without a Flux namespace (ns) every
// Flux object lands as well. A trait of the waiter's own group, or an earlier
// one, is applied before the group's wait. Anything the check cannot prove is
// admitted: a trait object that is no producer or does not say, a waiter that
// does not report what it requires, an object outside the waiter's namespace
// (ns, or the waiter's own without one), a maker that is no trait.
func checkFluxObjectMakerOrder(order *componentOrder, waiters []fluxWaiter, owned []traitSubApps, ns string) error {
	if len(waiters) == 0 {
		return nil
	}
	type maker struct {
		group int
		name  string
	}
	// Every application of a group entry, a sibling group's members included,
	// under the group and the component it is part of.
	at := make(map[*stack.Application]maker)
	for i, group := range order.groups {
		for _, e := range group {
			m := maker{group: i, name: e.component.Name}
			at[e.app] = m
			for _, member := range e.members {
				at[member.app] = m
			}
		}
	}
	for _, w := range waiters {
		configMaps, secrets := w.reads()
		if len(configMaps) == 0 && len(secrets) == 0 {
			continue
		}
		where := w.app.Namespace
		if ns != "" {
			where = ns
		}
		for _, o := range owned {
			by, ok := at[o.owner]
			if !ok || by.group <= w.group {
				continue
			}
			for _, sub := range o.subApps {
				in, ok := sub.Config.(fluxNamespaceInput)
				if !ok {
					continue
				}
				kind, name := in.FluxNamespaceInput()
				required := secrets
				if kind == "ConfigMap" {
					required = configMaps
				} else if kind != "Secret" {
					continue
				}
				if !slices.Contains(required, name) || sub.Namespace != where {
					continue
				}
				if p, ok := sub.Config.(fluxNamespaceProducer); !ok || !p.ProducesFluxNamespaceInput() {
					continue
				}
				return lateMakerError(order, w, kind, name, by.name, by.group, ns)
			}
		}
	}
	return nil
}

// lateMakerError is checkFluxObjectMakerOrder's refusal of the object kind
// name of waiter w that a trait of component maker makes in group g.
func lateMakerError(order *componentOrder, w fluxWaiter, kind, name, maker string, g int, ns string) error {
	provide := fmt.Sprintf("provide the %s apart from the document", kind)
	if ns != "" {
		provide += fmt.Sprintf(" in the Flux namespace %q", ns)
	}
	if w.consumer != "" {
		return &TransformError{Message: fmt.Sprintf(
			"%s %q requires Secret %q, which a trait of component %q makes in group %q, later than component %q in group %q, a consumer of %q; with each group waiting for its Flux objects, %q's group waits for %q, %q for the source, the source for the Secret, and the group of %q, which makes it, for %q's, so the delivery never converges: put the trait on a consumer in the earliest group, or %s",
			w.typ, w.component, name, maker, order.suffixes[g], w.consumer, order.suffixes[w.group], w.component,
			w.consumer, w.consumer, w.consumer, maker, w.consumer, provide)}
	}
	return &TransformError{Message: fmt.Sprintf(
		"%s %q requires %s %q, which a trait of component %q makes in group %q, later than %q's group %q; with each group waiting for its Flux objects, %q's group waits for %q, %q for the %s, and the group of %q, which makes it, for %q's, so the delivery never converges: put the trait on a component in group %q or earlier, or %s",
		w.typ, w.component, kind, name, maker, order.suffixes[g], w.component, order.suffixes[w.group],
		w.component, w.component, w.component, kind, maker, w.component, order.suffixes[w.group], provide)}
}
