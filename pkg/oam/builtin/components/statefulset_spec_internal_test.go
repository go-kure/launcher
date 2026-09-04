package components

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestStatefulSetSpecSchemaMatchesParser pins schemaStatefulSetSpec's key set to
// statefulSetSpecPropertyKeys: every key the parser reads is published, nothing
// it ignores is, and no rejected key is advertised.
func TestStatefulSetSpecSchemaMatchesParser(t *testing.T) {
	got := make([]string, 0, len(statefulSetSpecPropertyKeys))
	for k := range schemaStatefulSetSpec() {
		got = append(got, k)
	}
	slices.Sort(got)
	want := slices.Clone(statefulSetSpecPropertyKeys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("schemaStatefulSetSpec() keys = %v\nwant %v", got, want)
	}
	for k := range statefulSetSpecRejectedKeys {
		if _, ok := schemaStatefulSetSpec()[k]; ok {
			t.Errorf("schemaStatefulSetSpec publishes rejected key %q", k)
		}
	}

	// Nested levels: the top-level comparison above says nothing about them.
	root := oam.PropertySchema{Type: oam.PropertyTypeObject, Properties: schemaStatefulSetSpec()}
	assertSchemaKeysAt(t, root, "persistentVolumeClaimRetentionPolicy", statefulSetPVCRetentionKeys)
	assertSchemaKeysAt(t, root, "ordinals", statefulSetOrdinalsKeys)
	assertSchemaKeysAt(t, root, "updateStrategy", statefulSetUpdateStrategyKeys)
	assertSchemaKeysAt(t, root, "updateStrategy.rollingUpdate", statefulSetRollingUpdateKeys)
}

// TestStatefulSetSpecSchema_EveryKeyDescribed walks the fragment recursively:
// every property, nested property and array item carries a Description.
func TestStatefulSetSpecSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: missing Description", path)
		}
		for k, sub := range s.Properties {
			walk(path+"."+k, sub)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	for k, s := range schemaStatefulSetSpec() {
		walk(k, s)
	}
}

// TestParseStatefulSetSpec_RoundTrip authors every accepted key once and checks
// the appsv1.StatefulSetSpec fields it lands in.
func TestParseStatefulSetSpec_RoundTrip(t *testing.T) {
	cfg, err := parseStatefulSetSpec(map[string]any{
		"podManagementPolicy": "Parallel",
		"updateStrategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"partition":      2,
				"maxUnavailable": "50%",
			},
		},
		"revisionHistoryLimit": 3,
		"minReadySeconds":      15,
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenDeleted": "Delete",
			"whenScaled":  "Retain",
		},
		"ordinals": map[string]any{"start": 4},
	})
	if err != nil {
		t.Fatalf("parseStatefulSetSpec: %v", err)
	}
	if cfg.PodManagementPolicy != appsv1.ParallelPodManagement {
		t.Errorf("PodManagementPolicy = %q, want Parallel", cfg.PodManagementPolicy)
	}
	if cfg.UpdateStrategy == nil {
		t.Fatal("UpdateStrategy is nil")
	}
	if cfg.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want RollingUpdate", cfg.UpdateStrategy.Type)
	}
	ru := cfg.UpdateStrategy.RollingUpdate
	if ru == nil {
		t.Fatal("UpdateStrategy.RollingUpdate is nil")
	}
	if ru.Partition == nil || *ru.Partition != 2 {
		t.Errorf("RollingUpdate.Partition = %v, want 2", ru.Partition)
	}
	if ru.MaxUnavailable == nil || *ru.MaxUnavailable != intstr.FromString("50%") {
		t.Errorf("RollingUpdate.MaxUnavailable = %v, want 50%%", ru.MaxUnavailable)
	}
	if cfg.RevisionHistoryLimit == nil || *cfg.RevisionHistoryLimit != 3 {
		t.Errorf("RevisionHistoryLimit = %v, want 3", cfg.RevisionHistoryLimit)
	}
	if cfg.MinReadySeconds == nil || *cfg.MinReadySeconds != 15 {
		t.Errorf("MinReadySeconds = %v, want 15", cfg.MinReadySeconds)
	}
	if cfg.PersistentVolumeClaimRetentionPolicy == nil {
		t.Fatal("PersistentVolumeClaimRetentionPolicy is nil")
	}
	if got := cfg.PersistentVolumeClaimRetentionPolicy.WhenDeleted; got != appsv1.DeletePersistentVolumeClaimRetentionPolicyType {
		t.Errorf("WhenDeleted = %q, want Delete", got)
	}
	if got := cfg.PersistentVolumeClaimRetentionPolicy.WhenScaled; got != appsv1.RetainPersistentVolumeClaimRetentionPolicyType {
		t.Errorf("WhenScaled = %q, want Retain", got)
	}
	if cfg.Ordinals == nil || cfg.Ordinals.Start != 4 {
		t.Errorf("Ordinals = %v, want start 4", cfg.Ordinals)
	}
}

