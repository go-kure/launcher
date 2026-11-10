package builtin

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"fmt"
	"maps"
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
// The tag option case:strict is modelled: the jsonv2-backed decoder fills a field
// tagged so only from a key spelled exactly as its name, and the walk does the same.
// The v1 decoder reads no case option. The option case:ignore changes nothing, because
// matching already ignores case. No type read here carries either option.
// Two keys that encoding/json files under one field of T, such as url and URL, are
// refused with a *FoldedFieldError (FoldedDuplicateFieldPath): the decoder would
// keep one of the two values and drop the other without a message.
// A key given twice in one JSON object, at any depth, is refused too: a map cannot
// hold one twice, but a value that serializes itself, such as a json.RawMessage, can.
// The decoder merges the occurrences of an object member and keeps the last of a
// scalar, so one value would be dropped without a message (go-kure/launcher#1361).
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
	// After the decode, so an unknown key or a wrong type is reported first.
	// Before the fold walk: that walk reads the tree decoded into any, which keeps
	// only the last occurrence of a repeated key, so a repeat is refused first.
	path, found, err := repeatedJSONKey(data)
	if err != nil {
		return nil, nil, errors.Wrap(err, "read properties")
	}
	if found {
		return nil, nil, errors.Errorf("%s is given more than once in the same object; give each key once", path)
	}
	if later, earlier := foldedDuplicateInJSON[T](data); later != "" {
		return nil, nil, SameFieldError(later, earlier)
	}
	return &out, split, nil
}

// repeatedJSONKey returns the path of the first key that appears twice in one
// object of data, at any depth, and whether there is one. Paths are written as in
// FoldedFieldError. found is its own result because the path of a repeated empty
// key can be "" too, so the path cannot tell a repeat from none.
// The walk reads every value, including one inside an interface, a json.RawMessage
// or a type that decodes itself: a self-serializing value is the only way a repeat
// reaches data. It compares the names json.Decoder.Token returns, which are decoded,
// so a key written with an escape is the same key.
func repeatedJSONKey(data []byte) (path string, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var stack []repeatSegment
	found, err = repeatedJSONKeyIn(dec, &stack)
	if err != nil || !found {
		return "", false, err
	}
	return joinRepeatSegments(stack), true, nil
}

// repeatSegment is one step of the path to a value: an object key or a list index.
// The walk keeps the steps on one stack and writes the path text only for a repeat,
// so a deep value costs one entry per level, never a copy of the whole path.
type repeatSegment struct {
	key   string
	index int
	isIdx bool
}

// joinRepeatSegments writes the path as FoldedFieldError does: keys joined by ".",
// an index as "[n]" after its parent. An empty key is a segment like any other, so
// an empty first key leaves ".x" for a repeat of x under it.
func joinRepeatSegments(stack []repeatSegment) string {
	var b strings.Builder
	for i, s := range stack {
		switch {
		case s.isIdx:
			b.WriteString("[" + strconv.Itoa(s.index) + "]")
		case i > 0:
			b.WriteString("." + s.key)
		default:
			b.WriteString(s.key)
		}
	}
	return b.String()
}

// repeatedJSONKeyIn walks the next value of dec. On a repeat it returns true with
// *stack holding the path of the repeated key.
func repeatedJSONKeyIn(dec *json.Decoder, stack *[]repeatSegment) (bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return false, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return false, nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return false, err
			}
			key, _ := keyTok.(string)
			*stack = append(*stack, repeatSegment{key: key})
			if seen[key] {
				return true, nil
			}
			seen[key] = true
			if found, err := repeatedJSONKeyIn(dec, stack); err != nil || found {
				return found, err
			}
			*stack = (*stack)[:len(*stack)-1]
		}
	case '[':
		for i := 0; dec.More(); i++ {
			*stack = append(*stack, repeatSegment{index: i, isIdx: true})
			if found, err := repeatedJSONKeyIn(dec, stack); err != nil || found {
				return found, err
			}
			*stack = (*stack)[:len(*stack)-1]
		}
	}
	// The closing delimiter.
	_, err = dec.Token()
	return false, err
}

