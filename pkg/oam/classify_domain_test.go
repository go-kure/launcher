package oam

import "testing"

func TestDefaultDomain(t *testing.T) {
	if DefaultDomain != "gokure.dev" {
		t.Errorf("DefaultDomain = %q, want gokure.dev", DefaultDomain)
	}
}

func TestTierAnnotationKey(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"", "gokure.dev/tier"},
		{"example.com", "example.com/tier"},
		{"launcher.gokure.dev", "launcher.gokure.dev/tier"},
	}
	for _, tc := range cases {
		if got := TierAnnotationKey(tc.domain); got != tc.want {
			t.Errorf("TierAnnotationKey(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestComponentLabelKeyForDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"", "gokure.dev/component"},
		{"example.com", "example.com/component"},
		{"launcher.gokure.dev", "launcher.gokure.dev/component"},
	}
	for _, tc := range cases {
		if got := ComponentLabelKeyForDomain(tc.domain); got != tc.want {
			t.Errorf("ComponentLabelKeyForDomain(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestDeprecatedAliasesUseDefaultDomain(t *testing.T) {
	if TierAnnotation != "gokure.dev/tier" {
		t.Errorf("TierAnnotation = %q, want gokure.dev/tier", TierAnnotation)
	}
	if ComponentLabel != "gokure.dev/component" {
		t.Errorf("ComponentLabel = %q, want gokure.dev/component", ComponentLabel)
	}
}

// The deprecated aliases + ClassifyComponent must stay exported for source compatibility.
func TestDeprecatedSymbols_Compile(t *testing.T) {
	_ = TierAnnotation
	_ = ComponentLabel
	_ = ClassifyComponent
}

func TestClassifyComponentWithDomain_NilComponent(t *testing.T) {
	if _, err := ClassifyComponentWithDomain(nil, "example.com"); err == nil {
		t.Error("expected error for nil component, got nil")
	}
}

func TestClassifyComponentWithDomain_CustomDomain(t *testing.T) {
	c := &Component{
		Name:        "cache",
		Type:        "unknown",
		Annotations: map[string]string{"example.com/tier": "infra"},
	}
	tier, err := ClassifyComponentWithDomain(c, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierInfra {
		t.Errorf("got %q, want %q", tier, TierInfra)
	}
	// The default-domain annotation key must NOT be read under a custom domain.
	tierDefault, err := ClassifyComponentWithDomain(c, DefaultDomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tierDefault != TierApps { // unknown type falls back to apps; example.com/tier ignored
		t.Errorf("default-domain classify read the custom-domain annotation: got %q, want apps", tierDefault)
	}
}

func TestClassifyComponentWithDomain_EmptyDomainUsesDefault(t *testing.T) {
	c := &Component{
		Name:        "cache",
		Type:        "unknown",
		Annotations: map[string]string{"gokure.dev/tier": "services"},
	}
	tier, err := ClassifyComponentWithDomain(c, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierServices {
		t.Errorf("got %q, want %q", tier, TierServices)
	}
}

func TestClassifyComponentWithDomain_InvalidDomain(t *testing.T) {
	c := &Component{Name: "cache", Type: "webservice"}
	for _, bad := range []string{"https://example.com", "example.com/", "Example.com", "a..b"} {
		if _, err := ClassifyComponentWithDomain(c, bad); err == nil {
			t.Errorf("expected error for invalid domain %q, got nil", bad)
		}
	}
}
