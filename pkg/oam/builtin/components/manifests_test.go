package components

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

func generateManifests(t *testing.T, ns, inline string) ([]string, error) {
	t.Helper()
	cfg, err := (&ManifestsHandler{}).ToApplicationConfig(&oam.Component{
		Name: "m", Type: "manifests", Properties: map[string]any{"inline": inline},
	}, ns)
	if err != nil {
		return nil, err
	}
	objs, err := cfg.Generate(nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = (*o).GetObjectKind().GroupVersionKind().Kind + ":" + (*o).GetNamespace()
	}
	return out, nil
}

func TestManifestsHandler_StampsBuiltinNamespaced(t *testing.T) {
	got, err := generateManifests(t, "app-ns",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\n")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0] != "Deployment:app-ns" {
		t.Errorf("built-in namespaced object must be stamped with app ns, got %v", got)
	}
}

func TestManifestsHandler_ClusterScopedUntouched(t *testing.T) {
	got, err := generateManifests(t, "app-ns",
		"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: foo\n")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0] != "Namespace:" {
		t.Errorf("cluster-scoped object must not be stamped, got %v", got)
	}
}

func TestManifestsHandler_CustomResourceScopeFromSameSourceCRD(t *testing.T) {
	inline := crdYAML + "---\napiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w1\n"
	got, err := generateManifests(t, "app-ns", inline)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The CRD (cluster-scoped) is untouched; the Widget instance is namespaced
	// (its CRD declares scope Namespaced) and stamped with the app ns.
	var widgetNS string
	for _, g := range got {
		if after, ok := strings.CutPrefix(g, "Widget:"); ok {
			widgetNS = after
		}
	}
	if widgetNS != "app-ns" {
		t.Errorf("custom resource scope must be inferred from same-source CRD and stamped; got %v", got)
	}
}

func TestManifestsHandler_UnknownGVKNoNamespaceFailsClosed(t *testing.T) {
	_, err := generateManifests(t, "app-ns",
		"apiVersion: unknown.io/v1\nkind: Mystery\nmetadata:\n  name: m1\n")
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("unknown GVK without a namespace must fail closed, got %v", err)
	}
}

func TestManifestsHandler_UnknownGVKWithNamespacePasses(t *testing.T) {
	got, err := generateManifests(t, "app-ns",
		"apiVersion: unknown.io/v1\nkind: Mystery\nmetadata:\n  name: m1\n  namespace: explicit\n")
	if err != nil {
		t.Fatalf("explicit namespace should pass: %v", err)
	}
	if len(got) != 1 || got[0] != "Mystery:explicit" {
		t.Errorf("explicit namespace must be preserved, got %v", got)
	}
}