// decodeJSONRecovering is dec.Decode(out) with a panic turned into an error
// that names T and the panic's value. T is an external API type, and a type
// under it may decode itself with code that does not expect every value an
// author can write: Cilium's ICMPField.UnmarshalJSON dereferences a nil pointer
// when `type` is absent. An authored document must end as a build error, not as
// a crash, for that decoder and for the next one a dependency bump brings.
//
// Only the decode call is covered, and with it every UnmarshalJSON the decode
// reaches: encoding/json's, an external type's, and a launcher type's own (the
// helm rule's source.ref decodes itself).
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
// of a key that decode dropped inside one of those types, or "". A key given
// twice in one object is DecodeStrictJSON's to refuse, before this walk: the
// walk alone reads the last occurrence.
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
	if decodesItself(t) {
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
			fields := decoderFields(t)
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

// decodesItself reports whether encoding/json hands the value of a t to t's own
// code (UnmarshalJSON, UnmarshalText, or the json/v2 UnmarshalJSONFrom), which
// then checks no key.
func decodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	_, from := pt.MethodByName("UnmarshalJSONFrom")
	return pt.Implements(reflect.TypeFor[json.Unmarshaler]()) || pt.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) || from
}

// FoldedFieldError reports two keys of one authored object that encoding/json
// files under one field of the decoded type, which it matches ignoring case, so
// that one of the two values would be dropped. Later is the path of the key
// that sorts after the other, Earlier the path of the other: dots between keys
// and "[i]" for a list element, from the root of the decoded map.
//
// Its text is the one sentence every refusal of two spellings uses
// (SameFieldError). A caller that must show that sentence alone finds the error
// with errors.As and returns it unwrapped.
type FoldedFieldError struct {
	Later, Earlier string
}

// Error returns the sentence naming both paths.
func (e *FoldedFieldError) Error() string {
	return e.Later + ": sets the same field as " + e.Earlier + " (field names match case-insensitively, so one value would be dropped)"
}

// SameFieldError returns the *FoldedFieldError for the two paths. Every site
// that refuses two spellings of one field builds the error here, so the text
// has one source.
func SameFieldError(later, earlier string) error {
	return &FoldedFieldError{Later: later, Earlier: earlier}
}

// FoldedDuplicateFieldPath returns the paths of the first two keys of src that
// DecodeStrictJSON[T] called with the same owned keys files under one field of
// T, the later key first, or two empty strings when there is no such pair. It
// is the walk DecodeStrictJSON runs after a decode that succeeded.
//
// Keys are visited in sorted order at each level, and a pair is reported when
// its second key is reached, before the walk descends into that key's value.
// Two keys form a pair only when encoding/json would fill the same field from
// both (jsonFieldFor): two keys that name two fields of T, such as an exact Foo
// and an exact foo, are no pair. A key authored as null never forms a pair,
// because the package's null contract reads it as absent. The keys of a map
// are data and are not compared, and the walk does not look inside a type that
// decodes itself, an interface value or json.RawMessage, where the decoder
// checks no key either. Where it cannot show which field a key fills, it
// reports nothing. A key given twice in one object is DecodeStrictJSON's to
// refuse, before this walk: the walk alone reads the last occurrence.
func FoldedDuplicateFieldPath[T any](src map[string]any, owned ...string) (later, earlier string) {
	rest := make(map[string]any, len(src))
	for k, v := range src {
		if !isOwned(owned, k) {
			rest[k] = v
		}
	}
	data, err := json.Marshal(rest)
	if err != nil {
		return "", ""
	}
	return foldedDuplicateInJSON[T](data)
}

// foldedDuplicateInJSON is FoldedDuplicateFieldPath for the JSON text of an
// authored map with its owned keys already split off.
func foldedDuplicateInJSON[T any](data []byte) (later, earlier string) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", ""
	}
	return foldedDuplicatePath(reflect.TypeFor[T](), value, "")
}

