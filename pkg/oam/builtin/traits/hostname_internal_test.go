package traits

import (
	stderrors "errors"
	"testing"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
)

func TestMatchHostnameWildcard(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"", "anything.example.com", true},                    // empty pattern permits all
		{"*.apps.example.com", "foo.apps.example.com", true},  // one label
		{"*.apps.example.com", "a.b.apps.example.com", false}, // two labels
		{"*.apps.example.com", "apps.example.com", false},     // no label
		{"*.apps.example.com", "foo.apps.example.org", false}, // wrong suffix
		{"*.apps.example.com", "foo.apps.example.com.evil.com", false},
		{"exact.example.com", "exact.example.com", true},  // exact match
		{"exact.example.com", "other.example.com", false}, // exact mismatch
	}
	for _, c := range cases {
		if got := matchHostnameWildcard(c.pattern, c.host); got != c.want {
			t.Errorf("matchHostnameWildcard(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestValidateHostnames(t *testing.T) {
	if err := validateHostnames([]string{"anything.com"}, "", "c"); err != nil {
		t.Errorf("empty wildcard should skip validation, got %v", err)
	}
	if err := validateHostnames([]string{"a.apps.example.com"}, "*.apps.example.com", "c"); err != nil {
		t.Errorf("valid host rejected: %v", err)
	}
	err := validateHostnames([]string{"a.apps.example.com", "bad.other.com"}, "*.apps.example.com", "c")
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
}
