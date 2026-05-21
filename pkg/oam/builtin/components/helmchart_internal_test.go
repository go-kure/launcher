package components

import (
	"testing"
)

func TestHelmchartConfig_GenerateTemplate_HTTP(t *testing.T) {
	twoConfigMaps := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: first
data:
  key: value
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
data:
  key: value`)

	cfg := &HelmchartConfig{
		Name:       "myapp",
		Namespace:  "default",
		Delivery:   "template",
		Chart:      "myapp",
		SourceURL:  "https://charts.example.com",
		SourceKind: "HelmRepository",
		renderChart: func(chartURL, version string, values map[string]any) ([]byte, error) {
			if chartURL != "https://charts.example.com/myapp" {
				t.Errorf("chartURL = %q, want https://charts.example.com/myapp", chartURL)
			}
			return twoConfigMaps, nil
		},
	}

	objects, err := cfg.generateTemplate()
	if err != nil {
		t.Fatalf("generateTemplate: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
}

func TestHelmchartConfig_GenerateTemplate_OCI(t *testing.T) {
	oneConfigMap := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
data:
  key: value`)

	cfg := &HelmchartConfig{
		Name:       "myapp",
		Namespace:  "default",
		Delivery:   "template",
		SourceURL:  "oci://ghcr.io/example/charts/myapp",
		SourceKind: "OCIRepository",
		Version:    "1.2.3",
		renderChart: func(chartURL, version string, values map[string]any) ([]byte, error) {
			// OCI: chartURL must equal SourceURL as-is (no chart name appended)
			if chartURL != "oci://ghcr.io/example/charts/myapp" {
				t.Errorf("chartURL = %q, want oci://ghcr.io/example/charts/myapp", chartURL)
			}
			if version != "1.2.3" {
				t.Errorf("version = %q, want 1.2.3", version)
			}
			return oneConfigMap, nil
		},
	}

	objects, err := cfg.generateTemplate()
	if err != nil {
		t.Fatalf("generateTemplate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestDecodeKubeManifests_ErrorOnMalformedYAML(t *testing.T) {
	_, err := decodeKubeManifests([]byte("key: [unclosed"))
	if err == nil {
		t.Fatal("expected error on malformed YAML")
	}
}

func TestDecodeKubeManifests_ErrorOnMappingWithoutAPIVersion(t *testing.T) {
	_, err := decodeKubeManifests([]byte("kind: ConfigMap\nmetadata:\n  name: cm"))
	if err == nil {
		t.Fatal("expected error for map without apiVersion")
	}
}

func TestDecodeKubeManifests_SkipsNonMapDoc(t *testing.T) {
	yaml := []byte("just a string\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm")
	objects, err := decodeKubeManifests(yaml)
	if err != nil {
		t.Fatalf("decodeKubeManifests: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object (scalar doc skipped), got %d", len(objects))
	}
}
