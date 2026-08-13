package traits_test

import (
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// newWorkerStubConfig builds a real components.WorkerConfig whose Generate
// produces a Deployment with one container, for tests that need a genuine
// workload-producing ApplicationConfig rather than a hand-rolled stub.
func newWorkerStubConfig(t *testing.T) *components.WorkerConfig {
	t.Helper()
	return &components.WorkerConfig{
		Image:    "worker:v1",
		Replicas: 1,
	}
}

// newCronJobStubConfig builds a real components.CronjobConfig whose Generate
// produces a CronJob with one container.
func newCronJobStubConfig(t *testing.T) *components.CronjobConfig {
	t.Helper()
	return &components.CronjobConfig{
		Image:    "job:v1",
		Schedule: "0 2 * * *",
	}
}

// findDeployment pulls the first *appsv1.Deployment out of a generated object
// slice, failing the test if none is present.
func findDeployment(t *testing.T, objs []*client.Object) *appsv1.Deployment {
	t.Helper()
	for _, objPtr := range objs {
		if dep, ok := (*objPtr).(*appsv1.Deployment); ok {
			return dep
		}
	}
	t.Fatalf("no Deployment found in %d objects", len(objs))
	return nil
}

// podSpecOf extracts the PodSpec from whichever supported workload kind is
// present in objs, mirroring the kinds ExternalSecretDecorator.Generate
// switches on.
func podSpecOf(t *testing.T, objs []*client.Object) *corev1.PodSpec {
	t.Helper()
	for _, objPtr := range objs {
		switch w := (*objPtr).(type) {
		case *appsv1.Deployment:
			return &w.Spec.Template.Spec
		case *appsv1.StatefulSet:
			return &w.Spec.Template.Spec
		case *appsv1.DaemonSet:
			return &w.Spec.Template.Spec
		case *batchv1.CronJob:
			return &w.Spec.JobTemplate.Spec.Template.Spec
		}
	}
	t.Fatalf("no supported workload found in %d objects", len(objs))
	return nil
}

// stubWorkerWithService is a Deployment-producing stub that also satisfies
// servicePortProvider/serviceBackendNamer duck-typed interfaces, for tests
// checking those forward through decorator chains.
type stubWorkerWithService struct {
	port        int32
	serviceName string
}

func (s *stubWorkerWithService) ServicePort() int32         { return s.port }
func (s *stubWorkerWithService) BackendServiceName() string { return s.serviceName }
func (s *stubWorkerWithService) Generate(app *stack.Application) ([]*client.Object, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: app.Namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: app.Name, Image: "img:1"}},
				},
			},
		},
	}
	obj := client.Object(dep)
	return []*client.Object{&obj}, nil
}

// stubZeroContainerConfig is a Deployment-producing stub with no containers,
// to verify envFrom injection is a safe no-op rather than a panic.
type stubZeroContainerConfig struct{}

func (s *stubZeroContainerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: app.Namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
	}
	obj := client.Object(dep)
	return []*client.Object{&obj}, nil
}

// stubVolumeCollisionConfig is a Deployment-producing stub that already has a
// volume named volumeName, to verify mountPath's collision check.
type stubVolumeCollisionConfig struct {
	volumeName string
}

func (s *stubVolumeCollisionConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: app.Namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: app.Name, Image: "img:1"}},
					Volumes:    []corev1.Volume{{Name: s.volumeName}},
				},
			},
		},
	}
	obj := client.Object(dep)
	return []*client.Object{&obj}, nil
}

// Inline secretStoreRef in trait properties works without a capability.
func TestExternalSecretHandler_InlineSecretStoreRef(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "db-password",
			"secretStoreRef": map[string]any{
				"name": "vault-backend",
				"kind": "ClusterSecretStore",
			},
			"remoteRef": map[string]any{"key": "secret/db"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply with inline secretStoreRef: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	// Verify the inline secretStoreRef is used correctly in the generated ExternalSecret.
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "vault-backend" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "vault-backend")
	}
	if es.Spec.SecretStoreRef.Kind != "ClusterSecretStore" {
		t.Errorf("SecretStoreRef.Kind = %q, want %q", es.Spec.SecretStoreRef.Kind, "ClusterSecretStore")
	}
}

