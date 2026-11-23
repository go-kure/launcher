package oam

import (
	"errors"
	"fmt"
	"testing"
)

// TestTargetManifestRefusal pins what a consumer may rely on: the type is found
// through errors.As, however the violation wraps it, whatever the refusal's
// text says; it keeps the class; and its text is the one the refusal had before
// the type existed.
func TestTargetManifestRefusal(t *testing.T) {
	const text = "the object is a Secret, and the environment policy forbids explicit secrets; reference a Secret created out of band instead"
	refusal := NewPolicyRefusal(RefusalExplicitSecret, text)

	for _, at := range []string{"", "externalSecretSpec."} {
		t.Run("at "+at, func(t *testing.T) {
			err := &TargetManifestRefusal{At: at, Cause: refusal}

			if want := at + "target.manifest: " + text; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
			if !errors.Is(err, refusal) {
				t.Errorf("errors.Is(err, refusal) is false: Unwrap() = %v", errors.Unwrap(err))
			}

			v := NewViolationError("web", fmt.Errorf("outer: %w", err))
			if v.Class != RefusalExplicitSecret {
				t.Errorf("Class = %q, want %q: the type must keep the class", v.Class, RefusalExplicitSecret)
			}
			var found *TargetManifestRefusal
			if !errors.As(v, &found) || found.At != at {
				t.Errorf("errors.As on the violation = %v with At %q, want the refusal with At %q", found != nil, at, at)
			}
			var inner *PolicyRefusal
			if !errors.As(v, &inner) || inner.Class != RefusalExplicitSecret {
				t.Errorf("errors.As(*PolicyRefusal) must still reach the refusal of class %q, got %v", RefusalExplicitSecret, inner)
			}
		})
	}

	t.Run("never read from the text", func(t *testing.T) {
		var found *TargetManifestRefusal

		plain := errors.New("target.manifest: " + text)
		if errors.As(NewViolationError("web", plain), &found) {
			t.Errorf("a plain error that has the old text was taken for the type: %v", found)
		}
		bare := NewPolicyRefusal(RefusalExplicitSecret, text)
		if errors.As(NewViolationError("web", bare), &found) {
			t.Errorf("the same refusal, not in a target.manifest, was taken for the type: %v", found)
		}

		reworded := &TargetManifestRefusal{Cause: NewPolicyRefusal(RefusalExplicitSecret, "reworded")}
		if !errors.As(NewViolationError("web", reworded), &found) {
			t.Error("a refusal with other words was not taken for the type")
		}
	})
}
