package kurel

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// registryPolicy is NoopPolicy with a registry allowlist.
type registryPolicy struct {
	*oam.NoopPolicy
	registries []string
}

func (p registryPolicy) AllowedRegistries() []string { return p.registries }

// TestBuiltinHelm_TemplateDeliveryUnderPolicy drives a helm component with
// delivery: template through the built-in rules and handlers, as a consumer of
// the library does: the rule lowers it to a helmtemplate, and the transform
// holds that to the policy (go-kure/launcher#791). Under a registry allowlist
// that does not list the chart repository's host the transform is refused with
// a violation naming the component, and the repository receives no request.
// With the host listed, the result holds the chart's Deployment and its hook
// Job as their Go types.
func TestBuiltinHelm_TemplateDeliveryUnderPolicy(t *testing.T) {
	const pod = "      containers:\n        - name: app\n          image: registry.example/team/app:1.2.3\n"
	chartBuf := buildMinimalChartTar(t, "testchart", "0.1.0", map[string]string{
		"testchart/templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n" +
			"  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n    spec:\n" + pod,
		"testchart/templates/job.yaml": "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: migrate\n  annotations:\n    helm.sh/hook: pre-install\nspec:\n" +
			"  template:\n    spec:\n      restartPolicy: Never\n" + pod,
	})

	// The index names the archive by the request's own host: the handler runs
	// on the server's goroutine, so it reads nothing the test assigns later.
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", "http://"+r.Host+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			_, _ = w.Write(chartBuf)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	appYAML := fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: demo
spec:
  components:
    - name: web
      type: helm
      properties:
        delivery: template
        chart: testchart
        version: "0.1.0"
        source:
          url: %s
`, srv.URL)

	transform := func(t *testing.T, registries ...string) ([]client.Object, error) {
		t.Helper()
		transformer := newBuiltinTransformer()
		app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		policy := registryPolicy{NoopPolicy: &oam.NoopPolicy{}, registries: registries}
		cluster, _, err := transformer.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain, Policy: policy})
		if err != nil {
			return nil, err
		}
		apps, err := oam.GenerateApplications(cluster)
		if err != nil {
			return nil, err
		}
		var objs []client.Object
		for _, a := range apps {
			for _, o := range a.Objects {
				objs = append(objs, *o)
			}
		}
		return objs, nil
	}

	t.Run("source host outside the allowlist", func(t *testing.T) {
		before := requests.Load()
		_, err := transform(t, "registry.example")
		var v *oam.ViolationError
		if !errors.As(err, &v) {
			t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
		}
		if v.Component != "web" {
			t.Errorf("violation names component %q, want %q", v.Component, "web")
		}
		for _, want := range []string{"is not in allowed registries", host} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
		if n := requests.Load() - before; n != 0 {
			t.Errorf("the chart repository received %d request(s), want none: a refused source is not fetched", n)
		}
	})

	t.Run("source host allowed", func(t *testing.T) {
		before := requests.Load()
		objs, err := transform(t, "registry.example", host)
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if requests.Load() == before {
			t.Error("the chart repository received no request, though its host is allowed")
		}
		byName := map[string]client.Object{}
		for _, o := range objs {
			byName[o.GetName()] = o
		}
		dep, ok := byName["web"].(*appsv1.Deployment)
		if !ok {
			t.Fatalf("web is %T, want *appsv1.Deployment; generated %d object(s)", byName["web"], len(objs))
		}
		if got := dep.Spec.Template.Spec.Containers[0].Image; got != "registry.example/team/app:1.2.3" {
			t.Errorf("Deployment image = %q, want the chart's", got)
		}
		job, ok := byName["migrate"].(*batchv1.Job)
		if !ok {
			t.Fatalf("migrate is %T, want *batchv1.Job", byName["migrate"])
		}
		if got := job.Annotations["helm.sh/hook"]; got != "pre-install" {
			t.Errorf("hook Job annotation = %q, want it kept as rendered", got)
		}
	})
}
