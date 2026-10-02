package kurel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

const testAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: frontend
      type: webservice
      properties:
        image: ghcr.io/example/frontend:v1.0.0
        port: 8080
      traits:
        - type: expose
          properties:
            rules:
              - host: frontend.example.com
                paths:
                  - path: /
`

const testClusterYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeTempFile %s: %v", name, err)
	}
	return path
}

func TestBuildCommand_StdoutOutput(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build command failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if got == "" {
		t.Fatal("expected non-empty YAML output")
	}
	if !strings.Contains(got, "apiVersion") {
		t.Errorf("expected YAML output to contain 'apiVersion', got:\n%s", got)
	}
	if !strings.Contains(got, "Deployment") {
		t.Errorf("expected output to contain 'Deployment' for webservice component, got:\n%s", got)
	}
}

// testCRDManifestsAppYAML exercises the launcher-native crd and manifests
// component handlers: an inline CRD plus an inline namespaced manifest
// that omits metadata.namespace and must be stamped with the app namespace.
const testCRDManifestsAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: appns
spec:
  components:
    - name: widget-crds
      type: crd
      properties:
        inline: |
          apiVersion: apiextensions.k8s.io/v1
          kind: CustomResourceDefinition
          metadata:
            name: widgets.example.com
          spec:
            group: example.com
            names:
              kind: Widget
              plural: widgets
            scope: Namespaced
            versions:
              - name: v1
                served: true
                storage: true
                schema:
                  openAPIV3Schema:
                    type: object
    - name: extra
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: cm
          data:
            k: v
`

func TestBuildCommand_CRDAndManifestsComponents(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testCRDManifestsAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with crd/manifests components failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "kind: CustomResourceDefinition") {
		t.Errorf("expected emitted CustomResourceDefinition, got:\n%s", got)
	}
	if !strings.Contains(got, "widgets.example.com") {
		t.Errorf("expected CRD name in output, got:\n%s", got)
	}
	if !strings.Contains(got, "kind: ConfigMap") {
		t.Errorf("expected manifests ConfigMap in output, got:\n%s", got)
	}
	// The ConfigMap omitted metadata.namespace; the manifests handler must stamp
	// it with the app namespace.
	if !strings.Contains(got, "namespace: appns") {
		t.Errorf("expected namespaced manifest stamped with app namespace 'appns', got:\n%s", got)
	}
}

func TestBuildCommand_OutputDir(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	outDir := filepath.Join(dir, "manifests")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--output", outDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build command failed: %v\noutput: %s", err, out.String())
	}

	outFile := filepath.Join(outDir, "my-app.yaml")
	if _, err := os.Stat(outFile); os.IsNotExist(err) {
		t.Errorf("expected output file %q to exist", outFile)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "apiVersion") {
		t.Errorf("expected output file to contain YAML, got:\n%s", data)
	}
}

func TestBuildCommand_MissingProfileFlag(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --profile is missing")
	}
}

func TestBuildCommand_MissingAppFile(t *testing.T) {
	dir := t.TempDir()
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", "/nonexistent/app.yaml", "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing app file")
	}
}

func TestBuildCommand_InvalidAppYAML(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", "not: valid: oam: yaml: here")
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid app YAML")
	}
}

func TestBuildCommand_UnsupportedComponentType(t *testing.T) {
	const unknownApp = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: backend
      type: unknownxyz
      properties:
        image: ghcr.io/example/backend:v1.0.0
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", unknownApp)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported component type 'unknownxyz'")
	}
}

func TestBuildCommand_NamespaceOverride(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--namespace", "production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with namespace override failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "production") {
		t.Errorf("expected 'production' namespace in output, got:\n%s", got)
	}
}

