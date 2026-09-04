package components

import (
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// StatefulSetSpecConfig holds the StatefulSetSpec-level properties of the
// statefulset component (go-kure/launcher#339, ADR-036 L1): everything on
// appsv1.StatefulSetSpec that is neither the pod template (parsePodSpec), the
// claim templates (parseVolumeClaimTemplates), nor the fields the handler
// already owned (replicas, serviceName). Each field is nil / zero when not
// authored so apply leaves the constructor's value in place, which keeps
// existing output byte-identical.
type StatefulSetSpecConfig struct {
	PodManagementPolicy                  appsv1.PodManagementPolicyType
	UpdateStrategy                       *appsv1.StatefulSetUpdateStrategy
	RevisionHistoryLimit                 *int32
	MinReadySeconds                      *int32
	PersistentVolumeClaimRetentionPolicy *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy
	Ordinals                             *appsv1.StatefulSetOrdinals
}

// statefulSetSpecPropertyKeys lists every StatefulSetSpec-level property the
// statefulset component publishes and parses. TestStatefulSetSpecSchemaMatchesParser
// pins schemaStatefulSetSpec to this list.
var statefulSetSpecPropertyKeys = []string{
	"podManagementPolicy", "updateStrategy", "revisionHistoryLimit", "minReadySeconds",
	"persistentVolumeClaimRetentionPolicy", "ordinals",
}

// The nested key sets each parser below accepts, declared here rather than
// inline at the rejectUnknownKeys call so the schema fragment can be pinned to
// them (TestStatefulSetSpecSchemaMatchesParser walks both). An inline literal
// drifts silently: a key added to the schema and not to the parser, or the
// reverse, reads as correct at both halves.
var (
	statefulSetPVCRetentionKeys   = []string{"whenDeleted", "whenScaled"}
	statefulSetOrdinalsKeys       = []string{"start"}
	statefulSetUpdateStrategyKeys = []string{"type", "rollingUpdate"}
	statefulSetRollingUpdateKeys  = []string{"partition", "maxUnavailable"}
)

// statefulSetSpecRejectedKeys are StatefulSetSpec fields an author may not set;
// each maps to the error explaining why.
var statefulSetSpecRejectedKeys = map[string]string{
	"selector": "selector: not authorable; the StatefulSet selector is builder-managed (app: <component>), must equal the generated template labels and is immutable once created",
}

// parseStatefulSetSpec reads the StatefulSetSpec-level properties. Validation
// mirrors apps/v1 ValidateStatefulSetSpec where it is deterministic from the
// document alone: the two enums, rollingUpdate only under RollingUpdate,
// non-negative partition/minReadySeconds/ordinals.start, and a maxUnavailable
// that is a positive integer or a 1-100 percentage.
func parseStatefulSetSpec(props map[string]any) (StatefulSetSpecConfig, error) {
	var cfg StatefulSetSpecConfig

	rejected := make([]string, 0, len(statefulSetSpecRejectedKeys))
	for key := range statefulSetSpecRejectedKeys {
		rejected = append(rejected, key)
	}
	slices.Sort(rejected)
	for _, key := range rejected {
		if _, present := props[key]; present {
			return StatefulSetSpecConfig{}, errors.New(statefulSetSpecRejectedKeys[key])
		}
	}

	if v, present, err := optionalString(props, "podManagementPolicy", "podManagementPolicy"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		switch appsv1.PodManagementPolicyType(v) {
		case appsv1.OrderedReadyPodManagement, appsv1.ParallelPodManagement:
			cfg.PodManagementPolicy = appsv1.PodManagementPolicyType(v)
		default:
			return StatefulSetSpecConfig{}, errors.Errorf("podManagementPolicy: invalid value %q, must be %s or %s", v, appsv1.OrderedReadyPodManagement, appsv1.ParallelPodManagement)
		}
	}

	if raw, present, err := optionalObject(props, "updateStrategy", "updateStrategy"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		us, err := parseStatefulSetUpdateStrategy(raw)
		if err != nil {
			return StatefulSetSpecConfig{}, err
		}
		cfg.UpdateStrategy = us
	}

	if v, present, err := optionalInt32(props, "revisionHistoryLimit", "revisionHistoryLimit"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		if v < 0 {
			return StatefulSetSpecConfig{}, errors.Errorf("revisionHistoryLimit: must be >= 0, got %d", v)
		}
		cfg.RevisionHistoryLimit = &v
	}

	if v, present, err := optionalInt32(props, "minReadySeconds", "minReadySeconds"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		if v < 0 {
			return StatefulSetSpecConfig{}, errors.Errorf("minReadySeconds: must be >= 0, got %d", v)
		}
		cfg.MinReadySeconds = &v
	}

	if raw, present, err := optionalObject(props, "persistentVolumeClaimRetentionPolicy", "persistentVolumeClaimRetentionPolicy"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, statefulSetPVCRetentionKeys, "persistentVolumeClaimRetentionPolicy"); err != nil {
			return StatefulSetSpecConfig{}, err
		}
		policy := &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{}
		for _, key := range []string{"whenDeleted", "whenScaled"} {
			label := "persistentVolumeClaimRetentionPolicy." + key
			v, present, err := optionalString(raw, key, label)
			if err != nil {
				return StatefulSetSpecConfig{}, err
			}
			if !present {
				continue
			}
			switch appsv1.PersistentVolumeClaimRetentionPolicyType(v) {
			case appsv1.RetainPersistentVolumeClaimRetentionPolicyType, appsv1.DeletePersistentVolumeClaimRetentionPolicyType:
			default:
				return StatefulSetSpecConfig{}, errors.Errorf("%s: invalid value %q, must be %s or %s", label, v, appsv1.RetainPersistentVolumeClaimRetentionPolicyType, appsv1.DeletePersistentVolumeClaimRetentionPolicyType)
			}
			if key == "whenDeleted" {
				policy.WhenDeleted = appsv1.PersistentVolumeClaimRetentionPolicyType(v)
			} else {
				policy.WhenScaled = appsv1.PersistentVolumeClaimRetentionPolicyType(v)
			}
		}
		cfg.PersistentVolumeClaimRetentionPolicy = policy
	}

	if raw, present, err := optionalObject(props, "ordinals", "ordinals"); err != nil {
		return StatefulSetSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, statefulSetOrdinalsKeys, "ordinals"); err != nil {
			return StatefulSetSpecConfig{}, err
		}
		start, present, err := optionalInt32(raw, "start", "ordinals.start")
		if err != nil {
			return StatefulSetSpecConfig{}, err
		}
		if !present {
			return StatefulSetSpecConfig{}, errors.New("ordinals.start: required")
		}
		if start < 0 {
			return StatefulSetSpecConfig{}, errors.Errorf("ordinals.start: must be >= 0, got %d", start)
		}
		cfg.Ordinals = &appsv1.StatefulSetOrdinals{Start: start}
	}

	return cfg, nil
}

