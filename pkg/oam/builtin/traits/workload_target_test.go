package traits_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// go-kure/launcher#794, item 14: a trait that writes to a pod spec is refused
// on a component that generates no workload, in one message form for the three
// traits that do. The security-context trait's psaLevel is the exception: it
// declares the level the component requires, so alone it is accepted there and
// writes nothing.

// noWorkloadTail is the part of the refusal every trait shares: where the trait
// does write, and what it does not look into.
const noWorkloadTail = "; the trait writes to the pod spec of a Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob or Pod that a launcher kind builds, a manifests source yields or a helmtemplate chart renders, and an object passed through as raw, unstructured output (passthrough) is not inspected"

// psaLevelHint closes the security-context refusal: what the author may keep.
const psaLevelHint = "; psaLevel alone is accepted on such a component, as a declaration of the level it requires"

// noWorkloadMessage is the refusal of trait on component for the properties,
// which are already joined as prose with their verb ("runAsUser applies",
// "envFrom and mountPath apply").
func noWorkloadMessage(trait, component, propertiesAndVerb string) string {
	return trait + `: component "` + component + `" generates no workload the trait can act on, so ` +
		propertiesAndVerb + " to nothing" + noWorkloadTail
}

// stubObjectsConfig generates the objects it holds, as a component whose
// output a test spells out.
type stubObjectsConfig struct{ objects []client.Object }

func (s *stubObjectsConfig) Generate(*stack.Application) ([]*client.Object, error) {
	out := make([]*client.Object, 0, len(s.objects))
	for i := range s.objects {
		out = append(out, &s.objects[i])
	}
	return out, nil
}

// stubNamedObjectsConfig is a stubObjectsConfig that names its component
// (oam.ComponentNamed), as a config generated under another application name
// does.
type stubNamedObjectsConfig struct {
	stubObjectsConfig
	component string
}

func (s *stubNamedObjectsConfig) ComponentName() string { return s.component }

// noWorkloadComponent is one component that generates no workload a pod-spec
// trait can act on.
type noWorkloadComponent struct {
	name string
	// config builds the component's config afresh: a decorator must not see
	// what an earlier case left behind.
	config func(t *testing.T) stack.ApplicationConfig
}

// passthroughDeploymentConfig is a Deployment passed through as raw output: the
// passthrough kind emits it unstructured, so no trait reads its pod template.
// The config names its component itself, so the component is called "comp" as
// the application every case generates under is.
func passthroughDeploymentConfig(t *testing.T) stack.ApplicationConfig {
	t.Helper()
	cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(&oam.Component{
		Name: "comp",
		Type: "passthrough",
		Properties: map[string]any{"object": map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "raw"},
			"spec": map[string]any{
				"selector": map[string]any{"matchLabels": map[string]any{"app": "raw"}},
				"template": map[string]any{
					"metadata": map[string]any{"labels": map[string]any{"app": "raw"}},
					"spec": map[string]any{"containers": []any{
						map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"},
					}},
				},
			},
		}},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

func noWorkloadComponents() []noWorkloadComponent {
	return []noWorkloadComponent{
		{
			name: "an object that is no workload",
			config: func(*testing.T) stack.ApplicationConfig {
				return &stubObjectsConfig{objects: []client.Object{
					&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc"}},
				}}
			},
		},
		{
			name: "a custom resource a controller turns into pods",
			config: func(*testing.T) stack.ApplicationConfig {
				cr := &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "example.com/v1",
					"kind":       "Database",
					"metadata":   map[string]any{"name": "db"},
				}}
				return &stubObjectsConfig{objects: []client.Object{cr}}
			},
		},
		{
			name: "a stored pod template",
			config: func(t *testing.T) stack.ApplicationConfig {
				return newPodTemplateApp(t, podTemplateComponent, "batch").Config
			},
		},
		{
			name:   "a Deployment passed through as raw output",
			config: passthroughDeploymentConfig,
		},
		{
			name: "a ReplicationController without a template",
			config: func(*testing.T) stack.ApplicationConfig {
				return &stubObjectsConfig{objects: []client.Object{
					&corev1.ReplicationController{ObjectMeta: metav1.ObjectMeta{Name: "rc"}},
				}}
			},
		},
		{
			name: "no object at all",
			config: func(*testing.T) stack.ApplicationConfig {
				return &stubObjectsConfig{}
			},
		},
	}
}

