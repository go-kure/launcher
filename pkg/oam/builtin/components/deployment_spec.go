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

// DeploymentSpecConfig holds the DeploymentSpec-level properties of the
// deployment component (go-kure/launcher#343; ADR-036 L1, the stratified-levels
// decision: one PodSpec/Container projection shared by every kind, with
// kind-named components projecting their own API kind). appsv1.DeploymentSpec
// has eight fields; Replicas is the component's own `replicas` property,
// Selector is builder-managed and Template is the pod projection
// (parsePodSpec), which leaves the five here. Each is nil when not authored so
// apply leaves the constructor's value in place.
type DeploymentSpecConfig struct {
	Strategy                *appsv1.DeploymentStrategy
	MinReadySeconds         *int32
	RevisionHistoryLimit    *int32
	Paused                  *bool
	ProgressDeadlineSeconds *int32
}

// deploymentSpecPropertyKeys lists every DeploymentSpec-level property the
// deployment component publishes and parses. TestDeploymentSpecSchemaMatchesParser
// pins schemaDeploymentSpec to this list.
var deploymentSpecPropertyKeys = []string{
	"strategy", "minReadySeconds", "revisionHistoryLimit", "paused", "progressDeadlineSeconds",
}

// The nested key sets each parser below accepts, declared here rather than
// inline at the rejectUnknownKeys call so the schema fragment can be pinned to
// them (TestDeploymentSpecSchemaMatchesParser walks both). An inline literal
// drifts silently: a key added to the schema and not to the parser, or the
// reverse, reads as correct at both halves.
var (
	deploymentStrategyKeys      = []string{"type", "rollingUpdate"}
	deploymentRollingUpdateKeys = []string{"maxUnavailable", "maxSurge"}
)

// deploymentSpecRejectedKeys are DeploymentSpec fields an author may not set;
// each maps to the error explaining why.
var deploymentSpecRejectedKeys = map[string]string{
	"selector": "selector: not authorable; the Deployment selector is builder-managed (app: <component>), must equal the generated template labels and is immutable once created",
	"template": "template: not authorable as a whole; the pod template is projected from the component's own container and pod-level properties",
}

