package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestPodTemplateHandler_CanHandle(t *testing.T) {
	h := &components.PodTemplateHandler{}
	if !h.CanHandle("podtemplate") {
		t.Error("CanHandle(podtemplate) = false")
	}
	if h.CanHandle("pod") {
		t.Error("CanHandle(pod) = true")
	}
}

// podTemplateRich is a template with metadata and a pod spec that has lists of
// objects, a map, pointers, a quantity written as a number, a probe with
// authored non-zero timings and an activeDeadlineSeconds, which a controller's
// template may not set.
const podTemplateRich = `metadata:
  labels:
    tier: batch
  annotations:
    example.com/scrape: "true"
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 600
  terminationGracePeriodSeconds: 0
  serviceAccountName: batch
  automountServiceAccountToken: false
  nodeSelector:
    disktype: ssd
  volumes:
    - name: scratch
      emptyDir: {}
  initContainers:
    - name: init
      image: registry.example/team/init:1.0.0
  containers:
    - name: app
      image: registry.example/team/app:1.2.3
      resources:
        limits:
          cpu: 1
          memory: 512Mi
      volumeMounts:
        - name: scratch
          mountPath: /scratch
      readinessProbe:
        exec:
          command: ["true"]
        initialDelaySeconds: 0
        periodSeconds: 5
        timeoutSeconds: 2
        successThreshold: 1
        failureThreshold: 4
`

// podTemplatePlain is the smallest podtemplate component: a template running
// the given pod spec.
func podTemplatePlain(podSpec string) string {
	return "template:\n  spec:\n" + htIndent(podSpec, "    ")
}

// TestPodTemplateHandler_EmitsAuthoredTemplate: the PodTemplate is named after
// the component in the build namespace, unlabelled, and its template is the
// authored one, field for field: no `app` label is added to it, under any
// policy.
func TestPodTemplateHandler_EmitsAuthoredTemplate(t *testing.T) {
	var want corev1.PodTemplateSpec
	if err := yaml.UnmarshalStrict([]byte(podTemplateRich), &want); err != nil {
		t.Fatalf("decoding the test template: %v", err)
	}
	props := map[string]any{"template": ptObject(t, podTemplateRich)}
	obj := generateCoreKindUnder(t, &components.PodTemplateHandler{}, "podtemplate", "batch", props, ptStrictPolicy(), nil)
	pt, ok := obj.(*corev1.PodTemplate)
	if !ok {
		t.Fatalf("object = %T, want a PodTemplate", obj)
	}
	if pt.APIVersion != "v1" || pt.Kind != "PodTemplate" {
		t.Errorf("GVK = %s %s, want v1 PodTemplate", pt.APIVersion, pt.Kind)
	}
	if pt.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", pt.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(pt.Template, want) {
		t.Errorf("template differs from the authored one:\n got %+v\nwant %+v", pt.Template, want)
	}
	if wantLabels := map[string]string{"tier": "batch"}; !maps.Equal(pt.Template.Labels, wantLabels) {
		t.Errorf("template labels = %v, want the authored %v and no `app` label", pt.Template.Labels, wantLabels)
	}

	// A template without metadata stays without labels, and an authored `app`
	// label is carried as written: nothing selects a PodTemplate by it.
	bare := generateCoreKindUnder(t, &components.PodTemplateHandler{}, "podtemplate", "batch", ptObject(t, podTemplatePlain(htPlainPod)), nil).(*corev1.PodTemplate)
	if len(bare.Template.Labels) != 0 {
		t.Errorf("template labels = %v, want none", bare.Template.Labels)
	}
	authored := map[string]any{"template": map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "frontend"}},
		"spec":     ptObject(t, htPlainPod),
	}}
	kept := generateCoreKindUnder(t, &components.PodTemplateHandler{}, "podtemplate", "batch", authored, nil).(*corev1.PodTemplate)
	if wantLabels := map[string]string{"app": "frontend"}; !maps.Equal(kept.Template.Labels, wantLabels) {
		t.Errorf("template labels = %v, want the authored %v", kept.Template.Labels, wantLabels)
	}
}

// TestPodTemplateHandler_FillsNoPolicyDefault: a policy that carries resource
// defaults fills none of them.
func TestPodTemplateHandler_FillsNoPolicyDefault(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	pt := generateCoreKindUnder(t, &components.PodTemplateHandler{}, "podtemplate", "batch", ptObject(t, podTemplatePlain(htPlainPod)), defaults).(*corev1.PodTemplate)
	if res := pt.Template.Spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the podtemplate kind fills no policy default", res)
	}
}

