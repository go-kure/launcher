package components

import "testing"

// TestKubeletArgument pins kubeletArgument to Expand and tryReadVariableName
// (third_party/forked/golang/expansion/expand.go:39-105 at Kubernetes
// v1.37.1), whose output Alertmanager reads.
func TestKubeletArgument(t *testing.T) {
	cases := []struct {
		name, in, want string
		refers         bool
	}{
		{"no operator", "10.0.0.1:9094", "10.0.0.1:9094", false},
		{"a lone trailing $", "10.0.0.1:9094$", "10.0.0.1:9094$", false},
		{"an escaped $", "$$(POD_IP)", "$(POD_IP)", false},
		{"an unclosed reference", "[$(POD_IP]:9094", "[$(POD_IP]:9094", false},
		{"a closer before the opener", ")$(", ")$(", false},
		{"$ before another byte", "$x:9094", "$x:9094", false},
		// The kubelet decodes the rune after $ and writes it as decoded.
		{"$ before a multibyte rune", "$é", "$é", false},
		{"$ before an invalid byte", "$\xff", "$�", false},
		{"a complete reference", "[$(POD_IP)]:9094", "", true},
		{"a reference after an escape", "$$$(X)", "", true},
		// No variable has an empty name or one with '=', a rune beyond ASCII
		// or one not printable, so the kubelet passes such a reference as
		// written, and reads on after it.
		{"an empty reference", "$()", "$()", false},
		{"an empty reference in brackets", "[$()]:9094", "[$()]:9094", false},
		{"a reference of a name with =", "[$(a=b)]:9094", "[$(a=b)]:9094", false},
		{"a reference of a name beyond ASCII", "[$(é)]:9094", "[$(é)]:9094", false},
		{"a reference of a name with a tab", "$(a\tb)", "$(a\tb)", false},
		{"a reference after one no variable has", "$()$(POD_IP)", "", true},
		{"an unclosed reference after one no variable has", "$(=)$(POD_IP", "$(=)$(POD_IP", false},
		// A reference ends at the first ')': here that of the later $(PORT),
		// so the name is "a=]:$(PORT", which no variable has.
		{"an opener before the closer of a later reference", "[$(a=]:$(PORT)", "[$(a=]:$(PORT)", false},
		{"an opener before the closer of a later reference without =", "[$(x]:$(PORT)", "", true},
		// The first ')' ends the first reference, so the second is read.
		{"a reference after one no variable has, closed first", "$(a=b)$(PORT)", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, refers := kubeletArgument(c.in)
			if got != c.want || refers != c.refers {
				t.Errorf("kubeletArgument(%q) = %q, %v; want %q, %v", c.in, got, refers, c.want, c.refers)
			}
		})
	}
}
