package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// go-kure/launcher#790: a `service` of type ExternalName is a DNS alias and
// selects no pods. It owns its name, but a routing trait may not use it as its
// own backend, and a route naming it gets no synthesized NetworkPolicy: not on
// pods carrying its component's label, and not on pods a route's
// backendSelector names on its behalf.

var externalNamePorts = []any{map[string]any{"name": "pg", "port": 5432}}

// externalNameService is an ExternalName service named "db", with the given
// ports (nil for none).
func externalNameService(ports []any, traits ...oam.Trait) oam.Component {
	props := map[string]any{"type": "ExternalName", "externalName": "db.example.com"}
	if ports != nil {
		props["ports"] = ports
	}
	return oam.Component{Name: "db", Type: "service", Properties: props, Traits: traits}
}

// externalNamePortCases runs a subtest for a Service without ports and one
// with: its ports must change nothing about how it is routed to.
func externalNamePortCases(t *testing.T, run func(t *testing.T, ports []any)) {
	t.Helper()
	for name, ports := range map[string][]any{"no ports": nil, "with ports": externalNamePorts} {
		t.Run(name, func(t *testing.T) { run(t, ports) })
	}
}

// routeToDB is a deployment whose ingress names the Service "db" on port 5432
// and claims, through backendSelector, that its pods are app=unrelated.
func routeToDB() oam.Component { return routeTo("db") }

// routeTo is routeToDB for a Service of another name.
func routeTo(backend string) oam.Component {
	return oam.Component{
		Name:       "web",
		Type:       "deployment",
		Properties: map[string]any{"image": "ghcr.io/example/web:1.0"},
		Traits: []oam.Trait{ingressTrait(map[string]any{
			"path":            "/",
			"backend":         backend,
			"port":            5432,
			"backendSelector": map[string]any{"matchLabels": map[string]any{"app": "unrelated"}},
		})},
	}
}

// A routing trait that takes the ExternalName component as its backend has
// nothing to resolve: the Service reports no service port, whatever ports it
// lists.
func TestTransform_ExternalNameService_RefusesRoutingTrait(t *testing.T) {
	const noPort = `component "db" has no service port`
	const noServicePort = `servicePort may not be set on component "db": its Service has no ports to route to`
	rules := []any{map[string]any{"host": "db.example.com", "paths": []any{map[string]any{"path": "/"}}}}
	tests := []struct {
		name  string
		trait oam.Trait
		want  string
	}{
		{"ingress, implicit backend", ingressTrait(map[string]any{"path": "/"}), noPort},
		{"ingress, the Service's port number", ingressTrait(map[string]any{"path": "/", "port": 5432}), noPort},
		{"ingress, the Service's port name", ingressTrait(map[string]any{"path": "/", "portName": "pg"}), noPort},
		{"ingress, trait servicePort", oam.Trait{Type: "ingress", Properties: map[string]any{"servicePort": 5432, "rules": rules}}, noServicePort},
		{"httproute, implicit backend", oam.Trait{Type: "httproute", Properties: map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}}, "rules": []any{map[string]any{}},
		}}, noPort},
		{"httproute, trait servicePort", oam.Trait{Type: "httproute", Properties: map[string]any{
			"servicePort": 5432, "parentRefs": []any{map[string]any{"name": "gw"}}, "rules": []any{map[string]any{}},
		}}, noServicePort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			externalNamePortCases(t, func(t *testing.T, ports []any) {
				_, _, err := serviceKindTransformer().TransformWithPolicy(
					headlessRoutingApp(externalNameService(ports, tt.trait)),
					oam.TransformContext{Namespace: "default"})
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error = %v, want one containing %q", err, tt.want)
				}
			})
		})
	}
}

