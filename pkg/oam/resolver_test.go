package oam_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/oam"
)

// resolveOK calls ResolveParameters and fatally fails the test on any error.
func resolveOK(t *testing.T, tmpl string, schema []oam.ParameterDecl, supplied map[string]any) []byte {
	t.Helper()
	out, err := oam.ResolveParameters([]byte(tmpl), schema, supplied)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out
}

// mustUnmarshal parses YAML bytes into map[string]any, failing on error.
func mustUnmarshal(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("result not valid YAML: %v\n%s", err, data)
	}
	return m
}

func TestResolveParameters_Scalar_Integer(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Required: true}}}
	out := resolveOK(t, "replicas: ${replicas}\n", schema, map[string]any{"replicas": 2})

	m := mustUnmarshal(t, out)
	if m["replicas"] != 2 {
		t.Errorf("replicas = %v (%T), want 2 (int)", m["replicas"], m["replicas"])
	}
	if strings.Contains(string(out), `"2"`) {
		t.Errorf("integer value should be unquoted in YAML output:\n%s", out)
	}
}

func TestResolveParameters_Scalar_String(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "image", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}
	out := resolveOK(t, "image: \"${image}\"\n", schema, map[string]any{"image": "myregistry/app:v1.2.3"})

	m := mustUnmarshal(t, out)
	if m["image"] != "myregistry/app:v1.2.3" {
		t.Errorf("image = %q, want myregistry/app:v1.2.3", m["image"])
	}
}

func TestResolveParameters_Scalar_Boolean(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "enabled", PropertySchema: oam.PropertySchema{Type: "boolean", Required: true}}}
	out := resolveOK(t, "enabled: ${enabled}\n", schema, map[string]any{"enabled": true})

	m := mustUnmarshal(t, out)
	if m["enabled"] != true {
		t.Errorf("enabled = %v, want true", m["enabled"])
	}
	if strings.Contains(string(out), `"true"`) {
		t.Errorf("boolean should be unquoted in YAML output:\n%s", out)
	}
}

func TestResolveParameters_Inline(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "name", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}
	out := resolveOK(t, "label: \"prefix-${name}-suffix\"\n", schema, map[string]any{"name": "myapp"})

	m := mustUnmarshal(t, out)
	if m["label"] != "prefix-myapp-suffix" {
		t.Errorf("label = %q, want prefix-myapp-suffix", m["label"])
	}
}

func TestResolveParameters_Inline_MultipleParams(t *testing.T) {
	schema := []oam.ParameterDecl{
		{Name: "env", PropertySchema: oam.PropertySchema{Type: "string", Required: true}},
		{Name: "app", PropertySchema: oam.PropertySchema{Type: "string", Required: true}},
	}
	out := resolveOK(t, "label: \"${env}-${app}\"\n", schema, map[string]any{
		"env": "prod",
		"app": "web",
	})

	m := mustUnmarshal(t, out)
	if m["label"] != "prod-web" {
		t.Errorf("label = %q, want prod-web", m["label"])
	}
}

func TestResolveParameters_Default_Applied(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Default: 3}}}
	out := resolveOK(t, "replicas: ${replicas}\n", schema, map[string]any{})

	m := mustUnmarshal(t, out)
	if m["replicas"] != 3 {
		t.Errorf("replicas = %v, want 3 (from default)", m["replicas"])
	}
}

func TestResolveParameters_Default_ReferencesEarlierParam(t *testing.T) {
	schema := []oam.ParameterDecl{
		{Name: "appName", PropertySchema: oam.PropertySchema{Type: "string", Required: true}},
		{Name: "tlsSecret", PropertySchema: oam.PropertySchema{Type: "string", Default: "${appName}-tls"}},
	}
	out := resolveOK(t, "secret: ${tlsSecret}\n", schema, map[string]any{"appName": "myapp"})

	m := mustUnmarshal(t, out)
	if m["secret"] != "myapp-tls" {
		t.Errorf("secret = %q, want myapp-tls", m["secret"])
	}
}

func TestResolveParameters_RequiredMissing(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "image", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}
	_, err := oam.ResolveParameters([]byte("image: ${image}\n"), schema, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required parameter")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error should name the missing parameter, got: %v", err)
	}
}

func TestResolveParameters_UnknownSuppliedKey(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "image", PropertySchema: oam.PropertySchema{Type: "string"}}}
	_, err := oam.ResolveParameters([]byte("image: ${image}\n"), schema, map[string]any{
		"image": "foo",
		"extra": "bar",
	})
	if err == nil {
		t.Fatal("expected error for undeclared supplied key")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error should name the undeclared key 'extra', got: %v", err)
	}
}

func TestResolveParameters_NodeType_Rejected(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "items", PropertySchema: oam.PropertySchema{Type: "array"}}}
	_, err := oam.ResolveParameters([]byte("items: ${items}\n"), schema, map[string]any{
		"items": []any{"a", "b"},
	})
	if err == nil {
		t.Fatal("expected error: array node substitution is not implemented")
	}
	if !strings.Contains(err.Error(), "items") {
		t.Errorf("error should mention parameter name 'items', got: %v", err)
	}
}

func TestResolveParameters_SetCoercion_Integer(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Required: true}}}
	// Simulate --set: CLI value arrives as a string.
	out := resolveOK(t, "replicas: ${replicas}\n", schema, map[string]any{"replicas": "3"})

	m := mustUnmarshal(t, out)
	if m["replicas"] != 3 {
		t.Errorf("replicas = %v (%T), want 3 (int) after string coercion", m["replicas"], m["replicas"])
	}
}

