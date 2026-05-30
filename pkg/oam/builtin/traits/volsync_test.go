package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func TestVolSyncHandler_SubAppName_MatchesCrane(t *testing.T) {
	h := &traits.VolSyncHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "volsync",
		Properties: map[string]any{
			"sourcePVC": "data-pvc",
			"schedule":  "0 2 * * *",
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	want := "data-pvc-backup"
	if got := bundle.Applications[0].Name; got != want {
		t.Errorf("sub-app Name = %q, want %q", got, want)
	}
}
