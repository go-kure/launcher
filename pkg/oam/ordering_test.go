package oam

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// --- stubs ---

// fluxNamespaceConfig records the Flux namespace it is given.
type fluxNamespaceConfig struct{ fluxNamespace string }

func (c *fluxNamespaceConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}
func (c *fluxNamespaceConfig) SetFluxNamespace(ns string) { c.fluxNamespace = ns }

// fluxNamespaceHandler hands every component a fluxNamespaceConfig, recorded by
// component name.
type fluxNamespaceHandler struct {
	typ     string
	configs map[string]*fluxNamespaceConfig
}

func (h *fluxNamespaceHandler) CanHandle(t string) bool { return t == h.typ }
func (h *fluxNamespaceHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	cfg := &fluxNamespaceConfig{}
	h.configs[c.Name] = cfg
	return cfg, nil
}

// chartRule lowers a "chart" component into a "release" ordered after one
// "helmrepository" every chart component of the document shares, as the helm rule
// lowers an inline source: the first invocation emits the source, a later one
// adopts it. It declares a schema, so its output is synthesized.
type chartRule struct{}

func (chartRule) ComponentType() string                     { return "chart" }
func (chartRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (chartRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	name, adopted, err := lctx.Namer.NameOrAdopt(lctx.Origin.Document, "source", "the-one-source", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	release := Component{Name: comp.Name, Type: "release", Annotations: comp.Annotations}
	release.OrderAfter(name)
	var result LoweringResult
	if !adopted {
		result.Components = append(result.Components, Component{Name: name, Type: "helmrepository"})
	}
	result.Components = append(result.Components, release)
	return result, nil
}

// orderedPairRule lowers a component of type typ into "<name>-first" and
// "<name>", the second ordered after the first. The first is a webservice; the
// second is of type second, which another rule may lower again.
type orderedPairRule struct{ typ, second string }

func (r orderedPairRule) ComponentType() string { return r.typ }
func (orderedPairRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}
func (r orderedPairRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	second := Component{Name: comp.Name, Type: r.second}
	second.OrderAfter(comp.Name + "-first")
	return LoweringResult{Components: []Component{{Name: comp.Name + "-first", Type: "webservice"}, second}}, nil
}

// chartsRule lowers a component into two "chart" components, "<name>-a" ordered
// after "<name>-b", which chartRule lowers into releases sharing one source. It
// emits the later one first when laterFirst is set.
type chartsRule struct {
	typ        string
	laterFirst bool
}

func (r chartsRule) ComponentType() string                   { return r.typ }
func (chartsRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (r chartsRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	later := Component{Name: comp.Name + "-a", Type: "chart"}
	later.OrderAfter(comp.Name + "-b")
	earlier := Component{Name: comp.Name + "-b", Type: "chart"}
	if r.laterFirst {
		return LoweringResult{Components: []Component{later, earlier}}, nil
	}
	return LoweringResult{Components: []Component{earlier, later}}, nil
}

// seededChartRule lowers a "seeded-chart" component into a "release" ordered
// after a "helmrepository" it emits, which it orders after the document's
// "seed" component: a source that waits is not a generated source.
type seededChartRule struct{}

func (seededChartRule) ComponentType() string                     { return "seeded-chart" }
func (seededChartRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (seededChartRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	source := Component{Name: comp.Name + "-src", Type: "helmrepository"}
	source.OrderAfter("seed")
	release := Component{Name: comp.Name, Type: "release"}
	release.OrderAfter(source.Name)
	return LoweringResult{Components: []Component{source, release}}, nil
}

// relayRule lowers a "relay" component into "<name>" and "<name>-extra", both
// built from scratch: it copies nothing from the component it was handed, as
// the built-in rules do not.
type relayRule struct{}

func (relayRule) ComponentType() string                     { return "relay" }
func (relayRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (relayRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{
		{Name: comp.Name, Type: "webservice"},
		{Name: comp.Name + "-extra", Type: "webservice"},
	}}, nil
}

// waitingPairRule lowers a component of type typ into "<name>", ordered after
// "<name>-store", and the store, of type store, that waits as "<name>": as the
// postgresql rule's recovery store, the store follows what "<name>" follows.
type waitingPairRule struct{ typ, store string }

func (r waitingPairRule) ComponentType() string                   { return r.typ }
func (waitingPairRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (r waitingPairRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	store := Component{Name: comp.Name + "-store", Type: r.store}
	store.WaitsAs(comp.Name)
	out := Component{Name: comp.Name, Type: "webservice"}
	out.OrderAfter(store.Name)
	return LoweringResult{Components: []Component{out, store}}, nil
}

// loneSourceRule lowers a "lone-source" component into a "helmrepository" of the
// same name, which nothing else it emits is ordered after.
type loneSourceRule struct{}

func (loneSourceRule) ComponentType() string                     { return "lone-source" }
func (loneSourceRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (loneSourceRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "helmrepository"}}}, nil
}

// danglingRule orders the component it emits after one the document does not hold.
type danglingRule struct{}

func (danglingRule) ComponentType() string                     { return "dangling" }
func (danglingRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (danglingRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	out := Component{Name: comp.Name, Type: "webservice"}
	out.OrderAfter("nowhere")
	return LoweringResult{Components: []Component{out}}, nil
}

// depsPolicyHandler records the dependency edges it was built with.
type depsPolicyHandler struct{ deps map[string][]string }

func (h *depsPolicyHandler) CanHandle(t string) bool { return t == "dependency" }
func (h *depsPolicyHandler) Apply(_ *ApplicationPolicy, _ []string, result *PolicyResult) error {
	for component, on := range h.deps {
		result.Dependencies[component] = append(result.Dependencies[component], on...)
	}
	return nil
}

// tiersPolicyHandler records the placements it was built with.
type tiersPolicyHandler struct{ tiers map[string]Tier }

func (h *tiersPolicyHandler) CanHandle(t string) bool { return t == "placement" }
func (h *tiersPolicyHandler) Apply(_ *ApplicationPolicy, _ []string, result *PolicyResult) error {
	for component, tier := range h.tiers {
		result.TierOverrides[component] = tier
	}
	return nil
}

// orderingTransformer registers the stub component types the ordering tests use,
// the lowering rules above, and the given dependency edges and placements.
func orderingTransformer(deps map[string][]string, tiers map[string]Tier) (*Transformer, map[string]*fluxNamespaceConfig) {
	sources := map[string]*fluxNamespaceConfig{}
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice":     &pipelineComponentHandler{typ: "webservice"},
		"postgresql":     &pipelineComponentHandler{typ: "postgresql"},
		"daemonset":      &pipelineComponentHandler{typ: "daemonset"},
		"release":        &pipelineComponentHandler{typ: "release"},
		"helmrepository": &fluxNamespaceHandler{typ: "helmrepository", configs: sources},
	}, nil)
	tr.RegisterComponentLowering(chartRule{})
	tr.RegisterComponentLowering(orderedPairRule{typ: "pair", second: "webservice"})
	tr.RegisterComponentLowering(orderedPairRule{typ: "relayed-pair", second: "relay"})
	tr.RegisterComponentLowering(orderedPairRule{typ: "chart-pair", second: "chart"})
	tr.RegisterComponentLowering(orderedPairRule{typ: "seeded-pair", second: "seeded-chart"})
	tr.RegisterComponentLowering(seededChartRule{})
	tr.RegisterComponentLowering(relayRule{})
	tr.RegisterComponentLowering(chartsRule{typ: "charts-later-first", laterFirst: true})
	tr.RegisterComponentLowering(chartsRule{typ: "charts-earlier-first"})
	tr.RegisterComponentLowering(danglingRule{})
	tr.RegisterComponentLowering(waitingPairRule{typ: "waiting-pair", store: "relay"})
	tr.RegisterComponentLowering(waitingPairRule{typ: "waiting-source-pair", store: "lone-source"})
	tr.RegisterComponentLowering(loneSourceRule{})
	tr.RegisterPolicy("dependency", &depsPolicyHandler{deps: deps})
	tr.RegisterPolicy("placement", &tiersPolicyHandler{tiers: tiers})
	return tr, sources
}

// orderingApp builds an application carrying one policy per registered stub
// policy handler, so both run.
func orderingApp(components ...Component) *Application {
	app := makeApp("myapp", components...)
	app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
	app.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}, {Name: "place", Type: "placement"}}
	return app
}

