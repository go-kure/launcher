package patch

import (
	"strings"
	"testing"
)

// A root key that is an alias to a tagged key reads as the decoded text (`abc`), and the root
// mapping pairs it by that text, so the write-back keeps the alias and every comment. Before the
// fix the root paired by the encoded text (`YWJj`), found no pair and wrote `abc` beside `*k`.
const rootAliasToTaggedKey = `apiVersion: v1
kind: ConfigMap
metadata:
  name: tagged
  annotations:
    &k !!binary YWJj: v
# about data
data:
  other: x
*k : y
`

func TestWriteBack_RootAliasToTaggedKeyKeepsTheAlias(t *testing.T) {
	doc := loadOneDocument(t, rootAliasToTaggedKey)
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if !strings.Contains(out, "*k") {
		t.Errorf("the root alias key *k was not kept:\n%s", out)
	}
	if strings.Contains(out, "\nabc:") {
		t.Errorf("the decoded key abc is written beside the alias:\n%s", out)
	}
	if !strings.Contains(out, "# about data") {
		t.Errorf("the comment on data was lost:\n%s", out)
	}
	if !strings.Contains(out, "other: z") {
		t.Errorf("the patched value is missing:\n%s", out)
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A plain root key such as 0x10 keeps its authored text, because the loader reads a plain key
// as its own text. This guards the plain path of the fix and passes before and after it.
func TestWriteBack_RootPlainKeyKeepsItsAuthoredText(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: plain\n# about data\ndata:\n  other: x\n0x10: a\n")
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if got := strings.Count(out, "0x10: a\n"); got != 1 {
		t.Errorf("root key 0x10 appears %d times, want 1:\n%s", got, out)
	}
	if !strings.Contains(out, "# about data") {
		t.Errorf("the comment on data was lost:\n%s", out)
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A null root key does not decode into a string, and the loader skips that entry. The pairing of
// the other root keys must not break because of it. This guard passes before and after the fix.
func TestWriteBack_RootNullKeyIsLeftAlone(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: null-key\n# about data\ndata:\n  other: x\n~: a\n")
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if !strings.Contains(out, "# about data") {
		t.Errorf("the comment on data was lost:\n%s", out)
	}
	if !strings.Contains(out, "other: z") {
		t.Errorf("the patched value is missing:\n%s", out)
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// Two keys of one mapping can hold the same authored text after a merge, although the loader reads
// them as two keys: `!!binary WVdKag==` reads "YWJj" and `!!binary YWJj` reads "abc". The patch
// changes the first value, and the written text must still load. The check for two keys of one
// text compares the authored text, and not only the text the loader reads.
func TestWriteBack_TwoTaggedKeysThatReadApartStillLoad(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: tagged\ndata:\n  !!binary WVdKag==: a\n  !!binary YWJj: b\n")
	out := writeBack(t, doc, "patch", []string{"data", "WVdKag=="}, "c")
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A root key can be a binary key whose bytes are not text (`!!binary /w==` reads the byte 0xff).
// The marshaler writes that key as `!!binary /w==` again, so the root pairs the two by the text
// the loader reads on both sides. The document stays writable and keeps the key once.
func TestWriteBack_RootBinaryKeyThatIsNotTextKeepsItsText(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bin\ndata:\n  other: x\n!!binary /w==: a\n")
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if got := strings.Count(out, "!!binary /w==: a\n"); got != 1 {
		t.Errorf("the binary root key appears %d times, want 1:\n%s", got, out)
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// An untouched timestamp keeps its authored style under a root key that is not text. The style is
// paired with the written key by the text the loader reads, so the encoded `/w==` and the byte
// 0xff the loader reads are one key.
func TestWriteBack_RootBinaryKeyKeepsAnUntouchedTimestampStyle(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bin\ndata:\n  other: x\n!!binary /w==: 2026-11-07\n")
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if !strings.Contains(out, "!!binary /w==: 2026-11-07\n") {
		t.Errorf("the timestamp under the binary root key lost its authored style:\n%s", out)
	}
}
