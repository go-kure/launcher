package components_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestPodHandler_CanHandle(t *testing.T) {
	h := &components.PodHandler{}
	if !h.CanHandle("pod") {
		t.Error("CanHandle(pod) = false")
	}
	if h.CanHandle("podtemplate") {
		t.Error("CanHandle(podtemplate) = true")
	}
}

// podRich is a pod spec that sets fields of every shape the type has: lists of
// objects, a map, pointers to numbers and booleans, quantities written as
// numbers, and probes with authored non-zero timings.
const podRich = `restartPolicy: OnFailure
terminationGracePeriodSeconds: 0
activeDeadlineSeconds: 600
serviceAccountName: runner
automountServiceAccountToken: false
enableServiceLinks: false
priorityClassName: batch
runtimeClassName: gvisor
nodeSelector:
  disktype: ssd
tolerations:
  - key: dedicated
    operator: Exists
    effect: NoSchedule
imagePullSecrets:
  - name: pull
securityContext:
  runAsNonRoot: true
  fsGroup: 2000
volumes:
  - name: scratch
    emptyDir: {}
initContainers:
  - name: init
    image: registry.example/team/init:1.0.0
    command: [sh, -c, "true"]
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
    livenessProbe:
      httpGet:
        path: /healthz
        port: http
      initialDelaySeconds: 0
      periodSeconds: 5
      timeoutSeconds: 2
      successThreshold: 1
      failureThreshold: 4
`

// TestPodHandler_EmitsAuthoredSpec: the Pod is named after the component in
// the build namespace and its spec is the authored one, field for field, with
// nothing added: no label, no annotation, no ServiceAccount token setting and
// no resources of launcher's.
func TestPodHandler_EmitsAuthoredSpec(t *testing.T) {
	var want corev1.PodSpec
	if err := yaml.UnmarshalStrict([]byte(podRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	pod := generateCoreKindUnder(t, &components.PodHandler{}, "pod", "runner", ptObject(t, podRich), ptStrictPolicy(), nil).(*corev1.Pod)
	if pod.APIVersion != "v1" || pod.Kind != "Pod" {
		t.Errorf("GVK = %s %s, want v1 Pod", pod.APIVersion, pod.Kind)
	}
	if pod.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", pod.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(pod.Spec, want) {
		t.Errorf("spec differs from the authored one:\n got %+v\nwant %+v", pod.Spec, want)
	}
	if q := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; q.String() != "1" {
		t.Errorf("cpu limit = %s, want 1", q.String())
	}
}

// TestPodHandler_FillsNoPolicyDefault: a policy that carries resource and
// replica defaults fills none of them, on a container with resources and on
// one without.
func TestPodHandler_FillsNoPolicyDefault(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	pod := generateCoreKindUnder(t, &components.PodHandler{}, "pod", "runner", ptObject(t, htPlainPod), defaults).(*corev1.Pod)
	if res := pod.Spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the pod kind fills no policy default", res)
	}
	if pod.Spec.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want it unset as authored", *pod.Spec.AutomountServiceAccountToken)
	}
}

// TestPodHandler_Refusals: what the component refuses when it is read, with no
// policy involved: a property that is not a PodSpec field, at any depth; an
// unauthored containers list; the three fields no pod can be created with; and
// an image without a tag or tagged latest, on init and regular containers.
func TestPodHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 PodSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	with := func(extra map[string]any) map[string]any {
		props := map[string]any{"containers": []any{app}}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":           {nil, "containers: required"},
		"null containers":         {map[string]any{"containers": nil}, "containers: required"},
		"a workload kind's image": {with(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"metadata key":            {with(map[string]any{"labels": map[string]any{"team": "a"}}), notASpec},
		"container sub-key":       {map[string]any{"containers": []any{map[string]any{"name": "app", "imagee": "x:1"}}}, notASpec},
		"containers a string":     {map[string]any{"containers": "app"}, notASpec},
		"hostNetwork a string":    {with(map[string]any{"hostNetwork": "yes"}), notASpec},
		"null container":          {map[string]any{"containers": []any{app, nil}}, "containers[1]"},
		"two spellings":           {with(map[string]any{"hostname": "a", "Hostname": "b"}), "sets the same field as"},
		"ephemeral containers": {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}),
			"ephemeralContainers: not supported"},
		"priority":      {with(map[string]any{"priority": 1000}), "priority: not authorable"},
		"priority zero": {with(map[string]any{"priority": 0}), "priority: not authorable"},
		"overhead":      {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), "overhead: not authorable"},
		"untagged image": {map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app"}}},
			`containers[0] "app": image "registry.example/team/app" rejected: no tag or digest specified`},
		"latest image": {map[string]any{"containers": []any{app, map[string]any{"name": "side", "image": "registry.example/team/side:latest"}}},
			`containers[1] "side": image "registry.example/team/side:latest" rejected: :latest tag not allowed`},
		"no image": {map[string]any{"containers": []any{map[string]any{"name": "app"}}}, `containers[0] "app": image "" rejected`},
		"untagged init image": {with(map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "busybox"}}}),
			`initContainers[0] "init": image "busybox" rejected: no tag or digest specified`},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.PodHandler{}, "pod", "runner", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestPodHandler_EmptyRefusedFieldsAreUnset: an empty ephemeralContainers or