func applicationNames(b *stack.Bundle) []string {
	names := make([]string, 0, len(b.Applications))
	for _, a := range b.Applications {
		names = append(names, a.Name)
	}
	return names
}

func dependsOnNames(b *stack.Bundle) []string {
	var names []string
	for _, d := range b.DependsOn {
		names = append(names, d.Name)
	}
	return names
}

// group is one expected child bundle of an ordered application.
type group struct {
	name         string
	applications []string
	dependsOn    []string
}

// assertOrdered checks that the cluster holds one application bundle named
// myapp, with the given own applications and child groups, and no child node.
func assertOrdered(t *testing.T, cluster *stack.Cluster, own []string, groups []group) {
	t.Helper()
	root := cluster.Node.Bundle
	if root == nil || root.Name != "myapp" {
		t.Fatalf("root bundle = %v, want the application bundle myapp", root)
	}
	if len(cluster.Node.Children) != 0 {
		t.Errorf("root node has %d child nodes, want none: an application is one bundle", len(cluster.Node.Children))
	}
	assertRootNodeUnnamed(t, cluster)
	if got := applicationNames(root); !slices.Equal(got, own) {
		t.Errorf("application bundle's own applications = %v, want %v", got, own)
	}
	if len(root.Children) != len(groups) {
		t.Fatalf("got %d groups, want %d", len(root.Children), len(groups))
	}
	for i, want := range groups {
		child := root.Children[i]
		if child.Name != want.name {
			t.Errorf("group %d name = %q, want %q", i, child.Name, want.name)
		}
		if got := applicationNames(child); !slices.Equal(got, want.applications) {
			t.Errorf("group %s applications = %v, want %v", child.Name, got, want.applications)
		}
		if got := dependsOnNames(child); !slices.Equal(got, want.dependsOn) {
			t.Errorf("group %s dependsOn = %v, want %v", child.Name, got, want.dependsOn)
		}
	}
}

