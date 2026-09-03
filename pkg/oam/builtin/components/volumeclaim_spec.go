package components

import (
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file completes the corev1.PersistentVolumeClaimSpec projection behind
// the statefulset kind's `volumeClaimTemplates` property.
//
// The spec has nine fields. Three were already reachable — `accessModes` and
// `storageClass` directly, and `size` as a shorthand for
// resources.requests.storage. Five more are added here: `selector`,
// `resources`, `volumeMode`, `dataSourceRef` and `volumeAttributesClassName`.
// The ninth, `volumeName`, is rejected: see volumeClaimTemplateRejectedKeys.
//
// `mountPath` is not part of the claim spec at all — it is this repo's own key,
// driving the container VolumeMount that pairs with the claim.

// volumeClaimTemplatePropertyKeys is the accepted key set of one
// `volumeClaimTemplates` entry, pinned by the schema/parser parity test.
var volumeClaimTemplatePropertyKeys = []string{
	"name", "size", "mountPath", "storageClass", "accessModes",
	"selector", "resources", "volumeMode", "dataSourceRef", "volumeAttributesClassName",
}

// volumeClaimTemplateRejectedKeys names the claim-spec fields that are refused
// with an explanation rather than silently ignored.
//
// `volumeName` pre-binds a claim to one named PersistentVolume. In a *template*
// that is never what an author wants: every replica's claim would name the same
// PV, so at most one ordinal could ever bind.
//
// `dataSource` is the superseded spelling of `dataSourceRef`. The apiserver
// mirrors the two whenever the reference is one it understands, so authoring
// both is a way to write the same thing twice and disagree with yourself.
var volumeClaimTemplateRejectedKeys = map[string]string{
	"volumeName":  "pre-binding a claim template to a named PersistentVolume would point every replica at the same volume; omit it and let the provisioner bind each ordinal",
	"dataSource":  "superseded by dataSourceRef, which the apiserver mirrors back into dataSource; author dataSourceRef instead",
	"volumeMount": "not a claim-spec field; use mountPath",
}

// VolumeClaimSpecConfig carries the claim-spec fields projected here, each nil
// when unauthored so apply cannot move an unauthored claim's output.
type VolumeClaimSpecConfig struct {
	Selector                  *metav1.LabelSelector
	Resources                 *corev1.VolumeResourceRequirements
	VolumeMode                *corev1.PersistentVolumeMode
	DataSourceRef             *corev1.TypedObjectReference
	VolumeAttributesClassName *string
}

// parseVolumeClaimSpec reads the claim-spec fields of one volumeClaimTemplates
// entry. sizeAuthored says whether the entry also carries the `size` shorthand,
// so the two spellings of resources.requests.storage can be caught disagreeing.
func parseVolumeClaimSpec(m map[string]any, label string, sizeAuthored bool) (VolumeClaimSpecConfig, error) {
	var c VolumeClaimSpecConfig

	if raw, present, err := parseObjectField(m, "selector", label+".selector"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		sel, err := parseLabelSelector(raw, label+".selector")
		if err != nil {
			return VolumeClaimSpecConfig{}, err
		}
		c.Selector = sel
	}

	if raw, present, err := parseObjectField(m, "resources", label+".resources"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, []string{"requests", "limits"}, label+".resources"); err != nil {
			return VolumeClaimSpecConfig{}, err
		}
		rr := &corev1.VolumeResourceRequirements{}
		for _, f := range []struct {
			key string
			dst *corev1.ResourceList
		}{
			{"requests", &rr.Requests},
			{"limits", &rr.Limits},
		} {
			sub, present, err := parseObjectField(raw, f.key, label+".resources."+f.key)
			if err != nil {
				return VolumeClaimSpecConfig{}, err
			}
			if !present {
				continue
			}
			rl, err := parseStorageResourceList(sub, label+".resources."+f.key)
			if err != nil {
				return VolumeClaimSpecConfig{}, err
			}
			*f.dst = rl
		}
		// `size` and resources.requests.storage are two spellings of one field.
		// Accepting both would make the entry's meaning depend on which one the
		// applier happens to write last.
		if sizeAuthored {
			if _, ok := rr.Requests[corev1.ResourceStorage]; ok {
				return VolumeClaimSpecConfig{}, errors.Errorf("%s: size and resources.requests.storage are the same field; author one of them", label)
			}
		}
		c.Resources = rr
	}

	if v, present, err := parseStringField(m, "volumeMode", label+".volumeMode"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		mode := corev1.PersistentVolumeMode(v)
		if mode != corev1.PersistentVolumeFilesystem && mode != corev1.PersistentVolumeBlock {
			return VolumeClaimSpecConfig{}, errors.Errorf("%s.volumeMode: invalid value %q, want Filesystem or Block", label, v)
		}
		c.VolumeMode = &mode
	}

	if raw, present, err := parseObjectField(m, "dataSourceRef", label+".dataSourceRef"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		ref, err := parseDataSourceRef(raw, label+".dataSourceRef")
		if err != nil {
			return VolumeClaimSpecConfig{}, err
		}
		c.DataSourceRef = ref
	}

	if v, present, err := parseStringField(m, "volumeAttributesClassName", label+".volumeAttributesClassName"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return VolumeClaimSpecConfig{}, errors.Errorf("%s.volumeAttributesClassName: invalid name %q: %s", label, v, strings.Join(errs, "; "))
		}
		name := v
		c.VolumeAttributesClassName = &name
	}

	return c, nil
}

