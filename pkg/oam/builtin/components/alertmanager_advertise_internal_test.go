package components

import "testing"

// TestKubeletArgument pins kubeletArgument to Expand and tryReadVariableName
// (third_party/forked/golang/expansion/expand.go:35-102 at Kubernetes
// v1.35.0), whose output Alertmanager reads.
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
		// string(input[0]) there writes a byte above 0x7F as the rune of its
		// value: the first byte of é (c3 a9) becomes c3 83.
		{"$ before a byte above 0x7F", "$é", "$\xc3\x83\xa9", false},
		{"a complete reference", "[$(POD_IP)]:9094", "", true},
		{"an empty reference", "$()", "", true},
		{"a reference after an escape", "$$$(X)", "", true},
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
