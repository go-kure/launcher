package patch

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These tests load the examples README.md shows and apply them to a Deployment.
// They read the examples out of README.md, under the heading that holds them, so
// the text a reader copies is the text that is tested: an example that stops
// loading fails here instead of in a reader's hands.

const readmeDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  namespace: default
  labels:
    app: myapp
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: myapp
        image: nginx:1.0
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
`

// readmeFence returns the body of the nth (from 0) fenced block of language lang
// under the heading line that starts with heading (for example "### List Selectors").
func readmeFence(t *testing.T, heading, lang string, n int) string {
	t.Helper()
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	var (
		inHeading bool
		inFence   bool
		fenceLang string
		body      []string
		seen      int
	)
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case inFence && strings.HasPrefix(line, "```"):
			inFence = false
			if inHeading && fenceLang == lang {
				if seen == n {
					return strings.Join(body, "\n") + "\n"
				}
				seen++
			}
		case inFence:
			body = append(body, line)
		case strings.HasPrefix(line, "```"):
			inFence = true
			fenceLang = strings.TrimPrefix(line, "```")
			body = nil
		case strings.HasPrefix(line, "#"):
			inHeading = strings.HasPrefix(line, heading)
		}
	}
	t.Fatalf("README.md: no %s fence number %d under %q", lang, n, heading)
	return ""
}

// applyReadmePatch loads patchText with LoadPatchFileWithVariables and applies it to a
// fresh copy of the Deployment.
func applyReadmePatch(t *testing.T, patchText string, ctx *VariableContext) *unstructured.Unstructured {
	t.Helper()
	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(readmeDeployment))
	if err != nil {
		t.Fatalf("load the Deployment: %v", err)
	}
	specs, err := LoadPatchFileWithVariables(strings.NewReader(patchText), ctx)
	if err != nil {
		t.Fatalf("load the patch: %v\n%s", err, patchText)
	}
	set, err := NewPatchableAppSet(resources, specs)
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
	return resolved[0].Base
}

func readmeContainers(t *testing.T, obj *unstructured.Unstructured) []map[string]any {
	t.Helper()
	list, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		t.Fatalf("containers: found=%v err=%v", found, err)
	}
	out := make([]map[string]any, len(list))
	for i, c := range list {
		m, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("container %d is %T", i, c)
		}
		out[i] = m
	}
	return out
}

func readmeField(t *testing.T, obj map[string]any, want string, path ...string) {
	t.Helper()
	v, found, err := unstructured.NestedFieldNoCopy(obj, path...)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", strings.Join(path, "."), found, err)
	}
	if got := fmt.Sprint(v); got != want {
		t.Errorf("%s = %q, want %q", strings.Join(path, "."), got, want)
	}
}

func TestReadme_TOMLFormat(t *testing.T) {
	obj := applyReadmePatch(t, readmeFence(t, "### TOML Format", "toml", 0), nil)

	readmeField(t, obj.Object, "3", "spec", "replicas")
	readmeField(t, obj.Object, "production", "metadata", "labels", "env")
	c := readmeContainers(t, obj)[0]
	readmeField(t, c, "nginx:1.27", "image")
	readmeField(t, c, "200m", "resources", "requests", "cpu")
	readmeField(t, c, "256Mi", "resources", "requests", "memory")
	readmeField(t, c, "Always", "imagePullPolicy")
}

func TestReadme_VariableSubstitution(t *testing.T) {
	ctx := &VariableContext{
		Values: map[string]any{
			"registry": "docker.io",
			"image":    "myapp",
			"tag":      "v1.0.0",
			"replicas": 3,
		},
		Features: map[string]bool{"debug": true},
	}
	obj := applyReadmePatch(t, readmeFence(t, "### Variable Substitution", "toml", 0), ctx)

	readmeField(t, obj.Object, "3", "spec", "replicas")
	readmeField(t, readmeContainers(t, obj)[0], "docker.io/myapp:v1.0.0", "image")
}

