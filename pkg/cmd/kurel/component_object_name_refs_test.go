package kurel

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// These tests pin, through `kurel build`, which references follow a kind
// component's `objectName` and which keep the component name. The party that
// writes a reference names its target: a reference launcher writes to the
// renamed object carries the object name; a name launcher derives from the
// component (a trait's object, a label, a selector) keeps the component's.

// buildWithProfile runs `kurel build` on appYAML with profile and returns the
// emitted objects.
func buildWithProfile(t *testing.T, appYAML, profile string) ([]map[string]any, error) {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", appYAML)
	profilePath := writeTempFile(t, dir, "cluster.yaml", profile)

	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, nil
}

// namedDoc returns the emitted object of kind and name, failing the test when
// there is none.
func namedDoc(t *testing.T, docs []map[string]any, kind, name string) map[string]any {
	t.Helper()
	var have []string
	for _, doc := range docs {
		if doc["kind"] != kind {
			continue
		}
		got, _, _ := unstructured.NestedString(doc, "metadata", "name")
		if got == name {
			return doc
		}
		have = append(have, got)
	}
	t.Fatalf("no %s named %q; the %s objects are %v", kind, name, kind, have)
	return nil
}

// kindNames returns the names of the emitted objects of kind.
func kindNames(docs []map[string]any, kind string) []string {
	var names []string
	for _, doc := range docs {
		if doc["kind"] != kind {
			continue
		}
		name, _, _ := unstructured.NestedString(doc, "metadata", "name")
		names = append(names, name)
	}
	return names
}

func wantNestedString(t *testing.T, doc map[string]any, want string, fields ...string) {
	t.Helper()
	got, found, err := unstructured.NestedString(doc, fields...)
	if err != nil || !found || got != want {
		t.Errorf("%s = %q (found %v, err %v), want %q", strings.Join(fields, "."), got, found, err, want)
	}
}

const objectNameReferencesYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: deployment
      properties:
        image: ghcr.io/example/app:v1.0.0
        objectName: api-renamed
        ports:
          - name: http
            containerPort: 8080
      traits:
        - type: scaler
          properties:
            minReplicas: 2
            maxReplicas: 5
            enablePDB: true
    - name: api-svc
      type: service
      properties:
        objectName: api-svc-renamed
        selector:
          app: api
        ports:
          - name: http
            port: 80
            targetPort: 8080
      traits:
        - type: httproute
          properties:
            parentRefs:
              - name: gw
            rules:
              - backendRefs:
                  - port: 80
    - name: runner
      type: serviceaccount
      properties:
        objectName: runner-renamed
      traits:
        - type: rbac
          properties:
            rules:
              - apiGroups: [""]
                resources: ["configmaps"]
                verbs: ["get"]
