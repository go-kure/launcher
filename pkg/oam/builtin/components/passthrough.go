package components

import (
	"reflect"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PassthroughHandler handles the generic "passthrough" component type, which emits
// an arbitrary Kubernetes object (CRD or non-standard type) declared inline. The
// component properties separate control (clusterScoped) from the emitted body (object):
//
//	type: passthrough
//	properties:
//	  clusterScoped: false   # optional, default false
//	  object:                # the Kubernetes object, emitted verbatim
//	    apiVersion: ...
//	    kind: ...
//	    metadata: { ... }    # optional; name defaults to the component name
//	    spec: { ... }        # any top-level fields pass through
//
// "Object" is singular and is enforced as such: a list-shaped body (an `items`
// sequence, by apimachinery's own IsList) is rejected in ToApplicationConfig, because
// Generate emits ONE resource and a list would smuggle N past every per-object rule
// downstream. Declare one passthrough component per object.
type PassthroughHandler struct{}

// CanHandle returns true for the passthrough component type.
func (h *PassthroughHandler) CanHandle(componentType string) bool {
	return componentType == "passthrough"
}

// PropertySchema declares the passthrough component's properties. `object` is the
// escape-hatch body emitted verbatim, so it is an open object (additionalProperties).
func (h *PassthroughHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"object":        {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "The single Kubernetes object emitted verbatim (apiVersion, kind, metadata, and any body fields); a list is rejected."},
		"clusterScoped": {Type: oam.PropertyTypeBoolean, Default: false, Description: "Whether the emitted object is cluster-scoped, suppressing namespace stamping."},
	}
}

// ToApplicationConfig validates the passthrough properties and returns a PassthroughConfig.
func (h *PassthroughHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	props := component.Properties

	for key := range props {
		if key != "object" && key != "clusterScoped" {
			return nil, errors.Errorf("passthrough component %q: unsupported property %q (only 'object' and 'clusterScoped' are allowed)", component.Name, key)
		}
	}

	clusterScoped := false
	if raw, ok := props["clusterScoped"]; ok {
		b, isBool := raw.(bool)
		if !isBool {
			return nil, errors.Errorf("passthrough component %q: 'clusterScoped' must be a bool", component.Name)
		}
		clusterScoped = b
	}

	rawObj, ok := props["object"]
	if !ok {
		return nil, errors.Errorf("passthrough component %q: required property 'object' missing", component.Name)
	}
	object, ok := rawObj.(map[string]any)
	if !ok {
		return nil, errors.Errorf("passthrough component %q: 'object' must be a map", component.Name)
	}

	if err := validateEmittableObject(component.Name, object); err != nil {
		return nil, err
	}

	if rawMeta, ok := object["metadata"]; ok {
		meta, ok := rawMeta.(map[string]any)
		if !ok {
			return nil, errors.Errorf("passthrough component %q: object.metadata must be a map", component.Name)
		}
		if clusterScoped {
			if ns, ok := meta["namespace"].(string); ok && ns != "" {
				return nil, errors.Errorf("passthrough component %q: object.metadata.namespace must not be set when clusterScoped is true", component.Name)
			}
		}
	}

	return &PassthroughConfig{
		componentName: component.Name,
		Namespace:     namespace,
		ClusterScoped: clusterScoped,
		// Frozen here, not aliased, so a caller mutating the map it passed in
		// cannot change what was validated. Generate still copies again per call —
		// it stamps metadata and may run more than once, and that copy protects
		// the source properties, not this invariant.
		//
		// The copy alone does NOT make the checks above binding, and the comment
		// here used to claim it did. Object is an exported field: a caller holding
		// the config can assign a fresh map to it, and a struct literal or a JSON
		// decode never runs this function at all. Generate re-runs the arms on the
		// map it is about to emit; that, not the copy, is what closes those routes.
		Object: deepCopyMap(object),
	}, nil
}

// validateEmittableObject holds everything that must be true of the map passthrough
// is about to emit, so that ToApplicationConfig and Generate cannot disagree about
// what a valid body is. Splitting it — the constructor checking identity, Generate
// checking only list shape — is what let `&PassthroughConfig{Object: map[string]any{}}`
// through: non-nil, so it cleared the nil guard, and carrying no items, so it cleared
// both list arms, leaving Generate to emit a document consisting of nothing but the
// metadata it had just stamped on.
func validateEmittableObject(componentName string, object map[string]any) error {
	if apiVersion, ok := object["apiVersion"].(string); !ok || apiVersion == "" {
		return errors.Errorf("passthrough component %q: object.apiVersion is required and must be a non-empty string", componentName)
	}
	kind, ok := object["kind"].(string)
	if !ok || kind == "" {
		return errors.Errorf("passthrough component %q: object.kind is required and must be a non-empty string", componentName)
	}
	return rejectListEnvelope(componentName, kind, object)
}

