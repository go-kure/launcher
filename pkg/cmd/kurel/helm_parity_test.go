package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
)

// helmParityCases pairs each helm fixture with the helmchart document it replaced.
// Both helmchart files live under testdata/helm-parity: <name>.helmchart.yaml is
// the input document, kept as provenance, and <name>.helmchart.out.yaml is what
// kurel build printed for it before helmchart was removed (go-kure/launcher#350).
var helmParityCases = []struct {
	name, helm string
}{
	{"oci", "testdata/helm-oci/app.yaml"},
	{"repository", "testdata/helm-repository/app.yaml"},
	{"sourceref", "testdata/helm-sourceref/app.yaml"},
}

func helmParityPath(name, suffix string) string {
	return filepath.Join("testdata", "helm-parity", name+suffix)
}

// TestHelmParity is the parity oracle for retiring helmchart (go-kure/launcher#350):
// it builds each helm fixture against its cluster.yaml and compares the unified
// diff between the frozen helmchart output and that build with
// testdata/helm-parity/<name>.diff. Every hunk of a committed diff is one of the
// approved deltas in the helmchart migration table
// (pkg/oam/builtin/components/README.md), so a divergence outside that table
// shows up as a change to a .diff file in review. An empty .diff records a pair
// that builds identically (a referenced source has no generated-source delta).
// Set UPDATE_GOLDEN=1 to regenerate the .diff files; the frozen output is never
// regenerated.
func TestHelmParity(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, tc := range helmParityCases {
		t.Run(tc.name, func(t *testing.T) {
			frozen, err := os.ReadFile(helmParityPath(tc.name, ".helmchart.out.yaml"))
			if err != nil {
				t.Fatalf("reading the frozen helmchart output: %v", err)
			}
			old := string(frozen)
			profile := filepath.Join(filepath.Dir(tc.helm), "cluster.yaml")
			replacement := buildManifests(t, tc.helm, profile)
			diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A:        difflib.SplitLines(old),
				B:        difflib.SplitLines(replacement),
				FromFile: "helmchart",
				ToFile:   "helm",
				Context:  2,
			})
			if err != nil {
				t.Fatalf("diffing the builds: %v", err)
			}
			if old == "" || replacement == "" {
				t.Fatal("a build produced no manifests; an empty diff would prove nothing")
			}

			goldenPath := helmParityPath(tc.name, ".diff")
			if update {
				if err := os.WriteFile(goldenPath, []byte(diff), 0o644); err != nil {
					t.Fatalf("writing %s: %v", goldenPath, err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading %s: %v (run with UPDATE_GOLDEN=1 to generate)", goldenPath, err)
			}
			if diff != string(want) {
				t.Errorf("helm diverges from helmchart outside the recorded deltas in %s:\nwant:\n%s\ngot:\n%s", goldenPath, want, diff)
			}
		})
	}
}

// buildManifests runs kurel build and returns its stdout.
func buildManifests(t *testing.T, appPath, profilePath string) string {
	t.Helper()
	cmd := NewKurelCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kurel build %s: %v\nstderr: %s", appPath, err, stderr.String())
	}
	return out.String()
}
