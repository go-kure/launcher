package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Raw block volumes (go-kure/launcher#385). A `volumes[].pvc` entry and a
// `volumeClaimTemplates` entry take exactly one of `mountPath`/`devicePath`,
// `devicePath` holds exactly when `volumeMode: Block` is authored, and a device
// reaches the main container as a corev1.VolumeDevice rather than a
// VolumeMount. Init containers and sidecars address a Block volume through
// their own `volumeDevices`, never through `volumeMounts`.
//
// Every workload kind with a `volumes` property runs the same cases: the rule
// lives in the shared parsers, and a kind that forgot to wire the devices into
// its main container would otherwise build a pod whose claim is Block and
// whose container never sees it.

type blockKind struct {
	kind     string
	handler  sharedKindHandler
	base     map[string]any
	sidecars bool
}

func blockKinds() []blockKind {
	return []blockKind{
		{"webservice", &components.WebserviceHandler{}, map[string]any{}, true},
		{"worker", &components.WorkerHandler{}, map[string]any{}, true},
		{"deployment", &components.DeploymentHandler{}, map[string]any{}, true},
		{"statefulset", &components.StatefulsetHandler{}, map[string]any{}, true},
		{"daemonset", &components.DaemonsetHandler{}, map[string]any{}, false},
		{"job", &components.JobHandler{}, map[string]any{}, false},
		{"cronjob", &components.CronjobHandler{}, map[string]any{"schedule": "0 2 * * *"}, false},
	}
}

// props returns a fresh property map: the kind's own required keys, an image,
// and extra on top.
func (k blockKind) props(extra map[string]any) map[string]any {
	p := map[string]any{"image": "ghcr.io/org/app:v1"}
	for key, v := range k.base {
		p[key] = v
	}
	for key, v := range extra {
		p[key] = v
	}
	return p
}

func (k blockKind) configure(t *testing.T, extra map[string]any) (stack.ApplicationConfig, error) {
	t.Helper()
	return k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.kind, Properties: k.props(extra)}, "default")
}

// generated is the part of a workload's output these tests read.
type generated struct {
	pod    *corev1.PodSpec
	claims []*corev1.PersistentVolumeClaim
	vcts   []corev1.PersistentVolumeClaim
}

func (k blockKind) generate(t *testing.T, extra map[string]any) generated {
	t.Helper()
	cfg, err := k.configure(t, extra)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return collectGenerated(t, objects)
}

func collectGenerated(t *testing.T, objects []*client.Object) generated {
	t.Helper()
	var g generated
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *appsv1.Deployment:
			g.pod = &o.Spec.Template.Spec
		case *appsv1.StatefulSet:
			g.pod = &o.Spec.Template.Spec
			g.vcts = o.Spec.VolumeClaimTemplates
		case *appsv1.DaemonSet:
			g.pod = &o.Spec.Template.Spec
		case *batchv1.Job:
			g.pod = &o.Spec.Template.Spec
		case *batchv1.CronJob:
			g.pod = &o.Spec.JobTemplate.Spec.Template.Spec
		case *corev1.PersistentVolumeClaim:
			g.claims = append(g.claims, o)
		}
	}
	if g.pod == nil {
		t.Fatal("no workload pod template in the output")
	}
	return g
}

func blockPVCVolume(extra map[string]any) map[string]any {
	v := map[string]any{"name": "disk", "type": "pvc", "size": "1Gi", "accessModes": []any{"ReadWriteMany"}}
	for key, val := range extra {
		v[key] = val
	}
	return v
}

func containerByName(pod *corev1.PodSpec, name string) *corev1.Container {
	for i := range pod.InitContainers {
		if pod.InitContainers[i].Name == name {
			return &pod.InitContainers[i]
		}
	}
	for i := range pod.Containers {
		if pod.Containers[i].Name == name {
			return &pod.Containers[i]
		}
	}
	return nil
}

func hasMountNamed(c *corev1.Container, name string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return true
		}
	}
	return false
}

