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
	// The block device on the main container is the reshaped form's (a volume with
	// a devicePath), which the deployment kind no longer takes (go-kure/launcher#1629):
	// the role member holds it.
	cfg, err := (&components.RoleDeploymentHandler{}).ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "role-deployment",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumes": []any{map[string]any{
				"name": "disk", "type": "pvc", "claimName": "disk",
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
		"name": "disk", "type": "pvc", "claimName": "disk", "devicePath": "/dev/xvda", "volumeMode": "Block",
	}}}
	type source struct {
		kind    string
		handler interface {
			ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
		}
		props map[string]any
		// exact says props are the whole properties, with no image added: the
		// raw deployment, daemonset, job and cronjob take the upstream pod template,
		// whose block device is a claim volume of the pod.
		exact bool
	}
	blockPod := map[string]any{
		"containers": []any{map[string]any{
			"name": "app", "image": "ghcr.io/org/app:v1",
			"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/xvda"}},
		}},
		"volumes": []any{
			map[string]any{"name": "disk", "persistentVolumeClaim": map[string]any{"claimName": "disk"}},
		},
	}
	rawBlock := rawDeploymentProps("app", "ghcr.io/org/app:v1", rawPod(blockPod))
	sources := []source{
		{"webservice", webserviceViaRule{}, blockPVC, false},
		{"worker", workerViaRule{}, blockPVC, false},
		{"role-deployment", &components.RoleDeploymentHandler{}, blockPVC, false},
		{"deployment", &components.DeploymentHandler{}, rawBlock, true},
		{"statefulset", &components.StatefulsetHandler{}, blockPVC, false},
		{"daemonset", &components.DaemonsetHandler{}, rawBlock, true},
		{"job", &components.JobHandler{}, rawJobProps("ghcr.io/org/app:v1", rawPod(blockPod)), true},
		{"cronjob", &components.CronjobHandler{}, rawCronjobProps("ghcr.io/org/app:v1", blockPod), true},
		{"statefulset claim template", &components.StatefulsetHandler{}, map[string]any{"volumeClaimTemplates": []any{map[string]any{
			"name": "disk", "size": "1Gi", "devicePath": "/dev/xvda", "volumeMode": "Block",
		}}}, false},
	}
	for _, src := range sources {
		kind := strings.TrimSuffix(src.kind, " claim template")
		props := mergeBlockProps(map[string]any{"image": "ghcr.io/org/app:v1"}, src.props)
		if src.exact {
			props = src.props
		}
		cfg, err := src.handler.ToApplicationConfig(&oam.Component{
			Name: "app", Type: kind,
			Properties: props,
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
