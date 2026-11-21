package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"
)

// yamlProbeStrings are the strings whose plain YAML spelling a reader may take
// for another type or for syntax: the YAML 1.1 words Flux's reader knows, a
// sexagesimal, an octal, binary and underscore integer, a float word, the merge
// key, leading or trailing space, each indicator character at the start, and
// newlines and a tab. Each must come back from the stored bytes as the same
// string, both as a value and as a map key.
var yamlProbeStrings = []string{
	"", " ", " lead", "trail ", "yes", "no", "y", "n", "Y", "N", "No", "NO", "on", "off", "On", "Off",
	"true", "false", "True", "null", "Null", "~", "1:30", "190:20:30", "0777", "0o17", "0b11", "0x1F",
	"1_000", "1.10", "123", "-1", "+1", "1e3", ".inf", "-.inf", ".nan", "<<", "2026-11-21",
	"2026-11-21T10:00:00Z", "#hash", "-dash", "- dash", "*star", "&amp", "!bang", "%pct", "@at",
	"{brace", "[bracket", "}", "]", "|", ">", "?", "? q", ": ", "a: b", "a #b", "a:b", "'q'", `"dq"`,
	"line1\nline2", "line1\nline2\n", "line1\nline2\n\n", "\n", "trailing\n", "tab\there", "\ttab",
	"héllo ✓", "plain text",
	// texts the literal block style may not carry: space before or after a
	// newline, a carriage return, a first line that is indented or empty
	"a \nb", " \n", "x\n \n", "\r\n", "a\r", "line\n  indented", "\n\nlead", "  \n  x\n", "a\n\tb\n", "end \n",
	// Unicode line breaks and unusual characters, written as bytes: the block
	// header may take a leading separator as its own line break (found by
	// review). U+2028, U+2029, U+0085, U+FEFF, U+00A0 in that order of use.
	"\xe2\x80\xa8hello\n", "\xe2\x80\xa9hello\n", "\xc2\x85hello\n", "a\xe2\x80\xa8b\n", "a\xe2\x80\xa9b",
	"\xe2\x80\xa8", "\xe2\x80\xa9\n", "\xc2\x85", "\x07bell", "\xef\xbb\xbfbom", "\xc2\xa0nbsp\n",
	"\xe2\x80\xa8\xe2\x80\xa8x\n", "x\n\xe2\x80\xa8y\n",
	// a reader normalises these inside a block (added on the reviewer's advice)
	"a\r\nb\n", "x\xc2\x85y\n",
	// a leading tab in a multi-line text: the encoder's block is one its own
	// parser rejects, so Node.Encode itself fails (found by review round 3)
	"\tfirst\nsecond\n", "\tx\n", "\t\n", "\t\tdeep\nnext", " \tmixed\nlines\n",
}

// yamlProbeNumbers are numbers as JSON writes them; each reads back as a
// number of the same value, though the text may differ (1E5 reads as 100000).
var yamlProbeNumbers = []string{
	"0", "-0", "0.0", "1", "-1", "1.50", "1E5", "1e-7", "1e21", "12345678901234567890", "18446744073709551615",
	"123456789012345678901234567890", "0.1", "3.14159265358979323846",
}

// yamlProbeTree builds one tree holding every probe string as a value and as a
// key, every number, and the container shapes.
func yamlProbeTree() map[string]any {
	tree := map[string]any{}
	asKeys := map[string]any{}
	list := []any{}
	for i, s := range yamlProbeStrings {
		tree[fmt.Sprintf("v%03d", i)] = s
		asKeys[s] = json.Number(strconv.Itoa(i))
		list = append(list, s)
	}
	nums := map[string]any{}
	for i, n := range yamlProbeNumbers {
		nums[fmt.Sprintf("n%02d", i)] = json.Number(n)
	}
	tree["keys"] = asKeys
	tree["list"] = list
	tree["numbers"] = nums
	tree["bools"] = []any{true, false}
	tree["nothing"] = nil
	tree["emptymap"] = map[string]any{}
	tree["emptylist"] = []any{}
	tree["nested"] = map[string]any{"a": map[string]any{"b": []any{map[string]any{"c": "d"}, []any{"e"}}}}
	return tree
}

