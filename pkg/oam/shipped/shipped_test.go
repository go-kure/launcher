package shipped

import (
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
	"github.com/go-kure/launcher/pkg/oam/model"
)

const ruleIdentity = "vmrule.operator.victoriametrics.com@v1beta1"

// builtinTransformer registers every built-in and declares its type identities, as a
// consumer does. A shipped model names built-in kinds, so the embedded set loads on it only.
func builtinTransformer(t *testing.T) *oam.Transformer {
	t.Helper()
	tr := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		tr.RegisterBuiltinComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		tr.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		tr.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		tr.RegisterBuiltinTraitLowering(r)
	}
	if err := registry.DeclareTypes(tr); err != nil {
		t.Fatalf("DeclareTypes: %v", err)
	}
	return tr
}

// shippedModel is a model of one shipped kind, so it registers only after the definitions.
const shippedModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: alerts.acme.example
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: rule
      type: ` + ruleIdentity + `
      properties:
        groups:
          - name: g
            rules:
              - {alert: A, expr: "up == 0"}
`

func embeddedDefinitions(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(content, "content/definitions")
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// copyOf returns a writable copy of a file system's files.
func copyOf(t *testing.T, fsys fs.FS) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		out[path] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantError(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not hold %q: %v", w, err)
		}
	}
}

// TestEmbeddedContentLoads registers the embedded set on an empty Transformer: the three
// kinds resolve by identity, and each is a shipped type by name and by identity.
func TestEmbeddedContentLoads(t *testing.T) {
	tr := builtinTransformer(t)
	set, err := Load(tr)
	if err != nil {
		t.Fatal(err)
	}
	// The shipped model: its identity resolves and is a shipped type.
	const s3 = "s3-access.launcher.gokure.dev@v1"
	if _, err := tr.ResolveComponentType(s3); err != nil {
		t.Errorf("%s: %v", s3, err)
	}
	wantError(t, set.Clash("", oam.TypeID{Name: "s3-access.launcher.gokure.dev", Version: "v1"}), `"s3-access.launcher.gokure.dev@v1.0.0"`, "model s3-access.yaml")
	for _, c := range []struct{ name, identity string }{
		{"vm-rule", ruleIdentity},
		{"vm-pod-scrape", "vmpodscrape.operator.victoriametrics.com@v1beta1"},
		{"vm-service-scrape", "vmservicescrape.operator.victoriametrics.com@v1beta1"},
	} {
		id, err := tr.ResolveComponentType(c.identity)
		if err != nil {
			t.Errorf("%s: %v", c.identity, err)
			continue
		}
		wantError(t, set.Clash(c.name, oam.TypeID{}), `"`+c.name+`"`, "shipped by launcher")
		wantError(t, set.Clash("", id), `"`+c.identity+`"`, "shipped by launcher")
	}
}

// TestEmbeddedModelsCompile: Load registers the shipped models without compiling their
// expressions, so a model that does not compile would fail a user's first build and not the
// load. This is the proof that each embedded one compiles, with a control that one is embedded.
func TestEmbeddedModelsCompile(t *testing.T) {
	models, err := fs.Sub(content, "content/models")
	if err != nil {
		t.Fatal(err)
	}
	names, err := yamlFiles(models)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no model is embedded: the check below proves nothing")
	}
	for _, name := range names {
		data, err := fs.ReadFile(models, name)
		if err != nil {
			t.Fatal(err)
		}
		m, err := model.Load(data)
		if err != nil {
			t.Errorf("shipped model %s does not load: %v", name, err)
			continue
		}
		if _, err := model.Compile(m); err != nil {
			t.Errorf("shipped model %s does not compile: %v", name, err)
		}
	}
}

// badExpressionModel loads and registers, and does not compile.
const badExpressionModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: broken.acme.example
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data:
          k: "${inputs.nosuch}"
`

// TestLoadModelDecodesOnce: a model decoded once is the one every later load reads, and a text
// that does not load is refused every time, never kept.
func TestLoadModelDecodesOnce(t *testing.T) {
	first, err := loadModel([]byte(shippedModel))
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadModel([]byte(shippedModel))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("the second load decoded the model again")
	}
	for i := 0; i < 2; i++ {
		if _, err := loadModel([]byte("kind: Model\n")); err == nil {
			t.Fatalf("load %d of a text that is no model was accepted", i+1)
		}
	}
}

// TestLoadModelDecodesOnceWhenLoadsStartTogether: loads of a text nobody has decoded, started at
// once, all get one model. Run under -race it also proves the cache is safe to share.
func TestLoadModelDecodesOnceWhenLoadsStartTogether(t *testing.T) {
	// A text no other test decodes, so that it is a miss for every goroutine below.
	text := []byte(strings.Replace(shippedModel, "alerts.acme.example", "together.acme.example", 1))
	const n = 16
	got := make([]*model.Model, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i], errs[i] = loadModel(text)
		}()
	}
	close(start)
	wg.Wait()
	for i := range got {
		if errs[i] != nil {
			t.Fatalf("load %d: %v", i, errs[i])
		}
		if got[i] != got[0] {
			t.Errorf("load %d decoded the model again: %p, want %p", i, got[i], got[0])
		}
	}
}