func wantErrContaining(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestRawBlock_PVCVolume_RendersVolumeDevice: a Block pvc volume with a
// devicePath reaches the main container as a VolumeDevice, no VolumeMount of
// that name exists, and the emitted claim keeps volumeMode: Block.
func TestRawBlock_PVCVolume_RendersVolumeDevice(t *testing.T) {
	for _, k := range blockKinds() {
		t.Run(k.kind, func(t *testing.T) {
			g := k.generate(t, map[string]any{"volumes": []any{
				blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
			}})
			main := g.pod.Containers[0]
			if len(main.VolumeDevices) != 1 || main.VolumeDevices[0] != (corev1.VolumeDevice{Name: "disk", DevicePath: "/dev/xvda"}) {
				t.Errorf("main container VolumeDevices = %+v, want [{disk /dev/xvda}]", main.VolumeDevices)
			}
			if hasMountNamed(&main, "disk") {
				t.Errorf("main container also mounts the block volume as a filesystem: %+v", main.VolumeMounts)
			}
			if len(g.claims) != 1 {
				t.Fatalf("expected 1 PersistentVolumeClaim, got %d", len(g.claims))
			}
			if vm := g.claims[0].Spec.VolumeMode; vm == nil || *vm != corev1.PersistentVolumeBlock {
				t.Errorf("claim volumeMode = %v, want Block", vm)
			}
		})
	}
}

// TestRawBlock_PVCVolume_FilesystemModeAuthored: an explicit Filesystem mode
// keeps the mount and is written onto the claim; an unauthored mode stays nil,
// so every existing document's claim is byte-identical.
func TestRawBlock_PVCVolume_FilesystemModeAuthored(t *testing.T) {
	for _, k := range blockKinds() {
		t.Run(k.kind, func(t *testing.T) {
			g := k.generate(t, map[string]any{"volumes": []any{
				blockPVCVolume(map[string]any{"mountPath": "/data", "volumeMode": "Filesystem"}),
			}})
			main := g.pod.Containers[0]
			if !hasMountNamed(&main, "disk") || len(main.VolumeDevices) != 0 {
				t.Errorf("want a filesystem mount and no device, got mounts %+v devices %+v", main.VolumeMounts, main.VolumeDevices)
			}
			if vm := g.claims[0].Spec.VolumeMode; vm == nil || *vm != corev1.PersistentVolumeFilesystem {
				t.Errorf("claim volumeMode = %v, want Filesystem", vm)
			}

			g = k.generate(t, map[string]any{"volumes": []any{blockPVCVolume(map[string]any{"mountPath": "/data"})}})
			if vm := g.claims[0].Spec.VolumeMode; vm != nil {
				t.Errorf("unauthored volumeMode rendered as %v, want nil", *vm)
			}
		})
	}
}

// TestRawBlock_PVCVolume_Rejections: exactly one of mountPath/devicePath, and
// devicePath if and only if Block. Nothing is inferred from either side.
func TestRawBlock_PVCVolume_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		volume  map[string]any
		wantErr string
	}{
		{"mountPath with Block", blockPVCVolume(map[string]any{"mountPath": "/data", "volumeMode": "Block"}), "volumeMode Block is consumed through devicePath"},
		{"devicePath with Filesystem", blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Filesystem"}), "devicePath requires volumeMode: Block"},
		{"devicePath without a mode", blockPVCVolume(map[string]any{"devicePath": "/dev/xvda"}), "devicePath requires volumeMode: Block"},
		{"both paths", blockPVCVolume(map[string]any{"mountPath": "/data", "devicePath": "/dev/xvda", "volumeMode": "Block"}), "mountPath and devicePath are mutually exclusive"},
		{"neither path", blockPVCVolume(map[string]any{"volumeMode": "Block"}), "mountPath is required"},
		{"unknown mode", blockPVCVolume(map[string]any{"mountPath": "/data", "volumeMode": "Raw"}), `volumeMode: invalid value "Raw"`},
		{"devicePath with backsteps", blockPVCVolume(map[string]any{"devicePath": "/dev/../xvda", "volumeMode": "Block"}), "must not contain '..'"},
		{"devicePath on a non-pvc volume", map[string]any{"name": "scratch", "type": "emptyDir", "devicePath": "/dev/xvda"}, "devicePath is only valid on a pvc volume"},
	}
	for _, k := range blockKinds() {
		for _, tc := range cases {
			t.Run(k.kind+"/"+tc.name, func(t *testing.T) {
				_, err := k.configure(t, map[string]any{"volumes": []any{tc.volume}})
				wantErrContaining(t, err, tc.wantErr)
			})
		}
	}
}

