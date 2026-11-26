package kurel

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const customComponentExample = "../../../examples/custom-component"

// buildCustomComponent builds a copy of the example after edit has changed it.
func buildCustomComponent(t *testing.T, edit func(dir string)) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	src := os.DirFS(customComponentExample)
	if err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, path), 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, path), data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(dir)
	}
	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", dir, "--profile", filepath.Join(dir, "cluster.yaml")})
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// replaceIn replaces the first occurrence of old in a file of the copy.
func replaceIn(t *testing.T, file, old, replacement string) func(string) {
	return func(dir string) {
		t.Helper()
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), old) {
			t.Fatalf("%s does not hold %q", file, old)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildCommand_CustomComponentExample(t *testing.T) {
	out, errOut, err := buildCustomComponent(t, nil)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, errOut)
	}
	t.Logf("output:\n%s", out)
	if got := strings.Count(out, "kind: SparkApplication\n"); got != 2 {
		t.Errorf("want 2 SparkApplications, got %d", got)
	}
	for _, want := range []string{"name: pi\n", "name: nightly-app\n", "instances: 1\n", "instances: 4\n", "namespace: analytics\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not hold %q", want)
		}
	}
}

func TestBuildCommand_CustomComponentRefusals(t *testing.T) {
	cases := []struct {
		name, file, old, replacement, want string
	}{
		{"unknown top-level field", "app.yaml", "      mode: cluster\n", "      mode: cluster\n      bogus: 1\n", "bogus"},
		{"unknown nested field", "app.yaml", "        cores: 1\n", "        cores: 1\n        bogusNested: 1\n", "spec.driver.bogusNested"},
		{"wrong nested type", "app.yaml", "        instances: 1\n", "        instances: three\n", "spec.executor.instances"},
		{"missing required field", "app.yaml", "      sparkVersion: \"3.5.3\"\n", "", "sparkVersion"},
		{"value outside the CRD enum", "app.yaml", "      type: Scala\n", "      type: Cobol\n", "spec.type"},
		{"wrong type through the model", "app.yaml", "      executors: 4\n", "      executors: many\n", "executors"},
		{"digest mismatch", "definitions/sparkapplication.yaml", "sha256: 0f6a", "sha256: 1f6a", "the definition pins"},
		{"restricted trait", "app.yaml", "  - name: nightly\n", "    traits:\n    - type: scaler\n      properties: {maxReplicas: 3}\n  - name: nightly\n", `trait "scaler" is not supported on component type "spark-application"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := buildCustomComponent(t, replaceIn(t, c.file, c.old, c.replacement))
			if err == nil {
				t.Fatalf("built:\n%s", out)
			}
			t.Logf("refusal: %v", err)
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not hold %q", err, c.want)
			}
		})
	}
}

// Without the definitions directory the type is unknown, as it is today.
func TestBuildCommand_CustomComponentNeedsItsDefinition(t *testing.T) {
	_, _, err := buildCustomComponent(t, func(dir string) {
		if err := os.RemoveAll(filepath.Join(dir, "definitions")); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || !strings.Contains(err.Error(), `invalid value "spark-application"`) {
		t.Errorf("got %v, want the type refused", err)
	}
}
