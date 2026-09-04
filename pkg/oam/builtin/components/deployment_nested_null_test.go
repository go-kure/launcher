package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind's null-as-omission normalization is top-level: it strips explicit
// nulls from the component's own property map before parsing, and stops there.
// A null one level down — `securityContext: {runAsUser: null}` — still reaches
// a shared nested parser's type check and is refused, which is the gap
// go-kure/launcher#394 owns for every kind at once.
//
// These two cases pin the boundary from both sides, and the second is the
// reason the gap is not closed here by making the strip recursive: a recursive
// pre-strip would remove `bogusKey: null` from the nested map before
// parseSecurityContext's own rejectUnknownKeys ever sees it, turning a named
// refusal into silence. The correct fix therefore sits *inside* each nested
// parser, after its unknown-key rejection — shared code that all six workload
// kinds run, which is #394's scope and not this kind's to change alone.
//
// When #394 lands, the first case flips from refused to accepted and this test
// is the thing that says so.
func TestDeploymentHandler_NestedNullIsStillRefused(t *testing.T) {
	convert := func(t *testing.T, props map[string]any) error {
		t.Helper()
		h := &components.DeploymentHandler{}
		_, err := h.ToApplicationConfig(&oam.Component{
			Name: "app", Type: "deployment", Properties: props,
		}, "default")
		return err
	}

	t.Run("a null under a known nested key is refused, pending #394", func(t *testing.T) {
		err := convert(t, map[string]any{
			"image":           "nginx:1.27",
			"securityContext": map[string]any{"runAsUser": nil},
		})
		if err == nil {
			t.Fatal("expected the nested null to be refused; if go-kure/launcher#394 has landed, this test and the README's scope note both need updating")
		}
		if !strings.Contains(err.Error(), "securityContext.runAsUser") {
			t.Errorf("error should name the nested field, got %v", err)
		}
	})

	t.Run("a null under an unknown nested key is still an unknown-key refusal", func(t *testing.T) {
		// This is what a recursive pre-strip would break, so it is the case
		// that keeps the gap from being closed the wrong way.
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
}
