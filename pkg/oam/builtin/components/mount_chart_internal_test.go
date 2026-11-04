package components

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mcBadDeployment is a rendered Deployment whose container mounts a volume at
// an absolute subPath.
const mcBadDeployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n    spec:\n" +
	"      containers:\n        - name: app\n          image: registry.example/team/app:1.2.3\n          volumeMounts:\n            - name: data\n              mountPath: /data\n              subPath: /abs-token\n" +
	"      volumes:\n        - name: data\n          emptyDir: {}\n"

// TestHelmTemplate_RefusedRenderIsNotCached: a render whose mount breaks the
// rule is refused and nothing is kept of it, as for a scope override the
// objects contradict, so the next call renders again and is refused again.
func TestHelmTemplate_RefusedRenderIsNotCached(t *testing.T) {
	calls := 0
	cfg := helmTemplateFixture(t, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		calls++
		return []byte(mcBadDeployment), nil
	})
	for i := 1; i <= 2; i++ {
		_, err := cfg.Generate(nil)
		if err == nil || !strings.Contains(err.Error(), `helmtemplate "myapp": rendered Deployment "default/web": spec.template.spec.containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs-token"`) {
			t.Fatalf("Generate %d: err = %v, want the mount refused", i, err)
		}
		if cfg.rendered || cfg.hookGroups != nil {
			t.Errorf("Generate %d: rendered %v, hookGroups %v; want nothing kept of a refused render", i, cfg.rendered, cfg.hookGroups)
		}
		if calls != i {
			t.Errorf("Generate %d: renderChart called %d times, want %d", i, calls, i)
		}
	}
}

// TestHelmTemplate_MountRefusalWithSecretValues: with secretValues set, the
// mount refusal is reported as it came, the value quoted as the policy path and
// the scope-override refusal quote theirs, and not routed through
// secretRenderFailure, which withholds the cause of a failed render.
func TestHelmTemplate_MountRefusalWithSecretValues(t *testing.T) {
	cfg := secretTemplateFixture(t, nil, map[string]any{"token": "s3cret"}, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		return []byte(mcBadDeployment), nil
	})
	_, err := cfg.Generate(nil)
	if err == nil {
		t.Fatal("Generate succeeded, want the mount refused")
	}
	for _, want := range []string{`helmtemplate "myapp": rendered Deployment "default/web"`, `volumeMounts[0].subPath: must be a relative path, got "/abs-token"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one holding %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "withheld") {
		t.Errorf("err = %v, want it reported as it came, not through secretRenderFailure", err)
	}
	if cfg.rendered {
		t.Error("a refused render was kept")
	}
}

// TestValidateObjectMounts: the object check the manifests source, passthrough
// and chart renders share reads the pod spec of a typed workload, and passes
// every other object, naming the pod spec's path in what it refuses.
func TestValidateObjectMounts(t *testing.T) {
	bad := func() []corev1.Container {
		return []corev1.Container{{Name: "app", VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data", SubPath: "/abs"}}}}
	}
	deployment := &appsv1.Deployment{}
	deployment.Spec.Template.Spec.Containers = bad()
	err := validateObjectMounts(deployment)
	if err == nil || !strings.Contains(err.Error(), `spec.template.spec.containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs"`) {
		t.Errorf("Deployment: err = %v, want the mount refused with its path", err)
	}

	pod := &corev1.Pod{}
	pod.Spec.Containers = bad()
	if err := validateObjectMounts(pod); err == nil || !strings.Contains(err.Error(), `spec.containers[0] "app"`) {
		t.Errorf("Pod: err = %v, want the mount refused with its path", err)
	}

	for name, obj := range map[string]client.Object{
		"a ReplicationController with no template": &corev1.ReplicationController{},
		"a ConfigMap": &corev1.ConfigMap{Data: map[string]string{"subPath": "/abs"}},
		"an unstructured workload": &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1beta2", "kind": "Deployment",
		}},
	} {
		if err := validateObjectMounts(obj); err != nil {
			t.Errorf("%s: err = %v, want it passed", name, err)
		}
	}
}
