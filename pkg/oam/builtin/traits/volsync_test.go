package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func TestVolSyncHandler_SubAppName_IsSourcePVCBackup(t *testing.T) {
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

// TestVolSyncHandler_Name: an authored `name` names the ReplicationSource and
// its sub-application, used as written; an empty or invalid one is refused
// with the property named, not replaced by the default.
func TestVolSyncHandler_Name(t *testing.T) {
	apply := func(name any) (*stack.Bundle, error) {
		bundle := &stack.Bundle{}
		trait := &oam.Trait{Type: "volsync", Properties: map[string]any{
			"name": name, "sourcePVC": "data-pvc", "schedule": "0 2 * * *",
		}}
		return bundle, (&traits.VolSyncHandler{}).Apply(trait, stack.NewApplication("myapp", "default", nil), bundle)
	}

	bundle, err := apply("data-offsite")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sub := bundle.Applications[0]
	if sub.Name != "data-offsite" {
		t.Errorf("sub-app Name = %q, want the authored name", sub.Name)
	}
	objs, err := sub.Config.Generate(sub)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := (*objs[0]).GetName(); got != "data-offsite" {
		t.Errorf("ReplicationSource name = %q, want the authored name", got)
	}

	for bad, want := range map[string]string{
		"":             "name is empty",
		"Data_Offsite": `name "Data_Offsite" cannot name the ReplicationSource`,
	} {
		if _, err := apply(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("name %q: err = %v, want it to contain %q", bad, err, want)
		}
	}
}
