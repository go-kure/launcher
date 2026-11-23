package components_test

import (
	"errors"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestExternalSecretKinds_TargetManifestRefusalIsTold: a consumer tells the
// Secret an ExternalSecret or ClusterExternalSecret would write from every
// other explicit-secret refusal by the type, never by the text. The class stays
// explicit-secret.
func TestExternalSecretKinds_TargetManifestRefusalIsTold(t *testing.T) {
	manifest := func(at, apiVersion, kind string) map[string]any {
		return externalSecretIn(at, map[string]any{"target": map[string]any{
			"manifest": map[string]any{"apiVersion": apiVersion, "kind": kind},
		}})
	}

	for _, kind := range externalSecrets {
		t.Run(kind.component+"/a Secret", func(t *testing.T) {
			_, err := pvTransform(kind.component, kind.handler, manifest(kind.at, "v1", "Secret"), esForbidding())
			rcWantClass(t, err, oam.RefusalExplicitSecret)

			var told *oam.TargetManifestRefusal
			if !errors.As(err, &told) {
				t.Fatalf("the refusal is not an *oam.TargetManifestRefusal: %v", err)
			}
			if told.At != kind.at {
				t.Errorf("At = %q, want %q", told.At, kind.at)
			}
		})

		// The other refusal of a target.manifest has a class of its own and is
		// not the type.
		t.Run(kind.component+"/a kind the policy reads", func(t *testing.T) {
			_, err := pvTransform(kind.component, kind.handler, manifest(kind.at, "apps/v1", "Deployment"), esForbidding())
			rcWantClass(t, err, oam.RefusalUnreadableObject)

			var told *oam.TargetManifestRefusal
			if errors.As(err, &told) {
				t.Errorf("a refusal of class unreadable-object is an *oam.TargetManifestRefusal: %v", err)
			}
		})
	}

	// The same Secret carried as an object is the same refusal in the same
	// class, and not the type.
	t.Run("passthrough/a Secret", func(t *testing.T) {
		_, err := ptTransform(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "written"}}, esForbidding())

		var refusal *oam.PolicyRefusal
		if !errors.As(err, &refusal) || refusal.Class != oam.RefusalExplicitSecret {
			t.Fatalf("want a refusal of class %q, got %v", oam.RefusalExplicitSecret, err)
		}
		var told *oam.TargetManifestRefusal
		if errors.As(err, &told) {
			t.Errorf("the refusal of a Secret passthrough carries is an *oam.TargetManifestRefusal: %v", err)
		}
	})
}
