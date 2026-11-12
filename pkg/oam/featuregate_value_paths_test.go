package oam

import (
	"strings"
	"testing"
)

// These tests call checkValueGatedPaths directly (go-kure/launcher#1330), so the exact
// match, the walk and the refusal text are held without a component handler between
// the test and the check.

var restartValuePaths = map[string]map[string]string{
	"restartPolicy": {"Never": "ContainerRestartRules", "OnFailure": "ContainerRestartRules"},
}

func TestCheckValueGatedPaths(t *testing.T) {
	below := gateTarget{version: KubernetesVersion{Major: 1, Minor: 34}}
	at := gateTarget{version: KubernetesVersion{Major: 1, Minor: 35}}

	cases := []struct {
		name    string
		value   any
		target  gateTarget
		refused bool
		want    []string
	}{
		// The control: a named value below the release is refused, so the accepting
		// rows below can only pass because the check ignores them.
		{name: "Never below the release is refused", value: map[string]any{"restartPolicy": "Never"}, target: below, refused: true,
			want: []string{".restartPolicy", `value "Never"`, "ContainerRestartRules", "1.35", "1.34"}},
		{name: "OnFailure below the release is refused", value: map[string]any{"restartPolicy": "OnFailure"}, target: below, refused: true,
			want: []string{`value "OnFailure"`}},
		{name: "Never at the release is accepted", value: map[string]any{"restartPolicy": "Never"}, target: at},
		// R19: the match is exact, as the Enum a ValueGates key is held to.
		{name: "never in lower case is no match", value: map[string]any{"restartPolicy": "never"}, target: below},
		{name: "NEVER in upper case is no match", value: map[string]any{"restartPolicy": "NEVER"}, target: below},
		{name: "a string the table does not name", value: map[string]any{"restartPolicy": "Always"}, target: below},
		// R9: only a string is looked up.
		{name: "a number", value: map[string]any{"restartPolicy": 1}, target: below},
		{name: "a bool", value: map[string]any{"restartPolicy": true}, target: below},
		{name: "a list", value: map[string]any{"restartPolicy": []any{"Never"}}, target: below},
		{name: "a null", value: map[string]any{"restartPolicy": nil}, target: below},
		{name: "the key is absent", value: map[string]any{"name": "x"}, target: below},
		{name: "the value is no object", value: "Never", target: below},
		// The key matches case-insensitively and the refusal names the authored spelling.
		{name: "the key in another case", value: map[string]any{"RestartPolicy": "Never"}, target: below, refused: true,
			want: []string{".RestartPolicy"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkValueGatedPaths(restartValuePaths, tc.value, "spec.initContainers[0]", tc.target)
			if !tc.refused {
				if err != nil {
					t.Fatalf("want no refusal, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want a refusal, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestCheckValueGatedPaths_WalksEveryArrayElement(t *testing.T) {
	paths := map[string]map[string]string{
		"spec.initContainers[].restartPolicy": restartValuePaths["restartPolicy"],
	}
	value := map[string]any{"spec": map[string]any{"initContainers": []any{
		map[string]any{"name": "a", "restartPolicy": "Always"},
		map[string]any{"name": "b", "restartPolicy": "Never"},
	}}}
	below := gateTarget{version: KubernetesVersion{Major: 1, Minor: 34}}
	err := checkValueGatedPaths(paths, value, "template", below)
	if err == nil {
		t.Fatal("want the second element refused, got none")
	}
	if !strings.Contains(err.Error(), "template.spec.initContainers[1].restartPolicy") {
		t.Errorf("refusal %q does not name the second element", err)
	}
	// The control: with the second element's value accepted, nothing is refused.
	value["spec"].(map[string]any)["initContainers"].([]any)[1].(map[string]any)["restartPolicy"] = "Always"
	if err := checkValueGatedPaths(paths, value, "template", below); err != nil {
		t.Errorf("want no refusal once both are Always, got %v", err)
	}
}

func TestCheckValueGatedPaths_NilTable(t *testing.T) {
	err := checkValueGatedPaths(nil, map[string]any{"restartPolicy": "Never"}, "x", gateTarget{version: KubernetesVersion{Major: 1, Minor: 1}})
	if err != nil {
		t.Fatalf("a schema with no ValueGatedPaths refuses nothing, got %v", err)
	}
}
