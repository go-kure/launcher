package components_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPostgresqlRule_PropertySchemaUnchanged pins the rule's schema byte for byte
// against the one the former PostgresqlHandler published, captured before
// postgresql became a lowering rule.
func TestPostgresqlRule_PropertySchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("testdata/postgresql-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	got, err := json.MarshalIndent(components.PostgresqlRule{}.PropertySchema(), "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Error("the rule's schema differs from the one the former handler published")
	}
}

func lowerPostgresql(t *testing.T, comp *oam.Component, doc *oam.Application) oam.LoweringResult {
	t.Helper()
	res, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	return res
}

// The rule emits the Cluster, the ObjectStore under the same name, the Pooler
// and one Database per entry, in that order. The defaults are a post-policy
// step on the Cluster, not a trait, so the Cluster carries only the authored
// traits. Every component gets its own copy of the annotations.
func TestPostgresqlRule_Emission(t *testing.T) {
	comp := &oam.Component{
		Name: "db",
		Type: "postgresql",
		Properties: map[string]any{
			"pooler":      map[string]any{"enabled": true},
			"objectStore": map[string]any{"destinationPath": "s3://bucket/db/"},
			"databases": []any{
				map[string]any{"name": "orders", "owner": "app"},
				map[string]any{"name": "billing", "owner": "app"},
			},
		},
		Traits:      []oam.Trait{{Type: "prune-protection", Properties: map[string]any{}}},
		Annotations: map[string]string{"a": "b"},
	}
	res := lowerPostgresql(t, comp, nil)

	type nt struct{ name, typ string }
	var got []nt
	for _, c := range res.Components {
		got = append(got, nt{c.Name, c.Type})
	}
	want := []nt{
		{"db", "cnpg-cluster"},
		{"db", "cnpg-objectstore"},
		{"db-pooler", "cnpg-pooler"},
		{"db-orders", "cnpg-database"},
		{"db-billing", "cnpg-database"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted %v, want %v", got, want)
	}

	// The defaults are a post-policy step, not a trait: the Cluster carries the
	// authored trait only, and prune-protection decorates every object, so every
	// member carries it.
	for _, c := range res.Components {
		if len(c.Traits) != 1 || c.Traits[0].Type != "prune-protection" {
			t.Errorf("%s %q carries traits %+v, want the authored prune-protection", c.Type, c.Name, c.Traits)
		}
	}
	for i, c := range res.Components {
		if !reflect.DeepEqual(c.Annotations, comp.Annotations) {
			t.Errorf("component %d annotations = %v, want %v", i, c.Annotations, comp.Annotations)
		}
	}
	res.Components[0].Annotations["a"] = "changed"
	if comp.Annotations["a"] != "b" || res.Components[1].Annotations["a"] != "b" {
		t.Error("an emitted component shares the authored annotations map")
	}
	if len(res.Policies) != 0 {
		t.Errorf("emitted policies %+v without a document dependency policy", res.Policies)
	}
}

// instances and storage.size are written only when authored, so the policy
// default and the post-policy step's fallback apply exactly as they applied to
// replicas and storageSize.
func TestPostgresqlRule_InstancesAndStorageOnlyWhenAuthored(t *testing.T) {
	unauthored := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{}}, nil).Components[0].Properties
	if _, ok := unauthored["instances"]; ok {
		t.Errorf("unauthored replicas wrote instances: %v", unauthored["instances"])
	}
	// spec.storage is always encoded (not omitempty); only its size matters.
	if storage, _ := unauthored["storage"].(map[string]any); storage["size"] != nil {
		t.Errorf("unauthored storageSize wrote storage.size: %v", storage["size"])
	}

	authored := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"replicas": 3, "storageSize": "5Gi"}}, nil).Components[0].Properties
	if authored["instances"] != int64(3) {
		t.Errorf("instances = %#v, want 3", authored["instances"])
	}
	if storage, _ := authored["storage"].(map[string]any); storage["size"] != "5Gi" {
		t.Errorf("storage = %#v, want size 5Gi", authored["storage"])
	}
}

