package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// windowsDeployStub emits one Deployment whose pod template declares
// os.name: windows, the shape go-kure/launcher#342's pod-level `os` property
// produces.
type windowsDeployStub struct{}

func (d *windowsDeployStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					OS:             &corev1.PodOS{Name: corev1.Windows},
					Containers:     []corev1.Container{{Name: "main", Image: "example:latest"}},
					InitContainers: []corev1.Container{{Name: "init", Image: "example:latest"}},
				},
			},
		},
	}
	obj := client.Object(dep)
	return []*client.Object{&obj}, nil
}

// TestSecurityContextHandler_WindowsPod_RestrictedSubset: on a Windows pod the
// trait writes only the fields Kubernetes accepts there. Writing the Linux
// restricted profile would emit a workload the API server rejects, and the
// component-side os validation cannot catch it because it runs before this
// decorator.
func TestSecurityContextHandler_WindowsPod_RestrictedSubset(t *testing.T) {
	app := stack.NewApplication("svc", "ns", &windowsDeployStub{})
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
		app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ps := (*resources[0]).(*appsv1.Deployment).Spec.Template.Spec

	podSC := ps.SecurityContext
	if podSC == nil {
		t.Fatal("pod SecurityContext is nil; the downstream mutator would overwrite it")
	}
	if podSC.RunAsNonRoot == nil || !*podSC.RunAsNonRoot {
		t.Error("pod RunAsNonRoot must be true: Windows accepts it")
	}
	if podSC.SeccompProfile != nil {
		t.Error("pod SeccompProfile must be unset on a Windows pod")
	}
	if podSC.FSGroup != nil || podSC.RunAsUser != nil || podSC.RunAsGroup != nil {
		t.Errorf("pod fsGroup/runAsUser/runAsGroup must be unset on a Windows pod, got %+v", podSC)
	}

	for name, sc := range map[string]*corev1.SecurityContext{
		"main": ps.Containers[0].SecurityContext,
		"init": ps.InitContainers[0].SecurityContext,
	} {
		if sc == nil {
			t.Fatalf("%s: container SecurityContext is nil", name)
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Errorf("%s: RunAsNonRoot must be true", name)
		}
		if sc.Capabilities != nil {
			t.Errorf("%s: Capabilities must be unset on a Windows pod", name)
		}
		if sc.SeccompProfile != nil {
			t.Errorf("%s: SeccompProfile must be unset on a Windows pod", name)
		}
		if sc.AllowPrivilegeEscalation != nil {
			t.Errorf("%s: AllowPrivilegeEscalation must be unset on a Windows pod", name)
		}
		if sc.ReadOnlyRootFilesystem != nil {
			t.Errorf("%s: ReadOnlyRootFilesystem must be unset on a Windows pod", name)
		}
	}
}

// TestSecurityContextHandler_WindowsPod_RejectsIllegalOverride: an override the
// author set explicitly and Windows forbids fails loudly instead of being
// silently dropped.
func TestSecurityContextHandler_WindowsPod_RejectsIllegalOverride(t *testing.T) {
	for _, key := range []string{"runAsUser", "runAsGroup", "fsGroup"} {
		t.Run(key, func(t *testing.T) {
			app := stack.NewApplication("svc", "ns", &windowsDeployStub{})
			if err := (&traits.SecurityContextHandler{}).Apply(
				&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "baseline", key: 1000}},
				app, newBundle()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			_, err := app.Generate()
			if err == nil || !strings.Contains(err.Error(), key+" cannot be set on a component with os.name: windows") {
				t.Fatalf("Generate error = %v, want the Windows rejection for %s", err, key)
			}
		})
	}
	// readOnlyRootFilesystem: false is legal under baseline and still refused
	// on a Windows pod, so the check is not merely the psaLevel conflict.
	app := stack.NewApplication("svc", "ns", &windowsDeployStub{})
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "baseline", "readOnlyRootFilesystem": false}},
		app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := app.Generate(); err == nil || !strings.Contains(err.Error(), "readOnlyRootFilesystem cannot be set") {
		t.Fatalf("Generate error = %v, want the Windows rejection for readOnlyRootFilesystem", err)
	}
}

// TestSecurityContextHandler_WindowsPod_ThroughComponent covers the real path:
// the pod-level `os` property on a workload kind, then the trait on top. The
// generated workload must be one Kubernetes accepts.
func TestSecurityContextHandler_WindowsPod_ThroughComponent(t *testing.T) {
	cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(&oam.Component{
		Name: "api", Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/api:v1",
			"os":    map[string]any{"name": "windows"},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("api", "default", cfg)
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
		app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var dep *appsv1.Deployment
	for _, r := range resources {
		if d, ok := (*r).(*appsv1.Deployment); ok {
			dep = d
		}
	}
	if dep == nil {
		t.Fatal("no Deployment generated")
	}
	ps := dep.Spec.Template.Spec
	if ps.OS == nil || ps.OS.Name != corev1.Windows {
		t.Fatalf("pod OS = %+v, want windows", ps.OS)
	}
	if ps.SecurityContext == nil || ps.SecurityContext.SeccompProfile != nil {
		t.Errorf("pod SecurityContext = %+v, want non-nil without seccompProfile", ps.SecurityContext)
	}
	for i, c := range ps.Containers {
		if c.SecurityContext == nil {
			t.Fatalf("containers[%d]: SecurityContext is nil", i)
		}
		if c.SecurityContext.Capabilities != nil || c.SecurityContext.ReadOnlyRootFilesystem != nil {
			t.Errorf("containers[%d].securityContext = %+v, want the Windows subset", i, c.SecurityContext)
		}
	}
}

// TestSecurityContextHandler_LinuxUnchanged pins the no-os path: without
// os.name the restricted Linux profile is written exactly as before.
func TestSecurityContextHandler_LinuxUnchanged(t *testing.T) {
	app := stack.NewApplication("svc", "ns", &deployStub{name: "svc", namespace: "ns", withInit: true})
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
		app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ps := (*resources[0]).(*appsv1.Deployment).Spec.Template.Spec
	if ps.SecurityContext == nil || ps.SecurityContext.SeccompProfile == nil {
		t.Error("linux restricted: pod SeccompProfile must still be set")
	}
	sc := ps.Containers[0].SecurityContext
	if sc == nil || sc.Capabilities == nil || sc.ReadOnlyRootFilesystem == nil {
		t.Errorf("linux restricted: container securityContext = %+v, want the full profile", sc)
	}
}
