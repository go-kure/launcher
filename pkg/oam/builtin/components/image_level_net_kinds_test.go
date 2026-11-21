package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// freshDeployment embeds the stock deployment handler and builds a fresh
// component for it, as a wrapper that does not copy the component it is given
// does: the level the Transformer set is lost, and the handler reads `tagged`.
type freshDeployment struct{ *components.DeploymentHandler }

func (h freshDeployment) ToApplicationConfig(c *oam.Component, ns string) (stack.ApplicationConfig, error) {
	fresh := oam.Component{Name: c.Name, Type: "deployment", Properties: c.Properties}
	return h.DeploymentHandler.ToApplicationConfig(&fresh, ns)
}

// TestImageLevel_FreshComponentWrapperIsHeldByTheRecheck: a wrapper over a real
// handler that builds a fresh component reads `tagged` inside the handler, so
// `digest` would pass an image that names a tag there. The Transformer reads the
// images of the config the handler returns (oam.ImageReferenced) and refuses
// them at the policy's level, so the stricter level still holds; `any` fails
// closed, as the handler holds the default (go-kure/launcher#1644).
func TestImageLevel_FreshComponentWrapperIsHeldByTheRecheck(t *testing.T) {
	handlers := registry.ComponentHandlers()
	handlers["deployment"] = freshDeployment{&components.DeploymentHandler{}}
	labels := map[string]any{"app": "web"}
	props := func(image string) map[string]any {
		return map[string]any{
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": image}}},
			},
		}
	}
	build := func(image string, policy oam.Policy) error {
		tr := oam.NewTransformer(handlers, nil)
		app := &oam.Application{
			APIVersion: oam.SupportedAPIVersion, Kind: "Application",
			Metadata: oam.Metadata{Name: "net", Namespace: "test"},
			Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: "web", Type: "deployment", Properties: props(image)}}},
		}
		_, err := tr.Transform(app, oam.TransformContext{Namespace: "test", Policy: policy})
		return err
	}
	for _, tc := range []struct {
		name      string
		policy    oam.Policy
		reference string
		accepted  bool
	}{
		{"no policy refuses latest", nil, ilLatest, false},
		{"no policy accepts a tag", nil, ilTag, true},
		{"any still refuses latest: the wrapper holds the default", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelAny}, ilLatest, false},
		{"digest refuses a tag the handler's default accepts", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelDigest}, ilTag, false},
		{"digest accepts a digest", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelDigest}, ilPinned, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ilWant(t, tc.name, tc.reference, build(tc.reference, tc.policy), tc.accepted)
		})
	}
}

// The second half of the net of go-kure/launcher#1644: the CloudNativePG kinds,
// the three monitoring kinds and postgresql. Each place that names an image is
// run under no policy, `any` and `digest` (ilReferences), with every other image
// of the component pinned by digest so that only the field under test can fail.

