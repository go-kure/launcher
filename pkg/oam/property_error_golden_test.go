package oam

import (
	"os"
	"strings"
	"testing"
)

const perrGolden = "testdata/property_error_messages.golden"

// perrMessage runs one case through the public entry point and returns the error text.
func perrMessage(t *testing.T, tc perrCase) string {
	t.Helper()
	_, err := newPerrTransformer().ValidateAuthoredPropertiesWithCapabilities(tc.app, tc.caps)
	if err == nil {
		t.Fatalf("%s: the document was accepted, want a refusal", tc.name)
	}
	return err.Error()
}

// TestAuthoredPropertyRefusalMessages holds every refusal's text byte for byte to the
// golden file recorded before the refusals became a typed error
// (go-kure/launcher#1466): the type adds a location and changes no message.
// PERR_GOLDEN_WRITE=1 rewrites the file.
func TestAuthoredPropertyRefusalMessages(t *testing.T) {
	cases := perrCases()
	var got strings.Builder
	for _, tc := range cases {
		msg := perrMessage(t, tc)
		if strings.ContainsAny(msg, "\t\n") {
			t.Fatalf("%s: message holds a tab or a newline, which the golden file cannot carry: %q", tc.name, msg)
		}
		got.WriteString(tc.name + "\t" + msg + "\n")
	}
	if os.Getenv("PERR_GOLDEN_WRITE") == "1" {
		if err := os.WriteFile(perrGolden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(perrGolden)
	if err != nil {
		t.Fatal(err)
	}
	wantLines := strings.Split(strings.TrimSuffix(string(want), "\n"), "\n")
	gotLines := strings.Split(strings.TrimSuffix(got.String(), "\n"), "\n")
	if len(wantLines) != len(gotLines) {
		t.Fatalf("golden has %d lines, the cases give %d", len(wantLines), len(gotLines))
	}
	for i := range gotLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("message changed\n got: %s\nwant: %s", gotLines[i], wantLines[i])
		}
	}
}