// rejectListEnvelope refuses the two list-shaped bodies passthrough cannot honestly
// emit as one resource. It is reached from ToApplicationConfig, and again from
// Generate on the map that is actually about to be emitted — see the note on
// PassthroughConfig.Object for why the second call is not redundant.
//
// A List is N objects and this handler's contract is one — the type comment on
// PassthroughHandler and the schema description both say "the Kubernetes object
// emitted verbatim", singular, so a list was never inside the contract. It matters
// here rather than downstream because Generate emits the map as a SINGLE
// unstructured and stamps a name and a namespace onto it: a list would arrive as
// one named envelope whose items never see per-object label mutation, namespace
// stamping or ownership checks, while Flux's kustomize unwraps it at apply time
// into N objects that do reach the cluster. One envelope bypasses every per-object
// rule at once, which is why no single downstream check can catch it.
func rejectListEnvelope(componentName, kind string, object map[string]any) error {
	// Keyed on apimachinery's own predicate, not on the kind name. Unstructured.IsList
	// is "items is present AND is a []interface{}" (k8s.io/apimachinery v0.36.3), and a
	// kind check is wrong in BOTH directions: a typed ConfigMapList carries items and
	// would slip past `kind == "List"`, while a CRD whose kind merely ENDS in "List"
	// with no items is not a list at all and must keep compiling. Both directions are
	// pinned in TestPassthrough_ListShapedObjectIsRejected.
	//
	// Residual, scoped rather than merely disclosed: IsList requires exactly
	// []interface{}, so a Go-assembled []map[string]any under `items` would slip past.
	// NO IN-REPO PRODUCER CAN CONSTRUCT THAT TODAY — the authored path decodes through
	// yaml.v3 into any, which yields []interface{}, and every LowerComponent
	// implementation in this module is in a _test.go (see also transform.go's note that
	// no ComponentLoweringRule ships yet). Two events make it live, and the second is
	// the smaller edit: a production ComponentLoweringRule, OR any trait rule
	// populating LoweringResult.Components, which PositionTrait already permits
	// (lowering.go loweringPositionRules) and which the one production rule today,
	// traits.ExposeRule, does not do. Whoever does either is the trigger. Same class as
	// go-kure/launcher#428.
	if (&unstructured.Unstructured{Object: object}).IsList() {
		return errors.Errorf(
			"passthrough component %q: 'object' is a list (kind %q with an 'items' array), but passthrough emits a single object verbatim — declare one passthrough component per object",
			componentName, kind)
	}
	// The second arm covers what IsList structurally cannot see, and it is the
	// silent-drop end of the same bug. IsList wants exactly []interface{}; an
	// authored `items: null` decodes to an untyped nil, so the envelope passed
	// every check here and Kustomize expanded it to ZERO objects with no error
	// (sigs.k8s.io/kustomize/api resource/factory.go's explicit-null branch). Worse
	// than the N-objects case it sits next to: with pruning enabled an empty
	// desired result also removes whatever the previous inventory held.
	//
	// Narrowed to a NULL items rather than a PRESENT one deliberately. A CRD may
	// legitimately carry an object-valued `items` field, and a bare presence
	// predicate rejects it — pinned as accepted/items_is_an_object,_not_a_sequence
	// and confirmed by mutation, where a presence predicate fails exactly that
	// subtest and nothing else.
	//
	// The two arms are ORDER-COUPLED and the coupling is not visible from the code:
	// a first-arm predicate widened to also match a null items would intercept these
	// cases and emit the list diagnostic instead. TestPassthrough_ListShapedObjectIsRejected
	// asserts on the diagnostic TEXT, not merely on error presence, so that masking
	// fails the suite rather than passing it.
	if rawItems, hasItems := object["items"]; hasItems && isExplicitNull(rawItems) {
		return errors.Errorf(
			"passthrough component %q: object (kind %q) sets 'items' to null, which is an empty list envelope that expands to zero objects at apply time — passthrough emits a single object verbatim, so declare the object itself",
			componentName, kind)
	}
	return nil
}

// PassthroughConfig implements stack.ApplicationConfig for passthrough components.
// It emits the declared object verbatim, defaulting metadata.name to the component
// name and (for namespaced objects) metadata.namespace to the build namespace.
type PassthroughConfig struct {
	componentName string
	Namespace     string
	ClusterScoped bool
	Object        map[string]any
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *PassthroughConfig) ComponentName() string { return c.componentName }

