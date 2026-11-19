package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestMountDecorators_RefuseANameTakenByAClaimTemplate: a configmap or
// external-secret volume named like a claim template is refused. A claim
// template has no entry in the pod template's Volumes, so a name check against
// Volumes alone misses it, and the StatefulSet controller then replaces the
// trait's volume with the claim: the ConfigMap or Secret is never mounted and
// nothing reports it. The refusal reads the claim template names of the
// StatefulSet itself, so it holds whichever container mounts the claim, and when
// none does.
func TestMountDecorators_RefuseANameTakenByAClaimTemplate(t *testing.T) {
	container := func(name string, mounts ...any) map[string]any {
		c := map[string]any{"name": name, "image": "ghcr.io/org/app:v1"}
		if len(mounts) > 0 {
			c["volumeMounts"] = mounts
		}
		return c
	}
	dataMount := map[string]any{"name": "data", "mountPath": "/data"}
	shapes := map[string][]any{
		"mounted by the first container":  {container("app", dataMount)},
		"mounted by the second container": {container("app"), container("side", dataMount)},
		"mounted by no container":         {container("app")},
	}
	for shape, containers := range shapes {
		t.Run(shape, func(t *testing.T) {
			cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
				Name: "app",
				Type: "statefulset",
				Properties: rawStatefulsetProps("app", "ghcr.io/org/app:v1", rawPod(map[string]any{
					"containers": containers,
				}), map[string]any{
					"volumeClaimTemplates": []any{rawClaimTemplate("data", "1Gi")},
				}),
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			cases := map[string]func() error{
				"configmap": func() error {
					_, err := traits.NewConfigMapDecorator(cfg, "data", "/etc/cfg").Generate(newApp("app", "default"))
					return err
				},
				"external-secret": func() error {
					_, err := traits.NewExternalSecretDecorator(cfg, "data", "/etc/creds", false).Generate(newApp("app", "default"))
					return err
				},
			}
			for name, run := range cases {
				t.Run(name, func(t *testing.T) {
					err := run()
					if err == nil {
						t.Fatal("expected a volume named like the claim template to be refused")
					}
					if !strings.Contains(err.Error(), `volume "data" already exists on the workload as a claim template`) {
						t.Errorf("unexpected error: %v", err)
					}
				})
			}
		})
	}
}
