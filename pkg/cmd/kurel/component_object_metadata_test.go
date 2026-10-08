package kurel

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The label and the annotation the tests below author on a kind component. No
// fixture and no handler writes either key.
const (
	authoredLabelKey, authoredLabelValue           = "example.com/team", "payments"
	authoredAnnotationKey, authoredAnnotationValue = "example.com/owner", "payments team, room 4"
)

// objectMetadataApp is labelInvariantApp with `labels` and `annotations` set on
// the component. props is not mutated.
func objectMetadataApp(name, typ string, props, labels, annotations map[string]any) map[string]any {
	props = maps.Clone(props)
	if props == nil {
		props = map[string]any{}
	}
	if labels != nil {
		props[oam.ObjectLabelsProperty] = labels
	}
	if annotations != nil {
		props[oam.ObjectAnnotationsProperty] = annotations
	}
	return labelInvariantApp(name, typ, props, "", nil)
}

// docKey identifies an emitted object by type, namespace and name.
func docKey(doc map[string]any) string {
	meta, _ := doc["metadata"].(map[string]any)
	apiVersion, _ := doc["apiVersion"].(string)
	kind, _ := doc["kind"].(string)
	namespace, _ := meta["namespace"].(string)
	name, _ := meta["name"].(string)
	return apiVersion + " " + kind + " " + namespace + "/" + name
}

// takeMetadataKey returns whether doc's metadata.<field> holds key with value
// want, and removes the key, and the map once it is empty, so that what is left
// compares with a render that never had it.
func takeMetadataKey(doc map[string]any, field, key, want string) bool {
	meta, _ := doc["metadata"].(map[string]any)
	entries, _ := meta[field].(map[string]any)
	got, has := entries[key]
	delete(entries, key)
	if entries != nil && len(entries) == 0 {
		delete(meta, field)
	}
	return has && got == want
}

// TestObjectMetadata_EveryKindComponentTakesIt holds every registered kind
// component to the two properties (go-kure/launcher#790), as
// TestObjectName_EveryComponentTypeChooses holds it to `objectName`: a kind
// cannot ship without them. Each is rendered with and without `labels` and
// `annotations`. With them the object of the declared kind, named after the
// component (or the fixture's apiName), carries the authored label and
// annotation on its own metadata,
// and the output is otherwise the render without them, object for object: the
// pod template, the selectors and every other object are unchanged, and nothing
// is added or dropped. The handler's own schema declares neither property,
// since the engine reads both.
func TestObjectMetadata_EveryKindComponentTakesIt(t *testing.T) {
	const component = "widget"
	handlers := builtinComponentHandlers()
	kinds := 0
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		provider, declares := handlers[typ].(oam.ComponentObjectProvider)
		if !declares {
			continue
		}
		kinds++
		fx, has := componentLabelFixtures[typ]
		if !has {
			t.Errorf("component type %q has no fixture in componentLabelFixtures", typ)
			continue
		}
		t.Run(typ, func(t *testing.T) {
			if p, ok := handlers[typ].(oam.PropertySchemaProvider); ok {
				own := p.PropertySchema()
				for _, property := range []string{oam.ObjectLabelsProperty, oam.ObjectAnnotationsProperty} {
					if _, declared := own[property]; declared {
						t.Errorf("the %s handler declares %q itself; the engine reads it on every kind component (oam.Component.ObjectMetadata)", typ, property)
					}
				}
			}
			props := fx.props
			if fx.propsFor != nil {
				props = fx.propsFor(t)
			}
			kind, _ := provider.ComponentObject()
			// A type whose object the API names authors that name as
			// objectName.
			named := component
			if fx.apiName != "" {
				named = fx.apiName
			}

			plain := map[string]map[string]any{}
			for _, doc := range renderLabelInvariant(t, labelInvariantApp(component, typ, props, "", nil)) {
				plain[docKey(doc)] = doc
			}
			docs := renderLabelInvariant(t, objectMetadataApp(component, typ, props,
				map[string]any{authoredLabelKey: authoredLabelValue},
				map[string]any{authoredAnnotationKey: authoredAnnotationValue}))
			if len(docs) != len(plain) {
				t.Errorf("labels and annotations changed the number of emitted objects from %d to %d", len(plain), len(docs))
			}

			found := false
			for _, doc := range docs {
				key := docKey(doc)
				if names := docsOfKind([]map[string]any{doc}, kind.Kind, kind.Group); slices.Contains(names, named) {
					found = true
					if !takeMetadataKey(doc, "labels", authoredLabelKey, authoredLabelValue) {
						t.Errorf("%s carries no label %s=%s on its own metadata", key, authoredLabelKey, authoredLabelValue)
					}
					if !takeMetadataKey(doc, "annotations", authoredAnnotationKey, authoredAnnotationValue) {
						t.Errorf("%s carries no annotation %s=%q on its own metadata", key, authoredAnnotationKey, authoredAnnotationValue)
					}
				}
				want, had := plain[key]
				if !had {
					t.Errorf("%s is emitted only with labels and annotations authored", key)
					continue
				}
				if !reflect.DeepEqual(doc, want) {
					t.Errorf("%s differs from the render without labels and annotations in more than the two authored keys on its own metadata:\n got %v\nwant %v", key, doc, want)
				}
			}
			if !found {
				t.Fatalf("no %s named %q among the emitted objects", kind, named)
			}
		})
	}
	if kinds == 0 {
		t.Fatal("no registered component type declares its object; the test checked nothing")
	}
}