// foldedDuplicatePath is FoldedDuplicateFieldPath for one decoded JSON value,
// the type it decodes into and the path it sits at. A panic from reflection
// over an unusual type counts as nothing found.
func foldedDuplicatePath(t reflect.Type, value any, path string) (later, earlier string) {
	defer func() {
		if recover() != nil {
			later, earlier = "", ""
		}
	}()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if decodesItself(t) {
		return "", ""
	}
	child := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	switch v := value.(type) {
	case map[string]any:
		keys := slices.Sorted(maps.Keys(v))
		switch t.Kind() {
		case reflect.Struct:
			fields := decoderFields(t)
			// filled maps the index of a field to the key that filled it first.
			filled := map[string]string{}
			for _, k := range keys {
				if v[k] == nil {
					continue
				}
				f, ok := jsonFieldFor(fields, k)
				if !ok {
					continue
				}
				id := fmt.Sprint(f.index)
				if prev, dup := filled[id]; dup {
					return child(k), child(prev)
				}
				filled[id] = k
				if l, e := foldedDuplicatePath(t.FieldByIndex(f.index).Type, v[k], child(k)); l != "" {
					return l, e
				}
			}
		case reflect.Map:
			for _, k := range keys {
				if l, e := foldedDuplicatePath(t.Elem(), v[k], child(k)); l != "" {
					return l, e
				}
			}
		default:
			// Any other kind has no keys to compare.
		}
	case []any:
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return "", ""
		}
		if t.Kind() == reflect.Array && len(v) > t.Len() {
			// The decoder drops the elements a fixed array has no room for.
			v = v[:t.Len()]
		}
		for i, item := range v {
			if l, e := foldedDuplicatePath(t.Elem(), item, path+"["+strconv.Itoa(i)+"]"); l != "" {
				return l, e
			}
		}
	}
	return "", ""
}

// jsonFieldType returns the type of the field of t that encoding/json fills
// from key, selecting it the way the decoder does (jsonFieldFor). It reports
// false when no field keeps that key.
func jsonFieldType(t reflect.Type, fields []jsonField, key string) (reflect.Type, bool) {
	f, ok := jsonFieldFor(fields, key)
	if !ok {
		return nil, false
	}
	return t.FieldByIndex(f.index).Type, true
}

// jsonFieldFor is jsonFieldType that returns the field itself, with its index
// from the root of t, so a caller can tell whether two keys land on one field.
//
// It selects the field the way encoding/json's typeFields does, and by the same
// structure alone: the field a name keeps (keptJSONField) when a field is keyed
// exactly so, else, among the fields kept under the other names that equal the
// key ignoring case, the first by foldedBefore. That order follows the
// encoding/json the package is built with, which differs between the jsonv2-backed
// decoder (shallowest first) and the v1 decoder (index order). A name that no
// field keeps is skipped, so a key falls to the next name that matches it
// ignoring case. So is a field the decoder matches only by its exact name
// (strictCase): a key that differs from it in case never reaches it.
func jsonFieldFor(fields []jsonField, key string) (jsonField, bool) {
	if f, ok := keptJSONField(fields, key); ok {
		return f, true
	}
	var first *jsonField
	for _, o := range fields {
		if o.key == key || !strings.EqualFold(o.key, key) {
			continue
		}
		f, ok := keptJSONField(fields, o.key)
		if !ok || f.strict {
			continue
		}
		if first == nil || foldedBefore(f.index, first.index) {
			first = &f
		}
	}
	if first == nil {
		return jsonField{}, false
	}
	return *first, true
}

