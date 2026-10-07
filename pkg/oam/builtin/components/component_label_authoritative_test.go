package components_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// clLabelAt returns the value obj holds for key in the labels at path, and
// whether it holds one.
func clLabelAt(t *testing.T, obj client.Object, key string, path ...string) (string, bool) {
	t.Helper()
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatalf("to unstructured: %v", err)
	}
	labels, found, err := unstructured.NestedMap(content, append(path, "labels")...)
	if err != nil {
		t.Fatalf("labels at %s: %v", strings.Join(path, "."), err)
	}
	if !found {
		return "", false
	}
	v, held := labels[key].(string)
	return v, held
}

// clGenerate transforms app under ctx and generates every application.
func clGenerate(tr *oam.Transformer, app *oam.Application, ctx oam.TransformContext) ([]client.Object, error) {
	ctx.Namespace = "demo"
	// A document a lowering rule runs on states what it is.
	app.APIVersion, app.Kind = oam.SupportedAPIVersion, "Application"
	cluster, err := tr.Transform(app, ctx)
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// clObject returns the object of kind named name among objs.
func clObject(t *testing.T, objs []client.Object, kind, name string) client.Object {
	t.Helper()
	for _, o := range objs {
		if o.GetObjectKind().GroupVersionKind().Kind == kind && o.GetName() == name {
			return o
		}
	}
	t.Fatalf("no %s %q among the %d generated objects", kind, name, len(objs))
	return nil
}

// TestComponentLabel_AuthoritativeOnEveryPath is go-kure/launcher#790: every
// path that carries labels onto pods holds the component label's key to the
// owning component's value. A document, a kind's property or a rendered chart
// that puts another component's value there is refused, with the component, the
// object and the path. The kinds' `labels` property refuses the same value in
// the transform, as before.
func TestComponentLabel_AuthoritativeOnEveryPath(t *testing.T) {
	key := oam.ComponentLabelKeyForDomain("")
	podSpec := "    spec:\n" + htIndent(htPlainPod, "      ")
	template := func(labels string) string {
		return "template:\n  metadata:\n    labels:\n" + htIndent(labels, "      ") + "  spec:\n" + htIndent(htPlainPod, "    ")
	}
	foreign := key + ": db\n"
	deployment := func(selector, podLabels string) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  selector:\n    matchLabels:\n" + htIndent(selector, "      ") +
			"  template:\n    metadata:\n      labels:\n" + htIndent(podLabels, "        ") + podSpec
	}
	refusedAt := func(object, path string) []string {
		return []string{object, path + `["` + key + `"]: "db" is not the component label of component "web" ("web")`}
	}

	for _, tc := range []struct {
		name    string
		typ     string
		handler oam.ComponentHandler
		props   func(t *testing.T) map[string]any
		// refused is what the refusal says; nil when the document builds.
		refused []string
		// kind is which refusal it is; empty for a value an object holds.
		kind oam.ComponentLabelRefusal
		// built checks the objects of a document that builds.
		built func(t *testing.T, objs []client.Object)
	}{
		{
			name: "passthrough Deployment, pod template", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, deployment("tier: front\n", "tier: front\n"+foreign))}
			},
			refused: refusedAt(`Deployment "web"`, "spec.template.metadata.labels"),
		},
		{
			name: "passthrough Deployment, selector and pod template", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, deployment(foreign, foreign))}
			},
			refused: refusedAt(`Deployment "web"`, "spec.template.metadata.labels"),
		},
		{
			name: "passthrough Deployment, selector alone", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, deployment(foreign, "tier: front\n"))}
			},
			refused: []string{`Deployment "web"`, `spec.selector requires "db" for the label "` + key + `"`, `component "web" ("web")`},
			kind:    oam.ComponentLabelSelectorRequiresAnother,
		},
		{
			name: "passthrough Deployment, selector rules the label out", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  selector:\n"+
					"    matchLabels:\n      tier: front\n    matchExpressions:\n    - key: "+key+"\n      operator: DoesNotExist\n"+
					"  template:\n    metadata:\n      labels:\n        tier: front\n"+podSpec)}
			},
			refused: []string{`Deployment "web"`, `spec.selector rules out the label "` + key + `"`, `component "web" ("web")`},
			kind:    oam.ComponentLabelSelectorRulesOut,
		},
		{
			name: "passthrough Deployment, the component's own value", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, deployment("tier: front\n", "tier: front\n"+key+": web\n"))}
			},
			built: func(t *testing.T, objs []client.Object) {
				d := clObject(t, objs, "Deployment", "web")
				for _, path := range [][]string{{"metadata"}, {"spec", "template", "metadata"}} {
					if got, _ := clLabelAt(t, d, key, path...); got != "web" {
						t.Errorf("%s label = %q, want web", strings.Join(path, "."), got)
					}
				}
			},
		},
		{
			name: "passthrough Pod, its own labels", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\n  labels:\n    "+foreign+"spec:\n"+htIndent(htPlainPod, "  "))}
			},
			refused: refusedAt(`Pod "web"`, "metadata.labels"),
		},
		{
			name: "passthrough Cluster, inheritedMetadata", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, "apiVersion: postgresql.cnpg.io/v1\nkind: Cluster\nmetadata:\n  name: web\nspec:\n  instances: 1\n  inheritedMetadata:\n    labels:\n      "+foreign)}
			},
			refused: refusedAt(`Cluster "web"`, "spec.inheritedMetadata.labels"),
		},
		{
			name: "passthrough Alertmanager, podMetadata", typ: "passthrough", handler: &components.PassthroughHandler{},
			props: func(t *testing.T) map[string]any {
				return map[string]any{"object": ptObject(t, "apiVersion: monitoring.coreos.com/v1\nkind: Alertmanager\nmetadata:\n  name: web\nspec:\n  podMetadata:\n    labels:\n      "+foreign)}
			},
			refused: refusedAt(`Alertmanager "web"`, "spec.podMetadata.labels"),
		},
		{
			name: "replicaset kind", typ: "replicaset", handler: &components.ReplicaSetHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "selector:\n  matchLabels:\n    tier: front\n"+template("tier: front\n"+foreign))
			},
			refused: refusedAt(`ReplicaSet "web"`, "spec.template.metadata.labels"),
		},
		{
			name: "replicationcontroller kind", typ: "replicationcontroller", handler: &components.ReplicationControllerHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, template("tier: front\n"+foreign))
			},
			refused: refusedAt(`ReplicationController "web"`, "spec.template.metadata.labels"),
		},
		{
			name: "podtemplate kind", typ: "podtemplate", handler: &components.PodTemplateHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, template(foreign))
			},
			refused: refusedAt(`PodTemplate "web"`, "template.metadata.labels"),
		},
		{
			// The kinds' own property refuses the value in the transform, before
			// there is an object to name.
			name: "pod kind, labels property", typ: "pod", handler: &components.PodHandler{}, kind: oam.ComponentLabelInLabelsProperty,
			props: func(t *testing.T) map[string]any {
				props := ptObject(t, htPlainPod)
				props["labels"] = map[string]any{key: "db"}
				return props
			},
			refused: []string{`labels["` + key + `"]: "db" is not the component label of component "web" ("web")`},
		},
		{
			name: "cnpg-cluster kind, inheritedMetadata", typ: "cnpg-cluster", handler: &components.CnpgClusterHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "instances: 1\nstorage:\n  size: 1Gi\ninheritedMetadata:\n  labels:\n    "+foreign)
			},
			refused: refusedAt(`Cluster "web"`, "spec.inheritedMetadata.labels"),
		},
		{
			// The wrapper reads what an operator hands on and writes nothing there.
			name: "cnpg-cluster kind, nothing authored", typ: "cnpg-cluster", handler: &components.CnpgClusterHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "instances: 1\nstorage:\n  size: 1Gi\n")
			},
			built: func(t *testing.T, objs []client.Object) {
				c := clObject(t, objs, "Cluster", "web")
				if got, _ := clLabelAt(t, c, key, "metadata"); got != "web" {
					t.Errorf("Cluster label = %q, want web", got)
				}
				if got, held := clLabelAt(t, c, key, "spec", "inheritedMetadata"); held {
					t.Errorf("spec.inheritedMetadata label = %q, want nothing written there", got)
				}
			},
		},
		{
			name: "cnpg-pooler kind, pod template", typ: "cnpg-pooler", handler: &components.CnpgPoolerHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "cluster:\n  name: db\npgbouncer: {}\ntemplate:\n  metadata:\n    labels:\n      "+foreign+"  spec:\n    containers: []\n")
			},
			refused: refusedAt(`Pooler "web"`, "spec.template.metadata.labels"),
		},
		{
			name: "replicationsource kind, a mover's pod labels", typ: "replicationsource", handler: &components.ReplicationSourceHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "sourcePVC: data\ntrigger:\n  schedule: \"0 3 * * *\"\nrestic:\n  repository: restic-repo\n  copyMethod: Snapshot\n  moverPodLabels:\n    "+foreign)
			},
			refused: refusedAt(`ReplicationSource "web"`, "spec.restic.moverPodLabels"),
		},
		{
			name: "replicationdestination kind, a mover's pod labels", typ: "replicationdestination", handler: &components.ReplicationDestinationHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "trigger:\n  manual: restore-1\nrestic:\n  repository: restic-repo\n  copyMethod: Direct\n  destinationPVC: data\n  moverPodLabels:\n    "+foreign)
			},
			refused: refusedAt(`ReplicationDestination "web"`, "spec.restic.moverPodLabels"),
		},
		{
			name: "issuer kind, the pod template of a later solver", typ: "issuer", handler: &components.IssuerHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "acme:\n  server: https://acme-v02.api.letsencrypt.org/directory\n  privateKeySecretRef:\n    name: account\n  solvers:\n"+
					"  - http01:\n      ingress:\n        ingressClassName: nginx\n"+
					"  - http01:\n      gatewayHTTPRoute:\n        parentRefs:\n        - name: public\n        podTemplate:\n          metadata:\n            labels:\n              "+foreign)
			},
			refused: refusedAt(`Issuer "web"`, "spec.acme.solvers[1].http01.gatewayHTTPRoute.podTemplate.metadata.labels"),
		},
		{
			name: "clusterissuer kind, a solver's pod template", typ: "clusterissuer", handler: &components.ClusterIssuerHandler{},
			props: func(t *testing.T) map[string]any {
				return ptObject(t, "acme:\n  server: https://acme-v02.api.letsencrypt.org/directory\n  privateKeySecretRef:\n    name: account\n  solvers:\n"+
					"  - http01:\n      ingress:\n        ingressClassName: nginx\n        podTemplate:\n          metadata:\n            labels:\n              "+foreign)
			},
			refused: refusedAt(`ClusterIssuer "web"`, "spec.acme.solvers[0].http01.ingress.podTemplate.metadata.labels"),
		},
		{
			name: "gateway kind, infrastructure", typ: "gateway", handler: &components.GatewayHandler{},
			props: func(*testing.T) map[string]any {
				return withProperty(gatewayMinimal(), "infrastructure", map[string]any{"labels": map[string]any{key: "db"}})
			},
			refused: refusedAt(`Gateway "web"`, "spec.infrastructure.labels"),
		},
		{
			name: "helmtemplate, a chart's Deployment", typ: "helmtemplate", handler: &components.HelmTemplateHandler{},
			props: func(t *testing.T) map[string]any {
				srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{
					"deployment.yaml": strings.Replace(deployment("tier: front\n", "tier: front\n"+foreign), "name: web", "name: charted", 1),
				})
				return map[string]any{"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": srvURL}}
			},
			refused: refusedAt(`Deployment "charted"`, "spec.template.metadata.labels"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := pvTransform(tc.typ, tc.handler, tc.props(t), nil)
			if tc.refused == nil {
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				tc.built(t, objs)
				return
			}
			if err == nil {
				t.Fatal("the document built with another component's value for the component label")
			}
			kind := tc.kind
			if kind == "" {
				kind = oam.ComponentLabelForeignValue
			}
			got := clRefused(t, err, kind)
			if got.Component != "web" || got.Key != key || got.Want != "web" {
				t.Errorf("refusal = %+v, want component web, the key %s and the value web it should hold", *got, key)
			}
			if kind == oam.ComponentLabelInLabelsProperty {
				want := oam.ComponentLabelError{Refused: kind, Component: "web", Path: "labels", Key: key, Value: "db", Want: "web"}
				if !reflect.DeepEqual(*got, want) {
					t.Errorf("refusal = %+v\nwant      %+v", *got, want)
				}
			}
			for _, want := range tc.refused {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %s", err, want)
				}
			}
		})
	}
}

