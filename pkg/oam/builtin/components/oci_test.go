package components_test

import (
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fakeOCIPolicy implements oam.Policy via an embedded (nil) interface and
// overrides only AllowedRegistries — enough to exercise ApplyPolicy's host check.
type fakeOCIPolicy struct {
	oam.Policy
	allowed []string
}

func (f fakeOCIPolicy) AllowedRegistries() []string { return f.allowed }

func ociComponent(props map[string]any) *oam.Component {
	return &oam.Component{Name: "checkout", Type: "oci", Properties: props}
}

func validOCIProps() map[string]any {
	return map[string]any{
		"source":  map[string]any{"url": "oci://registry.wharf.zone/wharf/charts/checkout"},
		"version": "0.3.0",
	}
}

func mustOCIConfig(t *testing.T, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	h := &components.OCIHandler{}
	cfg, err := h.ToApplicationConfig(ociComponent(props), "checkout")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

func TestOCIHandler_CanHandle(t *testing.T) {
	h := &components.OCIHandler{}
	if !h.CanHandle("oci") {
		t.Error("expected true for oci")
	}
	if h.CanHandle("helmchart") {
		t.Error("expected false for helmchart")
	}
}

func TestOCIHandler_Validation(t *testing.T) {
	h := &components.OCIHandler{}
	cases := []struct {
		name  string
		props map[string]any
	}{
		{"no source", map[string]any{"version": "0.3.0"}},
		{"source without url", map[string]any{"source": map[string]any{}, "version": "0.3.0"}},
		{"non-oci url", map[string]any{"source": map[string]any{"url": "https://example.com/x"}, "version": "0.3.0"}},
		{"no version", map[string]any{"source": map[string]any{"url": "oci://registry.wharf.zone/x"}}},
		{"invalid interval", map[string]any{
			"source": map[string]any{"url": "oci://registry.wharf.zone/x"}, "version": "0.3.0", "interval": "5minutes",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.ToApplicationConfig(ociComponent(tc.props), "checkout"); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

func TestOCIHandler_Generate_Tag(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects (OCIRepository + Kustomization), got %d", len(objs))
	}

	repo, ok := (*objs[0]).(*sourcev1.OCIRepository)
	if !ok {
		t.Fatalf("objects[0]: expected *sourcev1.OCIRepository, got %T", *objs[0])
	}
	if repo.Spec.URL != "oci://registry.wharf.zone/wharf/charts/checkout" {
		t.Errorf("OCIRepository.Spec.URL = %q", repo.Spec.URL)
	}
	if repo.Spec.Reference == nil || repo.Spec.Reference.Tag != "0.3.0" {
		t.Errorf("OCIRepository ref = %+v, want tag 0.3.0", repo.Spec.Reference)
	}
	if repo.Spec.Reference != nil && repo.Spec.Reference.Digest != "" {
		t.Errorf("OCIRepository digest = %q, want empty for a tag", repo.Spec.Reference.Digest)
	}

	kz, ok := (*objs[1]).(*kustv1.Kustomization)
	if !ok {
		t.Fatalf("objects[1]: expected *kustv1.Kustomization, got %T", *objs[1])
	}
	if kz.Spec.Path != "./" {
		t.Errorf("Kustomization.Spec.Path = %q, want ./", kz.Spec.Path)
	}
	if !kz.Spec.Prune {
		t.Error("Kustomization.Spec.Prune = false, want true (default)")
	}
	if kz.Spec.SourceRef.Kind != "OCIRepository" || kz.Spec.SourceRef.Name != "checkout" {
		t.Errorf("Kustomization sourceRef = %+v, want OCIRepository/checkout", kz.Spec.SourceRef)
	}
}

func TestOCIHandler_Generate_Digest(t *testing.T) {
	props := validOCIProps()
	props["version"] = "sha256:abc123def4567890"
	cfg := mustOCIConfig(t, props)
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	repo := (*objs[0]).(*sourcev1.OCIRepository)
	if repo.Spec.Reference == nil || repo.Spec.Reference.Digest != "sha256:abc123def4567890" {
		t.Errorf("OCIRepository ref = %+v, want digest", repo.Spec.Reference)
	}
	if repo.Spec.Reference.Tag != "" {
		t.Errorf("OCIRepository tag = %q, want empty for a digest", repo.Spec.Reference.Tag)
	}
}

func TestOCIHandler_DefaultInterval(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	objs, _ := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	repo := (*objs[0]).(*sourcev1.OCIRepository)
	if repo.Spec.Interval.Duration.String() != "1h0m0s" {
		t.Errorf("OCIRepository interval = %q, want 1h0m0s (default)", repo.Spec.Interval.Duration.String())
	}
	kz := (*objs[1]).(*kustv1.Kustomization)
	if kz.Spec.Interval.Duration.String() != "1h0m0s" {
		t.Errorf("Kustomization interval = %q, want 1h0m0s (default)", kz.Spec.Interval.Duration.String())
	}
}

func TestOCIHandler_PathPruneTargetNamespace_Override(t *testing.T) {
	props := validOCIProps()
	props["path"] = "./deploy"
	props["prune"] = false
	props["targetNamespace"] = "checkout-workload"
	cfg := mustOCIConfig(t, props)
	objs, _ := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	kz := (*objs[1]).(*kustv1.Kustomization)
	if kz.Spec.Path != "./deploy" {
		t.Errorf("path = %q, want ./deploy", kz.Spec.Path)
	}
	if kz.Spec.Prune {
		t.Error("prune = true, want false (overridden)")
	}
	if kz.Spec.TargetNamespace != "checkout-workload" {
		t.Errorf("targetNamespace = %q, want checkout-workload", kz.Spec.TargetNamespace)
	}
}

func TestOCIHandler_GetSourceKey(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	s, ok := cfg.(interface{ GetSourceKey() string })
	if !ok {
		t.Fatal("OCIConfig does not implement GetSourceKey")
	}
	want := "oci:oci://registry.wharf.zone/wharf/charts/checkout:0.3.0"
	if got := s.GetSourceKey(); got != want {
		t.Errorf("GetSourceKey() = %q, want %q", got, want)
	}
}

func TestOCIHandler_SourceDedup(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	dedup, ok := cfg.(interface {
		SuppressSourceGeneration(string)
		GetSourceRefName() string
	})
	if !ok {
		t.Fatal("OCIConfig does not implement SourceDeduplicatable")
	}
	dedup.SuppressSourceGeneration("shared-oci")

	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate after suppression: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object (Kustomization only) after dedup, got %d", len(objs))
	}
	kz, ok := (*objs[0]).(*kustv1.Kustomization)
	if !ok {
		t.Fatalf("expected *kustv1.Kustomization, got %T", *objs[0])
	}
	if kz.Spec.SourceRef.Name != "shared-oci" {
		t.Errorf("sourceRef.Name = %q, want shared-oci (deduped source)", kz.Spec.SourceRef.Name)
	}
}

func TestOCIConfig_SetFluxNamespace(t *testing.T) {
	cfg := mustOCIConfig(t, validOCIProps())
	setter, ok := cfg.(interface{ SetFluxNamespace(string) })
	if !ok {
		t.Fatal("OCIConfig does not implement SetFluxNamespace")
	}
	setter.SetFluxNamespace("flux-system")

	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, objPtr := range objs {
		obj := *objPtr
		if ns := obj.GetNamespace(); ns != "flux-system" {
			t.Errorf("%T.Namespace = %q, want flux-system", obj, ns)
		}
	}
}

func TestOCIConfig_ApplyPolicy_RegistryAllowlist(t *testing.T) {
	deny := mustOCIConfig(t, validOCIProps()).(oam.Enforceable)
	if err := deny.ApplyPolicy(fakeOCIPolicy{allowed: []string{"trusted.example.com"}}); err == nil {
		t.Error("want host denial: registry.wharf.zone not in allowlist")
	}

	ok := mustOCIConfig(t, validOCIProps()).(oam.Enforceable)
	if err := ok.ApplyPolicy(fakeOCIPolicy{allowed: []string{"registry.wharf.zone"}}); err != nil {
		t.Errorf("allowed registry rejected: %v", err)
	}

	if err := mustOCIConfig(t, validOCIProps()).(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy must be a no-op, got %v", err)
	}
}
