package oam

import (
	"maps"
	"slices"
	"strings"

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// Object metadata on kind components (go-kure/launcher#790). A kind component
// is one object, and its properties are the fields of that object's spec, so
// the object's labels and annotations have two properties of their own on every
// kind: ObjectLabelsProperty and ObjectAnnotationsProperty. The engine reads
// them, as it reads ObjectNameProperty: they are no property of any handler's
// own schema, and a handler never sees them. It finds them on the component
// (Component.ObjectMetadata) and puts them on its object (ObjectMetadata.ApplyTo).
//
// They go on the object's own metadata and nowhere else: a pod template the
// object holds keeps the labels its kind gives it.

const (
	// ObjectLabelsProperty is the property an author sets the labels of a kind
	// component's object with.
	ObjectLabelsProperty = "labels"
	// ObjectAnnotationsProperty is the property an author sets the annotations
	// of a kind component's object with.
	ObjectAnnotationsProperty = "annotations"
)

// ObjectMetadata is the labels and annotations an author gave the object of a
// kind component. The zero value is none.
type ObjectMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// ObjectMetadata returns the labels and annotations authored for the object a
// kind component generates, as the engine read and checked them off the
// component's properties. The maps are the caller's own. A handler that
// declares its object (ComponentObjectProvider) carries them in its config and
// puts them on the object in Generate (ObjectMetadata.ApplyTo): the engine has
// removed the two properties by then, so nothing else brings them to the
// object.
func (c Component) ObjectMetadata() ObjectMetadata {
	return ObjectMetadata{
		Labels:      maps.Clone(c.objectMetadata.Labels),
		Annotations: maps.Clone(c.objectMetadata.Annotations),
	}
}

// ApplyTo puts the authored labels and annotations on obj, beside the ones its
// config set. A key the config set to another value is refused: the config's
// own labels are the ones selectors are built from. The maps obj held are left
// as they are and obj gets maps of its own, since a config may use one label
// map for an object, its selector and its pod template. The authored
// annotations were held to the API server's size limit on their own; with the
// ones the config set they are held to it again. A refusal leaves obj as it
// was: both maps are checked before either is set.
func (m ObjectMetadata) ApplyTo(obj client.Object) error {
	labels, err := withAuthored(obj.GetLabels(), m.Labels, ObjectLabelsProperty)
	if err != nil {
		return err
	}
	annotations, err := withAuthored(obj.GetAnnotations(), m.Annotations, ObjectAnnotationsProperty)
	if err != nil {
		return err
	}
	if len(m.Annotations) > 0 {
		if size := annotationsSize(annotations); size > apivalidation.TotalAnnotationSizeLimitB {
			return errors.Errorf("%s: with the annotations the component sets on its object, the keys and values hold %d bytes, over the %d-byte limit of an object's annotations", ObjectAnnotationsProperty, size, apivalidation.TotalAnnotationSizeLimitB)
		}
	}
	if len(m.Labels) > 0 {
		obj.SetLabels(labels)
	}
	if len(m.Annotations) > 0 {
		obj.SetAnnotations(annotations)
	}
	return nil
}

// withAuthored returns a copy of own with authored added. Keys are read in
// sorted order, so the one refused is the same on every build.
func withAuthored(own, authored map[string]string, property string) (map[string]string, error) {
	merged := make(map[string]string, len(own)+len(authored))
	maps.Copy(merged, own)
	for _, key := range slices.Sorted(maps.Keys(authored)) {
		if set, has := own[key]; has && set != authored[key] {
			return nil, errors.Errorf("%s[%q]: %q is not the value the component sets on its object (%q); remove the key, or write that value", property, key, authored[key], set)
		}
		merged[key] = authored[key]
	}
	return merged, nil
}

// annotationsSize is the size the API server holds an object's annotations to:
// the bytes of every key and value (ValidateAnnotations,
// k8s.io/apimachinery/pkg/api/validation).
func annotationsSize(annotations map[string]string) int {
	var size int
	for key, value := range annotations {
		size += len(key) + len(value)
	}
	return size
}

// objectLabelsSchema and objectAnnotationsSchema are the declarations the
// engine adds to the schema of every type that takes the two properties.
var (
	objectLabelsSchema = PropertySchema{
		Type: PropertyTypeObject, AdditionalProperties: true,
		Description: "Labels for the object this component generates, as a map of strings. They go on the object's own metadata only: a pod template the object holds is not labelled by them. Refused: a key or value that is no valid label, `app` with another value than the component's, and the component label key with another value than the component's. A label that names another object is written as authored and does not follow a rename of that object.",
	}
	objectAnnotationsSchema = PropertySchema{
		Type: PropertyTypeObject, AdditionalProperties: true,
		Description: "Annotations for the object this component generates, as a map of strings. They go on the object's own metadata only. Refused: a key that is no valid annotation key, and annotations over the size the API server accepts.",
	}
)

// withObjectProperties returns schema plus the properties the engine reads off
// a kind component (ObjectNameProperty, ObjectLabelsProperty,
// ObjectAnnotationsProperty) when handler takes them (ComponentObjectProvider).
// schema is never mutated.
func withObjectProperties(handler any, schema map[string]PropertySchema) map[string]PropertySchema {
	if _, takes := handler.(ComponentObjectProvider); !takes {
		return schema
	}
	out := make(map[string]PropertySchema, len(schema)+3)
	maps.Copy(out, schema)
	out[ObjectNameProperty] = objectNameSchema
	out[ObjectLabelsProperty] = objectLabelsSchema
	out[ObjectAnnotationsProperty] = objectAnnotationsSchema
	return out
}

// withObjectMetadata returns component with the labels and annotations authored
// for its object read, checked and recorded (Component.ObjectMetadata), and the
// two properties removed from its properties, which are copied first when they
// hold either. labelKey is the transform's component label key, "" where there
// is none to hold the labels to.
//
// Only a type that declares its object takes the two properties, and takes them
// on every component of it: an authored one, and a member a lowering rule
// emitted, whose labels are the rule's to write. Nothing is claimed for a label.
// On a type that declares no object the properties are the handler's own
// business, as any other key is: one that declares a schema without them is
// told why they are refused; one that declares them, or no schema at all, reads
// them itself.
//
// Refused, by property and key:
//
//   - a value that is no string map; a null entry is an absent one;
//   - a key or value the API server refuses: a label key or value that is none,
//     an annotation key that is none, annotations over its size limit;
//   - the `app` label with another value than the component's, which the kinds
//     that set it select by;
//   - the component label with another value than the one launcher gives the
//     component: the wrapper keeps a value that is already there, so the object
//     would leave the selectors generated for its component.
func withObjectMetadata(component Component, handler ComponentHandler, labelKey string) (Component, error) {
	rawLabels, hasLabels := component.Properties[ObjectLabelsProperty]
	rawAnnotations, hasAnnotations := component.Properties[ObjectAnnotationsProperty]
	if _, takes := handler.(ComponentObjectProvider); !takes {
		p, declares := handler.(PropertySchemaProvider)
		if !declares {
			return component, nil
		}
		for _, property := range []string{ObjectAnnotationsProperty, ObjectLabelsProperty} {
			if raw, has := component.Properties[property]; !has || isNullValue(raw) {
				continue
			}
			if _, own := p.PropertySchema()[property]; !own {
				return component, errors.Errorf("%s is not supported on component type %q: it sets the %s of the one object of a kind component, and this type generates no single object named after the component",
					property, component.Type, property)
			}
		}
		return component, nil
	}
	if !hasLabels && !hasAnnotations {
		return component, nil
	}
	// The handler never sees the properties, an explicit null included.
	component.Properties = maps.Clone(component.Properties)
	delete(component.Properties, ObjectLabelsProperty)
	delete(component.Properties, ObjectAnnotationsProperty)

	labels, err := authoredStringMap(rawLabels, ObjectLabelsProperty)
	if err != nil {
		return component, err
	}
	annotations, err := authoredStringMap(rawAnnotations, ObjectAnnotationsProperty)
	if err != nil {
		return component, err
	}
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			return component, errors.Errorf("%s[%q]: not a valid label key: %s", ObjectLabelsProperty, key, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(labels[key]); len(errs) > 0 {
			return component, errors.Errorf("%s[%q]: %q is not a valid label value: %s", ObjectLabelsProperty, key, labels[key], strings.Join(errs, "; "))
		}
	}
	if got, authored := labels[appLabelKey]; authored && got != ComponentLabelValue(component.Name) {
		return component, errors.Errorf("%s[%q]: %q is not the `app` label of component %q (%q): the kinds that set that label select by it; remove the label, or write that value",
			ObjectLabelsProperty, appLabelKey, got, component.Name, ComponentLabelValue(component.Name))
	}
	if labelKey != "" {
		owner := authoredComponent(component)
		if got, authored := labels[labelKey]; authored && got != ComponentLabelValue(owner) {
			return component, errors.Errorf("%s[%q]: %q is not the component label of component %q (%q): launcher sets that label on everything the component generates, and the NetworkPolicies generated for the component select by it; remove the label, or write that value",
				ObjectLabelsProperty, labelKey, got, owner, ComponentLabelValue(owner))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		// The API server validates an annotation key in lower case
		// (ValidateAnnotations, k8s.io/apimachinery/pkg/api/validation).
		if errs := validation.IsQualifiedName(strings.ToLower(key)); len(errs) > 0 {
			return component, errors.Errorf("%s[%q]: not a valid annotation key: %s", ObjectAnnotationsProperty, key, strings.Join(errs, "; "))
		}
	}
	if size := annotationsSize(annotations); size > apivalidation.TotalAnnotationSizeLimitB {
		return component, errors.Errorf("%s: the keys and values hold %d bytes, over the %d-byte limit of an object's annotations", ObjectAnnotationsProperty, size, apivalidation.TotalAnnotationSizeLimitB)
	}
	component.objectMetadata = ObjectMetadata{Labels: labels, Annotations: annotations}
	return component, nil
}

// authoredStringMap reads raw, the value of property, as a map of strings. An
// absent or null value is none, and so is a null entry, as at every other
// property. Keys are read in sorted order, so the one refused is the same on
// every build.
func authoredStringMap(raw any, property string) (map[string]string, error) {
	if isNullValue(raw) {
		return nil, nil
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: expected a map of strings, got %T", property, raw)
	}
	read := make(map[string]string, len(entries))
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		if isNullValue(entries[key]) {
			continue
		}
		value, ok := entries[key].(string)
		if !ok {
			return nil, errors.Errorf("%s[%q]: must be a string, got %T (quote a number or a boolean)", property, key, entries[key])
		}
		read[key] = value
	}
	if len(read) == 0 {
		return nil, nil
	}
	return read, nil
}
