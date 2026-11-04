package components_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#1193 for init containers and sidecars:
// subPathExpr, mountPropagation and recursiveReadOnly are read from a
// volumeMounts item by one helper (parseMountOptions), and the rules are the
// ones upstream ValidateVolumeMounts applies to one mount (k8s.io/kubernetes
// v1.37.1, pkg/apis/core/validation/validation.go:1448-1508 and :3177-3225).

func ptr[T any](v T) *T { return &v }

// containerWithMount returns an init-container or sidecar entry holding one
// mount, with an optional securityContext.
func containerWithMount(mount, securityContext map[string]any) map[string]any {
	c := map[string]any{
		"name": "helper", "image": "ghcr.io/org/helper:v1",
		"volumeMounts": []any{mount},
	}
	if securityContext != nil {
		c["securityContext"] = securityContext
	}
	return c
}

// TestMountOptions_Render: every option reaches the built container, on an
// init container and on a sidecar.
func TestMountOptions_Render(t *testing.T) {
	mounts := []any{
		map[string]any{"name": "data", "mountPath": "/expr", "subPathExpr": "$(POD_NAME)"},
		map[string]any{"name": "data", "mountPath": "/ro", "readOnly": true, "mountPropagation": "None", "recursiveReadOnly": "Enabled"},
		map[string]any{"name": "data", "mountPath": "/rw", "mountPropagation": "HostToContainer", "recursiveReadOnly": "Disabled"},
		map[string]any{"name": "data", "mountPath": "/sub", "subPath": "sub", "readOnly": true, "recursiveReadOnly": "IfPossible"},
	}
	want := []corev1.VolumeMount{
		{Name: "data", MountPath: "/expr", SubPathExpr: "$(POD_NAME)"},
		{Name: "data", MountPath: "/ro", ReadOnly: true, MountPropagation: ptr(corev1.MountPropagationNone), RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyEnabled)},
		{Name: "data", MountPath: "/rw", MountPropagation: ptr(corev1.MountPropagationHostToContainer), RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyDisabled)},
		{Name: "data", MountPath: "/sub", SubPath: "sub", ReadOnly: true, RecursiveReadOnly: ptr(corev1.RecursiveReadOnlyIfPossible)},
	}
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"initContainers": []any{map[string]any{
			"name": "prep", "image": "ghcr.io/org/prep:v1", "volumeMounts": mounts,
		}},
		"sidecars": []any{map[string]any{
			"name": "shipper", "image": "ghcr.io/org/shipper:v1", "volumeMounts": mounts,
		}},
	}
	ps := podTemplateSpec(t, generateKind(t, webserviceViaRule{}, "webservice", props))
	for name, c := range map[string]corev1.Container{
		"init":    containerNamed(t, ps.InitContainers, "prep"),
		"sidecar": containerNamed(t, ps.Containers, "shipper"),
	} {
		if !reflect.DeepEqual(c.VolumeMounts, want) {
			t.Errorf("%s VolumeMounts = %+v, want %+v", name, c.VolumeMounts, want)
		}
	}
}

