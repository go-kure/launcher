package kurel

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These two tests pin the accepted side of go-kure/launcher#1330: the raw-PodSpec kinds
// refuse the values of an init container's restartPolicy that need the
// ContainerRestartRules gate (rows in openGateCases and leafCasingCases), and accept the
// rest. They use no field the change adds, so they also run on the base, where they pass.

// TestFeatureGates_OpenSchemasInitRestartPolicyAlways: Always on an init container is
// accepted at every version, as Kubernetes accepts it with the gate off. It makes the
// init container a native sidecar. This is the other half of the value gate, which
// refuses Never and OnFailure.
func TestFeatureGates_OpenSchemasInitRestartPolicyAlways(t *testing.T) {
	const spec = `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
initContainers:
  - name: proxy
    image: ghcr.io/example/proxy:v1.0.0
    restartPolicy: Always
`
	oldest := oam.KubernetesVersion{Major: 1, Minor: 1}
	below := oam.KubernetesVersion{Major: 1, Minor: 34}
	for _, kind := range openGateCaseKinds {
		t.Run(kind, func(t *testing.T) {
			doc, _ := openGateDoc(kind, spec)
			for _, v := range []*oam.KubernetesVersion{nil, &oldest, &below} {
				if err := validateForTarget(t, doc, v); err != nil {
					t.Errorf("target %v: want Always on an init container accepted, got %v", v, err)
				}
			}
		})
	}
}

// TestFeatureGates_OpenSchemasRestartPolicyNullUsesNoGate: a null restartPolicy is absent
// at every depth, so it trips neither the value gate of an init container nor the gate of
// a regular container.
func TestFeatureGates_OpenSchemasRestartPolicyNullUsesNoGate(t *testing.T) {
	oldest := oam.KubernetesVersion{Major: 1, Minor: 1}
	specs := map[string]string{
		"container": "containers:\n  - name: app\n    image: ghcr.io/example/app:v1.0.0\n    restartPolicy: null\n",
		"initContainer": "containers:\n  - name: app\n    image: ghcr.io/example/app:v1.0.0\n" +
			"initContainers:\n  - name: migrate\n    image: ghcr.io/example/migrate:v1.0.0\n    restartPolicy: null\n",
	}
	for _, kind := range openGateCaseKinds {
		for name, spec := range specs {
			t.Run(kind+"/"+name, func(t *testing.T) {
				doc, _ := openGateDoc(kind, spec)
				if err := validateForTarget(t, doc, &oldest); err != nil {
					t.Fatalf("a null restartPolicy must pass any version, got %v", err)
				}
			})
		}
	}
}
