package oam

import "testing"

func TestClassifyComponent_Annotation(t *testing.T) {
	c := &Component{
		Name:        "cache",
		Type:        "unknown",
		Annotations: map[string]string{TierAnnotation: "infra"},
	}
	tier, err := ClassifyComponent(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierInfra {
		t.Errorf("got %q, want %q", tier, TierInfra)
	}
}

func TestClassifyComponent_InvalidAnnotation(t *testing.T) {
	c := &Component{
		Name:        "cache",
		Type:        "unknown",
		Annotations: map[string]string{TierAnnotation: "invalid"},
	}
	_, err := ClassifyComponent(c)
	if err == nil {
		t.Error("expected error for invalid tier annotation, got nil")
	}
}

func TestClassifyComponent_DefaultMap(t *testing.T) {
	cases := []struct {
		typ  string
		want Tier
	}{
		{"webservice", TierApps},
		{"worker", TierApps},
		{"cronjob", TierApps},
		{"helmrelease", TierApps},
		{"statefulset", TierApps},
		{"postgresql", TierServices},
		{"daemonset", TierInfra},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			c := &Component{Name: tc.typ, Type: tc.typ}
			tier, err := ClassifyComponent(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tier != tc.want {
				t.Errorf("got %q, want %q", tier, tc.want)
			}
		})
	}
}

func TestClassifyComponent_FallbackToApps(t *testing.T) {
	c := &Component{Name: "custom", Type: "unknown-type"}
	tier, err := ClassifyComponent(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierApps {
		t.Errorf("got %q, want %q", tier, TierApps)
	}
}