// assertFlat checks that the cluster is one bundle with the given applications
// and no child group.
func assertFlat(t *testing.T, cluster *stack.Cluster, applications []string) {
	t.Helper()
	root := cluster.Node.Bundle
	if root == nil || root.Name != "myapp" {
		t.Fatalf("root bundle = %v, want the application bundle myapp", root)
	}
	if root.IsUmbrella() || len(cluster.Node.Children) != 0 {
		t.Fatalf("got %d groups and %d child nodes, want one flat bundle", len(root.Children), len(cluster.Node.Children))
	}
	assertRootNodeUnnamed(t, cluster)
	if got := applicationNames(root); !slices.Equal(got, applications) {
		t.Errorf("applications = %v, want %v", got, applications)
	}
}

// assertRootNodeUnnamed checks that the cluster's root node has no name, as it
// has none whether or not the components are ordered: a named root node is a
// directory of its own above the application bundle's (go-kure/launcher#783).
func assertRootNodeUnnamed(t *testing.T, cluster *stack.Cluster) {
	t.Helper()
	if cluster.Node.Name != "" {
		t.Errorf("root node name = %q, want none", cluster.Node.Name)
	}
}

// --- structure ---

// TestOrdering_NothingDeclared_Flat: launcher orders nothing by itself, whatever
// the component types are (go-kure/launcher#783).
func TestOrdering_NothingDeclared_Flat(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	app := orderingApp(
		makeComponent("web", "webservice"),
		makeComponent("db", "postgresql"),
		makeComponent("log", "daemonset"),
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertFlat(t, cluster, []string{"web", "db", "log"})
}

// TestOrdering_Dependency_Levels: a dependency rule gives one application bundle
// with a group per level, never one bundle per component.
func TestOrdering_Dependency_Levels(t *testing.T) {
	tr, _ := orderingTransformer(map[string][]string{"web": {"jobs"}}, nil)
	app := orderingApp(
		makeComponent("web", "webservice"),
		makeComponent("db", "postgresql"),
		makeComponent("jobs", "webservice"),
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"db", "jobs"}},
		{name: "myapp-01", applications: []string{"web"}, dependsOn: []string{"myapp-00"}},
	})
}

// TestOrdering_Placement_GroupPerTier: placed tiers follow one another, and a
// group that is exactly one tier carries the tier's name.
func TestOrdering_Placement_GroupPerTier(t *testing.T) {
	tr, _ := orderingTransformer(nil, map[string]Tier{"log": TierInfra, "db": TierServices, "web": TierApps})
	app := orderingApp(
		makeComponent("web", "webservice"),
		makeComponent("db", "postgresql"),
		makeComponent("log", "daemonset"),
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-infra", applications: []string{"log"}},
		{name: "myapp-services", applications: []string{"db"}, dependsOn: []string{"myapp-infra"}},
		{name: "myapp-apps", applications: []string{"web"}, dependsOn: []string{"myapp-services"}},
	})
}

