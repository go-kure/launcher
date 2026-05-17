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
