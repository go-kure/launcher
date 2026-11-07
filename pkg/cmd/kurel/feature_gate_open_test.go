package kurel

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests below pin go-kure/launcher#1245: the feature-gate check of
// go-kure/launcher#1183 reaches a gated field under the open raw-PodSpec schemas
// (the pod kind's containers and initContainers, and the `template` of the kinds
// that take a pod template), not only the fields a schema declares.

// openGateKinds are the component types whose containers or pod template are open
// schemas. pod takes a PodSpec's fields at its top level; the others take them under
// template.spec.
var openGateKinds = []string{"pod", "podtemplate", "replicaset", "replicationcontroller"}

// The cnpg-pooler kind takes a pod template under `template` too, decoded into the
// CloudNativePG pod template type (go-kure/launcher#1321). openGateCaseKinds adds it to
// the kinds a case runs on, and podTemplateGateKinds adds it to the kinds that take a
// pod template, which are the only open schemas a pod spec's own fields
// (hostnameOverride, resources) can be authored under. The pod kind declares them as
// typed gated properties of its own.
var (
	openGateCaseKinds    = append(slices.Clone(openGateKinds), "cnpg-pooler")
	podTemplateGateKinds = append(slices.Clone(templateKinds), "cnpg-pooler")
)

// openGateCase is one gated property under an open schema: a PodSpec fragment that
// authors it, and the path the refusal names below the pod spec.
type openGateCase struct {
	name  string
	gate  string
	field string
	spec  string
	// kinds are the component types the fragment is authored on.
	kinds []string
}

const openGateVolume = `volumes:
  - name: envfiles
    emptyDir: {}
`

