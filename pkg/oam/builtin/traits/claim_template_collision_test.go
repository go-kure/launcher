package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestMountDecorators_RefuseANameTakenByAClaimTemplate: a configmap or
// external-secret volume named like a filesystem claim template is refused. A
// claim template has no entry in the pod template's Volumes — only the main
// container's mount — so a name check against Volumes alone misses it, and the
// StatefulSet controller then replaces the trait's volume with the claim: the
// ConfigMap or Secret is never mounted and nothing reports it.
func TestMountDecorators_RefuseANameTakenByAClaimTemplate(t *testing.T) {
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumeClaimTemplates": []any{map[string]any{
				"name": "data", "size": "1Gi", "mountPath": "/data",
			}},
		},
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
}
