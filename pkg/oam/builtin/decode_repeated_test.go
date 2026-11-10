package builtin_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// repeatRef, repeatSource and repeatTarget are plain structs with json tags, the
// shape of an external API type. A map cannot hold a key twice, so a test builds
// the repeat with a json.RawMessage: it serializes itself, and json.Marshal copies
// its text as written.
type repeatRef struct {
	Branch string `json:"branch"`
	Tag    string `json:"tag"`
}

type repeatSource struct {
	URL   string      `json:"url"`
	Ref   *repeatRef  `json:"ref"`
	Items []repeatRef `json:"items"`
}

type repeatTarget struct {
	Source repeatSource `json:"source"`
}

// repeatShapedHolder puts the self-decoding fixture of decode_json_test.go one key
// down, as the Cilium trait's rule sits under the trait's properties.
type repeatShapedHolder struct {
	T shapedTarget `json:"t"`
}

const repeatSentence = " is given more than once in the same object; give each key once"

// repeatSrc is the properties map of one authored component whose only key, source,
// holds text as a raw JSON value.
func repeatSrc(key, text string) map[string]any {
	return map[string]any{key: json.RawMessage(text)}
}

// TestDecodeStrictJSON_RepeatedKey: DecodeStrictJSON refuses a key given twice in
// one JSON object, at any depth, and names its path (go-kure/launcher#1361). The
// typed decode merges the occurrences of a repeated object member and drops a
// repeated scalar, so without the refusal the fold walk reads a different tree
// than the decode did.
func TestDecodeStrictJSON_RepeatedKey(t *testing.T) {
	// escapedTag is the key "tag" written with a JSON escape for its first letter.
	escapedTag := `{"ref":{"tag":"a","` + "\\" + `u0074ag":"b"}}`
	rows := []struct {
		name string
		text string
		want string
	}{
		{"R1 a pair in an earlier occurrence of a repeated key",
			`{"ref":{"Branch":"dev","branch":"main"},"ref":{"tag":"v1"}}`, "source.ref"},
		{"R2 an exact scalar repeat", `{"ref":{"tag":"v1","tag":"v2"}}`, "source.ref.tag"},
		{"R3 a pair across two occurrences", `{"ref":{"Branch":"dev"},"ref":{"branch":"main"}}`, "source.ref"},
		{"R9 a repeat in a list element", `{"items":[{"tag":"a"},{"tag":"b","tag":"c"}]}`, "source.items[1].tag"},
		{"R10 a repeat written with an escape", escapedTag, "source.ref.tag"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, _, err := builtin.DecodeStrictJSON[repeatTarget](repeatSrc("source", r.text))
			if err == nil {
				t.Fatalf("DecodeStrictJSON accepted %s, want an error naming %s", r.text, r.want)
			}
			if !strings.Contains(err.Error(), r.want+repeatSentence) {
				t.Errorf("err = %q, want it to contain %q", err, r.want+repeatSentence)
			}
		})
	}

	t.Run("R6 a self-decoding type drops the unknown key of the earlier occurrence", func(t *testing.T) {
		src := repeatSrc("t", `{"selector":{"mtach":{"a":"b"}},"selector":{"match":{"a":"b"}}}`)
		_, _, err := builtin.DecodeStrictJSON[repeatShapedHolder](src)
		if err == nil {
			t.Fatal("DecodeStrictJSON accepted the repeated selector, want an error naming t.selector")
		}
		if !strings.Contains(err.Error(), "t.selector"+repeatSentence) {
			t.Errorf("err = %q, want it to contain %q", err, "t.selector"+repeatSentence)
		}
	})

	// An empty key is a key, and a step of the path like any other: the path of a
	// repeat under an empty key keeps the empty step, so it is ".x" and never "x". A
	// struct refuses an empty key as unknown, so these rows decode into a map.
	emptyRows := []struct {
		name string
		text string
		want string
	}{
		{"R11 a repeated empty key under an empty key", `{"":1,"":2}`, "."},
		{"R12 a repeated empty key three levels down", `{"":{"":1,"":2}}`, ".."},
		{"R13 a repeated key under an empty key", `{"x":1,"x":2}`, ".x"},
	}
	for _, r := range emptyRows {
		t.Run(r.name, func(t *testing.T) {
			_, _, err := builtin.DecodeStrictJSON[map[string]any](repeatSrc("", r.text))
			if err == nil {
				t.Fatalf("DecodeStrictJSON accepted %s under an empty key, want an error naming %q", r.text, r.want)
			}
			if got := err.Error(); !strings.HasSuffix(got, r.want+repeatSentence) || strings.TrimSuffix(got, r.want+repeatSentence) != "" {
				t.Errorf("err = %q, want exactly %q", got, r.want+repeatSentence)
			}
		})
	}
}

