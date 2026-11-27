package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// componentDefinitionExample is the example `kurel build` is shown with: a type
// declared by definitions/batchjob.yaml and a CRD pinned by digest.
const componentDefinitionExample = "../../../examples/custom-component"

var componentDefinitionDigest = regexp.MustCompile(`sha256: [0-9a-f]{64}`)

// buildComponentDefinitionExample builds a copy of the example after edit has changed it.
func buildComponentDefinitionExample(t *testing.T, edit func(dir string)) (string, error) {
	t.Helper()
	dir := t.TempDir()
	src := os.DirFS(componentDefinitionExample)
	if err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, path), 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, path), data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(dir)
	}
	return runKurel(t, "build", dir, "--profile", filepath.Join(dir, "cluster.yaml"))
}

// editIn replaces the first occurrence of old in a file of the copy.
func editIn(t *testing.T, file, old, replacement string) func(string) {
	return func(dir string) {
		t.Helper()
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), old) {
			t.Fatalf("%s does not hold %q", file, old)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// editCRD changes the pinned CRD and pins the new digest, so a case reaches the
// check it is about and not the digest check.
func editCRD(t *testing.T, old, replacement string) func(string) {
	return func(dir string) {
		t.Helper()
		editIn(t, "definitions/crds/batchjob.yaml", old, replacement)(dir)
		crd, err := os.ReadFile(filepath.Join(dir, "definitions/crds/batchjob.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(crd)
		def := filepath.Join(dir, "definitions/batchjob.yaml")
		data, err := os.ReadFile(def)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(def, componentDefinitionDigest.ReplaceAll(data, []byte("sha256: "+hex.EncodeToString(sum[:]))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildCommand_ComponentDefinitionExample(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{
		"apiVersion: example.gokure.dev/v1alpha1\n", "kind: BatchJob\n", "name: nightly\n", "namespace: analytics\n",
		"mode: batch\n", "parallelism: 4\n", "port: 8080\n", "memory: 1Gi\n", "name: REPORT_DATE\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not hold %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "kind: BatchJob\n"); got != 1 {
		t.Errorf("want one BatchJob, got %d", got)
	}
}

// The declared identity names the type as the plain name does.
func TestBuildCommand_ComponentDefinitionByIdentity(t *testing.T) {
	plain, err := buildComponentDefinitionExample(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	byIdentity, err := buildComponentDefinitionExample(t, editIn(t, "app.yaml", "type: batch-job", "type: batchjob.example.gokure.dev@v1alpha1"))
	if err != nil {
		t.Fatalf("build by identity: %v", err)
	}
	if plain != byIdentity {
		t.Errorf("the identity form builds another output:\n%s\nvs\n%s", byIdentity, plain)
	}
}

func TestBuildCommand_ComponentDefinitionRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(dir string)
		want string
	}{
		{"unknown top-level field", editIn(t, "app.yaml", "      mode: batch\n", "      mode: batch\n      bogus: 1\n"), "bogus"},
		{"unknown nested field", editIn(t, "app.yaml", "        cpu: 500m\n", "        cpu: 500m\n        bogusNested: 1\n"), "spec.resources.bogusNested"},
		{"wrong type", editIn(t, "app.yaml", "parallelism: 4", "parallelism: many"), "parallelism"},
		{"wrong type in an array element", editIn(t, "app.yaml", "value: yesterday", "value: 7"), "spec.env[0].value"},
		{"missing required field", editIn(t, "app.yaml", "      image: registry.example.com/reports:1.4.2\n", ""), "image"},
		{"value outside the enum", editIn(t, "app.yaml", "mode: batch", "mode: cobol"), "spec.mode"},
		{"value outside a bound", editIn(t, "app.yaml", "parallelism: 4", "parallelism: 100"), "spec.parallelism"},
		{"digest mismatch", editIn(t, "definitions/batchjob.yaml", "sha256: cfb6", "sha256: 0fb6"), "the definition pins"},
		{"changed CRD under an old pin", editIn(t, "definitions/crds/batchjob.yaml", "maximum: 64", "maximum: 640"), "the definition pins"},
		{"a trait the type does not allow", editIn(t, "app.yaml", "    properties:\n", "    traits:\n    - type: scaler\n      properties: {maxReplicas: 3}\n    properties:\n"), `trait "scaler" is not supported on component type "batch-job"`},
		{"a type name a built-in holds", editIn(t, "definitions/batchjob.yaml", "name: batch-job", "name: deployment"), `"deployment" is already declared`},
		{"a type name a lowering rule holds", editIn(t, "definitions/batchjob.yaml", "name: batch-job", "name: webservice"), `"webservice" is already declared`},
		{"two definitions of one type", func(dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "definitions/batchjob.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "definitions/batchjob-copy.yaml"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}, `"batch-job" is already declared`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := buildComponentDefinitionExample(t, c.edit)
			if err == nil {
				t.Fatalf("built:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not hold %q", err, c.want)
			}
		})
	}
}

// Without its definition the type is unknown, as it is without this feature.
func TestBuildCommand_ComponentDefinitionNeededForItsType(t *testing.T) {
	_, err := buildComponentDefinitionExample(t, func(dir string) {
		if err := os.RemoveAll(filepath.Join(dir, "definitions")); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || !strings.Contains(err.Error(), `invalid value "batch-job"`) {
		t.Errorf("got %v, want the type refused", err)
	}
}

// A document of another kind in definitions/ is left to its own loader.
func TestBuildCommand_ComponentDefinitionBesideOtherKinds(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, func(dir string) {
		other := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: SomethingElse\nmetadata:\n  name: x\n"
		if err := os.WriteFile(filepath.Join(dir, "definitions/other.yaml"), []byte(other), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "kind: BatchJob\n") {
		t.Errorf("output holds no BatchJob:\n%s", out)
	}
}

// The scope is the CRD's: a cluster-scoped kind is emitted with no namespace.
func TestBuildCommand_ComponentDefinitionClusterScope(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, editCRD(t, "scope: Namespaced", "scope: Cluster"))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "kind: BatchJob\n") {
		t.Fatalf("output holds no BatchJob:\n%s", out)
	}
	if strings.Contains(out, "namespace:") {
		t.Errorf("a cluster-scoped object carries a namespace:\n%s", out)
	}
}

// A spec property the CRD leaves to x-kubernetes-preserve-unknown-fields has no
// type, and takes any value.
func TestBuildCommand_ComponentDefinitionOpenProperty(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, func(dir string) {
		editCRD(t, "              env:\n", "              extra:\n                x-kubernetes-preserve-unknown-fields: true\n              env:\n")(dir)
		editIn(t, "app.yaml", "      env:\n", "      extra:\n        anything: [1, two]\n      env:\n")(dir)
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "anything:\n") {
		t.Errorf("output does not hold the open property:\n%s", out)
	}
}

// A CRD whose spec fields are all optional accepts a component with no properties:
// its spec is the empty object, never null.
func TestBuildCommand_ComponentDefinitionWithNoProperties(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, func(dir string) {
		editCRD(t, "            required:\n            - image\n            - mode\n", "")(dir)
		app := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: reports\n  namespace: analytics\nspec:\n  components:\n  - name: nightly\n    type: batch-job\n"
		if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(app), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "kind: BatchJob\n") {
		t.Errorf("output holds no BatchJob:\n%s", out)
	}
}

// The API server enforces a list-type map or set apart from the schema check;
// so does the build.
func TestBuildCommand_ComponentDefinitionListMapKeysAreUnique(t *testing.T) {
	_, err := buildComponentDefinitionExample(t, func(dir string) {
		editCRD(t, "              env:\n                type: array\n", "              env:\n                type: array\n                x-kubernetes-list-type: map\n                x-kubernetes-list-map-keys:\n                - name\n")(dir)
		editIn(t, "app.yaml", "        value: yesterday\n", "        value: yesterday\n      - name: REPORT_DATE\n        value: today\n")(dir)
	})
	if err == nil || !strings.Contains(err.Error(), "spec.env[1]") {
		t.Errorf("got %v, want the duplicate key refused", err)
	}
}

// The CRD decides the scope, whatever kure's own table says of the same kind.
func TestBuildCommand_ComponentDefinitionNamespacedScopeIsKept(t *testing.T) {
	out, err := buildComponentDefinitionExample(t, func(dir string) {
		editIn(t, "definitions/batchjob.yaml", "apiVersion: example.gokure.dev/v1alpha1\n    kind: BatchJob", "apiVersion: cilium.io/v9\n    kind: CiliumClusterwideNetworkPolicy")(dir)
		editIn(t, "definitions/crds/batchjob.yaml", "group: example.gokure.dev", "group: cilium.io")(dir)
		editIn(t, "definitions/crds/batchjob.yaml", "name: v1alpha1", "name: v9")(dir)
		editCRD(t, "    kind: BatchJob\n", "    kind: CiliumClusterwideNetworkPolicy\n")(dir)
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "kind: CiliumClusterwideNetworkPolicy\n") || !strings.Contains(out, "namespace: analytics\n") {
		t.Errorf("a namespaced CRD's object carries no namespace:\n%s", out)
	}
}

// A definition file is one YAML document, and a definition in a later document is
// refused rather than skipped.
func TestBuildCommand_ComponentDefinitionFileWithTwoDocuments(t *testing.T) {
	cases := []struct {
		name string
		edit func(dir string)
		want string
	}{
		{"a definition after another kind", func(dir string) {
			def := filepath.Join(dir, "definitions/batchjob.yaml")
			data, err := os.ReadFile(def)
			if err != nil {
				t.Fatal(err)
			}
			other := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: SomethingElse\nmetadata:\n  name: x\n---\n"
			if err := os.WriteFile(def, append([]byte(other), data...), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "more than one YAML document"},
		{"two definitions", func(dir string) {
			def := filepath.Join(dir, "definitions/batchjob.yaml")
			data, err := os.ReadFile(def)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(def, append(append(data, "---\n"...), data...), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "more than one YAML document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := buildComponentDefinitionExample(t, c.edit)
			if err == nil {
				t.Fatalf("built:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not hold %q", err, c.want)
			}
		})
	}
}
