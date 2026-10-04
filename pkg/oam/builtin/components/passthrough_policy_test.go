package components_test

import (
	"fmt"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// ptObject decodes one YAML document as the authored path does (yaml.v3 into
// any), giving the map a passthrough component's `object` property holds.
func ptObject(t *testing.T, doc string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
		t.Fatalf("decoding the test object: %v\n%s", err, doc)
	}
	return obj
}

// ptWorkload is the YAML of an object of the given kind whose pod spec, the
// given YAML, sits at path.
func ptWorkload(kind, apiVersion, path, podSpec string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nmetadata:\n  name: thing\n", apiVersion, kind)
	indent := ""
	for _, l := range strings.Split(path, ".") {
		fmt.Fprintf(&b, "%s%s:\n", indent, l)
		indent += "  "
	}
	b.WriteString(htIndent(podSpec, indent))
	return b.String()
}

// ptDeployment is the YAML of an apps/v1 Deployment running podSpec.
func ptDeployment(podSpec string) string {
	return ptWorkload("Deployment", "apps/v1", "spec.template.spec", podSpec)
}

// ptTransform runs a one-component passthrough Application emitting object
// through the transform under policy, and generates every application of the
// result. The component is named web, the namespace demo.
func ptTransform(object map[string]any, policy oam.Policy) ([]client.Object, error) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"passthrough": &components.PassthroughHandler{}}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name:       "web",
			Type:       "passthrough",
			Properties: map[string]any{"object": object},
		}}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Policy: policy})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// ptStrictPolicy lists registry.example and allows nothing else: the policy
// the rendered-object cases of template delivery run under.
func ptStrictPolicy() *stubPolicy {
	return &stubPolicy{
		allowedRegistries:      []string{"registry.example"},
		forbiddenContainerCaps: []string{"NET_ADMIN"},
		maxCPU:                 "2",
		maxStorageSize:         "10Gi",
		maxReplicas:            int32ptr(3),
	}
}

