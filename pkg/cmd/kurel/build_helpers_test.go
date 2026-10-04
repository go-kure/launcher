package kurel

import (
	"bytes"
	"testing"
)

// emptyProfilePath is a ClusterProfile with no capability binding.
const emptyProfilePath = "testdata/empty-profile.yaml"

// runKurel runs the kurel command with args and returns its stdout and the
// command error.
func runKurel(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewKurelCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

// collisionAppYAML is an application "shop"; %s is the application namespace
// and %s the components.
const collisionAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: %s
spec:
  components:
%s`

// sharedConfigMapComponents are two components, %s and %s, of the given
// types, each with a configmap trait "settings".
const sharedConfigMapComponents = `    - name: %[1]s
      type: %[2]s
      properties:
        image: ghcr.io/example/%[1]s:v1.0.0
      traits:
        - type: configmap
          properties:
            name: settings
            data:
              KEY: %[1]s
    - name: %[3]s
      type: %[4]s
      properties:
        image: ghcr.io/example/%[3]s:v1.0.0
        port: 9090
      traits:
        - type: configmap
          properties:
            name: settings
            data:
              KEY: %[3]s
`
