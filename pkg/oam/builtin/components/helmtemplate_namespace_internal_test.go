package components

// Tests of the namespace template delivery gives a namespaced rendered object
// that carries none (stampRenderedNamespaces, go-kure/launcher#794).

import (
	"fmt"
	"testing"

	"github.com/go-kure/kure/pkg/stack/layout"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// nsFixtureCRD is a CustomResourceDefinition of group fixtures.example.com
// defining kind with the given scope, optionally under a helm.sh/hook.
func nsFixtureCRD(kind, plural, scope, hook string) string {
	annotations := ""
	if hook != "" {
		annotations = "  annotations:\n    helm.sh/hook: " + hook + "\n"
	}
	return fmt.Sprintf("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: %s.fixtures.example.com\n%s"+
		"spec:\n  group: fixtures.example.com\n  scope: %s\n  names:\n    kind: %s\n    plural: %s\n"+
		"  versions:\n    - name: v1\n      served: true\n      storage: true\n", plural, annotations, scope, kind, plural)
}

// nsDoc is one rendered document: apiVersion, kind, name, and the
// metadata.namespace the chart wrote ("" for none), optionally hooked.
func nsDoc(apiVersion, kind, name, namespace, hook string) string {
	doc := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n", apiVersion, kind, name)
	if namespace != "" {
		doc += "  namespace: " + namespace + "\n"
	}
	if hook != "" {
		doc += "  annotations:\n    helm.sh/hook: " + hook + "\n"
	}
	return doc
}

// nsRender joins rendered documents the way kure's renderer does.
func nsRender(docs ...string) string {
	raw := ""
	for i, d := range docs {
		if i > 0 {
			raw += "---\n"
		}
		raw += d
	}
	return raw
}

// nsFixture is a helmtemplate config in namespace on a stub render of raw.
func nsFixture(t *testing.T, namespace, raw string) *HelmTemplateConfig {
	t.Helper()
	cfg := helmTemplateFixture(t, stubRender(raw))
	cfg.Namespace = namespace
	return cfg
}

// nsKey names an object in a namespace table: Kind/name.
func nsKey(o client.Object) string {
	return o.GetObjectKind().GroupVersionKind().Kind + "/" + o.GetName()
}

// nsWant fails unless objs are exactly the objects of want, each carrying the
// namespace want gives it.
func nsWant(t *testing.T, where string, objs []client.Object, want map[string]string) {
	t.Helper()
	if len(objs) != len(want) {
		t.Errorf("%s: %d objects, want %d", where, len(objs), len(want))
	}
	for _, o := range objs {
		wantNS, ok := want[nsKey(o)]
		if !ok {
			t.Errorf("%s: unexpected object %s", where, nsKey(o))
			continue
		}
		if got := o.GetNamespace(); got != wantNS {
			t.Errorf("%s: %s namespace = %q, want %q", where, nsKey(o), got, wantNS)
		}
	}
}

// TestHelmTemplate_StampsNamespaceOnNamespacedRenderedObjects: a namespaced
// rendered object without metadata.namespace gets the application namespace —
// a typed one (a Lease, an EndpointSlice and an ImageRepository among them), an
// untyped one in an API version kure's scheme does not
// register, a hook's, and a custom resource whose CRD the same render emits —
// while a namespace the chart wrote, a cluster-scoped object (with or without
// a namespace) and an object of unknown scope stay as rendered. Nothing is
// refused. The hook-group child layouts hold the same stamped objects, and a
// second Generate returns them again.
func TestHelmTemplate_StampsNamespaceOnNamespacedRenderedObjects(t *testing.T) {
	raw := nsRender(
		nsDoc("v1", "ConfigMap", "bare", "", ""),
		nsDoc("v1", "ConfigMap", "authored", "other", ""),
		nsDoc("batch/v1", "Job", "migrate", "", "pre-install"),
		nsDoc("autoscaling/v1", "HorizontalPodAutoscaler", "untyped", "", ""),
		nsDoc("coordination.k8s.io/v1", "Lease", "lock", "", ""),
		nsDoc("discovery.k8s.io/v1", "EndpointSlice", "slice", "", ""),
		nsDoc("image.toolkit.fluxcd.io/v1", "ImageRepository", "repo", "", ""),
		nsDoc("scheduling.k8s.io/v1", "PriorityClass", "high", "", ""),
		nsDoc("v1", "Namespace", "extra", "", ""),
		nsDoc("rbac.authorization.k8s.io/v1", "ClusterRole", "stray", "kept", ""),
		nsFixtureCRD("Widget", "widgets", "Namespaced", ""),
		nsFixtureCRD("ClusterWidget", "clusterwidgets", "Cluster", ""),
		nsDoc("fixtures.example.com/v1", "Widget", "w", "", ""),
		nsDoc("fixtures.example.com/v1", "ClusterWidget", "cw", "", ""),
		nsDoc("example.io/v1", "Gadget", "unknown", "", ""),
		nsDoc("example.io/v1", "Gadget", "unknown-authored", "elsewhere", ""),
	)
	want := map[string]string{
		"ConfigMap/bare":                  "team",
		"ConfigMap/authored":              "other",
		"Job/migrate":                     "team",
		"HorizontalPodAutoscaler/untyped": "team",
		"Lease/lock":                      "team",
		"EndpointSlice/slice":             "team",
		"ImageRepository/repo":            "team",
		"PriorityClass/high":              "",
		"Namespace/extra":                 "",
		"ClusterRole/stray":               "kept",
		"Widget/w":                        "team",
		"ClusterWidget/cw":                "",
		"Gadget/unknown":                  "",
		"Gadget/unknown-authored":         "elsewhere",
		"CustomResourceDefinition/widgets.fixtures.example.com":        "",
		"CustomResourceDefinition/clusterwidgets.fixtures.example.com": "",
	}
	cfg := nsFixture(t, "team", raw)

	for _, pass := range []string{"first Generate", "second Generate"} {
		ptrs, err := cfg.Generate(nil)
		if err != nil {
			t.Fatalf("%s: %v", pass, err)
		}
		objs := make([]client.Object, len(ptrs))
		for i, p := range ptrs {
			objs[i] = *p
			if nsKey(objs[i]) != "HorizontalPodAutoscaler/untyped" {
				continue
			}
			if _, ok := objs[i].(*unstructured.Unstructured); !ok {
				t.Fatalf("test premise: the autoscaling/v1 HorizontalPodAutoscaler is %T, want it untyped", objs[i])
			}
		}
		nsWant(t, pass, objs, want)
	}

	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "team/myapp"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 2 {
		t.Fatalf("layout has %d children, want 2 (pre-install, main)", len(ml.Children))
	}
	var children []client.Object
	for _, child := range ml.Children {
		children = append(children, child.Resources...)
	}
	nsWant(t, "hook-group children", children, want)
}

// TestHelmTemplate_DroppedCRDDefinesNoScope: only an emitted
// CustomResourceDefinition gives its kind a scope. One under a hook the output
// drops is not applied by this output, so the custom resource it would define
// is of unknown scope and stays as rendered.
func TestHelmTemplate_DroppedCRDDefinesNoScope(t *testing.T) {
	cfg := nsFixture(t, "team", nsRender(
		nsFixtureCRD("Widget", "widgets", "Namespaced", "test"),
		nsDoc("fixtures.example.com/v1", "Widget", "w", "", ""),
		nsDoc("v1", "ConfigMap", "bare", "", ""),
	))
	ptrs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	objs := make([]client.Object, len(ptrs))
	for i, p := range ptrs {
		objs[i] = *p
	}
	nsWant(t, "Generate", objs, map[string]string{"Widget/w": "", "ConfigMap/bare": "team"})
}

// TestHelmTemplateConfig_NoNamespaceStampsNothing: a config built directly
// with no namespace has none to give, so a namespace-less rendered object
// stays so.
func TestHelmTemplateConfig_NoNamespaceStampsNothing(t *testing.T) {
	cfg := nsFixture(t, "", nsDoc("v1", "ConfigMap", "bare", "", ""))
	ptrs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(ptrs) != 1 {
		t.Fatalf("%d objects, want 1", len(ptrs))
	}
	if got := (*ptrs[0]).GetNamespace(); got != "" {
		t.Errorf("namespace = %q, want none", got)
	}
}
