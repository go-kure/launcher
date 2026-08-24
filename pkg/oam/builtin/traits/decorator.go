package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
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
// (traits/ingress.go:33,45,55 and oam/netpol_synthesis.go:57,60). Enforceable and
// SourceDeduplicatable are deliberately absent: the former is asserted only on
// trait sub-apps (transform.go:675), the latter runs in phase 1 (transform.go:461),
// so neither can see a decorator. A sixth, kure's layout.LayoutAugmenter, is
// forwarded separately — see wrapIfAugmenter below — because unlike these five
// it must NOT be present unconditionally.
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

// decoratedConfig is the method set every decoratorBase-embedding decorator
// satisfies unconditionally: Generate, plus decoratorBase's five forwards
// (Validate, SetFluxNamespace, EmitsAutoHealthCheck, ServicePort,
// BackendServiceName). augmentingDecorator embeds this — not the narrower
// stack.ApplicationConfig — because embedding an interface-typed field
// promotes only that interface's own declared method set, not the full
// method set of the dynamic value stored in it: a field typed as plain
// stack.ApplicationConfig would silently drop the five decoratorBase forwards
// the moment wrapIfAugmenter actually wraps, reintroducing Task 1's bug for
// every future component whose inner config implements LayoutAugmenter.
type decoratedConfig interface {
	stack.ApplicationConfig
	stack.Validator
	fluxNamespaceSettable
	autoHealthCheckEmitter
	servicePortProvider
	serviceBackendNamer
}

// augmentingDecorator adds AugmentLayout to an outer decorator only when the
// wrapped inner config implements layout.LayoutAugmenter. See wrapIfAugmenter
// for why this must be conditional rather than an unconditional forward like
// decoratorBase's other five methods, and see decoratedConfig for why this
// embeds that instead of stack.ApplicationConfig.
type augmentingDecorator struct {
	decoratedConfig
	augmenter layout.LayoutAugmenter
}

// AugmentLayout forwards to the inner config's LayoutAugmenter implementation.
func (a augmentingDecorator) AugmentLayout(l *layout.ManifestLayout) error {
	return a.augmenter.AugmentLayout(l)
}

// GenerateCoversAugmentLayout forwards to the inner augmenter's
// oam.LayoutAugmentationCoverage, false if it doesn't implement one. Unlike
// AugmentLayout above, this forward is UNCONDITIONAL: its presence carries no
// structural meaning to kure's layout walker (unlike LayoutAugmenter's
// presence, which augmentingDecorator's own construction already gates via
// wrapIfAugmenter below), and pkg/cmd/kurel's build guard already treats
// "absent" and "false" identically, so decorating an inner that doesn't
// implement it is exactly as fail-closed as not implementing this method at
// all. Defined directly on augmentingDecorator rather than as a sixth
// decoratorBase forward: that would also require adding it to decoratedConfig
// (the embedding hazard decoratedConfig's own doc comment describes) and
// would grant a meaningless method to every non-augmenting decorator.
func (a augmentingDecorator) GenerateCoversAugmentLayout() bool {
	cov, ok := a.augmenter.(oam.LayoutAugmentationCoverage)
	return ok && cov.GenerateCoversAugmentLayout()
}

var _ oam.LayoutAugmentationCoverage = augmentingDecorator{}

// wrapIfAugmenter returns outer unchanged when inner does not implement
// layout.LayoutAugmenter, or an augmentingDecorator embedding outer (so outer's
// own Generate and its decoratorBase forwards still promote through via
// decoratedConfig) when it does. kure's layout walker
// (pkg/stack/layout/walker.go:454-459) type-asserts LayoutAugmenter by
// PRESENCE, and that presence decides whether the app gets a per-app
// sub-layout or merges into the parent's flat Resources (walker.go:473-505) —
// a structural decision, not a side effect with a safe no-op default. So
// unlike decoratorBase's other five forwards, this one cannot be defined
// unconditionally: doing so would force every decorated component into
// per-app sub-layout placement regardless of what its inner config wants.
// Every trait decorator constructor must route its return value through this.
func wrapIfAugmenter(outer decoratedConfig, inner stack.ApplicationConfig) stack.ApplicationConfig {
	if a, ok := inner.(layout.LayoutAugmenter); ok {
		return augmentingDecorator{decoratedConfig: outer, augmenter: a}
	}
	return outer
}

// checkMountPathCollision returns an error if podSpec's first container already
// has a VolumeMount at mountPath. Two decorators can mount DIFFERENTLY NAMED
// volumes at the same path — checkVolumeCollision (which compares Volume.Name)
// does not catch that — yet Kubernetes requires every VolumeMount.MountPath in a
// container to be unique and rejects the PodSpec otherwise. hint names the
// property the caller can change to resolve the collision.
func checkMountPathCollision(podSpec *corev1.PodSpec, mountPath, source, hint string) error {
	if len(podSpec.Containers) == 0 {
		return nil
	}
	for _, vm := range podSpec.Containers[0].VolumeMounts {
		if vm.MountPath == mountPath {
			return errors.Errorf(
				"%s: mountPath %q is already used by volume %q on the workload; %s",
				source, mountPath, vm.Name, hint)
		}
	}
	return nil
}
