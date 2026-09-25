package components

import (
	"reflect"
	"strings"
	"testing"
)

// parseAffinity validated the outer `affinity` object but read all four of its
// sub-fields with a bare comma-ok type assertion, so a sub-field authored with
// the wrong type was discarded and the default emitted as though the key had
// never been written: `topologyKey: 123` became kubernetes.io/hostname,
// `enablePodAntiAffinity: "yes"` became false (go-kure/launcher#452). The
// postgresql handler's own affinity block was fixed the same way in
// go-kure/launcher#448; these cases mirror postgresql_affinity_test.go so the
// two readers stay converged.
//
// Each rejection case targets exactly one sub-field read, so reverting any one
// read fails its own subtest and no other.
func TestParseAffinity_WrongTypedSubFieldIsRejected(t *testing.T) {
	cases := []struct {
		name string
		aff  map[string]any
		want string
	}{
		{
			name: "enablePodAntiAffinity as a string",
			aff:  map[string]any{"enablePodAntiAffinity": "yes"},
			want: "affinity.enablePodAntiAffinity: must be a boolean",
		},
		{
			name: "topologyKey as a number",
			aff:  map[string]any{"topologyKey": float64(123)},
			want: "affinity.topologyKey: must be a string",
		},
		{
			name: "podAntiAffinityType as a number",
			aff:  map[string]any{"podAntiAffinityType": float64(1)},
			want: "affinity.podAntiAffinityType: must be a string",
		},
		{
			name: "nodeSelector as a list",
			aff:  map[string]any{"nodeSelector": []any{"zone"}},
			want: "affinity.nodeSelector: must be an object",
		},
		{
			// The envelope check alone would still drop a non-string value inside
			// it, reaching the cluster with a narrower selector than authored.
			name: "nodeSelector with a non-string value",
			aff:  map[string]any{"nodeSelector": map[string]any{"zone": "a", "rack": 3}},
			want: `affinity.nodeSelector["rack"]: must be a string`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAffinity(map[string]any{"affinity": tc.aff})
			if err == nil {
				t.Fatalf("parseAffinity(%v) = nil error; the value would be discarded for a default", tc.aff)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// The behaviours that were already right, and must survive the conversion.
func TestParseAffinity_PreservedBehaviour(t *testing.T) {
	defaults := AffinityConfig{TopologyKey: "kubernetes.io/hostname", PodAntiAffinityType: "preferred"}

	accepted := []struct {
		name string
		aff  map[string]any
		want AffinityConfig
	}{
		{"empty block takes the defaults", map[string]any{}, defaults},
		{"authored empty topologyKey keeps the default", map[string]any{"topologyKey": ""}, defaults},
		{
			// A null sub-field is absence (go-kure/launcher#444's nested-null
			// contract), not a wrong type.
			name: "null sub-fields take the defaults",
			aff: map[string]any{
				"enablePodAntiAffinity": nil,
				"topologyKey":           nil,
				"podAntiAffinityType":   nil,
				"nodeSelector":          map[string]any(nil),
			},
			want: defaults,
		},
		{
			// A null nodeSelector VALUE is absence too: the key is left out of the
			// selector rather than refused as a non-string value.
			name: "null nodeSelector value leaves the key out",
			aff:  map[string]any{"nodeSelector": map[string]any{"zone": "a", "rack": nil}},
			want: AffinityConfig{
				TopologyKey:         "kubernetes.io/hostname",
				PodAntiAffinityType: "preferred",
				NodeSelector:        map[string]string{"zone": "a"},
			},
		},
		{
			name: "fully authored block is honoured",
			aff: map[string]any{
				"enablePodAntiAffinity": true,
				"topologyKey":           "topology.kubernetes.io/zone",
				"podAntiAffinityType":   "required",
				"nodeSelector":          map[string]any{"workload": "batch"},
			},
			want: AffinityConfig{
				EnablePodAntiAffinity: true,
				TopologyKey:           "topology.kubernetes.io/zone",
				PodAntiAffinityType:   "required",
				NodeSelector:          map[string]string{"workload": "batch"},
			},
		},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAffinity(map[string]any{"affinity": tc.aff})
			if err != nil {
				t.Fatalf("parseAffinity: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseAffinity = %+v, want %+v", got, tc.want)
			}
		})
	}

	// A well-typed podAntiAffinityType outside the enum, and an explicit empty
	// one, are both refused by name: the empty string reaches the enum check
	// rather than being read as absent and falling back to "preferred".
	for _, v := range []string{"banana", ""} {
		t.Run(`podAntiAffinityType "`+v+`" is refused`, func(t *testing.T) {
			_, err := parseAffinity(map[string]any{"affinity": map[string]any{"podAntiAffinityType": v}})
			if err == nil {
				t.Fatalf("podAntiAffinityType %q accepted", v)
			}
			if want := `affinity.podAntiAffinityType: invalid value "` + v + `"`; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), want)
			}
		})
	}
}
