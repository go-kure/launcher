package oam_test

import (
	"reflect"
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

// TestResolveParameters_NodeSubstitution: a full-value ${x} for an array or object
// parameter becomes the value's own YAML list or map (go-kure/launcher#421).
func TestResolveParameters_NodeSubstitution(t *testing.T) {
	schema := []oam.ParameterDecl{
		{Name: "env", PropertySchema: oam.PropertySchema{Type: "array"}},
		{Name: "labels", PropertySchema: oam.PropertySchema{Type: "object"}},
	}
	env := []any{map[string]any{"name": "LOG_LEVEL", "value": "debug"}, map[string]any{"name": "PORT", "value": "8080"}}
	labels := map[string]any{"team": "web", "tier": "frontend"}
	out := resolveOK(t, "spec:\n  env: ${env}  # the env list\n  labels: ${labels}\n", schema,
		map[string]any{"env": env, "labels": labels})

	spec := mustUnmarshal(t, out)["spec"].(map[string]any)
	gotEnv, ok := spec["env"].([]any)
	if !ok || len(gotEnv) != 2 {
		t.Fatalf("env = %#v, want a 2-entry list\n%s", spec["env"], out)
	}
	if e, _ := gotEnv[1].(map[string]any); e["name"] != "PORT" || e["value"] != "8080" {
		t.Errorf("env[1] = %#v, want {name: PORT, value: \"8080\"} (a string, not an int)", gotEnv[1])
	}
	gotLabels, ok := spec["labels"].(map[string]any)
	if !ok || gotLabels["team"] != "web" || gotLabels["tier"] != "frontend" {
		t.Errorf("labels = %#v, want {team: web, tier: frontend}", spec["labels"])
	}
	if !strings.Contains(string(out), "# the env list") {
		t.Errorf("the placeholder's line comment was dropped:\n%s", out)
	}
}

// TestResolveParameters_NodeSubstitution_NotRescanned: a ${...} inside a supplied
// structured value is data, not a placeholder — the replacement is not walked again.
func TestResolveParameters_NodeSubstitution_NotRescanned(t *testing.T) {
	schema := []oam.ParameterDecl{
		{Name: "args", PropertySchema: oam.PropertySchema{Type: "array"}},
		{Name: "name", PropertySchema: oam.PropertySchema{Type: "string"}},
	}
	out := resolveOK(t, "args: ${args}\n", schema, map[string]any{
		"args": []any{"--name=${name}"}, "name": "web",
	})
	args := mustUnmarshal(t, out)["args"].([]any)
	if args[0] != "--name=${name}" {
		t.Errorf("args[0] = %q, want the literal %q", args[0], "--name=${name}")
	}
}

// TestResolveParameters_NodeSubstitution_AnchorKept: an anchor on the placeholder
// stays on the substituted node, so an alias to it resolves to the value.
func TestResolveParameters_NodeSubstitution_AnchorKept(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		value any
	}{
		{"array", []any{"a", "b"}},
		{"object", map[string]any{"k": "v"}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			schema := []oam.ParameterDecl{{Name: "p", PropertySchema: oam.PropertySchema{Type: oam.PropertyType(tc.typ)}}}
			out := resolveOK(t, "first: &shared ${p}\nsecond: *shared\n", schema, map[string]any{"p": tc.value})
			m := mustUnmarshal(t, out)
			if !reflect.DeepEqual(m["first"], tc.value) || !reflect.DeepEqual(m["second"], tc.value) {
				t.Errorf("first = %#v, second = %#v, want both %#v\n%s", m["first"], m["second"], tc.value, out)
			}
		})
	}
}

// TestResolveParameters_StringDefaultEmbedsStructuredRefused: a string default may
// reference an earlier parameter, but not an array or object one — the same
// refusal as an inline ${...} in the application template.
func TestResolveParameters_StringDefaultEmbedsStructuredRefused(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		value any
	}{
		{"array", []any{"a"}},
		{"object", map[string]any{"k": "v"}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			schema := []oam.ParameterDecl{
				{Name: "p", PropertySchema: oam.PropertySchema{Type: oam.PropertyType(tc.typ)}},
				{Name: "label", PropertySchema: oam.PropertySchema{Type: "string", Default: "prefix-${p}"}},
			}
			_, err := oam.ResolveParameters([]byte("x: ${label}\n"), schema, map[string]any{"p": tc.value})
			if err == nil || !strings.Contains(err.Error(), `"label" references "p"`) || !strings.Contains(err.Error(), "cannot be embedded in a string") {
				t.Fatalf("expected a refusal naming label and p, got %v", err)
			}
		})
	}
}

// TestResolveParameters_StructuredDefault: an array/object default is used as
// written; a string default is refused rather than parsed as YAML.
func TestResolveParameters_StructuredDefault(t *testing.T) {
	schema := []oam.ParameterDecl{{Name: "env", PropertySchema: oam.PropertySchema{
		Type: "array", Default: []any{map[string]any{"name": "A", "value": "1"}},
	}}}
	out := resolveOK(t, "env: ${env}\n", schema, nil)
	if env, _ := mustUnmarshal(t, out)["env"].([]any); len(env) != 1 {
		t.Errorf("env = %#v, want the 1-entry default list\n%s", mustUnmarshal(t, out)["env"], out)
	}

	schema = []oam.ParameterDecl{{Name: "env", PropertySchema: oam.PropertySchema{Type: "array", Default: "[a]"}}}
	_, err := oam.ResolveParameters([]byte("env: ${env}\n"), schema, nil)
	if err == nil || !strings.Contains(err.Error(), "is a string, not a list") {
		t.Fatalf("expected a string-default refusal, got %v", err)
	}
}

// TestResolveParameters_StructuredValueRefused: a supplied value of the wrong shape,
// a --set string, or an inline use is an error naming the parameter.
func TestResolveParameters_StructuredValueRefused(t *testing.T) {
	cases := []struct {
		name, typ, tmpl string
		value           any
		wantSub         string
	}{
		{"scalar for array", "array", "x: ${p}\n", 3, "not a list"},
		{"map for array", "array", "x: ${p}\n", map[string]any{"a": "b"}, "is a map, not a list"},
		{"null for array", "array", "x: ${p}\n", nil, "is null, not a list"},
		{"list for object", "object", "x: ${p}\n", []any{"a"}, "is a list, not a map"},
		{"null for object", "object", "x: ${p}\n", nil, "is null, not a map"},
		{"--set string", "array", "x: ${p}\n", "a,b", "cannot be set with --set"},
		{"inline use", "object", "x: \"prefix-${p}\"\n", map[string]any{"a": "b"}, "cannot be used in inline string substitution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := []oam.ParameterDecl{{Name: "p", PropertySchema: oam.PropertySchema{Type: oam.PropertyType(tc.typ)}}}
			_, err := oam.ResolveParameters([]byte(tc.tmpl), schema, map[string]any{"p": tc.value})
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) || !strings.Contains(err.Error(), `"p"`) {
				t.Fatalf("expected an error naming \"p\" and containing %q, got %v", tc.wantSub, err)
			}
		})
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
