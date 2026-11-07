package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// This file tests go-kure/launcher#1322 through the real handlers: a field
// authored under two spellings (encoding/json matches field names ignoring
// case) is refused at build, at any depth, for every raw kind. Before, such a
// kind kept one value and dropped the other without a message. Each refusal row
// has controls: the same document with one spelling builds, and so does a null
// spelling beside a set one, so a row that fails for another reason is no result.

// foldedSentence is the text every refusal of two spellings of one field ends in.
func foldedSentence(later, earlier string) string {
	return later + ": sets the same field as " + earlier + " (field names match case-insensitively, so one value would be dropped)"
}

// foldedKind builds one raw kind from properties.
type foldedKind struct {
	name    string
	minimal func(t *testing.T) map[string]any
	build   func(props map[string]any) error
}

// foldedKinds lists the raw kinds whose handlers call builtin.DecodeStrictJSON
// themselves. Every one of them declares the optional boolean suspend.
func foldedKinds() []foldedKind {
	var out []foldedKind
	for _, k := range fluxSourceKinds() {
		out = append(out, foldedKind{
			name:    k.typ,
			minimal: func(t *testing.T) map[string]any { return fluxSrcProps(t, k.minimal) },
			build: func(props map[string]any) error {
				_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "src", Type: k.typ, Properties: props}, "demo")
				return err
			},
		})
	}
	out = append(out,
		foldedKind{
			name:    "fluxcd-kustomization",
			minimal: func(*testing.T) map[string]any { return map[string]any{"sourceRef": kzSourceRef()} },
			build: func(props map[string]any) error {
				_, err := (&components.FluxcdKustomizationHandler{}).ToApplicationConfig(&oam.Component{Name: "src", Type: "fluxcd-kustomization", Properties: props}, "demo")
				return err
			},
		},
		foldedKind{
			name:    "helmrelease",
			minimal: func(*testing.T) map[string]any { return map[string]any{"chart": hrChart()} },
			build: func(props map[string]any) error {
				_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(&oam.Component{Name: "src", Type: "helmrelease", Properties: props}, "demo")
				return err
			},
		},
	)
	return out
}

// foldedWith returns props with the extra keys added.
func foldedWith(props, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range props {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// foldedRefusal checks err is the refusal of the pair: the sentence in its text,
// and the typed error under whatever wrapper the kind adds.
func foldedRefusal(t *testing.T, err error, later, earlier string) {
	t.Helper()
	if err == nil {
		t.Fatalf("built %s and %s together; one value would be dropped without a message", later, earlier)
	}
	if want := foldedSentence(later, earlier); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	var folded *builtin.FoldedFieldError
	if !errors.As(err, &folded) {
		t.Fatalf("error %q holds no *FoldedFieldError", err)
	}
	if folded.Later != later || folded.Earlier != earlier {
		t.Errorf("paths = (%q, %q), want (%q, %q)", folded.Later, folded.Earlier, later, earlier)
	}
}

// TestRawKinds_RefuseTwoSpellingsOfOneField: suspend authored as suspend and
// Suspend, with different values.
func TestRawKinds_RefuseTwoSpellingsOfOneField(t *testing.T) {
	for _, k := range foldedKinds() {
		t.Run(k.name, func(t *testing.T) {
			minimal := k.minimal(t)
			if err := k.build(minimal); err != nil {
				t.Fatalf("control: the minimal document is refused: %v", err)
			}
			if err := k.build(foldedWith(minimal, map[string]any{"Suspend": true})); err != nil {
				t.Fatalf("control: Suspend alone is refused: %v", err)
			}
			if err := k.build(foldedWith(minimal, map[string]any{"suspend": nil, "Suspend": true})); err != nil {
				t.Fatalf("control: a null suspend beside Suspend is refused: %v", err)
			}
			if err := k.build(foldedWith(minimal, map[string]any{"suspend": true, "Suspend": nil})); err != nil {
				t.Fatalf("control: suspend beside a null Suspend is refused: %v", err)
			}
			err := k.build(foldedWith(minimal, map[string]any{"suspend": true, "Suspend": false}))
			foldedRefusal(t, err, "suspend", "Suspend")
		})
	}
}

// TestRawKinds_RefuseTwoSpellingsBelowTheTop: a pair at depth, in an object
// authored under both spellings, and in a list element.
func TestRawKinds_RefuseTwoSpellingsBelowTheTop(t *testing.T) {
	git := func(t *testing.T) map[string]any {
		return map[string]any{"url": "https://git.example.com/app.git"}
	}
	build := func(props map[string]any) error {
		_, err := (&components.GitRepositoryHandler{}).ToApplicationConfig(&oam.Component{Name: "src", Type: "gitrepository", Properties: props}, "demo")
		return err
	}
	hr := func(props map[string]any) error {
		_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(&oam.Component{Name: "web", Type: "helmrelease", Properties: props}, "demo")
		return err
	}
	t.Run("gitrepository ref.branch and ref.Branch", func(t *testing.T) {
		if err := build(foldedWith(git(t), map[string]any{"ref": map[string]any{"branch": "main"}})); err != nil {
			t.Fatalf("control: one spelling is refused: %v", err)
		}
		err := build(foldedWith(git(t), map[string]any{"ref": map[string]any{"branch": "main", "Branch": "dev"}}))
		foldedRefusal(t, err, "ref.branch", "ref.Branch")
	})
	t.Run("gitrepository ref and Ref", func(t *testing.T) {
		err := build(foldedWith(git(t), map[string]any{
			"ref": map[string]any{"branch": "main"},
			"Ref": map[string]any{"branch": "dev"},
		}))
		foldedRefusal(t, err, "ref", "Ref")
	})
	t.Run("gitrepository url and URL", func(t *testing.T) {
		err := build(map[string]any{"url": "https://git.example.com/app.git", "URL": "https://git.example.com/other.git"})
		foldedRefusal(t, err, "url", "URL")
	})
	t.Run("helmrelease valuesFrom[0].name and Name", func(t *testing.T) {
		props := func(entry map[string]any) map[string]any {
			return map[string]any{"chart": hrChart(), "valuesFrom": []any{entry}}
		}
		if err := hr(props(map[string]any{"kind": "ConfigMap", "name": "a"})); err != nil {
			t.Fatalf("control: one spelling is refused: %v", err)
		}
		err := hr(props(map[string]any{"kind": "ConfigMap", "name": "a", "Name": "b"}))
		foldedRefusal(t, err, "valuesFrom[0].name", "valuesFrom[0].Name")
		// The caller's wrapper stays in front of the sentence.
		if want := "helmrelease: properties do not decode as a HelmReleaseSpec: " + foldedSentence("valuesFrom[0].name", "valuesFrom[0].Name"); err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
	})
}

// TestKindDecode_TwoSpellingsKeepTheBareSentence: a kind whose decode goes
// through decodeKindSpec returns the sentence alone, with no "properties do
// not decode" prefix, as it did when the kind's own guard found the pair. This
// row passes before the change too: it keeps the text of the guarded kinds.
func TestKindDecode_TwoSpellingsKeepTheBareSentence(t *testing.T) {
	props := map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers":       []any{map[string]any{"name": "app", "image": "ghcr.io/example/app:v1"}},
		"hostnameOverride": "a.example.com",
		"HostnameOverride": "b.example.com",
	}}}
	_, err := (&components.PodTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "web", Type: "podtemplate", Properties: props}, "demo")
	foldedRefusal(t, err, "template.spec.hostnameOverride", "template.spec.HostnameOverride")
	if strings.Contains(err.Error(), "properties do not decode") {
		t.Errorf("error %q carries the decode prefix, want the sentence alone", err)
	}
}

