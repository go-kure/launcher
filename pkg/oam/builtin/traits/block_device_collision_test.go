package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestMountDecorators_RefuseAPathTakenByABlockDevice: a configmap or
// external-secret mount at a path the main container already uses as a raw
// block devicePath is refused. ValidateVolumeDevices rejects a devicePath that
// is also a mountPath in the same container, and checkMountPathCollision
// previously looked at VolumeMounts only (go-kure/launcher#385).
func TestMountDecorators_RefuseAPathTakenByABlockDevice(t *testing.T) {
	cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "deployment",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumes": []any{map[string]any{
				"name": "disk", "type": "pvc", "size": "1Gi",
				"devicePath": "/dev/xvda", "volumeMode": "Block",
			}},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	cases := map[string]func() error{
		"configmap": func() error {
			_, err := traits.NewConfigMapDecorator(cfg, "cfg", "/dev/xvda").Generate(newApp("app", "default"))
			return err
		},
		"external-secret": func() error {
			_, err := traits.NewExternalSecretDecorator(cfg, "creds", "/dev/xvda", false).Generate(newApp("app", "default"))
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("expected a mount at the block device's path to be refused")
			}
			if !strings.Contains(err.Error(), "/dev/xvda") || !strings.Contains(err.Error(), `devicePath of block volume "disk"`) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
