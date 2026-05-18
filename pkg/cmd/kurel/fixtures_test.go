package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtures runs each scenario in testdata/ through the real CLI and compares
// stdout against expected.yaml. Set UPDATE_GOLDEN=1 to regenerate expected files.
func TestFixtures(t *testing.T) {
	scenarios, err := filepath.Glob("testdata/*/app.yaml")
	if err != nil {
		t.Fatalf("globbing testdata: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no fixture scenarios found in testdata/")
	}

	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, appPath := range scenarios {
		dir := filepath.Dir(appPath)
		name := filepath.Base(dir)

		t.Run(name, func(t *testing.T) {
			profilePath := filepath.Join(dir, "cluster.yaml")
			expectedPath := filepath.Join(dir, "expected.yaml")

			if _, err := os.Stat(profilePath); os.IsNotExist(err) {
				t.Fatalf("missing cluster.yaml for scenario %q", name)
			}

			cmd := NewKurelCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("build failed: %v\noutput: %s", err, out.String())
			}

			got := out.String()

			if update {
				if err := os.WriteFile(expectedPath, []byte(got), 0644); err != nil {
					t.Fatalf("writing expected.yaml: %v", err)
				}
				t.Logf("updated %s", expectedPath)
				return
			}

			expected, err := os.ReadFile(expectedPath)
			if os.IsNotExist(err) {
				t.Fatalf("expected.yaml missing for %q — run with UPDATE_GOLDEN=1 to generate", name)
			}
			if err != nil {
				t.Fatalf("reading expected.yaml: %v", err)
			}

			if strings.TrimSpace(got) != strings.TrimSpace(string(expected)) {
				t.Errorf("output mismatch for %q\n--- expected ---\n%s\n--- got ---\n%s", name, expected, got)
			}
		})
	}
}