// A generated name already used by a component of the document is refused,
// naming the generated name, the authored component and its type, and the
// postgresql property it came from. (The engine prefixes the postgresql
// component itself; see the kurel build test.)
func TestPostgresqlRule_RefusesGeneratedNameCollision(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		taken string
		want  string
	}{
		{
			"pooler",
			map[string]any{"pooler": map[string]any{"enabled": true}},
			"db-pooler",
			`pooler: generates component "db-pooler", which is already the name of component "db-pooler" (type "webservice") in the document; rename one of them`,
		},
		{
			"database",
			map[string]any{"databases": []any{map[string]any{"name": "orders", "owner": "app"}}},
			"db-orders",
			`databases[0] "orders": generates component "db-orders", which is already the name of component "db-orders" (type "webservice") in the document; rename one of them`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			doc := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
				comp,
				{Name: tc.taken, Type: "webservice"},
			}}}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A database name repeated in the list is refused at parse time, naming both
// entries: each entry is one Database object, so the repeat was one object
// authored twice (the Flux build refused it; kubectl kept the last).
func TestPostgresqlRule_RefusesRepeatedDatabaseName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			"orders",
			map[string]any{"databases": []any{
				map[string]any{"name": "orders", "owner": "app"},
				map[string]any{"name": "billing", "owner": "app"},
				map[string]any{"name": "orders", "owner": "app"},
			}},
			`databases[2]: repeats the name "orders" of databases[0]; each database is one object`,
		},
		{
			"pooler, with the pooler enabled",
			map[string]any{"pooler": map[string]any{"enabled": true}, "databases": []any{
				map[string]any{"name": "pooler", "owner": "app"},
				map[string]any{"name": "pooler", "owner": "app"},
			}},
			`databases[1]: repeats the name "pooler" of databases[0]; each database is one object`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// The rule orders the Pooler and the Databases after the Cluster with a
// dependency policy only when the document already orders its components: a
// dependency edge splits the application into ordered groups, which would order
// a document that never asked for ordering.
func TestPostgresqlRule_DependencyPolicyOnlyWhenTheDocumentOrders(t *testing.T) {
	comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"pooler":    map[string]any{"enabled": true},
		"databases": []any{map[string]any{"name": "orders", "owner": "app"}},
	}}

	unordered := &oam.Application{Spec: oam.ApplicationSpec{
		Components: []oam.Component{comp},
		Policies:   []oam.ApplicationPolicy{{Name: "where", Type: "placement", Properties: map[string]any{}}},
	}}
	if res := lowerPostgresql(t, &comp, unordered); len(res.Policies) != 0 {
		t.Errorf("document without a dependency policy: emitted %+v, want none", res.Policies)
	}

	ordered := &oam.Application{Spec: oam.ApplicationSpec{
		Components: []oam.Component{comp, {Name: "api", Type: "webservice"}},
		Policies: []oam.ApplicationPolicy{{Name: "order", Type: "dependency", Properties: map[string]any{
			"rules": []any{map[string]any{"component": "api", "dependsOn": []any{"db"}}},
		}}},
	}}
	res := lowerPostgresql(t, &comp, ordered)
	want := []oam.ApplicationPolicy{{Name: "db-dependencies", Type: "dependency", Properties: map[string]any{
		"rules": []any{
			map[string]any{"component": "db-pooler", "dependsOn": []any{"db"}},
			map[string]any{"component": "db-orders", "dependsOn": []any{"db"}},
			// api waited for every object of db; it now waits for each member.
			map[string]any{"component": "api", "dependsOn": []any{"db-pooler", "db-orders"}},
		},
	}}}
	if !reflect.DeepEqual(res.Policies, want) {
		t.Errorf("policies = %+v, want %+v", res.Policies, want)
	}

	// The name skips those the document's policies use, and only those: a
	// component named db-dependencies does not move it.
	taken := &oam.Application{Spec: oam.ApplicationSpec{
		Components: append(append([]oam.Component{}, ordered.Spec.Components...), oam.Component{Name: "db-dependencies", Type: "webservice"}),
		Policies: append(append([]oam.ApplicationPolicy{}, ordered.Spec.Policies...),
			oam.ApplicationPolicy{Name: "db-dependencies", Type: "placement"},
			oam.ApplicationPolicy{Name: "db-dependencies-1", Type: "placement"}),
	}}
	if res := lowerPostgresql(t, &comp, taken); len(res.Policies) != 1 || res.Policies[0].Name != "db-dependencies-2" {
		t.Errorf("policies with db-dependencies and db-dependencies-1 taken = %+v, want one named db-dependencies-2", res.Policies)
	}

	// Nothing to order: no Pooler, no Databases.
	bare := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{}}
	if res := lowerPostgresql(t, &bare, ordered); len(res.Policies) != 0 {
		t.Errorf("no pooler or databases: emitted %+v, want none", res.Policies)
	}
}

