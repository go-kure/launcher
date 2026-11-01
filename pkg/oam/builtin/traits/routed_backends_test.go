package traits_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Transformer.RoutedBackends (go-kure/launcher#1133): where each route of each
// routing trait lands. The document matrix is a golden file, regenerated with
// UPDATE_GOLDEN=1 and reviewed by hand; the cases that need a trait of their
// own to shape the bundles are separate tests below.

func routedApp(comps ...oam.Component) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
}

func routedTransformer() *oam.Transformer {
	tr := serviceKindTransformer()
	tr.RegisterComponent("helmrelease", &components.HelmReleaseHandler{})
	return tr
}

func routedBackends(t *testing.T, tr *oam.Transformer, app *oam.Application, caps map[string]oam.CapabilityBinding) []oam.RoutedBackend {
	t.Helper()
	got, err := tr.RoutedBackends(app, oam.TransformContext{Namespace: "default", Capabilities: caps})
	if err != nil {
		t.Fatalf("RoutedBackends: %v", err)
	}
	return got
}

// routerTo is a deployment named "router" whose ingress carries the given paths.
func routerTo(paths ...any) oam.Component {
	return oam.Component{
		Name:       "router",
		Type:       "deployment",
		Properties: map[string]any{"image": "ghcr.io/example/router:1.0"},
		Traits: []oam.Trait{{Type: "ingress", Properties: map[string]any{
			"rules": []any{map[string]any{"host": "api.example.com", "paths": paths}},
		}}},
	}
}

func routePath(backend string, port any) map[string]any {
	p := map[string]any{"path": "/", "backend": backend}
	switch v := port.(type) {
	case string:
		p["portName"] = v
	default:
		p["port"] = v
	}
	return p
}

func TestRoutedBackends_Matrix(t *testing.T) {
	chart := oam.Component{
		Name: "metrics",
		Type: "helmrelease",
		Properties: map[string]any{"chart": map[string]any{"spec": map[string]any{
			"chart":     "kube-prometheus-stack",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": "prometheus-community"},
		}}},
		Traits: []oam.Trait{{Type: "ingress", Properties: map[string]any{
			"servicePort": 9090,
			"rules":       []any{map[string]any{"host": "m.example.com", "paths": []any{map[string]any{"path": "/"}}}},
		}}},
	}
	web := oam.Component{
		Name:       "web",
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
	}
	cases := []struct {
		name string
		app  *oam.Application
	}{
		{"selector_numbered_and_named_port", routedApp(apiServerComponent(), apiServiceComponent(),
			routerTo(routePath("api", 80), routePath("api", "http")))},
		{"selector_unmatched_port_and_udp", routedApp(apiServerComponent(), apiServiceComponent(),
			routerTo(routePath("api", 9999), routePath("api", 53)))},
		{"externalname", routedApp(externalNameService(externalNamePorts), routerTo(routePath("db", 5432)))},
		{"config_wrapped", routedApp(apiServerComponent(),
			apiServiceComponent(decoratingTrait()),
			func() oam.Component {
				r := routerTo(routePath("api", 80))
				r.Traits = append([]oam.Trait{decoratingTrait()}, r.Traits...)
				return r
			}())},
		{"webservice_owner", routedApp(web, routerTo(routePath("web", 8080), routePath("web", 9999)))},
		{"deployment_without_a_service_is_unowned", routedApp(apiServerComponent(), routerTo(routePath("api-server", 8080)))},
		{"chart_with_service_port", routedApp(chart)},
		{"unowned_bare_service", routedApp(routerTo(map[string]any{
			"path": "/", "backend": "legacy", "port": 80,
			"backendSelector": map[string]any{"matchLabels": map[string]any{"app": "legacy"}},
		}))},
		// Two groups of one owned Service (one with an authored selector, one
		// without) are one row: the owner's selector wins and each port counts once.
		{"owned_service_in_two_selector_groups", routedApp(apiServerComponent(), apiServiceComponent(), routerTo(
			map[string]any{
				"path": "/a", "backend": "api", "port": 80,
				"backendSelector": map[string]any{"matchLabels": map[string]any{"app": "other"}},
			},
			routePath("api", 80), routePath("api", "http")))},
		{"self_route", routedApp(apiServerComponent(), apiServiceComponent(ingressTrait(map[string]any{"path": "/"})))},
		{"httproute", routedApp(apiServerComponent(), apiServiceComponent(), oam.Component{
			Name:       "gate",
			Type:       "webservice",
			Properties: map[string]any{"image": "nginx:1.25", "port": 8081},
			Traits: []oam.Trait{{Type: "httproute", Properties: map[string]any{
				"parentRefs": []any{map[string]any{"name": "gw"}},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{"name": "api", "port": 80}},
				}},
			}}},
		})},
	}
	got := map[string][]oam.RoutedBackend{}
	for _, tc := range cases {
		got[tc.name] = routedBackends(t, routedTransformer(), tc.app, nil)
	}
	assertRoutedGolden(t, filepath.Join("testdata", "routed_backends", "matrix.golden.json"), got)
}