// TestRawBlock_PVCVolume_DevicePathCollisions: a devicePath must be unique in
// the container and must not repeat a mountPath (ValidateVolumeDevices).
func TestRawBlock_PVCVolume_DevicePathCollisions(t *testing.T) {
	for _, k := range blockKinds() {
		t.Run(k.kind+"/duplicate devicePath", func(t *testing.T) {
			second := blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})
			second["name"] = "disk2"
			_, err := k.configure(t, map[string]any{"volumes": []any{
				blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}), second,
			}})
			wantErrContaining(t, err, `duplicate devicePath "/dev/xvda"`)
		})
		t.Run(k.kind+"/devicePath repeats a mountPath", func(t *testing.T) {
			_, err := k.configure(t, map[string]any{"volumes": []any{
				map[string]any{"name": "scratch", "type": "emptyDir", "mountPath": "/dev/xvda"},
				blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
			}})
			wantErrContaining(t, err, `"/dev/xvda" is already a mountPath`)
		})
	}
}

// extraContainers returns the init container (and, where the kind has them,
// the sidecar) entries carrying the given volume lists.
func (k blockKind) extraContainers(lists map[string]any) map[string]any {
	entry := func(name string) map[string]any {
		e := map[string]any{"name": name, "image": "ghcr.io/org/helper:v1"}
		for key, v := range lists {
			e[key] = v
		}
		return e
	}
	out := map[string]any{"initContainers": []any{entry("prep")}}
	if k.sidecars {
		out["sidecars"] = []any{entry("side")}
	}
	return out
}

func (k blockKind) extraNames() []string {
	if k.sidecars {
		return []string{"prep", "side"}
	}
	return []string{"prep"}
}

func mergeProps(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for key, v := range m {
			out[key] = v
		}
	}
	return out
}

// TestRawBlock_ExtraContainers_VolumeDevices: an init container or sidecar
// consumes a Block volume through its own volumeDevices, checked against the
// component's declared volumes.
func TestRawBlock_ExtraContainers_VolumeDevices(t *testing.T) {
	volumes := map[string]any{"volumes": []any{
		blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
		map[string]any{"name": "scratch", "type": "emptyDir", "mountPath": "/scratch"},
	}}
	for _, k := range blockKinds() {
		t.Run(k.kind+"/device naming a Block volume", func(t *testing.T) {
			g := k.generate(t, mergeProps(volumes, k.extraContainers(map[string]any{
				"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/block0"}},
			})))
			for _, name := range k.extraNames() {
				c := containerByName(g.pod, name)
				if c == nil {
					t.Fatalf("container %q missing", name)
				}
				if len(c.VolumeDevices) != 1 || c.VolumeDevices[0] != (corev1.VolumeDevice{Name: "disk", DevicePath: "/dev/block0"}) {
					t.Errorf("%s VolumeDevices = %+v, want [{disk /dev/block0}]", name, c.VolumeDevices)
				}
			}
		})
		t.Run(k.kind+"/device naming a filesystem volume", func(t *testing.T) {
			_, err := k.configure(t, mergeProps(volumes, k.extraContainers(map[string]any{
				"volumeDevices": []any{map[string]any{"name": "scratch", "devicePath": "/dev/block0"}},
			})))
			wantErrContaining(t, err, `volume "scratch" is not a Block volume`)
		})
		t.Run(k.kind+"/device naming an undeclared volume", func(t *testing.T) {
			_, err := k.configure(t, mergeProps(volumes, k.extraContainers(map[string]any{
				"volumeDevices": []any{map[string]any{"name": "nope", "devicePath": "/dev/block0"}},
			})))
			wantErrContaining(t, err, `volume "nope" is not declared`)
		})
		t.Run(k.kind+"/mount naming a Block volume", func(t *testing.T) {
			_, err := k.configure(t, mergeProps(volumes, k.extraContainers(map[string]any{
				"volumeMounts": []any{map[string]any{"name": "disk", "mountPath": "/mnt/disk"}},
			})))
			wantErrContaining(t, err, `volume "disk" is a Block volume`)
		})
		t.Run(k.kind+"/mount naming an undeclared volume stays accepted", func(t *testing.T) {
			// A trait (configmap, external-secret) adds its volume after the
			// component is generated, so a name the component does not declare
			// cannot be judged here.
			g := k.generate(t, mergeProps(volumes, k.extraContainers(map[string]any{
				"volumeMounts": []any{map[string]any{"name": "from-a-trait", "mountPath": "/etc/trait"}},
			})))
			for _, name := range k.extraNames() {
				if c := containerByName(g.pod, name); c == nil || !hasMountNamed(c, "from-a-trait") {
					t.Errorf("%s lost its mount of an undeclared volume", name)
				}
			}
		})
	}
}