// ilCluster is a cnpg-cluster whose images are pinned, with extra merged over it.
func ilCluster(extra map[string]any) map[string]any {
	props := map[string]any{"instances": 1, "storage": map[string]any{"size": "1Gi"}, "imageName": ilPinned}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

func TestImageLevel_CnpgKinds(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		props     func(image string) map[string]any
	}{
		{"cnpg-cluster imageName", "cnpg-cluster", func(image string) map[string]any {
			return ilCluster(map[string]any{"imageName": image})
		}},
		{"cnpg-cluster extension image", "cnpg-cluster", func(image string) map[string]any {
			return ilCluster(map[string]any{"postgresql": map[string]any{"extensions": []any{
				map[string]any{"name": "e", "image": map[string]any{"reference": image}},
			}}})
		}},
		{"cnpg-pooler pgbouncer image", "cnpg-pooler", func(image string) map[string]any {
			return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{"image": image}}
		}},
		{"cnpg-pooler container", "cnpg-pooler", func(image string) map[string]any {
			return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}, "template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "pgbouncer", "image": image}},
			}}}
		}},
		{"cnpg-pooler image volume", "cnpg-pooler", func(image string) map[string]any {
			return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}, "template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "pgbouncer", "image": ilPinned}},
				"volumes":    []any{map[string]any{"name": "v", "image": map[string]any{"reference": image}}},
			}}}
		}},
		{"cnpg-imagecatalog image", "cnpg-imagecatalog", func(image string) map[string]any {
			return map[string]any{"images": []any{map[string]any{"major": 17, "image": image}}}
		}},
		{"cnpg-imagecatalog extension image", "cnpg-imagecatalog", func(image string) map[string]any {
			return map[string]any{"images": []any{map[string]any{"major": 17, "image": ilPinned, "extensions": []any{
				map[string]any{"name": "e", "image": map[string]any{"reference": image}},
			}}}}
		}},
		{"cnpg-imagecatalog component image", "cnpg-imagecatalog", func(image string) map[string]any {
			return map[string]any{
				"images":          []any{map[string]any{"major": 17, "image": ilPinned}},
				"componentImages": []any{map[string]any{"key": "pgbouncer", "image": image}},
			}
		}},
		{"cnpg-clusterimagecatalog image", "cnpg-clusterimagecatalog", func(image string) map[string]any {
			return map[string]any{"images": []any{map[string]any{"major": 17, "image": image}}}
		}},
		{"cnpg-clusterimagecatalog component image", "cnpg-clusterimagecatalog", func(image string) map[string]any {
			return map[string]any{
				"images":          []any{map[string]any{"major": 17, "image": ilPinned}},
				"componentImages": []any{map[string]any{"key": "pgbouncer", "image": image}},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ilReferences(t, func(reference string, policy oam.Policy) error {
				return ilBuild(tc.typ, tc.props(reference), policy)
			})
		})
	}
}

func TestImageLevel_MonitoringKinds(t *testing.T) {
	container := func(image string) []any { return []any{map[string]any{"name": "sidecar", "image": image}} }
	volume := func(image string) []any {
		return []any{map[string]any{"name": "v", "image": map[string]any{"reference": image}}}
	}
	for _, tc := range []struct {
		name, typ string
		props     func(image string) map[string]any
	}{
		{"alertmanager image", "alertmanager", func(image string) map[string]any {
			return map[string]any{"image": image, "version": "v0.28.1"}
		}},
		{"alertmanager container", "alertmanager", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "version": "v0.28.1", "containers": container(image)}
		}},
		{"alertmanager init container", "alertmanager", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "version": "v0.28.1", "initContainers": container(image)}
		}},
		{"alertmanager image volume", "alertmanager", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "version": "v0.28.1", "volumes": volume(image)}
		}},
		{"prometheus image", "prometheus", func(image string) map[string]any { return map[string]any{"image": image} }},
		{"prometheus thanos image", "prometheus", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "thanos": map[string]any{"image": image}}
		}},
		{"prometheus container", "prometheus", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "containers": container(image)}
		}},
		{"prometheus image volume", "prometheus", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "volumes": volume(image)}
		}},
		{"thanosruler image", "thanosruler", func(image string) map[string]any { return map[string]any{"image": image} }},
		{"thanosruler container", "thanosruler", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "containers": container(image)}
		}},
		{"thanosruler image volume", "thanosruler", func(image string) map[string]any {
			return map[string]any{"image": ilPinned, "volumes": volume(image)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ilReferences(t, func(reference string, policy oam.Policy) error {
				return ilBuild(tc.typ, tc.props(reference), policy)
			})
		})
	}
}

// postgresql lowers to a Cluster and a Pooler: the rule reads imageName and
// pooler.image, and holds the default image composed from version to the level
// too.
func TestImageLevel_Postgresql(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props func(image string) map[string]any
	}{
		{"imageName", func(image string) map[string]any {
			return map[string]any{"imageName": image, "instances": 1, "storageSize": "1Gi"}
		}},
		{"pooler image", func(image string) map[string]any {
			return map[string]any{
				"imageName": ilPinned, "instances": 1, "storageSize": "1Gi",
				"pooler": map[string]any{"enabled": true, "image": image},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ilReferences(t, func(reference string, policy oam.Policy) error {
				return ilBuild("postgresql", tc.props(reference), policy)
			})
		})
	}
}
