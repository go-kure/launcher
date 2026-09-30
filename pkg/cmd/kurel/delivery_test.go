package kurel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	kio "github.com/go-kure/kure/pkg/io"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
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
	// viaPolicy builds through buildWithDependencyPolicy instead of the CLI:
	// kurel registers no policy handler, so a dependency-policy app (the
	// per-component-bundle cluster shape) cannot be built through the command.
	viaPolicy bool
}

var deliveryScenarios = []deliveryScenario{
	{name: "flat"},                                       // one bundle, no --oci-tag
	{name: "hierarchical", tag: "v1.0.0"},                // umbrella + one child per tier
	{name: "dependency", tag: "v1.0.0", viaPolicy: true}, // per-component bundles with dependsOn
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
	if s.viaPolicy {
		return buildWithDependencyPolicy(t, appPath, profilePath, outDir, deliveryOptions{repository: testOCIRepository, tag: s.tag})
	}
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

// testDependencyPolicy is a "dependency" policy handler reading
// properties.dependsOn: {component: [component, ...]}.
type testDependencyPolicy struct{}

func (testDependencyPolicy) CanHandle(policyType string) bool { return policyType == "dependency" }

func (testDependencyPolicy) Apply(p *oam.ApplicationPolicy, _ []string, r *oam.PolicyResult) error {
	deps, _ := p.Properties["dependsOn"].(map[string]any)
	for from, v := range deps {
		list, _ := v.([]any)
		for _, to := range list {
			name, _ := to.(string)
			r.Dependencies[from] = append(r.Dependencies[from], name)
		}
	}
	return nil
}

// buildWithDependencyPolicy is runBuild's pipeline (parse, validate, transform,
// augmenter guard, collect, delivery, -o write) on a transformer that also has
// a "dependency" policy handler. It returns the stdout build's bytes.
func buildWithDependencyPolicy(t *testing.T, appPath, profilePath, outDir string, delivery deliveryOptions) []byte {
	t.Helper()
	appData, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	tr := newBuiltinTransformer()
	tr.RegisterPolicy("dependency", testDependencyPolicy{})
	app, err := oam.ParseWithExtraTypes(appData, nil, tr.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing %s: %v", appPath, err)
	}
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating %s: %v", appPath, err)
	}
	profileData, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := oam.ParseClusterProfile(profileData)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := tr.Transform(app, oam.TransformContext{
		ClusterID:    "local",
		Capabilities: evaluated.Spec.Capabilities,
		Domain:       kurelDomain,
	})
	if err != nil {
		t.Fatalf("transforming %s: %v", appPath, err)
	}
	if err := rejectLayoutAugmenters(cluster.Node); err != nil {
		t.Fatal(err)
	}
	objects, err := collectFromNode(cluster.Node)
	if err != nil {
		t.Fatal(err)
	}
	yamlBytes, err := kio.EncodeObjectsToYAML(objects)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDelivery(outDir, app.Metadata.Name, cluster, delivery); err != nil {
		t.Fatalf("writeDelivery: %v", err)
	}
	if err := writeOutputDir(outDir, app.Metadata.Name, yamlBytes); err != nil {
		t.Fatal(err)
	}
	return yamlBytes
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

// TestDeliveryFlagsAccepted checks --oci-repository and --oci-tag values the
// flag check must let through.
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
