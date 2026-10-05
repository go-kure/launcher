package oam_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests of this file walk a transformed cluster with kure's layout walker,
// as a consumer that writes a layout does, with a config of the consumer's own
// around each application's config (go-kure/launcher#790).

const (
	walkReservedPrefix = "platform.example/"
	walkOwnerLabel     = walkReservedPrefix + "owner"
	walkComponentKey   = "example.org/component"
)

func walkConfigMap(name string, labels map[string]string) client.Object {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

// walkConfig generates one ConfigMap named after the component, with the labels
// the component authored in its `labels` property.
type walkConfig struct {
	name   string
	labels map[string]string
}

func (c *walkConfig) Generate(*stack.Application) ([]*client.Object, error) {
	obj := walkConfigMap(c.name, c.labels)
	return []*client.Object{&obj}, nil
}

// walkAugmenter is walkConfig as a layout augmenter. It adds a ConfigMap to a
// child layout when the component authored an `added` property, with that
// property's labels, and leaves the layout as it is otherwise.
type walkAugmenter struct {
	walkConfig
	adds  bool
	added map[string]string
}

func (c *walkAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	if c.adds {
		l.Children = append(l.Children, &layout.ManifestLayout{
			Name:      c.name + "-added",
			Namespace: l.Namespace,
			Resources: []client.Object{walkConfigMap(c.name+"-added", c.added)},
		})
	}
	return nil
}

type walkHandler struct{ augments bool }

func (walkHandler) CanHandle(string) bool { return true }

func (h walkHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	stringMap := func(property string) (map[string]string, bool) {
		raw, ok := component.Properties[property].(map[string]any)
		if !ok {
			return nil, false
		}
		out := make(map[string]string, len(raw))
		for k, v := range raw {
			out[k], _ = v.(string)
		}
		return out, true
	}
	cfg := walkConfig{name: component.Name}
	cfg.labels, _ = stringMap("labels")
	if !h.augments {
		return &cfg, nil
	}
	aug := &walkAugmenter{walkConfig: cfg}
	aug.added, aug.adds = stringMap("added")
	return aug, nil
}

// consumerWrapMode is what a consumer's wrapper does with the objects of the
// config it wraps.
type consumerWrapMode int

const (
	// labelInPlace sets the consumer's label on each object.
	labelInPlace consumerWrapMode = iota
	// labelCopies returns a labelled copy of each object.
	labelCopies
	// appendOwn returns the objects as they are and one of the consumer's own,
	// which carries its label.
	appendOwn
)

// consumerWrapper is a config a consumer wraps around an application's config
// after the transform. It says what it wraps (oam.ConfigWrapper).
type consumerWrapper struct {
	inner stack.ApplicationConfig
	mode  consumerWrapMode
}

func (w *consumerWrapper) WrappedApplicationConfig() stack.ApplicationConfig { return w.inner }

func (w *consumerWrapper) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := w.inner.Generate(app)
	if err != nil {
		return nil, err
	}
	label := func(obj client.Object) {
		labels := map[string]string{walkOwnerLabel: "platform"}
		for k, v := range obj.GetLabels() {
			labels[k] = v
		}
		obj.SetLabels(labels)
	}
	switch w.mode {
	case labelInPlace:
		for _, p := range objs {
			label(*p)
		}
	case labelCopies:
		out := make([]*client.Object, 0, len(objs))
		for _, p := range objs {
			copied := (*p).DeepCopyObject().(client.Object)
			label(copied)
			out = append(out, &copied)
		}
		return out, nil
	case appendOwn:
		own := walkConfigMap("platform-owned", map[string]string{walkOwnerLabel: "platform"})
		objs = append(objs, &own)
	}
	return objs, nil
}

// augmentingConsumerWrapper hands on AugmentLayout, which a wrapper around an
// augmenting config must for the walker to see the augmenter.
type augmentingConsumerWrapper struct {
	*consumerWrapper
	augmenter layout.LayoutAugmenter
}

func (w *augmentingConsumerWrapper) AugmentLayout(l *layout.ManifestLayout) error {
	return w.augmenter.AugmentLayout(l)
}

// wrapForConsumer wraps the config of every application under bundle.
func wrapForConsumer(bundle *stack.Bundle, mode consumerWrapMode) {
	if bundle == nil {
		return
	}
	for _, app := range bundle.Applications {
		w := &consumerWrapper{inner: app.Config, mode: mode}
		if a, ok := app.Config.(layout.LayoutAugmenter); ok {
			app.Config = &augmentingConsumerWrapper{consumerWrapper: w, augmenter: a}
		} else {
			app.Config = w
		}
	}
	for _, child := range bundle.Children {
		wrapForConsumer(child, mode)
	}
}

