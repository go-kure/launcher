package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// PodTemplateLabels is what a sibling's selector is checked against: it holds
// the authored labels beside the component's own (go-kure/launcher#1178).
func TestDeploymentConfig_PodTemplateLabelsHoldTheAuthoredOnes(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
		PodMetadata: components.PodMetadata{Labels: map[string]string{"example.com/team": "payments"}}}
	got := cfg.PodTemplateLabels()
	if got["example.com/team"] != "payments" || got["app"] != "web" {
		t.Errorf("PodTemplateLabels = %v, want the authored label beside app", got)
	}
}

// A config built directly is held to the same rule as a parsed one: an authored
// key the kind set to another value refuses to generate, and nothing is written
// to the Deployment.
func TestDeploymentConfig_PodMetadataCannotReplaceTheAppLabel(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
		PodMetadata: components.PodMetadata{Labels: map[string]string{"app": "other"}}}
	_, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err == nil || !strings.Contains(err.Error(), `podMetadata.labels["app"]: "other" is not the value the component sets on its pod template ("web")`) {
		t.Fatalf("err = %v\nwant the app label refused", err)
	}
}

// The selector and the Deployment's own labels are maps of their own: the
// authored pod template label reaches neither, and generating twice from one
// config gives the same objects.
func TestDeploymentConfig_PodMetadataKeepsTheMapsApart(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
		PodMetadata: components.PodMetadata{
			Labels:      map[string]string{"example.com/team": "payments"},
			Annotations: map[string]string{"example.com/owner": "x"},
		}}
	for range 2 {
		objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		dep := (*objs[0]).(*appsv1.Deployment)
		if dep.Spec.Template.Labels["example.com/team"] != "payments" || dep.Spec.Template.Annotations["example.com/owner"] != "x" {
			t.Errorf("the pod template carries %v and %v, want the authored ones", dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		}
		if _, has := dep.Spec.Selector.MatchLabels["example.com/team"]; has {
			t.Errorf("the selector is %v, want no authored label in it", dep.Spec.Selector.MatchLabels)
		}
		if _, has := dep.Labels["example.com/team"]; has || len(dep.Annotations) != 0 {
			t.Errorf("the Deployment carries %v and %v, want none of the pod template ones", dep.Labels, dep.Annotations)
		}
	}
}