// parseStorageResourceList parses a claim's requests/limits map. A claim
// measures exactly one resource: ValidatePersistentVolumeClaimSpec requires
// `storage` in requests and rejects any other standard resource name, so an
// entry such as `cpu` is a mistake to report rather than a value to carry.
func parseStorageResourceList(m map[string]any, label string) (corev1.ResourceList, error) {
	var rl corev1.ResourceList
	for k, v := range m {
		if corev1.ResourceName(k) != corev1.ResourceStorage {
			return nil, errors.Errorf("%s: %q is not a claim resource; a PersistentVolumeClaim measures only storage", label, k)
		}
		s, ok := decodedQuantityString(v)
		if !ok {
			return nil, errors.Errorf("%s.%s: quantity must be a string or number, got %T", label, k, v)
		}
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return nil, errors.Errorf("%s.%s: invalid quantity %q: %w", label, k, s, err)
		}
		if q.Sign() < 0 {
			return nil, errors.Errorf("%s.%s: quantity must not be negative, got %q", label, k, s)
		}
		if rl == nil {
			rl = corev1.ResourceList{}
		}
		rl[corev1.ResourceName(k)] = q
	}
	return rl, nil
}

// parseLabelSelector parses a metav1.LabelSelector: matchLabels plus the
// matchExpressions form, with the operator's arity checked the way
// metav1validation.ValidateLabelSelector does.
func parseLabelSelector(raw map[string]any, label string) (*metav1.LabelSelector, error) {
	if err := rejectUnknownKeys(raw, []string{"matchLabels", "matchExpressions"}, label); err != nil {
		return nil, err
	}
	sel := &metav1.LabelSelector{}
	if m, present, err := parseObjectField(raw, "matchLabels", label+".matchLabels"); err != nil {
		return nil, err
	} else if present {
		labels, err := parseLabelMap(m, label+".matchLabels")
		if err != nil {
			return nil, err
		}
		sel.MatchLabels = labels
	}
	list, present, err := parseObjectList(raw, "matchExpressions")
	if err != nil {
		return nil, err
	}
	if present {
		for i, item := range list {
			itemLabel := indexedLabel(label+".matchExpressions", i)
			if err := rejectUnknownKeys(item, []string{"key", "operator", "values"}, itemLabel); err != nil {
				return nil, err
			}
			key, err := requireQualifiedName(item, "key", itemLabel)
			if err != nil {
				return nil, err
			}
			op, present, err := parseStringField(item, "operator", itemLabel+".operator")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.operator: required", itemLabel)
			}
			values, _, err := parseStringList(item, "values", itemLabel+".values")
			if err != nil {
				return nil, err
			}
			switch metav1.LabelSelectorOperator(op) {
			case metav1.LabelSelectorOpIn, metav1.LabelSelectorOpNotIn:
				if len(values) == 0 {
					return nil, errors.Errorf("%s.values: at least one value is required for operator %s", itemLabel, op)
				}
			case metav1.LabelSelectorOpExists, metav1.LabelSelectorOpDoesNotExist:
				if len(values) > 0 {
					return nil, errors.Errorf("%s.values: must be empty for operator %s", itemLabel, op)
				}
			default:
				return nil, errors.Errorf("%s.operator: invalid value %q, want In, NotIn, Exists or DoesNotExist", itemLabel, op)
			}
			sel.MatchExpressions = append(sel.MatchExpressions, metav1.LabelSelectorRequirement{
				Key:      key,
				Operator: metav1.LabelSelectorOperator(op),
				Values:   values,
			})
		}
	}
	if sel.MatchLabels == nil && sel.MatchExpressions == nil {
		return nil, errors.Errorf("%s: at least one of matchLabels or matchExpressions is required", label)
	}
	return sel, nil
}

