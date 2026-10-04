package kurel

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// postgresqlMembersApp is a postgresql component emitting every member kind:
// the Cluster, the ObjectStore under its name, the Pooler, a Database and a
// Database named like the Pooler, followed by traitsYAML (indented for the
// component's traits list) and policiesYAML (for spec.policies, or empty).
func postgresqlMembersApp(traitsYAML, policiesYAML string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: db
      type: postgresql
      properties:
        pooler:
          enabled: true
        objectStore:
          destinationPath: s3://bucket/db/
        databases:
          - name: orders
            owner: app
          - name: pooler
            owner: app
      traits:
` + traitsYAML + `    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 9090
` + policiesYAML
}

const postgresqlOrderPolicy = `  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: api
            dependsOn: [db]
`

// postgresqlBundles transforms app with kurel's builtin transformer and
// returns the leaf bundles of its cluster, by name.
func postgresqlBundles(t *testing.T, app string) map[string]*stack.Bundle {
	t.Helper()
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	return leafBundles(cluster.Node)
}

const postgresqlPlacementPolicy = `    - name: where
      type: placement
      properties:
        component: db
        tier: apps
`

// TestBuild_PostgresqlPlacementReachesEveryMember: a placement of the
// postgresql component placed all of its objects; the rule repeats it for each
// member. Without a dependency policy the objects stay in one bundle, as they
// were; with one, the members are in the Cluster's tier, so the tier order adds
// no edge back to them (it made a cycle, which the transform refuses).
func TestBuild_PostgresqlPlacementReachesEveryMember(t *testing.T) {
	const traits = "        - type: prune-protection\n"
	t.Run("without a dependency policy", func(t *testing.T) {
		bundles := postgresqlBundles(t, postgresqlMembersApp(traits, "  policies:\n"+postgresqlPlacementPolicy))
		if got := slices.Sorted(maps.Keys(bundles)); !slices.Equal(got, []string{"shop"}) {
			t.Fatalf("bundles = %v, want the one bundle shop", got)
		}
	})
	t.Run("with a dependency policy", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, postgresqlMembersApp(traits, postgresqlOrderPolicy+postgresqlPlacementPolicy))
		if err != nil {
			t.Fatalf("transforming: %v", err)
		}
		want := []string{"shop-00: db", "shop-01: db-pooler db-orders", "shop-02: api"}
		if got := groupNames(t, cluster); !slices.Equal(got, want) {
			t.Errorf("groups = %v, want %v", got, want)
		}
	})
}

// TestBuild_PostgresqlAndWebserviceUnorderedIsOneBundle: no component type
// orders a component (go-kure/launcher#783). A postgresql beside a webservice,
// with no policy, is one flat bundle; the database came first only by its type
// before.
func TestBuild_PostgresqlAndWebserviceUnorderedIsOneBundle(t *testing.T) {
	cluster, _, err := transformWithBuiltins(t, postgresqlMembersApp("", ""))
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	root := cluster.Node.Bundle
	if root == nil || root.Name != "shop" || len(root.Children) != 0 || len(cluster.Node.Children) != 0 {
		t.Fatalf("root bundle = %v, want the one flat bundle shop", root)
	}
	var apps []string
	for _, a := range root.Applications {
		apps = append(apps, a.Name)
	}
	if want := []string{"db", "db-pooler", "db-orders", "api"}; !slices.Equal(apps, want) {
		t.Errorf("applications = %v, want %v in document order", apps, want)
	}
}

// TestBuild_PostgresqlPlacementOfALongMemberName: the placement copies are
// named after the Cluster, not the member, so a Database whose generated name
// is already as long as a policy name can be is still placed.
func TestBuild_PostgresqlPlacementOfALongMemberName(t *testing.T) {
	app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: db
      type: postgresql
      properties:
        databases:
          - name: ` + strings.Repeat("a", 241) + `
            owner: app
  policies:
` + strings.Replace(postgresqlPlacementPolicy, "tier: apps", "tier: services", 1)
	if _, out, err := buildDocs(t, app); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
}

