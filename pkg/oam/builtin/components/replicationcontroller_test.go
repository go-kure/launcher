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

func TestReplicationControllerHandler_CanHandle(t *testing.T) {
	h := &components.ReplicationControllerHandler{}
	if !h.CanHandle("replicationcontroller") {
		t.Error("CanHandle(replicationcontroller) = false")
	}
	if h.CanHandle("replicaset") {
		t.Error("CanHandle(replicaset) = true")
	}
}

// rcRich is a ReplicationController spec that sets every top-level field,
// template metadata beside the labels, and a pod spec with lists of objects, a
// map, pointers, a quantity written as a number and a probe with authored
// non-zero timings.
const rcRich = `replicas: 2
minReadySeconds: 5
selector:
  tier: web
template:
  metadata:
    labels:
      tier: web
      track: stable
    annotations:
      example.com/scrape: "true"
  spec:
    terminationGracePeriodSeconds: 0
    serviceAccountName: web
    automountServiceAccountToken: false
    nodeSelector:
      disktype: ssd
    securityContext:
      runAsNonRoot: true
    volumes:
      - name: scratch
        emptyDir: {}
    initContainers:
      - name: init
        image: registry.example/team/init:1.0.0
    containers:
      - name: app
        image: registry.example/team/app:1.2.3
        ports:
          - name: http
            containerPort: 8080
        resources:
          limits:
            cpu: 1
            memory: 512Mi
        volumeMounts:
          - name: scratch
            mountPath: /scratch
        readinessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 0
          periodSeconds: 5
          timeoutSeconds: 2
          successThreshold: 1
          failureThreshold: 4
`

// rcPlain is the smallest replicationcontroller component with a selector: a
// template whose labels the selector matches, running the given pod spec.
func rcPlain(podSpec string) string {
	return "selector:\n  tier: web\ntemplate:\n  metadata:\n    labels:\n      tier: web\n  spec:\n" + htIndent(podSpec, "    ")
}

// rcGenerate generates a replicationcontroller component under the given
// policies and returns the ReplicationController.
func rcGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *corev1.ReplicationController {
	t.Helper()
	return generateCoreKindUnder(t, &components.ReplicationControllerHandler{}, "replicationcontroller", name, props, policies...).(*corev1.ReplicationController)
}

// TestReplicationControllerHandler_EmitsAuthoredSpec: the controller is named
// after the component in the build namespace, unlabelled, and its spec is the
// authored one, field for field, with one thing added: the `app` label on the
// pod template, beside the authored labels.
func TestReplicationControllerHandler_EmitsAuthoredSpec(t *testing.T) {
	var want corev1.ReplicationControllerSpec
	if err := yaml.UnmarshalStrict([]byte(rcRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "web"
	rc := rcGenerate(t, "web", ptObject(t, rcRich), ptStrictPolicy(), nil)
	if rc.APIVersion != "v1" || rc.Kind != "ReplicationController" {
		t.Errorf("GVK = %s %s, want v1 ReplicationController", rc.APIVersion, rc.Kind)
	}
	if rc.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", rc.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(rc.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", rc.Spec, want)
	}
	if q := rc.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; q.String() != "1" {
		t.Errorf("cpu limit = %s, want 1", q.String())
	}
}

// TestReplicationControllerHandler_AppLabel: the pod template carries the
// `app` label every workload kind gives its pods, with the component's label
// value: the name itself up to 63 characters, its projection beyond. The
// authored labels stay, and so does the selector, an unset one included: the
// API server then defaults it to the template's labels.
func TestReplicationControllerHandler_AppLabel(t *testing.T) {
	for _, name := range []string{"web", "web-" + strings.Repeat("a", 63)} {
		rc := rcGenerate(t, name, ptObject(t, rcPlain(htPlainPod)), nil)
		want := map[string]string{"tier": "web", "app": oam.ComponentLabelValue(name)}
		if !maps.Equal(rc.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, rc.Spec.Template.Labels, want)
		}
		if want := map[string]string{"tier": "web"}; !maps.Equal(rc.Spec.Selector, want) {
			t.Errorf("%s: selector = %v, want the authored %v", name, rc.Spec.Selector, want)
		}
	}

	noSelector := ptObject(t, "template:\n  spec:\n"+htIndent(htPlainPod, "    "))
	rc := rcGenerate(t, "web", noSelector, nil)
	if rc.Spec.Selector != nil {
		t.Errorf("selector = %v, want it unset as authored", rc.Spec.Selector)
	}
	if want := map[string]string{"app": "web"}; !maps.Equal(rc.Spec.Template.Labels, want) {
		t.Errorf("template labels = %v, want %v", rc.Spec.Template.Labels, want)
	}
}

