package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-kure/launcher/pkg/oam/shipped"
)

// These tests pin the definitions and models that launcher ships in the binary
// (go-kure/launcher#1735): known with nothing in the application's directory, and
// refused when an application names a shipped type.

// shippedCRDs is where the shipped CRD files live, read here to build an application
// definition over the same kind.
const shippedCRDs = "../../oam/shipped/content/definitions/crds"

// TestShippedDefinitionsKnownWithEmptyAppDir builds an application whose directory holds
// app.yaml only, one component of each shipped type.
func TestShippedDefinitionsKnownWithEmptyAppDir(t *testing.T) {
	tests := []struct {
		typ, props, kind string
	}{
		{"vm-rule", "{groups: [{name: g, rules: [{alert: A, expr: up == 0}]}]}", "kind: VMRule"},
		{"vm-service-scrape", "{selector: {matchLabels: {app: shop}}, endpoints: [{port: metrics}]}", "kind: VMServiceScrape"},
		{"vm-pod-scrape", "{selector: {matchLabels: {app: shop}}, podMetricsEndpoints: [{port: metrics}]}", "kind: VMPodScrape"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			dir := modelPackage(t, modelApp(tt.typ, tt.props), nil)
			out, err := buildModelPackage(t, dir)
			requireBuilt(t, out, err, tt.kind, "apiVersion: operator.victoriametrics.com/v1beta1")
		})
	}
}

// TestShippedTypeNamedByAModel builds a model whose part names a shipped kind by its
// identity, with no definitions directory: shipped definitions load before models.
func TestShippedTypeNamedByAModel(t *testing.T) {
	model := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: alerts.acme.example
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: rule
      type: vmrule.operator.victoriametrics.com@v1beta1
      properties:
        groups:
          - name: g
            rules:
              - {alert: A, expr: "up == 0"}
`
	dir := modelPackage(t, modelApp("alerts.acme.example@v1", "{}"), map[string]string{"models/alerts.yaml": model})
	out, err := buildModelPackage(t, dir)
	requireBuilt(t, out, err, "kind: VMRule")
}

// exampleDefinition returns the example's definition document renamed to typeName.
func exampleDefinition(t *testing.T, typeName string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(componentDefinitionExample, "definitions", "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(string(data), "name: batch-job", "name: "+typeName, 1)
}

func exampleCRD(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(componentDefinitionExample, "definitions", "crds", "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestApplicationDefinitionNamingAShippedTypeIsRefused: the refusal names the
// application's file and the shipped one.
func TestApplicationDefinitionNamingAShippedTypeIsRefused(t *testing.T) {
	dir := modelPackage(t, modelApp("vm-rule", "{}"), map[string]string{
		"definitions/vm-rule.yaml":       exampleDefinition(t, "vm-rule"),
		"definitions/crds/batchjob.yaml": exampleCRD(t),
	})
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, filepath.Join("definitions", "vm-rule.yaml"), `"vm-rule"`, "shipped", "vmrule.yaml")
}

// TestApplicationDefinitionOfAShippedIdentityIsRefused: another type name over the same
// kind and version is the same identity.
func TestApplicationDefinitionOfAShippedIdentityIsRefused(t *testing.T) {
	crd, err := os.ReadFile(filepath.Join(shippedCRDs, "vmrules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(crd)
	def := `apiVersion: launcher.gokure.dev/v1alpha1
kind: ComponentDefinition
metadata:
  name: my-rule
spec:
  emits:
    apiVersion: operator.victoriametrics.com/v1beta1
    kind: VMRule
  schema:
    crd: crds/vmrules.yaml
    sha256: ` + hex.EncodeToString(sum[:]) + "\n"
	dir := modelPackage(t, modelApp("my-rule", "{}"), map[string]string{
		"definitions/my-rule.yaml":      def,
		"definitions/crds/vmrules.yaml": string(crd),
	})
	_, err = buildModelPackage(t, dir)
	requireRefused(t, err, filepath.Join("definitions", "my-rule.yaml"), "vmrule.operator.victoriametrics.com@v1beta1", "shipped", "vmrule.yaml")
}

// TestApplicationModelOfAShippedShortNameIsRefused: a model that answers to a shipped
// definition's type name would be built in that type's place. The refusal says the type is
// shipped and names both files, not only the registry's plain-name refusal.
func TestApplicationModelOfAShippedShortNameIsRefused(t *testing.T) {
	model := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: vm-rule.acme.example
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data: {k: v}
`
	dir := modelPackage(t, modelApp("vm-rule.acme.example@v1", "{}"), map[string]string{"models/rule.yaml": model})
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, filepath.Join("models", "rule.yaml"), `"vm-rule"`, "shipped", "vmrule.yaml")
}

// TestApplicationModelNamingAShippedModelIsRefused: no model ships yet, so the shipped set
// is built over an in-memory one. The application's model file is a model of the same
// identity, and the refusal names both files.
func TestApplicationModelNamingAShippedModelIsRefused(t *testing.T) {
	transformer := newBuiltinTransformer()
	ships, err := shipped.LoadFS(transformer, fstest.MapFS{}, fstest.MapFS{"settings.yaml": {Data: []byte(testBaseModel)}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTempFile(t, dir, "mine.yaml", testBaseModel)
	err = loadModels(transformer, dir, []string{filepath.Join(dir, "mine.yaml")}, ships)
	requireRefused(t, err, "mine.yaml", "settings.acme.example@v1.0.0", "shipped", "model settings.yaml")
}