// TestBuildCommand_NamespaceOverrideMustBeDNS1123Label guards go-kure/launcher#616: a
// --namespace value the apiserver would refuse (dotted, or over 63 characters) fails the
// build, and the error a CLI user sees names the value, not a Go field.
func TestBuildCommand_NamespaceOverrideMustBeDNS1123Label(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--namespace", "team.prod"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected an error for --namespace team.prod, output: %s", out.String())
	}
	want := `transforming application: namespace "team.prod" is not a valid DNS-1123 label ` +
		`(a Kubernetes namespace is at most 63 lowercase letters, digits or '-', starts and ends with a letter or digit, and contains no dots)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// kurel resolves the tier annotation under the launcher.gokure.dev domain (not the library
// default gokure.dev). A component annotated launcher.gokure.dev/tier with an invalid value
// must therefore fail the build — proving Domain is wired to kurelDomain. With the default
// domain, that annotation would be ignored and the build would succeed.
func TestBuildCommand_KurelDomain_TierAnnotationHonored(t *testing.T) {
	const appYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: frontend
      type: webservice
      annotations:
        launcher.gokure.dev/tier: not-a-valid-tier
      properties:
        image: ghcr.io/example/frontend:v1.0.0
        port: 8080
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected invalid-tier error (proving kurel reads launcher.gokure.dev/tier), got nil\noutput: %s", out.String())
	}
	if !strings.Contains(err.Error(), "tier") {
		t.Errorf("expected a tier-related error, got: %v", err)
	}
}

func TestBuildCommand_StaleProfileField_Rejected(t *testing.T) {
	const staleCraneProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: stale-cluster
spec:
  gitops:
    url: https://git.example.com
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", staleCraneProfile)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for stale downstream field spec.gitops in cluster.yaml")
	}
}

func TestBuildCommand_IsRegistered(t *testing.T) {
	cmd := NewKurelCommand()
	var found bool
	for _, sub := range cmd.Commands() {
		if extractCommandName(sub.Use) == "build" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'build' subcommand to be registered")
	}
}

func TestNewBuiltinTransformer_Registered(t *testing.T) {
	transformer := newBuiltinTransformer()
	if transformer == nil {
		t.Fatal("expected non-nil transformer")
	}
}

// TestBuiltinComponentHandlers_AcceptedByParser closes the gap that let the
// "deployment" type ship registered-but-unusable (go-kure/launcher#343):
// registering a handler in builtinComponentHandlers() does NOT admit its type
// name through the parser. oam.Parse* validates c.Type against pkg/oam's own
// validComponentTypes allowlist, and rejects the document before any handler is
// consulted; the transformer's LowerableTypes() does not widen it either,
// because a terminal handler-backed type is not a lowerable one. Every other
// test in this file calls the handler directly and so cannot see it.
//
// This drives the same entry point build.go does (build.go:134), on the
// smallest document each type can appear in — properties are deliberately
// omitted, since Parse* performs identity validation only and property schemas
// are applied later, at emission.
func TestBuiltinComponentHandlers_AcceptedByParser(t *testing.T) {
	transformer := newBuiltinTransformer()
	names := make([]string, 0, len(builtinComponentHandlers())+len(builtinComponentLoweringRules()))
	for name := range builtinComponentHandlers() {
		names = append(names, name)
	}
	// A type claimed by a component lowering rule (e.g. "worker") must be admitted
	// just the same: the parser sees the authored document, before any rule runs.
	for name := range builtinComponentLoweringRules() {
		names = append(names, name)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			doc := fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: parity
spec:
  components:
    - name: c1
      type: %s
`, name)
			if _, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes()); err != nil {
				t.Errorf("component type %q is registered as a built-in handler but the parser rejects it: %v", name, err)
			}
		})
	}
}