// provider: string shorthand maps to ClusterSecretStore (downstream backward-compat).
func TestExternalSecretHandler_ProviderShorthand(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "cloud-secret",
			"provider":   "aws-secretsmanager",
			"remoteRef":  map[string]any{"key": "prod/db"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply with provider shorthand: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	// Verify the provider name is used as StoreRefName.
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "aws-secretsmanager" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "aws-secretsmanager")
	}
	if es.Spec.SecretStoreRef.Kind != "ClusterSecretStore" {
		t.Errorf("SecretStoreRef.Kind = %q, want %q", es.Spec.SecretStoreRef.Kind, "ClusterSecretStore")
	}
}

// Inline secretStoreRef with output assertion.
func TestExternalSecretHandler_InlineSecretStoreRef_WithOutputAssertion(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"secretStoreRef": map[string]any{
				"name": "inline-store",
				"kind": "ClusterSecretStore",
			},
			"remoteRef": map[string]any{"key": "secret/val"},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	esApp := bundle.Applications[0]
	objs, err := esApp.Config.Generate(esApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	es, ok := (*objs[0]).(*esv1.ExternalSecret)
	if !ok {
		t.Fatal("expected *esv1.ExternalSecret")
	}
	if es.Spec.SecretStoreRef.Name != "inline-store" {
		t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "inline-store")
	}
}

// Neither inline nor capability → error with clear message.
func TestExternalSecretHandler_NoStoreRef_Error(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("myapp", "default", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"remoteRef":  map[string]any{"key": "secret/val"},
		},
	}
	if err := h.Apply(trait, app, bundle); err == nil {
		t.Fatal("expected error when no secretStoreRef or provider")
	}
}

// Capability-only path: secretStoreRef comes exclusively from the ClusterProfile capability
// rendering and is merged into trait properties before Apply is called.
func TestExternalSecretHandler_CapabilityOnly(t *testing.T) {
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"webservice": &components.WebserviceHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("external-secret", &traits.ExternalSecretHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name: "api",
				Type: "webservice",
				Properties: map[string]any{
					"image": "myimage:v1.0.0",
				},
				Traits: []oam.Trait{{
					Type: "external-secret",
					Properties: map[string]any{
						"secretName": "db-secret",
						"remoteRef":  map[string]any{"key": "secret/db"},
						// No secretStoreRef here — comes exclusively from capability
					},
				}},
			}},
		},
	}

	ctx := oam.TransformContext{
		Capabilities: map[string]oam.CapabilityBinding{
			"external-secret": {Rendering: map[string]any{
				"secretStoreRef": map[string]any{
					"name": "cap-store",
					"kind": "ClusterSecretStore",
				},
			}},
		},
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	// Walk leaf bundle to find the external-secret app and generate its ExternalSecret.
	found := false
	var collectApps func(node *stack.Node)
	collectApps = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil && !node.Bundle.IsUmbrella() {
			for _, bundleApp := range node.Bundle.Applications {
				if !strings.Contains(bundleApp.Name, "-external-secret-db-secret") {
					continue
				}
				objs, err := bundleApp.Config.Generate(bundleApp)
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, op := range objs {
					es, ok := (*op).(*esv1.ExternalSecret)
					if !ok {
						continue
					}
					found = true
					if es.Spec.SecretStoreRef.Name != "cap-store" {
						t.Errorf("SecretStoreRef.Name = %q, want %q", es.Spec.SecretStoreRef.Name, "cap-store")
					}
				}
			}
		}
		for _, child := range node.Children {
			collectApps(child)
		}
	}
	collectApps(cluster.Node)

	if !found {
		t.Error("no ExternalSecret found in cluster")
	}
}