var openGateCases = []openGateCase{
	{
		name: "container fileKeyRef", gate: "EnvFiles", field: "containers[0].env[0].valueFrom.fileKeyRef",
		kinds: openGateCaseKinds,
		spec: openGateVolume + `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
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
		name: "initContainer fileKeyRef", gate: "EnvFiles", field: "initContainers[0].env[0].valueFrom.fileKeyRef",
		kinds: openGateCaseKinds,
		spec: openGateVolume + `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
initContainers:
  - name: migrate
    image: ghcr.io/example/migrate:v1.0.0
    env:
      - name: API_KEY
        valueFrom:
          fileKeyRef:
            volumeName: envfiles
            path: api.env
            key: API_KEY
`,
	},
	// A raw PodSpec takes restartPolicyRules on a regular container too (corev1.Container,
	// the same gate); the init-only limit belongs to the workload kinds' shorthand.
	{
		name: "container restartPolicyRules", gate: "ContainerRestartRules", field: "containers[0].restartPolicyRules",
		kinds: openGateCaseKinds,
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
    restartPolicy: Never
    restartPolicyRules:
      - action: Restart
        exitCodes:
          operator: In
          values: [42]
`,
	},
	{
		name: "initContainer restartPolicyRules", gate: "ContainerRestartRules", field: "initContainers[0].restartPolicyRules",
		kinds: openGateCaseKinds,
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
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
	// The pod kind declares hostnameOverride and resources at its top level, so
	// the gate already reaches them there (TestFeatureGates_PodKindsCarryTheGates);
	// the pod template's spec is the open schema.
	{
		name: "template hostnameOverride", gate: "HostnameOverride", field: "hostnameOverride",
		kinds: podTemplateGateKinds,
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
hostnameOverride: web.example.com
`,
	},
	{
		name: "template resources", gate: "PodLevelResources", field: "resources",
		kinds: podTemplateGateKinds,
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
resources:
  requests:
    cpu: 500m
    memory: 1Gi
`,
	},
}

// indentLines prefixes every non-empty line of s with n spaces.
func indentLines(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

// openGateDoc is an Application with one component of the given kind whose pod spec
// is spec, and the prefix the kind puts before a pod spec field's path ("" for pod,
// "template.spec." for the kinds that take a pod template).
func openGateDoc(kind, spec string) (doc, prefix string) {
	var props string
	switch kind {
	case "pod":
		props = indentLines(spec, 8)
	case "podtemplate":
		props = "        template:\n          spec:\n" + indentLines(spec, 12)
		prefix = "template.spec."
	case "replicaset":
		props = "        selector:\n          matchLabels:\n            tier: web\n" +
			"        template:\n          metadata:\n            labels:\n              tier: web\n          spec:\n" + indentLines(spec, 12)
		prefix = "template.spec."
	case "replicationcontroller":
		props = "        selector:\n          tier: web\n" +
			"        template:\n          metadata:\n            labels:\n              tier: web\n          spec:\n" + indentLines(spec, 12)
		prefix = "template.spec."
	case "cnpg-pooler":
		props = "        cluster:\n          name: db\n        pgbouncer: {}\n" +
			"        template:\n          spec:\n" + indentLines(spec, 12)
		prefix = "template.spec."
	default:
		panic("openGateDoc: unknown kind " + kind)
	}
	doc = fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: gates
  namespace: default
spec:
  components:
    - name: web
      type: %s
      properties:
%s`, kind, props)
	return doc, prefix
}

// TestFeatureGates_OpenSchemasRefusedBelowAcceptedAtAndAbove: each gated property
// under an open raw-PodSpec schema is refused one release below the one where its
// gate is on by default, naming the authored path, the gate and both releases, and
// is accepted at that release, above it, and when no version is stated.
func TestFeatureGates_OpenSchemasRefusedBelowAcceptedAtAndAbove(t *testing.T) {
	for _, c := range openGateCases {
		for _, kind := range c.kinds {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				on, ok := oam.FeatureGateOnByDefault(c.gate)
				if !ok {
					t.Fatalf("gate %q is not in the table", c.gate)
				}
				doc, prefix := openGateDoc(kind, c.spec)
				field := prefix + c.field
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
					t.Fatalf("at %s: want %s refused", below, field)
				}
				for _, want := range []string{field, c.gate, "from " + on.String(), "kubernetesVersion is " + below.String()} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("at %s: error %q does not name %q", below, err, want)
					}
				}
			})
		}
	}
}

// TestFeatureGates_OpenSchemasChangeNoOutput: the gate pass only refuses. The
// validated document of an accepted target is the one the capability variant (which
// consults no gate) returns, so no version stated and a version at or above the gate
// leave it unchanged. This is the control that the open-schema annotation does not
// reach property validation.
func TestFeatureGates_OpenSchemasChangeNoOutput(t *testing.T) {
	for _, c := range openGateCases {
		for _, kind := range c.kinds {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				doc, _ := openGateDoc(kind, c.spec)
				on, _ := oam.FeatureGateOnByDefault(c.gate)
				above := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor + 5}
				transformer := newBuiltinTransformer()
				parse := func() *oam.Application {
					app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
					if err != nil {
						t.Fatalf("parsing: %v", err)
					}
					return app
				}
				want, err := transformer.ValidateAuthoredPropertiesWithCapabilities(parse(), nil)
				if err != nil {
					t.Fatalf("capability variant: %v", err)
				}
				for _, target := range []*oam.KubernetesVersion{nil, &above} {
					got, err := transformer.ValidateAuthoredPropertiesForTarget(parse(), nil, target)
					if err != nil {
						t.Fatalf("target %v: %v", target, err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("target %v: validated document differs from the capability variant's", target)
					}
				}
			})
		}
	}
}

// derefType strips pointers from typ.
func derefType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

// jsonField is the field of struct type typ whose json name is name, looking into
// an embedded struct that is inlined.
func jsonField(typ reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name {
			return f, true
		}
		if f.Anonymous && tag == "" {
			if inner, ok := jsonField(derefType(f.Type), name); ok {
				return inner, true
			}
		}
	}
	return reflect.StructField{}, false
}

// resolveGatedPath follows a GatedPaths path down the Kubernetes type typ by json
// name, "[]" naming a list, and reports where it leaves the type.
func resolveGatedPath(typ reflect.Type, path string) error {
	for _, key := range strings.Split(path, ".") {
		name, each := strings.CutSuffix(key, "[]")
		typ = derefType(typ)
		if typ.Kind() != reflect.Struct {
			return fmt.Errorf("%q: %s is not an object", key, typ)
		}
		f, ok := jsonField(typ, name)
		if !ok {
			return fmt.Errorf("%q: %s has no field with json name %q", key, typ, name)
		}
		typ = f.Type
		if each {
			typ = derefType(typ)
			if typ.Kind() != reflect.Slice {
				return fmt.Errorf("%q: %s is not a list", key, typ)
			}
			typ = typ.Elem()
		}
	}
	return nil
}

// TestFeatureGates_GatedPathsNameRealFields holds every GatedPaths path to a field of
// the Kubernetes type its open node is decoded into, so a typo cannot hide a gated
// field, and holds the set of nodes that carry one to the six listed in holders.
func TestFeatureGates_GatedPathsNameRealFields(t *testing.T) {
	// The resolver itself: a wrong key, a "[]" on a non-list and a missing "[]" all fail.
	container := reflect.TypeOf(corev1.Container{})
	if err := resolveGatedPath(container, "env[].valueFrom.fileKeyRef"); err != nil {
		t.Fatalf("resolver control: a real path must resolve, got %v", err)
	}
	for _, bad := range []string{"env[].valueFrom.fileKeyRefs", "env.valueFrom.fileKeyRef", "image[]", "restartPolicyRule"} {
		if err := resolveGatedPath(container, bad); err == nil {
			t.Errorf("resolver control: %q must not resolve", bad)
		}
	}

	schemaOf := func(kind string) map[string]oam.PropertySchema {
		h, ok := builtinComponentHandlers()[kind].(oam.PropertySchemaProvider)
		if !ok {
			t.Fatalf("%s publishes no schema", kind)
		}
		return h.PropertySchema()
	}
	type holder struct {
		label  string
		schema oam.PropertySchema
		typ    reflect.Type
	}
	pod := schemaOf("pod")
	holders := []holder{
		{"pod containers[]", *pod["containers"].Items, container},
		{"pod initContainers[]", *pod["initContainers"].Items, container},
	}
	for _, kind := range []string{"podtemplate", "replicaset", "replicationcontroller"} {
		holders = append(holders, holder{kind + " template", schemaOf(kind)["template"], reflect.TypeOf(corev1.PodTemplateSpec{})})
	}
	// The pooler's template is the CloudNativePG pod template type, not corev1's.
	holders = append(holders, holder{"cnpg-pooler template", schemaOf("cnpg-pooler")["template"], reflect.TypeOf(cnpgv1.PodTemplateSpec{})})
	for _, h := range holders[:2] {
		// A raw PodSpec container and init container take the same gated fields.
		for _, want := range []string{"env[].valueFrom.fileKeyRef", "restartPolicyRules"} {
			if _, ok := h.schema.GatedPaths[want]; !ok {
				t.Errorf("%s: gated paths lack %q", h.label, want)
			}
		}
	}
	for _, h := range holders {
		if len(h.schema.GatedPaths) == 0 {
			t.Errorf("%s: names no gated path", h.label)
		}
		for path := range h.schema.GatedPaths {
			if err := resolveGatedPath(h.typ, path); err != nil {
				t.Errorf("%s: gated path %q: %v", h.label, path, err)
			}
		}
	}

	// No other component node carries one: a new annotation needs its type listed here.
	carrying := 0
	var visit func(node oam.PropertySchema)
	visit = func(node oam.PropertySchema) {
		if len(node.GatedPaths) > 0 {
			carrying++
		}
		for _, k := range sortedSchemaKeys(node.Properties) {
			visit(node.Properties[k])
		}
		if node.Items != nil {
			visit(*node.Items)
		}
	}
	for _, h := range builtinComponentHandlers() {
		if p, ok := h.(oam.PropertySchemaProvider); ok {
			for _, s := range p.PropertySchema() {
				visit(s)
			}
		}
	}
	if carrying != len(holders) {
		t.Errorf("%d component schema nodes carry GatedPaths, want the %d listed above", carrying, len(holders))
	}
}

// TestFeatureGates_CnpgPoolerFragmentsBuild: every pod spec fragment the cases author
// on a cnpg-pooler builds, with no version stated. It is the control for the pooler
// rows above, which validate only (validateForTarget never runs the handler), so they
// would also accept a fragment the pooler kind itself refuses. This test shows that
// each fragment builds, so those rows refuse a document that is otherwise valid. It
// passes before the gate change too, so it discriminates nothing about it.
func TestFeatureGates_CnpgPoolerFragmentsBuild(t *testing.T) {
	for _, c := range openGateCases {
		if !slices.Contains(c.kinds, "cnpg-pooler") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			doc, _ := openGateDoc("cnpg-pooler", c.spec)
			transformer := newBuiltinTransformer()
			app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			validated, err := transformer.ValidateAuthoredProperties(app)
			if err != nil {
				t.Fatalf("validating: %v", err)
			}
			cluster, err := transformer.Transform(validated, oam.TransformContext{Domain: kurelDomain})
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if _, err := oam.GenerateApplications(cluster); err != nil {
				t.Fatalf("generating: %v", err)
			}
		})
	}
}

// TestFeatureGates_OpenSchemasNullUsesNoGate: a null is absent at every depth, so a
// gated key authored as null under an open schema does not trip its gate. It passes
// before the change too (the open schema was never walked), so it discriminates
// nothing about the old behaviour; it guards the new walk.
func TestFeatureGates_OpenSchemasNullUsesNoGate(t *testing.T) {
	oldest := oam.KubernetesVersion{Major: 1, Minor: 1}
	specs := map[string]string{
		"hostnameOverride": "containers:\n  - name: app\n    image: ghcr.io/example/app:v1.0.0\nhostnameOverride: null\n",
		"fileKeyRef": "containers:\n  - name: app\n    image: ghcr.io/example/app:v1.0.0\n    env:\n" +
			"      - name: API_KEY\n        valueFrom:\n          fileKeyRef: null\n",
		"restartPolicyRules": "containers:\n  - name: app\n    image: ghcr.io/example/app:v1.0.0\n    restartPolicyRules: null\n",
	}
	for _, kind := range podTemplateGateKinds {
		for name, spec := range specs {
			t.Run(kind+"/"+name, func(t *testing.T) {
				doc, _ := openGateDoc(kind, spec)
				if err := validateForTarget(t, doc, &oldest); err != nil {
					t.Fatalf("a null %s must pass any version, got %v", name, err)
				}
			})
		}
	}
	doc, _ := openGateDoc("pod", specs["fileKeyRef"])
	if err := validateForTarget(t, doc, &oldest); err != nil {
		t.Fatalf("a null fileKeyRef on a pod must pass any version, got %v", err)
	}
}

// casingCase is one gated property under an open schema authored with a field name
// spelled in a different case. The raw-kind decoder matches field names
// case-insensitively (encoding/json), so the document builds the same object as the
// canonical spelling does, and the gate check has to see it as the same field.
type casingCase struct {
	name  string
	gate  string
	field string // the authored path below the pod spec, in the authored spelling
	spec  string
	// specKey is the authored spelling of the pod template's `spec` key; empty keeps `spec`.
	specKey string
	kinds   []string
}

var templateKinds = []string{"podtemplate", "replicaset", "replicationcontroller"}

const casingApp = `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
`

// leafCasingCases spell the gated field itself in another case.
var leafCasingCases = []casingCase{
	{
		name: "template HostnameOverride", gate: "HostnameOverride", field: "HostnameOverride",
		kinds: templateKinds,
		spec:  casingApp + "HostnameOverride: web.example.com\n",
	},
	{
		name: "template HOSTNAMEOVERRIDE", gate: "HostnameOverride", field: "HOSTNAMEOVERRIDE",
		kinds: templateKinds,
		spec:  casingApp + "HOSTNAMEOVERRIDE: web.example.com\n",
	},
	{
		name: "template Resources", gate: "PodLevelResources", field: "Resources",
		kinds: templateKinds,
		spec:  casingApp + "Resources:\n  requests:\n    cpu: 500m\n    memory: 1Gi\n",
	},
	{
		name: "container RestartPolicyRules", gate: "ContainerRestartRules", field: "containers[0].RestartPolicyRules",
		kinds: openGateKinds,
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
    restartPolicy: Never
    RestartPolicyRules:
      - action: Restart
        exitCodes:
          operator: In
          values: [42]
`,
	},
	{
		name: "initContainer RestartPolicyRules", gate: "ContainerRestartRules", field: "initContainers[0].RestartPolicyRules",
		kinds: openGateKinds,
		spec: casingApp + `initContainers:
  - name: migrate
    image: ghcr.io/example/migrate:v1.0.0
    RestartPolicyRules:
      - action: Restart
        exitCodes:
          operator: In
          values: [42]
`,
	},
	{
		name: "container FileKeyRef", gate: "EnvFiles", field: "containers[0].env[0].valueFrom.FileKeyRef",
		kinds: openGateKinds,
		spec: openGateVolume + `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
    env:
      - name: API_KEY
        valueFrom:
          FileKeyRef:
            volumeName: envfiles
            path: api.env
            key: API_KEY
`,
	},
}

// intermediateCasingCases spell a key on the way to the gated field in another case.
var intermediateCasingCases = []casingCase{
	{
		name: "container Env and ValueFrom", gate: "EnvFiles", field: "containers[0].Env[0].ValueFrom.fileKeyRef",
		kinds: openGateKinds,
		spec: openGateVolume + `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
    Env:
      - name: API_KEY
        ValueFrom:
          fileKeyRef:
            volumeName: envfiles
            path: api.env
            key: API_KEY
`,
	},
	{
		name: "template Spec", gate: "HostnameOverride", field: "hostnameOverride",
		kinds: templateKinds, specKey: "Spec",
		spec: casingApp + "hostnameOverride: web.example.com\n",
	},
	{
		name: "template Containers", gate: "EnvFiles", field: "Containers[0].env[0].valueFrom.fileKeyRef",
		kinds: templateKinds,
		spec: openGateVolume + `Containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
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
		name: "template InitContainers", gate: "ContainerRestartRules", field: "InitContainers[0].restartPolicyRules",
		kinds: templateKinds,
		spec: casingApp + `InitContainers:
  - name: migrate
    image: ghcr.io/example/migrate:v1.0.0
    restartPolicyRules:
      - action: Restart
        exitCodes:
          operator: In
          values: [42]
`,
	},
}

// casingDoc is the document that authors c on kind, and the path the refusal names
// for it: the kind's prefix, with the authored spelling of `spec`, and c.field.
func casingDoc(kind string, c casingCase) (doc, path string) {
	doc, prefix := openGateDoc(kind, c.spec)
	if c.specKey != "" {
		doc = strings.Replace(doc, "\n          spec:\n", "\n          "+c.specKey+":\n", 1)
		prefix = strings.Replace(prefix, ".spec.", "."+c.specKey+".", 1)
	}
	return doc, prefix + c.field
}

// requireCasingRefusedBelow: the gated field authored in another case is refused one
// release below its gate, naming the authored spelling, the gate and both releases.
func requireCasingRefusedBelow(t *testing.T, cases []casingCase) {
	t.Helper()
	for _, c := range cases {
		for _, kind := range c.kinds {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				on, ok := oam.FeatureGateOnByDefault(c.gate)
				if !ok {
					t.Fatalf("gate %q is not in the table", c.gate)
				}
				doc, field := casingDoc(kind, c)
				below := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor - 1}
				err := validateForTarget(t, doc, &below)
				if err == nil {
					t.Fatalf("%s accepted at %s: want it refused (gate %s is on from %s)", field, below, c.gate, on)
				}
				for _, want := range []string{field, c.gate, "from " + on.String(), "kubernetesVersion is " + below.String()} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("at %s: error %q does not name %q", below, err, want)
					}
				}
			})
		}
	}
}

// TestFeatureGates_OpenSchemasLeafCasingRefusedBelow: go-kure/launcher#1245 F2. The
// raw-kind decoder matches field names case-insensitively, so `HostnameOverride` sets
// the field `hostnameOverride` does; the gate must refuse it below its release too.
func TestFeatureGates_OpenSchemasLeafCasingRefusedBelow(t *testing.T) {
	requireCasingRefusedBelow(t, leafCasingCases)
}

// TestFeatureGates_OpenSchemasIntermediateCasingRefusedBelow: the same for a key on the
// way to the gated field (`Spec`, `Containers`, `Env`, `ValueFrom`).
func TestFeatureGates_OpenSchemasIntermediateCasingRefusedBelow(t *testing.T) {
	requireCasingRefusedBelow(t, intermediateCasingCases)
}

// TestFeatureGates_OpenSchemasCasingAcceptedAtAndAbove: the control that the handler
// builds every casing variant above, so a refusal below is the gate's and not the
// decoder's: accepted with no version stated, at the gate's release, and above it.
func TestFeatureGates_OpenSchemasCasingAcceptedAtAndAbove(t *testing.T) {
	for _, c := range append(slices.Clone(leafCasingCases), intermediateCasingCases...) {
		for _, kind := range c.kinds {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				on, ok := oam.FeatureGateOnByDefault(c.gate)
				if !ok {
					t.Fatalf("gate %q is not in the table", c.gate)
				}
				doc, _ := casingDoc(kind, c)
				above := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor + 5}
				if err := validateForTarget(t, doc, nil); err != nil {
					t.Fatalf("no version stated: want accepted, got %v", err)
				}
				for _, v := range []oam.KubernetesVersion{on, above} {
					if err := validateForTarget(t, doc, &v); err != nil {
						t.Errorf("at %s: want accepted, got %v", v, err)
					}
				}
			})
		}
	}
}

// TestFeatureGates_OpenSchemasDuplicateSpellingRefusedBelow: when a field is authored
// under two spellings, the gate checks every spelling, so it refuses below the gate
// whichever one the decoder keeps. Neither spelling here is the canonical one, and in
// the second case the spelling that sets the field sits beside a null one (a null is
// absent). The document is built, not refused, with the duplicate (that is tracked
// separately), so this test asserts only the gate's refusal.
func TestFeatureGates_OpenSchemasDuplicateSpellingRefusedBelow(t *testing.T) {
	on, ok := oam.FeatureGateOnByDefault("HostnameOverride")
	if !ok {
		t.Fatal("gate HostnameOverride is not in the table")
	}
	below := oam.KubernetesVersion{Major: on.Major, Minor: on.Minor - 1}
	cases := []struct {
		name string
		spec string
		// set is the authored spelling that sets the field, which the error must name.
		set string
	}{
		{"both set", casingApp + "HostnameOverride: web.example.com\nHOSTNAMEOVERRIDE: other.example.com\n", "HostnameOverride"},
		{"null beside set", casingApp + "HostnameOverride: null\nHOSTNAMEOVERRIDE: web.example.com\n", "HOSTNAMEOVERRIDE"},
	}
	for _, c := range cases {
		for _, kind := range templateKinds {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				doc, prefix := openGateDoc(kind, c.spec)
				err := validateForTarget(t, doc, &below)
				if err == nil {
					t.Fatalf("%s%s accepted at %s: want it refused (gate HostnameOverride is on from %s)", prefix, c.set, below, on)
				}
				for _, want := range []string{"HostnameOverride", "from " + on.String(), "kubernetesVersion is " + below.String()} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("at %s: error %q does not name %q", below, err, want)
					}
				}
				if c.name == "null beside set" && !strings.Contains(err.Error(), prefix+c.set) {
					t.Errorf("at %s: error %q does not name the spelling that sets the field, %s%s", below, err, prefix, c.set)
				}
			})
		}
	}
}
