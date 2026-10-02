package oam

import "github.com/go-kure/kure/pkg/stack"

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
// trait's ConfigMap, or the Secret an external-secret trait's ExternalSecret
// produces in its own namespace. kind is "ConfigMap" or "Secret".
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
// secretRef. A sub-application the object does not read stays in the
// application namespace with the workloads, which may read it too. One the
// object reads moves even when the workloads read it as well: the Flux object
// needs it to reconcile at all, while a workload's reference through the
// chart's values cannot be seen here.
func moveFluxNamespaceInputs(owned []traitSubApps, ns string) {
	for _, o := range owned {
		reader, ok := o.owner.Config.(fluxNamespaceReader)
		if !ok {
			continue
		}
		configMaps, secrets := reader.FluxNamespaceReads()
		if len(configMaps) == 0 && len(secrets) == 0 {
			continue
		}
		reads := map[string]map[string]bool{"ConfigMap": {}, "Secret": {}}
		for _, n := range configMaps {
			reads["ConfigMap"][n] = true
		}
		for _, n := range secrets {
			reads["Secret"][n] = true
		}
		for _, sub := range o.subApps {
			in, ok := sub.Config.(fluxNamespaceInput)
			if !ok {
				continue
			}
			if kind, name := in.FluxNamespaceInput(); reads[kind][name] {
				sub.Namespace = ns
			}
		}
	}
}
