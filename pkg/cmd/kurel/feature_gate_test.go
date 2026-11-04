package kurel

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests below pin go-kure/launcher#1183: a ClusterProfile that states
// spec.kubernetesVersion has an authored property refused when its Kubernetes
// feature gate (PropertySchema.Gate) is off by default at that release, and a
// profile that states none is judged exactly as before.

// TestFeatureGates_TableAndSchemasAgree holds the lifecycle table
// (pkg/oam/featuregates.go) and the built-in schemas to each other: every Gate a
// schema carries names a row, and every row is carried by at least one schema.
// Neither direction is checked against upstream: a dependency bump re-reads the
// table (DEVELOPMENT.md).
func TestFeatureGates_TableAndSchemasAgree(t *testing.T) {
	used := map[string]bool{}
	var visit func(path string, node oam.PropertySchema)
	visit = func(path string, node oam.PropertySchema) {
		if node.Gate != "" {
			used[node.Gate] = true
			if _, ok := oam.FeatureGateOnByDefault(node.Gate); !ok {
				t.Errorf("%s: Gate %q names no row of the lifecycle table", path, node.Gate)
			}
		}
		for _, k := range sortedSchemaKeys(node.Properties) {
			visit(path+"."+k, node.Properties[k])
		}
		if node.Items != nil {
			visit(path+"[]", *node.Items)
		}
	}
	walk := func(kind, name string, h any) {
		p, ok := h.(oam.PropertySchemaProvider)
		if !ok {
			return
		}
		schema := p.PropertySchema()
		for _, k := range sortedSchemaKeys(schema) {
			visit(fmt.Sprintf("%s %s.%s", kind, name, k), schema[k])
		}
	}
	for name, h := range builtinComponentHandlers() {
		walk("component", name, h)
	}
	for name, h := range builtinTraitHandlers() {
		walk("trait", name, h)
	}
	for name, r := range builtinTraitLoweringRules() {
		walk("trait", name, r)
	}
	for name, r := range builtinComponentLoweringRules() {
		walk("component", name, r)
	}
	for name, h := range builtinPolicyHandlers() {
		walk("policy", name, h)
	}
	for _, gate := range oam.FeatureGateNames() {
		if !used[gate] {
			t.Errorf("table row %q is carried by no built-in schema", gate)
		}
	}
}

// gateCase is one gated property, with a component's properties (YAML, indented
// to sit under `properties:`) that author it.
type gateCase struct {
	name  string
	gate  string
	field string // the path the refusal names
	props string
}

var gateCases = []gateCase{
	{
		name: "fileKeyRef", gate: "EnvFiles", field: "env[0].valueFrom.fileKeyRef",
		props: `
        volumes:
          - name: envfiles
            type: emptyDir
            mountPath: /etc/envfiles
        env:
          - name: API_KEY
            valueFrom:
              fileKeyRef:
                volumeName: envfiles
                path: api.env
                key: API_KEY
`,
	},
	{
		name: "initContainer restartPolicyRules", gate: "ContainerRestartRules", field: "initContainers[0].restartPolicyRules",
		props: `
        initContainers:
          - name: migrate
            image: ghcr.io/example/migrate:v1.0.0
            restartPolicyRules:
              - action: Restart
                exitCodes:
                  operator: In
                  values: [42]
`,
	},
	{
		name: "podResources", gate: "PodLevelResources", field: "podResources",
		props: `
        podResources:
          requests:
            cpu: 500m
            memory: 1Gi
`,
	},
	{
		name: "hostnameOverride", gate: "HostnameOverride", field: "hostnameOverride",
		props: `
        hostnameOverride: web.example.com
`,
	},
}

// gatedKinds are the component types that take every gated property above; a
// webservice and a worker reach them through their lowering rules' schemas.
var gatedKinds = []string{"deployment", "webservice", "worker"}