`

// TestObjectName_ReferencesLauncherWritesFollow: the three references a trait
// writes to its component's own object carry the object name, and the trait's
// own objects keep the names built from the component name.
func TestObjectName_ReferencesLauncherWritesFollow(t *testing.T) {
	docs, err := buildWithProfile(t, objectNameReferencesYAML, testClusterYAML)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}

	t.Run("scaler scales the renamed Deployment", func(t *testing.T) {
		namedDoc(t, docs, "Deployment", "api-renamed")
		hpa := namedDoc(t, docs, "HorizontalPodAutoscaler", "api-hpa")
		wantNestedString(t, hpa, "api-renamed", "spec", "scaleTargetRef", "name")
		pdb := namedDoc(t, docs, "PodDisruptionBudget", "api-pdb")
		wantNestedString(t, pdb, "api", "spec", "selector", "matchLabels", "app")
	})

	t.Run("a route's implicit backend is the renamed Service", func(t *testing.T) {
		namedDoc(t, docs, "Service", "api-svc-renamed")
		route := namedDoc(t, docs, "HTTPRoute", "api-svc-httproute")
		rules, _, _ := unstructured.NestedSlice(route, "spec", "rules")
		if len(rules) != 1 {
			t.Fatalf("HTTPRoute rules = %d, want 1", len(rules))
		}
		refs, _, _ := unstructured.NestedSlice(rules[0].(map[string]any), "backendRefs")
		if len(refs) != 1 {
			t.Fatalf("HTTPRoute backendRefs = %d, want 1", len(refs))
		}
		wantNestedString(t, refs[0].(map[string]any), "api-svc-renamed", "name")
	})

	t.Run("rbac grants to the renamed ServiceAccount", func(t *testing.T) {
		namedDoc(t, docs, "ServiceAccount", "runner-renamed")
		namedDoc(t, docs, "Role", "runner")
		binding := namedDoc(t, docs, "RoleBinding", "runner")
		wantNestedString(t, binding, "runner", "roleRef", "name")
		subjects, _, _ := unstructured.NestedSlice(binding, "subjects")
		if len(subjects) != 1 {
			t.Fatalf("RoleBinding subjects = %d, want 1", len(subjects))
		}
		wantNestedString(t, subjects[0].(map[string]any), "runner-renamed", "name")
	})
}

// TestObjectName_EndpointSelectsTheObject: the endpoint of a kind component
// that selects pods by its object's name (the two CloudNativePG kinds) selects
// by the name the transform gives the object, for both endpoint readers. The
// authored `objectName` is every reader's; the hook's answer is the transform's
// and ComponentEndpointsNamed's, and ComponentEndpoints, which asks no hook,
// selects the component name.
func TestObjectName_EndpointSelectsTheObject(t *testing.T) {
	kinds := []struct {
		typ, kind, label, properties string
	}{
		{"cnpg-cluster", "Cluster", "cnpg.io/cluster", "        storage:\n          size: 10Gi\n"},
		{"cnpg-pooler", "Pooler", "cnpg.io/poolerName", "        cluster:\n          name: main\n        pgbouncer: {}\n"},
	}
	variants := []struct {
		name, authored string
		hooked         bool
		// transform is the object's name, named and plain what
		// ComponentEndpointsNamed and ComponentEndpoints select.
		transform, named, plain string
	}{
		{name: "authored", authored: "mine", hooked: true, transform: "mine", named: "mine", plain: "mine"},
		{name: "hook", hooked: true, transform: "theirs", named: "theirs", plain: "db"},
		{name: "default", transform: "db", named: "db", plain: "db"},
	}
	for _, k := range kinds {
		for _, v := range variants {
			t.Run(k.typ+"/"+v.name, func(t *testing.T) {
				component := "    - name: db\n      type: " + k.typ + "\n      properties:\n" + k.properties
				if v.authored != "" {
					component += "        objectName: " + v.authored + "\n"
				}
				doc := namingApp("", component)
				var asked int
				hook := func(req oam.NameRequest) (string, bool) {
					if req.Role != oam.NameRoleObject || req.Component != "db" {
						return "", false
					}
					asked++
					return "theirs", v.hooked
				}

				cluster, apps := namingTransform(t, doc, namingContext(hook))
				want := ": " + k.kind + " default/" + v.transform
				got := generatedNames(cluster, apps)
				if !slices.ContainsFunc(got, func(line string) bool { return strings.HasSuffix(line, want) }) {
					t.Fatalf("no generated object %q in\n  %s", want, strings.Join(got, "\n  "))
				}

				transformer, comp := postgresqlComponent(t, doc)
				selected := func(eps []netpol.Endpoint, err error) string {
					t.Helper()
					if err != nil || len(eps) != 1 {
						t.Fatalf("endpoints = %v, %v; want one", eps, err)
					}
					return eps[0].PodSelector.MatchLabels[k.label]
				}
				if got := selected(transformer.ComponentEndpointsNamed("shop", comp, hook)); got != v.named {
					t.Errorf("ComponentEndpointsNamed selects %s=%q, want %q", k.label, got, v.named)
				}
				if got := selected(transformer.ComponentEndpoints(comp)); got != v.plain {
					t.Errorf("ComponentEndpoints selects %s=%q, want %q", k.label, got, v.plain)
				}
				// Once by the transform and once for the named endpoint, and not at
				// all for an authored name.
				wantAsked := 2
				if v.authored != "" {
					wantAsked = 0
				}
				if asked != wantAsked {
					t.Errorf("the hook was asked for the object's name %d times, want %d", asked, wantAsked)
				}
			})
		}
	}
}

const objectNameAppHeader = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
`

