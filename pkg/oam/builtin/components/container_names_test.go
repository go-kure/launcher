package components_test

import (
	"strings"
	"testing"
)

// These tests cover go-kure/launcher#1226: the API server refuses a pod whose
// containers share a name, but nothing checked the names across the main
// container (named after the component), `initContainers` and `sidecars`, nor
// two entries of one list, so the document generated and failed at apply.

// cnEntry is a minimal init container or sidecar entry.
func cnEntry(name string) map[string]any {
	return map[string]any{"name": name, "image": "ghcr.io/org/helper:v1"}
}

// cnCases are the refused name pairs. The component is named "app" (see
// kindConvertErr), so the main container is "app". needsSidecars marks a case
// that authors `sidecars`, which job and cronjob do not take.
var cnCases = []struct {
	name          string
	extra         map[string]any
	want          string
	needsSidecars bool
}{
	{"init vs main", map[string]any{"initContainers": []any{cnEntry("app")}},
		`initContainers[0] "app": container name "app" is also used by the main container "app"`, false},
	{"init vs init", map[string]any{"initContainers": []any{cnEntry("a"), cnEntry("b"), cnEntry("a")}},
		`initContainers[2] "a": container name "a" is also used by initContainers[0] "a"`, false},
	{"sidecar vs main", map[string]any{"sidecars": []any{cnEntry("app")}},
		`sidecars[0] "app": container name "app" is also used by the main container "app"`, true},
	{"sidecar vs sidecar", map[string]any{"sidecars": []any{cnEntry("a"), cnEntry("a")}},
		`sidecars[1] "a": container name "a" is also used by sidecars[0] "a"`, true},
	{"init vs sidecar", map[string]any{"initContainers": []any{cnEntry("x"), cnEntry("proxy")}, "sidecars": []any{cnEntry("proxy")}},
		`sidecars[0] "proxy": container name "proxy" is also used by initContainers[1] "proxy"`, true},
	{"first repeat is reported: main before init before sidecar", map[string]any{
		"initContainers": []any{cnEntry("app"), cnEntry("p")}, "sidecars": []any{cnEntry("p"), cnEntry("app")}},
		`initContainers[0] "app": container name "app" is also used by the main container "app"`, true},
}

// TestContainerNames_Refused pins each refusal on every kind that authors the
// lists it names, with both entries named.
func TestContainerNames_Refused(t *testing.T) {
	for _, k := range workloadKinds {
		for _, c := range cnCases {
			if c.needsSidecars && !sidecarKinds[k.name] {
				continue
			}
			t.Run(k.name+"/"+c.name, func(t *testing.T) {
				err := kindConvertErr(k.handler, k.name, withProps(k.props, c.extra))
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("error = %v, want one containing %q", err, c.want)
				}
				if strings.Contains(err.Error(), "policy") {
					t.Errorf("error = %v, want a plain error, not a policy refusal", err)
				}
			})
		}
	}
}

// TestContainerNames_Accepted: distinct names, in any list, are accepted, and so
// is a name that differs only in case (a different string to the API server).
func TestContainerNames_Accepted(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			extra := map[string]any{"initContainers": []any{cnEntry("migrate"), cnEntry("App")}}
			if sidecarKinds[k.name] {
				extra["sidecars"] = []any{cnEntry("proxy"), cnEntry("logs")}
			}
			if err := kindConvertErr(k.handler, k.name, withProps(k.props, extra)); err != nil {
				t.Fatalf("error = %v, want distinct names accepted", err)
			}
		})
	}
}
