package builtin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// foldItem is a plain struct the walker descends into.
type foldItem struct {
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

// foldSelfDecoded keeps the raw bytes of its value, as a type with its own
// UnmarshalJSON does. encoding/json checks no key inside it. It declares an
// exported field, so a walk that wrongly looked inside would match the keys x
// and X to it and find a pair.
type foldSelfDecoded struct {
	raw string
	X   string `json:"x"`
}

func (s *foldSelfDecoded) UnmarshalJSON(b []byte) error {
	s.raw = string(b)
	return nil
}

// foldEmbedded is embedded below, so its field is promoted to the root.
type foldEmbedded struct {
	Inherited string `json:"inherited"`
}

// foldShadowed holds a field the outer type declares again at depth 0. Its
// type is a string, the outer one's a struct, so a walk that picks this field
// finds nothing inside it.
type foldShadowed struct {
	Label string `json:"label"`
}

// foldTarget has one field of each shape the walker meets.
type foldTarget struct {
	foldEmbedded
	foldShadowed
	URL    string                `json:"url"`
	Label  foldItem              `json:"label"`
	Ref    *foldItem             `json:"ref"`
	Items  []foldItem            `json:"items"`
	ByName map[string]foldItem   `json:"byName"`
	Labels map[string]string     `json:"labels"`
	Any    any                   `json:"any"`
	Raw    json.RawMessage       `json:"raw"`
	Self   foldSelfDecoded       `json:"self"`
	Fixed  [2]foldItem           `json:"fixed"`
	Upper  string                `json:"Foo"`
	Lower  string                `json:"foo"`
	Nested map[string][]foldItem `json:"nested"`
}

// foldSuspendTarget, foldKindTarget and foldAsTarget each declare one field.
// A non-ASCII spelling reaches it by simple case folding: the Kelvin sign
// (U+212A) folds to k, and the long s (U+017F) folds to s.
type foldSuspendTarget struct {
	Suspend bool `json:"suspend"`
}

type foldKindTarget struct {
	Kind string `json:"kind"`
}

type foldAsTarget struct {
	As string `json:"as"`
}

// foldSentence is the text every refusal of two spellings of one field carries.
func foldSentence(later, earlier string) string {
	return later + ": sets the same field as " + earlier + " (field names match case-insensitively, so one value would be dropped)"
}

// pair is an authored object with the key spelled two ways, in a given case.
func pair(a, b string, av, bv any) map[string]any {
	return map[string]any{a: av, b: bv}
}

// TestFoldedDuplicateFieldPath: the walk names the two keys of one object that
// encoding/json files under one field, the later key first. Keys sort in byte
// order, so an ASCII upper-case letter comes before its lower-case twin and the
// lower-case key is later. A non-ASCII character comes after every ASCII one.
func TestFoldedDuplicateFieldPath(t *testing.T) {
	item := func() map[string]any { return pair("name", "Name", "a", "b") }
	cases := []struct {
		name           string
		src            map[string]any
		owned          []string
		later, earlier string
	}{
		{name: "leaf at the root", src: pair("url", "URL", "a", "b"), later: "url", earlier: "URL"},
		{name: "leaf below a pointer to a struct", src: map[string]any{"ref": item()}, later: "ref.name", earlier: "ref.Name"},
		{name: "intermediate key spelled twice", src: pair("ref", "Ref", map[string]any{"name": "a"}, map[string]any{"name": "b"}), later: "ref", earlier: "Ref"},
		{name: "list element", src: map[string]any{"items": []any{map[string]any{"name": "ok"}, item()}}, later: "items[1].name", earlier: "items[1].Name"},
		{name: "map value that is a struct", src: map[string]any{"byName": map[string]any{"a": item()}}, later: "byName.a.name", earlier: "byName.a.Name"},
		{name: "list inside a map", src: map[string]any{"nested": map[string]any{"k": []any{item()}}}, later: "nested.k[0].name", earlier: "nested.k[0].Name"},
		{name: "promoted field of an embedded struct", src: pair("inherited", "Inherited", "a", "b"), later: "inherited", earlier: "Inherited"},
		{name: "fixed array, element in range", src: map[string]any{"fixed": []any{item()}}, later: "fixed[0].name", earlier: "fixed[0].Name"},
		{name: "a field declared again at depth 0, the shallow one is walked", src: map[string]any{"label": item()}, later: "label.name", earlier: "label.Name"},

		{name: "one spelling", src: map[string]any{"url": "a", "ref": map[string]any{"name": "a"}}},
		{name: "free-key map is data", src: map[string]any{"labels": pair("app", "App", "a", "b")}},
		{name: "keys of a struct-valued map are data", src: map[string]any{"byName": pair("a", "A", map[string]any{}, map[string]any{})}},
		{name: "interface value is not walked", src: map[string]any{"any": pair("x", "X", 1, 2)}},
		{name: "raw message is not walked", src: map[string]any{"raw": pair("x", "X", 1, 2)}},
		{name: "self-decoding type is not walked", src: map[string]any{"self": pair("x", "X", 1, 2)}},
		{name: "fixed array, element past its length", src: map[string]any{"fixed": []any{map[string]any{}, map[string]any{}, item()}}},
		{name: "null spelling beside a set one (null first)", src: pair("URL", "url", nil, "b")},
		{name: "null spelling beside a set one (null last)", src: pair("URL", "url", "a", nil)},
		{name: "two nulls", src: pair("URL", "url", nil, nil)},
		{name: "two keys that name two declared fields", src: pair("Foo", "foo", "a", "b")},
		{name: "owned keys are split off first", src: pair("url", "URL", "a", "b"), owned: []string{"url"}},
		{name: "owned keys are split off whatever their case", src: map[string]any{"url": "a", "Url": "b", "URL": "c"}, owned: []string{"url"}},

		{
			name:    "the pair under the earlier key comes first",
			src:     pair("Ref", "ref", item(), map[string]any{"name": "x"}),
			later:   "Ref.name",
			earlier: "Ref.Name",
		},
		{
			name:    "a pair of keys comes before a pair below the later one",
			src:     pair("Ref", "ref", map[string]any{"name": "x"}, item()),
			later:   "ref",
			earlier: "Ref",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			later, earlier := builtin.FoldedDuplicateFieldPath[foldTarget](tc.src, tc.owned...)
			if later != tc.later || earlier != tc.earlier {
				t.Errorf("FoldedDuplicateFieldPath = (%q, %q), want (%q, %q)", later, earlier, tc.later, tc.earlier)
			}
		})
	}
}

