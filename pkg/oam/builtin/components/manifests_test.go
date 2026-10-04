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

// TestManifestsHandler_ScopeOfEveryRegisteredAPIGroup: a kind of the
// coordination, discovery, scheduling, node, admission registration, API
// registration and image-reflector groups has a scope kure's table answers
// for, so a namespace-less source builds: the namespaced ones are stamped, the
// cluster-scoped ones are not.
func TestManifestsHandler_ScopeOfEveryRegisteredAPIGroup(t *testing.T) {
	doc := func(apiVersion, kind string) string {
		return "apiVersion: " + apiVersion + "\nkind: " + kind + "\nmetadata:\n  name: thing\n"
	}
	cases := []struct{ apiVersion, kind, want string }{
		{"coordination.k8s.io/v1", "Lease", "Lease:app-ns"},
		{"discovery.k8s.io/v1", "EndpointSlice", "EndpointSlice:app-ns"},
		{"image.toolkit.fluxcd.io/v1", "ImageRepository", "ImageRepository:app-ns"},
		{"image.toolkit.fluxcd.io/v1", "ImagePolicy", "ImagePolicy:app-ns"},
		{"scheduling.k8s.io/v1", "PriorityClass", "PriorityClass:"},
		{"node.k8s.io/v1", "RuntimeClass", "RuntimeClass:"},
		{"apiregistration.k8s.io/v1", "APIService", "APIService:"},
		{"admissionregistration.k8s.io/v1", "ValidatingWebhookConfiguration", "ValidatingWebhookConfiguration:"},
		{"admissionregistration.k8s.io/v1", "MutatingWebhookConfiguration", "MutatingWebhookConfiguration:"},
		{"admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicy", "ValidatingAdmissionPolicy:"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got, err := generateManifests(t, "app-ns", doc(tc.apiVersion, tc.kind))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("got %v, want [%s]", got, tc.want)
			}
		})
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

// --- scopeOverrides (go-kure/launcher#141) ---

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

// widgetCRDYAML bundles a *Namespaced* Widget CRD with a Widget in one source,
// the case where the source itself defines the scope the cluster will serve.
const widgetCRDYAML = "apiVersion: apiextensions.k8s.io/v1\n" +
	"kind: CustomResourceDefinition\n" +
	"metadata:\n  name: widgets.fixtures.example.com\n" +
	"spec:\n  group: fixtures.example.com\n  scope: Namespaced\n" +
	"  names:\n    kind: Widget\n    plural: widgets\n" +
	"---\n" +
	"apiVersion: fixtures.example.com/v1\nkind: Widget\nmetadata:\n  name: w\n"

// An override may outrank kure's table (the ClusterWidget cases above) but not a
// CRD in the same source: that CRD is the definition being applied, so honouring
// a Cluster override against it would emit a namespace-less object the cluster
// then creates in whatever namespace the applying client defaults to. Neither
// statement is silently dropped — the disagreement itself is the error.
func TestManifestsHandler_ScopeOverride_ConflictingSameSourceCRDRejected(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "Widget", "scope": "Cluster"}}
	_, err := generateManifestsWithOverrides(t, "app-ns", widgetCRDYAML, overrides)
	if err == nil {
		t.Fatal("a Cluster override contradicting a Namespaced same-source CRD must be rejected")
	}
	for _, want := range []string{"scopeOverrides says Cluster", "declares Namespaced"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name both scopes (missing %q)", err, want)
		}
	}
}

// The discriminating half of the test above: the rejection keys on the two
// statements disagreeing, not on a CRD being present at all.
func TestManifestsHandler_ScopeOverride_AgreeingSameSourceCRDPasses(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "Widget", "scope": "Namespaced"}}
	got, err := generateManifestsWithOverrides(t, "app-ns", widgetCRDYAML, overrides)
	if err != nil {
		t.Fatalf("an override agreeing with the same-source CRD must pass: %v", err)
	}
	if len(got) != 2 || got[0] != "CustomResourceDefinition:" || got[1] != "Widget:app-ns" {
		t.Errorf("want [CustomResourceDefinition: Widget:app-ns], got %v", got)
	}
}

