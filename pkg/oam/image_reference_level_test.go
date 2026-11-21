package oam_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// imageLevelPolicy is a Policy that implements the optional interfaces of the
// image reference level, and nothing else.
type imageLevelPolicy struct {
	*oam.NoopPolicy
	image, source oam.ImageReferenceLevel
}

func (p imageLevelPolicy) ImageReferenceLevel() oam.ImageReferenceLevel  { return p.image }
func (p imageLevelPolicy) SourceReferenceLevel() oam.ImageReferenceLevel { return p.source }

// plainPolicy implements Policy and neither optional interface.
type plainPolicy struct{ *oam.NoopPolicy }

func TestImageReferenceLevelOf(t *testing.T) {
	tests := []struct {
		name   string
		policy oam.Policy
		want   oam.ImageReferenceLevel
	}{
		{"no policy", nil, oam.ImageLevelTagged},
		{"NoopPolicy", &oam.NoopPolicy{}, oam.ImageLevelTagged},
		{"a policy without the interface", plainPolicy{&oam.NoopPolicy{}}, oam.ImageLevelTagged},
		{"a policy that names no level", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}}, oam.ImageLevelTagged},
		{"any", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, image: oam.ImageLevelAny}, oam.ImageLevelAny},
		{"tagged", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, image: oam.ImageLevelTagged}, oam.ImageLevelTagged},
		{"version", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, image: oam.ImageLevelVersion}, oam.ImageLevelVersion},
		{"digest", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, image: oam.ImageLevelDigest}, oam.ImageLevelDigest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := oam.ImageReferenceLevelOf(tc.policy); got != tc.want {
				t.Errorf("ImageReferenceLevelOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// The level of a source reference has its own default: nothing is checked
// today, so a policy that names no level changes nothing.
func TestSourceReferenceLevelOf(t *testing.T) {
	tests := []struct {
		name   string
		policy oam.Policy
		want   oam.ImageReferenceLevel
	}{
		{"no policy", nil, oam.ImageLevelAny},
		{"NoopPolicy", &oam.NoopPolicy{}, oam.ImageLevelAny},
		{"a policy without the interface", plainPolicy{&oam.NoopPolicy{}}, oam.ImageLevelAny},
		{"a policy that names no level", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}}, oam.ImageLevelAny},
		{"digest", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, source: oam.ImageLevelDigest}, oam.ImageLevelDigest},
		{"the image level does not reach it", imageLevelPolicy{NoopPolicy: &oam.NoopPolicy{}, image: oam.ImageLevelDigest}, oam.ImageLevelAny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := oam.SourceReferenceLevelOf(tc.policy); got != tc.want {
				t.Errorf("SourceReferenceLevelOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageReferenceLevel_Valid(t *testing.T) {
	for _, l := range []oam.ImageReferenceLevel{oam.ImageLevelAny, oam.ImageLevelTagged, oam.ImageLevelVersion, oam.ImageLevelDigest} {
		if !l.Valid() {
			t.Errorf("%q is not valid, want valid", l)
		}
	}
	for _, l := range []oam.ImageReferenceLevel{"", "Tagged", "strict", "latest"} {
		if l.Valid() {
			t.Errorf("%q is valid, want refused", l)
		}
	}
}