// TestObjectName_PoolerNamedLikeItsCluster: a Pooler cannot carry its Cluster's
// name, and the name compared is the Pooler's own, the object name: a pooler
// component renamed to its cluster's name is refused, and one named like the
// cluster but renamed away from it is not.
func TestObjectName_PoolerNamedLikeItsCluster(t *testing.T) {
	pooler := func(component, objectName string) string {
		return objectNameAppHeader + `    - name: ` + component + `
      type: cnpg-pooler
      properties:
        objectName: ` + objectName + `
        cluster:
          name: main
        pgbouncer: {}
`
	}
	_, err := buildWithProfile(t, pooler("pgb", "main"), labelInvariantProfile)
	const want = `cluster.name "main": a pooler cannot have the same name as its cluster`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a pooler renamed to its cluster's name: err = %v\nwant one containing %s", err, want)
	}
	docs, err := buildWithProfile(t, pooler("main", "edge"), labelInvariantProfile)
	if err != nil {
		t.Fatalf("a pooler component named like its cluster and renamed: %v", err)
	}
	wantNestedString(t, namedDoc(t, docs, "Pooler", "edge"), "main", "spec", "cluster", "name")
}

// TestObjectName_AuthoredReferenceStaysAsWritten pins the example of the oam
// README: a reference the author writes is not rewritten, so a HelmRelease
// naming a renamed `helmrepository` names it by its object name, and its own
// release name stays the component's.
func TestObjectName_AuthoredReferenceStaysAsWritten(t *testing.T) {
	docs, err := buildWithProfile(t, objectNameAppHeader+`    - name: charts
      type: helmrepository
      properties:
        objectName: shop-charts
        url: https://charts.example.com
    - name: app
      type: helmrelease
      properties:
        objectName: shop-app
        chart:
          spec:
            chart: app
            sourceRef:
              kind: HelmRepository
              name: shop-charts
`, labelInvariantProfile)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}
	if got := kindNames(docs, "HelmRepository"); !slices.Equal(got, []string{"shop-charts"}) {
		t.Errorf("the HelmRepository objects are %v, want [shop-charts]", got)
	}
	release := namedDoc(t, docs, "HelmRelease", "shop-app")
	wantNestedString(t, release, "shop-charts", "spec", "chart", "spec", "sourceRef", "name")
	wantNestedString(t, release, "app", "spec", "releaseName")
}

// webWithRBACTrait is component web with an `rbac` trait that names its
// objects "reader" and grants cluster-wide too: a Role, a RoleBinding, a
// ClusterRole and a ClusterRoleBinding.
const webWithRBACTrait = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/app:v1.0.0
        port: 8080
      traits:
        - type: rbac
          properties:
            name: reader
            clusterWide: true
            rules:
              - apiGroups: [""]
                resources: [pods]
                verbs: [get]
`

const (
	rbacKindRules = `        rules:
          - apiGroups: [""]
            resources: [pods]
            verbs: [get]
`
	rbacKindBinding = `        subjects:
          - kind: ServiceAccount
            name: other
            namespace: default
        roleRef:
          kind: ClusterRole
          name: view
`
)

// rbacKindComponent is component granted, of one of the four RBAC kinds, whose
// object is named objectName.
func rbacKindComponent(kind, objectName, properties string) string {
	return "    - name: granted\n      type: " + kind + "\n      properties:\n        objectName: " + objectName + "\n" + properties
}

// TestObjectName_CollidesWithAGeneratedName: an `objectName` that is the name
// of an object another component generates is refused with both named. A name
// launcher resolved for a role is refused as a name collision; an object a
// trait names without a role is caught where the two objects meet.
func TestObjectName_CollidesWithAGeneratedName(t *testing.T) {
	const database = `    - name: db
      type: postgresql
      properties:
        version: "16"
        storageSize: 10Gi
