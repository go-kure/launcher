package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// fluxNamespaceSettable mirrors oam.fluxNamespaceSettable locally because
// oam.fluxNamespaceSettable is unexported and cannot be referenced cross-package.
type fluxNamespaceSettable interface {
	SetFluxNamespace(string)
}

// fluxNamespaceReader mirrors oam.fluxNamespaceReader locally (unexported
// cross-package). Decorators forward it so a wrapped Flux config's inputs (a
// helmrelease's valuesFrom) still follow it to the Flux namespace.
type fluxNamespaceReader interface {
	FluxNamespaceReads() (configMaps, secrets []string)
}

// serviceRoutingTargeter mirrors oam.serviceRoutingTargeter locally (unexported
// cross-package). Decorators forward it so a wrapped `service` component's routed
// traffic still lands on its selector's pods in the inbound NetworkPolicy synthesis.
type serviceRoutingTargeter interface {
	ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString)
}

// podTemplateLabeler and identityPortMapper mirror the oam contracts of the same
// names locally (unexported cross-package). Decorators forward them so a sibling
// group still tells a member routing to its own sibling's pods when a trait wraps
// either member.
type podTemplateLabeler interface {
	PodTemplateLabels() map[string]string
}

type identityPortMapper interface {
	IdentityTargetPorts() bool
}

// decoratorBase holds the wrapped ApplicationConfig and forwards every optional
// interface a component config may satisfy. Trait decorators embed it so that an
// N-deep wrap chain never hides an interface from a later trait or a post-build
// phase.
//
// The twelve forwarded interfaces are exactly those type-asserted on app.Config
// AFTER traits run: stack.Validator (kure stack.Application.Generate),
// fluxNamespaceSettable (oam/transform.go postProcessFluxNamespace),
// fluxNamespaceReader (oam/flux_namespace_inputs.go moveFluxNamespaceInputs),
// servicePortProvider and serviceBackendNamer
// (traits/ingress.go:41,53,62 and oam/netpol_synthesis.go:68,71),
// servicePortNamer (traits/ingress.go:77), oam.ServiceAccountNamer
// (traits/rbac.go:88), nonRWXClaimer (traits/scaler.go:60),
// serviceRoutingTargeter (oam/netpol_synthesis.go:257), and podTemplateLabeler
// and identityPortMapper (oam/sibling_group.go ServiceRoutingTarget), and
// oam.ComponentNamed (oam/in_document_collisions.go generateBundle), which a
// decorated config must keep naming its component. Enforceable is deliberately
// absent: it is asserted only on trait sub-apps as their trait creates them
// (Transformer.applyEntryTraits), and no decorator wraps a sub-application,
// so it cannot see a decorator. So are the
// NetworkPolicy synthesis collectors (trafficSourceCollector,
// backendRefTargetCollector): they are trait sub-application contracts, and
// forwarding them would make every decorated component a router. One more,
// kure's layout.LayoutAugmenter, is forwarded separately — see wrapIfAugmenter
// below — because unlike these twelve it must NOT be present unconditionally;
// so is its extension layout.LayoutIntentAugmenter, for the same reason.
//
// Every method is defined unconditionally, so an embedding decorator ALWAYS
// satisfies all twelve. Call sites must therefore test the returned VALUE, not the
// interface's presence. Seven already do this correctly and must stay that way:
// resolveServiceName (ingress.go) checks for a non-empty name,
// checkImplicitPortName (ingress.go) checks the known flag, rbac.go's
// binding subject reads the runsPods flag beside the name, the scaler treats
// an empty claim name as "no claim limits the replicas",
// the NetworkPolicy synthesis treats a nil routing
// selector as "not a routing targeter", a sibling group's ServiceRoutingTarget
// treats nil pod template labels as "runs no pods" and false as "remaps a port",
// and generateBundle falls back to the application name on an empty component
// name.
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
// when it satisfies fluxNamespaceSettable (e.g. HelmReleaseConfig).
func (d decoratorBase) SetFluxNamespace(ns string) {
	if setter, ok := d.Inner.(fluxNamespaceSettable); ok {
		setter.SetFluxNamespace(ns)
	}
}