// walkOne transforms an application of one component `redis` with the reserved
// prefix, wraps every config for the consumer when wrap is set, and walks the
// cluster. It returns the objects of the walked layout by name.
func walkOne(t *testing.T, augments bool, properties map[string]any, wrap *consumerWrapMode) (map[string]client.Object, error) {
	t.Helper()
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"probe": walkHandler{augments: augments}}, nil)
	cluster, err := tr.Transform(&oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{Name: "redis", Type: "probe", Properties: properties},
		}},
	}, oam.TransformContext{ReservedMetadataKeys: []string{walkReservedPrefix}, ComponentLabelKey: walkComponentKey})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if wrap != nil {
		wrapForConsumer(cluster.Node.Bundle, *wrap)
	}
	root, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		return nil, err
	}
	objects := map[string]client.Object{}
	var collect func(l *layout.ManifestLayout)
	collect = func(l *layout.ManifestLayout) {
		for _, obj := range l.Resources {
			objects[obj.GetName()] = obj
		}
		for _, c := range l.Children {
			collect(c)
		}
	}
	collect(root)
	return objects, nil
}

// TestReservedMetadataKeys_AConsumerWrapperMayUseItsOwnPrefix is the defect of
// go-kure/launcher#790: a consumer that wraps an application's config after the
// transform and labels the objects under a prefix it reserved was refused by
// its own reservation when the component's config augments its layout, and only
// then. It is accepted on both kinds of config, however the wrapper adds the
// label: on the object, on a copy, or on an object of its own. The objects of
// the component keep the component label. The rows of a plain config are
// controls: no AugmentLayout runs for one, and they were accepted before.
func TestReservedMetadataKeys_AConsumerWrapperMayUseItsOwnPrefix(t *testing.T) {
	modes := map[string]consumerWrapMode{
		"labels each object":       labelInPlace,
		"returns labelled copies":  labelCopies,
		"appends an object of its": appendOwn,
	}
	for _, augments := range []bool{false, true} {
		for name, mode := range modes {
			// The augmenter also adds an object of the component's own, which is
			// read: it carries no reserved key.
			for _, adds := range []bool{false, true} {
				if adds && !augments {
					continue
				}
				kind := map[bool]string{false: "a plain config", true: "an augmenting config"}[augments]
				if adds {
					kind += " that adds an object"
				}
				t.Run(kind+"/the wrapper "+name, func(t *testing.T) {
					properties := map[string]any{}
					if adds {
						properties["added"] = map[string]any{"tier": "cache"}
					}
					objects, err := walkOne(t, augments, properties, &mode)
					if err != nil {
						t.Fatalf("WalkCluster = %v, want the consumer's own label accepted", err)
					}
					labelled := "redis"
					if mode == appendOwn {
						labelled = "platform-owned"
					}
					if objects[labelled] == nil || objects[labelled].GetLabels()[walkOwnerLabel] != "platform" {
						t.Errorf("%s in the layout = %v, want it there with the consumer's label", labelled, objects[labelled])
					}
					want := []string{"redis"}
					if adds {
						want = append(want, "redis-added")
					}
					for _, name := range want {
						if objects[name] == nil || objects[name].GetLabels()[walkComponentKey] != "redis" {
							t.Errorf("%s in the layout = %v, want it there with the component label", name, objects[name])
						}
					}
				})
			}
		}
	}
}

// TestReservedMetadataKeys_AnAuthoredKeyIsRefusedUnderAConsumerWrapper is the
// control: a key under the reserved prefix that the document authored is
// refused as before, with a consumer's wrapper around the config or without.
// One on an object the config generates is refused by Generate, which the
// walker calls first, on a plain config and on an augmenting one. One on an
// object the augmenter adds is refused by AugmentLayout. That row under
// a wrapper is no control: the refusal used to name the consumer's own label on
// the generated object, and now names the authored key on the added one.
func TestReservedMetadataKeys_AnAuthoredKeyIsRefusedUnderAConsumerWrapper(t *testing.T) {
	const augmentPath = "augment layout for application"
	authored := map[string]any{walkReservedPrefix + "zone": "a"}
	inPlace := labelInPlace
	for name, tc := range map[string]struct {
		augments   bool
		properties map[string]any
		wrap       *consumerWrapMode
		// want is in the refusal; byAugment says which call refused.
		want      string
		byAugment bool
	}{
		"generated, a plain config":                        {false, map[string]any{"labels": authored}, nil, `ConfigMap "redis"`, false},
		"generated, a plain config under a wrapper":        {false, map[string]any{"labels": authored}, &inPlace, `ConfigMap "redis"`, false},
		"generated, an augmenting config":                  {true, map[string]any{"labels": authored}, nil, `ConfigMap "redis"`, false},
		"generated, an augmenting config under a wrapper":  {true, map[string]any{"labels": authored}, &inPlace, `ConfigMap "redis"`, false},
		"added by the augmenter":                           {true, map[string]any{"added": authored}, nil, `ConfigMap "redis-added"`, true},
		"added by the augmenter, the config under wrapper": {true, map[string]any{"added": authored}, &inPlace, `ConfigMap "redis-added"`, true},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := walkOne(t, tc.augments, tc.properties, tc.wrap)
			if !errors.Is(err, oam.ErrReservedMetadataKey) {
				t.Fatalf("WalkCluster = %v, want ErrReservedMetadataKey", err)
			}
			for _, want := range []string{`component "redis"`, tc.want, `label "` + walkReservedPrefix + `zone"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %s", err, want)
				}
			}
			if got := strings.Contains(err.Error(), augmentPath); got != tc.byAugment {
				t.Errorf("refusal %q: refused by AugmentLayout = %v, want %v", err, got, tc.byAugment)
			}
		})
	}
}
