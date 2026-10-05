package kurel

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// These tests pin the names of the Cluster and the ObjectStore a postgresql
// component generates (go-kure/launcher#787) through kurel's own transformer.
// Each is named by the author (clusterObjectName, objectStoreObjectName), else
// by the Naming hook (roles postgresql-cluster, postgresql-objectstore), else
// after the component, and every reference launcher writes to one of them
// follows the name it got, while the Pooler's and the Database's default names
// keep the component's.

// postgresqlNamesDoc holds the postgresql component "db" of namingDBStore (an
// object store, a pooler and a database), with props appended to its
// properties, and extraComponents after it.
func postgresqlNamesDoc(props, extraComponents string) string {
	return helmNamesApp(namingDBStore + props + extraComponents)
}

func postgresqlNamesContext(hook func(oam.NameRequest) (string, bool)) oam.TransformContext {
	return oam.TransformContext{Naming: hook}
}

// isPostgresqlObjectRole reports whether role names the Cluster or the
// ObjectStore of a postgresql component.
func isPostgresqlObjectRole(role oam.NameRole) bool {
	return role == oam.NameRolePostgresqlCluster || role == oam.NameRolePostgresqlObjectStore
}

// postgresqlObjectRequests keeps the requests of the two roles.
func postgresqlObjectRequests(requests []oam.NameRequest) []oam.NameRequest {
	var out []oam.NameRequest
	for _, req := range requests {
		if isPostgresqlObjectRole(req.Role) {
			out = append(out, req)
		}
	}
	return out
}

// postgresqlObjectNames are the names of the Cluster and the ObjectStore of
// component db.
type postgresqlObjectNames struct{ cluster, store string }

var (
	postgresqlDefaultNames = postgresqlObjectNames{cluster: "db", store: "db"}
	postgresqlRenamedNames = postgresqlObjectNames{cluster: "shop-pg", store: "shop.backups"}
)

// assertPostgresqlObjects checks the four objects of component db among docs:
// the Cluster and the ObjectStore under want's names, the Pooler and the
// Database under their defaults, and each reference launcher writes to the
// Cluster or the ObjectStore naming it as want does. The component label of the
// two renamed objects stays the component's.
func assertPostgresqlObjects(t *testing.T, docs []map[string]any, want postgresqlObjectNames) {
	t.Helper()
	for kind, names := range map[string][]string{
		"Cluster":     {want.cluster},
		"ObjectStore": {want.store},
		"Pooler":      {"db-pooler"},
		"Database":    {"db-orders"},
	} {
		if got := kindNames(docs, kind); !sameSet(got, names) {
			t.Errorf("the %s objects are %v, want %v", kind, got, names)
		}
	}
	if t.Failed() {
		return
	}
	wantNestedString(t, namedDoc(t, docs, "Pooler", "db-pooler"), want.cluster, "spec", "cluster", "name")
	wantNestedString(t, namedDoc(t, docs, "Database", "db-orders"), want.cluster, "spec", "cluster", "name")

	cluster := namedDoc(t, docs, "Cluster", want.cluster)
	plugins, _, err := unstructured.NestedSlice(cluster, "spec", "plugins")
	if err != nil || len(plugins) != 1 {
		t.Fatalf("the Cluster's spec.plugins = %v (err %v), want the one backup plugin", plugins, err)
	}
	plugin, _ := plugins[0].(map[string]any)
	wantNestedString(t, plugin, want.store, "parameters", "barmanObjectName")

	wantNestedString(t, cluster, "db", "metadata", "labels", kurelComponentLabel)
	wantNestedString(t, namedDoc(t, docs, "ObjectStore", want.store), "db", "metadata", "labels", kurelComponentLabel)
}

// postgresqlClusterSelector returns the Cluster name the component's cluster
// endpoint selects.
func postgresqlClusterSelector(t *testing.T, eps []netpol.Endpoint, err error) string {
	t.Helper()
	if err != nil || len(eps) != 2 {
		t.Fatalf("endpoints = %v, %v; want the cluster's and the pooler's", eps, err)
	}
	return eps[0].PodSelector.MatchLabels["cnpg.io/cluster"]
}

