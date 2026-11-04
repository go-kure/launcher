package components

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// The nested form of a `volumes` entry (go-kure/launcher#1177). The flat
// types (hostPath, emptyDir, pvc, configMap, secret) read their source from
// flat keys beside name and mountPath. Every other source this package admits
// is authored as its upstream object, strictly decoded into a
// corev1.VolumeSource:
//
//	- {name: tok, type: projected, mountPath: /var/run/tok, projected: {sources: [...]}}
//
// The `type` names the source and the key of the same name holds it. configMap,
// secret and emptyDir also take the nested object, for the fields their flat
// form has no key for (items, defaultMode, optional, medium); a flat source key
// and the nested object on one entry are refused.

// nestedOnlyVolumeSources are the sources that have no flat form.
var nestedOnlyVolumeSources = []string{"csi", "downwardAPI", "ephemeral", "image", "projected"}

// flatSourceKey maps a type that has both forms to its flat source key.
var flatSourceKey = map[string]string{
	"configMap": "configMapName",
	"secret":    "secretName",
	"emptyDir":  "sizeLimit",
}

// volumeSourceKeys are the json names of the fields of corev1.VolumeSource.
// TestVolumeSourceKeys_MatchUpstream holds the list to the type.
var volumeSourceKeys = []string{
	"hostPath", "emptyDir", "gcePersistentDisk", "awsElasticBlockStore", "gitRepo",
	"secret", "nfs", "iscsi", "glusterfs", "persistentVolumeClaim", "rbd", "flexVolume",
	"cinder", "cephfs", "flocker", "downwardAPI", "fc", "azureFile", "configMap",
	"vsphereVolume", "quobyte", "azureDisk", "photonPersistentDisk", "projected",
	"portworxVolume", "scaleIO", "storageos", "csi", "ephemeral", "image",
}

// nestedCase is what volumeSourceKind returns for an entry that is read as the
// nested form. It is not a valid `type`, so it cannot collide with one.
const nestedCase = "\x00nested"

// volumeSourceKind is the switch key of parseVolumes: the entry's type, or
// nestedCase when the entry is the nested form.
func volumeSourceKind(m map[string]any, volType string) string {
	if slices.Contains(nestedOnlyVolumeSources, volType) {
		return nestedCase
	}
	if _, both := flatSourceKey[volType]; both {
		if _, nested := authoredValue(m, volType); nested {
			return nestedCase
		}
	}
	return volType
}

// refusedVolumeSource is the error for a type that is not on the allow-list. A
// type that is an upstream source gets its reason; any other is unrecognized.
func refusedVolumeSource(volName, volType string) error {
	switch {
	case volType == "persistentVolumeClaim":
		return errors.Errorf("volume %q: type %q is authored as type: pvc", volName, volType)
	case slices.Contains(volumeSourceKeys, volType):
		return errors.Errorf("volume %q: type %q is not supported: Pod Security restricted does not admit that volume source, and the controller components are what tenant workloads lower to", volName, volType)
	}
	return errors.Errorf("volume %q: unrecognized type %q", volName, volType)
}

// volumeSourceDefaultedZeros are the defaulted-zero fields of a VolumeSource
// tree rooted at the source (the `volumes[]` of a pod spec is stripped).
func volumeSourceDefaultedZeros() defaultedZeroFields {
	fields := map[string]string{}
	for path, def := range podSpecOwnDefaultedZeroFields {
		if rest, ok := strings.CutPrefix(path, "volumes[]."); ok {
			fields[rest] = def
		}
	}
	return defaultedZeroFields{api: "Kubernetes", defaulter: "API server", fields: fields}
}

// volumeSourceSelectors are the label selectors of a corev1.VolumeSource, as
// the pod kind holds them in a pod's volumes (podSpecLabelSelectors).
var volumeSourceSelectors = []string{
	"projected.sources[].clusterTrustBundle.labelSelector",
	"ephemeral.volumeClaimTemplate.spec.selector",
}

// validateVolumeSourceSelectors holds the label selectors of vs to
// validateLabelSelector (validatePodSpecLabelSelectors does the same for a pod).
func validateVolumeSourceSelectors(vs *corev1.VolumeSource) error {
	if p := vs.Projected; p != nil {
		for i, source := range p.Sources {
			if source.ClusterTrustBundle == nil {
				continue
			}
			if err := validateLabelSelector(fmt.Sprintf("projected.sources[%d].clusterTrustBundle.labelSelector", i), source.ClusterTrustBundle.LabelSelector); err != nil {
				return err
			}
		}
	}
	if e := vs.Ephemeral; e != nil && e.VolumeClaimTemplate != nil {
		return validateLabelSelector("ephemeral.volumeClaimTemplate.spec.selector", e.VolumeClaimTemplate.Spec.Selector)
	}
	return nil
}

