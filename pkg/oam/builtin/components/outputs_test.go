package components_test

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// These tests pin the outputs the builtin components declare
// (oam.OutputProvider, go-kure/launcher#1259): which keys, which kinds, which
// names, and that a build under the same inputs gives the objects those names.

// outputsTransformer registers every builtin, as a consumer does.
func outputsTransformer() *oam.Transformer {
	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		t.RegisterComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		t.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		t.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		t.RegisterBuiltinTraitLowering(r)
	}
	return t
}

func outputsByKey(t *testing.T, outs []oam.Output) map[string]oam.Output {
	t.Helper()
	m := map[string]oam.Output{}
	for _, o := range outs {
		if _, dup := m[o.Key]; dup {
			t.Fatalf("key %q declared twice in %v", o.Key, outs)
		}
		m[o.Key] = o
	}
	return m
}

// hookNaming prefixes the name of one role, as a consumer's Naming hook does.
func hookNaming(role oam.NameRole) func(oam.NameRequest) (string, bool) {
	return func(r oam.NameRequest) (string, bool) {
		if r.Role != role {
			return "", false
		}
		return "h-" + r.Default, true
	}
}

// A configmap and a secret declare one output, "object": the object's kind and
// the name the build gives it, the component name unless the Naming hook says
// otherwise.
func TestBuiltinOutputs_KindComponentsDeclareObject(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		kind string
	}{
		{"configmap", "ConfigMap"},
		{"secret", "Secret"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			tr := outputsTransformer()
			comp := &oam.Component{Name: "cfg", Type: tc.typ, Properties: map[string]any{}}
			want := []oam.Output{{Key: "object", Kind: schema.GroupKind{Kind: tc.kind}, Name: "cfg"}}

			got, err := tr.ComponentOutputs(comp)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ComponentOutputs = %v, %v; want %v", got, err, want)
			}

			got, err = tr.ComponentOutputsNamed("app", comp, hookNaming(oam.NameRoleObject))
			want[0].Name = "h-cfg"
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ComponentOutputsNamed = %v, %v; want %v", got, err, want)
			}
		})
	}
}

// A postgresql component declares the Cluster, the Secret CloudNativePG
// generates for the application user (<cluster>-app), and with backup the
// ObjectStore. The Secret follows the Cluster's name, whether the author or the
// hook names it.
func TestBuiltinOutputs_PostgresqlDeclaresClusterSecretAndStore(t *testing.T) {
	tr := outputsTransformer()
	comp := &oam.Component{Name: "db", Type: "postgresql", Properties: backupWith(nil, nil)}

	got, err := tr.ComponentOutputs(comp)
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	m := outputsByKey(t, got)
	if len(m) != 3 {
		t.Fatalf("outputs = %v, want cluster, app_secret and objectstore", got)
	}
	if o := m["cluster"]; o.Kind.Kind != "Cluster" || o.Name != "db" {
		t.Errorf("cluster = %v, want a Cluster named db", o)
	}
	if o := m["app_secret"]; o.Kind != (schema.GroupKind{Kind: "Secret"}) || o.Name != "db-app" {
		t.Errorf("app_secret = %v, want the Secret db-app", o)
	}
	if o := m["objectstore"]; o.Kind.Kind != "ObjectStore" || o.Name != "db" {
		t.Errorf("objectstore = %v, want an ObjectStore named db", o)
	}

	comp.Properties["clusterObjectName"] = "pg"
	got, err = tr.ComponentOutputs(comp)
	if err != nil {
		t.Fatalf("ComponentOutputs with clusterObjectName: %v", err)
	}
	m = outputsByKey(t, got)
	if m["cluster"].Name != "pg" || m["app_secret"].Name != "pg-app" {
		t.Errorf("authored clusterObjectName: cluster %q, app_secret %q; want pg, pg-app", m["cluster"].Name, m["app_secret"].Name)
	}

	got, err = tr.ComponentOutputsNamed("app", &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{}}, hookNaming(oam.NameRolePostgresqlCluster))
	if err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	m = outputsByKey(t, got)
	if m["cluster"].Name != "h-db" || m["app_secret"].Name != "h-db-app" {
		t.Errorf("hooked: cluster %q, app_secret %q; want h-db, h-db-app", m["cluster"].Name, m["app_secret"].Name)
	}
	if _, has := m["objectstore"]; has {
		t.Errorf("a component with no backup declares an objectstore: %v", got)
	}
}

// With recovery.secret.name the operator uses that Secret and generates none,
// so app_secret is not declared.
func TestBuiltinOutputs_PostgresqlRecoverySecretDropsAppSecret(t *testing.T) {
	tr := outputsTransformer()
	comp := &oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{
		"recovery": recoveryFrom("secret", map[string]any{"name": "creds"}),
	}}
	got, err := tr.ComponentOutputs(comp)
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	m := outputsByKey(t, got)
	if _, has := m["app_secret"]; has {
		t.Errorf("app_secret is declared although recovery.secret.name names the Secret: %v", got)
	}
	if m["cluster"].Name != "db" {
		t.Errorf("cluster = %v, want db", m["cluster"])
	}
}

// A declaration that fails to parse is the lowering's error, not an empty list.
func TestBuiltinOutputs_PostgresqlRefusesWhatTheLoweringRefuses(t *testing.T) {
	tr := outputsTransformer()
	comp := &oam.Component{Name: "db", Type: "postgresql", Properties: backupWith(nil, map[string]any{"tags": map[string]any{}})}
	if _, err := tr.ComponentOutputs(comp); err == nil || !strings.Contains(err.Error(), `backup: unrecognized key "tags"`) {
		t.Fatalf("ComponentOutputs = %v; want the parse error naming the key", err)
	}
}

// The check inside Transform holds what the components declare against what the
// build names, so a build that succeeds under a hook, an authored name and a
// backup is the proof that the prediction and the objects agree.
func TestBuiltinOutputs_BuildAgreesWithTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		props  map[string]any
		naming func(oam.NameRequest) (string, bool)
	}{
		{"defaults", map[string]any{}, nil},
		{"authored cluster name", map[string]any{"clusterObjectName": "pg"}, nil},
		{"hooked cluster name", map[string]any{}, hookNaming(oam.NameRolePostgresqlCluster)},
		{"backup and a hooked store", backupWith(nil, nil), hookNaming(oam.NameRolePostgresqlObjectStore)},
		{"recovery secret", map[string]any{"recovery": recoveryFrom("secret", map[string]any{"name": "creds"})}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := outputsTransformer()
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion, Kind: "Application",
				Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
				Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: "db", Type: "postgresql", Properties: tc.props}}},
			}
			if _, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Naming: tc.naming}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
		})
	}
}
