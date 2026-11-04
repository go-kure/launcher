package components_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover go-kure/launcher#1198: the main container's `volumes`
// entries and `volumeClaimTemplates` read the mount options that
// go-kure/launcher#1193 gave init containers and sidecars, through the same
// helper (parseMountOptions) and with the same upstream rules
// (mount_options_test.go).

// mountKeys are the keys beyond name and mountPath that the helper reads.
var mountKeys = []string{"readOnly", "subPath", "subPathExpr", "mountPropagation", "recursiveReadOnly"}

type props = map[string]any

func volumeEntry(extra props) props {
	e := props{"name": "data", "type": "emptyDir", "mountPath": "/data"}
	for k, v := range extra {
		e[k] = v
	}
	return e
}

// TestMainMount_Render: every option reaches the main container's mounts, on
// every workload kind.
func TestMainMount_Render(t *testing.T) {
	volumes := []any{
		props{"name": "a", "type": "emptyDir", "mountPath": "/expr", "subPathExpr": "$(POD_NAME)"},
		props{"name": "b", "type": "emptyDir", "mountPath": "/ro", "readOnly": true, "mountPropagation": "None", "recursiveReadOnly": "Enabled"},
		props{"name": "c", "type": "hostPath", "path": "/h", "mountPath": "/rw", "mountPropagation": "HostToContainer", "recursiveReadOnly": "Disabled"},
		props{"name": "d", "type": "emptyDir", "mountPath": "/sub", "subPath": "sub", "readOnly": true, "recursiveReadOnly": "IfPossible"},
	}
	want := []corev1.VolumeMount{
		{Name: "a", MountPath: "/expr", SubPathExpr: "$(POD_NAME)"},
		{Name: "b", MountPath: "/ro", ReadOnly: true, MountPropagation: ptr(corev1.MountPropagationNone), RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyEnabled)},
		{Name: "c", MountPath: "/rw", MountPropagation: ptr(corev1.MountPropagationHostToContainer), RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyDisabled)},
		{Name: "d", MountPath: "/sub", SubPath: "sub", ReadOnly: true, RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyIfPossible)},
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": volumes})))
			c := containerNamed(t, ps.Containers, "app")
			if !reflect.DeepEqual(c.VolumeMounts, want) {
				t.Errorf("VolumeMounts = %+v, want %+v", c.VolumeMounts, want)
			}
		})
	}
}

// TestMainMount_ReadOnlyHasOneReader: readOnly keeps its per-type meaning. On a
// pvc it sets the pod volume source and the mount. On configMap and secret it
// sets the mount only. recursiveReadOnly is checked against that one result.
func TestMainMount_ReadOnlyHasOneReader(t *testing.T) {
	volumes := []any{
		props{"name": "pv", "type": "pvc", "claimName": "existing", "mountPath": "/pv", "readOnly": true, "recursiveReadOnly": "Enabled", "subPath": "x"},
		props{"name": "cm", "type": "configMap", "configMapName": "settings", "mountPath": "/cm", "readOnly": true, "recursiveReadOnly": "IfPossible", "subPathExpr": "a"},
		props{"name": "sec", "type": "secret", "secretName": "creds", "mountPath": "/sec", "mountPropagation": "None"},
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": volumes})))
			c := containerNamed(t, ps.Containers, "app")
			want := []corev1.VolumeMount{
				{Name: "pv", MountPath: "/pv", ReadOnly: true, SubPath: "x", RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyEnabled)},
				{Name: "cm", MountPath: "/cm", ReadOnly: true, SubPathExpr: "a", RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyIfPossible)},
				{Name: "sec", MountPath: "/sec", MountPropagation: ptr(corev1.MountPropagationNone)},
			}
			if !reflect.DeepEqual(c.VolumeMounts, want) {
				t.Errorf("VolumeMounts = %+v, want %+v", c.VolumeMounts, want)
			}
			for _, v := range ps.Volumes {
				if v.Name == "pv" && (v.PersistentVolumeClaim == nil || !v.PersistentVolumeClaim.ReadOnly) {
					t.Errorf("pvc volume source ReadOnly = false, want true")
				}
			}
		})
	}
}

