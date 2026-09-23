package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// --- ValidateImageRef ---

func TestValidateImageRef_ExplicitTag(t *testing.T) {
	if err := components.ValidateImageRef("ghcr.io/org/app:v1.0.0"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageRef_Digest(t *testing.T) {
	if err := components.ValidateImageRef("ghcr.io/org/app@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1"); err != nil {
		t.Errorf("unexpected error for digest ref: %v", err)
	}
}

// A digest reference carrying an explicit :latest tag is rejected. The digest
// pins the content, but the tag stays part of the reference and a cluster is
// free to read it — and since this package stopped writing imagePullPolicy
// itself (go-kure/launcher#361) nothing here overrides how it does.
func TestValidateImageRef_LatestTagWithDigest(t *testing.T) {
	err := components.ValidateImageRef("ghcr.io/org/app:latest@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err == nil {
		t.Fatal("expected error for a :latest tag carrying a digest")
	}
	if !strings.Contains(err.Error(), ":latest") {
		t.Errorf("error should mention :latest, got: %v", err)
	}
}

// The discriminating half of the pair above: the rejection keys on the latest
// tag, not on a reference carrying both a tag and a digest.
func TestValidateImageRef_VersionTagWithDigest(t *testing.T) {
	if err := components.ValidateImageRef("ghcr.io/org/app:v1.0.0@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1"); err != nil {
		t.Errorf("unexpected error for a version tag carrying a digest: %v", err)
	}
}

func TestValidateImageRef_ExplicitLatest(t *testing.T) {
	err := components.ValidateImageRef("nginx:latest")
	if err == nil {
		t.Fatal("expected error for :latest tag")
	}
	if !strings.Contains(err.Error(), ":latest") {
		t.Errorf("error should mention :latest, got: %v", err)
	}
}

func TestValidateImageRef_ImplicitLatest(t *testing.T) {
	err := components.ValidateImageRef("nginx")
	if err == nil {
		t.Fatal("expected error for untagged image")
	}
}

func TestValidateImageRef_Invalid(t *testing.T) {
	if err := components.ValidateImageRef(""); err == nil {
		t.Fatal("expected error for empty image")
	}
}

// --- BuildPVC ---

func TestBuildPVC_Basic(t *testing.T) {
	pvc := components.PVCConfig{
		Name:         "data",
		Size:         "10Gi",
		StorageClass: "standard",
		AccessModes:  []string{"ReadWriteOnce"},
	}
	obj, err := components.BuildPVC(pvc, "default", map[string]string{"app": "test"})
	if err != nil {
		t.Fatalf("BuildPVC: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil PVC")
	}
	if obj.Name != "data" {
		t.Errorf("expected name 'data', got %q", obj.Name)
	}
}

func TestBuildPVC_NoStorageClass(t *testing.T) {
	pvc := components.PVCConfig{
		Name:        "data",
		Size:        "5Gi",
		AccessModes: []string{"ReadWriteOnce"},
	}
	obj, err := components.BuildPVC(pvc, "default", nil)
	if err != nil {
		t.Fatalf("BuildPVC: %v", err)
	}
	if obj.Spec.StorageClassName != nil {
		t.Error("expected nil StorageClassName when not set")
	}
}

func TestBuildPVC_InvalidSize(t *testing.T) {
	pvc := components.PVCConfig{
		Name: "data",
		Size: "notaquantity",
	}
	_, err := components.BuildPVC(pvc, "default", nil)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

// TestBuildPVC_NonPositiveSize guards the second parse of the size on the
// same path (go-kure/launcher#384): BuildPVC re-parses PVCConfig.Size, and
// the pvc trait reaches it without going through parseVolumes, so the
// positivity rule upstream applies to requests[storage] must hold here too.
func TestBuildPVC_NonPositiveSize(t *testing.T) {
	for _, size := range []string{"0", "-1Gi"} {
		t.Run(size, func(t *testing.T) {
			_, err := components.BuildPVC(components.PVCConfig{Name: "data", Size: size}, "default", nil)
			if err == nil {
				t.Fatalf("expected error for size %q", size)
			}
			if !strings.Contains(err.Error(), "size must be positive") {
				t.Errorf("error should name the positivity rule, got: %v", err)
			}
		})
	}
}

func TestBuildPVC_MultipleAccessModes(t *testing.T) {
	pvc := components.PVCConfig{
		Name:        "shared",
		Size:        "1Gi",
		AccessModes: []string{"ReadWriteMany"},
	}
	obj, err := components.BuildPVC(pvc, "default", nil)
	if err != nil {
		t.Fatalf("BuildPVC: %v", err)
	}
	if len(obj.Spec.AccessModes) != 1 {
		t.Errorf("expected 1 access mode, got %d", len(obj.Spec.AccessModes))
	}
}
