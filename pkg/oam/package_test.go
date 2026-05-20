package oam_test

import (
	stderrors "errors"
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

func TestParsePackage_RejectsArrayParamType(t *testing.T) {
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: items
    type: array
`
	_, err := oam.ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for unsupported param type 'array', got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Errorf("expected *errors.ValidationError, got %T: %v", err, err)
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
