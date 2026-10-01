package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

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

// TestMountDecorators_RefuseANameTakenByABlockDevice: a configmap or
// external-secret volume named like a Block volume is refused on every
// workload kind, whether the Block volume is a `volumes[].pvc` entry or a
// statefulset claim template. A claim template has no entry in the pod
// template's Volumes, so a name check against Volumes alone misses it, and the
// main container would then both mount and attach the one name — which
// ValidateVolumeDevices rejects (go-kure/launcher#385).
func TestMountDecorators_RefuseANameTakenByABlockDevice(t *testing.T) {
	blockPVC := map[string]any{"volumes": []any{map[string]any{
		"name": "disk", "type": "pvc", "size": "1Gi", "devicePath": "/dev/xvda", "volumeMode": "Block",
	}}}
	type source struct {
		kind    string
		handler interface {
			ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
		}
		props map[string]any
	}
	sources := []source{
		{"webservice", webserviceViaRule{}, blockPVC},
		{"worker", workerViaRule{}, blockPVC},
		{"deployment", &components.DeploymentHandler{}, blockPVC},
		{"statefulset", &components.StatefulsetHandler{}, blockPVC},
		{"daemonset", &components.DaemonsetHandler{}, blockPVC},
		{"job", &components.JobHandler{}, blockPVC},
		{"cronjob", &components.CronjobHandler{}, mergeBlockProps(blockPVC, map[string]any{"schedule": "0 2 * * *"})},
		{"statefulset claim template", &components.StatefulsetHandler{}, map[string]any{"volumeClaimTemplates": []any{map[string]any{
			"name": "disk", "size": "1Gi", "devicePath": "/dev/xvda", "volumeMode": "Block",
		}}}},
	}
	for _, src := range sources {
		kind := strings.TrimSuffix(src.kind, " claim template")
		cfg, err := src.handler.ToApplicationConfig(&oam.Component{
			Name: "app", Type: kind,
			Properties: mergeBlockProps(map[string]any{"image": "ghcr.io/org/app:v1"}, src.props),
		}, "default")
		if err != nil {
			t.Fatalf("%s: ToApplicationConfig: %v", src.kind, err)
		}
		decorators := map[string]func() error{
			"configmap": func() error {
				_, err := traits.NewConfigMapDecorator(cfg, "disk", "/etc/cfg").Generate(newApp("app", "default"))
				return err
			},
			"external-secret": func() error {
				_, err := traits.NewExternalSecretDecorator(cfg, "disk", "/etc/creds", false).Generate(newApp("app", "default"))
				return err
			},
		}
		for name, run := range decorators {
			t.Run(src.kind+"/"+name, func(t *testing.T) {
				err := run()
				if err == nil {
					t.Fatal("expected a volume named like the block device to be refused")
				}
				if !strings.Contains(err.Error(), `volume "disk" already exists on the workload`) {
					t.Errorf("unexpected error: %v", err)
				}
			})
		}
	}
}

func mergeBlockProps(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
