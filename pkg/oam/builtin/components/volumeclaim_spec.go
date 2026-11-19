package components

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds the claim-spec readers that the persistentvolumeclaim kind
// and its twin, the pvc trait, share (claim_spec.go): the label selector and
// dataSourceRef parsers, the key sets they accept, and the schema of the
// claim-spec keys. The statefulset kind no longer reads them: it is a raw kind
// and its `volumeClaimTemplates` are raw PersistentVolumeClaims.

// The nested key sets each parser below accepts, declared here rather than
// inline at the rejectUnknownKeys call so the schema fragment can be pinned to
// them (TestClaimSpecSchemaMatchesParser walks both). An inline literal drifts
// silently: a key added to the schema and not to the parser, or the reverse,
// reads as correct at both halves.
var (
	volumeClaimSelectorKeys      = []string{"matchLabels", "matchExpressions"}
	volumeClaimSelectorExprKeys  = []string{"key", "operator", "values"}
	volumeClaimDataSourceRefKeys = []string{"apiGroup", "kind", "name", "namespace"}
)

// volumeClaimTemplateRejectedKeys names the claim-spec fields that are refused
// with an explanation rather than silently ignored.
//
// `dataSource` is the superseded spelling of `dataSourceRef`. When
// `dataSourceRef` sets no namespace the apiserver mirrors the two, so
// authoring both is a way to write the same thing twice and disagree with
// yourself; when it does set one the apiserver does not mirror, and
// `dataSource` must stay empty.
var volumeClaimTemplateRejectedKeys = map[string]string{
	"dataSource": "superseded by dataSourceRef, which the apiserver mirrors back into dataSource when dataSourceRef sets no namespace (and requires dataSource to stay empty when it does); author dataSourceRef instead",
}

// parseLabelSelector parses a metav1.LabelSelector for a volume claim, where an
// entirely empty selector is refused (see parseLabelSelectorOpts).
func parseLabelSelector(raw map[string]any, label string) (*metav1.LabelSelector, error) {
	return parseLabelSelectorOpts(raw, label, true)
}

// parseLabelSelectorOpts parses a metav1.LabelSelector: matchLabels plus the
// matchExpressions form, with the operator's arity checked the way
// metav1validation.ValidateLabelSelector does.
//
// rejectEmpty says whether an entirely empty selector is an error. It is for a
// volume claim, where match-everything is never what the author meant (see the
// guard at the end of this function). It is NOT for a PodAffinityTerm or a
// TopologySpreadConstraint, where `labelSelector: {}` is a meaningful and
// commonly authored value — upstream reads it as matching every pod in scope
// (k8s.io/api core/v1 PodAffinityTerm.LabelSelector: "If it's null, this
// PodAffinityTerm matches with no Pods", i.e. empty and null differ), so
// refusing it there would reject documents the apiserver accepts and would make
// the emitted document unable to express a real upstream shape.
func parseLabelSelectorOpts(raw map[string]any, label string, rejectEmpty bool) (*metav1.LabelSelector, error) {
	if err := rejectUnknownKeys(raw, volumeClaimSelectorKeys, label); err != nil {
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
			if err := rejectUnknownKeys(item, volumeClaimSelectorExprKeys, itemLabel); err != nil {
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
			// ValidateLabelSelectorRequirement runs IsValidLabelValue over every
			// value unless AllowInvalidLabelValueInSelector is set
			// (apimachinery pkg/apis/meta/v1/validation/validation.go:93-98), and
			// a newly created claim template never sets it
			// (ValidationOptionsForPersistentVolumeClaimTemplate: false, threaded
			// into ValidateLabelSelector by ValidatePersistentVolumeClaimSpec).
			// Without this check a value such as "bad value" parsed cleanly and
			// produced a claim the apiserver rejects — the same class
			// parseLabelMap already closes on matchLabels (podspec.go:723-725).
			for i, v := range values {
				if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
					return nil, errors.Errorf("%s: invalid label value %q: %s", indexedLabel(itemLabel+".values", i), v, strings.Join(errs, "; "))
				}
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
	// Stricter than the apiserver, deliberately: ValidateLabelSelector accepts
	// an empty selector and treats it as matching everything, which is
	// indistinguishable from omitting the key. An author who wrote `selector`
	// meant to narrow binding, so an empty one is reported rather than dropped.
	// This rejection is launcher's, not the API's.
	// len, not nil: parseLabelMap returns a non-nil empty map for
	// `matchLabels: {}`, and an empty `matchExpressions: []` list parses to a
	// nil slice, so a nil test let `selector: {matchLabels: {}}` through as the
	// match-everything selector this guard exists to refuse. Upstream's own
	// LabelSelectorAsSelector reads len(MatchLabels)+len(MatchExpressions)==0
	// and returns labels.Everything() (apimachinery meta/v1/helpers.go).
	if rejectEmpty && len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		return nil, errors.Errorf("%s: empty selector — set matchLabels or matchExpressions, or omit the key. The apiserver would accept an empty selector as matching every volume; launcher reports it because it is never what an author who wrote `selector` meant", label)
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
//
// The `namespace` rule matches upstream even for an explicit `namespace: ""`,
// which upstream skips because it validates only a non-empty namespace: this
// package's parseStringField collapses an empty string to "absent" for every
// string field, so the empty case never reaches IsDNS1123Label here either.
// The two agree without a special case.
func parseDataSourceRef(raw map[string]any, label string) (*corev1.TypedObjectReference, error) {
	if err := rejectUnknownKeys(raw, volumeClaimDataSourceRefKeys, label); err != nil {
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

// schemaVolumeClaimSpec describes the claim-spec keys schemaClaimSpec
// (claim_spec.go) takes: selector, dataSourceRef and volumeAttributesClassName.
func schemaVolumeClaimSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"selector": {
			Type:        oam.PropertyTypeObject,
			Description: "Label query over the PersistentVolumes eligible to back this claim. Authoring it opts the claim out of dynamic provisioning entirely: a claim with a non-empty selector is never provisioned from its StorageClass and stays Pending until a pre-provisioned PV matches. Use it only when binding to volumes that already exist.",
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
		"dataSourceRef": {
			Type:        oam.PropertyTypeObject,
			Description: "Object to populate the volume from — a VolumeSnapshot, another PVC, or a custom populator. When no `namespace` is set the apiserver mirrors this into the superseded `dataSource` field, which is why that one is not authorable here; when a `namespace` is set it does not mirror, and `dataSource` must stay empty.",
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
