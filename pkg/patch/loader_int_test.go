package patch

import (
	"strings"
	"testing"

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
// panics on a Go int and Resolve copies every base resource.
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
