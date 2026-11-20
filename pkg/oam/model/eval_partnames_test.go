package model

import (
	"strings"
	"testing"
)

// TestEval_ABoundNameThatIsNotAStringIsRefused: a read of the name of a record is typed as a
// string, so `parts.a.name + parts.b.name` is text. A scope handed to Eval that binds a number, a
// null or a list as a name would make that sum a number, so the read refuses it and names the
// type, and a string name passes.
func TestEval_ABoundNameThatIsNotAStringIsRefused(t *testing.T) {
	m := celBase(t)
	m.Spec.Parts = append(m.Spec.Parts, Part{ID: "cfg", Type: "configmap@v1",
		Properties: map[string]any{"p": "${ parts.app.name + parts.app.name }"}})
	c, err := Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	const site = "spec.parts[1].properties.p"
	for name, tc := range map[string]struct {
		name any
		want string // the type the refusal names; empty when the name is accepted
	}{
		"a string":  {"a", ""},
		"an int":    {1, "int"},
		"a float":   {1.5, "float64"},
		"a null":    {nil, "<nil>"},
		"a list":    {[]any{"a"}, "[]interface {}"},
		"a boolean": {true, "bool"},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := c.Eval(site, Scope{Parts: map[string]map[string]any{"app": {"name": tc.name}}})
			if tc.want == "" {
				if err != nil || got != "aa" {
					t.Fatalf("Eval = %v, %v, want aa", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Eval accepted the name %v and gave %v", tc.name, got)
			}
			for _, want := range []string{"parts.app.name", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
