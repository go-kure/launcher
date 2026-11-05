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

// prSleepyDeployment is a rendered Deployment whose container's preStop sleeps
// longer than the default grace period.
const prSleepyDeployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n    spec:\n" +
	"      containers:\n        - name: app\n          image: registry.example/team/app:1.2.3\n          lifecycle:\n            preStop:\n              sleep:\n                seconds: 31\n"

// TestHelmTemplate_PodRuleRefusalIsNotCached: a render whose sleep breaks the
// rule is refused and nothing is kept of it, so the next call renders again and
// is refused again.
func TestHelmTemplate_PodRuleRefusalIsNotCached(t *testing.T) {
	calls := 0
	cfg := helmTemplateFixture(t, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		calls++
		return []byte(prSleepyDeployment), nil
	})
	for i := 1; i <= 2; i++ {
		_, err := cfg.Generate(nil)
		if err == nil || !strings.Contains(err.Error(), `helmtemplate "myapp": rendered Deployment "default/web": spec.template.spec.containers[0] "app": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`) {
			t.Fatalf("Generate %d: err = %v, want the sleep refused", i, err)
		}
		if cfg.rendered || cfg.hookGroups != nil {
			t.Errorf("Generate %d: rendered %v, hookGroups %v; want nothing kept of a refused render", i, cfg.rendered, cfg.hookGroups)
		}
		if calls != i {
			t.Errorf("Generate %d: renderChart called %d times, want %d", i, calls, i)
		}
	}
}

// TestHelmTemplate_PodRuleRefusalWithSecretValues: with secretValues set, the
// refusal is reported as it came, not routed through secretRenderFailure, which
// withholds the cause of a failed render.
func TestHelmTemplate_PodRuleRefusalWithSecretValues(t *testing.T) {
	cfg := secretTemplateFixture(t, nil, map[string]any{"token": "s3cret"}, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		return []byte(prSleepyDeployment), nil
	})
	_, err := cfg.Generate(nil)
	if err == nil {
		t.Fatal("Generate succeeded, want the sleep refused")
	}
	for _, want := range []string{`helmtemplate "myapp": rendered Deployment "default/web"`, `lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`} {
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

// TestValidateObjectPodRules: the object check the manifests source,
// passthrough and chart renders share reads the pod spec of a typed workload,
// and passes every other object, naming the pod spec's path in what it refuses.
func TestValidateObjectPodRules(t *testing.T) {
	sleep := func(seconds int64) *corev1.Lifecycle {
		return &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: seconds}}}
	}
	deployment := &appsv1.Deployment{}
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{Name: "app", Lifecycle: sleep(31)}}
	err := validateObjectPodRules(deployment)
	if err == nil || !strings.HasPrefix(err.Error(), `spec.template.spec.containers[0] "app": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`) {
		t.Errorf("Deployment sleep: err = %v, want the sleep refused with its path", err)
	}

	initDeployment := &appsv1.Deployment{}
	initDeployment.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", Lifecycle: sleep(31)}}
	if err := validateObjectPodRules(initDeployment); err == nil || !strings.Contains(err.Error(), `spec.template.spec.initContainers[0] "init": lifecycle: not supported on an init container that is not restartable`) {
		t.Errorf("Deployment regular init: err = %v, want the regular-init rule first", err)
	}

	// A sleep on an app container as well: the sleep rule would refuse that one
	// and does not skip it, so only the order decides, and the regular-init rule
	// comes first.
	both := &appsv1.Deployment{}
	both.Spec.Template.Spec.Containers = []corev1.Container{{Name: "app", Lifecycle: sleep(31)}}
	both.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", LivenessProbe: &corev1.Probe{}}}
	if err := validateObjectPodRules(both); err == nil || !strings.Contains(err.Error(), `spec.template.spec.initContainers[0] "init": livenessProbe: not supported on an init container that is not restartable`) {
		t.Errorf("Deployment with an over-long app sleep and a regular init probe: err = %v, want the regular-init rule first", err)
	}

	grace := int64(-1)
	pod := &corev1.Pod{}
	pod.Spec.TerminationGracePeriodSeconds = &grace
	pod.Spec.Containers = []corev1.Container{{Name: "app", Lifecycle: sleep(1)}}
	if err := validateObjectPodRules(pod); err != nil {
		t.Errorf("a bare Pod with grace -1 and a sleep of 1: err = %v, want it passed", err)
	}
	podTemplateOwner := &appsv1.Deployment{}
	podTemplateOwner.Spec.Template.Spec.TerminationGracePeriodSeconds = &grace
	podTemplateOwner.Spec.Template.Spec.Containers = []corev1.Container{{Name: "app", Lifecycle: sleep(1)}}
	if err := validateObjectPodRules(podTemplateOwner); err == nil {
		t.Error("a Deployment with grace -1 and a sleep of 1: passed, want it refused")
	}

	for name, obj := range map[string]client.Object{
		"a ReplicationController with no template": &corev1.ReplicationController{},
		"a ConfigMap": &corev1.ConfigMap{Data: map[string]string{"seconds": "31"}},
		"an unstructured workload": &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1beta2", "kind": "Deployment",
		}},
	} {
		if err := validateObjectPodRules(obj); err != nil {
			t.Errorf("%s: err = %v, want it passed", name, err)
		}
	}
}
