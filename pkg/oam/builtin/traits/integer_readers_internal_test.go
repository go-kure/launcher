package traits

import (
	"math"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// TestCoerceInt32_RefusesTruncationAndWrap is go-kure/launcher#525. coerceInt32 fed
// its callers' range checks a converted value: 8080.5 became 8080 and 2^32+80 became
// 80, both of which a port check then accepted. Every integer kind is read, and a
// value that does not fit is an error rather than a conversion.
func TestCoerceInt32_RefusesTruncationAndWrap(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int32
	}{
		{int8(80), 80}, {uint16(8080), 8080}, {uint64(8080), 8080},
		{int32(8080), 8080}, {int64(8080), 8080}, {float64(8080), 8080},
		{int64(math.MinInt32), math.MinInt32},
	} {
		if got, err := coerceInt32(tc.v); err != nil || got != tc.want {
			t.Errorf("coerceInt32(%T(%v)) = %d, %v; want %d", tc.v, tc.v, got, err, tc.want)
		}
	}
	for _, v := range []any{float64(8080.5), int64(math.MaxUint32) + 81, uint64(math.MaxUint64), int64(math.MinInt32) - 1, "8080"} {
		if got, err := coerceInt32(v); err == nil {
			t.Errorf("coerceInt32(%T(%v)) = %d, want an error", v, v, got)
		}
	}
}

// TestCoerceInt_RefusesTruncation is coerceInt32's untyped twin: every integer kind
// is read, and a fraction is an error rather than a truncated int.
func TestCoerceInt_RefusesTruncation(t *testing.T) {
	for _, v := range []any{int8(8), uint16(301), uint64(301), int32(301), int64(301), float64(301)} {
		want := int(8)
		if _, small := v.(int8); !small {
			want = 301
		}
		if got, err := coerceInt(v); err != nil || got != want {
			t.Errorf("coerceInt(%T(%v)) = %d, %v; want %d", v, v, got, err, want)
		}
	}
	for _, v := range []any{float64(301.5), uint64(math.MaxUint64), "301"} {
		if got, err := coerceInt(v); err == nil {
			t.Errorf("coerceInt(%T(%v)) = %d, want an error", v, v, got)
		}
	}
}

// TestRequestRedirect_FractionalStatusCodeRefused: a statusCode of 301.5 reaches
// the handler from a direct call and used to be truncated to an allowed 301.
func TestRequestRedirect_FractionalStatusCodeRefused(t *testing.T) {
	rr, err := parseRequestRedirect(map[string]any{"requestRedirect": map[string]any{
		"scheme": "https", "statusCode": float64(301.5),
	}}, "rules[0].filters[0]")
	if err == nil {
		t.Fatalf("statusCode 301.5 accepted as %v", *rr.StatusCode)
	}
	if !strings.Contains(err.Error(), "statusCode") {
		t.Errorf("error does not name statusCode: %v", err)
	}
}

// TestRoutePorts_EveryIntegerKindAndNoSilentFallback covers the two path-level port
// readers go-kure/launcher#525's sweep reached: an ingress rules[].paths[].port and an httproute
// rules[].backendRefs[].port. Each reads every integer kind; an invalid value is an
// error naming the field, where the ingress reader used to fall back to the
// component's port and the httproute reader used to convert 70000 or 8080.5 as-is.
func TestRoutePorts_EveryIntegerKindAndNoSilentFallback(t *testing.T) {
	ingress := func(port any) (int32, error) {
		cfg, err := (&IngressHandler{}).parseProperties(map[string]any{
			"rules": []any{map[string]any{
				"host":  "app.example.com",
				"paths": []any{map[string]any{"path": "/", "backend": "other-svc", "port": port}},
			}},
		}, stack.NewApplication("wrk", "default", nil))
		if err != nil {
			return 0, err
		}
		return cfg.Rules[0].Paths[0].Port, nil
	}
	route := func(port any) (int32, error) {
		cfg, err := (&HTTPRouteHandler{}).parseProperties(map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"backendRefs": []any{map[string]any{"name": "other-svc", "port": port}},
			}},
		}, stack.NewApplication("wrk", "default", nil))
		if err != nil {
			return 0, err
		}
		return cfg.Rules[0].BackendRefs[0].Port, nil
	}
	for name, read := range map[string]func(any) (int32, error){"ingress": ingress, "httproute": route} {
		for _, v := range []any{int(8080), int32(8080), int64(8080), uint16(8080), float64(8080)} {
			got, err := read(v)
			if err != nil || got != 8080 {
				t.Errorf("%s port %T(%v) = %d, %v; want 8080", name, v, v, got, err)
			}
		}
		for _, v := range []any{int32(70000), int64(0), float64(8080.5), "http"} {
			got, err := read(v)
			if err == nil {
				t.Errorf("%s port %T(%v) = %d, want an error", name, v, v, got)
				continue
			}
			if !strings.Contains(err.Error(), ".port") {
				t.Errorf("%s port %T(%v): error does not name the field: %v", name, v, v, err)
			}
		}
	}
}