// FluxNamespaceReads forwards the ConfigMaps and Secrets the inner config's Flux
// object reads from its own namespace (fluxNamespaceReader), or none when the
// inner config does not implement it.
func (d decoratorBase) FluxNamespaceReads() (configMaps, secrets []string) {
	if r, ok := d.Inner.(fluxNamespaceReader); ok {
		return r.FluxNamespaceReads()
	}
	return nil, nil
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

// ServicePortName forwards the inner config's service port name
// (servicePortNamer), or "" and false when the inner config does not know its
// port names. Without this forward, a decorating trait declared before a
// routing trait hides the port names, and an implicit backend addressed by a
// name the component's Service port does not carry slips past the check.
func (d decoratorBase) ServicePortName() (string, bool) {
	if n, ok := d.Inner.(servicePortNamer); ok {
		return n.ServicePortName()
	}
	return "", false
}

// ServiceAccountName forwards the inner config's ServiceAccount name and
// runsPods flag (oam.ServiceAccountNamer), or "" and false when the inner
// config does not implement it. Without this forward, a decorating trait
// declared before `rbac` (security-context, configmap, external-secret)
// hides the workload's authored serviceAccountName, and
// rbac.go could no longer tell a pod without one (refused) from a config that
// runs no pods.
func (d decoratorBase) ServiceAccountName() (string, bool) {
	if n, ok := d.Inner.(oam.ServiceAccountNamer); ok {
		return n.ServiceAccountName()
	}
	return "", false
}

// NonRWXClaim forwards the inner config's single-pod claim (nonRWXClaimer), or
// "" when the inner config names none. Without this forward, a decorating
// trait declared before `scaler` (configmap with a mountPath, security-context,
// external-secret) hides the claim and the scaler's maxReplicas check silently
// passes.
func (d decoratorBase) NonRWXClaim() string {
	if n, ok := d.Inner.(nonRWXClaimer); ok {
		return n.NonRWXClaim()
	}
	return ""
}

// ServiceRoutingTarget forwards the inner config's routing target
// (serviceRoutingTargeter), or a nil selector and no ports when the inner
// config is not one. Without this forward, a decorating trait on a `service`
// component hides its selector and the synthesized ingress allow selects the
// component label, which no pod carries.
func (d decoratorBase) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	if t, ok := d.Inner.(serviceRoutingTargeter); ok {
		return t.ServiceRoutingTarget(servicePorts)
	}
	return nil, nil
}

// PodTemplateLabels forwards the inner config's pod template labels
// (podTemplateLabeler), or nil when the inner config runs no pods. No trait
// rewrites pod template labels, so the inner answer stays true of the decorated
// output.
func (d decoratorBase) PodTemplateLabels() map[string]string {
	if l, ok := d.Inner.(podTemplateLabeler); ok {
		return l.PodTemplateLabels()
	}
	return nil
}

// IdentityTargetPorts forwards the inner config's answer (identityPortMapper), or
// false when the inner config is not one, which keeps a sibling group forwarding
// the routing target as before.
func (d decoratorBase) IdentityTargetPorts() bool {
	if m, ok := d.Inner.(identityPortMapper); ok {
		return m.IdentityTargetPorts()
	}
	return false
}

