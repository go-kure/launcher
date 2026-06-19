package traits_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// deployStub emits a single Deployment with one container (and one init container
// when withInit is true). Used to verify SecurityContext application.
type deployStub struct {
	name      string
	namespace string
	withInit  bool
}

func (d *deployStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	containers := []corev1.Container{
		{Name: "main", Image: "example:latest"},
	}
	var initContainers []corev1.Container
	if d.withInit {
		initContainers = []corev1.Container{
			{Name: "init", Image: "busybox:latest"},
		}
	}
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.name,
			Namespace: d.namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:     containers,
					InitContainers: initContainers,
				},
			},
		},
	}
	obj := client.Object(dep)
	return []*client.Object{&obj}, nil
}

func TestSecurityContextHandler_CanHandle(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"security-context", true},
		{"prune-protection", false},
		{"rbac", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

func TestSecurityContextHandler_Apply_Baseline(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("db", "db", &deployStub{name: "db", namespace: "db"})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{"psaLevel": "baseline"},
	}
	if err := h.Apply(trait, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("expected resources, got none")
	}

	dep, ok := (*resources[0]).(*appsv1.Deployment)
	if !ok {
		t.Fatalf("expected *appsv1.Deployment, got %T", *resources[0])
	}

	podSC := dep.Spec.Template.Spec.SecurityContext
	if podSC == nil {
		t.Fatal("pod SecurityContext is nil; expected non-nil for baseline")
	}
	if podSC.RunAsNonRoot != nil {
		t.Errorf("baseline: pod RunAsNonRoot should not be set, got %v", *podSC.RunAsNonRoot)
	}

	if len(dep.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("no containers in Deployment")
	}
	containerSC := dep.Spec.Template.Spec.Containers[0].SecurityContext
	if containerSC == nil {
		t.Fatal("container SecurityContext is nil; expected non-nil for baseline")
	}
	// Baseline must NOT set AllowPrivilegeEscalation, Capabilities.Drop, or SeccompProfile.
	if containerSC.AllowPrivilegeEscalation != nil {
		t.Errorf("baseline: AllowPrivilegeEscalation should not be set")
	}
	if containerSC.Capabilities != nil {
		t.Errorf("baseline: Capabilities should not be set")
	}
	if containerSC.SeccompProfile != nil {
		t.Errorf("baseline: SeccompProfile should not be set")
	}
}

func TestSecurityContextHandler_Apply_Restricted(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns"})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{"psaLevel": "restricted"},
	}
	if err := h.Apply(trait, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dep := (*resources[0]).(*appsv1.Deployment)
	podSC := dep.Spec.Template.Spec.SecurityContext
	if podSC == nil {
		t.Fatal("pod SecurityContext is nil")
	}
	if podSC.RunAsNonRoot == nil || !*podSC.RunAsNonRoot {
		t.Error("restricted: pod RunAsNonRoot must be true")
	}
	if podSC.SeccompProfile == nil {
		t.Error("restricted: pod SeccompProfile must be set")
	}

	containerSC := dep.Spec.Template.Spec.Containers[0].SecurityContext
	if containerSC == nil {
		t.Fatal("container SecurityContext is nil")
	}
	if containerSC.AllowPrivilegeEscalation == nil || *containerSC.AllowPrivilegeEscalation {
		t.Error("restricted: AllowPrivilegeEscalation must be false")
	}
	if containerSC.Capabilities == nil {
		t.Error("restricted: Capabilities must be set")
	}
	if containerSC.ReadOnlyRootFilesystem == nil || !*containerSC.ReadOnlyRootFilesystem {
		t.Error("restricted: ReadOnlyRootFilesystem must be true")
	}
}

func TestSecurityContextHandler_Apply_InitContainers(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("db", "db", &deployStub{name: "db", namespace: "db", withInit: true})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{"psaLevel": "baseline"},
	}
	if err := h.Apply(trait, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dep := (*resources[0]).(*appsv1.Deployment)
	if len(dep.Spec.Template.Spec.InitContainers) == 0 {
		t.Fatal("no init containers in Deployment")
	}
	initSC := dep.Spec.Template.Spec.InitContainers[0].SecurityContext
	if initSC == nil {
		t.Fatal("init container SecurityContext is nil; expected non-nil for baseline")
	}
}

func TestSecurityContextHandler_Apply_WithFsGroupOverride(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("db", "db", &deployStub{name: "db", namespace: "db"})
	trait := &oam.Trait{
		Type: "security-context",
		Properties: map[string]any{
			"psaLevel": "baseline",
			"fsGroup":  int64(1000),
		},
	}
	if err := h.Apply(trait, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dep := (*resources[0]).(*appsv1.Deployment)
	podSC := dep.Spec.Template.Spec.SecurityContext
	if podSC == nil || podSC.FSGroup == nil || *podSC.FSGroup != 1000 {
		t.Errorf("expected FSGroup=1000, got %v", podSC)
	}
}

func TestSecurityContextHandler_Error_MissingPSALevel(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns"})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{},
	}
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error for missing psaLevel, got nil")
	}
}

func TestSecurityContextHandler_Error_InvalidPSALevel(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns"})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{"psaLevel": "permissive"},
	}
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error for invalid psaLevel, got nil")
	}
}

func TestSecurityContextHandler_Error_ConflictingOverride(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns"})
	trait := &oam.Trait{
		Type: "security-context",
		Properties: map[string]any{
			"psaLevel":                 "restricted",
			"allowPrivilegeEscalation": true,
		},
	}
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error for restricted + allowPrivilegeEscalation:true, got nil")
	}
}

func TestSecurityContextHandler_NonPodSpecResourcesPassThrough(t *testing.T) {
	// cmStub is defined in pruneprotection_test.go (same package traits_test).
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("cfg", "ns", &cmStub{name: "cfg", namespace: "ns"})
	trait := &oam.Trait{
		Type:       "security-context",
		Properties: map[string]any{"psaLevel": "baseline"},
	}
	if err := h.Apply(trait, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("expected resources, got none")
	}
	// ConfigMap passes through untouched — just verify no panic and type is preserved.
	if _, ok := (*resources[0]).(*corev1.ConfigMap); !ok {
		t.Errorf("expected *corev1.ConfigMap, got %T", *resources[0])
	}
}

func TestSecurityContextHandler_Error_NonIntegralFloat(t *testing.T) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns"})
	trait := &oam.Trait{
		Type: "security-context",
		Properties: map[string]any{
			"psaLevel":  "baseline",
			"runAsUser": float64(1000.5),
		},
	}
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error for non-integral float runAsUser, got nil")
	}
}
