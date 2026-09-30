package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

const testEnvironmentsYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: EnvironmentSet
metadata:
  name: my-app-environments
spec:
  environments:
  - name: staging
    profile: profiles/staging.yaml
    values: values/staging.yaml
  - name: prod
    profile: profiles/prod.yaml
    values: values/prod.yaml
`

// testEnvAppYAML is testParamAppYAML plus an expose trait, so the ClusterProfile's
// ingressClassName reaches the output and a wrong profile is visible, not just
// wrong values.
const testEnvAppYAML = testParamAppYAML + `    traits:
    - type: expose
      properties:
        rules:
        - host: web.example.com
          paths:
          - path: /
`

// writeEnvironmentFixture lays out a package directory with two environments whose
// profile and values files live in subdirectories, so a relative path that resolved
// against the working directory instead of the environments file would fail to read.
func writeEnvironmentFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTempFile(t, dir, "app.yaml", testEnvAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	writeTempFile(t, dir, environmentsFileName, testEnvironmentsYAML)
	for _, sub := range []string{"profiles", "values"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTempFile(t, dir, "profiles/staging.yaml", testClusterYAML)
	writeTempFile(t, dir, "profiles/prod.yaml", strings.Replace(testClusterYAML, "ingressClassName: nginx", "ingressClassName: traefik", 1))
	writeTempFile(t, dir, "values/staging.yaml", "image: myregistry/app:staging\nreplicas: 1\n")
	writeTempFile(t, dir, "values/prod.yaml", "image: myregistry/app:v2.0.0\nreplicas: 3\n")
	return dir
}

func runKurelBuild(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"build"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// TestBuildCommand_EnvironmentMatchesExplicitFlags pins the issue's contract:
// --environment behaves as if --profile/--values had been passed explicitly, so the
// output is byte-identical to the hand-paired invocation.
func TestBuildCommand_EnvironmentMatchesExplicitFlags(t *testing.T) {
	dir := writeEnvironmentFixture(t)

	for _, env := range []string{"staging", "prod"} {
		t.Run(env, func(t *testing.T) {
			explicit, err := runKurelBuild(t, dir,
				"--profile", filepath.Join(dir, "profiles", env+".yaml"),
				"--values", filepath.Join(dir, "values", env+".yaml"))
			if err != nil {
				t.Fatalf("explicit build: %v", err)
			}
			named, err := runKurelBuild(t, dir, "--environment", env)
			if err != nil {
				t.Fatalf("--environment build: %v", err)
			}
			if named == "" {
				t.Fatal("expected non-empty output")
			}
			if named != explicit {
				t.Errorf("--environment %s output differs from explicit --profile/--values\n--- explicit\n%s\n--- environment\n%s", env, explicit, named)
			}
		})
	}

	// The two environments must actually differ, or the equality above would hold
	// for a resolver that ignored the name.
	staging, _ := runKurelBuild(t, dir, "--environment", "staging")
	prod, _ := runKurelBuild(t, dir, "--environment", "prod")
	if staging == prod {
		t.Fatal("staging and prod produced identical output; fixture does not distinguish environments")
	}
	if !strings.Contains(prod, "ingressClassName: traefik") || !strings.Contains(prod, "replicas: 3") {
		t.Errorf("prod output missing its profile or values:\n%s", prod)
	}
}

func TestBuildCommand_EnvironmentSetOverridesValues(t *testing.T) {
	dir := writeEnvironmentFixture(t)

	got, err := runKurelBuild(t, dir, "--environment", "prod", "--set", "replicas=5")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(got, "replicas: 5") {
		t.Errorf("expected --set to override the environment's values (replicas: 5), got:\n%s", got)
	}
	if !strings.Contains(got, "myregistry/app:v2.0.0") {
		t.Errorf("expected the environment's other values to survive --set, got:\n%s", got)
	}
}

func TestBuildCommand_EnvironmentsFileFlag(t *testing.T) {
	dir := writeEnvironmentFixture(t)
	// Move the environments file out of the package directory, next to its
	// profiles/ and values/: relative paths follow the file, not app.yaml.
	elsewhere := t.TempDir()
	if err := os.Rename(filepath.Join(dir, environmentsFileName), filepath.Join(elsewhere, "envs.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"profiles", "values"} {
		if err := os.Rename(filepath.Join(dir, sub), filepath.Join(elsewhere, sub)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := runKurelBuild(t, dir, "--environment", "staging"); err == nil {
		t.Fatal("expected an error: no environments file next to app.yaml")
	}

	got, err := runKurelBuild(t, dir, "--environment", "staging", "--environments", filepath.Join(elsewhere, "envs.yaml"))
	if err != nil {
		t.Fatalf("build with --environments: %v", err)
	}
	if !strings.Contains(got, "myregistry/app:staging") {
		t.Errorf("expected staging values in output:\n%s", got)
	}
}

// TestBuildCommand_EnvironmentWithoutPackage covers an Application with no
// kurel.yaml: a profile-only binding builds, and a binding carrying values fails
// exactly as --values does there.
func TestBuildCommand_EnvironmentWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "app.yaml", testAppYAML)
	writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	writeTempFile(t, dir, "values.yaml", testParamValuesYAML)
	writeTempFile(t, dir, environmentsFileName, "apiVersion: launcher.gokure.dev/v1alpha1\nkind: EnvironmentSet\nspec:\n  environments:\n  - name: bare\n    profile: cluster.yaml\n  - name: valued\n    profile: cluster.yaml\n    values: values.yaml\n")

	explicit, err := runKurelBuild(t, dir, "--profile", filepath.Join(dir, "cluster.yaml"))
	if err != nil {
		t.Fatalf("explicit build: %v", err)
	}
	named, err := runKurelBuild(t, dir, "--environment", "bare")
	if err != nil {
		t.Fatalf("profile-only environment: %v", err)
	}
	if named != explicit {
		t.Errorf("profile-only environment output differs from --profile\n--- explicit\n%s\n--- environment\n%s", explicit, named)
	}

	_, err = runKurelBuild(t, dir, "--environment", "valued")
	if err == nil || !strings.Contains(err.Error(), "require a kurel.yaml") {
		t.Errorf("expected the --values-without-package error, got %v", err)
	}
}

func TestBuildCommand_EnvironmentFlagConflicts(t *testing.T) {
	dir := writeEnvironmentFixture(t)
	profile := filepath.Join(dir, "profiles", "staging.yaml")
	values := filepath.Join(dir, "values", "staging.yaml")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"neither profile nor environment", []string{dir}, "at least one of the flags"},
		{"profile and environment", []string{dir, "--environment", "staging", "--profile", profile}, "if any flags in the group"},
		{"values and environment", []string{dir, "--environment", "staging", "--values", values}, "if any flags in the group"},
		{"environments without environment", []string{dir, "--profile", profile, "--environments", filepath.Join(dir, environmentsFileName)}, "--environments requires --environment"},
		// An explicitly empty value (an unset variable in a wrapper script) counts
		// as passed, not as absent.
		{"empty environments without environment", []string{dir, "--profile", profile, "--environments="}, "--environments requires --environment"},
		{"empty environment", []string{dir, "--environment="}, "--environment requires a non-empty name"},
		{"empty environments path", []string{dir, "--environment", "staging", "--environments="}, "--environments requires a non-empty path"},
		{"unknown environment", []string{dir, "--environment", "qa"}, `environment "qa" is not declared`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runKurelBuild(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseEnvironmentSet(t *testing.T) {
	const header = "apiVersion: launcher.gokure.dev/v1alpha1\nkind: EnvironmentSet\n"

	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"valid", testEnvironmentsYAML, ""},
		{"profile only, no metadata", header + "spec:\n  environments:\n  - name: dev\n    profile: dev.yaml\n", ""},
		{"wrong apiVersion", strings.Replace(testEnvironmentsYAML, "v1alpha1", "v1", 1), "unsupported apiVersion"},
		{"wrong kind", strings.Replace(testEnvironmentsYAML, "kind: EnvironmentSet", "kind: Package", 1), "expected kind EnvironmentSet"},
		{"unknown field", header + "spec:\n  environments:\n  - name: dev\n    profile: dev.yaml\n    cluster: x\n", "field cluster not found"},
		{"no environments", header + "spec:\n  environments: []\n", "declares no environments"},
		{"missing name", header + "spec:\n  environments:\n  - profile: dev.yaml\n", "name is required"},
		{"invalid name", header + "spec:\n  environments:\n  - name: Dev_1\n    profile: dev.yaml\n", "not a DNS-1123 label"},
		{"duplicate name", header + "spec:\n  environments:\n  - name: dev\n    profile: a.yaml\n  - name: dev\n    profile: b.yaml\n", `duplicate environment name "dev"`},
		{"trailing document", testEnvironmentsYAML + "---\n" + header + "spec:\n  environments:\n  - name: dev\n    profile: dev.yaml\n", "expected a single YAML document"},
		{"trailing malformed document", testEnvironmentsYAML + "---\nspec: [unclosed\n", "decoding"},
		{"missing profile", header + "spec:\n  environments:\n  - name: dev\n    values: dev.yaml\n", "profile is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseEnvironmentSet([]byte(tt.doc))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

// environmentFlags parses args into opts through a flag set carrying only the two
// environment flags, so resolveEnvironment sees real flag presence.
func environmentFlags(t *testing.T, opts *buildOptions, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("build", pflag.ContinueOnError)
	fs.StringVar(&opts.environment, "environment", "", "")
	fs.StringVar(&opts.environmentsPath, "environments", "", "")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestResolveEnvironment_PathResolution(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(t.TempDir(), "abs-profile.yaml")
	writeTempFile(t, dir, "envs.yaml", "apiVersion: launcher.gokure.dev/v1alpha1\nkind: EnvironmentSet\nspec:\n  environments:\n  - name: rel\n    profile: p.yaml\n    values: sub/v.yaml\n  - name: abs\n    profile: "+abs+"\n")

	envs := filepath.Join(dir, "envs.yaml")
	opts := &buildOptions{}
	if err := resolveEnvironment(opts, "/unused", environmentFlags(t, opts, "--environment", "rel", "--environments", envs)); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "p.yaml"); opts.profilePath != want {
		t.Errorf("profilePath = %q, want %q", opts.profilePath, want)
	}
	if want := filepath.Join(dir, "sub", "v.yaml"); opts.valuesPath != want {
		t.Errorf("valuesPath = %q, want %q", opts.valuesPath, want)
	}

	opts = &buildOptions{}
	if err := resolveEnvironment(opts, "/unused", environmentFlags(t, opts, "--environment", "abs", "--environments", envs)); err != nil {
		t.Fatal(err)
	}
	if opts.profilePath != abs {
		t.Errorf("profilePath = %q, want absolute %q unchanged", opts.profilePath, abs)
	}
	if opts.valuesPath != "" {
		t.Errorf("valuesPath = %q, want empty for a profile-only environment", opts.valuesPath)
	}
}
