package patch

import (
	"strings"
	"testing"
)

// These rows pin how a patch value gets its type. A YAML patch value keeps the type YAML gave
// it, so a quoted "8080" stays a string. A whole variable reference keeps the variable's own
// type. A TOML patch value has no type of its own: a double-quoted value is a string, and an
// unquoted one is typed by inferValueType.

// yamlPatchValue loads src and returns the value of the one field-level patch it holds.
func yamlPatchValue(t *testing.T, src string, ctx *VariableContext) any {
	t.Helper()
	patches, err := LoadPatchFileWithVariables(strings.NewReader(src), ctx)
	if err != nil {
		t.Fatalf("LoadPatchFileWithVariables: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("loaded %d patches, want 1", len(patches))
	}
	return patches[0].Patch.Value
}

// requireValue fails unless got has the same type and value as want.
func requireValue(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("value is %T %v, want %T %v", got, got, want, want)
	}
}

func TestYAMLPatchValue_QuotedScalarsStayStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"Y1_quoted_bool", "data.enabled: \"true\"\n", "true"},
		{"Y2_quoted_port", "data.port: \"8080\"\n", "8080"},
		{"Y3_key_containing_port", "metadata.labels.support: \"1\"\n", "1"},
		{"Y4_targeted_key_containing_number", "- target: app\n  patch:\n    metadata.annotations.phoneNumber: \"5\"\n", "5"},
		{"Y4b_targeted_integer_field", "- target: app\n  patch:\n    data.port: \"8080\"\n", "8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireValue(t, yamlPatchValue(t, tc.src, nil), tc.want)
		})
	}
}

func TestYAMLPatchValue_StrategicPayloadKeepsQuotedStrings(t *testing.T) {
	patches, err := LoadPatchFile(strings.NewReader("- target: app\n  type: strategic\n  patch:\n    data:\n      debug: \"false\"\n      httpPort: \"80\"\n"))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	if len(patches) != 1 || patches[0].Strategic == nil {
		t.Fatalf("loaded %d patches, want 1 strategic", len(patches))
	}
	data, ok := patches[0].Strategic.Patch["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T", patches[0].Strategic.Patch["data"])
	}
	t.Run("Y5_debug", func(t *testing.T) { requireValue(t, data["debug"], "false") })
	t.Run("Y5_httpPort", func(t *testing.T) { requireValue(t, data["httpPort"], "80") })
}

// Y6: the targeted form and the simple form both hand a map value over as map[string]any.
// C11 asserted RawPatchMap for the simple form until the loader stopped decoding into that
// type; the flip is an intended change.
func TestYAMLPatchValue_QuotedScalarInMapValueStaysString(t *testing.T) {
	t.Run("Y6_targeted_form", func(t *testing.T) {
		value, ok := yamlPatchValue(t, "- target: app\n  patch:\n    metadata.labels:\n      weight: \"3\"\n", nil).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		requireValue(t, value["weight"], "3")
	})
	t.Run("C11_simple_form_control", func(t *testing.T) {
		value, ok := yamlPatchValue(t, "metadata.labels:\n  weight: \"3\"\n", nil).(map[string]any)
		if !ok {
			t.Fatal("the patch value is not a map[string]any")
		}
		requireValue(t, value["weight"], "3")
	})
}

func TestYAMLPatchValue_VariableText(t *testing.T) {
	ctx := &VariableContext{
		Values:   map[string]any{"portText": "8080", "replicas": 3, "a": 1, "b": 0},
		Features: map[string]bool{"debug": true},
	}
	t.Run("Y7_whole_reference_to_a_string", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "data.port: ${values.portText}\n", ctx), "8080")
	})
	t.Run("Y8_two_references_make_a_string", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "spec.replicas: \"${values.a}${values.b}\"\n", ctx), "10")
	})
	t.Run("embedded_reference_makes_a_string", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "spec.replicas: \"n${values.a}\"\n", ctx), "n1")
	})
	t.Run("undefined_reference_is_left_as_written", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "data.port: ${values.missing}\n", ctx), "${values.missing}")
	})
	// Controls: a whole reference keeps the variable's type, and an unquoted number is a number.
	// They pass without the fix; they exist for the mutants.
	t.Run("C1_whole_reference_to_an_int", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "spec.replicas: ${values.replicas}\n", ctx), 3)
	})
	t.Run("C2_whole_reference_to_a_bool", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "data.flag: ${features.debug}\n", ctx), true)
	})
	t.Run("C3_unquoted_number", func(t *testing.T) {
		requireValue(t, yamlPatchValue(t, "spec.replicas: 3\n", nil), 3)
	})
}

