package requiredfields

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
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
// required maps a json path, with [] for a list element and {} for a map
// value, to what the refusal says of the field. A field's name may be followed
// by several such steps, applied left to right: `headers{}[].key` is the key of
// every element of every list a map holds. A path is followed through what was
// authored, and under a parent the author left out it holds nothing to refuse,
// so every field before the last must be one the type omits when unset. The
// keys of a struct's fields match case-insensitively, as the decode's do; the
// keys of a map are its entries, each descended as written. Where the author
// wrote one field in two spellings the decode keeps one or merges both, and
// which is the type's affair, so each spelling is held to the list: the check
// never reads another value than the object is built from. A last field
// authored null in one spelling and set in another is left out by the null
// one, which the decode may take last and so write the field empty; a caller
// that drops null keys keeps such a key, as null. Paths are read in sorted
// order, the spellings of a field in theirs, a list in its own and a map's
// keys in theirs, so the field reported is the same on every build. The
// refusal names a list element by its index and a map value by its key in
// brackets, the key quoted where it holds a dot.
func Refuse(authored map[string]any, required map[string]string) error {
	for _, path := range slices.Sorted(maps.Keys(required)) {
		if at := unauthoredAt(authored, strings.Split(path, "."), ""); at != "" {
			return errors.Errorf("%s: required (%s)", at, required[path])
		}
	}
	return nil
}

// LabelSelector is the required list of one label selector under the path at
// ("selector", "ingress[].from[].podSelector"): the two fields the API
// requires of a match expression, each of which the Go type would write empty.
// It holds for the Kubernetes type and for the copy of it Cilium's API
// declares, which name and encode the two fields alike.
func LabelSelector(at string) map[string]string {
	return map[string]string{
		at + ".matchExpressions[].key":      "the label key the expression applies to",
		at + ".matchExpressions[].operator": "the operator of the expression: In, NotIn, Exists or DoesNotExist",
	}
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
	name, steps := segmentSteps(segments[0])
	keys := spellings(fields, name)
	switch {
	case len(keys) == 0 && len(segments) == 1:
		return at + name
	case len(keys) == 0:
		return ""
	case len(segments) == 1:
		for _, key := range keys {
			if fields[key] == nil {
				return at + key
			}
		}
		return ""
	}
	for _, key := range keys {
		if missing := unauthoredUnder(fields[key], steps, segments[1:], at+key); missing != "" {
			return missing
		}
	}
	return ""
}

// unauthoredUnder applies steps to value, an authored value at the path at:
// [] descends every element of a list, {} every value of a map, and once no
// step is left the field the segments name is looked for. It returns where
// that field is missing, "" where it is not or value holds no parent of it.
func unauthoredUnder(value any, steps, segments []string, at string) string {
	if len(steps) == 0 {
		return unauthoredAt(value, segments, at)
	}
	switch steps[0] {
	case "[]":
		items, _ := value.([]any)
		for i, item := range items {
			if missing := unauthoredUnder(item, steps[1:], segments, fmt.Sprintf("%s[%d]", at, i)); missing != "" {
				return missing
			}
		}
	default:
		entries, _ := value.(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			if missing := unauthoredUnder(entries[key], steps[1:], segments, at+"["+mapKey(key)+"]"); missing != "" {
				return missing
			}
		}
	}
	return ""
}

// segmentSteps splits one segment of a path into the field's name and the
// steps that follow it, outermost first: `headers{}[]` is headers, {}, [].
func segmentSteps(segment string) (string, []string) {
	var steps []string
	for {
		name, list := strings.CutSuffix(segment, "[]")
		if !list {
			var values bool
			if name, values = strings.CutSuffix(segment, "{}"); !values {
				return segment, steps
			}
		}
		segment, steps = name, append([]string{segment[len(name):]}, steps...)
	}
}

// mapKey is how a refusal names a map value by its key: the key, quoted where
// it holds a dot, which would otherwise read as a field.
func mapKey(key string) string {
	if strings.Contains(key, ".") {
		return strconv.Quote(key)
	}
	return key
}

// spellings returns the keys of fields that name the field name, in sorted
// order: every key that matches it case-insensitively, the folding
// encoding/json applies to struct field names. A segment's name is a struct
// field's (a map's entries are a {} step), so two such keys are two spellings
// of one field and not two entries of a map.
func spellings(fields map[string]any, name string) []string {
	var keys []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	return keys
}