// TestObjectMetadata_RefusedWhereNoObjectIsDeclared: a component type that
// declares no object refuses `labels` and `annotations`, naming the property.
func TestObjectMetadata_RefusedWhereNoObjectIsDeclared(t *testing.T) {
	for _, typ := range slices.Sorted(maps.Keys(noObjectNameTypes)) {
		fx := componentLabelFixtures[typ]
		for _, property := range []string{oam.ObjectLabelsProperty, oam.ObjectAnnotationsProperty} {
			t.Run(typ+"/"+property, func(t *testing.T) {
				props := fx.props
				if fx.propsFor != nil {
					props = fx.propsFor(t)
				}
				props = maps.Clone(props)
				props[property] = map[string]any{"example.com/team": "payments"}
				_, err := renderLabelInvariantErr(t, labelInvariantApp("widget", typ, props, "", nil))
				if err == nil {
					t.Fatalf("%s accepted %s; want it refused", typ, property)
				}
				if !strings.Contains(err.Error(), property) {
					t.Errorf("%s refused %s with %q, want the property named", typ, property, err)
				}
			})
		}
	}
}

// TestObjectMetadata_NotOnALoweredType: a type a lowering rule expands
// (`webservice`, `worker`) is no kind component and has no one object, so it
// refuses the two properties as it refuses any field it does not declare.
func TestObjectMetadata_NotOnALoweredType(t *testing.T) {
	for _, typ := range []string{"webservice", "worker"} {
		for _, property := range []string{oam.ObjectLabelsProperty, oam.ObjectAnnotationsProperty} {
			t.Run(typ+"/"+property, func(t *testing.T) {
				props := workloadProps(map[string]any{property: map[string]any{"example.com/team": "payments"}})
				_, err := renderLabelInvariantErr(t, labelInvariantApp("widget", typ, props, "", nil))
				if err == nil || !strings.Contains(err.Error(), property) {
					t.Fatalf("err = %v\nwant %s refused on a %s, naming the property", err, property, typ)
				}
			})
		}
	}
}

