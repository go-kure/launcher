package kurel

import (
	"bytes"
	"strings"
	"testing"
)

// TestBuildCommand_RoleDeploymentIsNotAuthorable: `role-deployment` is the member
// webservice and worker lower into (go-kure/launcher#1629). It is registered so the
// lowered object builds, and no document may name it: the reshaped form belongs to
// webservice and worker.
func TestBuildCommand_RoleDeploymentIsNotAuthorable(t *testing.T) {
	dir := t.TempDir()
	app := writeTempFile(t, dir, "app.yaml", `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
  - name: web
    type: role-deployment
    properties:
      image: ghcr.io/example/web:v1
`)
	profile := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", app, "--profile", profile})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("a document naming type role-deployment built:\n%s", out.String())
	}
	if want := `invalid value "role-deployment" for "type" in "web"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not hold %q", err, want)
	}
}