// overhead carries nothing, so it is read as unset and not written.
func TestPodHandler_EmptyRefusedFieldsAreUnset(t *testing.T) {
	props := ptObject(t, htPlainPod)
	props["ephemeralContainers"] = []any{}
	props["overhead"] = map[string]any{}
	props["priority"] = nil
	pod := generateCoreKindUnder(t, &components.PodHandler{}, "pod", "runner", props, nil).(*corev1.Pod)
	if len(pod.Spec.EphemeralContainers) != 0 || len(pod.Spec.Overhead) != 0 || pod.Spec.Priority != nil {
		t.Errorf("spec carries ephemeralContainers %v, overhead %v, priority %v; want none", pod.Spec.EphemeralContainers, pod.Spec.Overhead, pod.Spec.Priority)
	}
}

// TestPodHandler_RefusesProbeZeros: a probe timing the API server defaults to
// a non-zero value cannot be written as 0, since the type would omit it and
// the default would apply. It is refused by path on each of the three probes
// of init and regular containers. initialDelaySeconds, whose omitted value is
// 0, is not.
func TestPodHandler_RefusesProbeZeros(t *testing.T) {
	defaults := map[string]string{"timeoutSeconds": "1", "periodSeconds": "10", "successThreshold": "1", "failureThreshold": "3"}
	plain := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	probed := func(list, probe, field string, value int) map[string]any {
		ctr := map[string]any{"name": "probed", "image": "registry.example/team/app:1.2.3",
			probe: map[string]any{"exec": map[string]any{"command": []any{"true"}}, field: value}}
		if list == "containers" {
			return map[string]any{"containers": []any{ctr}}
		}
		return map[string]any{"containers": []any{plain}, list: []any{ctr}}
	}
	for _, list := range []string{"initContainers", "containers"} {
		for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
			for _, field := range slices.Sorted(mapKeys(defaults)) {
				path := fmt.Sprintf("%s[0].%s.%s", list, probe, field)
				t.Run(path, func(t *testing.T) {
					err := coreKindErr(&components.PodHandler{}, "pod", "runner", probed(list, probe, field, 0))
					want := path + ": 0 cannot be carried by the Kubernetes API types (the field is omitted when zero, so the API server would apply its default " + defaults[field] + ")"
					if err == nil || err.Error() != want {
						t.Fatalf("err = %v\nwant %s", err, want)
					}
					if err := coreKindErr(&components.PodHandler{}, "pod", "runner", probed(list, probe, field, 2)); err != nil {
						t.Errorf("%s: 2 refused: %v", path, err)
					}
				})
			}
			if err := coreKindErr(&components.PodHandler{}, "pod", "runner", probed(list, probe, "initialDelaySeconds", 0)); err != nil {
				t.Errorf("%s[0].%s.initialDelaySeconds: 0 refused: %v", list, probe, err)
			}
		}
	}
}

