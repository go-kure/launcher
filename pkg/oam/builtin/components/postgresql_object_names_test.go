package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin the names of the Cluster and the ObjectStore a postgresql
// component generates (go-kure/launcher#787) on the rule itself:
// `clusterObjectName` and `objectStoreObjectName` name the two objects, the
// members keep the component's name, and every reference the rule writes to
// one of them follows the name it took.

// postgresqlWithStore is a postgresql component "db" with an object store, a
// pooler and a database, and props on top.
func postgresqlWithStore(props map[string]any) *oam.Component {
	all := map[string]any{
		"pooler":      map[string]any{"enabled": true},
		"objectStore": map[string]any{"destinationPath": "s3://bucket/db/"},
		"databases":   []any{map[string]any{"name": "orders", "owner": "app"}},
	}
	for k, v := range props {
		all[k] = v
	}
	return &oam.Component{Name: "db", Type: "postgresql", Properties: all}
}

// clusterReference returns the `cluster.name` a Pooler or a Database member
// carries.
func clusterReference(member oam.Component) any {
	cluster, _ := member.Properties["cluster"].(map[string]any)
	return cluster["name"]
}

// barmanObjectName returns the store the Cluster member's backup plugin names.
func barmanObjectName(t *testing.T, cluster oam.Component) any {
	t.Helper()
	plugins, _ := cluster.Properties["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("the Cluster's plugins = %v, want the one backup plugin", cluster.Properties["plugins"])
	}
	plugin, _ := plugins[0].(map[string]any)
	params, _ := plugin["parameters"].(map[string]any)
	return params["barmanObjectName"]
}

// clusterSelector returns the Cluster name the component's cluster endpoint
// selects.
func clusterSelector(t *testing.T, comp *oam.Component) string {
	t.Helper()
	eps, err := components.PostgresqlRule{}.Endpoints(comp)
	if err != nil || len(eps) == 0 {
		t.Fatalf("Endpoints = %v, %v; want the cluster's first", eps, err)
	}
	return eps[0].PodSelector.MatchLabels["cnpg.io/cluster"]
}

// The two properties name the Cluster and the ObjectStore as written, each on
// its own, and with neither both objects carry the component's name. The
// members keep the component's name whatever their objects are called, the
// Pooler and the Database keep their default names, and the Pooler's and the
// Database's cluster reference, the endpoint selector and the backup plugin's
// store follow.
func TestPostgresqlRule_ClusterAndObjectStoreNames(t *testing.T) {
	for _, tc := range []struct {
		name           string
		props          map[string]any
		cluster, store string
	}{
		{"neither", nil, "db", "db"},
		{"both", map[string]any{"clusterObjectName": "shop-pg", "objectStoreObjectName": "shop.backups"}, "shop-pg", "shop.backups"},
		{"the Cluster alone", map[string]any{"clusterObjectName": "shop-pg"}, "shop-pg", "db"},
		{"the ObjectStore alone", map[string]any{"objectStoreObjectName": "shop.backups"}, "db", "shop.backups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := postgresqlWithStore(tc.props)
			res := lowerPostgresql(t, comp, nil)

			want := []string{"cnpg-cluster/db", "cnpg-objectstore/db", "cnpg-pooler/db-pooler", "cnpg-database/db-orders"}
			if got := memberNames(res); !slices.Equal(got, want) {
				t.Fatalf("emitted %v, want %v", got, want)
			}
			cluster, store, pooler, database := res.Components[0], res.Components[1], res.Components[2], res.Components[3]
			if got := cluster.ObjectName(); got != tc.cluster {
				t.Errorf("the Cluster is named %q, want %q", got, tc.cluster)
			}
			if got := store.ObjectName(); got != tc.store {
				t.Errorf("the ObjectStore is named %q, want %q", got, tc.store)
			}
			if got := clusterReference(pooler); got != tc.cluster {
				t.Errorf("the Pooler's cluster = %v, want %q", got, tc.cluster)
			}
			if got := clusterReference(database); got != tc.cluster {
				t.Errorf("the Database's cluster = %v, want %q", got, tc.cluster)
			}
			if got := barmanObjectName(t, cluster); got != tc.store {
				t.Errorf("the backup plugin's barmanObjectName = %v, want %q", got, tc.store)
			}
			if got := clusterSelector(t, comp); got != tc.cluster {
				t.Errorf("the cluster endpoint selects cnpg.io/cluster=%q, want %q", got, tc.cluster)
			}
			for _, member := range res.Components {
				for _, property := range []string{"clusterObjectName", "objectStoreObjectName"} {
					if _, has := member.Properties[property]; has {
						t.Errorf("%s reached the spec of %s/%s", property, member.Type, member.Name)
					}
				}
			}
		})
	}
}

