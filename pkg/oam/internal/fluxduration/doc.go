// Package fluxduration checks a duration string against the form Flux's CRDs
// accept on their duration fields.
//
// Flux declares that form as a +kubebuilder:validation:Pattern on the Interval
// field of the source-controller v1 OCIRepositorySpec and HelmRepositorySpec,
// the helm-controller v2 HelmReleaseSpec and the kustomize-controller v1
// KustomizationSpec, at the API versions go.mod pins. The API server enforces
// it, so a value outside it builds cleanly and is then rejected at apply time.
// time.ParseDuration alone is wider: it accepts a sign and the ns, us and µs
// units.
//
// Validate checks the authored text. ValidateEmitted also checks what a
// metav1.Duration writes for it, which is Duration.String() rather than the
// authored text: a value below one millisecond is written in µs or ns, and a
// positive value below one nanosecond is written as 0s.
package fluxduration