// TestParseStatefulSetSpec_NullIsOmission covers every optional field this
// change adds at the StatefulSet level. A lowering rule may emit a nil for any
// of them: pkg/oam's emission validation accepts that (validatePropertyValue
// returns early for a null under an optional property), so a component can
// satisfy the published schema and still reach this parser with a nil. If the
// parser answered "must be an object/string/integer, got <nil>", that component
// would pass validation and then fail conversion.
//
// The property under test is therefore narrow and uniform: a null must never
// produce a TYPE error. For the plainly optional fields it must parse clean and
// leave the field unset; for a field that is required once its parent is
// authored, the null must surface as the requiredness error instead — which is
// what an author who wrote nothing there should be told.
func TestParseStatefulSetSpec_NullIsOmission(t *testing.T) {
	t.Run("optional fields parse clean and stay unset", func(t *testing.T) {
		cfg, err := parseStatefulSetSpec(map[string]any{
			"podManagementPolicy":                  nil,
			"updateStrategy":                       nil,
			"revisionHistoryLimit":                 nil,
			"minReadySeconds":                      nil,
			"persistentVolumeClaimRetentionPolicy": nil,
			"ordinals":                             nil,
		})
		if err != nil {
			t.Fatalf("parseStatefulSetSpec(all null) error = %v, want them read as omitted", err)
		}
		if cfg.PodManagementPolicy != "" {
			t.Errorf("PodManagementPolicy = %q, want empty", cfg.PodManagementPolicy)
		}
		if cfg.UpdateStrategy != nil {
			t.Errorf("UpdateStrategy = %v, want nil", cfg.UpdateStrategy)
		}
		if cfg.RevisionHistoryLimit != nil {
			t.Errorf("RevisionHistoryLimit = %v, want nil", cfg.RevisionHistoryLimit)
		}
		if cfg.MinReadySeconds != nil {
			t.Errorf("MinReadySeconds = %v, want nil", cfg.MinReadySeconds)
		}
		if cfg.PersistentVolumeClaimRetentionPolicy != nil {
			t.Errorf("PersistentVolumeClaimRetentionPolicy = %v, want nil", cfg.PersistentVolumeClaimRetentionPolicy)
		}
		if cfg.Ordinals != nil {
			t.Errorf("Ordinals = %v, want nil", cfg.Ordinals)
		}
	})

	// Nested leaves, one per parser call site, each authored as null under a
	// parent that IS authored — the case a whole-object null above cannot reach.
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string // "" = must parse clean
	}{
		{
			// A null type with nothing to infer from leaves the strategy with
			// no type at all, so the requiredness error is the right answer —
			// and it is the one an author who wrote nothing there needs.
			"updateStrategy.type alone",
			map[string]any{"updateStrategy": map[string]any{"type": nil}},
			"updateStrategy.type: required",
		},
		{
			// The same null, but now rollingUpdate is authored, so the type is
			// inferred exactly as it would be had the key been absent. This is
			// the case that proves the null reached the inference branch rather
			// than a type check.
			"updateStrategy.type with rollingUpdate authored",
			map[string]any{"updateStrategy": map[string]any{
				"type":          nil,
				"rollingUpdate": map[string]any{"partition": 2},
			}},
			"",
		},
		{
			"updateStrategy.rollingUpdate",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": nil}},
			"",
		},
		{
			"updateStrategy.rollingUpdate.partition",
			map[string]any{"updateStrategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"partition": nil},
			}},
			"",
		},
		{
			// parseMaxUnavailable takes an `any`, not a map, so this leaf
			// cannot go through an optional* wrapper and carries its own
			// isExplicitNull guard at the call site.
			"updateStrategy.rollingUpdate.maxUnavailable",
			map[string]any{"updateStrategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxUnavailable": nil},
			}},
			"",
		},
		{
			"persistentVolumeClaimRetentionPolicy.whenDeleted",
			map[string]any{"persistentVolumeClaimRetentionPolicy": map[string]any{"whenDeleted": nil}},
			"",
		},
		{
			"persistentVolumeClaimRetentionPolicy.whenScaled",
			map[string]any{"persistentVolumeClaimRetentionPolicy": map[string]any{"whenScaled": nil}},
			"",
		},
		{
			// start is required once ordinals is authored, so the null must
			// reach the requiredness error and not the type check.
			"ordinals.start",
			map[string]any{"ordinals": map[string]any{"start": nil}},
			"ordinals.start: required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStatefulSetSpec(tc.props)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("parseStatefulSetSpec(%s: null) error = %v, want it read as omitted", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseStatefulSetSpec(%s: null) = nil error, want %q", tc.name, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}

	// The guard that makes the two cases above distinguishable: whatever the
	// parser says about a null, it must never be the "got <nil>" type error.
	for _, props := range []map[string]any{
		{"podManagementPolicy": nil},
		{"updateStrategy": nil},
		{"revisionHistoryLimit": nil},
		{"minReadySeconds": nil},
		{"persistentVolumeClaimRetentionPolicy": nil},
		{"ordinals": nil},
		{"updateStrategy": map[string]any{"type": nil}},
		{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": nil}},
		{"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxUnavailable": nil},
		}},
		{"ordinals": map[string]any{"start": nil}},
	} {
		if _, err := parseStatefulSetSpec(props); err != nil && strings.Contains(err.Error(), "<nil>") {
			t.Errorf("parseStatefulSetSpec(%v) error = %q, want no type error naming <nil>", props, err)
		}
	}

	// A typed nil is not `== nil`. A lowering rule assembled in Go — rather
	// than decoded from YAML — produces exactly this for an unset optional
	// map, and pkg/oam's isNullValue reads it as null, so the parser must too.
	t.Run("typed nil map", func(t *testing.T) {
		var nilMap map[string]any
		cfg, err := parseStatefulSetSpec(map[string]any{
			"updateStrategy":                       nilMap,
			"persistentVolumeClaimRetentionPolicy": nilMap,
			"ordinals":                             nilMap,
		})
		if err != nil {
			t.Fatalf("parseStatefulSetSpec(typed nil maps) error = %v, want them read as omitted", err)
		}
		if cfg.UpdateStrategy != nil {
			t.Errorf("UpdateStrategy = %v, want nil", cfg.UpdateStrategy)
		}
		if cfg.PersistentVolumeClaimRetentionPolicy != nil {
			t.Errorf("PersistentVolumeClaimRetentionPolicy = %v, want nil", cfg.PersistentVolumeClaimRetentionPolicy)
		}
		if cfg.Ordinals != nil {
			t.Errorf("Ordinals = %v, want nil", cfg.Ordinals)
		}
	})

	t.Run("typed nil under an authored parent", func(t *testing.T) {
		var nilMap map[string]any
		if _, err := parseStatefulSetSpec(map[string]any{
			"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": nilMap},
		}); err != nil {
			t.Fatalf("parseStatefulSetSpec(typed nil rollingUpdate) error = %v, want it read as omitted", err)
		}
	})
}