// With its Cluster named apart, a component whose own name no Cluster can carry
// is lowered: the Cluster's rule is the Cluster's name's, not the component's.
// The Pooler's default still derives from the component name, so it is held to
// the Pooler's own rule, by the lowering and where endpoints are collected
// alike, in a refusal that names `poolerName`, which settles it.
func TestPostgresqlRule_ComponentNameNoClusterCanCarry(t *testing.T) {
	long := strings.Repeat("a", 60)
	for _, name := range []string{"db.main", long} {
		comp := &oam.Component{Name: name, Type: "postgresql", Properties: map[string]any{"clusterObjectName": "pg"}}
		res := lowerPostgresql(t, comp, nil)
		if got := res.Components[0].ObjectName(); got != "pg" {
			t.Errorf("component %q: the Cluster is named %q, want pg", name, got)
		}
		if got := clusterSelector(t, comp); got != "pg" {
			t.Errorf("component %q: the cluster endpoint selects %q, want pg", name, got)
		}

		comp.Properties["pooler"] = map[string]any{"enabled": true}
		want := `pooler: cnpg-pooler name "` + name + `-pooler": must be a DNS-1035 label of at most 63 characters (CloudNativePG names the pooler's Service after it); the default derives from the component name: set poolerName to name the Pooler otherwise`
		_, err := components.PostgresqlRule{}.Endpoints(comp)
		if err == nil || err.Error() != want {
			t.Errorf("component %q: Endpoints err = %v\nwant %q", name, err, want)
		}
		_, err = components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
		if err == nil || err.Error() != want {
			t.Errorf("component %q: LowerComponent err = %v\nwant %q", name, err, want)
		}

		comp.Properties["poolerName"] = "edge"
		if eps, err := (components.PostgresqlRule{}).Endpoints(comp); err != nil || len(eps) != 2 {
			t.Errorf("component %q with a poolerName: Endpoints = %v, %v; want two", name, eps, err)
		}
		res = lowerPostgresql(t, comp, nil)
		if got := res.Components[1].Name; got != "edge" {
			t.Errorf("component %q with a poolerName: the Pooler member is %q, want edge", name, got)
		}
	}
}

func TestPostgresqlRule_RefusesClusterAndObjectStoreNames(t *testing.T) {
	store := map[string]any{"destinationPath": "s3://bucket/db/"}
	tooLong := strings.Repeat("a", 51)
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"a clusterObjectName that is no string",
			map[string]any{"clusterObjectName": 7},
			"clusterObjectName: ",
		},
		{
			"a dotted clusterObjectName",
			map[string]any{"clusterObjectName": "pg.main"},
			`naming the Cluster: clusterObjectName "pg.main" cannot be the name for role "postgresql-cluster": not a valid DNS-1035 label: `,
		},
		{
			"a clusterObjectName over 50 characters",
			map[string]any{"clusterObjectName": tooLong},
			`clusterObjectName (or the Naming hook's answer for role "postgresql-cluster"): postgresql Cluster name "` + tooLong + `": must be a DNS-1035 label of at most 50 characters`,
		},
		{
			"an empty clusterObjectName",
			map[string]any{"clusterObjectName": ""},
			`naming the Cluster: clusterObjectName "" cannot be the name for role "postgresql-cluster": it is empty; write a valid name, or leave the property out for the default "db"`,
		},
		{
			"objectStoreObjectName without objectStore",
			map[string]any{"objectStoreObjectName": "backups"},
			"objectStoreObjectName: names the ObjectStore, and objectStore is not set; remove it, or set objectStore",
		},
		{
			"an objectStoreObjectName that is no string",
			map[string]any{"objectStore": store, "objectStoreObjectName": true},
			"objectStoreObjectName: ",
		},
		{
			"an objectStoreObjectName that is no subdomain",
			map[string]any{"objectStore": store, "objectStoreObjectName": "Backups_1"},
			`naming the ObjectStore: objectStoreObjectName "Backups_1" cannot be the name for role "postgresql-objectstore": not a valid DNS-1123 subdomain: `,
		},
		{
			"an empty objectStoreObjectName",
			map[string]any{"objectStore": store, "objectStoreObjectName": ""},
			`naming the ObjectStore: objectStoreObjectName "" cannot be the name for role "postgresql-objectstore": it is empty; write a valid name, or leave the property out for the default "db"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// The Cluster's rule is checked wherever the name is read: Parse and Endpoints
// refuse a `clusterObjectName` no Cluster can carry in the words LowerComponent
// uses, and a component name that is the Cluster's keeps the text it had.
func TestPostgresqlRule_ClusterNameRuleEverywhere(t *testing.T) {
	tooLong := strings.Repeat("a", 51)
	for _, tc := range []struct {
		name string
		comp *oam.Component
		want string
	}{
		{
			"an authored Cluster name",
			&oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"clusterObjectName": tooLong}},
			`postgresql Cluster name "` + tooLong + `": must be a DNS-1035 label of at most 50 characters`,
		},
		{
			"the component name",
			&oam.Component{Name: tooLong, Type: "postgresql"},
			`postgresql name "` + tooLong + `": must be a DNS-1035 label of at most 50 characters`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := components.PostgresqlRule{}
			_, lowerErr := rule.LowerComponent(tc.comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			_, parseErr := rule.Parse(tc.comp)
			_, endpointsErr := rule.Endpoints(tc.comp)
			for reader, err := range map[string]error{"LowerComponent": lowerErr, "Parse": parseErr, "Endpoints": endpointsErr} {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s: err = %v\nwant one containing %q", reader, err, tc.want)
				}
			}
		})
	}
}
