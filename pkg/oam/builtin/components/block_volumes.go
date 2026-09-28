package components

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// Raw block volumes (go-kure/launcher#385).
//
// A PersistentVolumeClaim with volumeMode: Block has no filesystem: a container
// consumes it through `volumeDevices` at a `devicePath`, never through
// `volumeMounts`. The claim and the pod template are validated as separate
// objects, so the apiserver accepts a Block claim paired with a filesystem
// mount and the pod then fails at kubelet mount time. This file keeps the two
// sides consistent at build time:
//
//   - a `volumes[].pvc` entry and a `volumeClaimTemplates` entry take exactly
//     one of `mountPath` and `devicePath`, and `devicePath` is authored if and
//     only if `volumeMode: Block` is. Neither side is inferred from the other,
//     so a document says what it builds.
//   - the main container receives a Block entry as a corev1.VolumeDevice.
//   - an init container or sidecar lists a Block volume under its own
//     `volumeDevices`, and may not name one under `volumeMounts`. Both are
//     checked against the volumes the component itself declares. A
//     `volumeMounts` name the component does not declare stays accepted: the
//     configmap and external-secret traits add their volumes after the
//     component is generated, so such a name cannot be judged here — and a
//     trait never adds a Block volume.

// volumeDeviceKeys is the closed key set of one `volumeDevices` entry on an
// init container or sidecar, pinned to the published schema by
// TestContainerEntrySchemaMatchesParser.
var volumeDeviceKeys = []string{"name", "devicePath"}

// parseVolumeModeValue validates an authored volumeMode. label names the
// field, e.g. `volume "data": volumeMode`.
func parseVolumeModeValue(v, label string) (corev1.PersistentVolumeMode, error) {
	mode := corev1.PersistentVolumeMode(v)
	switch mode {
	case corev1.PersistentVolumeFilesystem, corev1.PersistentVolumeBlock:
		return mode, nil
	}
	return "", errors.Errorf("%s: invalid value %q, want Filesystem or Block", label, v)
}

// checkDevicePath applies the one per-path rule ValidateVolumeDevices has for
// a devicePath beyond non-empty: no ".." element (validatePathNoBacksteps).
func checkDevicePath(p, label string) error {
	if slices.Contains(strings.Split(p, "/"), "..") {
		return errors.Errorf("%s: devicePath %q must not contain '..'", label, p)
	}
	return nil
}

// checkVolumeModePairing enforces devicePath ⇔ volumeMode: Block on one
// `volumes[].pvc` or `volumeClaimTemplates` entry, in both directions. The
// caller has already required exactly one of mountPath and devicePath.
func checkVolumeModePairing(label string, mode corev1.PersistentVolumeMode, hasMountPath bool, devicePath string) error {
	block := mode == corev1.PersistentVolumeBlock
	if block && hasMountPath {
		return errors.Errorf("%s: volumeMode Block is consumed through devicePath, not mountPath — a raw block device has no filesystem to mount. Author devicePath instead, or drop volumeMode: Block for a filesystem claim", label)
	}
	if devicePath != "" && !block {
		return errors.Errorf("%s: devicePath requires volumeMode: Block — only a raw block claim is attached as a device. Author volumeMode: Block, or use mountPath for a filesystem claim", label)
	}
	if devicePath != "" {
		return checkDevicePath(devicePath, label)
	}
	return nil
}

// parseVolumeDeviceList parses one init container's or sidecar's
// `volumeDevices` list. mounts are that same container's parsed volumeMounts:
// ValidateVolumeDevices refuses a volume name or a devicePath repeated within
// the list, a devicePath that is also a mountPath in the container, and a
// volume named in both lists.
func parseVolumeDeviceList(m map[string]any, prefix string, mounts []corev1.VolumeMount) ([]corev1.VolumeDevice, error) {
	list, present, err := parseObjectList(m, "volumeDevices")
	if err != nil {
		return nil, errors.Errorf("%s: %w", prefix, err)
	}
	if !present {
		return nil, nil
	}
	mountPaths := make(map[string]bool, len(mounts))
	mountNames := make(map[string]bool, len(mounts))
	for _, vm := range mounts {
		mountPaths[vm.MountPath] = true
		mountNames[vm.Name] = true
	}
	seen := map[string]bool{}
	seenNames := map[string]bool{}
	var out []corev1.VolumeDevice
	for i, dm := range list {
		label := fmt.Sprintf("%s: volumeDevices[%d]", prefix, i)
		if err := rejectUnknownKeys(dm, volumeDeviceKeys, label); err != nil {
			return nil, err
		}
		name, err := requiredStringField(dm, "name", label)
		if err != nil {
			return nil, err
		}
		devicePath, err := requiredStringField(dm, "devicePath", label)
		if err != nil {
			return nil, err
		}
		if err := checkDevicePath(devicePath, label); err != nil {
			return nil, err
		}
		if seenNames[name] {
			return nil, errors.Errorf("%s: volume %q is already listed under volumeDevices; a volume is attached as a device at most once per container", label, name)
		}
		seenNames[name] = true
		if seen[devicePath] {
			return nil, errors.Errorf("%s: duplicate devicePath %q", label, devicePath)
		}
		seen[devicePath] = true
		if mountPaths[devicePath] {
			return nil, errors.Errorf("%s: devicePath %q is already a mountPath in this container", label, devicePath)
		}
		if mountNames[name] {
			return nil, errors.Errorf("%s: volume %q is also listed under volumeMounts; a volume is either mounted or attached as a device, not both", label, name)
		}
		out = append(out, corev1.VolumeDevice{Name: name, DevicePath: devicePath})
	}
	return out, nil
}

