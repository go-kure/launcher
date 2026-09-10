package traits

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// parseNPPort is the third depth of the same defect class the peer and rule
// tests pin: a constraint the document authored, silently replaced by a WIDER or
// simply different one rather than reported.
//
// Two shapes did that here. `protocol` was read through a bare comma-ok string
// assertion, so any non-string — `protocol: [UDP]`, or a lowering rule that
// assembled a corev1.Protocol instead of a string — was discarded and the port
// rendered TCP: a policy permitting a protocol nobody authored while denying the
// one that was. And a numeric `port` went through a bare `int32(v)`, which
// truncates a fractional value and is implementation-defined outside int32
// range, so `port: 80.9` and `port: 4294967376` both rendered port 80.
//
// Each test below carries its control, for the same reason the peer file does:
// a parser that rejected everything would pass the rejection rows alone.

func TestParseNPPort_NonStringProtocolIsRejected(t *testing.T) {
	for name, value := range map[string]any{
		"list":    []any{"UDP"},
		"object":  map[string]any{"name": "UDP"},
		"number":  6,
		"boolean": true,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseNPPort(map[string]any{"port": 53, "protocol": value}, "ingress[0].ports[0]")
			if err == nil {
				t.Fatal("a non-string protocol must be rejected, not dropped to the TCP default")
			}
			if !strings.Contains(err.Error(), "protocol") {
				t.Errorf("diagnostic %q does not name the offending key", err)
			}
		})
	}
}

func TestParseNPPort_AbsentOrNullProtocolDefaultsToTCP(t *testing.T) {
	// The control for the test above on the null axis: rejecting a wrong-typed
	// protocol must not turn a null one into an error. Absence is the upstream
	// default (k8s.io/api networking/v1/types.go:159-162), and a null is absence
	// everywhere else in this parser, so both shapes render TCP.
	cases := map[string]map[string]any{
		"absent":      {"port": 53},
		"untyped nil": {"port": 53, "protocol": nil},
		"typed nil":   {"port": 53, "protocol": map[string]any(nil)},
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			port, err := parseNPPort(props, "ingress[0].ports[0]")
			if err != nil {
				t.Fatalf("a null or absent protocol must default to TCP, got error: %v", err)
			}
			if port.Protocol != corev1.ProtocolTCP {
				t.Errorf("protocol = %q, want TCP", port.Protocol)
			}
		})
	}
}

func TestParseNPPort_ValidProtocolStillParses(t *testing.T) {
	// The other control: the case-insensitive accept path is untouched.
	for input, want := range map[string]corev1.Protocol{
		"UDP":  corev1.ProtocolUDP,
		"udp":  corev1.ProtocolUDP,
		"SCTP": corev1.ProtocolSCTP,
		"tcp":  corev1.ProtocolTCP,
	} {
		t.Run(input, func(t *testing.T) {
			port, err := parseNPPort(map[string]any{"port": 53, "protocol": input}, "ingress[0].ports[0]")
			if err != nil {
				t.Fatalf("protocol %q must parse, got: %v", input, err)
			}
			if port.Protocol != want {
				t.Errorf("protocol = %q, want %q", port.Protocol, want)
			}
		})
	}
}

func TestParseNPPort_FractionalPortIsRejected(t *testing.T) {
	// `int32(80.9)` is 80 in Go, so a fractional port used to render a port the
	// document did not author — with no diagnostic anywhere.
	for name, value := range map[string]any{
		"fractional float64": 80.9,
		"fractional float32": float32(80.5),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseNPPort(map[string]any{"port": value}, "ingress[0].ports[0]")
			if err == nil {
				t.Fatal("a fractional port must be rejected, not truncated to a different port")
			}
			if !strings.Contains(err.Error(), "whole number") {
				t.Errorf("diagnostic %q does not say why the value was refused", err)
			}
		})
	}
}

