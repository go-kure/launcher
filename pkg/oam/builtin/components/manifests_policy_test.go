package components_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// mfApp is a one-component Application of the given type and properties. The
// component is named web.
func mfApp(componentType string, props map[string]any) *oam.Application {
	return &oam.Application{
		Metadata: oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name:       "web",
			Type:       componentType,
			Properties: props,
		}}},
	}
}

// mfTransformer knows the two components that share the manifest source.
func mfTransformer() *oam.Transformer {
	return oam.NewTransformer(map[string]oam.ComponentHandler{
		"manifests": &components.ManifestsHandler{},
		"crd":       &components.CRDHandler{},
	}, nil)
}

// mfTransform runs a one-component Application through the transform under
// policy in namespace demo, and generates every application of the result.
func mfTransform(componentType string, props map[string]any, policy oam.Policy) ([]client.Object, error) {
	cluster, err := mfTransformer().Transform(mfApp(componentType, props), oam.TransformContext{Namespace: "demo", Policy: policy})
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

// mfInline is the properties of a manifests component with an inline source.
func mfInline(doc string) map[string]any { return map[string]any{"inline": doc} }

// mfNamespaced gives the object named thing the namespace demo, so that a
// kind whose scope the build does not know passes the namespace stamping and
// reaches the policy check.
func mfNamespaced(doc string) string {
	return strings.Replace(doc, "  name: thing\n", "  name: thing\n  namespace: demo\n", 1)
}

// mfServe serves doc at every path and counts the requests it receives.
func mfServe(t *testing.T, doc string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// mfPolicyFor is the strict policy with the server's host added to the
// registries it lists, so that the source itself is allowed.
func mfPolicyFor(t *testing.T, srvURL string) *stubPolicy {
	t.Helper()
	p := ptStrictPolicy()
	p.allowedRegistries = append(p.allowedRegistries, htServerHost(t, srvURL))
	return p
}

// TestManifests_ObjectViolations: an object a manifests source yields is held
// to the image, pod-security, resource, storage and replica policy an authored
// workload is. Each case is one inline source, refused by the transform with a
// violation that names the component and the object. An object that cannot be
// read (a workload kind in an API version the build does not know, an item of
// a list of an unregistered kind) is refused rather than passed.
func TestManifests_ObjectViolations(t *testing.T) {
	const privileged = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n"
	withReplicas := func(doc string, n int) string {
		return strings.Replace(doc, "spec:\n", fmt.Sprintf("spec:\n  replicas: %d\n", n), 1)
	}
	cases := []struct {
		name   string
		inline string
		want   []string
	}{
		{
			name:   "privileged container in a Deployment",
			inline: ptDeployment(privileged),
			want:   []string{`object Deployment "demo/thing"`, `spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "image from a registry outside the allowlist",
			inline: ptDeployment("containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n"),
			want:   []string{`object Deployment "demo/thing"`, `image "other.example/team/app:1.2.3" is not from an allowed registry`},
		},
		{
			name:   "image without a tag or digest",
			inline: ptDeployment("containers:\n  - name: app\n    image: registry.example/team/app\n"),
			want:   []string{`object Deployment "demo/thing"`, "no tag or digest specified"},
		},
		{
			name:   "image tagged latest in an init container",
			inline: ptDeployment("initContainers:\n  - name: setup\n    image: registry.example/team/setup:latest\n" + htPlainPod),
			want:   []string{`spec.template.spec.initContainers[0] "setup"`, ":latest tag not allowed"},
		},
		{
			name:   "host network in a DaemonSet",
			inline: ptWorkload("DaemonSet", "apps/v1", "spec.template.spec", "hostNetwork: true\n"+htPlainPod),
			want:   []string{`object DaemonSet "demo/thing"`, "hostNetwork is not allowed"},
		},
		{
			name:   "host PID namespace in a bare Pod",
			inline: ptWorkload("Pod", "v1", "spec", "hostPID: true\n"+htPlainPod),
			want:   []string{`object Pod "demo/thing"`, "hostPID is not allowed"},
		},
		{
			name: "hostPath volume in a StatefulSet",
			inline: ptWorkload("StatefulSet", "apps/v1", "spec.template.spec",
				"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n"+htPlainPod),
			want: []string{`object StatefulSet "demo/thing"`, `volume "host": hostPath volumes are not allowed`},
		},
		{
			name:   "privileged container in a CronJob",
			inline: ptWorkload("CronJob", "batch/v1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+privileged),
			want:   []string{`object CronJob "demo/thing"`, `spec.jobTemplate.spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:   "privileged container in a Job",
			inline: ptWorkload("Job", "batch/v1", "spec.template.spec", "restartPolicy: Never\n"+privileged),
			want:   []string{`object Job "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "forbidden capability",
			inline: ptDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n"),
			want: []string{`object Deployment "demo/thing"`, `"NET_ADMIN" is forbidden by environment policy`},
		},
		{
			name: "cpu limit over the maximum",
			inline: ptDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        cpu: \"8\"\n"),
			want: []string{`object Deployment "demo/thing"`, `cpu limit "8" exceeds enforced maximum "2"`},
		},
		{
			name: "memory limit over the maximum",
			inline: ptDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        memory: 8Gi\n"),
			want: []string{`object Deployment "demo/thing"`, `memory limit "8Gi" exceeds enforced maximum "1Gi"`},
		},
		{
			name:   "ephemeral container",
			inline: ptDeployment(htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n"),
			want:   []string{`object Deployment "demo/thing"`, "spec.template.spec.ephemeralContainers: not supported"},
		},
		{
			name: "PersistentVolumeClaim over the storage maximum",
			inline: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: thing\nspec:\n" +
				"  resources:\n    requests:\n      storage: 1Ti\n",
			want: []string{`object PersistentVolumeClaim "demo/thing"`, `spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name: "StatefulSet claim template over the storage maximum",
			inline: "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: thing\nspec:\n" +
				"  volumeClaimTemplates:\n    - metadata:\n        name: data\n      spec:\n        resources:\n          requests:\n            storage: 1Ti\n" +
				"  template:\n    spec:\n" + htIndent(htPlainPod, "      "),
			want: []string{`object StatefulSet "demo/thing"`, `spec.volumeClaimTemplates[0] "data" spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name:   "Deployment over the replica maximum",
			inline: withReplicas(ptDeployment(htPlainPod), 5),
			want:   []string{`object Deployment "demo/thing"`, "spec.replicas: replicas 5 exceeds enforced maximum 3"},
		},
		{
			name: "HorizontalPodAutoscaler over the replica maximum",
			inline: "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  minReplicas: 1\n  maxReplicas: 9\n",
			want: []string{`object HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			// autoscaling/v1 is not in kure's scheme, so the object reaches the
			// check unstructured; spec.maxReplicas sits at the same path in every
			// version.
			name: "HorizontalPodAutoscaler in an unregistered API version over the replica maximum",
			inline: mfNamespaced("apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: 9\n"),
			want: []string{`object HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			name:   "workload in an API version the build cannot read",
			inline: mfNamespaced(ptWorkload("CronJob", "batch/v1beta1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+htPlainPod)),
			want:   []string{`object CronJob "demo/thing"`, `apiVersion "batch/v1beta1"`, "cannot be checked against environment policy"},
		},
		{
			name: "Pod inside a list of an unregistered kind",
			inline: "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
				"  - apiVersion: v1\n    kind: Pod\n    metadata:\n      name: thing\n      namespace: demo\n    spec:\n" + htIndent(htPlainPod, "      "),
			want: []string{`object Pod "demo/thing"`, `apiVersion "v1"`, "cannot be checked against environment policy"},
		},
		{
			name: "PersistentVolumeClaim inside a list of an unregistered kind",
			inline: "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
				"  - apiVersion: v1\n    kind: PersistentVolumeClaim\n    metadata:\n      name: thing\n      namespace: demo\n",
			want: []string{`object PersistentVolumeClaim "demo/thing"`, `apiVersion "v1"`, "cannot be checked against environment policy"},
		},
		{
			// The parser unpacks the outer list and leaves the inner one whole.
			name: "list left inside a list of an unregistered kind",
			inline: "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
				"  - apiVersion: example.io/v1\n    kind: InnerList\n    metadata:\n      name: inner\n      namespace: demo\n    items:\n" +
				"      - apiVersion: v1\n        kind: ConfigMap\n        metadata:\n          name: thing\n          namespace: demo\n",
			want: []string{`object InnerList "demo/inner"`, "has a top-level items list and sits inside a list of an unregistered kind"},
		},
		{
			name: "second document of a source",
			inline: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  key: value\n---\n" +
				ptDeployment(privileged),
			want: []string{`object Deployment "demo/thing"`, "securityContext.privileged is not allowed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The transform alone refuses an inline source: nothing is generated.
			_, err := mfTransformer().Transform(mfApp("manifests", mfInline(tc.inline)), oam.TransformContext{Namespace: "demo", Policy: ptStrictPolicy()})
			htWantViolation(t, err, append([]string{"manifest source: "}, tc.want...)...)
		})
	}
}

// TestManifests_PolicyAllowsWhatItAllows: a privileged, host-path workload from
// a registry the strict policy does not list builds under a policy that allows
// privileged containers and hostPath volumes and sets no registry allowlist.
func TestManifests_PolicyAllowsWhatItAllows(t *testing.T) {
	doc := ptDeployment(
		"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n" +
			"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")

	if _, err := mfTransform("manifests", mfInline(doc), &stubPolicy{}); err == nil {
		t.Fatal("control: the object builds under a policy allowing neither privileged containers nor hostPath volumes")
	}
	objs, err := mfTransform("manifests", mfInline(doc), &stubPolicy{allowPrivileged: true, allowHostPathVols: true})
	if err != nil {
		t.Fatalf("under a policy allowing privileged containers and hostPath volumes: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want the Deployment", len(objs))
	}
}

// TestManifests_NoPolicyDeniesPrivileged: with no policy passed the transform
// applies NoopPolicy, which denies a privileged container, as it does for an
// authored workload.
func TestManifests_NoPolicyDeniesPrivileged(t *testing.T) {
	_, err := mfTransform("manifests", mfInline(ptDeployment(
		"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")), nil)
	htWantViolation(t, err, `manifest source: object Deployment "demo/thing"`, "securityContext.privileged is not allowed")
}

// TestManifests_ObjectsThatRunNoPodPass: a source of objects that run no pod
// builds under the strict policy, and so does a custom resource, whatever it
// holds: the pods a custom resource's controller creates are not covered.
func TestManifests_ObjectsThatRunNoPodPass(t *testing.T) {
	cases := map[string]struct {
		inline string
		want   int
	}{
		"ConfigMap":       {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\ndata:\n  key: value\n", 1},
		"custom resource": {mfNamespaced("apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: thing\nspec:\n  size: 3\n"), 1},
		"custom resource carrying a pod template": {mfNamespaced(ptWorkload("Rollout", "example.io/v1", "spec.template.spec",
			"hostNetwork: true\ncontainers:\n  - name: app\n    image: other.example/team/app\n    securityContext:\n      privileged: true\n")), 1},
		"workload from an allowed registry": {ptDeployment(htPlainPod), 1},
		"object that runs no pod inside a list of an unregistered kind": {"apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
			"  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: thing\n      namespace: demo\n    data:\n      key: value\n", 1},
		"several documents": {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  key: value\n---\n" +
			ptDeployment(htPlainPod), 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			objs, err := mfTransform("manifests", mfInline(tc.inline), ptStrictPolicy())
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != tc.want {
				t.Fatalf("generated %d objects, want %d", len(objs), tc.want)
			}
		})
	}
}

// TestManifests_TypedListDoesNotParse: a source holding a v1 List is refused by
// the parser, as it was before the policy check, and not as a policy violation.
func TestManifests_TypedListDoesNotParse(t *testing.T) {
	_, err := mfTransform("manifests", mfInline("apiVersion: v1\nkind: List\nitems:\n"+
		"  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: thing\n      namespace: demo\n"), ptStrictPolicy())
	if err == nil {
		t.Fatal("a source holding a v1 List built")
	}
	var v *oam.ViolationError
	if errors.As(err, &v) {
		t.Fatalf("a parse failure is reported as a policy violation: %v", err)
	}
	if !strings.Contains(err.Error(), "manifest source: parse manifests") {
		t.Errorf("error %q is not the parser's", err)
	}
}

// TestCRD_BuildsUnderStrictPolicy: the crd component shares the manifest
// source and its policy step. A CustomResourceDefinition runs no pod, so it
// builds under the strict policy as it did.
func TestCRD_BuildsUnderStrictPolicy(t *testing.T) {
	const crd = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.io\nspec:\n" +
		"  group: example.io\n  names:\n    kind: Widget\n    plural: widgets\n  scope: Namespaced\n" +
		"  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n"
	objs, err := mfTransform("crd", mfInline(crd), ptStrictPolicy())
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want the CustomResourceDefinition", len(objs))
	}
}

// TestManifests_URLSourceIsCheckedAtGeneration: the objects of a url source
// are known only once it is fetched, and it is fetched at generation, as it
// was. The transform passes without a request; generation fetches the source
// and refuses the object, with the component's policy violation.
func TestManifests_URLSourceIsCheckedAtGeneration(t *testing.T) {
	srv, requests := mfServe(t, ptDeployment("hostNetwork: true\n"+htPlainPod))
	policy := mfPolicyFor(t, srv.URL)
	props := map[string]any{"url": srv.URL + "/manifests.yaml"}

	cluster, err := mfTransformer().Transform(mfApp("manifests", props), oam.TransformContext{Namespace: "demo", Policy: policy})
	if err != nil {
		t.Fatalf("transform of a url source: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the transform sent %d requests to the source, want the fetch left to generation", n)
	}

	_, err = oam.GenerateApplications(cluster)
	htWantViolation(t, err, `manifest source: object Deployment "demo/thing"`, "hostNetwork is not allowed")
	if n := requests.Load(); n != 1 {
		t.Errorf("generation sent %d requests to the source, want 1", n)
	}
}

// TestManifests_URLSourceWithinPolicyBuilds: a url source whose objects the
// policy allows is generated as before.
func TestManifests_URLSourceWithinPolicyBuilds(t *testing.T) {
	srv, _ := mfServe(t, ptDeployment(htPlainPod))
	objs, err := mfTransform("manifests", map[string]any{"url": srv.URL + "/manifests.yaml"}, mfPolicyFor(t, srv.URL))
	if err != nil {
		t.Fatalf("transform and generation: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want the Deployment", len(objs))
	}
}

// TestManifests_FetchFailureIsNotAViolation: a source that cannot be fetched
// fails generation with the fetch error it had, not with a policy violation.
func TestManifests_FetchFailureIsNotAViolation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	_, err := mfTransform("manifests", map[string]any{"url": srv.URL + "/manifests.yaml"}, mfPolicyFor(t, srv.URL))
	if err == nil {
		t.Fatal("generation of a source answering 404 succeeded")
	}
	var v *oam.ViolationError
	if errors.As(err, &v) {
		t.Fatalf("a fetch failure is reported as a policy violation: %v", err)
	}
}

// TestManifestConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, at
// the policy step and at generation.
func TestManifestConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg, err := (&components.ManifestsHandler{}).ToApplicationConfig(&oam.Component{
		Name: "web", Type: "manifests", Properties: mfInline(ptDeployment("hostNetwork: true\n" + htPlainPod)),
	}, "demo")
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

// TestManifestConfig_GenerateRechecksInlineSource: generation holds an inline
// source to the kept policy too, although the policy step already checked it.
// The source passes the policy step under a policy that allows a privileged
// container; the policy stops allowing it before generation, which is the only
// way the two checks of one inline source can differ.
func TestManifestConfig_GenerateRechecksInlineSource(t *testing.T) {
	cfg, err := (&components.ManifestsHandler{}).ToApplicationConfig(&oam.Component{
		Name: "raw", Type: "manifests", Properties: mfInline(ptDeployment(
			"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")),
	}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	policy := &stubPolicy{allowPrivileged: true}
	if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
		t.Fatalf("ApplyPolicy under a policy allowing privileged containers: %v", err)
	}
	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("control: Generate under the policy the source passed: %v", err)
	}
	policy.allowPrivileged = false
	_, err = cfg.Generate(nil)
	if err == nil {
		t.Fatal("Generate emitted a privileged workload under a policy that refuses it")
	}
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
	}
	if v.Component != "raw" {
		t.Errorf("violation names component %q, want %q", v.Component, "raw")
	}
	for _, f := range []string{`manifest source: object Deployment "demo/thing"`, "securityContext.privileged is not allowed"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q lacks %q", err, f)
		}
	}
}

// TestManifestConfig_GenerateChecksWhatItEmits: once a policy is applied,
// generation holds the objects of a url source to it, which is where they are
// first known; the refusal is the component's policy violation.
func TestManifestConfig_GenerateChecksWhatItEmits(t *testing.T) {
	srv, _ := mfServe(t, ptDeployment("hostNetwork: true\n"+htPlainPod))
	cfg, err := (&components.ManifestsHandler{}).ToApplicationConfig(&oam.Component{
		Name: "fetched", Type: "manifests", Properties: map[string]any{"url": srv.URL + "/manifests.yaml"},
	}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(mfPolicyFor(t, srv.URL)); err != nil {
		t.Fatalf("ApplyPolicy on a url source from an allowed host: %v", err)
	}
	_, err = cfg.Generate(nil)
	if err == nil {
		t.Fatal("Generate emitted a host-network workload under a policy that refuses it")
	}
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
	}
	if v.Component != "fetched" {
		t.Errorf("violation names component %q, want %q", v.Component, "fetched")
	}
	for _, f := range []string{`component "fetched"`, `manifest source: object Deployment "demo/thing"`, "hostNetwork is not allowed"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q lacks %q", err, f)
		}
	}
}
