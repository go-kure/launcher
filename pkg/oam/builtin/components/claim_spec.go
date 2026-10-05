package components

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds the corev1.PersistentVolumeClaimSpec fields a standalone
// claim authors beyond size, storageClassName, accessModes and volumeMode: the
// persistentvolumeclaim kind and its twin, the pvc trait, read them through
// ParseClaimProperties (go-kure/launcher#790).
//
// A workload's `pvc` volume does not author them, and a statefulset's
// `volumeClaimTemplates` entry has its own projection (volumeclaim_spec.go),
// whose selector and dataSourceRef parsers are reused here. `volumeName` is the
// one key the two treat differently: a template refuses it, since every replica
// would name the same volume, and a standalone claim accepts it.
//
// The long `resources` spelling of `size` stays unprojected on a standalone
// claim.

// ClaimSpecFields carries those fields, each zero when unauthored so apply
// leaves an unauthored claim's output unchanged.
type ClaimSpecFields struct {
	Selector      *metav1.LabelSelector
	DataSourceRef *corev1.TypedObjectReference
	// VolumeName pre-binds the claim to the PersistentVolume of that name.
	// No policy and no capability gates it.
	VolumeName                string
	VolumeAttributesClassName *string
}

// apply writes the authored fields onto spec. Every value is copied, never
// aliased: a config can render more than once, and an edit of a generated
// claim must not reach the config or a later render.
func (f ClaimSpecFields) apply(spec *corev1.PersistentVolumeClaimSpec) {
	if f.Selector != nil {
		spec.Selector = f.Selector.DeepCopy()
	}
	if f.DataSourceRef != nil {
		spec.DataSourceRef = f.DataSourceRef.DeepCopy()
	}
	if f.VolumeName != "" {
		spec.VolumeName = f.VolumeName
	}
	if f.VolumeAttributesClassName != nil {
		class := *f.VolumeAttributesClassName
		spec.VolumeAttributesClassName = &class
	}
}

// claimSpecPropertyKeys is the key set parseClaimSpec reads, pinned to
// schemaClaimSpec by the schema/parser parity test.
var claimSpecPropertyKeys = []string{"selector", "dataSourceRef", "volumeName", "volumeAttributesClassName"}

// claimRejectedKeys are claim-spec fields a standalone claim does not accept,
// each with the reason given to the author.
var claimRejectedKeys = map[string]string{
	"dataSource": "dataSource: not authorable — " + volumeClaimTemplateRejectedKeys["dataSource"],
}

// schemaClaimSpec describes the keys of claimSpecPropertyKeys; the kind merges
// it into its property schema, and the pvc trait clones the kind's.
func schemaClaimSpec() map[string]oam.PropertySchema {
	template := schemaVolumeClaimSpec()
	return map[string]oam.PropertySchema{
		"selector":      template["selector"],
		"dataSourceRef": template["dataSourceRef"],
		"volumeName": {
			Type:        oam.PropertyTypeString,
			Description: "Name of the PersistentVolume to bind the claim to (a DNS-1123 subdomain). The claim then binds to that volume only and is not dynamically provisioned; it stays Pending while the volume is missing, bound elsewhere, or does not satisfy the claim. No EnvironmentPolicy method and no ClusterProfile capability checks it.",
		},
		"volumeAttributesClassName": template["volumeAttributesClassName"],
	}
}

// parseClaimSpec reads the fields of claimSpecPropertyKeys from a claim's
// properties and refuses the keys of claimRejectedKeys.
func parseClaimSpec(props map[string]any) (ClaimSpecFields, error) {
	var f ClaimSpecFields
	// Sorted, so a document authoring two rejected keys names the same one on
	// every run.
	for _, key := range slices.Sorted(maps.Keys(claimRejectedKeys)) {
		if _, present := authoredValue(props, key); present {
			return ClaimSpecFields{}, errors.New(claimRejectedKeys[key])
		}
	}

	if raw, present, err := parseObjectField(props, "selector", "selector"); err != nil {
		return ClaimSpecFields{}, err
	} else if present {
		sel, err := parseLabelSelector(raw, "selector")
		if err != nil {
			return ClaimSpecFields{}, err
		}
		f.Selector = sel
	}

	if raw, present, err := parseObjectField(props, "dataSourceRef", "dataSourceRef"); err != nil {
		return ClaimSpecFields{}, err
	} else if present {
		ref, err := parseDataSourceRef(raw, "dataSourceRef")
		if err != nil {
			return ClaimSpecFields{}, err
		}
		f.DataSourceRef = ref
	}

	if v, present, err := parseStringField(props, "volumeName", "volumeName"); err != nil {
		return ClaimSpecFields{}, err
	} else if present {
		// Stricter than the apiserver, deliberately:
		// ValidatePersistentVolumeClaimSpec does not read volumeName, so any
		// string is admitted. A PersistentVolume's name is a DNS-1123 subdomain
		// (ValidatePersistentVolumeName), so a value that is not one names no
		// volume and the claim would stay Pending for good. This rejection is
		// launcher's, not the API's.
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return ClaimSpecFields{}, errors.Errorf("volumeName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		f.VolumeName = v
	}

	if v, present, err := parseStringField(props, "volumeAttributesClassName", "volumeAttributesClassName"); err != nil {
		return ClaimSpecFields{}, err
	} else if present {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return ClaimSpecFields{}, errors.Errorf("volumeAttributesClassName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		f.VolumeAttributesClassName = &v
	}

	return f, nil
}
