package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// fluxNamespaceSettable mirrors oam.fluxNamespaceSettable locally because
// oam.fluxNamespaceSettable is unexported and cannot be referenced cross-package.
type fluxNamespaceSettable interface {
	SetFluxNamespace(string)
}

// autoHealthCheckEmitter mirrors oam.autoHealthCheckEmitter locally (unexported
// cross-package). Decorators forward it so a wrapped helmchart's template-delivery
// veto still reaches the auto health-check synthesis.
type autoHealthCheckEmitter interface {
	EmitsAutoHealthCheck() bool
}

// decoratorBase holds the wrapped ApplicationConfig and forwards every optional
// interface a component config may satisfy. Trait decorators embed it so that an
// N-deep wrap chain never hides an interface from a later trait or a post-build
// phase.
//
// The five forwarded interfaces are exactly those type-asserted on app.Config
// AFTER traits run: stack.Validator (kure pkg/stack/application.go:51),
// fluxNamespaceSettable (oam/transform.go:830,887), autoHealthCheckEmitter
// (oam/transform.go:875), servicePortProvider and serviceBackendNamer
// (traits/ingress.go:33,42,51 and oam/netpol_synthesis.go:57,60). Enforceable and
// SourceDeduplicatable are deliberately absent: the former is asserted only on
// trait sub-apps (transform.go:675), the latter runs in phase 1 (transform.go:461),
// so neither can see a decorator.
//
// Every method is defined unconditionally, so an embedding decorator ALWAYS
// satisfies all five. Call sites must therefore test the returned VALUE, not the
// interface's presence. Two already do this correctly and must stay that way:
// resolveServiceName (ingress.go) checks for a non-empty name, and
// applyAutoHealthChecks (oam/transform.go:884-889) gates its settable check on
// isFluxControlPlaneGVK.
type decoratorBase struct {
	Inner stack.ApplicationConfig
}

// Validate forwards to the inner config's stack.Validator. kure's
// stack.Application.Generate calls Validate only on the OUTERMOST config, so
// without this a wrapped config is never validated at all.
func (d decoratorBase) Validate() error {
	if v, ok := d.Inner.(stack.Validator); ok {
		return v.Validate()
	}
	return nil
}

// SetFluxNamespace forwards the per-request Flux namespace to the inner config
// when it satisfies fluxNamespaceSettable (e.g. HelmchartConfig).
func (d decoratorBase) SetFluxNamespace(ns string) {
	if setter, ok := d.Inner.(fluxNamespaceSettable); ok {
		setter.SetFluxNamespace(ns)
	}
}

// EmitsAutoHealthCheck forwards the inner config's auto-health-check veto (e.g. a
// wrapped helmchart with delivery=template emits no HelmRelease). Defaults to true
// when the inner config does not implement the interface.
func (d decoratorBase) EmitsAutoHealthCheck() bool {
	if e, ok := d.Inner.(autoHealthCheckEmitter); ok {
		return e.EmitsAutoHealthCheck()
	}
	return true
}

// ServicePort forwards the inner config's Service port, or 0 when it exposes none.
// Callers already treat 0 as "no service port", so an unconditional method is safe.
func (d decoratorBase) ServicePort() int32 {
	if p, ok := d.Inner.(servicePortProvider); ok {
		return p.ServicePort()
	}
	return 0
}

// BackendServiceName forwards the inner config's Service name, or "" when it does
// not override it. Callers must treat "" as "fall back to the component name".
func (d decoratorBase) BackendServiceName() string {
	if n, ok := d.Inner.(serviceBackendNamer); ok {
		return n.BackendServiceName()
	}
	return ""
}

// checkVolumeCollision returns an error if podSpec already has a Volume named
// name. Both ExternalSecretDecorator and ConfigMapDecorator add a Volume named
// after their target resource (the Secret or ConfigMap), and decorators can
// wrap in either order depending on trait declaration order — so each must
// check for an existing same-named Volume before appending its own, not just
// one of them, or a component naming both the same collides silently into an
// invalid duplicate-volume PodSpec instead of a clear error. hint names the
// property the caller can change to resolve the collision (e.g. "rename the
// secret via targetSecretName").
func checkVolumeCollision(podSpec *corev1.PodSpec, name, source, hint string) error {
	for _, v := range podSpec.Volumes {
		if v.Name == name {
			return errors.Errorf(
				"%s: volume %q already exists on the workload; %s",
				source, name, hint)
		}
	}
	return nil
}
