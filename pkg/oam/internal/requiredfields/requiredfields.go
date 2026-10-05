package requiredfields

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// Refuse refuses a field of required the author left out. It is for a field
// the API requires that the upstream type encodes whether or not it was
// authored: a struct that is no pointer, or a list or a scalar without
// omitempty. The object would hold an empty one the author did not write, and
// where the API server accepts that value (an empty selector selects
// everything, 0 is an expression) nothing refuses the omission any more. The
// decoded value cannot tell an authored empty one from none, so authored is
// read: a property tree of string-keyed maps, lists and scalars in which a
// field authored as null is absent.
//
// required maps a json path, with [] for a list element, to what the refusal
// says of the field. A path is followed through what was authored, and under a
// parent the author left out it holds nothing to refuse, so every field before
// the last must be one the type omits when unset. Keys match
// case-insensitively, as the decode's do. Where the author wrote one field in
// two spellings the decode keeps one or merges both, and which is the type's
// affair, so each spelling is held to the list: the check never reads another
// value than the object is built from. Paths are read in sorted order, the
// spellings of a field in theirs and a list in its own, so the field reported
// is the same on every build.
func Refuse(authored map[string]any, required map[string]string) error {
	for _, path := range slices.Sorted(maps.Keys(required)) {
		if at := unauthoredAt(authored, strings.Split(path, "."), ""); at != "" {
			return errors.Errorf("%s: required (%s)", at, required[path])
		}
	}
	return nil
}

// unauthoredAt returns where under node, an authored value at the path at, the
// field the segments name is missing: "" when it is authored wherever its
// parent is, or when node holds no parent of it.
func unauthoredAt(node any, segments []string, at string) string {
	fields, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if at != "" {
		at += "."
	}
	name, list := strings.CutSuffix(segments[0], "[]")
	keys := spellings(fields, name)
	switch {
	case len(keys) == 0 && len(segments) == 1:
		return at + name
	case len(keys) == 0 || len(segments) == 1:
		return ""
	}
	for _, key := range keys {
		if !list {
			if missing := unauthoredAt(fields[key], segments[1:], at+key); missing != "" {
				return missing
			}
			continue
		}
		items, _ := fields[key].([]any)
		for i, item := range items {
			if missing := unauthoredAt(item, segments[1:], fmt.Sprintf("%s%s[%d]", at, key, i)); missing != "" {
				return missing
			}
		}
	}
	return ""
}

// spellings returns the keys of fields that name the field name, in sorted
// order: every key that matches it case-insensitively, the folding
// encoding/json applies to struct field names. A path names struct fields
// only, so two such keys are two spellings of one field and not two entries of
// a map.
func spellings(fields map[string]any, name string) []string {
	var keys []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	return keys
}