// Inline secretStoreRef overrides the capability rendering (inline wins).
func TestExternalSecretHandler_InlineOverridesCapability_Real(t *testing.T) {
	transformer := oam.NewTransformer(
		map[string]oam.ComponentHandler{
			"webservice": &components.WebserviceHandler{},
		},
		nil,
	)
	transformer.RegisterBuiltinTrait("external-secret", &traits.ExternalSecretHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "api",
				Type:       "webservice",
				Properties: map[string]any{"image": "myimage:v1.0.0"},
				Traits: []oam.Trait{{
					Type: "external-secret",
					Properties: map[string]any{
						"secretName": "db-secret",
						"remoteRef":  map[string]any{"key": "secret/db"},
						// Inline secretStoreRef — should win over capability
						"secretStoreRef": map[string]any{
							"name": "inline-store",
							"kind": "ClusterSecretStore",
						},
					},
				}},
			}},
		},
	}

	ctx := oam.TransformContext{
		Capabilities: map[string]oam.CapabilityBinding{
			"external-secret": {Rendering: map[string]any{
				"secretStoreRef": map[string]any{
					"name": "cap-store",
					"kind": "ClusterSecretStore",
				},
			}},
		},
	}

	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	found := false
	var collectApps func(node *stack.Node)
	collectApps = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil && !node.Bundle.IsUmbrella() {
			for _, bundleApp := range node.Bundle.Applications {
				if !strings.Contains(bundleApp.Name, "-external-secret-db-secret") {
					continue
				}
				objs, err := bundleApp.Config.Generate(bundleApp)
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, op := range objs {
					es, ok := (*op).(*esv1.ExternalSecret)
					if !ok {
						continue
					}
					found = true
					if es.Spec.SecretStoreRef.Name != "inline-store" {
						t.Errorf("SecretStoreRef.Name = %q, want %q (inline should override capability)", es.Spec.SecretStoreRef.Name, "inline-store")
					}
				}
			}
		}
		for _, child := range node.Children {
			collectApps(child)
		}
	}
	collectApps(cluster.Node)

	if !found {
		t.Error("no ExternalSecret found in cluster")
	}
}

// Two traits with provider: shorthand → two ExternalSecret CRs with different stores.
// Uses h.Apply() directly since provider: shorthand is self-contained (no capability needed).
func TestExternalSecretHandler_MultiProvider(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("api", "default", nil)
	bundle := &stack.Bundle{}

	traits_ := []*oam.Trait{
		{
			Type: "external-secret",
			Properties: map[string]any{
				"secretName": "vault-secret",
				"provider":   "vault-backend",
				"remoteRef":  map[string]any{"key": "secret/vault"},
			},
		},
		{
			Type: "external-secret",
			Properties: map[string]any{
				"secretName": "aws-secret",
				"provider":   "aws-secretsmanager",
				"remoteRef":  map[string]any{"key": "secret/aws"},
			},
		},
	}

	for _, trait := range traits_ {
		if err := h.Apply(trait, app, bundle); err != nil {
			t.Fatalf("Apply(%s): %v", trait.Properties["secretName"], err)
		}
	}

	if len(bundle.Applications) != 2 {
		t.Fatalf("expected 2 bundle applications, got %d", len(bundle.Applications))
	}

	storeNames := map[string]string{} // secretName → SecretStoreRef.Name
	for _, bundleApp := range bundle.Applications {
		objs, err := bundleApp.Config.Generate(bundleApp)
		if err != nil {
			t.Fatalf("Generate %s: %v", bundleApp.Name, err)
		}
		for _, op := range objs {
			es, ok := (*op).(*esv1.ExternalSecret)
			if !ok {
				continue
			}
			storeNames[es.Name] = es.Spec.SecretStoreRef.Name
		}
	}

	wantStores := map[string]string{
		"vault-secret": "vault-backend",
		"aws-secret":   "aws-secretsmanager",
	}
	for secretName, wantStore := range wantStores {
		if got, ok := storeNames[secretName]; !ok {
			t.Errorf("ExternalSecret %q not found in cluster", secretName)
		} else if got != wantStore {
			t.Errorf("ExternalSecret %q SecretStoreRef.Name = %q, want %q", secretName, got, wantStore)
		}
	}
}