// A trait that configures the component's bundle is forwarded once per bundle
// the members land in. Without a dependency policy that is the Cluster's own
// bundle, which carries the trait already. Under one, every member waits for
// the Cluster alone, so they share the next group: its first member carries the
// trait, and no other does, or a consumer's handler would configure that one
// bundle once per member. A trait that decorates objects reaches every member.
func TestPostgresqlRule_BundleTraitOncePerBundle(t *testing.T) {
	comp := oam.Component{Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"pooler":      map[string]any{"enabled": true},
			"objectStore": map[string]any{"destinationPath": "s3://backups/db"},
			"databases": []any{
				map[string]any{"name": "orders", "owner": "app"},
				map[string]any{"name": "billing", "owner": "app"},
			},
		},
		Traits: []oam.Trait{
			{Type: "fluxcd-patches", Properties: map[string]any{}},
			{Type: "prune-protection", Properties: map[string]any{}},
		},
	}
	carriers := func(res oam.LoweringResult, traitType string) []string {
		var out []string
		for _, c := range res.Components {
			if slices.ContainsFunc(c.Traits, func(tr oam.Trait) bool { return tr.Type == traitType }) {
				out = append(out, c.Type+"/"+c.Name)
			}
		}
		return out
	}
	everyObject := []string{"cnpg-cluster/db", "cnpg-objectstore/db", "cnpg-pooler/db-pooler", "cnpg-database/db-orders", "cnpg-database/db-billing"}

	unordered := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp}}}
	res := lowerPostgresql(t, &comp, unordered)
	if got, want := carriers(res, "fluxcd-patches"), []string{"cnpg-cluster/db"}; !slices.Equal(got, want) {
		t.Errorf("unordered: bundle trait on %v, want %v", got, want)
	}
	if got := carriers(res, "prune-protection"); !slices.Equal(got, everyObject) {
		t.Errorf("unordered: object trait on %v, want %v", got, everyObject)
	}

	ordered := &oam.Application{Spec: oam.ApplicationSpec{
		Components: []oam.Component{comp, {Name: "api", Type: "webservice"}},
		Policies: []oam.ApplicationPolicy{{Name: "order", Type: "dependency", Properties: map[string]any{
			"rules": []any{map[string]any{"component": "api", "dependsOn": []any{"db"}}},
		}}},
	}}
	res = lowerPostgresql(t, &comp, ordered)
	if got, want := carriers(res, "fluxcd-patches"), []string{"cnpg-cluster/db", "cnpg-pooler/db-pooler"}; !slices.Equal(got, want) {
		t.Errorf("ordered: bundle trait on %v, want %v: the Cluster's bundle and the members' one group, once each", got, want)
	}
	if got := carriers(res, "prune-protection"); !slices.Equal(got, everyObject) {
		t.Errorf("ordered: object trait on %v, want %v", got, everyObject)
	}

	// A policy of the document that orders or places one member on its own
	// could move it out of the members' group, where the one forwarded copy
	// does not reach: refused, rather than the trait silently lost there.
	lower := func(c oam.Component, policies ...oam.ApplicationPolicy) error {
		doc := &oam.Application{Spec: oam.ApplicationSpec{
			Components: ordered.Spec.Components,
			Policies:   append(slices.Clone(ordered.Spec.Policies), policies...),
		}}
		_, err := components.PostgresqlRule{}.LowerComponent(&c, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
		return err
	}
	memberAfterMember := oam.ApplicationPolicy{Name: "members", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": "db-orders", "dependsOn": []any{"db-pooler"}}},
	}}
	memberPlaced := oam.ApplicationPolicy{Name: "pooler-last", Type: "placement", Properties: map[string]any{"component": "db-pooler", "tier": "apps"}}
	for _, tc := range []struct {
		policy oam.ApplicationPolicy
		want   string
	}{
		{memberAfterMember, `trait "fluxcd-patches" configures the bundle of every object this component generates, but dependency policy "members" names "db-orders", a component generated for it, on its own`},
		{memberPlaced, `trait "fluxcd-patches" configures the bundle of every object this component generates, but placement policy "pooler-last" names "db-pooler", a component generated for it, on its own`},
	} {
		if err := lower(comp, tc.policy); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("policy %q: error = %v, want it to contain %q", tc.policy.Name, err, tc.want)
		}
	}
	// So is a placement of a member in a document that orders nothing: there
	// the members are in the Cluster's bundle, which carries the trait, and the
	// placement could move the member out of it.
	placedOnly := &oam.Application{Spec: oam.ApplicationSpec{
		Components: unordered.Spec.Components,
		Policies:   []oam.ApplicationPolicy{memberPlaced},
	}}
	c := comp
	_, err := components.PostgresqlRule{}.LowerComponent(&c, oam.LoweringContext{Document: placedOnly, Namer: oam.NewNameAllocator()})
	if want := `placement policy "pooler-last" names "db-pooler", a component generated for it, on its own`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("placement without a dependency policy: error = %v, want it to contain %q", err, want)
	}
	// Without a bundle trait nothing is forwarded per bundle, so a member may
	// be ordered or placed on its own; so may another component after a member.
	plain := comp
	plain.Traits = []oam.Trait{{Type: "prune-protection", Properties: map[string]any{}}}
	if err := lower(plain, memberAfterMember, memberPlaced); err != nil {
		t.Errorf("no bundle trait: %v, want the member policies accepted", err)
	}
	afterMember := oam.ApplicationPolicy{Name: "api-late", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": "api", "dependsOn": []any{"db-orders"}}},
	}}
	if err := lower(comp, afterMember); err != nil {
		t.Errorf("another component after a member: %v, want it accepted", err)
	}
}

