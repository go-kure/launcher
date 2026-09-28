package components

import (
	"fmt"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// A typed-nil ELEMENT (map[string]any(nil) inside an []any) asserts as an object
// with ok=true, so parseObjectList used to hand it to its caller as an empty
// object where an untyped null element is refused. For tolerations that empty
// object is an Exists toleration with no key, which tolerates every taint
// (go-kure/launcher#465). Both shapes now take the same refusal.
func TestParseObjectList_TypedNilElementRefusedLikeUntyped(t *testing.T) {
	run := func(elem any) error {
		_, _, err := parseObjectList(map[string]any{"imagePullSecrets": []any{elem}}, "imagePullSecrets")
		return err
	}
	untyped, typed := run(nil), run(map[string]any(nil))
	if untyped == nil {
		t.Fatal("untyped null element: want an error, got nil")
	}
	if fmt.Sprint(typed) != fmt.Sprint(untyped) {
		t.Fatalf("typed-nil element error = %v, untyped null element error = %v", typed, untyped)
	}
}

// The bespoke list readers that do not go through parseObjectList: each must give
// a typed-nil element (or, for successPolicy.rules, a typed-nil list) the untyped
// null's exact refusal.
func TestBespokeListReaders_TypedNilElementRefusedLikeUntyped(t *testing.T) {
	sidecar := func(ports any) map[string]any {
		return map[string]any{"sidecars": []any{map[string]any{"name": "s", "image": "ghcr.io/org/s:v1", "ports": ports}}}
	}
	cases := []struct {
		name  string
		typed any
		run   func(v any) error
	}{
		{"envFrom[0]", nil, func(v any) error { _, err := parseEnvFrom(map[string]any{"envFrom": []any{v}}); return err }},
		{"httpHeaders[0]", nil, func(v any) error { _, err := parseHTTPHeaders(map[string]any{"h": []any{v}}, "h"); return err }},
		{"volumes[0]", nil, func(v any) error { _, err := parseVolumes(map[string]any{"volumes": []any{v}}); return err }},
		{"sidecars[0].ports[0]", nil, func(v any) error { _, err := parseSidecars(sidecar([]any{v})); return err }},
		{"volumeMounts[0]", nil, func(v any) error {
			_, err := parseVolumeMountList(map[string]any{"volumeMounts": []any{v}}, "p")
			return err
		}},
		{"successPolicy.rules", []any(nil), func(v any) error {
			_, err := parseJobSuccessPolicy(map[string]any{"rules": v})
			return err
		}},
		{"successPolicy.rules[0]", nil, func(v any) error {
			_, err := parseJobSuccessPolicy(map[string]any{"rules": []any{v}})
			return err
		}},
		{"podFailurePolicy.rules[0]", nil, func(v any) error {
			_, err := parseJobPodFailurePolicy(map[string]any{"rules": []any{v}})
			return err
		}},
		{"onPodConditions[0]", nil, func(v any) error {
			_, err := parseJobPodFailurePolicyOnPodConditions([]any{v}, "c")
			return err
		}},
		{"volumeClaimTemplates[0]", nil, func(v any) error {
			_, err := parseVolumeClaimTemplates(map[string]any{"volumeClaimTemplates": []any{v}})
			return err
		}},
		{"helmchart valuesFrom[0]", nil, func(v any) error {
			_, err := (&HelmchartHandler{}).ToApplicationConfig(&oam.Component{Name: "c", Type: "helmchart",
				Properties: map[string]any{"valuesFrom": []any{v}}}, "ns")
			return err
		}},
		{"manifests scopeOverrides[0]", nil, func(v any) error {
			_, _, err := parseScopeOverrides(map[string]any{"scopeOverrides": []any{v}})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typed := tc.typed
			if typed == nil {
				typed = map[string]any(nil)
			}
			untyped, got := tc.run(nil), tc.run(typed)
			if untyped == nil {
				t.Fatal("untyped null: want an error, got nil")
			}
			if fmt.Sprint(got) != fmt.Sprint(untyped) {
				t.Fatalf("typed nil %T error = %v, untyped null error = %v", typed, got, untyped)
			}
		})
	}
}

func TestParseTolerations_TypedNilElementDoesNotTolerateEverything(t *testing.T) {
	tols, err := parseTolerations(map[string]any{"tolerations": []any{map[string]any(nil)}})
	if err == nil {
		t.Fatalf("typed-nil toleration element: want an error, got tolerations %+v", tols)
	}
}