// refusalCases are the rules of mount_options_test.go, applied to a mount
// that comes from a `volumes` entry or a claim template.
var refusalCases = []struct {
	name  string
	mount props
	sc    props
	want  []string
}{
	{"subPath and subPathExpr", props{"subPath": "a", "subPathExpr": "b"}, nil,
		[]string{"subPathExpr and subPath are mutually exclusive"}},
	{"subPath absolute", props{"subPath": "/abs"}, nil,
		[]string{".subPath", "must be a relative path", `"/abs"`}},
	{"subPath backstep", props{"subPath": "a/../b"}, nil,
		[]string{".subPath", "'..'", `"a/../b"`}},
	{"subPath bare backstep", props{"subPath": ".."}, nil,
		[]string{".subPath", "'..'"}},
	{"subPath leading backstep", props{"subPath": "../x"}, nil,
		[]string{".subPath", "'..'"}},
	{"bad subPath and subPathExpr", props{"subPath": "/abs", "subPathExpr": "b"}, nil,
		[]string{"subPathExpr and subPath are mutually exclusive"}},
	{"bad subPath and a wrong-typed subPathExpr", props{"subPath": "../x", "subPathExpr": 1}, nil,
		[]string{".subPathExpr", "must be a string"}},
	{"subPathExpr absolute", props{"subPathExpr": "/abs"}, nil,
		[]string{".subPathExpr", "must be a relative path"}},
	{"subPathExpr backstep", props{"subPathExpr": "a/../b"}, nil,
		[]string{".subPathExpr", "'..'"}},
	{"subPathExpr wrong type", props{"subPathExpr": 1}, nil,
		[]string{".subPathExpr", "must be a string"}},
	{"subPath wrong type", props{"subPath": 1}, nil,
		[]string{".subPath", "must be a string"}},
	{"mountPropagation unknown", props{"mountPropagation": "Shared"}, nil,
		[]string{".mountPropagation", "must be one of None, HostToContainer, Bidirectional", `"Shared"`}},
	{"mountPropagation empty", props{"mountPropagation": ""}, nil,
		[]string{".mountPropagation", "must be one of None, HostToContainer, Bidirectional"}},
	{"mountPropagation wrong type", props{"mountPropagation": true}, nil,
		[]string{".mountPropagation", "must be a string"}},
	{"Bidirectional without privileged", props{"mountPropagation": "Bidirectional"}, nil,
		[]string{"mountPropagation", "Bidirectional is available only", "privileged: true"}},
	{"Bidirectional with privileged false", props{"mountPropagation": "Bidirectional"}, props{"privileged": false},
		[]string{"mountPropagation", "Bidirectional is available only"}},
	{"recursiveReadOnly unknown", props{"readOnly": true, "recursiveReadOnly": "Always"}, nil,
		[]string{".recursiveReadOnly", "must be one of Disabled, IfPossible, Enabled", `"Always"`}},
	{"recursiveReadOnly wrong type", props{"recursiveReadOnly": true}, nil,
		[]string{".recursiveReadOnly", "must be a string"}},
	{"Enabled without readOnly", props{"recursiveReadOnly": "Enabled"}, nil,
		[]string{".recursiveReadOnly", "Enabled needs readOnly: true"}},
	{"IfPossible with readOnly false", props{"readOnly": false, "recursiveReadOnly": "IfPossible"}, nil,
		[]string{".recursiveReadOnly", "IfPossible needs readOnly: true"}},
	{"Enabled with HostToContainer", props{"readOnly": true, "mountPropagation": "HostToContainer", "recursiveReadOnly": "Enabled"}, nil,
		[]string{".recursiveReadOnly", "needs mountPropagation None"}},
	{"Enabled with Bidirectional", props{"readOnly": true, "mountPropagation": "Bidirectional", "recursiveReadOnly": "IfPossible"}, props{"privileged": true},
		[]string{".recursiveReadOnly", "needs mountPropagation None"}},
}

// entryName is the text an error names the entry by. The privileged check runs
// after both parsers, on the merged mount list, so it names the mount.
func entryName(caseName, label string) string {
	if strings.HasPrefix(caseName, "Bidirectional with") {
		return `mount "data"`
	}
	return label
}

func refuses(t *testing.T, kind string, p props, want []string) {
	t.Helper()
	var h oam.ComponentHandler
	for _, k := range workloadKinds {
		if k.name == kind {
			h = k.handler
		}
	}
	_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: p}, "default")
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not contain %q", err.Error(), w)
		}
	}
}

