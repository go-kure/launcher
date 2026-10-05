package oam

import (
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// metaKind is a kind component's handler that records, per component, the
// object metadata it was handed and which of the two properties were still on
// the component.
type metaKind struct {
	*kindStubHandler
	meta map[string]ObjectMetadata
	held map[string][]string
}

func newMetaKind() *metaKind {
	return &metaKind{
		kindStubHandler: kindStub("widget", widgetKind, ObjectScopeNamespaced),
		meta:            map[string]ObjectMetadata{},
		held:            map[string][]string{},
	}
}

func (h *metaKind) ToApplicationConfig(c *Component, ns string) (stack.ApplicationConfig, error) {
	h.meta[c.Name] = c.ObjectMetadata()
	for _, property := range []string{ObjectLabelsProperty, ObjectAnnotationsProperty} {
		if _, has := c.Properties[property]; has {
			h.held[c.Name] = append(h.held[c.Name], property)
		}
	}
	return h.kindStubHandler.ToApplicationConfig(c, ns)
}

// A kind component's `labels` and `annotations` reach its handler on the
// component, checked, and never as properties. A null value and a null entry
// are absent ones.
func TestObjectMetadata_ReadOffTheComponent(t *testing.T) {
	tests := []struct {
		name            string
		props           map[string]any
		wantLabels      map[string]string
		wantAnnotations map[string]string
	}{
		{name: "neither"},
		{name: "both",
			props: map[string]any{
				"labels":      map[string]any{"team": "payments", "example.com/tier": "backend"},
				"annotations": map[string]any{"example.com/owner": "a b, c", "note": ""},
			},
			wantLabels:      map[string]string{"team": "payments", "example.com/tier": "backend"},
			wantAnnotations: map[string]string{"example.com/owner": "a b, c", "note": ""}},
		{name: "labels alone", props: map[string]any{"labels": map[string]any{"team": "payments"}},
			wantLabels: map[string]string{"team": "payments"}},
		{name: "a null value is none", props: map[string]any{"labels": nil, "annotations": nil}},
		{name: "a null entry is none", props: map[string]any{"labels": map[string]any{"team": nil, "tier": "backend"}},
			wantLabels: map[string]string{"tier": "backend"}},
		{name: "an empty map is none", props: map[string]any{"labels": map[string]any{}, "annotations": map[string]any{}}},
		// A component built in Go, by a lowering rule for one, may hold the map
		// a decoder never produces.
		{name: "maps of strings as Go builds them",
			props: map[string]any{
				"labels":      map[string]string{"team": "payments"},
				"annotations": map[string]string{"example.com/owner": "a b, c"},
			},
			wantLabels:      map[string]string{"team": "payments"},
			wantAnnotations: map[string]string{"example.com/owner": "a b, c"}},
		{name: "a nil map of strings is none",
			props: map[string]any{"labels": map[string]string(nil), "annotations": map[string]string(nil)}},
		{name: "the component's own app label", props: map[string]any{"labels": map[string]any{"app": "web"}},
			wantLabels: map[string]string{"app": "web"}},
		{name: "an annotation key in upper case, as the API server reads it",
			props:           map[string]any{"annotations": map[string]any{"Example.com/Owner": "x"}},
			wantAnnotations: map[string]string{"Example.com/Owner": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newMetaKind()
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			if _, _, err := tr.TransformWithPolicy(siblingDoc(widget("web", tt.props)), TransformContext{}); err != nil {
				t.Fatalf("transform: %v", err)
			}
			got := h.meta["web"]
			if !maps.Equal(got.Labels, tt.wantLabels) {
				t.Errorf("the handler was handed labels %v, want %v", got.Labels, tt.wantLabels)
			}
			if !maps.Equal(got.Annotations, tt.wantAnnotations) {
				t.Errorf("the handler was handed annotations %v, want %v", got.Annotations, tt.wantAnnotations)
			}
			if held := h.held["web"]; len(held) != 0 {
				t.Errorf("the handler was handed the properties %v; the engine takes them out", held)
			}
		})
	}
}

// The author's properties are not changed by the engine taking the two out, and
// what the handler is handed is its own: changing it changes nothing else.
func TestObjectMetadata_LeavesTheAuthoredPropertiesAlone(t *testing.T) {
	h := newMetaKind()
	tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
	doc := siblingDoc(widget("web", map[string]any{"labels": map[string]any{"team": "payments"}}))
	for range 2 {
		if _, _, err := tr.TransformWithPolicy(doc, TransformContext{}); err != nil {
			t.Fatalf("transform: %v", err)
		}
		if got := h.meta["web"].Labels; !maps.Equal(got, map[string]string{"team": "payments"}) {
			t.Fatalf("labels %v, want the authored ones", got)
		}
		h.meta["web"].Labels["team"] = "changed"
	}
	authored, _ := doc.Spec.Components[0].Properties["labels"].(map[string]any)
	if authored["team"] != "payments" {
		t.Errorf("the document's labels are %v after two transforms, want them as authored", authored)
	}

	c := Component{objectMetadata: ObjectMetadata{Labels: map[string]string{"a": "1"}, Annotations: map[string]string{"b": "2"}}}
	got := c.ObjectMetadata()
	got.Labels["a"], got.Annotations["b"] = "x", "y"
	if again := c.ObjectMetadata(); again.Labels["a"] != "1" || again.Annotations["b"] != "2" {
		t.Errorf("ObjectMetadata() = %+v after its last result was changed, want the maps the caller's own", again)
	}
}

func TestObjectMetadata_Refusals(t *testing.T) {
	const ownerKey = "example.org/owner"
	tests := []struct {
		name  string
		props map[string]any
		ctx   TransformContext
		want  []string
	}{
		{name: "labels that are no map", props: map[string]any{"labels": []any{"a"}}, want: []string{"labels"}},
		{name: "annotations that are no map", props: map[string]any{"annotations": "a=b"}, want: []string{"annotations"}},
		{name: "a label value that is a number", props: map[string]any{"labels": map[string]any{"replicas": 3}},
			want: []string{`labels["replicas"]`, "string"}},
		{name: "an annotation value that is a boolean", props: map[string]any{"annotations": map[string]any{"enabled": true}},
			want: []string{`annotations["enabled"]`, "string"}},
		{name: "a label value that is a number, in a map as Go builds it", props: map[string]any{"labels": map[string]int{"replicas": 3}},
			want: []string{`labels["replicas"]`, "must be a string, got int"}},
		{name: "a label key that is none", props: map[string]any{"labels": map[string]any{"not a key": "x"}},
			want: []string{`labels["not a key"]`, "not a valid label key"}},
		{name: "a label key with an empty prefix", props: map[string]any{"labels": map[string]any{"/name": "x"}},
			want: []string{`labels["/name"]`, "not a valid label key"}},
		{name: "a label value that is none", props: map[string]any{"labels": map[string]any{"team": "a b"}},
			want: []string{`labels["team"]`, `"a b" is not a valid label value`}},
		{name: "a label value over 63 characters", props: map[string]any{"labels": map[string]any{"team": strings.Repeat("a", 64)}},
			want: []string{`labels["team"]`, "not a valid label value"}},
		{name: "an annotation key that is none", props: map[string]any{"annotations": map[string]any{"not a key": "x"}},
			want: []string{`annotations["not a key"]`, "not a valid annotation key"}},
		{name: "annotations over the size limit",
			props: map[string]any{"annotations": map[string]any{"a": strings.Repeat("x", 200_000), "b": strings.Repeat("x", 70_000)}},
			want:  []string{"annotations:", "270002 bytes", "262144-byte limit"}},
		{name: "the app label with another value", props: map[string]any{"labels": map[string]any{"app": "other"}},
			want: []string{`labels["app"]`, `"other" is not the `, `component "web" ("web")`}},
		{name: "the component label with another value, under the default key",
			props: map[string]any{"labels": map[string]any{ComponentLabelKeyForDomain(""): "other"}},
			want:  []string{`labels["` + ComponentLabelKeyForDomain("") + `"]`, `"other" is not the component label of component "web" ("web")`}},
		{name: "the component label with another value, under the consumer's key",
			props: map[string]any{"labels": map[string]any{ownerKey: "other"}},
			ctx:   TransformContext{Domain: "example.org", ComponentLabelKey: ownerKey},
			want:  []string{`labels["` + ownerKey + `"]`, `"other" is not the component label of component "web" ("web")`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newMetaKind()
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			_, _, err := tr.TransformWithPolicy(siblingDoc(widget("web", tt.props)), tt.ctx)
			if err == nil {
				t.Fatal("transform succeeded, want a refusal")
			}
			for _, want := range append([]string{`component "web"`}, tt.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v\nwant it to contain %q", err, want)
				}
			}
			if _, called := h.meta["web"]; called {
				t.Error("the handler was called on a component the engine refuses")
			}
		})
	}
}

