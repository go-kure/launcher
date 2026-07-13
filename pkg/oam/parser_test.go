package oam

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
)

func TestParse_ValidApplication(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if app.APIVersion != "launcher.gokure.dev/v1alpha1" {
		t.Errorf("apiVersion = %q, want %q", app.APIVersion, "launcher.gokure.dev/v1alpha1")
	}
	if app.Kind != "Application" {
		t.Errorf("kind = %q, want %q", app.Kind, "Application")
	}
	if app.Metadata.Name != "hello" {
		t.Errorf("metadata.name = %q, want %q", app.Metadata.Name, "hello")
	}
	if len(app.Spec.Components) != 1 {
		t.Fatalf("len(components) = %d, want 1", len(app.Spec.Components))
	}
	if app.Spec.Components[0].Name != "web" {
		t.Errorf("component.name = %q, want %q", app.Spec.Components[0].Name, "web")
	}
	if app.Spec.Components[0].Type != "webservice" {
		t.Errorf("component.type = %q, want %q", app.Spec.Components[0].Type, "webservice")
	}
}

func TestParse_MultipleComponents(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
  - name: frontend
    type: webservice
    properties:
      image: frontend:v1
  - name: backend
    type: worker
    properties:
      image: backend:v1
  - name: db
    type: postgresql
    properties:
      version: "15"
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(app.Spec.Components) != 3 {
		t.Fatalf("len(components) = %d, want 3", len(app.Spec.Components))
	}

	expected := []struct{ name, typ string }{
		{"frontend", "webservice"},
		{"backend", "worker"},
		{"db", "postgresql"},
	}
	for i, exp := range expected {
		if app.Spec.Components[i].Name != exp.name {
			t.Errorf("components[%d].name = %q, want %q", i, app.Spec.Components[i].Name, exp.name)
		}
		if app.Spec.Components[i].Type != exp.typ {
			t.Errorf("components[%d].type = %q, want %q", i, app.Spec.Components[i].Type, exp.typ)
		}
	}
}

func TestParse_WithTraits(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: webapp
spec:
  components:
  - name: api
    type: webservice
    properties:
      image: api:v1
    traits:
    - type: ingress
      properties:
        domain: api.example.com
    - type: configmap
      properties:
        name: api-config
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(app.Spec.Components[0].Traits) != 2 {
		t.Fatalf("len(traits) = %d, want 2", len(app.Spec.Components[0].Traits))
	}
	if app.Spec.Components[0].Traits[0].Type != "ingress" {
		t.Errorf("traits[0].type = %q, want %q", app.Spec.Components[0].Traits[0].Type, "ingress")
	}
	if app.Spec.Components[0].Traits[1].Type != "configmap" {
		t.Errorf("traits[1].type = %q, want %q", app.Spec.Components[0].Traits[1].Type, "configmap")
	}
}

func TestParse_WithPolicies(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: web:v1
  policies:
  - name: resource-limits
    type: env-policy
    properties:
      enforced:
        maxReplicas: 5
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(app.Spec.Policies) != 1 {
		t.Fatalf("len(policies) = %d, want 1", len(app.Spec.Policies))
	}
	if app.Spec.Policies[0].Name != "resource-limits" {
		t.Errorf("policies[0].name = %q, want %q", app.Spec.Policies[0].Name, "resource-limits")
	}
	if app.Spec.Policies[0].Type != "env-policy" {
		t.Errorf("policies[0].type = %q, want %q", app.Spec.Policies[0].Type, "env-policy")
	}
}

func TestParse_RejectsUnknownFields(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
  unknownField: value
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	var parseErr *errors.ParseError
	if !stderrors.As(err, &parseErr) {
		t.Errorf("expected *errors.ParseError, got %T: %v", err, err)
	}
}

func TestParse_RejectsInvalidYAML(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - this is invalid yaml
      - nested wrong
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	var parseErr *errors.ParseError
	if !stderrors.As(err, &parseErr) {
		t.Errorf("expected *errors.ParseError, got %T: %v", err, err)
	}
}

func TestParse_YAMLTypeErrorIncludesLineInfo(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
  unknownField: value
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	var parseErr *errors.ParseError
	if !stderrors.As(err, &parseErr) {
		t.Fatalf("expected *errors.ParseError, got %T: %v", err, err)
	}
	if parseErr.Line == 0 {
		t.Errorf("expected ParseError.Line > 0, got 0 (line info not extracted from yaml.TypeError)")
	}
}

