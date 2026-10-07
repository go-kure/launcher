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
// appended, recorded by applyEntryTraits for postProcessFluxNamespace. In a
// sibling group that is the member carrying the trait, not the group, so one
// member's reads never move another member's trait objects.
type traitSubApps struct {
	owner   *stack.Application
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
// chart's values cannot be seen here.
func moveFluxNamespaceInputs(owned []traitSubApps, ns string) {
	for _, o := range owned {
		for _, sub := range o.subApps {
			in, ok := sub.Config.(fluxNamespaceInput)
			if !ok {
				continue
			}
			if kind, name := in.FluxNamespaceInput(); fluxObjectReads(o.owner, kind, name) {
				sub.Namespace = ns
			}
		}
	}
}

// fluxObjectReads reports whether owner's Flux object reads the ConfigMap or
// Secret (kind) of name from its own namespace (fluxNamespaceReader), so that a
// trait object of that name moves with it to the Flux namespace. It is the one
// test of that move: moveFluxNamespaceInputs moves by it, and a trait's claim of
// such an object (NameSpec.FluxInput, Trait.ClaimFluxInputName) is held in the
// namespace it gives, so the claim and the move cannot disagree.
func fluxObjectReads(owner *stack.Application, kind, name string) bool {
	if owner == nil {
		return false
	}
	reader, ok := owner.Config.(fluxNamespaceReader)
	if !ok {
		return false
	}
	configMaps, secrets := reader.FluxNamespaceReads()
	switch kind {
	case "ConfigMap":
		return slices.Contains(configMaps, name)
	case "Secret":
		return slices.Contains(secrets, name)
	}
	return false
}
