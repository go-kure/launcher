package kurel

import (
	"fmt"
	"reflect"
	"slices"
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
		// The gates an open schema names for fields below it (go-kure/launcher#1245)
		// are held to the table in both directions as the declared ones are.
		for p, gate := range node.GatedPaths {
			used[gate] = true
			if _, ok := oam.FeatureGateOnByDefault(gate); !ok {
				t.Errorf("%s: GatedPaths %q names gate %q, which names no row of the lifecycle table", path, p, gate)
			}
		}
		// A per-value gate (PropertySchema.ValueGates) names a row too, and must sit on
		// a value the property's Enum lists (go-kure/launcher#1230).
		for value, gate := range node.ValueGates {
			used[gate] = true
			if _, ok := oam.FeatureGateOnByDefault(gate); !ok {
				t.Errorf("%s: ValueGates[%q] names gate %q, which is no row of the lifecycle table", path, value, gate)
			}
			listed := false
			for _, e := range node.Enum {
				if e == value {
					listed = true
				}
			}
			if !listed {
				t.Errorf("%s: ValueGates names %q, which the property's Enum does not list", path, value)
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
	// A gate a schema does not carry may be checked by code on the objects it
	// generates (oam.CodeCheckedGates, go-kure/launcher#1230): it is held to the
	// table the same way.
	for _, gate := range oam.CodeCheckedGates() {
		used[gate] = true
		if _, ok := oam.FeatureGateOnByDefault(gate); !ok {
			t.Errorf("CodeCheckedGates names %q, which names no row of the lifecycle table", gate)
		}
	}
	for _, gate := range oam.FeatureGateNames() {
		if !used[gate] {
			t.Errorf("table row %q is carried by no built-in schema and checked by no code", gate)
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
	// value is set for a per-value gate (PropertySchema.ValueGates): the refusal then
	// names the authored value as well.
	value string
	// kinds are the component types that take the property; nil means gatedKinds.
	kinds []string
}

// takes reports whether a component of the kind accepts the case's property.
func (c gateCase) takes(kind string) bool {
	if c.kinds == nil {
		return slices.Contains(gatedKinds, kind)
	}
	return slices.Contains(c.kinds, kind)
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

	// Per-value gates (PropertySchema.ValueGates, go-kure/launcher#1230): the
	// property is accepted at every release, one of its values is not. The
	// init container's value sits inside an array item, so these also prove the
	// gate walk descends into items.
	{
		name: "initContainer restartPolicy Never", gate: "ContainerRestartRules", field: "initContainers[0].restartPolicy", value: "Never",
		props: `
        initContainers:
          - name: migrate
            image: ghcr.io/example/migrate:v1.0.0
            restartPolicy: Never
`,
	},
	{
		name: "initContainer restartPolicy OnFailure", gate: "ContainerRestartRules", field: "initContainers[0].restartPolicy", value: "OnFailure",
		props: `
        initContainers:
          - name: migrate
            image: ghcr.io/example/migrate:v1.0.0
            restartPolicy: OnFailure
`,
	},
	{
		name: "trafficDistribution PreferSameZone", gate: "PreferSameTrafficDistribution", field: "trafficDistribution", value: "PreferSameZone",
		kinds: []string{"service"},
		props: `
        trafficDistribution: PreferSameZone
`,
	},
	{
		name: "trafficDistribution PreferSameNode", gate: "PreferSameTrafficDistribution", field: "trafficDistribution", value: "PreferSameNode",
		kinds: []string{"service"},
		props: `
        trafficDistribution: PreferSameNode
`,
	},
}

// ungatedValueCases author a value of a per-value-gated property that no gate
// stands behind: it is accepted at every release, with or without a version.
// Always makes an init container a native sidecar, which the cluster accepts with
// the gate on or off; PreferClose is the older name of PreferSameZone.
var ungatedValueCases = []gateCase{
	{
		name: "initContainer restartPolicy Always", field: "initContainers[0].restartPolicy",
		props: `
        initContainers:
          - name: proxy
            image: ghcr.io/example/proxy:v1.0.0
            restartPolicy: Always
`,
	},
	{
		name: "trafficDistribution PreferClose", field: "trafficDistribution", kinds: []string{"service"},
		props: `
        trafficDistribution: PreferClose
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
      properties:%s%s`, kind, gateBase(kind), props)
}

// gateBase is the one property a component of the kind needs besides the gated one
// under test: a workload takes an image, a service takes a port.
func gateBase(kind string) string {
	if kind == "service" {
		return "\n        ports:\n          - port: 80"
	}
	return "\n        image: ghcr.io/example/web:v1.0.0"
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
	_, err = transformer.ValidateAuthoredPropertiesForTarget(app, nil, target)
	return err
}

// TestFeatureGates_RefusedBelowAcceptedAtAndAbove: each gated property is
// refused one release below the one where its gate is on by default, naming the
// property, the gate and both releases, and is accepted at that release, above
// it, and when the profile states no version at all. It takes the release from
// the table under test, so it checks that the behaviour matches the table, not
// that the table is right: TestFeatureGateTable_PinnedToUpstream (pkg/oam) pins
// each row's release to upstream.
func TestFeatureGates_RefusedBelowAcceptedAtAndAbove(t *testing.T) {
	for _, kind := range append(slices.Clone(gatedKinds), "service") {
		for _, c := range gateCases {
			if !c.takes(kind) {
				continue
			}
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
				wants := []string{c.field, c.gate, "from " + on.String(), "kubernetesVersion is " + below.String()}
				if c.value != "" {
					wants = append(wants, fmt.Sprintf("value %q", c.value))
				}
				for _, want := range wants {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("at %s: error %q does not name %q", below, err, want)
					}
				}
			})
		}
	}
}

// TestFeatureGates_UngatedValuesOfAGatedPropertyPass: a per-value gate refuses its
// own values only. The sibling values of the same property, Always on an init
// container and PreferClose on a Service, are accepted at a release below every
// gate the table holds (go-kure/launcher#1230).
func TestFeatureGates_UngatedValuesOfAGatedPropertyPass(t *testing.T) {
	old := oam.KubernetesVersion{Major: 1, Minor: 1}
	for _, kind := range append(slices.Clone(gatedKinds), "service") {
		for _, c := range ungatedValueCases {
			if !c.takes(kind) {
				continue
			}
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				doc := gateAppYAML(kind, c.props)
				if err := validateForTarget(t, doc, &old); err != nil {
					t.Errorf("at %s: want accepted, got %v", old, err)
				}
				if err := validateForTarget(t, doc, nil); err != nil {
					t.Errorf("no version stated: want accepted, got %v", err)
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
