package components_test

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of go-kure/launcher#790 that project a Kubernetes core
// object: each publishes the json fields of the object's spec type, decodes
// them strictly, and emits one object carrying identity and the authored spec.

// coreKindSchemas lists those components with the upstream spec type each
// projects. Every field of each spec type is authorable, so none has an
// exclusion list.
var coreKindSchemas = []struct {
	component string
	typ       reflect.Type
	handler   interface {
		PropertySchema() map[string]oam.PropertySchema
	}
}{
	{"namespace", reflect.TypeFor[corev1.NamespaceSpec](), &components.NamespaceHandler{}},
	{"limitrange", reflect.TypeFor[corev1.LimitRangeSpec](), &components.LimitRangeHandler{}},
	{"resourcequota", reflect.TypeFor[corev1.ResourceQuotaSpec](), &components.ResourceQuotaHandler{}},
	{"persistentvolume", reflect.TypeFor[corev1.PersistentVolumeSpec](), &components.PersistentVolumeHandler{}},
}

// checkCoreKindProperty holds one published property to the Go type it decodes
// into: the same PropertyType, a description, an open object for a structured
// value (the strict decode checks its content), and for a list an item schema
// held to the element type the same way.
func checkCoreKindProperty(t *testing.T, key string, prop oam.PropertySchema, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if want := schemaTypeForGo(typ); prop.Type != want {
		t.Errorf("schema key %q declares type %q, but the field is %s (want %q)", key, prop.Type, typ, want)
		return
	}
	if prop.Description == "" {
		t.Errorf("schema key %q has no description", key)
	}
	switch prop.Type {
	case oam.PropertyTypeObject:
		if !prop.AdditionalProperties {
			t.Errorf("schema key %q is a closed object; structured fields are open objects checked by the strict decode", key)
		}
	case oam.PropertyTypeArray:
		if prop.Items == nil {
			t.Errorf("schema key %q declares no item schema", key)
			return
		}
		checkCoreKindProperty(t, key+"[]", *prop.Items, typ.Elem())
	case oam.PropertyTypeString, oam.PropertyTypeInteger, oam.PropertyTypeBoolean, oam.PropertyTypeNumber:
		// A scalar has no structure to hold beyond its type and description.
	}
}

// TestCoreKindSchemas_CoverSpec is TestCnpgKindSchemas_CoverSpec for the core
// kinds: the schema publishes exactly the spec type's json fields, each with
// the type its Go field decodes from. A dependency bump that adds, removes or
// retypes a top-level field fails here, naming it.
func TestCoreKindSchemas_CoverSpec(t *testing.T) {
	for _, tt := range coreKindSchemas {
		t.Run(tt.component, func(t *testing.T) {
			fields := specJSONFields(t, tt.typ)
			schema := tt.handler.PropertySchema()
			for _, name := range slices.Sorted(maps.Keys(fields)) {
				prop, ok := schema[name]
				if !ok {
					t.Errorf("%s field %q is not published in the %s schema", tt.typ, name, tt.component)
					continue
				}
				checkCoreKindProperty(t, name, prop, fields[name])
			}
			for _, key := range slices.Sorted(maps.Keys(schema)) {
				if _, ok := fields[key]; !ok {
					t.Errorf("schema key %q has no %s json field; the strict decode would refuse every value", key, tt.typ)
				}
			}
		})
	}
}

// TestCoreKindSchemas_EveryFieldReachable is
// TestCnpgClusterSchema_EveryFieldReachable for the core kinds.
func TestCoreKindSchemas_EveryFieldReachable(t *testing.T) {
	for _, tt := range coreKindSchemas {
		if got := builtin.UnreachableJSONFields(tt.typ); len(got) != 0 {
			t.Errorf("%s fields unreachable through the strict decode: %v", tt.typ, got)
		}
	}
}

// coreKindNamespace is the build namespace the core-kind tests convert and
// generate in.
const coreKindNamespace = "apps"

// coreKindErr returns the conversion error of one component.
func coreKindErr(h oam.ComponentHandler, typ, name string, props map[string]any) error {
	_, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, coreKindNamespace)
	return err
}

// generateCoreKind converts one component, applies a restrictive policy and no
// policy, and generates. It checks what every core kind promises: exactly one
// object, named after the component, with no label and no annotation of
// launcher's. The caller checks the namespace, which depends on the kind's
// scope.
func generateCoreKind(t *testing.T, h oam.ComponentHandler, typ, name string, props map[string]any) client.Object {
	t.Helper()
	one := int32(1)
	restrictive := &stubPolicy{
		maxReplicas: &one, maxCPU: "1m", maxMemory: "1Ki", maxStorageSize: "1Ki",
		allowedRegistries: []string{"registry.invalid"},
	}
	return generateCoreKindUnder(t, h, typ, name, props, restrictive, nil)
}

// generateCoreKindUnder is generateCoreKind under the given policies, for a
// kind the environment policy does constrain.
func generateCoreKindUnder(t *testing.T, h oam.ComponentHandler, typ, name string, props map[string]any, policies ...oam.Policy) client.Object {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	for _, p := range policies {
		if err := cfg.(policyApplier).ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy(%v): %v", p, err)
		}
	}
	objs, err := cfg.Generate(stack.NewApplication(name, coreKindNamespace, cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	obj := *objs[0]
	if obj.GetName() != name {
		t.Errorf("name = %q, want the component name %q", obj.GetName(), name)
	}
	if len(obj.GetLabels()) != 0 || len(obj.GetAnnotations()) != 0 {
		t.Errorf("labels = %v, annotations = %v; want none", obj.GetLabels(), obj.GetAnnotations())
	}
	return obj
}