// keptJSONField returns the one field encoding/json keeps among those keyed
// exactly name, or false when it keeps none. It mirrors typeFields and
// dominantField: the field at the least depth wins; at that depth a field whose
// key comes from a json tag beats the untagged ones; and when more than one
// field is left at that depth and tag state, encoding/json drops the name.
// Nothing here depends on the field's type or on a method it has.
func keptJSONField(fields []jsonField, name string) (jsonField, bool) {
	var shallow []jsonField
	for _, f := range fields {
		if f.key != name {
			continue
		}
		switch {
		case len(shallow) == 0 || len(f.index) < len(shallow[0].index):
			shallow = []jsonField{f}
		case len(f.index) == len(shallow[0].index):
			shallow = append(shallow, f)
		}
	}
	if len(shallow) == 0 {
		return jsonField{}, false
	}
	var tagged []jsonField
	for _, f := range shallow {
		if f.tagged {
			tagged = append(tagged, f)
		}
	}
	if len(tagged) > 0 {
		shallow = tagged
	}
	if len(shallow) != 1 {
		return jsonField{}, false
	}
	return shallow[0], true
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
// the root, its dotted Go path, the name it is reported by, whether its key
// comes from a json tag, and whether the decoder matches it only by its exact
// name (strictCase).
type jsonField struct {
	key    string
	index  []int
	path   string
	tagged bool
	strict bool
}

// embeddedStruct is a struct embedded without a name, which encoding/json
// explores for the fields it promotes: its type, its index from the root, and the
// Go path that reports its fields.
type embeddedStruct struct {
	typ    reflect.Type
	index  []int
	prefix string
}

// scanStruct splits the fields of the struct type t, which sits at index from the
// root, into the keyed fields it declares and the embedded structs to explore
// next. Both drivers below use it, so they read a field the same way. hidden, when
// not nil, collects the exported fields tagged `json:"-"`.
func scanStruct(t reflect.Type, index []int, prefix string, hidden *[]string) (own []jsonField, embeds []embeddedStruct) {
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			if f.IsExported() && hidden != nil {
				*hidden = append(*hidden, f.Name)
			}
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
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
			embeds = append(embeds, embeddedStruct{typ: ft, index: idx, prefix: prefix + f.Name + "."})
		case f.IsExported() || embedded:
			own = append(own, jsonField{key: cmp.Or(name, f.Name), index: idx, path: prefix + f.Name, tagged: name != "", strict: strictCase(options)})
		}
	}
	return own, embeds
}

// collectJSONFields gathers every keyed field of t and of the structs it embeds,
// once for every way to reach it. path holds only the current branch's
// embeddings, so a type that embeds itself stops at the repeat while one embedded
// twice is walked on both branches. That is more than encoding/json explores, and
// it is what UnreachableJSONFields wants: it probes the decoder for each copy and
// names the one the decoder never reaches. A walk that must select the field a key
// fills uses decoderFields.
func collectJSONFields(t reflect.Type, path []reflect.Type, index []int, prefix string, fields *[]jsonField, hidden *[]string) {
	if slices.Contains(path, t) {
		return
	}
	path = append(path, t)
	own, embeds := scanStruct(t, index, prefix, hidden)
	*fields = append(*fields, own...)
	for _, e := range embeds {
		collectJSONFields(e.typ, path, e.index, e.prefix, fields, hidden)
	}
}

// decoderFields gathers the keyed fields of the struct type t as encoding/json
// discovers them, which both decoders do the same way: level by level, the
// structs t embeds first, then the ones those embed. An embedded type is explored
// once per level however many structs embed it, and never again at a deeper level
// (typeFields' visited and nextCount in the v1 decoder; the seen set of
// makeStructFields in json/v2). The keyed fields of a type that was reached more
// than once at its level are listed twice, so that keptJSONField sees the
// duplicate and drops the name, as the decoders do (the "multiple instances" copy
// in typeFields, the repeated queue entry in json/v2). A type that embeds itself
// stops at the repeat.
func decoderFields(t reflect.Type) []jsonField {
	var fields []jsonField
	visited := map[reflect.Type]bool{}
	reached := map[reflect.Type]int{t: 1}
	current := []embeddedStruct{{typ: t}}
	for len(current) > 0 {
		var next []embeddedStruct
		nextReached := map[reflect.Type]int{}
		for _, e := range current {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			own, embeds := scanStruct(e.typ, e.index, e.prefix, nil)
			for _, f := range own {
				fields = append(fields, f)
				if reached[e.typ] > 1 {
					fields = append(fields, f)
				}
			}
			for _, em := range embeds {
				nextReached[em.typ]++
				if nextReached[em.typ] == 1 {
					next = append(next, em)
				}
			}
		}
		current, reached = next, nextReached
	}
	return fields
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