// TestBuild_PostgresqlDependentWaitsForEveryMember: a component made to wait
// for the postgresql component waits for every member, so its group comes after
// the members' group, which comes after the Cluster's.
func TestBuild_PostgresqlDependentWaitsForEveryMember(t *testing.T) {
	policies := postgresqlOrderPolicy + `    - name: where
      type: placement
      properties:
        component: api
        tier: services
`
	cluster, _, err := transformWithBuiltins(t, postgresqlMembersApp("        - type: prune-protection\n", policies))
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	want := []string{"shop-00: db", "shop-01: db-pooler db-orders", "shop-services: api"}
	if got := groupNames(t, cluster); !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuild_PostgresqlObjectTraitsReachEveryObject: prune-protection and
// force-replace annotate every object postgresql generates, as they did when
// postgresql generated them itself; the rule forwards them to each member.
func TestBuild_PostgresqlObjectTraitsReachEveryObject(t *testing.T) {
	for _, tc := range []struct{ trait, key, value string }{
		{"prune-protection", "kustomize.toolkit.fluxcd.io/prune", "disabled"},
		{"force-replace", "kustomize.toolkit.fluxcd.io/force", "enabled"},
	} {
		t.Run(tc.trait, func(t *testing.T) {
			docs, out, err := buildDocs(t, postgresqlMembersApp("        - type: "+tc.trait+"\n", ""))
			if err != nil {
				t.Fatalf("build failed: %v\noutput: %s", err, out)
			}
			kinds := map[string]int{}
			for _, d := range docs {
				api, _ := d["apiVersion"].(string)
				if !strings.HasPrefix(api, "postgresql.cnpg.io/") && !strings.HasPrefix(api, "barmancloud.cnpg.io/") {
					continue
				}
				kind, _ := d["kind"].(string)
				kinds[kind]++
				if v, ok := docAnnotation(d, tc.key); !ok || v != tc.value {
					t.Errorf("%s %v: %s = %q (present %v), want %q", kind, d["metadata"], tc.key, v, ok, tc.value)
				}
			}
			want := map[string]int{"Cluster": 1, "ObjectStore": 1, "Pooler": 1, "Database": 2}
			for k, n := range want {
				if kinds[k] != n {
					t.Errorf("generated %d %s, want %d (all: %v)", kinds[k], k, n, kinds)
				}
			}
		})
	}
}

// TestBuild_PostgresqlDatabaseNamedLikeThePooler: a Database named "pooler"
// generates the Pooler's name; the two are different objects, as they were
// when postgresql generated them, and the document builds, with or without
// a dependency policy.
func TestBuild_PostgresqlDatabaseNamedLikeThePooler(t *testing.T) {
	for _, policies := range []string{"", postgresqlOrderPolicy} {
		docs, out, err := buildDocs(t, postgresqlMembersApp("        - type: prune-protection\n", policies))
		if err != nil {
			t.Fatalf("build failed: %v\noutput: %s", err, out)
		}
		var named, order []string
		for _, d := range docs {
			md, _ := d["metadata"].(map[string]any)
			if md["name"] == "db-pooler" {
				named = append(named, d["kind"].(string))
			}
			if name, _ := md["name"].(string); strings.HasPrefix(name, "db") {
				order = append(order, d["kind"].(string)+"/"+name)
			}
		}
		if strings.Join(named, ",") != "Pooler,Database" {
			t.Errorf("objects named db-pooler = %v, want the Pooler then the Database", named)
		}
		// The Database joins the Pooler's sibling group, so it comes right
		// after the Pooler, ahead of the other Databases.
		want := "Cluster/db,ObjectStore/db,Pooler/db-pooler,Database/db-pooler,Database/db-orders"
		if got := strings.Join(order, ","); got != want {
			t.Errorf("object order = %s, want %s", got, want)
		}
	}
}

// TestBuild_PostgresqlDependencyPolicyNameIsFree: the dependency policy the
// rule emits takes the first name no policy of the document uses, and only
// policies count: a generated component of the same name, from this
// postgresql component or another one lowered before or after it, does not
// refuse the document.
func TestBuild_PostgresqlDependencyPolicyNameIsFree(t *testing.T) {
	const api = `    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 9090
`
	const dbWithDatabase = `    - name: db
      type: postgresql
      properties:
        databases:
          - name: %s
            owner: app
`
	const dbaWithPooler = `    - name: db-a
      type: postgresql
      properties:
        pooler:
          enabled: true
`
	policy := func(name, rules string) string {
		return "    - name: " + name + "\n      type: dependency\n      properties:\n        rules:\n" + rules
	}
	apiOnDB := "          - component: api\n            dependsOn: [db]\n"
	dbaOnDB := "          - component: db-a\n            dependsOn: [db]\n"
	for _, tc := range []struct {
		name, components, policies string
	}{
		{
			name:       "authored policies take the first names",
			components: fmt.Sprintf(dbWithDatabase, "orders") + api,
			policies:   policy("db-dependencies", apiOnDB) + policy("db-dependencies-1", "          - component: api\n            dependsOn: [db-orders]\n"),
		},
		{
			name:       "a database of the same component",
			components: fmt.Sprintf(dbWithDatabase, "dependencies") + api,
			policies:   policy("order", apiOnDB),
		},
		{
			name:       "a database of a component lowered after it",
			components: dbaWithPooler + fmt.Sprintf(dbWithDatabase, "a-dependencies"),
			policies:   policy("order", dbaOnDB),
		},
		{
			name:       "a database of a component lowered before it",
			components: fmt.Sprintf(dbWithDatabase, "a-dependencies") + dbaWithPooler,
			policies:   policy("order", dbaOnDB),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: shop\n  namespace: shop\nspec:\n  components:\n" +
				tc.components + "  policies:\n" + tc.policies
			if _, out, err := buildDocs(t, app); err != nil {
				t.Fatalf("build failed: %v\noutput: %s", err, out)
			}
		})
	}
}