// TestPodTemplateHandler_Refusals: what the component refuses when it is read,
// with no policy involved: a property other than template, the object's own
// kind, apiVersion and metadata included; a key that is not a PodTemplateSpec
// field, at any depth; and what the pod kind refuses of a pod spec, by its
// path under the template.
func TestPodTemplateHandler_Refusals(t *testing.T) {
	const notATemplate = "properties do not decode into a v1 PodTemplate"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	// with returns a component whose pod spec is the plain one plus extra.
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{"template": map[string]any{"spec": spec}}
	}
	top := func(extra map[string]any) map[string]any {
		props := with(nil)
		maps.Copy(props, extra)
		return props
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":      {nil, "template.spec.containers: required"},
		"null template":      {map[string]any{"template": nil}, "template.spec.containers: required"},
		"an empty template":  {map[string]any{"template": map[string]any{}}, "template.spec.containers: required"},
		"the object's name":  {top(map[string]any{"metadata": map[string]any{"name": "other"}}), notATemplate},
		"the object's kind":  {top(map[string]any{"kind": "PodTemplate"}), notATemplate},
		"an apiVersion":      {top(map[string]any{"apiVersion": "v1"}), notATemplate},
		"a spec":             {top(map[string]any{"spec": map[string]any{"containers": []any{app}}}), notATemplate},
		"a workload's image": {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notATemplate},
		"template sub-key":   {with(map[string]any{"containerz": []any{}}), notATemplate},
		"two spellings":      {map[string]any{"template": with(nil)["template"], "Template": with(nil)["template"]}, "sets the same field as"},
		"ephemeral containers": {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}),
			"template.spec.ephemeralContainers: not supported"},
		"priority": {with(map[string]any{"priority": 1000}), "template.spec.priority: not authorable"},
		"overhead": {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), "template.spec.overhead: not authorable"},
		"untagged image": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app"}}}),
			`template.spec.containers[0] "app": image "registry.example/team/app" rejected: no tag or digest specified`},
		"latest init image": {with(map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "busybox:latest"}}}),
			`template.spec.initContainers[0] "init": image "busybox:latest" rejected: :latest tag not allowed`},
		"probe timing of zero": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3",
			"livenessProbe": map[string]any{"exec": map[string]any{"command": []any{"true"}}, "timeoutSeconds": 0}}}}),
			"template.spec.containers[0].livenessProbe.timeoutSeconds: 0 cannot be carried by the Kubernetes API types (the field is omitted when zero, so the API server would apply its default 1)"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.PodTemplateHandler{}, "podtemplate", "batch", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestPodTemplateConfig_GenerateRepeatsTheRefusals: the config is exported, so
// a template built in code is held to the refusals the typed template can show
// before the PodTemplate is emitted.
func TestPodTemplateConfig_GenerateRepeatsTheRefusals(t *testing.T) {
	app := corev1.Container{Name: "app", Image: "registry.example/team/app:1.2.3"}
	priority := int32(10)
	for name, tc := range map[string]struct {
		spec corev1.PodSpec
		want string
	}{
		"no containers":  {corev1.PodSpec{}, "template.spec.containers: required"},
		"priority":       {corev1.PodSpec{Containers: []corev1.Container{app}, Priority: &priority}, "template.spec.priority: not authorable"},
		"overhead":       {corev1.PodSpec{Containers: []corev1.Container{app}, Overhead: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}, "template.spec.overhead: not authorable"},
		"untagged image": {corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}}, `template.spec.containers[0] "app": image "nginx" rejected`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &components.PodTemplateConfig{Name: "batch", Namespace: coreKindNamespace, Template: corev1.PodTemplateSpec{Spec: tc.spec}}
			_, err := cfg.Generate(stack.NewApplication("batch", coreKindNamespace, cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestPodTemplateConfig_GenerateLeavesTheConfigAlone: the emitted PodTemplate
// holds a copy of the config's template, so a change to one is not a change to
// the other.
func TestPodTemplateConfig_GenerateLeavesTheConfigAlone(t *testing.T) {
	cfg := &components.PodTemplateConfig{Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"tier": "batch"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/team/app:1.2.3"}}},
	}}
	objs, err := cfg.Generate(stack.NewApplication("batch", coreKindNamespace, cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	pt := (*objs[0]).(*corev1.PodTemplate)
	pt.Template.Labels["tier"] = "changed"
	pt.Template.Spec.Containers[0].Image = "registry.example/team/other:1.0.0"
	if got := cfg.Template.Labels["tier"]; got != "batch" {
		t.Errorf("the config's label = %q after the object changed, want batch", got)
	}
	if got := cfg.Template.Spec.Containers[0].Image; got != "registry.example/team/app:1.2.3" {
		t.Errorf("the config's image = %q after the object changed", got)
	}
}

// TestPodTemplateConfig_RunsNoPods: a PodTemplate is stored, not run, so its
// config reports no ServiceAccount for a trait to bind, whatever the template
// names.
func TestPodTemplateConfig_RunsNoPods(t *testing.T) {
	cfg, err := (&components.PodTemplateHandler{}).ToApplicationConfig(
		&oam.Component{Name: "batch", Type: "podtemplate", Properties: ptObject(t, podTemplatePlain(htPlainPod+"serviceAccountName: batch\n"))}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if _, ok := cfg.(oam.ServiceAccountNamer); ok {
		t.Error("the podtemplate config implements oam.ServiceAccountNamer; a PodTemplate runs no pods")
	}
}

// TestPodTemplateConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as
// on every other config.
func TestPodTemplateConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.PodTemplateConfig{Name: "batch", Namespace: coreKindNamespace, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		HostNetwork: true,
		Containers:  []corev1.Container{{Name: "app", Image: "other.example/team/app:1.2.3"}},
	}}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
