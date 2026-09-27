package kurel

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// These tests pin the force-replace trait (go-kure/launcher#406) through the
// real `kurel build` entry point. That path is what proves the trait is
// registered on BOTH sides: pkg/oam's validTraitTypes allowlist (else parsing
// rejects the document) and builtinTraitHandlers (else the transform has no
// handler to dispatch it to).

const forceReplaceJobYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-job
  namespace: default
spec:
  components:
    - name: migrate
      type: job
      properties:
        image: ghcr.io/example/migrate:v1.0.0
`

// buildDocs runs `kurel build` on appYAML and returns its output split into
// one decoded object per YAML document, plus the raw output for messages.
func buildDocs(t *testing.T, appYAML string) ([]map[string]any, string, error) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		return nil, out.String(), err
	}

	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, out.String(), nil
}

func docAnnotation(obj map[string]any, key string) (string, bool) {
	md, _ := obj["metadata"].(map[string]any)
	ann, _ := md["annotations"].(map[string]any)
	v, ok := ann[key].(string)
	return v, ok
}

// TestBuildCommand_JobWithForceReplaceTrait: with the trait, every object the
// job component emits — the Job and its ServiceAccount — carries
// kustomize.toolkit.fluxcd.io/force: enabled, and the Job keeps the component
// name (which is what the auto health check targets).
func TestBuildCommand_JobWithForceReplaceTrait(t *testing.T) {
	docs, out, err := buildDocs(t, forceReplaceJobYAML+"      traits:\n        - type: force-replace\n")
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
	kinds := map[string]bool{}
	for _, d := range docs {
		kind, _ := d["kind"].(string)
		kinds[kind] = true
		if kind == "Job" {
			md, _ := d["metadata"].(map[string]any)
			if md["name"] != "migrate" {
				t.Errorf("Job name = %v, want %q (force-replace must not rename the Job)", md["name"], "migrate")
			}
		}
		if v, _ := docAnnotation(d, "kustomize.toolkit.fluxcd.io/force"); v != "enabled" {
			t.Errorf("%s: kustomize.toolkit.fluxcd.io/force = %q, want \"enabled\"\noutput:\n%s", kind, v, out)
		}
	}
	for _, k := range []string{"Job", "ServiceAccount"} {
		if !kinds[k] {
			t.Errorf("output has no %s; the annotation assertion is vacuous for it\noutput:\n%s", k, out)
		}
	}
}

// TestBuildCommand_JobWithoutForceReplaceTrait pins the unchanged default: a
// job component without the trait emits no force annotation, so a pod-level
// update stays an apply error rather than a silent delete-and-recreate.
func TestBuildCommand_JobWithoutForceReplaceTrait(t *testing.T) {
	docs, out, err := buildDocs(t, forceReplaceJobYAML)
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
	if len(docs) == 0 {
		t.Fatal("build emitted no documents")
	}
	for _, d := range docs {
		if v, ok := docAnnotation(d, "kustomize.toolkit.fluxcd.io/force"); ok {
			t.Errorf("%v: carries kustomize.toolkit.fluxcd.io/force=%q without the trait", d["kind"], v)
		}
	}
}

// TestBuildCommand_ForceReplaceTrait_RejectsProperties: the trait accepts no
// properties, so any authored key is a build error naming it rather than being
// dropped in silence.
func TestBuildCommand_ForceReplaceTrait_RejectsProperties(t *testing.T) {
	appYAML := forceReplaceJobYAML + `      traits:
        - type: force-replace
          properties:
            enabled: true
`
	_, out, err := buildDocs(t, appYAML)
	if err == nil {
		t.Fatalf("build succeeded with an undeclared force-replace property; want an error\noutput:\n%s", out)
	}
	want := `component "migrate": trait "force-replace": properties: unsupported field "enabled"`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error does not name the undeclared property\n got: %v\nwant substring: %s", err, want)
	}
}