// TestDecodeStrictJSON_RepeatedKeyDeepValueCost: a deep value under a long key costs
// one path step per level, not a copy of the path at each level. A document with no
// repeat must not allocate in proportion to key length times depth.
func TestDecodeStrictJSON_RepeatedKeyDeepValueCost(t *testing.T) {
	const depth = 2000
	key := strings.Repeat("k", 64<<10)
	text := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
	src := repeatSrc(key, text)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := builtin.DecodeStrictJSON[map[string]json.RawMessage](src)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("DecodeStrictJSON refused a document with no repeat: %v", err)
	}
	// Copying the path at each level would allocate about 128 MiB here.
	if got := after.TotalAlloc - before.TotalAlloc; got > 16<<20 {
		t.Errorf("DecodeStrictJSON allocated %d bytes for a %d-deep value under a 64 KiB key, want under 16 MiB", got, depth)
	}
}

// TestDecodeStrictJSON_RepeatedKeyControls: the cases around the refusal. Each
// passes before and after the fix and discriminates nothing alone, so none is
// evidence for the fix.
func TestDecodeStrictJSON_RepeatedKeyControls(t *testing.T) {
	t.Run("C1 the decoder merges the occurrences and drops dev", func(t *testing.T) {
		var got repeatTarget
		text := `{"source":{"ref":{"Branch":"dev"},"ref":{"branch":"main"}}}`
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got.Source.Ref == nil || got.Source.Ref.Branch != "main" {
			t.Errorf("Source.Ref = %+v, want Branch main: the premise of the refusal", got.Source.Ref)
		}
	})

	t.Run("C2 an unknown key is reported before a repeat", func(t *testing.T) {
		_, _, err := builtin.DecodeStrictJSON[repeatTarget](repeatSrc("source", `{"ref":{"nope":1},"ref":{"tag":"a"}}`))
		if err == nil {
			t.Fatal("DecodeStrictJSON accepted an unknown key")
		}
		if !strings.Contains(err.Error(), "nope") || strings.Contains(err.Error(), "given more than once") {
			t.Errorf("err = %q, want the unknown-key error and no repeat sentence", err)
		}
	})

	t.Run("C3 a pair with no repeat still gets the fold sentence", func(t *testing.T) {
		_, _, err := builtin.DecodeStrictJSON[repeatTarget](repeatSrc("source", `{"ref":{"Branch":"dev","branch":"main"}}`))
		if err == nil {
			t.Fatal("DecodeStrictJSON accepted a folded pair")
		}
		if !strings.Contains(err.Error(), "source.ref.branch: sets the same field as source.ref.Branch") {
			t.Errorf("err = %q, want the fold sentence for source.ref.branch", err)
		}
	})

	t.Run("C4 a document with no repeat decodes", func(t *testing.T) {
		got, _, err := builtin.DecodeStrictJSON[repeatTarget](repeatSrc("source", `{"ref":{"branch":"main","tag":"v1"},"items":[{"tag":"a"},{"tag":"b"}]}`))
		if err != nil {
			t.Fatalf("DecodeStrictJSON refused a document with no repeat: %v", err)
		}
		if got.Source.Ref == nil || got.Source.Ref.Branch != "main" || len(got.Source.Items) != 2 {
			t.Errorf("decoded %+v, want branch main and two items", got.Source)
		}
	})

	t.Run("C5 an empty key given once decodes", func(t *testing.T) {
		if _, _, err := builtin.DecodeStrictJSON[map[string]any](repeatSrc("", `{"":1,"a":2}`)); err != nil {
			t.Fatalf("DecodeStrictJSON refused an empty key given once: %v", err)
		}
	})
}
