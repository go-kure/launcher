package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/fluxcd"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// webPairRule is a test ComponentLoweringRule that emits a `deployment` and a
// `service` under the authored name: the same-name sibling group the `webservice`
// rule will emit (go-kure/launcher#280).
// It routes the authored component's traits to the service member, as a rule must
// for a routing trait: traits run per member, against that member's own config.
// workload replaces the deployment member's type when set.
type webPairRule struct {
	paused   bool
	workload string
	// serviceTier, when set, classifies the service member into that tier by annotation.
	serviceTier string
}

func (webPairRule) ComponentType() string { return "web-pair" }

func (r webPairRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	workload, props := "deployment", webPairDeploymentProps(r.paused)
	if r.workload != "" {
		workload, props = r.workload, map[string]any{"image": "ghcr.io/org/app:v1"}
	}
	svc := oam.Component{Name: comp.Name, Type: "service", Properties: webPairServiceProps(comp.Name), Traits: comp.Traits}
	if r.serviceTier != "" {
		svc.Annotations = map[string]string{oam.TierAnnotationKey(oam.DefaultDomain): r.serviceTier}
	}
	return oam.LoweringResult{Components: []oam.Component{
		{Name: comp.Name, Type: workload, Properties: props},
		svc,
	}}, nil
}

// webPairDeploymentProps references a ReadWriteOnce claim so the Deployment has
// a non-empty NonRWXClaim to forward, and names its ServiceAccount so it has a
// non-empty ServiceAccountName: a deployment generates neither object
// (go-kure/launcher#702).
func webPairDeploymentProps(paused bool) map[string]any {
	props := map[string]any{
		"image":              "ghcr.io/org/app:v1",
		"serviceAccountName": "web-sa",
		"volumes": []any{map[string]any{
			"name": "data", "type": "pvc", "mountPath": "/data", "claimName": "data",
			"accessModes": []any{"ReadWriteOnce"},
		}},
	}
	if paused {
		props["paused"] = true
	}
	return props
}

func webPairServiceProps(name string) map[string]any {
	return map[string]any{
		"selector": map[string]any{"app": name},
		"ports":    []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}},
	}
}

// The contracts the engine and the builtin traits type-assert on a component's
// config, as the sibling group's config must answer them.
type (
	saNamer        interface{ ServiceAccountName() (string, bool) }
	nonRWXClaimer  interface{ NonRWXClaim() string }
	portProvider   interface{ ServicePort() int32 }
	portNamer      interface{ ServicePortName() (string, bool) }
	backendNamer   interface{ BackendServiceName() string }
	routingTargets interface {
		ServiceRoutingTarget([]intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString)
	}
)

func webPairTransformer(rule webPairRule) *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment":  &components.DeploymentHandler{},
		"service":     &components.ServiceHandler{},
		"svc-emitter": svcEmitterHandler{},
	}, map[string]oam.TraitHandler{"ingress": &traits.IngressHandler{}})
	tr.RegisterComponentLowering(rule)
	tr.RegisterPolicy("dependency", &policies.DependencyHandler{})
	tr.RegisterPolicy("placement", &policies.PlacementHandler{})
	return tr
}

// webPairApp authors one web-pair "web" depending on a plain deployment "db", so
// the cluster is built per component (buildDependencyAwareCluster): one bundle,
// node and Flux Kustomization per component name.
func webPairApp() *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "shop"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{Name: "web", Type: "web-pair", Properties: map[string]any{}},
				{Name: "db", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/db:v1"}},
			},
			Policies: []oam.ApplicationPolicy{{
				Name: "order", Type: "dependency",
				Properties: map[string]any{"rules": []any{
					map[string]any{"component": "web", "dependsOn": []any{"db"}},
				}},
			}},
		},
	}
}

func leafBundles(n *stack.Node) []*stack.Bundle {
	if n == nil {
		return nil
	}
	out := bundleTree(n.Bundle)
	for _, c := range n.Children {
		out = append(out, leafBundles(c)...)
	}
	return out
}

