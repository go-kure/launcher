package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Null-as-omission now reaches nested keys, not only the component's own
// top-level property map. `securityContext: {runAsUser: null}` builds exactly
// as omitting runAsUser does, because the shared field helpers read a null as
// absence (authoredValue, common.go) — go-kure/launcher#394.
//
// The second case is why the fix sits INSIDE each nested parser rather than in
// a recursive pre-strip over the property map. A recursive strip would remove
// `bogusKey: null` before parseSecurityContext's rejectUnknownKeys ever saw it,
// turning a named refusal into silence — trading one silent-drop bug for
// another. Running after unknown-key rejection keeps both answers: an
// unrecognized key is still named, a recognized key authored as null is still
// absence.
//
// These two cases pin the boundary from both sides. The first flipped from
// refused to accepted when go-kure/launcher#394 landed; the second must never
// flip, and is the control that the null handling did not widen into "any key
// I do not understand is absent".
func TestDeploymentHandler_NestedNull(t *testing.T) {
	convert := func(t *testing.T, props map[string]any) error {
		t.Helper()
		h := &components.DeploymentHandler{}
		_, err := h.ToApplicationConfig(&oam.Component{
			Name: "app", Type: "deployment", Properties: props,
		}, "default")
		return err
	}

	t.Run("a null under a known nested key is absence", func(t *testing.T) {
		if err := convert(t, map[string]any{
			"image":           "nginx:1.27",
			"securityContext": map[string]any{"runAsUser": nil},
		}); err != nil {
			t.Fatalf("a null under a known nested key must read as omission, got %v", err)
		}
	})

	t.Run("a nested null builds identically to omitting the key", func(t *testing.T) {
		// Absence and null must not merely both succeed — they must produce
		// the same thing. Two parsers that accept the same document and emit
		// different objects is the divergence go-kure/launcher#394 was about,
		// one level down from where it was reported.
		h := &components.DeploymentHandler{}
		build := func(t *testing.T, sc map[string]any) any {
			t.Helper()
			cfg, err := h.ToApplicationConfig(&oam.Component{
				Name: "app", Type: "deployment",
				Properties: map[string]any{"image": "nginx:1.27", "securityContext": sc},
			}, "default")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			return cfg
		}
		withNull := build(t, map[string]any{"runAsUser": nil})
		withAbsent := build(t, map[string]any{})
		if !reflect.DeepEqual(withNull, withAbsent) {
			t.Errorf("a null runAsUser produced a different config than omitting it:\n null:   %+v\n absent: %+v",
				withNull, withAbsent)
		}
	})

	t.Run("a null under an unknown nested key is still an unknown-key refusal", func(t *testing.T) {
		// The control. This is what a recursive pre-strip would have broken,
		// and it is the reason the fix went inside the parsers instead.
		err := convert(t, map[string]any{
			"image":           "nginx:1.27",
			"securityContext": map[string]any{"bogusKey": nil},
		})
		if err == nil {
			t.Fatal("expected an unknown-key refusal")
		}
		if !strings.Contains(err.Error(), `unrecognized key "bogusKey"`) {
			t.Errorf("error should name the unrecognized key, got %v", err)
		}
	})

	t.Run("a nested wrong type is still refused by name", func(t *testing.T) {
		// The other control: null became absence, but a present, non-null
		// value of the wrong type must still earn the error it always did.
		err := convert(t, map[string]any{
			"image":           "nginx:1.27",
			"securityContext": map[string]any{"runAsUser": "root"},
		})
		if err == nil {
			t.Fatal("expected a wrongly-typed runAsUser to be refused")
		}
		if !strings.Contains(err.Error(), "runAsUser") {
			t.Errorf("error should name the field, got %v", err)
		}
	})
}
