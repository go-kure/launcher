package patch

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These rows pin how a mapping value is handled. The simple YAML form decodes its document into
// one map type, and the decoder gives every nested mapping the same type. Variable substitution
// and the conversion to an unstructured value must treat that type like any other mapping.

func nestedMapContext() *VariableContext {
	return &VariableContext{Values: map[string]any{"v": "x"}}
}

func nestedMapConfigMap() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "app"},
		"data":       map[string]any{},
	}}
}

// applyLoadedPatch loads src, resolves it against one ConfigMap named app, applies it and returns
// the object. A panic is reported as a failed row, so one panicking row does not stop the others.
func applyLoadedPatch(t *testing.T, src string, ctx *VariableContext) (obj map[string]any) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Apply panicked: %v", r)
			obj = nil
		}
	}()
	patches, err := LoadPatchFileWithVariables(strings.NewReader(src), ctx)
	if err != nil {
		t.Fatalf("LoadPatchFileWithVariables: %v", err)
	}
	set, err := NewPatchableAppSet([]*unstructured.Unstructured{nestedMapConfigMap()}, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return resolved[0].Base.Object
}

// nestedString reads a string at path in obj, or reports a failed row.
func nestedString(t *testing.T, obj map[string]any, want string, path ...string) {
	t.Helper()
	if obj == nil {
		return
	}
	got, found, err := unstructured.NestedString(obj, path...)
	if err != nil || !found {
		t.Errorf("%s is not a string in the object (found %v, err %v)", strings.Join(path, "."), found, err)
		return
	}
	if got != want {
		t.Errorf("%s is %q, want %q", strings.Join(path, "."), got, want)
	}
}

func TestSimpleFormNestedMap_Substitution(t *testing.T) {
	t.Run("N1_nested_map", func(t *testing.T) {
		value, ok := yamlPatchValue(t, "app.metadata.labels:\n  a: ${values.v}\n", nestedMapContext()).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		if value["a"] != "x" {
			t.Errorf("nested value is %T %v, want string x", value["a"], value["a"])
		}
	})
	t.Run("N2_list_of_maps", func(t *testing.T) {
		value, ok := yamlPatchValue(t, "app.data:\n  list:\n    - k: ${values.v}\n", nestedMapContext()).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		list, ok := value["list"].([]any)
		if !ok || len(list) != 1 {
			t.Fatalf("list is %T %v, want one item", value["list"], value["list"])
		}
		item, ok := list[0].(map[string]any)
		if !ok {
			t.Fatalf("list item is %T, want map[string]any", list[0])
		}
		if item["k"] != "x" {
			t.Errorf("list item value is %T %v, want string x", item["k"], item["k"])
		}
	})
	t.Run("N3_value_type", func(t *testing.T) {
		got := yamlPatchValue(t, "app.data:\n  outer:\n    inner: b\n", nil)
		value, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("patch value is %T, want map[string]any", got)
		}
		if _, ok := value["outer"].(map[string]any); !ok {
			t.Errorf("nested value is %T, want map[string]any", value["outer"])
		}
	})
}

func TestSimpleFormNestedMap_Apply(t *testing.T) {
	t.Run("N4_nested_map_with_variable", func(t *testing.T) {
		obj := applyLoadedPatch(t, "app.metadata.labels:\n  a: ${values.v}\n", nestedMapContext())
		nestedString(t, obj, "x", "metadata", "labels", "a")
	})
	t.Run("N5_nested_map_without_variable", func(t *testing.T) {
		obj := applyLoadedPatch(t, "app.metadata.labels:\n  a: b\n", nil)
		nestedString(t, obj, "b", "metadata", "labels", "a")
	})
	t.Run("N6_list_of_maps", func(t *testing.T) {
		obj := applyLoadedPatch(t, "app.data:\n  list:\n    - k: ${values.v}\n", nestedMapContext())
		if obj == nil {
			return
		}
		list, found, err := unstructured.NestedSlice(obj, "data", "list")
		if err != nil || !found || len(list) != 1 {
			t.Fatalf("data.list is not a one item list (found %v, err %v)", found, err)
		}
		item, ok := list[0].(map[string]any)
		if !ok || item["k"] != "x" {
			t.Errorf("data.list[0] is %T %v, want a map with k x", list[0], list[0])
		}
	})
}

// A caller that builds a patch value of the map type by hand reaches the same two places without
// the loader: the conversion at Apply and the substitution of variables.
func TestSimpleFormNestedMap_CallerBuiltValue(t *testing.T) {
	t.Run("N7_apply_converts_the_map_type", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Apply panicked: %v", r)
			}
		}()
		r := &ResourceWithPatches{
			Name: "app",
			Base: nestedMapConfigMap(),
			Patches: []PatchOp{{
				Op:    "replace",
				Path:  "metadata.labels",
				Value: RawPatchMap{"a": "b", "inner": RawPatchMap{"c": "d"}},
			}},
		}
		if err := r.Apply(); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		nestedString(t, r.Base.Object, "b", "metadata", "labels", "a")
		nestedString(t, r.Base.Object, "d", "metadata", "labels", "inner", "c")
	})
	t.Run("N8_substitution_enters_the_map_type", func(t *testing.T) {
		got, err := substituteVariablesInValue(RawPatchMap{"a": "${values.v}"}, nestedMapContext())
		if err != nil {
			t.Fatalf("substituteVariablesInValue: %v", err)
		}
		value, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("result is %T, want map[string]any", got)
		}
		if value["a"] != "x" {
			t.Errorf("nested value is %T %v, want string x", value["a"], value["a"])
		}
	})
}

// Controls: these pass before and after the change. They show that the rows above fail for the
// named type and not for the variable syntax or the apply path.
func TestSimpleFormNestedMap_Controls(t *testing.T) {
	t.Run("Ca_targeted_form", func(t *testing.T) {
		src := "- target: app\n  patch:\n    metadata.labels:\n      a: ${values.v}\n"
		nestedString(t, applyLoadedPatch(t, src, nestedMapContext()), "x", "metadata", "labels", "a")
	})
	t.Run("Cb_simple_flat_key", func(t *testing.T) {
		nestedString(t, applyLoadedPatch(t, "app.metadata.labels.a: ${values.v}\n", nestedMapContext()), "x", "metadata", "labels", "a")
	})
	t.Run("Cc_strategic_payload", func(t *testing.T) {
		src := "- target: app\n  type: strategic\n  patch:\n    data:\n      a: ${values.v}\n"
		patches, err := LoadPatchFileWithVariables(strings.NewReader(src), nestedMapContext())
		if err != nil || len(patches) != 1 || patches[0].Strategic == nil {
			t.Fatalf("loaded %d patches, err %v, want 1 strategic", len(patches), err)
		}
		data, ok := patches[0].Strategic.Patch["data"].(map[string]any)
		if !ok || data["a"] != "x" {
			t.Errorf("strategic data is %T %v, want a map with a x", patches[0].Strategic.Patch["data"], patches[0].Strategic.Patch["data"])
		}
	})
	t.Run("Cd_targeted_list_of_maps", func(t *testing.T) {
		src := "- target: app\n  patch:\n    data:\n      list:\n        - k: ${values.v}\n"
		obj := applyLoadedPatch(t, src, nestedMapContext())
		list, _, _ := unstructured.NestedSlice(obj, "data", "list")
		if len(list) != 1 {
			t.Fatalf("data.list has %d items, want 1", len(list))
		}
		if item, ok := list[0].(map[string]any); !ok || item["k"] != "x" {
			t.Errorf("data.list[0] is %T %v, want a map with k x", list[0], list[0])
		}
	})
}
