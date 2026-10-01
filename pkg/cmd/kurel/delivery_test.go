package kurel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/go-kure/launcher/pkg/errors"
)

const (
	testOCIRepository = "oci://registry.example.com/apps"
	deliveryTestdata  = "testdata/delivery"
)

// deliveryScenario is one fixture under testdata/delivery/<name>/: app.yaml
// against testdata/delivery/cluster.yaml, with the expected output tree of
// `kurel build -o <dir> --oci-repository ...` under expected/.
type deliveryScenario struct {
	name string
	tag  string
}

var deliveryScenarios = []deliveryScenario{
	{name: "flat"},                        // one bundle, no --oci-tag
	{name: "hierarchical", tag: "v1.0.0"}, // umbrella + one child per tier
	{name: "dependency", tag: "v1.0.0"},   // per-component bundles with dependsOn, via the built-in dependency policy
}

// runKurel runs the kurel command with args and returns its stdout and the
// command error.
func runKurel(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewKurelCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

// buildScenario builds scenario s into outDir with delivery enabled and
// returns the stdout build of the same application.
func buildScenario(t *testing.T, s deliveryScenario, outDir string) []byte {
	t.Helper()
	dir := filepath.Join(deliveryTestdata, s.name)
	appPath := filepath.Join(dir, "app.yaml")
	profilePath := filepath.Join(deliveryTestdata, "cluster.yaml")
	args := []string{"build", appPath, "--profile", profilePath, "-o", outDir, "--oci-repository", testOCIRepository}
	if s.tag != "" {
		args = append(args, "--oci-tag", s.tag)
	}
	if _, err := runKurel(t, args...); err != nil {
		t.Fatalf("delivery build of %s: %v", s.name, err)
	}
	stdout, err := runKurel(t, "build", appPath, "--profile", profilePath)
	if err != nil {
		t.Fatalf("stdout build of %s: %v", s.name, err)
	}
	return []byte(stdout)
}

// readTree returns every regular file under root, keyed by slash path.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}
	return files
}