// parseStatefulSetUpdateStrategy decodes `updateStrategy`. `rollingUpdate` is
// only allowed under RollingUpdate.
//
// `type` is required only when it is the object's sole content: an authored
// `updateStrategy: {}` writes nothing, so its entire meaning would come from
// the apiserver's defaulting rather than from the document, and
// ValidateStatefulSetSpec rejects an empty Type outright once defaulting is
// out of the picture. When `rollingUpdate` is authored the type is inferred as
// RollingUpdate instead of demanded: that is both what the apiserver defaults
// to and the only type that reads the field at all (this parser refuses
// `rollingUpdate` under OnDelete two statements below), so requiring the
// author to restate it rejects a document upstream accepts and acts on
// exactly as written.
func parseStatefulSetUpdateStrategy(raw map[string]any) (*appsv1.StatefulSetUpdateStrategy, error) {
	if err := rejectUnknownKeys(raw, statefulSetUpdateStrategyKeys, "updateStrategy"); err != nil {
		return nil, err
	}
	typ, typeAuthored, err := optionalString(raw, "type", "updateStrategy.type")
	if err != nil {
		return nil, err
	}
	ru, ruAuthored, err := optionalObject(raw, "rollingUpdate", "updateStrategy.rollingUpdate")
	if err != nil {
		return nil, err
	}
	us := &appsv1.StatefulSetUpdateStrategy{}
	switch {
	case typeAuthored:
		switch appsv1.StatefulSetUpdateStrategyType(typ) {
		case appsv1.RollingUpdateStatefulSetStrategyType, appsv1.OnDeleteStatefulSetStrategyType:
			us.Type = appsv1.StatefulSetUpdateStrategyType(typ)
		default:
			return nil, errors.Errorf("updateStrategy.type: invalid value %q, must be %s or %s", typ, appsv1.RollingUpdateStatefulSetStrategyType, appsv1.OnDeleteStatefulSetStrategyType)
		}
	case ruAuthored:
		us.Type = appsv1.RollingUpdateStatefulSetStrategyType
	default:
		return nil, errors.New("updateStrategy.type: required")
	}

	if !ruAuthored {
		return us, nil
	}
	if us.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		return nil, errors.Errorf("updateStrategy.rollingUpdate: only allowed for updateStrategy.type %s", appsv1.RollingUpdateStatefulSetStrategyType)
	}
	if err := rejectUnknownKeys(ru, statefulSetRollingUpdateKeys, "updateStrategy.rollingUpdate"); err != nil {
		return nil, err
	}
	us.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{}
	if v, present, err := optionalInt32(ru, "partition", "updateStrategy.rollingUpdate.partition"); err != nil {
		return nil, err
	} else if present {
		if v < 0 {
			return nil, errors.Errorf("updateStrategy.rollingUpdate.partition: must be >= 0, got %d", v)
		}
		us.RollingUpdate.Partition = &v
	}
	if v, present := ru["maxUnavailable"]; present {
		mu, err := parseMaxUnavailable(v, "updateStrategy.rollingUpdate.maxUnavailable")
		if err != nil {
			return nil, err
		}
		us.RollingUpdate.MaxUnavailable = &mu
	}
	return us, nil
}

