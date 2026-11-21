package oam

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/google/go-containerregistry/pkg/name"
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

// LevelAwareHandler is an optional interface of a ComponentHandler that can build
// its config at an image reference level (go-kure/launcher#1644). The Transformer
// asks it at the level of the policy; ToApplicationConfig, which a caller with no
// policy reaches, is the same at ImageLevelTagged. A handler without it is built
// at the default level and held to the policy's by the Transformer afterwards, so
// it can be stricter than the default but never looser.
type LevelAwareHandler interface {
	ComponentHandler
	ToApplicationConfigAt(component *Component, namespace string, level ImageReferenceLevel) (stack.ApplicationConfig, error)
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
		return fmt.Errorf("image %q rejected: %q is not an image reference level (any, tagged, version or digest)", image, string(level))
	}
	ref, err := name.ParseReference(image)
	if err != nil {
		return fmt.Errorf("image %q rejected: %w", image, err)
	}
	switch level {
	case ImageLevelTagged:
		switch r := ref.(type) {
		case name.Digest:
			if hasExplicitLatestTag(image) {
				return fmt.Errorf("image %q rejected: :latest tag not allowed; use an explicit version tag or digest", image)
			}
		case name.Tag:
			if r.TagStr() != "latest" {
				return nil
			}
			if hasExplicitLatestTag(image) {
				return fmt.Errorf("image %q rejected: :latest tag not allowed; use an explicit version tag or digest", image)
			}
			return fmt.Errorf("image %q rejected: no tag or digest specified; use an explicit version tag or digest", image)
		}
	case ImageLevelDigest:
		if _, ok := ref.(name.Digest); !ok {
			return fmt.Errorf("image %q rejected: level %q requires a digest (name@sha256:...)", image, string(level))
		}
	case ImageLevelVersion:
		if !versionTag.MatchString(explicitTag(image)) {
			return fmt.Errorf("image %q rejected: level %q requires a version tag, such as v1.2 or 1.2.3-alpine", image, string(level))
		}
	}
	return nil
}

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
				return fmt.Errorf("%s: %w", r.Field, err)
			}
			return err
		}
	}
	return nil
}
