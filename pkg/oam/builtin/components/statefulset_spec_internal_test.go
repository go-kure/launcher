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
	assertKeysAt(t, root, "persistentVolumeClaimRetentionPolicy", statefulSetPVCRetentionKeys)
	assertKeysAt(t, root, "ordinals", statefulSetOrdinalsKeys)
	assertKeysAt(t, root, "updateStrategy", statefulSetUpdateStrategyKeys)
	assertKeysAt(t, root, "updateStrategy.rollingUpdate", statefulSetRollingUpdateKeys)
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
	got := cfg.UpdateStrategy.RollingUpdate.MaxUnavailable
	if got == nil || *got != intstr.FromInt32(2) {
		t.Errorf("MaxUnavailable = %v, want 2", got)
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