// tomlPatchValue loads one TOML patch line under a fixed header and returns its value.
func tomlPatchValue(t *testing.T, line string, ctx *VariableContext) any {
	t.Helper()
	patches, err := LoadTOMLPatchFile(strings.NewReader("[deployment.app]\n"+line+"\n"), ctx)
	if err != nil {
		t.Fatalf("LoadTOMLPatchFile: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("loaded %d patches, want 1", len(patches))
	}
	return patches[0].Patch.Value
}

func TestTOMLPatchValue(t *testing.T) {
	ctx := &VariableContext{
		Values:   map[string]any{"registry": "r.io", "replicas": 3, "portText": "8080"},
		Features: map[string]bool{},
	}
	for _, tc := range []struct {
		name string
		line string
		ctx  *VariableContext
		want any
	}{
		{"T1_quoted_bool_is_a_string", `data.enabled: "true"`, nil, "true"},
		{"T2_quoted_port_is_a_string", `data.port: "8080"`, nil, "8080"},
		{"T3_key_containing_port_stays_a_string", `metadata.labels.support: 1`, nil, "1"},
		{"T4_key_containing_number_stays_a_string", `metadata.labels.phoneNumber: 5`, nil, "5"},
		{"T5_quoted_value_is_substituted_as_text", `image: "${values.registry}/app"`, ctx, "r.io/app"},
		{"T6_quoted_whole_reference_is_text", `spec.replicas: "${values.replicas}"`, ctx, "3"},
		{"T9_whole_reference_to_a_string_is_not_inferred", `spec.replicas: ${values.portText}`, ctx, "8080"},
		{"T10_trailing_dot_still_names_the_field", `spec.replicas.: 3`, nil, 3},
		{"T11_dot_before_an_index_selector", `spec.replicas.[0]: 3`, nil, 3},
		{"T12_dot_before_an_append_selector", `spec.replicas.[-]: 3`, nil, 3},
		{"T7_quoted_escape", `note: "a\tb"`, nil, "a\tb"},
		// Controls: they pass without the fix; they exist for the mutants.
		{"C4_integer_field", `spec.replicas: 3`, nil, 3},
		{"C5_unquoted_bool", `enabled: true`, nil, true},
		{"C7_not_one_quoted_string", `note: "a" and "b"`, nil, `"a" and "b"`},
		{"C8_whole_reference_keeps_its_type", `spec.replicas: ${values.replicas}`, ctx, 3},
		{"C9_single_quotes_stay_as_written", `note: 'a b'`, nil, `'a b'`},
		{"C10_embedded_reference_is_text", `spec.replicas: n${values.replicas}`, ctx, "n3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireValue(t, tomlPatchValue(t, tc.line, tc.ctx), tc.want)
		})
	}
}

// C6: a key with a selector in the middle makes ParsePatchLine wrap the value in a map that
// holds the rest of the path, so the typed value sits one level down. The last segment of the
// key still decides: containerPort is an integer, and the same line under a name that only
// contains "port" stays a string.
func TestTOMLPatchValue_IntegerFieldAfterASelector(t *testing.T) {
	inner := func(line string) any {
		wrapped, ok := tomlPatchValue(t, line, nil).(map[string]any)
		if !ok {
			t.Fatalf("%s: the patch value is not a wrapped map", line)
		}
		for _, v := range wrapped {
			return v
		}
		t.Fatalf("%s: the wrapped map is empty", line)
		return nil
	}
	t.Run("C6_containerPort", func(t *testing.T) { requireValue(t, inner(`ports[0].containerPort: 8080`), 8080) })
	t.Run("T8_somePort", func(t *testing.T) { requireValue(t, inner(`ports[0].somePort: 8080`), "8080") })
}

func TestIsIntegerField(t *testing.T) {
	for key, want := range map[string]bool{
		"port":                          true,
		"spec.ports[0].containerPort":   true,
		"spec.REPLICAS":                 true,
		"spec.replicas[0]":              true,
		"spec.template.spec.fsGroup":    true,
		"containers[image=a.b].port":    true,
		"ports[-]":                      false,
		"support":                       false,
		"phoneNumber":                   false,
		"somePort":                      false,
		"delay":                         false,
		"spec.port.name":                false,
		"metadata.labels.weightedGroup": false,
		"":                              false,
		"spec.replicas.":                true,
		".spec.replicas":                true,
		"spec.replicas.[0]":             true,
		"spec.replicas.[-]":             true,
	} {
		if got := isIntegerField(key); got != want {
			t.Errorf("isIntegerField(%q) = %v, want %v", key, got, want)
		}
	}
}