// sameTyped reports where want and got differ, type for type: a string stays a
// string, a number a number of the same value, a bool a bool.
func sameTyped(path string, want, got any) string {
	switch w := want.(type) {
	case nil:
		if got != nil {
			return fmt.Sprintf("%s: want null, got %T %v", path, got, got)
		}
	case string:
		if g, ok := got.(string); !ok || g != w {
			return fmt.Sprintf("%s: want string %q, got %T %v", path, w, got, got)
		}
	case bool:
		if g, ok := got.(bool); !ok || g != w {
			return fmt.Sprintf("%s: want bool %v, got %T %v", path, w, got, got)
		}
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return fmt.Sprintf("%s: want number %s, got %T %v", path, w, got, got)
		}
		// Flux's reader hands a number on as a float64, as it did for the JSON
		// this replaced, so equality holds at that precision; the stored text
		// itself is checked exactly in TestHelmValuesYAML_NumbersKeepTheirText.
		wf, werr := strconv.ParseFloat(string(w), 64)
		gf, gerr := strconv.ParseFloat(string(g), 64)
		if werr != nil || gerr != nil || wf != gf {
			return fmt.Sprintf("%s: want number %s, got %s", path, w, g)
		}
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: want map of %d, got %T %v", path, len(w), got, got)
		}
		for k, e := range w {
			ge, present := g[k]
			if !present {
				return fmt.Sprintf("%s: key %q missing", path, k)
			}
			if d := sameTyped(path+"."+k, e, ge); d != "" {
				return d
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: want list of %d, got %T %v", path, len(w), got, got)
		}
		for i := range w {
			if d := sameTyped(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
	default:
		return fmt.Sprintf("%s: unexpected authored type %T", path, want)
	}
	return ""
}

// readAsFlux reads stored bytes the way Flux reads a values reference:
// sigs.k8s.io/yaml (YAML 1.1 words) into JSON, numbers kept as text.
func readAsFlux(data []byte) (map[string]any, error) {
	var got map[string]any
	err := sigsyaml.Unmarshal(data, &got, func(d *json.Decoder) *json.Decoder {
		d.UseNumber()
		return d
	})
	return got, err
}

// readBackDiff returns "" when data reads back as want, type for type, else the
// first difference or the read error.
func readBackDiff(want map[string]any, data []byte) string {
	got, err := readAsFlux(data)
	if err != nil {
		return "does not read back: " + err.Error()
	}
	return sameTyped("$", want, got)
}

// TestHelmValuesYAML_ReadsBackTypeForType: the stored bytes read back through
// Flux's reader equal the authored tree type for type, for every form a YAML 1.1
// reader would retype if written plain, as a value and as a map key. A form that
// does not survive is a finding, never a row to drop.
func TestHelmValuesYAML_ReadsBackTypeForType(t *testing.T) {
	tree := yamlProbeTree()
	data, err := helmValuesYAML(tree)
	if err != nil {
		t.Fatal(err)
	}
	if d := readBackDiff(tree, data); d != "" {
		t.Errorf("whole tree: %s", d)
	}
	// each probe string alone, as a value, in a list and as a key, so one
	// failure does not hide the rest
	for _, s := range yamlProbeStrings {
		for name, one := range map[string]map[string]any{
			"value": {"k": s},
			"list":  {"k": []any{s}},
			"key":   {s: "v"},
		} {
			b, err := helmValuesYAML(one)
			if err != nil {
				t.Errorf("%s %q: %v", name, s, err)
				continue
			}
			if d := readBackDiff(one, b); d != "" {
				t.Errorf("%s %q: %s\n%s", name, s, d, b)
			}
		}
	}
	for _, n := range yamlProbeNumbers {
		one := map[string]any{"k": json.Number(n)}
		b, err := helmValuesYAML(one)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if d := readBackDiff(one, b); d != "" {
			t.Errorf("number %s: %s\n%s", n, d, b)
		}
	}
}

// TestHelmValuesYAML_NumbersKeepTheirText: a number is written as the text it
// was authored with, so no digit is rounded and 1.50 stays 1.50 (the site
// reference promises numbers kept exact).
func TestHelmValuesYAML_NumbersKeepTheirText(t *testing.T) {
	data, err := helmValuesYAML(map[string]any{
		"big": json.Number("18446744073709551615"), "huge": json.Number("123456789012345678901234567890"),
		"f": json.Number("1.50"), "e": json.Number("1e21"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "big: 18446744073709551615\ne: 1e21\nf: 1.50\nhuge: 123456789012345678901234567890\n"
	if string(data) != want {
		t.Errorf("got\n%s\nwant\n%s", data, want)
	}
}

// TestHelmValuesYAML_FixedOrderAndShape: the bytes are one fixed text: keys in
// byte order (capitals first, a10 before a2), two-space
// indent, sequences indented under their key, empty containers written flat,
// a multi-line string as a block, and the text ends in one newline.
func TestHelmValuesYAML_FixedOrderAndShape(t *testing.T) {
	data, err := helmValuesYAML(map[string]any{
		"z": json.Number("1"), "a10": "x", "a2": "y", "B": true,
		"image":     map[string]any{"tag": "1.10", "repo": "r"},
		"list":      []any{"a", map[string]any{"k": "v"}},
		"emptymap":  map[string]any{},
		"emptylist": []any{},
		"multi":     "l1\nl2\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `B: true
a10: x
a2: "y"
emptylist: []
emptymap: {}
image:
  repo: r
  tag: "1.10"
list:
  - a
  - k: v
multi: |
  l1
  l2
z: 1
`
	if string(data) != want {
		t.Errorf("got\n%s\nwant\n%s", data, want)
	}
}

// TestHelmValuesYAML_Deterministic: equal trees give the same bytes, over many
// builds with different map insertion orders (Go randomises iteration too).
func TestHelmValuesYAML_Deterministic(t *testing.T) {
	keys := []string{"zeta", "alpha", "a10", "a2", "B", "mid", "Z", "0", "10", "9"}
	var first []byte
	for i := 0; i < 200; i++ {
		m := map[string]any{}
		sub := map[string]any{}
		// rotate the insertion order on every build
		for j := range keys {
			k := keys[(j+i)%len(keys)]
			m[k] = json.Number(fmt.Sprint(len(k)))
			sub[k] = []any{k, true}
		}
		m["sub"] = sub
		data, err := helmValuesYAML(m)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatalf("build %d differs:\n%s\nfrom\n%s", i, data, first)
		}
	}
}

// TestHelmValuesYAML_ErrorNamesNoValue: a tree that does not encode is refused
// with fixed text. The Secret path carries values nobody may read in an error.
func TestHelmValuesYAML_ErrorNamesNoValue(t *testing.T) {
	const secret = "s3cr3t-token-value"
	_, err := helmValuesYAML(map[string]any{"k": secret, "bad": func() {}})
	if err == nil {
		t.Fatal("a tree holding a func encoded without error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error repeats a value: %v", err)
	}
}
