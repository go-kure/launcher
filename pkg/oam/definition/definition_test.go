package definition_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/definition"
)

// exampleDefinitions is the definitions directory of the example a user starts from.
const exampleDefinitions = "../../../examples/custom-component/definitions"

var digestLine = regexp.MustCompile(`sha256: [0-9a-f]{64}`)

// copyExample copies the example's definition and CRD into a new directory and
// returns the path of the definition document.
func copyExample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "crds"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"batchjob.yaml", "crds/batchjob.yaml"} {
		data, err := os.ReadFile(filepath.Join(exampleDefinitions, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "batchjob.yaml")
}

// edit replaces the first old in the file at path.
func edit(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not hold %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCRD replaces the CRD with crd and pins its new digest in the definition.
func writeCRD(t *testing.T, def, crd string) {
	t.Helper()
	crdPath := filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml")
	if err := os.WriteFile(crdPath, []byte(crd), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(crd))
	data, err := os.ReadFile(def)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def, digestLine.ReplaceAll(data, []byte("sha256: "+hex.EncodeToString(sum[:]))), 0o644); err != nil {
		t.Fatal(err)
	}
}

// crdWithSchema is a CRD for example.gokure.dev/v1alpha1 BatchJob whose version
// block is versionBlock, so a case can break one part of it.
func crdWithSchema(versionBlock string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: batchjobs.example.gokure.dev
spec:
  group: example.gokure.dev
  names: {kind: BatchJob, listKind: BatchJobList, plural: batchjobs, singular: batchjob}
  scope: Namespaced
  versions:
` + versionBlock
}

func TestLoad_Example(t *testing.T) {
	h, err := definition.Load(filepath.Join(exampleDefinitions, "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := h.TypeName(); got != "batch-job" {
		t.Errorf("TypeName = %q, want batch-job", got)
	}
	if got := h.Identity(); got.Name != "batchjob.example.gokure.dev" || got.Version != "v1alpha1" {
		t.Errorf("Identity = %+v, want batchjob.example.gokure.dev@v1alpha1", got)
	}
	schema := h.PropertySchema()
	for _, name := range []string{"image", "mode", "parallelism", "port", "resources", "env"} {
		if _, ok := schema[name]; !ok {
			t.Errorf("PropertySchema does not publish %q", name)
		}
	}
	if !schema["image"].Required || schema["parallelism"].Required {
		t.Errorf("Required: image %v, parallelism %v; want true, false", schema["image"].Required, schema["parallelism"].Required)
	}
	if got := schema["port"].Types; len(got) != 2 {
		t.Errorf("port Types = %v, want string and integer", got)
	}
}

// A spec property with no type, which a CRD may leave to
// x-kubernetes-preserve-unknown-fields, is published as one of no declared type.
func TestLoad_TypelessSpecPropertyIsPublishedOpen(t *testing.T) {
	def := copyExample(t)
	crd, err := os.ReadFile(filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeCRD(t, def, strings.Replace(string(crd), "              env:\n", "              extra:\n                x-kubernetes-preserve-unknown-fields: true\n              env:\n", 1))
	h, err := definition.Load(def)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := h.PropertySchema()["extra"]
	if !ok || p.Type != "" || len(p.Types) != 0 {
		t.Errorf("extra = %+v (published %v), want a property of no declared type", p, ok)
	}
}

func TestLoad_Refusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, def string)
		want string
	}{
		{"unknown field in the document", func(t *testing.T, def string) { edit(t, def, "spec:\n", "spec:\n  extra: 1\n") }, "extra"},
		{"wrong apiVersion", func(t *testing.T, def string) {
			edit(t, def, "launcher.gokure.dev/v1alpha1", "launcher.gokure.dev/v1")
		}, "apiVersion"},
		{"no metadata.name", func(t *testing.T, def string) { edit(t, def, "  name: batch-job\n", "") }, "metadata.name"},
		{"emits without a group", func(t *testing.T, def string) {
			edit(t, def, "example.gokure.dev/v1alpha1", "v1alpha1")
		}, "spec.emits"},
		{"absolute crd path", func(t *testing.T, def string) { edit(t, def, "crds/batchjob.yaml", "/etc/hostname") }, "relative path inside"},
		{"crd path that leaves the directory", func(t *testing.T, def string) { edit(t, def, "crds/batchjob.yaml", "../batchjob.yaml") }, "relative path inside"},
		{"digest mismatch", func(t *testing.T, def string) { edit(t, def, "sha256: cfb6", "sha256: 0fb6") }, "the definition pins"},
		{"no digest", func(t *testing.T, def string) {
			data, _ := os.ReadFile(def)
			if err := os.WriteFile(def, digestLine.ReplaceAll(data, nil), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "the definition pins"},
		{"missing crd file", func(t *testing.T, def string) { edit(t, def, "crds/batchjob.yaml", "crds/missing.yaml") }, "reading the pinned CRD"},
		{"CRD names another kind", func(t *testing.T, def string) { edit(t, def, "kind: BatchJob", "kind: Other") }, "spec.emits names Other"},
		{"CRD in another group", func(t *testing.T, def string) {
			edit(t, def, "apiVersion: example.gokure.dev/v1alpha1", "apiVersion: other.gokure.dev/v1alpha1")
		}, "group other.gokure.dev"},
		{"version the CRD does not carry", func(t *testing.T, def string) {
			edit(t, def, "example.gokure.dev/v1alpha1", "example.gokure.dev/v1beta1")
		}, `does not serve version "v1beta1"`},
		{"version that is not served", func(t *testing.T, def string) {
			writeCRD(t, def, crdWithSchema("  - name: v1alpha1\n    served: false\n    storage: true\n    schema:\n      openAPIV3Schema: {type: object}\n"))
		}, "does not serve"},
		{"version with no schema", func(t *testing.T, def string) {
			writeCRD(t, def, crdWithSchema("  - name: v1alpha1\n    served: true\n    storage: true\n"))
		}, "has no schema"},
		{"schema that is not structural", func(t *testing.T, def string) {
			writeCRD(t, def, crdWithSchema("  - name: v1alpha1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n        properties:\n          spec:\n            type: object\n            properties:\n              untyped: {}\n"))
		}, "not structural"},
		{"schema with no spec", func(t *testing.T, def string) {
			writeCRD(t, def, crdWithSchema("  - name: v1alpha1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n        properties:\n          status: {type: object}\n"))
		}, "declares no spec"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			def := copyExample(t)
			c.edit(t, def)
			_, err := definition.Load(def)
			if err == nil {
				t.Fatal("loaded")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not hold %q", err, c.want)
			}
		})
	}
}

func TestRegister_RefusesTheSameTypeTwice(t *testing.T) {
	h, err := definition.Load(filepath.Join(exampleDefinitions, "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tr := oam.NewTransformer(nil, nil)
	if err := definition.Register(tr, h); err != nil {
		t.Fatal(err)
	}
	err = definition.Register(tr, h)
	if err == nil || !strings.Contains(err.Error(), `"batch-job" is already declared`) {
		t.Errorf("second Register: %v, want the type refused as already declared", err)
	}
	if _, err := tr.ResolveComponentType("batchjob.example.gokure.dev@v1alpha1"); err != nil {
		t.Errorf("the identity does not resolve: %v", err)
	}
}

// variant copies the example under another type name and kind, in the same group.
func variant(t *testing.T, typeName, kind string) string {
	t.Helper()
	def := copyExample(t)
	crd, err := os.ReadFile(filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeCRD(t, def, strings.NewReplacer("BatchJob", kind, "batchjob", strings.ToLower(kind)).Replace(string(crd)))
	edit(t, def, "name: batch-job", "name: "+typeName)
	edit(t, def, "kind: BatchJob", "kind: "+kind)
	return def
}

// A type's identity is also known by its short name, so an identity whose short
// name is a type already registered would take that type's name over.
func TestRegister_RefusesAnIdentityThatShadowsARegisteredName(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	first, err := definition.Load(variant(t, "widget", "BatchJob"))
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.Register(tr, first); err != nil {
		t.Fatal(err)
	}
	second, err := definition.Load(variant(t, "jobs", "Widget"))
	if err != nil {
		t.Fatal(err)
	}
	err = definition.Register(tr, second)
	if err == nil || !strings.Contains(err.Error(), `"widget"`) {
		t.Fatalf("Register: %v, want the short name widget refused", err)
	}
	if h, ok := tr.ComponentHandlerFor("widget"); !ok || h != oam.ComponentHandler(first) {
		t.Errorf("type widget is no longer the first definition's handler")
	}
}

// A library transformer may hold a lowering rule with no declared identity;
// the collision is an error, never a panic.
func TestRegister_RefusesALoweringRuleName(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponentLowering(components.WebserviceRule{})
	h, err := definition.Load(variant(t, "webservice", "BatchJob"))
	if err != nil {
		t.Fatal(err)
	}
	err = definition.Register(tr, h)
	if err == nil || !strings.Contains(err.Error(), `"webservice"`) {
		t.Errorf("Register: %v, want the name refused", err)
	}
}

func TestLoad_RefusesADocumentThatIsNotACRD(t *testing.T) {
	def := copyExample(t)
	crd, err := os.ReadFile(filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeCRD(t, def, strings.Replace(string(crd), "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n", "apiVersion: v1\nkind: ConfigMap\n", 1))
	_, err = definition.Load(def)
	if err == nil || !strings.Contains(err.Error(), "CustomResourceDefinition") {
		t.Errorf("Load: %v, want the pinned file refused as no CRD", err)
	}
}

// The published top level is closed, so a spec that is open cannot be published.
func TestLoad_RefusesAnOpenSpec(t *testing.T) {
	cases := []struct{ name, line string }{
		{"additionalProperties", "            additionalProperties: {type: string}\n"},
		{"x-kubernetes-preserve-unknown-fields", "            x-kubernetes-preserve-unknown-fields: true\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			def := copyExample(t)
			writeCRD(t, def, crdWithSchema("  - name: v1alpha1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n        properties:\n          spec:\n            type: object\n"+c.line))
			_, err := definition.Load(def)
			if err == nil || !strings.Contains(err.Error(), "spec is open") {
				t.Errorf("Load: %v, want the open spec refused", err)
			}
		})
	}
}

// A relative crd path that a symlink carries out of the definition's directory is
// refused, as an environment binding's path is; the digest of the file it reaches
// would match, so only the containment check stops the read.
func TestLoad_RefusesACRDPathThatASymlinkLeads_Outside(t *testing.T) {
	cases := []struct {
		name string
		link func(t *testing.T, def, outside string)
	}{
		{"a symlinked file", func(t *testing.T, def, outside string) {
			crds := filepath.Join(filepath.Dir(def), "crds")
			if err := os.Rename(filepath.Join(crds, "batchjob.yaml"), filepath.Join(outside, "batchjob.yaml")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "batchjob.yaml"), filepath.Join(crds, "batchjob.yaml")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlinked directory", func(t *testing.T, def, outside string) {
			dir := filepath.Dir(def)
			if err := os.Rename(filepath.Join(dir, "crds"), filepath.Join(outside, "crds")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "crds"), filepath.Join(dir, "crds")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			def := copyExample(t)
			c.link(t, def, t.TempDir())
			_, err := definition.Load(def)
			if err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Errorf("Load: %v, want the path refused as resolving outside the directory", err)
			}
		})
	}
}

// A symlink that stays inside the definition's directory is read.
func TestLoad_AcceptsACRDPathThatASymlinkLeadsInside(t *testing.T) {
	def := copyExample(t)
	crds := filepath.Join(filepath.Dir(def), "crds")
	if err := os.Symlink("batchjob.yaml", filepath.Join(crds, "alias.yaml")); err != nil {
		t.Fatal(err)
	}
	edit(t, def, "crds/batchjob.yaml", "crds/alias.yaml")
	if _, err := definition.Load(def); err != nil {
		t.Errorf("Load: %v", err)
	}
}

// The API server checks the apiVersion, kind and metadata of a field marked
// x-kubernetes-embedded-resource apart from the schema, which this check does not
// run, so such a CRD is refused at load rather than half checked.
func TestLoad_RefusesAnEmbeddedResource(t *testing.T) {
	cases := []struct{ name, old, replacement, path string }{
		{"in the spec", "              env:\n", "              template:\n                type: object\n                x-kubernetes-embedded-resource: true\n                x-kubernetes-preserve-unknown-fields: true\n              env:\n", "spec.template"},
		{"in an array item", "                    value:\n                      type: string\n", "                    value:\n                      type: string\n                    embedded:\n                      type: object\n                      x-kubernetes-embedded-resource: true\n                      x-kubernetes-preserve-unknown-fields: true\n", "spec.env[].embedded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			def := copyExample(t)
			crd, err := os.ReadFile(filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(crd), c.old) {
				t.Fatalf("the example CRD does not hold %q", c.old)
			}
			writeCRD(t, def, strings.Replace(string(crd), c.old, c.replacement, 1))
			_, err = definition.Load(def)
			if err == nil || !strings.Contains(err.Error(), "x-kubernetes-embedded-resource") || !strings.Contains(err.Error(), c.path) {
				t.Errorf("Load: %v, want the embedded resource at %s refused", err, c.path)
			}
		})
	}
}

// A pinned CRD file is one YAML document: a second, valid or not, is refused and
// never left unread.
func TestLoad_RefusesACRDFileWithASecondDocument(t *testing.T) {
	for name, extra := range map[string]string{"a malformed one": "---\nspec: [\n", "a valid one": "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"} {
		t.Run(name, func(t *testing.T) {
			def := copyExample(t)
			crd, err := os.ReadFile(filepath.Join(filepath.Dir(def), "crds", "batchjob.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			writeCRD(t, def, string(crd)+extra)
			_, err = definition.Load(def)
			if err == nil || !strings.Contains(err.Error(), "more than one YAML document") {
				t.Errorf("Load: %v, want the second document refused", err)
			}
		})
	}
}

// One YAML document per definition file: a second is refused, never dropped.
func TestLoad_RefusesASecondDocument(t *testing.T) {
	def := copyExample(t)
	data, err := os.ReadFile(def)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def, append(append(data, "---\n"...), data...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = definition.Load(def)
	if err == nil || !strings.Contains(err.Error(), "more than one YAML document") {
		t.Errorf("Load: %v, want the second document refused", err)
	}
}
