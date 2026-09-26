package builtin

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// DecodeStrict decodes src into T using yaml.v3 KnownFields mode.
// Unknown keys in src produce an error — they indicate a rendering map that
// does not match the handler's declared schema.
//
// yaml.v3 keys on yaml tags, falling back to the lowercased Go field name, so this
// is only for launcher's own yaml-tagged schema types. For an external API type
// that carries json tags only (Flux, Cilium), use DecodeStrictJSON.
func DecodeStrict[T any](src map[string]any) (*T, error) {
	data, err := yaml.Marshal(src)
	if err != nil {
		return nil, errors.Wrap(err, "internal: marshal rendering")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var out T
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DecodeStrictJSON decodes an authored property map into T, an external API type
// keyed by json tags, and refuses any key T does not declare.
//
// The keys named in owned are launcher's own (a mode switch, a delivery choice)
// rather than fields of T: they are split off before decoding and returned in the
// second result, so they neither reach the strict decoder nor get dropped. An owned
// key absent from src is absent from that map. src itself is not modified.
//
// The rest is marshalled to JSON and decoded with DisallowUnknownFields, so a
// misspelt or unsupported key, at any depth, is an error naming it, as is a value of
// the wrong type.
//
// Decoding is not defaulting and not validating: T comes back exactly as authored.
// Known gap: encoding/json does not apply DisallowUnknownFields inside a type with
// its own UnmarshalJSON, so unknown keys nested in such a field are still dropped.
// Key matching is case-insensitive, as it always is in encoding/json.
func DecodeStrictJSON[T any](src map[string]any, owned ...string) (*T, map[string]any, error) {
	rest := make(map[string]any, len(src))
	split := make(map[string]any)
	for k, v := range src {
		if slices.Contains(owned, k) {
			split[k] = v
			continue
		}
		rest[k] = v
	}

	data, err := json.Marshal(rest)
	if err != nil {
		return nil, nil, errors.Wrap(err, "marshal properties")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var out T
	if err := dec.Decode(&out); err != nil {
		return nil, nil, err
	}
	return &out, split, nil
}

// UnreachableJSONFields reports the top-level fields of t (a struct, or a pointer to
// one) that an author cannot set through DecodeStrictJSON called with the same owned
// keys: a field tagged `json:"-"` (reported by its Go name) and a field whose json
// key an owned key shadows (reported by that key, compared case-insensitively like
// encoding/json does). Fields promoted from an embedded struct are included.
//
// A terminal that decodes an external spec type asserts this is empty against an
// explicit exclusion list, so an upstream field added under a name launcher already
// owns turns the test red instead of silently becoming unreachable.
func UnreachableJSONFields(t reflect.Type, owned ...string) []string {
	var out []string
	collectUnreachable(t, owned, &out)
	slices.Sort(out)
	return out
}

func collectUnreachable(t reflect.Type, owned []string, out *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			if f.IsExported() {
				*out = append(*out, f.Name)
			}
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectUnreachable(ft, owned, out)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if slices.ContainsFunc(owned, func(o string) bool { return strings.EqualFold(o, name) }) {
			*out = append(*out, name)
		}
	}
}