// TestRawBlock_ExtraContainers_VolumeDeviceEntryRules: the entry-level rules
// ValidateVolumeDevices applies to one container.
func TestRawBlock_ExtraContainers_VolumeDeviceEntryRules(t *testing.T) {
	volumes := map[string]any{"volumes": []any{
		blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
	}}
	cases := []struct {
		name    string
		lists   map[string]any
		wantErr string
	}{
		{"missing devicePath", map[string]any{"volumeDevices": []any{map[string]any{"name": "disk"}}}, "devicePath is required"},
		{"missing name", map[string]any{"volumeDevices": []any{map[string]any{"devicePath": "/dev/b"}}}, "name is required"},
		{"unknown key", map[string]any{"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/b", "readOnly": true}}}, `unrecognized key "readOnly"`},
		{"not a list", map[string]any{"volumeDevices": map[string]any{"name": "disk"}}, "volumeDevices: must be"},
		{"duplicate devicePath", map[string]any{"volumeDevices": []any{
			map[string]any{"name": "disk", "devicePath": "/dev/b"},
			map[string]any{"name": "disk2", "devicePath": "/dev/b"},
		}}, `duplicate devicePath "/dev/b"`},
		{"duplicate device name", map[string]any{"volumeDevices": []any{
			map[string]any{"name": "disk", "devicePath": "/dev/a"},
			map[string]any{"name": "disk", "devicePath": "/dev/b"},
		}}, `volume "disk" is already listed under volumeDevices`},
		{"devicePath repeats a mountPath", map[string]any{
			"volumeMounts":  []any{map[string]any{"name": "other", "mountPath": "/dev/b"}},
			"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/b"}},
		}, `"/dev/b" is already a mountPath`},
		{"volume in both lists", map[string]any{
			"volumeMounts":  []any{map[string]any{"name": "disk", "mountPath": "/mnt/disk"}},
			"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/b"}},
		}, `volume "disk" is also listed under volumeMounts`},
		{"devicePath with backsteps", map[string]any{"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/../b"}}}, "must not contain '..'"},
	}
	for _, k := range blockKinds() {
		for _, tc := range cases {
			t.Run(k.kind+"/"+tc.name, func(t *testing.T) {
				_, err := k.configure(t, mergeProps(volumes, k.extraContainers(tc.lists)))
				wantErrContaining(t, err, tc.wantErr)
			})
		}
	}
}

// --- statefulset volumeClaimTemplates ---

func blockVCT(extra map[string]any) map[string]any {
	v := map[string]any{"name": "disk", "size": "1Gi"}
	for key, val := range extra {
		v[key] = val
	}
	return v
}

