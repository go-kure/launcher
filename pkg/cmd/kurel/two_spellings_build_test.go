package kurel

import (
	"fmt"
	"strings"
	"testing"
)

// twoSpellingsApp is an application with one gitrepository component whose
// properties come from props, a block of YAML lines indented under properties.
func twoSpellingsApp(props string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: src
      type: gitrepository
      properties:
` + props
}

// TestBuild_RefusesTwoSpellingsOfOneField: `kurel build` never builds a raw kind
// whose properties write one field in two spellings (go-kure/launcher#1322).
// encoding/json matches field names ignoring case, so before this the build kept
// one value and dropped the other without a message. Two paths refuse it now.
//
// At the top level the authored-property check runs first and refuses the
// spelling the schema does not declare (`Suspend`) as an unsupported field, before
// any decode. Below the top the schema leaves the object open (additionalProperties),
// so the decode refuses the pair (`ref.branch` and `ref.Branch`) with the
// "sets the same field" sentence. Neither refusal depends on a stated Kubernetes
// version: the feature-gate check that runs beside them does not see either. The
// one-spelling document is the control: it builds, with and without a version, so
// a refusal below is not the document failing for another reason.
func TestBuild_RefusesTwoSpellingsOfOneField(t *testing.T) {
	const url = "        url: https://git.example.com/shop.git\n"
	ok := twoSpellingsApp(url + "        suspend: true\n")
	pair := twoSpellingsApp(url + "        suspend: true\n        Suspend: false\n")
	below := twoSpellingsApp(url + "        ref:\n          branch: main\n          Branch: dev\n")

	for _, version := range []string{"", "1.34", "1.99"} {
		spec := " {}\n"
		if version != "" {
			spec = fmt.Sprintf("\n  kubernetesVersion: %q\n", version)
		}
		profile := profileWithSpec(spec)
		name := version
		if name == "" {
			name = "no version"
		}
		t.Run(name, func(t *testing.T) {
			if _, err := buildWithProfile(t, ok, profile); err != nil {
				t.Fatalf("control: one spelling is refused: %v", err)
			}
			for _, c := range []struct{ name, doc, want string }{
				{"top level", pair, `unsupported field "Suspend"`},
				{"below the top", below, "ref.branch: sets the same field as ref.Branch"},
			} {
				_, err := buildWithProfile(t, c.doc, profile)
				if err == nil {
					t.Fatalf("%s: built with two spellings of one field; one value would be dropped without a message", c.name)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: error = %q, want it to contain %q", c.name, err, c.want)
				}
			}
		})
	}
}