// declaredVolumeModes maps every pod volume name the component declares — its
// `volumes` entries and, on statefulset, its claim templates — to whether that
// volume is Block. A name absent from the map is one the component does not
// declare (see the file comment).
func declaredVolumeModes(parsed ParsedVolumes, vcts []VolumeClaimTemplate) map[string]bool {
	modes := make(map[string]bool, len(parsed.Volumes)+len(vcts))
	for _, v := range parsed.Volumes {
		modes[v.Name] = false
	}
	for _, d := range parsed.Devices {
		modes[d.Name] = true
	}
	for _, vct := range vcts {
		modes[vct.Name] = vct.DevicePath != ""
	}
	return modes
}

// checkExtraContainerVolumeModes checks every init container's and sidecar's
// volumeMounts and volumeDevices against modes (declaredVolumeModes): a
// device must name a declared Block volume, and a mount must not name one.
func checkExtraContainerVolumeModes(modes map[string]bool, inits []InitContainerConfig, sidecars []SidecarContainerConfig) error {
	for i, ic := range inits {
		if err := checkContainerVolumeModes(modes, fmt.Sprintf("initContainers[%d] %q", i, ic.Name), ic.VolumeMounts, ic.VolumeDevices); err != nil {
			return err
		}
	}
	for i, sc := range sidecars {
		if err := checkContainerVolumeModes(modes, fmt.Sprintf("sidecars[%d] %q", i, sc.Name), sc.VolumeMounts, sc.VolumeDevices); err != nil {
			return err
		}
	}
	return nil
}

func checkContainerVolumeModes(modes map[string]bool, label string, mounts []corev1.VolumeMount, devices []corev1.VolumeDevice) error {
	for i, vm := range mounts {
		if modes[vm.Name] {
			return errors.Errorf("%s: volumeMounts[%d]: volume %q is a Block volume and has no filesystem to mount; list it under volumeDevices with a devicePath instead", label, i, vm.Name)
		}
	}
	for i, d := range devices {
		block, declared := modes[d.Name]
		if !declared {
			return errors.Errorf("%s: volumeDevices[%d]: volume %q is not declared by this component; volumeDevices takes a pvc volume or claim template authored with volumeMode: Block", label, i, d.Name)
		}
		if !block {
			return errors.Errorf("%s: volumeDevices[%d]: volume %q is not a Block volume; volumeDevices takes a pvc volume or claim template authored with volumeMode: Block", label, i, d.Name)
		}
	}
	return nil
}

// checkMainContainerVolumeDevices applies ValidateVolumeDevices' name and path
// rules to the main container when its devices and mounts come from more than
// one parser — the statefulset kind's claim templates and its `volumes`. Each
// parser checks its own entries; this catches a device name or devicePath
// repeated across the two, a device name that is also a mount name, and a
// devicePath that equals a mountPath from either.
func checkMainContainerVolumeDevices(mounts []corev1.VolumeMount, devices []corev1.VolumeDevice) error {
	mountPaths := make(map[string]bool, len(mounts))
	mountNames := make(map[string]bool, len(mounts))
	for _, vm := range mounts {
		mountPaths[vm.MountPath] = true
		mountNames[vm.Name] = true
	}
	seen := make(map[string]bool, len(devices))
	seenNames := make(map[string]bool, len(devices))
	for _, d := range devices {
		if seenNames[d.Name] {
			return errors.Errorf("volume %q is attached as a device more than once in the main container; a claim template and a volume, or two claim templates, cannot share a name", d.Name)
		}
		seenNames[d.Name] = true
		if mountNames[d.Name] {
			return errors.Errorf("volume %q is both mounted and attached as a device in the main container; a volume is either mounted or attached as a device, not both — a claim template and a volume, or two claim templates, cannot share a name", d.Name)
		}
		if seen[d.DevicePath] {
			return errors.Errorf("volume %q: duplicate devicePath %q", d.Name, d.DevicePath)
		}
		seen[d.DevicePath] = true
		if mountPaths[d.DevicePath] {
			return errors.Errorf("volume %q: devicePath %q is already a mountPath in this container", d.Name, d.DevicePath)
		}
	}
	return nil
}

// copyVolumeDevices returns a fresh slice, nil when empty so an unauthored list
// renders as absent. A VolumeDevice holds only strings, so an element copy is
// a deep copy.
func copyVolumeDevices(in []corev1.VolumeDevice) []corev1.VolumeDevice {
	if len(in) == 0 {
		return nil
	}
	return slices.Clone(in)
}