`
	cases := []struct {
		name, components, want string
	}{
		{
			name: "the Pooler of a postgresql component",
			components: database + `        pooler:
          enabled: true
    - name: pgb
      type: cnpg-pooler
      properties:
        objectName: db-pooler
        cluster:
          name: db
        pgbouncer: {}
`,
			want: `name collision: Pooler.postgresql.cnpg.io "default/db-pooler" is named by ` +
				`component "db" (role "pooler", its default) and by ` +
				`component "pgb" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "a Database of a postgresql component",
			components: database + `        databases:
          - name: app
            owner: app
    - name: appdb
      type: cnpg-database
      properties:
        objectName: db-app
        cluster:
          name: db
        name: app
        owner: app
`,
			want: `name collision: Database.postgresql.cnpg.io "default/db-app" is named by ` +
				`component "db" (role "database", its default) and by ` +
				`component "appdb" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "the default name of another kind component",
			components: `    - name: cfg
      type: configmap
      properties:
        data:
          k: v
    - name: other
      type: configmap
      properties:
        objectName: cfg
        data:
          k: v
`,
			want: `name collision: ConfigMap "default/cfg" is named by ` +
				`component "cfg" (role "object", its default) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "the ConfigMap a trait names",
			components: `    - name: web
      type: deployment
      properties:
        image: ghcr.io/example/app:v1.0.0
      traits:
        - type: configmap
          properties:
            name: shared
            data:
              K: v
    - name: cfg
      type: configmap
      properties:
        objectName: shared
        data:
          k: v
`,
			want: `name collision: ConfigMap "default/shared" is named by ` +
				`component "cfg" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "configmap" (its own object, set by name); give one of them another name`,
		},
		// The `rbac` trait resolves the name of each object it generates for a
		// role, so a component of one of the four RBAC kinds that names the same
		// object in the same namespace, or the same cluster-scoped object, is
		// refused as a name collision, before the two objects meet.
		{
			name:       "the Role an rbac trait names",
			components: webWithRBACTrait + rbacKindComponent("role", "reader", rbacKindRules),
			want: `component "web" trait "rbac": rbac: name collision: Role.rbac.authorization.k8s.io "default/reader" is named by ` +
				`component "granted" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "rbac" (role "rbac", set by name); give one of them another name`,
		},
		{
			name:       "the Role an rbac trait names by default",
			components: strings.Replace(webWithRBACTrait, "            name: reader\n", "", 1) + rbacKindComponent("role", "web", rbacKindRules),
			want: `name collision: Role.rbac.authorization.k8s.io "default/web" is named by ` +
				`component "granted" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "rbac" (role "rbac", its default); give one of them another name`,
		},
		{
			name:       "the RoleBinding an rbac trait names",
			components: webWithRBACTrait + rbacKindComponent("rolebinding", "reader", rbacKindBinding),
			want: `name collision: RoleBinding.rbac.authorization.k8s.io "default/reader" is named by ` +
				`component "granted" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "rbac" (role "rbac", set by name); give one of them another name`,
		},
		{
			name:       "the ClusterRole an rbac trait names",
			components: webWithRBACTrait + rbacKindComponent("clusterrole", "reader", rbacKindRules),
			want: `name collision: ClusterRole.rbac.authorization.k8s.io "reader" is named by ` +
				`component "granted" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "rbac" (role "rbac", set by name); give one of them another name`,
		},
		{
			name:       "the ClusterRoleBinding an rbac trait names",
			components: webWithRBACTrait + rbacKindComponent("clusterrolebinding", "reader", rbacKindBinding),
			want: `name collision: ClusterRoleBinding.rbac.authorization.k8s.io "reader" is named by ` +
				`component "granted" (role "object", set by properties.objectName) and by ` +
				`component "web" traits[0] "rbac" (role "rbac", set by name); give one of them another name`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWithProfile(t, objectNameAppHeader+tc.components, labelInvariantProfile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tc.want)
			}
		})
	}
}

