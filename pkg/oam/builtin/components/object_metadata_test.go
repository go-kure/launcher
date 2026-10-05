package components_test

import (
	"strings"
	"testing"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// What a kind's config carries as Metadata is what the engine read off the
// component (oam.Component.ObjectMetadata); these tests build configs directly
// to reach what a build through the engine does not.

// A source whose long timeout makes it an unstructured copy carries the
// authored labels and annotations on the copy (go-kure/launcher#790).
func TestFluxSourceConfigs_LongTimeoutKeepsObjectMetadata(t *testing.T) {
	cfg := &components.GitRepositoryConfig{Name: "src", Namespace: "demo",
		Metadata: oam.ObjectMetadata{
			Labels:      map[string]string{"example.com/team": "payments"},
			Annotations: map[string]string{"example.com/owner": "x"},
		},
		Spec: sourcev1.GitRepositorySpec{
			URL:     "https://github.com/org/repo",
			Timeout: &metav1.Duration{Duration: 2 * time.Hour},
		}}
	objs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u, ok := (*objs[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("emitted %T, want the unstructured copy a long timeout makes", *objs[0])
	}
	if got := u.GetLabels()["example.com/team"]; got != "payments" {
		t.Errorf("labels = %v, want the authored one on the copy", u.GetLabels())
	}
	if got := u.GetAnnotations()["example.com/owner"]; got != "x" {
		t.Errorf("annotations = %v, want the authored one on the copy", u.GetAnnotations())
	}
}

// The labels go on the Deployment's own metadata. Its selector and its pod
// template keep the label map the kind built, which the Deployment no longer
// shares with them.
func TestDeploymentConfig_ObjectMetadataStaysOffThePodTemplate(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
		Metadata: oam.ObjectMetadata{
			Labels:      map[string]string{"example.com/team": "payments", "app": "web"},
			Annotations: map[string]string{"example.com/owner": "x"},
		}}
	objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dep, ok := (*objs[0]).(*appsv1.Deployment)
	if !ok {
		t.Fatalf("emitted %T, want *appsv1.Deployment", *objs[0])
	}
	if dep.Labels["example.com/team"] != "payments" || dep.Labels["app"] != "web" || dep.Annotations["example.com/owner"] != "x" {
		t.Errorf("the Deployment carries labels %v and annotations %v, want the authored ones beside app", dep.Labels, dep.Annotations)
	}
	if _, has := dep.Spec.Template.Labels["example.com/team"]; has || len(dep.Spec.Template.Annotations) != 0 {
		t.Errorf("the pod template carries labels %v and annotations %v, want none of the authored ones", dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
	}
	if _, has := dep.Spec.Selector.MatchLabels["example.com/team"]; has {
		t.Errorf("the selector is %v, want no authored label in it", dep.Spec.Selector.MatchLabels)
	}
}

// A config handed an `app` label with another value than the one its kind
// writes refuses to generate: the engine refuses it first on a component, and
// a config built directly is held to the same.
func TestDeploymentConfig_ObjectMetadataCannotReplaceTheAppLabel(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Image: "ghcr.io/example/web:v1.0.0", Replicas: 1,
		Metadata: oam.ObjectMetadata{Labels: map[string]string{"app": "other"}}}
	_, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err == nil || !strings.Contains(err.Error(), `labels["app"]: "other" is not the value the component sets on its object ("web")`) {
		t.Fatalf("err = %v\nwant the app label refused", err)
	}
}