// --- ExternalSecretDecorator: envFrom / mountPath workload injection ---

func TestExternalSecret_EnvFrom_AddsSecretRefToDeployment(t *testing.T) {
	app := stack.NewApplication("worker", "default", newWorkerStubConfig(t))
	tr := &oam.Trait{Type: "external-secret", Properties: map[string]any{
		"secretName": "worker-creds",
		"provider":   "vault-backend",
		"envFrom":    true,
		"data": []any{
			map[string]any{"secretKey": "DB_PASSWORD"},
		},
	}}
	if err := (&traits.ExternalSecretHandler{}).Apply(tr, app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dep := findDeployment(t, objs)
	ef := dep.Spec.Template.Spec.Containers[0].EnvFrom
	if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "worker-creds" {
		t.Fatalf("EnvFrom = %+v, want one secretRef named worker-creds", ef)
	}
}

// TestExternalSecretDecorator_WorkloadKinds_Matrix covers all four supported
// workload kinds (Deployment, StatefulSet, DaemonSet, CronJob) crossed with the
// three consumption modes (envFrom only, mountPath only, both), asserting the
// exact injected objects on the PodSpec each kind exposes.
func TestExternalSecretDecorator_WorkloadKinds_Matrix(t *testing.T) {
	kinds := []struct {
		name  string
		inner stack.ApplicationConfig
	}{
		{"Deployment", newWorkerStubConfig(t)},
		{"StatefulSet", &stubStatefulSetConfig{}},
		{"DaemonSet", &stubDaemonSetConfig{}},
		{"CronJob", newCronJobStubConfig(t)},
	}
	modes := []struct {
		name      string
		envFrom   bool
		mountPath string
	}{
		{"envFromOnly", true, ""},
		{"mountPathOnly", false, "/etc/secret"},
		{"both", true, "/etc/secret"},
	}

	for _, kind := range kinds {
		for _, mode := range modes {
			t.Run(kind.name+"_"+mode.name, func(t *testing.T) {
				dec := traits.NewExternalSecretDecorator(kind.inner, "my-secret", mode.mountPath, mode.envFrom)
				objs, err := dec.Generate(newApp("app", "default"))
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				podSpec := podSpecOf(t, objs)

				if mode.envFrom {
					ef := podSpec.Containers[0].EnvFrom
					if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "my-secret" {
						t.Errorf("EnvFrom = %+v, want one secretRef named my-secret", ef)
					}
				} else if len(podSpec.Containers[0].EnvFrom) != 0 {
					t.Errorf("unexpected EnvFrom: %+v", podSpec.Containers[0].EnvFrom)
				}

				if mode.mountPath != "" {
					foundVol := false
					for _, v := range podSpec.Volumes {
						if v.Name == "my-secret" && v.Secret != nil && v.Secret.SecretName == "my-secret" {
							foundVol = true
						}
					}
					if !foundVol {
						t.Errorf("expected volume %q, got %+v", "my-secret", podSpec.Volumes)
					}
					foundMount := false
					for _, vm := range podSpec.Containers[0].VolumeMounts {
						if vm.Name == "my-secret" && vm.MountPath == mode.mountPath {
							foundMount = true
						}
					}
					if !foundMount {
						t.Errorf("expected volumeMount at %q, got %+v", mode.mountPath, podSpec.Containers[0].VolumeMounts)
					}
				} else if len(podSpec.Volumes) != 0 {
					t.Errorf("unexpected volumes: %+v", podSpec.Volumes)
				}
			})
		}
	}
}

// neither key set: config must NOT be wrapped, so output is byte-identical
// (same pointer, no decorator introduced).
func TestExternalSecret_NoConsumption_LeavesConfigUnwrapped(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	app := stack.NewApplication("worker", "default", newWorkerStubConfig(t))
	originalConfig := app.Config
	trait := &oam.Trait{Type: "external-secret", Properties: map[string]any{
		"secretName": "worker-creds",
		"provider":   "vault-backend",
		"remoteRef":  map[string]any{"key": "secret/creds"},
	}}
	if err := h.Apply(trait, app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if app.Config != originalConfig {
		t.Error("expected app.Config unchanged when neither envFrom nor mountPath is set")
	}
}

// targetSecretName override: the workload must reference the override, not
// secretName.
func TestExternalSecret_TargetSecretNameOverride_UsedInEnvFrom(t *testing.T) {
	app := stack.NewApplication("worker", "default", newWorkerStubConfig(t))
	tr := &oam.Trait{Type: "external-secret", Properties: map[string]any{
		"secretName":       "worker-creds",
		"targetSecretName": "worker-creds-target",
		"provider":         "vault-backend",
		"envFrom":          true,
		"data": []any{
			map[string]any{"secretKey": "DB_PASSWORD"},
		},
	}}
	if err := (&traits.ExternalSecretHandler{}).Apply(tr, app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := app.Config.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dep := findDeployment(t, objs)
	ef := dep.Spec.Template.Spec.Containers[0].EnvFrom
	if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "worker-creds-target" {
		t.Fatalf("EnvFrom = %+v, want one secretRef named worker-creds-target", ef)
	}
}

// zero-container workload: envFrom is a no-op, no panic.
func TestExternalSecret_ZeroContainers_DoesNotPanic(t *testing.T) {
	dec := traits.NewExternalSecretDecorator(&stubZeroContainerConfig{}, "my-secret", "", true)
	objs, err := dec.Generate(newApp("zero", "default"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dep := findDeployment(t, objs)
	if len(dep.Spec.Template.Spec.Containers) != 0 {
		t.Fatalf("expected zero containers, got %d", len(dep.Spec.Template.Spec.Containers))
	}
}

// volume name already taken: clear error, not a duplicate volume.
func TestExternalSecret_MountPath_VolumeNameCollision_Errors(t *testing.T) {
	dec := traits.NewExternalSecretDecorator(&stubVolumeCollisionConfig{volumeName: "my-secret"}, "my-secret", "/etc/secret", false)
	_, err := dec.Generate(newApp("app", "default"))
	if err == nil {
		t.Fatal("expected error for volume name collision")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// unsupported component (passthrough): error naming the supported kinds.
func TestExternalSecret_UnsupportedComponent_ReturnsError(t *testing.T) {
	dec := traits.NewExternalSecretDecorator(&stubUnsupportedConfig{}, "my-secret", "/etc/secret", true)
	_, err := dec.Generate(newApp("svc", "default"))
	if err == nil {
		t.Fatal("expected error for unsupported workload type")
	}
	if !strings.Contains(err.Error(), "Deployment, StatefulSet, DaemonSet, or CronJob") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// envFrom + the top-level remoteRef shorthand must be rejected: the shorthand
// derives its secretKey from secretName, which is a Secret name, not an
// environment variable name.
func TestExternalSecret_EnvFromWithShorthand_Rejected(t *testing.T) {
	app := stack.NewApplication("worker", "default", newWorkerStubConfig(t))
	tr := &oam.Trait{Type: "external-secret", Properties: map[string]any{
		"secretName": "my-worker-credentials",
		"provider":   "vault-backend",
		"envFrom":    true,
		"remoteRef":  map[string]any{"key": "prod/my-worker/credentials"},
	}}
	err := (&traits.ExternalSecretHandler{}).Apply(tr, app, &stack.Bundle{})
	if err == nil {
		t.Fatal("expected an error for envFrom + remoteRef shorthand")
	}
	if !strings.Contains(err.Error(), "remoteRef") || !strings.Contains(err.Error(), "envFrom") {
		t.Errorf("error %q should name both envFrom and remoteRef", err)
	}
}

func TestExternalSecret_EnvFromValidation(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string // substring; empty means no error
	}{
		{
			name: "valid authored keys",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend", "envFrom": true,
				"data": []any{map[string]any{"secretKey": "DB_PASSWORD"}}},
		},
		{
			name: "dataFrom only is allowed",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend", "envFrom": true,
				"dataFrom": []any{map[string]any{"extract": map[string]any{"key": "prod/creds"}}}},
		},
		{
			name: "invalid secretKey rejected",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend", "envFrom": true,
				"data": []any{map[string]any{"secretKey": "1BAD"}}},
			wantErr: "data[0].secretKey",
		},
		{
			name: "envFrom false skips validation",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend",
				"data": []any{map[string]any{"secretKey": "1BAD"}}},
		},
		{
			name: "invalid template data key rejected",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend", "envFrom": true,
				"target": map[string]any{"template": map[string]any{
					"data": map[string]any{"1BAD": "value"},
				}}},
			wantErr: "target.template.data",
		},
		{
			name: "mountPath with dotted target rejected",
			props: map[string]any{"secretName": "creds", "provider": "vault-backend",
				"targetSecretName": "creds.prod", "mountPath": "/etc/creds"},
			wantErr: "targetSecretName",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := stack.NewApplication("worker", "default", newWorkerStubConfig(t))
			err := (&traits.ExternalSecretHandler{}).Apply(
				&oam.Trait{Type: "external-secret", Properties: tt.props}, app, &stack.Bundle{})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// nesting: external-secret then configmap, and configmap then external-secret —
// both orders produce both injections and keep ServicePort/BackendServiceName
// forwarding through the decorator chain.
func TestExternalSecret_NestedWithConfigMap_BothOrders(t *testing.T) {
	newBase := func() *stubWorkerWithService {
		return &stubWorkerWithService{port: 8080, serviceName: "custom-svc"}
	}

	cases := []struct {
		name string
		dec  stack.ApplicationConfig
	}{
		{
			name: "external-secret-then-configmap",
			dec: traits.NewConfigMapDecorator(
				traits.NewExternalSecretDecorator(newBase(), "creds", "/etc/creds", true),
				"app-config", "/etc/config"),
		},
		{
			name: "configmap-then-external-secret",
			dec: traits.NewExternalSecretDecorator(
				traits.NewConfigMapDecorator(newBase(), "app-config", "/etc/config"),
				"creds", "/etc/creds", true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := tc.dec.Generate(newApp("app", "default"))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			dep := findDeployment(t, objs)

			ef := dep.Spec.Template.Spec.Containers[0].EnvFrom
			if len(ef) != 1 || ef[0].SecretRef == nil || ef[0].SecretRef.Name != "creds" {
				t.Errorf("EnvFrom = %+v, want one secretRef named creds", ef)
			}

			var gotConfigVol, gotSecretVol bool
			for _, v := range dep.Spec.Template.Spec.Volumes {
				if v.Name == "app-config" {
					gotConfigVol = true
				}
				if v.Name == "creds" {
					gotSecretVol = true
				}
			}
			if !gotConfigVol || !gotSecretVol {
				t.Errorf("expected both app-config and creds volumes, got %+v", dep.Spec.Template.Spec.Volumes)
			}

			pp, ok := tc.dec.(interface{ ServicePort() int32 })
			if !ok || pp.ServicePort() != 8080 {
				t.Errorf("ServicePort not forwarded through decorator chain")
			}
			bn, ok := tc.dec.(interface{ BackendServiceName() string })
			if !ok || bn.BackendServiceName() != "custom-svc" {
				t.Errorf("BackendServiceName not forwarded through decorator chain")
			}
		})
	}
}
