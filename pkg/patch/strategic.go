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
// typed object. StrategicMergeMapPatch mutates its arguments, so we deep-copy
// the resource map before passing it in. On error the resource is left untouched.
func applyTypedSMP(resource *unstructured.Unstructured, patch map[string]any, typedObj any) error {
	original := deepCopyMap(resource.Object)

	merged, err := strategicpatch.StrategicMergeMapPatch(original, patch, typedObj)
	if err != nil {
		return errors.Wrap(err, "strategic merge patch failed")
	}

	// The merged map keeps the Go types of its inputs, such as a plain int, which
	// DeepCopy refuses. Give it the types of a decoded JSON document first.
	typed, err := jsonTyped(merged)
	if err != nil {
		return errors.Wrap(err, "failed to normalize strategic merge patch result")
	}

	resource.Object = typed
	return nil
}

// jsonTyped returns m as the JSON document it serializes to, decoded into the
// types an unstructured object may hold: map[string]any, []any, string, bool,
// nil and numbers. A number is an int64 where the integer fits, a float64 for a
// fraction or an exponent, and a json.Number, with its own digits, for an
// integer beyond the int64 range, which a float64 would round.
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
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t
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