// ComponentName forwards the inner config's component (oam.ComponentNamed), or
// "" when the inner config does not name one. Callers must treat "" as "fall
// back to the application name" — generateBundle does. Without this forward, a
// decorated config that names its component loses it, and a caller reading
// ComponentNamed attributes its objects to the application's own name.
func (d decoratorBase) ComponentName() string {
	if n, ok := d.Inner.(oam.ComponentNamed); ok {
		return n.ComponentName()
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
//
// The main container's raw block devices are checked by name too
// (go-kure/launcher#385): a statefulset's volumeMode: Block claim template
// has no entry in podSpec.Volumes, only a VolumeDevice, and a same-named
// mount beside it is refused by ValidateVolumeDevices.
//
// So are the main container's mounts: a statefulset's filesystem claim
// template likewise has no entry in podSpec.Volumes, only a VolumeMount, and
// the StatefulSet controller replaces a pod volume named like a claim template
// with the claim — the trait's ConfigMap or Secret would never be mounted.
// Every other mount names a Volume, which the first loop already covers.
func checkVolumeCollision(podSpec *corev1.PodSpec, name, source, hint string) error {
	for _, v := range podSpec.Volumes {
		if v.Name == name {
			return errors.Errorf(
				"%s: volume %q already exists on the workload; %s",
				source, name, hint)
		}
	}
	if len(podSpec.Containers) > 0 {
		for _, vd := range podSpec.Containers[0].VolumeDevices {
			if vd.Name == name {
				return errors.Errorf(
					"%s: volume %q already exists on the workload as a raw block device; %s",
					source, name, hint)
			}
		}
		for _, vm := range podSpec.Containers[0].VolumeMounts {
			if vm.Name == name {
				return errors.Errorf(
					"%s: volume %q already exists on the workload as a claim template; %s",
					source, name, hint)
			}
		}
	}
	return nil
}

// decoratedConfig is the method set every decoratorBase-embedding decorator
// satisfies unconditionally: Generate, plus decoratorBase's twelve forwards
// (Validate, SetFluxNamespace, FluxNamespaceReads, ServicePort,
// BackendServiceName, ServicePortName, ServiceAccountName, NonRWXClaim,
// ServiceRoutingTarget, PodTemplateLabels, IdentityTargetPorts, ComponentName).
// augmentingDecorator embeds this — not the narrower
// stack.ApplicationConfig — because embedding an interface-typed field
// promotes only that interface's own declared method set, not the full
// method set of the dynamic value stored in it: a field typed as plain
// stack.ApplicationConfig would silently drop the twelve decoratorBase forwards
// the moment wrapIfAugmenter actually wraps, reintroducing Task 1's bug for
// every future component whose inner config implements LayoutAugmenter.
type decoratedConfig interface {
	stack.ApplicationConfig
	stack.Validator
	fluxNamespaceSettable
	fluxNamespaceReader
	servicePortProvider
	serviceBackendNamer
	servicePortNamer
	oam.ServiceAccountNamer
	nonRWXClaimer
	serviceRoutingTargeter
	podTemplateLabeler
	identityPortMapper
	oam.ComponentNamed
}

// augmentingDecorator adds AugmentLayout to an outer decorator only when the
// wrapped inner config implements layout.LayoutAugmenter. See wrapIfAugmenter
// for why this must be conditional rather than an unconditional forward like
// decoratorBase's other twelve methods, and see decoratedConfig for why this
// embeds that instead of stack.ApplicationConfig.
type augmentingDecorator struct {
	decoratedConfig
	augmenter layout.LayoutAugmenter
}

// AugmentLayout forwards to the inner config's LayoutAugmenter implementation.
// No decorator processes the augmented layout: every one rewrites only what its
// inner Generate returns (security-context patching a PodSpec, for one). What
// must reach the objects an augmenter adds is stated as a delivery intent on
// the application instead (prune-protection, force-replace), which the
// delivering workflow applies to the whole layout (go-kure/launcher#782).
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
// all. Defined directly on augmentingDecorator rather than as a tenth
// decoratorBase forward: that would also require adding it to decoratedConfig
// (the embedding hazard decoratedConfig's own doc comment describes) and
// would grant a meaningless method to every non-augmenting decorator.
func (a augmentingDecorator) GenerateCoversAugmentLayout() bool {
	cov, ok := a.augmenter.(oam.LayoutAugmentationCoverage)
	return ok && cov.GenerateCoversAugmentLayout()
}

var _ oam.LayoutAugmentationCoverage = augmentingDecorator{}

// intentAugmentingDecorator is an augmentingDecorator that also forwards
// kure's layout.LayoutIntentAugmenter, for an inner config that implements it.
// The layout walker reads WantsOwnLayout by assertion and treats an absent
// method as "wants its own layout" (pkg/stack/layout/walker.go:454-469), so an
// inner augmenter answering false would lose its placement the moment a trait
// decorated it. Like AugmentLayout, the method is present only when the inner
// has it; wrapIfAugmenter picks this type then, and augmentingDecorator
// otherwise. A wrap chain keeps the intent by induction: each level's inner is
// the previous level's wrapper.
type intentAugmentingDecorator struct {
	augmentingDecorator
	intent layout.LayoutIntentAugmenter
}

// WantsOwnLayout forwards to the inner config's LayoutIntentAugmenter.
func (a intentAugmentingDecorator) WantsOwnLayout() bool {
	return a.intent.WantsOwnLayout()
}

var _ layout.LayoutIntentAugmenter = intentAugmentingDecorator{}

// wrapIfAugmenter returns outer unchanged when inner does not implement
// layout.LayoutAugmenter, or an augmentingDecorator embedding outer (so outer's
// own Generate and its decoratorBase forwards still promote through via
// decoratedConfig) when it does — an intentAugmentingDecorator when inner also
// implements layout.LayoutIntentAugmenter. kure's layout walker
// (pkg/stack/layout/walker.go:454-459) type-asserts LayoutAugmenter by
// PRESENCE, and that presence decides whether the app gets a per-app
// sub-layout or merges into the parent's flat Resources (walker.go:473-505) —
// a structural decision, not a side effect with a safe no-op default. So
// unlike decoratorBase's other twelve forwards, this one cannot be defined
// unconditionally: doing so would force every decorated component into
// per-app sub-layout placement regardless of what its inner config wants.
// Every trait decorator constructor must route its return value through this.
func wrapIfAugmenter(outer decoratedConfig, inner stack.ApplicationConfig) stack.ApplicationConfig {
	a, ok := inner.(layout.LayoutAugmenter)
	if !ok {
		return outer
	}
	wrapped := augmentingDecorator{decoratedConfig: outer, augmenter: a}
	if intent, ok := inner.(layout.LayoutIntentAugmenter); ok {
		return intentAugmentingDecorator{augmentingDecorator: wrapped, intent: intent}
	}
	return wrapped
}

// checkMountPathCollision returns an error if podSpec's first container already
// has a VolumeMount at mountPath. Two decorators can mount DIFFERENTLY NAMED
// volumes at the same path — checkVolumeCollision (which compares Volume.Name)
// does not catch that — yet Kubernetes requires every VolumeMount.MountPath in a
// container to be unique and rejects the PodSpec otherwise. A raw block
// device's devicePath is checked too (go-kure/launcher#385):
// ValidateVolumeDevices refuses a devicePath that is also a mountPath in the
// same container. hint names the property the caller can change to resolve
// the collision.
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
	for _, vd := range podSpec.Containers[0].VolumeDevices {
		if vd.DevicePath == mountPath {
			return errors.Errorf(
				"%s: mountPath %q is already the devicePath of block volume %q on the workload; %s",
				source, mountPath, vd.Name, hint)
		}
	}
	return nil
}
