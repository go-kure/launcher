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

// TestClassifyComponent_NoTierFromType pins that no component type places a
// component in a tier (go-kure/launcher#783): every type the removed table
// listed, a rule-generated source and an unknown type are in no tier until
// the document declares one.
func TestClassifyComponent_NoTierFromType(t *testing.T) {
	types := []string{
		"webservice", "worker", "cronjob", "helm", "helmrelease", "helmtemplate",
		"statefulset", "daemonset", "postgresql", "cnpg-cluster", "cnpg-pooler",
		"cnpg-database", "cnpg-objectstore", "crd", "manifests", "oci",
		"helmrepository", "ocirepository", "gitrepository", "bucket", "helmchart",
		"unknown-type",
	}
	for _, typ := range types {
		for _, synthesized := range []bool{false, true} {
			c := &Component{Name: typ, Type: typ, synthesized: synthesized}
			tier, err := ClassifyComponent(c)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", typ, err)
			}
			if tier != "" {
				t.Errorf("%s (synthesized %v): tier = %q, want none", typ, synthesized, tier)
			}
		}
	}
}