// TestParseStatefulSetSpec_IntegerMaxUnavailable: the int-or-string field also
// accepts a plain positive integer.
func TestParseStatefulSetSpec_IntegerMaxUnavailable(t *testing.T) {
	cfg, err := parseStatefulSetSpec(map[string]any{
		"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxUnavailable": 2},
		},
	})
	if err != nil {
		t.Fatalf("parseStatefulSetSpec: %v", err)
	}
	if cfg.UpdateStrategy == nil {
		t.Fatal("UpdateStrategy is nil")
	}
	if cfg.UpdateStrategy.RollingUpdate == nil {
		t.Fatal("UpdateStrategy.RollingUpdate is nil")
	}
	got := cfg.UpdateStrategy.RollingUpdate.MaxUnavailable
	if got == nil || *got != intstr.FromInt32(2) {
		t.Errorf("MaxUnavailable = %v, want 2", got)
	}
}

// TestStatefulSetSpecSchema_UpdateStrategyLeavesMatchTheParser guards the two
// places where a schema declaration would reject a document the parser accepts.
// PropertySchema is checked before the handler runs, so an over-tight leaf here
// is not merely cosmetic — it makes the parser's own branch unreachable.
func TestStatefulSetSpecSchema_UpdateStrategyLeavesMatchTheParser(t *testing.T) {
	us := schemaStatefulSetSpec()["updateStrategy"]

	// parseMaxUnavailable takes a positive integer as well as a percentage
	// string, and validatePropertyValue rejects a non-string outright once
	// Type is PropertyTypeString (pkg/oam/property_validate.go:118-121) —
	// Type "" is the only declaration that leaves both forms reachable.
	if mu := us.Properties["rollingUpdate"].Properties["maxUnavailable"]; mu.Type != "" {
		t.Errorf("updateStrategy.rollingUpdate.maxUnavailable declares Type %q; want it unset so the integer form the parser accepts is not rejected before the handler runs", mu.Type)
	}

	// parseStatefulSetUpdateStrategy requires `type` only for an otherwise
	// empty object and infers RollingUpdate when `rollingUpdate` is authored;
	// PropertySchema has no conditional requiredness, so declaring Required
	// would reject `updateStrategy: {rollingUpdate: {...}}` outright.
	if us.Properties["type"].Required {
		t.Error("updateStrategy.type is declared Required; the parser infers RollingUpdate when rollingUpdate is authored, so a Required declaration rejects a document the parser accepts")
	}
}