// The refusal above is of the component as a backend, not of the trait: an
// ingress on the ExternalName component that names another Service explicitly
// is carried as on any component. The route is to that Service, so the policy
// is the one that Service's component gets, and none is written for db.
func TestTransform_ExternalNameService_CarriesRouteToAnotherBackend(t *testing.T) {
	api := oam.Component{Name: "api", Type: "service", Properties: map[string]any{
		"selector": map[string]any{"app": "api-server"},
		"ports":    []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}},
	}}
	externalNamePortCases(t, func(t *testing.T, ports []any) {
		db := externalNameService(ports, ingressTrait(map[string]any{"path": "/", "backend": "api", "port": 80}))
		cluster, _, err := serviceKindTransformer().TransformWithPolicy(
			headlessRoutingApp(db, api),
			oam.TransformContext{Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx")})
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		if allows := synthesizedAllows(cluster); len(allows) != 1 || allows[0] != "api-allow-ingress-traffic" {
			t.Fatalf("synthesized allows = %v, want only api-allow-ingress-traffic", allows)
		}
		np := synthesizedNetworkPolicy(t, cluster, "api-allow-ingress-traffic")
		if sel := np.Spec.PodSelector.MatchLabels; len(sel) != 1 || sel["app"] != "api-server" {
			t.Errorf("podSelector = %v, want the routed Service's selector app=api-server", sel)
		}
		if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 8080 {
			t.Errorf("ingress = %+v, want one rule on the routed Service's target port 8080", np.Spec.Ingress)
		}
	})
}

// synthesizedAllows returns the names of the synthesized inbound allow
// policies in the cluster.
func synthesizedAllows(cluster *stack.Cluster) []string {
	var allows []string
	for _, name := range clusterAppNames(cluster) {
		if strings.HasSuffix(name, "-allow-ingress-traffic") {
			allows = append(allows, name)
		}
	}
	return allows
}

// routeToDBCluster transforms db beside a deployment routing to the Service
// "db", with the capability that makes a routed component get an allow policy.
func routeToDBCluster(t *testing.T, tr *oam.Transformer, db oam.Component) *stack.Cluster {
	t.Helper()
	return routedCluster(t, tr, db, routeToDB())
}

// routedCluster is routeToDBCluster for any routing component.
func routedCluster(t *testing.T, tr *oam.Transformer, db, route oam.Component) *stack.Cluster {
	t.Helper()
	cluster, _, err := tr.TransformWithPolicy(
		headlessRoutingApp(db, route),
		oam.TransformContext{Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx")})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	return cluster
}

// A route from another component naming the ExternalName Service is a route
// to a Service this application owns and that leads to no pods: no inbound
// policy is synthesized for it at all. Not the component-label one, which
// would select no pods here; and not one on the pods the route's
// backendSelector names, which is not trusted for an owned Service.
func TestTransform_ExternalNameService_RouteGetsNoPolicy(t *testing.T) {
	externalNamePortCases(t, func(t *testing.T, ports []any) {
		cluster := routeToDBCluster(t, serviceKindTransformer(), externalNameService(ports))
		if allows := synthesizedAllows(cluster); len(allows) != 0 {
			t.Fatalf("synthesized allows = %v, want none", allows)
		}
	})
}

// The control for the test above: the same route to a ClusterIP Service of the
// same name does get its policy, on the Service's selector, so "none" above is
// the ExternalName type and not a missing capability.
func TestTransform_ClusterIPService_SameRouteGetsPolicy(t *testing.T) {
	db := oam.Component{Name: "db", Type: "service", Properties: map[string]any{
		"selector": map[string]any{"app": "postgres"}, "ports": externalNamePorts,
	}}
	cluster := routeToDBCluster(t, serviceKindTransformer(), db)
	if allows := synthesizedAllows(cluster); len(allows) != 1 || allows[0] != "db-allow-ingress-traffic" {
		t.Fatalf("synthesized allows = %v, want only db-allow-ingress-traffic", allows)
	}
	np := synthesizedNetworkPolicy(t, cluster, "db-allow-ingress-traffic")
	if sel := np.Spec.PodSelector.MatchLabels; len(sel) != 1 || sel["app"] != "postgres" {
		t.Errorf("podSelector = %v, want the Service's selector app=postgres", sel)
	}
}

// An ExternalName Service named apart from its component (`objectName`) is
// reached by a route under the Service's name. It stays a Service this
// application owns that leads to no pods: no policy is synthesized, and the
// route's backendSelector is not trusted on its behalf.
func TestTransform_ExternalNameService_NamedByObjectName(t *testing.T) {
	externalNamePortCases(t, func(t *testing.T, ports []any) {
		db := externalNameService(ports)
		db.Properties["objectName"] = "db-alias"
		cluster := routedCluster(t, serviceKindTransformer(), db, routeTo("db-alias"))
		if allows := synthesizedAllows(cluster); len(allows) != 0 {
			t.Fatalf("synthesized allows = %v, want none", allows)
		}
		// The name the route used is the emitted Service's, so "none" above is
		// a route to this Service and not to a name nothing answers to.
		svc := applicationService(t, cluster, "db")
		if svc.Name != "db-alias" || svc.Spec.Type != corev1.ServiceTypeExternalName {
			t.Fatalf("Service = %s (type %s), want db-alias of type ExternalName", svc.Name, svc.Spec.Type)
		}
	})
}

// The control for the test above: the component's name is no longer the
// Service's. A route naming "db" names no Service of this application, so it
// is read as a route to a Service from elsewhere, and its backendSelector is
// what the policy opens.
func TestTransform_ExternalNameService_ComponentNameIsNotTheObjectName(t *testing.T) {
	externalNamePortCases(t, func(t *testing.T, ports []any) {
		db := externalNameService(ports)
		db.Properties["objectName"] = "db-alias"
		cluster := routedCluster(t, serviceKindTransformer(), db, routeTo("db"))
		allows := synthesizedAllows(cluster)
		if len(allows) != 1 {
			t.Fatalf("synthesized allows = %v, want one, for the Service from elsewhere", allows)
		}
		np := synthesizedNetworkPolicy(t, cluster, allows[0])
		if sel := np.Spec.PodSelector.MatchLabels; len(sel) != 1 || sel["app"] != "unrelated" {
			t.Errorf("podSelector of %s = %v, want the route's backendSelector app=unrelated", allows[0], sel)
		}
	})
}

// aliasedWorkloadRule emits a `deployment` and a `service` under the authored
// name: a sibling group. With externalName set the Service is an ExternalName
// one, which does not lead to the group's own pods; otherwise it is a ClusterIP
// Service selecting them. The authored traits go to the service member.
type aliasedWorkloadRule struct{ externalName bool }

func (aliasedWorkloadRule) ComponentType() string { return "aliased-workload" }

func (r aliasedWorkloadRule) LowerComponent(c *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	svc := map[string]any{"ports": externalNamePorts}
	if r.externalName {
		svc["type"] = "ExternalName"
		svc["externalName"] = "db.example.com"
	}
	return oam.LoweringResult{Components: []oam.Component{
		{Name: c.Name, Type: "deployment", Properties: map[string]any{"image": "ghcr.io/example/db:1.0"}},
		{Name: c.Name, Type: "service", Properties: svc, Traits: c.Traits},
	}}, nil
}

func aliasedWorkloadTransformer(externalName bool) *oam.Transformer {
	tr := serviceKindTransformer()
	tr.RegisterComponentLowering(aliasedWorkloadRule{externalName: externalName})
	return tr
}

func externalNameGroupTransformer() *oam.Transformer { return aliasedWorkloadTransformer(true) }

// In a sibling group the ExternalName member still gives the group no service
// port: a routing trait on the group is refused even though the group has a
// workload member.
func TestSiblingGroup_ExternalNameMemberRefusesRoutingTrait(t *testing.T) {
	for name, path := range map[string]map[string]any{
		"implicit backend":    {"path": "/"},
		"the Service's port":  {"path": "/", "port": 5432},
		"its own name, named": {"path": "/", "backend": "db", "port": 5432},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := externalNameGroupTransformer().TransformWithPolicy(
				headlessRoutingApp(oam.Component{Name: "db", Type: "aliased-workload", Traits: []oam.Trait{ingressTrait(path)}}),
				oam.TransformContext{Namespace: "default"})
			if want := `component "db" has no service port`; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
	}
}

// A route from another component to the group's ExternalName Service opens
// nothing on the workload member's pods. The group's component label is on
// those pods, so the component-label policy would open the routed port on them
// although the Service does not lead there; no policy is synthesized instead.
func TestSiblingGroup_ExternalNameMemberRouteOpensNothingOnWorkloadPods(t *testing.T) {
	cluster := routeToDBCluster(t, externalNameGroupTransformer(), oam.Component{Name: "db", Type: "aliased-workload"})
	if allows := synthesizedAllows(cluster); len(allows) != 0 {
		t.Fatalf("synthesized allows = %v, want none", allows)
	}
	// The group did deploy its workload, and its pods carry the label the
	// component-label policy selects (the control below): the absence above is
	// a policy withheld from real pods, not an empty group.
	labels := groupDeploymentPodLabels(t, cluster, "db")
	if labels[componentLabelKey] != "db" {
		t.Fatalf("pod labels of deployment db = %v, want %s=db", labels, componentLabelKey)
	}
}

// componentLabelKey is the pod label a component-label policy selects.
const componentLabelKey = "gokure.dev/component"

// groupDeploymentPodLabels generates the application named name and returns
// the pod template labels of the Deployment it emits.
func groupDeploymentPodLabels(t *testing.T, cluster *stack.Cluster, name string) map[string]string {
	t.Helper()
	for _, o := range applicationObjects(t, cluster, name) {
		if d, ok := o.(*appsv1.Deployment); ok {
			return d.Spec.Template.Labels
		}
	}
	t.Fatalf("application %q emits no Deployment", name)
	return nil
}

// applicationService generates the application named name and returns the
// Service it emits.
func applicationService(t *testing.T, cluster *stack.Cluster, name string) *corev1.Service {
	t.Helper()
	for _, o := range applicationObjects(t, cluster, name) {
		if svc, ok := o.(*corev1.Service); ok {
			return svc
		}
	}
	t.Fatalf("application %q emits no Service", name)
	return nil
}

// applicationObjects generates the application named name and returns its
// objects.
func applicationObjects(t *testing.T, cluster *stack.Cluster, name string) []client.Object {
	t.Helper()
	var found *stack.Application
	var visitBundle func(b *stack.Bundle)
	visitBundle = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		for _, a := range b.Applications {
			if a.Name == name && found == nil {
				found = a
			}
		}
		for _, ch := range b.Children {
			visitBundle(ch)
		}
	}
	var visitNode func(n *stack.Node)
	visitNode = func(n *stack.Node) {
		if n == nil {
			return
		}
		visitBundle(n.Bundle)
		for _, ch := range n.Children {
			visitNode(ch)
		}
	}
	visitNode(cluster.Node)
	if found == nil {
		t.Fatalf("no application %q; apps: %v", name, clusterAppNames(cluster))
	}
	objs, err := found.Config.Generate(found)
	if err != nil {
		t.Fatalf("Generate %q: %v", name, err)
	}
	out := make([]client.Object, 0, len(objs))
	for _, o := range objs {
		out = append(out, *o)
	}
	return out
}

// The control for the test above: the same group with a ClusterIP Service
// selecting its own pods keeps the component-label policy on the routed port.
func TestSiblingGroup_ClusterIPMemberSameRouteKeepsComponentPolicy(t *testing.T) {
	cluster := routeToDBCluster(t, aliasedWorkloadTransformer(false), oam.Component{Name: "db", Type: "aliased-workload"})
	if allows := synthesizedAllows(cluster); len(allows) != 1 || allows[0] != "db-allow-ingress-traffic" {
		t.Fatalf("synthesized allows = %v, want only db-allow-ingress-traffic", allows)
	}
	np := synthesizedNetworkPolicy(t, cluster, "db-allow-ingress-traffic")
	if sel := np.Spec.PodSelector.MatchLabels; len(sel) != 1 || sel[componentLabelKey] != "db" {
		t.Errorf("podSelector = %v, want only the component label of db", sel)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 ||
		np.Spec.Ingress[0].Ports[0].Port.IntVal != 5432 || *np.Spec.Ingress[0].Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("ingress = %+v, want one rule on TCP 5432", np.Spec.Ingress)
	}
}
