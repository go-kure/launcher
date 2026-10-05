package oam

import (
	"fmt"
	"maps"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
)

// ObjectNameProperty is the property an author names a kind component's object
// with (go-kure/launcher#787). The engine reads it: it is no property of any
// handler's own schema, and a handler never sees it.
const ObjectNameProperty = "objectName"

// ObjectScope is where the one object of a kind component lands.
type ObjectScope int

const (
	// ObjectScopeNamespaced is an object of the namespace the document is
	// transformed into.
	ObjectScopeNamespaced ObjectScope = iota
	// ObjectScopeCluster is a cluster-scoped object.
	ObjectScopeCluster
	// ObjectScopeFlux is an object of the Flux namespace when the transform has
	// one (TransformContext.FluxNamespace), else of the document's.
	ObjectScopeFlux
)

// ComponentObjectProvider is an optional ComponentHandler interface for a type
// whose component is exactly one object, named after the component: a kind
// component. ComponentObject declares that object's kind and scope, and with it
// the type takes `objectName` and the name role "object":
//
//   - the engine reads `objectName` off the component, resolves the object's
//     name (the author's, else the Naming hook's, else the component name),
//     claims it, and removes the property before ToApplicationConfig, which
//     reads the result from Component.ObjectName;
//   - the handler names its object with that, and only its object: the labels
//     and selectors it generates keep the component name.
//
// On a type that does not implement it `objectName` is refused. It is also
// refused, and the hook not asked, on a component a lowering rule emitted: a
// rule that wants its member's name choosable resolves it itself, under its own
// role (LoweringContext.ResolveName).
type ComponentObjectProvider interface {
	ComponentObject() (kind schema.GroupKind, scope ObjectScope)
}

// objectNameSchema is the declaration of ObjectNameProperty the engine adds to
// the schema of every type that takes it.
var objectNameSchema = PropertySchema{
	Type:        PropertyTypeString,
	Description: "The name of the object this component generates, in place of the component name. It names the object alone: labels, selectors and the names traits generate keep the component name. A reference another component writes to this object names it by this name.",
}

// withObjectNameProperty returns schema plus ObjectNameProperty when handler
// takes it (ComponentObjectProvider). schema is never mutated.
func withObjectNameProperty(handler any, schema map[string]PropertySchema) map[string]PropertySchema {
	if _, takes := handler.(ComponentObjectProvider); !takes {
		return schema
	}
	out := make(map[string]PropertySchema, len(schema)+1)
	maps.Copy(out, schema)
	out[ObjectNameProperty] = objectNameSchema
	return out
}

// ObjectName returns the name of the object a kind component generates: the
// one the engine resolved for it, else the component name. A handler names its
// object with it in ToApplicationConfig.
func (c Component) ObjectName() string {
	if c.objectName != "" {
		return c.objectName
	}
	return c.Name
}

// emitted reports whether a lowering rule produced the component, as opposed to
// an authored one, forwarded or not.
func (c Component) emitted() bool {
	return c.origin != nil && c.origin.Rule != ""
}

// authoredObjectName reads ObjectNameProperty off the component. present is
// false when the author wrote none; an explicit null is none, as it is at every
// other property.
func authoredObjectName(component *Component) (name string, present bool, err error) {
	raw, has := component.Properties[ObjectNameProperty]
	if !has || raw == nil {
		return "", false, nil
	}
	name, ok := raw.(string)
	if !ok {
		return "", false, errors.Errorf("%s: expected string, got %T", ObjectNameProperty, raw)
	}
	return name, true, nil
}

// emittedObjectNameError refuses ObjectNameProperty on a kind component a
// lowering rule emitted.
func emittedObjectNameError(origin *Origin) error {
	return errors.Errorf("%s is set on a component a lowering rule emitted%s: the rule names what it emits, and a name it lets the author or the Naming hook choose it resolves itself, under its own role",
		ObjectNameProperty, emittedBy(origin))
}

// withObjectName returns component with its object's name resolved
// (Component.ObjectName) and ObjectNameProperty removed from its properties,
// which are copied first when they hold it. names resolves and claims the
// name; fluxNamespace is the transform's, "" for none.
//
// The name is resolved only for an authored component of a type that takes the
// property. On a component a lowering rule emitted the property is refused, and
// without it the component is returned as it is: the rule named its object. A
// type that does not take the property is left to its handler.
func withObjectName(component Component, handler ComponentHandler, namespace, fluxNamespace string, names *nameResolver) (Component, error) {
	authored, present, err := authoredObjectName(&component)
	if err != nil {
		return component, err
	}
	provider, takes := handler.(ComponentObjectProvider)
	switch {
	case !takes:
		// The property is then the handler's own business, as any other key is: one
		// that declares a schema without it is told why it is refused; one that
		// declares it, or no schema at all, reads it itself.
		if p, declares := handler.(PropertySchemaProvider); present && declares {
			if _, own := p.PropertySchema()[ObjectNameProperty]; !own {
				return component, errors.Errorf("%s is not supported on component type %q: it names the one object of a kind component, and this type generates no single object named after the component",
					ObjectNameProperty, component.Type)
			}
		}
		return component, nil
	case present && component.emitted():
		return component, emittedObjectNameError(component.origin)
	case component.emitted():
		return component, nil
	}

	if _, has := component.Properties[ObjectNameProperty]; has {
		component.Properties = maps.Clone(component.Properties)
		delete(component.Properties, ObjectNameProperty)
	}
	kind, scope := provider.ComponentObject()
	spec := NameSpec{Role: NameRoleObject, Kind: kind, Default: component.Name}
	switch scope {
	case ObjectScopeNamespaced:
		spec.Namespace = namespace
	case ObjectScopeFlux:
		spec.Namespace = namespace
		if fluxNamespace != "" {
			spec.Namespace = fluxNamespace
		}
	case ObjectScopeCluster:
		spec.ClusterScoped = true
	default:
		return component, errors.Errorf("component type %q declares an unknown object scope %d", component.Type, scope)
	}
	if present {
		spec.Property, spec.Authored = fmt.Sprintf("properties.%s", ObjectNameProperty), authored
	}
	name, err := names.resolve(nameOwner{component: component.Name, role: NameRoleObject, def: component.Name}, spec)
	if err != nil {
		return component, err
	}
	component.objectName = name
	return component, nil
}