// TestFoldedDuplicateFieldPath_ByteOrder pins the order the documentation
// states: keys compare in byte order, at the first byte where they differ. The
// rows pass before and after the documentation change, because the code was
// right and only the sentence was wrong. They pin the sentence and kill no
// mutant.
func TestFoldedDuplicateFieldPath_ByteOrder(t *testing.T) {
	cases := []struct {
		name           string
		later, earlier string
		run            func() (string, string)
	}{
		{
			name: "ASCII letters: upper case first", later: "suspend", earlier: "Suspend",
			run: func() (string, string) {
				return builtin.FoldedDuplicateFieldPath[foldSuspendTarget](pair("suspend", "Suspend", "a", "b"))
			},
		},
		{
			name: "the Kelvin sign sorts after k", later: "\U0000212Aind", earlier: "kind",
			run: func() (string, string) {
				return builtin.FoldedDuplicateFieldPath[foldKindTarget](pair("kind", "\U0000212Aind", "a", "b"))
			},
		},
		{
			name: "an ASCII difference before a non-ASCII one decides", later: "as", earlier: "A\U0000017F",
			run: func() (string, string) {
				return builtin.FoldedDuplicateFieldPath[foldAsTarget](pair("A\U0000017F", "as", "a", "b"))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			later, earlier := tc.run()
			if later != tc.later || earlier != tc.earlier {
				t.Errorf("FoldedDuplicateFieldPath = (%q, %q), want (%q, %q)", later, earlier, tc.later, tc.earlier)
			}
		})
	}
}

// Fixtures for the selection of the field a key fills. encoding/json keeps one
// field per name (the shallowest, a tagged one before an untagged one, none when
// that leaves two). A key that matches no kept name exactly goes to a kept field
// whose name equals it ignoring case. Which one comes first differs between the
// decoders: the jsonv2-backed one (the default on the Go version go.mod names) goes breadth
// first, the v1 one by index path. A case that depends on that order asks the
// decoder which field a single key fills and builds its expectation from the
// answer, so it holds on both. Each fixture is decoded by encoding/json in the
// test, so the expected result of the walk rests on the decoder and not on this
// file's reading of it.

// foldIndexTarget: Upper has the index [0 0] and Lower [1]. A key FoO matches
// both names ignoring case. The decoder in use decides which one it fills.
type foldIndexInner struct {
	Upper string `json:"FOO"`
}

type foldIndexTarget struct {
	foldIndexInner
	Lower string `json:"foo"`
}

// foldAmbTarget: X (and Xy) is declared by two embedded structs at one depth,
// so the decoder drops that name and a key x or X (xy or XY) fills the outer
// field Lower (Lower2).
type foldAmbA struct{ X, Xy string }

