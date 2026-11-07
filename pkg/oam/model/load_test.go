package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

const validModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: webapp.acme.example
  version: v2
spec:
  celEnv: "1"
  inputs:
    image:
      type: string
      required: true
    replicas:
      type: integer
      default: 2
  values:
    - name: full
      expr: "${inputs.image + ':latest'}"
  parts:
    - id: app
      name: "${self.name}"
      type: deployment.apps@v1
      properties:
        replicas: "${inputs.replicas}"
      traits:
        - type: expose.launcher.gokure.dev@v1
          properties:
            port: 80
      annotations:
        owner: acme
    - id: cfg
      type: configmap@v1
      when: "${inputs.replicas > 1}"
      forEach:
        as: item
        in: "${[1, 2]}"
      properties:
        data: {}
`

func TestLoad_Valid(t *testing.T) {
	m, err := Load([]byte(validModel))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Metadata.Name != "webapp.acme.example" || m.Metadata.Version != "v2" {
		t.Fatalf("metadata = %+v", m.Metadata)
	}
	if m.Spec.CELEnv != "1" {
		t.Fatalf("celEnv = %q", m.Spec.CELEnv)
	}
	if got := m.Spec.Inputs["image"]; got.Type != oam.PropertyTypeString || !got.Required {
		t.Fatalf("input image = %+v", got)
	}
	if got := m.Spec.Inputs["replicas"]; got.Type != oam.PropertyTypeInteger || got.Default != 2 {
		t.Fatalf("input replicas = %+v", got)
	}
	if len(m.Spec.Values) != 1 || m.Spec.Values[0].Name != "full" || m.Spec.Values[0].Expr != "${inputs.image + ':latest'}" {
		t.Fatalf("values = %+v", m.Spec.Values)
	}
	if len(m.Spec.Parts) != 2 {
		t.Fatalf("parts = %d", len(m.Spec.Parts))
	}
	app := m.Spec.Parts[0]
	if app.ID != "app" || app.Type != "deployment.apps@v1" || app.Name != "${self.name}" {
		t.Fatalf("part app = %+v", app)
	}
	if len(app.Traits) != 1 || app.Traits[0].Type != "expose.launcher.gokure.dev@v1" || app.Annotations["owner"] != "acme" {
		t.Fatalf("part app traits or annotations = %+v", app)
	}
	cfg := m.Spec.Parts[1]
	if cfg.When != "${inputs.replicas > 1}" || cfg.ForEach == nil || cfg.ForEach.As != "item" || cfg.ForEach.In != "${[1, 2]}" {
		t.Fatalf("part cfg = %+v", cfg)
	}
}

func TestModel_Identity(t *testing.T) {
	m, err := Load([]byte(validModel))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.Identity(); got != (oam.TypeID{Name: "webapp.acme.example", Version: "v2"}) {
		t.Fatalf("Identity = %+v, want the version as written", got)
	}
	// The registry key is the canonical form, so v2, v2.0 and v2.0.0 are one model
	// (go-kure/launcher#1289: the registry returns a version as registered).
	if got := m.Key(); got != "webapp.acme.example@v2.0.0" {
		t.Fatalf("Key = %q, want webapp.acme.example@v2.0.0", got)
	}
}

// refused is a model document that must fail, the error text that must name the
// place, and whether it must wrap ErrInvalid (a decoded document that breaks a rule)
// as opposed to a YAML or envelope failure.
func replace(t *testing.T, old, repl string) []byte {
	t.Helper()
	if !strings.Contains(validModel, old) {
		t.Fatalf("test bug: %q not in validModel", old)
	}
	return []byte(strings.Replace(validModel, old, repl, 1))
}

func TestLoad_Refused(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) []byte
		want string // a substring that names the place or the reason
	}{
		{"empty input", func(*testing.T) []byte { return nil }, "empty"},
		{"wrong apiVersion", func(t *testing.T) []byte {
			return replace(t, "launcher.gokure.dev/v1alpha1", "launcher.gokure.dev/v1")
		}, "apiVersion"},
		{"wrong kind", func(t *testing.T) []byte { return replace(t, "kind: Model", "kind: Package") }, "kind"},
		{"unknown top-level field", func(t *testing.T) []byte { return replace(t, "kind: Model\n", "kind: Model\nextends: base\n") }, "extends"},
		{"unknown spec field (typo)", func(t *testing.T) []byte { return replace(t, "  parts:\n", "  partz:\n") }, "partz"},
		{"unknown part field", func(t *testing.T) []byte { return replace(t, "      annotations:\n", "      patches:\n") }, "patches"},
		{"unknown forEach field", func(t *testing.T) []byte {
			return replace(t, "        as: item\n", "        as: item\n        step: 2\n")
		}, "step"},
		{"two documents", func(t *testing.T) []byte { return append(replace(t, "", ""), []byte("---\n"+validModel)...) }, "one document"},
		{"name without a domain", func(t *testing.T) []byte { return replace(t, "webapp.acme.example", "webapp") }, "metadata.name"},
		{"name in upper case", func(t *testing.T) []byte { return replace(t, "webapp.acme.example", "WebApp.acme.example") }, "metadata.name"},
		{"version missing", func(t *testing.T) []byte { return replace(t, "  version: v2\n", "") }, "metadata.version"},
		{"version not semver", func(t *testing.T) []byte { return replace(t, "version: v2", "version: two") }, "metadata.version"},
		{"launcher domain with an API version", func(t *testing.T) []byte {
			return replace(t, "name: webapp.acme.example\n  version: v2", "name: webapp.launcher.gokure.dev\n  version: v1beta1")
		}, "metadata"},
		{"celEnv missing", func(t *testing.T) []byte { return replace(t, "  celEnv: \"1\"\n", "") }, "spec.celEnv"},
		{"celEnv other value", func(t *testing.T) []byte { return replace(t, "celEnv: \"1\"", "celEnv: \"2\"") }, "spec.celEnv"},
		{"input name with a hyphen", func(t *testing.T) []byte { return replace(t, "    image:\n", "    my-image:\n") }, "spec.inputs"},
		{"input without a type", func(t *testing.T) []byte {
			return replace(t, "      type: string\n      required: true\n", "      required: true\n")
		}, "spec.inputs.image"},
		{"value name twice", func(t *testing.T) []byte {
			return replace(t, "  parts:\n", "    - name: full\n      expr: \"${1}\"\n  parts:\n")
		}, "spec.values[1].name"},
		{"value name with a hyphen", func(t *testing.T) []byte { return replace(t, "name: full", "name: full-name") }, "spec.values[0].name"},
		{"value without an expression", func(t *testing.T) []byte {
			return replace(t, "      expr: \"${inputs.image + ':latest'}\"\n", "      expr: \"\"\n")
		}, "spec.values[0].expr"},
		{"no parts", func(t *testing.T) []byte {
			i := strings.Index(validModel, "  parts:\n")
			return []byte(validModel[:i] + "  parts: []\n")
		}, "spec.parts"},
		{"part id twice", func(t *testing.T) []byte { return replace(t, "    - id: cfg\n", "    - id: app\n") }, "spec.parts[1].id"},
		{"part id with a hyphen", func(t *testing.T) []byte { return replace(t, "id: cfg", "id: my-cfg") }, "spec.parts[1].id"},
		{"part id missing", func(t *testing.T) []byte { return replace(t, "    - id: cfg\n      type", "    - type") }, "spec.parts[1].id"},
		{"part type without a version", func(t *testing.T) []byte { return replace(t, "type: deployment.apps@v1", "type: deployment") }, "spec.parts[0].type"},
		{"part type short name", func(t *testing.T) []byte { return replace(t, "type: configmap@v1", "type: configmap") }, "spec.parts[1].type"},
		{"part type empty", func(t *testing.T) []byte { return replace(t, "type: configmap@v1", "type: \"\"") }, "spec.parts[1].type"},
		{"trait type without a version", func(t *testing.T) []byte {
			return replace(t, "type: expose.launcher.gokure.dev@v1", "type: expose")
		}, "spec.parts[0].traits[0].type"},
		{"forEach without an expression", func(t *testing.T) []byte { return replace(t, "        in: \"${[1, 2]}\"\n", "") }, "spec.parts[1].forEach.in"},
		{"forEach variable with a hyphen", func(t *testing.T) []byte { return replace(t, "as: item", "as: my-item") }, "spec.parts[1].forEach.as"},
		{"forEach variable twice in scope", func(t *testing.T) []byte { return replace(t, "as: item", "as: inputs") }, "spec.parts[1].forEach.as"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Load(tc.doc(t))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if m != nil {
				t.Fatalf("Load returned a model together with an error: %+v", m)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

// A rule failure on a decoded document wraps ErrInvalid; an envelope failure
// (YAML, apiVersion, kind) does not, so a caller can tell a bad file from a bad model.
func TestLoad_ErrInvalid(t *testing.T) {
	_, err := Load(replace(t, "celEnv: \"1\"", "celEnv: \"2\""))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a rule failure must wrap ErrInvalid: %v", err)
	}
	_, err = Load(replace(t, "kind: Model", "kind: Package"))
	if errors.Is(err, ErrInvalid) {
		t.Fatalf("a wrong kind is an envelope failure, not ErrInvalid: %v", err)
	}
}

func TestLoad_ReturnsFirstFailureInDocumentOrder(t *testing.T) {
	doc := strings.Replace(validModel, "celEnv: \"1\"", "celEnv: \"2\"", 1)
	doc = strings.Replace(doc, "id: cfg", "id: app", 1)
	_, err := Load([]byte(doc))
	if err == nil || !strings.Contains(err.Error(), "spec.celEnv") {
		t.Fatalf("the first failure in document order is celEnv: %v", err)
	}
}
