package builtin

import (
	"bytes"
	"cmp"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

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
// the wrong type. Numbers in interface-typed fields decode as json.Number, exactly.
//
// Decoding is not defaulting and not validating: T comes back exactly as authored.
// Known gap: encoding/json does not apply DisallowUnknownFields inside a type with
// its own UnmarshalJSON, so unknown keys nested in such a field are still dropped.
// Key matching is case-insensitive, as in encoding/json, and so is the owned split.
func DecodeStrictJSON[T any](src map[string]any, owned ...string) (*T, map[string]any, error) {
	rest := make(map[string]any, len(src))
	split := make(map[string]any)
	for k, v := range src {
		if isOwned(owned, k) {
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
	dec.UseNumber()
	var out T
	if err := dec.Decode(&out); err != nil {
		return nil, nil, err
	}
	return &out, split, nil
}

func isOwned(owned []string, key string) bool {
	return slices.ContainsFunc(owned, func(o string) bool { return strings.EqualFold(o, key) })
}

// UnreachableJSONFields reports the json keys of t (a struct, or a pointer to one),
// embedded ones included, that an author cannot use through DecodeStrictJSON called
// with the same owned keys: a field tagged `json:"-"` (by Go name), a key encoding/json
// itself refuses (an ambiguous promotion), a key an owned key shadows, and a key whose
// field the decoder cannot set (behind an unexported embedded pointer). Each key is
// probed against encoding/json, so the check cannot disagree with the decoder.
// Known limit: the check is per key, not per field; a field hidden behind another
// field under the same key (a shallower or case-insensitively equal one) is not reported.
//
// A terminal that decodes an external spec type asserts this is empty against an
// explicit exclusion list, so an upstream field added under a name launcher already
// owns turns the test red instead of silently becoming unreachable.
func UnreachableJSONFields(t reflect.Type, owned ...string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	keys := map[string]bool{}
	collectJSONKeys(t, nil, keys, &out)
	for key := range keys {
		if isOwned(owned, key) || !decodesKey(t, key) {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// collectJSONKeys gathers the json key of every field of t and of the structs it
// embeds. path holds only the current branch's embeddings, so a type that embeds
// itself stops at the repeat while one embedded twice is walked on both branches.
func collectJSONKeys(t reflect.Type, path []reflect.Type, keys map[string]bool, hidden *[]string) {
	if slices.Contains(path, t) {
		return
	}
	path = append(path, t)
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			if f.IsExported() {
				*hidden = append(*hidden, f.Name)
			}
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if !validTagName(name) {
			name = "" // v1 encoding/json falls back to the Go name (and flattens an embed)
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		embedded := f.Anonymous && ft.Kind() == reflect.Struct
		switch {
		case embedded && name == "":
			collectJSONKeys(ft, path, keys, hidden)
		case f.IsExported() || embedded:
			keys[cmp.Or(name, f.Name)] = true
		}
	}
}

// validTagName mirrors encoding/json's isValidTag (encode.go).
func validTagName(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(c rune) bool {
		return !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c) && !unicode.IsLetter(c) && !unicode.IsDigit(c)
	})
}

// decodesKey reports whether a strict encoding/json decode into t accepts key and can
// set its field. A panic (a tagged unexported embedded pointer) counts as unreachable.
func decodesKey(t reflect.Type, key string) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	data, _ := json.Marshal(map[string]any{key: nil})
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(reflect.New(t).Interface())
	return err == nil || !strings.Contains(err.Error(), "unknown field "+strconv.Quote(key)) &&
		!strings.Contains(err.Error(), "cannot set embedded pointer to unexported struct")
}
