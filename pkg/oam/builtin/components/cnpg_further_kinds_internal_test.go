package components

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// cnpgFurtherRule is one expression rule of a CRD a kind checks: properties
// that break it, and the refusal.
type cnpgFurtherRule struct {
	props map[string]any
	want  string
}

// cnpgFurtherTransitionRule is why a rule that reads oldSelf is left to the
// API server: it compares the object with the stored one, and a build has
// none.
const cnpgFurtherTransitionRule = "a transition rule: it compares a field with its value on the stored object, which a build does not have"

// cnpgFurtherKinds lists seven kind components of the CloudNativePG API
// (go-kure/launcher#790), each with the CRD it emits an object of, the spec
// type it decodes into and its required list. The CRDs are the linked
// module's.
//
// checked names the CRD's expression rules the kind's validate holds, by the
// path of the value they are declared on and their text, each with a case that
// breaks it; left names the ones the kind leaves to the API server, each with
// the reason. A rule in neither fails TestCnpgFurtherKinds_ExpressionRules.
var cnpgFurtherKinds = []struct {
	component  string
	handler    oam.ComponentHandler
	crd        string
	typ        reflect.Type
	namespaced bool
	required   map[string]string
	checked    map[string]cnpgFurtherRule
	left       map[string]string
}{
	{
		component: "cnpg-imagecatalog", handler: &CnpgImageCatalogHandler{},
		crd: "imagecatalogs", typ: reflect.TypeFor[cnpgv1.ImageCatalogSpec](), namespaced: true,
		required: cnpgImageCatalogKind.required,
		checked:  cnpgFurtherCatalogRules,
	},
	{
		component: "cnpg-clusterimagecatalog", handler: &CnpgClusterImageCatalogHandler{},
		crd: "clusterimagecatalogs", typ: reflect.TypeFor[cnpgv1.ImageCatalogSpec](),
		required: cnpgClusterImageCatalogKind.required,
		checked:  cnpgFurtherCatalogRules,
	},
	{
		component: "cnpg-backup", handler: &CnpgBackupHandler{},
		crd: "backups", typ: reflect.TypeFor[cnpgv1.BackupSpec](), namespaced: true,
		required: cnpgBackupKind.required,
		// The whole spec is immutable: a Backup is a request made once.
		left: map[string]string{"spec: oldSelf == self": cnpgFurtherTransitionRule},
	},
	{
		component: "cnpg-scheduledbackup", handler: &CnpgScheduledBackupHandler{},
		crd: "scheduledbackups", typ: reflect.TypeFor[cnpgv1.ScheduledBackupSpec](), namespaced: true,
		required: cnpgScheduledBackupKind.required,
		left:     map[string]string{"spec.cluster: self == oldSelf": cnpgFurtherTransitionRule},
	},
	{
		component: "cnpg-databaserole", handler: &CnpgDatabaseRoleHandler{},
		crd: "databaseroles", typ: reflect.TypeFor[cnpgv1.DatabaseRoleSpec](), namespaced: true,
		required: cnpgDatabaseRoleKind.required,
		checked: map[string]cnpgFurtherRule{
			"spec: self.name.size() != 0": {
				cnpgFurtherRole("name", ""), "name: empty, the DatabaseRole CRD refuses a role with no name",
			},
			"spec: self.name != 'postgres'": {
				cnpgFurtherRole("name", "postgres"), `name "postgres": reserved, the DatabaseRole CRD refuses it`,
			},
			"spec: self.name != 'streaming_replica'": {
				cnpgFurtherRole("name", "streaming_replica"), `name "streaming_replica": reserved, the DatabaseRole CRD refuses it`,
			},
			"spec: !self.name.startsWith('pg_')": {
				cnpgFurtherRole("name", "pg_app"), `name "pg_app": a name that starts with pg_ is reserved by PostgreSQL, the DatabaseRole CRD refuses it`,
			},
			"spec: !self.name.startsWith('cnpg_')": {
				cnpgFurtherRole("name", "cnpg_app"), `name "cnpg_app": a name that starts with cnpg_ is reserved by the operator, the DatabaseRole CRD refuses it`,
			},
			"spec: !has(self.ensure) || self.ensure != 'absent'": {
				cnpgFurtherRole("ensure", "absent"), "ensure: absent is not supported for a DatabaseRole, the CRD refuses it",
			},
			"spec: !has(self.passwordSecret) || !has(self.disablePassword) || !self.disablePassword": {
				cnpgFurtherRole("passwordSecret", map[string]any{"name": "app-password"}, "disablePassword", true),
				"passwordSecret and disablePassword: true are both set; the API takes at most one",
			},
			"spec: !has(self.clientCertificate) || !self.clientCertificate.enabled || self.login": {
				cnpgFurtherRole("clientCertificate", map[string]any{}), "clientCertificate: an enabled client certificate requires login: true",
			},
		},
		left: map[string]string{
			"spec.cluster: self == oldSelf":   cnpgFurtherTransitionRule,
			"spec: self.name == oldSelf.name": cnpgFurtherTransitionRule,
		},
	},
	{
		component: "cnpg-publication", handler: &CnpgPublicationHandler{},
		crd: "publications", typ: reflect.TypeFor[cnpgv1.PublicationSpec](), namespaced: true,
		required: cnpgPublicationKind.required,
		checked: map[string]cnpgFurtherRule{
			"spec.target: (has(self.allTables) && !has(self.objects)) || (!has(self.allTables) && has(self.objects))": {
				cnpgFurtherPublication(map[string]any{"allTables": true, "objects": []any{map[string]any{"tablesInSchema": "sales"}}}),
				"target: allTables and objects are both set; the API takes exactly one",
			},
			"spec.target.objects[]: (has(self.tablesInSchema) && !has(self.table)) || (!has(self.tablesInSchema) && has(self.table))": {
				cnpgFurtherPublication(map[string]any{"objects": []any{map[string]any{}}}),
				"target.objects[0]: one of tablesInSchema and table is required",
			},
			"spec.target.objects: !(self.exists(o, has(o.table) && has(o.table.columns)) && self.exists(o, has(o.tablesInSchema)))": {
				cnpgFurtherPublication(map[string]any{"objects": []any{
					map[string]any{"tablesInSchema": "sales"},
					map[string]any{"table": map[string]any{"name": "orders", "columns": []any{"id"}}},
				}}),
				"target.objects[1].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[0])",
			},
		},
		left: map[string]string{
			"spec.cluster: self == oldSelf":          cnpgFurtherTransitionRule,
			"spec.dbname: self == oldSelf":           cnpgFurtherTransitionRule,
			"spec.name: self == oldSelf":             cnpgFurtherTransitionRule,
			"spec.target.allTables: self == oldSelf": cnpgFurtherTransitionRule,
		},
	},
	{
		component: "cnpg-subscription", handler: &CnpgSubscriptionHandler{},
		crd: "subscriptions", typ: reflect.TypeFor[cnpgv1.SubscriptionSpec](), namespaced: true,
		required: cnpgSubscriptionKind.required,
		left: map[string]string{
			"spec.cluster: self == oldSelf": cnpgFurtherTransitionRule,
			"spec.dbname: self == oldSelf":  cnpgFurtherTransitionRule,
			"spec.name: self == oldSelf":    cnpgFurtherTransitionRule,
		},
	},
}