// parseDataSourceRef parses a corev1.TypedObjectReference, mirroring
// validateDataSourceRef exactly.
//
// `kind` and `name` are required but carry no format rule upstream — a Kind is
// a CamelCase identifier, not a DNS name — so they are only checked non-empty.
// `apiGroup` must be a DNS-1123 subdomain when non-empty, and its absence pins
// `kind` to PersistentVolumeClaim: the core group holds no other populator.
// `namespace` is a DNS-1123 *label* (ValidateNamespaceName), not a subdomain,
// and is honoured only on a cluster with cross-namespace volume data sources
// enabled and a matching ReferenceGrant; without one the claim stays pending
// rather than failing, which the schema description states.
func parseDataSourceRef(raw map[string]any, label string) (*corev1.TypedObjectReference, error) {
	if err := rejectUnknownKeys(raw, []string{"apiGroup", "kind", "name", "namespace"}, label); err != nil {
		return nil, err
	}
	ref := &corev1.TypedObjectReference{}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"kind", &ref.Kind},
		{"name", &ref.Name},
	} {
		v, present, err := parseStringField(raw, f.key, label+"."+f.key)
		if err != nil {
			return nil, err
		}
		if !present || v == "" {
			return nil, errors.Errorf("%s.%s: required", label, f.key)
		}
		*f.dst = v
	}

	apiGroup, present, err := parseStringField(raw, "apiGroup", label+".apiGroup")
	if err != nil {
		return nil, err
	}
	if present {
		if apiGroup != "" {
			if errs := validation.IsDNS1123Subdomain(apiGroup); len(errs) > 0 {
				return nil, errors.Errorf("%s.apiGroup: invalid group %q: %s", label, apiGroup, strings.Join(errs, "; "))
			}
		}
		g := apiGroup
		ref.APIGroup = &g
	}
	if apiGroup == "" && ref.Kind != "PersistentVolumeClaim" {
		return nil, errors.Errorf("%s: kind must be PersistentVolumeClaim when apiGroup names the core group, got %q", label, ref.Kind)
	}

	if v, present, err := parseStringField(raw, "namespace", label+".namespace"); err != nil {
		return nil, err
	} else if present {
		if errs := validation.IsDNS1123Label(v); len(errs) > 0 {
			return nil, errors.Errorf("%s.namespace: invalid name %q: %s", label, v, strings.Join(errs, "; "))
		}
		ns := v
		ref.Namespace = &ns
	}
	return ref, nil
}

