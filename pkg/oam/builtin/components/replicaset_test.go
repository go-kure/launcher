package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestReplicaSetHandler_CanHandle(t *testing.T) {
	h := &components.ReplicaSetHandler{}
	if !h.CanHandle("replicaset") {
		t.Error("CanHandle(replicaset) = false")
	}
	if h.CanHandle("replicationcontroller") {
		t.Error("CanHandle(replicationcontroller) = true")
	}
}

// rsRich is a ReplicaSet spec that sets every top-level field, a selector of
// both forms, template metadata beside the labels, and a pod spec with lists of
// objects, a map, pointers, a quantity written as a number and a probe with
// authored non-zero timings.
const rsRich = `replicas: 2
minReadySeconds: 5
selector:
  matchLabels:
    tier: web
  matchExpressions:
    - key: track
      operator: In
      values: [stable, canary]
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

// rsPlain is the smallest replicaset component: a selector, and a template
// whose labels it matches, running the given pod spec.
func rsPlain(podSpec string) string {
	return "selector:\n  matchLabels:\n    tier: web\ntemplate:\n  metadata:\n    labels:\n      tier: web\n  spec:\n" + htIndent(podSpec, "    ")
}

// rsGenerate generates a replicaset component under the given policies and
// returns the ReplicaSet.
func rsGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *appsv1.ReplicaSet {
	t.Helper()
	return generateCoreKindUnder(t, &components.ReplicaSetHandler{}, "replicaset", name, props, policies...).(*appsv1.ReplicaSet)
}

// TestReplicaSetHandler_EmitsAuthoredSpec: the ReplicaSet is named after the
// component in the build namespace, unlabelled, and its spec is the authored
// one, field for field, with one thing added: the `app` label on the pod
// template, beside the authored labels.
func TestReplicaSetHandler_EmitsAuthoredSpec(t *testing.T) {
	var want appsv1.ReplicaSetSpec
	if err := yaml.UnmarshalStrict([]byte(rsRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "web"
	rs := rsGenerate(t, "web", ptObject(t, rsRich), ptStrictPolicy(), nil)
	if rs.APIVersion != "apps/v1" || rs.Kind != "ReplicaSet" {
		t.Errorf("GVK = %s %s, want apps/v1 ReplicaSet", rs.APIVersion, rs.Kind)
	}
	if rs.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", rs.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(rs.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", rs.Spec, want)
	}
	if q := rs.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; q.String() != "1" {
		t.Errorf("cpu limit = %s, want 1", q.String())
	}
}

// TestReplicaSetHandler_AppLabel: the pod template carries the `app` label
// every workload kind gives its pods, with the component's label value: the
// name itself up to 63 characters, its projection beyond. The authored labels
// stay, and so does the selector.
func TestReplicaSetHandler_AppLabel(t *testing.T) {
	for _, name := range []string{"web", "web-" + strings.Repeat("a", 63)} {
		rs := rsGenerate(t, name, ptObject(t, rsPlain(htPlainPod)), nil)
		want := map[string]string{"tier": "web", "app": oam.ComponentLabelValue(name)}
		if !maps.Equal(rs.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, rs.Spec.Template.Labels, want)
		}
		if want := map[string]string{"tier": "web"}; !maps.Equal(rs.Spec.Selector.MatchLabels, want) || len(rs.Spec.Selector.MatchExpressions) != 0 {
			t.Errorf("%s: selector = %+v, want the authored matchLabels %v", name, rs.Spec.Selector, want)
		}
	}
}

// rsLabelled is a replicaset component whose template carries the given labels
// under the given selector.
func rsLabelled(selector map[string]any, labels map[string]any) map[string]any {
	metadata := map[string]any{}
	if labels != nil {
		metadata["labels"] = labels
	}
	return map[string]any{
		"selector": selector,
		"template": map[string]any{
			"metadata": metadata,
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"},
			}},
		},
	}
}

// TestReplicaSetHandler_AuthoredAppLabel: an authored template label `app` is
// kept when it is the component's own label value and refused, naming the
// component, when it is another: traits and Services select the component's
// pods by that label, so it cannot say something else.
func TestReplicaSetHandler_AuthoredAppLabel(t *testing.T) {
	long := "web-" + strings.Repeat("a", 63)
	for _, name := range []string{"web", long} {
		value := oam.ComponentLabelValue(name)
		props := rsLabelled(map[string]any{"matchLabels": map[string]any{"app": value}}, map[string]any{"app": value})
		rs := rsGenerate(t, name, props, nil)
		if want := map[string]string{"app": value}; !maps.Equal(rs.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, rs.Spec.Template.Labels, want)
		}
	}

	for name, tc := range map[string]struct {
		component, authored, want string
	}{
		"another value": {"web", "frontend",
			"template.metadata.labels.app: \"frontend\" is not the `app` label of component \"web\" (\"web\")"},
		// The raw name of a long component is not its label value.
		"the raw long name": {long, long,
			"is not the `app` label of component \"" + long + "\" (\"" + oam.ComponentLabelValue(long) + "\")"},
		"empty": {"web", "", "template.metadata.labels.app: \"\" is not the `app` label of component \"web\""},
	} {
		t.Run(name, func(t *testing.T) {
			props := rsLabelled(map[string]any{"matchLabels": map[string]any{"tier": "web"}},
				map[string]any{"tier": "web", "app": tc.authored})
			err := coreKindErr(&components.ReplicaSetHandler{}, "replicaset", tc.component, props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicaSetHandler_SelectorAndTheAppLabel: a selector that matches the
// authored template labels and stops matching once the `app` label is on them
// is refused. A selector on the `app` label's own value is satisfied by the
// label launcher adds. A selector that does not match the authored labels
// either is the API server's to refuse, as its other value rules are. One that
// cannot be read as a selector is refused with apimachinery's reason.
func TestReplicaSetHandler_SelectorAndTheAppLabel(t *testing.T) {
	const rulesOut = "selector: rules out the label `app: web`, which launcher sets on the pods of component \"web\""
	expr := func(op string, values ...any) map[string]any {
		e := map[string]any{"key": "app", "operator": op}
		if values != nil {
			e["values"] = values
		}
		return map[string]any{"matchExpressions": []any{e}}
	}
	for name, tc := range map[string]struct {
		selector map[string]any
		labels   map[string]any
		want     string // "" when the component builds
	}{
		"app must not exist":              {expr("DoesNotExist"), map[string]any{"tier": "web"}, rulesOut},
		"app not in the label value":      {expr("NotIn", "web"), map[string]any{"tier": "web"}, rulesOut},
		"app not in another value":        {expr("NotIn", "db"), map[string]any{"tier": "web"}, ""},
		"app exists":                      {expr("Exists"), nil, ""},
		"app is the label value":          {map[string]any{"matchLabels": map[string]any{"app": "web"}}, nil, ""},
		"no match on the authored labels": {map[string]any{"matchLabels": map[string]any{"tier": "db"}}, map[string]any{"tier": "web"}, ""},
		// A selector that cannot be read cannot be compared with the labels.
		"an unknown operator": {expr("Bogus"), map[string]any{"tier": "web"}, `selector: "Bogus" is not a valid label selector operator`},
		"a value that is no label": {map[string]any{"matchLabels": map[string]any{"tier": "not a label"}}, map[string]any{"tier": "web"},
			"selector: "},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.ReplicaSetHandler{}, "replicaset", "web", rsLabelled(tc.selector, tc.labels))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicaSetHandler_FillsNoPolicyDefault: a policy that carries replica and
// resource defaults fills none of them.
func TestReplicaSetHandler_FillsNoPolicyDefault(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	rs := rsGenerate(t, "web", ptObject(t, rsPlain(htPlainPod)), defaults)
	if rs.Spec.Replicas != nil {
		t.Errorf("replicas = %d, want it unset as authored", *rs.Spec.Replicas)
	}
	if res := rs.Spec.Template.Spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the replicaset kind fills no policy default", res)
	}
}

// TestReplicaSetHandler_Refusals: what the component refuses when it is read,
// with no policy involved: a property that is not a ReplicaSetSpec field, at
// any depth; an unauthored selector or template; what the pod kind refuses of
// a pod spec, by its path under the template; and activeDeadlineSeconds.
func TestReplicaSetHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a apps/v1 ReplicaSetSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	selector := map[string]any{"matchLabels": map[string]any{"tier": "web"}}
	// with returns a component whose pod spec is the plain one plus extra.
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"selector": selector,
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
		"no properties":        {nil, "selector: required"},
		"no selector":          {map[string]any{"template": with(nil)["template"]}, "selector: required"},
		"null selector":        {top(map[string]any{"selector": nil}), "selector: required"},
		"no template":          {map[string]any{"selector": selector}, "template.spec.containers: required"},
		"a workload's image":   {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"a deployment's field": {top(map[string]any{"strategy": map[string]any{"type": "Recreate"}}), notASpec},
		"replicas a string":    {top(map[string]any{"replicas": "two"}), notASpec},
		"template sub-key":     {with(map[string]any{"containerz": []any{}}), notASpec},
		"two spellings":        {top(map[string]any{"replicas": 1, "Replicas": 2}), "sets the same field as"},
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
			err := coreKindErr(&components.ReplicaSetHandler{}, "replicaset", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicaSetHandler_ZerosTheTypeCarries: replicas and minReadySeconds of 0
// are carried: replicas is a pointer, and minReadySeconds defaults to 0.
func TestReplicaSetHandler_ZerosTheTypeCarries(t *testing.T) {
	props := ptObject(t, rsPlain(htPlainPod))
	props["replicas"] = 0
	props["minReadySeconds"] = 0
	rs := rsGenerate(t, "web", props, ptStrictPolicy())
	if rs.Spec.Replicas == nil || *rs.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want an authored 0", rs.Spec.Replicas)
	}
}

// TestReplicaSetConfig_GenerateRepeatsTheRefusals: the config is exported, so
// a spec built in code is held to the refusals the typed spec can show before
// the ReplicaSet is emitted.
func TestReplicaSetConfig_GenerateRepeatsTheRefusals(t *testing.T) {
	app := corev1.Container{Name: "app", Image: "registry.example/team/app:1.2.3"}
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "web"}}
	deadline, priority := int64(600), int32(10)
	template := func(labels map[string]string, spec corev1.PodSpec) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: spec}
	}
	tier := map[string]string{"tier": "web"}
	for name, tc := range map[string]struct {
		spec appsv1.ReplicaSetSpec
		want string
	}{
		"no selector":   {appsv1.ReplicaSetSpec{Template: template(tier, corev1.PodSpec{Containers: []corev1.Container{app}})}, "selector: required"},
		"no containers": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(tier, corev1.PodSpec{})}, "template.spec.containers: required"},
		"active deadline": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, ActiveDeadlineSeconds: &deadline})}, "template.spec.activeDeadlineSeconds: not supported"},
		"priority": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, Priority: &priority})}, "template.spec.priority: not authorable"},
		"overhead": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{app}, Overhead: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}})}, "template.spec.overhead: not authorable"},
		"untagged image": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(tier,
			corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}})}, `template.spec.containers[0] "app": image "nginx" rejected`},
		"another app label": {appsv1.ReplicaSetSpec{Selector: selector, Template: template(map[string]string{"tier": "web", "app": "frontend"},
			corev1.PodSpec{Containers: []corev1.Container{app}})}, "template.metadata.labels.app: \"frontend\" is not the `app` label of component \"web\""},
		"selector against the app label": {appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpDoesNotExist}}},
			Template: template(tier, corev1.PodSpec{Containers: []corev1.Container{app}})}, "selector: rules out the label `app: web`"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &components.ReplicaSetConfig{Name: "web", Namespace: coreKindNamespace, Spec: tc.spec}
			_, err := cfg.Generate(stack.NewApplication("web", coreKindNamespace, cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestReplicaSetConfig_GenerateLeavesTheConfigAlone: generating does not write
// the `app` label into the config's own template, so a second Generate under
// another application name labels the pods for that name.
func TestReplicaSetConfig_GenerateLeavesTheConfigAlone(t *testing.T) {
	cfg := &components.ReplicaSetConfig{Spec: appsv1.ReplicaSetSpec{
		Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "web"}},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"tier": "web"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/team/app:1.2.3"}}},
		},
	}}
	for _, name := range []string{"web", "api"} {
		objs, err := cfg.Generate(stack.NewApplication(name, coreKindNamespace, cfg))
		if err != nil {
			t.Fatalf("%s: Generate: %v", name, err)
		}
		got := (*objs[0]).(*appsv1.ReplicaSet).Spec.Template.Labels
		if want := map[string]string{"tier": "web", "app": name}; !maps.Equal(got, want) {
			t.Errorf("%s: template labels = %v, want %v", name, got, want)
		}
	}
	if want := map[string]string{"tier": "web"}; !maps.Equal(cfg.Spec.Template.Labels, want) {
		t.Errorf("the config's template labels = %v after Generate, want the authored %v", cfg.Spec.Template.Labels, want)
	}
}

// TestReplicaSetConfig_ServiceAccountName: the ReplicaSet reports the account
// its pods run as to the traits that bind identity to it: the template's
// serviceAccountName, the deprecated serviceAccount where that one is unset,
// and "" for the namespace's default account. It always runs pods.
func TestReplicaSetConfig_ServiceAccountName(t *testing.T) {
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
			cfg, err := (&components.ReplicaSetHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "replicaset", Properties: ptObject(t, rsPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the replicaset config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}

// TestReplicaSetPolicy_Replicas: replicas is held to the policy's replica
// maximum, an unset one as the 1 the API server defaults it to, and the
// violation names the component.
func TestReplicaSetPolicy_Replicas(t *testing.T) {
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
			props := ptObject(t, rsPlain(htPlainPod))
			if tc.replicas != nil {
				props["replicas"] = tc.replicas
			}
			objs, err := pvTransform("replicaset", &components.ReplicaSetHandler{}, props, pvPolicy(tc.policy))
			if tc.want != "" {
				htWantViolation(t, err, `component "web": `+tc.want)
				return
			}
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "ReplicaSet" {
				t.Fatalf("generated %v, want the one ReplicaSet", objs)
			}
		})
	}
}

// TestReplicaSetConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as
// on every other config.
func TestReplicaSetConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.ReplicaSetConfig{Name: "web", Namespace: coreKindNamespace, Spec: appsv1.ReplicaSetSpec{
		Replicas: int32ptr(50),
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			HostNetwork: true,
			Containers:  []corev1.Container{{Name: "app", Image: "other.example/team/app:1.2.3"}},
		}},
	}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
