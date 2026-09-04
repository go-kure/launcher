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

// DaemonSetSpecConfig holds the DaemonSetSpec-level properties of the daemonset
// component (go-kure/launcher#340, ADR-036 L1). appsv1.DaemonSetSpec has five
// fields; Selector is builder-managed and Template is the pod projection
// (parsePodSpec), which leaves the three here. Each is nil when not authored so
// apply leaves the constructor's value in place, keeping existing output
// byte-identical.
type DaemonSetSpecConfig struct {
	UpdateStrategy       *appsv1.DaemonSetUpdateStrategy
	MinReadySeconds      *int32
	RevisionHistoryLimit *int32
}

// daemonSetSpecPropertyKeys lists every DaemonSetSpec-level property the
// daemonset component publishes and parses. TestDaemonSetSpecSchemaMatchesParser
// pins schemaDaemonSetSpec to this list.
var daemonSetSpecPropertyKeys = []string{
	"updateStrategy", "minReadySeconds", "revisionHistoryLimit",
}

// The nested key sets each parser below accepts, declared here rather than
// inline at the rejectUnknownKeys call so the schema fragment can be pinned to
// them (TestDaemonSetSpecSchemaMatchesParser walks both). An inline literal
// drifts silently: a key added to the schema and not to the parser, or the
// reverse, reads as correct at both halves.
var (
	daemonSetUpdateStrategyKeys = []string{"type", "rollingUpdate"}
	daemonSetRollingUpdateKeys  = []string{"maxUnavailable", "maxSurge"}
)

// daemonSetSpecRejectedKeys are DaemonSetSpec fields an author may not set;
// each maps to the error explaining why.
var daemonSetSpecRejectedKeys = map[string]string{
	"selector": "selector: not authorable; the DaemonSet selector is builder-managed (app: <component>), must equal the generated template labels and is immutable once created",
}

// parseDaemonSetSpec reads the DaemonSetSpec-level properties. Validation
// mirrors apps/v1 ValidateDaemonSetSpec where it is deterministic from the
// document alone: the updateStrategy enum, non-negative minReadySeconds and
// revisionHistoryLimit, and the int-or-percent rules on the two rolling-update
// knobs.
//
// It is deliberately STRICTER than upstream in exactly two places. Upstream
// accepts both shapes; they differ in what it then does with them:
//
//   - updateStrategy.type is required. Upstream defaults it to RollingUpdate
//     and acts on that, so `updateStrategy: {}` is a legal document there whose
//     entire meaning comes from defaulting rather than from anything written.
//   - updateStrategy.rollingUpdate is refused under type: OnDelete.
//     ValidateDaemonSetUpdateStrategy's OnDelete branch is empty, so upstream
//     accepts the field and never reads it — the silently-ignored knob this
//     projection exists to remove.
//
// Both are additive: `updateStrategy` is a new property, so no document that
// built before this change can carry either shape.
func parseDaemonSetSpec(props map[string]any) (DaemonSetSpecConfig, error) {
	var cfg DaemonSetSpecConfig

	rejected := make([]string, 0, len(daemonSetSpecRejectedKeys))
	for key := range daemonSetSpecRejectedKeys {
		rejected = append(rejected, key)
	}
	slices.Sort(rejected)
	for _, key := range rejected {
		if _, present := props[key]; present {
			return DaemonSetSpecConfig{}, errors.New(daemonSetSpecRejectedKeys[key])
		}
	}

	if raw, present, err := parseObjectField(props, "updateStrategy", "updateStrategy"); err != nil {
		return DaemonSetSpecConfig{}, err
	} else if present {
		us, err := parseDaemonSetUpdateStrategy(raw)
		if err != nil {
			return DaemonSetSpecConfig{}, err
		}
		cfg.UpdateStrategy = us
	}

	if v, present, err := parseInt32Field(props, "minReadySeconds", "minReadySeconds"); err != nil {
		return DaemonSetSpecConfig{}, err
	} else if present {
		if v < 0 {
			return DaemonSetSpecConfig{}, errors.Errorf("minReadySeconds: must be >= 0, got %d", v)
		}
		cfg.MinReadySeconds = &v
	}

	if v, present, err := parseInt32Field(props, "revisionHistoryLimit", "revisionHistoryLimit"); err != nil {
		return DaemonSetSpecConfig{}, err
	} else if present {
		if v < 0 {
			return DaemonSetSpecConfig{}, errors.Errorf("revisionHistoryLimit: must be >= 0, got %d", v)
		}
		cfg.RevisionHistoryLimit = &v
	}

	return cfg, nil
}

