package components

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// The Flux duration checks the components share. A Flux CRD declares a pattern
// on each of its duration fields, and the API server enforces it, so a value
// outside it builds cleanly and is then rejected at apply time. Every duration
// launcher emits goes out as the parsed duration, in fluxduration's
// Form.Format rather than the authored text, so the emitted form is checked as
// well as the authored one (package fluxduration). That is the
// metav1.Duration's own Duration.String(), except for a source timeout of an
// hour or more (emitFluxSource).

// fluxDurationField is one Flux duration field of a kind-named component whose
// properties decode strictly into the Flux spec S (go-kure/launcher#601,
// go-kure/launcher#606): its JSON path in the component's properties, the form
// its CRD pattern takes, and how to read the decoded value.
type fluxDurationField[S any] struct {
	path []string
	form fluxduration.Form
	// get returns the decoded duration, or nil when the field, or a struct
	// that holds it, is unset.
	get func(*S) *metav1.Duration
}

func (f fluxDurationField[S]) name() string {
	return strings.Join(f.path, ".")
}

// effectiveInterval is the interval the oci component emits: the authored one,
// or 60m when unset.
func effectiveInterval(interval string) string {
	if interval == "" {
		return "60m"
	}
	return interval
}

// parseDuration reads an interval validateFluxInterval has already accepted.
func parseDuration(s string) metav1.Duration {
	d, _ := time.ParseDuration(s)
	return metav1.Duration{Duration: d}
}

// validateFluxInterval refuses an authored interval the Flux CRDs the oci
// component emits would reject at apply time. The interval
// reaches them through parseDuration, as a metav1.Duration, so the emitted form
// is checked as well as the authored one; that also refuses a positive value
// too small for the duration type, which would be emitted as 0s. Called at parse
// time (ToApplicationConfig) and again from Generate, which is what covers a
// config built directly rather than parsed.
func validateFluxInterval(component, interval string) error {
	return validateFluxDuration(component, "interval", interval, fluxduration.Interval)
}

// validateFluxDuration checks an authored value of the named field in form:
// as authored, and in the form it is emitted in.
func validateFluxDuration(component, field, value string, form fluxduration.Form) error {
	return fluxDurationError(component, field, value, form, form.ValidateEmitted(value))
}

// fluxDurationError turns a fluxduration error for value into the component's
// build error, naming the field. nil stays nil.
func fluxDurationError(component, field, value string, form fluxduration.Form, err error) error {
	if err == nil {
		return nil
	}
	var re *fluxduration.ResolutionError
	if errors.As(err, &re) {
		return errors.Errorf("%s: %s %q is invalid: it would be emitted as %q, below Flux's millisecond resolution (use 0s or at least 1ms)", component, field, value, re.Emitted)
	}
	return errors.Errorf("%s: %s %q is invalid: must be a Flux duration (%s)", component, field, value, form.Describe())
}

// checkAuthoredFluxDurations checks each field's authored text. It reads the
// text from the property map, because the decoded duration has lost it: a
// positive value below a nanosecond decodes to zero, which Generate would read
// as unset or emit as 0s. Called after the strict decode, so a present value is
// JSON text; keys match case-insensitively there, as in encoding/json, so every
// spelling is checked, at every level of the path. The text is read through the
// same JSON encoding the decode used, so a value Go code built with a named
// string type is checked like a plain string.
func checkAuthoredFluxDurations[S any](component string, props map[string]any, fields []fluxDurationField[S]) error {
	for _, f := range fields {
		if err := checkAuthoredFluxDuration(component, props, f.path, f.name(), f.form); err != nil {
			return err
		}
	}
	return nil
}

func checkAuthoredFluxDuration(component string, props map[string]any, path []string, field string, form fluxduration.Form) error {
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if !strings.EqualFold(k, path[0]) {
			continue
		}
		raw, err := json.Marshal(props[k])
		if err != nil {
			continue
		}
		if len(path) > 1 {
			var nested map[string]any
			if json.Unmarshal(raw, &nested) != nil {
				continue
			}
			if err := checkAuthoredFluxDuration(component, nested, path[1:], field, form); err != nil {
				return err
			}
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			continue
		}
		if err := validateFluxDuration(component, field, s, form); err != nil {
			return err
		}
	}
	return nil
}

// checkFluxDurations checks each decoded field in the form it is emitted,
// Form.Format. It covers a config built directly rather than parsed: a negative
// or sub-millisecond duration is emitted outside the field's pattern. Unset and
// zero are passed over: Generate defaults a zero interval, and a set zero is
// emitted as 0s, which every form accepts.
func checkFluxDurations[S any](component string, spec *S, fields []fluxDurationField[S]) error {
	for _, f := range fields {
		d := f.get(spec)
		if d == nil || d.Duration == 0 {
			continue
		}
		if err := fluxDurationError(component, f.name(), d.Duration.String(), f.form, f.form.ValidateDuration(d.Duration)); err != nil {
			return err
		}
	}
	return nil
}

// emitFluxSource returns the source CR obj of the named component for Generate,
// timeout being its spec.timeout. A metav1.Duration serializes as
// Duration.String(), which writes an h from an hour on, and spec.timeout takes
// no h (fluxduration.SourceTimeout). So a timeout of an hour or more goes out
// as an unstructured copy of obj whose spec.timeout is in minutes, 90m0s for
// 1h30m (go-kure/launcher#619). Every other source is emitted as the typed
// object, as before.
func emitFluxSource(component string, obj client.Object, timeout *metav1.Duration) ([]*client.Object, error) {
	if timeout != nil {
		if text := fluxduration.SourceTimeout.Format(timeout.Duration); text != timeout.Duration.String() {
			m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return nil, errors.Wrapf(err, "%s: convert the source to set spec.timeout %q", component, text)
			}
			if err := unstructured.SetNestedField(m, text, "spec", "timeout"); err != nil {
				return nil, errors.Wrapf(err, "%s: set spec.timeout %q", component, text)
			}
			obj = &unstructured.Unstructured{Object: m}
		}
	}
	return []*client.Object{&obj}, nil
}