// TestOrdering_Placement_EmptyTierSkipped: a tier with no component is no group,
// and the next populated tier follows the previous populated one.
func TestOrdering_Placement_EmptyTierSkipped(t *testing.T) {
	tr, _ := orderingTransformer(nil, map[string]Tier{"log": TierInfra, "web": TierApps})
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("log", "daemonset"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-infra", applications: []string{"log"}},
		{name: "myapp-apps", applications: []string{"web"}, dependsOn: []string{"myapp-infra"}},
	})
}

// TestOrdering_Placement_OneTier_Flat: one populated tier orders nothing.
func TestOrdering_Placement_OneTier_Flat(t *testing.T) {
	tr, _ := orderingTransformer(nil, map[string]Tier{"db": TierServices})
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("db", "postgresql"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertFlat(t, cluster, []string{"web", "db"})
}

// TestOrdering_TierAnnotation_IsPlacement: the tier annotation is an author
// declaration like the placement policy.
func TestOrdering_TierAnnotation_IsPlacement(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	web := makeComponent("web", "webservice")
	web.Annotations = map[string]string{TierAnnotationKey(DefaultDomain): string(TierApps)}
	log := makeComponent("log", "webservice")
	log.Annotations = map[string]string{TierAnnotationKey(DefaultDomain): string(TierInfra)}
	cluster, err := tr.Transform(orderingApp(web, log), TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-infra", applications: []string{"log"}},
		{name: "myapp-apps", applications: []string{"web"}, dependsOn: []string{"myapp-infra"}},
	})
}

// TestOrdering_UnplacedComponent_HasNoPredecessor: a component nothing places
// waits for nothing, so it shares the first group, which is then no single tier.
func TestOrdering_UnplacedComponent_HasNoPredecessor(t *testing.T) {
	tr, _ := orderingTransformer(nil, map[string]Tier{"log": TierInfra, "web": TierApps})
	app := orderingApp(
		makeComponent("web", "webservice"),
		makeComponent("misc", "webservice"),
		makeComponent("log", "daemonset"),
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"misc", "log"}},
		{name: "myapp-apps", applications: []string{"web"}, dependsOn: []string{"myapp-00"}},
	})
}

// TestOrdering_PlacementAndDependency_Combine: a dependency edge is added inside
// the placement order, splitting a tier over two groups, which are then numbered.
func TestOrdering_PlacementAndDependency_Combine(t *testing.T) {
	tr, _ := orderingTransformer(
		map[string][]string{"web": {"jobs"}},
		map[string]Tier{"db": TierInfra, "web": TierApps, "jobs": TierApps},
	)
	app := orderingApp(
		makeComponent("web", "webservice"),
		makeComponent("db", "postgresql"),
		makeComponent("jobs", "webservice"),
	)
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-infra", applications: []string{"db"}},
		{name: "myapp-01", applications: []string{"jobs"}, dependsOn: []string{"myapp-infra"}},
		{name: "myapp-02", applications: []string{"web"}, dependsOn: []string{"myapp-01"}},
	})
}

// TestOrdering_PlacementContradictsDependency_Refused: a dependency against the
// placement order is a cycle, refused naming both components and both sources of
// the order.
func TestOrdering_PlacementContradictsDependency_Refused(t *testing.T) {
	tr, _ := orderingTransformer(
		map[string][]string{"db": {"web"}},
		map[string]Tier{"db": TierInfra, "web": TierApps},
	)
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("db", "postgresql"))
	_, err := tr.Transform(app, TransformContext{})
	if err == nil {
		t.Fatal("Transform succeeded, want the contradiction refused")
	}
	const want = `components cannot be ordered: "web" is after "db" (placement: tier apps is after tier infra), "db" is after "web" (dependency policy)`
	if err.Error() != want {
		t.Errorf("Transform error = %v\nwant %s", err, want)
	}
}