// TestBuiltinComponentHandlers_RegisteredTypes pins the set of component type
// strings the CLI accepts, split by the registry that claims each: a terminal
// handler, or a component-position lowering rule. The schema-parity and
// description tests below iterate these maps, so they say nothing about which
// entries they contain: a type dropped from both leaves them iterating one fewer
// entry and still green, while every document using that type stops building.
// Each name is also the user-facing contract published in the component docs, so
// a rename is a document-format change, not an internal one — moving a type from
// one registry to the other (worker and webservice, go-kure/launcher#280) is not.
func TestBuiltinComponentHandlers_RegisteredTypes(t *testing.T) {
	wantHandlers := []string{
		"cnpg-cluster", "crd", "cronjob", "daemonset", "deployment", "helmrelease", "helmtemplate", "job", "manifests",
		"oci", "passthrough", "service", "statefulset",
		// The kind-named Flux source components (go-kure/launcher#347,
		// go-kure/launcher#351).
		"bucket", "gitrepository", "helmchart", "helmrepository", "ocirepository",
		// The CloudNativePG kind components beside cnpg-cluster (go-kure/launcher#573).
		"cnpg-database", "cnpg-objectstore", "cnpg-pooler",
		// The kind components for the objects the workload kinds generated
		// (go-kure/launcher#702).
		"configmap", "persistentvolumeclaim", "serviceaccount",
	}
	sort.Strings(wantHandlers)
	wantRules := []string{"helm", "postgresql", "webservice", "worker"}

	got := make([]string, 0, len(builtinComponentHandlers()))
	for name, h := range builtinComponentHandlers() {
		got = append(got, name)
		if !h.CanHandle(name) {
			t.Errorf("handler registered under %q does not report CanHandle(%q)", name, name)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantHandlers, ",") {
		t.Errorf("builtinComponentHandlers() registers %v, want %v", got, wantHandlers)
	}

	got = got[:0]
	for name, r := range builtinComponentLoweringRules() {
		got = append(got, name)
		if r.ComponentType() != name {
			t.Errorf("lowering rule registered under %q claims ComponentType() %q", name, r.ComponentType())
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantRules, ",") {
		t.Errorf("builtinComponentLoweringRules() registers %v, want %v", got, wantRules)
	}
}

// TestNewBuiltinTransformer_PublishesWorkerSchemaUnchanged pins the schema
// HandlerSchemas publishes for "worker" byte for byte against the one the former
// WorkerHandler published, captured before worker became a lowering rule
// (pkg/oam/builtin/components/testdata/worker-property-schema.json). The
// components package pins the rule's own PropertySchema against the same file;
// this pins what a consumer of the transformer actually reads.
func TestNewBuiltinTransformer_PublishesWorkerSchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("../../oam/builtin/components/testdata/worker-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	published, ok := newBuiltinTransformer().HandlerSchemas().Components["worker"]
	if !ok {
		t.Fatal("HandlerSchemas() publishes no schema for worker")
	}
	got, err := json.MarshalIndent(published, "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Error("the schema published for worker differs from the one the former handler published")
	}
}

// TestNewBuiltinTransformer_PublishesPostgresqlSchemaUnchanged is
// TestNewBuiltinTransformer_PublishesWorkerSchemaUnchanged for "postgresql",
// against the schema the former PostgresqlHandler published
// (pkg/oam/builtin/components/testdata/postgresql-property-schema.json).
func TestNewBuiltinTransformer_PublishesPostgresqlSchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("../../oam/builtin/components/testdata/postgresql-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	published, ok := newBuiltinTransformer().HandlerSchemas().Components["postgresql"]
	if !ok {
		t.Fatal("HandlerSchemas() publishes no schema for postgresql")
	}
	got, err := json.MarshalIndent(published, "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Error("the schema published for postgresql differs from the one the former handler published")
	}
}

// TestNewBuiltinTransformer_EngineTraitsAreNotPublished: the engine-only traits
// are registered (a document using postgresql builds), are not among the traits a
// document may author, and are left out of every listing a consumer reads.
func TestNewBuiltinTransformer_EngineTraitsAreNotPublished(t *testing.T) {
	tr := newBuiltinTransformer()
	schemas := tr.HandlerSchemas().Traits
	contracts := tr.HandlerContracts().Traits
	for name := range builtinEngineTraits() {
		if _, ok := builtinTraitHandlers()[name]; ok {
			t.Errorf("engine-only trait %q is also in builtinTraitHandlers", name)
		}
		if _, ok := schemas[name]; ok {
			t.Errorf("HandlerSchemas() publishes engine-only trait %q", name)
		}
		if _, ok := contracts[name]; ok {
			t.Errorf("HandlerContracts() lists engine-only trait %q", name)
		}
	}
	if _, ok := builtinEngineTraits()["cnpg-postgresql-defaults"]; !ok {
		t.Error("builtinEngineTraits() does not register cnpg-postgresql-defaults, which the postgresql rule attaches")
	}
}

// TestNewBuiltinTransformer_PublishesPolicySchemas: every built-in policy handler's
// schema is discoverable through HandlerSchemas().Policies, the same place a caller
// finds component and trait schemas, and equals what the handler itself declares.
func TestNewBuiltinTransformer_PublishesPolicySchemas(t *testing.T) {
	published := newBuiltinTransformer().HandlerSchemas().Policies
	for name, h := range builtinPolicyHandlers() {
		got, ok := published[name]
		if !ok {
			t.Errorf("HandlerSchemas() publishes no schema for policy %q", name)
			continue
		}
		want := h.(oam.PropertySchemaProvider).PropertySchema()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("HandlerSchemas().Policies[%q] differs from the handler's PropertySchema()", name)
		}
	}
	if len(published) != len(builtinPolicyHandlers()) {
		t.Errorf("HandlerSchemas().Policies has %d entries, want %d", len(published), len(builtinPolicyHandlers()))
	}
}

// TestNewBuiltinTransformer_HandlerSchemaParity asserts that every registered
// built-in component, trait and policy handler exposes a PropertySchema (via the optional
// oam.PropertySchemaProvider interface). It iterates the registration maps
// directly — the same source newBuiltinTransformer registers from — so a handler
// added without a schema is caught by the type assertion below rather than being
// silently dropped by HandlerSchemas(). This is the launcher-side guard for the
// downstream parity gate. Also covers builtinTraitLoweringRules() (e.g. "expose")
// and builtinComponentLoweringRules() (e.g. "worker"), which are lowering rules
// rather than dispatchable handlers — HandlerSchemas() folds each into the same
// Traits/Components map as the handlers (transform.go), so this test does too, or
// a rule-only type's schema regression would go undetected.
func TestNewBuiltinTransformer_HandlerSchemaParity(t *testing.T) {
	for name, h := range builtinComponentHandlers() {
		assertExposesSchema(t, "component", name, h)
	}
	for name, h := range builtinTraitHandlers() {
		assertExposesSchema(t, "trait", name, h)
	}
	for name, r := range builtinTraitLoweringRules() {
		assertExposesSchema(t, "trait", name, r)
	}
	for name, r := range builtinComponentLoweringRules() {
		assertExposesSchema(t, "component", name, r)
	}
	for name, h := range builtinPolicyHandlers() {
		assertExposesSchema(t, "policy", name, h)
	}
}

// assertExposesSchema fails if h does not implement oam.PropertySchemaProvider or
// returns a nil schema map. An empty (non-nil) map is allowed — e.g.
// prune-protection accepts no user-facing properties.
func assertExposesSchema(t *testing.T, kind, name string, h any) {
	t.Helper()
	p, ok := h.(oam.PropertySchemaProvider)
	if !ok {
		t.Errorf("%s handler %q does not implement oam.PropertySchemaProvider", kind, name)
		return
	}
	if p.PropertySchema() == nil {
		t.Errorf("%s handler %q returns a nil PropertySchema map", kind, name)
	}
}

// TestBuiltinHandlerSchemaDescriptions asserts that every built-in handler's
// PropertySchema carries a non-empty Description on every node at every depth —
// each top-level property, each nested Properties field, and each array Items
// schema, recursively. The downstream runtime renders these in its Handler API
// Reference; a blank description shows as an empty table row, including on the
// deepest nested tables. It iterates the same registration maps as the parity test
// above so it covers exactly the set the downstream runtime renders.
func TestBuiltinHandlerSchemaDescriptions(t *testing.T) {
	for name, h := range builtinComponentHandlers() {
		assertSchemaDescribed(t, "component", name, h)
	}
	for name, h := range builtinTraitHandlers() {
		assertSchemaDescribed(t, "trait", name, h)
	}
	for name, r := range builtinTraitLoweringRules() {
		assertSchemaDescribed(t, "trait", name, r)
	}
	for name, r := range builtinComponentLoweringRules() {
		assertSchemaDescribed(t, "component", name, r)
	}
	for name, h := range builtinPolicyHandlers() {
		assertSchemaDescribed(t, "policy", name, h)
	}
}

// assertSchemaDescribed walks a handler's top-level PropertySchema entries. A
// handler that exposes no properties (e.g. prune-protection's empty map) has
// nothing to walk and passes.
func assertSchemaDescribed(t *testing.T, kind, name string, h any) {
	t.Helper()
	p, ok := h.(oam.PropertySchemaProvider)
	if !ok {
		return // TestNewBuiltinTransformer_HandlerSchemaParity already flags this.
	}
	schema := p.PropertySchema()
	for _, k := range sortedSchemaKeys(schema) {
		assertDescribed(t, fmt.Sprintf("%s %s.%s", kind, name, k), schema[k])
	}
}

// assertDescribed fails if node — or any nested Properties value or Items schema,
// recursively — has a blank (or whitespace-only) Description. Keys are sorted so
// failure output is deterministic.
func assertDescribed(t *testing.T, path string, node oam.PropertySchema) {
	t.Helper()
	if strings.TrimSpace(node.Description) == "" {
		t.Errorf("%s: PropertySchema node has empty Description", path)
	}
	for _, k := range sortedSchemaKeys(node.Properties) {
		assertDescribed(t, path+"."+k, node.Properties[k])
	}
	if node.Items != nil {
		assertDescribed(t, path+"[]", *node.Items)
	}
}

func sortedSchemaKeys(m map[string]oam.PropertySchema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestBuiltinHandlerSchemaEnumMembersHoldNoNull asserts that no built-in handler
// declares an Enum member holding a null, at any depth. It iterates the same four
// registration maps as the description test above, so it covers exactly the schemas
// that ship.
//
// This is the static half of the rule validatePropertyValue enforces at runtime
// ("schema declares Enum member N holding a null, which no validated value can
// match"). The runtime guard only fires when a document actually validates against the
// property carrying the Enum, so a defective member on a rarely-exercised nested field
// can ship unnoticed; this test reads every schema instead of waiting for a value to
// reach one.
//
// Why the rule: Enum members are compared against a value validatePropertyValue has
// already normalized (explicit nulls stripped, typed collections copied), while the
// declared members are not, so a member holding a null where the strip reaches can
// never match.
//
// The walk is pkg/oam's own CheckEnumMembersHoldNoNull, which reads every member
// through the validator's containsNullValue rather than a copy of it here
// (go-kure/launcher#464). It is deliberately STRICTER than the runtime arm: it does
// not read the schema around a member, so it also flags a null the runtime admits
// because normalization leaves it in place — under an AdditionalProperties key, inside
// an element of an array with no Items, below a schema with no Type
// (go-kure/launcher#481). No built-in schema declares a member holding a null
// anywhere, so the stricter reading costs nothing today; a built-in that needs one
// would have to stop using the stricter check here.
//
// Keyed per MEMBER, not per schema type, because that is what the validator does: an
// Enum declared on an array- or object-typed node is legal and keeps matching, as long
// as no member holds a null. This test previously asserted the per-type rule and said
// validatePropertyValue rejected it at runtime. It does not — see the Enum arm in
// pkg/oam/property_validate.go and the matching paragraph in pkg/oam/README.md, which
// record why the rule was narrowed — so that assertion would eventually have failed a
// legal schema for a reason that was not true.
func TestBuiltinHandlerSchemaEnumMembersHoldNoNull(t *testing.T) {
	for name, h := range builtinComponentHandlers() {
		assertSchemaEnumMembersNonNull(t, "component", name, h)
	}
	for name, h := range builtinTraitHandlers() {
		assertSchemaEnumMembersNonNull(t, "trait", name, h)
	}
	for name, r := range builtinTraitLoweringRules() {
		assertSchemaEnumMembersNonNull(t, "trait", name, r)
	}
	for name, r := range builtinComponentLoweringRules() {
		assertSchemaEnumMembersNonNull(t, "component", name, r)
	}
	for name, h := range builtinPolicyHandlers() {
		assertSchemaEnumMembersNonNull(t, "policy", name, h)
	}
}

// assertSchemaEnumMembersNonNull checks each of a handler's top-level PropertySchema
// entries with oam.CheckEnumMembersHoldNoNull, which walks its nested Properties and
// Items itself, mirroring assertSchemaDescribed.
func assertSchemaEnumMembersNonNull(t *testing.T, kind, name string, h any) {
	t.Helper()
	p, ok := h.(oam.PropertySchemaProvider)
	if !ok {
		return // TestNewBuiltinTransformer_HandlerSchemaParity already flags this.
	}
	schema := p.PropertySchema()
	for _, k := range sortedSchemaKeys(schema) {
		if err := oam.CheckEnumMembersHoldNoNull(schema[k]); err != nil {
			t.Errorf("%s %s.%s: no built-in schema may hold a null in an Enum member, even where validatePropertyValue would admit it: %v", kind, name, k, err)
		}
	}
}

// TestBuiltinHandlerSchemaEveryNodeDeclaresItsType asserts that every node of every
// built-in handler schema states its type exactly one way: a single Type, or a
// Types union (go-kure/launcher#383). An untyped node skips property validation's
// type check entirely, which is how the int-or-string and quantity leaves used to
// publish themselves before the union existed; a node declaring both is a schema
// error validatePropertyValue reports only once a value reaches it. Reading every
// schema catches either without waiting for a document to exercise the leaf.
func TestBuiltinHandlerSchemaEveryNodeDeclaresItsType(t *testing.T) {
	for name, h := range builtinComponentHandlers() {
		assertSchemaNodesTyped(t, "component", name, h)
	}
	for name, h := range builtinTraitHandlers() {
		assertSchemaNodesTyped(t, "trait", name, h)
	}
	for name, r := range builtinTraitLoweringRules() {
		assertSchemaNodesTyped(t, "trait", name, r)
	}
	for name, r := range builtinComponentLoweringRules() {
		assertSchemaNodesTyped(t, "component", name, r)
	}
	for name, h := range builtinPolicyHandlers() {
		assertSchemaNodesTyped(t, "policy", name, h)
	}
}

func assertSchemaNodesTyped(t *testing.T, kind, name string, h any) {
	t.Helper()
	p, ok := h.(oam.PropertySchemaProvider)
	if !ok {
		return // TestNewBuiltinTransformer_HandlerSchemaParity already flags this.
	}
	schema := p.PropertySchema()
	for _, k := range sortedSchemaKeys(schema) {
		assertSchemaNodeTyped(t, fmt.Sprintf("%s %s.%s", kind, name, k), schema[k])
	}
}

func assertSchemaNodeTyped(t *testing.T, path string, node oam.PropertySchema) {
	t.Helper()
	switch {
	case node.Type == "" && len(node.Types) == 0:
		t.Errorf("%s: declares neither Type nor Types, so property validation checks no type at all", path)
	case node.Type != "" && len(node.Types) > 0:
		t.Errorf("%s: declares both Type %q and Types %v", path, node.Type, node.Types)
	}
	for _, k := range sortedSchemaKeys(node.Properties) {
		assertSchemaNodeTyped(t, path+"."+k, node.Properties[k])
	}
	if node.Items != nil {
		assertSchemaNodeTyped(t, path+"[]", *node.Items)
	}
}

// --- Parameter substitution tests ---

const testKurelYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: test-pkg
spec:
  parameters:
  - name: image
    type: string
    required: true
  - name: replicas
    type: integer
    required: false
    default: 1
`

const testParamAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
  - name: web
    type: webservice
    properties:
      image: ${image}
      port: 8080
      replicas: ${replicas}
`

const testParamValuesYAML = `image: myregistry/app:v1.2.3
replicas: 2
`

func TestBuildCommand_ValuesFile(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with --values failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image myregistry/app:v1.2.3 in output:\n%s", got)
	}
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected replicas: 2 in output:\n%s", got)
	}
}

