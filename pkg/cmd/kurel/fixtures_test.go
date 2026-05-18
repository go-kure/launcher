package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtures runs all scenario directories under testdata/.
// Each scenario directory must contain app.yaml and cluster.yaml.
// The expected output is compared against expected.yaml.
// Set UPDATE_GOLDEN=1 to regenerate expected.yaml files.
func TestFixtures(t *testing.T) {
	scenarios, err := filepath.Glob("testdata/*/app.yaml")
	if err != nil {
		t.Fatalf("globbing fixture scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no fixture scenarios found under testdata/*/app.yaml")
	}

	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, appPath := range scenarios {
		dir := filepath.Dir(appPath)
		name := filepath.Base(dir)

		t.Run(name, func(t *testing.T) {
			profilePath := filepath.Join(dir, "cluster.yaml")
			expectedPath := filepath.Join(dir, "expected.yaml")

			cmd := NewKurelCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("build command failed: %v\noutput: %s", err, out.String())
			}
			got := out.String()

			if update {
				if err := os.WriteFile(expectedPath, []byte(got), 0644); err != nil {
					t.Fatalf("writing golden file %q: %v", expectedPath, err)
				}
				return
			}

			data, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("reading expected file %q: %v (run with UPDATE_GOLDEN=1 to generate)", expectedPath, err)
			}
			if strings.TrimSpace(got) != strings.TrimSpace(string(data)) {
				t.Errorf("fixture %q output mismatch:\nwant:\n%s\ngot:\n%s", name, data, got)
			}
		})
	}
}