// The component label is the one refused label that is not refused by its key
// alone: the value launcher writes is accepted, under either key, and so is the
// default key with any value once the consumer chose another.
func TestObjectMetadata_TheComponentLabelWithItsOwnValue(t *testing.T) {
	const ownerKey = "example.org/owner"
	for _, tt := range []struct {
		name   string
		labels map[string]any
		ctx    TransformContext
	}{
		{name: "the default key", labels: map[string]any{ComponentLabelKeyForDomain(""): "web"}},
		{name: "the consumer's key", labels: map[string]any{ownerKey: "web"},
			ctx: TransformContext{Domain: "example.org", ComponentLabelKey: ownerKey}},
		{name: "another domain's key is a label like any other", labels: map[string]any{ComponentLabelKeyForDomain("other.example"): "x"}},
		{name: "the default key with another value, once the consumer chose its own",
			labels: map[string]any{ComponentLabelKeyForDomain("example.org"): "other"},
			ctx:    TransformContext{Domain: "example.org", ComponentLabelKey: ownerKey}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newMetaKind()
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
			if _, _, err := tr.TransformWithPolicy(siblingDoc(widget("web", map[string]any{"labels": tt.labels})), tt.ctx); err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := h.meta["web"].Labels; len(got) != 1 {
				t.Errorf("the handler was handed labels %v, want the authored one", got)
			}
		})
	}
}