// parseMaxUnavailable accepts a positive integer or a "N%" string with
// 1 <= N <= 100, mirroring validateRollingUpdateStatefulSet (positive
// int-or-percent, not 0, not above 100%).
func parseMaxUnavailable(v any, label string) (intstr.IntOrString, error) {
	if s, ok := v.(string); ok {
		// IsValidPercent is upstream's own form check (^[0-9]+%$), used here
		// rather than a CutSuffix/Atoi pair: Atoi accepts a leading sign, so
		// "+50%" parsed cleanly and was carried through to a document
		// validateRollingUpdateStatefulSet then rejects at admission.
		if errs := validation.IsValidPercent(s); len(errs) > 0 {
			return intstr.IntOrString{}, errors.Errorf("%s: string value must be a percentage such as \"25%%\", got %q", label, s)
		}
		// The form check leaves only digits, so the sole ParseUint failure is
		// an overflowing one — which is out of range anyway.
		n, err := strconv.ParseUint(strings.TrimSuffix(s, "%"), 10, 32)
		if err != nil || n < 1 || n > 100 {
			return intstr.IntOrString{}, errors.Errorf("%s: percentage must be between 1%% and 100%%, got %q", label, s)
		}
		return intstr.FromString(s), nil
	}
	n, ok := toInt32(v)
	if !ok {
		return intstr.IntOrString{}, errors.Errorf("%s: must be a positive integer or a percentage string, got %T", label, v)
	}
	if n < 1 {
		return intstr.IntOrString{}, errors.Errorf("%s: must be >= 1, got %d", label, n)
	}
	return intstr.FromInt32(n), nil
}

// apply writes the authored StatefulSetSpec-level fields directly onto the
// StatefulSet. Unauthored fields keep whatever the constructor set
// (podManagementPolicy OrderedReady, an empty updateStrategy), so output for
// documents that author none of them does not change.
func (c StatefulSetSpecConfig) apply(sts *appsv1.StatefulSet) {
	if c.PodManagementPolicy != "" {
		sts.Spec.PodManagementPolicy = c.PodManagementPolicy
	}
	if c.UpdateStrategy != nil {
		sts.Spec.UpdateStrategy = *c.UpdateStrategy
	}
	if c.RevisionHistoryLimit != nil {
		sts.Spec.RevisionHistoryLimit = c.RevisionHistoryLimit
	}
	if c.MinReadySeconds != nil {
		sts.Spec.MinReadySeconds = *c.MinReadySeconds
	}
	if c.PersistentVolumeClaimRetentionPolicy != nil {
		sts.Spec.PersistentVolumeClaimRetentionPolicy = c.PersistentVolumeClaimRetentionPolicy
	}
	if c.Ordinals != nil {
		sts.Spec.Ordinals = c.Ordinals
	}
}

