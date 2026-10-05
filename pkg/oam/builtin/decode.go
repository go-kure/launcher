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
//
// A panic inside the decode is returned as an error: see decodeJSONRecovering.
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
	if err := decodeJSONRecovering(dec, &out); err != nil {
		return nil, nil, err
	}
	return &out, split, nil
}

// decodeJSONRecovering is dec.Decode(out) with a panic turned into an error
// that names T and the panic's value. T is an external API type, and a type
// under it may decode itself with code that does not expect every value an
// author can write: Cilium's ICMPField.UnmarshalJSON dereferences a nil pointer
// when `type` is absent. An authored document must end as a build error, not as
// a crash, for that decoder and for the next one a dependency bump brings.
//
// Only the decode call is covered. No launcher type decoded through
// DecodeStrictJSON has an UnmarshalJSON of its own, so the panics this catches
// are encoding/json's and an external type's.
func decodeJSONRecovering[T any](dec *json.Decoder, out *T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("the decoder of %s panicked on this value: %v (a type under it decodes itself and does not handle what was written, a required field left out for one)", reflect.TypeFor[T](), r)
		}
	}()
	return dec.Decode(out)
}

func isOwned(owned []string, key string) bool {
	return slices.ContainsFunc(owned, func(o string) bool { return strings.EqualFold(o, key) })
}

// UnknownJSONFieldPath returns the path of the first key of src that
// DecodeStrictJSON[T] called with the same owned keys refuses as unknown: the
// keys from the root joined by ".", a list element as "[i]", for example
// sourceRef.tag or patches[0].target.kinds. encoding/json's own error names
// only the key, which reads as a different field when T declares the same key
// elsewhere. A caller that got an unknown-field error from the decode uses it
// to name where the key sits.
//
// It returns "" when it finds no such key. It walks src along T's fields and
// asks encoding/json itself whether each struct accepts each key, and descends
// only into the field the decoder fills from a key (jsonFieldType), so it does
// not call a key unknown that the decoder accepts. Where it cannot show which
// field that is, it does not descend. It does not look inside a value whose
// type decodes itself (UnmarshalJSON, UnmarshalText), where the decoder does
// not check keys either, nor at the elements past a fixed array's length,
// which the decoder drops. Keys are visited in sorted order at each level, the
// order DecodeStrictJSON's marshalled input has.
func UnknownJSONFieldPath[T any](src map[string]any, owned ...string) string {
	return UnknownJSONFieldPathIn[T](src, nil, owned...)
}

// SelfDecodedShapes maps a type that decodes itself (UnmarshalJSON) to its
// shape: the plain type its decoding reads the value into, whose fields are the
// keys it keeps. encoding/json does not check keys inside such a type, so
// DecodeStrictJSON drops an unknown one there; a caller that knows the shape
// checks it with UnknownJSONFieldPathIn. A shape does not decode itself.
type SelfDecodedShapes map[reflect.Type]reflect.Type

// UnknownJSONFieldPathIn is UnknownJSONFieldPath that also looks inside a value
// whose type decodes itself, for the types shapes names: such a value is walked
// as its shape, so a key the shape does not declare is reported by its path
// like any other. A type that decodes itself and is not in shapes is not looked
// into, as in UnknownJSONFieldPath.
//
// Called after DecodeStrictJSON[T] accepted the same src, it returns the path
// of a key that decode dropped inside one of those types, or "".
func UnknownJSONFieldPathIn[T any](src map[string]any, shapes SelfDecodedShapes, owned ...string) string {
	rest := make(map[string]any, len(src))
	for k, v := range src {
		if !isOwned(owned, k) {
			rest[k] = v
		}
	}
	data, err := json.Marshal(rest)
	if err != nil {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return ""
	}
	return unknownJSONFieldPath(reflect.TypeFor[T](), value, shapes)
}

// unknownJSONFieldPath is UnknownJSONFieldPathIn for one decoded JSON value and
// the type it decodes into. A panic from reflection over an unusual type (a
// field behind an unexported embedded pointer) counts as nothing found.
func unknownJSONFieldPath(t reflect.Type, value any, shapes SelfDecodedShapes) (path string) {
	defer func() {
		if recover() != nil {
			path = ""
		}
	}()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	pt := reflect.PointerTo(t)
	named := func(name string) bool {
		_, found := pt.MethodByName(name)
		return found
	}
	if pt.Implements(reflect.TypeFor[json.Unmarshaler]()) || pt.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) || named("UnmarshalJSONFrom") {
		shape, ok := shapes[t]
		if !ok || shape == t {
			return ""
		}
		return unknownJSONFieldPath(shape, value, shapes)
	}
	join := func(head, rest string) string {
		if strings.HasPrefix(rest, "[") {
			return head + rest
		}
		return head + "." + rest
	}
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if t.Kind() == reflect.Struct {
			var fields []jsonField
			var hidden []string
			collectJSONFields(t, nil, nil, "", &fields, &hidden)
			for _, k := range keys {
				if !decodesKey(t, k) {
					return k
				}
				if ft, ok := jsonFieldType(t, fields, k); ok {
					if sub := unknownJSONFieldPath(ft, v[k], shapes); sub != "" {
						return join(k, sub)
					}
				}
			}
		} else if t.Kind() == reflect.Map {
			for _, k := range keys {
				if sub := unknownJSONFieldPath(t.Elem(), v[k], shapes); sub != "" {
					return join(k, sub)
				}
			}
		}
	case []any:
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return ""
		}
		if t.Kind() == reflect.Array && len(v) > t.Len() {
			// The decoder drops the elements a fixed array has no room for.
			v = v[:t.Len()]
		}
		for i, item := range v {
			if sub := unknownJSONFieldPath(t.Elem(), item, shapes); sub != "" {
				return join("["+strconv.Itoa(i)+"]", sub)
			}
		}
	}
	return ""
}