// parseDeploymentSpec reads the DeploymentSpec-level properties. Validation
// mirrors apps/v1 ValidateDeploymentSpec where it is deterministic from the
// document alone: the strategy enum and its rolling-update rules, non-negative
// minReadySeconds, revisionHistoryLimit and progressDeadlineSeconds, and
// upstream's requirement that progressDeadlineSeconds exceed minReadySeconds.
//
// It is deliberately STRICTER than upstream in exactly one place:
// strategy.type is required whenever `strategy` is authored at all. Upstream
// defaults it to RollingUpdate and acts on that, so `strategy: {}` is a legal
// document there whose entire meaning comes from defaulting rather than from
// anything written. That strictness is additive: `strategy` is a new property
// on a new component, so no existing document can carry the bare shape.
func parseDeploymentSpec(props map[string]any) (DeploymentSpecConfig, error) {
	var cfg DeploymentSpecConfig

	rejected := make([]string, 0, len(deploymentSpecRejectedKeys))
	for key := range deploymentSpecRejectedKeys {
		rejected = append(rejected, key)
	}
	slices.Sort(rejected)
	for _, key := range rejected {
		if _, present := props[key]; present {
			return DeploymentSpecConfig{}, errors.New(deploymentSpecRejectedKeys[key])
		}
	}

	// Deliberately after the rejected-key loop, not before: `selector: null` is
	// an author naming a key that must not appear at all, and the explanatory
	// refusal is more useful there than silence. Null-as-absence is a rule about
	// OPTIONAL properties, which the rejected keys are not.
	props = withoutExplicitNulls(props)

	if raw, present, err := parseObjectField(props, "strategy", "strategy"); err != nil {
		return DeploymentSpecConfig{}, err
	} else if present {
		s, err := parseDeploymentStrategy(raw)
		if err != nil {
			return DeploymentSpecConfig{}, err
		}
		cfg.Strategy = s
	}

	if v, present, err := parseInt32Field(props, "minReadySeconds", "minReadySeconds"); err != nil {
		return DeploymentSpecConfig{}, err
	} else if present {
		if v < 0 {
			return DeploymentSpecConfig{}, errors.Errorf("minReadySeconds: must be >= 0, got %d", v)
		}
		cfg.MinReadySeconds = &v
	}

	if v, present, err := parseInt32Field(props, "revisionHistoryLimit", "revisionHistoryLimit"); err != nil {
		return DeploymentSpecConfig{}, err
	} else if present {
		if v < 0 {
			return DeploymentSpecConfig{}, errors.Errorf("revisionHistoryLimit: must be >= 0, got %d", v)
		}
		cfg.RevisionHistoryLimit = &v
	}

	paused, err := parseBoolField(props, "paused", "paused")
	if err != nil {
		return DeploymentSpecConfig{}, err
	}
	cfg.Paused = paused

	if v, present, err := parseInt32Field(props, "progressDeadlineSeconds", "progressDeadlineSeconds"); err != nil {
		return DeploymentSpecConfig{}, err
	} else if present {
		if v < 0 {
			return DeploymentSpecConfig{}, errors.Errorf("progressDeadlineSeconds: must be >= 0, got %d", v)
		}
		cfg.ProgressDeadlineSeconds = &v
	}

	// ValidateDeploymentSpec compares the two as EFFECTIVE values, and BOTH
	// halves have an API default the document may be leaving it to:
	// DeploymentSpec.MinReadySeconds is a plain int32, so an omitted one is 0
	// with no unset state at the API, and ProgressDeadlineSeconds is a pointer
	// the apiserver defaults to 600 (k8s.io/api/apps/v1/types.go, the
	// ProgressDeadlineSeconds field doc: "Defaults to 600s.").
	//
	// Comparing the effective pair turns an apply-time rejection into a
	// build-time one in both directions, which is why this check sits outside
	// the `present` branch above rather than inside it. `progressDeadlineSeconds: 0`
	// alone is an error against the defaulted minReadySeconds 0, and
	// `minReadySeconds: 600` alone is equally an error — against the defaulted
	// deadline 600 — even though that document mentions no deadline at all.
	// Checking only the authored half would accept the second and let the
	// apiserver refuse it later.
	if effectiveProgressDeadlineSeconds(cfg.ProgressDeadlineSeconds) <= effectiveMinReadySeconds(cfg.MinReadySeconds) {
		return DeploymentSpecConfig{}, errors.Errorf(
			"progressDeadlineSeconds (%s) must be greater than minReadySeconds (%s)",
			describeProgressDeadlineSeconds(cfg.ProgressDeadlineSeconds),
			describeMinReadySeconds(cfg.MinReadySeconds))
	}

	return cfg, nil
}

// defaultProgressDeadlineSeconds is what the apiserver writes when the document
// omits progressDeadlineSeconds — see the field doc quoted above. Named rather
// than inlined because it is upstream's number, not launcher's, and a reader
// checking it needs to know which.
const defaultProgressDeadlineSeconds = 600

