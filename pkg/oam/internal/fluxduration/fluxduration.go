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
package fluxduration

import (
	"fmt"
	"regexp"
	"time"

	"github.com/go-kure/launcher/pkg/errors"
)

// pattern is the Flux CRD pattern quoted above.
var pattern = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`)

// ErrForm reports a value that time.ParseDuration accepts but that is outside
// Flux's pattern: signed, or in the ns, us or µs unit.
var ErrForm = errors.New("not a Flux duration: unsigned, units ms, s, m, h")

// ResolutionError reports a value that is itself a Flux duration but whose
// emitted form is not. A caller that emits the duration through a
// metav1.Duration writes Duration.String(), not the authored text, and that
// switches to µs or ns below one millisecond: 0.5ms is written as 500µs.
type ResolutionError struct {
	// Value is the authored duration.
	Value string
	// Emitted is the Duration.String() form a metav1.Duration serializes to.
	Emitted string
}

func (e *ResolutionError) Error() string {
	return fmt.Sprintf("%q is emitted as %q, below Flux's millisecond resolution", e.Value, e.Emitted)
}

// Validate checks value as authored: time.ParseDuration must accept it, and it
// must match Flux's pattern. A parse failure returns time.ParseDuration's own
// error unchanged; a value outside the pattern returns ErrForm.
//
// It does not check the form the value is finally emitted in; see
// ValidateEmitted.
func Validate(value string) error {
	_, err := parse(value)
	return err
}

// ValidateEmitted checks value as Validate does, and then that the parsed
// duration's String() form also matches Flux's pattern, returning a
// *ResolutionError when it does not.
//
// Use it where the value reaches the output as a metav1.Duration, which
// serializes through Duration.String().
func ValidateEmitted(value string) error {
	d, err := parse(value)
	if err != nil {
		return err
	}
	if emitted := d.String(); !pattern.MatchString(emitted) {
		return &ResolutionError{Value: value, Emitted: emitted}
	}
	return nil
}

func parse(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if !pattern.MatchString(value) {
		return 0, ErrForm
	}
	return d, nil
}
