package patch

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These rows pin how a TOML header resolves to a field path when the header spells the start of a
// shorthand path itself, and where `ports` goes under a container item. A section name such as
// `containers` expands to `spec.template.spec.containers`. A header that already spells that start
// must not get it twice. `ports` under `containers.0` is the container field, not the service one.

func requirePathParts(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("path parts = %q, want %q", got, want)
	}
}

func TestTOMLHeaderPath_ExplicitPrefixIsNotRepeated(t *testing.T) {
	rows := []struct {
		name     string
		kind     string
		sections []string
		want     []string
	}{
		{"E1_workload_full_prefix_containers", "deployment", []string{"spec", "template", "spec", "containers"}, []string{"spec", "template", "spec", "containers"}},
		{"E2_workload_spec_containers", "deployment", []string{"spec", "containers"}, []string{"spec", "template", "spec", "containers"}},
		{"E3_pod_spec_containers", "pod", []string{"spec", "containers"}, []string{"spec", "containers"}},
		{"E4_workload_full_prefix_initContainers", "deployment", []string{"spec", "template", "spec", "initContainers"}, []string{"spec", "template", "spec", "initContainers"}},
		{"E5_pod_spec_initContainers", "pod", []string{"spec", "initContainers"}, []string{"spec", "initContainers"}},
		{"E6_workload_full_prefix_volumes", "deployment", []string{"spec", "template", "spec", "volumes"}, []string{"spec", "template", "spec", "volumes"}},
		{"E7_pod_spec_volumes", "pod", []string{"spec", "volumes"}, []string{"spec", "volumes"}},
		{"E8_service_spec_ports", "service", []string{"spec", "ports"}, []string{"spec", "ports"}},
		{"E9_service_spec_selector", "service", []string{"spec", "selector"}, []string{"spec", "selector"}},
		{"E10_service_spec_type", "service", []string{"spec", "type"}, []string{"spec", "type"}},
		{"E11_ingress_spec_tls", "ingress", []string{"spec", "tls"}, []string{"spec", "tls"}},
		{"E12_ingress_spec_rules", "ingress", []string{"spec", "rules"}, []string{"spec", "rules"}},
		// A header that spells only the first part of the shorthand path, from its start.
		{"E13_part_of_the_prefix_from_the_start", "deployment", []string{"spec", "template", "containers"}, []string{"spec", "template", "spec", "containers"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := &TOMLHeader{Kind: r.kind, Name: "app", Sections: r.sections}
			requirePathParts(t, h.mapSectionsToKubernetesPath(), r.want...)
		})
	}
}

// A header that does not start with the shorthand's own first parts is left as it was. These
// results are not real field paths, before or after. The rows pin that the change leaves them alone.
func TestTOMLHeaderPath_ExplicitPrefixIsNotRepeated_Controls(t *testing.T) {
	rows := []struct {
		name     string
		kind     string
		sections []string
		want     []string
	}{
		{"C1_shorthand_containers", "deployment", []string{"containers"}, []string{"spec", "template", "spec", "containers"}},
		{"C2_spec_alone", "deployment", []string{"spec"}, []string{"spec"}},
		{"C3_spec_then_an_unknown_name", "deployment", []string{"spec", "replicas"}, []string{"spec", "replicas"}},
		{"C4_role_rules", "role", []string{"rules"}, []string{"rules"}},
		{"U1_other_name_first_then_spec", "deployment", []string{"template", "spec", "containers"}, []string{"template", "spec", "spec", "template", "spec", "containers"}},
		{"U2_metadata_then_spec", "deployment", []string{"metadata", "spec", "containers"}, []string{"metadata", "spec", "spec", "template", "spec", "containers"}},
		{"U3_spec_twice", "deployment", []string{"spec", "spec", "containers"}, []string{"spec", "spec", "spec", "template", "spec", "containers"}},
		{"U4_service_metadata_then_spec", "service", []string{"metadata", "spec", "ports"}, []string{"metadata", "spec", "spec", "ports"}},
		{"U5_two_shorthand_names", "deployment", []string{"containers", "volumes"}, []string{"spec", "template", "spec", "containers", "spec", "template", "spec", "volumes"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := &TOMLHeader{Kind: r.kind, Name: "app", Sections: r.sections}
			requirePathParts(t, h.mapSectionsToKubernetesPath(), r.want...)
		})
	}
}

func TestTOMLHeaderPath_PortsUnderContainerItem(t *testing.T) {
	rows := []struct {
		name     string
		kind     string
		sections []string
		want     []string
	}{
		{"P1_containers_item_ports", "deployment", []string{"containers", "0", "ports"}, []string{"spec", "template", "spec", "containers[0]", "ports"}},
		{"P2_initContainers_item_ports", "deployment", []string{"initContainers", "0", "ports"}, []string{"spec", "template", "spec", "initContainers[0]", "ports"}},
		{"P3_spelled_prefix_and_item", "deployment", []string{"spec", "template", "spec", "containers", "0", "ports"}, []string{"spec", "template", "spec", "containers[0]", "ports"}},
		{"P7_initContainers_ports", "deployment", []string{"initContainers", "ports"}, []string{"spec", "template", "spec", "initContainers", "ports"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := &TOMLHeader{Kind: r.kind, Name: "app", Sections: r.sections}
			requirePathParts(t, h.mapSectionsToKubernetesPath(), r.want...)
		})
	}
}

