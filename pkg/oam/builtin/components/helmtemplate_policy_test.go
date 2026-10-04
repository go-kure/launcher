package components_test

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// htPolicyDeployment is a chart template rendering one Deployment whose pod
// spec is the given YAML, indented under spec.template.spec.
func htPolicyDeployment(podSpec string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: {{ .Release.Namespace }}\nspec:\n" +
		"  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n    spec:\n" +
		htIndent(podSpec, "      ")
}

// htIndent prefixes every non-empty line of s with indent.
func htIndent(s, indent string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString(indent)
		}
		b.WriteString(line)
	}
	return b.String()
}

// htPlainPod is a pod spec no policy refuses: one container, a tagged image
// from registry.example.
const htPlainPod = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n"

// htPolicyChart is a chart of unremarkable workloads: a Deployment, a
// pre-install hook Job, and an object of a kind kure's scheme does not know.
var htPolicyChart = map[string]string{
	"deployment.yaml": htPolicyDeployment(htPlainPod),
	"job.yaml": "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: migrate\n  annotations:\n    helm.sh/hook: pre-install\nspec:\n  template:\n    spec:\n" +
		"      restartPolicy: Never\n" + htIndent(htPlainPod, "      "),
	"widget.yaml": "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: gadget\nspec:\n  size: 3\n",
}

// htServerHost is the host, port included, of a chart server's URL: what a
// registry allowlist entry has to name to admit it.
func htServerHost(t *testing.T, srvURL string) string {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parsing %q: %v", srvURL, err)
	}
	return u.Host
}

// htTransform runs a one-component helmtemplate Application on the chart
// served at srvURL through the transform under policy, and generates every
// application of the result.
func htTransform(srvURL string, policy oam.Policy) ([]client.Object, error) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web",
			Type: "helmtemplate",
			Properties: map[string]any{
				"chart":   "testchart",
				"version": "0.1.0",
				"source":  map[string]any{"url": srvURL},
			},
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

// htWantViolation fails unless err is a policy violation of component web
// whose message carries every fragment.
func htWantViolation(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("transform succeeded, want a policy violation")
	}
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
	}
	if v.Component != "web" {
		t.Errorf("violation names component %q, want %q", v.Component, "web")
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q lacks %q", err, f)
		}
	}
}

// TestHelmTemplate_SourceOutsideAllowlistIsRefusedBeforeAnyFetch: under a
// registry allowlist that does not list the chart repository's host, the
// transform fails with a violation naming the component, and the repository
// receives no request — neither its index nor the chart is fetched. With the
// host listed, the same document builds and the repository is read.
func TestHelmTemplate_SourceOutsideAllowlistIsRefusedBeforeAnyFetch(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", htPolicyChart)

	_, err := htTransform(srvURL, &stubPolicy{allowedRegistries: []string{"registry.example"}})
	htWantViolation(t, err, "helmtemplate: source.url", "is not in allowed registries", htServerHost(t, srvURL))
	if n := requests.Load(); n != 0 {
		t.Errorf("the chart repository received %d request(s), want none: a refused source is not fetched", n)
	}

	allowed := &stubPolicy{allowedRegistries: []string{"registry.example", htServerHost(t, srvURL)}}
	if _, err := htTransform(srvURL, allowed); err != nil {
		t.Fatalf("with the repository's host listed: %v", err)
	}
	if requests.Load() == 0 {
		t.Error("the chart repository received no request, though its host is allowed")
	}
}

