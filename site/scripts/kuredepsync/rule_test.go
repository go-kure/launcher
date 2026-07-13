package main

import (
	"os"
	"strings"
	"testing"
)

// reasons returns module→reason for the violations, for compact assertions.
func reasons(vs []violation) map[string]string {
	m := map[string]string{}
	for _, v := range vs {
		m[v.module] = v.reason
	}
	return m
}

func TestEvaluate(t *testing.T) {
	const D = "example.com/dep"

	tests := []struct {
		name                       string
		lHead, kHead, lBase, kBase map[string]string
		wantReason                 string // "" means no violation
	}{
		{
			name:  "in-sync: launcher equals kure",
			lHead: m(D, "v1.2.0"), kHead: m(D, "v1.2.0"),
			lBase: m(D, "v1.2.0"), kBase: m(D, "v1.2.0"),
			wantReason: "",
		},
		{
			name:  "grandfathered: pre-existing lead, PR touches nothing",
			lHead: m(D, "v1.6.0"), kHead: m(D, "v1.5.1"),
			lBase: m(D, "v1.6.0"), kBase: m(D, "v1.5.1"),
			wantReason: "",
		},
		{
			name:  "launcher-raised: PR bumps shared dep above kure",
			lHead: m(D, "v1.21.0"), kHead: m(D, "v1.20.3"),
			lBase: m(D, "v1.20.3"), kBase: m(D, "v1.20.3"),
			wantReason: "launcher-raised",
		},
		{
			name:  "kure-regressed: kure lowered for D on a grandfathered lead (widened)",
			lHead: m(D, "v1.6.0"), kHead: m(D, "v1.4.0"),
			lBase: m(D, "v1.6.0"), kBase: m(D, "v1.5.1"),
			wantReason: "kure-regressed",
		},
		{
			name:  "newly-shared: PR adds D as direct dep already ahead (absent at base)",
			lHead: m(D, "v2.0.0"), kHead: m(D, "v1.0.0"),
			lBase: map[string]string{}, kBase: map[string]string{},
			wantReason: "newly-shared/ahead",
		},
		{
			name:  "newly-ahead via kure regression: was in sync at base, kure lowered D",
			lHead: m(D, "v1.5.0"), kHead: m(D, "v1.4.0"),
			lBase: m(D, "v1.5.0"), kBase: m(D, "v1.5.0"),
			wantReason: "kure-regressed", // kure going down is the informative cause
		},
		{
			name:  "catch-up: kure regressed for D but launcher no longer leads → harmless",
			lHead: m(D, "v1.4.0"), kHead: m(D, "v1.5.0"),
			lBase: m(D, "v1.6.0"), kBase: m(D, "v1.5.1"),
			wantReason: "", // !headAhead gates it out despite kure_head < kure_base
		},
		{
			name:  "prerelease precedence: v1.0.0 leads v1.0.0-rc.1 (sort -V would miss)",
			lHead: m(D, "v1.0.0"), kHead: m(D, "v1.0.0-rc.1"),
			lBase: map[string]string{}, kBase: map[string]string{},
			wantReason: "newly-shared/ahead",
		},
		{
			name:  "not shared: D direct in launcher only → ignored",
			lHead: m(D, "v9.9.9"), kHead: map[string]string{},
			lBase: m(D, "v9.9.9"), kBase: map[string]string{},
			wantReason: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reasons(evaluate(tc.lHead, tc.kHead, tc.lBase, tc.kBase))
			if tc.wantReason == "" {
				if len(got) != 0 {
					t.Fatalf("expected no violation, got %v", got)
				}
				return
			}
			if got[D] != tc.wantReason {
				t.Fatalf("D reason = %q, want %q (all: %v)", got[D], tc.wantReason, got)
			}
		})
	}
}

func TestKureModuleExcluded(t *testing.T) {
	// kure itself is never treated as a shared dep even if it appears ahead.
	vs := evaluate(
		map[string]string{kureModule: "v0.3.0"},
		map[string]string{kureModule: "v0.2.0"},
		map[string]string{kureModule: "v0.2.0"},
		map[string]string{kureModule: "v0.2.0"},
	)
	if len(vs) != 0 {
		t.Fatalf("kure module should be excluded, got %v", vs)
	}
}

func TestMessageIncludesReasonAndFix(t *testing.T) {
	v := violation{module: "example.com/dep", reason: "launcher-raised", launcherHead: "v1.21.0", kureHead: "v1.20.3", kureTag: "v0.2.0-beta.6"}
	msg := v.message()
	for _, want := range []string{"launcher-raised", "v1.21.0", "v1.20.3", "v0.2.0-beta.6", "check-kure-dep-sync.sh"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
	// Empty tag must still produce a sensible message (no dangling "kure ").
	bare := violation{module: "d", reason: "launcher-raised", launcherHead: "v2", kureHead: "v1"}.message()
	if strings.Contains(bare, "imported kure  pins") {
		t.Errorf("empty tag left a double space: %s", bare)
	}
}

func TestKureVersion(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/go.mod"
	const src = `module example.com/x

go 1.26.5

require (
	github.com/go-kure/kure v0.2.0-beta.6
	example.com/other v1.2.3 // indirect
)
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := kureVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.2.0-beta.6" {
		t.Fatalf("kureVersion = %q, want v0.2.0-beta.6", got)
	}
}

func m(k, v string) map[string]string { return map[string]string{k: v} }
