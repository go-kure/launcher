package components

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
)

// schemaKeysAt returns the property names the schema publishes at a
// dot-separated path, e.g. "updateStrategy.rollingUpdate".
func schemaKeysAt(t *testing.T, root map[string]oam.PropertySchema, path string) []string {
	t.Helper()
	cur := root
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		s, ok := cur[seg]
		if !ok {
			t.Fatalf("schema has no property at %q (missing segment %q)", path, seg)
		}
		if i == len(segments)-1 {
			cur = s.Properties
			break
		}
		cur = s.Properties
	}
	keys := make([]string, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func assertKeysAt(t *testing.T, root map[string]oam.PropertySchema, path string, want []string) {
	t.Helper()
	sorted := slices.Clone(want)
	slices.Sort(sorted)
	if got := schemaKeysAt(t, root, path); !slices.Equal(got, sorted) {
		t.Errorf("schema keys at %q = %v, want %v", path, got, sorted)
	}
}

// TestDaemonSetSpecSchemaMatchesParser pins the published schema to the key
// slices the parser rejects against, at every nesting level. Without it a key
// added to one half and not the other reads as correct at both: the schema
// consumer accepts a document the parser then refuses, or the parser accepts a
// key no schema documents.
func TestDaemonSetSpecSchemaMatchesParser(t *testing.T) {
	s := schemaDaemonSetSpec()

	top := make([]string, 0, len(s))
	for k := range s {
		top = append(top, k)
	}
	slices.Sort(top)
	want := slices.Clone(daemonSetSpecPropertyKeys)
	slices.Sort(want)
	if !slices.Equal(top, want) {
		t.Errorf("schemaDaemonSetSpec keys = %v, want %v", top, want)
	}

	assertKeysAt(t, s, "updateStrategy", daemonSetUpdateStrategyKeys)
	assertKeysAt(t, s, "updateStrategy.rollingUpdate", daemonSetRollingUpdateKeys)

	// Pinning the key SETS leaves the per-key contract unpinned: dropping
	// Required from updateStrategy.type would publish a schema saying the key
	// is optional while the parser still demands it, and the key-set checks
	// above would stay green.
	typ := s["updateStrategy"].Properties["type"]
	if !typ.Required {
		t.Error("updateStrategy.type.Required = false, want true — the parser demands it")
	}
	wantEnum := []any{
		string(appsv1.RollingUpdateDaemonSetStrategyType),
		string(appsv1.OnDeleteDaemonSetStrategyType),
	}
	if !slices.Equal(typ.Enum, wantEnum) {
		t.Errorf("updateStrategy.type.Enum = %v, want %v", typ.Enum, wantEnum)
	}
	// Nothing else in the fragment is required; a Required leaf the parser does
	// not enforce rejects documents the parser accepts.
	if s["minReadySeconds"].Required || s["revisionHistoryLimit"].Required ||
		s["updateStrategy"].Required || s["updateStrategy"].Properties["rollingUpdate"].Required {
		t.Error("a property outside updateStrategy.type is marked Required; the parser enforces none of them")
	}
}

// TestParseDaemonSetSpec_StrategyWithoutRollingUpdate covers both accept paths
// that leave `rollingUpdate` out entirely. `type: RollingUpdate` alone is the
// one place this parser is deliberately laxer than
// ValidateDaemonSetUpdateStrategy, which requires a non-nil rollingUpdate there
// — a requirement apiserver defaulting satisfies, not the author.
func TestParseDaemonSetSpec_StrategyWithoutRollingUpdate(t *testing.T) {
	for _, typ := range []appsv1.DaemonSetUpdateStrategyType{
		appsv1.RollingUpdateDaemonSetStrategyType,
		appsv1.OnDeleteDaemonSetStrategyType,
	} {
		cfg, err := parseDaemonSetSpec(map[string]any{
			"updateStrategy": map[string]any{"type": string(typ)},
		})
		if err != nil {
			t.Fatalf("type %s: parseDaemonSetSpec: %v", typ, err)
		}
		if cfg.UpdateStrategy == nil || cfg.UpdateStrategy.Type != typ {
			t.Fatalf("type %s: UpdateStrategy = %+v", typ, cfg.UpdateStrategy)
		}
		// Left nil so the apiserver fills it, rather than written as an empty
		// object that would appear in the generated manifest as authored.
		if cfg.UpdateStrategy.RollingUpdate != nil {
			t.Errorf("type %s: RollingUpdate = %+v, want nil", typ, cfg.UpdateStrategy.RollingUpdate)
		}
	}
}

// TestParseDaemonSetSpec_IntegerFormIsUncapped: the 100 ceiling is
// IsNotMoreThan100Percent, which inspects percentages only. An integer knob is
// a pod count, so 150 is a legal document even though "150%" is not.
func TestParseDaemonSetSpec_IntegerFormIsUncapped(t *testing.T) {
	cfg, err := parseDaemonSetSpec(map[string]any{
		"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxUnavailable": 150},
		},
	})
	if err != nil {
		t.Fatalf("parseDaemonSetSpec: %v", err)
	}
	mu := cfg.UpdateStrategy.RollingUpdate.MaxUnavailable
	if mu == nil || *mu != intstr.FromInt32(150) {
		t.Errorf("MaxUnavailable = %v, want the integer 150", mu)
	}
}