// clRefused checks err is the component label's refusal of the kind refused: it
// answers to the sentinel and holds the typed error, through whatever the build
// wrapped it in.
func clRefused(t *testing.T, err error, refused oam.ComponentLabelRefusal) *oam.ComponentLabelError {
	t.Helper()
	var got *oam.ComponentLabelError
	if !errors.Is(err, oam.ErrComponentLabelValue) || !errors.As(err, &got) {
		t.Fatalf("the refusal %v is no *ComponentLabelError answering to ErrComponentLabelValue", err)
	}
	if got.Refused != refused {
		t.Errorf("the refusal %v is of the kind %q, want %q", err, got.Refused, refused)
	}
	return got
}

// clRenameRule lowers a component to a deployment and a service entry, each
// under a name of its own: the authored name plus a suffix.
type clRenameRule struct{}

func (clRenameRule) ComponentType() string { return "renamed-pair" }

func (clRenameRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	name := comp.Name + "-renamed"
	return oam.LoweringResult{Components: []oam.Component{
		{Name: name, Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/app:v1"}},
		{Name: name + "-svc", Type: "service", Properties: map[string]any{
			"selector": map[string]any{"app": name},
			"ports":    []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}},
		}},
	}}, nil
}

// TestComponentLabel_UnderTheAppKey: with ComponentLabelKey "app" the label is
// the one the kinds write themselves, valued with the name of the entry they
// generate for. An entry a rule emitted under a name of its own carries that
// value on what launcher generates, and the build accepts its own output. A
// value that is neither the owner's nor that entry's is refused as under any
// other key: a rendered chart's, and an authored one naming another component.
func TestComponentLabel_UnderTheAppKey(t *testing.T) {
	ctx := oam.TransformContext{ComponentLabelKey: "app"}
	podSpec := "    spec:\n" + htIndent(htPlainPod, "      ")
	foreignDeployment := func(name string) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\nspec:\n  selector:\n    matchLabels:\n      tier: front\n" +
			"  template:\n    metadata:\n      labels:\n        tier: front\n        app: db\n" + podSpec
	}

	t.Run("an entry a rule renamed", func(t *testing.T) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{
			"deployment": &components.DeploymentHandler{},
			"service":    &components.ServiceHandler{},
		}, nil)
		tr.RegisterComponentLowering(clRenameRule{})
		objs, err := clGenerate(tr, mfApp("renamed-pair", map[string]any{}), ctx)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		d := clObject(t, objs, "Deployment", "web-renamed")
		for _, path := range [][]string{{"metadata"}, {"spec", "template", "metadata"}} {
			if got, _ := clLabelAt(t, d, "app", path...); got != "web-renamed" {
				t.Errorf("Deployment %s label app = %q, want the entry's web-renamed", strings.Join(path, "."), got)
			}
		}
		if got, _ := clLabelAt(t, clObject(t, objs, "Service", "web-renamed-svc"), "app", "metadata"); got != "web-renamed-svc" {
			t.Errorf("Service label app = %q, want the entry's web-renamed-svc", got)
		}
	})

	// Those pods carry the entry's value, so the policy synthesized for the entry
	// selects them, and the owner's value as well (go-kure/launcher#790).
	t.Run("the policy of an entry a rule renamed", func(t *testing.T) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{
			"deployment": &components.DeploymentHandler{},
			"service":    &components.ServiceHandler{},
		}, nil)
		tr.RegisterComponentLowering(clRenameRule{})
		egress := ctx
		egress.EgressPeers = map[string][]netpol.EgressPeer{"web-renamed": {{
			Namespace:   "data",
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "pg"}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
		}}}
		objs, err := clGenerate(tr, mfApp("renamed-pair", map[string]any{}), egress)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(clObject(t, objs, "NetworkPolicy", "web-renamed-allow-egress-traffic"))
		if err != nil {
			t.Fatalf("to unstructured: %v", err)
		}
		var np networkingv1.NetworkPolicy
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(content, &np); err != nil {
			t.Fatalf("decode the NetworkPolicy: %v", err)
		}
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
		if err != nil {
			t.Fatalf("podSelector %+v: %v", np.Spec.PodSelector, err)
		}
		d, err := runtime.DefaultUnstructuredConverter.ToUnstructured(clObject(t, objs, "Deployment", "web-renamed"))
		if err != nil {
			t.Fatalf("to unstructured: %v", err)
		}
		pod, _, err := unstructured.NestedStringMap(d, "spec", "template", "metadata", "labels")
		if err != nil {
			t.Fatalf("pod template labels: %v", err)
		}
		for _, tc := range []struct {
			labels labels.Set
			want   bool
		}{{pod, true}, {labels.Set{"app": "web"}, true}, {labels.Set{"app": "web-renamed-svc"}, false}} {
			if got := sel.Matches(tc.labels); got != tc.want {
				t.Errorf("podSelector %s matches %v = %t, want %t", sel, tc.labels, got, tc.want)
			}
		}
	})

	// The entry's value passes because it is no other component's. Here it would
	// be: "web" is lowered to an entry named as the second component, which is
	// itself lowered to entries under other names, so no name collides.
	t.Run("an entry a rule named as another component", func(t *testing.T) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{
			"deployment": &components.DeploymentHandler{},
			"service":    &components.ServiceHandler{},
		}, nil)
		tr.RegisterComponentLowering(clRenameRule{})
		app := mfApp("renamed-pair", map[string]any{})
		app.Spec.Components = append(app.Spec.Components, oam.Component{Name: "web-renamed", Type: "renamed-pair", Properties: map[string]any{}})
		_, err := clGenerate(tr, app, ctx)
		want := `component "web": its lowering emitted "web-renamed", whose ` + "`app`" + ` label value "web-renamed" is the component label of component "web-renamed"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build = %v, want a refusal saying %s", err, want)
		}
		if got := clRefused(t, err, oam.ComponentLabelOfAnotherComponent); got.Component != "web" || got.Entry != "web-renamed" || got.Other != "web-renamed" {
			t.Errorf("refusal = %+v, want component web, entry and other component web-renamed", *got)
		}
	})

	t.Run("a chart's Deployment", func(t *testing.T) {
		srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"deployment.yaml": foreignDeployment("charted")})
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}}, nil)
		_, err := clGenerate(tr, mfApp("helmtemplate", map[string]any{"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": srvURL}}), ctx)
		want := `Deployment "charted": spec.template.metadata.labels["app"]: "db" is not the component label of component "web" ("web")`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build = %v, want a refusal saying %s", err, want)
		}
		if got := clRefused(t, err, oam.ComponentLabelForeignValue); got.Name != "charted" || got.Path != "spec.template.metadata.labels" || got.Key != "app" || got.Value != "db" {
			t.Errorf("refusal = %+v, want the Deployment charted, its pod template's labels, app and db", *got)
		}
	})

	t.Run("an authored value that names another component", func(t *testing.T) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{
			"passthrough": &components.PassthroughHandler{},
			"deployment":  &components.DeploymentHandler{},
		}, nil)
		app := mfApp("passthrough", map[string]any{"object": ptObject(t, foreignDeployment("web"))})
		app.Spec.Components = append(app.Spec.Components, oam.Component{Name: "db", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/db:v1"}})
		_, err := clGenerate(tr, app, ctx)
		want := `Deployment "web": spec.template.metadata.labels["app"]: "db" is not the component label of component "web" ("web")`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build = %v, want a refusal saying %s", err, want)
		}
	})

	// The kinds' own `app` check refuses the property first; under this key its
	// refusal is the component label's.
	t.Run("the labels property of a kind", func(t *testing.T) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"pod": &components.PodHandler{}}, nil)
		props := ptObject(t, htPlainPod)
		props["labels"] = map[string]any{"app": "db"}
		_, err := clGenerate(tr, mfApp("pod", props), ctx)
		want := `labels["app"]: "db" is not the component label of component "web" ("web")`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build = %v, want a refusal saying %s", err, want)
		}
		if got := clRefused(t, err, oam.ComponentLabelInLabelsProperty); got.Component != "web" || got.Path != "labels" || got.Key != "app" || got.Value != "db" || got.Want != "web" {
			t.Errorf("refusal = %+v, want component web, the labels property, app, db and web", *got)
		}
	})
}
