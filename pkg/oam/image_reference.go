package oam

import (
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/go-kure/launcher/pkg/errors"
)

// ImageReference is one image reference a config holds, with where it came
// from. Field is the text a refusal puts in front of the reference's own
// ("containers[0] \"app\"", "volumes[1] \"ext\" image.reference"), and is ""
// for the one image of a component, where the refusal needs no field.
type ImageReference struct {
	Field     string
	Reference string
}

// ImageReferenced is an optional interface of an ApplicationConfig: the image
// references it holds (go-kure/launcher#1644). After a handler builds a config,
// the Transformer holds each of them to the level of the policy, so a level
// stricter than the handler knows is enforced whatever the handler implements.
type ImageReferenced interface {
	ImageReferences() []ImageReference
}

// versionTag is the tag the version level accepts: an optional v, one to three
// numeric parts, and an optional suffix of what an OCI tag can hold after a
// separator (v1.2, 1.2.3-alpine, 1.2.3-rc.1). Loose on purpose; it is no
// semver validator.
var versionTag = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}([-_.][A-Za-z0-9._-]*)?$`)

// CheckImageReference holds one image reference to a level. At ImageLevelTagged
// it is the rule as it was before there was a level: no untagged image and no
// :latest, including a digest reference that still carries an explicit :latest
// tag (`repo:latest@sha256:...`). The digest pins the content, but the tag
// stays part of the reference and a cluster is free to read it. At the other
// levels a refusal names the reference and the level; a caller names the field.
// A reference the parser refuses is refused at every level, and a level that is
// not one of the four is refused, never read as the default: a misspelt level
// must not loosen the rule.
func CheckImageReference(image string, level ImageReferenceLevel) error {
	if !level.Valid() {
		return errors.Errorf("image %q rejected: %q is not an image reference level (any, tagged, version or digest)", image, string(level))
	}
	ref, err := name.ParseReference(image)
	if err != nil {
		return errors.Wrapf(err, "image %q rejected", image)
	}
	switch level {
	case ImageLevelAny:
		// Syntax only: the parser accepted it.
	case ImageLevelTagged:
		switch r := ref.(type) {
		case name.Digest:
			if hasExplicitLatestTag(image) {
				return errors.Errorf("image %q rejected: :latest tag not allowed; use an explicit version tag or digest", image)
			}
		case name.Tag:
			if r.TagStr() != "latest" {
				return nil
			}
			if hasExplicitLatestTag(image) {
				return errors.Errorf("image %q rejected: :latest tag not allowed; use an explicit version tag or digest", image)
			}
			return errors.Errorf("image %q rejected: no tag or digest specified; use an explicit version tag or digest", image)
		}
	case ImageLevelDigest:
		if _, ok := ref.(name.Digest); !ok {
			return errors.Errorf("image %q rejected: level %q requires a digest (name@sha256:...)", image, string(level))
		}
	case ImageLevelVersion:
		if !versionTag.MatchString(explicitTag(image)) {
			return errors.Errorf("image %q rejected: level %q requires a version tag, such as v1.2 or 1.2.3-alpine", image, string(level))
		}
	}
	return nil
}

// CheckSourceVersion holds the version of an OCI source component, which is a
// tag or sha256:<digest> and no image reference, to level
// (go-kure/launcher#1644):
//
//   - any: nothing.
//   - tagged: a tag or a digest, as for an image: not empty and not latest.
//   - version: a version tag (the pattern CheckImageReference's version level
//     reads) or a digest.
//   - digest: sha256:<digest> only.
//
// A level that is not one of the four is refused, as CheckImageReference
// refuses it.
func CheckSourceVersion(version string, level ImageReferenceLevel) error {
	if !level.Valid() {
		return errors.Errorf("version %q rejected: %q is not an image reference level (any, tagged, version or digest)", version, string(level))
	}
	digest := len(version) > len(sourceDigestPrefix) && strings.HasPrefix(version, sourceDigestPrefix)
	switch level {
	case ImageLevelAny:
		// Nothing is held.
	case ImageLevelTagged:
		if version == "" {
			return errors.Errorf("version rejected: level %q requires a tag or a digest", string(level))
		}
		if version == "latest" {
			return errors.Errorf("version %q rejected: level %q refuses the latest tag; use an explicit version tag or a digest", version, string(level))
		}
	case ImageLevelVersion:
		if !digest && !versionTag.MatchString(version) {
			return errors.Errorf("version %q rejected: level %q requires a version tag, such as v1.2 or 1.2.3-alpine, or a digest", version, string(level))
		}
	case ImageLevelDigest:
		if !digest {
			return errors.Errorf("version %q rejected: level %q requires a digest (sha256:<digest>)", version, string(level))
		}
	}
	return nil
}

// sourceDigestPrefix starts the digest form an OCI source's version takes. The
// component reads any value with this prefix as a digest and leaves its shape
// to the source controller, and so does this check.
const sourceDigestPrefix = "sha256:"

func hasExplicitLatestTag(image string) bool {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	return strings.HasSuffix(image, ":latest")
}

// explicitTag is the tag an image reference writes, "" when it writes none. A
// digest after it is left out first, and the tag is looked for only after the
// last "/", so a registry port is no tag.
func explicitTag(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	if i := strings.Index(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return ""
}

// checkImageReferences holds the references config names to level. The text of
// a refusal is the field, when there is one, then the refusal.
func checkImageReferences(config any, level ImageReferenceLevel) error {
	imaged, ok := config.(ImageReferenced)
	if !ok {
		return nil
	}
	for _, r := range imaged.ImageReferences() {
		if err := CheckImageReference(r.Reference, level); err != nil {
			if r.Field != "" {
				return errors.Wrap(err, r.Field)
			}
			return err
		}
	}
	return nil
}