// TestDaemonSetSpecSchema_EveryKeyDescribed: an undescribed property reaches the
// generated API reference as a bare name, which is what the doc gate exists to
// prevent.
func TestDaemonSetSpecSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(prefix string, m map[string]oam.PropertySchema)
	walk = func(prefix string, m map[string]oam.PropertySchema) {
		for k, v := range m {
			path := prefix + k
			if v.Description == "" {
				t.Errorf("%s has no Description", path)
			}
			walk(path+".", v.Properties)
		}
	}
	walk("", schemaDaemonSetSpec())
}

func TestParseDaemonSetSpec_RoundTrip(t *testing.T) {
	cfg, err := parseDaemonSetSpec(map[string]any{
		"minReadySeconds":      30,
		"revisionHistoryLimit": 3,
		"updateStrategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"maxUnavailable": "25%",
			},
		},
	})
	if err != nil {
		t.Fatalf("parseDaemonSetSpec: %v", err)
	}
	if cfg.MinReadySeconds == nil || *cfg.MinReadySeconds != 30 {
		t.Errorf("MinReadySeconds = %v, want 30", cfg.MinReadySeconds)
	}
	if cfg.RevisionHistoryLimit == nil || *cfg.RevisionHistoryLimit != 3 {
		t.Errorf("RevisionHistoryLimit = %v, want 3", cfg.RevisionHistoryLimit)
	}
	if cfg.UpdateStrategy == nil {
		t.Fatal("UpdateStrategy is nil")
	}
	if cfg.UpdateStrategy.Type != appsv1.RollingUpdateDaemonSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want RollingUpdate", cfg.UpdateStrategy.Type)
	}
	if cfg.UpdateStrategy.RollingUpdate == nil {
		t.Fatal("RollingUpdate is nil")
	}
	if cfg.UpdateStrategy.RollingUpdate.MaxUnavailable == nil ||
		*cfg.UpdateStrategy.RollingUpdate.MaxUnavailable != intstr.FromString("25%") {
		t.Errorf("MaxUnavailable = %v, want 25%%", cfg.UpdateStrategy.RollingUpdate.MaxUnavailable)
	}
	// Unauthored, so it must stay nil rather than being written as an explicit
	// zero — the apiserver's own default is what should fill it in.
	if cfg.UpdateStrategy.RollingUpdate.MaxSurge != nil {
		t.Errorf("MaxSurge = %v, want nil (unauthored)", cfg.UpdateStrategy.RollingUpdate.MaxSurge)
	}
}

// TestParseDaemonSetSpec_UnauthoredIsZero: a document authoring none of the
// DaemonSetSpec-level keys parses to the zero config, so apply cannot move the
// constructor's output.
func TestParseDaemonSetSpec_UnauthoredIsZero(t *testing.T) {
	cfg, err := parseDaemonSetSpec(map[string]any{"image": "ghcr.io/org/app:v1"})
	if err != nil {
		t.Fatalf("parseDaemonSetSpec: %v", err)
	}
	if cfg != (DaemonSetSpecConfig{}) {
		t.Errorf("config = %+v, want the zero value", cfg)
	}
}

