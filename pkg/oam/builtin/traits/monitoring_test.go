package traits_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The monitoring trait writes the VictoriaMetrics objects of a workload: a
// VMServiceScrape for a component with a Service, a VMPodScrape for one
// without, and a VMRule when rule groups are given (go-kure/launcher#1736).
// These tests run the whole transformer with every built-in registered, as the
// CLI does, because a webservice's trait is routed to its Service member by the
// lowering rule.

const (
	vmGroup   = "operator.victoriametrics.com"
	vmVersion = "v1beta1"
)

func monitoringTrait(props map[string]any) oam.Trait {
	return oam.Trait{Type: "monitoring", Properties: props}
}

// monitoringObjects transforms comps under policy (nil: none) and returns every
// VictoriaMetrics object they generate, or the first error of the transform or
// of a Generate.
func monitoringObjects(t *testing.T, policy oam.Policy, comps ...oam.Component) ([]*unstructured.Unstructured, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	cluster, err := disruptionTransformer().Transform(app, oam.TransformContext{Policy: policy})
	if err != nil {
		return nil, err
	}
	if _, err := oam.GenerateApplications(cluster); err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					return err
				}
				for _, o := range objs {
					if u, ok := (*o).(*unstructured.Unstructured); ok && u.GroupVersionKind().Group == vmGroup {
						out = append(out, u)
					}
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(cluster.Node); err != nil {
		return nil, err
	}
	return out, nil
}

// onlyOfKind returns the one object of kind, failing when there is not exactly one.
func onlyOfKind(t *testing.T, objs []*unstructured.Unstructured, kind string) *unstructured.Unstructured {
	t.Helper()
	var found []*unstructured.Unstructured
	for _, o := range objs {
		if o.GetKind() == kind {
			found = append(found, o)
		}
	}
	if len(found) != 1 {
		t.Fatalf("generated %d %s objects, want 1 (all: %v)", len(found), kind, kindsOf(objs))
	}
	return found[0]
}

func kindsOf(objs []*unstructured.Unstructured) []string {
	var kinds []string
	for _, o := range objs {
		kinds = append(kinds, o.GetKind()+"/"+o.GetName())
	}
	return kinds
}

func nested(t *testing.T, o *unstructured.Unstructured, fields ...string) any {
	t.Helper()
	v, found, err := unstructured.NestedFieldNoCopy(o.Object, fields...)
	if err != nil || !found {
		t.Fatalf("%s %q has no %s (err %v)", o.GetKind(), o.GetName(), strings.Join(fields, "."), err)
	}
	return v
}

// TestMonitoring_WebserviceWritesAServiceScrape: a webservice has a Service, so
// the scrape is a VMServiceScrape that selects that Service by the component's
// `app` label and scrapes the Service's named port.
func TestMonitoring_WebserviceWritesAServiceScrape(t *testing.T) {
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice",
		monitoringTrait(map[string]any{"path": "/stats", "interval": "30s"})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %v, want one VMServiceScrape and no VMRule", kindsOf(objs))
	}
	scrape := onlyOfKind(t, objs, "VMServiceScrape")
	if scrape.GetAPIVersion() != vmGroup+"/"+vmVersion || scrape.GetName() != "web" || scrape.GetNamespace() != "default" {
		t.Errorf("object is %s %s/%s, want %s/%s default/web", scrape.GetAPIVersion(), scrape.GetNamespace(), scrape.GetName(), vmGroup, vmVersion)
	}
	if got := nested(t, scrape, "spec", "selector", "matchLabels", "app"); got != "web" {
		t.Errorf("selector app = %v, want web", got)
	}
	eps, _ := nested(t, scrape, "spec", "endpoints").([]any)
	if len(eps) != 1 {
		t.Fatalf("endpoints = %v, want one", eps)
	}
	ep := eps[0].(map[string]any)
	if ep["port"] != "http" || ep["path"] != "/stats" || ep["interval"] != "30s" {
		t.Errorf("endpoint = %v, want port http, path /stats, interval 30s", ep)
	}
	if _, has := ep["targetPort"]; has {
		t.Errorf("endpoint writes targetPort beside the port name: %v", ep)
	}
}

