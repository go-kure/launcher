package fluxduration

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-kure/launcher/pkg/errors"
)

// pattern is the Flux CRD pattern the package documentation describes.
var pattern = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`)

// ErrForm reports a value that time.ParseDuration accepts but that is outside
// Flux's pattern: signed, or in the ns, us or µs unit.
var ErrForm = errors.New("not a Flux duration: unsigned, units ms, s, m, h")

// ResolutionError reports a value that is itself a Flux duration but that is
// not emitted as one, or not as the same value. A caller that emits the
// duration through a metav1.Duration writes Duration.String(), not the authored
// text. That switches to µs or ns below one millisecond: 0.5ms is written as
// 500µs, which is outside Flux's pattern. And a positive value below
// time.Duration's nanosecond resolution parses to zero: 0.0000000001ms is
// written as 0s, which Flux accepts but is not the value authored.
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

// ValidateEmitted checks value as Validate does, and then the parsed duration's
// String() form: it must also match Flux's pattern, and it must not be 0s for
// a value authored as anything but zero. Either failure returns a
// *ResolutionError.
//
// Use it where the value reaches the output as a metav1.Duration, which
// serializes through Duration.String().
func ValidateEmitted(value string) error {
	d, err := parse(value)
	if err != nil {
		return err
	}
	emitted := d.String()
	if !pattern.MatchString(emitted) {
		return &ResolutionError{Value: value, Emitted: emitted}
	}
	// time.ParseDuration truncates below one nanosecond without an error, so a
	// positive value can parse to zero. Inside the pattern the only digits are
	// the number parts, so a value authored as zero ("0s", "0h0m", "0.000s")
	// has no digit other than 0.
	if d == 0 && strings.ContainsAny(value, "123456789") {
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
