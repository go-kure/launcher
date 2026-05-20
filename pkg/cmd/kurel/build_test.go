package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: frontend
      type: webservice
      properties:
        image: ghcr.io/example/frontend:v1.0.0
        port: 8080
      traits:
        - type: expose
          properties:
            controllerType: ingress
            rules:
              - host: frontend.example.com
                paths:
                  - path: /
`

const testClusterYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeTempFile %s: %v", name, err)
	}
	return path
}

func TestBuildCommand_StdoutOutput(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build command failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if got == "" {
		t.Fatal("expected non-empty YAML output")
	}
	if !strings.Contains(got, "apiVersion") {
		t.Errorf("expected YAML output to contain 'apiVersion', got:\n%s", got)
	}
	if !strings.Contains(got, "Deployment") {
		t.Errorf("expected output to contain 'Deployment' for webservice component, got:\n%s", got)
	}
}

func TestBuildCommand_OutputDir(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	outDir := filepath.Join(dir, "manifests")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--output", outDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build command failed: %v\noutput: %s", err, out.String())
	}

	outFile := filepath.Join(outDir, "my-app.yaml")
	if _, err := os.Stat(outFile); os.IsNotExist(err) {
		t.Errorf("expected output file %q to exist", outFile)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "apiVersion") {
		t.Errorf("expected output file to contain YAML, got:\n%s", data)
	}
}

func TestBuildCommand_MissingProfileFlag(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --profile is missing")
	}
}

func TestBuildCommand_MissingAppFile(t *testing.T) {
	dir := t.TempDir()
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", "/nonexistent/app.yaml", "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing app file")
	}
}

func TestBuildCommand_InvalidAppYAML(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", "not: valid: oam: yaml: here")
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid app YAML")
	}
}

func TestBuildCommand_UnsupportedComponentType(t *testing.T) {
	const unknownApp = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: backend
      type: unknownxyz
      properties:
        image: ghcr.io/example/backend:v1.0.0
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", unknownApp)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported component type 'unknownxyz'")
	}
}

func TestBuildCommand_NamespaceOverride(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--namespace", "production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with namespace override failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "production") {
		t.Errorf("expected 'production' namespace in output, got:\n%s", got)
	}
}

func TestBuildCommand_StaleProfileField_Rejected(t *testing.T) {
	const staleCraneProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: stale-cluster
spec:
  gitops:
    url: https://git.example.com
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", staleCraneProfile)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for stale crane field spec.gitops in cluster.yaml")
	}
}

func TestBuildCommand_IsRegistered(t *testing.T) {
	cmd := NewKurelCommand()
	var found bool
	for _, sub := range cmd.Commands() {
		if extractCommandName(sub.Use) == "build" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'build' subcommand to be registered")
	}
}

func TestNewBuiltinTransformer_Registered(t *testing.T) {
	transformer := newBuiltinTransformer()
	if transformer == nil {
		t.Fatal("expected non-nil transformer")
	}
}
