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

// TestMountDecorators_RefuseANameTakenByAnUnbackedBlockDevice: a container's
// volumeDevices entry (any container) or the main container's volumeMounts
// entry names a volume even when the pod has no volume or claim template of
// that name, so a decorator volume of the same name is refused by that name
// alone. A later container's mount is accepted: it may mount the volume a trait
// adds.
func TestMountDecorators_RefuseANameTakenByAnUnbackedBlockDevice(t *testing.T) {
	device := map[string]any{"name": "disk", "devicePath": "/dev/xvda"}
	mount := map[string]any{"name": "disk", "mountPath": "/data"}
	image := "ghcr.io/org/app:v1"
	for shape, tc := range map[string]struct {
		containers []any
		want       string
	}{
		"device in the first container":  {[]any{map[string]any{"name": "app", "image": image, "volumeDevices": []any{device}}}, "as a raw block device"},
		"device in the second container": {[]any{map[string]any{"name": "app", "image": image}, map[string]any{"name": "side", "image": image, "volumeDevices": []any{device}}}, "as a raw block device"},
		"mount in the first container":   {[]any{map[string]any{"name": "app", "image": image, "volumeMounts": []any{mount}}}, "as a volume mount"},
		// A mount in a later container is accepted: a sidecar may mount the
		// volume a trait adds. The claim template check is what covers a claim.
		"mount in the second container": {[]any{map[string]any{"name": "app", "image": image}, map[string]any{"name": "side", "image": image, "volumeMounts": []any{mount}}}, ""},
	} {
		containers := tc.containers
		cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{
			Name: "app", Type: "deployment",
			Properties: rawDeploymentProps("app", "ghcr.io/org/app:v1", rawPod(map[string]any{"containers": containers})),
		}, "default")
		if err != nil {
			t.Fatalf("%s: ToApplicationConfig: %v", shape, err)
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
			t.Run(shape+"/"+name, func(t *testing.T) {
				err := run()
				if tc.want == "" {
					if err != nil {
						t.Errorf("err = %v, want a later container's mount accepted", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), `volume "disk" already exists on the workload `+tc.want) {
					t.Errorf("err = %v, want the name refused %s", err, tc.want)
				}
			})
		}
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
		// raw deployment, statefulset, daemonset, job and cronjob take the upstream
		// pod template, whose block device is a claim volume of the pod.
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
	// A raw statefulset's claim template is a block device when the claim's
	// volumeMode is Block and a container lists it in volumeDevices.
	blockClaim := rawClaimTemplate("disk", "1Gi")
	blockClaim["spec"].(map[string]any)["volumeMode"] = "Block"
	rawClaimBlock := rawStatefulsetProps("app", "ghcr.io/org/app:v1", rawPod(map[string]any{
		"containers": []any{map[string]any{
			"name": "app", "image": "ghcr.io/org/app:v1",
			"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/xvda"}},
		}},
	}), map[string]any{"volumeClaimTemplates": []any{blockClaim}})
	sources := []source{
		{"webservice", webserviceViaRule{}, blockPVC, false},
		{"worker", workerViaRule{}, blockPVC, false},
		{"role-deployment", &components.RoleDeploymentHandler{}, blockPVC, false},
		{"deployment", &components.DeploymentHandler{}, rawBlock, true},
		{"statefulset", &components.StatefulsetHandler{}, rawBlock, true},
		{"daemonset", &components.DaemonsetHandler{}, rawBlock, true},
		{"job", &components.JobHandler{}, rawJobProps("ghcr.io/org/app:v1", rawPod(blockPod)), true},
		{"cronjob", &components.CronjobHandler{}, rawCronjobProps("ghcr.io/org/app:v1", blockPod), true},
		{"statefulset claim template", &components.StatefulsetHandler{}, rawClaimBlock, true},
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