func TestBuildCommand_SetFlag(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{
		"build", appPath,
		"--profile", profilePath,
		"--set", "image=myregistry/app:v1.2.3",
		"--set", "replicas=2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with --set failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image myregistry/app:v1.2.3 in output:\n%s", got)
	}
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected replicas: 2 in output:\n%s", got)
	}
}

func TestBuildCommand_RequiredParamMissing(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// No --values or --set: required parameter 'image' is missing.
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when required parameter 'image' is not supplied")
	}
}

func TestBuildCommand_ValuesWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	// No kurel.yaml written — app directory has no package descriptor.
	appPath := writeTempFile(t, dir, "app.yaml", testAppYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: --values requires a kurel.yaml in the app directory")
	}
}

func TestBuildCommand_ValuesFileNotAMapping(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	// Values file is a YAML list, not a mapping.
	valuesPath := writeTempFile(t, dir, "values.yaml", "- item1\n- item2\n")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath, "--values", valuesPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when values file is a YAML list, not a mapping")
	}
}

func TestBuildCommand_SetOverridesValues(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	// values.yaml supplies replicas: 1; --set overrides it to 2.
	valuesPath := writeTempFile(t, dir, "values.yaml", "image: myregistry/app:v1.2.3\nreplicas: 1\n")

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SetArgs([]string{
		"build", appPath,
		"--profile", profilePath,
		"--values", valuesPath,
		"--set", "replicas=2",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "replicas: 2") {
		t.Errorf("expected --set to override values.yaml (replicas: 2), got:\n%s", got)
	}
}

func TestBuildCommand_DirectoryArg(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "app.yaml", testParamAppYAML)
	writeTempFile(t, dir, "kurel.yaml", testKurelYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)
	valuesPath := writeTempFile(t, dir, "values.yaml", testParamValuesYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// Pass the directory instead of app.yaml; kurel.yaml is auto-discovered.
	cmd.SetArgs([]string{"build", dir, "--profile", profilePath, "--values", valuesPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build with directory arg failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "myregistry/app:v1.2.3") {
		t.Errorf("expected image in output, got:\n%s", got)
	}
}

// buildMinimalChartTar builds a gzipped tar chart archive. extraFiles are merged on top of the
// auto-generated Chart.yaml, keyed by their path inside the archive (e.g. "chartname/templates/x.yaml").
func buildMinimalChartTar(t *testing.T, name, version string, extraFiles map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		name + "/Chart.yaml": fmt.Sprintf("apiVersion: v2\nname: %s\nversion: %s\n", name, version),
	}
	for k, v := range extraFiles {
		files[k] = v
	}
	for path, content := range files {
		hdr := &tar.Header{Name: path, Mode: 0o600, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func helmIndexYAML(name, version, url string) string {
	return fmt.Sprintf(
		"apiVersion: v1\nentries:\n  %s:\n  - name: %s\n    version: %s\n    urls:\n      - %s\ngenerated: \"2024-01-01T00:00:00Z\"\n",
		name, name, version, url,
	)
}

// TestBuildCommand_HelmtemplateComponent builds a document authoring the
// kind-named helmtemplate component end to end — parser, authored-property
// validation, handler, trait decoration, kurel build's LayoutAugmenter guard —
// against a chart served locally. The chart has two hook groups, so the
// component is a LayoutAugmenter the guard must let through (its Generate
// covers AugmentLayout, also through the prune-protection decorator), and both
// objects must reach the flat output in hook order.
func TestBuildCommand_HelmtemplateComponent(t *testing.T) {
	chartFiles := map[string]string{
		"testchart/templates/cm.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\ndata:\n  key: value\n",
		"testchart/templates/hook.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: pre-install-cm\n  annotations:\n    helm.sh/hook: pre-install\ndata:\n  key: value\n",
		"testchart/templates/NOTES.txt": "chart: testchart\nversion: 0.1.0\n",
	}
	chartBuf := buildMinimalChartTar(t, "testchart", "0.1.0", chartFiles)

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", srvURL+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			w.Write(chartBuf)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	appYAML := fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: testapp
      type: helmtemplate
      properties:
        chart: testchart
        version: "0.1.0"
        source:
          url: %s
      traits:
        - type: prune-protection
`, srvURL)

	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	hookIdx, mainIdx := strings.Index(got, "name: pre-install-cm"), strings.Index(got, "name: test-cm")
	if hookIdx < 0 || mainIdx < 0 {
		t.Fatalf("expected both rendered ConfigMaps in output, got:\n%s", got)
	}
	if hookIdx > mainIdx {
		t.Errorf("the pre-install hook object must precede the main group in the output, got:\n%s", got)
	}
	if strings.Contains(got, "chart: testchart") {
		t.Errorf("NOTES.txt content must not appear in output, got:\n%s", got)
	}
	if strings.Contains(got, "kind: HelmRelease") || strings.Contains(got, "kind: HelmRepository") {
		t.Errorf("helmtemplate must emit neither a HelmRelease nor a source CR, got:\n%s", got)
	}
	if !strings.Contains(got, "kustomize.toolkit.fluxcd.io/prune") {
		t.Errorf("expected the prune-protection annotation on the rendered objects, got:\n%s", got)
	}
}

// TestBuildCommand_HelmtemplateUndeclaredPropertyRejected: a release-identity
// property helmtemplate does not declare is a build error naming it, before any
// chart is fetched — authored-property validation runs first.
func TestBuildCommand_HelmtemplateUndeclaredPropertyRejected(t *testing.T) {
	appYAML := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: testapp
      type: helmtemplate
      properties:
        chart: testchart
        releaseName: testapp
        source:
          url: https://charts.example.com
`
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected build to reject releaseName on a helmtemplate component, got success, output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), `unsupported field "releaseName"`) {
		t.Errorf("error should name the refused field, got: %v", err)
	}
}

// topologySpreadAppYAML is a deployment component carrying the topology-spread
// trait; %s is spliced into the trait entry so the same document can carry an
// authored property.
const topologySpreadAppYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: api
      type: deployment
      properties:
        image: ghcr.io/example/api:v1.0.0
        replicas: 3
      traits:
        - type: topology-spread%s
`

// TestBuildCommand_TopologySpreadTrait is the end-to-end registration check for
// the topology-spread trait: the validate.go allowlist admits it and
// builtinTraitHandlers dispatches it, so a deployment at three replicas comes
// out with both default constraints.
func TestBuildCommand_TopologySpreadTrait(t *testing.T) {
	if _, ok := builtinTraitHandlers()["topology-spread"]; !ok {
		t.Error(`builtinTraitHandlers() does not register "topology-spread"`)
	}

	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", fmt.Sprintf(topologySpreadAppYAML, ""))
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out.String())
	}

	got := out.String()
	for _, want := range []string{
		"topologySpreadConstraints:",
		"topologyKey: kubernetes.io/hostname",
		"whenUnsatisfiable: DoNotSchedule",
		"topologyKey: topology.kubernetes.io/zone",
		"whenUnsatisfiable: ScheduleAnyway",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}
}

// TestBuildCommand_TopologySpreadTrait_RejectsProperties: the trait takes no
// properties, and kurel build reports an authored one by name.
func TestBuildCommand_TopologySpreadTrait_RejectsProperties(t *testing.T) {
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml",
		fmt.Sprintf(topologySpreadAppYAML, "\n          properties:\n            maxSkew: 2"))
	profilePath := writeTempFile(t, dir, "cluster.yaml", testClusterYAML)

	cmd := NewKurelCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected build to fail for an authored topology-spread property, output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "maxSkew") {
		t.Errorf("error should name the rejected property, got: %v", err)
	}
}

// augmenterOnlyStub implements layout.LayoutAugmenter but not
// oam.LayoutAugmentationCoverage — the fail-closed proof for every augmenter
// this repo doesn't yet know the coverage of.
type augmenterOnlyStub struct{}

func (augmenterOnlyStub) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }
func (augmenterOnlyStub) AugmentLayout(*layout.ManifestLayout) error            { return nil }

// augmenterCoverageFalseStub implements both layout.LayoutAugmenter and
// oam.LayoutAugmentationCoverage, but the latter reports false — a sibling
// case to augmenterOnlyStub that must also be rejected.
type augmenterCoverageFalseStub struct{ augmenterOnlyStub }

func (augmenterCoverageFalseStub) GenerateCoversAugmentLayout() bool { return false }

// TestRejectLayoutAugmenters_FailsClosedForUnknownAugmenter calls
// rejectLayoutAugmenters directly on hand-built stack.Node/stack.Bundle
// fixtures. No built-in component needs the guard today, so these stubs are
// what pins its fail-closed default for any future LayoutAugmenter it doesn't
// yet know the coverage of, and the error text an author would then see.
func TestRejectLayoutAugmenters_FailsClosedForUnknownAugmenter(t *testing.T) {
	t.Run("NoCoverageInterface", func(t *testing.T) {
		node := &stack.Node{
			Bundle: &stack.Bundle{
				Applications: []*stack.Application{
					{Name: "app-no-coverage", Config: augmenterOnlyStub{}},
				},
			},
		}
		err := rejectLayoutAugmenters(node)
		if err == nil {
			t.Fatal("expected rejection for a LayoutAugmenter that does not implement LayoutAugmentationCoverage")
		}
		if !strings.Contains(err.Error(), `"app-no-coverage"`) || !strings.Contains(err.Error(), "layout") {
			t.Errorf("error should name the component and explain the layout gap, got: %v", err)
		}
	})

	t.Run("CoverageReturnsFalse", func(t *testing.T) {
		node := &stack.Node{
			Bundle: &stack.Bundle{
				Applications: []*stack.Application{
					{Name: "app-coverage-false", Config: augmenterCoverageFalseStub{}},
				},
			},
		}
		if err := rejectLayoutAugmenters(node); err == nil {
			t.Fatal("expected rejection for a LayoutAugmenter whose GenerateCoversAugmentLayout returns false")
		}
	})
}

// TestBuildCommand_RefusesInDocumentObjectCollision checks that kurel build,
// to stdout and to -o without delivery, refuses an application in which two
// producers generate one object (go-kure/launcher#646), naming both, and
// writes nothing: two configmap traits named alike, and the issue's case, a
// configmap trait and a manifests component that renders the same ConfigMap.
func TestBuildCommand_RefusesInDocumentObjectCollision(t *testing.T) {
	traitAndComponent := `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: configmap
          properties:
            name: shared
    - name: extra
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: shared
            namespace: shop
`
	// A persistentvolumeclaim component and a pvc trait emitting one claim
	// (go-kure/launcher#702).
	claimComponentAndTrait := `    - name: shared
      type: persistentvolumeclaim
      properties:
        size: 1Gi
    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
      traits:
        - type: pvc
          properties:
            name: shared
            size: 5Gi
`
	tests := []struct {
		name       string
		components string
		want       string
	}{
		{"trait and trait", fmt.Sprintf(sharedConfigMapComponents, "web", "webservice", "api", "webservice"),
			`application "shop": generated-object collision: ConfigMap "shop/settings" is generated by both sub-application "settings" of component "web" and sub-application "settings" of component "api"; rename one so that each object has one producer`},
		{"trait and component", traitAndComponent,
			`application "shop": generated-object collision: ConfigMap "shop/shared" is generated by both sub-application "shared" of component "web" and component "extra"; rename one so that each object has one producer`},
		{"claim component and pvc trait", claimComponentAndTrait,
			`application "shop": generated-object collision: PersistentVolumeClaim "shop/shared" is generated by both component "shared" and sub-application "shared" of component "web"; rename one so that each object has one producer`},
	}
	for _, tt := range tests {
		for _, toDir := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, output directory %t", tt.name, toDir), func(t *testing.T) {
				appPath := writeTempFile(t, t.TempDir(), "app.yaml", fmt.Sprintf(collisionAppYAML, "shop", tt.components))
				args := []string{"build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml")}
				out := filepath.Join(t.TempDir(), "out")
				if toDir {
					args = append(args, "-o", out)
				}
				stdout, err := runKurel(t, args...)
				if err == nil {
					t.Fatal("build accepted one object generated by two producers")
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error %q does not contain %q", err, tt.want)
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
}
