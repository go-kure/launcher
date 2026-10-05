package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// memberNames lists the emitted components as "type/name", in order.
func memberNames(res oam.LoweringResult) []string {
	var out []string
	for _, c := range res.Components {
		out = append(out, c.Type+"/"+c.Name)
	}
	return out
}

// An authored `poolerName` and `databases[].objectName` name the Pooler and the
// Database, as written. They name the object only: the Pooler still points at
// the Cluster, the Database still creates the database its `name` says, and the
// pooler endpoint selects the Pooler's pods by the chosen name.
func TestPostgresqlRule_AuthoredObjectNames(t *testing.T) {
	comp := &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"pooler":     map[string]any{"enabled": true},
		"poolerName": "edge",
		"databases": []any{
			map[string]any{"name": "orders", "owner": "app", "objectName": "orders.v2"},
			map[string]any{"name": "billing", "owner": "app"},
		},
	}}
	res := lowerPostgresql(t, comp, &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{*comp}}})

	want := []string{"cnpg-cluster/db", "cnpg-pooler/edge", "cnpg-database/orders.v2", "cnpg-database/db-billing"}
	if got := memberNames(res); !slices.Equal(got, want) {
		t.Fatalf("emitted %v, want %v", got, want)
	}
	if cluster, _ := res.Components[1].Properties["cluster"].(map[string]any); cluster["name"] != "db" {
		t.Errorf("the Pooler's cluster = %v, want db", res.Components[1].Properties["cluster"])
	}
	database := res.Components[2].Properties
	if cluster, _ := database["cluster"].(map[string]any); database["name"] != "orders" || cluster["name"] != "db" {
		t.Errorf("the Database = %v, want database orders of cluster db", database)
	}
	if _, has := database["objectName"]; has {
		t.Errorf("objectName reached the Database's spec: %v", database)
	}

	eps, err := components.PostgresqlRule{}.Endpoints(comp)
	if err != nil || len(eps) != 2 {
		t.Fatalf("Endpoints = %v, %v; want two", eps, err)
	}
	if got := eps[1].PodSelector.MatchLabels["cnpg.io/poolerName"]; got != "edge" {
		t.Errorf("the pooler endpoint selects cnpg.io/poolerName=%q, want the authored name", got)
	}
}

// A database whose name cannot be part of an object name ("app_data") has no
// default object name. An authored `objectName` names its Database all the
// same; without one it is refused, and an invalid one is refused without
// pointing at a default there is none of.
func TestPostgresqlRule_ObjectNameWhereTheDefaultCannotBeBuilt(t *testing.T) {
	database := func(extra map[string]any) *oam.Component {
		db := map[string]any{"name": "app_data", "owner": "app"}
		for k, v := range extra {
			db[k] = v
		}
		return &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"databases": []any{db}}}
	}

	comp := database(map[string]any{"objectName": "app-data"})
	res := lowerPostgresql(t, comp, nil)
	if got, want := memberNames(res), []string{"cnpg-cluster/db", "cnpg-database/app-data"}; !slices.Equal(got, want) {
		t.Fatalf("emitted %v, want %v", got, want)
	}
	if got := res.Components[1].Properties["name"]; got != "app_data" {
		t.Errorf("the Database creates %v, want app_data", got)
	}

	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  []string
	}{
		{"no objectName", nil, []string{`databases[0] "app_data": `, `generated name "db-app_data" is not a valid DNS-1123 subdomain`}},
		{
			"an invalid objectName",
			map[string]any{"objectName": "App_Data"},
			[]string{`databases[0] "app_data": databases[0].objectName "App_Data" cannot be the name for role "database": not a valid DNS-1123 subdomain: `, "; write a valid name"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := database(tc.extra)
			_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err == nil {
				t.Fatal("LowerComponent succeeded, want a refusal")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "leave the property out") {
				t.Errorf("error = %q points at a default that cannot be built", err)
			}
		})
	}
}