// TestParseStatefulSetSpec_UpdateStrategyTypeInferred: `rollingUpdate` without
// `type` is a partition-only strategy the apiserver defaults to RollingUpdate
// and acts on as written; the bare `updateStrategy: {}` case still requires
// `type` (TestParseStatefulSetSpec_Errors).
func TestParseStatefulSetSpec_UpdateStrategyTypeInferred(t *testing.T) {
	cfg, err := parseStatefulSetSpec(map[string]any{
		"updateStrategy": map[string]any{
			"rollingUpdate": map[string]any{"partition": 3},
		},
	})
	if err != nil {
		t.Fatalf("parseStatefulSetSpec: %v", err)
	}
	if cfg.UpdateStrategy == nil {
		t.Fatal("UpdateStrategy is nil")
	}
	if cfg.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want %q", cfg.UpdateStrategy.Type, appsv1.RollingUpdateStatefulSetStrategyType)
	}
	ru := cfg.UpdateStrategy.RollingUpdate
	if ru == nil {
		t.Fatal("UpdateStrategy.RollingUpdate is nil")
	}
	if ru.Partition == nil || *ru.Partition != 3 {
		t.Errorf("Partition = %v, want 3", ru.Partition)
	}
}

// TestParseStatefulSetSpec_Empty: nothing authored leaves every field at its
// zero value, so apply is a no-op and existing output cannot move.
func TestParseStatefulSetSpec_Empty(t *testing.T) {
	cfg, err := parseStatefulSetSpec(map[string]any{"image": "example:latest"})
	if err != nil {
		t.Fatalf("parseStatefulSetSpec: %v", err)
	}
	if cfg != (StatefulSetSpecConfig{}) {
		t.Errorf("parseStatefulSetSpec(no keys) = %+v, want the zero config", cfg)
	}

	sts := appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{
		PodManagementPolicy: appsv1.OrderedReadyPodManagement,
	}}
	before := sts
	cfg.apply(&sts)
	if sts.Spec.PodManagementPolicy != before.Spec.PodManagementPolicy {
		t.Errorf("apply changed PodManagementPolicy to %q", sts.Spec.PodManagementPolicy)
	}
	if sts.Spec.UpdateStrategy.Type != "" || sts.Spec.RevisionHistoryLimit != nil ||
		sts.Spec.MinReadySeconds != 0 || sts.Spec.PersistentVolumeClaimRetentionPolicy != nil ||
		sts.Spec.Ordinals != nil {
		t.Errorf("apply wrote a field from the zero config: %+v", sts.Spec)
	}
}