// parseDaemonSetUpdateStrategy decodes `updateStrategy`, applying the two
// deliberate strictnesses documented on parseDaemonSetSpec: `type` is required,
// and `rollingUpdate` is refused under OnDelete rather than accepted and
// ignored the way ValidateDaemonSetUpdateStrategy's empty OnDelete branch does.
//
// In the other direction it is deliberately LAXER in one place:
// ValidateDaemonSetUpdateStrategy requires a non-nil rollingUpdate under
// RollingUpdate, but that requirement is satisfied by apiserver defaulting
// rather than by the author, so `type: RollingUpdate` alone is accepted here —
// the same reasoning the statefulset kind applies to its own optional
// rollingUpdate.
func parseDaemonSetUpdateStrategy(raw map[string]any) (*appsv1.DaemonSetUpdateStrategy, error) {
	if err := rejectUnknownKeys(raw, daemonSetUpdateStrategyKeys, "updateStrategy"); err != nil {
		return nil, err
	}
	typ, present, err := parseStringField(raw, "type", "updateStrategy.type")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("updateStrategy.type: required")
	}
	us := &appsv1.DaemonSetUpdateStrategy{}
	switch appsv1.DaemonSetUpdateStrategyType(typ) {
	case appsv1.RollingUpdateDaemonSetStrategyType, appsv1.OnDeleteDaemonSetStrategyType:
		us.Type = appsv1.DaemonSetUpdateStrategyType(typ)
	default:
		return nil, errors.Errorf("updateStrategy.type: invalid value %q, must be %s or %s", typ, appsv1.RollingUpdateDaemonSetStrategyType, appsv1.OnDeleteDaemonSetStrategyType)
	}

	ru, present, err := parseObjectField(raw, "rollingUpdate", "updateStrategy.rollingUpdate")
	if err != nil {
		return nil, err
	}
	if !present {
		return us, nil
	}
	if us.Type != appsv1.RollingUpdateDaemonSetStrategyType {
		return nil, errors.Errorf("updateStrategy.rollingUpdate: only allowed for updateStrategy.type %s", appsv1.RollingUpdateDaemonSetStrategyType)
	}
	if err := rejectUnknownKeys(ru, daemonSetRollingUpdateKeys, "updateStrategy.rollingUpdate"); err != nil {
		return nil, err
	}

	us.RollingUpdate = &appsv1.RollingUpdateDaemonSet{}
	maxUnavailable, unavailableAuthored, err := parseDaemonSetIntOrPercent(ru, "maxUnavailable", "updateStrategy.rollingUpdate.maxUnavailable")
	if err != nil {
		return nil, err
	}
	if unavailableAuthored {
		us.RollingUpdate.MaxUnavailable = &maxUnavailable
	}
	maxSurge, surgeAuthored, err := parseDaemonSetIntOrPercent(ru, "maxSurge", "updateStrategy.rollingUpdate.maxSurge")
	if err != nil {
		return nil, err
	}
	if surgeAuthored {
		us.RollingUpdate.MaxSurge = &maxSurge
	}

	// ValidateRollingUpdateDaemonSet requires exactly one of the pair to be
	// non-zero. Both fields are pointers the apiserver defaults when the
	// document omits them — maxUnavailable to 1, maxSurge to 0
	// (k8s.io/api/apps/v1/types.go, the RollingUpdateDaemonSet field docs:
	// "Default value is 1." and "Default value is 0."). Those defaults make
	// the rule decidable from the document in every case, not only when both
	// fields are authored: an omitted maxUnavailable counts as non-zero and an
	// omitted maxSurge counts as zero. Checking the effective pair turns a
	// rejection the apiserver would issue at apply time into one the author
	// sees at build time — `maxSurge: 5` alone reads as valid until you notice
	// maxUnavailable defaulted to 1 behind it.
	unavailableZero := unavailableAuthored && isZeroIntOrPercent(maxUnavailable)
	surgeZero := !surgeAuthored || isZeroIntOrPercent(maxSurge)
	switch {
	case !unavailableZero && !surgeZero:
		return nil, errors.Errorf("updateStrategy.rollingUpdate.maxSurge: may not be non-zero while maxUnavailable is non-zero (%s); exactly one of the two carries the update", describeRollingUpdateKnob(maxUnavailable, unavailableAuthored, "1"))
	case unavailableZero && surgeZero:
		return nil, errors.Errorf("updateStrategy.rollingUpdate.maxUnavailable: cannot be 0 while maxSurge is 0 (%s); the update would never make progress", describeRollingUpdateKnob(maxSurge, surgeAuthored, "0"))
	}

	return us, nil
}