// A hook that declines every name changes nothing: the output is the one
// without a hook, object for object. It is asked for the Cluster's name and the
// ObjectStore's with the component, in the order the rule emits them, and not
// for either under the object role, as it is for an authored kind component.
func TestPostgresqlObjectNames_DecliningHookChangesNothing(t *testing.T) {
	doc := postgresqlNamesDoc("", "")
	cluster, apps := namingTransform(t, doc, postgresqlNamesContext(nil))
	without, withoutDocs := generatedNames(cluster, apps), generatedDocs(t, apps)
	assertPostgresqlObjects(t, withoutDocs, postgresqlDefaultNames)

	var requests []oam.NameRequest
	cluster, apps = namingTransform(t, doc, postgresqlNamesContext(declineEveryName(&requests)))
	if with := generatedNames(cluster, apps); !slices.Equal(without, with) {
		t.Errorf("a hook that declines every name changed the names:\nwithout: %s\nwith:    %s",
			strings.Join(without, "\n         "), strings.Join(with, "\n         "))
	}
	if withDocs := generatedDocs(t, apps); !reflect.DeepEqual(withoutDocs, withDocs) {
		t.Errorf("a hook that declines every name changed the generated objects")
	}

	want := []oam.NameRequest{
		{Application: "shop", Component: "db", Role: oam.NameRolePostgresqlCluster, Kind: clusterKindName, Default: "db"},
		{Application: "shop", Component: "db", Role: oam.NameRolePostgresqlObjectStore, Kind: objectStoreKindName, Default: "db"},
	}
	if got := postgresqlObjectRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", got, want)
	}
	for _, req := range requests {
		if req.Role == oam.NameRoleObject {
			t.Errorf("the hook was asked for an object name: %+v", req)
		}
	}
}

// The hook names the Cluster and the ObjectStore, each differently. The objects
// take the names, every reference follows the name its target got, and nothing
// else moves: every other generated object and every bundle is the one without
// a hook. ComponentEndpointsNamed asks the hook the request the transform asked,
// so the cluster endpoint selects the Cluster under the name it got;
// ComponentEndpoints asks no hook and selects the default.
func TestPostgresqlObjectNames_HookAnswerIsUsed(t *testing.T) {
	doc := postgresqlNamesDoc("", "")
	var asked []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		if req.Role == oam.NameRolePostgresqlCluster {
			asked = append(asked, req)
		}
		return renameBy(map[string]string{
			"postgresql-cluster db":     postgresqlRenamedNames.cluster,
			"postgresql-objectstore db": postgresqlRenamedNames.store,
		})(req)
	}
	cluster, apps := namingTransform(t, doc, postgresqlNamesContext(hook))
	assertPostgresqlObjects(t, generatedDocs(t, apps), postgresqlRenamedNames)
	request := oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRolePostgresqlCluster, Kind: clusterKindName, Default: "db"}
	if !slices.Equal(asked, []oam.NameRequest{request}) {
		t.Errorf("the transform asked the hook for the Cluster\n  %+v\nwant once, for the lowering and every reference alike:\n  %+v", asked, request)
	}

	plainCluster, plainApps := namingTransform(t, doc, postgresqlNamesContext(nil))
	want := generatedNames(plainCluster, plainApps)
	renamed := 0
	for i, line := range want {
		switch line {
		case "db: Cluster default/db":
			want[i], renamed = "db: Cluster default/"+postgresqlRenamedNames.cluster, renamed+1
		case "db: ObjectStore default/db":
			want[i], renamed = "db: ObjectStore default/"+postgresqlRenamedNames.store, renamed+1
		}
	}
	if renamed != 2 {
		t.Fatalf("the names without a hook hold no Cluster and ObjectStore of db:\n  %s", strings.Join(want, "\n  "))
	}
	if got := generatedNames(cluster, apps); !slices.Equal(got, want) {
		t.Errorf("generated names:\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	transformer, comp := postgresqlComponent(t, doc)
	eps, err := transformer.ComponentEndpointsNamed("shop", comp, hook)
	if selected := postgresqlClusterSelector(t, eps, err); selected != postgresqlRenamedNames.cluster {
		t.Errorf("ComponentEndpointsNamed selects cnpg.io/cluster=%q, want the name the Cluster got", selected)
	}
	if !slices.Equal(asked, []oam.NameRequest{request, request}) {
		t.Errorf("the hook was asked for the Cluster\n  %+v\nwant the transform's request, and the same again for the endpoint:\n  %+v", asked, request)
	}

	eps, err = transformer.ComponentEndpoints(comp)
	if selected := postgresqlClusterSelector(t, eps, err); selected != "db" {
		t.Errorf("ComponentEndpoints selects cnpg.io/cluster=%q, want the default: it asks no hook", selected)
	}
}

// An authored name is used as written and the hook is not asked for it, by the
// transform or for the endpoint, through the whole build: kurel writes the
// objects under the authored names, with the references following.
func TestPostgresqlObjectNames_AuthoredWinsAndHookIsNotAsked(t *testing.T) {
	doc := postgresqlNamesDoc("        clusterObjectName: shop-pg\n        objectStoreObjectName: shop.backups\n", "")

	var requests []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		requests = append(requests, req)
		return "theirs", isPostgresqlObjectRole(req.Role)
	}
	_, apps := namingTransform(t, doc, postgresqlNamesContext(hook))
	assertPostgresqlObjects(t, generatedDocs(t, apps), postgresqlRenamedNames)

	transformer, comp := postgresqlComponent(t, doc)
	eps, err := transformer.ComponentEndpointsNamed("shop", comp, hook)
	if selected := postgresqlClusterSelector(t, eps, err); selected != postgresqlRenamedNames.cluster {
		t.Errorf("ComponentEndpointsNamed selects cnpg.io/cluster=%q, want the authored name", selected)
	}
	eps, err = transformer.ComponentEndpoints(comp)
	if selected := postgresqlClusterSelector(t, eps, err); selected != postgresqlRenamedNames.cluster {
		t.Errorf("ComponentEndpoints selects cnpg.io/cluster=%q, want the authored name", selected)
	}
	if got := postgresqlObjectRequests(requests); len(got) != 0 {
		t.Errorf("the hook was asked for names the author set:\n  %+v", got)
	}

	docs, err := buildWithProfile(t, doc, objectNameRouteProfile)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}
	assertPostgresqlObjects(t, docs, postgresqlRenamedNames)
}

