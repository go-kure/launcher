package patch

import (
	"reflect"
	"strings"
	"testing"
)

// These rows pin how a variable is substituted inside a mapping with a non-string key. The YAML
// decoder gives such a mapping the type map[interface{}]interface{}, and substitution must enter it
// like any other mapping. Its keys stay as authored: Apply turns them into strings later.

type anyMap = map[interface{}]interface{}

func nonStringContext() *VariableContext {
	return &VariableContext{Values: map[string]any{"v": "x", "n": 3}}
}

const (
	nonStringTargeted = "- target: app\n  patch:\n    data.cfg:\n      1: ${values.v}\n"
	nonStringSimple   = "app.data.cfg:\n  1: ${values.v}\n"
)

// requireAnyMap fails unless got is a map[interface{}]interface{} equal to want.
func requireAnyMap(t *testing.T, form string, got any, want anyMap) {
	t.Helper()
	m, ok := got.(anyMap)
	if !ok {
		t.Errorf("%s: value is %T %v, want %T %v", form, got, got, want, want)
		return
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("%s: value is %T %v, want %v", form, got, got, want)
	}
}

// requireOnlyValue fails unless got is a mapping with exactly one entry whose value is want. It is
// for a key that does not equal itself (.nan) or that has no useful spelling (a null key).
func requireOnlyValue(t *testing.T, form string, got any, want any) {
	t.Helper()
	m, ok := got.(anyMap)
	if !ok {
		t.Errorf("%s: value is %T %v, want a map[interface{}]interface{}", form, got, got)
		return
	}
	if len(m) != 1 {
		t.Errorf("%s: value has %d entries, want 1: %v", form, len(m), m)
		return
	}
	for _, v := range m {
		if v != want {
			t.Errorf("%s: the entry is %T %v, want %T %v", form, v, v, want, want)
		}
	}
}

func TestNonStringKey_Substitution(t *testing.T) {
	ctx := nonStringContext()
	t.Run("N1_simple_form", func(t *testing.T) {
		requireAnyMap(t, "simple", yamlPatchValue(t, nonStringSimple, ctx), anyMap{1: "x"})
	})
	t.Run("N2_targeted_form", func(t *testing.T) {
		requireAnyMap(t, "targeted", yamlPatchValue(t, nonStringTargeted, ctx), anyMap{1: "x"})
	})
	t.Run("N3_inside_a_string_key_mapping", func(t *testing.T) {
		src := "- target: app\n  patch:\n    data.cfg:\n      a:\n        1: ${values.v}\n"
		value, ok := yamlPatchValue(t, src, ctx).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		requireAnyMap(t, "targeted nested", value["a"], anyMap{1: "x"})
	})
	t.Run("N4_inside_a_list", func(t *testing.T) {
		src := "- target: app\n  patch:\n    data.cfg:\n      - 1: ${values.v}\n"
		list, ok := yamlPatchValue(t, src, ctx).([]any)
		if !ok || len(list) != 1 {
			t.Fatalf("the patch value is not a one item list")
		}
		requireAnyMap(t, "targeted list", list[0], anyMap{1: "x"})
	})
	t.Run("N5_strategic_payload", func(t *testing.T) {
		src := "- target: app\n  type: strategic\n  patch:\n    data:\n      cfg:\n        1: ${values.v}\n"
		patches, err := LoadPatchFileWithVariables(strings.NewReader(src), ctx)
		if err != nil || len(patches) != 1 || patches[0].Strategic == nil {
			t.Fatalf("loaded %d patches, err %v, want 1 strategic", len(patches), err)
		}
		data, ok := patches[0].Strategic.Patch["data"].(map[string]any)
		if !ok {
			t.Fatalf("data is %T", patches[0].Strategic.Patch["data"])
		}
		requireAnyMap(t, "strategic", data["cfg"], anyMap{1: "x"})
	})
	t.Run("N7_depth_in_a_mapping", func(t *testing.T) {
		got, ok := yamlPatchValue(t, "app.data.cfg:\n  1:\n    2: ${values.v}\n", ctx).(anyMap)
		if !ok {
			t.Fatal("the patch value is not a map[interface{}]interface{}")
		}
		requireAnyMap(t, "depth two mapping", got[1], anyMap{2: "x"})
	})
	t.Run("N7_depth_in_a_list", func(t *testing.T) {
		got, ok := yamlPatchValue(t, "app.data.cfg:\n  1:\n    - ${values.v}\n", ctx).(anyMap)
		if !ok {
			t.Fatal("the patch value is not a map[interface{}]interface{}")
		}
		if want := []any{"x"}; !reflect.DeepEqual(got[1], want) {
			t.Errorf("depth two list: value is %T %v, want %v", got[1], got[1], want)
		}
	})
	t.Run("N8_simple_form_inside_a_string_key_mapping", func(t *testing.T) {
		value, ok := yamlPatchValue(t, "app.data.cfg:\n  a:\n    1: ${values.v}\n", ctx).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		requireAnyMap(t, "simple nested", value["a"], anyMap{1: "x"})
	})
	t.Run("N9_mixed_keys", func(t *testing.T) {
		requireAnyMap(t, "mixed", yamlPatchValue(t, "app.data.cfg:\n  1: a\n  b: ${values.v}\n", ctx), anyMap{1: "a", "b": "x"})
	})
	t.Run("N10_nan_key", func(t *testing.T) {
		requireOnlyValue(t, "nan key", yamlPatchValue(t, "app.data.cfg:\n  .nan: ${values.v}\n", ctx), "x")
	})
	t.Run("N10_null_key", func(t *testing.T) {
		requireOnlyValue(t, "null key", yamlPatchValue(t, "app.data.cfg:\n  ~: ${values.v}\n", ctx), "x")
	})
	t.Run("N11_whole_reference_keeps_its_type", func(t *testing.T) {
		requireAnyMap(t, "whole reference", yamlPatchValue(t, "app.data.cfg:\n  1: ${values.n}\n", ctx), anyMap{1: 3})
	})
	t.Run("N12_embedded_reference", func(t *testing.T) {
		requireAnyMap(t, "embedded", yamlPatchValue(t, "app.data.cfg:\n  1: pre-${values.v}-post\n", ctx), anyMap{1: "pre-x-post"})
	})
}