// TestKindDecode_AnUnknownNullKeyComesBeforeThePair: a key the type does not
// declare, authored as null, is refused by name, also when the same document
// holds two spellings of one field. The null strip hides that key from the
// first decode, which would end on the pair. Before the pair was refused the
// unknown key was the error, so it stays the first one. Both places that
// decode the stripped tree and then the raw one are covered: decodeKindSpec
// (podtemplate) and the cnpg-cluster handler.
func TestKindDecode_AnUnknownNullKeyComesBeforeThePair(t *testing.T) {
	podTemplate := func(pair, typo bool) error {
		spec := map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": "ghcr.io/example/app:v1"}},
		}
		if pair {
			spec["hostnameOverride"] = "a.example.com"
			spec["HostnameOverride"] = "b.example.com"
		}
		if typo {
			spec["typo"] = nil
		}
		props := map[string]any{"template": map[string]any{"spec": spec}}
		_, err := (&components.PodTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "web", Type: "podtemplate", Properties: props}, "demo")
		return err
	}
	cluster := func(pair, typo bool) error {
		storage := map[string]any{"size": "1Gi"}
		if pair {
			storage["Size"] = "2Gi"
		}
		if typo {
			storage["typo"] = nil
		}
		_, err := (&components.CnpgClusterHandler{}).ToApplicationConfig(&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: map[string]any{"storage": storage}}, "data")
		return err
	}
	kinds := []struct {
		name  string
		build func(pair, typo bool) error
	}{
		{"podtemplate", podTemplate},
		{"cnpg-cluster", cluster},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			if err := k.build(false, false); err != nil {
				t.Fatalf("control: the document with neither is refused: %v", err)
			}
			err := k.build(false, true)
			if err == nil || !strings.Contains(err.Error(), `unknown field "typo"`) {
				t.Fatalf("control: an unknown null key alone: error = %v, want unknown field \"typo\"", err)
			}
			err = k.build(true, false)
			if err == nil {
				t.Fatalf("control: the pair alone was built")
			}
			var folded *builtin.FoldedFieldError
			if !errors.As(err, &folded) {
				t.Fatalf("control: the pair alone: error %q holds no *FoldedFieldError", err)
			}
			err = k.build(true, true)
			if err == nil || !strings.Contains(err.Error(), `unknown field "typo"`) {
				t.Fatalf("pair and unknown null key: error = %v, want unknown field \"typo\" first", err)
			}
			if errors.As(err, &folded) {
				t.Errorf("pair and unknown null key: error %q is the pair, want the unknown key", err)
			}
		})
	}
}
