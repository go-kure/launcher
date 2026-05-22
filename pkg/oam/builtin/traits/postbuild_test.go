package traits_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func TestPostBuildHandler_CanHandle(t *testing.T) {
	h := &traits.PostBuildHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"fluxcd-postbuild", true},
		{"fluxcd-patches", false},
		{"prune-protection", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

func TestPostBuildHandler_Apply_SubstituteOnly(t *testing.T) {
	h := &traits.PostBuildHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-postbuild",
		Properties: map[string]any{
			"substitute": map[string]any{
				"CLUSTER_NAME": "prod-eu",
				"REGION":       "eu-west-1",
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("svc", "ns"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.PostBuild == nil {
		t.Fatal("expected bundle.PostBuild to be set")
	}
	if bundle.PostBuild.Substitute["CLUSTER_NAME"] != "prod-eu" {
		t.Errorf("Substitute[CLUSTER_NAME] = %q, want %q", bundle.PostBuild.Substitute["CLUSTER_NAME"], "prod-eu")
	}
	if len(bundle.PostBuild.SubstituteFrom) != 0 {
		t.Errorf("expected no substituteFrom, got %d", len(bundle.PostBuild.SubstituteFrom))
	}
}

func TestPostBuildHandler_Apply_SubstituteFrom(t *testing.T) {
	h := &traits.PostBuildHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-postbuild",
		Properties: map[string]any{
			"substituteFrom": []any{
				map[string]any{"kind": "ConfigMap", "name": "cluster-vars"},
				map[string]any{"kind": "Secret", "name": "cluster-secrets", "optional": true},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("svc", "ns"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.PostBuild == nil {
		t.Fatal("expected bundle.PostBuild to be set")
	}
	if len(bundle.PostBuild.SubstituteFrom) != 2 {
		t.Fatalf("expected 2 substituteFrom refs, got %d", len(bundle.PostBuild.SubstituteFrom))
	}
	if bundle.PostBuild.SubstituteFrom[0].Kind != "ConfigMap" || bundle.PostBuild.SubstituteFrom[0].Name != "cluster-vars" {
		t.Errorf("unexpected substituteFrom[0]: %+v", bundle.PostBuild.SubstituteFrom[0])
	}
	if !bundle.PostBuild.SubstituteFrom[1].Optional {
		t.Error("substituteFrom[1] should be optional")
	}
}

func TestPostBuildHandler_Apply_InvalidKind_Errors(t *testing.T) {
	h := &traits.PostBuildHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-postbuild",
		Properties: map[string]any{
			"substituteFrom": []any{
				map[string]any{"kind": "Deployment", "name": "bad"},
			},
		},
	}
	if err := h.Apply(trait, newApp("svc", "ns"), newBundle()); err == nil {
		t.Error("expected error for invalid substituteFrom kind")
	}
}

func TestPostBuildHandler_Apply_EmptyErrors(t *testing.T) {
	h := &traits.PostBuildHandler{}
	if err := h.Apply(&oam.Trait{Type: "fluxcd-postbuild", Properties: map[string]any{}}, newApp("svc", "ns"), newBundle()); err == nil {
		t.Error("expected error for empty trait (neither substitute nor substituteFrom set)")
	}
}

func TestPostBuildHandler_Apply_BothSubstituteAndFrom(t *testing.T) {
	h := &traits.PostBuildHandler{}
	trait := &oam.Trait{
		Type: "fluxcd-postbuild",
		Properties: map[string]any{
			"substitute": map[string]any{"CLUSTER": "staging"},
			"substituteFrom": []any{
				map[string]any{"kind": "ConfigMap", "name": "env-vars"},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("svc", "ns"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.PostBuild == nil {
		t.Fatal("expected bundle.PostBuild set")
	}
	if bundle.PostBuild.Substitute["CLUSTER"] != "staging" {
		t.Errorf("Substitute[CLUSTER] = %q, want %q", bundle.PostBuild.Substitute["CLUSTER"], "staging")
	}
	if len(bundle.PostBuild.SubstituteFrom) != 1 {
		t.Errorf("expected 1 substituteFrom, got %d", len(bundle.PostBuild.SubstituteFrom))
	}
}