// TestMonitoring_WebserviceKeepsDefaultsUnwritten: a path and an interval the
// author left out are left out of the object, so VictoriaMetrics' own defaults
// apply.
func TestMonitoring_WebserviceKeepsDefaultsUnwritten(t *testing.T) {
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(map[string]any{})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	ep := nested(t, onlyOfKind(t, objs, "VMServiceScrape"), "spec", "endpoints").([]any)[0].(map[string]any)
	for _, key := range []string{"path", "interval"} {
		if _, has := ep[key]; has {
			t.Errorf("endpoint writes %s the author left out: %v", key, ep)
		}
	}
}

// TestMonitoring_WebserviceIntegerPortIsATargetPort: a port written as a number
// names the pod's port, so it is written as targetPort, not as a Service port name.
func TestMonitoring_WebserviceIntegerPortIsATargetPort(t *testing.T) {
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(map[string]any{"port": 9090})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	ep := nested(t, onlyOfKind(t, objs, "VMServiceScrape"), "spec", "endpoints").([]any)[0].(map[string]any)
	if _, has := ep["port"]; has {
		t.Errorf("endpoint writes a port name for a number: %v", ep)
	}
	if got, ok := ep["targetPort"].(int64); !ok || got != 9090 {
		t.Errorf("targetPort = %v (%T), want 9090", ep["targetPort"], ep["targetPort"])
	}
}

// TestMonitoring_WorkerWritesAPodScrape: a worker has no Service, so the scrape
// is a VMPodScrape that selects its pods by the `app` label, on the port the
// author names (a worker declares no ports of its own).
func TestMonitoring_WorkerWritesAPodScrape(t *testing.T) {
	objs, err := monitoringObjects(t, nil, disruptionComponent("jobs", "worker", monitoringTrait(map[string]any{"port": 9100, "interval": "1m"})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("generated %v, want one VMPodScrape", kindsOf(objs))
	}
	scrape := onlyOfKind(t, objs, "VMPodScrape")
	if scrape.GetName() != "jobs" {
		t.Errorf("name = %q, want jobs", scrape.GetName())
	}
	if got := nested(t, scrape, "spec", "selector", "matchLabels", "app"); got != "jobs" {
		t.Errorf("selector app = %v, want jobs", got)
	}
	ep := nested(t, scrape, "spec", "podMetricsEndpoints").([]any)[0].(map[string]any)
	if got, ok := ep["portNumber"].(int64); !ok || got != 9100 || ep["interval"] != "1m" {
		t.Errorf("endpoint = %v, want portNumber 9100 and interval 1m", ep)
	}
	if _, has := ep["port"]; has {
		t.Errorf("endpoint writes a port name for a number: %v", ep)
	}
}

// TestMonitoring_WorkerNamedPort: a port written as a name is a container port's name.
func TestMonitoring_WorkerNamedPort(t *testing.T) {
	objs, err := monitoringObjects(t, nil, disruptionComponent("jobs", "worker", monitoringTrait(map[string]any{"port": "metrics"})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	ep := nested(t, onlyOfKind(t, objs, "VMPodScrape"), "spec", "podMetricsEndpoints").([]any)[0].(map[string]any)
	if ep["port"] != "metrics" {
		t.Errorf("endpoint = %v, want port metrics", ep)
	}
	if _, has := ep["portNumber"]; has {
		t.Errorf("endpoint writes a port number for a name: %v", ep)
	}
}

// TestMonitoring_RulesWriteAVMRule: rule groups give a VMRule beside the scrape;
// none gives no VMRule.
func TestMonitoring_RulesWriteAVMRule(t *testing.T) {
	props := map[string]any{"rules": []any{
		map[string]any{"name": "availability", "interval": "30s", "rules": []any{
			map[string]any{"alert": "WebDown", "expr": `up{job="web"} == 0`, "for": "5m",
				"labels": map[string]any{"severity": "page"}, "annotations": map[string]any{"summary": "web is down"}},
			map[string]any{"record": "web:up:ratio", "expr": `avg(up{job="web"})`},
		}},
	}}
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(props)))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	onlyOfKind(t, objs, "VMServiceScrape")
	rule := onlyOfKind(t, objs, "VMRule")
	if rule.GetName() != "web-rules" || rule.GetNamespace() != "default" {
		t.Errorf("rule is %s/%s, want default/web-rules", rule.GetNamespace(), rule.GetName())
	}
	groups := nested(t, rule, "spec", "groups").([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want one", groups)
	}
	g := groups[0].(map[string]any)
	rules := g["rules"].([]any)
	if g["name"] != "availability" || g["interval"] != "30s" || len(rules) != 2 {
		t.Fatalf("group = %v, want availability, 30s, two rules", g)
	}
	alert := rules[0].(map[string]any)
	if alert["alert"] != "WebDown" || alert["expr"] != `up{job="web"} == 0` || alert["for"] != "5m" {
		t.Errorf("alert = %v", alert)
	}
	if alert["labels"].(map[string]any)["severity"] != "page" || alert["annotations"].(map[string]any)["summary"] != "web is down" {
		t.Errorf("alert labels or annotations lost: %v", alert)
	}
	if rec := rules[1].(map[string]any); rec["record"] != "web:up:ratio" || rec["expr"] != `avg(up{job="web"})` {
		t.Errorf("record = %v", rec)
	}

	// The same trait with no rules writes no VMRule.
	objs, err = monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(map[string]any{"rules": []any{}})))
	if err != nil {
		t.Fatalf("transform with an empty rules list: %v", err)
	}
	if len(objs) != 1 || objs[0].GetKind() != "VMServiceScrape" {
		t.Errorf("an empty rules list generated %v, want only the VMServiceScrape", kindsOf(objs))
	}
}

