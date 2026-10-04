package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// podTemplateKind is one kind component whose object carries a pod template the
// traits decorate: its handler, the properties of a minimal component, and how
// to read the template back out of the generated object.
type podTemplateKind struct {
	typ      string
	handler  oam.ComponentHandler
	props    map[string]any
	template func(client.Object) *corev1.PodTemplateSpec
}

// podTemplateProps is the pod template every podTemplateKind is authored with:
// one init and one regular container under the label `tier: web`.
func podTemplateProps() map[string]any {
	return map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
		"spec": map[string]any{
			"initContainers": []any{map[string]any{"name": "init", "image": "registry.example/team/init:1.0.0"}},
			"containers":     []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}},
		},
	}
}

var podTemplateKinds = []podTemplateKind{
	{
		typ:     "replicaset",
		handler: &components.ReplicaSetHandler{},
		props: map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
			"template": podTemplateProps(),
		},
		template: func(obj client.Object) *corev1.PodTemplateSpec {
			if rs, ok := obj.(*appsv1.ReplicaSet); ok {
				return &rs.Spec.Template
			}
			return nil
		},
	},
}

// newPodTemplateApp builds a component of kind k the way the transform does:
// the real handler decodes the properties, and the application carries its
// config.
func newPodTemplateApp(t *testing.T, k podTemplateKind, name string) *stack.Application {
	t.Helper()
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: name, Type: k.typ, Properties: k.props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return stack.NewApplication(name, "default", cfg)
}

// generatedPodTemplate generates app and returns the pod template of its one
// object of kind k.
func generatedPodTemplate(t *testing.T, k podTemplateKind, app *stack.Application) *corev1.PodTemplateSpec {
	t.Helper()
	objs, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, objPtr := range objs {
		if tmpl := k.template(*objPtr); tmpl != nil {
			return tmpl
		}
	}
	t.Fatalf("no %s object found in %d objects", k.typ, len(objs))
	return nil
}

// TestPodTemplateKinds_SecurityContextApplies: the security-context trait
// writes the restricted profile onto the pod template of each kind and onto
// each of its init and regular containers.
func TestPodTemplateKinds_SecurityContextApplies(t *testing.T) {
	for _, k := range podTemplateKinds {
		t.Run(k.typ, func(t *testing.T) {
			app := newPodTemplateApp(t, k, "web")
			if before := generatedPodTemplate(t, k, app); before.Spec.SecurityContext != nil {
				t.Fatalf("control: the template carries a securityContext before the trait: %+v", before.Spec.SecurityContext)
			}
			if err := (&traits.SecurityContextHandler{}).Apply(
				&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
				app, newBundle()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			spec := generatedPodTemplate(t, k, app).Spec
			if sc := spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.SeccompProfile == nil {
				t.Errorf("pod securityContext = %+v, want the restricted profile", sc)
			}
			for name, sc := range map[string]*corev1.SecurityContext{
				"init": spec.InitContainers[0].SecurityContext,
				"app":  spec.Containers[0].SecurityContext,
			} {
				if sc == nil || sc.Capabilities == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
					t.Errorf("%s: container securityContext = %+v, want the restricted profile", name, sc)
				}
			}
		})
	}
}

// TestPodTemplateKinds_ConfigMapMounts: a configmap trait with a mountPath
// mounts its ConfigMap into the pod template of each kind.
func TestPodTemplateKinds_ConfigMapMounts(t *testing.T) {
	for _, k := range podTemplateKinds {
		t.Run(k.typ, func(t *testing.T) {
			app := newPodTemplateApp(t, k, "web")
			if err := (&traits.ConfigMapHandler{}).Apply(
				&oam.Trait{Type: "configmap", Properties: map[string]any{
					"name": "cfg", "mountPath": "/etc/cfg", "data": map[string]any{"k": "v"},
				}}, app, newBundle()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			spec := generatedPodTemplate(t, k, app).Spec
			if len(spec.Volumes) != 1 || spec.Volumes[0].ConfigMap == nil || spec.Volumes[0].ConfigMap.Name != "cfg" {
				t.Fatalf("volumes = %+v, want one ConfigMap volume named cfg", spec.Volumes)
			}
			mounts := spec.Containers[0].VolumeMounts
			if len(mounts) != 1 || mounts[0].Name != "cfg" || mounts[0].MountPath != "/etc/cfg" {
				t.Errorf("volumeMounts = %+v, want cfg at /etc/cfg", mounts)
			}
		})
	}
}

// TestPodTemplateKinds_ExternalSecretInjects: an external-secret trait with
// envFrom and a mountPath injects its Secret into the pod template of each
// kind.
func TestPodTemplateKinds_ExternalSecretInjects(t *testing.T) {
	for _, k := range podTemplateKinds {
		t.Run(k.typ, func(t *testing.T) {
			app := newPodTemplateApp(t, k, "web")
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
			spec := generatedPodTemplate(t, k, app).Spec
			ef := spec.Containers[0].EnvFrom
			if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "creds" {
				t.Errorf("envFrom = %+v, want one secretRef named creds", ef)
			}
			if len(spec.Volumes) != 1 || spec.Volumes[0].Secret == nil || spec.Volumes[0].Secret.SecretName != "creds" {
				t.Fatalf("volumes = %+v, want one Secret volume named creds", spec.Volumes)
			}
			mounts := spec.Containers[0].VolumeMounts
			if len(mounts) != 1 || mounts[0].Name != "creds" || mounts[0].MountPath != "/etc/creds" {
				t.Errorf("volumeMounts = %+v, want creds at /etc/creds", mounts)
			}
		})
	}
}

// TestPodTemplateKinds_NetworkPolicySelectsThePods: the NetworkPolicy the
// networkpolicy trait builds for a component of each kind selects the pods its
// template describes, under a name long enough to be projected into the label
// value.
func TestPodTemplateKinds_NetworkPolicySelectsThePods(t *testing.T) {
	for _, k := range podTemplateKinds {
		for _, name := range []string{"web", "web-" + strings.Repeat("a", 63)} {
			t.Run(k.typ+"/"+name, func(t *testing.T) {
				app := newPodTemplateApp(t, k, name)
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
				tmpl := generatedPodTemplate(t, k, app)
				if !selector.Matches(labels.Set(tmpl.Labels)) {
					t.Errorf("podSelector %v does not match the template's labels %v", np.Spec.PodSelector.MatchLabels, tmpl.Labels)
				}
			})
		}
	}
}
