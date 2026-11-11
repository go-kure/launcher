package patch

import (
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/go-kure/launcher/pkg/errors"
)

// StrategicPatch represents a partial YAML document for deep merge
// using Kubernetes strategic merge patch semantics.
type StrategicPatch struct {
	Patch map[string]any
}

// ApplyStrategicMergePatch applies a strategic merge patch to a resource.
// For known Kubernetes kinds (registered in the lookup scheme), it uses
// StrategicMergeMapPatch with typed struct tags to enable list-merge-by-key
// semantics (e.g. containers merged by name).
// For unknown kinds (CRDs), it falls back to RFC 7386 JSON merge patch.
// If lookup is nil, fallback is always used.
// On an unknown kind a nil patch changes nothing, the resource included; on a
// known kind the resource is normalized as for any patch (see jsonTyped). With a
// patch, both paths read a nil one in the resource or in the patch, through
// chains of map[string]any and []any, as an empty one, so only an explicit nil
// value deletes a field, and a chain nested deeper than maxDepth or containing
// itself is an error. Any other container type is left to encoding/json: a nil
// map[string]string is null. On both paths a number in the merged resource has
// the type jsonTyped describes: an int64 where a whole number fits, not the
// float64 that the JSON merge fallback used to return.
func ApplyStrategicMergePatch(
	resource *unstructured.Unstructured,
	patch map[string]any,
	lookup KindLookup,
) error {
	gvk := resource.GroupVersionKind()

	if lookup != nil {
		typedObj, ok := lookup.LookupKind(gvk)
		if ok {
			// applyTypedSMP normalizes, and so copies, the patch.
			return applyTypedSMP(resource, patch, typedObj)
		}
	}

	// On the fallback a nil patch changes nothing: marshalled as null it would
	// replace the whole resource.
	if patch == nil {
		return nil
	}
	// A nil slice or map in the patch is an empty one, as on a known kind; only an
	// explicit nil value is the null that deletes a field. The merge only reads the
	// patch, so it is not copied unless it holds a nil container.
	patch, err := normalizeNil(patch)
	if err != nil {
		return errors.Wrap(err, "failed to normalize patch for JSON merge patch")
	}
	return applyJSONMergePatch(resource, patch)
}

// applyTypedSMP applies a strategic merge patch using Go struct tags from the
// typed object. StrategicMergeMapPatch mutates its arguments, so it merges
// normalized copies of the resource map and the patch. On error the resource is
// left untouched.
func applyTypedSMP(resource *unstructured.Unstructured, patch map[string]any, typedObj any) error {
	// Both inputs go through JSON, which has no NaN and no infinity: name the number
	// before the marshal fails without a path.
	if err := refuseNonFinite(resource.Object, patch); err != nil {
		return err
	}
	// Merge two inputs of the same types. The merge compares list merge keys by
	// type, so an int in the patch would not match the int64 an earlier patch left
	// in the resource. The round trip also copies, as the merge mutates its inputs.
	original, err := jsonTyped(resource.Object)
	if err != nil {
		return errors.Wrap(err, "failed to normalize resource for strategic merge patch")
	}
	patch, err = jsonTyped(patch)
	if err != nil {
		return errors.Wrap(err, "failed to normalize patch for strategic merge patch")
	}

	merged, err := strategicpatch.StrategicMergeMapPatch(original, patch, typedObj)
	if err != nil {
		return errors.Wrap(err, "strategic merge patch failed")
	}

	// Both inputs are normalized, so the merged map already holds JSON types. This
	// call copies it, so the resource shares nothing with the merge, and guards
	// against a type the merge could add.
	typed, err := jsonTyped(merged)
	if err != nil {
		return errors.Wrap(err, "failed to normalize strategic merge patch result")
	}

	resource.Object = typed
	return nil
}

// jsonTyped returns m as the JSON document it serializes to, decoded into the
// types an unstructured object may hold: map[string]any, []any, string, bool,
// nil and numbers. A number has the type its own JSON encoding decodes to: an
// int64 where a whole number fits, so that 80, 80.0 and 8e1 are alike, a float64
// for a fraction, and a json.Number, with its own digits, for an integer beyond
// the int64 range, which a float64 would round. A whole number written with a
// point or an exponent keeps its own digits too, up to 1e21: 9007199254740993.0 is
// the integer 9007199254740993, not the float64 beside it. Written that way from
// 1e21 a whole number stays a float64; between 2^63 and 1e21 it is a json.Number
// of plain digits, as is a plain integer of any size beyond the int64 range.
// Those are the types its encoding decodes to.
// A nil []any or map[string]any, at any depth through chains of those two types,
// becomes an empty one, not null, which a merge patch reads as a deletion; an
// explicit nil value stays nil, and so does a nil m itself. Any other container
// type (an array, a typed slice or map, a pointer), and whatever it holds, is
// left to encoding/json, so a nil []any inside one is written as encoding/json
// writes that element: null for an ordinary element, nothing under an omitempty
// tag, whatever a custom marshaler returns. A chain of []any and map[string]any
// that nests deeper than maxDepth, or contains itself, is an error. Nesting
// through any other container type is bounded only by encoding/json, which
// limits the nesting of JSON objects and arrays (a pointer or interface adds
// none) and returns an error at that limit, so a document nested
// beyond maxDepth through such a type can pass once and come out as plain
// []any and map[string]any. Normalizing twice changes nothing for a document
// within maxDepth; beyond it, the second pass can return errTooDeep.
func jsonTyped(m map[string]any) (map[string]any, error) {
	m, err := normalizeNil(m)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return decodeTyped(raw)
}

