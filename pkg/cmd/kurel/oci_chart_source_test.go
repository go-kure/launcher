package kurel

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// An oci component and a helm component over OCI naming the same artifact and
// version each get their own OCIRepository (go-kure/launcher#665): the chart's
// copies the chart layer as-is, the oci component's keeps Flux's default and
// extracts the layer. One shared object cannot carry both, so a shared source
// would hand one consumer a source shaped for the other.
const ociAndChartSameArtifactYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: manifests
      type: oci
      properties:
        source:
          url: oci://registry.example.com/charts/shop
        version: 1.2.3
    - name: chart
      type: helm
      properties:
        version: 1.2.3
        source:
          url: oci://registry.example.com/charts/shop
`

// buildStdoutDocs is buildDocs with stderr kept apart, so the warning a
// deprecated type prints does not land among the manifests.
func buildStdoutDocs(t *testing.T, appYAML string) ([]map[string]any, string) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\nstderr: %s", err, errOut.String())
	}

	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, out.String()
}

func TestBuild_OCIAndHelmSameArtifact_SeparateSources(t *testing.T) {
	docs, out := buildStdoutDocs(t, ociAndChartSameArtifactYAML)

	repos := map[string]map[string]any{}
	for _, d := range docs {
		if d["kind"] != "OCIRepository" {
			continue
		}
		md, _ := d["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		spec, _ := d["spec"].(map[string]any)
		repos[name] = spec
	}
	// The helm component's source is generated under a content-derived name.
	names := slices.Sorted(maps.Keys(repos))
	if len(names) != 2 || names[0] != "manifests" || !strings.HasPrefix(names[1], "shop-source-") {
		t.Fatalf("OCIRepositories = %v, want the oci component's and one generated for the chart [manifests shop-source-...]\noutput:\n%s", names, out)
	}
	chartSource := names[1]

	// The oci component leaves layerSelector unset: Flux's default, extract.
	if sel, ok := repos["manifests"]["layerSelector"]; ok {
		t.Errorf("manifests layerSelector = %v, want unset (Flux default extract)", sel)
	}
	sel, _ := repos[chartSource]["layerSelector"].(map[string]any)
	if sel["operation"] != "copy" || sel["mediaType"] != "application/vnd.cncf.helm.chart.content.v1.tar+gzip" {
		t.Errorf("chart source layerSelector = %v, want the chart content layer with operation copy", repos[chartSource]["layerSelector"])
	}

	// Each consumer references its own source.
	var refs []string
	for _, d := range docs {
		spec, _ := d["spec"].(map[string]any)
		var ref map[string]any
		switch d["kind"] {
		case "HelmRelease":
			ref, _ = spec["chartRef"].(map[string]any)
		case "Kustomization":
			ref, _ = spec["sourceRef"].(map[string]any)
		default:
			continue
		}
		md, _ := d["metadata"].(map[string]any)
		refs = append(refs, fmt.Sprintf("%s/%v->%v/%v", d["kind"], md["name"], ref["kind"], ref["name"]))
	}
	slices.Sort(refs)
	want := []string{"HelmRelease/chart->OCIRepository/" + chartSource, "Kustomization/manifests->OCIRepository/manifests"}
	if !slices.Equal(refs, want) {
		t.Errorf("source references = %v, want %v", refs, want)
	}
}
