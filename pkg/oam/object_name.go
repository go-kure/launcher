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
// refused, and the hook not asked, on a member a component or trait lowering
// rule emitted: a rule that wants its member's name choosable resolves it
// itself, under its own role (LoweringContext.ResolveName). What a document
// rule or a raw document rule returns is authored input, the components it
// built included: the property and the hook apply there, and a document rule
// that wants to fix a kind component's object name writes `objectName` itself.
type ComponentObjectProvider interface {
	ComponentObject() (kind schema.GroupKind, scope ObjectScope)
}

// objectNameSchema is the declaration of ObjectNameProperty the engine adds to
// the schema of every type that takes it (withObjectProperties).
var objectNameSchema = PropertySchema{
	Type:        PropertyTypeString,
	Description: "The name of the object this component generates, in place of the component name. It names the object alone: labels, selectors and the names traits generate keep the component name. A reference another component writes to this object names it by this name.",
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

// emitted reports whether the component is a member a component or trait
// lowering rule emitted. An authored component is not, and neither is one a
// document rule returned, forwarded or built: what a document rule returns is
// authored input (lowerDocumentOnce). What a raw document rule writes carries no
// rule at all.
func (c Component) emitted() bool {
	return c.origin != nil && c.origin.Rule != "" && !isDocumentRuleIdentity(c.origin.Rule)
}

// objectNameSet reports whether props holds ObjectNameProperty with a value. An
// explicit null is none, as it is at every other property.
func objectNameSet(props map[string]any) (raw any, set bool) {
	raw, has := props[ObjectNameProperty]
	return raw, has && !isNullValue(raw)
}

// emittedObjectNameError refuses ObjectNameProperty on a kind component that is
// a member a component or trait lowering rule emitted. It names those two rule
// kinds, the only ones that emit a component into a document: what a document
// rule returns is not refused.
func emittedObjectNameError(origin *Origin) error {
	return errors.Errorf("%s is set on a component a component or trait lowering rule emitted%s: that rule names its members, and a name it lets the author or the Naming hook choose it resolves itself, under its own role",
		ObjectNameProperty, emittedBy(origin))
}

// withObjectName returns component with its object's name resolved
// (Component.ObjectName) and ObjectNameProperty removed from its properties,
// which are copied first when they hold it. names resolves and claims the
// name; fluxNamespace is the transform's, "" for none.
//
// The name is resolved only for a component of a type that takes the property
// that is no emitted member (Component.emitted). On a member a component or
// trait lowering rule emitted the property is refused, and without it the
// rule's name for the object stands. A type that does not take the property is
// left to its handler, whatever it holds under that key: the engine reads the
// value as a name only where the property is its own.
func withObjectName(component Component, handler ComponentHandler, namespace, fluxNamespace string, names *nameResolver) (Component, error) {
	raw, present := objectNameSet(component.Properties)
	provider, takes := handler.(ComponentObjectProvider)
	if !takes {
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
	}
	if present && component.emitted() {
		return component, emittedObjectNameError(component.origin)
	}
	// The handler never sees the property, an explicit null included.
	if _, has := component.Properties[ObjectNameProperty]; has {
		component.Properties = maps.Clone(component.Properties)
		delete(component.Properties, ObjectNameProperty)
	}
	if component.emitted() {
		return component, nil
	}
	var authored string
	if present {
		name, ok := raw.(string)
		if !ok {
			return component, errors.Errorf("%s: expected string, got %T", ObjectNameProperty, raw)
		}
		authored = name
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