// Each name is its own: naming one object leaves the other at the component
// name.
func TestPostgresqlObjectNames_OneNameAtATime(t *testing.T) {
	for _, tc := range []struct {
		name, props string
		want        postgresqlObjectNames
	}{
		{"the Cluster", "        clusterObjectName: shop-pg\n", postgresqlObjectNames{cluster: "shop-pg", store: "db"}},
		{"the ObjectStore", "        objectStoreObjectName: shop.backups\n", postgresqlObjectNames{cluster: "db", store: "shop.backups"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, apps := namingTransform(t, postgresqlNamesDoc(tc.props, ""), postgresqlNamesContext(nil))
			assertPostgresqlObjects(t, generatedDocs(t, apps), tc.want)
		})
	}
}

// A component with no objectStore generates no ObjectStore, so the hook is not
// asked for one.
func TestPostgresqlObjectNames_HookNotAskedForAStoreNotGenerated(t *testing.T) {
	var requests []oam.NameRequest
	namingTransform(t, helmNamesApp(namingDB), postgresqlNamesContext(declineEveryName(&requests)))
	want := []oam.NameRequest{
		{Application: "shop", Component: "db", Role: oam.NameRolePostgresqlCluster, Kind: clusterKindName, Default: "db"},
	}
	if got := postgresqlObjectRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", got, want)
	}
}

// With its Cluster named apart, a component whose own name no Cluster can carry
// builds: the Cluster's rule is held to the Cluster's name. The Pooler's default
// still derives from the component name and is held to the Pooler's rule, in a
// refusal that names `poolerName`, which settles it.
func TestPostgresqlObjectNames_ComponentNameNoClusterCanCarry(t *testing.T) {
	doc := func(props string) string {
		return helmNamesApp("    - name: db.main\n      type: postgresql\n      properties:\n        storageSize: 1Gi\n" + props)
	}
	err := transformErr(t, doc(""), postgresqlNamesContext(nil))
	if want := `postgresql name "db.main": must be a DNS-1035 label of at most 50 characters`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("without a Cluster name: err = %v\nwant one containing %q", err, want)
	}

	docs, err := buildWithProfile(t, doc("        clusterObjectName: pg\n"), objectNameRouteProfile)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}
	if got := kindNames(docs, "Cluster"); !slices.Equal(got, []string{"pg"}) {
		t.Errorf("the Cluster objects are %v, want [pg]", got)
	}

	const pooled = "        clusterObjectName: pg\n        pooler:\n          enabled: true\n"
	err = transformErr(t, doc(pooled), postgresqlNamesContext(nil))
	want := `pooler: cnpg-pooler name "db.main-pooler": must be a DNS-1035 label of at most 63 characters (CloudNativePG names the pooler's Service after it); the default derives from the component name: set poolerName to name the Pooler otherwise`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("with a pooler and no poolerName: err = %v\nwant one containing %q", err, want)
	}
	docs, err = buildWithProfile(t, doc(pooled+"        poolerName: edge\n"), objectNameRouteProfile)
	if err != nil {
		t.Fatalf("kurel build with a poolerName: %v", err)
	}
	if got := kindNames(docs, "Pooler"); !slices.Equal(got, []string{"edge"}) {
		t.Errorf("the Pooler objects are %v, want [edge]", got)
	}
	wantNestedString(t, namedDoc(t, docs, "Pooler", "edge"), "pg", "spec", "cluster", "name")
}