// Generate emits the declared object as an unstructured resource. It deep-copies
// the object first so the metadata fixup — and any later in-place mutation of
// labels/annotations by the delivery layer (kure stack.Bundle.Generate) — never
// touches the source component properties.
//
// It re-runs the list rejection on that copy. ToApplicationConfig is not the only
// way to reach here: Object is exported, so a caller holding the config can assign
// a list-shaped map to it, and a struct literal or a JSON decode of PassthroughConfig
// never calls the constructor at all. Checking the map that is about to be emitted,
// rather than trusting a check that ran on some earlier map, is what makes the
// rejection binding instead of advisory.
func (c *PassthroughConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	// Only a config that never went through ToApplicationConfig can be here with no
	// object — the constructor requires a non-empty apiVersion and kind. Emitting a
	// body consisting solely of the metadata stamped below would be a resource nobody
	// declared, so say what is wrong instead.
	if c.Object == nil {
		return nil, errors.Errorf("passthrough component %q: config has no 'object' — build it with PassthroughHandler.ToApplicationConfig", c.componentName)
	}
	obj := deepCopyMap(c.Object)

	if err := validateEmittableObject(c.componentName, obj); err != nil {
		return nil, err
	}

	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		obj["metadata"] = meta
	}
	if name, ok := meta["name"].(string); !ok || name == "" {
		meta["name"] = c.componentName
	}
	if !c.ClusterScoped {
		if ns, ok := meta["namespace"].(string); !ok || ns == "" {
			meta["namespace"] = c.Namespace
		}
	}

	u := &unstructured.Unstructured{Object: obj}
	out := client.Object(u)
	return []*client.Object{&out}, nil
}

// deepCopyMap returns a deep copy of a decoded YAML/JSON map: nested maps and
// slices are cloned, scalars (immutable) are copied by value. Unlike
// runtime.DeepCopyJSON it does not assume JSON-typed scalars, so it is safe for
// whatever scalar types the OAM decoder produces.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue detaches one value. The two fast paths are the shapes the authored
// path actually produces (yaml.v3 into any yields map[string]any and []any); the
// reflection arm exists for the shapes a Go-assembled body can hold and the fast
// paths cannot see — map[string]string, []string, []int32, named map/slice types,
// arrays. Those aliased through the previous `default: return v`, which made the
// freeze partial in exactly the way that is invisible from the authored path.
//
// Two deliberate limits, stated rather than left to be rediscovered:
//
//   - A typed nil (map[string]any(nil), []string(nil), a nil pointer) is returned
//     as it stands. Copying it through the container arms would turn a null into an
//     empty {} or [], which changes the emitted document — isExplicitNull is the same
//     predicate the null-items arm keys on, so both agree on what a null is.
//   - A value that merely CONTAINS a pointer (a pointer field, resource.Quantity's
//     *inf.Dec, time.Time's *Location) is copied by value and its pointee still
//     aliases. Chasing pointees generically is where a reflective copy starts
//     inventing semantics — an unexported field or a self-referential graph — and no
//     shape reaching this function today has one. Copying the containers is what the
//     freeze needs; copying the world is not.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	}
	if isExplicitNull(v) {
		return v
	}
	rv := reflect.ValueOf(v)
	elem := func(i int) reflect.Value { return copiedValue(rv.Index(i).Interface(), rv.Type().Elem()) }
	switch rv.Kind() {
	case reflect.Map:
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		valueType := rv.Type().Elem()
		iter := rv.MapRange()
		for iter.Next() {
			// Keys are left as they stand: a map key must be comparable, so it can
			// hold no slice or map to detach, and copying a pointer key would change
			// which entry it is.
			out.SetMapIndex(iter.Key(), copiedValue(iter.Value().Interface(), valueType))
		}
		return out.Interface()
	case reflect.Slice:
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(elem(i))
		}
		return out.Interface()
	case reflect.Array:
		out := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(elem(i))
		}
		return out.Interface()
	default:
		return v
	}
}

// copiedValue is deepCopyValue with a reflect.Value result assignable to t.
//
// The nil branch is not defensive. A nil interface has no reflect.Value of its own:
// reflect.ValueOf(nil) is the ZERO Value, and the two uses above fail differently on
// it — Set panics, while SetMapIndex takes a zero Value as "delete this key" and
// silently drops the entry. An element of an `any`-valued collection is nil whenever
// a Go-assembled body writes an explicit null into one, which is exactly the shape
// this package spends two rejection arms reasoning about.
func copiedValue(v any, t reflect.Type) reflect.Value {
	c := deepCopyValue(v)
	if c == nil {
		return reflect.Zero(t)
	}
	return reflect.ValueOf(c)
}