// cnpgFurtherCatalogRules is the two expression rules the ImageCatalog and
// ClusterImageCatalog CRDs share, both checked.
var cnpgFurtherCatalogRules = map[string]cnpgFurtherRule{
	"spec.images: self.all(e, self.filter(f, f.major==e.major).size() == 1)": {
		map[string]any{"images": []any{
			map[string]any{"image": "registry.example/postgresql:17.2", "major": 17},
			map[string]any{"image": "registry.example/postgresql:17.4", "major": 17},
		}},
		"images[1].major: 17 is also the major version of images[0]; the API takes each major version once",
	},
	"spec.componentImages: self.all(e, self.filter(f, f.key==e.key).size() == 1)": {
		map[string]any{
			"images": []any{map[string]any{"image": "registry.example/postgresql:17.2", "major": 17}},
			"componentImages": []any{
				map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
				map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:2"},
			},
		},
		`componentImages[1].key: "pgbouncer" is also the key of componentImages[0]; the API takes each key once`,
	},
}

// cnpgFurtherRole is the properties of a cnpg-databaserole that authors what
// the API requires and the given properties, as name and value in turn.
func cnpgFurtherRole(properties ...any) map[string]any {
	role := map[string]any{"cluster": map[string]any{"name": "db"}, "name": "app"}
	for i := 0; i+1 < len(properties); i += 2 {
		role[properties[i].(string)] = properties[i+1]
	}
	return role
}