type foldAmbB struct{ X, Xy string }

type foldAmbTarget struct {
	foldAmbA
	foldAmbB
	Lower  string `json:"x"`
	Lower2 string `json:"xy"`
}

// foldTagTarget: Other (tag Val) and Val (no tag) are at one depth, so the
// tagged Other is kept and the untagged Val never fills.
type foldTagA struct {
	Other string `json:"Val"`
}

type foldTagB struct{ Val string }

type foldTagTarget struct {
	foldTagA
	foldTagB
}

// foldDeepTarget: Item is declared at depth 0 and again at depth 1, so the
// shallow one is kept and the name is not dropped.
type foldDeepA struct{ Item string }

type foldDeepTarget struct {
	foldDeepA
	Item string
}

// foldRepTarget embeds foldRepShared twice, through foldRepLeft and foldRepRight.
// Both decoders explore an embedded type once and reach foldRepLeaf only through
// the first embedding, so the key value fills foldRepLeft.foldRepShared.foldRepLeaf.Value
// and is not ambiguous. foldRepOnce embeds foldRepShared once.
type (
	foldRepLeaf struct {
		Value string `json:"value"`
	}
	foldRepShared struct{ foldRepLeaf }
	foldRepLeft   struct{ foldRepShared }
	foldRepRight  struct{ foldRepShared }
	foldRepTarget struct {
		foldRepLeft
		foldRepRight
	}
	foldRepOnce struct{ foldRepLeft }
)

// decodeLikeTheDecoder decodes src into a T with encoding/json, the keys in the
// order json.Marshal gives a map, as DecodeStrictJSON feeds them.
func decodeLikeTheDecoder[T any](t *testing.T, src map[string]any) T {
	t.Helper()
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal %v: %v", src, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("encoding/json refused %s: %v", data, err)
	}
	return out
}

// wantWalk fails the test when the walk of src over T is not (later, earlier).
func wantWalk[T any](t *testing.T, src map[string]any, later, earlier string) {
	t.Helper()
	gl, ge := builtin.FoldedDuplicateFieldPath[T](src)
	if gl != later || ge != earlier {
		t.Errorf("walk = (%q, %q), want (%q, %q)", gl, ge, later, earlier)
	}
}