func assertRoutedGolden(t *testing.T, path string, got any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(got); err != nil {
		t.Fatalf("encoding the result: %v", err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (UPDATE_GOLDEN=1 writes it): %v", path, err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Errorf("%s differs; run with UPDATE_GOLDEN=1 and review the diff\ngot:\n%s", path, buf.String())
	}
}

// A route is reported whether or not any traffic source is configured; a source
// changes nothing about where the route lands.
func TestRoutedBackends_TrafficSourcesChangeNothing(t *testing.T) {
	app := routedApp(apiServerComponent(), apiServiceComponent(),
		routerTo(routePath("api", 80), routePath("api", 9999)))
	bare := routedBackends(t, routedTransformer(), app, nil)
	if len(bare) == 0 {
		t.Fatal("no route reported without traffic sources")
	}
	with := routedBackends(t, routedTransformer(), app, ingressNetworkPolicyCapabilities("ingress-nginx"))
	if !reflect.DeepEqual(bare, with) {
		t.Errorf("with traffic sources: %+v\nwithout: %+v", with, bare)
	}
}

func TestRoutedBackends_Deterministic(t *testing.T) {
	app := routedApp(apiServerComponent(), apiServiceComponent(),
		routerTo(routePath("api", 80), routePath("api", "http"), routePath("api", 9999)))
	first := routedBackends(t, routedTransformer(), app, nil)
	second := routedBackends(t, routedTransformer(), app, nil)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two calls differ:\n%+v\n%+v", first, second)
	}
}

// The method only observes: it neither changes the document nor what a later
// TransformWithPolicy builds from it.
func TestRoutedBackends_LeavesTheBuildUnchanged(t *testing.T) {
	app := routedApp(apiServerComponent(), apiServiceComponent(), routerTo(routePath("api", 80)))
	caps := ingressNetworkPolicyCapabilities("ingress-nginx")
	snapshot := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshalling a snapshot: %v", err)
		}
		return string(b)
	}
	// build renders every object the build generates, policies included.
	build := func(tr *oam.Transformer) string {
		cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Capabilities: caps})
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		generated, err := oam.GenerateApplications(cluster)
		if err != nil {
			t.Fatalf("GenerateApplications: %v", err)
		}
		type rendered struct {
			Name    string
			Objects []client.Object
		}
		var out []rendered
		for _, g := range generated {
			r := rendered{Name: g.Name}
			for _, o := range g.Objects {
				r.Objects = append(r.Objects, *o)
			}
			out = append(out, r)
		}
		return snapshot(out)
	}
	tr := routedTransformer()
	appBefore, buildBefore := snapshot(app), build(tr)
	if rows := routedBackends(t, tr, app, caps); len(rows) == 0 {
		t.Fatal("no route reported, so the case observes nothing")
	}
	if appAfter := snapshot(app); appAfter != appBefore {
		t.Errorf("RoutedBackends changed the document:\nbefore %s\nafter  %s", appBefore, appAfter)
	}
	if buildAfter := build(tr); buildAfter != buildBefore {
		t.Errorf("the build after RoutedBackends differs from the build before it:\nbefore %s\nafter  %s", buildBefore, buildAfter)
	}
}

// An error the transform raises before the observation point is returned as is.
func TestRoutedBackends_ReturnsTheTransformError(t *testing.T) {
	app := routedApp(apiServerComponent(), apiServiceComponent(ingressTrait(map[string]any{"path": "/", "port": 53})))
	_, err := routedTransformer().RoutedBackends(app, oam.TransformContext{Namespace: "default"})
	if err == nil {
		t.Fatal("expected the implicit-backend port refusal")
	}
}

// subAppBundleTrait puts what the ingress handler builds somewhere other than
// the bundle it was handed, or removes it, as its `op` says.
type subAppBundleTrait struct{}

func (subAppBundleTrait) CanHandle(t string) bool { return t == "subapp-shape" }

func (subAppBundleTrait) Apply(trait *oam.Trait, app *stack.Application, b *stack.Bundle) error {
	switch trait.Properties["op"] {
	case "remove-routing":
		kept := b.Applications[:0]
		for _, a := range b.Applications {
			if _, routes := a.Config.(interface{ TargetComponentName() string }); !routes {
				kept = append(kept, a)
			}
		}
		b.Applications = kept
	case "move-routing-to-child":
		child := &stack.Bundle{Name: "routing"}
		kept := b.Applications[:0]
		for _, a := range b.Applications {
			if _, routes := a.Config.(interface{ TargetComponentName() string }); routes {
				child.Applications = append(child.Applications, a)
			} else {
				kept = append(kept, a)
			}
		}
		b.Applications = kept
		b.Children = append(b.Children, child)
	case "add-empty-child":
		b.Children = append(b.Children, &stack.Bundle{Name: "extra"})
	}
	return nil
}