// jsonFieldType returns the type of the field of t that encoding/json fills
// from key, selecting it the way the decoder does: the field keyed exactly
// so, else the first in declaration order whose key equals it ignoring case.
// Only a field encoding/json keeps counts. Of several fields with one key it
// keeps the dominant one (the shallowest, a tagged one before an untagged one)
// or none, and which is asked of encoding/json itself (encodesField). It
// reports false when that cannot be shown, so the caller walks no field the
// decoder may not fill.
func jsonFieldType(t reflect.Type, fields []jsonField, key string) (reflect.Type, bool) {
	// kept is the one field keyed k that encoding/json keeps. ok is false when
	// several fields are keyed k and none can be shown to be kept.
	kept := func(k string) (f jsonField, ok bool) {
		var same []jsonField
		for _, o := range fields {
			if o.key == k {
				same = append(same, o)
			}
		}
		if len(same) == 1 {
			return same[0], true
		}
		for _, o := range same {
			if encodesField(t, o.index) {
				return o, true
			}
		}
		return jsonField{}, false
	}

	var folded []string
	for _, f := range fields {
		if f.key == key {
			exact, ok := kept(key)
			if !ok {
				return nil, false
			}
			return t.FieldByIndex(exact.index).Type, true
		}
		if strings.EqualFold(f.key, key) && !slices.Contains(folded, f.key) {
			folded = append(folded, f.key)
		}
	}
	var first *jsonField
	for _, k := range folded {
		f, ok := kept(k)
		if !ok {
			return nil, false
		}
		if first == nil || slices.Compare(f.index, first.index) < 0 {
			first = &f
		}
	}
	if first == nil {
		return nil, false
	}
	return t.FieldByIndex(first.index).Type, true
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
// MarshalJSON, MarshalText, UnmarshalJSON or UnmarshalText (promoted ones included),
// or the AppendText, MarshalJSONTo or UnmarshalJSONFrom the jsonv2-backed encoding/json
// also calls, or of a t whose encoding fails or panics.
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

// The methods that take encoding or decoding away from encoding/json's field selection.
// AppendText is called only by the jsonv2-backed encoding/json (GOEXPERIMENT=jsonv2).
var selfCoding = []reflect.Type{
	reflect.TypeFor[json.Marshaler](),
	reflect.TypeFor[encoding.TextMarshaler](),
	reflect.TypeFor[encoding.TextAppender](),
	reflect.TypeFor[json.Unmarshaler](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

// The json/v2 methods the jsonv2-backed encoding/json also calls. Their jsontext
// signatures need the experiment to compile, so they are matched by name alone: a
// same-named method with another signature is reported too, which errs on the safe side.
var selfCodingByName = []string{"MarshalJSONTo", "UnmarshalJSONFrom"}

// encodesField reports whether encoding/json selects the field at index of t: filling
// that field alone on a fresh value must change t's encoding. It is false whenever
// that cannot be shown, including behind an unexported embedded pointer (which the
// decoder cannot set either), for a t that encodes or decodes itself (own or promoted
// method, json/v2's MarshalJSONTo and UnmarshalJSONFrom included), and when encoding
// fails or panics (a method reached through a nil embedded pointer).
func encodesField(t reflect.Type, index []int) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	pt := reflect.PointerTo(t)
	named := func(name string) bool {
		_, found := pt.MethodByName(name)
		return found
	}
	if slices.ContainsFunc(selfCoding, pt.Implements) || slices.ContainsFunc(selfCodingByName, named) {
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
// set its field. reflect panics when the decoder sets a tagged unexported embedded
// pointer, and that counts as unreachable. Any other panic is the decoder of the field's
// own type, which the key has reached, failing on the null the probe writes (a
// value-typed field whose UnmarshalJSON dereferences what a null leaves unset): the key
// is one the type declares.
func decodesKey(t reflect.Type, key string) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			s, _ := r.(string)
			ok = !strings.Contains(s, "using value obtained using unexported field")
		}
	}()
	data, _ := json.Marshal(map[string]any{key: nil})
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(reflect.New(t).Interface())
	return err == nil || !strings.Contains(err.Error(), "unknown field "+strconv.Quote(key)) &&
		!strings.Contains(err.Error(), "cannot set embedded pointer to unexported struct")
}