// rcLabelled is a replicationcontroller component whose template carries the
// given labels under the given selector.
func rcLabelled(selector, labels map[string]any) map[string]any {
	metadata := map[string]any{}
	if labels != nil {
		metadata["labels"] = labels
	}
	props := map[string]any{
		"template": map[string]any{
			"metadata": metadata,
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"},
			}},
		},
	}
	if selector != nil {
		props["selector"] = selector
	}
	return props
}

// TestReplicationControllerHandler_AuthoredAppLabel: an authored template
// label `app` is kept when it is the component's own label value and refused,
// naming the component, when it is another. A selector on the `app` label's
// own value is satisfied by the label launcher adds; one that matches no
// template label is the API server's to refuse.
func TestReplicationControllerHandler_AuthoredAppLabel(t *testing.T) {
	long := "web-" + strings.Repeat("a", 63)
	for _, name := range []string{"web", long} {
		value := oam.ComponentLabelValue(name)
		rc := rcGenerate(t, name, rcLabelled(map[string]any{"app": value}, map[string]any{"app": value}), nil)
		if want := map[string]string{"app": value}; !maps.Equal(rc.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, rc.Spec.Template.Labels, want)
		}
	}

	for name, tc := range map[string]struct {
		component string
		selector  map[string]any
		labels    map[string]any
		want      string // "" when the component builds
	}{
		"another value": {"web", map[string]any{"tier": "web"}, map[string]any{"tier": "web", "app": "frontend"},
			"template.metadata.labels.app: \"frontend\" is not the `app` label of component \"web\" (\"web\")"},
		// The raw name of a long component is not its label value.
		"the raw long name": {long, nil, map[string]any{"app": long},
			"is not the `app` label of component \"" + long + "\" (\"" + oam.ComponentLabelValue(long) + "\")"},
		"selector on the label value":       {"web", map[string]any{"app": "web"}, nil, ""},
		"selector matching no label":        {"web", map[string]any{"tier": "db"}, map[string]any{"tier": "web"}, ""},
		"selector on another app, no label": {"web", map[string]any{"app": "frontend"}, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.ReplicationControllerHandler{}, "replicationcontroller", tc.component, rcLabelled(tc.selector, tc.labels))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicationControllerHandler_FillsNoPolicyDefault: a policy that carries
// replica and resource defaults fills none of them.
func TestReplicationControllerHandler_FillsNoPolicyDefault(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	rc := rcGenerate(t, "web", ptObject(t, rcPlain(htPlainPod)), defaults)
	if rc.Spec.Replicas != nil {
		t.Errorf("replicas = %d, want it unset as authored", *rc.Spec.Replicas)
	}
	if res := rc.Spec.Template.Spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the replicationcontroller kind fills no policy default", res)
	}
}

