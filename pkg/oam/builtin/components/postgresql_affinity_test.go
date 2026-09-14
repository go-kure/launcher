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

// TestPostgresqlAffinity_NullEnvelopeIsAbsent covers the layer above every case in
// TestPostgresqlAffinity_WrongTypeIsRejected: the affinity key itself authored as a
// null. The two nil shapes disagreed, and in OPPOSITE directions, which is why neither
// half of that pair shows up as a plain missing case.
//
// An untyped nil — `affinity:` with nothing after it — failed the object assertion and
// became a hard error, while pkg/oam's own validatePropertyValue reads a null under an
// optional property as absence and passes it. So that document validated against the
// published schema and then failed to convert.
//
// A typed nil — what a Go lowering rule produces for an unset map — satisfied the same
// assertion with a nil map and reported PRESENT, switching pod anti-affinity on with
// every default. That is a scheduling constraint the document never asked for, from a
// value no document can distinguish from the first one.
func TestPostgresqlAffinity_NullEnvelopeIsAbsent(t *testing.T) {
	for shape, value := range map[string]any{
		"untyped nil": nil,
		"typed nil":   map[string]any(nil),
	} {
		t.Run(shape, func(t *testing.T) {
			cfg, err := postgresqlConfigFor(t, map[string]any{"affinity": value})
			if err != nil {
				t.Fatalf("a null affinity must read as absent, got error: %v", err)
			}
			if cfg.AffinityEnabled {
				t.Error("AffinityEnabled = true for a null affinity; a null must not enable a scheduling constraint")
			}
			if cfg.AffinityEnablePodAntiAffinity {
				t.Error("AffinityEnablePodAntiAffinity = true for a null affinity")
			}
			if cfg.AffinityTopologyKey != "" {
				t.Errorf("AffinityTopologyKey = %q for a null affinity, want empty", cfg.AffinityTopologyKey)
			}
		})
	}
}

// TestPostgresqlAffinity_NullSubFieldsAreStillRejected is the boundary control for the
// test above. Reading a null as absence applies to the affinity ENVELOPE, which is what
// the validator classifies; it is not a licence to accept a null anywhere inside the
// block. These sub-fields keep their existing wrong-type diagnostics, so the envelope
// fix cannot be mistaken for "nulls are fine everywhere".
func TestPostgresqlAffinity_NullSubFieldsAreStillRejected(t *testing.T) {
	for _, key := range []string{"enablePodAntiAffinity", "topologyKey", "nodeSelector"} {
		t.Run(key, func(t *testing.T) {
			_, err := postgresqlConfigFor(t, map[string]any{
				"affinity": map[string]any{key: nil},
			})
			if err == nil {
				t.Fatalf("a null %s inside a present affinity block must still be rejected", key)
			}
		})
	}
}

// TestPostgresqlAffinity_NodeSelectorValuesMustBeStrings closes the silent discard the
// rest of this file exists to remove, one level further down. The block rejected a
// wrongly-typed nodeSelector CONTAINER by name and then handed its CONTENTS to
// stringMap, which drops every non-string value without a word — so
// `nodeSelector: {rack: 3}` reached the emitted cluster as a nodeSelector with no rack
// constraint, which is the exact failure #448 is about.
func TestPostgresqlAffinity_NodeSelectorValuesMustBeStrings(t *testing.T) {
	_, err := postgresqlConfigFor(t, map[string]any{
		"affinity": map[string]any{
			"nodeSelector": map[string]any{"zone": "a", "rack": float64(3)},
		},
	})
	if err == nil {
		t.Fatal("a non-string nodeSelector value must be rejected; it was silently dropped, narrowing the emitted selector")
	}
	if !strings.Contains(err.Error(), "affinity.nodeSelector") {
		t.Errorf("error = %q, want it to name affinity.nodeSelector", err)
	}
	if !strings.Contains(err.Error(), "rack") {
		t.Errorf("error = %q, want it to name the offending key so an author can find it", err)
	}
}

// The control for the test above: an all-string nodeSelector must still round-trip
// every pair. A parser that rejected any multi-key selector would pass the test above.
func TestPostgresqlAffinity_ValidNodeSelectorRoundTrips(t *testing.T) {
	cfg, err := postgresqlConfigFor(t, map[string]any{
		"affinity": map[string]any{
			"nodeSelector": map[string]any{"zone": "a", "workload-type": "database"},
		},
	})
	if err != nil {
		t.Fatalf("a valid nodeSelector must parse, got: %v", err)
	}
	if got, want := len(cfg.AffinityNodeSelector), 2; got != want {
		t.Fatalf("AffinityNodeSelector has %d entries, want %d: %#v", got, want, cfg.AffinityNodeSelector)
	}
	if cfg.AffinityNodeSelector["zone"] != "a" || cfg.AffinityNodeSelector["workload-type"] != "database" {
		t.Errorf("AffinityNodeSelector = %#v", cfg.AffinityNodeSelector)
	}
}
