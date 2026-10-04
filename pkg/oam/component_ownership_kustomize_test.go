package oam

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// chartOutput stands for what a chart renders: a Deployment whose pod template
// already carries the component key with the chart's own value, a CronJob, a
// ReplicaSet and a ReplicationController that do not carry it, and an object
// with no pod template.
const chartOutput = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: chart-web
spec:
  selector:
    matchLabels:
      app: chart
  template:
    metadata:
      labels:
        app: chart
        launcher.gokure.dev/component: set-by-the-chart
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: chart-cron
spec:
  schedule: "@daily"
  jobTemplate:
    spec:
      template:
        metadata:
          labels:
            app: cron
---
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: chart-replicas
spec:
  selector:
    matchLabels:
      app: replicas
  template:
    metadata:
      labels:
        app: replicas
---
apiVersion: v1
kind: ReplicationController
metadata:
  name: chart-controller
spec:
  selector:
    app: controller
  template:
    metadata:
      labels:
        app: controller
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: chart-settings
data:
  k: v
`

// applyComponentLabelPostRenderer applies the post-renderer's patches for
// key: value the way Flux applies a HelmRelease's: as kustomize patches over
// the chart's output. It returns the chart's objects by name.
func applyComponentLabelPostRenderer(t *testing.T, key, value string) map[string]map[string]any {
	t.Helper()
	pr, err := componentLabelPostRenderer(key, value)
	if err != nil {
		t.Fatalf("componentLabelPostRenderer: %v", err)
	}
	kustomization, err := yaml.Marshal(map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"resources":  []string{"chart.yaml"},
		"patches":    pr.Kustomize.Patches,
	})
	if err != nil {
		t.Fatalf("marshaling the kustomization: %v", err)
	}
	fs := filesys.MakeFsInMemory()
	for name, content := range map[string]string{"kustomization.yaml": string(kustomization), "chart.yaml": chartOutput} {
		if err := fs.WriteFile(name, []byte(content)); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	result, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(fs, ".")
	if err != nil {
		t.Fatalf("kustomize: %v\n%s", err, kustomization)
	}
	out, err := result.AsYaml()
	if err != nil {
		t.Fatalf("rendering the result: %v", err)
	}

	byName := map[string]map[string]any{}
	for _, raw := range strings.Split(string(out), "\n---\n") {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("decoding the result: %v\n%s", err, raw)
		}
		name, _, _ := unstructured.NestedString(doc, "metadata", "name")
		byName[name] = doc
	}
	if len(byName) != 5 {
		t.Fatalf("kustomize returned objects %v, want the chart's five under their own names\n%s", byName, out)
	}
	return byName
}

// TestComponentLabelPostRenderer_AppliedByKustomize: the component's value
// replaces whatever the chart set, every pod template gets it, each object keeps
// its own name, and an object without a pod template is left alone.
func TestComponentLabelPostRenderer_AppliedByKustomize(t *testing.T) {
	byName := applyComponentLabelPostRenderer(t, ownershipKey, "web")
	for name, path := range map[string][]string{
		"chart-web":        {"spec", "template", "metadata", "labels"},
		"chart-cron":       {"spec", "jobTemplate", "spec", "template", "metadata", "labels"},
		"chart-replicas":   {"spec", "template", "metadata", "labels"},
		"chart-controller": {"spec", "template", "metadata", "labels"},
	} {
		labels, _, err := unstructured.NestedStringMap(byName[name], path...)
		if err != nil {
			t.Fatalf("%s pod template labels: %v", name, err)
		}
		if got := labels[ownershipKey]; got != "web" {
			t.Errorf("%s pod template %s = %q, want %q", name, ownershipKey, got, "web")
		}
		if len(labels) != 2 {
			t.Errorf("%s pod template labels = %v, want the chart's own label kept beside the component's", name, labels)
		}
	}
	// A ReplicationController's selector is a plain label map; the patch leaves it
	// as the chart wrote it.
	selector, _, err := unstructured.NestedStringMap(byName["chart-controller"], "spec", "selector")
	if err != nil {
		t.Fatalf("chart-controller selector: %v", err)
	}
	if len(selector) != 1 || selector["app"] != "controller" {
		t.Errorf("chart-controller selector = %v, want the chart's own", selector)
	}
	if labels, found, _ := unstructured.NestedMap(byName["chart-settings"], "metadata", "labels"); found {
		t.Errorf("the ConfigMap got labels %v, want it left alone", labels)
	}
	if _, found, _ := unstructured.NestedMap(byName["chart-settings"], "spec"); found {
		t.Errorf("the ConfigMap got a spec: %v", byName["chart-settings"])
	}
}

// TestComponentLabelPostRenderer_OverwritesAKeyTheChartSelectsOn pins the
// documented limit of the post-renderer: it overwrites whatever key is
// configured and touches no selector. With a ComponentLabelKey the chart's own
// selector uses ("app"), the pod template gets the component's value while the
// selector keeps the chart's, and the two no longer match: the cluster refuses
// such a workload. Launcher does not look into a chart to detect that; the
// consumer picks a key no chart sets.
func TestComponentLabelPostRenderer_OverwritesAKeyTheChartSelectsOn(t *testing.T) {
	web := applyComponentLabelPostRenderer(t, "app", "web")["chart-web"]
	podLabels, _, err := unstructured.NestedStringMap(web, "spec", "template", "metadata", "labels")
	if err != nil {
		t.Fatalf("pod template labels: %v", err)
	}
	if got := podLabels["app"]; got != "web" {
		t.Errorf("pod template app = %q, want the component's value %q in place of the chart's", got, "web")
	}
	selector, _, err := unstructured.NestedStringMap(web, "spec", "selector", "matchLabels")
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if got := selector["app"]; got != "chart" {
		t.Errorf("selector app = %q, want the chart's %q: the patch touches the pod template only", got, "chart")
	}
}
