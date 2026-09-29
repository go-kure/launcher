package builtin

import (
	"bytes"
	"cmp"
	"encoding"
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

// UnreachableJSONFields reports the fields of t (a struct, or a pointer to one),
// embedded ones included, that an author cannot set through DecodeStrictJSON called
// with the same owned keys.
//
// A field whose key fails is reported by that key: tagged `json:"-"` (by Go name),
// refused by encoding/json itself (an ambiguous promotion), shadowed by an owned key,
// or one the decoder cannot set (behind an unexported embedded pointer). Each key is
// probed against encoding/json, so this cannot disagree with the decoder.
//
// A field whose key works but lands on another field is reported by its Go field
// path (Inner.Value): one a shallower field dominates, or one dropped as ambiguous
// whose key then folds onto a field equal ignoring case. Only a field whose key
// matches another's ignoring case is checked: it is filled alone on a fresh value, and
// it is reachable when that changes the encoding, since encoding/json selects fields
// the same way to encode and decode. What this cannot prove reachable is reported
// too: a value that encodes like its zero, or any such field of a t with its own
// MarshalJSON or MarshalText.
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
	var fields []jsonField
	collectJSONFields(t, nil, nil, "", &fields, &out)
	refused := map[string]bool{}
	for _, f := range fields {
		if _, seen := refused[f.key]; !seen {
			refused[f.key] = isOwned(owned, f.key) || !decodesKey(t, f.key)
			if refused[f.key] {
				out = append(out, f.key)
			}
		}
	}
	for _, f := range fields {
		if !refused[f.key] && hasRival(fields, f) && !encodesField(t, f.index) {
			out = append(out, f.path)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// jsonField is a field encoding/json keys by name: its key, its reflect index from
// the root, and its dotted Go path, the name it is reported by.
type jsonField struct {
	key   string
	index []int
	path  string
}

// collectJSONFields gathers every keyed field of t and of the structs it embeds.
// path holds only the current branch's embeddings, so a type that embeds itself
// stops at the repeat while one embedded twice is walked on both branches.
func collectJSONFields(t reflect.Type, path []reflect.Type, index []int, prefix string, fields *[]jsonField, hidden *[]string) {
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
		idx := append(slices.Clone(index), f.Index...)
		embedded := f.Anonymous && ft.Kind() == reflect.Struct
		switch {
		case embedded && name == "":
			collectJSONFields(ft, path, idx, prefix+f.Name+".", fields, hidden)
		case f.IsExported() || embedded:
			*fields = append(*fields, jsonField{key: cmp.Or(name, f.Name), index: idx, path: prefix + f.Name})
		}
	}
}

// hasRival reports whether another field's key equals f's ignoring case, the only
// way a key the decoder accepts can land on a field other than f.
func hasRival(fields []jsonField, f jsonField) bool {
	return slices.ContainsFunc(fields, func(o jsonField) bool {
		return o.path != f.path && strings.EqualFold(o.key, f.key)
	})
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

// encodesField reports whether encoding/json selects the field at index of t: filling
// that field alone on a fresh value must change t's encoding. It is false whenever
// that cannot be shown, including behind an unexported embedded pointer (which the
// decoder cannot set either) and for a t that encodes itself.
func encodesField(t reflect.Type, index []int) bool {
	if pt := reflect.PointerTo(t); pt.Implements(jsonMarshaler) || pt.Implements(textMarshaler) {
		return false
	}
	root := reflect.New(t)
	v := root.Elem()
	for _, i := range index[:len(index)-1] {
		v = v.Field(i)
		if v.Kind() == reflect.Pointer {
			if !v.CanSet() {
				return false
			}
			v.Set(reflect.New(v.Type().Elem()))
			v = v.Elem()
		}
	}
	before, err := json.Marshal(root.Interface())
	if err != nil || !fill(v.Field(index[len(index)-1])) {
		return false
	}
	after, err := json.Marshal(root.Interface())
	return err == nil && !bytes.Equal(before, after)
}

// fill sets v to a value other than its zero and reports whether it could. A struct
// (possibly reached through an unexported embed) gets every field it can fill;
// pointers, slices and maps get one zero element and are not descended into, so a
// type that refers to itself terminates.
func fill(v reflect.Value) bool {
	if k := v.Kind(); k != reflect.Struct && k != reflect.Array && !v.CanSet() {
		return false
	}
	switch v.Kind() {
	case reflect.Struct:
		filled := false
		for i := range v.NumField() {
			filled = fill(v.Field(i)) || filled
		}
		return filled
	case reflect.Array:
		return v.Len() > 0 && fill(v.Index(0))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.Zero(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		v.Set(m)
	case reflect.Interface:
		if v.NumMethod() != 0 {
			return false
		}
		v.Set(reflect.ValueOf("x"))
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	default:
		return false
	}
	return true
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
