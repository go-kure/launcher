package fluxduration_test

import (
	stderrors "errors"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		value string
		// want is "ok", "parse" (time.ParseDuration's error) or "form" (ErrForm).
		want string
	}{
		{"10m", "ok"},
		{"1h30m", "ok"},
		{"1.5h", "ok"},
		{"500ms", "ok"},
		{"0s", "ok"},
		// Inside the pattern: Validate checks the authored text only.
		{"0.5ms", "ok"},
		{"0.0001s", "ok"},
		{"0.0000000001ms", "ok"},

		{"-5m", "form"},
		{"+5m", "form"},
		{"500us", "form"},
		{"1µs", "form"},
		{"100ns", "form"},

		{"", "parse"},
		{"5", "parse"},
		{"1d", "parse"},
		{"bogus", "parse"},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			err := fluxduration.Validate(tc.value)
			checkOutcome(t, tc.value, err, tc.want)
		})
	}
}

func TestValidateEmitted(t *testing.T) {
	cases := []struct {
		value string
		// want is "ok", "parse", "form" or "resolution" (*ResolutionError).
		want string
		// emitted is the expected ResolutionError.Emitted, for "resolution".
		emitted string
	}{
		{"10m", "ok", ""},
		{"1h30m", "ok", ""},
		{"1.5h", "ok", ""},
		{"500ms", "ok", ""},
		{"1ms", "ok", ""},
		{"0s", "ok", ""},
		{"0.0015s", "ok", ""},
		// Authored as zero, in every spelling: emitted as 0s, which is the value.
		{"0ms", "ok", ""},
		{"0h0m", "ok", ""},
		{"0.000s", "ok", ""},
		{"00.0h0ms", "ok", ""},

		{"0.5ms", "resolution", "500µs"},
		{"0.0001s", "resolution", "100µs"},
		{"0.000001s", "resolution", "1µs"},
		// Positive, but below time.Duration's nanosecond resolution:
		// time.ParseDuration truncates it to zero without an error.
		{"0.0000000001ms", "resolution", "0s"},
		{"0.0000000001s", "resolution", "0s"},
		{"0h0.0000000001s", "resolution", "0s"},

		{"-5m", "form", ""},
		{"+5m", "form", ""},
		{"500us", "form", ""},
		{"1µs", "form", ""},
		{"100ns", "form", ""},

		{"", "parse", ""},
		{"5", "parse", ""},
		{"1d", "parse", ""},
		{"bogus", "parse", ""},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			err := fluxduration.ValidateEmitted(tc.value)
			if tc.want != "resolution" {
				checkOutcome(t, tc.value, err, tc.want)
				return
			}
			var re *fluxduration.ResolutionError
			if !stderrors.As(err, &re) {
				t.Fatalf("ValidateEmitted(%q) = %v, want a *ResolutionError", tc.value, err)
			}
			if re.Value != tc.value || re.Emitted != tc.emitted {
				t.Errorf("ResolutionError = %+v, want Value %q, Emitted %q", re, tc.value, tc.emitted)
			}
		})
	}
}

// TestSourceTimeout_ValidateEmitted covers the form without h: an authored h is
// outside the pattern, and a value of an hour or more, authored without h, is
// emitted in minutes and accepted.
func TestSourceTimeout_ValidateEmitted(t *testing.T) {
	cases := []struct {
		value string
		// want is "ok", "parse", "form" or "resolution".
		want    string
		emitted string
	}{
		{"30s", "ok", ""},
		{"5m", "ok", ""},
		{"59m59.999s", "ok", ""},
		{"500ms", "ok", ""},
		{"0s", "ok", ""},

		{"60m", "ok", ""},
		{"3600s", "ok", ""},
		{"90m", "ok", ""},
		{"1440m", "ok", ""},
		{"60m0.0005s", "ok", ""},

		{"1h", "form", ""},
		{"1h30m", "form", ""},
		{"-5m", "form", ""},
		{"500us", "form", ""},

		{"0.5ms", "resolution", "500µs"},
		{"0.0000000001s", "resolution", "0s"},

		{"5", "parse", ""},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			err := fluxduration.SourceTimeout.ValidateEmitted(tc.value)
			checkEmittedOutcome(t, tc.value, err, tc.want, tc.value, tc.emitted)
		})
	}
}