// TestBuild_PostgresqlLargeIntegerSurvivesLowering: an integer field the
// former typed decode accepted still builds, instead of rounding through a
// float64 in the rule's encoding of the Cluster spec into a value the
// cnpg-cluster decode refuses as overflowing. (The exact value on the
// generated Cluster is pinned in the components package; the printed YAML is
// the output serializer's.)
func TestBuild_PostgresqlLargeIntegerSurvivesLowering(t *testing.T) {
	app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: db
      type: postgresql
      properties:
        bootstrap:
          recovery:
            source: origin
        externalClusters:
          - name: origin
            barmanObjectStore:
              destinationPath: s3://bucket/origin/
              wal:
                maxParallel: 9223372036854775807
`
	if _, out, err := buildDocs(t, app); err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
}

// TestBuild_PostgresqlDefaultsIsNotATraitType: the postgresql defaults are a
// post-policy step the rule attaches to its Cluster, not a trait, so the name
// the former engine-only trait had is refused as an unknown trait type, on
// postgresql and on the cnpg-cluster kind it lowers onto.
func TestBuild_PostgresqlDefaultsIsNotATraitType(t *testing.T) {
	for _, typ := range []string{"postgresql", "cnpg-cluster"} {
		t.Run(typ, func(t *testing.T) {
			app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: db
      type: ` + typ + `
      properties: {}
      traits:
        - type: cnpg-postgresql-defaults
          properties: {}
`
			_, out, err := buildDocs(t, app)
			const want = `invalid value "cnpg-postgresql-defaults" for "type" in "db"`
			if err == nil {
				t.Fatalf("build accepted an authored cnpg-postgresql-defaults trait on %s:\n%s", typ, out)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one naming %s", err, want)
			}
		})
	}
}

// TestBuild_PostgresqlGeneratedNameCollision: a database whose generated
// component name is already a component of the document is refused, naming the
// postgresql component, the database and the authored component.
func TestBuild_PostgresqlGeneratedNameCollision(t *testing.T) {
	app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: db
      type: postgresql
      properties:
        databases:
          - name: orders
            owner: app
    - name: db-orders
      type: webservice
      properties:
        image: ghcr.io/example/orders:v1.0.0
        port: 8080
`
	_, out, err := buildDocs(t, app)
	if err == nil {
		t.Fatalf("build accepted a generated name collision:\n%s", out)
	}
	for _, want := range []string{
		`component "db" (type "postgresql")`,
		`databases[0] "orders": generates component "db-orders", which is already the name of component "db-orders" (type "webservice") in the document; rename one of them`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	}
}
