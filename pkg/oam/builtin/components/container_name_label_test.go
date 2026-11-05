package components_test

import (
	"fmt"
	"strings"
	"testing"
)

// These tests cover go-kure/launcher#1254: the API server holds every container
// name to a DNS-1123 label, and the main container's name was checked against
// it, but an `initContainers` or `sidecars` name got no check beyond being
// present, so a dotted, upper-case or over-long name generated and failed at
// apply.

// cnlBad are the refused names: a dot, upper case, a name of 64 characters (one
// over the limit), a leading and a trailing hyphen.
var cnlBad = []struct{ name, value string }{
	{"dotted", "a.b"},
	{"upper case", "Migrate"},
	{"64 characters", strings.Repeat("a", 64)},
	{"leading hyphen", "-init"},
	{"trailing hyphen", "init-"},
}

// TestContainerNameLabel_Refused pins the refusal on every kind that authors the
// lists, naming the entry and the name.
func TestContainerNameLabel_Refused(t *testing.T) {
	for _, k := range workloadKinds {
		for _, list := range []string{"initContainers", "sidecars"} {
			if list == "sidecars" && !sidecarKinds[k.name] {
				continue
			}
			for _, c := range cnlBad {
				t.Run(k.name+"/"+list+"/"+c.name, func(t *testing.T) {
					extra := map[string]any{list: []any{cnEntry("fine"), cnEntry(c.value)}}
					err := kindConvertErr(k.handler, k.name, withProps(k.props, extra))
					want := fmt.Sprintf("%s[1]: invalid container name %q", list, c.value)
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("error = %v, want one containing %q", err, want)
					}
				})
			}
		}
	}
}

// TestContainerNameLabel_Accepted: a name of 63 characters, a one-character name
// and a name with inner hyphens and digits are labels, and build.
func TestContainerNameLabel_Accepted(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			extra := map[string]any{"initContainers": []any{cnEntry(strings.Repeat("a", 63)), cnEntry("m"), cnEntry("db-migrate-2")}}
			if sidecarKinds[k.name] {
				extra["sidecars"] = []any{cnEntry(strings.Repeat("b", 63)), cnEntry("s"), cnEntry("log-shipper-1")}
			}
			if err := kindConvertErr(k.handler, k.name, withProps(k.props, extra)); err != nil {
				t.Fatalf("error = %v, want DNS-1123 label names accepted", err)
			}
		})
	}
}