// TestOrdering_DependencyCycle_Refused: a cycle is refused naming its components,
// the same way on every run.
func TestOrdering_DependencyCycle_Refused(t *testing.T) {
	const want = `components cannot be ordered: "a" is after "c" (dependency policy), "c" is after "b" (dependency policy), "b" is after "a" (dependency policy)`
	for range 20 {
		tr, _ := orderingTransformer(map[string][]string{"a": {"c"}, "b": {"a"}, "c": {"b"}}, nil)
		app := orderingApp(makeComponent("a", "webservice"), makeComponent("b", "webservice"), makeComponent("c", "webservice"))
		_, err := tr.Transform(app, TransformContext{})
		if err == nil || err.Error() != want {
			t.Fatalf("Transform error = %v\nwant %s", err, want)
		}
	}
}

// --- order a lowering rule declares ---

// TestOrdering_RuleOrder_Levels: a rule ordering the components it emits gives
// the same groups a dependency rule would.
func TestOrdering_RuleOrder_Levels(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("two", "pair"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"web", "two-first"}},
		{name: "myapp-01", applications: []string{"two"}, dependsOn: []string{"myapp-00"}},
	})
}

// TestOrdering_RuleOrder_SurvivesFurtherLowering: an order a rule declared on
// a component another rule lowers again reaches everything that component
// becomes, though the second rule builds its output from scratch.
func TestOrdering_RuleOrder_SurvivesFurtherLowering(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("two", "relayed-pair"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"web", "two-first"}},
		{name: "myapp-01", applications: []string{"two", "two-extra"}, dependsOn: []string{"myapp-00"}},
	})
}

// TestOrdering_RuleOrder_GeneratedSourceDoesNotWait: a component that waits and
// is lowered into a release and the source generated for it makes the release
// wait, not the source. The source is the application's: it stays one of the
// application bundle's own applications.
func TestOrdering_RuleOrder_GeneratedSourceDoesNotWait(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	cluster, err := tr.Transform(orderingApp(makeComponent("two", "chart-pair")), TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, []string{"myapp-source"}, []group{
		{name: "myapp-00", applications: []string{"two-first"}},
		{name: "myapp-01", applications: []string{"two"}, dependsOn: []string{"myapp-00"}},
	})
}

// TestOrdering_RuleOrder_SourceThatWaitsInheritsTheOrder: a source its rule
// orders after a component is not hoisted, so it is a component like any other
// and waits as the component it was emitted for did. "two" waits on
// "two-first", which waits on "seed": the source follows both, not "seed" only.
func TestOrdering_RuleOrder_SourceThatWaitsInheritsTheOrder(t *testing.T) {
	tr, _ := orderingTransformer(map[string][]string{"two-first": {"seed"}}, nil)
	app := orderingApp(makeComponent("seed", "webservice"), makeComponent("two", "seeded-pair"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"seed"}},
		{name: "myapp-01", applications: []string{"two-first"}, dependsOn: []string{"myapp-00"}},
		{name: "myapp-02", applications: []string{"two-src"}, dependsOn: []string{"myapp-01"}},
		{name: "myapp-03", applications: []string{"two"}, dependsOn: []string{"myapp-02"}},
	})
}

// TestOrdering_RuleOrder_SharedSourceOfOrderedConsumers: two consumers of one
// generated source, one ordered after the other by the rule that emitted them,
// in either emission order. The source waits on neither, so no consumer waits
// on a source that waits on it.
func TestOrdering_RuleOrder_SharedSourceOfOrderedConsumers(t *testing.T) {
	for _, typ := range []string{"charts-later-first", "charts-earlier-first"} {
		t.Run(typ, func(t *testing.T) {
			tr, _ := orderingTransformer(nil, nil)
			cluster, err := tr.Transform(orderingApp(makeComponent("x", typ)), TransformContext{})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			assertOrdered(t, cluster, []string{"myapp-source"}, []group{
				{name: "myapp-00", applications: []string{"x-b"}},
				{name: "myapp-01", applications: []string{"x-a"}, dependsOn: []string{"myapp-00"}},
			})
		})
	}
}

// TestOrdering_RuleOrder_UnknownComponent_Refused: a rule cannot order its
// output after a component the document does not hold.
func TestOrdering_RuleOrder_UnknownComponent_Refused(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	_, err := tr.Transform(orderingApp(makeComponent("x", "dangling")), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `component "x" is ordered after "nowhere" by its lowering rule, but the document has no component "nowhere"`) {
		t.Fatalf("Transform error = %v, want the unknown component refused", err)
	}
}