// TestFoldedDuplicateFieldPath_FieldSelection: the walk files a key under the
// field encoding/json fills, so it finds a pair exactly when the decoder drops
// a value. Each case first states what the decoder did, then what the walk found.
func TestFoldedDuplicateFieldPath_FieldSelection(t *testing.T) {
	// foldsOntoUpper: the decoder in use files a key FoO under the promoted
	// field Upper (index order) and not under the shallower Lower.
	foldsOntoUpper := decodeLikeTheDecoder[foldIndexTarget](t, map[string]any{"FoO": "a"}).Upper == "a"

	t.Run("a key that folds onto the field the decoder prefers, and an exact key for the other", func(t *testing.T) {
		src := pair("FoO", "foo", "a", "b")
		got := decodeLikeTheDecoder[foldIndexTarget](t, src)
		if foldsOntoUpper {
			// FoO fills Upper, foo fills Lower: two fields, no pair.
			if got.Upper != "a" || got.Lower != "b" {
				t.Fatalf("premise: the decoder filled Upper %q and Lower %q, want a and b", got.Upper, got.Lower)
			}
			wantWalk[foldIndexTarget](t, src, "", "")
			return
		}
		// FoO and foo both fill Lower, and the later one wins.
		if got.Upper != "" || got.Lower != "b" {
			t.Fatalf("premise: the decoder filled Upper %q and Lower %q, want empty and b", got.Upper, got.Lower)
		}
		wantWalk[foldIndexTarget](t, src, "foo", "FoO")
	})
	t.Run("a key that folds onto the field the decoder prefers, beside an exact key for the same field", func(t *testing.T) {
		src := pair("FOO", "FoO", "a", "b")
		got := decodeLikeTheDecoder[foldIndexTarget](t, src)
		if foldsOntoUpper {
			// FOO and FoO both fill Upper, and the later one wins.
			if got.Upper != "b" || got.Lower != "" {
				t.Fatalf("premise: the decoder filled Upper %q and Lower %q, want b and empty", got.Upper, got.Lower)
			}
			wantWalk[foldIndexTarget](t, src, "FoO", "FOO")
			return
		}
		// FOO fills Upper, FoO fills Lower: two fields, no pair.
		if got.Upper != "a" || got.Lower != "b" {
			t.Fatalf("premise: the decoder filled Upper %q and Lower %q, want a and b", got.Upper, got.Lower)
		}
		wantWalk[foldIndexTarget](t, src, "", "")
	})
	t.Run("a name that two fields at one depth declare is dropped, and its key folds on", func(t *testing.T) {
		src := pair("X", "x", "a", "b")
		got := decodeLikeTheDecoder[foldAmbTarget](t, src)
		if got.Lower != "b" || got.foldAmbA.X != "" || got.foldAmbB.X != "" {
			t.Fatalf("premise: the decoder filled Lower %q, A.X %q, B.X %q, want b and two empty", got.Lower, got.foldAmbA.X, got.foldAmbB.X)
		}
		wantWalk[foldAmbTarget](t, src, "x", "X")
	})
	t.Run("a dropped name that no key spells exactly is skipped, and the key folds on", func(t *testing.T) {
		src := pair("XY", "xy", "a", "b")
		got := decodeLikeTheDecoder[foldAmbTarget](t, src)
		if got.Lower2 != "b" || got.foldAmbA.Xy != "" || got.foldAmbB.Xy != "" {
			t.Fatalf("premise: the decoder filled Lower2 %q, A.Xy %q, B.Xy %q, want b and two empty", got.Lower2, got.foldAmbA.Xy, got.foldAmbB.Xy)
		}
		wantWalk[foldAmbTarget](t, src, "xy", "XY")
	})
	t.Run("a tagged field beats an untagged one at one depth, and a pair under it is refused", func(t *testing.T) {
		src := pair("Val", "VAL", "a", "b")
		got := decodeLikeTheDecoder[foldTagTarget](t, src)
		if got.Other != "a" || got.foldTagB.Val != "" {
			t.Fatalf("premise: the decoder filled Other %q and Val %q, want a and empty", got.Other, got.foldTagB.Val)
		}
		wantWalk[foldTagTarget](t, src, "Val", "VAL")
	})
	t.Run("a name declared at two depths keeps the shallow field and is not dropped", func(t *testing.T) {
		src := pair("Item", "ITEM", "a", "b")
		got := decodeLikeTheDecoder[foldDeepTarget](t, src)
		if got.Item != "a" || got.foldDeepA.Item != "" {
			t.Fatalf("premise: the decoder filled Item %q and the embedded Item %q, want a and empty", got.Item, got.foldDeepA.Item)
		}
		wantWalk[foldDeepTarget](t, src, "Item", "ITEM")
	})
	t.Run("a type embedded twice is explored once, and a pair under it is refused", func(t *testing.T) {
		src := pair("Value", "value", "first", "last")
		got := decodeLikeTheDecoder[foldRepTarget](t, src)
		if got.foldRepLeft.foldRepShared.foldRepLeaf.Value != "last" || got.foldRepRight.foldRepShared.foldRepLeaf.Value != "" {
			t.Fatalf("premise: the decoder filled the left Value %q and the right Value %q, want last and empty",
				got.foldRepLeft.foldRepShared.foldRepLeaf.Value, got.foldRepRight.foldRepShared.foldRepLeaf.Value)
		}
		wantWalk[foldRepTarget](t, src, "value", "Value")
		_, _, err := builtin.DecodeStrictJSON[foldRepTarget](src)
		var folded *builtin.FoldedFieldError
		if !errors.As(err, &folded) || folded.Later != "value" || folded.Earlier != "Value" {
			t.Errorf("DecodeStrictJSON error = %v, want a *FoldedFieldError for value and Value", err)
		}
	})
	t.Run("a type embedded once, and a pair under it is refused", func(t *testing.T) {
		src := pair("Value", "value", "first", "last")
		got := decodeLikeTheDecoder[foldRepOnce](t, src)
		if got.foldRepLeft.foldRepShared.foldRepLeaf.Value != "last" {
			t.Fatalf("premise: the decoder filled Value %q, want last", got.foldRepLeft.foldRepShared.foldRepLeaf.Value)
		}
		wantWalk[foldRepOnce](t, src, "value", "Value")
	})
	t.Run("a type embedded twice at one level has its own field dropped, and no pair is invented", func(t *testing.T) {
		// reachDiamond embeds reachLeaf through reachLeft and reachRight, both at
		// one depth, so the decoder drops the name value. Neither key fills a field.
		src := pair("Value", "value", "first", "last")
		got := decodeLikeTheDecoder[reachDiamond](t, src)
		if got.reachLeft.reachLeaf.Value != "" || got.reachRight.reachLeaf.Value != "" {
			t.Fatalf("premise: the decoder filled the left Value %q and the right Value %q, want both empty",
				got.reachLeft.reachLeaf.Value, got.reachRight.reachLeaf.Value)
		}
		wantWalk[reachDiamond](t, src, "", "")
		_, _, err := builtin.DecodeStrictJSON[reachDiamond](src)
		var folded *builtin.FoldedFieldError
		if err == nil || errors.As(err, &folded) || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("DecodeStrictJSON error = %v, want the decoder's unknown field error and no *FoldedFieldError", err)
		}
	})
	t.Run("a type that embeds itself is walked once", func(t *testing.T) {
		wantWalk[RecursiveEmbed](t, pair("name", "Name", "a", "b"), "name", "Name")
	})
}