// parseNestedVolume reads a nested-form entry into its corev1.Volume.
func parseNestedVolume(m map[string]any, volName, volType string) (corev1.Volume, error) {
	label := fmt.Sprintf("volume %q: %s", volName, volType)
	if flat, both := flatSourceKey[volType]; both {
		if _, flatAuthored := authoredValue(m, flat); flatAuthored {
			return corev1.Volume{}, errors.Errorf("%s: %s and the nested %s object are mutually exclusive; author the source in one form", label, flat, volType)
		}
	}
	// The alternate flat key is a known key here: a null one reads as absent
	// (authoredValue above), and a non-null one was refused as exclusive. So is
	// devicePath: a set one never reaches here (parseVolumes refuses it on a
	// type other than pvc), and a null one reads as absent.
	known := []string{"name", "type", "mountPath", "readOnly", volType, "devicePath"}
	if flat, both := flatSourceKey[volType]; both {
		known = append(known, flat)
	}
	if err := rejectUnknownKeys(m, volumeKeys(known...), label); err != nil {
		return corev1.Volume{}, err
	}
	raw, present := authoredValue(m, volType)
	if !present {
		return corev1.Volume{}, errors.Errorf("%s: type %s takes the source in a %s object, which is required", label, volType, volType)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return corev1.Volume{}, errors.Errorf("%s: must be an object, got %T", label, raw)
	}
	authored := map[string]any{volType: obj}
	spec, stripped, err := decodeKindSpec[corev1.VolumeSource](authored, "v1 VolumeSource")
	if err != nil {
		return corev1.Volume{}, errors.Wrap(err, label)
	}
	if err := refuseUncarriedSpecValues(stripped, spec, volumeSourceDefaultedZeros()); err != nil {
		return corev1.Volume{}, errors.Wrap(err, label)
	}
	// The label selectors a source reaches are held as pod does (podSpecLabelSelectors).
	if err := refuseUnauthoredRequired(stripped, labelSelectorRequired(volumeSourceSelectors...)); err != nil {
		return corev1.Volume{}, errors.Wrap(err, label)
	}
	if err := validateVolumeSourceSelectors(spec); err != nil {
		return corev1.Volume{}, errors.Wrap(err, label)
	}
	// The flat routes require the referenced name (configMapName, secretName);
	// the API server refuses a volume without one, so the nested form does too.
	if cm := spec.ConfigMap; cm != nil && cm.Name == "" {
		return corev1.Volume{}, errors.Errorf("%s: name is required", label)
	}
	if s := spec.Secret; s != nil && s.SecretName == "" {
		return corev1.Volume{}, errors.Errorf("%s: secretName is required", label)
	}
	if c := spec.CSI; c != nil && c.Driver == "" {
		return corev1.Volume{}, errors.Errorf("%s: driver is required", label)
	}
	if p := spec.Projected; p != nil {
		for i, source := range p.Sources {
			if cm := source.ConfigMap; cm != nil && cm.Name == "" {
				return corev1.Volume{}, errors.Errorf("%s: sources[%d].configMap: name is required", label, i)
			}
			if s := source.Secret; s != nil && s.Name == "" {
				return corev1.Volume{}, errors.Errorf("%s: sources[%d].secret: name is required", label, i)
			}
		}
	}
	if es := spec.EmptyDir; es != nil && es.SizeLimit != nil && es.SizeLimit.Sign() < 0 {
		return corev1.Volume{}, errors.Errorf("%s: sizeLimit must not be negative, got %q", label, es.SizeLimit.String())
	}
	// A Block claim is a raw device, which a filesystem mount cannot carry and
	// devicePath (pvc only) cannot name: the kubelet would refuse the pod.
	if e := spec.Ephemeral; e != nil && e.VolumeClaimTemplate != nil {
		if mode := e.VolumeClaimTemplate.Spec.VolumeMode; mode != nil && *mode == corev1.PersistentVolumeBlock {
			return corev1.Volume{}, errors.Errorf("%s: volumeClaimTemplate.spec.volumeMode: Block is not supported; the volume is mounted at mountPath as a filesystem, and devicePath is authored on a pvc volume only", label)
		}
	}
	if img := spec.Image; img != nil && img.Reference != "" {
		if err := ValidateImageRef(img.Reference); err != nil {
			return corev1.Volume{}, errors.Wrapf(err, "%s: image.reference", label)
		}
	}
	return corev1.Volume{Name: volName, VolumeSource: *spec}, nil
}
