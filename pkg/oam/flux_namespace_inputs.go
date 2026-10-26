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

// checkGeneratedSourceInputOrder refuses, under a Flux namespace (ns), a trait
// object a generated source reads that a component makes in a later group than
// another consumer of that source (go-kure/launcher#1016). Each group depends
// on the one before it (buildCluster), so the earlier consumer waits for the
// source, the source for the trait object, and the maker's group for the
// earlier consumer: the delivery never converges. The consumers of a source
// are the Flux objects sources (generatedSourceReaders) names it for; a
// workload ordered after it does not wait on it. Without a Flux namespace it
// refuses nothing (go-kure/launcher#1039).
func checkGeneratedSourceInputOrder(order *componentOrder, sources map[*stack.Application][]*stack.Application, owned []traitSubApps, ns string) error {
	if ns == "" || len(sources) == 0 {
		return nil
	}
	type consumer struct {
		group int
		name  string
	}
	// Every application of a group entry, a sibling group's members included,
	// under the group and the component it is part of.
	at := make(map[*stack.Application]consumer)
	// The earliest consumer of each source: groups are walked in order, so the
	// first one seen.
	earliest := make(map[*stack.Application]consumer)
	for i, group := range order.groups {
		for _, e := range group {
			apps := []*stack.Application{e.app}
			for _, m := range e.members {
				apps = append(apps, m.app)
			}
			for _, a := range apps {
				c := consumer{group: i, name: e.component.Name}
				at[a] = c
				for _, s := range sources[a] {
					if _, seen := earliest[s]; !seen {
						earliest[s] = c
					}
				}
			}
		}
	}
	source := make(map[*stack.Application]componentEntry, len(order.sources))
	for _, s := range order.sources {
		source[s.app] = s
	}
	for _, o := range owned {
		maker, ok := at[o.owner]
		if !ok {
			continue
		}
		for _, r := range o.readers {
			first, ok := earliest[r]
			if !ok || maker.group <= first.group {
				continue
			}
			for _, sub := range o.subApps {
				in, ok := sub.Config.(fluxNamespaceInput)
				if !ok {
					continue
				}
				kind, name := in.FluxNamespaceInput()
				if !fluxObjectReads([]*stack.Application{r}, kind, name) {
					continue
				}
				s := source[r]
				return &TransformError{Message: fmt.Sprintf(
					"%s %q reads %s %q, which a trait of component %q makes in a later group than component %q, another consumer of %q: %q waits for the source, the source for the %s, and the group of %q, which makes it, for %q, so the delivery never converges; put the trait on a consumer in the earliest group, or provide the %s in the Flux namespace %q",
					s.component.Type, s.component.Name, kind, name, maker.name, first.name, s.component.Name,
					first.name, kind, maker.name, first.name, kind, ns)}
			}
		}
	}
	return nil
}