func effectiveMinReadySeconds(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func effectiveProgressDeadlineSeconds(v *int32) int32 {
	if v == nil {
		return defaultProgressDeadlineSeconds
	}
	return *v
}

// describeMinReadySeconds and describeProgressDeadlineSeconds render the two
// halves of the comparison, saying explicitly when a value came from the API
// default rather than from the document. An unauthored half is the one the
// author cannot see in their own YAML, and naming it is the difference between
// "why is this rejected" and a fix.
func describeMinReadySeconds(v *int32) string {
	if v == nil {
		return "unset, so the API default 0 applies"
	}
	return "authored as " + strconv.FormatInt(int64(*v), 10)
}

func describeProgressDeadlineSeconds(v *int32) string {
	if v == nil {
		return "unset, so the API default " + strconv.Itoa(defaultProgressDeadlineSeconds) + " applies"
	}
	return "authored as " + strconv.FormatInt(int64(*v), 10)
}

// parseDeploymentStrategy decodes `strategy`, applying the one deliberate
// strictness documented on parseDeploymentSpec: `type` is required.
//
// In the other direction it is deliberately LAXER in one place:
// ValidateDeploymentStrategy requires a non-nil rollingUpdate under
// RollingUpdate — its own comment says "this should be defaulted and never be
// nil" — but that requirement is satisfied by apiserver defaulting rather than
// by the author, so `type: RollingUpdate` alone is accepted here.
//
// `rollingUpdate` under `type: Recreate` is refused, which is not extra
// strictness: ValidateDeploymentStrategy's Recreate branch reports the field
// as Forbidden, so the apiserver refuses the same document.
func parseDeploymentStrategy(raw map[string]any) (*appsv1.DeploymentStrategy, error) {
	if err := rejectUnknownKeys(raw, deploymentStrategyKeys, "strategy"); err != nil {
		return nil, err
	}
	// Unknown keys are rejected against the authored map, null-as-absence
	// applies to what is read out of it. `type: null` therefore surfaces as
	// "strategy.type: required" rather than as a type error — the same thing an
	// absent key produces, which is the point.
	raw = withoutExplicitNulls(raw)
	typ, present, err := parseStringField(raw, "type", "strategy.type")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("strategy.type: required")
	}
	s := &appsv1.DeploymentStrategy{}
	switch appsv1.DeploymentStrategyType(typ) {
	case appsv1.RecreateDeploymentStrategyType, appsv1.RollingUpdateDeploymentStrategyType:
		s.Type = appsv1.DeploymentStrategyType(typ)
	default:
		return nil, errors.Errorf("strategy.type: invalid value %q, must be %s or %s", typ, appsv1.RecreateDeploymentStrategyType, appsv1.RollingUpdateDeploymentStrategyType)
	}

	ru, present, err := parseObjectField(raw, "rollingUpdate", "strategy.rollingUpdate")
	if err != nil {
		return nil, err
	}
	if !present {
		return s, nil
	}
	if s.Type != appsv1.RollingUpdateDeploymentStrategyType {
		return nil, errors.Errorf("strategy.rollingUpdate: only allowed for strategy.type %s", appsv1.RollingUpdateDeploymentStrategyType)
	}
	if err := rejectUnknownKeys(ru, deploymentRollingUpdateKeys, "strategy.rollingUpdate"); err != nil {
		return nil, err
	}
	ru = withoutExplicitNulls(ru)

	s.RollingUpdate = &appsv1.RollingUpdateDeployment{}
	// maxUnavailable is capped at 100%, maxSurge is NOT.
	// ValidateRollingUpdateDeployment calls IsNotMoreThan100Percent on
	// maxUnavailable only; surging past the desired count is meaningful
	// ("maxSurge: 200%" triples the pods mid-update) while making more than
	// 100% of them unavailable is not. This is the reverse of the DaemonSet
	// kind, which caps both — do not unify the two parsers.
	maxUnavailable, unavailableAuthored, err := parseDeploymentIntOrPercent(ru, "maxUnavailable", "strategy.rollingUpdate.maxUnavailable", true)
	if err != nil {
		return nil, err
	}
	if unavailableAuthored {
		s.RollingUpdate.MaxUnavailable = &maxUnavailable
	}
	maxSurge, surgeAuthored, err := parseDeploymentIntOrPercent(ru, "maxSurge", "strategy.rollingUpdate.maxSurge", false)
	if err != nil {
		return nil, err
	}
	if surgeAuthored {
		s.RollingUpdate.MaxSurge = &maxSurge
	}

	// ValidateRollingUpdateDeployment rejects only the case where BOTH
	// effective values are zero — unlike the DaemonSet rule, which rejects
	// both-non-zero as well. Both fields are pointers the apiserver defaults
	// to 25% when the document omits them (k8s.io/api/apps/v1/types.go, the
	// RollingUpdateDeployment field docs: "Defaults to 25%." on each), and
	// SetDefaults_Deployment guards each with its own `== nil` check rather
	// than one covering the pair, so authoring one knob does NOT suppress the
	// other's default. That is why a single authored zero is always fine and
	// only two authored zeroes can trip this. Counting the defaults keeps the
	// rule stated the way upstream states it rather than as a special case of
	// "both authored" — the two happen to coincide here, but they do not on
	// the DaemonSet kind, whose rule this must not be conflated with.
	unavailableZero := unavailableAuthored && intOrPercentIsZero(maxUnavailable)
	surgeZero := surgeAuthored && intOrPercentIsZero(maxSurge)
	if unavailableZero && surgeZero {
		return nil, errors.New("strategy.rollingUpdate.maxUnavailable: may not be 0 when maxSurge is 0; the update would never make progress")
	}

	return s, nil
}

