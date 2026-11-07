package patch

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// intDeployment holds integers at three depths: a map value, a value in a list of maps
// and a value two lists down. yaml.v3 decodes each of them to a Go int.
const intDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  generation: 2
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: myapp
        image: nginx:1.0
        ports:
        - containerPort: 8080
        livenessProbe:
          initialDelaySeconds: 5
`

// requireInt64 reads path as an int64 the way unstructured does, and fails when the
// stored value has another type or another number.
func requireInt64(t *testing.T, obj *unstructured.Unstructured, want int64, path ...string) {
	t.Helper()
	got, found, err := unstructured.NestedInt64(obj.Object, path...)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", strings.Join(path, "."), found, err)
	}
	if got != want {
		t.Errorf("%s = %d, want %d", strings.Join(path, "."), got, want)
	}
}

func requireContainerInts(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireInt64(t, obj, 1, "spec", "replicas")
	requireInt64(t, obj, 2, "metadata", "generation")
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("containers: found=%v err=%v len=%d", found, err, len(containers))
	}
	c, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container is %T", containers[0])
	}
	requireInt64(t, &unstructured.Unstructured{Object: c}, 5, "livenessProbe", "initialDelaySeconds")
	ports, found, err := unstructured.NestedSlice(c, "ports")
	if err != nil || !found || len(ports) != 1 {
		t.Fatalf("ports: found=%v err=%v len=%d", found, err, len(ports))
	}
	p, ok := ports[0].(map[string]any)
	if !ok {
		t.Fatalf("port is %T", ports[0])
	}
	requireInt64(t, &unstructured.Unstructured{Object: p}, 8080, "containerPort")
}

// An integer in a base resource must be an int64, because Unstructured.DeepCopy
// panics on a Go int and Resolve copies each targeted base resource.
func TestLoadResourcesFromMultiYAML_IntegersSurviveResolve(t *testing.T) {
	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(intDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesFromMultiYAML: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("loaded %d resources, want 1", len(resources))
	}
	requireContainerInts(t, resources[0])

	patches, err := LoadPatchFile(strings.NewReader("- target: myapp\n  patch:\n    spec.replicas: 3\n"))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSet(resources, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requireInt64(t, resolved[0].Base, 3, "spec", "replicas")
	requireInt64(t, resolved[0].Base, 2, "metadata", "generation")
}

func TestLoadResourcesWithStructure_IntegersSurviveResolve(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(intDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	if len(docSet.Documents) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docSet.Documents))
	}
	requireContainerInts(t, docSet.Documents[0].Resource)

	patches, err := LoadPatchFile(strings.NewReader("- target: myapp\n  patch:\n    spec.replicas: 3\n"))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSetWithStructure(docSet, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSetWithStructure: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requireInt64(t, resolved[0].Base, 3, "spec", "replicas")
}

// stampConfig holds the three timestamp shapes the loaders must hand back as the text the
// author wrote: a date, a time with a fraction and an offset, and (as a control) a quoted
// date that yaml.v3 already decodes as a string. It also holds a timestamp in a list element.
const stampConfig = `apiVersion: v1
kind: ConfigMap
metadata:
  name: stamps
spec:
  dateOnly: 2026-11-07
  fractionOffset: 2026-11-07T12:00:00.5+01:00
  quoted: "2026-11-07"
  list:
  - when: 2026-11-07
`

// keyedConfig holds maps that yaml.v3 decodes to map[interface{}]interface{}: one with
// integer keys (and an integer value) and one with a timestamp key.
const keyedConfig = `apiVersion: v1
kind: ConfigMap
metadata:
  name: stamps
spec:
  numbered:
    123: hello
    456: 7
  keys:
    2026-11-07: dated