// decodeTyped decodes a JSON object into the types described on jsonTyped. The
// JSON merge fallback decodes its merged document with it too, so that an unknown
// kind returns the same number types as a known one.
func decodeTyped(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	for k, v := range out {
		out[k] = numbered(v)
	}
	return out, nil
}

// numbered gives the json.Number values of a document decoded with UseNumber
// the types described on jsonTyped.
func numbered(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if !strings.ContainsAny(t.String(), ".eE") {
			return t
		}
		if len(t) > maxNumberLiteral {
			return t
		}
		f, err := t.Float64()
		if err != nil {
			return t
		}
		// A whole float encodes as digits. Give it the type those digits decode to,
		// an int64 or, beyond that range, a json.Number, so that normalizing the
		// result again changes nothing. Above 2^53 a float64 has already rounded the
		// number, so take the digits from the number as written when it is a whole
		// number.
		digits := exactDigits(t, f)
		if digits == "" {
			if enc, err := json.Marshal(f); err == nil {
				digits = string(enc)
			}
		}
		if digits != "" {
			n := json.Number(digits)
			if i, err := n.Int64(); err == nil {
				return i
			}
			if !strings.ContainsAny(n.String(), ".eE") {
				return n
			}
		}
		return f
	case map[string]any:
		for k, e := range t {
			t[k] = numbered(e)
		}
	case []any:
		for i, e := range t {
			t[i] = numbered(e)
		}
	}
	return v
}

// exactDigits returns the digits of n as an integer when n is a whole number as
// written, f is its float64 and the integer lies in [2^53, 1e21), where a float64
// cannot hold every integer but the encoding of f is still plain digits.
// Otherwise it returns "". f is tested with 1e21 inclusive, as a number just
// below 1e21 rounds to it; the exact value is then tested against the bound. The
// f range keeps an exponent such as 1e-99999999 away from the exact parser.
func exactDigits(n json.Number, f float64) string {
	if a := math.Abs(f); a < 1<<53 || a > 1e21 {
		return ""
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok || !r.IsInt() || r.Num().CmpAbs(bigLimit) >= 0 {
		return ""
	}
	return r.Num().String()
}

// maxNumberLiteral is the longest number literal, in bytes, that numbered
// reads for its digits. The numbers it must read are shorter: at most 21 digits
// below bigLimit, a sign, a point and a few zeros or an exponent, about 30
// bytes. A longer literal, such as "9007199254740993." followed by a million
// zeros, would make the exact parse (big.Rat) cost time and memory in
// proportion to its length for no gain, so it is kept as the json.Number it
// was, with its text unchanged, and never rounded to a float64.
const maxNumberLiteral = 64

// bigLimit is 1e21, the first whole number the encoding of a float64 writes with
// an exponent.
var bigLimit = new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil)

// maxDepth bounds how deep the nil scan and the emptied walk go through []any and
// map[string]any, as encoding/json bounds the cycle check of json.Marshal: a
// chain of those two types that nests deeper, or contains itself, is an error,
// not a stack overflow. It does not bound any other container type.
const maxDepth = 1000

// normalizeNil returns m itself when it holds no nil []any or nil map[string]any
// reachable through chains of those two types, and otherwise a copy in which each
// is an empty one. The scan only reads, so a document without such a nil
// container is not copied here. A nil m is returned as is, not emptied.
func normalizeNil(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	found, err := hasNilContainer(m, 0)
	if err != nil || !found {
		return m, err
	}
	c, err := emptied(m, 0)
	if err != nil {
		return nil, err
	}
	return c.(map[string]any), nil
}

// hasNilContainer reports whether v holds a nil []any or nil map[string]any at
// any depth through chains of those two types; it does not look inside any other
// container type. A chain nested deeper than maxDepth, or containing itself, is
// an error on the branches the scan visits: it returns as soon as it finds a nil
// container, and the walk of emptied that follows covers the whole document.
func hasNilContainer(v any, depth int) (bool, error) {
	if depth > maxDepth {
		return false, errTooDeep
	}
	switch t := v.(type) {
	case map[string]any:
		if t == nil {
			return true, nil
		}
		for _, e := range t {
			if found, err := hasNilContainer(e, depth+1); err != nil || found {
				return found, err
			}
		}
	case []any:
		if t == nil {
			return true, nil
		}
		for _, e := range t {
			if found, err := hasNilContainer(e, depth+1); err != nil || found {
				return found, err
			}
		}
	}
	return false, nil
}