// mapKeys is maps.Keys for the one map type the test above sorts.
func mapKeys(m map[string]string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestPodConfig_GenerateRepeatsTheRefusals: the config is exported, so a spec
// built in code is held to the refusals the typed spec can show before the Pod
// is emitted.
func TestPodConfig_GenerateRepeatsTheRefusals(t *testing.T) {
	app := corev1.Container{Name: "app", Image: "registry.example/team/app:1.2.3"}
	priority := int32(10)
	for name, tc := range map[string]struct {
		spec corev1.PodSpec
		want string
	}{
		"no containers":        {corev1.PodSpec{}, "containers: required"},
		"ephemeral containers": {corev1.PodSpec{Containers: []corev1.Container{app}, EphemeralContainers: []corev1.EphemeralContainer{{}}}, "ephemeralContainers: not supported"},
		"priority":             {corev1.PodSpec{Containers: []corev1.Container{app}, Priority: &priority}, "priority: not authorable"},
		"overhead":             {corev1.PodSpec{Containers: []corev1.Container{app}, Overhead: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}, "overhead: not authorable"},
		"untagged image":       {corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}}, `containers[0] "app": image "nginx" rejected`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &components.PodConfig{Name: "runner", Namespace: coreKindNamespace, Spec: tc.spec}
			_, err := cfg.Generate(stack.NewApplication("runner", coreKindNamespace, cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestPodConfig_ServiceAccountName: the pod reports the account it runs as to
// the traits that bind identity to it: the authored serviceAccountName, the
// deprecated serviceAccount where that one is unset (as the API server reads
// the two), and "" for the namespace's default account. It always runs pods.
func TestPodConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]any
		want  string
	}{
		"none authored":      {nil, ""},
		"serviceAccountName": {map[string]any{"serviceAccountName": "runner"}, "runner"},
		"deprecated alias":   {map[string]any{"serviceAccount": "legacy"}, "legacy"},
		"both":               {map[string]any{"serviceAccountName": "runner", "serviceAccount": "legacy"}, "runner"},
	} {
		t.Run(name, func(t *testing.T) {
			props := ptObject(t, htPlainPod)
			for k, v := range tc.extra {
				props[k] = v
			}
			cfg, err := (&components.PodHandler{}).ToApplicationConfig(&oam.Component{Name: "runner", Type: "pod", Properties: props}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the pod config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}

// podPolicyPaths are the four ways a build produces a Pod: the pod kind, and
// the three components that emit an object written elsewhere. Each builds the
// pod with the given spec under the given policy; a nil policy is none passed.
// rendered is how the three name the object in a refusal; the kind names
// nothing, since its properties are the spec's fields.
var podPolicyPaths = []struct {
	name     string
	rendered string
	build    func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error)
}{
	{
		name: "kind",
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			return pvTransform("pod", &components.PodHandler{}, ptObject(t, spec), pvPolicy(policy))
		},
	},
	{
		name: "template delivery", rendered: "helmtemplate: rendered Pod",
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"pod.yaml": ptWorkload("Pod", "v1", "spec", spec)})
			if policy != nil {
				withHost := *policy
				withHost.allowedRegistries = append(slices.Clone(policy.allowedRegistries), htServerHost(t, srvURL))
				policy = &withHost
			}
			return htTransform(srvURL, pvPolicy(policy))
		},
	},
	{
		name: "passthrough", rendered: "passthrough: object Pod",
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			return ptTransform(ptObject(t, ptWorkload("Pod", "v1", "spec", spec)), pvPolicy(policy))
		},
	},
	{
		name: "manifests", rendered: "manifest source: object Pod",
		build: func(_ *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			return mfTransform("manifests", mfInline(ptWorkload("Pod", "v1", "spec", spec)), pvPolicy(policy))
		},
	},
}