// postgresqlDefaultsCluster builds a cnpg-cluster config from props, applies p,
// then the postgresql defaults, as the engine does for the Cluster the rule emits.
func postgresqlDefaultsCluster(t *testing.T, props map[string]any, p oam.Policy) (*components.CnpgClusterConfig, error) {
	t.Helper()
	cfg, err := (&components.CnpgClusterHandler{}).ToApplicationConfig(&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	cc := cfg.(*components.CnpgClusterConfig)
	if p != nil {
		if err := cc.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
	}
	return cc, cc.ApplyPostgresqlDefaults()
}

// The 1Gi fallback is set only when nothing else sized the Cluster, and the
// policy maximum is enforced on it with postgresql's text: ApplyPolicy runs
// before the fallback exists, so it cannot.
func TestCnpgClusterConfig_ApplyPostgresqlDefaults_Storage(t *testing.T) {
	t.Run("unauthored fallback above the maximum is refused", func(t *testing.T) {
		_, err := postgresqlDefaultsCluster(t, map[string]any{}, &stubPolicy{maxStorageSize: "512Mi"})
		const want = `storageSize "1Gi" exceeds enforced maximum "512Mi"`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
	t.Run("authored size under the maximum is kept", func(t *testing.T) {
		cc, err := postgresqlDefaultsCluster(t, map[string]any{"storage": map[string]any{"size": "500Mi"}}, &stubPolicy{maxStorageSize: "512Mi"})
		if err != nil {
			t.Fatalf("ApplyPostgresqlDefaults: %v", err)
		}
		if got := cc.Spec.StorageConfiguration.Size; got != "500Mi" {
			t.Errorf("size = %q, want 500Mi", got)
		}
	})
	t.Run("policy default wins over the fallback", func(t *testing.T) {
		cc, err := postgresqlDefaultsCluster(t, map[string]any{}, &stubPolicy{defaultStorageSize: "2Gi", maxStorageSize: "4Gi"})
		if err != nil {
			t.Fatalf("ApplyPostgresqlDefaults: %v", err)
		}
		if got := cc.Spec.StorageConfiguration.Size; got != "2Gi" {
			t.Errorf("size = %q, want 2Gi", got)
		}
	})
	t.Run("no policy falls back to 1Gi", func(t *testing.T) {
		cc, err := postgresqlDefaultsCluster(t, map[string]any{}, nil)
		if err != nil {
			t.Fatalf("ApplyPostgresqlDefaults: %v", err)
		}
		if got := cc.Spec.StorageConfiguration.Size; got != "1Gi" {
			t.Errorf("size = %q, want 1Gi", got)
		}
	})
}

// enablePDB follows the instance count after the policy, and a Cluster that
// already sets it is refused.
func TestCnpgClusterConfig_ApplyPostgresqlDefaults_EnablePDB(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		p     oam.Policy
		want  bool
	}{
		{"one instance", map[string]any{}, nil, false},
		{"authored three", map[string]any{"instances": 3}, nil, true},
		{"policy default three", map[string]any{}, &stubPolicy{defaultReplicas: int32ptr(3)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cc, err := postgresqlDefaultsCluster(t, tc.props, tc.p)
			if err != nil {
				t.Fatalf("ApplyPostgresqlDefaults: %v", err)
			}
			if cc.Spec.EnablePDB == nil || *cc.Spec.EnablePDB != tc.want {
				t.Errorf("enablePDB = %v, want %v", cc.Spec.EnablePDB, tc.want)
			}
		})
	}
	t.Run("already set", func(t *testing.T) {
		_, err := postgresqlDefaultsCluster(t, map[string]any{"enablePDB": false}, nil)
		const want = "enablePDB is already set; the postgresql defaults decide it from the instance count"
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
}

// An integer above 2^53 keeps its exact value from the authored property to
// the generated Cluster: the rule's encoding of the spec does not round it
// through a float64 into a value the Cluster's decode refuses.
func TestPostgresqlRule_LargeIntegerReachesTheCluster(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"bootstrap": map[string]any{"recovery": map[string]any{"source": "origin"}},
		"externalClusters": []any{map[string]any{
			"name": "origin",
			"barmanObjectStore": map[string]any{
				"destinationPath": "s3://bucket/origin/",
				"wal":             map[string]any{"maxParallel": int64(math.MaxInt64)},
			},
		}},
	})
	cluster := (*generatePostgresql(t, pc)[0]).(*cnpgv1.Cluster)
	got := cluster.Spec.ExternalClusters[0].BarmanObjectStore.Wal.MaxParallel
	if got != math.MaxInt64 {
		t.Errorf("maxParallel = %d, want %d", got, int64(math.MaxInt64))
	}
}
