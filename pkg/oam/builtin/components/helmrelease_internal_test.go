package components

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// TestBoundedResourceName_TruncationPreservesUniqueness pins that two distinct
// valid component names (each within validate.go's 253-byte DNS-1123 max)
// sharing a long common prefix still produce distinct names once truncated.
// Component names are unique only in full (validate.go's duplicate-name
// check), so a plain truncation would map both to the identical name — one
// object silently clobbering the other's.
func TestBoundedResourceName_TruncationPreservesUniqueness(t *testing.T) {
	shared := strings.Repeat("a", 246)
	nameA := shared + strings.Repeat("b", 7) // 253 bytes
	nameB := shared + strings.Repeat("c", 7) // 253 bytes, same 246-byte prefix
	gotA := boundedResourceName(nameA, "-values")
	gotB := boundedResourceName(nameB, "-values")
	if gotA == gotB {
		t.Fatalf("boundedResourceName collided: %q and %q both produced %q", nameA, nameB, gotA)
	}
	for _, got := range []string{gotA, gotB} {
		if !strings.HasSuffix(got, "-values") {
			t.Errorf("%q lost its suffix", got)
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) != 0 {
			t.Errorf("IsDNS1123Subdomain(%q) = %v, want no errors", got, errs)
		}
	}
}
