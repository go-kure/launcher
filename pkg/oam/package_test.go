package oam_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

func TestParsePackage_Valid(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
  version: "1.0.0"
  description: A test package
spec:
  parameters:
  - name: image
    type: string
    required: true
    description: Container image
  - name: replicas
    type: integer
    required: false
    default: 1
`
	pkg, err := oam.ParsePackage([]byte(input))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if pkg.Metadata.Name != "my-app" {
		t.Errorf("name = %q, want my-app", pkg.Metadata.Name)
	}
	if pkg.Metadata.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", pkg.Metadata.Version)
	}
	if len(pkg.Spec.Parameters) != 2 {
		t.Errorf("parameters count = %d, want 2", len(pkg.Spec.Parameters))
	}
}

func TestParsePackage_RejectsUnknownFields(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
  unknownField: oops
spec: {}
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	var parseErr *errors.ParseError
	if !stderrors.As(err, &parseErr) {
		t.Errorf("expected *errors.ParseError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsWrongAPIVersion(t *testing.T) {
	input := `
apiVersion: core.oam.dev/v1beta1
kind: Package
metadata:
  name: my-app
spec: {}
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for wrong apiVersion, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsWrongKind(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
spec: {}
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for wrong kind, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsMissingName(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: ""
spec: {}
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsInvalidParamType(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: myval
    type: badtype
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid param type, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

// TestParsePackage_StructuredParamTypes: array and object are parameter types
// (go-kure/launcher#421). A default must have the declared shape, a list or a map;
// a string default is refused, not parsed as YAML.
func TestParsePackage_StructuredParamTypes(t *testing.T) {
	pkg := func(param string) string {
		return `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
` + param
	}
	accepted := map[string]string{
		"array, no default":   "  - name: items\n    type: array\n",
		"object, no default":  "  - name: labels\n    type: object\n",
		"array, list default": "  - name: items\n    type: array\n    default: [a, b]\n",
		"array, empty list":   "  - name: items\n    type: array\n    default: []\n",
		"object, map default": "  - name: labels\n    type: object\n    default: {team: web}\n",
		"object, empty map":   "  - name: labels\n    type: object\n    default: {}\n",
	}
	for name, param := range accepted {
		t.Run("accepted/"+name, func(t *testing.T) {
			if _, err := oam.ParsePackage([]byte(pkg(param))); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	refused := map[string]struct{ param, wantSub string }{
		"array, map default":      {"  - name: items\n    type: array\n    default: {a: b}\n", "is a map, not a list"},
		"array, string default":   {"  - name: items\n    type: array\n    default: \"[a, b]\"\n", "is a string, not a list"},
		"array, placeholder":      {"  - name: items\n    type: array\n    default: \"${other}\"\n", "is a string, not a list"},
		"array, scalar default":   {"  - name: items\n    type: array\n    default: 3\n", "not a list"},
		"object, list default":    {"  - name: labels\n    type: object\n    default: [a]\n", "is a list, not a map"},
		"object, string default":  {"  - name: labels\n    type: object\n    default: \"team: web\"\n", "is a string, not a map"},
		"unknown type still gone": {"  - name: n\n    type: number\n", "supported types: string, integer, boolean, array, object"},
	}
	for name, tc := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			_, err := oam.ParsePackage([]byte(pkg(tc.param)))
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected an error containing %q, got %v", tc.wantSub, err)
			}
			var valErr *errors.ValidationError
			if !stderrors.As(err, &valErr) {
				t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestParsePackage_RejectsStringDefaultForIntegerParam(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: replicas
    type: integer
    default: foo
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for string default 'foo' on integer param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsStringDefaultForBooleanParam(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: enabled
    type: boolean
    default: maybe
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for string default 'maybe' on boolean param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsMapDefaultForStringParam(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: image
    type: string
    default:
      repo: ghcr.io/app
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for map default on string param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsSliceDefaultForStringParam(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: tags
    type: string
    default: [a, b]
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for list default on string param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_RejectsFractionalDefaultForIntegerParam(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: replicas
    type: integer
    default: 2.5
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for fractional default 2.5 on integer param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}

func TestParsePackage_AcceptsPlaceholderDefaultOnIntegerParam(t *testing.T) {
	// Placeholder defaults (${…}) are validated at build time, not parse time.
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: base
    type: integer
    required: true
  - name: extra
    type: integer
    default: "${base}"
`
	_, err := oam.ParsePackage([]byte(input))
	if err != nil {
		t.Fatalf("expected no error for placeholder default on integer param, got: %v", err)
	}
}

func TestParsePackage_RejectsDuplicateParamName(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: image
    type: string
  - name: image
    type: string
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for duplicate parameter name, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
	}
}