// TestFoldedDuplicateFieldPath_DoesNotModifySource: the walk reads src only.
func TestFoldedDuplicateFieldPath_DoesNotModifySource(t *testing.T) {
	src := pair("url", "URL", "a", "b")
	builtin.FoldedDuplicateFieldPath[foldTarget](src, "url")
	if len(src) != 2 || src["url"] != "a" || src["URL"] != "b" {
		t.Errorf("src = %v, want it unchanged", src)
	}
}

// TestDecodeStrictJSON_RefusesTwoSpellings: the decoder itself refuses a pair,
// with an error that carries both paths and the one shared sentence.
func TestDecodeStrictJSON_RefusesTwoSpellings(t *testing.T) {
	_, _, err := builtin.DecodeStrictJSON[foldTarget](map[string]any{"ref": pair("name", "Name", "a", "b")})
	if err == nil {
		t.Fatal("DecodeStrictJSON accepted ref.name and ref.Name; one value would be dropped without a message")
	}
	var folded *builtin.FoldedFieldError
	if !errors.As(err, &folded) {
		t.Fatalf("error %q is no *FoldedFieldError", err)
	}
	if folded.Later != "ref.name" || folded.Earlier != "ref.Name" {
		t.Errorf("paths = (%q, %q), want (ref.name, ref.Name)", folded.Later, folded.Earlier)
	}
	if want := foldSentence("ref.name", "ref.Name"); err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// TestDecodeStrictJSON_TwoSpellingsFoundThroughWrapping: errors.As finds the
// typed error under the wrappers the repository's callers add (A4).
func TestDecodeStrictJSON_TwoSpellingsFoundThroughWrapping(t *testing.T) {
	_, _, err := builtin.DecodeStrictJSON[foldTarget](pair("url", "URL", "a", "b"))
	wrapped := errors.Wrapf(errors.Wrap(err, "inner"), "outer %d", 1)
	wrapped = errors.Errorf("helmrelease: properties do not decode as a spec: %w", wrapped)
	var folded *builtin.FoldedFieldError
	if !errors.As(wrapped, &folded) {
		t.Fatalf("errors.As found no *FoldedFieldError in %q", wrapped)
	}
}

// TestDecodeStrictJSON_UnknownKeyBeforeTwoSpellings: a pair is looked for after
// the decode, so an unknown key or a wrong type is still the first error.
func TestDecodeStrictJSON_UnknownKeyBeforeTwoSpellings(t *testing.T) {
	src := map[string]any{"zzz": 1, "url": "a", "URL": "b"}
	_, _, err := builtin.DecodeStrictJSON[foldTarget](src)
	if err == nil || !strings.Contains(err.Error(), `unknown field "zzz"`) {
		t.Fatalf("error = %v, want one naming the unknown field zzz", err)
	}
	var folded *builtin.FoldedFieldError
	if errors.As(err, &folded) {
		t.Errorf("error %q is the pair, want the unknown field first", err)
	}
}

// TestDecodeStrictJSON_OneSpellingAndNullStillDecode: the cases that are not a
// pair decode as before.
func TestDecodeStrictJSON_OneSpellingAndNullStillDecode(t *testing.T) {
	spec, _, err := builtin.DecodeStrictJSON[foldTarget](map[string]any{"URL": "kept", "ref": map[string]any{"Name": "n"}})
	if err != nil || spec.URL != "kept" || spec.Ref == nil || spec.Ref.Name != "n" {
		t.Fatalf("one spelling: spec %+v, err %v; want URL kept and ref.name n", spec, err)
	}
	spec, _, err = builtin.DecodeStrictJSON[foldTarget](pair("url", "URL", nil, "x"))
	if err != nil || spec.URL != "x" {
		t.Fatalf("null beside a set value: spec %+v, err %v; want URL x", spec, err)
	}
	spec, _, err = builtin.DecodeStrictJSON[foldTarget](pair("Foo", "foo", "a", "b"))
	if err != nil || spec.Upper != "a" || spec.Lower != "b" {
		t.Fatalf("two declared fields: spec %+v, err %v; want Foo a and foo b", spec, err)
	}
}
