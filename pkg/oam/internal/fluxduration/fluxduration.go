package fluxduration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-kure/launcher/pkg/errors"
)

// Form is the pattern one kind of Flux duration field takes. Flux uses two:
// Interval and SourceTimeout.
type Form struct {
	pattern *regexp.Regexp
	units   string
	example string
	hours   bool
}

var (
	// Interval is the form of every helm-controller HelmRelease duration field
	// and of spec.interval on the source-controller and kustomize-controller
	// kinds: units ms, s, m and h.
	Interval = Form{
		pattern: regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`),
		units:   "ms, s, m, h",
		example: "10m, 1h30m",
		hours:   true,
	}
	// SourceTimeout is the form of spec.timeout on the source-controller
	// HelmRepository, OCIRepository, GitRepository and Bucket kinds: units ms, s
	// and m, with no h. A duration of an hour or more is emitted in minutes
	// (Form.Format).
	SourceTimeout = Form{
		pattern: regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m))+$`),
		units:   "ms, s, m",
		example: "30s, 5m",
	}
)

// Describe states the form for an error message: "unsigned; units ms, s, m, h;
// e.g. 10m, 1h30m" for Interval.
func (f Form) Describe() string {
	return fmt.Sprintf("unsigned; units %s; e.g. %s", f.units, f.example)
}

// ErrForm reports a value that time.ParseDuration accepts but that is outside
// the form's pattern: signed, or in a unit the form does not take (ns, us and
// µs for both forms, h for SourceTimeout).
var ErrForm = errors.New("not a Flux duration: signed, or in a unit the field does not take")

// ResolutionError reports a value that is itself a Flux duration but that is
// not emitted as one, or not as the same value. A caller emits the parsed
// duration in Form.Format, not as the authored text, and that switches to µs or
// ns below one millisecond: 0.5ms is written as 500µs, which is outside Flux's
// pattern. And a positive value below time.Duration's nanosecond resolution
// parses to zero: 0.0000000001ms is written as 0s, which Flux accepts but is not
// the value authored.
type ResolutionError struct {
	// Value is the authored duration.
	Value string
	// Emitted is the form the duration is emitted in (Form.Format).
	Emitted string
}

func (e *ResolutionError) Error() string {
	return fmt.Sprintf("%q is emitted as %q, below Flux's millisecond resolution", e.Value, e.Emitted)
}

// Format returns the text d is emitted as under the form. That is
// Duration.String(), which a metav1.Duration serializes to, except under a form
// without h (SourceTimeout), where a duration of an hour or more has its hours
// folded into its minutes: 1h30m0s is written as 90m0s. A caller emitting such a
// duration has to write this text in place of the metav1.Duration's.
func (f Form) Format(d time.Duration) string {
	s := d.String()
	if f.hours || d < time.Hour {
		return s
	}
	// Duration.String() writes a duration of an hour or more as <h>h<m>m<s>s,
	// so its seconds part follows the first m.
	_, seconds, _ := strings.Cut(s, "m")
	return strconv.FormatInt(int64(d/time.Minute), 10) + "m" + seconds
}

// Validate checks value as authored against Interval. See Form.Validate.
func Validate(value string) error {
	return Interval.Validate(value)
}

// ValidateEmitted checks value against Interval. See Form.ValidateEmitted.
func ValidateEmitted(value string) error {
	return Interval.ValidateEmitted(value)
}

// Validate checks value as authored: time.ParseDuration must accept it, and it
// must match the form's pattern. A parse failure returns time.ParseDuration's
// own error unchanged; a value outside the pattern returns ErrForm.
//
// It does not check the form the value is finally emitted in; see
// ValidateEmitted.
func (f Form) Validate(value string) error {
	_, err := f.parse(value)
	return err
}

// ValidateEmitted checks value as Validate does, then the parsed duration as
// ValidateDuration does, and last that it is not 0s for a value authored as
// anything but zero, which returns a *ResolutionError.
//
// Use it where the value reaches the output as a parsed duration, emitted in
// Format rather than as the authored text.
func (f Form) ValidateEmitted(value string) error {
	d, err := f.parse(value)
	if err != nil {
		return err
	}
	if err := f.validateDuration(value, d); err != nil {
		return err
	}
	// time.ParseDuration truncates below one nanosecond without an error, so a
	// positive value can parse to zero. Inside the pattern the only digits are
	// the number parts, so a value authored as zero ("0s", "0h0m", "0.000s")
	// has no digit other than 0.
	if d == 0 && strings.ContainsAny(value, "123456789") {
		return &ResolutionError{Value: value, Emitted: d.String()}
	}
	return nil
}

// ValidateDuration checks d in the form it is emitted in, Format: it must
// match the form's pattern. A signed duration returns ErrForm; one below a
// millisecond, a *ResolutionError, whose Value is d.String().
//
// Use it on a duration that was never authored as text, such as a field of a
// config built directly.
func (f Form) ValidateDuration(d time.Duration) error {
	return f.validateDuration(d.String(), d)
}

func (f Form) validateDuration(value string, d time.Duration) error {
	emitted := f.Format(d)
	switch {
	case f.pattern.MatchString(emitted):
		return nil
	case d < 0:
		return ErrForm
	default:
		return &ResolutionError{Value: value, Emitted: emitted}
	}
}

func (f Form) parse(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if !f.pattern.MatchString(value) {
		return 0, ErrForm
	}
	return d, nil
}
