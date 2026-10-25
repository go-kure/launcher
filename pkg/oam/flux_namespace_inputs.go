package oam

import (
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
// it is not.
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
// whichever member's trait made it.
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
// objects with its workload, where it reads them.
func fluxInputReaders(e componentEntry, fluxMembers []*stack.Application, member *stack.Application) []*stack.Application {
	if len(e.members) == 0 {
		return []*stack.Application{member}
	}
	if slices.Contains(fluxMembers, member) {
		return fluxMembers
	}
	return nil
}
