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

// What the component declares is what the build generates: each queried output
// names an object of its kind the build produced, under a hook, an authored name
// and a backup. app_secret is the one output the build does not generate:
// CloudNativePG does, from the Cluster.
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
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion, Kind: "Application",
				Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
				Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: "db", Type: "postgresql", Properties: tc.props}}},
			}
			requireOutputsBuilt(t, outputsTransformer(), app, tc.naming, "app_secret")
		})
	}
}

// roleProps is a role component's properties: the image, and a data volume
// that generates a claim beside a shared one that references an existing claim.
func roleProps(extra map[string]any) map[string]any {
	props := map[string]any{
		"image": "nginx:1",
		"volumes": []any{
			map[string]any{"name": "data-1", "type": "pvc", "mountPath": "/data", "size": "1Gi"},
			map[string]any{"name": "shared", "type": "pvc", "mountPath": "/shared", "claimName": "existing"},
			map[string]any{"name": "tmp", "type": "emptyDir", "mountPath": "/tmp"},
		},
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

// A webservice declares its Deployment, its Service, its ServiceAccount and the
// claim each generating volume gets. A volume that references a claim, and one
// that is no pvc, declare nothing.
func TestBuiltinOutputs_WebserviceDeclaresItsObjects(t *testing.T) {
	tr := outputsTransformer()
	comp := &oam.Component{Name: "web", Type: "webservice", Properties: roleProps(nil)}

	got, err := tr.ComponentOutputs(comp)
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	m := outputsByKey(t, got)
	want := map[string]oam.Output{
		"deployment":     {Key: "deployment", Kind: schema.GroupKind{Group: "apps", Kind: "Deployment"}, Name: "web"},
		"service":        {Key: "service", Kind: schema.GroupKind{Kind: "Service"}, Name: "web"},
		"serviceaccount": {Key: "serviceaccount", Kind: schema.GroupKind{Kind: "ServiceAccount"}, Name: "web"},
		"claim_data_1":   {Key: "claim_data_1", Kind: schema.GroupKind{Kind: "PersistentVolumeClaim"}, Name: "web-data--1"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("outputs = %v\nwant     %v", m, want)
	}
}

// The authored object names, the Naming hook and serviceAccountName each decide
// the output as they decide the object.
func TestBuiltinOutputs_WebserviceNamesFollowTheBuild(t *testing.T) {
	tr := outputsTransformer()
	comp := &oam.Component{Name: "web", Type: "webservice", Properties: roleProps(map[string]any{
		"deploymentObjectName": "web-deploy",
		"serviceObjectName":    "web-svc",
		"serviceAccountName":   "existing-sa",
		"volumes": []any{
			map[string]any{"name": "data-1", "type": "pvc", "mountPath": "/data", "size": "1Gi", "claimObjectName": "web-data"},
		},
	})}
	got, err := tr.ComponentOutputs(comp)
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	m := outputsByKey(t, got)
	if m["deployment"].Name != "web-deploy" || m["service"].Name != "web-svc" || m["claim_data_1"].Name != "web-data" {
		t.Errorf("authored names: %v", got)
	}
	if _, has := m["serviceaccount"]; has {
		t.Errorf("serviceAccountName names an existing account, yet a serviceaccount output is declared: %v", got)
	}

	hooked := &oam.Component{Name: "web", Type: "webservice", Properties: roleProps(nil)}
	got, err = tr.ComponentOutputsNamed("shop", hooked, func(r oam.NameRequest) (string, bool) { return "h-" + r.Default, true })
	if err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	m = outputsByKey(t, got)
	if m["deployment"].Name != "h-web" || m["service"].Name != "h-web" || m["serviceaccount"].Name != "h-web" || m["claim_data_1"].Name != "h-web-data--1" {
		t.Errorf("hooked names: %v", got)
	}
}

// A worker is a webservice without the Service.
func TestBuiltinOutputs_WorkerDeclaresNoService(t *testing.T) {
	tr := outputsTransformer()
	got, err := tr.ComponentOutputs(&oam.Component{Name: "jobs", Type: "worker", Properties: roleProps(nil)})
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	m := outputsByKey(t, got)
	if _, has := m["service"]; has || len(m) != 3 {
		t.Errorf("a worker declares %v, want deployment, serviceaccount and claim_data_1", got)
	}
	// The claim's default escapes the volume name's hyphen (escapeForPVCQualification).
	if m["claim_data_1"].Name != "jobs-data--1" {
		t.Errorf("claim_data_1 = %v, want jobs-data--1", m["claim_data_1"])
	}
}

// Both refuse what the lowering refuses, and a serviceAccountObjectName beside
// serviceAccountName is the same refusal.
func TestBuiltinOutputs_RoleRulesRefuseWhatTheLoweringRefuses(t *testing.T) {
	tr := outputsTransformer()
	if _, err := tr.ComponentOutputs(&oam.Component{Name: "web", Type: "webservice", Properties: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("a webservice without an image: err = %v, want one naming image", err)
	}
	both := &oam.Component{Name: "web", Type: "webservice", Properties: roleProps(map[string]any{
		"serviceAccountName": "sa", "serviceAccountObjectName": "other",
	})}
	if _, err := tr.ComponentOutputs(both); err == nil || !strings.Contains(err.Error(), "serviceAccountName") {
		t.Errorf("serviceAccountName beside serviceAccountObjectName: err = %v, want the refusal", err)
	}
}

// What a webservice and a worker declare is what the build generates, under the
// hook, authored names and a generated claim.
func TestBuiltinOutputs_RoleBuildAgreesWithTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    string
		props  map[string]any
		naming func(oam.NameRequest) (string, bool)
	}{
		{"webservice defaults", "webservice", roleProps(nil), nil},
		{"webservice hooked", "webservice", roleProps(nil), func(r oam.NameRequest) (string, bool) { return "h-" + r.Default, true }},
		{"webservice authored", "webservice", roleProps(map[string]any{"deploymentObjectName": "d", "serviceObjectName": "s", "serviceAccountObjectName": "a"}), nil},
		{"worker defaults", "worker", roleProps(nil), nil},
		{"worker hooked", "worker", roleProps(nil), func(r oam.NameRequest) (string, bool) { return "h-" + r.Default, true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion, Kind: "Application",
				Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
				Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: "app", Type: tc.typ, Properties: tc.props}}},
			}
			requireOutputsBuilt(t, outputsTransformer(), app, tc.naming)
		})
	}
}