// N6: the substituted value reaches the resource, with the key turned into a string by Apply.
func TestNonStringKey_Apply(t *testing.T) {
	t.Run("N6_apply_writes_the_substituted_value", func(t *testing.T) {
		obj := applyLoadedPatch(t, nonStringTargeted, nonStringContext())
		nestedString(t, obj, "x", "data", "cfg", "1")
	})
}

// Controls: they pass before and after the change. They show that the rows above fail for the
// map type and not for the variable syntax.
func TestNonStringKey_Controls(t *testing.T) {
	ctx := nonStringContext()
	t.Run("Ca_string_keys", func(t *testing.T) {
		src := "- target: app\n  patch:\n    data.cfg:\n      a: ${values.v}\n"
		got := yamlPatchValue(t, src, ctx)
		if want := map[string]any{"a": "x"}; !reflect.DeepEqual(got, want) {
			t.Errorf("value is %T %v, want %v", got, got, want)
		}
	})
	t.Run("Cb_loader_keeps_the_authored_key", func(t *testing.T) {
		m, ok := yamlPatchValue(t, nonStringTargeted, ctx).(anyMap)
		if !ok {
			t.Fatal("the patch value is not a map[interface{}]interface{}")
		}
		if _, found := m[1]; !found {
			t.Errorf("the integer key 1 is not in %v", m)
		}
	})
	t.Run("Cc_nil_context_keeps_the_text", func(t *testing.T) {
		requireAnyMap(t, "nil context", yamlPatchValue(t, nonStringTargeted, nil), anyMap{1: "${values.v}"})
	})
	t.Run("Cd_unknown_reference_stays", func(t *testing.T) {
		src := "- target: app\n  patch:\n    data.cfg:\n      1: ${values.zz}\n"
		requireAnyMap(t, "unknown reference", yamlPatchValue(t, src, ctx), anyMap{1: "${values.zz}"})
	})
	t.Run("Ce_argument_is_not_written", func(t *testing.T) {
		in := anyMap{1: "${values.v}"}
		if _, err := substituteVariablesInValue(in, ctx); err != nil {
			t.Fatalf("substituteVariablesInValue: %v", err)
		}
		if !reflect.DeepEqual(in, anyMap{1: "${values.v}"}) {
			t.Errorf("the argument was written: %v", in)
		}
	})
}
