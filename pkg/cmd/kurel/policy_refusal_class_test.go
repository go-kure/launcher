package kurel

import (
	"errors"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestHelmSecretValues_RefusalClass: through the whole built-in pipeline, a
// policy that forbids explicit secrets refuses a helm component's secretValues
// with the explicit-secret class under either delivery (go-kure/launcher#849).
// Under delivery: flux the refusal is the one of the secret trait the helm rule
// adds, on its sub-application; under delivery: template it is helmtemplate's
// own. A consumer reads one class for both.
func TestHelmSecretValues_RefusalClass(t *testing.T) {
	template := func(p map[string]any) {
		p["delivery"] = "template"
		// Nothing listens here: the refusal comes before any fetch.
		p["source"] = map[string]any{"url": "http://127.0.0.1:1"}
		delete(p, "valuesFrom")
	}
	cases := map[string]oam.Component{
		"flux delivery":     helmSecretComponent(secretSentinel, nil),
		"template delivery": helmSecretComponent(secretSentinel, template),
	}
	for name, comp := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := generateSecretApp("", noExplicitSecrets{&oam.NoopPolicy{}}, comp)
			assertNoSentinel(t, err)
			assertViolation(t, err, comp.Name)
			var v *oam.ViolationError
			if errors.As(err, &v) && v.Class != oam.RefusalExplicitSecret {
				t.Errorf("violation has class %q, want %q: %v", v.Class, oam.RefusalExplicitSecret, err)
			}
		})
	}
}
