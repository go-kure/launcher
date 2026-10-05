package components_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Reserved metadata keys on a chart rendered at build time, whose config
// augments its layout into hook groups, under a config a consumer wraps around
// it after the transform (go-kure/launcher#790).

const (
	htReservedPrefix = "platform.example/"
	htOwnerLabel     = htReservedPrefix + "owner"
	htComponentKey   = "example.org/component"
)

// htReservedLabelChart is htTemplateChart with one more object in its main
// group, which the chart labels under the reserved prefix.
func htReservedLabelChart() map[string]string {
	chart := maps.Clone(htTemplateChart)
	chart["zoned.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: zoned\n  labels:\n    " + htReservedPrefix + "zone: a\n"
	return chart
}

// htConsumerWrapper is a consumer's config around an application's config: it
// labels every object under the prefix the consumer reserved, and hands on
// AugmentLayout, which the walker must see for the hook groups to be laid out.
type htConsumerWrapper struct {
	inner     stack.ApplicationConfig
	augmenter layout.LayoutAugmenter
}

func (w *htConsumerWrapper) WrappedApplicationConfig() stack.ApplicationConfig { return w.inner }

func (w *htConsumerWrapper) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := w.inner.Generate(app)
	if err != nil {
		return nil, err
	}
	for _, p := range objs {
		labels := map[string]string{htOwnerLabel: "platform"}
		maps.Copy(labels, (*p).GetLabels())
		(*p).SetLabels(labels)
	}
	return objs, nil
}

func (w *htConsumerWrapper) AugmentLayout(l *layout.ManifestLayout) error {
	return w.augmenter.AugmentLayout(l)
}

// htReservedCluster transforms an application `shop` whose one component db is
// a helmtemplate on the chart served at srvURL, with the reserved prefix, and
// returns the cluster and db's application.
func htReservedCluster(t *testing.T, srvURL string) (*stack.Cluster, *stack.Application) {
	t.Helper()
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}}, nil)
	cluster, err := tr.Transform(&oam.Application{
		Metadata: oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "db",
			Type: "helmtemplate",
			Properties: map[string]any{
				"chart":   "testchart",
				"version": "0.1.0",
				"source":  map[string]any{"url": srvURL},
				"values":  map[string]any{"replicas": 3},
			},
		}}},
	}, oam.TransformContext{Namespace: "demo", ReservedMetadataKeys: []string{htReservedPrefix}, ComponentLabelKey: htComponentKey})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var apps []*stack.Application
	var collect func(b *stack.Bundle)
	collect = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		apps = append(apps, b.Applications...)
		for _, child := range b.Children {
			collect(child)
		}
	}
	collect(cluster.Node.Bundle)
	if len(apps) != 1 {
		t.Fatalf("the cluster has %d applications, want db's one", len(apps))
	}
	return cluster, apps[0]
}

// htWrapForConsumer puts the consumer's wrapper around app's config.
func htWrapForConsumer(t *testing.T, app *stack.Application) {
	t.Helper()
	augmenter, ok := app.Config.(layout.LayoutAugmenter)
	if !ok {
		t.Fatalf("%T is not a layout.LayoutAugmenter", app.Config)
	}
	app.Config = &htConsumerWrapper{inner: app.Config, augmenter: augmenter}
}

// htHookGroupObjects returns the objects of every hook-group child of the
// layout AugmentLayout or the walker filled, failing when there are no groups.
func htHookGroupObjects(t *testing.T, componentLayout *layout.ManifestLayout) []client.Object {
	t.Helper()
	if len(componentLayout.Children) < 2 {
		t.Fatalf("the component's layout has %d children, want its hook groups", len(componentLayout.Children))
	}
	var objs []client.Object
	for _, child := range componentLayout.Children {
		objs = append(objs, child.Resources...)
	}
	if len(objs) == 0 {
		t.Fatal("the hook groups hold no object")
	}
	return objs
}

// TestHelmTemplate_ReservedKeys_AConsumerWrapperMayUseItsOwnPrefix: a chart
// with hook groups, walked by kure's layout walker under a consumer's wrapper
// that labels the objects under the prefix the consumer reserved, is laid out.
// It was refused by AugmentLayout. The hook groups hold the objects with the
// consumer's label and the component label.
func TestHelmTemplate_ReservedKeys_AConsumerWrapperMayUseItsOwnPrefix(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htTemplateChart)
	cluster, app := htReservedCluster(t, srvURL)
	htWrapForConsumer(t, app)
	root, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		t.Fatalf("WalkCluster = %v, want the consumer's own label accepted", err)
	}
	var find func(ml *layout.ManifestLayout) *layout.ManifestLayout
	find = func(ml *layout.ManifestLayout) *layout.ManifestLayout {
		if ml.Name == "db" {
			return ml
		}
		for _, child := range ml.Children {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	componentLayout := find(root)
	if componentLayout == nil {
		t.Fatal("the walked tree has no layout for component db")
	}
	for _, obj := range htHookGroupObjects(t, componentLayout) {
		labels := obj.GetLabels()
		if labels[htOwnerLabel] != "platform" || labels[htComponentKey] != "db" {
			t.Errorf("%s labels = %v, want the consumer's label and the component label", obj.GetName(), labels)
		}
	}
}

// TestHelmTemplate_ReservedKeys_AChartsOwnKeyIsRefused is the control, the case
// the reservation is there for: a chart that renders an object with a label
// under the reserved prefix is refused, with the consumer's wrapper in place as
// without it. Walked, it is refused by Generate, which the walker calls before
// AugmentLayout; AugmentLayout refuses it too when it is the first to run, on
// the hook groups it then builds from the render.
func TestHelmTemplate_ReservedKeys_AChartsOwnKeyIsRefused(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htReservedLabelChart())
	refused := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, oam.ErrReservedMetadataKey) {
			t.Fatalf("error = %v, want ErrReservedMetadataKey", err)
		}
		for _, want := range []string{`component "db"`, `ConfigMap "zoned"`, `label "` + htReservedPrefix + `zone"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %s", err, want)
			}
		}
	}
	for name, wrap := range map[string]bool{"walked": false, "walked under a consumer's wrapper": true} {
		t.Run(name, func(t *testing.T) {
			cluster, app := htReservedCluster(t, srvURL)
			if wrap {
				htWrapForConsumer(t, app)
			}
			_, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
			refused(t, err)
			if strings.Contains(err.Error(), "augment layout for application") {
				t.Errorf("refusal %q comes from AugmentLayout, want it from Generate", err)
			}
		})
	}
	t.Run("AugmentLayout before any Generate", func(t *testing.T) {
		_, app := htReservedCluster(t, srvURL)
		err := app.Config.(layout.LayoutAugmenter).AugmentLayout(&layout.ManifestLayout{Name: "db", Namespace: "demo"})
		refused(t, err)
	})
}

// TestHelmTemplate_ReservedKeys_AugmentLayoutLabelsItsHookGroups is a control,
// which held before AugmentLayout told what it added from what was there: when
// AugmentLayout is the first to run, the hook groups it builds hold objects no
// Generate returned, and they still get the component label there.
func TestHelmTemplate_ReservedKeys_AugmentLayoutLabelsItsHookGroups(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htTemplateChart)
	_, app := htReservedCluster(t, srvURL)
	ml := &layout.ManifestLayout{Name: "db", Namespace: "demo"}
	if err := app.Config.(layout.LayoutAugmenter).AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	for _, obj := range htHookGroupObjects(t, ml) {
		if got := obj.GetLabels()[htComponentKey]; got != "db" {
			t.Errorf("%s component label = %q, want db", obj.GetName(), got)
		}
	}
}
