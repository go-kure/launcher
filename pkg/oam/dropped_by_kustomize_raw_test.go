package oam

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// rawLocalConfig is an unstructured ConfigMap whose local-config annotation is
// the given value as authored in YAML, so a boolean or a number stays one.
func rawLocalConfig(value any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"namespace":   "shop",
			"name":        "settings",
			"annotations": map[string]any{localConfigAnnotation: value},
		},
	}}
}

// TestDroppedByKustomize_ReadsARawNonStringValue: kustomize keeps an object only
// where the local-config annotation is missing or its text is exactly "false"
// (sigs.k8s.io/kustomize/api v0.21.3 resource/factory.go:149-150: `if !exist ||
// localConfig == "false" { keep }`). Any other value drops it: a boolean true, a
// number or a null authored in YAML included. An unstructured object holds such a
// value as it was authored, and GetAnnotations returns no annotations then, so the
// value is read from the object's content. A boolean false reads as the text "false".
func TestDroppedByKustomize_ReadsARawNonStringValue(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		missing bool
		want    bool
	}{
		{name: "string true", value: "true", want: true},
		{name: "string false", value: "false", want: false},
		{name: "boolean true", value: true, want: true},
		{name: "boolean false", value: false, want: false},
		{name: "number 1", value: int64(1), want: true},
		{name: "number 0", value: int64(0), want: true},
		{name: "float 1", value: float64(1), want: true},
		{name: "null", value: nil, want: true},
		{name: "empty string", value: "", want: true},
		{name: "annotation missing", missing: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := rawLocalConfig(tc.value)
			if tc.missing {
				unstructured.RemoveNestedField(u.Object, "metadata", "annotations")
			}
			var obj client.Object = u
			if got := droppedByKustomize(obj); got != tc.want {
				t.Errorf("droppedByKustomize(local-config: %#v, missing %v) = %v, want %v", tc.value, tc.missing, got, tc.want)
			}
		})
	}
}

// TestCheckCrossDocumentCollisions_SkipsARawBooleanLocalConfig: the same object
// from two documents is no collision when kustomize drops it from both.
func TestCheckCrossDocumentCollisions_SkipsARawBooleanLocalConfig(t *testing.T) {
	a := client.Object(rawLocalConfig(true))
	b := client.Object(rawLocalConfig(true))
	err := CheckCrossDocumentCollisions([]GeneratedDocument{
		generatedDoc("shop", "one", &a),
		generatedDoc("shop", "two", &b),
	})
	if err != nil {
		t.Errorf("two dropped objects reported as a collision: %v", err)
	}
}
