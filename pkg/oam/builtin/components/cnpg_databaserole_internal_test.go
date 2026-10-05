package components

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestCnpgDatabaseRole_ValidUntilRoundTrip holds the round trip of a decoded
// validUntil on the case no authored document reaches: the fraction guard
// refuses a fraction before the decode, so only a time built here carries one
// the written form cuts off.
func TestCnpgDatabaseRole_ValidUntilRoundTrip(t *testing.T) {
	cut := metav1.NewTime(time.Date(2030, 1, 1, 0, 0, 0, 500_000_000, time.UTC))
	want := "validUntil: the CloudNativePG API types write it as 2030-01-01T00:00:00Z, which is not the instant authored"
	if err := refuseCnpgValidUntilWritten(&cut); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	whole := metav1.NewTime(time.Date(2030, 1, 1, 2, 0, 0, 0, time.FixedZone("", 2*60*60)))
	if err := refuseCnpgValidUntilWritten(&whole); err != nil {
		t.Errorf("a whole second with an offset: %v", err)
	}
}
