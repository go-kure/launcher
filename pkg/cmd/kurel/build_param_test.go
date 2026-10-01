package kurel

import (
	"bytes"
	"strings"
	"testing"
)

// --- go-kure/launcher#421: array/object parameters, end to end ---

const testStructuredKurelYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: test-pkg
spec:
  parameters:
  - name: env
    type: array
    required: false
    default:
    - name: LOG_LEVEL
      value: info
`

const testStructuredAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: ghcr.io/example/web:v1
      port: 8080
      env: ${env}
`

func runStructuredBuild(t *testing.T, values string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testStructuredAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testStructuredKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	args := []string{"build", appPath, "--profile", profilePath}
	if values != "" {
		args = append(args, "--values", writeTempFile(t, dir, "values.yaml", values))
	}
	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// TestBuildCommand_ArrayParameter_ValuesFile: a list supplied with --values is
// substituted as the component's env list and reaches the rendered Deployment.
func TestBuildCommand_ArrayParameter_ValuesFile(t *testing.T) {
	got, err := runStructuredBuild(t, "env:\n- name: LOG_LEVEL\n  value: debug\n- name: REGION\n  value: eu-west-1\n")
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, got)
	}
	for _, want := range []string{"name: LOG_LEVEL", "value: debug", "name: REGION", "value: eu-west-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
}

// TestBuildCommand_ArrayParameter_Default: with no value supplied, the declared
// list default is substituted.
func TestBuildCommand_ArrayParameter_Default(t *testing.T) {
	got, err := runStructuredBuild(t, "")
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, got)
	}
	if !strings.Contains(got, "name: LOG_LEVEL") || !strings.Contains(got, "value: info") {
		t.Errorf("expected the default env entry in output:\n%s", got)
	}
}

// TestBuildCommand_ArrayParameter_ContentValidatedByHandler: the parameter checks
// only the shape; the substituted list is then validated against the webservice
// schema like an authored one, so an entry of the wrong type is refused.
func TestBuildCommand_ArrayParameter_ContentValidatedByHandler(t *testing.T) {
	got, err := runStructuredBuild(t, "env:\n- name: LOG_LEVEL\n  value: 7\n")
	if err == nil {
		t.Fatalf("expected the webservice schema to refuse env[0].value: 7, got success:\n%s", got)
	}
	if !strings.Contains(err.Error(), "properties.env[0].value: expected string") {
		t.Errorf("error %q is not the webservice schema refusing env[0].value", err)
	}
}

// TestBuildCommand_ArrayParameter_WrongShapeRefused: a map supplied for an array
// parameter is refused before substitution.
func TestBuildCommand_ArrayParameter_WrongShapeRefused(t *testing.T) {
	_, err := runStructuredBuild(t, "env:\n  LOG_LEVEL: debug\n")
	if err == nil || !strings.Contains(err.Error(), "is a map, not a list") {
		t.Fatalf("expected a shape refusal, got %v", err)
	}
}
