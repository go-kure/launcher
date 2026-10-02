package kurel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
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

// deliveryKustomizations runs a delivery build of app and returns the spec of
// each Flux Kustomization it writes, by name.
func deliveryKustomizations(t *testing.T, app string) map[string]map[string]any {
	t.Helper()
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", app)
	outDir := filepath.Join(dir, "out")
	if _, err := runKurel(t, "build", appPath, "--profile", filepath.Join(deliveryTestdata, "cluster.yaml"),
		"-o", outDir, "--oci-repository", testOCIRepository, "--oci-tag", "v1.0.0"); err != nil {
		t.Fatalf("delivery build: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "shop.flux.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ks := map[string]map[string]any{}
	for _, raw := range strings.Split(string(data), "\n---\n") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding %s: %v", raw, err)
		}
		if obj["kind"] != "Kustomization" {
			continue
		}
		md, _ := obj["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		ks[name], _ = obj["spec"].(map[string]any)
	}
	return ks
}

// dependsOnNames lists the Kustomizations spec depends on, in order.
func dependsOnNames(spec map[string]any) []string {
	deps, _ := spec["dependsOn"].([]any)
	names := make([]string, 0, len(deps))
	for _, d := range deps {
		m, _ := d.(map[string]any)
		name, _ := m["name"].(string)
		names = append(names, name)
	}
	return names
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
// were; with one, the members' bundles are in the Cluster's tier, so the tier
// order adds no edge back to them (it made a cycle) and fluxcd-postbuild
// reaches each of them.
func TestBuild_PostgresqlPlacementReachesEveryMember(t *testing.T) {
	postbuild := `        - type: fluxcd-patches
          properties:
            patches:
              - patch: |
                  - op: add
                    path: /metadata/labels/patched
                    value: "yes"
                target:
                  group: postgresql.cnpg.io
        - type: fluxcd-postbuild
          properties:
            substitute:
              REGION: eu-west-1
`
	t.Run("without a dependency policy", func(t *testing.T) {
		ks := deliveryKustomizations(t, postgresqlMembersApp(postbuild, "  policies:\n"+postgresqlPlacementPolicy))
		if len(ks) != 1 || ks["shop"] == nil {
			t.Fatalf("Kustomizations = %v, want the one bundle shop", keysOf(ks))
		}
		// The members share the Cluster's bundle, which carries the patch once.
		if patches, _ := ks["shop"]["patches"].([]any); len(patches) != 1 {
			t.Errorf("shop patches = %v, want the one authored patch", ks["shop"]["patches"])
		}
	})
	t.Run("with a dependency policy", func(t *testing.T) {
		ks := deliveryKustomizations(t, postgresqlMembersApp(postbuild, postgresqlOrderPolicy+postgresqlPlacementPolicy))
		for _, name := range []string{"shop-db", "shop-db-pooler", "shop-db-orders"} {
			pb, _ := ks[name]["postBuild"].(map[string]any)
			if sub, _ := pb["substitute"].(map[string]any); sub["REGION"] != "eu-west-1" {
				t.Errorf("%s postBuild = %v, want the authored substitute (have %v)", name, ks[name]["postBuild"], keysOf(ks))
			}
		}
	})
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
// for the postgresql component waited for the one bundle of all its objects;
// it now waits for each member's bundle too, also when it shares the tier, so
// no tier order adds those edges.
func TestBuild_PostgresqlDependentWaitsForEveryMember(t *testing.T) {
	policies := postgresqlOrderPolicy + `    - name: where
      type: placement
      properties:
        component: api
        tier: services
`
	ks := deliveryKustomizations(t, postgresqlMembersApp("        - type: prune-protection\n", policies))
	got := strings.Join(dependsOnNames(ks["shop-api"]), ",")
	if want := "shop-db,shop-db-pooler,shop-db-orders"; got != want {
		t.Errorf("shop-api dependsOn = %s, want %s", got, want)
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

// TestBuild_PostgresqlBundleTraitsReachEveryBundle: under a dependency policy
// the Pooler and each Database get bundles of their own, so fluxcd-patches and
// fluxcd-postbuild, which used to act on the one bundle holding all of
// postgresql's objects, reach each of those Kustomizations. The Database named
// like the Pooler shares the Pooler's bundle, which carries the patch once.
func TestBuild_PostgresqlBundleTraitsReachEveryBundle(t *testing.T) {
	traits := `        - type: fluxcd-patches
          properties:
            patches:
              - patch: |
                  - op: add
                    path: /metadata/labels/patched
                    value: "yes"
                target:
                  group: postgresql.cnpg.io
        - type: fluxcd-postbuild
          properties:
            substitute:
              REGION: eu-west-1
`
	ks := deliveryKustomizations(t, postgresqlMembersApp(traits, postgresqlOrderPolicy))
	for _, name := range []string{"shop-db", "shop-db-pooler", "shop-db-orders"} {
		spec, ok := ks[name]
		if !ok {
			t.Errorf("no Kustomization %s (have %v)", name, keysOf(ks))
			continue
		}
		if patches, _ := spec["patches"].([]any); len(patches) != 1 {
			t.Errorf("%s patches = %v, want the one authored patch", name, spec["patches"])
		}
		pb, _ := spec["postBuild"].(map[string]any)
		if sub, _ := pb["substitute"].(map[string]any); sub["REGION"] != "eu-west-1" {
			t.Errorf("%s postBuild = %v, want the authored substitute", name, spec["postBuild"])
		}
	}
	if spec, ok := ks["shop-api"]; ok && (spec["patches"] != nil || spec["postBuild"] != nil) {
		t.Errorf("shop-api got postgresql's bundle traits: %v", spec)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
