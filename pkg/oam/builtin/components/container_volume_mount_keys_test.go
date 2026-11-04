package components_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#1172: a `volumeMounts` item inside an
// `initContainers` or `sidecars` entry read name, mountPath, readOnly and
// subPath and dropped every other key, so an authored subPathExpr,
// mountPropagation or recursiveReadOnly built cleanly and reached no container.
// The item is now a closed key set: those keys are refused by name.
// go-kure/launcher#1193 then made subPathExpr, mountPropagation and
// recursiveReadOnly real keys of that item (mount_options_test.go), so the
// refusal table below keeps only the keys that stay unread.

// mountEntryKinds returns the workload kinds that publish the given list key.
func mountEntryKinds(listKey string) []string {
	var out []string
	for _, k := range workloadKinds {
		if listKey == "sidecars" && !sidecarKinds[k.name] {
			continue
		}
		out = append(out, k.name)
	}
	return out
}

func kindProps(t *testing.T, name string) (oam.ComponentHandler, map[string]any) {
	t.Helper()
	for _, k := range workloadKinds {
		if k.name == name {
			return k.handler, k.props
		}
	}
	t.Fatalf("no workload kind %q", name)
	return nil, nil
}

// TestContainerEntry_VolumeMountRefusesUnreadKeys: each key the parser does not
// read is refused on every kind and on both lists, and the error names the key,
// the list position and the container.
func TestContainerEntry_VolumeMountRefusesUnreadKeys(t *testing.T) {
	keys := []struct {
		key   string
		value any
	}{
		{"bindMountOptions", map[string]any{}},
		{"readonly", true}, // a typo of readOnly
	}
	for _, listKey := range []string{"initContainers", "sidecars"} {
		for _, kind := range mountEntryKinds(listKey) {
			for _, tc := range keys {
				t.Run(listKey+"/"+kind+"/"+tc.key, func(t *testing.T) {
					h, base := kindProps(t, kind)
					mount := map[string]any{"name": "data", "mountPath": "/data", tc.key: tc.value}
					props := withProps(base, map[string]any{
						listKey: []any{map[string]any{
							"name": "helper", "image": "ghcr.io/org/helper:v1",
							"volumeMounts": []any{mount},
						}},
					})
					_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
					if err == nil {
						t.Fatalf("expected an error, got none")
					}
					for _, w := range []string{listKey + `[0] "helper"`, "volumeMounts[0]", `unrecognized key "` + tc.key + `"`} {
						if !strings.Contains(err.Error(), w) {
							t.Errorf("error %q does not contain %q", err.Error(), w)
						}
					}
				})
			}
		}
	}
}

// TestContainerEntry_VolumeMountKeepsReadKeys: the four keys the parser reads
// still render on an init container and on a sidecar.
func TestContainerEntry_VolumeMountKeepsReadKeys(t *testing.T) {
	mount := func() map[string]any {
		return map[string]any{"name": "data", "mountPath": "/data", "readOnly": true, "subPath": "sub"}
	}
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"initContainers": []any{map[string]any{
			"name": "prep", "image": "ghcr.io/org/prep:v1", "volumeMounts": []any{mount()},
		}},
		"sidecars": []any{map[string]any{
			"name": "shipper", "image": "ghcr.io/org/shipper:v1", "volumeMounts": []any{mount()},
		}},
	}
	ps := podTemplateSpec(t, generateKind(t, webserviceViaRule{}, "webservice", props))
	want := corev1.VolumeMount{Name: "data", MountPath: "/data", ReadOnly: true, SubPath: "sub"}
	for name, c := range map[string]corev1.Container{
		"init":    containerNamed(t, ps.InitContainers, "prep"),
		"sidecar": containerNamed(t, ps.Containers, "shipper"),
	} {
		if len(c.VolumeMounts) != 1 || !reflect.DeepEqual(c.VolumeMounts[0], want) {
			t.Errorf("%s VolumeMounts = %+v, want [%+v]", name, c.VolumeMounts, want)
		}
	}
}

// TestContainerEntry_VolumeMountItemSchemaIsClosed: the published item declares
// exactly the keys the parser reads (name, mountPath and mountOptionKeys) and
// admits no others.
func TestContainerEntry_VolumeMountItemSchemaIsClosed(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			schema := k.handler.(oam.PropertySchemaProvider).PropertySchema()
			for _, listKey := range []string{"initContainers", "sidecars"} {
				list, ok := schema[listKey]
				if !ok {
					continue
				}
				item := list.Items.Properties["volumeMounts"].Items
				if item == nil {
					t.Fatalf("%s: no volumeMounts item schema", listKey)
				}
				if item.AdditionalProperties {
					t.Errorf("%s: volumeMounts item is open, want closed", listKey)
				}
				want := []string{"name", "mountPath", "readOnly", "subPath", "subPathExpr", "mountPropagation", "recursiveReadOnly"}
				if len(item.Properties) != len(want) {
					t.Errorf("%s: volumeMounts item declares %d keys, want %v", listKey, len(item.Properties), want)
				}
				for _, key := range want {
					if _, declared := item.Properties[key]; !declared {
						t.Errorf("%s: volumeMounts item does not declare %q, which the parser reads", listKey, key)
					}
				}
			}
		})
	}
}
