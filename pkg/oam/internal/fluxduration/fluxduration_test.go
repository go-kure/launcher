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
// outside the pattern, and any value of an hour or more, however authored, is
// emitted with an h.
func TestSourceTimeout_ValidateEmitted(t *testing.T) {
	cases := []struct {
		value string
		// want is "ok", "parse", "form", "resolution" or "hour" (*HourError).
		want    string
		emitted string
	}{
		{"30s", "ok", ""},
		{"5m", "ok", ""},
		{"59m59.999s", "ok", ""},
		{"500ms", "ok", ""},
		{"0s", "ok", ""},

		{"60m", "hour", "1h0m0s"},
		{"3600s", "hour", "1h0m0s"},
		{"90m", "hour", "1h30m0s"},

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
		{fluxduration.SourceTimeout, "timeout 1h", time.Hour, "hour"},
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

// checkEmittedOutcome is checkOutcome plus the two typed emission errors, whose
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
	case "hour":
		var he *fluxduration.HourError
		if !stderrors.As(err, &he) {
			t.Fatalf("%q: error = %v, want a *HourError", label, err)
		}
		if he.Value != value || he.Emitted != emitted {
			t.Errorf("HourError = %+v, want Value %q, Emitted %q", he, value, emitted)
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
