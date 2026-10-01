// Package fluxduration checks a duration string against the form Flux's CRDs
// accept on their duration fields.
//
// Flux declares that form as a +kubebuilder:validation:Pattern on each duration
// field, at the API versions go.mod pins, and uses two: Interval, on every
// helm-controller v2 HelmReleaseSpec duration and on the Interval of the
// source-controller and kustomize-controller kinds, and SourceTimeout, on the
// Timeout of the source-controller v1 HelmRepositorySpec, OCIRepositorySpec,
// GitRepositorySpec and BucketSpec, which has no h unit. The API server enforces
// the pattern, so a value outside it builds cleanly and is then rejected at
// apply time. time.ParseDuration alone is wider: it accepts a sign and the ns,
// us and µs units.
//
// Form.Validate checks the authored text. Form.ValidateEmitted also checks what
// a metav1.Duration writes for it, which is Duration.String() rather than the
// authored text: a value below one millisecond is written in µs or ns, a
// positive value below one nanosecond is written as 0s, and a value of an hour
// or more is written with an h. Form.ValidateDuration checks a decoded duration
// alone. The package-level Validate and ValidateEmitted use Interval.
package fluxduration
