package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// newPodApp builds a `pod` component the way the transform does: the real
// handler decodes the properties, and the application carries its config.
func newPodApp(t *testing.T, name string) *stack.Application {
	t.Helper()
	cfg, err := (&components.PodHandler{}).ToApplicationConfig(&oam.Component{
		Name: name, Type: "pod",
		Properties: map[string]any{
			"initContainers": []any{map[string]any{"name": "init", "image": "registry.example/team/init:1.0.0"}},
			"containers":     []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return stack.NewApplication(name, "default", cfg)
}

// generatedPod generates app and returns its one Pod.
func generatedPod(t *testing.T, app *stack.Application) *corev1.Pod {
	t.Helper()
	objs, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, objPtr := range objs {
		if pod, ok := (*objPtr).(*corev1.Pod); ok {
			return pod
		}
	}
	t.Fatalf("no Pod found in %d objects", len(objs))
	return nil
}

// TestPodComponent_SecurityContextApplies: the security-context trait writes
// the restricted profile onto a `pod` component's Pod and each of its init and
// regular containers, as it does on a workload kind's pod template.
func TestPodComponent_SecurityContextApplies(t *testing.T) {
	app := newPodApp(t, "runner")
	if before := generatedPod(t, app); before.Spec.SecurityContext != nil {
		t.Fatalf("control: the pod carries a securityContext before the trait: %+v", before.Spec.SecurityContext)
	}
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
		app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	pod := generatedPod(t, app)
	if sc := pod.Spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.SeccompProfile == nil {
		t.Errorf("pod securityContext = %+v, want the restricted profile", sc)
	}
	for name, sc := range map[string]*corev1.SecurityContext{
		"init": pod.Spec.InitContainers[0].SecurityContext,
		"app":  pod.Spec.Containers[0].SecurityContext,
	} {
		if sc == nil || sc.Capabilities == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Errorf("%s: container securityContext = %+v, want the restricted profile", name, sc)
		}
	}
}

// TestPodComponent_ConfigMapMounts: a configmap trait with a mountPath mounts
// its ConfigMap into a `pod` component's Pod.
func TestPodComponent_ConfigMapMounts(t *testing.T) {
	app := newPodApp(t, "runner")
	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{
			"name": "cfg", "mountPath": "/etc/cfg", "data": map[string]any{"k": "v"},
		}}, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	pod := generatedPod(t, app)
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].ConfigMap == nil || pod.Spec.Volumes[0].ConfigMap.Name != "cfg" {
		t.Fatalf("volumes = %+v, want one ConfigMap volume named cfg", pod.Spec.Volumes)
	}
	mounts := pod.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].Name != "cfg" || mounts[0].MountPath != "/etc/cfg" {
		t.Errorf("volumeMounts = %+v, want cfg at /etc/cfg", mounts)
	}
}

// TestPodComponent_ExternalSecretInjects: an external-secret trait with envFrom
// and a mountPath injects its Secret into a `pod` component's Pod.
func TestPodComponent_ExternalSecretInjects(t *testing.T) {
	app := newPodApp(t, "runner")
	if err := (&traits.ExternalSecretHandler{}).Apply(
		&oam.Trait{Type: "external-secret", Properties: map[string]any{
			"secretName": "creds",
			"provider":   "vault-backend",
			"envFrom":    true,
			"mountPath":  "/etc/creds",
			"data":       []any{map[string]any{"secretKey": "DB_PASSWORD"}},
		}}, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	pod := generatedPod(t, app)
	ef := pod.Spec.Containers[0].EnvFrom
	if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "creds" {
		t.Errorf("envFrom = %+v, want one secretRef named creds", ef)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].Secret == nil || pod.Spec.Volumes[0].Secret.SecretName != "creds" {
		t.Fatalf("volumes = %+v, want one Secret volume named creds", pod.Spec.Volumes)
	}
	mounts := pod.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].Name != "creds" || mounts[0].MountPath != "/etc/creds" {
		t.Errorf("volumeMounts = %+v, want creds at /etc/creds", mounts)
	}
}

// TestPodComponent_NetworkPolicySelectsThePod: the NetworkPolicy the
// networkpolicy trait builds for a `pod` component selects that component's
// Pod, under a name long enough to be projected into the label value.
func TestPodComponent_NetworkPolicySelectsThePod(t *testing.T) {
	for _, name := range []string{"runner", "runner-" + strings.Repeat("a", 63)} {
		t.Run(name, func(t *testing.T) {
			app := newPodApp(t, name)
			bundle := newBundle()
			if err := (&traits.NetworkPolicyHandler{}).Apply(
				&oam.Trait{Type: "networkpolicy", Properties: map[string]any{"ingress": []any{}}},
				app, bundle); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(bundle.Applications) != 1 {
				t.Fatalf("bundle applications = %d, want the policy's", len(bundle.Applications))
			}
			npApp := bundle.Applications[0]
			objs, err := npApp.Config.Generate(npApp)
			if err != nil {
				t.Fatalf("Generate (policy): %v", err)
			}
			np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
			if !ok {
				t.Fatalf("object = %T, want a NetworkPolicy", *objs[0])
			}
			if len(np.Spec.PodSelector.MatchLabels) == 0 {
				t.Fatal("the policy's podSelector is empty: it would select every pod in the namespace")
			}
			selector, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
			if err != nil {
				t.Fatalf("podSelector: %v", err)
			}
			pod := generatedPod(t, app)
			if !selector.Matches(labels.Set(pod.Labels)) {
				t.Errorf("podSelector %v does not match the Pod's labels %v", np.Spec.PodSelector.MatchLabels, pod.Labels)
			}
		})
	}
}