func gateAppYAML(kind, props string) string {
	return fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: gates
  namespace: default
spec:
  components:
    - name: web
      type: %s
      properties:
        image: ghcr.io/example/web:v1.0.0%s`, kind, props)
}

// validateForTarget runs the authored-property validation of appYAML for target
// (nil: no version stated), as `kurel build` does.
func validateForTarget(t *testing.T, appYAML string, target *oam.KubernetesVersion) error {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return transformer.ValidateAuthoredPropertiesForTarget(app, nil, target)
}

// TestFeatureGates_RefusedBelowAcceptedAtAndAbove: each gated property is
// refused one release below the one where its gate is on by default, naming the
// property, the gate and both releases, and is accepted at that release, above
// it, and when the profile states no version at all. It takes the release from
// the table under test, so it checks that the behaviour matches the table, not
// that the table is right: TestFeatureGateTable_PinnedToUpstream (pkg/oam) pins
// each row's release to upstream.
func TestFeatureGates_RefusedBelowAcceptedAtAndAbove(t *testing.T) {
	for _, kind := range gatedKinds {
		for _, c := range gateCases {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				on, ok := oam.FeatureGateOnByDefault(c.gate)
				if !ok {
					t.Fatalf("gate %q is not in the table", c.gate)
				}
				doc := gateAppYAML(kind, c.props)
				below := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor - 1}
				above := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor + 5}

				if err := validateForTarget(t, doc, nil); err != nil {
					t.Fatalf("no version stated: want the document accepted as before, got %v", err)
				}
				for _, v := range []oam.KubernetesVersion{on, above} {
					if err := validateForTarget(t, doc, &v); err != nil {
						t.Errorf("at %s: want accepted, got %v", v, err)
					}
				}
				err := validateForTarget(t, doc, &below)
				if err == nil {
					t.Fatalf("at %s: want %s refused", below, c.field)
				}
				for _, want := range []string{c.field, c.gate, "from " + on.String(), "kubernetesVersion is " + below.String()} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("at %s: error %q does not name %q", below, err, want)
					}
				}
			})
		}
	}
}

// TestFeatureGates_PodKindsCarryTheGates: the pod kind's own schema (pod.go
// declares a PodSpec's fields apart from the workload kinds') names the same
// gates for the same fields.
func TestFeatureGates_PodKindsCarryTheGates(t *testing.T) {
	h, ok := builtinComponentHandlers()["pod"].(oam.PropertySchemaProvider)
	if !ok {
		t.Fatal("the pod handler publishes no schema")
	}
	schema := h.PropertySchema()
	for key, gate := range map[string]string{"resources": "PodLevelResources", "hostnameOverride": "HostnameOverride"} {
		if got := schema[key].Gate; got != gate {
			t.Errorf("pod %s: Gate = %q, want %q", key, got, gate)
		}
	}
}

// TestFeatureGates_NullUsesNoGate: a null is absent at every depth, so a gated key
// authored as null does not trip its gate.
func TestFeatureGates_NullUsesNoGate(t *testing.T) {
	doc := gateAppYAML("deployment", "\n        hostnameOverride: null\n")
	if err := validateForTarget(t, doc, &oam.KubernetesVersion{Major: 1, Minor: 1}); err != nil {
		t.Fatalf("a null hostnameOverride must pass any version, got %v", err)
	}
}

// profileWithSpec is a ClusterProfile whose spec is the given YAML mapping body.
func profileWithSpec(spec string) string {
	return "apiVersion: launcher.gokure.dev/v1alpha1\nkind: ClusterProfile\nmetadata:\n  name: test-cluster\nspec:" + spec
}

// TestBuildCommand_KubernetesVersion drives the gates through `kurel build`:
// the profile's version reaches the check, the refusal names the field, and a
// document either builds identically at every version it is accepted at or is
// refused.
func TestBuildCommand_KubernetesVersion(t *testing.T) {
	// One document that authors every gated property a build accepts together
	// (restartPolicyRules needs a restartPolicy beside it, which the per-property
	// matrix above does not exercise at transform time).
	const props = `
        volumes:
          - name: envfiles
            type: emptyDir
            mountPath: /etc/envfiles
        env:
          - name: API_KEY
            valueFrom:
              fileKeyRef:
                volumeName: envfiles
                path: api.env
                key: API_KEY
        initContainers:
          - name: migrate
            image: ghcr.io/example/migrate:v1.0.0
            restartPolicy: OnFailure
            restartPolicyRules:
              - action: Restart
                exitCodes:
                  operator: In
                  values: [42]
        podResources:
          requests:
            cpu: 500m
            memory: 1Gi
        hostnameOverride: web.example.com
`
	doc := gateAppYAML("deployment", props)

	// build runs `kurel build` with a profile stating version, or none when "".
	build := func(version string) ([]map[string]any, error) {
		spec := " {}\n"
		if version != "" {
			spec = fmt.Sprintf("\n  kubernetesVersion: %q\n", version)
		}
		return buildWithProfile(t, doc, profileWithSpec(spec))
	}

	// The refusal: the highest gate here turns on at 1.35, so 1.34 refuses the
	// first gated key in sorted order, and the message names the profile field.
	_, err := build("1.34")
	if err == nil {
		t.Fatal("a 1.34 profile must refuse the gated properties")
	}
	for _, want := range []string{"kubernetesVersion", "1.34", "feature gate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	// The disclosure claim: the check only refuses. What an accepted document
	// renders to does not depend on the version, nor on whether one is stated.
	base, err := build("")
	if err != nil {
		t.Fatalf("no version stated: %v", err)
	}
	if len(base) == 0 {
		t.Fatal("the base build rendered nothing")
	}
	for _, v := range []string{"1.35", "v1.37.2", "1.99"} {
		got, err := build(v)
		if err != nil {
			t.Errorf("kubernetesVersion %s: %v", v, err)
			continue
		}
		if !reflect.DeepEqual(got, base) {
			t.Errorf("kubernetesVersion %s renders differently from no version:\n got  %v\n base %v", v, summarize(got), summarize(base))
		}
	}

	// A malformed version fails the profile parse, naming the field.
	if _, err := build("1.34abc"); err == nil || !strings.Contains(err.Error(), "kubernetesVersion") {
		t.Errorf("a malformed version must be refused naming the field, got %v", err)
	}
}

// summarize names the kinds and names of docs, for a readable failure.
func summarize(docs []map[string]any) []string {
	var out []string
	for _, d := range docs {
		meta, _ := d["metadata"].(map[string]any)
		out = append(out, fmt.Sprintf("%v/%v", d["kind"], meta["name"]))
	}
	sort.Strings(out)
	return out
}