func TestParseNPPort_OutOfRangePortIsRejected(t *testing.T) {
	// 4294967376 is the case that motivated the bound: converting it to int32 is
	// implementation-defined, and on this host it landed on 80 — a port the
	// author never wrote, silently opened. 0 and 65536 are the boundary rows;
	// the API server refuses both (IsValidPortNum), so failing here reports at
	// the line that wrote the value instead of at apply time.
	for name, value := range map[string]any{
		"beyond int32":  4294967376.0,
		"beyond uint64": uint64(18446744073709551615),
		"zero":          0,
		"negative":      -1,
		"65536":         65536,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseNPPort(map[string]any{"port": value}, "ingress[0].ports[0]")
			if err == nil {
				t.Fatal("a port outside 1-65535 must be rejected, not converted")
			}
			if !strings.Contains(err.Error(), "out of range") {
				t.Errorf("diagnostic %q does not say why the value was refused", err)
			}
		})
	}
}

func TestParseNPPort_ValidPortShapesStillParse(t *testing.T) {
	// The control for both numeric tests, and the reason they cannot be "reject
	// anything unusual": every integer kind a YAML/JSON decode or a lowering rule
	// assembling properties in Go can produce is still accepted, the boundary
	// values included, and a named port is still a string.
	for name, value := range map[string]any{
		"int":           8080,
		"int64":         int64(8080),
		"int32":         int32(8080),
		"uint16":        uint16(8080),
		"float64 whole": 8080.0,
	} {
		t.Run(name, func(t *testing.T) {
			port, err := parseNPPort(map[string]any{"port": value}, "ingress[0].ports[0]")
			if err != nil {
				t.Fatalf("port %v (%T) must parse, got: %v", value, value, err)
			}
			if port.Port.IntValue() != 8080 {
				t.Errorf("port = %v, want 8080", port.Port)
			}
		})
	}
	for name, value := range map[string]any{"low boundary": 1, "high boundary": 65535} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNPPort(map[string]any{"port": value}, "ingress[0].ports[0]"); err != nil {
				t.Fatalf("port %v is inside 1-65535 and must parse, got: %v", value, err)
			}
		})
	}
	t.Run("named port", func(t *testing.T) {
		port, err := parseNPPort(map[string]any{"port": "http"}, "ingress[0].ports[0]")
		if err != nil {
			t.Fatalf("a named port must still parse, got: %v", err)
		}
		if port.Port.StrVal != "http" {
			t.Errorf("port = %v, want the named port http", port.Port)
		}
	})
}

func TestParseNPPort_UnknownKeyIsRejected(t *testing.T) {
	// The port item is the one object in this trait whose key set the SCHEMA
	// cannot close — it is declared AdditionalProperties because `port` is an
	// int-or-string union — so `protcol` reached the parser and was dropped to
	// TCP. `endPort` is a real NetworkPolicyPort field this parser does not
	// implement: accepting it would render a single port where a range was
	// authored.
	for _, key := range []string{"protcol", "endPort", "Port"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseNPPort(map[string]any{"port": 53, key: "x"}, "ingress[0].ports[0]")
			if err == nil {
				t.Fatalf("key %q must be rejected, not silently ignored", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("diagnostic %q does not name the offending key", err)
			}
		})
	}
}

func TestParseNPPort_UnknownKeyDiagnosticIsDeterministic(t *testing.T) {
	// The port half of the peer file's ordering test. Go randomizes map
	// iteration, so an unsorted loop over four bad keys reports a different one
	// per run; twenty passes over the same input pin that the FIRST offender
	// lexicographically is what comes out, not merely that something does.
	props := map[string]any{
		"port":     53,
		"aardvark": "x",
		"protcol":  "x",
		"endPort":  32,
		"zebra":    "x",
	}
	for i := range 20 {
		_, err := parseNPPort(props, "ingress[0].ports[0]")
		if err == nil {
			t.Fatalf("pass %d: four unsupported keys must be rejected", i)
		}
		if !strings.Contains(err.Error(), `"aardvark"`) {
			t.Fatalf("pass %d: diagnostic %q does not name the lexicographically first offender", i, err)
		}
	}
}