func TestPostgresqlObjectNames_Refusals(t *testing.T) {
	tooLong := strings.Repeat("a", 51)
	const second = `    - name: db2
      type: postgresql
      properties:
`
	for _, tc := range []struct {
		name, props, extra string
		names              map[string]string
		want               string
		// endpoint is set when ComponentEndpointsNamed refuses the answer too, in
		// the same words.
		endpoint bool
	}{
		{
			name:     "a Cluster answer that is no DNS-1035 label",
			names:    map[string]string{"postgresql-cluster db": "pg.main"},
			want:     `naming the Cluster: the Naming hook returned "pg.main" for role "postgresql-cluster" in place of "db": not a valid DNS-1035 label: `,
			endpoint: true,
		},
		{
			name:     "a Cluster answer over 50 characters",
			names:    map[string]string{"postgresql-cluster db": tooLong},
			want:     `clusterObjectName (or the Naming hook's answer for role "postgresql-cluster"): postgresql Cluster name "` + tooLong + `": must be a DNS-1035 label of at most 50 characters`,
			endpoint: true,
		},
		{
			name:  "an ObjectStore answer that is no subdomain",
			names: map[string]string{"postgresql-objectstore db": "Backups_1"},
			want:  `naming the ObjectStore: the Naming hook returned "Backups_1" for role "postgresql-objectstore" in place of "db": not a valid DNS-1123 subdomain: `,
		},
		{
			name:  "an authored Cluster name that is another component's Cluster",
			extra: second + "        clusterObjectName: db\n",
			want: `name collision: Cluster.postgresql.cnpg.io "db" is named by ` +
				`component "db" (role "postgresql-cluster", its default) and by ` +
				`component "db2" (role "postgresql-cluster", set by clusterObjectName); give one of them another name`,
		},
		{
			name:  "a Cluster answer that is another component's Cluster",
			extra: second + "        storageSize: 1Gi\n",
			names: map[string]string{"postgresql-cluster db2": "db"},
			want: `name collision: Cluster.postgresql.cnpg.io "db" is named by ` +
				`component "db" (role "postgresql-cluster", its default) and by ` +
				`component "db2" (role "postgresql-cluster", returned by the Naming hook in place of "db2"); give one of them another name`,
		},
		{
			name:  "an authored ObjectStore name that is another component's ObjectStore",
			extra: second + "        objectStore:\n          destinationPath: s3://backups/db2\n        objectStoreObjectName: db\n",
			want: `name collision: ObjectStore.barmancloud.cnpg.io "db" is named by ` +
				`component "db" (role "postgresql-objectstore", its default) and by ` +
				`component "db2" (role "postgresql-objectstore", set by objectStoreObjectName); give one of them another name`,
		},
		{
			// The authored kind component's name is resolved after lowering, in
			// the same space as the rule's.
			name:  "an authored Cluster name that is a cnpg-cluster component's",
			props: "        clusterObjectName: pg\n",
			extra: "    - name: pg\n      type: cnpg-cluster\n      properties:\n        instances: 1\n        storage:\n          size: 1Gi\n",
			want:  `name collision: Cluster.postgresql.cnpg.io "default/pg" is named by `,
		},
		{
			name:  "objectStoreObjectName without objectStore",
			extra: second + "        objectStoreObjectName: backups\n",
			want:  `objectStoreObjectName: names the ObjectStore, and objectStore is not set; remove it, or set objectStore`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := postgresqlNamesDoc(tc.props, tc.extra)
			hook := renameBy(tc.names)
			err := transformErr(t, doc, postgresqlNamesContext(hook))
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
