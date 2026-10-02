package oam

import (
	"maps"
	"math"
	"reflect"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// renderedValues is the record of the PlatformReserved values a lowering rule rendered
// into one element's properties (Component.RenderReserved, Trait.RenderReserved): the
// dot-separated object-key path of each, mapped to a snapshot of the value written
// there (snapshotRenderedValue). enforcePlatformReserved exempts a reserved key only
// while its current value is the recorded one at its own path (sameRenderedValue),
// so the record vouches for a value, not for a key: a value copied into the key from
// anywhere else, or the rendered one changed afterwards, is checked as authored.
//
// The snapshot is a deep copy in the shapes emission validation normalizes to:
// strings, booleans, numbers, []any and map[string]any. It cannot be changed through
// the value the rule passed in, and the comparison reads the current value through
// the same coercions validatePropertyValue applies (asStringValue, asArrayValue,
// asObjectValue, asExactNumber), so the rewrite emission validation makes — a typed Go
// collection becoming []any or map[string]any, a named scalar its predeclared type, an
// integer of another kind int — compares equal, while two numbers a property reader
// tells apart never do: numbers are compared exactly, never through float64 or a
// JSON encoding, which reads 1000000000000000100 and 1.0000000000000001e+18 as the
// same value though IntegerValue does not.
//
// A record map is never written once built: adding a path builds a new one
// (renderedValues.with), so recording on one copy of an element never exempts
// another copy that shares the old map. The record is unexported, so YAML can
// neither set it nor carry it: a document that is marshalled and parsed again
// arrives without one.
type renderedValues map[string]any

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
// map[string]any, and the properties map itself when it is nil; an object along it
// that is null (nil, or a typed nil such as map[string]any(nil)) is absent by the
// null contract in property_validate.go, and is replaced by a new map[string]any the
// same way. An empty path or segment, a segment holding '[' or ']' (an array item
// cannot be addressed), and an existing non-null value along the path that is not a
// map[string]any are errors, and leave c unchanged. The write is in place, like an
// assignment into c.Properties, so a rule that copied a component it was handed gives
// the copy its own properties map first: a rule must not mutate its input.
//
// value must be a property value — a string, boolean or finite number of any Go kind,
// a slice or array of them, or a map with string-kinded keys holding them, nested to
// any depth — and hold no null at any depth: a null is absence, and emission
// validation drops a nested one, which would leave the value no longer matching its
// record. A nil value is refused for the same reason, and so is any other Go type (a
// pointer, a struct, a func, a channel, a map with keys that are not strings), NaN,
// ±Inf and a collection that contains itself.
//
// The exemption covers exactly the value recorded at exactly that path. A reserved
// key nested inside a recorded object needs its own call; a recorded value changed
// afterwards — through a map it holds, by assignment, or by a later RenderReserved
// call below its path — is checked as authored until it is rendered again. The
// record survives copies of the component and later lowering rounds, but not
// serialization, so a RawDocumentLoweringRule cannot use it: what LowerRaws returns
// is parsed again before Transform.
func (c *Component) RenderReserved(path string, value any) error {
	if c == nil {
		return errors.Errorf("render reserved %q: nil component", path)
	}
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
	if t == nil {
		return errors.Errorf("render reserved %q: nil trait", path)
	}
	return renderReserved(&t.Properties, &t.rendered, path, value)
}

// renderReserved is RenderReserved over a properties map and its record, shared by
// Component and Trait.
func renderReserved(props *map[string]any, rendered *renderedValues, path string, value any) error {
	segments, err := parseRenderedPath(path)
	if err != nil {
		return err
	}
	snapshot, err := snapshotRenderedValue(value, map[propertyCopyKey]bool{})
	if err != nil {
		return errors.Wrapf(err, "render reserved %q", path)
	}
	if err := setPropertyAt(props, segments, value); err != nil {
		return errors.Wrapf(err, "render reserved %q", path)
	}
	*rendered = rendered.with(path, snapshot)
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

// snapshotRenderedValue is the snapshot renderReserved records for value: a deep copy
// holding a string for every string kind, a bool for every boolean kind, an int64,
// uint64 or float64 for every integer, unsigned or floating-point kind (each holds
// its value exactly), []any for every slice or array kind and map[string]any for
// every map with string-kinded keys. It refuses a null at any depth, NaN and ±Inf,
// any other Go type, and a map or slice that contains itself; onPath holds the maps
// and slices being copied above value.
func snapshotRenderedValue(value any, onPath map[propertyCopyKey]bool) (any, error) {
	if isNullValue(value) {
		return nil, errors.New("value is or holds a null, which is absence and cannot be rendered")
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint(), nil
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.Errorf("value is or holds %v, which is not a finite number", f)
		}
		return f, nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Len() > 0 {
			key := propertyCopyKey{ptr: rv.Pointer(), len: rv.Len(), typ: rv.Type()}
			if onPath[key] {
				return nil, errors.New("value contains itself")
			}
			onPath[key] = true
			defer delete(onPath, key)
		}
		out := make([]any, rv.Len())
		for i := range out {
			item, err := snapshotRenderedValue(rv.Index(i).Interface(), onPath)
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, errors.Errorf("value is or holds %T, a map whose keys are not strings", value)
		}
		key := propertyCopyKey{ptr: rv.Pointer(), typ: rv.Type()}
		if onPath[key] {
			return nil, errors.New("value contains itself")
		}
		onPath[key] = true
		defer delete(onPath, key)
		out := make(map[string]any, rv.Len())
		for iter := rv.MapRange(); iter.Next(); {
			item, err := snapshotRenderedValue(iter.Value().Interface(), onPath)
			if err != nil {
				return nil, err
			}
			out[iter.Key().String()] = item
		}
		return out, nil
	default:
		return nil, errors.Errorf("value is or holds %T, which is not a property value (a string, boolean, number, list or string-keyed object)", value)
	}
}

// sameRenderedValue reports whether current is the value recorded, a snapshot
// snapshotRenderedValue took. A null in current is never equal, since the snapshot
// holds none. Otherwise current is read through the coercions validatePropertyValue
// applies — any string or boolean kind, any slice or array kind as a list, any map
// with string-kinded keys as an object — and numbers are compared exactly
// (asExactNumber), so an integer of any kind equals a float only when the float is
// exactly that integer. The walk follows the snapshot, so it ends at its depth
// whatever current holds.
func sameRenderedValue(recorded, current any) bool {
	if isNullValue(current) {
		return false
	}
	switch r := recorded.(type) {
	case string:
		s, ok := asStringValue(current)
		return ok && s == r
	case bool:
		cv := reflect.ValueOf(current)
		return cv.Kind() == reflect.Bool && cv.Bool() == r
	case []any:
		items, ok := asArrayValue(current)
		if !ok || len(items) != len(r) {
			return false
		}
		for i := range r {
			if !sameRenderedValue(r[i], items[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		obj, ok := asObjectValue(current)
		if !ok || len(obj) != len(r) {
			return false
		}
		for key, item := range r {
			cur, present := obj[key]
			if !present || !sameRenderedValue(item, cur) {
				return false
			}
		}
		return true
	default:
		rn, ok := asExactNumber(recorded)
		if !ok {
			return false
		}
		cn, ok := asExactNumber(current)
		return ok && rn.equal(cn)
	}
}

// setPropertyAt writes value at the object-key path segments in *props, creating the
// map and missing intermediate objects; an intermediate holding a null is missing, and
// is replaced. It fails only on an intermediate value that already exists, is not
// null and is not a map[string]any. That is checked before anything is created or
// replaced — once one object is new, every object below it is too — so a failed write
// changes nothing.
func setPropertyAt(props *map[string]any, segments []string, value any) error {
	if *props == nil {
		*props = map[string]any{}
	}
	obj := *props
	last := len(segments) - 1
	for i, segment := range segments[:last] {
		next, present := obj[segment]
		if !present || isNullValue(next) {
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
func (r renderedValues) with(path string, snapshot any) renderedValues {
	out := make(renderedValues, len(r)+1)
	maps.Copy(out, r)
	out[path] = snapshot
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
	return ok && sameRenderedValue(recorded, value)
}
