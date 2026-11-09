package patch

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These rows pin how a TOML patch line is typed when its key is only dots. Such a line sets the
// field that the header names, so the header supplies the field name for the integer test. A line
// with a key of its own keeps being typed from that key.

func tomlHeaderContext() *VariableContext {
	return &VariableContext{Values: map[string]any{"n": 3, "s": "3"}}
}

// tomlWithHeaderValue loads one TOML patch line and returns its value.
func tomlWithHeaderValue(t *testing.T, src string) any {
	t.Helper()
	patches, err := LoadTOMLPatchFile(strings.NewReader(src), tomlHeaderContext())
	if err != nil {
		t.Fatalf("LoadTOMLPatchFile: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("loaded %d patches, want 1", len(patches))
	}
	return patches[0].Patch.Value
}

func TestTOMLPatchValue_HeaderSuppliesIntegerField(t *testing.T) {
	t.Run("N1_header_names_the_field", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n.: 3\n"), 3)
	})
	t.Run("N2_key_of_two_dots", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n..: 3\n"), 3)
	})
	t.Run("N3_deep_header", func(t *testing.T) {
		src := "[deployment.app.spec.template.spec.terminationGracePeriodSeconds]\n.: 30\n"
		requireValue(t, tomlWithHeaderValue(t, src), 30)
	})
	t.Run("N9_selector_only_key", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n[0]: 3\n"), 3)
	})
	t.Run("N6_apply_writes_an_integer", func(t *testing.T) {
		patches, err := LoadTOMLPatchFile(strings.NewReader("[deployment.app.spec.replicas]\n.: 3\n"), nil)
		if err != nil {
			t.Fatalf("LoadTOMLPatchFile: %v", err)
		}
		dep := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "app"},
			"spec":       map[string]any{"replicas": int64(1)},
		}}
		set, err := NewPatchableAppSet([]*unstructured.Unstructured{dep}, patches)
		if err != nil {
			t.Fatalf("NewPatchableAppSet: %v", err)
		}
		resolved, err := set.Resolve()
		if err != nil || len(resolved) != 1 {
			t.Fatalf("Resolve: %d resources, err %v", len(resolved), err)
		}
		if err := resolved[0].Apply(); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		got, found, err := unstructured.NestedFieldNoCopy(resolved[0].Base.Object, "spec", "replicas")
		if err != nil || !found {
			t.Fatalf("spec.replicas not found (err %v)", err)
		}
		switch v := got.(type) {
		case int:
			if v != 3 {
				t.Errorf("spec.replicas is %v, want 3", v)
			}
		case int64:
			if v != 3 {
				t.Errorf("spec.replicas is %v, want 3", v)
			}
		default:
			t.Errorf("spec.replicas is %T %v, want an integer 3", got, got)
		}
	})
	// N8: an earlier name matches an integer field and the last name does not. The last name decides.
	t.Run("N8_last_name_decides", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas.label]\n.: 3\n"), "3")
	})
}

// Controls: they pass before and after the change. They show that the rows above change only
// the line with a key of only dots.
func TestTOMLPatchValue_HeaderSuppliesIntegerField_Controls(t *testing.T) {
	t.Run("Ca_named_key", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec]\nreplicas: 3\n"), 3)
	})
	t.Run("Cb_key_with_a_name_of_its_own", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\nvalue: 3\n"), "3")
	})
	t.Run("Cc_text_value", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n.: abc\n"), "abc")
	})
	t.Run("Cd_header_names_no_integer_field", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.metadata.name]\n.: 3\n"), "3")
	})
	t.Run("Ce_boolean", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.paused]\n.: true\n"), true)
	})
	t.Run("Cf_named_key_with_a_string_variable", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec]\nreplicas: ${values.s}\n"), "3")
	})
	t.Run("Cg_whole_reference_keeps_its_type", func(t *testing.T) {
		requireValue(t, tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n.: ${values.n}\n"), 3)
	})
	// N5: a line with a key of only dots types exactly like the named-key line, whatever that is.
	t.Run("N5_parity_with_the_named_key_for_a_string_variable", func(t *testing.T) {
		named := tomlWithHeaderValue(t, "[deployment.app.spec]\nreplicas: ${values.s}\n")
		dots := tomlWithHeaderValue(t, "[deployment.app.spec.replicas]\n.: ${values.s}\n")
		if named != dots {
			t.Errorf("the dots line is %T %v, the named line is %T %v", dots, dots, named, named)
		}
	})
}