// TestReplicationControllerHandler_Refusals: what the component refuses when
// it is read, with no policy involved: a property that is not a
// ReplicationControllerSpec field, at any depth; an unauthored template; what
// the pod kind refuses of a pod spec, by its path under the template; and
// activeDeadlineSeconds.
func TestReplicationControllerHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 ReplicationControllerSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	// with returns a component whose pod spec is the plain one plus extra.
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"selector": map[string]any{"tier": "web"},
			"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "web"}}, "spec": spec},
		}
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
		"no properties":           {nil, "template: required"},
		"no template":             {map[string]any{"selector": map[string]any{"tier": "web"}}, "template: required"},
		"null template":           {top(map[string]any{"template": nil}), "template: required"},
		"an empty template":       {top(map[string]any{"template": map[string]any{}}), "template.spec.containers: required"},
		"a workload's image":      {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"a label selector object": {top(map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}}}), notASpec},
		"replicas a string":       {top(map[string]any{"replicas": "two"}), notASpec},
		"template sub-key":        {with(map[string]any{"containerz": []any{}}), notASpec},
		"two spellings":           {top(map[string]any{"replicas": 1, "Replicas": 2}), "sets the same field as"},
		"active deadline": {with(map[string]any{"activeDeadlineSeconds": 600}),
			"template.spec.activeDeadlineSeconds: not supported"},
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
			err := coreKindErr(&components.ReplicationControllerHandler{}, "replicationcontroller", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicationControllerHandler_ZerosTheTypeCarries: replicas and
// minReadySeconds of 0 are carried: replicas is a pointer, and
// minReadySeconds defaults to 0.
func TestReplicationControllerHandler_ZerosTheTypeCarries(t *testing.T) {
	props := ptObject(t, rcPlain(htPlainPod))
	props["replicas"] = 0
	props["minReadySeconds"] = 0
	rc := rcGenerate(t, "web", props, ptStrictPolicy())
	if rc.Spec.Replicas == nil || *rc.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want an authored 0", rc.Spec.Replicas)
	}
}

// TestReplicationControllerConfig_GenerateRepeatsTheRefusals: the config is
// exported, so a spec built in code is held to the refusals the typed spec can
// show before the controller is emitted.
func TestReplicationControllerConfig_GenerateRepeatsTheRefusals(t *testing.T) {
	app := corev1.Container{Name: "app", Image: "registry.example/team/app:1.2.3"}
	deadline, priority := int64(600), int32(10)
	template := func(labels map[string]string, spec corev1.PodSpec) *corev1.PodTemplateSpec {
		return &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: spec}
	}
	tier := map[string]string{"tier": "web"}
	for name, tc := range map[string]struct {
		spec corev1.ReplicationControllerSpec
		want string
	}{
		"no template":   {corev1.ReplicationControllerSpec{Selector: tier}, "template: required"},
		"no containers": {corev1.ReplicationControllerSpec{Template: template(tier, corev1.PodSpec{})}, "template.spec.containers: required"},
		"active deadline": {corev1.ReplicationControllerSpec{Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, ActiveDeadlineSeconds: &deadline})}, "template.spec.activeDeadlineSeconds: not supported"},
		"priority": {corev1.ReplicationControllerSpec{Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, Priority: &priority})}, "template.spec.priority: not authorable"},
		"overhead": {corev1.ReplicationControllerSpec{Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, Overhead: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}})}, "template.spec.overhead: not authorable"},
		"untagged image": {corev1.ReplicationControllerSpec{Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}})}, `template.spec.containers[0] "app": image "nginx" rejected`},
		"another app label": {corev1.ReplicationControllerSpec{Template: template(map[string]string{"tier": "web", "app": "frontend"},
			corev1.PodSpec{Containers: []corev1.Container{app}})}, "template.metadata.labels.app: \"frontend\" is not the `app` label of component \"web\""},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &components.ReplicationControllerConfig{Name: "web", Namespace: coreKindNamespace, Spec: tc.spec}
			_, err := cfg.Generate(stack.NewApplication("web", coreKindNamespace, cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicationControllerConfig_GenerateLeavesTheConfigAlone: generating
// does not write the `app` label into the config's own template, which the
// spec holds by pointer, so a second Generate under another application name
// labels the pods for that name.
func TestReplicationControllerConfig_GenerateLeavesTheConfigAlone(t *testing.T) {
	cfg := &components.ReplicationControllerConfig{Spec: corev1.ReplicationControllerSpec{
		Template: &corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"tier": "web"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/team/app:1.2.3"}}},
		},
	}}
	for _, name := range []string{"web", "api"} {
		objs, err := cfg.Generate(stack.NewApplication(name, coreKindNamespace, cfg))
		if err != nil {
			t.Fatalf("%s: Generate: %v", name, err)
		}
		rc := (*objs[0]).(*corev1.ReplicationController)
		if rc.Spec.Template == cfg.Spec.Template {
			t.Fatalf("%s: the emitted controller shares the config's template", name)
		}
		if want := map[string]string{"tier": "web", "app": name}; !maps.Equal(rc.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, rc.Spec.Template.Labels, want)
		}
	}
	if want := map[string]string{"tier": "web"}; !maps.Equal(cfg.Spec.Template.Labels, want) {
		t.Errorf("the config's template labels = %v after Generate, want the authored %v", cfg.Spec.Template.Labels, want)
	}
}

