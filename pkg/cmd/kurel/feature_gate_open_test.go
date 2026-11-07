package kurel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

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
		kinds: openGateKinds,
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
		kinds: openGateKinds,
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
		kinds: openGateKinds,
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
		kinds: openGateKinds,
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
		kinds: []string{"podtemplate", "replicaset", "replicationcontroller"},
		spec: `containers:
  - name: app
    image: ghcr.io/example/app:v1.0.0
hostnameOverride: web.example.com
`,
	},
	{
		name: "template resources", gate: "PodLevelResources", field: "resources",
		kinds: []string{"podtemplate", "replicaset", "replicationcontroller"},
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
// field, and holds the set of nodes that carry one to the five this change adds.
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
	for _, kind := range []string{"podtemplate", "replicaset", "replicationcontroller"} {
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
