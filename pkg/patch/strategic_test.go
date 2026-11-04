package patch

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestApplyStrategicMergePatch_DeploymentContainersMergedByName(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      "my-app",
				"namespace": "default",
			},
			"spec": map[string]any{
				"replicas": int64(1),
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{
							map[string]any{
								"name":  "main",
								"image": "nginx:1.24",
							},
							map[string]any{
								"name":  "logger",
								"image": "fluentd:latest",
							},
						},
					},
				},
			},
		},
	}

	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "main",
							"image": "nginx:1.25",
						},
						map[string]any{
							"name":  "sidecar",
							"image": "envoy:v1.28",
						},
					},
				},
			},
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	containers, found, err := unstructured.NestedSlice(resource.Object,
		"spec", "template", "spec", "containers")
	if err != nil || !found {
		t.Fatal("containers not found after patch")
	}

	// SMP should merge by name: main updated, logger preserved, sidecar added
	if len(containers) != 3 {
		t.Fatalf("expected 3 containers, got %d: %v", len(containers), containers)
	}

	containerNames := make(map[string]string)
	for _, c := range containers {
		cm := c.(map[string]any)
		containerNames[cm["name"].(string)] = cm["image"].(string)
	}

	if containerNames["main"] != "nginx:1.25" {
		t.Errorf("expected main image nginx:1.25, got %s", containerNames["main"])
	}
	if containerNames["logger"] != "fluentd:latest" {
		t.Errorf("expected logger image fluentd:latest, got %s", containerNames["logger"])
	}
	if containerNames["sidecar"] != "envoy:v1.28" {
		t.Errorf("expected sidecar image envoy:v1.28, got %s", containerNames["sidecar"])
	}
}

func TestApplyStrategicMergePatch_ConfigMapDataMerged(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "my-config",
				"namespace": "default",
			},
			"data": map[string]any{
				"key1": "value1",
				"key2": "value2",
			},
		},
	}

	patch := map[string]any{
		"data": map[string]any{
			"key2": "updated",
			"key3": "new-value",
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	data, found, err := unstructured.NestedStringMap(resource.Object, "data")
	if err != nil || !found {
		t.Fatal("data not found after patch")
	}

	if data["key1"] != "value1" {
		t.Errorf("key1 should be preserved, got %s", data["key1"])
	}
	if data["key2"] != "updated" {
		t.Errorf("key2 should be updated, got %s", data["key2"])
	}
	if data["key3"] != "new-value" {
		t.Errorf("key3 should be added, got %s", data["key3"])
	}
}

func TestApplyStrategicMergePatch_UnknownCRDFallback(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "example.com/v1",
			"kind":       "MyCRD",
			"metadata": map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			"spec": map[string]any{
				"items": []any{
					map[string]any{"name": "a"},
					map[string]any{"name": "b"},
				},
				"replicas": int64(1),
			},
		},
	}

	patch := map[string]any{
		"spec": map[string]any{
			"items": []any{
				map[string]any{"name": "c"},
			},
			"replicas": int64(3),
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	// JSON merge patch replaces lists entirely (no merge-by-key for unknown kinds)
	items, found, err := unstructured.NestedSlice(resource.Object, "spec", "items")
	if err != nil || !found {
		t.Fatal("items not found after patch")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (JSON merge replaces list), got %d", len(items))
	}

	replicas, found, err := unstructured.NestedFieldNoCopy(resource.Object, "spec", "replicas")
	if err != nil || !found {
		t.Fatal("replicas not found")
	}
	// JSON round-trip may convert int64 to float64
	switch v := replicas.(type) {
	case float64:
		if v != 3 {
			t.Errorf("expected replicas 3, got %v", v)
		}
	case int64:
		if v != 3 {
			t.Errorf("expected replicas 3, got %v", v)
		}
	default:
		t.Errorf("unexpected replicas type %T", replicas)
	}
}

func TestApplyStrategicMergePatch_NilLookup(t *testing.T) {
	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name": "test",
			},
			"spec": map[string]any{
				"replicas": int64(1),
			},
		},
	}

	patch := map[string]any{
		"spec": map[string]any{
			"replicas": int64(5),
		},
	}

	// nil lookup should fall back to JSON merge patch
	if err := ApplyStrategicMergePatch(resource, patch, nil); err != nil {
		t.Fatalf("ApplyStrategicMergePatch with nil lookup: %v", err)
	}

	replicas, found, err := unstructured.NestedFieldNoCopy(resource.Object, "spec", "replicas")
	if err != nil || !found {
		t.Fatal("replicas not found")
	}
	// JSON round-trip converts to float64
	switch v := replicas.(type) {
	case float64:
		if v != 5 {
			t.Errorf("expected replicas 5, got %v", v)
		}
	case int64:
		if v != 5 {
			t.Errorf("expected replicas 5, got %v", v)
		}
	}
}

func TestStrategicPatch_VariableSubstitution(t *testing.T) {
	patchYAML := `- target: my-app
  type: strategic
  patch:
    spec:
      replicas: "${values.replicas}"
      template:
        spec:
          containers:
          - name: main
            image: "${values.image}"
`
	varCtx := &VariableContext{
		Values: map[string]any{
			"replicas": "5",
			"image":    "nginx:1.25",
		},
	}

	patches, err := LoadYAMLPatchFile(strings.NewReader(patchYAML), varCtx)
	if err != nil {
		t.Fatalf("LoadYAMLPatchFile: %v", err)
	}

	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	if patches[0].Strategic == nil {
		t.Fatal("expected a strategic patch")
	}

	smpPatch := patches[0].Strategic.Patch

	// Verify nested substitution worked
	spec, ok := smpPatch["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec not a map: %T", smpPatch["spec"])
	}
	// After type inference, replicas should be converted to int
	if fmt.Sprintf("%v", spec["replicas"]) != "5" {
		t.Errorf("expected replicas value 5, got %v", spec["replicas"])
	}

	tmpl, ok := spec["template"].(map[string]any)
	if !ok {
		t.Fatalf("template not a map: %T", spec["template"])
	}
	tmplSpec, ok := tmpl["spec"].(map[string]any)
	if !ok {
		t.Fatalf("template.spec not a map: %T", tmpl["spec"])
	}
	containers, ok := tmplSpec["containers"].([]any)
	if !ok || len(containers) == 0 {
		t.Fatal("containers not found or empty")
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatal("container is not a map")
	}
	if container["image"] != "nginx:1.25" {
		t.Errorf("expected image 'nginx:1.25', got %v", container["image"])
	}
}