// TestReplicationControllerConfig_ServiceAccountName: the controller reports
// the account its pods run as to the traits that bind identity to it: the
// template's serviceAccountName, the deprecated serviceAccount where that one
// is unset, and "" for the namespace's default account. It always runs pods.
func TestReplicationControllerConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		podSpec string
		want    string
	}{
		"none authored":      {htPlainPod, ""},
		"serviceAccountName": {htPlainPod + "serviceAccountName: web\n", "web"},
		"deprecated alias":   {htPlainPod + "serviceAccount: legacy\n", "legacy"},
		"both":               {htPlainPod + "serviceAccountName: web\nserviceAccount: legacy\n", "web"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (&components.ReplicationControllerHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "replicationcontroller", Properties: ptObject(t, rcPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the replicationcontroller config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
	// A config built in code without a template still answers.
	if got, runsPods := (&components.ReplicationControllerConfig{}).ServiceAccountName(); got != "" || !runsPods {
		t.Errorf("ServiceAccountName() without a template = %q, %v; want \"\", true", got, runsPods)
	}
}

// TestReplicationControllerPolicy_Replicas: replicas is held to the policy's
// replica maximum, an unset one as the 1 the API server defaults it to, and
// the violation names the component.
func TestReplicationControllerPolicy_Replicas(t *testing.T) {
	zero := ptStrictPolicy()
	zero.maxReplicas = int32ptr(0)
	for name, tc := range map[string]struct {
		replicas any // nil leaves it unset
		policy   *stubPolicy
		want     string // "" when the component builds
	}{
		"over the maximum":             {4, ptStrictPolicy(), "replicas 4 exceeds enforced maximum 3"},
		"at the maximum":               {3, ptStrictPolicy(), ""},
		"unset under a maximum of 0":   {nil, zero, "replicas 1 exceeds enforced maximum 0"},
		"zero under a maximum of 0":    {0, zero, ""},
		"over it with no maximum set":  {50, &stubPolicy{}, ""},
		"over it with no policy given": {50, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			props := ptObject(t, rcPlain(htPlainPod))
			if tc.replicas != nil {
				props["replicas"] = tc.replicas
			}
			objs, err := pvTransform("replicationcontroller", &components.ReplicationControllerHandler{}, props, pvPolicy(tc.policy))
			if tc.want != "" {
				htWantViolation(t, err, `component "web": `+tc.want)
				return
			}
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "ReplicationController" {
				t.Fatalf("generated %v, want the one ReplicationController", objs)
			}
		})
	}
}

// TestReplicationControllerConfig_ApplyPolicy_NilAndNoTemplate: a nil policy
// checks nothing, as on every other config, and a config built in code
// without a template has no pod spec to check.
func TestReplicationControllerConfig_ApplyPolicy_NilAndNoTemplate(t *testing.T) {
	cfg := &components.ReplicationControllerConfig{Name: "web", Namespace: coreKindNamespace, Spec: corev1.ReplicationControllerSpec{
		Replicas: int32ptr(50),
		Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			HostNetwork: true,
			Containers:  []corev1.Container{{Name: "app", Image: "other.example/team/app:1.2.3"}},
		}},
	}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
	bare := &components.ReplicationControllerConfig{Name: "web", Namespace: coreKindNamespace}
	if err := bare.ApplyPolicy(ptStrictPolicy()); err != nil {
		t.Errorf("ApplyPolicy without a template = %v, want nil", err)
	}
}