// TestMainMount_Refusals: each rule refuses on every workload kind, and the
// error names the volume.
func TestMainMount_Refusals(t *testing.T) {
	for _, k := range workloadKinds {
		for _, tc := range refusalCases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				extra := props{"volumes": []any{volumeEntry(tc.mount)}}
				if tc.sc != nil {
					extra["securityContext"] = tc.sc
				}
				refuses(t, k.name, withProps(k.props, extra), append([]string{entryName(tc.name, `volume "data"`)}, tc.want...))
			})
		}
	}
}

// TestMainMount_SubPathShapesUpstreamAccepts: a relative subPath that holds no
// ".." element builds on a `volumes` entry and on a claim template, and an empty
// one is absent, as upstream skips it (validation.go:3205). The refusals above
// are the other half.
func TestMainMount_SubPathShapesUpstreamAccepts(t *testing.T) {
	dep, sts := workloadKinds[5], workloadKinds[2]
	if dep.name != "deployment" || sts.name != "statefulset" {
		t.Fatalf("workloadKinds order changed: %q, %q", dep.name, sts.name)
	}
	for _, sp := range []string{"a/b", "./a", "a..b/..c", ""} {
		t.Run("volumes/"+sp, func(t *testing.T) {
			p := withProps(dep.props, props{"volumes": []any{volumeEntry(props{"subPath": sp})}})
			if _, err := dep.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: dep.name, Properties: p}, "default"); err != nil {
				t.Errorf("subPath %q was refused: %v", sp, err)
			}
		})
		t.Run("claim template/"+sp, func(t *testing.T) {
			vct := props{"name": "data", "size": "1Gi", "mountPath": "/data", "subPath": sp}
			p := withProps(sts.props, props{"volumeClaimTemplates": []any{vct}})
			if _, err := sts.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: sts.name, Properties: p}, "default"); err != nil {
				t.Errorf("subPath %q was refused: %v", sp, err)
			}
		})
	}
}

// TestMainMount_BidirectionalNeedsPrivilegedOnEveryKind fails if one kind does
// not run the check: an unprivileged Bidirectional mount is refused and a
// privileged one builds, on all seven kinds.
func TestMainMount_BidirectionalNeedsPrivilegedOnEveryKind(t *testing.T) {
	vol := props{"volumes": []any{volumeEntry(props{"mountPropagation": "Bidirectional"})}}
	for _, k := range workloadKinds {
		t.Run(k.name+"/unprivileged", func(t *testing.T) {
			refuses(t, k.name, withProps(k.props, vol), []string{`mount "data"`, "Bidirectional is available only"})
		})
		t.Run(k.name+"/privileged", func(t *testing.T) {
			p := withProps(k.props, vol)
			p["securityContext"] = props{"privileged": true}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, p))
			c := containerNamed(t, ps.Containers, "app")
			if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].MountPropagation == nil || *c.VolumeMounts[0].MountPropagation != corev1.MountPropagationBidirectional {
				t.Errorf("VolumeMounts = %+v, want one Bidirectional mount", c.VolumeMounts)
			}
		})
	}
}

// TestMainMount_RulesCheckBidirectionalThemselves: webservice and worker are
// lowering rules, and the adapters in workloadKinds hand their emitted
// deployment to DeploymentHandler, which runs its own check. Those cases
// therefore cannot see a missing check in the rules. This test calls each
// rule's LowerComponent, where only the rule's own check can refuse.
func TestMainMount_RulesCheckBidirectionalThemselves(t *testing.T) {
	rules := map[string]interface {
		LowerComponent(*oam.Component, oam.LoweringContext) (oam.LoweringResult, error)
	}{
		"webservice": components.WebserviceRule{},
		"worker":     components.WorkerRule{},
	}
	base := map[string]props{
		"webservice": {"image": "ghcr.io/org/app:v1", "port": 8080},
		"worker":     {"image": "ghcr.io/org/app:v1"},
	}
	vol := props{"volumes": []any{volumeEntry(props{"mountPropagation": "Bidirectional"})}}
	for name, rule := range rules {
		t.Run(name+"/unprivileged", func(t *testing.T) {
			_, err := rule.LowerComponent(&oam.Component{Name: "app", Type: name, Properties: withProps(base[name], vol)}, oam.LoweringContext{})
			if err == nil || !strings.Contains(err.Error(), `mount "data"`) || !strings.Contains(err.Error(), "Bidirectional is available only") {
				t.Errorf("LowerComponent error = %v, want the Bidirectional refusal naming mount \"data\"", err)
			}
		})
		t.Run(name+"/privileged", func(t *testing.T) {
			p := withProps(base[name], vol)
			p["securityContext"] = props{"privileged": true}
			if _, err := rule.LowerComponent(&oam.Component{Name: "app", Type: name, Properties: p}, oam.LoweringContext{}); err != nil {
				t.Errorf("LowerComponent: %v", err)
			}
		})
	}
}