// TestOrdering_OrderAfter_CopySemantics: declaring an order on one copy of a
// component leaves another copy as it was.
func TestOrdering_OrderAfter_CopySemantics(t *testing.T) {
	a := Component{Name: "a"}
	a.OrderAfter("x")
	b := a
	b.OrderAfter("y")
	a.OrderAfter("z")
	if !slices.Equal(a.orderAfter, []string{"x", "z"}) || !slices.Equal(b.orderAfter, []string{"x", "y"}) {
		t.Errorf("a = %v, b = %v, want [x z] and [x y]", a.orderAfter, b.orderAfter)
	}
}

// --- waiting as another component ---

// TestOrdering_WaitsAs_SurvivesFurtherLowering: a component that waits as
// another, lowered again by a rule that builds its output from scratch, passes
// the wait to everything it becomes: the relay's two components both follow
// what "x" follows, and "x" follows them.
func TestOrdering_WaitsAs_SurvivesFurtherLowering(t *testing.T) {
	tr, _ := orderingTransformer(map[string][]string{"x": {"seed"}}, nil)
	app := orderingApp(makeComponent("seed", "webservice"), makeComponent("x", "waiting-pair"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, nil, []group{
		{name: "myapp-00", applications: []string{"seed"}},
		{name: "myapp-01", applications: []string{"x-store", "x-store-extra"}, dependsOn: []string{"myapp-00"}},
		{name: "myapp-02", applications: []string{"x"}, dependsOn: []string{"myapp-01"}},
	})
}

// TestOrdering_WaitsAs_InheritedByAGeneratedSource: a component that waits as
// another, lowered again into a generated source nothing else its rule emits is
// ordered after, passes the wait to the source, which is then refused as one an
// authored rule makes wait is, where an inherited OrderAfter would have made it
// an ordinary component.
func TestOrdering_WaitsAs_InheritedByAGeneratedSource(t *testing.T) {
	tr, _ := orderingTransformer(map[string][]string{"x": {"seed"}}, nil)
	app := orderingApp(makeComponent("seed", "webservice"), makeComponent("x", "waiting-source-pair"))
	_, err := tr.Transform(app, TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `helmrepository "x-store" cannot wait on seed`) {
		t.Errorf("Transform error = %v, want the generated source's inherited wait refused", err)
	}
}

// TestOrdering_WaitsAs_CopySemantics: declaring a wait on one copy of a
// component leaves another copy as it was.
func TestOrdering_WaitsAs_CopySemantics(t *testing.T) {
	// Spare capacity, so an append that does not clip would write both copies'
	// next name into one shared array.
	a := Component{Name: "a", waitsAs: make([]string, 0, 4)}
	a.WaitsAs("x")
	b := a
	b.WaitsAs("y")
	a.WaitsAs("z")
	if !slices.Equal(a.waitsAs, []string{"x", "z"}) || !slices.Equal(b.waitsAs, []string{"x", "y"}) {
		t.Errorf("a = %v, b = %v, want [x z] and [x y]", a.waitsAs, b.waitsAs)
	}
}

// TestOrdering_WaitsAs_Groups: what a component waiting as another follows,
// read off the dependency rules once every lowering has run.
func TestOrdering_WaitsAs_Groups(t *testing.T) {
	waiting := func(name, as string) componentEntry {
		c := Component{Name: name}
		c.WaitsAs(as)
		return componentEntry{component: c}
	}
	entry := func(name string) componentEntry { return componentEntry{component: Component{Name: name}} }
	source := componentEntry{component: Component{Name: "src", Type: "helmrepository", synthesized: true}}
	consumer := Component{Name: "r"}
	consumer.OrderAfter("src")
	names := func(entries []componentEntry) []string {
		var out []string
		for _, e := range entries {
			out = append(out, e.component.Name)
		}
		return out
	}
	tests := []struct {
		name    string
		entries []componentEntry
		deps    map[string][]string
		sources []string
		groups  [][]string
	}{
		{
			name:    "it follows what the other follows, not itself",
			entries: []componentEntry{entry("a"), entry("b"), entry("c"), waiting("s", "b")},
			deps:    map[string][]string{"b": {"a", "s"}},
			groups:  [][]string{{"a", "c"}, {"s"}, {"b"}},
		},
		{
			name:    "no dependency rule adds nothing",
			entries: []componentEntry{waiting("s", "b"), entry("b")},
			groups:  [][]string{{"s", "b"}},
		},
		{
			name:    "a name no rule makes wait adds nothing",
			entries: []componentEntry{waiting("s", "nobody"), entry("a"), entry("b")},
			deps:    map[string][]string{"b": {"a"}},
			groups:  [][]string{{"s", "a"}, {"b"}},
		},
		{
			name: "a sibling group waits as any member does",
			entries: []componentEntry{
				{component: Component{Name: "g"}, members: []componentEntry{entry("g"), waiting("g", "x")}},
				entry("x"), entry("y"),
			},
			deps:   map[string][]string{"x": {"y"}},
			groups: [][]string{{"y"}, {"g", "x"}},
		},
		{
			name:    "an inherited wait on a generated source adds no edge",
			entries: []componentEntry{source, {component: consumer}, entry("b"), waiting("s", "b")},
			deps:    map[string][]string{"b": {"src"}},
			sources: []string{"src"},
			groups:  [][]string{{"r", "b", "s"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := maps.Clone(tt.deps)
			order, err := orderComponents(tt.entries, tt.deps)
			if err != nil {
				t.Fatalf("orderComponents: %v", err)
			}
			if got := names(order.sources); !slices.Equal(got, tt.sources) {
				t.Errorf("sources = %v, want %v", got, tt.sources)
			}
			var groups [][]string
			for _, g := range order.groups {
				groups = append(groups, names(g))
			}
			if !slices.EqualFunc(groups, tt.groups, slices.Equal) {
				t.Errorf("groups = %v, want %v", groups, tt.groups)
			}
			if !maps.EqualFunc(tt.deps, before, slices.Equal) {
				t.Errorf("deps = %v, changed from %v", tt.deps, before)
			}
		})
	}
}

// TestOrdering_WaitsAs_Refusals: an inherited wait is a dependency like any
// other, so it closes a cycle and cannot make a generated source wait.
func TestOrdering_WaitsAs_Refusals(t *testing.T) {
	a := Component{Name: "a"}
	a.WaitsAs("b")
	_, err := orderComponents([]componentEntry{{component: a}, {component: Component{Name: "b"}}, {component: Component{Name: "c"}}},
		map[string][]string{"b": {"c"}, "c": {"a"}})
	const cycle = `components cannot be ordered: "a" is after "c" (dependency policy), "c" is after "a" (dependency policy)`
	if err == nil || err.Error() != cycle {
		t.Errorf("orderComponents error = %v\nwant %s", err, cycle)
	}

	src := Component{Name: "src", Type: "helmrepository", synthesized: true}
	src.WaitsAs("x")
	consumer := Component{Name: "r"}
	consumer.OrderAfter("src")
	_, err = orderComponents([]componentEntry{{component: src}, {component: consumer}, {component: Component{Name: "x"}}, {component: Component{Name: "y"}}},
		map[string][]string{"x": {"y"}})
	if err == nil || !strings.Contains(err.Error(), `helmrepository "src" cannot wait on y`) {
		t.Errorf("orderComponents error = %v, want the generated source's wait refused", err)
	}
}

// --- generated sources ---

// TestOrdering_GeneratedSource_InApplicationBundle: a source a rule generates and
// orders before its release sits in the application bundle itself, and the
// release in one group below it.
func TestOrdering_GeneratedSource_InApplicationBundle(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	cluster, err := tr.Transform(orderingApp(makeComponent("api", "chart")), TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, []string{"myapp-source"}, []group{
		{name: "myapp-00", applications: []string{"api"}},
	})
}

// TestOrdering_GeneratedSource_SharedOnce: two consumers share one generated
// source, which appears once, ahead of every group, wherever its consumers are
// placed; GenerateApplications lists it first, and it is placed in the Flux
// namespace.
func TestOrdering_GeneratedSource_SharedOnce(t *testing.T) {
	tr, sources := orderingTransformer(nil, map[string]Tier{"api": TierApps, "base": TierInfra})
	app := orderingApp(makeComponent("api", "chart"), makeComponent("base", "chart"))
	cluster, err := tr.Transform(app, TransformContext{FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, []string{"myapp-source"}, []group{
		{name: "myapp-infra", applications: []string{"base"}},
		{name: "myapp-apps", applications: []string{"api"}, dependsOn: []string{"myapp-infra"}},
	})

	if got := sources["myapp-source"]; got == nil || got.fluxNamespace != "flux-system" {
		t.Errorf("generated source config = %+v, want it placed in the Flux namespace", got)
	}

	generated, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	var names []string
	for _, g := range generated {
		names = append(names, g.Name)
	}
	if want := []string{"myapp-source", "base", "api"}; !slices.Equal(names, want) {
		t.Errorf("GenerateApplications = %v, want %v", names, want)
	}
}

// TestOrdering_GeneratedSource_DependencyOnItIsTheShape: a component an author
// makes wait for the generated source waits for nothing in the groups, since the
// application bundle's own applications come before every group.
func TestOrdering_GeneratedSource_DependencyOnItIsTheShape(t *testing.T) {
	tr, _ := orderingTransformer(map[string][]string{"web": {"myapp-source"}}, nil)
	app := orderingApp(makeComponent("api", "chart"), makeComponent("web", "webservice"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertOrdered(t, cluster, []string{"myapp-source"}, []group{
		{name: "myapp-00", applications: []string{"api", "web"}},
	})
}

// TestOrdering_GeneratedSource_CannotBePlacedOrDelayed: a generated source is in
// no group, so a placement of it and a dependency making it wait are refused.
func TestOrdering_GeneratedSource_CannotBePlacedOrDelayed(t *testing.T) {
	tr, _ := orderingTransformer(nil, map[string]Tier{"myapp-source": TierInfra})
	_, err := tr.Transform(orderingApp(makeComponent("api", "chart")), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `helmrepository "myapp-source" cannot be placed in tier infra`) {
		t.Errorf("Transform error = %v, want the placement refused", err)
	}

	tr, _ = orderingTransformer(map[string][]string{"myapp-source": {"api"}}, nil)
	_, err = tr.Transform(orderingApp(makeComponent("api", "chart")), TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `helmrepository "myapp-source" cannot wait on api`) {
		t.Errorf("Transform error = %v, want the dependency refused", err)
	}
}

// TestOrdering_AuthoredSource_IsAComponentLikeAnyOther: a source the author
// writes is ordered by nothing, so it shares the flat bundle.
func TestOrdering_AuthoredSource_IsAComponentLikeAnyOther(t *testing.T) {
	tr, _ := orderingTransformer(nil, nil)
	app := orderingApp(makeComponent("web", "webservice"), makeComponent("repo", "helmrepository"))
	cluster, err := tr.Transform(app, TransformContext{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertFlat(t, cluster, []string{"web", "repo"})
}

// --- the order entries are built in ---

// TestOrdering_Sequence: the order the application's entries are built in
// follows the groups, then the document.
func TestOrdering_Sequence(t *testing.T) {
	entry := func(name string, tier Tier) componentEntry {
		return componentEntry{component: Component{Name: name}, tier: tier}
	}
	tests := []struct {
		name    string
		entries []componentEntry
		deps    map[string][]string
		want    []string
	}{
		{
			name:    "nothing declared keeps document order",
			entries: []componentEntry{entry("a", ""), entry("b", ""), entry("c", "")},
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "tiers order before document position",
			entries: []componentEntry{entry("app", TierApps), entry("svc", TierServices), entry("infra", TierInfra)},
			want:    []string{"infra", "svc", "app"},
		},
		{
			name:    "a dependency moves a component to a later group",
			entries: []componentEntry{entry("a", ""), entry("b", ""), entry("c", "")},
			deps:    map[string][]string{"a": {"c"}},
			want:    []string{"b", "c", "a"},
		},
		{
			name:    "a dependency and tiers combine",
			entries: []componentEntry{entry("a", TierApps), entry("db", TierServices), entry("b", TierApps)},
			deps:    map[string][]string{"a": {"b"}},
			want:    []string{"db", "b", "a"},
		},
		{
			name:    "an unknown dependency name is ignored",
			entries: []componentEntry{entry("a", ""), entry("b", "")},
			deps:    map[string][]string{"a": {"missing"}},
			want:    []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := orderComponents(tt.entries, tt.deps)
			if err != nil {
				t.Fatalf("orderComponents: %v", err)
			}
			var got []string
			for _, e := range order.sequence() {
				got = append(got, e.component.Name)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("sequence = %v, want %v", got, tt.want)
			}
		})
	}
}
