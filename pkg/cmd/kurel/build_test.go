package kurel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// --- Parameter substitution tests ---

const testKurelYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: test-pkg
spec:
  parameters:
  - name: image
    type: string
    required: true
  - name: replicas
    type: integer
    required: false
    default: 1
`

const testParamAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: ${image}
      port: 8080
      replicas: ${replicas}
`

const testParamValuesYAML = `image: myregistry/app:v1.2.3
replicas: 2
`

func TestBuildCommand_ValuesFile(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with --values failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image myregistry/app:v1.2.3 in output:\n%s", got)
	}
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected replicas: 2 in output:\n%s", got)
	}
}

func TestBuildCommand_SetFlag(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{
		"build", appPath,
		"--profile", profilePath,
		"--set", "image=myregistry/app:v1.2.3",
		"--set", "replicas=2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with --set failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image myregistry/app:v1.2.3 in output:\n%s", got)
	}
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected replicas: 2 in output:\n%s", got)
	}
}

func TestBuildCommand_RequiredParamMissing(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// No --values or --set: required parameter 'image' is missing.
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when required parameter 'image' is not supplied")
	}
}

func TestBuildCommand_ValuesWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	// No kurel.yaml written — app directory has no package descriptor.
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: --values requires a kurel.yaml in the app directory")
	}
}

func TestBuildCommand_ValuesFileNotAMapping(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	// Values file is a YAML list, not a mapping.
	valuesPath := writeTempFile(t, dir, "values.yaml", "- item1\n- item2\n")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when values file is a YAML list, not a mapping")
	}
}

func TestBuildCommand_SetOverridesValues(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	// values.yaml supplies replicas: 1; --set overrides it to 2.
	valuesPath := writeTempFile(t, dir, "values.yaml", "image: myregistry/app:v1.2.3\nreplicas: 1\n")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{
		"build", appPath,
		"--profile", profilePath,
		"--values", valuesPath,
		"--set", "replicas=2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected --set to override values.yaml (replicas: 2), got:\n%s", got)
	}
}

func TestBuildCommand_DirectoryArg(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// Pass the directory instead of app.yaml; kurel.yaml is auto-discovered.
	cmd.SetArgs([]string{"build", dir, "--profile", profilePath, "--values", valuesPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with directory arg failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image in output, got:\n%s", got)
	}
}

// buildMinimalChartTar builds a gzipped tar chart archive. extraFiles are merged on top of the
// auto-generated Chart.yaml, keyed by their path inside the archive (e.g. "chartname/templates/x.yaml").
func buildMinimalChartTar(t *testing.T, name, version string, extraFiles map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		name + "/Chart.yaml": fmt.Sprintf("apiVersion: v2\nname: %s\nversion: %s\n", name, version),
	}
	for k, v := range extraFiles {
		files[k] = v
	}
	for path, content := range files {
		hdr := &tar.Header{Name: path, Mode: 0o600, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func helmIndexYAML(name, version, url string) string {
	return fmt.Sprintf(
		"apiVersion: v1\nentries:\n  %s:\n  - name: %s\n    version: %s\n    urls:\n      - %s\ngenerated: \"2024-01-01T00:00:00Z\"\n",
		name, name, version, url,
	)
}

func TestBuildCommand_HelmchartTemplateDelivery(t *testing.T) {
	// NOTES.txt content renders as a YAML mapping — without kure alpha.8's NOTES.txt filter,
	// decodeKubeManifests would return "missing apiVersion or kind" on this chart.
	chartFiles := map[string]string{
		"testchart/templates/cm.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\ndata:\n  key: value\n",
		"testchart/templates/NOTES.txt": "chart: testchart\nversion: 0.1.0\n",
	}
	chartBuf := buildMinimalChartTar(t, "testchart", "0.1.0", chartFiles)

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", srvURL+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			w.Write(chartBuf)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	appYAML := fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: testapp
      type: helmchart
      properties:
        chart: testchart
        version: "0.1.0"
        delivery: template
        source:
          url: %s
`, srvURL)

	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "kind: ConfigMap") {
		t.Errorf("expected ConfigMap in output, got:\n%s", got)
	}
	if strings.Contains(got, "chart: testchart") {
		t.Errorf("NOTES.txt content must not appear in output, got:\n%s", got)
	}
}
