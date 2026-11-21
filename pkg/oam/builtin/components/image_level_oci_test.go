package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// ilSourcePolicy is a Policy that names the OCI source level and nothing else.
type ilSourcePolicy struct {
	*oam.NoopPolicy
	level oam.ImageReferenceLevel
}

func (p ilSourcePolicy) SourceReferenceLevel() oam.ImageReferenceLevel { return p.level }

// TestImageLevel_OCISourceVersion: the version of an oci component is held to the
// source level of the policy, which is `any` without one, so that a policy that
// names only an image level changes nothing for a source (go-kure/launcher#1644).
func TestImageLevel_OCISourceVersion(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	build := func(version string, policy oam.Policy) error {
		return ilBuild("oci", map[string]any{
			"source":  map[string]any{"url": "oci://registry.example.com/manifests"},
			"version": version,
		}, policy)
	}
	at := func(level oam.ImageReferenceLevel) oam.Policy { return ilSourcePolicy{&oam.NoopPolicy{}, level} }
	for _, tc := range []struct {
		name     string
		policy   oam.Policy
		version  string
		accepted bool
	}{
		{"no policy accepts latest", nil, "latest", true},
		{"an image level alone changes nothing", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelDigest}, "latest", true},
		{"any accepts latest", at(oam.ImageLevelAny), "latest", true},
		{"tagged accepts a tag", at(oam.ImageLevelTagged), "stable", true},
		{"tagged refuses latest, as it does for an image", at(oam.ImageLevelTagged), "latest", false},
		{"tagged accepts a digest", at(oam.ImageLevelTagged), digest, true},
		{"version accepts a version tag", at(oam.ImageLevelVersion), "1.2.3", true},
		{"version accepts a v-prefixed tag with a suffix", at(oam.ImageLevelVersion), "v1.2-alpine", true},
		{"version accepts a digest", at(oam.ImageLevelVersion), digest, true},
		{"version refuses latest", at(oam.ImageLevelVersion), "latest", false},
		{"digest refuses latest", at(oam.ImageLevelDigest), "latest", false},
		{"digest refuses a version tag", at(oam.ImageLevelDigest), "1.2.3", false},
		{"digest accepts a digest", at(oam.ImageLevelDigest), digest, true},
		{"a level that is none of the four is refused", at(oam.ImageReferenceLevel("pinned")), "1.2.3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := build(tc.version, tc.policy)
			switch {
			case tc.accepted && err != nil:
				t.Errorf("version %q is refused: %v", tc.version, err)
			case !tc.accepted && err == nil:
				t.Errorf("version %q is accepted, want refused", tc.version)
			}
		})
	}
}