// TestPassthrough_ObjectViolations: the object a passthrough component emits is
// held to the image, pod-security, storage and replica policy an authored
// workload is. Each case is one object, refused with a violation that names
// the component and the object. An object that cannot be read — a workload
// kind in an API version the build does not know, a registered kind that does
// not decode — is refused rather than passed.
func TestPassthrough_ObjectViolations(t *testing.T) {
	const privileged = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n"
	withReplicas := func(doc string, n int) string {
		return strings.Replace(doc, "spec:\n", fmt.Sprintf("spec:\n  replicas: %d\n", n), 1)
	}
	cases := []struct {
		name   string
		object string
		want   []string
	}{
		{
			name:   "privileged container in a Deployment",
			object: ptDeployment(privileged),
			want:   []string{`object Deployment "demo/thing"`, `spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "image from a registry outside the allowlist",
			object: ptDeployment("containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n"),
			want:   []string{`object Deployment "demo/thing"`, `image "other.example/team/app:1.2.3" is not from an allowed registry`},
		},
		{
			name:   "image without a tag or digest",
			object: ptDeployment("containers:\n  - name: app\n    image: registry.example/team/app\n"),
			want:   []string{`object Deployment "demo/thing"`, "no tag or digest specified"},
		},
		{
			name:   "image tagged latest in an init container",
			object: ptDeployment("initContainers:\n  - name: setup\n    image: registry.example/team/setup:latest\n" + htPlainPod),
			want:   []string{`spec.template.spec.initContainers[0] "setup"`, ":latest tag not allowed"},
		},
		{
			name:   "host network in a DaemonSet",
			object: ptWorkload("DaemonSet", "apps/v1", "spec.template.spec", "hostNetwork: true\n"+htPlainPod),
			want:   []string{`object DaemonSet "demo/thing"`, "hostNetwork is not allowed"},
		},
		{
			name:   "host PID namespace in a Deployment",
			object: ptDeployment("hostPID: true\n" + htPlainPod),
			want:   []string{`object Deployment "demo/thing"`, "hostPID is not allowed"},
		},
		{
			name:   "host IPC namespace in a Deployment",
			object: ptDeployment("hostIPC: true\n" + htPlainPod),
			want:   []string{`object Deployment "demo/thing"`, "hostIPC is not allowed"},
		},
		{
			name: "hostPath volume in a StatefulSet",
			object: ptWorkload("StatefulSet", "apps/v1", "spec.template.spec",
				"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n"+htPlainPod),
			want: []string{`object StatefulSet "demo/thing"`, `volume "host": hostPath volumes are not allowed`},
		},
		{
			name:   "privileged container in a CronJob",
			object: ptWorkload("CronJob", "batch/v1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+privileged),
			want:   []string{`object CronJob "demo/thing"`, `spec.jobTemplate.spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a Job",
			object: ptWorkload("Job", "batch/v1", "spec.template.spec", "restartPolicy: Never\n"+privileged),
			want:   []string{`object Job "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a bare Pod",
			object: ptWorkload("Pod", "v1", "spec", privileged),
			want:   []string{`object Pod "demo/thing"`, `spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a ReplicaSet",
			object: ptWorkload("ReplicaSet", "apps/v1", "spec.template.spec", privileged),
			want:   []string{`object ReplicaSet "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a ReplicationController",
			object: ptWorkload("ReplicationController", "v1", "spec.template.spec", privileged),
			want:   []string{`object ReplicationController "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a PodTemplate",
			object: ptWorkload("PodTemplate", "v1", "template.spec", privileged),
			want:   []string{`object PodTemplate "demo/thing"`, `template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "StatefulSet claim template over the storage maximum",
			object: "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: thing\nspec:\n" +
				"  volumeClaimTemplates:\n    - metadata:\n        name: data\n      spec:\n        resources:\n          requests:\n            storage: 1Ti\n" +
				"  template:\n    spec:\n" + htIndent(htPlainPod, "      "),
			want: []string{`object StatefulSet "demo/thing"`, `spec.volumeClaimTemplates[0] "data" spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name: "PersistentVolumeClaim over the storage maximum",
			object: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: thing\nspec:\n" +
				"  resources:\n    requests:\n      storage: 1Ti\n",
			want: []string{`object PersistentVolumeClaim "demo/thing"`, `spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name:   "Deployment over the replica maximum",
			object: withReplicas(ptDeployment(htPlainPod), 5),
			want:   []string{`object Deployment "demo/thing"`, "spec.replicas: replicas 5 exceeds enforced maximum 3"},
		},
		{
			name:   "StatefulSet over the replica maximum",
			object: withReplicas(ptWorkload("StatefulSet", "apps/v1", "spec.template.spec", htPlainPod), 4),
			want:   []string{`object StatefulSet "demo/thing"`, "spec.replicas: replicas 4 exceeds enforced maximum 3"},
		},
		{
			name:   "ReplicaSet over the replica maximum",
			object: withReplicas(ptWorkload("ReplicaSet", "apps/v1", "spec.template.spec", htPlainPod), 4),
			want:   []string{`object ReplicaSet "demo/thing"`, "spec.replicas: replicas 4 exceeds enforced maximum 3"},
		},
		{
			name: "HorizontalPodAutoscaler over the replica maximum",
			object: "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  minReplicas: 1\n  maxReplicas: 9\n",
			want: []string{`object HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			// autoscaling/v1 is not in kure's scheme, so the object is checked as
			// it was authored; spec.maxReplicas sits at the same path in every version.
			name: "HorizontalPodAutoscaler in an unregistered API version over the replica maximum",
			object: "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: 9\n",
			want: []string{`object HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			name: "HorizontalPodAutoscaler in an unregistered API version with an unreadable maximum",
			object: "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: \"9\"\n",
			want: []string{`object HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas is not an integer", "cannot be checked against environment policy"},
		},
		{
			name:   "workload in an API version the build cannot read",
			object: ptWorkload("CronJob", "batch/v1beta1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+htPlainPod),
			want:   []string{`object CronJob "demo/thing"`, `apiVersion "batch/v1beta1"`, "cannot be checked against environment policy"},
		},
		{
			// A field of a registered kind whose value has the wrong type: the
			// object does not decode as its kind, so nothing in it can be read.
			name:   "Deployment that does not decode as its kind",
			object: strings.Replace(ptDeployment(privileged), "spec:\n", "spec:\n  replicas: three\n", 1),
			want:   []string{`object Deployment "demo/thing"`, "cannot be checked against environment policy"},
		},
		{
			name:   "ephemeral container",
			object: ptDeployment(htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n"),
			want:   []string{`object Deployment "demo/thing"`, "spec.template.spec.ephemeralContainers: not supported"},
		},
		{
			name: "forbidden capability",
			object: ptDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n"),
			want: []string{`object Deployment "demo/thing"`, `"NET_ADMIN" is forbidden by environment policy`},
		},
		{
			name: "cpu limit over the maximum",
			object: ptDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        cpu: \"8\"\n"),
			want: []string{`object Deployment "demo/thing"`, `cpu limit "8" exceeds enforced maximum "2"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ptTransform(ptObject(t, tc.object), ptStrictPolicy())
			htWantViolation(t, err, append([]string{"passthrough: "}, tc.want...)...)
		})
	}
}

// TestPassthrough_GoAssembledObjectIsChecked: an object a lowering rule or a
// library caller assembles in Go holds typed values (an int32, a
// map[string]string, a []map[string]any) that the authored path never
// produces. It is read all the same.
func TestPassthrough_GoAssembledObjectIsChecked(t *testing.T) {
	object := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "thing", "labels": map[string]string{"app": "thing"}},
		"spec": map[string]any{
			"replicas": int32(5),
			"selector": map[string]any{"matchLabels": map[string]string{"app": "thing"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]string{"app": "thing"}},
				"spec": map[string]any{
					"containers": []map[string]any{{"name": "app", "image": "registry.example/team/app:1.2.3"}},
				},
			},
		},
	}
	_, err := ptTransform(object, ptStrictPolicy())
	htWantViolation(t, err, `passthrough: object Deployment "demo/thing"`, "spec.replicas: replicas 5 exceeds enforced maximum 3")
}

// TestPassthrough_PolicyAllowsWhatItAllows: a privileged, host-path workload
// from a registry the strict policy does not list builds under a policy that
// allows privileged containers and hostPath volumes and sets no registry
// allowlist, and is emitted as it was authored: unstructured, a field the
// vendored API type does not declare included.
func TestPassthrough_PolicyAllowsWhatItAllows(t *testing.T) {
	doc := strings.Replace(ptDeployment(
		"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n"+
			"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n"),
		"spec:\n", "spec:\n  fieldOfALaterVersion: kept\n", 1)

	if _, err := ptTransform(ptObject(t, doc), &stubPolicy{}); err == nil {
		t.Fatal("control: the object builds under a policy allowing neither privileged containers nor hostPath volumes")
	}
	objs, err := ptTransform(ptObject(t, doc), &stubPolicy{allowPrivileged: true, allowHostPathVols: true})
	if err != nil {
		t.Fatalf("under a policy allowing privileged containers and hostPath volumes: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want the Deployment", len(objs))
	}
	u, ok := objs[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("emitted %T, want the authored object as *unstructured.Unstructured", objs[0])
	}
	if got, _, _ := unstructured.NestedString(u.Object, "spec", "fieldOfALaterVersion"); got != "kept" {
		t.Errorf("spec.fieldOfALaterVersion = %q, want it emitted as authored", got)
	}
}

// TestPassthrough_NoPolicyDeniesPrivileged: with no policy passed the transform
// applies NoopPolicy, which denies a privileged container, as it does for an
// authored workload.
func TestPassthrough_NoPolicyDeniesPrivileged(t *testing.T) {
	_, err := ptTransform(ptObject(t, ptDeployment(
		"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")), nil)
	htWantViolation(t, err, `passthrough: object Deployment "demo/thing"`, "securityContext.privileged is not allowed")
}

// TestPassthrough_ObjectsThatRunNoPodPass: an object of a kind that runs no
// pod builds under the strict policy, and so does a custom resource, whatever
// it holds: the pods a custom resource's controller creates are not covered.
func TestPassthrough_ObjectsThatRunNoPodPass(t *testing.T) {
	cases := map[string]string{
		"custom resource": "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: thing\nspec:\n  size: 3\n",
		"ConfigMap":       "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\ndata:\n  key: value\n",
		"custom resource carrying a pod template": ptWorkload("Rollout", "example.io/v1", "spec.template.spec",
			"hostNetwork: true\ncontainers:\n  - name: app\n    image: other.example/team/app\n    securityContext:\n      privileged: true\n"),
		"workload from an allowed registry": ptDeployment(htPlainPod),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			objs, err := ptTransform(ptObject(t, doc), ptStrictPolicy())
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != 1 {
				t.Fatalf("generated %d objects, want 1", len(objs))
			}
		})
	}
}

// TestPassthrough_ObjectValuedItemsStillBuilds: a custom resource whose
// top-level `items` is an object, not a sequence, is one ordinary resource
// (TestPassthrough_ListShapedObjectIsRejected accepts it), and the policy check
// reads it as one.
func TestPassthrough_ObjectValuedItemsStillBuilds(t *testing.T) {
	objs, err := ptTransform(ptObject(t,
		"apiVersion: example.io/v1\nkind: Catalog\nmetadata:\n  name: thing\nitems:\n  blue: 1\n  green: 2\n"), ptStrictPolicy())
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want 1", len(objs))
	}
}

// TestPassthrough_UnserializableObjectIsRefused: an object holding a value
// with no JSON form (a mapping with a key that is not a string, which the
// authored path decodes to a map[any]any) cannot be read, so it is refused
// under a policy. Nothing that built before is lost: the same object, emitted
// with no policy applied, does not encode as a manifest either.
func TestPassthrough_UnserializableObjectIsRefused(t *testing.T) {
	doc := "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: thing\nspec:\n  ports:\n    80: http\n"
	if _, ok := ptObject(t, doc)["spec"].(map[string]any)["ports"].(map[any]any); !ok {
		t.Fatal("control: the test object's spec.ports is not a map[any]any")
	}

	_, err := ptTransform(ptObject(t, doc), ptStrictPolicy())
	htWantViolation(t, err, `passthrough: object Widget "demo/thing"`, "cannot be read", "cannot be checked against environment policy")

	cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(passthroughComponent(map[string]any{"object": ptObject(t, doc)}), "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	objs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate with no policy applied: %v", err)
	}
	if out, err := kureio.EncodeObjectsToYAML(objs); err == nil {
		t.Fatalf("control: the object encodes as a manifest, so refusing it under a policy loses a build that worked:\n%s", out)
	}
}

// TestPassthroughConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing.
func TestPassthroughConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(passthroughComponent(map[string]any{
		"object": ptObject(t, ptDeployment("hostNetwork: true\n"+htPlainPod)),
	}), "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Fatalf("ApplyPolicy(nil): %v", err)
	}
	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("Generate after ApplyPolicy(nil): %v", err)
	}
}

// TestPassthrough_GenerateRechecksTheEmittedObject: Object is an exported
// field, so a caller holding the config can replace it after the policy was
// applied. Generate holds the object it is about to emit to that policy again,
// so the check binds what is emitted and not an earlier map.
func TestPassthrough_GenerateRechecksTheEmittedObject(t *testing.T) {
	cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(passthroughComponent(map[string]any{
		"object": ptObject(t, ptDeployment(htPlainPod)),
	}), "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(ptStrictPolicy()); err != nil {
		t.Fatalf("ApplyPolicy on the allowed object: %v", err)
	}
	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("Generate of the allowed object: %v", err)
	}

	cfg.(*components.PassthroughConfig).Object = ptObject(t, ptDeployment("hostNetwork: true\n"+htPlainPod))
	_, err = cfg.Generate(nil)
	if err == nil {
		t.Fatal("Generate emitted an object replaced after the policy was applied, unchecked")
	}
	for _, f := range []string{`passthrough component "my-res"`, `object Deployment "demo/thing"`, "hostNetwork is not allowed"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q lacks %q", err, f)
		}
	}
}