// TestPodPolicy_RefusedOnEveryPath: what the environment policy refuses on a
// pod is refused whichever of the four paths produces it, by the same check,
// with a violation naming the component and the field. The kind names the
// field as the property it is; the other three name it under the object's
// spec. With no policy passed the transform applies its default, which allows
// no hostPath volume and no host namespace.
func TestPodPolicy_RefusedOnEveryPath(t *testing.T) {
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
		// field is the refusal as the kind gives it; sep joins it to "spec" on
		// the other three paths: ": " for a refusal of the pod, "." for one that
		// starts with a field path.
		sep, field string
	}{
		{"host network", htPlainPod + "hostNetwork: true\n", ptStrictPolicy(), ": ", "hostNetwork is not allowed by environment policy"},
		{"host network with no policy passed", htPlainPod + "hostNetwork: true\n", nil, ": ", "hostNetwork is not allowed by environment policy"},
		{"hostPath volume", htPlainPod + "volumes:\n  - name: host\n    hostPath:\n      path: /var/lib/data\n", ptStrictPolicy(), ": ",
			`volume "host": hostPath volumes are not allowed by environment policy`},
		{"hostPath volume with no policy passed", htPlainPod + "volumes:\n  - name: host\n    hostPath:\n      path: /var/lib/data\n", nil, ": ",
			`volume "host": hostPath volumes are not allowed by environment policy`},
		{"pod-level hostProcess", htPlainPod + "securityContext:\n  windowsOptions:\n    hostProcess: true\n", ptStrictPolicy(), ".",
			"securityContext.windowsOptions.hostProcess is not allowed by environment policy"},
		{"pod-level memory over the maximum", htPlainPod + "resources:\n  limits:\n    memory: 2Gi\n", ptStrictPolicy(), ": ",
			`resources memory limit "2Gi" exceeds enforced maximum "1Gi"`},
		{"registry not allowed", "containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n", ptStrictPolicy(), ".",
			`containers[0] "app": image "other.example/team/app:1.2.3" is not from an allowed registry`},
		{"cpu over the maximum", htPlainPod + "    resources:\n      limits:\n        cpu: 4\n", ptStrictPolicy(), ".",
			`containers[0] "app": cpu limit "4" exceeds enforced maximum "2"`},
		{"forbidden capability", htPlainPod + "    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n", ptStrictPolicy(), ".",
			`containers[0] "app": securityContext.capabilities.add: "NET_ADMIN" is forbidden by environment policy`},
		{"privileged init container", htPlainPod + "initContainers:\n  - name: init\n    image: registry.example/team/init:1.0.0\n    securityContext:\n      privileged: true\n",
			ptStrictPolicy(), ".", `initContainers[0] "init": securityContext.privileged is not allowed by environment policy`},
	}
	for _, path := range podPolicyPaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				_, err := path.build(t, tc.spec, tc.policy)
				if path.rendered != "" {
					htWantViolation(t, err, path.rendered, "spec"+tc.sep+tc.field)
					return
				}
				// The violation's cause starts at the field: no path before it.
				htWantViolation(t, err, `component "web": `+tc.field)
			})
		}
	}
}

// TestPodPolicy_AllowedOnEveryPath: a pod the policy does not refuse builds on
// each of the four paths.
func TestPodPolicy_AllowedOnEveryPath(t *testing.T) {
	hostPathAllowed := ptStrictPolicy()
	hostPathAllowed.allowHostPathVols = true
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
	}{
		{"plain pod", htPlainPod, ptStrictPolicy()},
		{"plain pod with no policy passed", htPlainPod, nil},
		{"cpu at the maximum", htPlainPod + "    resources:\n      limits:\n        cpu: 2\n", ptStrictPolicy()},
		{"hostPath volume where the policy allows it", htPlainPod + "volumes:\n  - name: host\n    hostPath:\n      path: /var/lib/data\n", hostPathAllowed},
		{"any registry with no policy passed", "containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n", nil},
	}
	for _, path := range podPolicyPaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				objs, err := path.build(t, tc.spec, tc.policy)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "Pod" {
					t.Fatalf("generated %v, want the one Pod", objs)
				}
			})
		}
	}
}

// TestPodPolicy_ReadTimeRefusalsAndTheRenderedPaths pins how the four paths
// differ on what the kind refuses when it is read. An untagged image and
// ephemeral containers are refused on every path. priority and overhead are
// refused by the kind only: the three paths that take an object written
// elsewhere pass them, and the API server's admission controllers decide.
func TestPodPolicy_ReadTimeRefusalsAndTheRenderedPaths(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		want     string
		rendered bool // refused on the three rendered paths too
	}{
		{"untagged image", "containers:\n  - name: app\n    image: registry.example/team/app\n", `containers[0] "app": image "registry.example/team/app" rejected`, true},
		{"ephemeral containers", htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n", "ephemeralContainers: not supported", true},
		{"priority", htPlainPod + "priority: 1000\n", "priority: not authorable", false},
		{"overhead", htPlainPod + "overhead:\n  cpu: 100m\n", "overhead: not authorable", false},
	}
	for _, path := range podPolicyPaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				_, err := path.build(t, tc.spec, ptStrictPolicy())
				switch {
				case path.rendered == "" || tc.rendered:
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
					}
				case err != nil:
					t.Fatalf("refused: %v; the rendered paths do not refuse this field (update the README's stated difference if they now do)", err)
				}
			})
		}
	}
}

// TestPodConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as on
// every other config.
func TestPodConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.PodConfig{Name: "runner", Namespace: coreKindNamespace, Spec: corev1.PodSpec{
		HostNetwork: true,
		Containers:  []corev1.Container{{Name: "app", Image: "other.example/team/app:1.2.3"}},
	}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
