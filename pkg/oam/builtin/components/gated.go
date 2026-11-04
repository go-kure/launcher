package components

import "github.com/go-kure/launcher/pkg/oam"

// gated returns s tagged with the Kubernetes feature gate the property needs
// (oam.PropertySchema.Gate): a build for a ClusterProfile whose kubernetesVersion is
// below the release where the gate is on by default refuses an authored value
// (go-kure/launcher#1183). gate names a row of the lifecycle table in
// pkg/oam/featuregates.go; a test holds every Gate a schema carries to one.
func gated(gate string, s oam.PropertySchema) oam.PropertySchema {
	s.Gate = gate
	return s
}