// A member a component lowering rule emitted takes the two properties as an
// authored component does: its labels are its rule's to write. Its `app` label
// is held to the member's own name and its component label to the authored
// component it came from.
func TestObjectMetadata_EmittedMember(t *testing.T) {
	transform := func(labels map[string]any) (*metaKind, error) {
		h := newMetaKind()
		tr := NewTransformer(map[string]ComponentHandler{"widget": h}, nil)
		tr.RegisterComponentLowering(emitRule{typ: "role", fn: func(c *Component) []Component {
			return []Component{{Name: c.Name + "-store", Type: "widget", Properties: map[string]any{
				"labels": labels, "annotations": map[string]any{"example.com/by": "rule"}}}}
		}})
		_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "role", Properties: map[string]any{}}), TransformContext{})
		return h, err
	}

	key := ComponentLabelKeyForDomain("")
	h, err := transform(map[string]any{"team": "payments", "app": "web-store", key: "web"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	got := h.meta["web-store"]
	if !maps.Equal(got.Labels, map[string]string{"team": "payments", "app": "web-store", key: "web"}) || got.Annotations["example.com/by"] != "rule" {
		t.Errorf("the member's handler was handed %+v, want what the rule wrote", got)
	}
	if held := h.held["web-store"]; len(held) != 0 {
		t.Errorf("the member's handler was handed the properties %v", held)
	}

	if _, err := transform(map[string]any{"app": "web"}); err == nil || !strings.Contains(err.Error(), `labels["app"]`) {
		t.Errorf("err = %v\nwant the app label held to the member's own name", err)
	}
	if _, err := transform(map[string]any{key: "web-store"}); err == nil || !strings.Contains(err.Error(), `is not the component label of component "web" ("web")`) {
		t.Errorf("err = %v\nwant the component label held to the authored component", err)
	}
}

// A type that declares a schema and no object refuses the two properties by
// name; a null is none there too.
func TestObjectMetadata_NotAKindComponent(t *testing.T) {
	for _, property := range []string{ObjectLabelsProperty, ObjectAnnotationsProperty} {
		tr := NewTransformer(map[string]ComponentHandler{"plain": &plainHandler{}}, nil)
		doc := siblingDoc(Component{Name: "web", Type: "plain", Properties: map[string]any{property: map[string]any{"a": "b"}}})
		_, _, err := tr.TransformWithPolicy(doc, TransformContext{})
		if err == nil || !strings.Contains(err.Error(), property+` is not supported on component type "plain"`) {
			t.Errorf("%s: err = %v\nwant the property refused on a type that declares no object", property, err)
		}

		null := siblingDoc(Component{Name: "web", Type: "plain", Properties: map[string]any{property: nil}})
		if _, _, err := tr.TransformWithPolicy(null, TransformContext{}); err != nil {
			t.Errorf("%s: null: err = %v\nwant a null read as the property left out", property, err)
		}
	}
}

// On a type that declares no object the two properties are the handler's own
// business: one that declares them itself, or no schema at all, reads whatever
// it takes there, and the engine does not parse them.
func TestObjectMetadata_AHandlersOwnProperties(t *testing.T) {
	value := []any{"not", "a", "map"}
	for _, tt := range []struct {
		name   string
		schema map[string]PropertySchema
	}{
		{name: "declared by the handler", schema: map[string]PropertySchema{
			"labels":      {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}},
			"annotations": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}}}},
		{name: "a handler without a schema"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]any{}
			var h ComponentHandler = &ownMetaHandler{seen: seen}
			if tt.schema != nil {
				h = &ownMetaSchemaHandler{ownMetaHandler: ownMetaHandler{seen: seen}, schema: tt.schema}
			}
			tr := NewTransformer(map[string]ComponentHandler{"own": h}, nil)
			doc := siblingDoc(Component{Name: "web", Type: "own", Properties: map[string]any{"labels": value, "annotations": value}})
			if _, _, err := tr.TransformWithPolicy(doc, TransformContext{}); err != nil {
				t.Fatalf("transform: %v\nwant the handler's own labels and annotations left to it", err)
			}
			for _, property := range []string{"labels", "annotations"} {
				if got, _ := seen[property].([]any); len(got) != 3 {
					t.Errorf("the handler read %s = %v, want what the author wrote", property, seen[property])
				}
			}
		})
	}
}