// TestStrategicPatch_VariableSubstitutionInfersTypes verifies that after
// variable substitution, numeric and boolean strings are converted to their
// typed equivalents (matching field-level patch behavior).
func TestStrategicPatch_VariableSubstitutionInfersTypes(t *testing.T) {
	patchYAML := `- target: my-app
  type: strategic
  patch:
    spec:
      replicas: "${values.replicas}"
`
	varCtx := &VariableContext{
		Values: map[string]any{
			"replicas": "3",
		},
	}

	patches, err := LoadYAMLPatchFile(strings.NewReader(patchYAML), varCtx)
	if err != nil {
		t.Fatalf("LoadYAMLPatchFile: %v", err)
	}

	if len(patches) != 1 || patches[0].Strategic == nil {
		t.Fatal("expected 1 strategic patch")
	}

	spec, ok := patches[0].Strategic.Patch["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec not a map: %T", patches[0].Strategic.Patch["spec"])
	}

	replicas := spec["replicas"]
	// After type inference, replicas should be an int, not a string
	switch replicas.(type) {
	case int, int64:
		// OK
	default:
		t.Errorf("expected replicas to be inferred as integer, got %T(%v)", replicas, replicas)
	}
}

// TestLoadYAMLPatchFile_StrategicNilPatchPayload verifies that a strategic
// entry with no patch block returns a clear error instead of silently
// producing a nil patch map.
func TestLoadYAMLPatchFile_StrategicNilPatchPayload(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name: "no patch key",
			input: `- target: my-app
  type: strategic
`,
		},
		{
			name: "explicit empty patch",
			input: `- target: my-app
  type: strategic
  patch: {}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadYAMLPatchFile(strings.NewReader(tt.input), nil)
			if err == nil {
				t.Fatal("expected error for strategic patch with no payload, got nil")
			}
			if !strings.Contains(err.Error(), "has no patch payload") {
				t.Errorf("expected 'has no patch payload' in error, got: %s", err.Error())
			}
		})
	}
}

// TestApplyTypedSMP_OriginalMapNotMutated verifies that applyTypedSMP does not
// mutate the original resource.Object map during a successful merge. This
// ensures that if a caller holds a reference to the original map, it remains
// intact after the patch is applied.
func TestApplyTypedSMP_OriginalMapNotMutated(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			"spec": map[string]any{
				"replicas": int64(1),
			},
		},
	}

	// Capture a reference to the original map
	origMap := resource.Object

	patch := map[string]any{
		"spec": map[string]any{
			"replicas": int64(5),
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	// resource.Object should be a new map (the merge result)
	if resource.Object == nil {
		t.Fatal("resource.Object is nil after patch")
	}

	// The original map should still have replicas=1
	origSpec, ok := origMap["spec"].(map[string]any)
	if !ok {
		t.Fatal("original spec not a map")
	}
	if origSpec["replicas"] != int64(1) {
		t.Errorf("original map was mutated: replicas = %v, want 1", origSpec["replicas"])
	}

	// The patched resource should have replicas=5
	patchedReplicas, found, err := unstructured.NestedFieldNoCopy(resource.Object, "spec", "replicas")
	if err != nil || !found {
		t.Fatal("replicas not found in patched resource")
	}
	if fmt.Sprintf("%v", patchedReplicas) != "5" {
		t.Errorf("patched replicas = %v, want 5", patchedReplicas)
	}
}

// TestApplyStrategicMergePatch_TypedResultDeepCopies is the reproducer of
// go-kure/launcher#1164: plain Go ints in the resource and in the patch used to
// stay ints in the merged object, and DeepCopy panicked on them.
func TestApplyStrategicMergePatch_TypedResultDeepCopies(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "test"},
			"spec": map[string]any{
				"minReadySeconds": 3,
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{
							map[string]any{
								"name":  "app",
								"ports": []any{map[string]any{"containerPort": 80}},
							},
						},
					},
				},
			},
		},
	}
	patch := map[string]any{
		"spec": map[string]any{
			"revisionHistoryLimit": 5,
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"ports": []any{map[string]any{"containerPort": 80, "hostPort": 8080}},
						},
					},
				},
			},
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DeepCopy panicked: %v", r)
		}
	}()
	copied := resource.DeepCopy()

	for _, path := range [][]string{
		{"spec", "minReadySeconds"},
		{"spec", "revisionHistoryLimit"},
	} {
		got, found, err := unstructured.NestedFieldNoCopy(copied.Object, path...)
		if err != nil || !found {
			t.Fatalf("%v not found in the copy: %v", path, err)
		}
		if _, ok := got.(int64); !ok {
			t.Errorf("%v = %v (%T), want an int64", path, got, got)
		}
	}
	ports, found, err := unstructured.NestedSlice(copied.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(ports) != 1 {
		t.Fatalf("containers not found in the copy: %v", err)
	}
	port := ports[0].(map[string]any)["ports"].([]any)[0].(map[string]any)
	if port["containerPort"] != int64(80) || port["hostPort"] != int64(8080) {
		t.Errorf("port = %v, want containerPort 80 and hostPort 8080 as int64", port)
	}
}

// TestApplyStrategicMergePatch_TypedResultKeepsNumbers pins the number types of
// the merged object: an int64 where the integer fits, a float64 for a fraction,
// and a json.Number with its own digits for an integer beyond the int64 range.
func TestApplyStrategicMergePatch_TypedResultKeepsNumbers(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "test"},
		},
	}
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{"fraction": 0.5},
			"generation":  int64(math.MaxInt64),
			"extra":       uint64(math.MaxUint64),
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}

	meta := resource.Object["metadata"].(map[string]any)
	if got := meta["annotations"].(map[string]any)["fraction"]; got != float64(0.5) {
		t.Errorf("fraction = %v (%T), want float64 0.5", got, got)
	}
	if got := meta["generation"]; got != int64(math.MaxInt64) {
		t.Errorf("generation = %v (%T), want int64 max", got, got)
	}
	if got := meta["extra"]; got != json.Number("18446744073709551615") {
		t.Errorf("extra = %v (%T), want json.Number 18446744073709551615", got, got)
	}
	// Every value must be one DeepCopy accepts.
	_ = resource.DeepCopy()
}

// TestApplyStrategicPatchesToDocument_BigIntegerWrittenAsNumber checks that an
// integer beyond the int64 range, which the merged object holds as a
// json.Number, is written to the YAML document as a plain number. yaml.v3 would
// quote the json.Number as a string.
func TestApplyStrategicPatchesToDocument_BigIntegerWrittenAsNumber(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	docSet, err := LoadResourcesWithStructure(strings.NewReader(`apiVersion: v1
kind: ConfigMap
metadata:
  name: config
  generation: 18446744073709551615
`))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	doc := docSet.Documents[0]

	patches := []StrategicPatch{{Patch: map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"a": "b"}},
	}}}
	if err := doc.ApplyStrategicPatchesToDocument(patches, lookup); err != nil {
		t.Fatalf("ApplyStrategicPatchesToDocument: %v", err)
	}

	got, found, err := unstructured.NestedFieldNoCopy(doc.Resource.Object, "metadata", "generation")
	if err != nil || !found {
		t.Fatalf("generation not found: %v", err)
	}
	if got != json.Number("18446744073709551615") {
		t.Errorf("generation = %v (%T), want json.Number 18446744073709551615", got, got)
	}

	out, err := yaml.Marshal(doc.Node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	if !strings.Contains(string(out), "generation: 18446744073709551615\n") {
		t.Errorf("generation not written as a plain number:\n%s", out)
	}
}

// TestApplyStrategicPatchesToDocument_BigIntegersKeepTheirDigits checks that an
// integer beyond the uint64 range and one below the int64 range are written to
// the YAML document with all their digits. A float64 would round them.
func TestApplyStrategicPatchesToDocument_BigIntegersKeepTheirDigits(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	docSet, err := LoadResourcesWithStructure(strings.NewReader(`apiVersion: v1
kind: ConfigMap
metadata:
  name: config
`))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	doc := docSet.Documents[0]

	patches := []StrategicPatch{{Patch: map[string]any{
		"metadata": map[string]any{
			"generation":      json.Number("18446744073709551617"),
			"resourceVersion": json.Number("-9223372036854775809"),
		},
	}}}
	if err := doc.ApplyStrategicPatchesToDocument(patches, lookup); err != nil {
		t.Fatalf("ApplyStrategicPatchesToDocument: %v", err)
	}

	out, err := yaml.Marshal(doc.Node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	for _, want := range []string{
		"generation: 18446744073709551617\n",
		"resourceVersion: -9223372036854775809\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("want %q in the document:\n%s", want, out)
		}
	}
}

// TestApplyStrategicMergePatch_TypedSuccessivePatchesMergeNumericKeys checks
// that a patch applied twice updates a list entry merged by a numeric key the
// second time, as it did when the values were plain ints, and that the caller's
// patch is not changed.
func TestApplyStrategicMergePatch_TypedSuccessivePatchesMergeNumericKeys(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "test"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{
					"name":  "app",
					"ports": []any{map[string]any{"containerPort": 80}},
				}},
			}}},
		},
	}
	port := map[string]any{"containerPort": 80, "name": "http"}
	patch := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "app", "ports": []any{port}}},
	}}}}

	for i := 0; i < 2; i++ {
		if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
			t.Fatalf("ApplyStrategicMergePatch %d: %v", i+1, err)
		}
	}

	containers, found, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("containers not found: %v", err)
	}
	ports := containers[0].(map[string]any)["ports"].([]any)
	if len(ports) != 1 {
		t.Errorf("ports after two applications = %v, want one entry", ports)
	}
	if _, ok := port["containerPort"].(int); !ok {
		t.Errorf("the caller's patch was changed: containerPort = %T, want int", port["containerPort"])
	}
}

// TestApplyStrategicMergePatch_TypedWholeNumberKeysMatch checks that a merge
// key written as a whole number with a decimal point or an exponent matches an
// integer key, on the first and on the second patch.
func TestApplyStrategicMergePatch_TypedWholeNumberKeysMatch(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	for _, key := range []any{json.Number("80.0"), json.Number("8e1"), float64(80)} {
		t.Run(fmt.Sprint(key), func(t *testing.T) {
			resource := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata":   map[string]any{"name": "test"},
					"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
						"containers": []any{map[string]any{
							"name":  "app",
							"ports": []any{map[string]any{"containerPort": 80}},
						}},
					}}},
				},
			}
			patch := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "ports": []any{
					map[string]any{"containerPort": key, "name": "http"},
				}}},
			}}}}

			for i := 0; i < 2; i++ {
				if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
					t.Fatalf("ApplyStrategicMergePatch %d: %v", i+1, err)
				}
				containers, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
				if err != nil || len(containers) != 1 {
					t.Fatalf("containers not found after application %d: %v", i+1, err)
				}
				ports := containers[0].(map[string]any)["ports"].([]any)
				if len(ports) != 1 {
					t.Errorf("ports after application %d = %v, want one entry", i+1, ports)
				}
			}
		})
	}
}

// TestJSONTypedNumbersMatchTheirJSON checks that jsonTyped gives each number the
// type its own JSON encoding decodes to, so that normalizing twice changes
// nothing.
func TestJSONTypedNumbersMatchTheirJSON(t *testing.T) {
	// "fraction", "fractionf" and "integer" are controls: they hold on the code
	// without the exact-digits fix and show the fix leaves them alone.
	in := map[string]any{
		"whole":     json.Number("80.0"),
		"exponent":  json.Number("8e1"),
		"float":     float64(80),
		"fraction":  json.Number("80.5"),
		"fractionf": 80.5,
		"integer":   80,
		"beyond":    json.Number("1e20"),
		"beyondf":   float64(1e20),
		"huge":      json.Number("1e21"),
	}
	want := map[string]any{
		"beyond":    json.Number("100000000000000000000"),
		"beyondf":   json.Number("100000000000000000000"),
		"huge":      1e21,
		"whole":     int64(80),
		"exponent":  int64(80),
		"float":     int64(80),
		"fraction":  80.5,
		"fractionf": 80.5,
		"integer":   int64(80),
	}

	once, err := jsonTyped(in)
	if err != nil {
		t.Fatalf("jsonTyped: %v", err)
	}
	for k, w := range want {
		if once[k] != w {
			t.Errorf("%s = %#v (%T), want %#v (%T)", k, once[k], once[k], w, w)
		}
	}
	twice, err := jsonTyped(once)
	if err != nil {
		t.Fatalf("jsonTyped again: %v", err)
	}
	for k := range want {
		if twice[k] != once[k] {
			t.Errorf("%s changed on the second pass: %#v to %#v", k, once[k], twice[k])
		}
	}
}

// TestJSONTypedKeepsDigitsOfWholeFloatsBeyondFloat53 checks that a whole number
// written with a point or an exponent keeps its own digits above 2^53, where a
// float64 would round it.
func TestJSONTypedKeepsDigitsOfWholeFloatsBeyondFloat53(t *testing.T) {
	in := map[string]any{
		"point":      json.Number("9007199254740993.0"),
		"exponent":   json.Number("9.007199254740993e15"),
		"negative":   json.Number("-9007199254740993.0"),
		"beyondI64":  json.Number("12345678901234567891.0"),
		"fraction":   json.Number("9007199254740993.5"),
		"plain":      json.Number("9007199254740993"),
		"belowFloat": json.Number("9007199254740991.0"),
		// Rounds to 1e21 as a float64, but is below it as written.
		"justBelow": json.Number("999999999999999999999.0"),
		"atLimit":   json.Number("1000000000000000000000.0"),
		"plainHuge": json.Number("1000000000000000000001"),
	}
	want := map[string]any{
		"point":      int64(9007199254740993),
		"exponent":   int64(9007199254740993),
		"negative":   int64(-9007199254740993),
		"beyondI64":  json.Number("12345678901234567891"),
		"fraction":   int64(9007199254740994), // not a whole number as written: the float64 beside it
		"plain":      int64(9007199254740993),
		"belowFloat": int64(9007199254740991),
		"justBelow":  json.Number("999999999999999999999"),
		"atLimit":    1e21,
		"plainHuge":  json.Number("1000000000000000000001"),
	}

	once, err := jsonTyped(in)
	if err != nil {
		t.Fatalf("jsonTyped: %v", err)
	}
	for k, w := range want {
		if once[k] != w {
			t.Errorf("%s = %#v (%T), want %#v (%T)", k, once[k], once[k], w, w)
		}
	}
	twice, err := jsonTyped(once)
	if err != nil {
		t.Fatalf("jsonTyped again: %v", err)
	}
	for k := range want {
		if twice[k] != once[k] {
			t.Errorf("%s changed on the second pass: %#v to %#v", k, once[k], twice[k])
		}
	}
}

// TestJSONTypedNilSliceAndMapAreEmpty checks that a nil slice or map, at any
// depth, comes out as an empty one, not null, and that an explicit nil stays nil.
func TestJSONTypedNilSliceAndMapAreEmpty(t *testing.T) {
	var nilSlice []any
	var nilMap map[string]any
	in := map[string]any{
		"slice":  nilSlice,
		"map":    nilMap,
		"null":   nil,
		"nested": map[string]any{"slice": nilSlice, "map": nilMap},
		"inList": []any{nilSlice, nilMap},
	}

	once, err := jsonTyped(in)
	if err != nil {
		t.Fatalf("jsonTyped: %v", err)
	}
	if s, ok := once["slice"].([]any); !ok || s == nil || len(s) != 0 {
		t.Errorf("slice = %#v, want an empty non-nil []any", once["slice"])
	}
	if m, ok := once["map"].(map[string]any); !ok || m == nil || len(m) != 0 {
		t.Errorf("map = %#v, want an empty non-nil map", once["map"])
	}
	if v, found := once["null"]; !found || v != nil {
		t.Errorf("null = %#v (found %v), want an explicit nil", v, found)
	}
	nested, _ := once["nested"].(map[string]any)
	if s, ok := nested["slice"].([]any); !ok || s == nil {
		t.Errorf("nested slice = %#v, want an empty []any", nested["slice"])
	}
	if m, ok := nested["map"].(map[string]any); !ok || m == nil {
		t.Errorf("nested map = %#v, want an empty map", nested["map"])
	}
	list, _ := once["inList"].([]any)
	if len(list) != 2 {
		t.Fatalf("inList = %#v, want two entries", once["inList"])
	}
	if s, ok := list[0].([]any); !ok || s == nil {
		t.Errorf("inList[0] = %#v, want an empty []any", list[0])
	}
	if m, ok := list[1].(map[string]any); !ok || m == nil {
		t.Errorf("inList[1] = %#v, want an empty map", list[1])
	}
	if in["slice"].([]any) != nil || in["map"].(map[string]any) != nil {
		t.Error("jsonTyped changed its input")
	}

	twice, err := jsonTyped(once)
	if err != nil {
		t.Fatalf("jsonTyped again: %v", err)
	}
	if s, ok := twice["slice"].([]any); !ok || s == nil {
		t.Errorf("slice after second pass = %#v, want an empty []any", twice["slice"])
	}
}

// TestJSONTypedNumberLiteralBound pins the bound on the number literals read for
// their digits: a whole float of maxNumberLiteral bytes still gets its exact
// int64, and one byte more is kept as the json.Number it was, text unchanged and
// never rounded to a float64.
func TestJSONTypedNumberLiteralBound(t *testing.T) {
	const head = "9007199254740993."
	atBound := head + strings.Repeat("0", maxNumberLiteral-len(head))
	pastBound := atBound + "0"
	if len(atBound) != maxNumberLiteral || len(pastBound) != maxNumberLiteral+1 {
		t.Fatalf("literal lengths %d and %d, want %d and %d", len(atBound), len(pastBound), maxNumberLiteral, maxNumberLiteral+1)
	}
	in := map[string]any{
		"atBound":   json.Number(atBound),
		"pastBound": json.Number(pastBound),
	}
	once, err := jsonTyped(in)
	if err != nil {
		t.Fatalf("jsonTyped: %v", err)
	}
	if once["atBound"] != int64(9007199254740993) {
		t.Errorf("atBound = %#v (%T), want int64 9007199254740993", once["atBound"], once["atBound"])
	}
	if once["pastBound"] != json.Number(pastBound) {
		t.Errorf("pastBound = %#v (%T), want the json.Number as written", once["pastBound"], once["pastBound"])
	}
	twice, err := jsonTyped(once)
	if err != nil {
		t.Fatalf("jsonTyped again: %v", err)
	}
	for k := range once {
		if twice[k] != once[k] {
			t.Errorf("%s changed on the second pass: %#v to %#v", k, once[k], twice[k])
		}
	}
}

// TestJSONTypedTypedNilContainerStaysNull pins the documented limit of the nil
// rule: only a nil map[string]any and a nil []any become empty. Any other
// container type goes to encoding/json as it is, so a nil one is null, as it is
// anywhere else in Go.
func TestJSONTypedTypedNilContainerStaysNull(t *testing.T) {
	var nilStrings []string
	var nilStringMap map[string]string
	in := map[string]any{
		"strings":   nilStrings,
		"stringMap": nilStringMap,
		"anySlice":  []any(nil),
	}
	out, err := jsonTyped(in)
	if err != nil {
		t.Fatalf("jsonTyped: %v", err)
	}
	for _, k := range []string{"strings", "stringMap"} {
		if v, found := out[k]; !found || v != nil {
			t.Errorf("%s = %#v (found %v), want an explicit nil", k, v, found)
		}
	}
	if s, ok := out["anySlice"].([]any); !ok || s == nil || len(s) != 0 {
		t.Errorf("anySlice = %#v, want an empty non-nil []any", out["anySlice"])
	}
}

// TestJSONTypedSelfReferenceIsAnError checks that a document containing itself
// is an error, as json.Marshal reports it, not a stack overflow.
func TestJSONTypedSelfReferenceIsAnError(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	if _, err := jsonTyped(m); err == nil {
		t.Fatal("jsonTyped: want an error for a map that contains itself")
	}
}

// deepMap returns a map nested n levels deep. Its innermost value is a string, one
// level below the innermost map.
func deepMap(n int) map[string]any {
	return deepMapWith(n, "x")
}

// deepMapWith is deepMap with leaf as the innermost value.
func deepMapWith(n int, leaf any) map[string]any {
	m := map[string]any{"leaf": leaf}
	for i := 0; i < n; i++ {
		m = map[string]any{"k": m}
	}
	return m
}

// TestNormalizeNilDepthBound pins the exact bound: the walk accepts a document
// whose deepest value sits at depth maxDepth and refuses one level more, with or
// without a nil container to empty.
func TestNormalizeNilDepthBound(t *testing.T) {
	var nilSlice []any
	for name, leaf := range map[string]any{"nil-free": "x", "with a nil slice": nilSlice} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeNil(deepMapWith(maxDepth-1, leaf)); err != nil {
				t.Errorf("a document with its deepest value at depth %d: %v", maxDepth, err)
			}
			if _, err := normalizeNil(deepMapWith(maxDepth, leaf)); err == nil {
				t.Errorf("a document with its deepest value at depth %d: want an error", maxDepth+1)
			}
		})
	}
}

// TestApplyStrategicMergePatch_TypedRefusesCyclicAndTooDeepDocuments checks that
// through the typed path a patch or a resource that contains itself, or nests
// deeper than maxDepth, is an error and leaves the resource as it was.
func TestApplyStrategicMergePatch_TypedRefusesCyclicAndTooDeepDocuments(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	newResource := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "test"},
			"data": map[string]any{"a": "1"},
		}}
	}
	cyclic := func() map[string]any {
		m := map[string]any{}
		m["self"] = m
		return m
	}

	for name, tc := range map[string]struct {
		resource func() *unstructured.Unstructured
		patch    func() map[string]any
	}{
		"cyclic patch": {newResource, cyclic},
		"deep patch":   {newResource, func() map[string]any { return map[string]any{"data": deepMap(maxDepth + 1)} }},
		"cyclic resource": {
			func() *unstructured.Unstructured {
				r := newResource()
				r.Object["self"] = r.Object
				return r
			},
			func() map[string]any { return map[string]any{"data": map[string]any{"b": "2"}} },
		},
		"deep resource": {
			func() *unstructured.Unstructured {
				r := newResource()
				r.Object["deep"] = deepMap(maxDepth + 1)
				return r
			},
			func() map[string]any { return map[string]any{"data": map[string]any{"b": "2"}} },
		},
	} {
		t.Run(name, func(t *testing.T) {
			resource := tc.resource()
			before := resource.Object
			dataBefore := before["data"]
			if err := ApplyStrategicMergePatch(resource, tc.patch(), lookup); err == nil {
				t.Fatal("ApplyStrategicMergePatch: want an error")
			}
			if reflect.ValueOf(resource.Object).Pointer() != reflect.ValueOf(before).Pointer() {
				t.Error("resource.Object was replaced")
			}
			if !reflect.DeepEqual(resource.Object["data"], dataBefore) || len(resource.Object["data"].(map[string]any)) != 1 {
				t.Errorf("resource data changed: %v", resource.Object["data"])
			}
		})
	}

	// A nested patch well inside the bound is still applied; the bound itself is
	// pinned by TestNormalizeNilDepthBound.
	resource := newResource()
	if err := ApplyStrategicMergePatch(resource, map[string]any{"data": deepMap(10)}, lookup); err != nil {
		t.Errorf("ApplyStrategicMergePatch: a shallow nested patch: %v", err)
	}
}

// TestNormalizeNilCopiesOnlyWhenNeeded checks that a document without a nil
// slice or map is returned as it is, not copied, and one with a nil is copied.
func TestNormalizeNilCopiesOnlyWhenNeeded(t *testing.T) {
	var nilSlice []any
	clean := map[string]any{
		"a": map[string]any{"b": []any{"x", map[string]any{"c": nil}}},
		"d": "e",
	}
	got, err := normalizeNil(clean)
	if err != nil {
		t.Fatalf("normalizeNil: %v", err)
	}
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(clean).Pointer() {
		t.Error("a document without a nil container was copied")
	}

	dirty := map[string]any{"a": map[string]any{"b": nilSlice}}
	got, err = normalizeNil(dirty)
	if err != nil {
		t.Fatalf("normalizeNil: %v", err)
	}
	if reflect.ValueOf(got).Pointer() == reflect.ValueOf(dirty).Pointer() {
		t.Error("a document with a nil container was not copied")
	}
	if dirty["a"].(map[string]any)["b"].([]any) != nil {
		t.Error("normalizeNil changed its input")
	}
	if b := got["a"].(map[string]any)["b"].([]any); b == nil || len(b) != 0 {
		t.Errorf("b = %#v, want an empty non-nil list", b)
	}

	if got, err := normalizeNil(nil); got != nil || err != nil {
		t.Errorf("normalizeNil(nil) = %v, %v, want nil, nil", got, err)
	}
}

// TestApplyStrategicMergePatch_TypedNilMapInPatchKeepsField checks that on a known
// kind a nil map in the patch is an empty one, so the field is kept as {}, while
// an explicit nil value still deletes it.
func TestApplyStrategicMergePatch_TypedNilMapInPatchKeepsField(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "test", "labels": map[string]any{"k": "v"}, "annotations": map[string]any{"a": "b"}},
	}}
	var nilMap map[string]any
	patch := map[string]any{"metadata": map[string]any{"labels": nilMap, "annotations": nil}}
	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	meta := resource.Object["metadata"].(map[string]any)
	if labels, ok := meta["labels"].(map[string]any); !ok || len(labels) != 1 {
		t.Errorf("labels = %#v, want the existing labels kept", meta["labels"])
	}
	if _, found := meta["annotations"]; found {
		t.Errorf("annotations = %#v, want it deleted by the explicit nil", meta["annotations"])
	}
}

// widget returns a resource of a kind the lookup does not know, so that the JSON
// merge fallback applies to it.
func widget() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.io/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "test"},
		"spec": map[string]any{
			"a":    "1",
			"list": []any{"x"},
			"m":    map[string]any{"k": "v"},
		},
	}}
}

// TestApplyStrategicMergePatch_JSONMergeNilMapKeepsField checks that the JSON
// merge fallback reads a nil map in the patch as an empty one, as the typed path
// does, so the field is kept and nothing is deleted.
func TestApplyStrategicMergePatch_JSONMergeNilMapKeepsField(t *testing.T) {
	var nilMap map[string]any
	for name, patch := range map[string]map[string]any{
		"nested": {"spec": map[string]any{"m": nilMap}},
		"parent": {"spec": nilMap},
	} {
		t.Run(name, func(t *testing.T) {
			resource := widget()
			if err := ApplyStrategicMergePatch(resource, patch, nil); err != nil {
				t.Fatalf("ApplyStrategicMergePatch: %v", err)
			}
			want := widget().Object
			if !reflect.DeepEqual(resource.Object, want) {
				t.Errorf("resource = %#v, want it unchanged: %#v", resource.Object, want)
			}
		})
	}
}

// TestApplyStrategicMergePatch_JSONMergeNilSliceAndMapAlike checks that a nil
// slice and a nil map in a patch are read alike on the fallback and on a known
// kind: an empty one, never a deletion.
func TestApplyStrategicMergePatch_JSONMergeNilSliceAndMapAlike(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	var nilSlice []any
	var nilMap map[string]any

	fallback := widget()
	if err := ApplyStrategicMergePatch(fallback, map[string]any{"spec": map[string]any{"list": nilSlice, "m": nilMap}}, nil); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	spec := fallback.Object["spec"].(map[string]any)
	if list, ok := spec["list"].([]any); !ok || len(list) != 0 {
		t.Errorf("fallback list = %#v, want an empty list", spec["list"])
	}
	if m, ok := spec["m"].(map[string]any); !ok || len(m) != 1 {
		t.Errorf("fallback m = %#v, want the existing map kept", spec["m"])
	}

	typed := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "test", "labels": map[string]any{"k": "v"}},
	}}
	if err := ApplyStrategicMergePatch(typed, map[string]any{"metadata": map[string]any{"labels": nilMap}}, lookup); err != nil {
		t.Fatalf("typed: %v", err)
	}
	if labels, ok := typed.Object["metadata"].(map[string]any)["labels"].(map[string]any); !ok || len(labels) != 1 {
		t.Errorf("typed labels = %#v, want the existing labels kept", labels)
	}
}

// TestApplyStrategicMergePatch_JSONMergeExplicitNilDeletes checks that on the
// fallback only an explicit nil value deletes, as RFC 7386 says.
func TestApplyStrategicMergePatch_JSONMergeExplicitNilDeletes(t *testing.T) {
	resource := widget()
	if err := ApplyStrategicMergePatch(resource, map[string]any{"spec": map[string]any{"m": nil}}, nil); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	spec := resource.Object["spec"].(map[string]any)
	if _, found := spec["m"]; found {
		t.Errorf("m = %#v, want it deleted by the explicit nil", spec["m"])
	}
	if spec["a"] != "1" {
		t.Errorf("a = %#v, want it kept", spec["a"])
	}
}

// TestApplyStrategicMergePatch_NilPatchIsNoOp checks that a nil patch adds
// nothing to a resource, on the fallback as on a known kind: it used to leave a
// custom resource with a nil object. A known kind still normalizes the resource
// itself, as it does for any patch, so a nil map in it becomes an empty one; the
// fallback leaves the resource untouched.
func TestApplyStrategicMergePatch_NilPatchIsNoOp(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	configMap := func(data map[string]any) func() *unstructured.Unstructured {
		return func() *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]any{"name": "test"},
				"data":       data,
			}}
		}
	}
	var nilMap map[string]any
	for name, tc := range map[string]struct {
		lookup KindLookup
		build  func() *unstructured.Unstructured
		want   func() *unstructured.Unstructured
	}{
		"fallback":                      {nil, widget, widget},
		"typed":                         {lookup, configMap(map[string]any{"k": "v"}), configMap(map[string]any{"k": "v"})},
		"typed normalizes the resource": {lookup, configMap(nilMap), configMap(map[string]any{})},
	} {
		t.Run(name, func(t *testing.T) {
			resource := tc.build()
			if err := ApplyStrategicMergePatch(resource, nil, tc.lookup); err != nil {
				t.Fatalf("ApplyStrategicMergePatch: %v", err)
			}
			if want := tc.want().Object; !reflect.DeepEqual(resource.Object, want) {
				t.Errorf("resource = %#v, want %#v", resource.Object, want)
			}
		})
	}
}

// nilContainerResource returns an object of the given kind and API version
// whose untouched fields hold a nil []any and a nil map[string]any, at the top
// of a section, nested in a map and nested in a list.
func nilContainerResource(apiVersion, kind, section string) *unstructured.Unstructured {
	var nilSlice []any
	var nilMap map[string]any
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "test"},
		section: map[string]any{
			"list":   nilSlice,
			"m":      nilMap,
			"inMap":  map[string]any{"list": nilSlice, "m": nilMap},
			"inList": []any{map[string]any{"list": nilSlice, "m": nilMap}},
			"a":      "1",
		},
	}}
}

// TestApplyStrategicMergePatch_JSONMergeNormalizesNilContainersInResource checks
// that the fallback reads a nil slice or map in the resource as an empty one, as
// the typed path does, instead of serializing null (go-kure/launcher#1195).
func TestApplyStrategicMergePatch_JSONMergeNormalizesNilContainersInResource(t *testing.T) {
	for name, patch := range map[string]map[string]any{
		"empty patch":          {},
		"patch on other field": {"other": map[string]any{"x": "y"}},
	} {
		t.Run(name, func(t *testing.T) {
			resource := nilContainerResource("example.io/v1", "Widget", "spec")
			if err := ApplyStrategicMergePatch(resource, patch, nil); err != nil {
				t.Fatalf("ApplyStrategicMergePatch: %v", err)
			}
			got, err := json.Marshal(resource.Object["spec"])
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			want := `{"a":"1","inList":[{"list":[],"m":{}}],"inMap":{"list":[],"m":{}},"list":[],"m":{}}`
			if string(got) != want {
				t.Errorf("spec = %s, want %s", got, want)
			}
		})
	}
}

// TestApplyStrategicMergePatch_JSONMergeMatchesTypedOnNilContainers checks that
// the same untouched nil containers serialize the same on the fallback as on a
// known kind (a ConfigMap, whose data and metadata.labels are free-form maps).
func TestApplyStrategicMergePatch_JSONMergeMatchesTypedOnNilContainers(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	var nilMap map[string]any
	build := func(apiVersion, kind string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata":   map[string]any{"name": "test", "labels": nilMap},
			"data":       nilMap,
		}}
	}
	typed, fallback := build("v1", "ConfigMap"), build("v1", "ConfigMap")
	for _, tc := range []struct {
		name     string
		resource *unstructured.Unstructured
		lookup   KindLookup
	}{{"typed", typed, lookup}, {"fallback", fallback, nil}} {
		if err := ApplyStrategicMergePatch(tc.resource, map[string]any{}, tc.lookup); err != nil {
			t.Fatalf("%s: ApplyStrategicMergePatch: %v", tc.name, err)
		}
	}
	if !reflect.DeepEqual(typed.Object, fallback.Object) {
		t.Errorf("fallback = %#v, typed = %#v, want them equal", fallback.Object, typed.Object)
	}
}

// TestApplyStrategicMergePatch_JSONMergeResourceNilContainerDeletedByNil checks
// that normalizing the resource leaves the RFC 7386 delete alone: an explicit nil
// in the patch still removes a field whose resource value was a nil container.
func TestApplyStrategicMergePatch_JSONMergeResourceNilContainerDeletedByNil(t *testing.T) {
	resource := nilContainerResource("example.io/v1", "Widget", "spec")
	if err := ApplyStrategicMergePatch(resource, map[string]any{"spec": map[string]any{"m": nil}}, nil); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	spec := resource.Object["spec"].(map[string]any)
	if _, found := spec["m"]; found {
		t.Errorf("m = %#v, want it deleted by the explicit nil", spec["m"])
	}
	if list, ok := spec["list"].([]any); !ok || list == nil || len(list) != 0 {
		t.Errorf("list = %#v, want an empty, non-nil list", spec["list"])
	}
}

// TestApplyStrategicMergePatch_JSONMergeResourceErrorLeavesResourceAlone checks
// that a resource nested deeper than maxDepth, or containing itself, is an error
// on the fallback, as on the typed path, and that the resource is left as it was.
func TestApplyStrategicMergePatch_JSONMergeResourceErrorLeavesResourceAlone(t *testing.T) {
	var nilSlice []any
	self := map[string]any{"list": nilSlice}
	self["self"] = self
	for name, spec := range map[string]map[string]any{
		"too deep":        deepMapWith(maxDepth, nilSlice),
		"contains itself": self,
	} {
		t.Run(name, func(t *testing.T) {
			resource := widget()
			resource.Object["spec"] = spec
			before := resource.Object["spec"]
			if err := ApplyStrategicMergePatch(resource, map[string]any{"a": "2"}, nil); err == nil {
				t.Fatal("ApplyStrategicMergePatch: want an error")
			}
			if !reflect.DeepEqual(resource.Object["spec"], before) || len(resource.Object) != 4 {
				t.Errorf("resource = %#v, want it unchanged", resource.Object)
			}
		})
	}
}

// TestApplyStrategicMergePatch_JSONMergeDepthBound checks that the fallback has
// the bound of the typed path: a patch whose deepest value sits at depth maxDepth
// merges, and one level more is an error that leaves the resource as it was.
func TestApplyStrategicMergePatch_JSONMergeDepthBound(t *testing.T) {
	resource := widget()
	if err := ApplyStrategicMergePatch(resource, map[string]any{"spec": deepMapWith(maxDepth-2, "x")}, nil); err != nil {
		t.Errorf("a patch with its deepest value at depth %d: %v", maxDepth, err)
	}
	resource = widget()
	if err := ApplyStrategicMergePatch(resource, map[string]any{"spec": deepMapWith(maxDepth-1, "x")}, nil); err == nil {
		t.Errorf("a patch with its deepest value at depth %d: want an error", maxDepth+1)
	}
	if !reflect.DeepEqual(resource.Object, widget().Object) {
		t.Errorf("resource = %#v, want it unchanged", resource.Object)
	}
}

// TestApplyStrategicMergePatch_JSONMergeLeavesPatchAlone checks that the fallback
// does not change the patch it is given, nil containers included.
func TestApplyStrategicMergePatch_JSONMergeLeavesPatchAlone(t *testing.T) {
	var nilSlice []any
	var nilMap map[string]any
	patch := map[string]any{"spec": map[string]any{"list": nilSlice, "m": nilMap, "a": "2"}}
	if err := ApplyStrategicMergePatch(widget(), patch, nil); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	spec := patch["spec"].(map[string]any)
	if list, _ := spec["list"].([]any); list != nil {
		t.Errorf("list = %#v, want the nil slice as given", spec["list"])
	}
	if m, _ := spec["m"].(map[string]any); m != nil {
		t.Errorf("m = %#v, want the nil map as given", spec["m"])
	}
}

// TestApplyStrategicMergePatch_JSONMergeSelfContainingPatchIsAnError checks that
// a patch that contains itself is an error on the fallback, not a stack overflow,
// and leaves the resource as it was.
func TestApplyStrategicMergePatch_JSONMergeSelfContainingPatchIsAnError(t *testing.T) {
	patch := map[string]any{"spec": map[string]any{}}
	patch["spec"].(map[string]any)["self"] = patch
	resource := widget()
	if err := ApplyStrategicMergePatch(resource, patch, nil); err == nil {
		t.Fatal("ApplyStrategicMergePatch: want an error for a patch that contains itself")
	}
	if !reflect.DeepEqual(resource.Object, widget().Object) {
		t.Errorf("resource = %#v, want it unchanged", resource.Object)
	}
}

// TestApplyStrategicMergePatch_TypedNilSliceInPatchStaysEmpty checks that a nil
// slice in a patch writes an empty list, as it did before the patch was
// normalized by a JSON round trip, instead of null, which deletes the field.
func TestApplyStrategicMergePatch_TypedNilSliceInPatchStaysEmpty(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "test"},
			"spec": map[string]any{
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{map[string]any{"name": "app", "args": []any{"-v"}}},
					},
				},
			},
		},
	}
	var none []any
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "app", "args": none}},
				},
			},
		},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	containers, _, err := unstructured.NestedSlice(resource.Object, "spec", "template", "spec", "containers")
	if err != nil || len(containers) != 1 {
		t.Fatalf("containers = %v, %v", containers, err)
	}
	args, found := containers[0].(map[string]any)["args"]
	if !found {
		t.Fatal("args was deleted, want an empty list")
	}
	if list, ok := args.([]any); !ok || len(list) != 0 {
		t.Errorf("args = %#v, want an empty list", args)
	}
	if patch["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["args"].([]any) != nil {
		t.Error("patch was changed")
	}
}

// TestApplyStrategicMergePatch_TypedLeavesPatchUnchanged checks that the typed
// path does not mutate the caller's patch.
func TestApplyStrategicMergePatch_TypedLeavesPatchUnchanged(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "test"},
			"data":       map[string]any{"a": "1", "b": "2"},
		},
	}
	patch := map[string]any{
		"data":     map[string]any{"a": "x", "b": nil},
		"metadata": map[string]any{"labels": map[string]any{"k": "v"}},
	}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err != nil {
		t.Fatalf("ApplyStrategicMergePatch: %v", err)
	}
	data := patch["data"].(map[string]any)
	if len(data) != 2 || data["a"] != "x" {
		t.Errorf("patch data = %#v, want it unchanged", data)
	}
	if v, found := data["b"]; !found || v != nil {
		t.Errorf("patch data[b] = %#v (found %v), want an explicit nil", v, found)
	}
	if _, found := resource.Object["data"].(map[string]any)["b"]; found {
		t.Errorf("resource data = %#v, want b deleted", resource.Object["data"])
	}
}

// TestApplyStrategicMergePatch_TypedUnserializableLeavesResource checks that a
// value that cannot be serialized is an error and leaves the resource as it was.
func TestApplyStrategicMergePatch_TypedUnserializableLeavesResource(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}

	resource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "test"},
		},
	}
	patch := map[string]any{"data": map[string]any{"bad": make(chan int)}}

	if err := ApplyStrategicMergePatch(resource, patch, lookup); err == nil {
		t.Fatal("ApplyStrategicMergePatch: want an error for an unserializable value")
	}
	if _, found := resource.Object["data"]; found {
		t.Errorf("resource changed on error: %v", resource.Object)
	}
}
