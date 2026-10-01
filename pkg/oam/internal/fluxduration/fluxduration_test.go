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
