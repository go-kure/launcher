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

// --- scopeOverrides (launcher#141) ---

// clusterWidgetYAML is a fictitious cluster-scoped custom resource kind kure
// will never register — the fixture for "a CRD installed out of band, so its
// scope can only come from scopeOverrides."
const clusterWidgetYAML = "apiVersion: fixtures.example.com/v1\nkind: ClusterWidget\nmetadata:\n  name: widget\n"

func generateManifestsWithOverrides(t *testing.T, ns, inline string, overrides any) ([]string, error) {
	t.Helper()
	props := map[string]any{"inline": inline}
	if overrides != nil {
		props["scopeOverrides"] = overrides
	}
	cfg, err := (&ManifestsHandler{}).ToApplicationConfig(&oam.Component{
		Name: "m", Type: "manifests", Properties: props,
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

func TestManifestsHandler_ScopeOverride_ClusterPasses(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget", "scope": "Cluster"}}
	got, err := generateManifestsWithOverrides(t, "app-ns", clusterWidgetYAML, overrides)
	if err != nil {
		t.Fatalf("cluster-scope override should pass: %v", err)
	}
	if len(got) != 1 || got[0] != "ClusterWidget:" {
		t.Errorf("cluster-scoped override must leave the object namespace-less, got %v", got)
	}
}

func TestManifestsHandler_ScopeOverride_FailsClosedWithout(t *testing.T) {
	// The same namespace-less ClusterWidget still fails closed without an override
	// (the fail-closed default is preserved).
	_, err := generateManifestsWithOverrides(t, "app-ns", clusterWidgetYAML, nil)
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("namespace-less ClusterWidget must fail closed without an override, got %v", err)
	}
}

func TestManifestsHandler_ScopeOverride_Namespaced(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget", "scope": "Namespaced"}}
	got, err := generateManifestsWithOverrides(t, "app-ns", clusterWidgetYAML, overrides)
	if err != nil {
		t.Fatalf("namespaced override should pass: %v", err)
	}
	if len(got) != 1 || got[0] != "ClusterWidget:app-ns" {
		t.Errorf("namespaced override must stamp the app ns, got %v", got)
	}
}

func TestManifestsHandler_ScopeOverride_IgnoredForKnownScope(t *testing.T) {
	// Overrides apply only to ScopeUnknown objects: an override on a built-in
	// namespaced kind must not flip it to cluster-scoped.
	overrides := []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "scope": "Cluster"}}
	got, err := generateManifestsWithOverrides(t, "app-ns",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\n", overrides)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0] != "Deployment:app-ns" {
		t.Errorf("override must not contradict a known scope; Deployment must still be stamped, got %v", got)
	}
}

// PriorityClass is cluster-scoped by the Kubernetes API, but kure registers no
// builder for it, so it is absent from the generated table and
// KindForAnyVersion does not report it. A Namespaced override must still be
// ignored: its scope is API-governed exactly like a registered built-in's.
// TestManifestsHandler_ScopeOverride_Namespaced above is the discriminating
// half — the same override on an unregistered kind that is NOT in kure's
// residual cluster-scoped set does stamp the namespace.
func TestManifestsHandler_ScopeOverride_IgnoredForUnregisteredClusterBuiltin(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "scheduling.k8s.io/v1", "kind": "PriorityClass", "scope": "Namespaced"}}
	got, err := generateManifestsWithOverrides(t, "app-ns",
		"apiVersion: scheduling.k8s.io/v1\nkind: PriorityClass\nmetadata:\n  name: high\nvalue: 1000\n", overrides)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0] != "PriorityClass:" {
		t.Errorf("a Namespaced override must not stamp a cluster-scoped API built-in, got %v", got)
	}
}

func TestManifestsHandler_ScopeOverride_Validation(t *testing.T) {
	cases := []struct {
		name      string
		overrides any
	}{
		{"not a list", "nope"},
		{"entry not an object", []any{"fixtures.example.com/v1/ClusterWidget"}},
		{"missing kind", []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "scope": "Cluster"}}},
		{"invalid scope", []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget", "scope": "Galaxy"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&ManifestsHandler{}).ToApplicationConfig(&oam.Component{
				Name: "m", Type: "manifests",
				Properties: map[string]any{"inline": clusterWidgetYAML, "scopeOverrides": tc.overrides},
			}, "app-ns")
			if err == nil {
				t.Fatalf("expected validation error for %q", tc.name)
			}
		})
	}
}
