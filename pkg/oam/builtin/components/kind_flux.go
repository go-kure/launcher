package components

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what a kind component of a Flux API adds to policyFreeKind
// (go-kure/launcher#790): its object is one a Flux controller reconciles, so
// it lands where the other Flux objects of the application do, in the Flux
// namespace when one is set (SetFluxNamespace), and it reports the ConfigMaps
// and Secrets it reads by name from that namespace (FluxNamespaceReads, see
// flux_namespace_reads.go). The decode and its refusals are policyFreeKind's,
// unchanged, as for a policyHeldKind. A kind whose object names a host the
// environment policy holds is held to it as well (enforce), as the sources
// are.
//
// The Flux kinds written before this file (the sources, helmrelease,
// fluxcd-kustomization) keep their own configs.

// fluxKind is a policyFreeKind whose object moves to the Flux namespace.
type fluxKind[T any] struct {
	policyFreeKind[T]
	// reads lists the ConfigMaps and Secrets decoded names in the object's own
	// namespace. Nil for a kind that names none.
	reads func(decoded *T, r *fluxReads)
	// durations lists the duration fields of T, each with the form its CRD
	// pattern takes: an authored value outside it, or one emitted outside it,
	// is refused, as on the Flux kinds written before this file
	// (go-kure/launcher#601). Nil for a kind with none.
	// TestFluxKinds_DurationsMatchMarkers holds the list to the type.
	durations []fluxDurationField[T]
	// enforce holds decoded to the policy p, which is never nil, as a
	// policyHeldKind's does: it refuses or passes, fills no default and must
	// not change decoded. Nil for a kind no dimension of the environment policy
	// reaches.
	enforce func(decoded *T, p oam.Policy) error
}

// fluxDefaultedZeros is a Flux kind's defaulted-zero list for
// refuseUncarriedSpecValues: fields maps a json path of its spec type that the
// encoding omits when empty to the other default the CRD gives it, as its JSON
// literal. TestFluxKinds_DefaultedZeros holds each kind's list to the default
// markers of the linked modules' source.
func fluxDefaultedZeros(fields map[string]string) defaultedZeroFields {
	return defaultedZeroFields{api: "Flux", defaulter: "API server", fields: fields}
}

// config is policyFreeKind.config and the check of the authored durations,
// returning a config that moves to the Flux namespace. The durations are
// checked on the authored text, after the strict decode
// (checkAuthoredFluxDurations); a config is built here only, so Generate has
// nothing further to check.
func (k *fluxKind[T]) config(component *oam.Component) (stack.ApplicationConfig, error) {
	cfg, err := k.policyFreeKind.config(component)
	if err != nil {
		return nil, err
	}
	if err := checkAuthoredFluxDurations(component.Type, component.Properties, k.durations); err != nil {
		return nil, err
	}
	free, ok := cfg.(*policyFreeKindConfig[T])
	if !ok {
		return nil, errors.Errorf("internal: a policy-free kind's config is a %T", cfg)
	}
	return &fluxKindConfig[T]{policyFreeKindConfig: free, component: component.Type, reads: k.reads, durations: k.durations, enforce: k.enforce}, nil
}

// fluxKindConfig implements stack.ApplicationConfig for a fluxKind: a
// policyFreeKindConfig that builds its object in the Flux namespace when one
// is set, and that asks the kind's enforce, where it has one, about the
// policy.
type fluxKindConfig[T any] struct {
	*policyFreeKindConfig[T]
	// component is the component's type, for an error of Generate.
	component string
	reads     func(decoded *T, r *fluxReads)
	durations []fluxDurationField[T]
	enforce   func(decoded *T, p oam.Policy) error

	// fluxNS overrides the object's namespace. Set by postProcessFluxNamespace
	// via TransformContext.FluxNamespace. Empty means the application's.
	fluxNS string
}

// SetFluxNamespace moves the object to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *fluxKindConfig[T]) SetFluxNamespace(ns string) { c.fluxNS = ns }

// FluxNamespaceReads reports the ConfigMaps and Secrets the object reads by
// name from the namespace it lands in.
func (c *fluxKindConfig[T]) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	if c.reads != nil {
		c.reads(c.decoded, &r)
	}
	return r.configMaps, r.secrets
}

// ApplyPolicy holds the decoded value to the policy, for a kind with an
// enforce. A nil policy checks nothing, and so does a kind without one.
func (c *fluxKindConfig[T]) ApplyPolicy(p oam.Policy) error {
	if p == nil || c.enforce == nil {
		return nil
	}
	return c.enforce(c.decoded, p)
}

// Generate is policyFreeKindConfig.Generate in the Flux namespace when one is
// set, else in the application's, with every duration in the text its pattern
// takes (emitFluxKind).
func (c *fluxKindConfig[T]) Generate(app *stack.Application) ([]*client.Object, error) {
	obj := c.kind.build(kindObjectName(c.objectName, app.Name), fluxSourceNamespace(app.Namespace, c.fluxNS), c.decoded)
	return emitFluxKind(c.component, obj, c.decoded, c.durations, c.metadata)
}