func TestTOMLHeaderPath_PortsUnderContainerItem_Controls(t *testing.T) {
	rows := []struct {
		name     string
		kind     string
		sections []string
		want     []string
	}{
		{"P4_containers_ports", "deployment", []string{"containers", "ports"}, []string{"spec", "template", "spec", "containers", "ports"}},
		{"P5_service_ports", "service", []string{"ports"}, []string{"spec", "ports"}},
		{"P6_a_name_that_is_not_an_index", "deployment", []string{"containers", "env", "ports"}, []string{"spec", "template", "spec", "containers", "env", "spec", "ports"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			h := &TOMLHeader{Kind: r.kind, Name: "app", Sections: r.sections}
			requirePathParts(t, h.mapSectionsToKubernetesPath(), r.want...)
		})
	}
}

// loadOnePatch loads one TOML source and returns its only patch.
func loadOnePatch(t *testing.T, src string) PatchSpec {
	t.Helper()
	patches, err := LoadTOMLPatchFile(strings.NewReader(src), nil)
	if err != nil {
		t.Fatalf("LoadTOMLPatchFile: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("loaded %d patches, want 1", len(patches))
	}
	return patches[0]
}

func TestTOMLPatchLoad_SpelledHeader(t *testing.T) {
	const wantPath = "spec.template.spec.containers"
	t.Run("L1_ticket_input_ports", func(t *testing.T) {
		p := loadOnePatch(t, "[deployment.app.spec.template.spec.containers.0.ports]\n.: 8080\n")
		if p.Patch.Path != wantPath || p.Patch.Selector != "0" {
			t.Errorf("path %q selector %q, want %q selector 0", p.Patch.Path, p.Patch.Selector, wantPath)
		}
		want := map[string]any{"ports..": "8080"}
		if !reflect.DeepEqual(p.Patch.Value, want) {
			t.Errorf("value = %#v, want %#v", p.Patch.Value, want)
		}
		short := loadOnePatch(t, "[deployment.app.containers.0.ports]\n.: 8080\n")
		if !reflect.DeepEqual(p.Patch, short.Patch) {
			t.Errorf("spelled header gives %#v, the shorthand header gives %#v", p.Patch, short.Patch)
		}
	})
	t.Run("L2_ticket_input_name", func(t *testing.T) {
		p := loadOnePatch(t, "[deployment.app.spec.template.spec.containers.0.name]\n.: 3\n")
		if p.Patch.Path != wantPath || p.Patch.Selector != "0" {
			t.Errorf("path %q selector %q, want %q selector 0", p.Patch.Path, p.Patch.Selector, wantPath)
		}
		want := map[string]any{"name..": "3"}
		if !reflect.DeepEqual(p.Patch.Value, want) {
			t.Errorf("value = %#v, want %#v", p.Patch.Value, want)
		}
		short := loadOnePatch(t, "[deployment.app.containers.0.name]\n.: 3\n")
		if !reflect.DeepEqual(p.Patch, short.Patch) {
			t.Errorf("spelled header gives %#v, the shorthand header gives %#v", p.Patch, short.Patch)
		}
	})
	t.Run("L3_shorthand_ports_is_the_container_field", func(t *testing.T) {
		p := loadOnePatch(t, "[deployment.app.containers.0.ports]\n.: 8080\n")
		want := map[string]any{"ports..": "8080"}
		if !reflect.DeepEqual(p.Patch.Value, want) {
			t.Errorf("value = %#v, want %#v", p.Patch.Value, want)
		}
	})
}

func expansionDeployment() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "app"},
		"spec": map[string]any{
			"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "main", "image": "old"}},
			}},
		},
	}}
}

// applyTOMLToDeployment loads src, applies it to the fixture and returns its containers.
func applyTOMLToDeployment(t *testing.T, src string) []any {
	t.Helper()
	patches, err := LoadTOMLPatchFile(strings.NewReader(src), nil)
	if err != nil {
		t.Fatalf("LoadTOMLPatchFile: %v", err)
	}
	set, err := NewPatchableAppSet([]*unstructured.Unstructured{expansionDeployment()}, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil || len(resolved) != 1 {
		t.Fatalf("Resolve: %d resources, err %v", len(resolved), err)
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	containers, found, err := unstructured.NestedSlice(resolved[0].Base.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		t.Fatalf("containers not found (err %v)", err)
	}
	return containers
}

func TestTOMLPatchApply_SpelledHeader(t *testing.T) {
	want := []any{map[string]any{"name": "main", "image": "nginx"}}
	t.Run("A1_spelled_header_sets_the_image", func(t *testing.T) {
		got := applyTOMLToDeployment(t, "[deployment.app.spec.template.spec.containers.0.image]\n.: nginx\n")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("containers = %#v, want %#v", got, want)
		}
	})
	t.Run("A2_shorthand_header_gives_the_same", func(t *testing.T) {
		got := applyTOMLToDeployment(t, "[deployment.app.containers.0.image]\n.: nginx\n")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("containers = %#v, want %#v", got, want)
		}
	})
}