// TestValidateDuration checks a duration that was never authored as text: its
// emitted form is the error's Value too.
func TestValidateDuration(t *testing.T) {
	cases := []struct {
		form fluxduration.Form
		name string
		d    time.Duration
		want string
	}{
		{fluxduration.Interval, "interval 2h", 2 * time.Hour, "ok"},
		{fluxduration.Interval, "interval 1ms", time.Millisecond, "ok"},
		{fluxduration.Interval, "interval zero", 0, "ok"},
		{fluxduration.Interval, "interval negative", -time.Minute, "form"},
		{fluxduration.Interval, "interval 500µs", 500 * time.Microsecond, "resolution"},
		{fluxduration.SourceTimeout, "timeout 59m", 59 * time.Minute, "ok"},
		{fluxduration.SourceTimeout, "timeout zero", 0, "ok"},
		{fluxduration.SourceTimeout, "timeout 1h", time.Hour, "ok"},
		{fluxduration.SourceTimeout, "timeout 25h", 25 * time.Hour, "ok"},
		{fluxduration.SourceTimeout, "timeout negative 2h", -2 * time.Hour, "form"},
		{fluxduration.SourceTimeout, "timeout 1ns", time.Nanosecond, "resolution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.form.ValidateDuration(tc.d)
			checkEmittedOutcome(t, tc.d.String(), err, tc.want, tc.d.String(), tc.d.String())
		})
	}
}

// TestFormat pins the text each form emits a duration as: Duration.String(),
// except that SourceTimeout folds the hours of a duration of an hour or more
// into its minutes. Every SourceTimeout text it gives for a non-negative
// duration is inside that form's pattern and parses back to the duration.
func TestFormat(t *testing.T) {
	cases := []struct {
		form fluxduration.Form
		name string
		d    time.Duration
		want string
	}{
		{fluxduration.Interval, "interval 90m", 90 * time.Minute, "1h30m0s"},
		{fluxduration.Interval, "interval 25h", 25 * time.Hour, "25h0m0s"},
		{fluxduration.SourceTimeout, "timeout zero", 0, "0s"},
		{fluxduration.SourceTimeout, "timeout 500ms", 500 * time.Millisecond, "500ms"},
		{fluxduration.SourceTimeout, "timeout 59m", 59 * time.Minute, "59m0s"},
		{fluxduration.SourceTimeout, "timeout 59m59.999s", time.Hour - time.Millisecond, "59m59.999s"},
		{fluxduration.SourceTimeout, "timeout 1h", time.Hour, "60m0s"},
		{fluxduration.SourceTimeout, "timeout 1h30m", 90 * time.Minute, "90m0s"},
		{fluxduration.SourceTimeout, "timeout 1h30m0.5s", 90*time.Minute + 500*time.Millisecond, "90m0.5s"},
		{fluxduration.SourceTimeout, "timeout 1h0m0.0005s", time.Hour + 500*time.Microsecond, "60m0.0005s"},
		{fluxduration.SourceTimeout, "timeout 25h1m1s", 25*time.Hour + time.Minute + time.Second, "1501m1s"},
		{fluxduration.SourceTimeout, "timeout negative 2h", -2 * time.Hour, "-2h0m0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.form.Format(tc.d)
			if got != tc.want {
				t.Fatalf("Format(%v) = %q, want %q", tc.d, got, tc.want)
			}
			if tc.d < 0 {
				return
			}
			if err := tc.form.Validate(got); err != nil {
				t.Errorf("Format(%v) = %q, outside the form: %v", tc.d, got, err)
			}
			if back, err := time.ParseDuration(got); err != nil || back != tc.d {
				t.Errorf("Format(%v) = %q parses back as %v, %v", tc.d, got, back, err)
			}
		})
	}
}

// checkEmittedOutcome is checkOutcome plus the typed emission error, whose
// Value and Emitted must be value and emitted.
func checkEmittedOutcome(t *testing.T, label string, err error, want, value, emitted string) {
	t.Helper()
	switch want {
	case "resolution":
		var re *fluxduration.ResolutionError
		if !stderrors.As(err, &re) {
			t.Fatalf("%q: error = %v, want a *ResolutionError", label, err)
		}
		if re.Value != value || re.Emitted != emitted {
			t.Errorf("ResolutionError = %+v, want Value %q, Emitted %q", re, value, emitted)
		}
	default:
		checkOutcome(t, label, err, want)
	}
}

func checkOutcome(t *testing.T, value string, err error, want string) {
	t.Helper()
	switch want {
	case "ok":
		if err != nil {
			t.Errorf("%q: unexpected error %v", value, err)
		}
	case "form":
		if !stderrors.Is(err, fluxduration.ErrForm) {
			t.Errorf("%q: error = %v, want ErrForm", value, err)
		}
	case "parse":
		_, parseErr := time.ParseDuration(value)
		if parseErr == nil {
			t.Fatalf("%q: test premise wrong, time.ParseDuration accepts it", value)
		}
		if err == nil || err.Error() != parseErr.Error() {
			t.Errorf("%q: error = %v, want time.ParseDuration's %v", value, err, parseErr)
		}
	default:
		t.Fatalf("unknown outcome %q", want)
	}
}