// builtObject is a (kind, name) of an object the build generated.
type builtObject struct{ kind, name string }

// builtObjects transforms app and returns the kind and name of every object it
// generated, read from the objects themselves.
func builtObjects(t *testing.T, tr *oam.Transformer, app *oam.Application, naming func(oam.NameRequest) (string, bool)) map[builtObject]bool {
	t.Helper()
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Naming: naming})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	built := map[builtObject]bool{}
	for _, a := range apps {
		for _, o := range a.Objects {
			obj := *o
			kind := obj.GetObjectKind().GroupVersionKind().Kind
			if kind == "" {
				kind = reflect.TypeOf(obj).Elem().Name()
			}
			built[builtObject{kind, obj.GetName()}] = true
		}
	}
	return built
}

// requireOutputsBuilt holds what the component declares, read through
// ComponentOutputsNamed, against the objects the build generated: each output
// but those in notBuilt names an object of its kind that exists. notBuilt are
// outputs of objects another controller generates.
func requireOutputsBuilt(t *testing.T, tr *oam.Transformer, app *oam.Application, naming func(oam.NameRequest) (string, bool), notBuilt ...string) {
	t.Helper()
	built := builtObjects(t, tr, app, naming)
	comp := &app.Spec.Components[0]
	outs, err := tr.ComponentOutputsNamed(app.Metadata.Name, comp, naming)
	if err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	if len(outs) == 0 {
		t.Fatalf("the component declared no output: the comparison would be vacuous")
	}
	skip := map[string]bool{}
	for _, k := range notBuilt {
		skip[k] = true
	}
	for _, o := range outs {
		if skip[o.Key] {
			continue
		}
		if !built[builtObject{o.Kind.Kind, o.Name}] {
			t.Errorf("output %q declares %s %q, but the build generated %v", o.Key, o.Kind.Kind, o.Name, built)
		}
	}
}

// A configmap and a secret answer only for a component their own parse
// accepts, as every other builtin does.
func TestBuiltinOutputs_KindComponentsRefuseWhatTheParseRefuses(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		props map[string]any
		want  string
	}{
		{"configmap", map[string]any{"data": map[string]any{"x": 1}}, "data.x: must be a string"},
		{"secret", map[string]any{"type": 42}, "type"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			comp := &oam.Component{Name: "cfg", Type: tc.typ, Properties: tc.props}
			if _, err := outputsTransformer().ComponentOutputs(comp); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ComponentOutputs = %v; want the parse error containing %q", err, tc.want)
			}
		})
	}
}

// What a kind component declares is the object the build generates.
func TestBuiltinOutputs_KindBuildGeneratesTheDeclaredObject(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		props  map[string]any
		naming func(oam.NameRequest) (string, bool)
	}{
		{"configmap", map[string]any{"data": map[string]any{"a": "b"}}, nil},
		{"configmap", map[string]any{"data": map[string]any{"a": "b"}}, hookNaming(oam.NameRoleObject)},
		{"configmap", map[string]any{"objectName": "authored", "data": map[string]any{"a": "b"}}, nil},
		{"secret", map[string]any{"stringData": map[string]any{"a": "b"}}, nil},
		{"secret", map[string]any{"stringData": map[string]any{"a": "b"}}, hookNaming(oam.NameRoleObject)},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion, Kind: "Application",
				Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
				Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: "cfg", Type: tc.typ, Properties: tc.props}}},
			}
			requireOutputsBuilt(t, outputsTransformer(), app, tc.naming)
		})
	}
}