// bundleTree returns b and its nested child bundles that hold applications;
// a hierarchical cluster nests its tier bundles under an umbrella bundle.
func bundleTree(b *stack.Bundle) []*stack.Bundle {
	if b == nil {
		return nil
	}
	var out []*stack.Bundle
	if len(b.Applications) > 0 {
		out = append(out, b)
	}
	for _, c := range b.Children {
		out = append(out, bundleTree(c)...)
	}
	return out
}

// groupApp returns the one deployed application named name and the bundle
// holding it, failing unless exactly one exists across the cluster.
func groupApp(t *testing.T, cluster *stack.Cluster, name string) (*stack.Application, *stack.Bundle) {
	t.Helper()
	var app *stack.Application
	var bundle *stack.Bundle
	for _, b := range leafBundles(cluster.Node) {
		for _, a := range b.Applications {
			if a.Name != name {
				continue
			}
			if app != nil {
				t.Fatalf("component %q deploys as more than one application", name)
			}
			app, bundle = a, b
		}
	}
	if app == nil {
		t.Fatalf("no application named %q", name)
	}
	return app, bundle
}

func objectIDs(objs []*client.Object) []string {
	ids := make([]string, 0, len(objs))
	for _, o := range objs {
		ids = append(ids, (*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName())
	}
	return ids
}

func memberConfig(t *testing.T, h oam.ComponentHandler, typ string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "web", Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("%s ToApplicationConfig: %v", typ, err)
	}
	return cfg
}