// TestMonitoring_AuthoredNames: scrapeName and ruleName name the objects, as
// written.
func TestMonitoring_AuthoredNames(t *testing.T) {
	props := map[string]any{
		"scrapeName": "web-scrape", "ruleName": "web-alerts",
		"rules": []any{map[string]any{"name": "g", "rules": []any{map[string]any{"record": "r", "expr": "up"}}}},
	}
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(props)))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := onlyOfKind(t, objs, "VMServiceScrape").GetName(); got != "web-scrape" {
		t.Errorf("scrape name = %q, want web-scrape", got)
	}
	if got := onlyOfKind(t, objs, "VMRule").GetName(); got != "web-alerts" {
		t.Errorf("rule name = %q, want web-alerts", got)
	}
}

func oneGroup(rules ...any) []any {
	return []any{map[string]any{"name": "g", "rules": rules}}
}

// TestMonitoring_Refusals: every refusal names the property it is about.
func TestMonitoring_Refusals(t *testing.T) {
	alert := map[string]any{"alert": "A", "expr": "up == 0"}
	cases := []struct {
		name  string
		kind  string
		props map[string]any
		want  string
	}{
		{"unknown key", "webservice", map[string]any{"selector": map[string]any{}}, `unknown property "selector"`},
		{"worker without a port", "worker", map[string]any{}, "port"},
		{"port zero", "webservice", map[string]any{"port": 0}, "port"},
		{"port too large", "webservice", map[string]any{"port": 70000}, "port"},
		{"port is a list", "webservice", map[string]any{"port": []any{80}}, "port"},
		{"empty port name", "worker", map[string]any{"port": ""}, "port"},
		{"path without a slash", "webservice", map[string]any{"path": "metrics"}, "path"},
		{"empty path", "webservice", map[string]any{"path": ""}, "path"},
		{"empty interval", "webservice", map[string]any{"interval": ""}, "interval"},
		{"interval with a space", "webservice", map[string]any{"interval": "30 s"}, "interval"},
		{"interval is a number", "webservice", map[string]any{"interval": 30}, "interval"},
		{"rules is not a list", "webservice", map[string]any{"rules": "x"}, "rules"},
		{"group is not an object", "webservice", map[string]any{"rules": []any{"x"}}, "rules[0]"},
		{"group without a name", "webservice", map[string]any{"rules": []any{map[string]any{"rules": []any{alert}}}}, `rules[0]: "name" is required`},
		{"duplicate group names", "webservice", map[string]any{"rules": []any{
			map[string]any{"name": "g", "rules": []any{alert}}, map[string]any{"name": "g", "rules": []any{alert}}}}, `group name "g"`},
		{"group without rules", "webservice", map[string]any{"rules": []any{map[string]any{"name": "g", "rules": []any{}}}}, "rules[0].rules"},
		{"unknown group key", "webservice", map[string]any{"rules": []any{map[string]any{"name": "g", "limit": 1, "rules": []any{alert}}}}, `unknown property "limit"`},
		{"alert and record", "webservice", map[string]any{"rules": oneGroup(map[string]any{"alert": "A", "record": "r", "expr": "up"})}, `"alert" and "record" are mutually exclusive`},
		{"neither alert nor record", "webservice", map[string]any{"rules": oneGroup(map[string]any{"expr": "up"})}, `exactly one of "alert" and "record"`},
		{"empty expr", "webservice", map[string]any{"rules": oneGroup(map[string]any{"alert": "A", "expr": ""})}, "expr"},
		{"missing expr", "webservice", map[string]any{"rules": oneGroup(map[string]any{"alert": "A"})}, "expr"},
		{"unknown rule key", "webservice", map[string]any{"rules": oneGroup(map[string]any{"alert": "A", "expr": "up", "debug": true})}, `unknown property "debug"`},
		{"labels not strings", "webservice", map[string]any{"rules": oneGroup(map[string]any{"alert": "A", "expr": "up", "labels": map[string]any{"a": 1}})}, "labels"},
		{"port name the Service lacks", "webservice", map[string]any{"port": "metrics"}, `port "metrics" is not the name of the Service port`},
		{"ruleName without rules", "webservice", map[string]any{"ruleName": "web-alerts"}, "ruleName"},
		{"ruleName with an empty rules list", "webservice", map[string]any{"ruleName": "web-alerts", "rules": []any{}}, "ruleName"},
		{"empty scrapeName", "webservice", map[string]any{"scrapeName": ""}, "scrapeName"},
		{"bad ruleName", "webservice", map[string]any{"ruleName": "Not Valid", "rules": oneGroup(map[string]any{"record": "r", "expr": "up"})}, "ruleName"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := monitoringObjects(t, nil, disruptionComponent("web", tc.kind, monitoringTrait(tc.props)))
			wantRefused(t, err, tc.want)
		})
	}
}