// parseDaemonSetIntOrPercent reads one of the two rolling-update knobs,
// mirroring ValidatePositiveIntOrPercent plus IsNotMoreThan100Percent: a
// non-negative integer, or a "N%" string with N <= 100. Zero is valid for
// either field on its own — unlike the statefulset kind, where maxUnavailable
// is explicitly "cannot be 0"; here it is the pair that must not both be zero,
// which the caller checks.
func parseDaemonSetIntOrPercent(raw map[string]any, key, label string) (intstr.IntOrString, bool, error) {
	v, present := raw[key]
	if !present {
		return intstr.IntOrString{}, false, nil
	}
	if s, ok := v.(string); ok {
		// IsValidPercent is upstream's own form check (^[0-9]+%$), used here
		// rather than a CutSuffix/Atoi pair: Atoi accepts a leading sign, so
		// "+50%" would parse cleanly and be carried through to a document
		// ValidatePositiveIntOrPercent then rejects at admission.
		if errs := validation.IsValidPercent(s); len(errs) > 0 {
			return intstr.IntOrString{}, false, errors.Errorf("%s: string value must be a percentage such as \"25%%\", got %q", label, s)
		}
		// The form check leaves only digits, so the sole ParseUint failure is
		// an overflowing one — which is above 100 anyway.
		n, err := strconv.ParseUint(strings.TrimSuffix(s, "%"), 10, 32)
		if err != nil || n > 100 {
			return intstr.IntOrString{}, false, errors.Errorf("%s: percentage must not be greater than 100%%, got %q", label, s)
		}
		return intstr.FromString(s), true, nil
	}
	n, ok := toInt32(v)
	if !ok {
		return intstr.IntOrString{}, false, errors.Errorf("%s: must be a non-negative integer or a percentage string, got %T", label, v)
	}
	if n < 0 {
		return intstr.IntOrString{}, false, errors.Errorf("%s: must be >= 0, got %d", label, n)
	}
	// No 100 cap on the integer form: an integer here is a pod count, not a
	// percentage, and IsNotMoreThan100Percent only inspects percentages.
	return intstr.FromInt32(n), true, nil
}

// isZeroIntOrPercent reports whether an int-or-percent carries the value zero
// in whichever form it was authored, matching upstream's getIntOrPercentValue:
// the integer 0, or the percentage "0%".
func isZeroIntOrPercent(v intstr.IntOrString) bool {
	if v.Type == intstr.String {
		n, err := strconv.ParseUint(strings.TrimSuffix(v.StrVal, "%"), 10, 32)
		return err == nil && n == 0
	}
	return v.IntValue() == 0
}

// describeRollingUpdateKnob renders one rolling-update knob for the pairwise
// error above, saying explicitly when the value came from the API default
// rather than from the document. The defaulted half is the one the author
// cannot see in their own YAML, and naming it is the difference between "why
// is this rejected" and a fix.
func describeRollingUpdateKnob(v intstr.IntOrString, authored bool, apiDefault string) string {
	if !authored {
		return "unset, so the API default " + apiDefault + " applies"
	}
	return "authored as " + v.String()
}