// withoutExplicitNulls returns a copy of raw with every key whose value is an
// explicit null removed, so the typed parse helpers read `field: null` as
// omission instead of as a present-but-wrong type.
//
// Without it the two halves of the pipeline disagree about the same byte.
// pkg/oam's property validator treats a null under an optional property as
// absent — it returns early rather than type-checking it, and reads a null as
// unset when deciding requiredness (property_validate.go) — while
// parseObjectField, parseStringField, parseInt32Field, parseBoolField and
// parseDeploymentIntOrPercent all answer "present?" with a bare map lookup, so
// the nil reaches their type check and comes back as "must be an object, got
// <nil>". A lowering rule that emits an unset optional as an explicit null
// therefore produces a component that satisfies the published schema and then
// fails during handler conversion.
//
// A typed nil (map[string]any(nil) or []any(nil) inside an `any`) is not
// `== nil`, so isExplicitNull (common.go) mirrors the validator's own
// isNullValue and the two agree on that shape too.
//
// Scoped to this kind's parser rather than folded into the shared helpers:
// those carry ~20 pre-existing call sites whose behaviour would change with
// them, which is wider than this change should reach. The statefulset kind
// answered the same problem per call site instead, with the optionalString /
// optionalObject / optionalInt32 wrappers in common.go; the two approaches
// classify a null identically — they share isExplicitNull — and differ only in
// whether the filtering happens once over the whole map or once per field.
// Collapsing them onto one mechanism is tracked as go-kure/launcher#394.
func withoutExplicitNulls(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if isExplicitNull(v) {
			continue
		}
		out[k] = v
	}
	return out
}

