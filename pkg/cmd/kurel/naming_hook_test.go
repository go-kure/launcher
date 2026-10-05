package kurel

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// These tests pin the consumer naming hook (go-kure/launcher#787) through
// kurel's own transformer: TransformContext.Naming is asked once for each name
// the author did not set, its answer is used as returned or refused, and two
// names that end up naming one object fail the transform.

// namingApp is a document with a name of every role: two ordered groups, and on
// component web the scaler, rbac, networkpolicy and configmap traits. extraWeb
// is appended to web's traits and extraComponents to the components.
func namingApp(extraWeb, extraComponents string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: agent
      type: daemonset
      properties:
        image: ghcr.io/example/agent:v1.0.0
    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: scaler
          properties:
            minReplicas: 2
            maxReplicas: 4
            enablePDB: true
        - type: rbac
          properties:
            rules:
              - apiGroups: [""]
                resources: [pods]
                verbs: [get]
        - type: networkpolicy
          properties:
            ingress: []
        - type: configmap
          properties:
            name: web-config
            data:
              a: "1"
` + extraWeb + extraComponents + `
  policies:
    - name: agent-first
      type: placement
      properties:
        component: agent
        tier: infra
    - name: web-last
      type: placement
      properties:
        component: web
        tier: apps
`
}

// namingContext is a transform context whose egress peers make the synthesis
// generate a NetworkPolicy for web, and whose Naming hook is hook.
func namingContext(hook func(oam.NameRequest) (string, bool)) oam.TransformContext {
	return oam.TransformContext{
		Naming: hook,
		EgressPeers: map[string][]netpol.EgressPeer{"web": {{
			Namespace:   "data",
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
		}}},
	}
}

// namingTransform transforms appYAML under ctx and generates the result.
func namingTransform(t *testing.T, appYAML string, ctx oam.TransformContext) (*stack.Cluster, []oam.GeneratedApplication) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	ctx.Domain = kurelDomain
	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := oam.CheckInDocumentCollisions(apps); err != nil {
		t.Fatalf("collision check: %v", err)
	}
	return cluster, apps
}

// generatedNames lists every bundle of cluster and every generated object as
// "application: Kind namespace/name", in order.
func generatedNames(cluster *stack.Cluster, apps []oam.GeneratedApplication) []string {
	var out []string
	var walk func(b *stack.Bundle)
	walk = func(b *stack.Bundle) {
		out = append(out, "bundle "+b.Name)
		for _, child := range b.Children {
			walk(child)
		}
	}
	walk(cluster.Node.Bundle)
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			obj := *p
			out = append(out, fmt.Sprintf("%s: %s %s/%s", a.Name, obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName()))
		}
	}
	return out
}

func declineEveryName(requests *[]oam.NameRequest) func(oam.NameRequest) (string, bool) {
	return func(req oam.NameRequest) (string, bool) {
		*requests = append(*requests, req)
		return "", false
	}
}

// namingDB is a postgresql component with a pooler and a database: the names a
// lowering rule resolves.
const namingDB = `    - name: db
      type: postgresql
      properties:
        pooler:
          enabled: true
        databases:
          - name: orders
            owner: app
`

const (
	poolerKindName   = "Pooler.postgresql.cnpg.io"
	databaseKindName = "Database.postgresql.cnpg.io"
)

func TestNamingHook_AskedOncePerNameOfEveryRole(t *testing.T) {
	var requests []oam.NameRequest
	jobs := hookComponent("jobs", "helmtemplate", serveHookChart(t), "")
	namingTransform(t, namingApp("", namingDB+namingChart+jobs), namingContext(declineEveryName(&requests)))

	const (
		np      = "NetworkPolicy.networking.k8s.io"
		rbac    = ".rbac.authorization.k8s.io"
		subApp  = oam.NameRoleSubApplication
		synthNP = "web-allow-egress-traffic"
	)
	// Each name once, in the order the transform reaches it: the names the
	// lowering rules make, the object of each authored kind component, the
	// hook-group prefix of the helmtemplate component, the bundles, each trait's
	// objects and then its sub-application, the synthesized policy last. agent
	// is the one authored kind component: the
	// members the webservice, postgresql and helm rules emit are named by their
	// rule, and the hook is not asked for them. The helm rule's generated source
	// is the document's, so its request carries no component.
	want := []oam.NameRequest{
		{Application: "shop", Component: "db", Role: oam.NameRolePooler, Kind: poolerKindName, Default: "db-pooler"},
		{Application: "shop", Component: "db", Role: oam.NameRoleDatabase, Kind: databaseKindName, Default: "db-orders"},
		{Application: "shop", Component: "chart", Role: oam.NameRoleValuesSecret, Kind: "Secret", Default: chartSecretDefault},
		{Application: "shop", Component: "chart", Role: oam.NameRoleValuesConfigMap, Kind: "ConfigMap", Default: chartConfigMapDefault},
		{Application: "shop", Role: oam.NameRoleHelmSource, Kind: "HelmRepository.source.toolkit.fluxcd.io", Default: chartSourceDefault},
		{Application: "shop", Component: "agent", Role: oam.NameRoleObject, Kind: "DaemonSet.apps", Default: "agent"},
		// The prefix of jobs' hook-group layouts: no object, so no kind.
		{Application: "shop", Component: "jobs", Role: oam.NameRoleHookGroup, Default: "shop-jobs"},
		{Application: "shop", Role: oam.NameRoleBundle, Default: "shop"},
		// db, chart and jobs are placed in no tier and share the first group with agent,
		// so that group is numbered: a group carries a tier's name only when it is
		// that tier and nothing else.
		{Application: "shop", Role: oam.NameRoleGroup, Default: "shop-00"},
		// The sub-application of each trait the helm rule added, named as its object.
		{Application: "shop", Component: "chart", Role: subApp, Default: chartConfigMapDefault},
		{Application: "shop", Component: "chart", Role: subApp, Default: chartSecretDefault},
		{Application: "shop", Role: oam.NameRoleGroup, Default: "shop-apps"},
		{Application: "shop", Component: "web", Role: oam.NameRoleHPA, Kind: "HorizontalPodAutoscaler.autoscaling", Default: "web-hpa"},
		{Application: "shop", Component: "web", Role: oam.NameRolePDB, Kind: "PodDisruptionBudget.policy", Default: "web-pdb"},
		{Application: "shop", Component: "web", Role: subApp, Default: "web-scaler"},
		{Application: "shop", Component: "web", Role: oam.NameRoleRBAC, Kind: "Role" + rbac, Default: "web"},
		{Application: "shop", Component: "web", Role: oam.NameRoleRBAC, Kind: "RoleBinding" + rbac, Default: "web"},
		{Application: "shop", Component: "web", Role: subApp, Default: "web-rbac"},
		{Application: "shop", Component: "web", Role: oam.NameRoleNetworkPolicy, Kind: np, Default: "web-allow"},
		{Application: "shop", Component: "web", Role: subApp, Default: "web-networkpolicy"},
		{Application: "shop", Component: "web", Role: subApp, Default: "web-config"},
		{Application: "shop", Component: "web", Role: oam.NameRoleNetpolSynth, Kind: np, Default: synthNP},
		{Application: "shop", Component: "web", Role: subApp, Default: synthNP},
	}
	if !slices.Equal(requests, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", requests, want)
	}

	// The document has a name of every role, so the list above leaves none out.
	roles := map[oam.NameRole]bool{}
	for _, req := range want {
		roles[req.Role] = true
	}
	for _, role := range oam.NameRoles() {
		if !roles[role] {
			t.Errorf("no request of role %q: the document must carry a name of every role", role)
		}
	}
}

// renameBy answers with names[Role+" "+Default] where the map has one.
func renameBy(names map[string]string) func(oam.NameRequest) (string, bool) {
	return func(req oam.NameRequest) (string, bool) {
		name, ok := names[string(req.Role)+" "+req.Default]
		return name, ok
	}
}

func TestNamingHook_AnswerIsUsed(t *testing.T) {
	cluster, apps := namingTransform(t, namingApp("", ""), namingContext(renameBy(map[string]string{
		"bundle shop":                           "site",
		"group shop-apps":                       "site-workloads",
		"hpa web-hpa":                           "web-autoscaler",
		"rbac web":                              "web-reader",
		"networkpolicy web-allow":               "web-ingress",
		"netpol-synth web-allow-egress-traffic": "web-egress",
		// A sub-application's name is not its object's: renaming one leaves the
		// other alone.
		"sub-application web-config": "web-settings",
	})))
	got := generatedNames(cluster, apps)
	want := []string{
		"bundle site",
		"bundle shop-infra",
		"bundle site-workloads",
		"web-scaler: HorizontalPodAutoscaler default/web-autoscaler",
		"web-scaler: PodDisruptionBudget default/web-pdb",
		"web-rbac: Role default/web-reader",
		"web-rbac: RoleBinding default/web-reader",
		"web-networkpolicy: NetworkPolicy default/web-ingress",
		"web-settings: ConfigMap default/web-config",
		"web-allow-egress-traffic: NetworkPolicy default/web-egress",
	}
	for _, line := range want {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
}

func TestNamingHook_NotAskedForANameTheAuthorSet(t *testing.T) {
	doc := strings.Replace(namingApp("", ""), "            enablePDB: true\n", "            enablePDB: true\n            hpaName: mine\n", 1)
	var requests []oam.NameRequest
	cluster, apps := namingTransform(t, doc, namingContext(declineEveryName(&requests)))
	for _, req := range requests {
		if req.Role == oam.NameRoleHPA {
			t.Errorf("the hook was asked for the name hpaName sets: %+v", req)
		}
	}
	if !slices.ContainsFunc(requests, func(req oam.NameRequest) bool { return req.Role == oam.NameRolePDB }) {
		t.Error("the hook was not asked for the PodDisruptionBudget, whose name the author left out")
	}
	if got := generatedNames(cluster, apps); !slices.Contains(got, "web-scaler: HorizontalPodAutoscaler default/mine") {
		t.Errorf("the authored hpaName was not used:\n  %s", strings.Join(got, "\n  "))
	}
}

func TestNamingHook_Refusals(t *testing.T) {
	const api = `    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 8080
      traits:
        - type: scaler
          properties:
            minReplicas: 2
            maxReplicas: 4
`
	for _, tc := range []struct {
		name  string
		names map[string]string
		want  string
	}{
		{
			name:  "an answer that is no subdomain",
			names: map[string]string{"hpa web-hpa": "Web_HPA"},
			want:  `component "web" trait "scaler": the Naming hook returned "Web_HPA" for role "hpa" in place of "web-hpa": not a valid DNS-1123 subdomain: `,
		},
		{
			name:  "an empty answer",
			names: map[string]string{"bundle shop": ""},
			want:  `bundle name: the Naming hook returned "" for role "bundle" in place of "shop": it is empty; return a valid name, or false to keep the default`,
		},
		{
			name:  "an answer over 253 characters",
			names: map[string]string{"sub-application web-config": strings.Repeat("a", 254)},
			want:  `for role "sub-application" in place of "web-config": not a valid DNS-1123 subdomain: must be no more than 253`,
		},
		{
			name:  "one object named twice",
			names: map[string]string{"hpa web-hpa": "shared", "hpa api-hpa": "shared"},
			want: `name collision: HorizontalPodAutoscaler.autoscaling "default/shared" is named by ` +
				`component "api" traits[0] "scaler" (role "hpa", returned by the Naming hook in place of "api-hpa") and by ` +
				`component "web" traits[0] "scaler" (role "hpa", returned by the Naming hook in place of "web-hpa"); give one of them another name`,
		},
		{
			name:  "a group named as the bundle",
			names: map[string]string{"group shop-apps": "shop"},
			want: `group name: name collision: bundle "shop" is named by the application (role "bundle", its default) and by ` +
				`the application (role "group", returned by the Naming hook in place of "shop-apps"); give one of them another name`,
		},
		{
			name:  "a synthesized policy named as the trait's",
			names: map[string]string{"netpol-synth web-allow-egress-traffic": "web-allow"},
			want: `synthesized NetworkPolicy "web-allow-egress-traffic": name collision: NetworkPolicy.networking.k8s.io "default/web-allow" is named by ` +
				`component "web" traits[2] "networkpolicy" (role "networkpolicy", its default) and by ` +
				`component "web" (role "netpol-synth", returned by the Naming hook in place of "web-allow-egress-traffic"); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := transformErr(t, namingApp("", api), namingContext(renameBy(tc.names)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// Two traits of one type on one component name the same objects. Transform
// refuses them itself, naming both, where it used to leave them to
// CheckInDocumentCollisions (go-kure/launcher#757).
func TestTwoTraitsOfOneType_RefusedByTransform(t *testing.T) {
	const rbacGroup = ".rbac.authorization.k8s.io"
	for _, tc := range []struct {
		name, trait, want string
	}{
		{
			name: "scaler", trait: scalerTrait,
			want: `component "web" trait "scaler": name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named by ` +
				`component "web" traits[0] "scaler" (role "hpa", its default) and by component "web" traits[1] "scaler" (role "hpa", its default); give one of them another name`,
		},
		{
			name: "rbac", trait: rbacTrait,
			want: `component "web" trait "rbac": rbac: name collision: Role` + rbacGroup + ` "default/web" is named by ` +
				`component "web" traits[0] "rbac" (role "rbac", its default) and by component "web" traits[1] "rbac" (role "rbac", its default); give one of them another name`,
		},
		{
			name: "networkpolicy", trait: networkPolicyTrait,
			want: `component "web" trait "networkpolicy": name collision: NetworkPolicy.networking.k8s.io "default/web-allow" is named by ` +
				`component "web" traits[0] "networkpolicy" (role "networkpolicy", its default) and by component "web" traits[1] "networkpolicy" (role "networkpolicy", its default); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := duplicateApp(`    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
`+tc.trait+tc.trait, "", "")
			err := transformErr(t, doc, oam.TransformContext{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// postgresqlComponent parses appYAML and returns its authored postgresql
// component named db, as a consumer holds it when it asks for endpoints.
func postgresqlComponent(t *testing.T, appYAML string) (*oam.Transformer, *oam.Component) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	for i := range app.Spec.Components {
		if app.Spec.Components[i].Name == "db" {
			return transformer, &app.Spec.Components[i]
		}
	}
	t.Fatal("the document has no component db")
	return nil, nil
}

// poolerSelector returns the Pooler name the component's pooler endpoint selects.
func poolerSelector(t *testing.T, eps []netpol.Endpoint, err error) string {
	t.Helper()
	if err != nil || len(eps) != 2 {
		t.Fatalf("endpoints = %v, %v; want the cluster's and the pooler's", eps, err)
	}
	return eps[1].PodSelector.MatchLabels["cnpg.io/poolerName"]
}

// The hook names the Pooler and the Database a postgresql component generates,
// and ComponentEndpointsNamed asks it the request the transform asked, so the
// pooler endpoint selects the Pooler under the name it got. ComponentEndpoints
// asks no hook and selects the default.
func TestNamingHook_PostgresqlNames(t *testing.T) {
	doc := namingApp("", namingDB)
	var asked []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		if req.Role == oam.NameRolePooler {
			asked = append(asked, req)
		}
		return renameBy(map[string]string{"pooler db-pooler": "edge", "database db-orders": "orders.v2"})(req)
	}

	cluster, apps := namingTransform(t, doc, namingContext(hook))
	got := generatedNames(cluster, apps)
	for _, line := range []string{"edge: Pooler default/edge", "orders.v2: Database default/orders.v2"} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}

	transformer, comp := postgresqlComponent(t, doc)
	eps, err := transformer.ComponentEndpointsNamed("shop", comp, hook)
	if selected := poolerSelector(t, eps, err); selected != "edge" {
		t.Errorf("ComponentEndpointsNamed selects cnpg.io/poolerName=%q, want the name the Pooler got", selected)
	}
	want := oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRolePooler, Kind: poolerKindName, Default: "db-pooler"}
	if !slices.Equal(asked, []oam.NameRequest{want, want}) {
		t.Errorf("the hook was asked for the Pooler\n  %+v\nwant the transform's request, and the same again for the endpoint:\n  %+v", asked, want)
	}

	eps, err = transformer.ComponentEndpoints(comp)
	if selected := poolerSelector(t, eps, err); selected != "db-pooler" {
		t.Errorf("ComponentEndpoints selects cnpg.io/poolerName=%q, want the default: it asks no hook", selected)
	}
}

// An authored poolerName names the Pooler for every reader: the hook is not
// asked, by the transform or for the endpoint.
func TestNamingHook_NotAskedForAnAuthoredPoolerName(t *testing.T) {
	doc := namingApp("", strings.Replace(namingDB, "        pooler:\n", "        poolerName: mine\n        pooler:\n", 1))
	var requests []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		requests = append(requests, req)
		return "theirs", req.Role == oam.NameRolePooler
	}

	cluster, apps := namingTransform(t, doc, namingContext(hook))
	if got := generatedNames(cluster, apps); !slices.Contains(got, "mine: Pooler default/mine") {
		t.Errorf("the authored poolerName was not used:\n  %s", strings.Join(got, "\n  "))
	}
	transformer, comp := postgresqlComponent(t, doc)
	eps, err := transformer.ComponentEndpointsNamed("shop", comp, hook)
	if selected := poolerSelector(t, eps, err); selected != "mine" {
		t.Errorf("ComponentEndpointsNamed selects cnpg.io/poolerName=%q, want the authored name", selected)
	}
	eps, err = transformer.ComponentEndpoints(comp)
	if selected := poolerSelector(t, eps, err); selected != "mine" {
		t.Errorf("ComponentEndpoints selects cnpg.io/poolerName=%q, want the authored name", selected)
	}
	for _, req := range requests {
		if req.Role == oam.NameRolePooler {
			t.Errorf("the hook was asked for the name poolerName sets: %+v", req)
		}
	}
}

func TestNamingHook_PostgresqlRefusals(t *testing.T) {
	const twoDatabases = `          - name: billing
            owner: app
`
	for _, tc := range []struct {
		name  string
		names map[string]string
		want  string
		// endpoint is set when ComponentEndpointsNamed refuses the answer too, in
		// the same words.
		endpoint bool
	}{
		{
			name:     "a pooler answer that is no DNS-1035 label",
			names:    map[string]string{"pooler db-pooler": "edge.v2"},
			want:     `pooler: the Naming hook returned "edge.v2" for role "pooler" in place of "db-pooler": not a valid DNS-1035 label: `,
			endpoint: true,
		},
		{
			name:  "a database answer that is no subdomain",
			names: map[string]string{"database db-orders": "Orders"},
			want:  `databases[0] "orders": the Naming hook returned "Orders" for role "database" in place of "db-orders": not a valid DNS-1123 subdomain: `,
		},
		{
			name:  "two databases named alike",
			names: map[string]string{"database db-orders": "shared", "database db-billing": "shared"},
			want: `databases[1] "billing": name collision: Database.postgresql.cnpg.io "shared" is named by ` +
				`component "db" (role "database", returned by the Naming hook in place of "db-orders") and by ` +
				`component "db" (role "database", returned by the Naming hook in place of "db-billing"); give one of them another name`,
		},
		{
			name:  "the pooler named as another component",
			names: map[string]string{"pooler db-pooler": "web"},
			want:  `pooler: generates component "web", which is already the name of component "web" (type "webservice") in the document; rename one of them`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := namingApp("", namingDB+twoDatabases)
			hook := renameBy(tc.names)
			err := transformErr(t, doc, namingContext(hook))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
			if !tc.endpoint {
				return
			}
			transformer, comp := postgresqlComponent(t, doc)
			_, err = transformer.ComponentEndpointsNamed("shop", comp, hook)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ComponentEndpointsNamed: err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// TestNamingHook_KindNameRuleNamesBothSources: a kind that holds its object's
// name to a rule of its own (a Service, a Namespace, the length of a CronJob's
// or a Job's) is handed the resolved name only, so its refusal names both places
// that name can come from. A hook's answer is not reported as the author's
// `objectName`.
func TestNamingHook_KindNameRuleNamesBothSources(t *testing.T) {
	const source = oam.ObjectNameProperty + ` (or the Naming hook's answer for role "object"): `
	app := func(name, typ, props string) string {
		return fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: %s
      type: %s
      properties:
%s`, name, typ, props)
	}
	const servicePorts = "        selector:\n          app: api\n        ports:\n          - port: 80\n"
	over52, over63 := strings.Repeat("a", 53), strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, component, typ, props, bad, want string
	}{
		{"service", "api", "service", servicePorts, "1api", `"1api" is not a valid Service name`},
		{"namespace", "tenant", "namespace", "        finalizers: []\n", "tenant.a", `"tenant.a" is not a valid Namespace name`},
		{"cronjob", "nightly", "cronjob", "        image: busybox:1\n        schedule: \"0 2 * * *\"\n", over52,
			`"` + over52 + `" is not a valid CronJob name, which must be at most 52 characters`},
		{"job", "migrate", "job", "        image: busybox:1\n", over63,
			`"` + over63 + `" is not a valid Job name, which must be at most 63 characters`},
		{"indexed job", "migrate", "job", "        image: busybox:1\n        completionMode: Indexed\n        completions: 2\n", "migrate.v2",
			`"migrate.v2" is not a valid name for this Job: with completionMode Indexed and completions 2`},
	} {
		t.Run(tc.name+" authored", func(t *testing.T) {
			authored := app(tc.component, tc.typ, tc.props+"        "+oam.ObjectNameProperty+": "+tc.bad+"\n")
			err := transformErr(t, authored, namingContext(nil))
			if err == nil || !strings.Contains(err.Error(), source+tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, source+tc.want)
			}
		})
		t.Run(tc.name+" from the hook", func(t *testing.T) {
			hook := renameBy(map[string]string{"object " + tc.component: tc.bad})
			err := transformErr(t, app(tc.component, tc.typ, tc.props), namingContext(hook))
			if err == nil || !strings.Contains(err.Error(), source+tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, source+tc.want)
			}
		})
	}
}

func TestNamingHook_DecliningHookChangesNothing(t *testing.T) {
	cluster, apps := namingTransform(t, namingApp("", ""), namingContext(nil))
	without := generatedNames(cluster, apps)

	var requests []oam.NameRequest
	cluster, apps = namingTransform(t, namingApp("", ""), namingContext(declineEveryName(&requests)))
	with := generatedNames(cluster, apps)

	if len(requests) == 0 {
		t.Fatal("the hook was never asked")
	}
	if !slices.Equal(without, with) {
		t.Errorf("a hook that declines every name changed the output:\nwithout: %s\nwith:    %s",
			strings.Join(without, "\n         "), strings.Join(with, "\n         "))
	}
}