// objectNameRouteProfile gives an `httproute` trait its traffic sources, so a
// route's backends get a synthesized ingress NetworkPolicy.
const objectNameRouteProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    httproute:
      rendering:
        networkPolicy:
          trafficSources:
            - namespace: gateway-system
`

// objectNameRouteApp is a `service` component "api" whose Service is renamed
// "api-renamed", and a webservice whose route has the given backendRefs.
func objectNameRouteApp(backendRefs string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: service
      properties:
        objectName: api-renamed
        selector:
          app: backend-pods
        ports:
          - name: http
            port: 80
            targetPort: 8080
    - name: front
      type: webservice
      properties:
        image: ghcr.io/example/app:v1.0.0
        port: 8080
      traits:
        - type: httproute
          properties:
            parentRefs:
              - name: gw
            rules:
              - backendRefs:
` + backendRefs
}

const (
	routeByObjectName = `                  - name: api-renamed
                    port: 80
`
	routeByComponentName = `                  - name: api
                    port: 80
`
	routeByComponentNameSelected = routeByComponentName + `                    backendSelector:
                      matchLabels:
                        app: elsewhere
`
)

// TestObjectName_RouteToARenamedService: a route names a Service, so a route to
// a renamed component is written with the object name. Written that way it
// resolves to the component, and the synthesized policy admits the traffic to
// the Service's pods. Written with the component name it names a Service the
// document does not own: an external one, which gets a policy only with a
// `backendSelector`, and then its own.
func TestObjectName_RouteToARenamedService(t *testing.T) {
	const policy = "api-allow-ingress-traffic"

	t.Run("by object name it is the component", func(t *testing.T) {
		docs, err := buildWithProfile(t, objectNameRouteApp(routeByObjectName), objectNameRouteProfile)
		if err != nil {
			t.Fatalf("kurel build: %v", err)
		}
		np := namedDoc(t, docs, "NetworkPolicy", policy)
		wantNestedString(t, np, "backend-pods", "spec", "podSelector", "matchLabels", "app")
		wantNestedString(t, np, "api", "metadata", "labels", kurelComponentLabel)
	})

	t.Run("by component name it is an external Service, left alone without a selector", func(t *testing.T) {
		docs, err := buildWithProfile(t, objectNameRouteApp(routeByComponentName), objectNameRouteProfile)
		if err != nil {
			t.Fatalf("kurel build: %v", err)
		}
		if got := kindNames(docs, "NetworkPolicy"); len(got) != 0 {
			t.Errorf("NetworkPolicy objects = %v, want none: the route names no Service of the document and gives no selector", got)
		}
	})

	t.Run("by component name with a selector it gets the external policy", func(t *testing.T) {
		docs, err := buildWithProfile(t, objectNameRouteApp(routeByComponentNameSelected), objectNameRouteProfile)
		if err != nil {
			t.Fatalf("kurel build: %v", err)
		}
		np := namedDoc(t, docs, "NetworkPolicy", policy)
		wantNestedString(t, np, "elsewhere", "spec", "podSelector", "matchLabels", "app")
		if _, found, _ := unstructured.NestedString(np, "metadata", "labels", kurelComponentLabel); found {
			t.Errorf("the external Service's policy carries the component label; it is no component's")
		}
	})

	t.Run("both in one document name one policy and are refused", func(t *testing.T) {
		_, err := buildWithProfile(t, objectNameRouteApp(routeByComponentNameSelected+routeByObjectName), objectNameRouteProfile)
		const want = `name collision: NetworkPolicy.networking.k8s.io "default/api-allow-ingress-traffic" is named by ` +
			`component "api" (role "netpol-synth", its default) and by ` +
			`external backend Service "api" (role "netpol-synth", its default); give one of them another name`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
}