func TestParseStatefulSetSpec_Errors(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"selector rejected", map[string]any{"selector": map[string]any{}}, "selector: not authorable"},
		{"podManagementPolicy enum", map[string]any{"podManagementPolicy": "Ordered"}, "podManagementPolicy: invalid value"},
		{"podManagementPolicy not a string", map[string]any{"podManagementPolicy": 1}, "podManagementPolicy: must be a string"},
		{"updateStrategy not an object", map[string]any{"updateStrategy": "RollingUpdate"}, "updateStrategy: must be an object"},
		{"updateStrategy unknown key", map[string]any{"updateStrategy": map[string]any{"type": "OnDelete", "partition": 1}}, `updateStrategy: unrecognized key "partition"`},
		{"updateStrategy type missing", map[string]any{"updateStrategy": map[string]any{}}, "updateStrategy.type: required"},
		{"updateStrategy type enum", map[string]any{"updateStrategy": map[string]any{"type": "Recreate"}}, "updateStrategy.type: invalid value"},
		{
			"rollingUpdate under OnDelete",
			map[string]any{"updateStrategy": map[string]any{"type": "OnDelete", "rollingUpdate": map[string]any{"partition": 1}}},
			"only allowed for updateStrategy.type RollingUpdate",
		},
		{
			"rollingUpdate unknown key",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": 1}}},
			`updateStrategy.rollingUpdate: unrecognized key "maxSurge"`,
		},
		{
			"negative partition",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"partition": -1}}},
			"partition: must be >= 0",
		},
		{
			"maxUnavailable zero",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": 0}}},
			"maxUnavailable: must be >= 1",
		},
		{
			// The lower bound of the percentage range, distinct from the
			// integer `maxUnavailable: 0` case above: that one is caught by the
			// int branch, this one by the `n < 1` half of the percent branch,
			// which nothing else exercised. Upstream
			// validateRollingUpdateStatefulSet rejects zero the same way.
			"maxUnavailable zero percent",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "0%"}}},
			"percentage must be between 1% and 100%",
		},
		{
			"maxUnavailable over 100 percent",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "150%"}}},
			"percentage must be between 1% and 100%",
		},
		{
			"maxUnavailable bare string",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "half"}}},
			"string value must be a percentage",
		},
		{
			"maxUnavailable malformed percentage",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": " 50%"}}},
			"string value must be a percentage",
		},
		// strconv.Atoi accepted a leading sign, so these three parsed cleanly
		// and were carried through to a document upstream's ^[0-9]+%$ rejects.
		{
			"maxUnavailable signed percentage",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "+50%"}}},
			"string value must be a percentage",
		},
		{
			"maxUnavailable negative percentage",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "-50%"}}},
			"string value must be a percentage",
		},
		{
			"maxUnavailable overflowing percentage",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "99999999999999999999%"}}},
			"percentage must be between 1% and 100%",
		},
		{
			"maxUnavailable wrong type",
			map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": true}}},
			"must be a positive integer or a percentage string",
		},
		{"negative revisionHistoryLimit", map[string]any{"revisionHistoryLimit": -1}, "revisionHistoryLimit: must be >= 0"},
		{"revisionHistoryLimit not an integer", map[string]any{"revisionHistoryLimit": "3"}, "revisionHistoryLimit"},
		{"negative minReadySeconds", map[string]any{"minReadySeconds": -1}, "minReadySeconds: must be >= 0"},
		{
			"retention policy unknown key",
			map[string]any{"persistentVolumeClaimRetentionPolicy": map[string]any{"whenUpdated": "Delete"}},
			`persistentVolumeClaimRetentionPolicy: unrecognized key "whenUpdated"`,
		},
		{
			"retention policy enum",
			map[string]any{"persistentVolumeClaimRetentionPolicy": map[string]any{"whenScaled": "Keep"}},
			"persistentVolumeClaimRetentionPolicy.whenScaled: invalid value",
		},
		{"ordinals unknown key", map[string]any{"ordinals": map[string]any{"start": 1, "end": 5}}, `ordinals: unrecognized key "end"`},
		{"ordinals start missing", map[string]any{"ordinals": map[string]any{}}, "ordinals.start: required"},
		{"ordinals start negative", map[string]any{"ordinals": map[string]any{"start": -1}}, "ordinals.start: must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStatefulSetSpec(tc.props)
			if err == nil {
				t.Fatalf("parseStatefulSetSpec(%v) succeeded, want error containing %q", tc.props, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