// A Database given the Pooler's name is a different object of that name, by
// default name or by an authored one: it joins the Pooler's sibling group.
func TestPostgresqlRule_DatabaseNamedLikeThePooler(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{
			"the pooler renamed to a database's default name",
			map[string]any{"pooler": map[string]any{"enabled": true}, "poolerName": "db-orders",
				"databases": []any{map[string]any{"name": "orders", "owner": "app"}}},
			[]string{"cnpg-cluster/db", "cnpg-pooler/db-orders", "cnpg-database/db-orders"},
		},
		{
			"a database renamed to the pooler's default name",
			map[string]any{"pooler": map[string]any{"enabled": true},
				"databases": []any{map[string]any{"name": "orders", "owner": "app", "objectName": "db-pooler"}}},
			[]string{"cnpg-cluster/db", "cnpg-pooler/db-pooler", "cnpg-database/db-pooler"},
		},
		{
			// With the Pooler renamed, the database named "pooler" is no longer its
			// sibling.
			"the database named pooler beside a renamed pooler",
			map[string]any{"pooler": map[string]any{"enabled": true}, "poolerName": "edge",
				"databases": []any{map[string]any{"name": "pooler", "owner": "app"}}},
			[]string{"cnpg-cluster/db", "cnpg-pooler/edge", "cnpg-database/db-pooler"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			if got := memberNames(lowerPostgresql(t, comp, nil)); !slices.Equal(got, tc.want) {
				t.Errorf("emitted %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPostgresqlRule_RefusesAuthoredObjectNames(t *testing.T) {
	pooler := map[string]any{"enabled": true}
	orders := func(objectName any) map[string]any {
		return map[string]any{"name": "orders", "owner": "app", "objectName": objectName}
	}
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"poolerName without the pooler",
			map[string]any{"poolerName": "edge"},
			"poolerName: names the Pooler, and pooler.enabled is not true; remove it, or enable the pooler",
		},
		{
			"poolerName with the pooler disabled",
			map[string]any{"pooler": map[string]any{"enabled": false}, "poolerName": "edge"},
			"poolerName: names the Pooler, and pooler.enabled is not true; remove it, or enable the pooler",
		},
		{
			"a poolerName that is no string",
			map[string]any{"pooler": pooler, "poolerName": 7},
			"poolerName: ",
		},
		{
			"a dotted poolerName",
			map[string]any{"pooler": pooler, "poolerName": "edge.v2"},
			`pooler: poolerName "edge.v2" cannot be the name for role "pooler": not a valid DNS-1035 label: `,
		},
		{
			"a poolerName over 63 characters",
			map[string]any{"pooler": pooler, "poolerName": strings.Repeat("a", 64)},
			`cannot be the name for role "pooler": not a valid DNS-1035 label: must be no more than 63`,
		},
		{
			"an empty poolerName",
			map[string]any{"pooler": pooler, "poolerName": ""},
			`pooler: poolerName "" cannot be the name for role "pooler": it is empty; write a valid name, or leave the property out for the default "db-pooler"`,
		},
		{
			"a poolerName that is the component's own",
			map[string]any{"pooler": pooler, "poolerName": "db"},
			`pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
		},
		{
			"an objectName that is no subdomain",
			map[string]any{"databases": []any{orders("Orders_DB")}},
			`databases[0] "orders": databases[0].objectName "Orders_DB" cannot be the name for role "database": not a valid DNS-1123 subdomain: `,
		},
		{
			"an empty objectName",
			map[string]any{"databases": []any{orders("")}},
			`databases[0] "orders": databases[0].objectName "" cannot be the name for role "database": it is empty; write a valid name, or leave the property out for the default "db-orders"`,
		},
		{
			"an objectName that is no string",
			map[string]any{"databases": []any{orders(true)}},
			"databases[0].objectName: ",
		},
		{
			"two databases given one objectName",
			map[string]any{"databases": []any{orders("shared"), map[string]any{"name": "billing", "owner": "app", "objectName": "shared"}}},
			`databases[1] "billing": name collision: Database.postgresql.cnpg.io "shared" is named by ` +
				`component "db" (role "database", set by databases[0].objectName) and by ` +
				`component "db" (role "database", set by databases[1].objectName); give one of them another name`,
		},
		{
			"an objectName that is another database's default",
			map[string]any{"databases": []any{orders("db-billing"), map[string]any{"name": "billing", "owner": "app"}}},
			`databases[1] "billing": name collision: Database.postgresql.cnpg.io "db-billing" is named by ` +
				`component "db" (role "database", set by databases[0].objectName) and by ` +
				`component "db" (role "database", its default); give one of them another name`,
		},
		{
			"an objectName that is another component's name",
			map[string]any{"databases": []any{orders("api")}},
			`databases[0] "orders": generates component "api", which is already the name of component "api" (type "webservice") in the document; rename one of them`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			doc := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp, {Name: "api", Type: "webservice"}}}}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}