// generatedCopies generates cfg under an application named name and returns
// the objects, dereferenced.
func generatedCopies(t *testing.T, cfg stack.ApplicationConfig, name string) []client.Object {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication(name, "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := make([]client.Object, 0, len(objs))
	for _, p := range objs {
		out = append(out, (*p).DeepCopyObject().(client.Object))
	}
	return out
}

// withSecurityContext puts a security-context trait with props on an
// application named name over cfg, and returns the application.
func withSecurityContext(t *testing.T, cfg stack.ApplicationConfig, name string, props map[string]any) *stack.Application {
	t.Helper()
	app := stack.NewApplication(name, "default", cfg)
	if err := (&traits.SecurityContextHandler{}).Apply(
		&oam.Trait{Type: "security-context", Properties: props}, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return app
}

// TestSecurityContext_NoWorkload_PSALevelAloneWritesNothing: on a component
// that generates no workload, a security-context trait carrying only psaLevel
// is accepted at every level and leaves the component's objects as they were.
func TestSecurityContext_NoWorkload_PSALevelAloneWritesNothing(t *testing.T) {
	for _, c := range noWorkloadComponents() {
		for _, level := range []string{"restricted", "baseline", "privileged"} {
			t.Run(c.name+"/"+level, func(t *testing.T) {
				want := generatedCopies(t, c.config(t), "comp")
				app := withSecurityContext(t, c.config(t), "comp", map[string]any{"psaLevel": level})
				got := generatedCopies(t, app.Config, "comp")
				if !reflect.DeepEqual(got, want) {
					t.Errorf("the trait changed the component's objects:\n got %+v\nwant %+v", got, want)
				}
			})
		}
	}
}

// TestSecurityContext_NoWorkload_PodSpecPropertyRefused: each property the
// trait writes to a pod spec is refused on a component that generates no
// workload, naming the trait, the component and that property.
func TestSecurityContext_NoWorkload_PodSpecPropertyRefused(t *testing.T) {
	properties := []struct {
		name  string
		value any
	}{
		{"runAsNonRoot", true},
		{"allowPrivilegeEscalation", false},
		{"readOnlyRootFilesystem", true},
		{"runAsUser", 1000},
		{"runAsGroup", 1000},
		{"fsGroup", 1000},
	}
	for _, c := range noWorkloadComponents() {
		for _, p := range properties {
			t.Run(c.name+"/"+p.name, func(t *testing.T) {
				app := withSecurityContext(t, c.config(t), "comp",
					map[string]any{"psaLevel": "restricted", p.name: p.value})
				_, err := app.Config.Generate(app)
				want := noWorkloadMessage("security-context", "comp", p.name+" applies") + psaLevelHint
				if err == nil || err.Error() != want {
					t.Fatalf("Generate err:\n got %v\nwant %s", err, want)
				}
			})
		}
	}
}

// TestSecurityContext_NoWorkload_RefusalNamesEveryProperty: the refusal lists
// every pod-spec property the trait carries, in the schema's order whatever the
// order they were authored in, and never psaLevel.
func TestSecurityContext_NoWorkload_RefusalNamesEveryProperty(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			name:  "two",
			props: map[string]any{"psaLevel": "baseline", "fsGroup": 2000, "runAsUser": 1000},
			want:  "runAsUser and fsGroup apply",
		},
		{
			name: "all six",
			props: map[string]any{
				"psaLevel": "restricted", "fsGroup": 2000, "runAsGroup": 1000, "runAsUser": 1000,
				"readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "runAsNonRoot": true,
			},
			want: "runAsNonRoot, allowPrivilegeEscalation, readOnlyRootFilesystem, runAsUser, runAsGroup and fsGroup apply",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := withSecurityContext(t, &stubUnsupportedConfig{}, "svc", tc.props)
			_, err := app.Config.Generate(app)
			want := noWorkloadMessage("security-context", "svc", tc.want) + psaLevelHint
			if err == nil || err.Error() != want {
				t.Fatalf("Generate err:\n got %v\nwant %s", err, want)
			}
		})
	}
}

