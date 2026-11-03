package patch

import (
	"encoding/json"
	"fmt"
	"math"
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

func TestDeepCopyMap(t *testing.T) {
	original := map[string]any{
		"a": "hello",
		"b": map[string]any{
			"c": []any{int64(1), int64(2)},
		},
	}

	copied := deepCopyMap(original)

	// Mutate the copy
	copied["a"] = "world"
	innerCopy := copied["b"].(map[string]any)
	innerCopy["c"] = []any{int64(3)}

	// Original should be unchanged
	if original["a"] != "hello" {
		t.Error("original was mutated")
	}
	innerOrig := original["b"].(map[string]any)
	origSlice := innerOrig["c"].([]any)
	if len(origSlice) != 2 {
		t.Error("original nested slice was mutated")
	}
}