// ownMetaHandler declares no object and no schema, and records the `labels` and
// `annotations` it reads off its component.
type ownMetaHandler struct{ seen map[string]any }

func (*ownMetaHandler) CanHandle(t string) bool { return t == "own" }
func (h *ownMetaHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.seen["labels"], h.seen["annotations"] = c.Properties[ObjectLabelsProperty], c.Properties[ObjectAnnotationsProperty]
	return &siblingStub{}, nil
}

// ownMetaSchemaHandler is ownMetaHandler declaring a schema.
type ownMetaSchemaHandler struct {
	ownMetaHandler
	schema map[string]PropertySchema
}

func (h *ownMetaSchemaHandler) PropertySchema() map[string]PropertySchema { return h.schema }

// HandlerSchemas publishes `labels` and `annotations` for a kind component and
// for no other type, without changing the handler's own schema.
func TestObjectMetadata_HandlerSchemas(t *testing.T) {
	h := kindStub("widget", widgetKind, ObjectScopeNamespaced)
	tr := NewTransformer(map[string]ComponentHandler{"widget": h, "plain": &plainHandler{}}, nil)
	set := tr.HandlerSchemas()
	for _, property := range []string{ObjectLabelsProperty, ObjectAnnotationsProperty} {
		if p, ok := set.Components["widget"][property]; !ok || p.Type != PropertyTypeObject || !p.AdditionalProperties || p.Required {
			t.Errorf("the kind component's published schema has %s = %+v (present %t), want an optional open object", property, p, ok)
		}
		if _, ok := set.Components["plain"][property]; ok {
			t.Errorf("a type that declares no object is published with %s", property)
		}
		if _, ok := h.PropertySchema()[property]; ok {
			t.Errorf("the handler's own schema was given %s", property)
		}
	}
}