// mixedOutputConfig is a component that generates a Deployment next to objects
// no pod-spec trait reads: a stored pod template, a raw workload and a Service.
func mixedOutputConfig() *stubObjectsConfig {
	podSpec := corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/team/app:1.2.3"}}}
	return &stubObjectsConfig{objects: []client.Object{
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web"}},
		&corev1.PodTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "stored"},
			Template:   corev1.PodTemplateSpec{Spec: *podSpec.DeepCopy()},
		},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata":   map[string]any{"name": "raw"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}},
			}}},
		}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web"},
			Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: *podSpec.DeepCopy()}},
		},
	}}
}

// TestPodSpecTraits_MixedOutput_ApplyToTheWorkload: a component that generates
// a workload next to objects the traits do not read is not refused. Each trait
// applies to the workload and leaves the other objects as they were.
func TestPodSpecTraits_MixedOutput_ApplyToTheWorkload(t *testing.T) {
	cases := []struct {
		name  string
		apply func(app *stack.Application) error
		check func(t *testing.T, spec corev1.PodSpec)
	}{
		{
			name: "security-context",
			apply: func(app *stack.Application) error {
				return (&traits.SecurityContextHandler{}).Apply(&oam.Trait{Type: "security-context", Properties: map[string]any{
					"psaLevel": "restricted", "runAsUser": 1000,
				}}, app, newBundle())
			},
			check: func(t *testing.T, spec corev1.PodSpec) {
				if sc := spec.SecurityContext; sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
					t.Errorf("pod securityContext = %+v, want runAsUser 1000", sc)
				}
				if spec.Containers[0].SecurityContext == nil {
					t.Error("the container has no securityContext")
				}
			},
		},
		{
			name: "configmap mountPath",
			apply: func(app *stack.Application) error {
				return (&traits.ConfigMapHandler{}).Apply(&oam.Trait{Type: "configmap", Properties: map[string]any{
					"name": "cfg", "mountPath": "/etc/cfg", "data": map[string]any{"k": "v"},
				}}, app, newBundle())
			},
			check: func(t *testing.T, spec corev1.PodSpec) {
				if len(spec.Volumes) != 1 || spec.Volumes[0].Name != "cfg" {
					t.Errorf("volumes = %+v, want the ConfigMap's", spec.Volumes)
				}
			},
		},
		{
			name: "external-secret envFrom and mountPath",
			apply: func(app *stack.Application) error {
				return (&traits.ExternalSecretHandler{}).Apply(&oam.Trait{Type: "external-secret", Properties: map[string]any{
					"secretName": "creds", "provider": "vault-backend", "envFrom": true, "mountPath": "/etc/creds",
					"data": []any{map[string]any{"secretKey": "DB_PASSWORD"}},
				}}, app, newBundle())
			},
			check: func(t *testing.T, spec corev1.PodSpec) {
				if len(spec.Volumes) != 1 || spec.Volumes[0].Name != "creds" {
					t.Errorf("volumes = %+v, want the Secret's", spec.Volumes)
				}
				if len(spec.Containers[0].EnvFrom) != 1 {
					t.Errorf("envFrom = %+v, want the Secret's", spec.Containers[0].EnvFrom)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := generatedCopies(t, mixedOutputConfig(), "web")
			app := stack.NewApplication("web", "default", mixedOutputConfig())
			if err := tc.apply(app); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			after := generatedCopies(t, app.Config, "web")
			if len(after) != len(before) {
				t.Fatalf("objects = %d, want %d", len(after), len(before))
			}
			for i := range after {
				dep, ok := after[i].(*appsv1.Deployment)
				if !ok {
					if !reflect.DeepEqual(after[i], before[i]) {
						t.Errorf("object %d (%T) changed:\n got %+v\nwant %+v", i, after[i], after[i], before[i])
					}
					continue
				}
				if reflect.DeepEqual(after[i], before[i]) {
					t.Fatal("the Deployment is unchanged: the trait did not apply to the workload")
				}
				tc.check(t, dep.Spec.Template.Spec)
			}
		})
	}
}