// TestMainMount_BlockDeviceRefusesMountKeys: a pvc volume with devicePath is a
// raw block device. It has no mount, so the four mount keys are refused.
// readOnly stays accepted there: it sets the claim source, as before
// go-kure/launcher#1198.
func TestMainMount_BlockDeviceRefusesMountKeys(t *testing.T) {
	values := map[string]any{
		"subPath": "a", "subPathExpr": "a",
		"mountPropagation": "None", "recursiveReadOnly": "Disabled",
	}
	for _, key := range mountKeys {
		if key == "readOnly" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			entry := props{"name": "raw", "type": "pvc", "claimName": "existing", "volumeMode": "Block", "devicePath": "/dev/xvda", key: values[key]}
			for _, k := range workloadKinds {
				if k.name != "deployment" {
					continue
				}
				refuses(t, k.name, withProps(k.props, props{"volumes": []any{entry}}),
					[]string{`volume "raw"`, key, "devicePath", "no mount"})
			}
		})
	}
}

// TestMainMount_NullIsAbsent: an explicit null on any mount key reads as
// omission on every path that reads or refuses one (the null-as-absence rule in
// the README). The four paths are a `volumes` mount, a `volumes` device, a
// claim template mount and a claim template device. A device path used to
// refuse the key by presence alone.
func TestMainMount_NullIsAbsent(t *testing.T) {
	sts := workloadKinds[2]
	dep := workloadKinds[5]
	if sts.name != "statefulset" || dep.name != "deployment" {
		t.Fatalf("workloadKinds order changed: %q, %q", sts.name, dep.name)
	}
	for _, key := range mountKeys {
		t.Run(key, func(t *testing.T) {
			cases := []struct {
				path string
				kind struct {
					name    string
					handler oam.ComponentHandler
					props   map[string]any
				}
				extra props
			}{
				{"volumes mount", dep, props{"volumes": []any{volumeEntry(props{key: nil})}}},
				{"volumes device", dep, props{"volumes": []any{props{"name": "raw", "type": "pvc", "claimName": "existing", "volumeMode": "Block", "devicePath": "/dev/xvda", key: nil}}}},
				{"claim template mount", sts, props{"volumeClaimTemplates": []any{props{"name": "data", "size": "1Gi", "mountPath": "/data", key: nil}}}},
				{"claim template device", sts, props{"volumeClaimTemplates": []any{props{"name": "raw", "size": "1Gi", "volumeMode": "Block", "devicePath": "/dev/xvda", key: nil}}}},
			}
			for _, tc := range cases {
				t.Run(tc.path, func(t *testing.T) {
					if _, err := tc.kind.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: tc.kind.name, Properties: withProps(tc.kind.props, tc.extra)}, "default"); err != nil {
						t.Errorf("a null %s was refused: %v", key, err)
					}
				})
			}
		})
	}
}

// TestMainMount_BlockDeviceKeepsReadOnly pins the other half of the split: a
// `volumes` pvc entry with devicePath still accepts readOnly. It sets the
// claim source, a valid field before go-kure/launcher#1198, and the entry
// renders a device and no mount. (A claim template with devicePath refuses
// readOnly: TestMainMount_ClaimTemplates.)
func TestMainMount_BlockDeviceKeepsReadOnly(t *testing.T) {
	entry := props{"name": "raw", "type": "pvc", "claimName": "existing", "volumeMode": "Block", "devicePath": "/dev/xvda", "readOnly": true}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": []any{entry}})))
			c := containerNamed(t, ps.Containers, "app")
			if len(c.VolumeMounts) != 0 || len(c.VolumeDevices) != 1 {
				t.Errorf("mounts = %+v, devices = %+v, want no mount and one device", c.VolumeMounts, c.VolumeDevices)
			}
			var found bool
			for _, v := range ps.Volumes {
				if v.Name == "raw" {
					found = true
					if v.PersistentVolumeClaim == nil || !v.PersistentVolumeClaim.ReadOnly {
						t.Errorf("claim source ReadOnly = false, want true")
					}
				}
			}
			if !found {
				t.Errorf("no pod volume named raw")
			}
		})
	}
}

