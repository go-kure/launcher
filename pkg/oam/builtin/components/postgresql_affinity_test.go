package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// postgresqlConfigFor runs the handler and returns the config, or the error, without
// failing the test — the rejection cases below assert on the error itself.
func postgresqlConfigFor(t *testing.T, props map[string]any) (*components.PostgresqlConfig, error) {
	t.Helper()
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       "db",
		Type:       "postgresql",
		Properties: props,
	}, "default")
	if err != nil {
		return nil, err
	}
	return cfg.(*components.PostgresqlConfig), nil
}

// TestPostgresqlAffinity_WrongTypeIsRejected covers go-kure/launcher#448: every read in
// the handler's affinity block used a bare comma-ok, so a wrongly typed block or
// sub-field was silently discarded and the emitted cluster kept a default that the
// document did not ask for.
//
// Each case is paired with a control in TestPostgresqlAffinity_AbsenceAndDefaults —
// "absent" and "present but wrong type" were indistinguishable before this change, so a
// test that only pins rejection passes for the wrong reason if absence regresses into
// an error.
func TestPostgresqlAffinity_WrongTypeIsRejected(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			name:  "affinity as a list",
			props: map[string]any{"affinity": []any{map[string]any{"topologyKey": "zone"}}},
			want:  "affinity: must be an object",
		},
		{
			name:  "affinity as a string",
			props: map[string]any{"affinity": "enabled"},
			want:  "affinity: must be an object",
		},
		{
			name:  "enablePodAntiAffinity as a string",
			props: map[string]any{"affinity": map[string]any{"enablePodAntiAffinity": "yes"}},
			want:  "affinity.enablePodAntiAffinity: must be a boolean",
		},
		{
			name:  "topologyKey as a number",
			props: map[string]any{"affinity": map[string]any{"topologyKey": float64(123)}},
			want:  "affinity.topologyKey: must be a string",
		},
		{
			name:  "podAntiAffinityType as a number",
			props: map[string]any{"affinity": map[string]any{"podAntiAffinityType": float64(1)}},
			want:  "affinity.podAntiAffinityType: must be a string",
		},
		{
			name:  "nodeSelector as a list",
			props: map[string]any{"affinity": map[string]any{"nodeSelector": []any{"zone"}}},
			want:  "affinity.nodeSelector: must be an object",
		},
		{
			// Not a type error: a well-typed string outside the accepted set. The shared
			// parseAffinity has always refused this; the postgresql handler forwarded it
			// to the emitted cluster verbatim.
			name:  "podAntiAffinityType outside the enum",
			props: map[string]any{"affinity": map[string]any{"podAntiAffinityType": "banana"}},
			want:  `affinity.podAntiAffinityType: invalid value "banana"`,
		},
		{
			// An explicit empty string must reach the enum check rather than fall back to
			// the default, which is why podAntiAffinityType is not read with
			// parseStringField (that helper reports "" as absent).
			name:  "podAntiAffinityType explicitly empty",
			props: map[string]any{"affinity": map[string]any{"podAntiAffinityType": ""}},
			want:  `affinity.podAntiAffinityType: invalid value ""`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := postgresqlConfigFor(t, tc.props)
			if err == nil {
				t.Fatalf("expected an error for %s, got none", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestPostgresqlAffinity_AbsenceAndDefaults is the control set for the rejection cases
// above: absence must stay absence, and a present-but-empty block must produce the same
// defaults the shared parseAffinity produces.
func TestPostgresqlAffinity_AbsenceAndDefaults(t *testing.T) {
	t.Run("absent key emits no affinity", func(t *testing.T) {
		cfg, err := postgresqlConfigFor(t, map[string]any{})
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if cfg.AffinityEnabled {
			t.Error("AffinityEnabled = true for a document with no affinity key")
		}
	})

	t.Run("empty block takes every default", func(t *testing.T) {
		cfg, err := postgresqlConfigFor(t, map[string]any{"affinity": map[string]any{}})
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if !cfg.AffinityEnabled {
			t.Error("AffinityEnabled = false for an authored affinity block")
		}
		if !cfg.AffinityEnablePodAntiAffinity {
			t.Error("AffinityEnablePodAntiAffinity = false, want the true default")
		}
		if got, want := cfg.AffinityTopologyKey, "kubernetes.io/hostname"; got != want {
			t.Errorf("AffinityTopologyKey = %q, want %q", got, want)
		}
		// Before #448 this was "" — the handler set no default, so an omitted
		// podAntiAffinityType reached the emitted cluster as an empty string while every
		// other kind emitted "preferred".
		if got, want := cfg.AffinityPodAntiAffinityType, "preferred"; got != want {
			t.Errorf("AffinityPodAntiAffinityType = %q, want %q", got, want)
		}
	})

	t.Run("an authored empty topologyKey keeps the default", func(t *testing.T) {
		// parseStringField reports "" as absent, matching parseAffinity's `ok && v != ""`.
		cfg, err := postgresqlConfigFor(t, map[string]any{"affinity": map[string]any{"topologyKey": ""}})
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if got, want := cfg.AffinityTopologyKey, "kubernetes.io/hostname"; got != want {
			t.Errorf("AffinityTopologyKey = %q, want %q", got, want)
		}
	})

	t.Run("a fully authored block is unchanged", func(t *testing.T) {
		cfg, err := postgresqlConfigFor(t, map[string]any{
			"affinity": map[string]any{
				"enablePodAntiAffinity": false,
				"topologyKey":           "topology.kubernetes.io/zone",
				"podAntiAffinityType":   "required",
				"nodeSelector":          map[string]any{"workload-type": "database"},
			},
		})
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if cfg.AffinityEnablePodAntiAffinity {
			t.Error("AffinityEnablePodAntiAffinity = true, want the authored false")
		}
		if got, want := cfg.AffinityTopologyKey, "topology.kubernetes.io/zone"; got != want {
			t.Errorf("AffinityTopologyKey = %q, want %q", got, want)
		}
		if got, want := cfg.AffinityPodAntiAffinityType, "required"; got != want {
			t.Errorf("AffinityPodAntiAffinityType = %q, want %q", got, want)
		}
		if got, want := cfg.AffinityNodeSelector["workload-type"], "database"; got != want {
			t.Errorf("AffinityNodeSelector[workload-type] = %q, want %q", got, want)
		}
	})
}