func statefulsetKind() blockKind {
	for _, k := range blockKinds() {
		if k.kind == "statefulset" {
			return k
		}
	}
	panic("statefulset kind missing")
}

// TestRawBlock_VolumeClaimTemplate_RendersVolumeDevice is the issue's own
// acceptance: a claim template declared as a block device generates a claim
// with volumeMode: Block and a container volumeDevices entry, with no
// volumeMounts entry for that volume.
func TestRawBlock_VolumeClaimTemplate_RendersVolumeDevice(t *testing.T) {
	k := statefulsetKind()
	g := k.generate(t, map[string]any{"volumeClaimTemplates": []any{
		blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
		map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"},
	}})
	main := g.pod.Containers[0]
	if len(main.VolumeDevices) != 1 || main.VolumeDevices[0] != (corev1.VolumeDevice{Name: "disk", DevicePath: "/dev/xvda"}) {
		t.Errorf("VolumeDevices = %+v, want [{disk /dev/xvda}]", main.VolumeDevices)
	}
	if hasMountNamed(&main, "disk") {
		t.Errorf("block claim template also mounted as a filesystem: %+v", main.VolumeMounts)
	}
	if !hasMountNamed(&main, "data") {
		t.Errorf("filesystem claim template lost its mount: %+v", main.VolumeMounts)
	}
	if len(g.vcts) != 2 {
		t.Fatalf("expected 2 claim templates, got %d", len(g.vcts))
	}
	if vm := g.vcts[0].Spec.VolumeMode; vm == nil || *vm != corev1.PersistentVolumeBlock {
		t.Errorf("claim template volumeMode = %v, want Block", vm)
	}
	if vm := g.vcts[1].Spec.VolumeMode; vm != nil {
		t.Errorf("unauthored claim template volumeMode rendered as %v, want nil", *vm)
	}
}

func TestRawBlock_VolumeClaimTemplate_Rejections(t *testing.T) {
	k := statefulsetKind()
	cases := []struct {
		name    string
		entry   map[string]any
		wantErr string
	}{
		{"mountPath with Block", blockVCT(map[string]any{"mountPath": "/data", "volumeMode": "Block"}), "volumeMode Block is consumed through devicePath"},
		{"devicePath with Filesystem", blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Filesystem"}), "devicePath requires volumeMode: Block"},
		{"devicePath without a mode", blockVCT(map[string]any{"devicePath": "/dev/xvda"}), "devicePath requires volumeMode: Block"},
		{"both paths", blockVCT(map[string]any{"mountPath": "/data", "devicePath": "/dev/xvda", "volumeMode": "Block"}), "mountPath and devicePath are mutually exclusive"},
		{"neither path", blockVCT(map[string]any{"volumeMode": "Block"}), "missing required field 'mountPath'"},
		{"devicePath with backsteps", blockVCT(map[string]any{"devicePath": "../xvda", "volumeMode": "Block"}), "must not contain '..'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := k.configure(t, map[string]any{"volumeClaimTemplates": []any{tc.entry}})
			wantErrContaining(t, err, tc.wantErr)
		})
	}
	t.Run("devicePath shared with a pvc volume", func(t *testing.T) {
		_, err := k.configure(t, map[string]any{
			"volumeClaimTemplates": []any{blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})},
			"volumes": []any{map[string]any{
				"name": "other", "type": "pvc", "size": "1Gi", "devicePath": "/dev/xvda", "volumeMode": "Block",
			}},
		})
		wantErrContaining(t, err, `duplicate devicePath "/dev/xvda"`)
	})
	t.Run("devicePath shared with a volumes mountPath", func(t *testing.T) {
		_, err := k.configure(t, map[string]any{
			"volumeClaimTemplates": []any{blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})},
			"volumes":              []any{map[string]any{"name": "scratch", "type": "emptyDir", "mountPath": "/dev/xvda"}},
		})
		wantErrContaining(t, err, `"/dev/xvda" is already a mountPath`)
	})
	t.Run("devicePath shared with a claim template mountPath", func(t *testing.T) {
		_, err := k.configure(t, map[string]any{"volumeClaimTemplates": []any{
			map[string]any{"name": "data", "size": "1Gi", "mountPath": "/dev/xvda"},
			blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
		}})
		wantErrContaining(t, err, `"/dev/xvda" is already a mountPath`)
	})
}

