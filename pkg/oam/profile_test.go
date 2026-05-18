package oam

import (
	"strings"
	"testing"
)

func TestParseClusterProfile_Valid(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`)
	got, err := ParseClusterProfile(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Metadata.Name != "test-cluster" {
		t.Errorf("expected name 'test-cluster', got %q", got.Metadata.Name)
	}
	cap, ok := got.Spec.Capabilities["expose"]
	if !ok {
		t.Fatal("expected 'expose' capability")
	}
	if cap.Rendering["controllerType"] != "ingress" {
		t.Errorf("expected controllerType 'ingress', got %v", cap.Rendering["controllerType"])
	}
}

func TestParseClusterProfile_UnknownTopLevelField(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  gitops:
    url: https://example.com
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for unknown field spec.gitops")
	}
	if !strings.Contains(err.Error(), "gitops") {
		t.Errorf("expected error to mention 'gitops', got: %v", err)
	}
}

func TestParseClusterProfile_UnknownMetadataField(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
  labels:
    app: test
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for unknown metadata field 'labels'")
	}
}

func TestParseClusterProfile_EmptyCapabilities(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: minimal
`)
	got, err := ParseClusterProfile(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Spec.Capabilities) != 0 {
		t.Errorf("expected no capabilities, got %d", len(got.Spec.Capabilities))
	}
}

func TestParseClusterProfile_InvalidYAML(t *testing.T) {
	_, err := ParseClusterProfile([]byte("not: valid: yaml: ["))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseClusterProfile_WrongAPIVersion(t *testing.T) {
	data := []byte(`
apiVersion: core.oam.dev/v1beta1
kind: ClusterProfile
metadata:
  name: test-cluster
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for wrong apiVersion")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("expected error to mention 'apiVersion', got: %v", err)
	}
}

func TestParseClusterProfile_WrongKind(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: test-cluster
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for wrong kind")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("expected error to mention 'kind', got: %v", err)
	}
}

func TestParseClusterProfile_MissingName(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: ""
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for missing metadata.name")
	}
	if !strings.Contains(err.Error(), "metadata.name") {
		t.Errorf("expected error to mention 'metadata.name', got: %v", err)
	}
}

func TestParseClusterProfile_InvalidName(t *testing.T) {
	data := []byte(`
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: "Not A Valid DNS Name!"
`)
	_, err := ParseClusterProfile(data)
	if err == nil {
		t.Fatal("expected error for invalid DNS name")
	}
}