func TestManifestsHandler_ScopeOverride_IgnoredForKnownScope(t *testing.T) {
	// An override never reaches a kind the Kubernetes API itself scopes: a
	// Cluster override on a built-in namespaced kind must not flip it.
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

// PriorityClass is cluster-scoped by the Kubernetes API, and kure's generated
// table records it so (ScopeSourceBuiltin). A Namespaced override must be
// ignored: the scope is API-governed.
// TestManifestsHandler_ScopeOverride_Namespaced above is the discriminating
// half — the same override on a kind kure does not register does stamp the
// namespace. No kind exercises isAPIGovernedScope's probe for a cluster-scoped
// built-in kure fixes the scope of without registering it: kure's set of those
// is empty.
func TestManifestsHandler_ScopeOverride_IgnoredForClusterBuiltin(t *testing.T) {
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

// A cluster-scoped object whose own YAML authors metadata.namespace must be
// rejected, not silently emitted: the Kubernetes API validates cluster-scoped
// objects with apimachinery's ValidateObjectMetaAccessor, which forbids a
// non-empty namespace on that kind ("not allowed on this type") — so passing
// the authored namespace through would only defer the failure to apply time
// with a less legible error. TestManifestsHandler_ClusterScopedUntouched above
// is the discriminating half: the same kind with no authored namespace passes.
func TestManifestsHandler_ClusterScopedWithAuthoredNamespaceRejected(t *testing.T) {
	_, err := generateManifests(t, "app-ns",
		"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: foo\n  namespace: stray\n")
	if err == nil {
		t.Fatal("a cluster-scoped object authoring metadata.namespace must be rejected")
	}
	for _, want := range []string{"Cluster", "stray"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the scope and the offending namespace (missing %q)", err, want)
		}
	}
}

// Same rejection, reached via a scopeOverrides-declared Cluster scope rather
// than a built-in one — proves the fix sits in the shared post-determination
// switch, not only on the manifest.Scope-derived path.
// TestManifestsHandler_ScopeOverride_ClusterPasses above is the discriminating
// half: the same override with no authored namespace passes.
func TestManifestsHandler_ScopeOverride_ClusterWithAuthoredNamespaceRejected(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget", "scope": "Cluster"}}
	inline := "apiVersion: fixtures.example.com/v1\nkind: ClusterWidget\nmetadata:\n  name: widget\n  namespace: stray\n"
	_, err := generateManifestsWithOverrides(t, "app-ns", inline, overrides)
	if err == nil {
		t.Fatal("a Cluster override on an object authoring metadata.namespace must be rejected")
	}
	for _, want := range []string{"Cluster", "stray"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the scope and the offending namespace (missing %q)", err, want)
		}
	}
}

// clusterIssuerYAML is cert-manager's ClusterIssuer — a kind kure actually
// registers (unlike the fictitious ClusterWidget above), whose scope comes
// from a source-code marker on the vendored type rather than the Kubernetes
// API itself (kure's generated table records it as
// `ScopeSource: "marker"`, not `"builtin"`) — a "registered, non-API-governed"
// kind. Neither ClusterWidget (never registered) nor PriorityClass above
// (registered, with a scope the Kubernetes API governs) exercises this branch:
// kure's own registered, marker-sourced table entry.
const clusterIssuerYAML = "apiVersion: cert-manager.io/v1\nkind: ClusterIssuer\nmetadata:\n  name: ci\n"

func TestManifestsHandler_RegisteredMarkerScopedKind_UntouchedWithoutOverride(t *testing.T) {
	got, err := generateManifests(t, "app-ns", clusterIssuerYAML)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0] != "ClusterIssuer:" {
		t.Errorf("kure's table registers ClusterIssuer cluster-scoped (marker-sourced); it must not be stamped, got %v", got)
	}
}

// The discriminating half: a Namespaced override must outrank kure's own
// registered table entry for a marker-sourced kind exactly as it does for an
// unregistered one (TestManifestsHandler_ScopeOverride_Namespaced above) —
// proving the outranking rule keys on ScopeSource (isAPIGovernedScope), not
// on whether kure registers the kind at all. A regression that widened
// isAPIGovernedScope's `registered` branch to ignore ScopeSource would pass
// every existing override test — none of them use a registered, non-builtin
// kind — yet fail this one.
func TestManifestsHandler_ScopeOverride_OutranksRegisteredMarkerScopedKind(t *testing.T) {
	overrides := []any{map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer", "scope": "Namespaced"}}
	got, err := generateManifestsWithOverrides(t, "app-ns", clusterIssuerYAML, overrides)
	if err != nil {
		t.Fatalf("namespaced override on a registered marker-scoped kind should pass: %v", err)
	}
	if len(got) != 1 || got[0] != "ClusterIssuer:app-ns" {
		t.Errorf("override must outrank kure's registered table for a non-API-governed kind, got %v", got)
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