// TestMonitoring_TypeRestriction: the trait is admitted on a webservice and a
// worker, and refused on the component types it does not read a workload from.
func TestMonitoring_TypeRestriction(t *testing.T) {
	_, err := monitoringObjects(t, nil, disruptionComponent("app", "daemonset", monitoringTrait(map[string]any{"port": 9100})))
	wantRefused(t, err, "monitoring", "daemonset")
}

// TestMonitoring_NoVMObjectBeyondTheTrait: two components with the trait write
// each its own scrape, and nothing else is written.
func TestMonitoring_TwoComponents(t *testing.T) {
	objs, err := monitoringObjects(t, nil,
		disruptionComponent("web", "webservice", monitoringTrait(map[string]any{})),
		disruptionComponent("jobs", "worker", monitoringTrait(map[string]any{"port": 9100})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	got := kindsOf(objs)
	slices.Sort(got)
	if want := []string{"VMPodScrape/jobs", "VMServiceScrape/web"}; !slices.Equal(got, want) {
		t.Errorf("generated %v, want %v", got, want)
	}
}

// TestMonitoring_SchemaDeclaresNoBound: the trait raises no refusal of a policy
// class (it writes no image, resource, storage, capability, host namespace or
// secret), so its schema publishes no boundedBy, at any depth.
func TestMonitoring_SchemaDeclaresNoBound(t *testing.T) {
	var walk func(path string, props map[string]oam.PropertySchema)
	walk = func(path string, props map[string]oam.PropertySchema) {
		for name, p := range props {
			if len(p.BoundedBy) > 0 {
				t.Errorf("%s%s declares boundedBy %v", path, name, p.BoundedBy)
			}
			walk(path+name+".", p.Properties)
			if p.Items != nil {
				walk(path+name+"[].", p.Items.Properties)
			}
		}
	}
	walk("", (&traits.MonitoringHandler{}).PropertySchema())
}

// TestMonitoring_ObjectKindPolicyRefusesUnknownScope pins a limit, tracked by
// go-kure/launcher#1744: the base library does not know the scope of the
// VictoriaMetrics kinds and checkObjectKind passes it no CRD scopes, so under a
// policy that does not allow cluster-scoped objects the trait's objects are
// refused as of unknown scope, as any custom resource of an unregistered kind is.
func TestMonitoring_ObjectKindPolicyRefusesUnknownScope(t *testing.T) {
	comp := disruptionComponent("web", "webservice", monitoringTrait(map[string]any{}))
	_, err := monitoringObjects(t, &kindOnlyPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}}, comp)
	var v *oam.ViolationError
	if !errors.As(err, &v) || v.Class != oam.RefusalObjectKind || v.Component != "web" {
		t.Fatalf("err = %v, want component web's violation of class %q", err, oam.RefusalObjectKind)
	}
	for _, part := range []string{"VMServiceScrape", "scope"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("refusal %q does not contain %q", err, part)
		}
	}
	// Where cluster-scoped objects are allowed the scope is not read.
	if _, err := monitoringObjects(t, &kindOnlyPolicy{allowCluster: true}, comp); err != nil {
		t.Errorf("under a policy that allows cluster-scoped objects: %v", err)
	}
}