// TestSiblingGroup_ForwardsEachContractToItsMember proves the group's one config
// answers every forwarded contract with the member that owns it: the Deployment's
// ServiceAccount and single-pod claim, the Service's port, port
// name and routing target, and no backend Service name, which neither member sets.
func TestSiblingGroup_ForwardsEachContractToItsMember(t *testing.T) {
	cluster, _, err := webPairTransformer(webPairRule{}).TransformWithPolicy(webPairApp(), oam.TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	app, _ := groupApp(t, cluster, "web")
	cfg := app.Config

	dep := memberConfig(t, &components.DeploymentHandler{}, "deployment", webPairDeploymentProps(false))
	svc := memberConfig(t, &components.ServiceHandler{}, "service", webPairServiceProps("web"))

	wantSA, _ := dep.(saNamer).ServiceAccountName()
	if got, _ := cfg.(saNamer).ServiceAccountName(); wantSA == "" || got != wantSA {
		t.Errorf("ServiceAccountName = %q, want the deployment's %q", got, wantSA)
	}
	wantClaim := dep.(nonRWXClaimer).NonRWXClaim()
	if got := cfg.(nonRWXClaimer).NonRWXClaim(); wantClaim == "" || got != wantClaim {
		t.Errorf("NonRWXClaim = %q, want the deployment's %q", got, wantClaim)
	}
	if got, want := cfg.(portProvider).ServicePort(), svc.(portProvider).ServicePort(); want != 80 || got != want {
		t.Errorf("ServicePort = %d, want the service's %d", got, want)
	}
	gotName, gotKnown := cfg.(portNamer).ServicePortName()
	wantName, wantKnown := svc.(portNamer).ServicePortName()
	if !wantKnown || gotName != wantName || gotKnown != wantKnown {
		t.Errorf("ServicePortName = (%q, %v), want the service's (%q, %v)", gotName, gotKnown, wantName, wantKnown)
	}
	routed := []intstr.IntOrString{intstr.FromInt32(80)}
	gotSel, gotPorts := cfg.(routingTargets).ServiceRoutingTarget(routed)
	wantSel, wantPorts := svc.(routingTargets).ServiceRoutingTarget(routed)
	if wantSel == nil || !reflect.DeepEqual(gotSel, wantSel) || !reflect.DeepEqual(gotPorts, wantPorts) {
		t.Errorf("ServiceRoutingTarget = (%v, %v), want the service's (%v, %v)", gotSel, gotPorts, wantSel, wantPorts)
	}
	if got := cfg.(backendNamer).BackendServiceName(); got != "" {
		t.Errorf("BackendServiceName = %q, want \"\": no member names a backend Service", got)
	}
}

// TestSiblingGroup_DeploysAsOneUnit proves the group is one component to every
// name-keyed step: one application generating the Deployment, the Service, then
// the Deployment's other objects,
// one bundle with a dependsOn on db, one layout
// directory holding both objects, and one Flux Kustomization.
func TestSiblingGroup_DeploysAsOneUnit(t *testing.T) {
	cluster, _, err := webPairTransformer(webPairRule{}).TransformWithPolicy(webPairApp(), oam.TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	app, bundle := groupApp(t, cluster, "web")

	objs, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The group generates exactly what its members generate on their own, each
	// member's primary object first — the Deployment, then the Service — and then
	// any remaining objects. Neither member generates more than its primary
	// object (a deployment emits no ServiceAccount or claim, go-kure/launcher#702),
	// so the tail is empty here; pkg/oam's sibling-group tests pin the tail order.
	var heads, tails []string
	for _, m := range []struct {
		h     oam.ComponentHandler
		typ   string
		props map[string]any
	}{
		{&components.DeploymentHandler{}, "deployment", webPairDeploymentProps(false)},
		{&components.ServiceHandler{}, "service", webPairServiceProps("web")},
	} {
		alone, err := stack.NewApplication("web", "default", memberConfig(t, m.h, m.typ, m.props)).Generate()
		if err != nil {
			t.Fatalf("%s Generate: %v", m.typ, err)
		}
		ids := objectIDs(alone)
		heads = append(heads, ids[0])
		tails = append(tails, ids[1:]...)
	}
	want := slices.Concat(heads, tails)
	if !reflect.DeepEqual(want, []string{"Deployment/web", "Service/web"}) {
		t.Fatalf("members generate %v; want a Deployment and a Service", want)
	}
	if got := objectIDs(objs); !reflect.DeepEqual(got, want) {
		t.Errorf("group generates %v, want %v", got, want)
	}

	if len(bundle.HealthChecks) != 0 {
		t.Errorf("health checks = %v, want none: launcher sets no delivery field", bundle.HealthChecks)
	}
	if len(bundle.DependsOn) != 1 || !strings.HasSuffix(bundle.DependsOn[0].Name, "-db") {
		t.Errorf("web bundle dependsOn = %d bundles, want exactly db's", len(bundle.DependsOn))
	}

	ml, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		t.Fatalf("WalkCluster: %v", err)
	}
	var holding []*layout.ManifestLayout
	var walk func(*layout.ManifestLayout)
	walk = func(l *layout.ManifestLayout) {
		for _, o := range l.Resources {
			if o.GetName() == "web" {
				holding = append(holding, l)
				break
			}
		}
		for _, c := range l.Children {
			walk(c)
		}
	}
	walk(ml)
	if len(holding) != 1 || len(holding[0].Resources) != len(objs) {
		t.Fatalf("web's objects sit in %d layout directories, want exactly one holding all %d", len(holding), len(objs))
	}

	for _, b := range leafBundles(cluster.Node) {
		b.SourceRef = &stack.SourceRef{Kind: "OCIRepository", Name: b.Name, URL: "oci://registry.example/" + b.Name, Tag: "v1"}
	}
	flux, err := fluxcd.NewResourceGenerator().GenerateFromLayout(ml, cluster)
	if err != nil {
		t.Fatalf("GenerateFromLayout: %v", err)
	}
	var web []string
	for _, o := range flux {
		if k, ok := o.(*kustv1.Kustomization); ok && strings.Contains(k.Name, "web") {
			web = append(web, k.Name)
		}
	}
	if len(web) != 1 {
		t.Errorf("Flux Kustomizations for web = %v, want exactly one", web)
	}
}

// TestSiblingGroup_RoutingTraitOnTheServiceMember: a routing trait the rule places
// on the service member resolves that member's port and Service name, and its
// sub-application joins the group's one bundle.
func TestSiblingGroup_RoutingTraitOnTheServiceMember(t *testing.T) {
	doc := webPairApp()
	doc.Spec.Components[0].Traits = []oam.Trait{{Type: "ingress", Properties: map[string]any{
		"rules": []any{map[string]any{"host": "web.example.com", "paths": []any{map[string]any{"path": "/"}}}},
	}}}
	cluster, _, err := webPairTransformer(webPairRule{}).TransformWithPolicy(doc, oam.TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	_, bundle := groupApp(t, cluster, "web")
	var ingress *stack.Application
	for _, a := range bundle.Applications {
		if a.Name == "web-ingress" {
			ingress = a
		}
	}
	if ingress == nil {
		t.Fatal("the group's bundle carries no web-ingress application")
	}
	objs, err := ingress.Generate()
	if err != nil {
		t.Fatalf("ingress Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("ingress generates %d objects, want 1", len(objs))
	}
	ing, ok := (*objs[0]).(*networkingv1.Ingress)
	if !ok {
		t.Fatalf("ingress generates %T, want *networkingv1.Ingress", *objs[0])
	}
	backend := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	if backend.Name != "web" || backend.Port.Number != 80 {
		t.Errorf("ingress backend = %s:%d, want the service member's web:80", backend.Name, backend.Port.Number)
	}
}

// TestSiblingGroup_PlacementSettlesMixedTiers: members classified into different
// tiers are refused, unless a placement policy places the group, which overrides
// classification and gives the group its one tier.
func TestSiblingGroup_PlacementSettlesMixedTiers(t *testing.T) {
	rule := webPairRule{serviceTier: "infra"}

	_, _, err := webPairTransformer(rule).TransformWithPolicy(webPairApp(), oam.TransformContext{})
	if want := "a group deploys as one unit and needs one tier"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("unplaced mixed group: err = %v, want it to contain %q", err, want)
	}

	// No dependency web -> db here: placing web in infra, ahead of db, would cycle.
	doc := webPairApp()
	doc.Spec.Policies = []oam.ApplicationPolicy{{
		Name: "web-in-infra", Type: "placement",
		Properties: map[string]any{"component": "web", "tier": "infra"},
	}}
	cluster, _, err := webPairTransformer(rule).TransformWithPolicy(doc, oam.TransformContext{})
	if err != nil {
		t.Fatalf("placed mixed group: TransformWithPolicy: %v", err)
	}
	_, web := groupApp(t, cluster, "web")
	_, db := groupApp(t, cluster, "db")
	if !strings.Contains(web.Name, "infra") || strings.Contains(db.Name, "infra") {
		t.Errorf("bundles: web in %q, db in %q; want web alone in the infra tier", web.Name, db.Name)
	}
}

// svcEmitterHandler is a test component that generates a Service named after
// its Application and answers none of the group's contracts. No builtin kind
// besides `service` generates a Service since go-kure/launcher#690.
type svcEmitterHandler struct{}

func (svcEmitterHandler) CanHandle(t string) bool { return t == "svc-emitter" }

func (svcEmitterHandler) ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error) {
	return svcEmitterConfig{}, nil
}

type svcEmitterConfig struct{}

func (svcEmitterConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	svc := client.Object(&corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: app.Namespace},
	})
	return []*client.Object{&svc}, nil
}

// TestSiblingGroup_TwoMembersGeneratingOneObjectRefused: the workload member
// generates a Service named after the group, which the service member also
// generates; the group refuses rather than deploy one object twice.
func TestSiblingGroup_TwoMembersGeneratingOneObjectRefused(t *testing.T) {
	cluster, _, err := webPairTransformer(webPairRule{workload: "svc-emitter"}).TransformWithPolicy(webPairApp(), oam.TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	app, _ := groupApp(t, cluster, "web")
	_, err = app.Generate()
	if want := `members "svc-emitter" and "service" both generate Service "default/web"`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Generate err = %v, want it to contain %q", err, want)
	}
}