// TestRawBlock_VolumeClaimTemplate_NameCollisions: the main container takes its
// devices and mounts from both the claim templates and `volumes`, each parser
// seeing only its own entries. ValidateVolumeDevices refuses a device name
// listed twice, and one that is also a volumeMounts name, in the same
// container.
func TestRawBlock_VolumeClaimTemplate_NameCollisions(t *testing.T) {
	k := statefulsetKind()
	blockDisk := blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"Block claim template and a volumes mount", map[string]any{
			"volumeClaimTemplates": []any{blockDisk},
			"volumes":              []any{map[string]any{"name": "disk", "type": "emptyDir", "mountPath": "/data"}},
		}, `volume "disk" is both mounted and attached as a device`},
		{"Block claim template and a filesystem claim template", map[string]any{
			"volumeClaimTemplates": []any{blockDisk, map[string]any{"name": "disk", "size": "1Gi", "mountPath": "/data"}},
		}, `volume "disk" is both mounted and attached as a device`},
		{"filesystem claim template and a Block pvc volume", map[string]any{
			"volumeClaimTemplates": []any{map[string]any{"name": "disk", "size": "1Gi", "mountPath": "/data"}},
			"volumes":              []any{blockPVCVolume(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})},
		}, `volume "disk" is both mounted and attached as a device`},
		{"Block claim template and a Block pvc volume", map[string]any{
			"volumeClaimTemplates": []any{blockDisk},
			"volumes":              []any{blockPVCVolume(map[string]any{"devicePath": "/dev/xvdb", "volumeMode": "Block"})},
		}, `volume "disk" is attached as a device more than once`},
		{"two Block claim templates", map[string]any{
			"volumeClaimTemplates": []any{blockDisk, blockVCT(map[string]any{"devicePath": "/dev/xvdb", "volumeMode": "Block"})},
		}, `volume "disk" is attached as a device more than once`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := k.configure(t, tc.props)
			wantErrContaining(t, err, tc.wantErr)
		})
	}
}

// TestRawBlock_VolumeClaimTemplate_ExtraContainers: the name→mode map takes
// the claim templates as well as `volumes`.
func TestRawBlock_VolumeClaimTemplate_ExtraContainers(t *testing.T) {
	k := statefulsetKind()
	vcts := map[string]any{"volumeClaimTemplates": []any{
		blockVCT(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"}),
		map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"},
	}}
	t.Run("device naming a Block claim template", func(t *testing.T) {
		g := k.generate(t, mergeProps(vcts, k.extraContainers(map[string]any{
			"volumeDevices": []any{map[string]any{"name": "disk", "devicePath": "/dev/block0"}},
			"volumeMounts":  []any{map[string]any{"name": "data", "mountPath": "/data"}},
		})))
		for _, name := range k.extraNames() {
			c := containerByName(g.pod, name)
			if c == nil || len(c.VolumeDevices) != 1 || c.VolumeDevices[0].Name != "disk" {
				t.Errorf("%s did not get the block device", name)
			}
		}
	})
	t.Run("device naming a filesystem claim template", func(t *testing.T) {
		_, err := k.configure(t, mergeProps(vcts, k.extraContainers(map[string]any{
			"volumeDevices": []any{map[string]any{"name": "data", "devicePath": "/dev/block0"}},
		})))
		wantErrContaining(t, err, `volume "data" is not a Block volume`)
	})
	t.Run("mount naming a Block claim template", func(t *testing.T) {
		_, err := k.configure(t, mergeProps(vcts, k.extraContainers(map[string]any{
			"volumeMounts": []any{map[string]any{"name": "disk", "mountPath": "/mnt/disk"}},
		})))
		wantErrContaining(t, err, `volume "disk" is a Block volume`)
	})
}
