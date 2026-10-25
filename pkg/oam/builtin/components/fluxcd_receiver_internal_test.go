package components

import (
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestReceiverDefaultAPIVersionKinds_PinsTheControllerDefaults holds the list
// to the kinds notification-controller v1.9.4 defaults an apiVersion for
// (defaultFluxAPIVersions in internal/server/receiver_handlers.go), so that a
// kind dropped or misspelled in the list fails here. This second copy is
// deliberate: a Flux upgrade that changes the defaults edits both.
func TestReceiverDefaultAPIVersionKinds_PinsTheControllerDefaults(t *testing.T) {
	want := []string{"Bucket", "GitRepository", "HelmChart", "HelmRepository", "OCIRepository", "ImageRepository", "ArtifactGenerator"}
	if !slices.Equal(receiverDefaultAPIVersionKinds, want) {
		t.Errorf("receiverDefaultAPIVersionKinds = %v, want %v", receiverDefaultAPIVersionKinds, want)
	}
}

// TestReceiverDefaultAPIVersionKinds_AcceptedUnversioned holds every kind of
// receiverDefaultAPIVersionKinds to being accepted without an apiVersion, so
// that a kind added to the list is exercised with no second edit
// (go-kure/launcher#984).
func TestReceiverDefaultAPIVersionKinds_AcceptedUnversioned(t *testing.T) {
	h := &FluxcdReceiverHandler{}
	for _, kind := range receiverDefaultAPIVersionKinds {
		t.Run(kind, func(t *testing.T) {
			props := map[string]any{
				"type":      "github",
				"resources": []any{map[string]any{"kind": kind, "name": "*"}},
			}
			if _, err := h.ToApplicationConfig(&oam.Component{Name: "hook", Type: fluxcdReceiverType, Properties: props}, "default"); err != nil {
				t.Errorf("ToApplicationConfig = %v, want no refusal for an unversioned %s", err, kind)
			}
		})
	}
}