`

func requireString(t *testing.T, obj map[string]any, want string, path ...string) {
	t.Helper()
	got, found, err := unstructured.NestedString(obj, path...)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", strings.Join(path, "."), found, err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", strings.Join(path, "."), got, want)
	}
}

func requireStampConfig(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireString(t, obj.Object, "2026-11-07", "spec", "dateOnly")
	requireString(t, obj.Object, "2026-11-07T12:00:00.5+01:00", "spec", "fractionOffset")
	requireString(t, obj.Object, "2026-11-07", "spec", "quoted")
	list, found, err := unstructured.NestedSlice(obj.Object, "spec", "list")
	if err != nil || !found || len(list) != 1 {
		t.Fatalf("list: found=%v err=%v len=%d", found, err, len(list))
	}
	item, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("list item is %T", list[0])
	}
	requireString(t, item, "2026-11-07", "when")
}

func requireKeyedConfig(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireString(t, obj.Object, "hello", "spec", "numbered", "123")
	requireInt64(t, obj, 7, "spec", "numbered", "456")
	requireString(t, obj.Object, "dated", "spec", "keys", "2026-11-07")
}

// resolveStamps resolves a no-op patch over the set, which copies the targeted base
// resource, and runs check on the resolved base.
func resolveStamps(t *testing.T, set *PatchableAppSet, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	check(t, resolved[0].Base)
}

const stampPatch = "- target: stamps\n  patch:\n    metadata.labels.touched: \"yes\"\n"

// A plain timestamp in a base resource must load as the text the author wrote, because
// yaml.v3 decodes it to time.Time, DeepCopy panics on that type and formatting it would
// rewrite what was authored.
func TestLoadResourcesFromMultiYAML_TimestampsKeepAuthoredText(t *testing.T) {
	multiYAMLSurvivesResolve(t, stampConfig, requireStampConfig)
}

// A mapping with non-string keys must load with string keys and int64 values, because
// yaml.v3 decodes it to map[interface{}]interface{}, which DeepCopy panics on.
func TestLoadResourcesFromMultiYAML_NonStringKeyMapsAreConverted(t *testing.T) {
	multiYAMLSurvivesResolve(t, keyedConfig, requireKeyedConfig)
}

func TestLoadResourcesWithStructure_TimestampsKeepAuthoredText(t *testing.T) {
	structureSurvivesResolve(t, stampConfig, requireStampConfig)
}

func TestLoadResourcesWithStructure_NonStringKeyMapsAreConverted(t *testing.T) {
	structureSurvivesResolve(t, keyedConfig, requireKeyedConfig)
}

func multiYAMLSurvivesResolve(t *testing.T, src string, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadResourcesFromMultiYAML: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("loaded %d resources, want 1", len(resources))
	}
	check(t, resources[0])

	patches, err := LoadPatchFile(strings.NewReader(stampPatch))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSet(resources, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolveStamps(t, set, check)
}

func structureSurvivesResolve(t *testing.T, src string, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	docSet, err := LoadResourcesWithStructure(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	if len(docSet.Documents) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docSet.Documents))
	}
	check(t, docSet.Documents[0].Resource)

	patches, err := LoadPatchFile(strings.NewReader(stampPatch))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSetWithStructure(docSet, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSetWithStructure: %v", err)
	}
	resolveStamps(t, set, check)
}

// The structure path keeps the yaml.Node it writes back from: the loader must not retag
// that node, because a string tag on a plain timestamp makes the encoder quote it.
func TestLoadResourcesWithStructure_WriteBackKeepsPlainTimestamp(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(stampConfig))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	out, err := yaml.Marshal(docSet.Documents[0].Node)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{
		"  dateOnly: 2026-11-07\n",
		"  fractionOffset: 2026-11-07T12:00:00.5+01:00\n",
		"  quoted: \"2026-11-07\"\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("write-back lacks %q:\n%s", want, out)
		}
	}
}

// A timestamp in a patch value must reach the patch as the authored text, in the
// list form and in the simple map form.
func TestLoadPatchFile_TimestampValueKeepsAuthoredText(t *testing.T) {
	for name, src := range map[string]string{
		"list":   "- target: stamps\n  patch:\n    spec.dateOnly: 2026-11-07\n    spec.fractionOffset: 2026-11-07T12:00:00.5+01:00\n    spec.quoted: \"2026-11-07\"\n",
		"simple": "spec.dateOnly: 2026-11-07\nspec.fractionOffset: 2026-11-07T12:00:00.5+01:00\nspec.quoted: \"2026-11-07\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			patches, err := LoadPatchFile(strings.NewReader(src))
			if err != nil {
				t.Fatalf("LoadPatchFile: %v", err)
			}
			if len(patches) != 3 {
				t.Fatalf("loaded %d patches, want 3", len(patches))
			}
			want := map[string]string{
				"spec.dateOnly":       "2026-11-07",
				"spec.fractionOffset": "2026-11-07T12:00:00.5+01:00",
				"spec.quoted":         "2026-11-07",
			}
			for _, p := range patches {
				if got, ok := p.Patch.Value.(string); !ok || got != want[p.Patch.Path] {
					t.Errorf("%s value is %T %v, want string %q", p.Patch.Path, p.Patch.Value, p.Patch.Value, want[p.Patch.Path])
				}
			}
		})
	}
}
