package components

import (
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestContainerFieldsSchemaMatchesParser pins schemaContainerFields' key set to
// containerFieldKeys and its `resizePolicy` entry to resizePolicyEntryKeys, so a
// key is published exactly when parseContainerFields reads it.
func TestContainerFieldsSchemaMatchesParser(t *testing.T) {
	schema := schemaContainerFields()
	got := slices.Sorted(maps.Keys(schema))
	want := slices.Sorted(slices.Values(containerFieldKeys))
	if !slices.Equal(got, want) {
		t.Errorf("schemaContainerFields keys = %v\nparser reads %v", got, want)
	}
	item := schema["resizePolicy"].Items
	if item == nil {
		t.Fatal("resizePolicy has no Items")
	}
	if item.AdditionalProperties {
		t.Error("resizePolicy entry schema is open (AdditionalProperties: true)")
	}
	gotEntry := slices.Sorted(maps.Keys(item.Properties))
	wantEntry := slices.Sorted(slices.Values(resizePolicyEntryKeys))
	if !slices.Equal(gotEntry, wantEntry) {
		t.Errorf("resizePolicy entry keys = %v\nparser reads %v", gotEntry, wantEntry)
	}
	for k, p := range item.Properties {
		if !p.Required {
			t.Errorf("resizePolicy entry key %q is not Required; the parser requires it", k)
		}
	}
}

// TestContainerFieldsSchemaEnumsMatchParser: each published enum is the set the
// parser accepts, value for value.
func TestContainerFieldsSchemaEnumsMatchParser(t *testing.T) {
	schema := schemaContainerFields()
	entry := schema["resizePolicy"].Items.Properties
	for _, tc := range []struct {
		name string
		got  []any
		want []any
	}{
		{"imagePullPolicy", schema["imagePullPolicy"].Enum, enumValues(pullPolicies)},
		{"terminationMessagePolicy", schema["terminationMessagePolicy"].Enum, enumValues(terminationMessagePolicies)},
		{"resizePolicy.resourceName", entry["resourceName"].Enum, enumValues(resizeResources)},
		{"resizePolicy.restartPolicy", entry["restartPolicy"].Enum, enumValues(resizeRestartPolicies)},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s enum = %v, parser accepts %v", tc.name, tc.got, tc.want)
		}
	}
	// The sets themselves are the upstream ones (validatePullPolicy,
	// validateContainerCommon, validateResizePolicy).
	if !slices.Equal(pullPolicies, []corev1.PullPolicy{"Always", "IfNotPresent", "Never"}) {
		t.Errorf("pullPolicies = %v", pullPolicies)
	}
	if !slices.Equal(terminationMessagePolicies, []corev1.TerminationMessagePolicy{"File", "FallbackToLogsOnError"}) {
		t.Errorf("terminationMessagePolicies = %v", terminationMessagePolicies)
	}
	if !slices.Equal(resizeResources, []corev1.ResourceName{"cpu", "memory"}) {
		t.Errorf("resizeResources = %v", resizeResources)
	}
	if !slices.Equal(resizeRestartPolicies, []corev1.ResourceResizeRestartPolicy{"NotRequired", "RestartContainer"}) {
		t.Errorf("resizeRestartPolicies = %v", resizeRestartPolicies)
	}
}

// TestContainerFieldsSchema_EveryKeyDescribed: every property, nested property
// and array item carries a Description.
func TestContainerFieldsSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if s.Description == "" {
			t.Errorf("%s: no Description", path)
		}
		for k, p := range s.Properties {
			walk(path+"."+k, p)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	for k, s := range schemaContainerFields() {
		walk(k, s)
	}
}

// TestContainerFieldsApplyCopiesResizePolicy: apply hands the container its own
// list. buildPodSpec deep-copies every container it assembles, which hides an
// aliased list from a render-twice test, so the copy is pinned here, on the
// container apply writes to.
func TestContainerFieldsApplyCopiesResizePolicy(t *testing.T) {
	fields := ContainerFields{ResizePolicy: []corev1.ContainerResizePolicy{
		{ResourceName: corev1.ResourceCPU, RestartPolicy: corev1.NotRequired},
	}}
	var container corev1.Container
	fields.apply(&container)
	container.ResizePolicy[0].RestartPolicy = corev1.RestartContainer
	if got := fields.ResizePolicy[0].RestartPolicy; got != corev1.NotRequired {
		t.Errorf("editing the container's resize policy reached the parsed fields: %q", got)
	}
}

// TestContainerFieldKeysDoNotCollide: none of the keys is one the pod-level
// parser, an entry's own key list or a refusal list already uses, so copying
// the fragment into a kind's schema or an entry's key list overwrites nothing.
func TestContainerFieldKeysDoNotCollide(t *testing.T) {
	for _, k := range containerFieldKeys {
		if slices.Contains(podSpecPropertyKeys, k) {
			t.Errorf("container field %q is also a pod-level key", k)
		}
		if _, ok := podSpecRejectedKeys[k]; ok {
			t.Errorf("container field %q is also a refused pod-level key", k)
		}
		if _, ok := initContainerRejectedKeys[k]; ok {
			t.Errorf("container field %q is also a refused init container key", k)
		}
		for _, list := range [][]string{initContainerPropertyKeys, sidecarPropertyKeys} {
			if n := len(slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s != k })); n != 1 {
				t.Errorf("container field %q appears %d times in an entry key list, want once", k, n)
			}
		}
	}
}
