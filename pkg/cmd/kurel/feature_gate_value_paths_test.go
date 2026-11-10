package kurel

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests below pin go-kure/launcher#1330 at the schema: every ValueGatedPaths node
// names a real field, a value its type lists and a row of the lifecycle table. The rows
// of the refusal sit in openGateCases and leafCasingCases, and the accepted side in
// feature_gate_restart_policy_test.go.

// containerRestartPolicyValues are the values of corev1.ContainerRestartPolicy, the type
// of a container's restartPolicy. A gated value must be one of them.
var containerRestartPolicyValues = []string{
	string(corev1.ContainerRestartPolicyAlways),
	string(corev1.ContainerRestartPolicyNever),
	string(corev1.ContainerRestartPolicyOnFailure),
}

// enumValuesOf holds a type that has an enum to that enum. A ValueGatedPaths path that
// ends at a type not listed here fails the test, so a new one needs its enum written.
var enumValuesOf = map[reflect.Type][]string{
	reflect.TypeOf(corev1.ContainerRestartPolicy("")): containerRestartPolicyValues,
}

// valuesOutside returns the keys of gates that enum does not list, sorted.
func valuesOutside(gates map[string]string, enum []string) []string {
	var out []string
	for value := range gates {
		if !slices.Contains(enum, value) {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

// TestFeatureGates_ValueGatedPathsNameRealFieldsAndValues holds every ValueGatedPaths path
// to a field of the Kubernetes type its open node is decoded into, every value to that
// type's enum, and every gate to a row of the lifecycle table, and holds the set of nodes
// that carry one to the five listed in holders. It holds the raw table to the workload
// kinds' shorthand table, so the two cannot diverge.
func TestFeatureGates_ValueGatedPathsNameRealFieldsAndValues(t *testing.T) {
	// The controls: a wrong path does not resolve, a mistyped value is reported, and a
	// path that ends at a type with no listed enum is told apart from one that has it.
	container := reflect.TypeOf(corev1.Container{})
	end, err := resolveGatedPathType(container, "restartPolicy")
	if err != nil {
		t.Fatalf("resolver control: a real path must resolve, got %v", err)
	}
	if _, ok := enumValuesOf[end]; !ok {
		t.Fatalf("resolver control: restartPolicy ends at %s, which has no listed enum", end)
	}
	if _, err := resolveGatedPathType(container, "restartPolicyy"); err == nil {
		t.Error("resolver control: restartPolicyy must not resolve")
	}
	if got := valuesOutside(map[string]string{"Never": "g", "neverr": "g"}, containerRestartPolicyValues); !slices.Equal(got, []string{"neverr"}) {
		t.Errorf("enum control: want [neverr] reported, got %v", got)
	}

	// webservice and worker are component lowering rules, not handlers, so a kind is
	// looked up in both registries.
	schemaOf := func(kind string) map[string]oam.PropertySchema {
		if h, ok := builtinComponentHandlers()[kind].(oam.PropertySchemaProvider); ok {
			return h.PropertySchema()
		}
		if r, ok := builtinComponentLoweringRules()[kind].(oam.PropertySchemaProvider); ok {
			return r.PropertySchema()
		}
		t.Fatalf("%s publishes no schema", kind)
		return nil
	}
	type holder struct {
		label  string
		schema oam.PropertySchema
		typ    reflect.Type
	}
	pod := schemaOf("pod")
	holders := []holder{{"pod initContainers[]", *pod["initContainers"].Items, container}}
	for _, kind := range []string{"podtemplate", "replicaset", "replicationcontroller"} {
		holders = append(holders, holder{kind + " template", schemaOf(kind)["template"], reflect.TypeOf(corev1.PodTemplateSpec{})})
	}
	holders = append(holders, holder{"cnpg-pooler template", schemaOf("cnpg-pooler")["template"], reflect.TypeOf(cnpgv1.PodTemplateSpec{})})

	for _, h := range holders {
		if len(h.schema.ValueGatedPaths) == 0 {
			t.Errorf("%s: names no value-gated path", h.label)
		}
		for path, gates := range h.schema.ValueGatedPaths {
			end, err := resolveGatedPathType(h.typ, path)
			if err != nil {
				t.Errorf("%s: value-gated path %q: %v", h.label, path, err)
				continue
			}
			enum, ok := enumValuesOf[end]
			if !ok {
				t.Errorf("%s: value-gated path %q ends at %s, which has no listed enum", h.label, path, end)
				continue
			}
			if bad := valuesOutside(gates, enum); len(bad) > 0 {
				t.Errorf("%s: value-gated path %q names values %v, which %s does not list", h.label, path, bad, end)
			}
			if _, ok := gates[string(corev1.ContainerRestartPolicyAlways)]; ok && end == reflect.TypeOf(corev1.ContainerRestartPolicy("")) {
				t.Errorf("%s: value-gated path %q gates Always, which Kubernetes accepts at every release", h.label, path)
			}
			for value, gate := range gates {
				if _, ok := oam.FeatureGateOnByDefault(gate); !ok {
					t.Errorf("%s: value-gated path %q value %q names gate %q, which is no row of the lifecycle table", h.label, path, value, gate)
				}
			}
		}
	}

	// The raw table is the shorthand table: every workload kind whose init container takes
	// a restartPolicy declares the same values with the same gates.
	want := holders[0].schema.ValueGatedPaths["restartPolicy"]
	for _, kind := range []string{"webservice", "worker", "deployment"} {
		prop, ok := schemaOf(kind)["initContainers"].Items.Properties["restartPolicy"]
		if !ok {
			t.Errorf("%s: initContainers declares no restartPolicy", kind)
			continue
		}
		if !maps.Equal(prop.ValueGates, want) {
			t.Errorf("%s: initContainers restartPolicy ValueGates %v differ from the raw kinds' %v", kind, prop.ValueGates, want)
		}
	}

	// A regular container's restartPolicy is gated whole, not by value: Kubernetes forbids
	// the field there with the gate off, Always included.
	for _, h := range []struct {
		label string
		paths map[string]string
		path  string
	}{
		{"pod containers[]", pod["containers"].Items.GatedPaths, "restartPolicy"},
		{"podtemplate template", schemaOf("podtemplate")["template"].GatedPaths, "spec.containers[].restartPolicy"},
		{"cnpg-pooler template", schemaOf("cnpg-pooler")["template"].GatedPaths, "spec.containers[].restartPolicy"},
	} {
		if h.paths[h.path] != "ContainerRestartRules" {
			t.Errorf("%s: gated paths lack %q for ContainerRestartRules, have %v", h.label, h.path, h.paths)
		}
	}
	if _, ok := pod["initContainers"].Items.GatedPaths["restartPolicy"]; ok {
		t.Error("pod initContainers[]: restartPolicy is gated whole, but Always is accepted there at every release")
	}

	// No other component node carries one: a new annotation needs its type listed here.
	carrying := 0
	var visit func(node oam.PropertySchema)
	visit = func(node oam.PropertySchema) {
		if len(node.ValueGatedPaths) > 0 {
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
		t.Errorf("%d component schema nodes carry ValueGatedPaths, want the %d listed above", carrying, len(holders))
	}
}
