package components

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

func TestCRDHandler_CanHandle(t *testing.T) {
	h := &CRDHandler{}
	if !h.CanHandle("crd") || h.CanHandle("manifests") {
		t.Error("CRDHandler should handle only \"crd\"")
	}
}

func TestCRDHandler_InlineCRDPasses(t *testing.T) {
	h := &CRDHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "widget-crds", Type: "crd",
		Properties: map[string]any{"inline": crdYAML},
	}, "widgets")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 || (*objs[0]).GetObjectKind().GroupVersionKind().Kind != "CustomResourceDefinition" {
		t.Errorf("want one CRD, got %d objects", len(objs))
	}
}

func TestCRDHandler_RejectsNonCRDInline(t *testing.T) {
	h := &CRDHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "bad", Type: "crd",
		Properties: map[string]any{"inline": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\n  namespace: nsx\n"},
	}, "widgets")
	if err == nil || !strings.Contains(err.Error(), "CustomResourceDefinition") {
		t.Errorf("want non-CRD rejection, got %v", err)
	}
}

func TestCRDHandler_URLHostPolicyDenied(t *testing.T) {
	h := &CRDHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "remote-crds", Type: "crd",
		Properties: map[string]any{"url": "https://evil.example.com/crds.yaml"},
	}, "widgets")
	if err != nil {
		t.Fatalf("ToApplicationConfig (url, no eager fetch): %v", err)
	}
	ap, ok := cfg.(interface {
		ApplyPolicy(oam.Policy) error
	})
	if !ok {
		t.Fatal("config must implement ApplyPolicy")
	}
	if err := ap.ApplyPolicy(fakePolicy{allowed: []string{"trusted.example.com"}}); err == nil {
		t.Error("want policy denial for a disallowed url host")
	}
}
