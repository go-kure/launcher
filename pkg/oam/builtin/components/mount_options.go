package components

import (
	"path"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// mountOptionKeys are the VolumeMount keys parseMountOptions reads, beyond the
// name and mountPath every mount carries (go-kure/launcher#1193).
var mountOptionKeys = append([]string{"readOnly"}, mountExtraKeys...)

// mountExtraKeys are the mountOptionKeys other than readOnly. A `volumes` entry
// reads readOnly itself, per volume type (parseVolumes), and hands these four
// to parseMountExtras (go-kure/launcher#1198).
var mountExtraKeys = []string{"subPath", "subPathExpr", "mountPropagation", "recursiveReadOnly"}

// parseMountOptions reads readOnly, subPath, subPathExpr, mountPropagation and
// recursiveReadOnly from one authored mount into vm, and applies the rules
// upstream ValidateVolumeMounts applies to a single mount
// (k8s.io/kubernetes pkg/apis/core/validation/validation.go, v1.37.1):
//   - mountPropagation is None, HostToContainer or Bidirectional (:1457-1463);
//   - recursiveReadOnly is Disabled, IfPossible or Enabled (:1486-1501), and
//     Enabled or IfPossible need readOnly: true (:1490-1492) and a
//     mountPropagation of None or none at all (:1493-1495);
//   - subPath and subPathExpr are mutually exclusive (:3209-3212), and
//     subPathExpr is relative and holds no ".." element (:3214, :1421-1430).
//
// The one rule that needs the container is Bidirectional propagation, which
// upstream allows on a privileged container only (:1474-1476). The caller
// holds the securityContext, so checkMountPropagationPrivileged applies it.
//
// label names the mount, like "initContainers[0] \"x\": volumeMounts[1]".
func parseMountOptions(mm map[string]any, label string, vm *corev1.VolumeMount) error {
	roPtr, err := parseBoolField(mm, "readOnly", label+".readOnly")
	if err != nil {
		return err
	}
	if roPtr != nil {
		vm.ReadOnly = *roPtr
	}
	return parseMountExtras(mm, label, vm)
}

// parseMountExtras reads the mountExtraKeys into vm. vm.ReadOnly must already
// hold the mount's readOnly, because recursiveReadOnly is checked against it.
// parseMountOptions and the `volumes` parser both end here, so the two read the
// four keys in one place.
func parseMountExtras(mm map[string]any, label string, vm *corev1.VolumeMount) error {
	if sp, present, err := parseStringField(mm, "subPath", label+".subPath"); err != nil {
		return err
	} else if present {
		vm.SubPath = sp
	}
	if sp, present, err := parseStringField(mm, "subPathExpr", label+".subPathExpr"); err != nil {
		return err
	} else if present {
		if vm.SubPath != "" {
			return errors.Errorf("%s: subPathExpr and subPath are mutually exclusive", label)
		}
		if path.IsAbs(sp) {
			return errors.Errorf("%s.subPathExpr: must be a relative path, got %q", label, sp)
		}
		if slices.Contains(strings.Split(sp, "/"), "..") {
			return errors.Errorf("%s.subPathExpr: must not contain a '..' element, got %q", label, sp)
		}
		vm.SubPathExpr = sp
	}
	if mp, present, err := parseRawStringField(mm, "mountPropagation", label+".mountPropagation"); err != nil {
		return err
	} else if present {
		mode := corev1.MountPropagationMode(mp)
		switch mode {
		case corev1.MountPropagationNone, corev1.MountPropagationHostToContainer, corev1.MountPropagationBidirectional:
		default:
			return errors.Errorf("%s.mountPropagation: must be one of None, HostToContainer, Bidirectional, got %q", label, mp)
		}
		vm.MountPropagation = &mode
	}
	if rro, present, err := parseRawStringField(mm, "recursiveReadOnly", label+".recursiveReadOnly"); err != nil {
		return err
	} else if present {
		mode := corev1.RecursiveReadOnlyMode(rro)
		switch mode {
		case corev1.RecursiveReadOnlyDisabled:
		case corev1.RecursiveReadOnlyEnabled, corev1.RecursiveReadOnlyIfPossible:
			if !vm.ReadOnly {
				return errors.Errorf("%s.recursiveReadOnly: %s needs readOnly: true", label, rro)
			}
			if vm.MountPropagation != nil && *vm.MountPropagation != corev1.MountPropagationNone {
				return errors.Errorf("%s.recursiveReadOnly: %s needs mountPropagation None or none at all, got %q", label, rro, *vm.MountPropagation)
			}
		default:
			return errors.Errorf("%s.recursiveReadOnly: must be one of Disabled, IfPossible, Enabled, got %q", label, rro)
		}
		vm.RecursiveReadOnly = &mode
	}
	return nil
}

// checkMountPropagationPrivileged refuses a Bidirectional mount on a container
// that is not privileged, as upstream does once the container is known
// (validateMountPropagation, validation.go:1474-1476). sc is the container's
// parsed securityContext, nil when it authors none.
func checkMountPropagationPrivileged(label string, mounts []corev1.VolumeMount, sc *corev1.SecurityContext) error {
	privileged := sc != nil && sc.Privileged != nil && *sc.Privileged
	for i, vm := range mounts {
		if vm.MountPropagation != nil && *vm.MountPropagation == corev1.MountPropagationBidirectional && !privileged {
			return errors.Errorf("%s: volumeMounts[%d].mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true", label, i)
		}
	}
	return nil
}

// checkMainContainerMountPropagation is checkMountPropagationPrivileged for the
// main container, whose mounts come from `volumes` and `volumeClaimTemplates`
// (go-kure/launcher#1198). Each kind calls it once, after it has parsed both
// the securityContext and the mounts: parseVolumes runs apart from the
// securityContext parse, so the check cannot live there. The error names the
// mount, because the list holds claim-template mounts before `volumes` ones.
func checkMainContainerMountPropagation(mounts []corev1.VolumeMount, sc *corev1.SecurityContext) error {
	privileged := sc != nil && sc.Privileged != nil && *sc.Privileged
	for _, vm := range mounts {
		if vm.MountPropagation != nil && *vm.MountPropagation == corev1.MountPropagationBidirectional && !privileged {
			return errors.Errorf("mount %q: mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true", vm.Name)
		}
	}
	return nil
}

// refuseMountKeysOnDevice refuses the keys in keys that a devicePath entry
// authors: a volumeMode: Block claim reaches the container as a raw block
// device, which has no mount to carry them (go-kure/launcher#1198). label names
// the entry. A key that is null, typed or untyped, is absent: authoredValue is
// the presence test every parser of this package uses.
func refuseMountKeysOnDevice(mm map[string]any, keys []string, label string) error {
	for _, k := range keys {
		if _, present := authoredValue(mm, k); present {
			return errors.Errorf("%s: %s is not valid with devicePath, because a raw block device has no mount", label, k)
		}
	}
	return nil
}