// TestMonitoring_ReportsDefaultNames: an object name that is launcher's default
// is reported as a fill (oam.BuiltinDefaultReporter), and one the author wrote
// is not. The VMRule's name is reported only where a VMRule is written.
func TestMonitoring_ReportsDefaultNames(t *testing.T) {
	reported := func(props map[string]any) []string {
		app := &oam.Application{
			APIVersion: oam.SupportedAPIVersion, Kind: "Application",
			Metadata: oam.Metadata{Name: "pkg", Namespace: "default"},
			Spec:     oam.ApplicationSpec{Components: []oam.Component{disruptionComponent("web", "webservice", monitoringTrait(props))}},
		}
		cluster, err := disruptionTransformer().Transform(app, oam.TransformContext{})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		var out []string
		var walk func(node *stack.Node)
		walk = func(node *stack.Node) {
			if node == nil {
				return
			}
			if node.Bundle != nil {
				for _, a := range node.Bundle.Applications {
					if cfg, ok := oam.UnwrapConfig(a.Config).(*traits.MonitoringConfig); ok {
						for _, d := range cfg.BuiltinDefaults() {
							out = append(out, d.Path)
						}
					}
				}
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(cluster.Node)
		slices.Sort(out)
		return out
	}
	rules := []any{map[string]any{"name": "g", "rules": []any{map[string]any{"record": "r", "expr": "up"}}}}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{"no rules", map[string]any{}, []string{"scrapeName"}},
		{"rules, both default", map[string]any{"rules": rules}, []string{"ruleName", "scrapeName"}},
		{"names written", map[string]any{"rules": rules, "scrapeName": "a", "ruleName": "b"}, nil},
	} {
		if got := reported(tc.props); !slices.Equal(got, tc.want) {
			t.Errorf("%s: reported %v, want %v", tc.name, got, tc.want)
		}
	}
}
