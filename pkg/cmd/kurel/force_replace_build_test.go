package kurel

import (
	"bytes"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/yaml"
)

// These tests pin the force-replace trait (go-kure/launcher#406) through the
// real `kurel build` entry point. That path is what proves the trait is
// registered on BOTH sides: pkg/oam's validTraitTypes allowlist (else parsing
// rejects the document) and builtinTraitHandlers (else the transform has no
// handler to dispatch it to). The trait sets a delivery intent on the
// application (go-kure/launcher#782); kurel's flat output has no place for it,
// so the output is the same with and without the trait.

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

// TestBuildCommand_JobWithForceReplaceTrait: with the trait, the job
// component's application carries the force-replace delivery intent, the Job
// keeps the component name, and the output is byte for byte what the component
// builds without the trait: no object carries a Flux annotation.
func TestBuildCommand_JobWithForceReplaceTrait(t *testing.T) {
	appYAML := forceReplaceJobYAML + "      traits:\n        - type: force-replace\n"
	docs, out, err := buildDocs(t, appYAML)
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
	jobs := 0
	for _, d := range docs {
		if kind, _ := d["kind"].(string); kind == "Job" {
			jobs++
			md, _ := d["metadata"].(map[string]any)
			if md["name"] != "migrate" {
				t.Errorf("Job name = %v, want %q (force-replace must not rename the Job)", md["name"], "migrate")
			}
		}
	}
	if jobs != 1 {
		t.Errorf("output has %d Job(s), want 1\noutput:\n%s", jobs, out)
	}
	assertNoFluxObjectKeys(t, docs...)
	_, plain, err := buildDocs(t, forceReplaceJobYAML)
	if err != nil {
		t.Fatalf("build without the trait failed: %v\noutput: %s", err, plain)
	}
	if out != plain {
		t.Errorf("the trait changed kurel's output\nwith:\n%s\nwithout:\n%s", out, plain)
	}

	want := map[string]stack.DeliveryIntent{"migrate": {ForceReplace: true}}
	if got := deliveryIntents(t, appYAML); !maps.Equal(got, want) {
		t.Errorf("delivery intents = %+v, want %+v", got, want)
	}
}

// TestBuildCommand_JobWithoutForceReplaceTrait pins the unchanged default: a
// job component without the trait states no intent and emits no force
// annotation, so a pod-level update stays an apply error rather than a silent
// delete-and-recreate.
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
	want := map[string]stack.DeliveryIntent{"migrate": {}}
	if got := deliveryIntents(t, forceReplaceJobYAML); !maps.Equal(got, want) {
		t.Errorf("delivery intents = %+v, want %+v", got, want)
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
