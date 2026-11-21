package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

const ilDigest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ilMatrix is every reference the level check is read on, with whether each
// level accepts it (go-kure/launcher#1644). The tagged column is the rule as it
// stood before there was a level.
var ilMatrix = []struct {
	name, reference                string
	any, tagged, version, digested bool
}{
	{"untagged", "registry.example/app", true, false, false, false},
	{"latest", "registry.example/app:latest", true, false, false, false},
	{"latest with a digest", "registry.example/app:latest" + ilDigest, true, false, false, true},
	{"a branch tag", "registry.example/app:main", true, true, false, false},
	{"a tag naming a major version", "registry.example/app:1", true, true, true, false},
	{"a v and two parts", "registry.example/app:v1.2", true, true, true, false},
	{"three parts and a suffix", "registry.example/app:1.2.3-alpine", true, true, true, false},
	{"a pre-release suffix", "registry.example/app:1.2.3-rc.1", true, true, true, false},
	{"a suffix with no version", "registry.example/app:alpine", true, true, false, false},
	{"a version tag with a digest", "registry.example/app:v1.2" + ilDigest, true, true, true, true},
	{"a branch tag with a digest", "registry.example/app:main" + ilDigest, true, true, false, true},
	{"a digest alone", "registry.example/app" + ilDigest, true, true, false, true},
}

func TestValidateImageRefAt_Matrix(t *testing.T) {
	for _, tc := range ilMatrix {
		for _, lv := range []struct {
			level  oam.ImageReferenceLevel
			accept bool
		}{
			{oam.ImageLevelAny, tc.any},
			{oam.ImageLevelTagged, tc.tagged},
			{oam.ImageLevelVersion, tc.version},
			{oam.ImageLevelDigest, tc.digested},
		} {
			t.Run(tc.name+"/"+string(lv.level), func(t *testing.T) {
				err := components.ValidateImageRefAt(tc.reference, lv.level)
				switch {
				case lv.accept && err != nil:
					t.Errorf("%q at level %q is refused: %v", tc.reference, lv.level, err)
				case !lv.accept && err == nil:
					t.Errorf("%q at level %q is accepted, want refused", tc.reference, lv.level)
				case !lv.accept:
					if !strings.Contains(err.Error(), `image "`+tc.reference+`" rejected`) {
						t.Errorf("err = %v, want one naming the reference", err)
					}
					if lv.level != oam.ImageLevelTagged && !strings.Contains(err.Error(), `level "`+string(lv.level)+`"`) {
						t.Errorf("err = %v, want one naming the level %q", err, lv.level)
					}
				}
			})
		}
	}
}

// A reference the parser refuses is refused at every level, with the parser's
// reason.
func TestValidateImageRefAt_NotAReference(t *testing.T) {
	for _, reference := range []string{"", "registry.example/Team/app:1.0.0", "registry.example/app:"} {
		for _, level := range []oam.ImageReferenceLevel{oam.ImageLevelAny, oam.ImageLevelTagged, oam.ImageLevelVersion, oam.ImageLevelDigest} {
			if err := components.ValidateImageRefAt(reference, level); err == nil {
				t.Errorf("%q at level %q is accepted, want refused", reference, level)
			}
		}
	}
}

// An unknown level is refused, never read as the default: a misspelt level must
// not loosen a rule.
func TestValidateImageRefAt_UnknownLevel(t *testing.T) {
	err := components.ValidateImageRefAt("registry.example/app:1.0.0", "strict")
	if err == nil || !strings.Contains(err.Error(), `"strict"`) {
		t.Errorf("err = %v, want one naming the level \"strict\"", err)
	}
}

// The default level is the rule as it was: same answer, same text, for every
// reference of the matrix and for the refusals of the original tests.
func TestValidateImageRefAt_TaggedIsTheOldRule(t *testing.T) {
	references := []string{"nginx", "nginx:latest", "", "registry.example/Team/app:1.0.0"}
	for _, tc := range ilMatrix {
		references = append(references, tc.reference)
	}
	for _, reference := range references {
		old := components.ValidateImageRef(reference)
		got := components.ValidateImageRefAt(reference, oam.ImageLevelTagged)
		switch {
		case old == nil && got == nil:
		case old == nil || got == nil || old.Error() != got.Error():
			t.Errorf("%q: ValidateImageRef gave %v, ValidateImageRefAt at tagged gave %v", reference, old, got)
		}
	}
}
