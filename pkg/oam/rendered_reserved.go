package oam

import (
	"encoding/json"
	"maps"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// renderedValues is the record of the PlatformReserved values a lowering rule rendered
// into one element's properties (Component.RenderReserved, Trait.RenderReserved): the
// dot-separated
// object-key path of each, mapped to the JSON encoding of the value written there.
// enforcePlatformReserved exempts a reserved key only while its current value
// encodes to exactly what is recorded at its own path, so the record vouches for a
// value, not for a key: a value copied into the key from anywhere else, or the
// rendered one changed afterwards, is checked as authored.
//
// The encoding is the snapshot. It cannot be changed through the value the rule
// passed in, and it compares equal across the rewrite emission validation makes (a
// typed Go collection becoming []any or map[string]any, a named scalar its
// predeclared type), which leaves what the value serializes to untouched.
//
// A record map is never written once built: adding a path builds a new one
// (renderedValues.with), so recording on one copy of an element never exempts
// another copy that shares the old map. The record is unexported, so YAML can
// neither set it nor carry it: a document that is marshalled and parsed again
// arrives without one.
type renderedValues map[string]string

// RenderReserved writes value at path in c.Properties and records it as a value a
// lowering rule rendered, so the D3 check (enforcePlatformReserved) accepts it in a
// key the component's schema marks PlatformReserved. It is for a rule whose output
// is otherwise checked as authored — every component a DocumentLoweringRule builds,
// and the output of any rule whose input nothing checked — that renders a reserved
// value from LoweringContext.Capabilities. Writing a reserved value any other way
// leaves it authored, and Transform rejects it with ErrPlatformReserved.
//
// path is a dot-separated list of object keys below Properties, for example
// "networkPolicy" or "tls.secretName". Missing objects along it are created as
// map[string]any, and the properties map itself when it is nil. An empty path or
// segment, a segment holding '[' or ']' (an array item cannot be addressed), and an
// existing value along the path that is not a map[string]any are errors, and leave
// c unchanged. The write is in place, like an assignment into c.Properties, so a
// rule that copied a component it was handed gives the copy its own properties map
// first: a rule must not mutate its input.
//
// value must encode to JSON and hold no null at any depth: a null is absence (the
// null contract in property_validate.go), and emission validation drops a nested one,
// which would leave the value no longer matching its record. A nil value is refused
// for the same reason.
//
// The exemption covers exactly the value recorded at exactly that path. A reserved
// key nested inside a recorded object needs its own call; a recorded value changed
// afterwards — through a map it holds, by assignment, or by a later RenderReserved
// call below its path — is checked as authored until it is rendered again. The
// record survives copies of the component and later lowering rounds, but not
// serialization, so a RawDocumentLoweringRule cannot use it: what LowerRaws returns
// is parsed again before Transform.
func (c *Component) RenderReserved(path string, value any) error {
	return renderReserved(&c.Properties, &c.rendered, path, value)
}

// RenderReserved is Component.RenderReserved for a trait, with the same path, value
// and exemption rules, checked against the schema of the trait's own type: it writes
// value at path in t.Properties and records it, so the D3 check accepts it in a key
// that schema marks PlatformReserved. It is for every trait a DocumentLoweringRule
// builds, and any other trait the engine checks as authored, that carries a reserved
// value rendered from LoweringContext.Capabilities.
//
// The record covers t's own properties only. The ClusterProfile capability applyTraits
// merges into an unsealed trait at transform time is not recorded and needs none: the
// check runs on the trait before that merge. A trait handed to a ComponentLoweringRule
// shares its properties map with the authored one, so a rule gives a trait it copied
// its own map before writing, as for a component.
func (t *Trait) RenderReserved(path string, value any) error {
	return renderReserved(&t.Properties, &t.rendered, path, value)
}

// renderReserved is RenderReserved over a properties map and its record, shared by
// Component and Trait.
func renderReserved(props *map[string]any, rendered *renderedValues, path string, value any) error {
	segments, err := parseRenderedPath(path)
	if err != nil {
		return err
	}
	encoded, err := encodeRenderedValue(value)
	if err != nil {
		return errors.Wrapf(err, "render reserved %q", path)
	}
	if err := setPropertyAt(props, segments, value); err != nil {
		return errors.Wrapf(err, "render reserved %q", path)
	}
	*rendered = rendered.with(path, encoded)
	return nil
}

// parseRenderedPath splits a RenderReserved path into its object keys.
func parseRenderedPath(path string) ([]string, error) {
	if path == "" {
		return nil, errors.New("render reserved: empty path")
	}
	segments := strings.Split(path, ".")
	for _, segment := range segments {
		if segment == "" {
			return nil, errors.Errorf("render reserved %q: empty path segment", path)
		}
		if strings.ContainsAny(segment, "[]") {
			return nil, errors.Errorf("render reserved %q: segment %q indexes an array, which is not supported", path, segment)
		}
	}
	return segments, nil
}

// encodeRenderedValue is the snapshot renderReserved records for value, refusing a
// value that does not encode to JSON or holds a null.
func encodeRenderedValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.Wrap(err, "value does not encode to JSON")
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return "", errors.Wrap(err, "value does not decode from its own JSON encoding")
	}
	if jsonHoldsNull(decoded) {
		return "", errors.New("value is or holds a null, which is absence and cannot be rendered")
	}
	return string(encoded), nil
}

// jsonHoldsNull reports whether a decoded JSON value is null or holds one at any depth.
func jsonHoldsNull(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range v {
			if jsonHoldsNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if jsonHoldsNull(item) {
				return true
			}
		}
	}
	return false
}

// setPropertyAt writes value at the object-key path segments in *props, creating the
// map and missing intermediate objects. It fails only on an intermediate value that
// already exists, before creating anything, so a failed write changes nothing.
func setPropertyAt(props *map[string]any, segments []string, value any) error {
	if *props == nil {
		*props = map[string]any{}
	}
	obj := *props
	last := len(segments) - 1
	for i, segment := range segments[:last] {
		next, present := obj[segment]
		if !present {
			child := map[string]any{}
			obj[segment] = child
			obj = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return errors.Errorf("%q holds %T, not an object", strings.Join(segments[:i+1], "."), next)
		}
		obj = child
	}
	obj[segments[last]] = value
	return nil
}

// with returns a new record holding r's entries and path, never writing r itself.
func (r renderedValues) with(path, encoded string) renderedValues {
	out := make(renderedValues, len(r)+1)
	maps.Copy(out, r)
	out[path] = encoded
	return out
}

// child is the record and path the D3 walk carries into key below at: nil once the
// key cannot be named by a RenderReserved path (empty, or holding a dot that would
// read as a separator), so nothing below it is exempt.
func (r renderedValues) child(at, key string) (renderedValues, string) {
	if r == nil || key == "" || strings.Contains(key, ".") {
		return nil, ""
	}
	if at == "" {
		return r, key
	}
	return r, at + "." + key
}

// exempts reports whether value is the one recorded at path.
func (r renderedValues) exempts(path string, value any) bool {
	recorded, ok := r[path]
	if !ok {
		return false
	}
	encoded, err := json.Marshal(value)
	return err == nil && string(encoded) == recorded
}