func TestResolveParameters_SetCoercion_Boolean(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "enabled", PropertySchema: oam.PropertySchema{Type: "boolean", Required: true}}}
	// Simulate --set: CLI value arrives as a string.
	out := resolveOK(t, "enabled: ${enabled}\n", schema, map[string]any{"enabled": "true"})

	m := mustUnmarshal(t, out)
	if m["enabled"] != true {
		t.Errorf("enabled = %v, want true after string coercion", m["enabled"])
	}
}

func TestResolveParameters_SetCoercion_BadInteger(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Required: true}}}
	_, err := oam.ResolveParameters([]byte("replicas: ${replicas}\n"), schema, map[string]any{"replicas": "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer value with integer type")
	}
	if !strings.Contains(err.Error(), "replicas") {
		t.Errorf("error should name the parameter, got: %v", err)
	}
}

func TestResolveParameters_FloatIntegerRejected(t *testing.T) {
	// values.yaml float64 like replicas: 2.5 must be rejected, not silently truncated.
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Required: true}}}
	_, err := oam.ResolveParameters([]byte("replicas: ${replicas}\n"), schema, map[string]any{"replicas": float64(2.5)})
	if err == nil {
		t.Fatal("expected error: 2.5 is not a valid integer (would silently truncate to 2)")
	}
	if !strings.Contains(err.Error(), "replicas") {
		t.Errorf("error should name the parameter, got: %v", err)
	}
}

func TestResolveParameters_FloatWholeNumberAccepted(t *testing.T) {
	// A whole-number float like 2.0 decoded from YAML is acceptable for integer params.
	schema := []oam.ParameterDecl{{Name: "replicas", PropertySchema: oam.PropertySchema{Type: "integer", Required: true}}}
	out := resolveOK(t, "replicas: ${replicas}\n", schema, map[string]any{"replicas": float64(2.0)})
	m := mustUnmarshal(t, out)
	if m["replicas"] != 2 {
		t.Errorf("replicas = %v (%T), want 2 (int)", m["replicas"], m["replicas"])
	}
}

func TestResolveParameters_StringParamRejectsMap(t *testing.T) {
	// image: {repo: ghcr.io/app} in values.yaml must be rejected, not stringified.
	schema := []oam.ParameterDecl{{Name: "image", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}
	_, err := oam.ResolveParameters([]byte("image: ${image}\n"), schema, map[string]any{
		"image": map[string]any{"repo": "ghcr.io/app"},
	})
	if err == nil {
		t.Fatal("expected error: map value is not a valid string parameter")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error should name the parameter, got: %v", err)
	}
}

func TestResolveParameters_StringParamRejectsNull(t *testing.T) {
	// image: null in values.yaml must be rejected.
	schema := []oam.ParameterDecl{{Name: "image", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}
	_, err := oam.ResolveParameters([]byte("image: ${image}\n"), schema, map[string]any{"image": nil})
	if err == nil {
		t.Fatal("expected error: null is not a valid string parameter")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error should name the parameter, got: %v", err)
	}
}

func TestResolveParameters_UnknownPlaceholderInAppYAML(t *testing.T) {
	schema := []oam.ParameterDecl{}
	_, err := oam.ResolveParameters([]byte("image: ${undefined}\n"), schema, map[string]any{})
	if err == nil {
		t.Fatal("expected error for undeclared placeholder in app YAML")
	}
	if !strings.Contains(err.Error(), "undefined") {
		t.Errorf("error should name the undeclared placeholder, got: %v", err)
	}
}

// TestResolveParameters_StringWithSpecialChars verifies that string values
// containing characters that would break raw YAML (colons, hashes, quotes,
// backslashes, newlines) survive the substitution round-trip intact.
// Both full-value (entire scalar is the placeholder) and inline (placeholder
// embedded in a larger string) substitution paths are exercised.
func TestResolveParameters_StringWithSpecialChars(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"colon", "host: example.com"},
		{"hash", "value # not a comment"},
		{"double-quote", `say "hello"`},
		{"backslash", `C:\Users\foo`},
		{"newline", "line1\nline2"},
		{"combined", "host: example.com # note \"special\" C:\\path\nnext"},
	}

	schema := []oam.ParameterDecl{{Name: "text", PropertySchema: oam.PropertySchema{Type: "string", Required: true}}}

	for _, tc := range cases {
		t.Run("full-value/"+tc.name, func(t *testing.T) {
			out := resolveOK(t, "message: \"${text}\"\n", schema, map[string]any{"text": tc.value})
			m := mustUnmarshal(t, out)
			got, _ := m["message"].(string)
			if got != tc.value {
				t.Errorf("message = %q, want %q\nresolved YAML:\n%s", got, tc.value, out)
			}
		})

		t.Run("inline/"+tc.name, func(t *testing.T) {
			out := resolveOK(t, "message: \"PREFIX-${text}-SUFFIX\"\n", schema, map[string]any{"text": tc.value})
			m := mustUnmarshal(t, out)
			got, _ := m["message"].(string)
			want := "PREFIX-" + tc.value + "-SUFFIX"
			if got != want {
				t.Errorf("message = %q, want %q\nresolved YAML:\n%s", got, want, out)
			}
		})
	}
}

func TestResolveParameters_Default_ForwardReference(t *testing.T) {
	// 'first' default references 'second' which is declared later and not supplied.
	// Effective values are built in schema order; 'second' is not yet set when 'first' is processed.
	schema := []oam.ParameterDecl{
		{Name: "first", PropertySchema: oam.PropertySchema{Type: "string", Default: "${second}-suffix"}},
		{Name: "second", PropertySchema: oam.PropertySchema{Type: "string", Default: "hello"}},
	}
	_, err := oam.ResolveParameters([]byte("val: ${first}\n"), schema, map[string]any{})
	if err == nil {
		t.Fatal("expected error: default for 'first' references 'second' which has no value yet")
	}
}