func TestExtractYAMLLine(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"line 5: cannot unmarshal", 5},
		{"line 12: field foo not found", 12},
		{"line 1: something", 1},
		{"no line prefix here", 0},
		{"line abc: bad", 0},
		{"line : empty", 0},
		{"", 0},
	}
	for _, tc := range tests {
		got := extractYAMLLine(tc.input)
		if got != tc.want {
			t.Errorf("extractYAMLLine(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParse_RejectsCoreOAMAPIVersion(t *testing.T) {
	// core.oam.dev/v1beta1 is the upstream OAM API group — not accepted by launcher.
	input := `
apiVersion: core.oam.dev/v1beta1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for core.oam.dev/v1beta1, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "core.oam.dev/v1beta1") {
		t.Errorf("error = %q, want to contain 'core.oam.dev/v1beta1'", err.Error())
	}
}

func TestParse_RejectsUnsupportedAPIVersion(t *testing.T) {
	input := `
apiVersion: core.oam.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported apiVersion, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParse_RejectsWrongKind(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Component
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for wrong kind, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParse_RejectsMissingName(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  namespace: default
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	if !strings.Contains(err.Error(), "metadata.name is required") {
		t.Errorf("error = %q, want to contain 'metadata.name is required'", err.Error())
	}
}

func TestParse_RejectsEmptyComponents(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components: []
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for empty components, got nil")
	}
	if !strings.Contains(err.Error(), "at least one component") {
		t.Errorf("error = %q, want to contain 'at least one component'", err.Error())
	}
}

func TestParse_RejectsUnknownComponentType(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: unknown-type
    properties:
      image: nginx:1.25
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown component type, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Fatalf("expected *errors.ValidationError, got %T: %v", err, err)
	}
	if valErr.Value != "unknown-type" {
		t.Errorf("Value = %q, want %q", valErr.Value, "unknown-type")
	}
	if len(valErr.ValidValues) == 0 {
		t.Error("expected ValidValues to be populated for unknown component type")
	}
}

func TestParse_RejectsUnknownTraitType(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
    traits:
    - type: unknown-trait
      properties:
        foo: bar
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown trait type, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Fatalf("expected *errors.ValidationError, got %T: %v", err, err)
	}
	if valErr.Value != "unknown-trait" {
		t.Errorf("Value = %q, want %q", valErr.Value, "unknown-trait")
	}
	if len(valErr.ValidValues) == 0 {
		t.Error("expected ValidValues to be populated for unknown trait type")
	}
}

func TestParse_RejectsDuplicateComponentNames(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
  - name: web
    type: worker
    properties:
      image: worker:1.0.0
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for duplicate component name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate component name") {
		t.Errorf("error = %q, want to contain 'duplicate component name'", err.Error())
	}
}

func TestParse_RejectsInvalidDNS1123Name(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name: "uppercase",
			input: `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: Hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`,
		},
		{
			name: "underscore",
			input: `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello_world
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`,
		},
		{
			name: "starts with hyphen",
			input: `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: -hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil {
				t.Fatal("expected error for invalid DNS-1123 name, got nil")
			}
			if !strings.Contains(err.Error(), "DNS-1123") {
				t.Errorf("error = %q, want to contain 'DNS-1123'", err.Error())
			}
		})
	}
}

func TestParse_ComponentProperties(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
      port: 8080
      replicas: 3
      env:
      - name: FOO
        value: bar
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	props := app.Spec.Components[0].Properties
	if props["image"] != "nginx:1.25" {
		t.Errorf("properties.image = %q, want %q", props["image"], "nginx:1.25")
	}
	if props["port"] != 8080 {
		t.Errorf("properties.port = %v, want %v", props["port"], 8080)
	}
	if props["replicas"] != 3 {
		t.Errorf("properties.replicas = %v, want %v", props["replicas"], 3)
	}
}

func TestParseMulti_SingleDocument(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	apps, err := ParseMulti([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d, want 1", len(apps))
	}
	if apps[0].Metadata.Name != "hello" {
		t.Errorf("apps[0].metadata.name = %q, want %q", apps[0].Metadata.Name, "hello")
	}
}

func TestParseMulti_MultipleDocuments(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: app-one
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: app-one:v1
---
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: app-two
spec:
  components:
  - name: worker
    type: worker
    properties:
      image: app-two:v1
---
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: app-three
spec:
  components:
  - name: db
    type: postgresql
    properties:
      version: "16"
`
	apps, err := ParseMulti([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 3 {
		t.Fatalf("len(apps) = %d, want 3", len(apps))
	}
	expected := []string{"app-one", "app-two", "app-three"}
	for i, name := range expected {
		if apps[i].Metadata.Name != name {
			t.Errorf("apps[%d].metadata.name = %q, want %q", i, apps[i].Metadata.Name, name)
		}
	}
}

func TestParseMulti_EmptyDocument(t *testing.T) {
	_, err := ParseMulti([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
	if !strings.Contains(err.Error(), "no documents found") {
		t.Errorf("error = %q, want to contain 'no documents found'", err.Error())
	}
}

func TestParseMulti_InvalidSecondDocument(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: good-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
---
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: bad-app
  unknownField: oops
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := ParseMulti([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid second document, got nil")
	}
}

func TestParseMulti_ValidationErrorInSecondDocument(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: good-app
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
---
apiVersion: core.oam.dev/v1beta1
kind: Application
metadata:
  name: bad-version
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	_, err := ParseMulti([]byte(input))
	if err == nil {
		t.Fatal("expected validation error for second document, got nil")
	}
	if !strings.Contains(err.Error(), "core.oam.dev/v1beta1") {
		t.Errorf("error = %q, want to contain 'core.oam.dev/v1beta1'", err.Error())
	}
}

func TestMustParse_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustParse should panic on invalid input")
		}
	}()

	MustParse([]byte("invalid yaml: {{{"))
}

func TestMustParse_Success(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: nginx:1.25
`
	app := MustParse([]byte(input))
	if app.Metadata.Name != "hello" {
		t.Errorf("metadata.name = %q, want %q", app.Metadata.Name, "hello")
	}
}

func TestParse_ComponentAnnotations(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
  - name: cert-manager
    type: helmchart
    properties:
      chart: cert-manager
    annotations:
      gokure.dev/tier: infra
`
	app, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	comp := app.Spec.Components[0]
	if comp.Annotations == nil {
		t.Fatal("expected non-nil Annotations")
	}
	if got := comp.Annotations["gokure.dev/tier"]; got != "infra" {
		t.Errorf("annotations[gokure.dev/tier] = %q, want %q", got, "infra")
	}
}
