package kurel

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCIWorkflow_CoverageCommentSkipsForkPRs pins go-kure/launcher#1140: on a fork
// PR the GITHUB_TOKEN is read-only, so the coverage comment step fails with
// "Resource not accessible by integration" and turns the required build check
// red. The step must run only when the head repository is this repository.
func TestCIWorkflow_CoverageCommentSkipsForkPRs(t *testing.T) {
	data, err := os.ReadFile("../../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}

	const stepName = "Comment coverage on PR"
	// The whole condition, not a substring: an `||` in place of the `&&` keeps
	// the guard text and makes the step run on every pull request again.
	const wantIf = "github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository"
	found := 0
	for job, j := range wf.Jobs {
		for _, s := range j.Steps {
			if s.Name != stepName {
				continue
			}
			found++
			if strings.TrimSpace(s.If) != wantIf {
				t.Errorf("job %q step %q has if: %q, want %q", job, stepName, s.If, wantIf)
			}
		}
	}
	if found == 0 {
		t.Errorf("no step named %q in ci.yml; if it was renamed, update this test", stepName)
	}
}