// TestMountTraits_NoWorkload_Refused: a configmap mount and an external-secret
// injection are refused on a component that generates no workload, in the form
// the security-context refusal has, naming the properties that have nowhere to
// go.
func TestMountTraits_NoWorkload_Refused(t *testing.T) {
	cases := []struct {
		name      string
		decorate  func(inner stack.ApplicationConfig) stack.ApplicationConfig
		trait     string
		propsVerb string
	}{
		{
			name: "configmap mountPath",
			decorate: func(inner stack.ApplicationConfig) stack.ApplicationConfig {
				return traits.NewConfigMapDecorator(inner, "cfg", "/etc/cfg")
			},
			trait: "configmap", propsVerb: "mountPath applies",
		},
		{
			name: "external-secret envFrom",
			decorate: func(inner stack.ApplicationConfig) stack.ApplicationConfig {
				return traits.NewExternalSecretDecorator(inner, "creds", "", true)
			},
			trait: "external-secret", propsVerb: "envFrom applies",
		},
		{
			name: "external-secret mountPath",
			decorate: func(inner stack.ApplicationConfig) stack.ApplicationConfig {
				return traits.NewExternalSecretDecorator(inner, "creds", "/etc/creds", false)
			},
			trait: "external-secret", propsVerb: "mountPath applies",
		},
		{
			name: "external-secret envFrom and mountPath",
			decorate: func(inner stack.ApplicationConfig) stack.ApplicationConfig {
				return traits.NewExternalSecretDecorator(inner, "creds", "/etc/creds", true)
			},
			trait: "external-secret", propsVerb: "envFrom and mountPath apply",
		},
	}
	for _, c := range noWorkloadComponents() {
		for _, tc := range cases {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				cfg := tc.decorate(c.config(t))
				_, err := cfg.Generate(stack.NewApplication("comp", "default", cfg))
				want := noWorkloadMessage(tc.trait, "comp", tc.propsVerb)
				if err == nil || err.Error() != want {
					t.Fatalf("Generate err:\n got %v\nwant %s", err, want)
				}
			})
		}
	}
}

// TestNoWorkloadRefusal_NamesTheComponentTheConfigNames: the refusal names the
// component the decorated config names (oam.ComponentNamed) ahead of the
// application it is generated under, which a consumer may have named apart.
func TestNoWorkloadRefusal_NamesTheComponentTheConfigNames(t *testing.T) {
	named := func() stack.ApplicationConfig {
		return &stubNamedObjectsConfig{component: "web"}
	}
	for name, generate := range map[string]func() error{
		"security-context": func() error {
			app := withSecurityContext(t, named(), "web-objects", map[string]any{"psaLevel": "baseline", "runAsUser": 1000})
			_, err := app.Config.Generate(app)
			return err
		},
		"configmap": func() error {
			cfg := traits.NewConfigMapDecorator(named(), "cfg", "/etc/cfg")
			_, err := cfg.Generate(stack.NewApplication("web-objects", "default", cfg))
			return err
		},
		"external-secret": func() error {
			cfg := traits.NewExternalSecretDecorator(named(), "creds", "", true)
			_, err := cfg.Generate(stack.NewApplication("web-objects", "default", cfg))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := generate()
			want := name + `: component "web" generates no workload the trait can act on, so `
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("Generate err = %v, want one starting %q", err, want)
			}
		})
	}
}

// TestTransform_SecurityContext_OnAComponentWithoutWorkload: through the
// transform, a `service` component (a Service in front of another component's
// pods) builds with psaLevel alone and is refused with a pod-spec property,
// under the component's own name.
func TestTransform_SecurityContext_OnAComponentWithoutWorkload(t *testing.T) {
	build := func(props map[string]any) error {
		app := &oam.Application{
			APIVersion: oam.SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
			Spec: oam.ApplicationSpec{Components: []oam.Component{
				apiServerComponent(),
				apiServiceComponent(oam.Trait{Type: "security-context", Properties: props}),
			}},
		}
		cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		_, err = oam.GenerateApplications(cluster)
		return err
	}

	if err := build(map[string]any{"psaLevel": "baseline"}); err != nil {
		t.Errorf("psaLevel alone: %v", err)
	}
	err := build(map[string]any{"psaLevel": "baseline", "runAsUser": 1000})
	want := noWorkloadMessage("security-context", "api", "runAsUser applies") + psaLevelHint
	if err == nil || err.Error() != want {
		t.Fatalf("with a pod-spec property:\n got %v\nwant %s", err, want)
	}
}
