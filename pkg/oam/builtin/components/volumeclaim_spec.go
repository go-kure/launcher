package components

import (
	"maps"
	"slices"
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

// The nested key sets each parser below accepts, declared here rather than
// inline at the rejectUnknownKeys call so the schema fragment can be pinned to
// them (TestVolumeClaimTemplateSchemaMatchesParser walks both). An inline
// literal drifts silently: a key added to the schema and not to the parser, or
// the reverse, reads as correct at both halves.
var (
	volumeClaimResourcesKeys     = []string{"requests", "limits"}
	volumeClaimStorageKeys       = []string{"storage"}
	volumeClaimSelectorKeys      = []string{"matchLabels", "matchExpressions"}
	volumeClaimSelectorExprKeys  = []string{"key", "operator", "values"}
	volumeClaimDataSourceRefKeys = []string{"apiGroup", "kind", "name", "namespace"}
)

// volumeClaimTemplateRejectedKeys names the claim-spec fields that are refused
// with an explanation rather than silently ignored.
//
// `volumeName` pre-binds a claim to one named PersistentVolume. In a *template*
// that is never what an author wants: every replica's claim would name the same
// PV, so at most one ordinal could ever bind.
//
// `dataSource` is the superseded spelling of `dataSourceRef`. When
// `dataSourceRef` sets no namespace the apiserver mirrors the two, so
// authoring both is a way to write the same thing twice and disagree with
// yourself; when it does set one the apiserver does not mirror, and
// `dataSource` must stay empty.
//
// `volumeMount` is not a claim-spec field at all; the container mount is
// authored as `mountPath` on the same entry.
var volumeClaimTemplateRejectedKeys = map[string]string{
	"volumeName":  "pre-binding a claim template to a named PersistentVolume would point every replica at the same volume; omit it and let the provisioner bind each ordinal",
	"dataSource":  "superseded by dataSourceRef, which the apiserver mirrors back into dataSource when dataSourceRef sets no namespace (and requires dataSource to stay empty when it does); author dataSourceRef instead",
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

	if raw, present, err := optionalObject(m, "selector", label+".selector"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		sel, err := parseLabelSelector(raw, label+".selector")
		if err != nil {
			return VolumeClaimSpecConfig{}, err
		}
		c.Selector = sel
	}

	if raw, present, err := optionalObject(m, "resources", label+".resources"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, volumeClaimResourcesKeys, label+".resources"); err != nil {
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
			sub, present, err := optionalObject(raw, f.key, label+".resources."+f.key)
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

	if v, present, err := optionalString(m, "volumeMode", label+".volumeMode"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		mode := corev1.PersistentVolumeMode(v)
		// Block is a valid PersistentVolumeMode that this handler cannot render.
		// Every volumeClaimTemplates entry requires a `mountPath`
		// (parseVolumeClaimTemplates) and the statefulset kind turns each one
		// into a filesystem corev1.VolumeMount unconditionally; a Block volume
		// must instead be consumed through `volumeDevices`/`devicePath`, which
		// this handler does not emit. The two objects are validated separately,
		// so nothing rejects the pair: the StatefulSet and its claims are
		// created and the pods then fail at mount time. Rejecting here reports
		// it at build time instead. Raw block support is go-kure/launcher#385.
		if mode == corev1.PersistentVolumeBlock {
			return VolumeClaimSpecConfig{}, errors.Errorf("%s.volumeMode: Block is not supported — this kind mounts every claim template at its `mountPath` as a filesystem, and a Block volume must be consumed through volumeDevices/devicePath instead. Omit volumeMode or set Filesystem", label)
		}
		if mode != corev1.PersistentVolumeFilesystem {
			return VolumeClaimSpecConfig{}, errors.Errorf("%s.volumeMode: invalid value %q, want Filesystem", label, v)
		}
		c.VolumeMode = &mode
	}

	if raw, present, err := optionalObject(m, "dataSourceRef", label+".dataSourceRef"); err != nil {
		return VolumeClaimSpecConfig{}, err
	} else if present {
		ref, err := parseDataSourceRef(raw, label+".dataSourceRef")
		if err != nil {
			return VolumeClaimSpecConfig{}, err
		}
		c.DataSourceRef = ref
	}

	if v, present, err := optionalString(m, "volumeAttributesClassName", label+".volumeAttributesClassName"); err != nil {
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

// parseStorageResourceList parses a claim's requests/limits map, accepting only
// `storage`.
//
// That is stricter than the apiserver, deliberately.
// ValidatePersistentVolumeClaimSpec requires `storage` in requests
// (k8s-validation.go's field.Required on requests[storage]) but never iterates
// the rest of the map, so an authored `cpu` reaches the API unread and is
// silently carried on the object forever. A claim measures storage and nothing
// else, so any other resource name is an authoring mistake worth reporting
// rather than a value worth carrying. This rejection is launcher's, not the
// API's.
func parseStorageResourceList(m map[string]any, label string) (corev1.ResourceList, error) {
	var rl corev1.ResourceList
	// Sorted, for the same reason the rejected-key scan in
	// parseVolumeClaimTemplates is: map iteration order is randomised, so a
	// list authoring two invalid resource names would otherwise name a
	// different one run to run. The shared rejectUnknownKeys has the same
	// defect and is deliberately left alone here — it is used by every handler
	// in the package, so changing its messages does not belong in a change
	// scoped to this kind (go-kure/launcher#386).
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := m[k]
		if !slices.Contains(volumeClaimStorageKeys, k) {
			return nil, errors.Errorf("%s: %q is not a claim resource; a PersistentVolumeClaim measures only storage. The apiserver would ignore it rather than reject it; launcher reports it because a claim carrying an unread resource request is an authoring mistake", label, k)
		}
		// Null as omission, deliberately AFTER the unknown-name check and not
		// before it: `cpu: null` is still an author naming a resource a claim
		// cannot carry, and the explanation above is more useful there than
		// silence. `storage: null` is different — the property validator reads
		// a null under an optional property as absent, so the document is
		// schema-valid and must reach the requiredness error ("missing
		// required field 'size' (or resources.requests.storage)") rather than a
		// type error rendering the nil. Same rule, and the same ordering, as
		// every other optional read on this branch.
		if isExplicitNull(v) {
			continue
		}
		s, ok := decodedQuantityString(v)
		if !ok {
			return nil, errors.Errorf("%s.%s: quantity must be a string or number, got %T", label, k, v)
		}
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return nil, errors.Errorf("%s.%s: invalid quantity %q: %w", label, k, s, err)
		}
		// Non-positive is rejected on both lists, for two different reasons.
		// On requests[storage] this mirrors the API:
		// ValidatePersistentVolumeClaimSpec runs ValidatePositiveQuantityValue
		// on it, so zero is as invalid as negative and the claim would fail at
		// admission. On limits the rejection is launcher's own —
		// ValidatePersistentVolumeClaimSpec never reads Limits at all, so the
		// apiserver would accept `limits: {storage: 0}`; launcher reports it
		// because a claim capped at zero is an authoring mistake, not a
		// document anyone means to write. The short `size` spelling gets the
		// requests rule in parseVolumeClaimTemplates.
		if q.Sign() <= 0 {
			return nil, errors.Errorf("%s.%s: quantity must be positive, got %q", label, k, s)
		}
		if rl == nil {
			rl = corev1.ResourceList{}
		}
		rl[corev1.ResourceName(k)] = q
	}
	return rl, nil
}

// checkClaimStorageLimit rejects a `resources.limits.storage` below the entry's
// effective storage request. The request is read through
// effectiveStorageRequest, so the `size` shorthand and the long
// `resources.requests.storage` spelling are treated alike — the same reason
// policy enforcement uses it rather than reading Size.
//
// The request has already been parsed as a quantity by the caller, so the
// ParseQuantity failure below is unreachable; it is reported rather than
// swallowed.
func checkClaimStorageLimit(vct VolumeClaimTemplate, label string) error {
	if vct.Spec.Resources == nil {
		return nil
	}
	limit, ok := vct.Spec.Resources.Limits[corev1.ResourceStorage]
	if !ok {
		return nil
	}
	req := vct.effectiveStorageRequest()
	if req == "" {
		return nil
	}
	request, err := resource.ParseQuantity(req)
	if err != nil {
		return errors.Errorf("%s: invalid storage request %q: %w", label, req, err)
	}
	if request.Cmp(limit) > 0 {
		return errors.Errorf("%s: resources.limits.storage (%s) is below the requested storage (%s); a claim cannot request more than its limit", label, limit.String(), request.String())
	}
	return nil
}

// parseLabelSelector parses a metav1.LabelSelector: matchLabels plus the
// matchExpressions form, with the operator's arity checked the way
// metav1validation.ValidateLabelSelector does.
func parseLabelSelector(raw map[string]any, label string) (*metav1.LabelSelector, error) {
	if err := rejectUnknownKeys(raw, volumeClaimSelectorKeys, label); err != nil {
		return nil, err
	}
	sel := &metav1.LabelSelector{}
	if m, present, err := optionalObject(raw, "matchLabels", label+".matchLabels"); err != nil {
		return nil, err
	} else if present {
		labels, err := parseLabelMap(m, label+".matchLabels")
		if err != nil {
			return nil, err
		}
		sel.MatchLabels = labels
	}
	list, present, err := optionalObjectList(raw, "matchExpressions")
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
			op, present, err := optionalString(item, "operator", itemLabel+".operator")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.operator: required", itemLabel)
			}
			values, _, err := optionalStringList(item, "values", itemLabel+".values")
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
			// produced a StatefulSet the apiserver rejects — the same class
			// parseLabelMap already closes on matchLabels (podspec.go:722-724).
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
	if len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
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
		v, present, err := optionalString(raw, f.key, label+"."+f.key)
		if err != nil {
			return nil, err
		}
		if !present || v == "" {
			return nil, errors.Errorf("%s.%s: required", label, f.key)
		}
		*f.dst = v
	}

	apiGroup, present, err := optionalString(raw, "apiGroup", label+".apiGroup")
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

	if v, present, err := optionalString(raw, "namespace", label+".namespace"); err != nil {
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
//
// Everything projected here is deep-copied out of the config, never aliased
// into the generated object. A handler config is reusable — the same one can
// render more than once — and editing a generated object in place is an
// expected use, which is exactly what the label-aliasing tests exercise
// (label_aliasing_test.go). A shared pointer or map would carry such an edit
// back into the config and into every later render. Copying is the same rule
// buildResourceRequirements already applies to the container resource maps
// (common.go:2887-2889); the pointers to scalars are copied by value for the
// same reason, since a caller can write through them just as easily.
func (c VolumeClaimSpecConfig) apply(pvc *corev1.PersistentVolumeClaim) {
	if c.Selector != nil {
		pvc.Spec.Selector = c.Selector.DeepCopy()
	}
	if c.Resources != nil {
		// The constructor already wrote requests.storage from `size`; merge
		// rather than replace so authoring only `limits` keeps it.
		if c.Resources.Requests != nil {
			if pvc.Spec.Resources.Requests == nil {
				pvc.Spec.Resources.Requests = corev1.ResourceList{}
			}
			// The destination map is built fresh per render, so this one
			// cannot alias at the map level the way Limits does; the copy
			// guards a Quantity's own internal *inf.Dec, which a caller can
			// still reach through AsDec(). Unlike the assignments around it,
			// no test can catch its removal through the rendering path — kept
			// for the same reason buildResourceRequirements copies.
			maps.Copy(pvc.Spec.Resources.Requests, c.Resources.Requests.DeepCopy())
		}
		if c.Resources.Limits != nil {
			pvc.Spec.Resources.Limits = c.Resources.Limits.DeepCopy()
		}
	}
	if c.VolumeMode != nil {
		mode := *c.VolumeMode
		pvc.Spec.VolumeMode = &mode
	}
	if c.DataSourceRef != nil {
		pvc.Spec.DataSourceRef = c.DataSourceRef.DeepCopy()
	}
	if c.VolumeAttributesClassName != nil {
		class := *c.VolumeAttributesClassName
		pvc.Spec.VolumeAttributesClassName = &class
	}
}

// schemaVolumeClaimSpec describes the claim-spec keys added here; the caller
// merges it into the volumeClaimTemplates item schema.
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
		"resources": {
			Type:        oam.PropertyTypeObject,
			Description: "Storage the claim asks for. A claim measures only `storage`; any other resource name is rejected. requests.storage is the long form of `size` — author one or the other, not both.",
			Properties: map[string]oam.PropertySchema{
				// Closed, unlike the container and pod-level resource lists
				// (schemaResources, schema.go): their keys are an open space of
				// resource names (hugepages-<size>, extended resources), while a
				// claim measures exactly one thing. An open object here would let
				// a schema consumer accept `requests: {cpu: "1"}` that
				// parseStorageResourceList then rejects.
				//
				// `storage` itself carries no declared Type, for the reason
				// schemaResources gives for cpu/memory: the parser takes either a
				// quantity string or a bare number (decodedQuantityString), and
				// PropertySchema has no string-or-number union, so declaring
				// PropertyTypeString would reject the numeric form the parser
				// accepts.
				"requests": {Type: oam.PropertyTypeObject, Description: `Minimum storage, e.g. {"storage": "10Gi"}.`, Properties: schemaClaimStorage("Storage the claim asks for. The long spelling of `size` — author one or the other, not both. Must be positive.")},
				"limits":   {Type: oam.PropertyTypeObject, Description: "Upper bound on storage; honoured only by provisioners that implement it.", Properties: schemaClaimStorage("Upper bound on the volume's size. Must be positive — launcher rejects a non-positive limit as an authoring mistake, though the apiserver never reads this field.")},
			},
		},
		"volumeMode": {Type: oam.PropertyTypeString, Enum: []any{"Filesystem"}, Description: "How the volume is consumed. Only Filesystem is accepted: this kind mounts every claim template at its `mountPath`, and the API's other mode, Block, must be consumed through volumeDevices/devicePath, which this kind does not emit (go-kure/launcher#385)."},
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

// schemaClaimStorage returns the single-key property map a claim's requests and
// limits each publish, built fresh per call so the two share no map state.
func schemaClaimStorage(desc string) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"storage": {Description: desc},
	}
}
