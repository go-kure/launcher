package components

import (
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

	if apiVersion, ok := object["apiVersion"].(string); !ok || apiVersion == "" {
		return nil, errors.Errorf("passthrough component %q: object.apiVersion is required and must be a non-empty string", component.Name)
	}
	kind, ok := object["kind"].(string)
	if !ok || kind == "" {
		return nil, errors.Errorf("passthrough component %q: object.kind is required and must be a non-empty string", component.Name)
	}

	// A List is N objects and this handler's contract is one — the type comment above
	// and the schema description both say "the Kubernetes object emitted verbatim",
	// singular, so a list was never inside the contract. Rejecting it here rather than
	// downstream, because Generate emits the map as a SINGLE unstructured and stamps a
	// name and a namespace onto it: a list would arrive as one named envelope whose
	// items never see per-object label mutation, namespace stamping or ownership
	// checks, while Flux's kustomize unwraps it at apply time into N objects that do
	// reach the cluster. One envelope bypasses every per-object rule at once, which is
	// why no single downstream check can catch it.
	//
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
		return nil, errors.Errorf(
			"passthrough component %q: 'object' is a list (kind %q with an 'items' array), but passthrough emits a single object verbatim — declare one passthrough component per object",
			component.Name, kind)
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
		Object:        object,
	}, nil
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
func (c *PassthroughConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	obj := deepCopyMap(c.Object)

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
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

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
	default:
		return v
	}
}