// apply writes the projected fields onto a claim template kure's
// CreateVolumeClaimTemplate has already built. Every field is written only when
// authored, so an entry using none of them is byte-identical to before.
func (c VolumeClaimSpecConfig) apply(pvc *corev1.PersistentVolumeClaim) {
	if c.Selector != nil {
		pvc.Spec.Selector = c.Selector
	}
	if c.Resources != nil {
		// The constructor already wrote requests.storage from `size`; merge
		// rather than replace so authoring only `limits` keeps it.
		if c.Resources.Requests != nil {
			if pvc.Spec.Resources.Requests == nil {
				pvc.Spec.Resources.Requests = corev1.ResourceList{}
			}
			maps.Copy(pvc.Spec.Resources.Requests, c.Resources.Requests)
		}
		if c.Resources.Limits != nil {
			pvc.Spec.Resources.Limits = c.Resources.Limits
		}
	}
	if c.VolumeMode != nil {
		pvc.Spec.VolumeMode = c.VolumeMode
	}
	if c.DataSourceRef != nil {
		pvc.Spec.DataSourceRef = c.DataSourceRef
	}
	if c.VolumeAttributesClassName != nil {
		pvc.Spec.VolumeAttributesClassName = c.VolumeAttributesClassName
	}
}

// schemaVolumeClaimSpec describes the claim-spec keys added here; the caller
// merges it into the volumeClaimTemplates item schema.
func schemaVolumeClaimSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"selector": {
			Type:        oam.PropertyTypeObject,
			Description: "Label query over the PersistentVolumes eligible to back this claim. Narrows binding to pre-provisioned volumes; it has no effect on a dynamically provisioned one.",
			Properties: map[string]oam.PropertySchema{
				"matchLabels": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Labels a volume must carry, all of them."},
				"matchExpressions": {
					Type:        oam.PropertyTypeArray,
					Description: "Label requirements combined with matchLabels.",
					Items: &oam.PropertySchema{
						Type:        oam.PropertyTypeObject,
						Description: "One label requirement.",
						Properties: map[string]oam.PropertySchema{
							"key":      {Type: oam.PropertyTypeString, Required: true, Description: "Label key the requirement applies to."},
							"operator": {Type: oam.PropertyTypeString, Required: true, Enum: []any{"In", "NotIn", "Exists", "DoesNotExist"}, Description: "How key relates to values. In/NotIn need at least one value; Exists/DoesNotExist need none."},
							"values":   {Type: oam.PropertyTypeArray, Description: "Values the operator compares against.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single label value."}},
						},
					},
				},
			},
		},
		"resources": {
			Type:        oam.PropertyTypeObject,
			Description: "Storage the claim asks for. A claim measures only `storage`; any other resource name is rejected. requests.storage is the long form of `size` — author one or the other, not both.",
			Properties: map[string]oam.PropertySchema{
				"requests": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: `Minimum storage, e.g. {"storage": "10Gi"}.`},
				"limits":   {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Upper bound on storage; honoured only by provisioners that implement it."},
			},
		},
		"volumeMode": {Type: oam.PropertyTypeString, Enum: []any{"Filesystem", "Block"}, Description: "Whether the volume is mounted as a filesystem (the API default) or handed to the container as a raw block device."},
		"dataSourceRef": {
			Type:        oam.PropertyTypeObject,
			Description: "Object to populate the volume from — a VolumeSnapshot, another PVC, or a custom populator. The apiserver mirrors this into the superseded `dataSource` field, which is why that one is not authorable here.",
			Properties: map[string]oam.PropertySchema{
				"apiGroup":  {Type: oam.PropertyTypeString, Description: "API group of the referent, a DNS-1123 subdomain. Omitted or empty names the core group, which pins kind to PersistentVolumeClaim — the core group holds no other populator."},
				"kind":      {Type: oam.PropertyTypeString, Required: true, Description: "Kind of the referent, e.g. VolumeSnapshot. A Kind is a CamelCase identifier, so upstream applies no format rule beyond non-empty; only the core-group pairing above constrains it."},
				"name":      {Type: oam.PropertyTypeString, Required: true, Description: "Name of the referent. Required non-empty; upstream applies no format rule."},
				"namespace": {Type: oam.PropertyTypeString, Description: "Namespace of the referent. Honoured only where cross-namespace data sources are enabled and a ReferenceGrant permits the reference; otherwise the claim stays pending rather than failing."},
			},
		},
		"volumeAttributesClassName": {Type: oam.PropertyTypeString, Description: "VolumeAttributesClass applied to the volume, carrying provisioner-specific mutable attributes. Requires the cluster to have that feature enabled."},
	}
}
