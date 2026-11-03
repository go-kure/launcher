package patch

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
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

	return applyJSONMergePatch(resource, deepCopyMap(patch))
}

// applyTypedSMP applies a strategic merge patch using Go struct tags from the
// typed object. StrategicMergeMapPatch mutates its arguments, so it merges
// normalized copies of the resource map and the patch. On error the resource is
// left untouched.
func applyTypedSMP(resource *unstructured.Unstructured, patch map[string]any, typedObj any) error {
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
// A nil slice or map, at any depth, becomes an empty one, not null, which a merge
// patch reads as a deletion; an explicit nil value stays nil. A document that
// nests deeper than maxDepth, or contains itself, is an error. Normalizing twice
// changes nothing.
func jsonTyped(m map[string]any) (map[string]any, error) {
	m, err := normalizeNil(m)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

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

// maxDepth bounds how deep the nil scan and emptied walk, as encoding/json bounds the cycle
// check of json.Marshal: a document that nests deeper, or that contains
// itself, is an error, not a stack overflow.
const maxDepth = 1000

// normalizeNil returns m itself when it holds no nil []any or nil map[string]any,
// and otherwise a copy in which each is an empty one. The scan only reads, so a
// document without a nil container is not copied here. A nil m is returned as is.
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
// any depth. A document nested deeper than maxDepth, or containing itself, is an
// error.
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

// emptied returns a copy of v in which a nil []any or nil map[string]any, at any
// depth, is an empty one. Other values, an explicit nil among them, are kept.
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
	originalJSON, err := json.Marshal(resource.Object)
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

	var merged map[string]any
	if err := json.Unmarshal(mergedJSON, &merged); err != nil {
		return errors.Wrap(err, "failed to unmarshal merged result")
	}

	resource.Object = merged
	return nil
}

// deepCopyMap creates a deep copy of a map[string]interface{} to prevent
// StrategicMergeMapPatch from mutating the original patch data.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = deepCopyValue(v)
	}
	return result
}

func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		copied := make([]any, len(val))
		for i, item := range val {
			copied[i] = deepCopyValue(item)
		}
		return copied
	default:
		return v
	}
}
