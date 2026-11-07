package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// validateTypedVolumeMounts applies to the volumeMounts of every init and
// regular container of ps the rules parseMountExtras applies to an authored
// app.yaml mount, which are the single-mount rules of upstream
// ValidateVolumeMounts (k8s.io/kubernetes pkg/apis/core/validation/validation.go,
// v1.37.1): subPath and subPathExpr are mutually exclusive and each is a
// relative path with no ".." element (:3206-3214); mountPropagation is None,
// HostToContainer or Bidirectional (:1450-1463), and Bidirectional needs a
// privileged container (:1474-1476); recursiveReadOnly is Disabled, IfPossible
// or Enabled, and the last two need readOnly: true and a mountPropagation of
// None or none at all (:1481-1500). go-kure/launcher#1212.
//
// The mounts here are typed, read from a pod spec a kind component decoded
// from its properties or from an object written elsewhere (a manifests
// source), so there is no authored map and no null to read: parseMountExtras
// cannot run on them. The two rule sets are held together by
// TestTypedVolumeMounts_MatchAuthoredMounts. An empty subPath or subPathExpr is
// absent, as upstream skips it; a mountPropagation or recursiveReadOnly that
// points at an empty string is set to a value no mode has, and is refused.
//
// prefix is the pod spec's path in the object, ending in a dot, or "" when the
// object is the pod spec's own, as in validateAuthoredPodSpec. The rules the
// API server keeps to itself (mountPath, the volume a mount names, devices that
// overlap a mount, bindMountOptions) are not applied. Ephemeral containers are
// not read: they are refused outright by the callers.
func validateTypedVolumeMounts(prefix string, ps *corev1.PodSpec) error {
	for i := range ps.InitContainers {
		ctr := &ps.InitContainers[i]
		if err := validateContainerMounts(fmt.Sprintf("%sinitContainers[%d] %q", prefix, i, ctr.Name), ctr); err != nil {
			return err
		}
	}
	for i := range ps.Containers {
		ctr := &ps.Containers[i]
		if err := validateContainerMounts(fmt.Sprintf("%scontainers[%d] %q", prefix, i, ctr.Name), ctr); err != nil {
			return err
		}
	}
	return nil
}

// validateContainerMounts holds each mount of ctr to the single-mount rules,
// then the mounts to the privileged-container rule. label names the container,
// like `containers[0] "app"`.
func validateContainerMounts(label string, ctr *corev1.Container) error {
	for j := range ctr.VolumeMounts {
		if err := validateTypedMount(fmt.Sprintf("%s: volumeMounts[%d]", label, j), &ctr.VolumeMounts[j]); err != nil {
			return err
		}
	}
	return checkMountPropagationPrivileged(label, ctr.VolumeMounts, ctr.SecurityContext)
}

// validateTypedMount applies the rules of one mount that parseMountExtras
// applies, in its order, so an input that breaks two rules gets the same first
// diagnostic from either. label names the mount, like
// `containers[0] "app": volumeMounts[1]`.
func validateTypedMount(label string, vm *corev1.VolumeMount) error {
	if vm.SubPathExpr != "" {
		if vm.SubPath != "" {
			return errors.Errorf("%s: subPathExpr and subPath are mutually exclusive", label)
		}
		if err := checkDescendingPath(label+".subPathExpr", vm.SubPathExpr); err != nil {
			return err
		}
	}
	if vm.SubPath != "" {
		if err := checkDescendingPath(label+".subPath", vm.SubPath); err != nil {
			return err
		}
	}
	if mp := vm.MountPropagation; mp != nil {
		switch *mp {
		case corev1.MountPropagationNone, corev1.MountPropagationHostToContainer, corev1.MountPropagationBidirectional:
		default:
			return errors.Errorf("%s.mountPropagation: must be one of None, HostToContainer, Bidirectional, got %q", label, *mp)
		}
	}
	if rro := vm.RecursiveReadOnly; rro != nil {
		switch *rro {
		case corev1.RecursiveReadOnlyDisabled:
		case corev1.RecursiveReadOnlyEnabled, corev1.RecursiveReadOnlyIfPossible:
			if !vm.ReadOnly {
				return errors.Errorf("%s.recursiveReadOnly: %s needs readOnly: true", label, *rro)
			}
			if vm.MountPropagation != nil && *vm.MountPropagation != corev1.MountPropagationNone {
				return errors.Errorf("%s.recursiveReadOnly: %s needs mountPropagation None or none at all, got %q", label, *rro, *vm.MountPropagation)
			}
		default:
			return errors.Errorf("%s.recursiveReadOnly: must be one of Disabled, IfPossible, Enabled, got %q", label, *rro)
		}
	}
	return nil
}

// validateObjectMounts holds the pod spec of one typed workload object to
// validateTypedVolumeMounts and returns its refusal bare, for the caller to
// name the object in (validateManifestObjects for a manifests source). An
// object that runs no pod, and one that reached the build untyped (a workload
// in an API version kure's scheme does not register), passes: the latter has
// no typed pod spec to read, and the policy check refuses it as unreadable
// (enforceRenderedObjectPolicy), which every transform runs.
func validateObjectMounts(obj client.Object) error {
	path, ps := renderedPodSpec(obj)
	if ps == nil {
		return nil
	}
	return validateTypedVolumeMounts(path+".", ps)
}
