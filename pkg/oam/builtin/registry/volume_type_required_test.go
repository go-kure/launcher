package registry_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// A volume's `type` is required (go-kure/launcher#1626): parseVolumes refuses an
// entry without one, so the published schema says so, for each of the six kinds
// that share the volumes declaration (the `deployment` kind takes the upstream
// `volumes` since go-kure/launcher#1629, and the declaration its reshaped form
// had is the role member's, which `worker` and `webservice` publish: the member
// itself is read from the schema, as no document can author it). The cases go through the property
// validation that runs before any handler, which is what a consumer of the
// published schema relies on, and each has a control: the same volume with a type
// passes.
func TestVolumeTypeIsRequired(t *testing.T) {
	kinds := map[string]map[string]any{
		"worker":      {"image": "ghcr.io/org/app:v1"},
		"webservice":  {"image": "ghcr.io/org/app:v1"},
		"statefulset": {"image": "ghcr.io/org/app:v1"},
		"daemonset":   {"image": "ghcr.io/org/app:v1"},
	}
	tr := newTransformer()
	published := tr.HandlerSchemas().Components
	t.Run("role-deployment", func(t *testing.T) {
		volumes, ok := published["role-deployment"]["volumes"]
		if !ok || volumes.Items == nil || !volumes.Items.Properties["type"].Required {
			t.Errorf("the role member publishes no volumes array whose type is Required: %+v", volumes)
		}
	})
	for kind, base := range kinds {
		t.Run(kind, func(t *testing.T) {
			volumes, ok := published[kind]["volumes"]
			if !ok || volumes.Items == nil {
				t.Fatalf("%s publishes no volumes array", kind)
			}
			if !volumes.Items.Properties["type"].Required {
				t.Errorf("%s: a volume's type is not declared Required, though a volume without one is refused", kind)
			}
			validate := func(volume map[string]any) error {
				props := map[string]any{"volumes": []any{volume}}
				for k, v := range base {
					props[k] = v
				}
				_, err := tr.ValidateAuthoredProperties(&oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
					{Name: "app", Type: kind, Properties: props},
				}}})
				return err
			}
			if err := validate(map[string]any{"name": "data", "type": "emptyDir", "mountPath": "/data"}); err != nil {
				t.Fatalf("%s: a volume with a type was refused: %v", kind, err)
			}
			err := validate(map[string]any{"name": "data", "mountPath": "/data"})
			if err == nil || !strings.Contains(err.Error(), "type") {
				t.Errorf("%s: a volume without a type must be refused naming type, got: %v", kind, err)
			}
		})
	}
}
