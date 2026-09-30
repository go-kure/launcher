package components_test

import (
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// ociKustomizationGolden is the delivery Kustomization validOCIProps built
// before `wait` and `healthChecks` existed (go-kure/launcher#432), captured
// from that code. A document authoring neither must keep producing these
// exact bytes.
const ociKustomizationGolden = `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: checkout
  namespace: checkout
spec:
  interval: 1h0m0s
  path: ./
  prune: true
  sourceRef:
    kind: OCIRepository
    name: checkout
status: {}
`

// ociKustomization converts props and returns the emitted Kustomization.
func ociKustomization(t *testing.T, props map[string]any) *kustv1.Kustomization {
	t.Helper()
	cfg := mustOCIConfig(t, props)
	objs, err := cfg.Generate(stack.NewApplication("checkout", "checkout", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	kz, ok := (*objs[len(objs)-1]).(*kustv1.Kustomization)
	if !ok {
		t.Fatalf("last object: expected *kustv1.Kustomization, got %T", *objs[len(objs)-1])
	}
	return kz
}

// ociKustomizationYAML is ociKustomization serialized, the form a consumer
// writes to disk.
func ociKustomizationYAML(t *testing.T, props map[string]any) string {
	t.Helper()
	out, err := yaml.Marshal(ociKustomization(t, props))
	if err != nil {
		t.Fatalf("marshal Kustomization: %v", err)
	}
	return string(out)
}

func TestOCIKustomization_WithoutWaitOrHealthChecksIsByteIdentical(t *testing.T) {
	if got := ociKustomizationYAML(t, validOCIProps()); got != ociKustomizationGolden {
		t.Errorf("Kustomization changed for a document authoring neither wait nor healthChecks:\n--- got\n%s\n--- want\n%s", got, ociKustomizationGolden)
	}
}

// wait: false, a null wait, a null healthChecks and an empty healthChecks list
// all say "nothing to add": each builds exactly the Kustomization an omitted
// key does.
func TestOCIKustomization_FalseNullAndEmptyAreAbsence(t *testing.T) {
	var typedNilList []any
	cases := map[string]map[string]any{
		"wait false":             withProp(validOCIProps(), "wait", false),
		"wait null":              withProp(validOCIProps(), "wait", nil),
		"healthChecks null":      withProp(validOCIProps(), "healthChecks", nil),
		"healthChecks typed nil": withProp(validOCIProps(), "healthChecks", typedNilList),
		"healthChecks empty":     withProp(validOCIProps(), "healthChecks", []any{}),
		"wait false and empty":   withProp(withProp(validOCIProps(), "wait", false), "healthChecks", []any{}),
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ociKustomizationYAML(t, props); got != ociKustomizationGolden {
				t.Errorf("got\n%s\nwant\n%s", got, ociKustomizationGolden)
			}
		})
	}
}

func TestOCIKustomization_WaitTrue(t *testing.T) {
	kz := ociKustomization(t, withProp(validOCIProps(), "wait", true))
	if !kz.Spec.Wait {
		t.Error("Kustomization.Spec.Wait = false, want true")
	}
	if kz.Spec.HealthChecks != nil {
		t.Errorf("Kustomization.Spec.HealthChecks = %v, want none", kz.Spec.HealthChecks)
	}
	// The only change from the default document is the one added line.
	want := strings.Replace(ociKustomizationGolden, "    name: checkout\nstatus: {}\n", "    name: checkout\n  wait: true\nstatus: {}\n", 1)
	if got := ociKustomizationYAML(t, withProp(validOCIProps(), "wait", true)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// An empty list is absence, so it does not collide with wait: true.
func TestOCIKustomization_WaitTrueWithEmptyHealthChecks(t *testing.T) {
	kz := ociKustomization(t, withProp(withProp(validOCIProps(), "wait", true), "healthChecks", []any{}))
	if !kz.Spec.Wait || kz.Spec.HealthChecks != nil {
		t.Errorf("Spec.Wait = %v, Spec.HealthChecks = %v; want true and none", kz.Spec.Wait, kz.Spec.HealthChecks)
	}
}

func ociHealthChecks() []any {
	return []any{
		map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout-api", "namespace": "checkout"},
		// Cluster-scoped: no namespace.
		map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition", "name": "orders.shop.example.com"},
		map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "checkout-db", "namespace": "checkout-data"},
	}
}

func TestOCIKustomization_HealthChecksInAuthoredOrder(t *testing.T) {
	kz := ociKustomization(t, withProp(validOCIProps(), "healthChecks", ociHealthChecks()))
	want := []meta.NamespacedObjectKindReference{
		{APIVersion: "apps/v1", Kind: "Deployment", Name: "checkout-api", Namespace: "checkout"},
		{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition", Name: "orders.shop.example.com"},
		{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "checkout-db", Namespace: "checkout-data"},
	}
	if len(kz.Spec.HealthChecks) != len(want) {
		t.Fatalf("Spec.HealthChecks = %+v, want %+v", kz.Spec.HealthChecks, want)
	}
	for i := range want {
		if kz.Spec.HealthChecks[i] != want[i] {
			t.Errorf("Spec.HealthChecks[%d] = %+v, want %+v", i, kz.Spec.HealthChecks[i], want[i])
		}
	}
	if kz.Spec.Wait {
		t.Error("Spec.Wait = true, want false: healthChecks does not imply wait")
	}
	// Explicit wait: false is compatible with a non-empty list.
	kz = ociKustomization(t, withProp(withProp(validOCIProps(), "wait", false), "healthChecks", ociHealthChecks()))
	if kz.Spec.Wait || len(kz.Spec.HealthChecks) != len(want) {
		t.Errorf("wait false + healthChecks: Spec.Wait = %v, %d health checks; want false and %d", kz.Spec.Wait, len(kz.Spec.HealthChecks), len(want))
	}
}

// A config is reusable and a generated object may be edited in place, so the
// health checks a render carries must not share a backing array with the config.
func TestOCIKustomization_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	cfg := mustOCIConfig(t, withProp(validOCIProps(), "healthChecks", ociHealthChecks()))
	app := stack.NewApplication("checkout", "checkout", cfg)
	first, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	kz := (*first[len(first)-1]).(*kustv1.Kustomization)
	kz.Spec.HealthChecks[0].Name = "edited"
	kz.Spec.HealthChecks = append(kz.Spec.HealthChecks, meta.NamespacedObjectKindReference{Kind: "Extra"})

	second, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	got := (*second[len(second)-1]).(*kustv1.Kustomization).Spec.HealthChecks
	if len(got) != 3 || got[0].Name != "checkout-api" {
		t.Errorf("second render's health checks = %+v; the first render's edit leaked into the config", got)
	}
}

func TestOCIHandler_WaitAndHealthChecksRejections(t *testing.T) {
	entry := func(overrides map[string]any, drop ...string) []any {
		e := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "checkout-api"}
		for k, v := range overrides {
			e[k] = v
		}
		for _, k := range drop {
			delete(e, k)
		}
		return []any{e}
	}
	cases := []struct {
		name string
		key  string
		val  any
		want string
	}{
		{"wait not a boolean", "wait", "true", "wait: must be a boolean, got string"},
		{"healthChecks not an array", "healthChecks", map[string]any{"kind": "Deployment"}, "healthChecks: must be an array, got map[string]interface {}"},
		{"entry not an object", "healthChecks", []any{"apps/v1/Deployment/checkout-api"}, "healthChecks[0]: must be an object, got string"},
		{"null entry", "healthChecks", []any{nil}, "healthChecks[0]: must be an object, got <nil>"},
		{"second entry not an object", "healthChecks", append(entry(nil), 3), "healthChecks[1]: must be an object, got int"},
		{"missing apiVersion", "healthChecks", entry(nil, "apiVersion"), "healthChecks[0]: apiVersion is required"},
		{"empty apiVersion", "healthChecks", entry(map[string]any{"apiVersion": ""}), "healthChecks[0]: apiVersion is required"},
		{"missing kind", "healthChecks", entry(nil, "kind"), "healthChecks[0]: kind is required"},
		{"missing name", "healthChecks", entry(nil, "name"), "healthChecks[0]: name is required"},
		{"null name", "healthChecks", entry(map[string]any{"name": nil}), "healthChecks[0]: name is required"},
		{"apiVersion not a string", "healthChecks", entry(map[string]any{"apiVersion": 1}), "healthChecks[0].apiVersion: must be a string, got int"},
		{"kind not a string", "healthChecks", entry(map[string]any{"kind": true}), "healthChecks[0].kind: must be a string, got bool"},
		{"name not a string", "healthChecks", entry(map[string]any{"name": []any{"a"}}), "healthChecks[0].name: must be a string, got []interface {}"},
		{"namespace not a string", "healthChecks", entry(map[string]any{"namespace": 7}), "healthChecks[0].namespace: must be a string, got int"},
		{"unknown key", "healthChecks", entry(map[string]any{"namspace": "checkout"}), `healthChecks[0]: unrecognized key "namspace"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := convert(&components.OCIHandler{}, "oci", withProp(validOCIProps(), tc.key, tc.val))
			if err == nil {
				t.Fatalf("%s=%#v converted without error", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// kustomize-controller ignores healthChecks when wait is true, so authoring
// both would ship a list that is never read.
func TestOCIHandler_WaitTrueWithHealthChecksIsRejected(t *testing.T) {
	props := withProp(withProp(validOCIProps(), "wait", true), "healthChecks", ociHealthChecks())
	err := convert(&components.OCIHandler{}, "oci", props)
	if err == nil {
		t.Fatal("wait: true with healthChecks converted without error; the health checks would be silently ignored")
	}
	for _, want := range []string{"oci: wait: true and healthChecks are mutually exclusive", "ignores healthChecks when wait is true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestOCIHandler_WaitAndHealthChecksSchema(t *testing.T) {
	schema := (&components.OCIHandler{}).PropertySchema()
	if w := schema["wait"]; w.Type != oam.PropertyTypeBoolean || w.Required || w.Default != nil || w.Description == "" {
		t.Errorf("wait schema = %+v, want an optional boolean with no default and a description", w)
	}
	hc := schema["healthChecks"]
	if hc.Type != oam.PropertyTypeArray || hc.Required || hc.Items == nil || hc.Description == "" {
		t.Fatalf("healthChecks schema = %+v, want an optional array with an item schema", hc)
	}
	item := hc.Items
	if item.Type != oam.PropertyTypeObject || item.AdditionalProperties {
		t.Errorf("healthChecks item = %+v, want a closed object", *item)
	}
	for key, required := range map[string]bool{"apiVersion": true, "kind": true, "name": true, "namespace": false} {
		p, ok := item.Properties[key]
		if !ok || p.Type != oam.PropertyTypeString || p.Required != required || p.Description == "" {
			t.Errorf("healthChecks item %q = %+v (declared %v), want a described string, required=%v", key, p, ok, required)
		}
	}
	if len(item.Properties) != 4 {
		t.Errorf("healthChecks item declares %d properties, want 4", len(item.Properties))
	}
}

// The authored pipeline — schema validation, then conversion — accepts a valid
// document and refuses the malformed shapes at one layer or the other.
func TestOCIHandler_WaitAndHealthChecksAuthoredPipeline(t *testing.T) {
	build := func(props map[string]any) error {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"oci": &components.OCIHandler{}}, nil)
		comp := oam.Component{Name: "checkout", Type: "oci", Properties: props}
		app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
		if err := tr.ValidateAuthoredProperties(app); err != nil {
			return err
		}
		_, err := (&components.OCIHandler{}).ToApplicationConfig(&comp, "checkout")
		return err
	}
	if err := build(withProp(validOCIProps(), "healthChecks", ociHealthChecks())); err != nil {
		t.Errorf("valid healthChecks refused: %v", err)
	}
	if err := build(withProp(validOCIProps(), "wait", true)); err != nil {
		t.Errorf("wait: true refused: %v", err)
	}
	for name, props := range map[string]map[string]any{
		"unknown entry key":     withProp(validOCIProps(), "healthChecks", []any{map[string]any{"apiVersion": "v1", "kind": "Service", "name": "x", "extra": 1}}),
		"missing entry name":    withProp(validOCIProps(), "healthChecks", []any{map[string]any{"apiVersion": "v1", "kind": "Service"}}),
		"wait with healthCheck": withProp(withProp(validOCIProps(), "wait", true), "healthChecks", ociHealthChecks()),
	} {
		if err := build(props); err == nil {
			t.Errorf("%s: built without error", name)
		}
	}
}
