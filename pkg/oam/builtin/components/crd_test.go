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

// A CustomResourceDefinition document is always cluster-scoped; an authored
// metadata.namespace must be rejected here for the same reason
// TestManifestsHandler_ClusterScopedWithAuthoredNamespaceRejected rejects it on
// the sibling manifests component — the Kubernetes API forbids a namespace on
// a cluster-scoped object, so passing it through defers the failure to apply
// time. TestCRDHandler_InlineCRDPasses above is the discriminating half: the
// same CRD with no authored namespace passes.
func TestCRDHandler_RejectsAuthoredNamespace(t *testing.T) {
	h := &CRDHandler{}
	inline := "apiVersion: apiextensions.k8s.io/v1\n" +
		"kind: CustomResourceDefinition\n" +
		"metadata:\n  name: widgets.example.com\n  namespace: stray\n" +
		"spec:\n  group: example.com\n  scope: Namespaced\n" +
		"  names:\n    kind: Widget\n    plural: widgets\n"
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "widget-crds", Type: "crd",
		Properties: map[string]any{"inline": inline},
	}, "widgets")
	if err == nil || !strings.Contains(err.Error(), "stray") {
		t.Errorf("want rejection naming the offending namespace, got %v", err)
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
