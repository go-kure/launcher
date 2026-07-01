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
type PassthroughHandler struct{}

// CanHandle returns true for the passthrough component type.
func (h *PassthroughHandler) CanHandle(componentType string) bool {
	return componentType == "passthrough"
}

// PropertySchema declares the passthrough component's properties. `object` is the
// escape-hatch body emitted verbatim, so it is an open object (additionalProperties).
func (h *PassthroughHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"object":        {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true},
		"clusterScoped": {Type: oam.PropertyTypeBoolean, Default: false},
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
	if kind, ok := object["kind"].(string); !ok || kind == "" {
		return nil, errors.Errorf("passthrough component %q: object.kind is required and must be a non-empty string", component.Name)
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
		ComponentName: component.Name,
		Namespace:     namespace,
		ClusterScoped: clusterScoped,
		Object:        object,
	}, nil
}

// PassthroughConfig implements stack.ApplicationConfig for passthrough components.
// It emits the declared object verbatim, defaulting metadata.name to the component
// name and (for namespaced objects) metadata.namespace to the build namespace.
type PassthroughConfig struct {
	ComponentName string
	Namespace     string
	ClusterScoped bool
	Object        map[string]any
}

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
		meta["name"] = c.ComponentName
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