// TestShippedModelCompilesAtFirstUse: a shipped model registers without compiling, so a
// compile error appears at the first build that uses it, and names the shipped file.
func TestShippedModelCompilesAtFirstUse(t *testing.T) {
	tr := builtinTransformer(t)
	if _, err := LoadFS(tr, fstest.MapFS{}, fstest.MapFS{"broken.yaml": {Data: []byte(badExpressionModel)}}); err != nil {
		t.Fatalf("the model registers, it is the first use that compiles: %v", err)
	}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{Name: "orders", Type: "broken.acme.example@v1", Properties: map[string]any{}},
		}},
	}
	_, err := tr.Transform(app, oam.TransformContext{Namespace: "demo"})
	wantError(t, err, "shipped model broken.yaml", "nosuch")
}

// TestShippedDefinitionClash: the same authored name or the same identity is a clash and
// names the shipped file; another name over another version, an unrelated type and a Set
// that ships nothing are not.
func TestShippedDefinitionClash(t *testing.T) {
	set, err := Load(builtinTransformer(t))
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, set.Clash("vm-rule", oam.TypeID{Name: "other.example", Version: "v1"}), `"vm-rule"`, "definition vmrule.yaml")
	wantError(t, set.Clash("my-rule", oam.TypeID{Name: "vmrule.operator.victoriametrics.com", Version: "v1beta1"}), `"`+ruleIdentity+`"`, "definition vmrule.yaml")
	if err := set.Clash("my-rule", oam.TypeID{Name: "vmrule.operator.victoriametrics.com", Version: "v1"}); err != nil {
		t.Errorf("another version of a shipped kind is another type: %v", err)
	}
	if err := set.Clash("batch-job", oam.TypeID{Name: "batchjob.example.gokure.dev", Version: "v1alpha1"}); err != nil {
		t.Errorf("an unrelated type: %v", err)
	}
	var none *Set
	if err := none.Clash("vm-rule", oam.TypeID{}); err != nil {
		t.Errorf("a nil Set ships nothing: %v", err)
	}
}

// TestShippedModelClash: a shipped model loads after the definitions it names, and an
// application's model of the same identity is a clash naming the shipped file.
func TestShippedModelClash(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	set, err := LoadFS(tr, embeddedDefinitions(t), fstest.MapFS{"alerts.yaml": {Data: []byte(shippedModel)}})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, set.Clash("", oam.TypeID{Name: "alerts.acme.example", Version: "v1"}), `"alerts.acme.example@v1.0.0"`, "model alerts.yaml")
	if err := set.Clash("", oam.TypeID{Name: "alerts.acme.example", Version: "v2"}); err != nil {
		t.Errorf("another version of a shipped model is another type: %v", err)
	}
}

// TestShippedFilesRefuseAsAWhole: a pinned CRD that was changed, a model that does not
// load and a model that names a type nobody declares each refuse the load, naming the file.
func TestShippedFilesRefuseAsAWhole(t *testing.T) {
	t.Run("changed CRD", func(t *testing.T) {
		defs := copyOf(t, embeddedDefinitions(t))
		defs["crds/vmrules.yaml"].Data = append(defs["crds/vmrules.yaml"].Data, '#')
		_, err := LoadFS(oam.NewTransformer(nil, nil), defs, fstest.MapFS{})
		wantError(t, err, `shipped definition "vmrule.yaml"`, "sha256")
	})
	t.Run("bad model", func(t *testing.T) {
		_, err := LoadFS(oam.NewTransformer(nil, nil), embeddedDefinitions(t), fstest.MapFS{"bad.yaml": {Data: []byte("kind: Model\n")}})
		wantError(t, err, `shipped model "bad.yaml"`)
	})
	t.Run("model naming an undeclared type", func(t *testing.T) {
		_, err := LoadFS(oam.NewTransformer(nil, nil), fstest.MapFS{}, fstest.MapFS{"alerts.yaml": {Data: []byte(shippedModel)}})
		wantError(t, err, `shipped model "alerts.yaml"`, ruleIdentity)
	})
	t.Run("twice the same type", func(t *testing.T) {
		defs := copyOf(t, embeddedDefinitions(t))
		defs["again.yaml"] = defs["vmrule.yaml"]
		_, err := LoadFS(oam.NewTransformer(nil, nil), defs, fstest.MapFS{})
		wantError(t, err, `shipped definition "vmrule.yaml"`)
	})
}