// TestDeliveryGolden compares each scenario's whole output tree — <app>.yaml,
// <app>.flux.yaml and every artifact directory — with testdata/delivery/<name>/expected.
// Set UPDATE_GOLDEN=1 to regenerate.
func TestDeliveryGolden(t *testing.T) {
	for _, s := range deliveryScenarios {
		t.Run(s.name, func(t *testing.T) {
			out := t.TempDir()
			buildScenario(t, s, out)
			got := readTree(t, out)
			expectedDir := filepath.Join(deliveryTestdata, s.name, "expected")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.RemoveAll(expectedDir); err != nil {
					t.Fatal(err)
				}
				for rel, content := range got {
					p := filepath.Join(expectedDir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			want := readTree(t, expectedDir)
			for rel, w := range want {
				g, ok := got[rel]
				if !ok {
					t.Errorf("missing output file %s", rel)
					continue
				}
				if g != w {
					t.Errorf("%s mismatch:\nwant:\n%s\ngot:\n%s", rel, w, g)
				}
			}
			for rel := range got {
				if _, ok := want[rel]; !ok {
					t.Errorf("unexpected output file %s", rel)
				}
			}
		})
	}
}

// TestDeliveryDeterministic builds every scenario twice and requires
// byte-identical trees.
func TestDeliveryDeterministic(t *testing.T) {
	for _, s := range deliveryScenarios {
		t.Run(s.name, func(t *testing.T) {
			a, b := t.TempDir(), t.TempDir()
			buildScenario(t, s, a)
			buildScenario(t, s, b)
			ta, tb := readTree(t, a), readTree(t, b)
			if len(ta) != len(tb) {
				t.Fatalf("run 1 wrote %d files, run 2 wrote %d", len(ta), len(tb))
			}
			for rel, content := range ta {
				if tb[rel] != content {
					t.Errorf("%s differs between two runs", rel)
				}
			}
		})
	}
}

// decodeDocs decodes a multi-document YAML stream into objects.
func decodeDocs(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []map[string]any
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}
		if err != nil {
			t.Fatalf("decoding YAML: %v", err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

// canonical renders an object as sorted-key JSON, so two objects compare equal
// exactly when their content is equal.
func canonical(t *testing.T, obj map[string]any) string {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// objectID is an object's apiVersion/kind/namespace/name.
func objectID(obj map[string]any) string {
	md, _ := obj["metadata"].(map[string]any)
	return strings.Join([]string{str(obj["apiVersion"]), str(obj["kind"]), str(md["namespace"]), str(md["name"])}, "/")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// artifactDirs returns the artifact directory names in out: every directory,
// since kurel writes nothing else as a directory there.
func artifactDirs(t *testing.T, out string) []string {
	t.Helper()
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

// checkDelivery asserts the delivery invariants of one build in out, whose
// application is appName and whose stdout build is stdout:
//   - every artifact directory builds with kustomize (krusty) to exactly its
//     manifests' objects (the same apiVersion/kind/namespace/name set); an
//     artifact without objects has no manifests.yaml;
//   - the artifacts partition the stdout build: every object is in exactly
//     one artifact, and their union equals the stdout build's objects;
//   - there is one Kustomization per artifact, named after it, with
//     spec.path "./" and a sourceRef to the OCIRepository of that name, which
//     pulls <repository>/<name> at tag.
func checkDelivery(t *testing.T, out, appName, tag string, stdout []byte) {
	t.Helper()
	dirs := artifactDirs(t, out)
	if len(dirs) == 0 {
		t.Fatal("no artifact directories written")
	}

	kustomizer := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	owner := map[string]string{} // object ID -> artifact
	var union []string
	for _, d := range dirs {
		adir := filepath.Join(out, d)
		var objs []map[string]any
		data, err := os.ReadFile(filepath.Join(adir, artifactManifestsFile))
		switch {
		case err == nil:
			objs = decodeDocs(t, data)
			if len(objs) == 0 {
				t.Errorf("artifact %s: manifests.yaml holds no objects", d)
			}
		case os.IsNotExist(err):
		default:
			t.Fatal(err)
		}
		resMap, err := kustomizer.Run(filesys.MakeFsOnDisk(), adir)
		if err != nil {
			t.Errorf("artifact %s: kustomize build: %v", d, err)
		} else {
			built := []string{}
			for _, r := range resMap.Resources() {
				built = append(built, strings.Join([]string{r.GetApiVersion(), r.GetKind(), r.GetNamespace(), r.GetName()}, "/"))
			}
			held := []string{}
			for _, o := range objs {
				held = append(held, objectID(o))
			}
			slices.Sort(built)
			slices.Sort(held)
			if !slices.Equal(built, held) {
				t.Errorf("artifact %s: kustomize built %v, manifests.yaml holds %v", d, built, held)
			}
		}
		for _, o := range objs {
			id := objectID(o)
			if prev, dup := owner[id]; dup {
				t.Errorf("object %s is in artifacts %s and %s", id, prev, d)
			}
			owner[id] = d
			union = append(union, canonical(t, o))
		}
	}

	var want []string
	for _, o := range decodeDocs(t, stdout) {
		want = append(want, canonical(t, o))
	}
	slices.Sort(want)
	slices.Sort(union)
	if !slices.Equal(want, union) {
		t.Errorf("artifact union (%d objects) differs from the stdout build (%d objects)", len(union), len(want))
	}

	fluxData, err := os.ReadFile(filepath.Join(out, appName+".flux.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	kusts := map[string]map[string]any{}
	repos := map[string]map[string]any{}
	for _, o := range decodeDocs(t, fluxData) {
		md, _ := o["metadata"].(map[string]any)
		if ns := str(md["namespace"]); ns != "flux-system" {
			t.Errorf("%s %s: namespace %q, want flux-system", str(o["kind"]), str(md["name"]), ns)
		}
		switch str(o["kind"]) {
		case "Kustomization":
			kusts[str(md["name"])] = o
		case "OCIRepository":
			repos[str(md["name"])] = o
		default:
			t.Errorf("unexpected Flux object kind %q", str(o["kind"]))
		}
	}
	if len(kusts) != len(dirs) || len(repos) != len(dirs) {
		t.Errorf("%d artifacts, %d Kustomizations, %d OCIRepositories: want one of each per artifact", len(dirs), len(kusts), len(repos))
	}
	for _, d := range dirs {
		k, ok := kusts[d]
		if !ok {
			t.Errorf("artifact %s has no Kustomization", d)
			continue
		}
		spec, _ := k["spec"].(map[string]any)
		if p := str(spec["path"]); p != "./" {
			t.Errorf("Kustomization %s: spec.path %q, want \"./\"", d, p)
		}
		ref, _ := spec["sourceRef"].(map[string]any)
		if str(ref["kind"]) != "OCIRepository" || str(ref["name"]) != d {
			t.Errorf("Kustomization %s: sourceRef %v, want OCIRepository %s", d, ref, d)
		}
		repo, ok := repos[d]
		if !ok {
			t.Errorf("artifact %s has no OCIRepository", d)
			continue
		}
		rspec, _ := repo["spec"].(map[string]any)
		if u := str(rspec["url"]); u != testOCIRepository+"/"+d {
			t.Errorf("OCIRepository %s: url %q, want %q", d, u, testOCIRepository+"/"+d)
		}
		rref, _ := rspec["ref"].(map[string]any)
		if got := str(rref["tag"]); got != tag {
			t.Errorf("OCIRepository %s: ref.tag %q, want %q", d, got, tag)
		}
	}
}

// TestDeliveryScenarioInvariants checks the delivery invariants on the
// delivery scenarios.
func TestDeliveryScenarioInvariants(t *testing.T) {
	for _, s := range deliveryScenarios {
		t.Run(s.name, func(t *testing.T) {
			out := t.TempDir()
			stdout := buildScenario(t, s, out)
			checkDelivery(t, out, "shop", s.tag, stdout)
		})
	}
}

// TestDeliveryAllFixtures checks the delivery invariants on every build
// fixture TestFixtures runs (testdata/*/app.yaml), so the partition and the
// kustomize build hold for every component and trait those fixtures cover.
func TestDeliveryAllFixtures(t *testing.T) {
	scenarios, err := filepath.Glob("testdata/*/app.yaml")
	if err != nil || len(scenarios) == 0 {
		t.Fatalf("globbing fixtures: %v (%d found)", err, len(scenarios))
	}
	for _, appPath := range scenarios {
		dir := filepath.Dir(appPath)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			base := []string{"build", appPath, "--profile", filepath.Join(dir, "cluster.yaml")}
			if vp := filepath.Join(dir, "values.yaml"); fileExists(vp) {
				base = append(base, "--values", vp)
			}
			stdout, err := runKurel(t, base...)
			if err != nil {
				t.Fatalf("stdout build: %v", err)
			}
			out := t.TempDir()
			if _, err := runKurel(t, append(base, "-o", out, "--oci-repository", testOCIRepository)...); err != nil {
				t.Fatalf("delivery build: %v", err)
			}
			data, err := os.ReadFile(appPath)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			checkDelivery(t, out, doc.Metadata.Name, "", []byte(stdout))
		})
	}
}

// TestDeliveryDependsOn checks that each per-component Kustomization of the
// dependency scenario depends on exactly what the policy and the tier order
// say: api on cache (policy) and db (services before apps), cache on db
// (tier order), db on nothing.
func TestDeliveryDependsOn(t *testing.T) {
	out := t.TempDir()
	buildScenario(t, deliveryScenarios[2], out)
	data, err := os.ReadFile(filepath.Join(out, "shop.flux.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, o := range decodeDocs(t, data) {
		if str(o["kind"]) != "Kustomization" {
			continue
		}
		md, _ := o["metadata"].(map[string]any)
		spec, _ := o["spec"].(map[string]any)
		deps, _ := spec["dependsOn"].([]any)
		names := []string{}
		for _, d := range deps {
			m, _ := d.(map[string]any)
			names = append(names, str(m["name"]))
		}
		got[str(md["name"])] = names
	}
	want := map[string][]string{
		"shop-db":    {},
		"shop-cache": {"shop-db"},
		"shop-api":   {"shop-cache", "shop-db"},
	}
	if len(got) != len(want) {
		t.Fatalf("Kustomizations %v, want %v", got, want)
	}
	for name, w := range want {
		if !slices.Equal(got[name], w) {
			t.Errorf("Kustomization %s dependsOn %v, want %v", name, got[name], w)
		}
	}
}

// TestDeliveryEmptyArtifactDropsStaleManifests builds the hierarchical
// scenario into a directory where its umbrella artifact already holds a
// manifests.yaml: the umbrella carries no objects, so the file must go.
func TestDeliveryEmptyArtifactDropsStaleManifests(t *testing.T) {
	out := t.TempDir()
	stale := filepath.Join(out, "shop", artifactManifestsFile)
	if err := os.MkdirAll(filepath.Dir(stale), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	buildScenario(t, deliveryScenarios[1], out)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale %s survived the build (stat error %v)", stale, err)
	}
}

// emptyAppYAML is an application whose only component renders no objects: a
// helmtemplate component over a chart with no templates; %s is the chart
// repository url.
const emptyAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: empty
  namespace: empty
spec:
  components:
    - name: nothing
      type: helmtemplate
      properties:
        chart: emptychart
        version: "0.1.0"
        source:
          url: %s
`

// TestDeliveryEmptyBuild checks that a build rendering no objects still writes
// the delivery output — the bundle's empty artifact and its Flux objects —
// and drops a manifests.yaml an earlier build left in that artifact, while the
// same build without --oci-repository keeps its behaviour: a warning, and
// nothing written.
func TestDeliveryEmptyBuild(t *testing.T) {
	chartBuf := buildMinimalChartTar(t, "emptychart", "0.1.0", map[string]string{
		"emptychart/templates/NOTES.txt": "renders no objects\n",
	})
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			_, _ = fmt.Fprint(w, helmIndexYAML("emptychart", "0.1.0", srvURL+"/emptychart-0.1.0.tgz"))
		case "/emptychart-0.1.0.tgz":
			_, _ = w.Write(chartBuf)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(emptyAppYAML, srvURL))
	profilePath := filepath.Join(deliveryTestdata, "cluster.yaml")

	stdout, err := runKurel(t, "build", appPath, "--profile", profilePath)
	if err != nil {
		t.Fatalf("stdout build: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout build of an empty application wrote:\n%s", stdout)
	}

	plain := filepath.Join(t.TempDir(), "out")
	if _, err := runKurel(t, "build", appPath, "--profile", profilePath, "-o", plain); err != nil {
		t.Fatalf("-o build without delivery: %v", err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("-o build without delivery created %s (stat error %v)", plain, err)
	}

	out := t.TempDir()
	stale := filepath.Join(out, "empty", artifactManifestsFile)
	if err := os.MkdirAll(filepath.Dir(stale), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runKurel(t, "build", appPath, "--profile", profilePath, "-o", out, "--oci-repository", testOCIRepository); err != nil {
		t.Fatalf("delivery build: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale %s survived the build (stat error %v)", stale, err)
	}
	kust, err := os.ReadFile(filepath.Join(out, "empty", artifactKustomizationFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(kust) != emptyArtifactKustomization {
		t.Errorf("empty artifact kustomization.yaml:\n%s\nwant:\n%s", kust, emptyArtifactKustomization)
	}
	checkDelivery(t, out, "empty", "", nil)
}

// collisionAppYAML is an application "shop", one reconciliation unit named
// "shop" whose delivery objects are Kustomization and OCIRepository
// flux-system/shop; %s is the application namespace and %s the components.
const collisionAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: %s
spec:
  components:
%s`

// ociShopComponent is an oci component named like the unit: it renders its
// own OCIRepository and Kustomization "shop" in the application namespace.
const ociShopComponent = `    - name: shop
      type: oci
      properties:
        source:
          url: oci://registry.example.com/manifests/shop
        version: "1.0.0"
`

// configMapShopComponent is a webservice with a ConfigMap named like the unit.
const configMapShopComponent = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: configmap
          properties:
            name: shop
            data:
              KEY: value
`

// TestDeliveryRefusesArtifactCollision checks that a build whose artifact
// carries an object with a delivery object's identity is refused before
// anything is written: an oci component named like its unit, in the Flux
// namespace, renders the OCIRepository and Kustomization the delivery output
// generates for that unit.
func TestDeliveryRefusesArtifactCollision(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(collisionAppYAML, "flux-system", ociShopComponent))
	out := filepath.Join(t.TempDir(), "out")
	stdout, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
		"-o", out, "--oci-repository", testOCIRepository)
	if err == nil {
		t.Fatal("build accepted an artifact object with a delivery object's identity")
	}
	t.Logf("refused: %v", err)
	for _, want := range []string{`artifact "shop"`, "flux-system/shop", "rename the component"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if stdout != "" {
		t.Errorf("wrote to stdout before refusing:\n%s", stdout)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("created the output directory before refusing (stat error %v)", err)
	}
}

// listMemberComponent is a manifests component whose inline manifest is the
// envelope %s: kurel's own manifest parsing expands the outer ObjectsList, so
// the artifact carries the envelope, with an OCIRepository flux-system/%s
// somewhere inside it.
const listMemberComponent = `    - name: wrapped
      type: manifests
      properties:
        inline: |
          apiVersion: example.com/v1
          kind: ObjectsList
          items:
%s`

// ociRepositoryItem is an OCIRepository flux-system/%s as a YAML sequence
// item, indented by %s.
const ociRepositoryItem = `%[2]s- apiVersion: source.toolkit.fluxcd.io/v1
%[2]s  kind: OCIRepository
%[2]s  metadata:
%[2]s    name: %[1]s
%[2]s    namespace: flux-system
%[2]s  spec:
%[2]s    interval: 1m
%[2]s    url: oci://registry.example.com/other
`

// listEnvelopeItem is a %s envelope (apiVersion and kind) named "wrapped" as
// a YAML sequence item, indented by %s, whose items follow.
const listEnvelopeItem = `%[3]s- apiVersion: %[1]s
%[3]s  kind: %[2]s
%[3]s  metadata:
%[3]s    name: wrapped
%[3]s    namespace: flux-system
%[3]s  items:
`

// listMemberComponents are the envelopes an artifact object can carry an
// OCIRepository flux-system/<member> in, one component per shape.
func listMemberComponents(member string) map[string]string {
	const indent = "            "
	return map[string]string{
		// The review's reproducer: a v1 List, which kustomize expands.
		"List": fmt.Sprintf(listMemberComponent,
			fmt.Sprintf(listEnvelopeItem, "v1", "List", indent)+
				fmt.Sprintf(ociRepositoryItem, member, indent+"    ")),
		"nested List": fmt.Sprintf(listMemberComponent,
			fmt.Sprintf(listEnvelopeItem, "v1", "List", indent)+
				fmt.Sprintf(listEnvelopeItem, "v1", "List", indent+"    ")+
				fmt.Sprintf(ociRepositoryItem, member, indent+"        ")),
		// Not a *List kind: kustomize passes it through, and kustomize-
		// controller's decoder expands it.
		"items envelope of another kind": fmt.Sprintf(listMemberComponent,
			fmt.Sprintf(listEnvelopeItem, "example.com/v1", "Bundle", indent)+
				fmt.Sprintf(ociRepositoryItem, member, indent+"    ")),
		// A member with an items array of its own: kustomize-controller's
		// decoder expands only the outer envelope, so it applies the member.
		"member with an empty items array": fmt.Sprintf(listMemberComponent,
			fmt.Sprintf(listEnvelopeItem, "example.com/v1", "Bundle", indent)+
				fmt.Sprintf(ociRepositoryItem, member, indent+"    ")+
				indent+"      items: []\n"),
	}
}

// TestDeliveryRefusesListMemberCollision checks that the refusal covers an
// object carried inside a list envelope, which reconciliation applies as its
// members: the build is refused and writes nothing. The accepted control, a
// member named like no delivery object, shows the envelope does reach the
// artifact.
func TestDeliveryRefusesListMemberCollision(t *testing.T) {
	profile := filepath.Join(deliveryTestdata, "cluster.yaml")
	for shape, component := range listMemberComponents("shop") {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(collisionAppYAML, "flux-system", component))
			out := filepath.Join(t.TempDir(), "out")
			stdout, err := runKurel(t, "build", appPath, "--profile", profile,
				"-o", out, "--oci-repository", testOCIRepository)
			if err == nil {
				t.Fatal("build accepted a list member with a delivery object's identity")
			}
			t.Logf("refused: %v", err)
			for _, want := range []string{`artifact "shop"`, "OCIRepository.source.toolkit.fluxcd.io flux-system/shop", "a member of list", "rename the component"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if stdout != "" {
				t.Errorf("wrote to stdout before refusing:\n%s", stdout)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("created the output directory before refusing (stat error %v)", err)
			}
		})
	}
	for shape, component := range listMemberComponents("other") {
		t.Run("control/"+shape, func(t *testing.T) {
			dir := t.TempDir()
			appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(collisionAppYAML, "flux-system", component))
			out := t.TempDir()
			if _, err := runKurel(t, "build", appPath, "--profile", profile,
				"-o", out, "--oci-repository", testOCIRepository); err != nil {
				t.Fatalf("delivery build refused: %v", err)
			}
			manifests, err := os.ReadFile(filepath.Join(out, "shop", artifactManifestsFile))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(manifests), "name: other") || !strings.Contains(string(manifests), "items:") {
				t.Errorf("artifact does not carry the envelope with its member:\n%s", manifests)
			}
		})
	}
}

// readObjects is kustomize-controller's decoder, ReadObjects in
// github.com/fluxcd/pkg/ssa v0.76.2 (utils/object.go:44-77, with
// IsKubernetesObject and IsKustomization from utils/is.go:71-82 inlined),
// copied: the module is not a dependency of this one.
func readObjects(r io.Reader) ([]*unstructured.Unstructured, error) {
	reader := yamlutil.NewYAMLOrJSONDecoder(r, 2048)
	objects := make([]*unstructured.Unstructured, 0)
	for {
		obj := &unstructured.Unstructured{}
		err := reader.Decode(obj)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return objects, err
		}
		if obj.IsList() {
			err = obj.EachListItem(func(item k8sruntime.Object) error {
				objects = append(objects, item.(*unstructured.Unstructured))
				return nil
			})
			if err != nil {
				return objects, err
			}
			continue
		}
		isKubernetesObject := obj.GetName() != "" && obj.GetKind() != "" && obj.GetAPIVersion() != ""
		isKustomization := strings.ToLower(obj.GetKind()) == "kustomization" &&
			strings.HasPrefix(obj.GetAPIVersion(), "kustomize.config.k8s.io/")
		if isKubernetesObject && !isKustomization {
			objects = append(objects, obj)
		}
	}
	return objects, nil
}

// TestAppliedObjectsMatchReconciliation checks that appliedObjects, the set
// checkCollisions gives ownership to, is exactly what reconciling an artifact
// applies: the artifact built with kustomize (krusty), and kustomize's output
// decoded with kustomize-controller's ReadObjects (readObjects). Both are
// compared as multisets of identities, and against want, for every shape; a
// shape kustomize refuses to build must be refused by appliedObjects too.
func TestAppliedObjectsMatchReconciliation(t *testing.T) {
	obj := func(apiVersion, kind, name string) map[string]any {
		return map[string]any{
			"apiVersion": apiVersion, "kind": kind,
			"metadata": map[string]any{"name": name, "namespace": "flux-system"},
		}
	}
	with := func(o map[string]any, key string, value any) map[string]any {
		c := maps.Clone(o)
		c[key] = value
		return c
	}
	list := func(apiVersion, kind string, items ...any) map[string]any {
		return with(obj(apiVersion, kind, "wrapped"), "items", items)
	}
	oci := obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "shop")
	cm := obj("v1", "ConfigMap", "cm")
	const (
		ociID     = "OCIRepository.source.toolkit.fluxcd.io flux-system/shop"
		cmID      = "ConfigMap flux-system/cm"
		bundleID  = "Bundle.example.com flux-system/wrapped"
		widgetsID = "WidgetList.example.com flux-system/wrapped"
	)
	tests := []struct {
		name   string
		object map[string]any
		// want is the applied identities; wantErr that kustomize refuses
		// the artifact.
		want    []string
		wantErr bool
	}{
		{name: "plain object", object: oci, want: []string{ociID}},
		{name: "List", object: list("v1", "List", oci, cm), want: []string{ociID, cmID}},
		{name: "nested List", object: list("v1", "List", list("v1", "List", oci)), want: []string{ociID}},
		{name: "custom *List kind", object: list("example.com/v1", "WidgetList", oci), want: []string{ociID}},
		{name: "*List kind without items", object: obj("example.com/v1", "WidgetList", "wrapped"), want: []string{widgetsID}},
		{name: "List with null items", object: with(obj("v1", "List", "wrapped"), "items", nil)},
		{name: "List with an empty item", object: list("v1", "List", map[string]any{}, oci), want: []string{ociID}},
		{name: "items envelope of another kind", object: list("example.com/v1", "Bundle", oci), want: []string{ociID}},
		{name: "another kind inside a List", object: list("v1", "List", list("example.com/v1", "Bundle", oci)), want: []string{ociID}},
		// The review's reproducer: the decoder expands the Bundle one level
		// and applies the OCIRepository as it is, items and all.
		{name: "member with an empty items array", object: list("example.com/v1", "Bundle", with(oci, "items", []any{})), want: []string{ociID}},
		{name: "envelope of another kind inside another", object: list("example.com/v1", "Bundle", list("example.com/v1", "Bundle", oci)), want: []string{bundleID}},
		// Kept by kustomize, then expanded to nothing by the decoder.
		{name: "object with an empty items array", object: with(oci, "items", []any{})},
		{name: "List member with an empty items array", object: list("v1", "List", with(oci, "items", []any{}))},
		{name: "object with items that are not an array", object: with(oci, "items", "x"), want: []string{ociID}},
		// Built by kustomize, then dropped by the decoder.
		{name: "object without an apiVersion", object: with(cm, "apiVersion", "")},
		{name: "kustomize config Kustomization", object: obj("kustomize.config.k8s.io/v1beta1", "Kustomization", "k")},
		// A member the decoder expands is applied whatever it lacks.
		{name: "member without an apiVersion", object: list("example.com/v1", "Bundle", with(cm, "apiVersion", "")), want: []string{"ConfigMap flux-system/cm"}},
		{name: "List with items that are not an array", object: with(obj("v1", "List", "wrapped"), "items", "x"), wantErr: true},
		{name: "List with an item that is not an object", object: list("v1", "List", "x"), wantErr: true},
	}
	// Each shape is built alone, and beside a companion ConfigMap: kustomize
	// reads a manifests.yaml whose only document is a List through a reader
	// that unwraps it, and any other through inlineAnyEmbeddedLists. Both
	// yield the same members; only the second refuses a malformed List, so a
	// refused shape is built beside the companion only.
	companion := obj("v1", "ConfigMap", "companion")
	const companionID = "ConfigMap flux-system/companion"
	for _, tt := range tests {
		for _, beside := range []bool{false, true} {
			if tt.wantErr && !beside {
				continue
			}
			name := tt.name
			objects := []map[string]any{tt.object}
			want := slices.Clone(tt.want)
			if beside {
				name += "/beside another object"
				objects = append(objects, companion)
				want = append(want, companionID)
			}
			t.Run(name, func(t *testing.T) {
				var artifact []*client.Object
				var model []string
				var checkErr error
				for _, content := range objects {
					o := client.Object(&unstructured.Unstructured{Object: content})
					artifact = append(artifact, &o)
					applied, err := appliedObjects(o)
					if err != nil {
						checkErr = err
					}
					for _, m := range applied {
						model = append(model, identityOf(m.object).String())
					}
				}
				d := &deliveryOutput{artifacts: []deliveryArtifact{{name: "a", objects: artifact}}}
				out := t.TempDir()
				if err := d.write(out, "app"); err != nil {
					t.Fatal(err)
				}
				res, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), filepath.Join(out, "a"))
				if tt.wantErr {
					if err == nil {
						t.Error("kustomize built the artifact")
					}
					if checkErr == nil {
						t.Error("appliedObjects accepted what kustomize refuses")
					}
					return
				}
				if err != nil {
					t.Fatalf("kustomize build: %v", err)
				}
				if checkErr != nil {
					t.Fatalf("appliedObjects: %v", checkErr)
				}
				built, err := res.AsYaml()
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := readObjects(bytes.NewReader(built))
				if err != nil {
					t.Fatalf("ReadObjects: %v", err)
				}
				var applied []string
				for _, u := range decoded {
					applied = append(applied, identityOf(u).String())
				}
				for _, s := range [][]string{applied, model, want} {
					slices.Sort(s)
				}
				if !slices.Equal(applied, want) {
					t.Errorf("reconciliation applies %q, want %q", applied, want)
				}
				if !slices.Equal(model, applied) {
					t.Errorf("appliedObjects gives ownership to %q, reconciliation applies %q", model, applied)
				}
			})
		}
	}
}

// TestCheckCollisions checks the identity checkCollisions compares against the
// delivery objects: API group, kind, exact namespace and name, not the
// version; and that it compares exactly what reconciliation applies: the
// members of an envelope it expands, not the envelope, and a member with an
// items array of its own as itself.
func TestCheckCollisions(t *testing.T) {
	obj := func(apiVersion, kind, namespace, name string) *client.Object {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(apiVersion)
		u.SetKind(kind)
		u.SetNamespace(namespace)
		u.SetName(name)
		o := client.Object(u)
		return &o
	}
	list := func(apiVersion, kind string, items ...*client.Object) *client.Object {
		u := (*obj(apiVersion, kind, "flux-system", "wrapped")).(*unstructured.Unstructured)
		content := make([]any, 0, len(items))
		for _, i := range items {
			content = append(content, (*i).(*unstructured.Unstructured).Object)
		}
		u.Object["items"] = content
		o := client.Object(u)
		return &o
	}
	flux := []*client.Object{
		obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "flux-system", "shop"),
		obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "flux-system", "shop"),
	}
	source := func() *client.Object {
		return obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "flux-system", "shop")
	}
	// sourceWithItems is the delivery source with an empty items array.
	sourceWithItems := func() *client.Object {
		o := source()
		(*o).(*unstructured.Unstructured).Object["items"] = []any{}
		return o
	}
	tests := []struct {
		name    string
		object  *client.Object
		collide bool
	}{
		{"same Kustomization", obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "flux-system", "shop"), true},
		{"same OCIRepository at another version", obj("source.toolkit.fluxcd.io/v1beta2", "OCIRepository", "flux-system", "shop"), true},
		{"other namespace", obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "apps", "shop"), false},
		{"no namespace", obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "", "shop"), false},
		{"other name", obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "flux-system", "shop-db"), false},
		{"other group, same kind", obj("kustomize.config.k8s.io/v1beta1", "Kustomization", "flux-system", "shop"), false},
		{"other kind", obj("v1", "ConfigMap", "flux-system", "shop"), false},
		{"List member", list("v1", "List", obj("v1", "ConfigMap", "flux-system", "cm"), source()), true},
		{"nested List member", list("v1", "List", list("v1", "List", source())), true},
		{"member of an items envelope of another kind", list("example.com/v1", "Bundle", source()), true},
		{"List member in another namespace", list("v1", "List", obj("source.toolkit.fluxcd.io/v1", "OCIRepository", "apps", "shop")), false},
		{"empty List", list("v1", "List"), false},
		// An envelope is never applied, only its members are: an items
		// envelope with the delivery Kustomization's own identity collides
		// with nothing.
		{"items envelope with a delivery object's identity", func() *client.Object {
			o := list("kustomize.toolkit.fluxcd.io/v1", "Kustomization", obj("v1", "ConfigMap", "flux-system", "cm"))
			(*o).SetName("shop")
			return o
		}(), false},
		// kustomize-controller's decoder expands one level only, so a
		// member with an items array of its own is applied as itself.
		{"member with an empty items array", list("example.com/v1", "Bundle", sourceWithItems()), true},
		{"List member with an items array inside an envelope", list("v1", "List", list("example.com/v1", "Bundle", sourceWithItems())), true},
		// kustomize keeps it and the decoder expands it to nothing.
		{"delivery object's identity with an empty items array", sourceWithItems(), false},
		// The inner envelope is applied as itself, so its member is not.
		{"member of an envelope inside another", list("example.com/v1", "Bundle", list("example.com/v1", "Bundle", source())), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &deliveryOutput{
				artifacts: []deliveryArtifact{{name: "shop", objects: []*client.Object{tt.object}}},
				flux:      flux,
			}
			err := d.checkCollisions()
			if tt.collide && err == nil {
				t.Error("collision accepted")
			}
			if !tt.collide && err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}

// TestCheckCollisionsBetweenArtifacts checks that checkCollisions refuses an
// object identity carried by two artifacts, or twice by one, and names where
// each is carried.
func TestCheckCollisionsBetweenArtifacts(t *testing.T) {
	obj := func(apiVersion, kind, namespace, name string) *client.Object {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(apiVersion)
		u.SetKind(kind)
		u.SetNamespace(namespace)
		u.SetName(name)
		o := client.Object(u)
		return &o
	}
	// list is an unnamed v1 List envelope, as list envelopes usually are.
	list := func(items ...*client.Object) *client.Object {
		content := make([]any, 0, len(items))
		for _, i := range items {
			content = append(content, (*i).(*unstructured.Unstructured).Object)
		}
		o := client.Object(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List", "items": content,
		}})
		return &o
	}
	nullList := func() *client.Object {
		o := client.Object(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List", "items": nil,
		}})
		return &o
	}
	cm := func(namespace string) *client.Object { return obj("v1", "ConfigMap", namespace, "settings") }
	// envelope is an unnamed items envelope that is not a *List kind.
	envelope := func(items ...*client.Object) *client.Object {
		o := list(items...)
		(*o).(*unstructured.Unstructured).SetAPIVersion("example.com/v1")
		(*o).(*unstructured.Unstructured).SetKind("Bundle")
		return o
	}
	withItems := func(o *client.Object) *client.Object {
		(*o).(*unstructured.Unstructured).Object["items"] = []any{}
		return o
	}
	tests := []struct {
		name string
		a, b []*client.Object
		// want is the refusal's text, empty when the output is accepted.
		want []string
	}{
		{"same object in two artifacts", []*client.Object{cm("shop")}, []*client.Object{cm("shop")},
			[]string{`artifacts "a" and "b" both carry ConfigMap shop/settings`}},
		{"same object at another version", []*client.Object{obj("example.com/v1", "Widget", "shop", "w")}, []*client.Object{obj("example.com/v2", "Widget", "shop", "w")},
			[]string{`artifacts "a" and "b" both carry Widget.example.com shop/w`}},
		{"same cluster-scoped object", []*client.Object{obj("v1", "Namespace", "", "shop")}, []*client.Object{obj("v1", "Namespace", "", "shop")},
			[]string{`artifacts "a" and "b" both carry Namespace /shop`}},
		{"a List member and an object", []*client.Object{cm("shop")}, []*client.Object{list(cm("shop"))},
			[]string{`artifacts "a" and "b" both carry ConfigMap shop/settings (a member of list List /)`}},
		{"same object twice in one artifact", []*client.Object{cm("shop"), cm("shop")}, nil,
			[]string{`artifact "a" carries ConfigMap shop/settings twice`}},
		{"a List member and an object in one artifact", []*client.Object{list(cm("shop")), cm("shop")}, nil,
			[]string{`artifact "a" carries ConfigMap shop/settings twice (also as ConfigMap shop/settings (a member of list List /))`}},
		// kustomize builds it (TestCheckKustomizeBuilds), and reconciling it
		// would apply the one object twice.
		{"same object at two versions in one artifact", []*client.Object{obj("example.com/v1", "Widget", "shop", "w"), obj("example.com/v2", "Widget", "shop", "w")}, nil,
			[]string{`artifact "a" carries Widget.example.com shop/w twice: reconciling it would apply that one object twice`}},
		{"a member with an empty items array and an object", []*client.Object{cm("shop")}, []*client.Object{envelope(withItems(cm("shop")))},
			[]string{`artifacts "a" and "b" both carry ConfigMap shop/settings (a member of list Bundle.example.com /)`}},
		{"other namespace", []*client.Object{cm("shop")}, []*client.Object{cm("other")}, nil},
		{"other kind", []*client.Object{cm("shop")}, []*client.Object{obj("v1", "Secret", "shop", "settings")}, nil},
		{"other group", []*client.Object{obj("example.com/v1", "Widget", "shop", "w")}, []*client.Object{obj("example.org/v1", "Widget", "shop", "w")}, nil},
		{"unnamed List envelopes of other members", []*client.Object{list(cm("shop"))}, []*client.Object{list(cm("other"))}, nil},
		// kustomize drops a *List with null items, so it applies nothing.
		{"unnamed Lists with null items", []*client.Object{nullList()}, []*client.Object{nullList()}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &deliveryOutput{artifacts: []deliveryArtifact{{name: "a", objects: tt.a}, {name: "b", objects: tt.b}}}
			err := d.checkCollisions()
			if tt.want == nil {
				if err != nil {
					t.Errorf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

// TestKustomizeRefusesDuplicateArtifactObject pins the kustomize behaviour
// checkKustomizeBuilds mirrors to refuse one object twice in one artifact: the
// artifact, written without the check, does not build.
func TestKustomizeRefusesDuplicateArtifactObject(t *testing.T) {
	cm := func() *client.Object {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("v1")
		u.SetKind("ConfigMap")
		u.SetNamespace("shop")
		u.SetName("settings")
		o := client.Object(u)
		return &o
	}
	d := &deliveryOutput{artifacts: []deliveryArtifact{{name: "a", objects: []*client.Object{cm(), cm()}}}}
	out := t.TempDir()
	if err := d.write(out, "app"); err != nil {
		t.Fatal(err)
	}
	_, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), filepath.Join(out, "a"))
	if err == nil || !strings.Contains(err.Error(), "already registered id") {
		t.Fatalf("kustomize build: %v, want the duplicate id refused", err)
	}
}

// TestCheckKustomizeBuilds checks that checkKustomizeBuilds refuses exactly
// the artifacts kustomize refuses to build for a duplicate resource id: each
// row's artifact is written without the check and built with kustomize
// (krusty), and the check must refuse it if and only if kustomize does,
// naming the id kustomize names.
func TestCheckKustomizeBuilds(t *testing.T) {
	obj := func(apiVersion, kind, namespace, name string) map[string]any {
		md := map[string]any{"name": name}
		if namespace != "" {
			md["namespace"] = namespace
		}
		o := map[string]any{"kind": kind, "metadata": md}
		if apiVersion != "" {
			o["apiVersion"] = apiVersion
		}
		return o
	}
	envelope := func(apiVersion, kind string, items ...map[string]any) map[string]any {
		o := obj(apiVersion, kind, "shop", "wrapped")
		content := make([]any, 0, len(items))
		for _, i := range items {
			content = append(content, i)
		}
		o["items"] = content
		return o
	}
	const kustomizeConfig = "kustomize.config.k8s.io/v1beta1"
	kustomization := func(apiVersion, namespace string) map[string]any {
		return obj(apiVersion, "Kustomization", namespace, "duplicate")
	}
	cm := func(namespace string) map[string]any { return obj("v1", "ConfigMap", namespace, "settings") }
	tests := []struct {
		name    string
		objects []map[string]any
		// id is the resource id kustomize refuses to add twice; empty when
		// kustomize builds the artifact.
		id string
	}{
		// The review's reproducer: both are dropped by kustomize-controller's
		// decoder, so neither owns an identity in checkCollisions.
		{"identical kustomize config Kustomizations",
			[]map[string]any{kustomization(kustomizeConfig, "shop"), kustomization(kustomizeConfig, "shop")},
			"Kustomization.v1beta1.kustomize.config.k8s.io/duplicate.shop"},
		{"identical objects without an apiVersion",
			[]map[string]any{obj("", "ConfigMap", "shop", "cm"), obj("", "ConfigMap", "shop", "cm")},
			"ConfigMap.[noVer].[noGrp]/cm.shop"},
		{"identical ConfigMaps", []map[string]any{cm("shop"), cm("shop")}, "ConfigMap.v1.[noGrp]/settings.shop"},
		{"a List member and an object", []map[string]any{envelope("v1", "List", cm("shop")), cm("shop")},
			"ConfigMap.v1.[noGrp]/settings.shop"},
		// kustomize reads no namespace as "default" and ignores a
		// cluster-scoped kind's namespace.
		{"no namespace and the default namespace", []map[string]any{cm(""), cm("default")},
			"ConfigMap.v1.[noGrp]/settings.default"},
		{"a cluster-scoped kind in two namespaces",
			[]map[string]any{obj("v1", "Namespace", "a", "shop"), obj("v1", "Namespace", "b", "shop")},
			"Namespace.v1.[noGrp]/shop.b"},
		// Near misses: one name, another kustomize resource id.
		{"kustomize config Kustomizations in two namespaces",
			[]map[string]any{kustomization(kustomizeConfig, "shop"), kustomization(kustomizeConfig, "apps")}, ""},
		{"kustomize config Kustomizations at two versions",
			[]map[string]any{kustomization(kustomizeConfig, "shop"), kustomization("kustomize.config.k8s.io/v1alpha1", "shop")}, ""},
		{"one object at two API versions",
			[]map[string]any{obj("example.com/v1", "Widget", "shop", "w"), obj("example.com/v2", "Widget", "shop", "w")}, ""},
		{"a namespaced kind in two namespaces", []map[string]any{cm("shop"), cm("apps")}, ""},
		// kustomize reads an items envelope that is not a *List kind as one
		// resource; checkCollisions refuses its two members.
		{"two members of an envelope of another kind",
			[]map[string]any{envelope("example.com/v1", "Bundle", cm("shop"), cm("shop"))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var artifact []*client.Object
			for _, content := range tt.objects {
				o := client.Object(&unstructured.Unstructured{Object: content})
				artifact = append(artifact, &o)
			}
			d := &deliveryOutput{artifacts: []deliveryArtifact{{name: "a", objects: artifact}}}
			checkErr := d.checkKustomizeBuilds()
			out := t.TempDir()
			if err := d.write(out, "app"); err != nil {
				t.Fatal(err)
			}
			_, buildErr := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), filepath.Join(out, "a"))
			if tt.id == "" {
				if buildErr != nil {
					t.Fatalf("kustomize build: %v", buildErr)
				}
				if checkErr != nil {
					t.Errorf("refused what kustomize builds: %v", checkErr)
				}
				return
			}
			if buildErr == nil || !strings.Contains(buildErr.Error(), "already registered id: "+tt.id) {
				t.Fatalf("kustomize build: %v, want %s refused as a duplicate", buildErr, tt.id)
			}
			if checkErr == nil {
				t.Fatal("accepted what kustomize refuses")
			}
			for _, want := range []string{`artifact "a" would not build under kustomize`, tt.id} {
				if !strings.Contains(checkErr.Error(), want) {
					t.Errorf("error %q does not contain %q", checkErr, want)
				}
			}
		})
	}
}

// kustomizationsComponent is a manifests component whose inline manifests are
// two kustomize config Kustomizations "duplicate", in namespaces %s and %s.
const kustomizationsComponent = `    - name: configs
      type: manifests
      properties:
        inline: |
          apiVersion: kustomize.config.k8s.io/v1beta1
          kind: Kustomization
          metadata:
            name: duplicate
            namespace: %s
          ---
          apiVersion: kustomize.config.k8s.io/v1beta1
          kind: Kustomization
          metadata:
            name: duplicate
            namespace: %s
`

// TestDeliveryRefusesKustomizeDuplicate checks, through the CLI, that a build
// whose artifact kustomize would refuse for a duplicate resource id is refused
// before anything is written, though kustomize-controller's decoder would drop
// both objects: two identical kustomize config Kustomizations. The near miss,
// the two in different namespaces, is delivered, and its artifact builds.
func TestDeliveryRefusesKustomizeDuplicate(t *testing.T) {
	profile := filepath.Join(deliveryTestdata, "cluster.yaml")
	t.Run("identical", func(t *testing.T) {
		appPath := writeTempFile(t, t.TempDir(), "app.yaml",
			fmt.Sprintf(collisionAppYAML, "shop", fmt.Sprintf(kustomizationsComponent, "shop", "shop")))
		out := filepath.Join(t.TempDir(), "out")
		stdout, err := runKurel(t, "build", appPath, "--profile", profile, "-o", out, "--oci-repository", testOCIRepository)
		if err == nil {
			t.Fatal("build accepted an artifact kustomize refuses")
		}
		t.Logf("refused: %v", err)
		for _, want := range []string{`artifact "shop" would not build under kustomize`,
			"Kustomization.v1beta1.kustomize.config.k8s.io/duplicate.shop"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
		if stdout != "" {
			t.Errorf("wrote to stdout before refusing:\n%s", stdout)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("created the output directory before refusing (stat error %v)", err)
		}
	})
	t.Run("near miss in two namespaces", func(t *testing.T) {
		appPath := writeTempFile(t, t.TempDir(), "app.yaml",
			fmt.Sprintf(collisionAppYAML, "shop", fmt.Sprintf(kustomizationsComponent, "shop", "apps")))
		out := t.TempDir()
		if _, err := runKurel(t, "build", appPath, "--profile", profile, "-o", out, "--oci-repository", testOCIRepository); err != nil {
			t.Fatalf("delivery build refused: %v", err)
		}
		manifests, err := os.ReadFile(filepath.Join(out, "shop", artifactManifestsFile))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(manifests), "name: duplicate"); n != 2 {
			t.Errorf("artifact carries %d Kustomizations named duplicate, want 2:\n%s", n, manifests)
		}
		if _, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), filepath.Join(out, "shop")); err != nil {
			t.Errorf("kustomize build of the artifact: %v", err)
		}
	})
}

// sharedConfigMapComponents are two components, %s and %s, of the given
// types, each with a configmap trait "settings".
const sharedConfigMapComponents = `    - name: %[1]s
      type: %[2]s
      properties:
        image: ghcr.io/example/%[1]s:v1.0.0
        port: 8080
      traits:
        - type: configmap
          properties:
            name: settings
            data:
              KEY: %[1]s
    - name: %[3]s
      type: %[4]s
      properties:
        image: ghcr.io/example/%[3]s:v1.0.0
        port: 9090
      traits:
        - type: configmap
          properties:
            name: settings
            data:
              KEY: %[3]s
`

// TestDeliveryRefusesSharedArtifactObject checks, through the CLI, that a
// build is refused before anything is written when two components render one
// object: a configmap trait of one name on a daemonset (infra tier) and a
// webservice (apps tier), whose objects go to two artifacts, or on two
// webservices, whose objects go to one (the application has a single tier,
// so a single unit, "shop"); and a configmap trait on a daemonset with the
// same ConfigMap inside a list envelope in the apps artifact.
func TestDeliveryRefusesSharedArtifactObject(t *testing.T) {
	tests := []struct {
		name       string
		components string
		want       []string
	}{
		{"two artifacts", fmt.Sprintf(sharedConfigMapComponents, "agent", "daemonset", "web", "webservice"),
			[]string{`"shop-infra"`, `"shop-apps"`, "both carry ConfigMap shop/settings", "rename one of the components or traits"}},
		{"one artifact", fmt.Sprintf(sharedConfigMapComponents, "web", "webservice", "api", "webservice"),
			[]string{`artifact "shop" would not build under kustomize: it carries ConfigMap shop/settings twice`, "ConfigMap.v1.[noGrp]/settings.shop"}},
		// The apps artifact carries the ConfigMap as the member of an
		// envelope, with an empty items array of its own:
		// kustomize-controller's decoder expands only the envelope, so it
		// applies the ConfigMap there too.
		{"two artifacts, one as a member with an empty items array", `    - name: agent
      type: daemonset
      properties:
        image: ghcr.io/example/agent:v1.0.0
      traits:
        - type: configmap
          properties:
            name: settings
    - name: wrapped
      type: manifests
      properties:
        inline: |
          apiVersion: example.com/v1
          kind: ObjectsList
          items:
            - apiVersion: example.com/v1
              kind: Bundle
              metadata:
                name: wrapped
                namespace: shop
              items:
                - apiVersion: v1
                  kind: ConfigMap
                  metadata:
                    name: settings
                    namespace: shop
                  items: []
`, []string{`"shop-infra"`, `"shop-apps"`, "both carry ConfigMap shop/settings (a member of list Bundle.example.com shop/wrapped)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appPath := writeTempFile(t, t.TempDir(), "app.yaml", fmt.Sprintf(collisionAppYAML, "shop", tt.components))
			out := filepath.Join(t.TempDir(), "out")
			stdout, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
				"-o", out, "--oci-repository", testOCIRepository)
			if err == nil {
				t.Fatal("build accepted one object rendered by two components")
			}
			t.Logf("refused: %v", err)
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			if stdout != "" {
				t.Errorf("wrote to stdout before refusing:\n%s", stdout)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("created the output directory before refusing (stat error %v)", err)
			}
		})
	}
}

// TestDeliveryAcceptsSharedNameInOtherNamespace checks the near miss of
// TestDeliveryRefusesSharedArtifactObject: a ConfigMap "settings" in the
// application namespace in the infra artifact, and one in another namespace
// in the apps artifact, are two objects, and both are delivered.
func TestDeliveryAcceptsSharedNameInOtherNamespace(t *testing.T) {
	components := `    - name: agent
      type: daemonset
      properties:
        image: ghcr.io/example/agent:v1.0.0
      traits:
        - type: configmap
          properties:
            name: settings
    - name: extra
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: settings
            namespace: other
`
	appPath := writeTempFile(t, t.TempDir(), "app.yaml", fmt.Sprintf(collisionAppYAML, "shop", components))
	out := t.TempDir()
	if _, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
		"-o", out, "--oci-repository", testOCIRepository); err != nil {
		t.Fatalf("delivery build refused: %v", err)
	}
	for artifact, namespace := range map[string]string{"shop-infra": "shop", "shop-apps": "other"} {
		manifests, err := os.ReadFile(filepath.Join(out, artifact, artifactManifestsFile))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, o := range decodeDocs(t, manifests) {
			md, _ := o["metadata"].(map[string]any)
			if str(o["kind"]) == "ConfigMap" && str(md["name"]) == "settings" && str(md["namespace"]) == namespace {
				found = true
			}
		}
		if !found {
			t.Errorf("artifact %s holds no ConfigMap %s/settings:\n%s", artifact, namespace, manifests)
		}
	}
}

// dnsName returns a valid DNS-1123 subdomain of n characters: 63-character
// labels of 'a' joined by '.'.
func dnsName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
		if (i+1)%64 == 0 {
			b[i] = '.'
		}
	}
	return string(b)
}

// TestDeliveryRefusesOverlongFileName checks, through the CLI, the file name
// limit on the delivery output: the flat application (one unit, named like
// the application) builds with the longest name whose <app>.flux.yaml fits in
// 255 bytes, and one character more is refused before anything is written,
// though its url, <app>.yaml and artifact directory would all fit. The
// hierarchical shape's longer unit names are refused before any write too.
func TestDeliveryRefusesOverlongFileName(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(deliveryTestdata, "flat", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	hierarchical, err := os.ReadFile(filepath.Join(deliveryTestdata, "hierarchical", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	longest := maxFileNameBytes - len(fluxFileName(""))
	build := func(t *testing.T, app []byte, name, out string) error {
		t.Helper()
		appPath := writeTempFile(t, t.TempDir(), "app.yaml", strings.Replace(string(app), "name: shop", "name: "+name, 1))
		_, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
			"-o", out, "--oci-repository", testOCIRepository)
		return err
	}

	t.Run("longest accepted", func(t *testing.T) {
		out := t.TempDir()
		name := dnsName(longest)
		if err := build(t, data, name, out); err != nil {
			t.Fatalf("refused: %v", err)
		}
		if _, err := os.Stat(filepath.Join(out, fluxFileName(name))); err != nil {
			t.Errorf("Flux objects file not written: %v", err)
		}
	})
	for _, tt := range []struct {
		name, want string
		app        []byte
		length     int
	}{
		{"Flux objects file name", "shorten the application name to at most 245 characters", data, longest + 1},
		{"unit names", "more than 255", hierarchical, longest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			err := build(t, tt.app, dnsName(tt.length), out)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want one containing %q", err, tt.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("created the output directory before refusing (stat error %v)", err)
			}
		})
	}
}

// TestCheckFileNames checks the file name limit on each name the delivery
// output derives: <app>.flux.yaml and each artifact directory. write refuses
// an over-long one before creating anything.
func TestCheckFileNames(t *testing.T) {
	tests := []struct {
		name     string
		app      string
		artifact string
		ok       bool
	}{
		{"both at the limit", strings.Repeat("a", maxFileNameBytes-len(fluxFileName(""))), strings.Repeat("a", maxFileNameBytes), true},
		{"Flux objects file name over", strings.Repeat("a", maxFileNameBytes-len(fluxFileName(""))+1), "shop", false},
		{"artifact directory name over", "shop", strings.Repeat("a", maxFileNameBytes+1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &deliveryOutput{artifacts: []deliveryArtifact{{name: tt.artifact}}}
			if err := d.checkFileNames(tt.app); (err == nil) != tt.ok {
				t.Fatalf("checkFileNames: %v, want ok=%v", err, tt.ok)
			}
			if tt.ok {
				return
			}
			out := filepath.Join(t.TempDir(), "out")
			if err := d.write(out, tt.app); err == nil {
				t.Fatal("write accepted the name")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("write created the output directory before refusing (stat error %v)", err)
			}
		})
	}
}

// TestDeliveryAcceptsNearCollision checks artifact objects that share a
// delivery object's name but not its identity: the same kinds in another
// namespace, and another kind in the Flux namespace.
func TestDeliveryAcceptsNearCollision(t *testing.T) {
	tests := []struct {
		name, namespace, components string
	}{
		{"same kinds, other namespace", "shop", ociShopComponent},
		{"other kind, Flux namespace", "flux-system", configMapShopComponent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(collisionAppYAML, tt.namespace, tt.components))
			out := t.TempDir()
			if _, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
				"-o", out, "--oci-repository", testOCIRepository); err != nil {
				t.Fatalf("delivery build refused: %v", err)
			}
			manifests, err := os.ReadFile(filepath.Join(out, "shop", artifactManifestsFile))
			if err != nil {
				t.Fatal(err)
			}
			// The near miss is really in the artifact: an object named "shop".
			found := false
			for _, o := range decodeDocs(t, manifests) {
				md, _ := o["metadata"].(map[string]any)
				if str(md["name"]) == "shop" {
					found = true
				}
			}
			if !found {
				t.Errorf("artifact holds no object named shop:\n%s", manifests)
			}
		})
	}
}

// TestDeliveryFlagsAccepted checks --oci-repository and --oci-tag values the
// flag check must let through.
// reconciliationAppYAML is the flat fixture's application (one bundle, shop)
// with a reconciliation policy setting interval, retryInterval and timeout all
// to duration.
func reconciliationAppYAML(t *testing.T, duration string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(deliveryTestdata, "flat", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + fmt.Sprintf(`  policies:
    - name: flux
      type: reconciliation
      properties:
        interval: %[1]s
        retryInterval: %[1]s
        timeout: %[1]s
`, duration)
}

// TestDeliveryRefusesSubMillisecondDuration checks, through the CLI, that a
// reconciliation policy duration the policy accepts as authored but that a
// Kustomization writes outside Flux's duration pattern (0.5ms is written as
// 500µs) is refused before anything is written, and that the near misses at
// and above one millisecond are delivered as authored.
func TestDeliveryRefusesSubMillisecondDuration(t *testing.T) {
	profile := filepath.Join(deliveryTestdata, "cluster.yaml")
	t.Run("0.5ms refused", func(t *testing.T) {
		appPath := writeTempFile(t, t.TempDir(), "app.yaml", reconciliationAppYAML(t, "0.5ms"))
		out := filepath.Join(t.TempDir(), "out")
		stdout, err := runKurel(t, "build", appPath, "--profile", profile, "-o", out, "--oci-repository", testOCIRepository)
		if err == nil {
			t.Fatal("delivery build accepted a 0.5ms reconciliation interval")
		}
		for _, want := range []string{"Kustomization.kustomize.toolkit.fluxcd.io flux-system/shop", "spec.interval", `"500µs"`, "millisecond resolution"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
		if stdout != "" {
			t.Errorf("wrote to stdout before refusing:\n%s", stdout)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("created the output directory before refusing (stat error %v)", err)
		}
	})
	for _, duration := range []string{"1ms", "1.5s"} {
		t.Run(duration+" delivered", func(t *testing.T) {
			appPath := writeTempFile(t, t.TempDir(), "app.yaml", reconciliationAppYAML(t, duration))
			out := t.TempDir()
			if _, err := runKurel(t, "build", appPath, "--profile", profile, "-o", out, "--oci-repository", testOCIRepository); err != nil {
				t.Fatalf("delivery build refused: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(out, fluxFileName("shop")))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, o := range decodeDocs(t, data) {
				if str(o["kind"]) != "Kustomization" {
					continue
				}
				found = true
				spec, _ := o["spec"].(map[string]any)
				for _, field := range []string{"interval", "retryInterval", "timeout"} {
					if got := str(spec[field]); got != duration {
						t.Errorf("spec.%s = %q, want %q", field, got, duration)
					}
				}
			}
			if !found {
				t.Fatalf("no Kustomization in %s:\n%s", fluxFileName("shop"), data)
			}
		})
	}
}

// TestCheckDurations checks the duration check on every generated delivery
// object's metav1.Duration fields: each must be written (Duration.String())
// inside its Flux CRD pattern, an unset one is skipped, a written 0s is left
// alone, and an object type with unknown duration fields is refused.
func TestCheckDurations(t *testing.T) {
	d := func(v time.Duration) *metav1.Duration { return &metav1.Duration{Duration: v} }
	kust := func(interval time.Duration, retry, timeout *metav1.Duration) client.Object {
		k := &kustv1.Kustomization{}
		k.Name = "shop"
		k.Spec.Interval = metav1.Duration{Duration: interval}
		k.Spec.RetryInterval = retry
		k.Spec.Timeout = timeout
		return k
	}
	oci := func(interval time.Duration, timeout *metav1.Duration) client.Object {
		r := &sourcev1.OCIRepository{}
		r.Name = "shop"
		r.Spec.Interval = metav1.Duration{Duration: interval}
		r.Spec.Timeout = timeout
		return r
	}
	tests := []struct {
		name string
		obj  client.Object
		want string // "" accepts
	}{
		{"Kustomization defaults", kust(time.Hour, nil, nil), ""},
		{"Kustomization at one millisecond", kust(time.Millisecond, d(time.Millisecond), d(time.Millisecond)), ""},
		{"Kustomization fractional seconds", kust(1500*time.Millisecond, d(1500*time.Microsecond), d(90*time.Minute)), ""},
		{"Kustomization zero", kust(0, d(0), d(0)), ""},
		{"Kustomization interval below 1ms", kust(500*time.Microsecond, nil, nil), `spec.interval is written as "500µs"`},
		{"Kustomization retryInterval below 1ms", kust(time.Minute, d(999*time.Nanosecond), nil), `spec.retryInterval is written as "999ns"`},
		{"Kustomization timeout below 1ms", kust(time.Minute, nil, d(time.Nanosecond)), `spec.timeout is written as "1ns"`},
		{"Kustomization negative timeout", kust(time.Minute, nil, d(-time.Second)), `spec.timeout is written as "-1s"`},
		{"OCIRepository default", oci(time.Hour, nil), ""},
		{"OCIRepository interval below 1ms", oci(500*time.Microsecond, nil), `spec.interval is written as "500µs"`},
		{"OCIRepository timeout in minutes", oci(time.Hour, d(59*time.Minute)), ""},
		{"OCIRepository timeout in hours", oci(time.Hour, d(time.Hour)), `spec.timeout is written as "1h0m0s"`},
		{"unexpected type", &unstructured.Unstructured{}, "unexpected delivery object type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &deliveryOutput{flux: []*client.Object{&tt.obj}}
			err := out.checkDurations()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestDeliveryFlagsAccepted(t *testing.T) {
	tests := []struct {
		name, repository, tag string
		tagSet                bool
	}{
		{"registry and path", "oci://registry.example.com/apps", "", false},
		{"registry only", "oci://registry.example.com", "", false},
		{"trailing slash", "oci://registry.example.com/apps/", "", false},
		{"nested path", "oci://registry.example.com/team/apps", "", false},
		{"registry port", "oci://registry.example.com:5000/apps", "", false},
		{"localhost", "oci://localhost/apps", "", false},
		{"localhost port", "oci://localhost:5000/apps", "", false},
		{"ip and port", "oci://10.0.0.1:5000/apps", "", false},
		{"uppercase host", "oci://Registry.Example.com/apps", "", false},
		{"bracketed IPv6", "oci://[::1]/apps", "", false},
		{"bracketed IPv6 and port", "oci://[::1]:5000/apps", "", false},
		{"component separators", "oci://registry.example.com/a.b/c_d/e__f/g--h", "", false},
		{"semver tag", testOCIRepository, "v1.0.0", true},
		{"underscore-led tag", testOCIRepository, "_latest", true},
		{"tag of 128 characters", testOCIRepository, "v" + strings.Repeat("a", 127), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &buildOptions{outputDir: "out", delivery: deliveryOptions{repository: tt.repository, tag: tt.tag}}
			if err := validateDeliveryFlags(opts, true, tt.tagSet); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}

// TestCheckOCIRepositoryBundleURL checks the per-bundle url check
// generateDelivery applies after appending the bundle name: a path is
// required, and it may not exceed the parser's length limit.
func TestCheckOCIRepositoryBundleURL(t *testing.T) {
	prefix := "oci://registry.example.com/"
	if err := checkOCIRepository(prefix+strings.Repeat("a", ociRepositoryPathMax), true); err != nil {
		t.Errorf("path of %d characters refused: %v", ociRepositoryPathMax, err)
	}
	if err := checkOCIRepository(prefix+strings.Repeat("a", ociRepositoryPathMax+1), true); err == nil {
		t.Errorf("path of %d characters accepted", ociRepositoryPathMax+1)
	}
	if err := checkOCIRepository("oci://registry.example.com", true); err == nil {
		t.Error("url without a repository path accepted")
	}
}

// TestDeliveryDisabledWritesOnlyAppFile checks that -o without
// --oci-repository writes exactly <app>.yaml, as before delivery existed.
func TestDeliveryDisabledWritesOnlyAppFile(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(deliveryTestdata, "hierarchical")
	if _, err := runKurel(t, "build", filepath.Join(dir, "app.yaml"), "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"), "-o", out); err != nil {
		t.Fatal(err)
	}
	files := readTree(t, out)
	if len(files) != 1 {
		t.Errorf("wrote %d files, want only shop.yaml", len(files))
	}
	if _, ok := files["shop.yaml"]; !ok {
		t.Error("shop.yaml not written")
	}
}

// TestDeliveryFlagRefusals checks the refused flag combinations, each of
// which must fail before anything is written.
func TestDeliveryFlagRefusals(t *testing.T) {
	appPath := filepath.Join(deliveryTestdata, "flat", "app.yaml")
	profilePath := filepath.Join(deliveryTestdata, "cluster.yaml")
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"repository without output", []string{"--oci-repository", testOCIRepository}, "--oci-repository requires --output"},
		{"tag without repository", []string{"-o", "OUT", "--oci-tag", "v1"}, "--oci-tag requires --oci-repository"},
		{"tag without repository or output", []string{"--oci-tag", "v1"}, "--oci-tag requires --oci-repository"},
		{"https URL", []string{"-o", "OUT", "--oci-repository", "https://registry.example.com/apps"}, "must be an oci:// URL"},
		{"bare registry", []string{"-o", "OUT", "--oci-repository", "registry.example.com/apps"}, "must be an oci:// URL"},
		{"scheme only", []string{"-o", "OUT", "--oci-repository", "oci://"}, "must be an oci:// URL"},
		{"scheme and slashes only", []string{"-o", "OUT", "--oci-repository", "oci:///"}, "must be an oci:// URL"},
		{"explicitly empty", []string{"-o", "OUT", "--oci-repository="}, "must be an oci:// URL"},
		{"query", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com/apps?oops"}, "must be an oci:// URL"},
		{"fragment", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com/apps#frag"}, "must be an oci:// URL"},
		{"whitespace in path", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com/my apps"}, "must be an oci:// URL"},
		{"uppercase in path", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com/Apps"}, "must be an oci:// URL"},
		{"empty path segment", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com//apps"}, "must be an oci:// URL"},
		{"separator-led segment", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com/-apps"}, "must be an oci:// URL"},
		{"query on registry", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com?oops"}, "must be an oci:// URL"},
		{"userinfo", []string{"-o", "OUT", "--oci-repository", "oci://user@registry.example.com/apps"}, "must be an oci:// URL"},
		{"non-numeric port", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com:port/apps"}, "must be an oci:// URL"},
		{"implicit Docker Hub registry", []string{"-o", "OUT", "--oci-repository", "oci://registry/apps"}, "must be an oci:// URL"},
		{"no registry", []string{"-o", "OUT", "--oci-repository", "oci:///apps"}, "must be an oci:// URL"},
		{"port without hostname", []string{"-o", "OUT", "--oci-repository", "oci://:5000/apps"}, "must be an oci:// URL"},
		{"port without hostname or path", []string{"-o", "OUT", "--oci-repository", "oci://:5000"}, "must be an oci:// URL"},
		{"colon only", []string{"-o", "OUT", "--oci-repository", "oci://:/apps"}, "must be an oci:// URL"},
		{"empty port", []string{"-o", "OUT", "--oci-repository", "oci://registry.example.com:/apps"}, "must be an oci:// URL"},
		{"unbracketed IPv6", []string{"-o", "OUT", "--oci-repository", "oci://::1/apps"}, "must be an oci:// URL"},
		{"empty IPv6 brackets", []string{"-o", "OUT", "--oci-repository", "oci://[]:5000/apps"}, "must be an oci:// URL"},
		{"empty host label", []string{"-o", "OUT", "--oci-repository", "oci://.example.com/apps"}, "must be an oci:// URL"},
		{"tag with whitespace", []string{"-o", "OUT", "--oci-repository", testOCIRepository, "--oci-tag", "bad tag"}, "is not a valid OCI tag"},
		{"dot-led tag", []string{"-o", "OUT", "--oci-repository", testOCIRepository, "--oci-tag", ".v1"}, "is not a valid OCI tag"},
		{"tag with a colon", []string{"-o", "OUT", "--oci-repository", testOCIRepository, "--oci-tag", "v1:x"}, "is not a valid OCI tag"},
		{"tag of 129 characters", []string{"-o", "OUT", "--oci-repository", testOCIRepository, "--oci-tag", "v" + strings.Repeat("a", 128)}, "is not a valid OCI tag"},
		{"explicitly empty tag", []string{"-o", "OUT", "--oci-repository", testOCIRepository, "--oci-tag="}, "is not a valid OCI tag"},
		{"bad tag without output", []string{"--oci-repository", testOCIRepository, "--oci-tag", "bad tag"}, "is not a valid OCI tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			args := []string{"build", appPath, "--profile", profilePath}
			for _, a := range tt.args {
				if a == "OUT" {
					a = out
				}
				args = append(args, a)
			}
			stdout, err := runKurel(t, args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			}
			if stdout != "" {
				t.Errorf("wrote to stdout before refusing:\n%s", stdout)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("created the output directory before refusing (stat error %v)", err)
			}
		})
	}
}