// TestObjectMetadata_Refusals runs the engine's refusals through a build, on a
// kind that sets the `app` label and on one that writes no label at all.
func TestObjectMetadata_Refusals(t *testing.T) {
	storageClass := map[string]any{"provisioner": "csi.example.com"}
	for _, tt := range []struct {
		name        string
		typ         string
		props       map[string]any
		labels      map[string]any
		annotations map[string]any
		want        []string
	}{
		{name: "the app label with another value, on a workload", typ: "deployment", props: workloadProps(nil),
			labels: map[string]any{"app": "other"},
			want:   []string{`component "widget"`, `labels["app"]`, `"other" is not the `}},
		{name: "the app label with another value, on a kind that sets none", typ: "storageclass", props: storageClass,
			labels: map[string]any{"app": "other"},
			want:   []string{`component "widget"`, `labels["app"]`}},
		{name: "the component label with another value", typ: "configmap", props: map[string]any{"data": map[string]any{"k": "v"}},
			labels: map[string]any{oam.ComponentLabelKeyForDomain(kurelDomain): "other"},
			want:   []string{`component "widget"`, `is not the component label of component "widget" ("widget")`}},
		{name: "a label value the API server refuses", typ: "storageclass", props: storageClass,
			labels: map[string]any{"team": "not a value"},
			want:   []string{`component "widget"`, `labels["team"]`, "not a valid label value"}},
		{name: "an annotation key the API server refuses", typ: "service", props: map[string]any{"ports": []any{map[string]any{"port": 80}}},
			annotations: map[string]any{"not a key": "x"},
			want:        []string{`component "widget"`, `annotations["not a key"]`, "not a valid annotation key"}},
		{name: "a label value that is no string", typ: "namespace",
			labels: map[string]any{"pod-security.kubernetes.io/enforce-version": 1.31},
			want:   []string{`labels`, "pod-security.kubernetes.io/enforce-version"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderLabelInvariantErr(t, objectMetadataApp("widget", tt.typ, tt.props, tt.labels, tt.annotations))
			if err == nil {
				t.Fatal("the build succeeded, want a refusal")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v\nwant it to contain %q", err, want)
				}
			}
		})
	}
}

// TestObjectMetadata_TheCasesTheyAnswer builds the four objects whose metadata
// had to be authorable (go-kure/launcher#790): a Namespace's Pod Security
// Admission labels, a default StorageClass and IngressClass, and a
// ServiceMonitor a Prometheus selects on a fixed label. Each object carries
// what was authored beside the component label.
func TestObjectMetadata_TheCasesTheyAnswer(t *testing.T) {
	for _, tt := range []struct {
		typ, kind   string
		props       map[string]any
		labels      map[string]any
		annotations map[string]any
	}{
		{typ: "namespace", kind: "Namespace",
			labels: map[string]any{"pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/enforce-version": "v1.31"}},
		{typ: "storageclass", kind: "StorageClass", props: map[string]any{"provisioner": "csi.example.com"},
			annotations: map[string]any{"storageclass.kubernetes.io/is-default-class": "true"}},
		{typ: "ingressclass", kind: "IngressClass", props: map[string]any{"controller": "example.com/ingress"},
			annotations: map[string]any{"ingressclass.kubernetes.io/is-default-class": "true"}},
		{typ: "servicemonitor", kind: "ServiceMonitor", props: componentLabelFixtures["servicemonitor"].props,
			labels: map[string]any{"release": "kube-prometheus"}},
	} {
		t.Run(tt.typ, func(t *testing.T) {
			docs := renderLabelInvariant(t, objectMetadataApp("widget", tt.typ, tt.props, tt.labels, tt.annotations))
			meta := childMap(t, findDoc(t, docs, tt.kind), "metadata")
			labels, _ := meta["labels"].(map[string]any)
			annotations, _ := meta["annotations"].(map[string]any)
			for key, want := range tt.labels {
				if labels[key] != want {
					t.Errorf("label %s = %v, want %v (labels %v)", key, labels[key], want, labels)
				}
			}
			for key, want := range tt.annotations {
				if annotations[key] != want {
					t.Errorf("annotation %s = %v, want %v (annotations %v)", key, annotations[key], want, annotations)
				}
			}
			if got := labels[oam.ComponentLabelKeyForDomain(kurelDomain)]; got != "widget" {
				t.Errorf("the component label is %v beside the authored labels, want the component's (labels %v)", got, labels)
			}
		})
	}
}