// TestParseDaemonSetSpec_SingleKnobUsesAPIDefaults walks every
// one-field-authored shape through the exactly-one-non-zero rule. The half the
// document leaves out is not unconstrained: the apiserver defaults
// maxUnavailable to 1 and maxSurge to 0 (k8s.io/api/apps/v1/types.go,
// RollingUpdateDaemonSet field docs), so each of these four cases has one
// correct answer and `maxSurge: 2` alone is an invalid document, not an
// undecidable one.
func TestParseDaemonSetSpec_SingleKnobUsesAPIDefaults(t *testing.T) {
	cases := []struct {
		name    string
		ru      map[string]any
		wantErr string // empty means the document must be accepted
	}{
		{"maxUnavailable non-zero alone", map[string]any{"maxUnavailable": 2}, ""},
		{"maxSurge zero alone", map[string]any{"maxSurge": 0}, ""},
		{
			// maxUnavailable defaults to 1, so this is the both-non-zero case.
			"maxSurge non-zero alone",
			map[string]any{"maxSurge": 2},
			"maxSurge: may not be non-zero while maxUnavailable is non-zero (unset, so the API default 1 applies)",
		},
		{
			// maxSurge defaults to 0, so this is the both-zero case.
			"maxUnavailable zero alone",
			map[string]any{"maxUnavailable": 0},
			"maxUnavailable: cannot be 0 while maxSurge is 0 (unset, so the API default 0 applies)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseDaemonSetSpec(map[string]any{
				"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": tc.ru},
			})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseDaemonSetSpec succeeded, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDaemonSetSpec: %v", err)
			}
			if cfg.UpdateStrategy == nil || cfg.UpdateStrategy.RollingUpdate == nil {
				t.Fatal("rollingUpdate not parsed")
			}
		})
	}
}

// TestParseDaemonSetSpec_NeitherKnobAuthored: `rollingUpdate: {}` under
// RollingUpdate is accepted — the defaults are maxUnavailable 1 / maxSurge 0,
// which is exactly one non-zero.
func TestParseDaemonSetSpec_NeitherKnobAuthored(t *testing.T) {
	cfg, err := parseDaemonSetSpec(map[string]any{
		"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("parseDaemonSetSpec: %v", err)
	}
	ru := cfg.UpdateStrategy.RollingUpdate
	if ru == nil || ru.MaxUnavailable != nil || ru.MaxSurge != nil {
		t.Errorf("rollingUpdate = %+v, want both knobs nil so the apiserver defaults them", ru)
	}
}

// TestParseDaemonSetSpec_SurgeIdiomAccepted: the documented way to use surge is
// `maxUnavailable: 0` alongside a non-zero maxSurge, in either spelling. Both
// must survive the pairwise check.
func TestParseDaemonSetSpec_SurgeIdiomAccepted(t *testing.T) {
	for _, ru := range []map[string]any{
		{"maxUnavailable": 0, "maxSurge": 2},
		{"maxUnavailable": "0%", "maxSurge": "50%"},
	} {
		if _, err := parseDaemonSetSpec(map[string]any{
			"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": ru},
		}); err != nil {
			t.Errorf("rollingUpdate %v: parseDaemonSetSpec: %v", ru, err)
		}
	}
}

