package kurel

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGoreleaserConfig_KeepsTheChangelogStepForReleaseNotes: the shared release
// workflow hands GoReleaser the release notes with --release-notes, and GoReleaser
// loads that file only inside its changelog step. With `changelog.disable: true`
// every release step succeeds and the GitHub release body is one newline. With
// --release-notes set, GoReleaser makes no changelog of its own, so the step costs
// nothing and must stay enabled.
func TestGoreleaserConfig_KeepsTheChangelogStepForReleaseNotes(t *testing.T) {
	data, err := os.ReadFile("../../../.goreleaser.yml")
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	var cfg struct {
		Changelog struct {
			Disable any `yaml:"disable"`
		} `yaml:"changelog"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}
	// Any value other than absent is refused: GoReleaser also takes a template
	// string for disable, which can evaluate to true.
	if cfg.Changelog.Disable != nil {
		t.Errorf("changelog.disable is set to %v: GoReleaser would skip the step that loads --release-notes, and the release body would be empty", cfg.Changelog.Disable)
	}
}