// cnpgFurtherPublication is the properties of a cnpg-publication with the
// given target.
func cnpgFurtherPublication(target map[string]any) map[string]any {
	return map[string]any{"cluster": map[string]any{"name": "db"}, "name": "pub", "dbname": "app", "target": target}
}

// cnpgFurtherCRD reads one CRD of the linked CloudNativePG module, by the
// plural of its kind, and returns it with the schema of v1, which must be the
// one version it serves and stores.
func cnpgFurtherCRD(t *testing.T, plural string) (*apiextensionsv1.CustomResourceDefinition, apiextensionsv1.JSONSchemaProps) {
	t.Helper()
	data, err := os.ReadFile(cnpgCRDFile(linkedModuleDir(t, cnpgModulePath), plural))
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", plural, err)
	}
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("%s has %d versions, want the one the kinds emit; update this test", plural, len(crd.Spec.Versions))
	}
	version := crd.Spec.Versions[0]
	if version.Name != cnpgv1.SchemeGroupVersion.Version || !version.Served || !version.Storage || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
		t.Fatalf("%s: version %s is served %v, stored %v, and has a schema: %v; the kinds emit %s", plural, version.Name, version.Served, version.Storage, version.Schema != nil, cnpgv1.SchemeGroupVersion.Version)
	}
	return &crd, *version.Schema.OpenAPIV3Schema
}

// cnpgFurtherSpec is the schema of the CRD's spec.
func cnpgFurtherSpec(t *testing.T, plural string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	_, root := cnpgFurtherCRD(t, plural)
	spec, ok := root.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", plural)
	}
	return spec
}

// TestCnpgFurtherKinds_EmitTheServedVersion: each kind's CRD names the kind
// the handler declares, in the group the handler declares and with the scope
// it declares, and the version the kinds emit is the one the CRD serves and
// stores (cnpgFurtherCRD fails on another).
func TestCnpgFurtherKinds_EmitTheServedVersion(t *testing.T) {
	var namespaced int
	for _, kind := range cnpgFurtherKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd, _ := cnpgFurtherCRD(t, kind.crd)
			declared, scope := kind.handler.(oam.ComponentObjectProvider).ComponentObject()
			if crd.Spec.Group != declared.Group || crd.Spec.Names.Kind != declared.Kind {
				t.Errorf("the CRD is %s %s, the handler declares %s", crd.Spec.Group, crd.Spec.Names.Kind, declared)
			}
			wantCRD, wantScope := apiextensionsv1.ClusterScoped, oam.ObjectScopeCluster
			if kind.namespaced {
				wantCRD, wantScope = apiextensionsv1.NamespaceScoped, oam.ObjectScopeNamespaced
				namespaced++
			}
			if crd.Spec.Scope != wantCRD || scope != wantScope {
				t.Errorf("the CRD's scope is %s and the handler declares scope %d; want %s and %d", crd.Spec.Scope, scope, wantCRD, wantScope)
			}
		})
	}
	// Vacuity guard: both scopes are read.
	if namespaced == 0 || namespaced == len(cnpgFurtherKinds) {
		t.Errorf("%d of %d kinds are namespaced; want some of each scope", namespaced, len(cnpgFurtherKinds))
	}
}

