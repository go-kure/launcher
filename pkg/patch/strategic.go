package patch

import (
	"bytes"
	"encoding/json"
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
	patchCopy := deepCopyMap(patch)

	if lookup != nil {
		typedObj, ok := lookup.LookupKind(gvk)
		if ok {
			return applyTypedSMP(resource, patchCopy, typedObj)
		}
	}

	return applyJSONMergePatch(resource, patchCopy)
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
// the int64 range, which a float64 would round. Normalizing twice changes nothing.
func jsonTyped(m map[string]any) (map[string]any, error) {
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
		f, err := t.Float64()
		if err != nil {
			return t
		}
		// A whole float encodes as digits. Give it the type those digits decode to,
		// an int64 or, beyond that range, a json.Number, so that normalizing the
		// result again changes nothing.
		if enc, err := json.Marshal(f); err == nil {
			n := json.Number(enc)
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