func TestReadme_ListSelectors(t *testing.T) {
	t.Run("by index and by name", func(t *testing.T) {
		obj := applyReadmePatch(t, readmeFence(t, "### List Selectors", "toml", 0), nil)
		readmeField(t, readmeContainers(t, obj)[0], "updated:latest", "image")
	})
	t.Run("append", func(t *testing.T) {
		obj := applyReadmePatch(t, readmeFence(t, "### List Selectors", "yaml", 0), nil)
		cs := readmeContainers(t, obj)
		if len(cs) != 2 {
			t.Fatalf("%d containers after the append, want 2", len(cs))
		}
		readmeField(t, cs[1], "sidecar", "name")
		readmeField(t, cs[1], "envoy:v1.28", "image")
	})
}

func TestReadme_YAMLFormat(t *testing.T) {
	t.Run("flat map", func(t *testing.T) {
		obj := applyReadmePatch(t, readmeFence(t, "### YAML Format", "yaml", 0), nil)
		readmeField(t, obj.Object, "3", "spec", "replicas")
		readmeField(t, obj.Object, "production", "metadata", "labels", "env")
	})
	t.Run("targeted list", func(t *testing.T) {
		obj := applyReadmePatch(t, readmeFence(t, "### YAML Format", "yaml", 1), nil)
		readmeField(t, obj.Object, "3", "spec", "replicas")
		readmeField(t, obj.Object, "production", "metadata", "labels", "env")
		readmeField(t, readmeContainers(t, obj)[0], "nginx:1.27", "image")
	})
	t.Run("kind.name prefix is not a flat-map target", func(t *testing.T) {
		specs, err := LoadPatchFile(strings.NewReader("deployment.myapp.spec.replicas: 3\n"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		resources, err := LoadResourcesFromMultiYAML(strings.NewReader(readmeDeployment))
		if err != nil {
			t.Fatalf("load the Deployment: %v", err)
		}
		_, err = NewPatchableAppSet(resources, specs)
		if err == nil || !strings.Contains(err.Error(), "could not determine target resource") {
			t.Errorf("err = %v, want one containing %q", err, "could not determine target resource")
		}
	})
	t.Run("other top-level shape is refused", func(t *testing.T) {
		_, err := LoadPatchFileWithVariables(strings.NewReader("just a scalar\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "unrecognized patch format") {
			t.Errorf("err = %v, want one containing %q", err, "unrecognized patch format")
		}
	})
	t.Run("unknown type is refused", func(t *testing.T) {
		_, err := LoadPatchFileWithVariables(strings.NewReader("- target: deployment.myapp\n  type: merge\n  patch:\n    spec.replicas: 3\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "unknown patch type") {
			t.Errorf("err = %v, want one containing %q", err, "unknown patch type")
		}
	})
}

// The Quick Start writes a patched file from a set built with the document layout, and
// says WriteToFile fails on a set built without it.
func TestReadme_QuickStartWriteToFile(t *testing.T) {
	specs, err := LoadPatchFile(strings.NewReader(readmeFence(t, "### TOML Format", "toml", 0)))
	if err != nil {
		t.Fatalf("load the patch: %v", err)
	}
	out := t.TempDir() + "/output.yaml"

	docs, err := LoadResourcesWithStructure(strings.NewReader(readmeDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	set, err := NewPatchableAppSetWithStructure(docs, specs)
	if err != nil {
		t.Fatalf("NewPatchableAppSetWithStructure: %v", err)
	}
	if err := set.WriteToFile(out); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, want := range []string{"replicas: 3", "env: production", "image: nginx:1.27"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("output lacks %q:\n%s", want, written)
		}
	}

	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(readmeDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesFromMultiYAML: %v", err)
	}
	plain, err := NewPatchableAppSet(resources, specs)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	if err := plain.WriteToFile(out); err == nil || !strings.Contains(err.Error(), "no document set available") {
		t.Errorf("WriteToFile on a set without a document set: err = %v, want one containing %q", err, "no document set available")
	}
}

// A line the TOML loader cannot split at a colon is refused, which is what the README
// states and what its old `key = value` examples ran into.
func TestReadme_TOMLLineWithoutAColonIsRefused(t *testing.T) {
	_, err := LoadTOMLPatchFile(strings.NewReader("[deployment.myapp]\nspec.replicas = 3\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid patch line format") {
		t.Errorf("err = %v, want one containing %q", err, "invalid patch line format")
	}
}