// ApplyTo adds the authored labels and annotations to the object's own, in maps
// of the object's own: a label map the config shares with a selector or a pod
// template does not gain them.
func TestObjectMetadata_ApplyTo(t *testing.T) {
	shared := map[string]string{"app": "web"}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Labels: shared, Annotations: map[string]string{"own": "kept"}}}
	meta := ObjectMetadata{
		Labels:      map[string]string{"team": "payments", "app": "web"},
		Annotations: map[string]string{"example.com/owner": "x"},
	}
	if err := meta.ApplyTo(pod); err != nil {
		t.Fatalf("ApplyTo: %v", err)
	}
	if want := map[string]string{"app": "web", "team": "payments"}; !maps.Equal(pod.Labels, want) {
		t.Errorf("labels %v, want %v", pod.Labels, want)
	}
	if want := map[string]string{"own": "kept", "example.com/owner": "x"}; !maps.Equal(pod.Annotations, want) {
		t.Errorf("annotations %v, want %v", pod.Annotations, want)
	}
	if len(shared) != 1 {
		t.Errorf("the label map the object was built with is now %v; ApplyTo gives the object a map of its own", shared)
	}
	pod.Labels["later"] = "x"
	if _, leaked := meta.Labels["later"]; leaked {
		t.Error("the object's labels are the authored map itself")
	}

	// The zero value leaves the object as it is, nil maps included.
	bare := &corev1.Pod{}
	if err := (ObjectMetadata{}).ApplyTo(bare); err != nil || bare.Labels != nil || bare.Annotations != nil {
		t.Errorf("ApplyTo of no metadata: err %v, labels %v, annotations %v; want the object untouched", err, bare.Labels, bare.Annotations)
	}

	// A key the config set to another value is refused, for labels and
	// annotations alike, and so are authored annotations that are within the
	// API server's size limit alone and over it with the object's own. The
	// object is left as it was.
	for _, tt := range []struct {
		name string
		meta ObjectMetadata
		want string
	}{
		{name: "a label", meta: ObjectMetadata{Labels: map[string]string{"app": "other", "team": "x"}}, want: `labels["app"]: "other" is not the value the component sets on its object ("web")`},
		{name: "an annotation", meta: ObjectMetadata{Annotations: map[string]string{"own": "other"}}, want: `annotations["own"]: "other" is not the value the component sets on its object ("kept")`},
		{name: "an annotation, beside labels that would have been added", meta: ObjectMetadata{Labels: map[string]string{"team": "x"}, Annotations: map[string]string{"own": "other"}}, want: `annotations["own"]: "other" is not the value the component sets on its object ("kept")`},
		{name: "annotations over the size limit with the object's own, beside labels that would have been added",
			meta: ObjectMetadata{Labels: map[string]string{"team": "x"}, Annotations: map[string]string{"big": strings.Repeat("x", 262_140)}},
			want: `annotations: with the annotations the component sets on its object, the keys and values hold 262150 bytes, over the 262144-byte limit`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}, Annotations: map[string]string{"own": "kept"}}}
			err := tt.meta.ApplyTo(obj)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant %s", err, tt.want)
			}
			if len(obj.Labels) != 1 || len(obj.Annotations) != 1 {
				t.Errorf("the refused object now holds labels %v and annotations %v", obj.Labels, obj.Annotations)
			}
		})
	}

	// At the limit exactly, the object's own annotations counted in, they are
	// taken.
	atLimit := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"own": "kept"}}}
	if err := (ObjectMetadata{Annotations: map[string]string{"big": strings.Repeat("x", 262_134)}}).ApplyTo(atLimit); err != nil {
		t.Errorf("ApplyTo of annotations that reach the size limit with the object's own: %v", err)
	}
	if len(atLimit.Annotations) != 2 {
		t.Errorf("annotations at the size limit: the object holds %d, want its own and the authored one", len(atLimit.Annotations))
	}
}
