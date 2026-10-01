package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
)

// helmParityCases pairs each helm fixture with the helmchart document it replaced.
// The helmchart input of a moved fixture is kept under testdata/helm-parity; the
// repository case keeps its helmchart fixture, which still builds (deprecated).
var helmParityCases = []struct {
	name, helmchart, helm string
}{
	{"oci", "testdata/helm-parity/oci.helmchart.yaml", "testdata/helm-oci/app.yaml"},
	{"repository", "testdata/helmchart-repository/app.yaml", "testdata/helm-repository/app.yaml"},
	{"sourceref", "testdata/helm-parity/sourceref.helmchart.yaml", "testdata/helm-sourceref/app.yaml"},
}

// TestHelmParity is the parity oracle for retiring helmchart (go-kure/launcher#350):
// it builds each pair against the helm fixture's cluster.yaml and compares the
// unified diff of the two manifest streams with testdata/helm-parity/<name>.diff.
// Every hunk of a committed diff is one of the approved deltas in the helmchart
// migration table (pkg/oam/builtin/components/README.md), so a divergence outside
// that table shows up as a change to a .diff file in review. An empty .diff records
// a pair that builds identically (a referenced source has no generated-source
// delta). Set UPDATE_GOLDEN=1 to
// regenerate. Only stdout is compared: helmchart's deprecation warning goes to
// stderr.
func TestHelmParity(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, tc := range helmParityCases {
		t.Run(tc.name, func(t *testing.T) {
			profile := filepath.Join(filepath.Dir(tc.helm), "cluster.yaml")
			old := buildManifests(t, tc.helmchart, profile)
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

			goldenPath := filepath.Join("testdata", "helm-parity", tc.name+".diff")
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