func shapedRouter(op string) oam.Component {
	r := routerTo(routePath("api", 80))
	r.Traits = append(r.Traits, oam.Trait{Type: "subapp-shape", Properties: map[string]any{"op": op}})
	return r
}

func shapedTransformer() *oam.Transformer {
	tr := routedTransformer()
	tr.RegisterTrait("subapp-shape", subAppBundleTrait{})
	return tr
}

func TestRoutedBackends_BundleShapes(t *testing.T) {
	plain := routedBackends(t, routedTransformer(), routedApp(apiServerComponent(), apiServiceComponent(),
		routerTo(routePath("api", 80))), nil)
	if len(plain) != 1 {
		t.Fatalf("baseline rows = %+v, want one", plain)
	}

	t.Run("a removed sub-application is not reported", func(t *testing.T) {
		got := routedBackends(t, shapedTransformer(), routedApp(apiServerComponent(), apiServiceComponent(),
			shapedRouter("remove-routing")), nil)
		if len(got) != 0 {
			t.Errorf("rows = %+v, want none", got)
		}
	})

	t.Run("a parent bundle with a child still reports its routes", func(t *testing.T) {
		got := routedBackends(t, shapedTransformer(), routedApp(apiServerComponent(), apiServiceComponent(),
			shapedRouter("add-empty-child")), nil)
		if !reflect.DeepEqual(got, plain) {
			t.Errorf("rows = %+v, want %+v", got, plain)
		}
	})

	t.Run("a sub-application in a child bundle is reported once", func(t *testing.T) {
		got := routedBackends(t, shapedTransformer(), routedApp(apiServerComponent(), apiServiceComponent(),
			shapedRouter("move-routing-to-child")), nil)
		if !reflect.DeepEqual(got, plain) {
			t.Errorf("rows = %+v, want %+v", got, plain)
		}
	})
}

// chartWithServiceRule lowers one component to a same-name sibling group: a chart
// release carrying an ingress with a trait-level servicePort, and a `service`
// member that fronts the chart's pods.
type chartWithServiceRule struct{}

func (chartWithServiceRule) ComponentType() string { return "chart-with-service" }

func (chartWithServiceRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{
		{
			Name: comp.Name,
			Type: "helmrelease",
			Properties: map[string]any{"chart": map[string]any{"spec": map[string]any{
				"chart":     "kube-prometheus-stack",
				"sourceRef": map[string]any{"kind": "HelmRepository", "name": "prometheus-community"},
			}}},
			Traits: []oam.Trait{{Type: "ingress", Properties: map[string]any{
				"servicePort": 9090,
				"rules":       []any{map[string]any{"host": "m.example.com", "paths": []any{map[string]any{"path": "/"}}}},
			}}},
		},
		{
			Name: comp.Name,
			Type: "service",
			Properties: map[string]any{
				"selector": map[string]any{"app": "chart"},
				"ports":    []any{map[string]any{"name": "http", "port": 9090, "targetPort": 9091}},
			},
		},
	}}, nil
}

// In a sibling group the trait ran on the chart member, and the route is owned
// by the group, whose `service` member answers for the Service ports.
func TestRoutedBackends_SiblingGroup(t *testing.T) {
	tr := routedTransformer()
	tr.RegisterComponentLowering(chartWithServiceRule{})
	got := routedBackends(t, tr, routedApp(oam.Component{
		Name: "metrics", Type: "chart-with-service", Properties: map[string]any{},
	}), nil)
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want one", got)
	}
	row := got[0]
	if row.Component != "metrics" || row.Service != "metrics" || row.Owner != "metrics" {
		t.Errorf("component %q service %q owner %q, want metrics for each", row.Component, row.Service, row.Owner)
	}
	if row.Pods.Kind != oam.RoutedPodsSelector || len(row.Ports) != 1 || row.Ports[0].Status != oam.RoutedPortMatched {
		t.Errorf("pods %+v ports %+v, want the service member's selector and a matched port", row.Pods, row.Ports)
	}
}

// chartWithNamedService is chartWithServiceRule with the group's Service named
// apart from the group by the consumer's Naming hook, and a chart ingress whose
// one path names that Service. The routing member's own Service is then not the
// group's, so the route is a route to another Service of the same component.
type chartWithNamedService struct{}

func (chartWithNamedService) ComponentType() string { return "chart-with-named-service" }

