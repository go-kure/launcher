package kurel

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests pin how `kurel build` loads configuration-defined component models
// (go-kure/launcher#1570): from <app dir>/models/ by file name, then from the
// --model files in command order, with the library's refusals.

// testBaseModel is a model of one ConfigMap.
const testBaseModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: settings.acme.example
  version: v1
spec:
  celEnv: "1"
  inputs:
    greeting: {type: string, required: true}
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data:
          greeting: "${inputs.greeting}"
`

// testComposedModel names testBaseModel, so it registers only after it.
const testComposedModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: bundle.acme.example
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: inner
      type: settings.acme.example@v1
      properties:
        greeting: hello-from-the-bundle
`

// testModelAppYAML is an application of one component; %TYPE% and %PROPS% are
// its type and properties.
const testModelAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: orders
      type: %TYPE%
      properties: %PROPS%
`

func modelApp(typ, props string) string {
	return strings.NewReplacer("%TYPE%", typ, "%PROPS%", props).Replace(testModelAppYAML)
}

func settingsApp() string { return modelApp("settings.acme.example@v1", "{greeting: hi}") }
func bundleApp() string   { return modelApp("bundle.acme.example@v1", "{}") }

// modelPackage writes app.yaml and the files, each at its path relative to the
// package directory, and returns the directory.
func modelPackage(t *testing.T, app string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTempFile(t, dir, "app.yaml", app)
	for name, content := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0755); err != nil {
			t.Fatal(err)
		}
		writeTempFile(t, dir, name, content)
	}
	return dir
}

func buildModelPackage(t *testing.T, dir string, extra ...string) (string, error) {
	t.Helper()
	return runKurel(t, append([]string{"build", dir, "--profile", emptyProfilePath}, extra...)...)
}

func requireBuilt(t *testing.T, out string, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output does not hold %q:\n%s", w, out)
		}
	}
}

func requireRefused(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected the build to be refused")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not hold %q: %v", w, err)
		}
	}
}

// The control for every build below: with no model loaded, the type is unknown.
func TestBuildCommand_ModelsNoneLoadedRefusesTheType(t *testing.T) {
	dir := modelPackage(t, settingsApp(), nil)
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, "settings.acme.example@v1")
}

func TestBuildCommand_ModelsDirectory(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"models/settings.yaml": testBaseModel})
	out, err := buildModelPackage(t, dir)
	requireBuilt(t, out, err, "kind: ConfigMap", "name: orders-cm", "greeting: hi")
}

// The directory is found next to app.yaml when the argument is the file too.
func TestBuildCommand_ModelsDirectoryBesideAnAppFile(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"models/settings.yaml": testBaseModel})
	out, err := runKurel(t, "build", filepath.Join(dir, "app.yaml"), "--profile", emptyProfilePath)
	requireBuilt(t, out, err, "name: orders-cm")
}

func TestBuildCommand_ModelsFlag(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"elsewhere/settings.yaml": testBaseModel})
	out, err := buildModelPackage(t, dir, "--model", filepath.Join(dir, "elsewhere", "settings.yaml"))
	requireBuilt(t, out, err, "name: orders-cm", "greeting: hi")
}

func TestBuildCommand_ModelsDirectoryLoadsInFileNameOrder(t *testing.T) {
	t.Run("the named model sorts first", func(t *testing.T) {
		dir := modelPackage(t, bundleApp(), map[string]string{
			"models/01-settings.yaml": testBaseModel,
			"models/02-bundle.yaml":   testComposedModel,
		})
		out, err := buildModelPackage(t, dir)
		requireBuilt(t, out, err, "name: orders-inner-cm", "greeting: hello-from-the-bundle")
	})
	t.Run("the named model sorts last", func(t *testing.T) {
		dir := modelPackage(t, bundleApp(), map[string]string{
			"models/01-bundle.yaml":   testComposedModel,
			"models/02-settings.yaml": testBaseModel,
		})
		_, err := buildModelPackage(t, dir)
		requireRefused(t, err, "01-bundle.yaml", "spec.parts[0].type")
	})
}

func TestBuildCommand_ModelsFlagsLoadInCommandOrder(t *testing.T) {
	// The file names sort against the command order, so a loader that sorted the
	// flags would fail the first case and pass the second.
	dir := modelPackage(t, bundleApp(), map[string]string{
		"extra/a-bundle.yaml":   testComposedModel,
		"extra/b-settings.yaml": testBaseModel,
	})
	bundle := filepath.Join(dir, "extra", "a-bundle.yaml")
	settings := filepath.Join(dir, "extra", "b-settings.yaml")

	out, err := buildModelPackage(t, dir, "--model", settings, "--model", bundle)
	requireBuilt(t, out, err, "name: orders-inner-cm")

	_, err = buildModelPackage(t, dir, "--model", bundle, "--model", settings)
	requireRefused(t, err, "a-bundle.yaml", "spec.parts[0].type")
}

func TestBuildCommand_ModelsDirectoryLoadsBeforeFlags(t *testing.T) {
	t.Run("a flag model names a directory model", func(t *testing.T) {
		dir := modelPackage(t, bundleApp(), map[string]string{
			"models/settings.yaml": testBaseModel,
			"extra/bundle.yaml":    testComposedModel,
		})
		out, err := buildModelPackage(t, dir, "--model", filepath.Join(dir, "extra", "bundle.yaml"))
		requireBuilt(t, out, err, "name: orders-inner-cm")
	})
	t.Run("a directory model does not see a flag model", func(t *testing.T) {
		dir := modelPackage(t, bundleApp(), map[string]string{
			"models/bundle.yaml":  testComposedModel,
			"extra/settings.yaml": testBaseModel,
		})
		_, err := buildModelPackage(t, dir, "--model", filepath.Join(dir, "extra", "settings.yaml"))
		requireRefused(t, err, "bundle.yaml", "spec.parts[0].type")
	})
}

// One identity twice is the library's refusal: kurel does not drop the second,
// not even when it is the same file.
func TestBuildCommand_ModelsRefuseOneIdentityTwice(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"models/settings.yaml": testBaseModel})
	_, err := buildModelPackage(t, dir, "--model", filepath.Join(dir, "models", "settings.yaml"))
	requireRefused(t, err, "settings.yaml", "already registered")
}

func TestBuildCommand_ModelsRefusalsNameTheFile(t *testing.T) {
	cases := []struct {
		name, content string
		want          []string
	}{
		{"an unknown field", testBaseModel + "extends: other\n", []string{"extends"}},
		{"no outputs", strings.Replace(testBaseModel, "  outputs: {}\n", "", 1), []string{"spec.outputs"}},
		{
			"another kind",
			"apiVersion: launcher.gokure.dev/v1alpha1\nkind: CapabilityDefinition\nmetadata:\n  name: a.acme.example\n  version: v1\n",
			[]string{`kind "CapabilityDefinition"`},
		},
		{"an empty file", "", []string{"empty input"}},
		{
			"a built-in type",
			strings.Replace(testBaseModel, "settings.acme.example", "deployment.apps", 1),
			[]string{"deployment.apps@v1", "already declared"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := modelPackage(t, settingsApp(), map[string]string{"models/broken.yaml": tc.content})
			_, err := buildModelPackage(t, dir)
			requireRefused(t, err, append(tc.want, "broken.yaml")...)
		})
	}
}

// The component's properties are checked against the model's inputs.
func TestBuildCommand_ModelsInputsCheckTheComponent(t *testing.T) {
	dir := modelPackage(t, modelApp("settings.acme.example@v1", "{greeting: hi, extra: no}"),
		map[string]string{"models/settings.yaml": testBaseModel})
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, "extra")
}

func TestBuildCommand_ModelsMissingFlagFileRefused(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"models/settings.yaml": testBaseModel})
	_, err := buildModelPackage(t, dir, "--model", filepath.Join(dir, "absent.yaml"))
	requireRefused(t, err, "absent.yaml")
}

func TestBuildCommand_ModelsPathThatIsNoDirectoryRefused(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{"models": testBaseModel})
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, "models directory")
}

// The example's second model names its first, and each queue gets its own
// consumer: a ConfigMap and a Deployment that reads the queue name from it.
func TestBuildCommand_ModelsExample(t *testing.T) {
	out, err := runKurel(t, "build", "../../../examples/custom-model",
		"--profile", "../../../examples/cluster-profiles/minimal.yaml")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// The emitted objects by "<kind>/<name>".
	objects := map[string]map[string]any{}
	for _, raw := range strings.Split(out, "\n---\n") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding an output document: %v\n%s", err, raw)
		}
		if obj == nil {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		objects[fmt.Sprintf("%v/%v", obj["kind"], meta["name"])] = obj
	}

	for _, queue := range []string{"orders", "invoices"} {
		settings := "shop-" + queue + "-settings"
		cm, ok := objects["ConfigMap/"+settings]
		if !ok {
			t.Fatalf("no ConfigMap %s:\n%s", settings, out)
		}
		if got, want := cm["data"], map[string]any{"queue": queue}; !reflect.DeepEqual(got, want) {
			t.Errorf("ConfigMap %s: data = %v, want %v", settings, got, want)
		}

		app := "shop-" + queue + "-app"
		deployment, ok := objects["Deployment/"+app]
		if !ok {
			t.Fatalf("no Deployment %s:\n%s", app, out)
		}
		spec, _ := deployment["spec"].(map[string]any)
		template, _ := spec["template"].(map[string]any)
		pod, _ := template["spec"].(map[string]any)
		containers, _ := pod["containers"].([]any)
		if len(containers) != 1 {
			t.Fatalf("Deployment %s has %d containers, want 1:\n%s", app, len(containers), out)
		}
		container, _ := containers[0].(map[string]any)
		want := []any{map[string]any{
			"name": "QUEUE",
			"valueFrom": map[string]any{
				"configMapKeyRef": map[string]any{"name": settings, "key": "queue"},
			},
		}}
		if got := container["env"]; !reflect.DeepEqual(got, want) {
			t.Errorf("Deployment %s: env = %v, want %v", app, got, want)
		}
	}
}

// As with definitions/, only the *.yaml files of the directory itself are read:
// each of the others here would be refused if it were.
func TestBuildCommand_ModelsDirectoryReadsItsYAMLFilesOnly(t *testing.T) {
	dir := modelPackage(t, settingsApp(), map[string]string{
		"models/settings.yaml":     testBaseModel,
		"models/README.md":         "not a model",
		"models/other.yml":         "not: a model",
		"models/nested/inner.yaml": "not: a model",
		// A directory with the suffix: only the directory check leaves it out.
		"models/more.yaml/inner.yaml": "not: a model",
	})
	out, err := buildModelPackage(t, dir)
	requireBuilt(t, out, err, "name: orders-cm")
}
