package model

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// An explicit YAML tag that does not fit its value makes yaml.v3 fail with no line:
// `required: !!bool notabool` is one case and an invalid `!!binary` payload is another.
// Load finds the tagged scalar that fails and names its line. The failure stays a
// YAML-level one and does not wrap ErrInvalid. The README says so.
func TestLoad_YAMLTagErrorNamesTheLine(t *testing.T) {
	cases := []struct {
		name   string
		block  string
		marker string // the text whose line the error must name
	}{
		{"bool tag on a word", "  inputs:\n    image:\n      type: string\n      required: !!bool notabool\n", "!!bool"},
		{"binary tag on bad base64", "  inputs:\n    image:\n      type: string\n      default: !!binary '!!'\n", "!!binary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := withInputs(t, tc.block)
			i := bytes.Index(doc, []byte(tc.marker))
			if i < 0 {
				t.Fatalf("test bug: %q is not in the document", tc.marker)
			}
			wantLine := fmt.Sprintf("line %d:", bytes.Count(doc[:i], []byte("\n"))+1)

			m, err := loadNoPanic(doc)
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !strings.Contains(err.Error(), wantLine) {
				t.Fatalf("error %q does not name %q", err, wantLine)
			}
			if errors.Is(err, ErrInvalid) {
				t.Fatalf("a YAML tag error is an envelope failure, not ErrInvalid: %v", err)
			}

			// The walk reports the node that the strict decode failed on, with the
			// message of that decode. The text after the line prefix equals the strict
			// decode's own message without its "yaml: " prefix.
			strict := yaml.NewDecoder(bytes.NewReader(doc))
			strict.KnownFields(true)
			var probe Model
			sErr := strict.Decode(&probe)
			if sErr == nil {
				t.Fatal("test bug: the strict decode accepted the document")
			}
			want := strings.TrimPrefix(sErr.Error(), "yaml: ")
			_, after, ok := strings.Cut(err.Error(), wantLine+" ")
			if !ok || after != want {
				t.Fatalf("message after the line prefix = %q, want the strict decode message %q", after, want)
			}
		})
	}
}

// The walk names a line only when exactly one tagged scalar is at fault. The strict
// decode never decodes a value under a null key of a map with string keys, so a walk that
// took the first failing tagged scalar could name a node the decode never reached. With
// two or more failing nodes Load keeps the error without a line. The README says so.
func TestLoad_YAMLTagErrorNamesNoLineWhenAmbiguous(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{
			"one failing node under a null key, one the decode reaches",
			"  inputs:\n    null:\n      required: !!bool first\n    image:\n      type: string\n      required: !!bool second\n",
		},
		{
			"two failing nodes the decode can reach",
			"  inputs:\n    image:\n      type: string\n      required: !!bool first\n    other:\n      type: string\n      required: !!bool second\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(withInputs(t, tc.block))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if strings.Contains(err.Error(), "line ") {
				t.Fatalf("error %q names a line, but two tagged values are at fault", err)
			}
			if !strings.HasPrefix(err.Error(), "model: yaml: ") {
				t.Fatalf("error %q is not the YAML error as it was", err)
			}
		})
	}
}

// A bad tag only under a null key is never decoded, so it is no failure, and the walk
// is not run. Load behaves as it did.
func TestLoad_YAMLTagUnderNullKeyStaysAccepted(t *testing.T) {
	block := "  inputs:\n    null:\n      required: !!bool first\n    image:\n      type: string\n"
	if m, err := loadNoPanic(withInputs(t, block)); err != nil {
		t.Fatalf("Load refused a document whose only bad tag sits under a null key: %v (model %+v)", err, m)
	}
}

// The walk names a line only when its message equals the message of the strict decode.
// Here exactly one tagged scalar fails when decoded alone: the `!!bool` under the null key,
// which the strict decode never reaches. The strict decode fails on something else, a
// sequence used as a mapping key inside `default`, which is a plain YAML error with no
// line. Naming the line of the `!!bool` would point at a value that is not at fault, so
// Load keeps the error as it was.
func TestLoad_YAMLTagErrorKeepsTheErrorWhenMessagesDiffer(t *testing.T) {
	block := "  inputs:\n    null:\n      required: !!bool first\n    image:\n      type: string\n      default:\n        ? [a, b]\n        : c\n"
	doc := withInputs(t, block)

	// The row proves the equality half only if the strict decode fails with an error that
	// is not a tag failure and not a type error. Stop the test on a miss, so a changed
	// decoder reads as a broken row and never as a pass.
	strict := yaml.NewDecoder(bytes.NewReader(doc))
	strict.KnownFields(true)
	var probe Model
	sErr := strict.Decode(&probe)
	if sErr == nil {
		t.Fatal("test bug: the strict decode accepted the document")
	}
	var typeErr *yaml.TypeError
	if errors.As(sErr, &typeErr) {
		t.Fatalf("test bug: the strict decode error is a *yaml.TypeError, which carries its own lines: %v", sErr)
	}
	if !strings.Contains(sErr.Error(), "invalid map key") {
		t.Fatalf("test bug: the strict decode failed on something else than the sequence key: %v", sErr)
	}
	var tagged yaml.Node
	if err := yaml.Unmarshal([]byte("required: !!bool first\n"), &tagged); err != nil {
		t.Fatalf("test bug: %v", err)
	}
	var v any
	tagErr := tagged.Content[0].Content[1].Decode(&v)
	if tagErr == nil {
		t.Fatal("test bug: the tagged scalar decodes without an error")
	}
	if strings.TrimPrefix(tagErr.Error(), "yaml: ") == strings.TrimPrefix(sErr.Error(), "yaml: ") {
		t.Fatalf("test bug: the tag failure and the strict decode failure have the same message: %v", tagErr)
	}

	t.Logf("precondition met: strict decode error %q is no *yaml.TypeError and differs from the tag failure %q", sErr, tagErr)

	m, err := loadNoPanic(doc)
	if err == nil {
		t.Fatalf("Load accepted the document: %+v", m)
	}
	if strings.Contains(err.Error(), "line ") {
		t.Fatalf("error %q names a line, but the tagged scalar is not what the strict decode failed on", err)
	}
	want := "model: " + sErr.Error()
	if err.Error() != want {
		t.Fatalf("error = %q, want the strict decode error as it was: %q", err, want)
	}
}
