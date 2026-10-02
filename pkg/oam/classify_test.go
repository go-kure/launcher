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
		{"helmtemplate", TierApps},
		{"statefulset", TierApps},
		{"postgresql", TierServices},
		{"cnpg-cluster", TierServices},
		{"daemonset", TierInfra},
		{"crd", TierApps},
		{"manifests", TierApps},
		{"helmrepository", TierApps},
		{"ocirepository", TierApps},
		{"gitrepository", TierApps},
		{"bucket", TierApps},
		{"helmchart", TierApps},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			// ClassifyComponent falls back to TierApps for an unmapped type, so
			// its result alone cannot show that a TierApps row is registered.
			if got, ok := defaultTierMap[tc.typ]; !ok || got != tc.want {
				t.Errorf("defaultTierMap[%q] = %q, %v; want %q, true", tc.typ, got, ok, tc.want)
			}
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
