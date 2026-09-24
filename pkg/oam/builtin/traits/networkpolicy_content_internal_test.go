package traits

import (
	"math"
	"strings"
	"testing"
)

// go-kure/launcher#469: the networkpolicy parser checked the TYPE of every label
// key/value and CIDR string it accepted, never its CONTENT. A well-typed value the
// API server rejects rendered here and failed only at apply time. These tests pin
// the parse-time rejection against Kubernetes' own validators for the pinned minor,
// plus the controls that nothing upstream accepts is newly refused.

// contentErr asserts err is non-nil, starts with want, and carries a reason after
// it — the reason text is apimachinery's, so only its presence is pinned.
func contentErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error starting %q, got nil", want)
	}
	got := err.Error()
	if !strings.HasPrefix(got, want) || len(got) <= len(want) {
		t.Errorf("diagnostic = %q, want prefix %q followed by a reason", got, want)
	}
}

func TestParseNPPeer_InvalidLabelContentIsRejected(t *testing.T) {
	long64 := strings.Repeat("a", 64)
	for _, key := range []string{"podSelector", "namespaceSelector"} {
		for _, tc := range []struct {
			name   string
			labels map[string]any
			want   string
		}{
			{"float renders with exponent", map[string]any{"size": 1000000.0},
				`from[0].` + key + `.matchLabels: "size" has invalid label value "1e+06" (rendered from float64): `},
			{"negative number", map[string]any{"n": -1},
				`from[0].` + key + `.matchLabels: "n" has invalid label value "-1" (rendered from int): `},
			{"infinity", map[string]any{"n": math.Inf(1)},
				`from[0].` + key + `.matchLabels: "n" has invalid label value "+Inf" (rendered from float64): `},
			{"64-character string", map[string]any{"name": long64},
				`from[0].` + key + `.matchLabels: "name" has invalid label value "` + long64 + `": `},
			{"string with a space", map[string]any{"env": "pro d"},
				`from[0].` + key + `.matchLabels: "env" has invalid label value "pro d": `},
			{"key with a space", map[string]any{"bad key": "x"},
				`from[0].` + key + `.matchLabels: invalid label key "bad key": `},
			{"key with an invalid prefix", map[string]any{"Foo_/name": "x"},
				`from[0].` + key + `.matchLabels: invalid label key "Foo_/name": `},
			{"empty key", map[string]any{"": "x"},
				`from[0].` + key + `.matchLabels: invalid label key "": `},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				_, err := parseNPPeer(map[string]any{
					key: map[string]any{"matchLabels": tc.labels},
				}, "from[0]")
				contentErr(t, err, tc.want)
			})
		}
	}
}

func TestParseNPPeer_ValidLabelContentStillParses(t *testing.T) {
	// Controls: every shape upstream accepts must still parse, rendered unchanged.
	long63 := strings.Repeat("a", 63)
	labels := map[string]any{
		"app.kubernetes.io/name": "web",
		"empty":                  "",
		"port":                   8080,
		"enabled":                true,
		"max":                    long63,
		"ratio":                  1.5,
	}
	want := map[string]string{
		"app.kubernetes.io/name": "web",
		"empty":                  "",
		"port":                   "8080",
		"enabled":                "true",
		"max":                    long63,
		"ratio":                  "1.5",
	}
	peer, err := parseNPPeer(map[string]any{
		"podSelector": map[string]any{"matchLabels": labels},
	}, "from[0]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for k, v := range want {
		if got := peer.PodSelector.MatchLabels[k]; got != v {
			t.Errorf("matchLabels[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestParseNPPeer_InvalidCIDRContentIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ipBlock map[string]any
		want    string
	}{
		{"malformed cidr", map[string]any{"cidr": "not-cidr"},
			`from[0].ipBlock.cidr: invalid CIDR "not-cidr": `},
		{"bare address", map[string]any{"cidr": "10.0.0.1"},
			`from[0].ipBlock.cidr: invalid CIDR "10.0.0.1": `},
		{"host bits set", map[string]any{"cidr": "10.0.0.1/8"},
			`from[0].ipBlock.cidr: invalid CIDR "10.0.0.1/8": `},
		{"leading zeros", map[string]any{"cidr": "010.0.0.0/8"},
			`from[0].ipBlock.cidr: invalid CIDR "010.0.0.0/8": `},
		{"ipv4-mapped ipv6", map[string]any{"cidr": "::ffff:10.0.0.0/104"},
			`from[0].ipBlock.cidr: invalid CIDR "::ffff:10.0.0.0/104": `},
		{"malformed except", map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16", "also-bad"}},
			`from[0].ipBlock.except[1]: invalid CIDR "also-bad": `},
		{"except outside cidr", map[string]any{"cidr": "10.0.0.0/8", "except": []any{"192.168.0.0/16"}},
			`from[0].ipBlock.except[0]: "192.168.0.0/16" must be a strict subset of cidr "10.0.0.0/8"`},
		{"except equal to cidr", map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.0.0.0/8"}},
			`from[0].ipBlock.except[0]: "10.0.0.0/8" must be a strict subset of cidr "10.0.0.0/8"`},
		{"except wider than cidr", map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.0.0.0/7"}},
			`from[0].ipBlock.except[0]: "10.0.0.0/7" must be a strict subset of cidr "10.0.0.0/8"`},
		{"except in the other family", map[string]any{"cidr": "10.0.0.0/8", "except": []any{"2001:db8::/64"}},
			`from[0].ipBlock.except[0]: "2001:db8::/64" must be a strict subset of cidr "10.0.0.0/8"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer, err := parseNPPeer(map[string]any{"ipBlock": tc.ipBlock}, "from[0]")
			if err == nil {
				t.Fatalf("expected rejection, got %+v", peer.IPBlock)
			}
			if strings.Contains(tc.want, "strict subset") {
				if got := err.Error(); got != tc.want {
					t.Errorf("diagnostic = %q, want %q", got, tc.want)
				}
				return
			}
			contentErr(t, err, tc.want)
		})
	}
}

func TestParseNPPeer_ValidCIDRContentStillParses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cidr   string
		except []any
	}{
		{"ipv4 with except", "10.0.0.0/8", []any{"10.1.0.0/16", "10.2.3.4/32"}},
		{"everything", "0.0.0.0/0", []any{"169.254.169.254/32"}},
		{"ipv6 with except", "2001:db8::/32", []any{"2001:db8:1::/48"}},
		{"non-canonical ipv6 (legacy field form)", "2001:DB8:0:0::/64", nil},
		{"single host", "192.168.1.1/32", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ib := map[string]any{"cidr": tc.cidr}
			if tc.except != nil {
				ib["except"] = tc.except
			}
			peer, err := parseNPPeer(map[string]any{"ipBlock": ib}, "from[0]")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if peer.IPBlock.CIDR != tc.cidr {
				t.Errorf("cidr = %q, want %q (rendered verbatim)", peer.IPBlock.CIDR, tc.cidr)
			}
			if len(peer.IPBlock.Except) != len(tc.except) {
				t.Errorf("except = %v, want %v", peer.IPBlock.Except, tc.except)
			}
		})
	}
}