// TestMountOptions_Refusals: each rule refuses on every workload kind and on
// both lists, and the error names the list position, the mount and the field.
func TestMountOptions_Refusals(t *testing.T) {
	privileged := map[string]any{"privileged": true}
	cases := []struct {
		name  string
		mount map[string]any
		sc    map[string]any
		want  []string
	}{
		{"subPath and subPathExpr", map[string]any{"subPath": "a", "subPathExpr": "b"}, nil,
			[]string{"subPathExpr and subPath are mutually exclusive"}},
		{"subPathExpr absolute", map[string]any{"subPathExpr": "/abs"}, nil,
			[]string{".subPathExpr", "must be a relative path"}},
		{"subPathExpr backstep", map[string]any{"subPathExpr": "a/../b"}, nil,
			[]string{".subPathExpr", "'..'"}},
		{"subPathExpr wrong type", map[string]any{"subPathExpr": 1}, nil,
			[]string{".subPathExpr", "must be a string"}},
		{"mountPropagation unknown", map[string]any{"mountPropagation": "Shared"}, nil,
			[]string{".mountPropagation", "must be one of None, HostToContainer, Bidirectional", `"Shared"`}},
		{"mountPropagation empty", map[string]any{"mountPropagation": ""}, nil,
			[]string{".mountPropagation", "must be one of None, HostToContainer, Bidirectional"}},
		{"mountPropagation wrong type", map[string]any{"mountPropagation": true}, nil,
			[]string{".mountPropagation", "must be a string"}},
		{"Bidirectional without privileged", map[string]any{"mountPropagation": "Bidirectional"}, nil,
			[]string{"mountPropagation", "Bidirectional is available only", "privileged: true"}},
		{"Bidirectional with privileged false", map[string]any{"mountPropagation": "Bidirectional"}, map[string]any{"privileged": false},
			[]string{"mountPropagation", "Bidirectional is available only"}},
		{"recursiveReadOnly unknown", map[string]any{"readOnly": true, "recursiveReadOnly": "Always"}, nil,
			[]string{".recursiveReadOnly", "must be one of Disabled, IfPossible, Enabled", `"Always"`}},
		{"recursiveReadOnly wrong type", map[string]any{"recursiveReadOnly": true}, nil,
			[]string{".recursiveReadOnly", "must be a string"}},
		{"Enabled without readOnly", map[string]any{"recursiveReadOnly": "Enabled"}, nil,
			[]string{".recursiveReadOnly", "Enabled needs readOnly: true"}},
		{"IfPossible with readOnly false", map[string]any{"readOnly": false, "recursiveReadOnly": "IfPossible"}, nil,
			[]string{".recursiveReadOnly", "IfPossible needs readOnly: true"}},
		{"Enabled with HostToContainer", map[string]any{"readOnly": true, "mountPropagation": "HostToContainer", "recursiveReadOnly": "Enabled"}, nil,
			[]string{".recursiveReadOnly", "needs mountPropagation None"}},
		{"Enabled with Bidirectional", map[string]any{"readOnly": true, "mountPropagation": "Bidirectional", "recursiveReadOnly": "IfPossible"}, privileged,
			[]string{".recursiveReadOnly", "needs mountPropagation None"}},
	}
	for _, listKey := range []string{"initContainers", "sidecars"} {
		for _, kind := range mountEntryKinds(listKey) {
			for _, tc := range cases {
				t.Run(listKey+"/"+kind+"/"+tc.name, func(t *testing.T) {
					h, base := kindProps(t, kind)
					mount := map[string]any{"name": "data", "mountPath": "/data"}
					for k, v := range tc.mount {
						mount[k] = v
					}
					props := withProps(base, map[string]any{
						listKey: []any{containerWithMount(mount, tc.sc)},
					})
					_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
					if err == nil {
						t.Fatalf("expected an error, got none")
					}
					for _, w := range append([]string{listKey + `[0] "helper"`, "volumeMounts[0]"}, tc.want...) {
						if !strings.Contains(err.Error(), w) {
							t.Errorf("error %q does not contain %q", err.Error(), w)
						}
					}
				})
			}
		}
	}
}

// TestMountOptions_Accepts: the combinations upstream allows build cleanly.
func TestMountOptions_Accepts(t *testing.T) {
	cases := []struct {
		name  string
		mount map[string]any
		sc    map[string]any
	}{
		{"Bidirectional on a privileged container", map[string]any{"mountPropagation": "Bidirectional"}, map[string]any{"privileged": true}},
		{"Disabled without readOnly", map[string]any{"recursiveReadOnly": "Disabled"}, nil},
		{"Enabled with readOnly and no propagation", map[string]any{"readOnly": true, "recursiveReadOnly": "Enabled"}, nil},
		{"subPathExpr with a nested relative path", map[string]any{"subPathExpr": "a/$(POD_NAME)/b"}, nil},
		{"a dotted name that is no backstep", map[string]any{"subPathExpr": "a..b/..c"}, nil},
	}
	for _, listKey := range []string{"initContainers", "sidecars"} {
		for _, tc := range cases {
			t.Run(listKey+"/"+tc.name, func(t *testing.T) {
				h, base := kindProps(t, "deployment")
				mount := map[string]any{"name": "data", "mountPath": "/data"}
				for k, v := range tc.mount {
					mount[k] = v
				}
				props := withProps(base, map[string]any{
					listKey: []any{containerWithMount(mount, tc.sc)},
				})
				if _, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: props}, "default"); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		}
	}
}

// TestMountOptions_SchemaDeclaresEnums: the published item gives the two enum
// keys their upstream values, on every kind and both lists.
func TestMountOptions_SchemaDeclaresEnums(t *testing.T) {
	wantEnum := map[string][]any{
		"mountPropagation":  {"None", "HostToContainer", "Bidirectional"},
		"recursiveReadOnly": {"Disabled", "IfPossible", "Enabled"},
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			schema := k.handler.(oam.PropertySchemaProvider).PropertySchema()
			for _, listKey := range []string{"initContainers", "sidecars"} {
				list, ok := schema[listKey]
				if !ok {
					continue
				}
				item := list.Items.Properties["volumeMounts"].Items
				for key, want := range wantEnum {
					if got := item.Properties[key].Enum; !reflect.DeepEqual(got, want) {
						t.Errorf("%s: %s enum = %v, want %v", listKey, key, got, want)
					}
				}
			}
		})
	}
}