func (chartWithNamedService) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	svc := oam.Component{
		Name: comp.Name,
		Type: "service",
		Properties: map[string]any{
			"selector": map[string]any{"app": "chart"},
			"ports":    []any{map[string]any{"name": "http", "port": 9090, "targetPort": 9091}},
		},
	}
	if err := lctx.ResolveMemberName(&svc, oam.NameSpec{Role: oam.NameRoleObject, Kind: schema.GroupKind{Kind: "Service"}}); err != nil {
		return oam.LoweringResult{}, err
	}
	return oam.LoweringResult{Components: []oam.Component{
		{
			Name: comp.Name,
			Type: "helmrelease",
			Properties: map[string]any{"chart": map[string]any{"spec": map[string]any{
				"chart":     "kube-prometheus-stack",
				"sourceRef": map[string]any{"kind": "HelmRepository", "name": "prometheus-community"},
			}}},
			Traits: []oam.Trait{{Type: "ingress", Properties: map[string]any{
				"rules": []any{map[string]any{"host": "m.example.com", "paths": []any{
					map[string]any{"path": "/", "backend": "metrics-http", "port": 9090},
				}}},
			}}},
		},
		svc,
	}}, nil
}

// A route that names another Service of its own component, here the group's
// Service under a name the consumer chose, is reported like any other route.
func TestRoutedBackends_SiblingServiceNamedApart(t *testing.T) {
	tr := routedTransformer()
	tr.RegisterComponentLowering(chartWithNamedService{})
	got, err := tr.RoutedBackends(routedApp(oam.Component{
		Name: "metrics", Type: "chart-with-named-service", Properties: map[string]any{},
	}), oam.TransformContext{Namespace: "default", Naming: func(req oam.NameRequest) (string, bool) {
		if req.Role == oam.NameRoleObject && req.Component == "metrics" {
			return "metrics-http", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatalf("RoutedBackends: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want one", got)
	}
	row := got[0]
	if row.Component != "metrics" || row.Service != "metrics-http" || row.Owner != "metrics" {
		t.Errorf("component %q service %q owner %q, want metrics, metrics-http, metrics", row.Component, row.Service, row.Owner)
	}
	if row.Pods.Kind != oam.RoutedPodsSelector || len(row.Ports) != 1 || row.Ports[0].Status != oam.RoutedPortMatched {
		t.Errorf("pods %+v ports %+v, want the service member's selector and a matched port", row.Pods, row.Ports)
	}
}

// subAppStampTrait wraps every sub-application of its component, so the routing
// sub-application ends the build behind a decorator.
type subAppStampTrait struct{}

func (subAppStampTrait) CanHandle(t string) bool { return t == "subapp-stamp" }

func (subAppStampTrait) DecoratesSubApplications() bool { return true }

func (subAppStampTrait) Apply(_ *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	app.Config = opaqueConfig{inner: app.Config}
	return nil
}

// opaqueConfig wraps a config so that none of its optional interfaces is visible,
// and marks every object it generates, which is how a test sees the wrapping
// through the ownership wrapper the build puts around it.
type opaqueConfig struct{ inner stack.ApplicationConfig }

func (c opaqueConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := c.inner.Generate(app)
	for _, p := range objs {
		ann := (*p).GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann["test/opaque"] = "true"
		(*p).SetAnnotations(ann)
	}
	return objs, err
}

// A sub-application decorator wraps the routing sub-application late in the
// build; the method reads it at the observation point, before the wrapping.
func TestRoutedBackends_SubApplicationDecorated(t *testing.T) {
	tr := routedTransformer()
	tr.RegisterTrait("subapp-stamp", subAppStampTrait{})
	router := routerTo(routePath("api", 80))
	router.Traits = append(router.Traits, oam.Trait{Type: "subapp-stamp"})
	app := routedApp(apiServerComponent(), apiServiceComponent(), router)

	got := routedBackends(t, tr, app, nil)
	plain := routedBackends(t, routedTransformer(), routedApp(apiServerComponent(), apiServiceComponent(),
		routerTo(routePath("api", 80))), nil)
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want one", got)
	}
	if !reflect.DeepEqual(got, plain) {
		t.Errorf("rows = %+v, want %+v", got, plain)
	}
	// The full build does run the wrapping, so the case does pin what it claims.
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	generated, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	wrapped := false
	for _, a := range generated {
		if a.Name != got[0].Application {
			continue
		}
		for _, o := range a.Objects {
			if (*o).GetAnnotations()["test/opaque"] == "true" {
				wrapped = true
			}
		}
	}
	if !wrapped {
		t.Errorf("the built cluster does not hold %q behind the decorator; the case pins nothing", got[0].Application)
	}
}