var errTooDeep = errors.New("the document is nested too deeply or contains itself")

// refuseNonFinite returns an error that names the first NaN or infinity in the
// resource or the patch, as "<where> holds the non-finite number <number> at <path>"
// with where "the resource" or "the patch", or nil when neither holds one. The
// strategic merge goes through JSON, which cannot carry such a number, so the merge
// could not keep it and refuses it. The inputs are read in the order the
// normalization reads them, the resource first. Each is first checked for structure
// with normalizeNil, the check the normalization makes: an input that nests deeper
// than maxDepth, or contains itself, ends the whole check with no finding, so that the
// normalization that follows reports that error first, as it did before, and the walk
// below never meets a cycle. The walk goes through chains of map[string]any and []any,
// in sorted key order, so the path does not depend on the order a map is read; the
// path is spelled as the ResolveTOMLPath field path is (keys joined by a dot, a list
// index as [n]). Any other container type is left to encoding/json, as normalizeNil
// leaves it.
func refuseNonFinite(resource, patch map[string]any) error {
	for _, in := range []struct {
		where string
		m     map[string]any
	}{{"the resource", resource}, {"the patch", patch}} {
		m, err := normalizeNil(in.m)
		if err != nil {
			//nolint:nilerr // a structure error is not ours to report: the normalization that follows reports it first
			return nil
		}
		if path, number, found := findNonFinite(m, "", 0); found {
			return errors.Errorf("%s holds the non-finite number %s at %s, and a strategic merge patch goes through JSON, which has no NaN or Inf", in.where, number, path)
		}
	}
	return nil
}

// findNonFinite returns the path and the text (NaN, +Inf or -Inf) of the first
// non-finite float in v, and whether there is one. It reads a value that
// normalizeNil accepted, so no chain of []any and map[string]any in it is deeper than
// maxDepth or contains itself; the depth test only keeps the walk bounded.
func findNonFinite(v any, path string, depth int) (string, string, bool) {
	if depth > maxDepth {
		return "", "", false
	}
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return path, strconv.FormatFloat(t, 'g', -1, 64), true
		}
	case float32:
		if f := float64(t); math.IsNaN(f) || math.IsInf(f, 0) {
			return path, strconv.FormatFloat(f, 'g', -1, 64), true
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			at := k
			if path != "" {
				at = path + "." + k
			}
			if p, n, found := findNonFinite(t[k], at, depth+1); found {
				return p, n, true
			}
		}
	case []any:
		for i, e := range t {
			if p, n, found := findNonFinite(e, path+"["+strconv.Itoa(i)+"]", depth+1); found {
				return p, n, true
			}
		}
	}
	return "", "", false
}

// emptied returns a copy of v in which a nil []any or nil map[string]any, at any
// depth through chains of those two types, is an empty one. Other values, an
// explicit nil and any other container type among them, are kept as they are.
func emptied(v any, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errTooDeep
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			c, err := emptied(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			c, err := emptied(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	return v, nil
}

// applyJSONMergePatch applies RFC 7386 JSON merge patch as a fallback for
// CRDs that lack Go struct tags.
func applyJSONMergePatch(resource *unstructured.Unstructured, patch map[string]any) error {
	// Both inputs go through JSON, which has no NaN and no infinity: name the number
	// before the marshal fails without a path.
	if err := refuseNonFinite(resource.Object, patch); err != nil {
		return err
	}
	// A nil slice or map in the resource is an empty one, as on a known kind
	// (jsonTyped), not the null that json.Marshal would write. The resource is
	// only read here, so it is not copied unless it holds a nil container, and
	// resource.Object is not assigned until the merge has succeeded.
	normalized, err := normalizeNil(resource.Object)
	if err != nil {
		return errors.Wrap(err, "failed to normalize resource for JSON merge patch")
	}
	originalJSON, err := json.Marshal(normalized)
	if err != nil {
		return errors.Wrap(err, "failed to marshal resource for JSON merge patch")
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return errors.Wrap(err, "failed to marshal patch for JSON merge patch")
	}

	mergedJSON, err := jsonpatch.MergePatch(originalJSON, patchJSON)
	if err != nil {
		return errors.Wrap(err, "JSON merge patch failed")
	}

	// Numbers get the types jsonTyped gives them, so a whole number is an int64 here
	// as on a known kind, and an integer beyond 2^53 keeps its digits.
	merged, err := decodeTyped(mergedJSON)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal merged result")
	}

	resource.Object = merged
	return nil
}