func TestParseDaemonSetSpec_Errors(t *testing.T) {
	rolling := func(ru map[string]any) map[string]any {
		return map[string]any{"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": ru}}
	}
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"selector rejected", map[string]any{"selector": map[string]any{}}, "selector: not authorable"},
		{"unknown key in updateStrategy", map[string]any{"updateStrategy": map[string]any{"type": "OnDelete", "partition": 1}}, `unrecognized key "partition"`},
		{"unknown key in rollingUpdate", rolling(map[string]any{"partition": 1}), `unrecognized key "partition"`},
		{"updateStrategy not an object", map[string]any{"updateStrategy": "RollingUpdate"}, "updateStrategy: must be an object"},
		{"updateStrategy type missing", map[string]any{"updateStrategy": map[string]any{}}, "updateStrategy.type: required"},
		{"updateStrategy type invalid", map[string]any{"updateStrategy": map[string]any{"type": "Recreate"}}, "updateStrategy.type: invalid value"},
		{
			"rollingUpdate under OnDelete",
			map[string]any{"updateStrategy": map[string]any{"type": "OnDelete", "rollingUpdate": map[string]any{"maxUnavailable": 1}}},
			"only allowed for updateStrategy.type RollingUpdate",
		},
		// The two halves of ValidateRollingUpdateDaemonSet's
		// exactly-one-non-zero rule, both authored.
		{
			"both maxUnavailable and maxSurge non-zero",
			rolling(map[string]any{"maxUnavailable": 1, "maxSurge": 1}),
			"maxSurge: may not be non-zero while maxUnavailable is non-zero (authored as 1)",
		},
		{
			"both zero as integers",
			rolling(map[string]any{"maxUnavailable": 0, "maxSurge": 0}),
			"maxUnavailable: cannot be 0 while maxSurge is 0 (authored as 0)",
		},
		{
			// The percentage spelling of zero, which upstream's
			// getIntOrPercentValue also reads as zero.
			"both zero as percentages",
			rolling(map[string]any{"maxUnavailable": "0%", "maxSurge": "0%"}),
			`maxUnavailable: cannot be 0 while maxSurge is 0 (authored as 0%)`,
		},
		{
			// Both non-zero in the percentage spelling. Distinct from the
			// integer case above because intstr's own IntValue() returns 0 for
			// any string it cannot Atoi — "25%" included — so a zero-check that
			// forgets the percentage form reads BOTH of these as zero and
			// reports the opposite error.
			"both non-zero as percentages",
			rolling(map[string]any{"maxUnavailable": "25%", "maxSurge": "30%"}),
			`maxSurge: may not be non-zero while maxUnavailable is non-zero (authored as 25%)`,
		},
		{"maxUnavailable negative", rolling(map[string]any{"maxUnavailable": -1}), "maxUnavailable: must be >= 0"},
		{"maxSurge negative", rolling(map[string]any{"maxSurge": -1}), "maxSurge: must be >= 0"},
		{"maxUnavailable over 100 percent", rolling(map[string]any{"maxUnavailable": "150%"}), "must not be greater than 100%"},
		{"maxSurge over 100 percent", rolling(map[string]any{"maxSurge": "150%"}), "must not be greater than 100%"},
		{"maxUnavailable signed percentage", rolling(map[string]any{"maxUnavailable": "+50%"}), "string value must be a percentage"},
		{"maxUnavailable bare string", rolling(map[string]any{"maxUnavailable": "half"}), "string value must be a percentage"},
		{"maxUnavailable overflowing percentage", rolling(map[string]any{"maxUnavailable": "99999999999999999999%"}), "must not be greater than 100%"},
		{"maxUnavailable wrong type", rolling(map[string]any{"maxUnavailable": []any{1}}), "must be a non-negative integer or a percentage string"},
		{"minReadySeconds negative", map[string]any{"minReadySeconds": -1}, "minReadySeconds: must be >= 0"},
		{"revisionHistoryLimit negative", map[string]any{"revisionHistoryLimit": -1}, "revisionHistoryLimit: must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDaemonSetSpec(tc.props)
			if err == nil {
				t.Fatalf("parseDaemonSetSpec(%v) succeeded, want error containing %q", tc.props, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDaemonSetSpec_RejectedKeyOrderIsDeterministic: the rejected-key scan sorts
// its keys, so a document authoring several rejected keys names the same one
// every run. Vacuously true today with a single rejected key, and the guard
// that keeps it true when a second is added.
func TestDaemonSetSpec_RejectedKeyOrderIsDeterministic(t *testing.T) {
	props := map[string]any{}
	for key := range daemonSetSpecRejectedKeys {
		props[key] = map[string]any{}
	}
	_, first := parseDaemonSetSpec(props)
	if first == nil {
		t.Fatal("parseDaemonSetSpec succeeded, want a rejected-key error")
	}
	for range 50 {
		_, err := parseDaemonSetSpec(props)
		if err == nil || err.Error() != first.Error() {
			t.Fatalf("error varies between runs: %v then %v", first, err)
		}
	}
}

func TestDaemonSetSpecConfig_ApplyLeavesUnauthoredAlone(t *testing.T) {
	ds := &appsv1.DaemonSet{}
	ds.Spec.MinReadySeconds = 7
	ds.Spec.UpdateStrategy = appsv1.DaemonSetUpdateStrategy{Type: appsv1.OnDeleteDaemonSetStrategyType}

	DaemonSetSpecConfig{}.apply(ds)

	if ds.Spec.MinReadySeconds != 7 {
		t.Errorf("MinReadySeconds = %d, want the constructor's 7", ds.Spec.MinReadySeconds)
	}
	if ds.Spec.UpdateStrategy.Type != appsv1.OnDeleteDaemonSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want the constructor's OnDelete", ds.Spec.UpdateStrategy.Type)
	}
	if ds.Spec.RevisionHistoryLimit != nil {
		t.Errorf("RevisionHistoryLimit = %v, want nil", ds.Spec.RevisionHistoryLimit)
	}
}
