package traits_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func TestFluxCDPatchesHandler_CanHandle(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"fluxcd-patches", true},
		{"prune-protection", false},
		{"ingress", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

// TestFluxCDPatchesHandler_Schema_Strict guards the strict shape: the patch item and
// its target selector are closed objects (unknown keys rejected), and
// the target enumerates the seven kustomize selector fields as strings so valid
// selectors still pass. Downstream consumers preflight against this schema.
func TestFluxCDPatchesHandler_Schema_Strict(t *testing.T) {
	schema := (&traits.FluxCDPatchesHandler{}).PropertySchema()
	patches, ok := schema["patches"]
	if !ok {
		t.Fatal("schema missing 'patches' property")
	}
	if patches.Items == nil {
		t.Fatal("patches.Items is nil")
	}
	if patches.Items.AdditionalProperties {
		t.Error("patch item must be a closed object (AdditionalProperties=false) so unknown keys are rejected")
	}

	target, ok := patches.Items.Properties["target"]
	if !ok {
		t.Fatal("patch item missing 'target' property")
	}
	if target.AdditionalProperties {
		t.Error("patch target must be a closed object (AdditionalProperties=false) so unknown keys in target are rejected")
	}
	for _, field := range []string{"group", "version", "kind", "name", "namespace", "labelSelector", "annotationSelector"} {
		p, ok := target.Properties[field]
		if !ok {
			t.Errorf("target missing enumerated selector field %q", field)
			continue
		}
		if p.Type != oam.PropertyTypeString {
			t.Errorf("target.%s type = %q, want %q", field, p.Type, oam.PropertyTypeString)
		}
	}
}

func TestFluxCDPatchesHandler_Apply_AppendsToBundlePatches(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-patches",
		Properties: map[string]any{
			"patches": []any{
				map[string]any{
					"patch": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: mysvc\n",
					"target": map[string]any{
						"kind": "Deployment",
						"name": "mysvc",
					},
				},
			},
		},
	}

	app := newApp("mysvc", "myns")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(bundle.Patches))
	}
	p := bundle.Patches[0]
	if p.Target == nil {
		t.Fatal("expected non-nil target")
	}
	if p.Target.Kind != "Deployment" {
		t.Errorf("target.Kind = %q, want %q", p.Target.Kind, "Deployment")
	}
	if p.Target.Name != "mysvc" {
		t.Errorf("target.Name = %q, want %q", p.Target.Name, "mysvc")
	}
}

func TestFluxCDPatchesHandler_Apply_AccumulatesAcrossComponents(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	bundle := newBundle()

	makeTrait := func(kind, name string) *oam.Trait {
		return &oam.Trait{
			Type: "fluxcd-patches",
			Properties: map[string]any{
				"patches": []any{
					map[string]any{
						"patch":  "apiVersion: apps/v1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n",
						"target": map[string]any{"kind": kind, "name": name},
					},
				},
			},
		}
	}

	if err := h.Apply(makeTrait("Deployment", "svc1"), newApp("svc1", "ns"), bundle); err != nil {
		t.Fatalf("Apply svc1: %v", err)
	}
	if err := h.Apply(makeTrait("Deployment", "svc2"), newApp("svc2", "ns"), bundle); err != nil {
		t.Fatalf("Apply svc2: %v", err)
	}
	if len(bundle.Patches) != 2 {
		t.Errorf("expected 2 patches, got %d", len(bundle.Patches))
	}
}

func TestFluxCDPatchesHandler_Apply_NilTarget(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-patches",
		Properties: map[string]any{
			"patches": []any{
				map[string]any{
					"patch": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: svc\n",
				},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("svc", "ns"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(bundle.Patches))
	}
	if bundle.Patches[0].Target != nil {
		t.Error("expected nil Target for patch without target selector")
	}
}

func TestFluxCDPatchesHandler_Apply_MissingPatch_Errors(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-patches",
		Properties: map[string]any{
			"patches": []any{
				map[string]any{"target": map[string]any{"kind": "Deployment"}},
			},
		},
	}
	if err := h.Apply(trait, newApp("svc", "ns"), newBundle()); err == nil {
		t.Fatal("expected error for missing patch string")
	}
}

func TestFluxCDPatchesHandler_Apply_MissingPatches_Errors(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	if err := h.Apply(&oam.Trait{Type: "fluxcd-patches", Properties: map[string]any{}}, newApp("svc", "ns"), newBundle()); err == nil {
		t.Fatal("expected error for missing patches property")
	}
}

func TestFluxCDPatchesHandler_Apply_Target_NonStringKind_Errors(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-patches",
		Properties: map[string]any{
			"patches": []any{
				map[string]any{
					"patch":  "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: svc\n",
					"target": map[string]any{"kind": 42},
				},
			},
		},
	}
	if err := h.Apply(trait, newApp("svc", "ns"), newBundle()); err == nil {
		t.Fatal("expected error for non-string target.kind")
	}
}

func TestFluxCDPatchesHandler_Apply_Target_NonStringName_Errors(t *testing.T) {
	h := &traits.FluxCDPatchesHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-patches",
		Properties: map[string]any{
			"patches": []any{
				map[string]any{
					"patch":  "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: svc\n",
					"target": map[string]any{"kind": "Deployment", "name": true},
				},
			},
		},
	}
	if err := h.Apply(trait, newApp("svc", "ns"), newBundle()); err == nil {
		t.Fatal("expected error for non-string target.name")
	}
}