// schemaStatefulSetSpec describes the StatefulSetSpec-level properties (see
// parseStatefulSetSpec). The key set is pinned to statefulSetSpecPropertyKeys.
func schemaStatefulSetSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"podManagementPolicy": {
			Type:        oam.PropertyTypeString,
			Enum:        []any{string(appsv1.OrderedReadyPodManagement), string(appsv1.ParallelPodManagement)},
			Description: "How pods are created and deleted during scaling: OrderedReady (one at a time, the API default) or Parallel.",
		},
		"updateStrategy": {
			Type:        oam.PropertyTypeObject,
			Description: "How pods are replaced when the pod template changes. Unset leaves the API default (RollingUpdate).",
			Properties: map[string]oam.PropertySchema{
				// Not Required: the parser demands `type` only for an otherwise
				// empty `updateStrategy: {}` and infers RollingUpdate when
				// `rollingUpdate` is authored (parseStatefulSetUpdateStrategy).
				// PropertySchema has no conditional requiredness, and declaring
				// Required here would make a schema-validating consumer reject
				// `updateStrategy: {rollingUpdate: {partition: 3}}` before the
				// parser ever sees it. The parser is the enforcement point.
				"type": {
					Type:        oam.PropertyTypeString,
					Enum:        []any{string(appsv1.RollingUpdateStatefulSetStrategyType), string(appsv1.OnDeleteStatefulSetStrategyType)},
					Description: "RollingUpdate replaces pods in reverse ordinal order; OnDelete only recreates pods that are deleted manually. Required unless rollingUpdate is authored, in which case RollingUpdate is inferred; an otherwise empty updateStrategy object must name it.",
				},
				"rollingUpdate": {
					Type:        oam.PropertyTypeObject,
					Description: "RollingUpdate parameters; only allowed when type is RollingUpdate.",
					Properties: map[string]oam.PropertySchema{
						"partition": {Type: oam.PropertyTypeInteger, Description: "Ordinal below which pods are left untouched by a rolling update (canary partitioning). Must be >= 0; the API default is 0."},
						// No declared Type, mirroring schemaResources' cpu/memory
						// quantities (schema.go:148-155). PropertyType carries no
						// int-or-string member (pkg/oam/schema.go), and declaring
						// `string` here does not merely understate the accepted set —
						// validatePropertyValue rejects a non-string outright
						// (pkg/oam/property_validate.go:118-121), so `maxUnavailable: 2`
						// could never reach parseMaxUnavailable's integer branch through
						// a schema-validating consumer. Type "" skips the check
						// (property_validate.go:114-117) and leaves both forms reachable;
						// the Description carries the constraint instead. Tracked in
						// go-kure/launcher#383.
						"maxUnavailable": {Description: `Maximum pods unavailable during the update: a percentage string such as "25%" (at most 100%), or a positive integer such as 2. The API default is 1; 0 is never valid. No declared type because this schema vocabulary has no int-or-string union — an integer is accepted and carried through as an integer, not converted to a string.`},
					},
				},
			},
		},
		"revisionHistoryLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: "Number of superseded ControllerRevisions kept for rollback. Must be >= 0; the API default is 10.",
		},
		"minReadySeconds": {
			Type:        oam.PropertyTypeInteger,
			Description: "Seconds a new pod must be ready without any container crashing before it counts as available. Must be >= 0; the API default is 0.",
		},
		"persistentVolumeClaimRetentionPolicy": {
			Type:        oam.PropertyTypeObject,
			Description: "What happens to claims created from volumeClaimTemplates. A field left unset defaults to Retain in the API.",
			Properties: map[string]oam.PropertySchema{
				"whenDeleted": {Type: oam.PropertyTypeString, Enum: []any{string(appsv1.RetainPersistentVolumeClaimRetentionPolicyType), string(appsv1.DeletePersistentVolumeClaimRetentionPolicyType)}, Description: "Claim handling when the StatefulSet is deleted: Retain or Delete."},
				"whenScaled":  {Type: oam.PropertyTypeString, Enum: []any{string(appsv1.RetainPersistentVolumeClaimRetentionPolicyType), string(appsv1.DeletePersistentVolumeClaimRetentionPolicyType)}, Description: "Claim handling when the StatefulSet is scaled down: Retain or Delete."},
			},
		},
		"ordinals": {
			Type:        oam.PropertyTypeObject,
			Description: "Pod ordinal numbering.",
			Properties: map[string]oam.PropertySchema{
				"start": {Type: oam.PropertyTypeInteger, Required: true, Description: "First pod ordinal; pods are numbered start..start+replicas-1. Must be >= 0; the API default is 0."},
			},
		},
	}
}
