package oam_test

import (
	"math"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

type namedPort int32

// TestIntegerValue pins the conversion every integer property reader shares
// (go-kure/launcher#525): every Go integer kind and an integral float are read; a
// fraction, a non-finite float, a non-number and anything int64 cannot hold are
// refused rather than wrapped.
func TestIntegerValue(t *testing.T) {
	accept := []struct {
		v    any
		want int64
	}{
		{int(7), 7}, {int8(-5), -5}, {int16(300), 300}, {int32(8080), 8080}, {int64(-1), -1},
		{uint(7), 7}, {uint8(255), 255}, {uint16(8080), 8080}, {uint32(math.MaxUint32), math.MaxUint32},
		{uint64(math.MaxInt64), math.MaxInt64},
		{float64(8080), 8080}, {float32(3), 3}, {float64(math.MinInt64), math.MinInt64},
		{namedPort(443), 443}, {uintptr(80), 80},
	}
	for _, tc := range accept {
		got, ok := oam.IntegerValue(tc.v)
		if !ok || got != tc.want {
			t.Errorf("IntegerValue(%T(%v)) = %d, %v; want %d, true", tc.v, tc.v, got, ok, tc.want)
		}
	}

	refuse := []any{
		uint64(math.MaxInt64) + 1, uint64(math.MaxUint64),
		float64(1 << 63), // 2^63: one past math.MaxInt64
		// The float below -2^63, which a missing lower-bound check reads as MinInt64.
		math.Nextafter(math.MinInt64, math.Inf(-1)),
		1.5, float32(2.5), math.NaN(), math.Inf(1), math.Inf(-1),
		"8080", true, nil, []any{1},
	}
	for _, v := range refuse {
		if got, ok := oam.IntegerValue(v); ok {
			t.Errorf("IntegerValue(%T(%v)) = %d, true; want refused", v, v, got)
		}
	}
}

// TestIntegerInRange: beyond int64 is out of range, not a type error.
func TestIntegerInRange(t *testing.T) {
	for v, want := range map[any]string{
		1e20: "between 0 and 10, got 100000000000000000000", uint64(math.MaxUint64): "between 0 and 10, got 18446744073709551615",
		11: "between 0 and 10, got 11", -1: "between 0 and 10, got -1", 2.5: "got float64 2.5 (not a whole number)",
		math.Inf(1): "got float64 +Inf (not finite)", "8": "got string",
	} {
		if _, err := oam.IntegerInRange(v, 0, 10); err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("IntegerInRange(%T(%v), 0, 10) error = %v, want suffix %q", v, v, err, want)
		}
	}
}