// parseDeploymentIntOrPercent reads one of the two rolling-update knobs,
// mirroring ValidatePositiveIntOrPercent: a non-negative integer, or a "N%"
// string whose digits the rollout controller's own conversion can read back.
// capPercent additionally applies IsNotMoreThan100Percent, which upstream runs
// on maxUnavailable and not on maxSurge — the representability check applies to
// both, since a percentage nothing can convert is unusable whether or not it
// has a ceiling.
//
// This deliberately does not reuse the DaemonSet kind's parseDaemonSetIntOrPercent:
// that one caps both knobs, because ValidateRollingUpdateDaemonSet calls
// IsNotMoreThan100Percent on both. Sharing one helper would silently import
// the wrong ceiling into whichever kind lost the argument.
func parseDeploymentIntOrPercent(raw map[string]any, key, label string, capPercent bool) (intstr.IntOrString, bool, error) {
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
		// The form check leaves only digits, so the sole Atoi failure is an
		// overflowing one. Checked for BOTH knobs, not just the capped one:
		// maxSurge has no ceiling, but a percentage nothing can convert is not
		// a number the rollout can act on either — intstr carries the string
		// verbatim and ResolveFenceposts is the first thing to look at it, long
		// after this component is out of the message. strconv.Atoi is
		// deliberately the very conversion it performs there
		// (getIntOrPercentValueSafely, apimachinery intstr.go:252), so the
		// boundary refused here is exactly the boundary the controller hits —
		// no strictness beyond it. The 100% cap stays on maxUnavailable alone.
		n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
		if err != nil {
			return intstr.IntOrString{}, false, errors.Errorf("%s: percentage is too large to be represented, got %q", label, s)
		}
		if capPercent && n > 100 {
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
	// No 100 cap on the integer form even when capPercent is set: an integer
	// here is a pod count, not a percentage, and IsNotMoreThan100Percent only
	// inspects percentages.
	return intstr.FromInt32(n), true, nil
}

// intOrPercentIsZero reports whether an int-or-percent carries the value zero
// in whichever form it was authored, matching upstream's getIntOrPercentValue:
// the integer 0, or the percentage "0%". intstr.IntValue() alone is not enough
// — it returns 0 for any string it cannot Atoi, "25%" included, which would
// make every percentage read as zero.
func intOrPercentIsZero(v intstr.IntOrString) bool {
	if v.Type == intstr.String {
		n, err := strconv.ParseUint(strings.TrimSuffix(v.StrVal, "%"), 10, 32)
		return err == nil && n == 0
	}
	return v.IntValue() == 0
}

// apply writes the authored DeploymentSpec-level fields directly onto the
// Deployment. Unauthored fields keep whatever the constructor set.
// Deep-copied on the way out for the reason spelled out on
// VolumeClaimSpecConfig.apply: this kind projects the same two shapes the
// others do — a strategy whose struct holds a *RollingUpdateDeployment, so
// dereferencing alone still shares that block, and two bare *int32.
func (c DeploymentSpecConfig) apply(dep *appsv1.Deployment) {
	if c.Strategy != nil {
		dep.Spec.Strategy = *c.Strategy.DeepCopy()
	}
	if c.MinReadySeconds != nil {
		dep.Spec.MinReadySeconds = *c.MinReadySeconds
	}
	if c.RevisionHistoryLimit != nil {
		limit := *c.RevisionHistoryLimit
		dep.Spec.RevisionHistoryLimit = &limit
	}
	if c.Paused != nil {
		dep.Spec.Paused = *c.Paused
	}
	if c.ProgressDeadlineSeconds != nil {
		deadline := *c.ProgressDeadlineSeconds
		dep.Spec.ProgressDeadlineSeconds = &deadline
	}
}

// schemaDeploymentSpec describes the DeploymentSpec-level properties (see
// parseDeploymentSpec). The key set is pinned to deploymentSpecPropertyKeys.
func schemaDeploymentSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"strategy": {
			Type:        oam.PropertyTypeObject,
			Description: "How pods are replaced when the pod template changes. Unset leaves the API default (RollingUpdate with maxUnavailable and maxSurge both 25%).",
			Properties: map[string]oam.PropertySchema{
				"type": {
					Type:        oam.PropertyTypeString,
					Required:    true,
					Enum:        []any{string(appsv1.RecreateDeploymentStrategyType), string(appsv1.RollingUpdateDeploymentStrategyType)},
					Description: "RollingUpdate replaces pods gradually; Recreate kills every existing pod before creating new ones. Required whenever strategy is authored at all — the API would default it to RollingUpdate, but a strategy object whose only meaning comes from defaulting is not worth writing.",
				},
				"rollingUpdate": {
					Type:        oam.PropertyTypeObject,
					Description: "RollingUpdate parameters. Only allowed when type is RollingUpdate — the API forbids it under Recreate, so this matches rather than tightens. May be omitted under RollingUpdate, leaving the API defaults. Both knobs may be non-zero at once; only both being zero is refused, since the update would never make progress.",
					Properties: map[string]oam.PropertySchema{
						// Neither declares a Type, mirroring schemaResources'
						// cpu/memory quantities. Launcher's PropertyType set
						// carries no int-or-string union, and declaring
						// `string` would not merely understate the accepted
						// set: property validation rejects a non-string
						// outright, so the parser's integer branch could never
						// be reached through a schema-validating consumer.
						// Type "" skips that check and leaves both forms
						// reachable; each Description carries the constraint.
						// Tracked in go-kure/launcher#383.
						"maxUnavailable": {Description: `Maximum pods that may be unavailable during the update: a percentage string such as "25%" (at most 100%), or a non-negative integer such as 2. The API default is 25%.`},
						"maxSurge":       {Description: `Maximum pods that may be scheduled above the desired count during the update: a percentage string such as "25%", or a non-negative integer such as 2. The API default is 25%. Deliberately NOT capped at 100% — unlike maxUnavailable, the API permits surging past the desired count.`},
					},
				},
			},
		},
		"minReadySeconds": {
			Type:        oam.PropertyTypeInteger,
			Description: "Seconds a new pod must be ready without any container crashing before it counts as available. Must be >= 0; the API default is 0. Must stay below the effective progressDeadlineSeconds, whose own API default is 600.",
		},
		"revisionHistoryLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: "Number of superseded ReplicaSets kept for rollback. Must be >= 0; the API default is 10.",
		},
		"paused": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Whether the deployment controller stops acting on template changes. A paused Deployment still creates its ReplicaSet but does not roll new pods out, and progress is not estimated while paused. The API default is false.",
		},
		"progressDeadlineSeconds": {
			Type:        oam.PropertyTypeInteger,
			Description: "Seconds the deployment may make no progress before it is marked failed with ProgressDeadlineExceeded. Must be >= 0 and strictly greater than the effective minReadySeconds. The API default is 600, and that default takes part in the comparison: authoring minReadySeconds >= 600 without also raising this is refused, because the apiserver would refuse the defaulted pair.",
		},
	}
}