// TestHelmTemplate_RenderedObjectsAreTyped: an object of a kind kure's scheme
// registers leaves the transform as its Go type — the Deployment and the hook
// Job alike — and one of an unregistered kind as unstructured.
func TestHelmTemplate_RenderedObjectsAreTyped(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htPolicyChart)
	objs, err := htTransform(srvURL, &stubPolicy{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	byName := map[string]client.Object{}
	for _, o := range objs {
		byName[o.GetName()] = o
	}
	if len(byName) != 3 {
		t.Fatalf("generated %d objects, want the Deployment, the Job and the Widget: %v", len(byName), byName)
	}
	dep, ok := byName["web"].(*appsv1.Deployment)
	if !ok {
		t.Fatalf("web is %T, want *appsv1.Deployment", byName["web"])
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "registry.example/team/app:1.2.3" {
		t.Errorf("Deployment image = %q, want the chart's", got)
	}
	if dep.Namespace != "demo" {
		t.Errorf("Deployment namespace = %q, want the release namespace %q", dep.Namespace, "demo")
	}
	job, ok := byName["migrate"].(*batchv1.Job)
	if !ok {
		t.Fatalf("migrate is %T, want *batchv1.Job", byName["migrate"])
	}
	if got := job.Annotations["helm.sh/hook"]; got != "pre-install" {
		t.Errorf("hook Job annotation = %q, want it kept as rendered", got)
	}
	if objs[0] != client.Object(job) {
		t.Errorf("first object is %T %q, want the pre-install hook Job", objs[0], objs[0].GetName())
	}
	widget, ok := byName["gadget"].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("gadget is %T, want *unstructured.Unstructured", byName["gadget"])
	}
	if size, _, _ := unstructured.NestedInt64(widget.Object, "spec", "size"); size != 3 {
		t.Errorf("Widget spec.size = %d, want 3", size)
	}
}

// TestHelmTemplate_RenderedWorkloadViolations: a rendered workload is held to
// the image and pod-security policy an authored workload is. Each case is one
// chart, refused with a violation that names the component and the rendered
// object, under a policy that lists the chart repository and registry.example
// and allows nothing else.
func TestHelmTemplate_RenderedWorkloadViolations(t *testing.T) {
	podIn := func(kind, apiVersion, path, podSpec string) string {
		levels := strings.Split(path, ".")
		var b strings.Builder
		fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nmetadata:\n  name: thing\n", apiVersion, kind)
		indent := ""
		for _, l := range levels {
			fmt.Fprintf(&b, "%s%s:\n", indent, l)
			indent += "  "
		}
		b.WriteString(htIndent(podSpec, indent))
		return b.String()
	}
	const privileged = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n"
	cases := []struct {
		name      string
		templates map[string]string
		want      []string
	}{
		{
			name:      "privileged container in a Deployment",
			templates: map[string]string{"d.yaml": htPolicyDeployment(privileged)},
			want:      []string{`rendered Deployment "demo/web"`, `spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "image from a registry outside the allowlist",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n")},
			want: []string{`rendered Deployment "demo/web"`, `image "other.example/team/app:1.2.3" is not from an allowed registry`},
		},
		{
			name: "image without a tag or digest",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app\n")},
			want: []string{`rendered Deployment "demo/web"`, "no tag or digest specified"},
		},
		{
			name: "image tagged latest in an init container",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				"initContainers:\n  - name: setup\n    image: registry.example/team/setup:latest\n" + htPlainPod)},
			want: []string{`spec.template.spec.initContainers[0] "setup"`, ":latest tag not allowed"},
		},
		{
			name:      "host network in a DaemonSet",
			templates: map[string]string{"d.yaml": podIn("DaemonSet", "apps/v1", "spec.template.spec", "hostNetwork: true\n"+htPlainPod)},
			want:      []string{`rendered DaemonSet "demo/thing"`, "hostNetwork is not allowed"},
		},
		{
			name: "hostPath volume in a StatefulSet",
			templates: map[string]string{"d.yaml": podIn("StatefulSet", "apps/v1", "spec.template.spec",
				"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n"+htPlainPod)},
			want: []string{`rendered StatefulSet "demo/thing"`, `volume "host": hostPath volumes are not allowed`},
		},
		{
			name:      "privileged container in a CronJob",
			templates: map[string]string{"d.yaml": podIn("CronJob", "batch/v1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+privileged)},
			want:      []string{`rendered CronJob "demo/thing"`, `spec.jobTemplate.spec.template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:      "privileged container in a kept hook Job",
			templates: map[string]string{"d.yaml": strings.Replace(podIn("Job", "batch/v1", "spec.template.spec", "restartPolicy: Never\n"+privileged), "  name: thing\n", "  name: thing\n  annotations:\n    helm.sh/hook: post-install\n", 1)},
			want:      []string{`rendered Job "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name:      "privileged container in a bare Pod",
			templates: map[string]string{"d.yaml": podIn("Pod", "v1", "spec", privileged)},
			want:      []string{`rendered Pod "demo/thing"`, `spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name:      "privileged container in a ReplicaSet",
			templates: map[string]string{"d.yaml": podIn("ReplicaSet", "apps/v1", "spec.template.spec", privileged)},
			want:      []string{`rendered ReplicaSet "demo/thing"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "privileged container in a Pod inside a v1 List",
			templates: map[string]string{"d.yaml": "apiVersion: v1\nkind: List\nitems:\n" +
				"  - apiVersion: v1\n    kind: Pod\n    metadata:\n      name: inner\n    spec:\n" + htIndent(privileged, "      ")},
			want: []string{`rendered Pod "demo/inner"`, `spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "privileged container in a Deployment inside a typed list",
			templates: map[string]string{"d.yaml": "apiVersion: apps/v1\nkind: DeploymentList\nitems:\n" +
				"  - metadata:\n      name: inner\n    spec:\n      template:\n        spec:\n" + htIndent(privileged, "          ")},
			want: []string{`rendered Deployment "demo/inner"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "Pod inside a list of an unregistered kind",
			templates: map[string]string{"d.yaml": "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
				"  - apiVersion: v1\n    kind: Pod\n    metadata:\n      name: inner\n    spec:\n" + htIndent(htPlainPod, "      ")},
			want: []string{`rendered Pod "demo/inner"`, `apiVersion "v1"`, "cannot be checked against environment policy"},
		},
		{
			name: "list left inside a list of an unregistered kind",
			templates: map[string]string{"d.yaml": "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
				"  - apiVersion: v1\n    kind: List\n    metadata:\n      name: wrapped\n    items:\n" +
				"      - apiVersion: v1\n        kind: Pod\n        metadata:\n          name: inner\n        spec:\n" + htIndent(privileged, "          ")},
			want: []string{`rendered List "wrapped"`, "has a top-level items list", "cannot be checked against environment policy"},
		},
		{
			// A list is told by its items array alone, so a custom resource
			// with a field of that name is refused in the same position.
			name: "custom resource with an items field inside a list of an unregistered kind",
			templates: map[string]string{"d.yaml": "apiVersion: example.io/v1\nkind: CatalogList\nitems:\n" +
				"  - apiVersion: example.io/v1\n    kind: Catalog\n    metadata:\n      name: colors\n    items: [blue, green]\n"},
			want: []string{`rendered Catalog "colors"`, "has a top-level items list", "cannot be checked against environment policy"},
		},
		{
			name:      "privileged container in a PodTemplate",
			templates: map[string]string{"d.yaml": podIn("PodTemplate", "v1", "template.spec", privileged)},
			want:      []string{`rendered PodTemplate "demo/thing"`, `template.spec.containers[0] "app"`, "securityContext.privileged is not allowed"},
		},
		{
			name: "StatefulSet claim template over the storage maximum",
			templates: map[string]string{"d.yaml": "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: thing\nspec:\n" +
				"  volumeClaimTemplates:\n    - metadata:\n        name: data\n      spec:\n        resources:\n          requests:\n            storage: 1Ti\n" +
				"  template:\n    spec:\n" + htIndent(htPlainPod, "      ")},
			want: []string{`rendered StatefulSet "demo/thing"`, `spec.volumeClaimTemplates[0] "data" spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name: "PersistentVolumeClaim over the storage maximum",
			templates: map[string]string{"d.yaml": "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: thing\nspec:\n" +
				"  resources:\n    requests:\n      storage: 1Ti\n"},
			want: []string{`rendered PersistentVolumeClaim "demo/thing"`, `spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		},
		{
			name: "Deployment over the replica maximum",
			templates: map[string]string{"d.yaml": strings.Replace(htPolicyDeployment(htPlainPod),
				"spec:\n", "spec:\n  replicas: 5\n", 1)},
			want: []string{`rendered Deployment "demo/web"`, "spec.replicas: replicas 5 exceeds enforced maximum 3"},
		},
		{
			name: "StatefulSet over the replica maximum",
			templates: map[string]string{"d.yaml": strings.Replace(podIn("StatefulSet", "apps/v1", "spec.template.spec", htPlainPod),
				"spec:\n", "spec:\n  replicas: 4\n", 1)},
			want: []string{`rendered StatefulSet "demo/thing"`, "spec.replicas: replicas 4 exceeds enforced maximum 3"},
		},
		{
			name: "ReplicaSet over the replica maximum",
			templates: map[string]string{"d.yaml": strings.Replace(podIn("ReplicaSet", "apps/v1", "spec.template.spec", htPlainPod),
				"spec:\n", "spec:\n  replicas: 4\n", 1)},
			want: []string{`rendered ReplicaSet "demo/thing"`, "spec.replicas: replicas 4 exceeds enforced maximum 3"},
		},
		{
			name: "HorizontalPodAutoscaler over the replica maximum",
			templates: map[string]string{"d.yaml": "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  minReplicas: 1\n  maxReplicas: 9\n"},
			want: []string{`rendered HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			// autoscaling/v1 is not in kure's scheme, so the object arrives
			// unstructured; spec.maxReplicas sits at the same path in every version.
			name: "HorizontalPodAutoscaler in an unregistered API version over the replica maximum",
			templates: map[string]string{"d.yaml": "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: 9\n"},
			want: []string{`rendered HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas: replicas 9 exceeds enforced maximum 3"},
		},
		{
			name: "HorizontalPodAutoscaler in an unregistered API version with an unreadable maximum",
			templates: map[string]string{"d.yaml": "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
				"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: \"9\"\n"},
			want: []string{`rendered HorizontalPodAutoscaler "demo/thing"`, "spec.maxReplicas is not an integer", "cannot be checked against environment policy"},
		},
		{
			name:      "workload in an API version the build cannot read",
			templates: map[string]string{"d.yaml": podIn("CronJob", "batch/v1beta1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+htPlainPod)},
			want:      []string{`rendered CronJob "demo/thing"`, `apiVersion "batch/v1beta1"`, "cannot be checked against environment policy"},
		},
		{
			name: "ephemeral container",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n")},
			want: []string{`rendered Deployment "demo/web"`, "spec.template.spec.ephemeralContainers: not supported"},
		},
		{
			name: "forbidden capability",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n")},
			want: []string{`rendered Deployment "demo/web"`, `"NET_ADMIN" is forbidden by environment policy`},
		},
		{
			name: "cpu limit over the maximum",
			templates: map[string]string{"d.yaml": htPolicyDeployment(
				"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        cpu: \"8\"\n")},
			want: []string{`rendered Deployment "demo/web"`, `cpu limit "8" exceeds enforced maximum "2"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", tc.templates)
			policy := &stubPolicy{
				allowedRegistries:      []string{"registry.example", htServerHost(t, srvURL)},
				forbiddenContainerCaps: []string{"NET_ADMIN"},
				maxCPU:                 "2",
				maxStorageSize:         "10Gi",
				maxReplicas:            int32ptr(3),
			}
			_, err := htTransform(srvURL, policy)
			htWantViolation(t, err, append([]string{"helmtemplate: "}, tc.want...)...)
		})
	}
}

// TestHelmTemplate_PolicyAllowsWhatItAllows: the same privileged, host-path
// chart builds under a policy that allows both, and a chart is not held to an
// empty registry allowlist.
func TestHelmTemplate_PolicyAllowsWhatItAllows(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"d.yaml": htPolicyDeployment(
		"volumes:\n  - name: host\n    hostPath:\n      path: /etc\n" +
			"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")})

	if _, err := htTransform(srvURL, &stubPolicy{}); err == nil {
		t.Fatal("control: the chart builds under a policy allowing neither privileged containers nor hostPath volumes")
	}
	objs, err := htTransform(srvURL, &stubPolicy{allowPrivileged: true, allowHostPathVols: true})
	if err != nil {
		t.Fatalf("under a policy allowing privileged containers and hostPath volumes: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %d objects, want the Deployment", len(objs))
	}
}

// TestHelmTemplate_NoPolicyDeniesPrivileged: with no policy passed the
// transform applies NoopPolicy, which denies a privileged container, as it
// does for an authored workload.
func TestHelmTemplate_NoPolicyDeniesPrivileged(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"d.yaml": htPolicyDeployment(
		"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n")})
	_, err := htTransform(srvURL, nil)
	htWantViolation(t, err, `rendered Deployment "demo/web"`, "securityContext.privileged is not allowed")
}

// TestHelmTemplate_DroppedHookIsNotChecked: an object hook grouping drops is
// never emitted, so it is not held to the policy; the rest builds.
func TestHelmTemplate_DroppedHookIsNotChecked(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{
		"main.yaml": htPolicyDeployment(htPlainPod),
		"test.yaml": "apiVersion: v1\nkind: Pod\nmetadata:\n  name: smoke\n  annotations:\n    helm.sh/hook: test\nspec:\n  hostNetwork: true\n  containers:\n    - name: c\n      image: busybox\n      securityContext:\n        privileged: true\n",
	})
	objs, err := htTransform(srvURL, &stubPolicy{allowedRegistries: []string{"registry.example", htServerHost(t, srvURL)}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 || objs[0].GetName() != "web" {
		t.Fatalf("generated %d objects, want only the Deployment", len(objs))
	}
}

// TestHelmTemplate_TraitDecoratesRenderedDeploymentOnEveryGenerate: a rendered
// Deployment is typed, so a workload-decorating trait acts on it as on one a
// manifests source decodes. Generating the same transform result twice gives
// the same output: the second run decorates a fresh copy of the render, not the
// Deployment the first run already wrote constraints into (which the trait
// refuses).
func TestHelmTemplate_TraitDecoratesRenderedDeploymentOnEveryGenerate(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{
		"d.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 2\n" +
			"  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n    spec:\n" +
			htIndent(htPlainPod, "      "),
	})
	tr := oam.NewTransformer(
		map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}},
		map[string]oam.TraitHandler{"topology-spread": &traits.TopologySpreadHandler{}})
	cluster, err := tr.Transform(&oam.Application{
		Metadata: oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name:       "web",
			Type:       "helmtemplate",
			Properties: map[string]any{"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": srvURL}},
			Traits:     []oam.Trait{{Type: "topology-spread"}},
		}}},
	}, oam.TransformContext{Namespace: "demo", Policy: &stubPolicy{}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	want := components.BuildTopologySpreadConstraints(2, map[string]string{"app": "web"})
	for _, run := range []string{"first", "second"} {
		apps, err := oam.GenerateApplications(cluster)
		if err != nil {
			t.Fatalf("%s generate: %v", run, err)
		}
		if len(apps) != 1 || len(apps[0].Objects) != 1 {
			t.Fatalf("%s generate: got %d application(s), want one holding the Deployment", run, len(apps))
		}
		dep, ok := (*apps[0].Objects[0]).(*appsv1.Deployment)
		if !ok {
			t.Fatalf("%s generate: object is %T, want *appsv1.Deployment", run, *apps[0].Objects[0])
		}
		if got := dep.Spec.Template.Spec.TopologySpreadConstraints; !reflect.DeepEqual(got, want) {
			t.Errorf("%s generate: constraints %+v, want %+v", run, got, want)
		}
	}
}

// TestHelmTemplateConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing
// and fetches nothing, as for every other component.
func TestHelmTemplateConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", htPolicyChart)
	cfg := htRenderTerminal(t, srvURL, nil)
	if err := cfg.(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Fatalf("ApplyPolicy(nil): %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("ApplyPolicy(nil) made %d request(s), want none", n)
	}
}

// TestHelmTemplateConfig_ApplyPolicy_RendersOnceForGenerate: the render the
// policy check triggers is the one Generate and AugmentLayout return; the
// chart is not fetched again.
func TestHelmTemplateConfig_ApplyPolicy_RendersOnceForGenerate(t *testing.T) {
	srvURL, requests := startCountingHelmChartServer(t, "testchart", "0.1.0", htPolicyChart)
	cfg := htRenderTerminal(t, srvURL, nil)
	policy := &stubPolicy{allowedRegistries: []string{"registry.example", htServerHost(t, srvURL)}}
	if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	after := requests.Load()
	if after == 0 {
		t.Fatal("ApplyPolicy made no request: the workload checks need the render")
	}
	if got := len(htGenerate(t, cfg)); got != 3 {
		t.Errorf("Generate returned %d objects, want 3", got)
	}
	htAugment(t, cfg)
	if n := requests.Load(); n != after {
		t.Errorf("Generate and AugmentLayout made %d more request(s), want none", n-after)
	}
}