// apply writes the authored DaemonSetSpec-level fields directly onto the
// DaemonSet. Unauthored fields keep whatever the constructor set, so output for
// documents that author none of them does not change.
func (c DaemonSetSpecConfig) apply(ds *appsv1.DaemonSet) {
	if c.UpdateStrategy != nil {
		ds.Spec.UpdateStrategy = *c.UpdateStrategy
	}
	if c.MinReadySeconds != nil {
		ds.Spec.MinReadySeconds = *c.MinReadySeconds
	}
	if c.RevisionHistoryLimit != nil {
		ds.Spec.RevisionHistoryLimit = c.RevisionHistoryLimit
	}
}

// schemaDaemonSetSpec describes the DaemonSetSpec-level properties (see
// parseDaemonSetSpec). The key set is pinned to daemonSetSpecPropertyKeys.
func schemaDaemonSetSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"updateStrategy": {
			Type:        oam.PropertyTypeObject,
			Description: "How pods are replaced when the pod template changes. Unset leaves the API default (RollingUpdate).",
			Properties: map[string]oam.PropertySchema{
				"type": {
					Type:        oam.PropertyTypeString,
					Required:    true,
					Enum:        []any{string(appsv1.RollingUpdateDaemonSetStrategyType), string(appsv1.OnDeleteDaemonSetStrategyType)},
					Description: "RollingUpdate replaces pods node by node; OnDelete only recreates pods that are deleted manually. Required whenever updateStrategy is authored at all — the API would default it to RollingUpdate, but an updateStrategy object whose only meaning comes from defaulting is not worth writing.",
				},
				"rollingUpdate": {
					Type:        oam.PropertyTypeObject,
					Description: "RollingUpdate parameters. Only allowed when type is RollingUpdate: the API accepts this object under OnDelete and then never reads it, which is refused here rather than silently dropped. May be omitted under RollingUpdate, leaving the API defaults. Exactly one of maxUnavailable and maxSurge must be non-zero, counting the API defaults for whichever is left out (maxUnavailable 1, maxSurge 0) — so a non-zero maxSurge requires maxUnavailable: 0 alongside it.",
					Properties: map[string]oam.PropertySchema{
						// Neither declares a Type, mirroring schemaResources'
						// cpu/memory quantities (schema.go:148-155).
						// PropertyType carries no int-or-string member
						// (PropertyType's doc comment, pkg/oam/schema.go), and
						// declaring `string` does not merely understate the
						// accepted set — validatePropertyValue rejects a
						// non-string outright
						// (pkg/oam/property_validate.go:118-121), so
						// `maxUnavailable: 2` could never reach
						// parseDaemonSetIntOrPercent's integer branch through a
						// schema-validating consumer. Type "" skips the check
						// (property_validate.go:114-117) and leaves both forms
						// reachable; each Description carries the constraint.
						// Tracked in go-kure/launcher#383.
						"maxUnavailable": {Description: `Maximum pods taken down at once during the update: a percentage string such as "25%" (at most 100%), or a non-negative integer such as 2. The API default is 1. No declared type because this schema vocabulary has no int-or-string union — an integer is accepted and carried through as an integer, not converted to a string.`},
						"maxSurge":       {Description: `Maximum nodes that may run an old and a new pod at once during the update: a percentage string such as "25%" (at most 100%), or a non-negative integer such as 2. The API default is 0, and it may only be non-zero when maxUnavailable is 0. No declared type, for the same reason as maxUnavailable.`},
					},
				},
			},
		},
		"minReadySeconds": {
			Type:        oam.PropertyTypeInteger,
			Description: "Seconds a new pod must be ready without any container crashing before it counts as available. Must be >= 0; the API default is 0.",
		},
		"revisionHistoryLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: "Number of superseded ControllerRevisions kept for rollback. Must be >= 0; the API default is 10.",
		},
	}
}