// TestCnpgFurtherKinds_RequiredMatchCRD derives, from the linked module's CRD
// and the spec type, the fields of each kind that the API requires and the
// type would write unauthored, and holds the kind's required list to them. A
// dependency bump that adds, drops or moves one fails here, naming it.
//
// None of these types leaves a required field out when it is not authored. One
// that came to do so fails here: the required list cannot hold it, so the
// kind's validate refuses it on the decoded value, with a test row, or it is
// listed in this test with the reason the decoded value cannot show the
// omission.
//
// A required field may sit under a struct that is no pointer (the name of a
// Backup's cluster). The list reads what was authored, so such a struct must be
// required itself: then it is authored wherever its fields are looked for.
func TestCnpgFurtherKinds_RequiredMatchCRD(t *testing.T) {
	for _, kind := range cnpgFurtherKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := ciliumBGPTypeFields(kind.typ)
			var crdRequired []string
			walkCiliumBGPSchema(cnpgFurtherSpec(t, kind.crd), "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, name := range s.Required {
					if path != "" {
						name = path + "." + name
					}
					crdRequired = append(crdRequired, name)
				}
			})
			if len(crdRequired) == 0 || len(fields) == 0 {
				t.Fatalf("the CRD requires %d fields and the type has %d; a walk is broken", len(crdRequired), len(fields))
			}
			var listed []string
			for _, path := range crdRequired {
				f, ok := fields[path]
				switch {
				case !ok:
					t.Errorf("the CRD requires %s, which is no field of %s", path, kind.typ)
				case strings.Contains(path, "{}"):
					t.Errorf("%s is required under a map value, which a required list cannot name", path)
				case !f.writtenUnauthored():
					t.Errorf("the CRD requires %s, which the type leaves out when it is not authored; refuse it in the kind's validate with a test row, or list it in this test with the reason the decoded value cannot show the omission", path)
				default:
					listed = append(listed, path)
				}
				segments := strings.Split(path, ".")
				for i := 1; i < len(segments); i++ {
					parent := strings.Join(segments[:i], ".")
					parent = strings.TrimSuffix(strings.TrimSuffix(parent, "[]"), "{}")
					above, ok := fields[parent]
					if !ok {
						t.Errorf("%s: its parent %s is no field of %s", path, parent, kind.typ)
						continue
					}
					if above.field.Type.Kind() == reflect.Struct && !slices.Contains(crdRequired, parent) {
						t.Errorf("%s is required under %s, a struct the type writes unauthored that the CRD does not require; the kind's validate must refuse it, and this test hold that", path, parent)
					}
				}
			}
			slices.Sort(listed)
			t.Logf("the CRD requires %d fields, all written unauthored: %v", len(crdRequired), listed)
			if got := slices.Sorted(maps.Keys(kind.required)); !slices.Equal(got, listed) {
				t.Errorf("required list = %v\nthe CRD and the type give %v", got, listed)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
		})
	}
}

// TestCnpgFurtherKinds_ExpressionRules: every expression rule the CRDs
// declare, anywhere in the object, is one a kind checks or one it leaves to the
// API server with a reason. A checked one is refused on a document that breaks
// it, and one left as a transition rule reads the stored object. A dependency
// bump that adds or rewords a rule fails here until it is classified.
func TestCnpgFurtherKinds_ExpressionRules(t *testing.T) {
	var total, checked int
	for _, kind := range cnpgFurtherKinds {
		t.Run(kind.component, func(t *testing.T) {
			_, root := cnpgFurtherCRD(t, kind.crd)
			var declared []string
			walkCiliumBGPSchema(root, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
				for _, rule := range s.XValidations {
					declared = append(declared, path+": "+rule.Rule)
				}
			})
			slices.Sort(declared)
			total += len(declared)
			checked += len(kind.checked)
			classified := slices.Sorted(maps.Keys(kind.checked))
			for rule, why := range kind.left {
				if _, both := kind.checked[rule]; both || strings.TrimSpace(why) == "" {
					t.Errorf("rule %q is left to the API server with the reason %q, and checked: %v", rule, why, both)
				}
				if why == cnpgFurtherTransitionRule && !strings.Contains(rule, "oldSelf") {
					t.Errorf("rule %q is left as a transition rule and does not read oldSelf", rule)
				}
				classified = append(classified, rule)
			}
			slices.Sort(classified)
			if !slices.Equal(declared, classified) {
				t.Errorf("the CRD declares the rules %q\nthe kind classifies   %q", declared, classified)
			}
			for rule, breaks := range kind.checked {
				if strings.Contains(rule, "oldSelf") {
					t.Errorf("rule %q reads the stored object and is listed as checked", rule)
				}
				_, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: kind.component, Properties: breaks.props}, "apps")
				if err == nil || !strings.Contains(err.Error(), breaks.want) {
					t.Errorf("rule %q: err = %v, want one mentioning %q", rule, err, breaks.want)
				}
			}
		})
	}
	// Vacuity guard: the walk reads the rules of all seven CRDs.
	if total != 26 || checked != 15 {
		t.Errorf("the seven CRDs declare %d expression rules and the kinds check %d, want 26 and 15", total, checked)
	}
}