// TestMainMount_ClaimTemplates: a claim template with a mountPath reads the
// same keys, including readOnly, and a template with a devicePath refuses all
// of them.
func TestMainMount_ClaimTemplates(t *testing.T) {
	sts := workloadKinds[2]
	if sts.name != "statefulset" {
		t.Fatalf("workloadKinds[2] = %q, want statefulset", sts.name)
	}
	tmpl := func(extra props) props {
		e := props{"name": "data", "size": "1Gi", "mountPath": "/data"}
		for k, v := range extra {
			e[k] = v
		}
		return e
	}
	t.Run("render", func(t *testing.T) {
		vcts := []any{
			tmpl(props{"name": "a", "mountPath": "/expr", "subPathExpr": "$(POD_NAME)"}),
			tmpl(props{"name": "b", "mountPath": "/ro", "readOnly": true, "mountPropagation": "None", "recursiveReadOnly": "Enabled"}),
			tmpl(props{"name": "c", "mountPath": "/sub", "subPath": "sub", "mountPropagation": "HostToContainer"}),
		}
		want := []corev1.VolumeMount{
			{Name: "a", MountPath: "/expr", SubPathExpr: "$(POD_NAME)"},
			{Name: "b", MountPath: "/ro", ReadOnly: true, MountPropagation: ptr(corev1.MountPropagationNone), RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyEnabled)},
			{Name: "c", MountPath: "/sub", SubPath: "sub", MountPropagation: ptr(corev1.MountPropagationHostToContainer)},
		}
		ps := podTemplateSpec(t, generateKind(t, sts.handler, "statefulset", withProps(sts.props, props{"volumeClaimTemplates": vcts})))
		c := containerNamed(t, ps.Containers, "app")
		if !reflect.DeepEqual(c.VolumeMounts, want) {
			t.Errorf("VolumeMounts = %+v, want %+v", c.VolumeMounts, want)
		}
	})
	for _, tc := range refusalCases {
		t.Run("refuses/"+tc.name, func(t *testing.T) {
			extra := props{"volumeClaimTemplates": []any{tmpl(tc.mount)}}
			if tc.sc != nil {
				extra["securityContext"] = tc.sc
			}
			refuses(t, "statefulset", withProps(sts.props, extra), append([]string{entryName(tc.name, `volumeClaimTemplate "data"`)}, tc.want...))
		})
	}
	values := map[string]any{
		"readOnly": true, "subPath": "a", "subPathExpr": "a",
		"mountPropagation": "None", "recursiveReadOnly": "Disabled",
	}
	for _, key := range mountKeys {
		t.Run("devicePath refuses/"+key, func(t *testing.T) {
			raw := props{"name": "raw", "size": "1Gi", "volumeMode": "Block", "devicePath": "/dev/xvda", key: values[key]}
			refuses(t, "statefulset", withProps(sts.props, props{"volumeClaimTemplates": []any{raw}}),
				[]string{`volumeClaimTemplate "raw"`, key, "devicePath", "no mount"})
		})
	}
}

// TestMainMount_SchemaDeclaresKeys: the published `volumes` item declares the
// keys the parser reads, with the upstream enums, on every kind. The claim
// template item of statefulset declares them too.
func TestMainMount_SchemaDeclaresKeys(t *testing.T) {
	wantEnum := map[string][]any{
		"mountPropagation":  {"None", "HostToContainer", "Bidirectional"},
		"recursiveReadOnly": {"Disabled", "IfPossible", "Enabled"},
	}
	check := func(t *testing.T, where string, item map[string]oam.PropertySchema) {
		t.Helper()
		for _, key := range mountKeys {
			if _, ok := item[key]; !ok {
				t.Errorf("%s does not declare %q", where, key)
			}
		}
		for key, want := range wantEnum {
			if got := item[key].Enum; !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s enum = %v, want %v", where, key, got, want)
			}
		}
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			schema := k.handler.(oam.PropertySchemaProvider).PropertySchema()
			check(t, "volumes item", schema["volumes"].Items.Properties)
			if k.name == "statefulset" {
				check(t, "volumeClaimTemplates item", schema["volumeClaimTemplates"].Items.Properties)
			}
		})
	}
}
