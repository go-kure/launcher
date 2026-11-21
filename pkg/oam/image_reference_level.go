package oam

// ImageReferenceLevel says how exactly an image reference must pin what it
// names (go-kure/launcher#1644). The levels are not nested: `any` accepts what
// `tagged` refuses, `tagged` accepts a branch tag that `version` refuses.
type ImageReferenceLevel string

const (
	// ImageLevelAny accepts every reference the parser accepts, an untagged one
	// and :latest included.
	ImageLevelAny ImageReferenceLevel = "any"
	// ImageLevelTagged is the default and the rule as it was before there was a
	// level: a tag other than latest, or a digest, and no explicit :latest.
	ImageLevelTagged ImageReferenceLevel = "tagged"
	// ImageLevelVersion requires a version tag: an optional v, one to three
	// numeric parts and an optional suffix (v1.2, 1.2.3-alpine). A digest may
	// follow it; a digest alone names no version.
	ImageLevelVersion ImageReferenceLevel = "version"
	// ImageLevelDigest requires a digest, with or without a tag.
	ImageLevelDigest ImageReferenceLevel = "digest"
)

// Valid reports whether l is one of the four levels.
func (l ImageReferenceLevel) Valid() bool {
	switch l {
	case ImageLevelAny, ImageLevelTagged, ImageLevelVersion, ImageLevelDigest:
		return true
	}
	return false
}

// ImageLevel is the level a handler holds the component's image references to:
// ImageLevelTagged on a component the Transformer did not hand over under a
// policy that names a level, whatever else it is. A handler reads it in
// ToApplicationConfig, so a type that embeds a handler and overrides
// ToApplicationConfig, then copies the component it is given, passes the level on
// (go-kure/launcher#1644).
func (c Component) ImageLevel() ImageReferenceLevel {
	if c.imageLevel == "" {
		return ImageLevelTagged
	}
	return c.imageLevel
}

// WithImageLevel returns a copy of the component whose image references a
// handler holds to level. The Transformer does this for every component it hands
// a handler; code that drives a handler directly calls it to ask for a level.
func (c Component) WithImageLevel(level ImageReferenceLevel) Component {
	c.imageLevel = level
	return c
}

// ImageReferencePolicy is an optional interface of a Policy: the level the
// image references of a document are held to (go-kure/launcher#1644), on
// containers, init containers, image volumes, rendered charts and the image
// fields of the operator components.
//
// A Policy that does not implement it, a Policy that returns "", and
// NoopPolicy are held to ImageLevelTagged, the rule as it was: the interface
// exists so that adding the level changes nothing for a consumer that has not
// asked for it. Only a Policy that names a level changes it.
type ImageReferencePolicy interface {
	ImageReferenceLevel() ImageReferenceLevel
}

// ImageReferenceLevelOf is the level policy holds an image reference to:
// ImageLevelTagged for a nil policy, one that does not implement
// ImageReferencePolicy, and one that names no level.
func ImageReferenceLevelOf(policy Policy) ImageReferenceLevel {
	if p, ok := policy.(ImageReferencePolicy); ok {
		if l := p.ImageReferenceLevel(); l != "" {
			return l
		}
	}
	return ImageLevelTagged
}

// SourceReferencePolicy is an optional interface of a Policy: the level the
// reference of an OCI source component (its version) is held to. It is a level
// of its own, because a source that tracks a moving tag is a different thing
// from a workload that runs one.
//
// A Policy that does not implement it, one that returns "", and NoopPolicy
// hold the reference to ImageLevelAny: nothing was checked before, so nothing
// changes until a consumer asks.
type SourceReferencePolicy interface {
	SourceReferenceLevel() ImageReferenceLevel
}

// SourceReferenceLevelOf is the level policy holds an OCI source reference to:
// ImageLevelAny for a nil policy, one that does not implement
// SourceReferencePolicy, and one that names no level.
func SourceReferenceLevelOf(policy Policy) ImageReferenceLevel {
	if p, ok := policy.(SourceReferencePolicy); ok {
		if l := p.SourceReferenceLevel(); l != "" {
			return l
		}
	}
	return ImageLevelAny
}
