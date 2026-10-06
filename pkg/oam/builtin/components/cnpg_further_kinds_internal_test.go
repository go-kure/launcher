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
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// cnpgFurtherRule is one expression rule of a CRD a kind checks: the kind's
// refusals of it, each with properties that break the rule, and properties
// next to them that keep it. The API server's own validator answers for each
// (checkedRules.show).
type cnpgFurtherRule struct {
	refusals []cnpgFurtherRefusal
	keeps    []map[string]any
}

// cnpgFurtherRefusal is one refusal a kind gives of a rule, and properties
// that break the rule in the way it names. as says how the API server refuses
// them where that is not by the rule's own refusal and nothing else
// (checkedRules.showRefused).
type cnpgFurtherRefusal struct {
	want   string
	breaks []map[string]any
	as     crdRefusal
}

// cnpgFurtherRefused is a rule with the one refusal.
func cnpgFurtherRefused(want string, breaks ...map[string]any) []cnpgFurtherRefusal {
	return []cnpgFurtherRefusal{{want: want, breaks: breaks}}
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
// path of the value they are declared on and their text, each with properties
// that break it and properties that keep it; left names the ones the kind
// leaves to the API server, each with the reason. A rule in neither fails
// TestCnpgFurtherKinds_ExpressionRules.
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
				cnpgFurtherRefused("name: empty, the DatabaseRole CRD refuses a role with no name", cnpgFurtherRole("name", "")),
				[]map[string]any{cnpgFurtherRole("name", "a")},
			},
			"spec: self.name != 'postgres'": {
				cnpgFurtherRefused(`name "postgres": reserved, the DatabaseRole CRD refuses it`, cnpgFurtherRole("name", "postgres")),
				[]map[string]any{cnpgFurtherRole("name", "postgresql"), cnpgFurtherRole("name", "Postgres")},
			},
			"spec: self.name != 'streaming_replica'": {
				cnpgFurtherRefused(`name "streaming_replica": reserved, the DatabaseRole CRD refuses it`, cnpgFurtherRole("name", "streaming_replica")),
				[]map[string]any{cnpgFurtherRole("name", "streaming_replicas")},
			},
			"spec: !self.name.startsWith('pg_')": {
				cnpgFurtherRefused(`name "pg_app": a name that starts with pg_ is reserved by PostgreSQL, the DatabaseRole CRD refuses it`, cnpgFurtherRole("name", "pg_app")),
				[]map[string]any{cnpgFurtherRole("name", "pgapp"), cnpgFurtherRole("name", "app_pg_")},
			},
			"spec: !self.name.startsWith('cnpg_')": {
				cnpgFurtherRefused(`name "cnpg_app": a name that starts with cnpg_ is reserved by the operator, the DatabaseRole CRD refuses it`, cnpgFurtherRole("name", "cnpg_app")),
				[]map[string]any{cnpgFurtherRole("name", "cnpgapp"), cnpgFurtherRole("name", "app_cnpg_")},
			},
			"spec: !has(self.ensure) || self.ensure != 'absent'": {
				cnpgFurtherRefused("ensure: absent is not supported for a DatabaseRole, the CRD refuses it", cnpgFurtherRole("ensure", "absent")),
				[]map[string]any{cnpgFurtherRole("ensure", "present"), cnpgFurtherRole(), cnpgFurtherRole("ensure", nil)},
			},
			"spec: !has(self.passwordSecret) || !has(self.disablePassword) || !self.disablePassword": {
				cnpgFurtherRefused("passwordSecret and disablePassword: true are both set; the API takes at most one",
					cnpgFurtherRole("passwordSecret", map[string]any{"name": "app-password"}, "disablePassword", true),
				),
				[]map[string]any{
					cnpgFurtherRole("passwordSecret", map[string]any{"name": "app-password"}, "disablePassword", false),
					cnpgFurtherRole("passwordSecret", map[string]any{"name": "app-password"}),
					cnpgFurtherRole("disablePassword", true),
					cnpgFurtherRole("passwordSecret", nil, "disablePassword", true),
				},
			},
			// The CRD fills `enabled: true` into a client certificate that does
			// not author it, before the rule is evaluated: an authored empty
			// block is an enabled certificate. It fills nothing into `login`,
			// which the rule reads without asking whether it is there: of an
			// enabled certificate with no `login` authored, the API server
			// refuses the rule as one it cannot evaluate, and says so before
			// the rule's message. The kind gives the two the one refusal.
			"spec: !has(self.clientCertificate) || !self.clientCertificate.enabled || self.login": {
				[]cnpgFurtherRefusal{
					{
						want: "clientCertificate: an enabled client certificate requires login: true",
						breaks: []map[string]any{
							cnpgFurtherRole("clientCertificate", map[string]any{}, "login", false),
							cnpgFurtherRole("clientCertificate", map[string]any{"enabled": nil}, "login", false),
							cnpgFurtherRole("clientCertificate", map[string]any{"enabled": true}, "login", false),
						},
					},
					{
						want: "clientCertificate: an enabled client certificate requires login: true",
						breaks: []map[string]any{
							cnpgFurtherRole("clientCertificate", map[string]any{}),
							cnpgFurtherRole("clientCertificate", map[string]any{"enabled": nil}),
							cnpgFurtherRole("clientCertificate", map[string]any{"enabled": true}),
							cnpgFurtherRole("clientCertificate", map[string]any{}, "login", nil),
						},
						as: crdRefusedUnevaluated("no such key: login"),
					},
				},
				[]map[string]any{
					cnpgFurtherRole("clientCertificate", map[string]any{}, "login", true),
					cnpgFurtherRole("clientCertificate", map[string]any{"enabled": true}, "login", true),
					cnpgFurtherRole("clientCertificate", map[string]any{"enabled": false}),
					cnpgFurtherRole("clientCertificate", map[string]any{"enabled": false}, "login", false),
					cnpgFurtherRole("clientCertificate", nil),
					cnpgFurtherRole(),
				},
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
		// The three rules read which fields the object holds. The rows author
		// a field with a value the type writes, or leave it out or null: what
		// the type leaves out of an authored field is
		// TestCnpgPublication_TargetAsWritten's.
		checked: map[string]cnpgFurtherRule{
			"spec.target: (has(self.allTables) && !has(self.objects)) || (!has(self.allTables) && has(self.objects))": {
				[]cnpgFurtherRefusal{
					{
						want:   "target: allTables and objects are both set; the API takes exactly one",
						breaks: []map[string]any{cnpgFurtherPublication(map[string]any{"allTables": true, "objects": cnpgFurtherObjects(cnpgFurtherSchemaTables)})},
					},
					{
						want: "target: one of allTables: true and objects is required",
						breaks: []map[string]any{
							cnpgFurtherPublication(map[string]any{}),
							cnpgFurtherPublication(map[string]any{"allTables": nil, "objects": nil}),
						},
					},
				},
				[]map[string]any{
					cnpgFurtherPublication(map[string]any{"allTables": true}),
					cnpgFurtherPublication(map[string]any{"allTables": true, "objects": nil}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables)}),
					cnpgFurtherPublication(map[string]any{"allTables": nil, "objects": cnpgFurtherObjects(cnpgFurtherTable())}),
				},
			},
			"spec.target.objects[]: (has(self.tablesInSchema) && !has(self.table)) || (!has(self.tablesInSchema) && has(self.table))": {
				[]cnpgFurtherRefusal{
					{
						want: "target.objects[0]: one of tablesInSchema and table is required",
						breaks: []map[string]any{
							cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(map[string]any{})}),
							cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(map[string]any{"tablesInSchema": nil, "table": nil})}),
						},
					},
					{
						want: "target.objects[1]: tablesInSchema and table are both set; the API takes exactly one",
						breaks: []map[string]any{cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(
							cnpgFurtherTable(),
							map[string]any{"tablesInSchema": "sales", "table": map[string]any{"name": "orders"}},
						)})},
					},
				},
				[]map[string]any{
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables)}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherTable())}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(map[string]any{"tablesInSchema": "sales", "table": nil})}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(map[string]any{"tablesInSchema": nil, "table": map[string]any{"name": "orders"}})}),
				},
			},
			"spec.target.objects: !(self.exists(o, has(o.table) && has(o.table.columns)) && self.exists(o, has(o.tablesInSchema)))": {
				[]cnpgFurtherRefusal{
					{
						want:   "target.objects[1].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[0])",
						breaks: []map[string]any{cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables, cnpgFurtherTable("id"))})},
					},
					{
						want:   "target.objects[0].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[2])",
						breaks: []map[string]any{cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherTable("id"), cnpgFurtherTable(), cnpgFurtherSchemaTables)})},
					},
				},
				[]map[string]any{
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables, cnpgFurtherTable())}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherTable("id"), cnpgFurtherTable("id", "total"))}),
					cnpgFurtherPublication(map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables, map[string]any{"table": map[string]any{"name": "orders", "columns": nil}})}),
				},
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
		cnpgFurtherRefused("images[1].major: 17 is also the major version of images[0]; the API takes each major version once",
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2"), cnpgFurtherImage(17, "17.4")}, nil),
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2"), cnpgFurtherImage(17, "17.4"), cnpgFurtherImage(18, "18.1")}, nil),
		),
		[]map[string]any{
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2")}, nil),
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2"), cnpgFurtherImage(18, "18.1")}, nil),
		},
	},
	// The component images are a list map keyed on `key`, so the schema refuses
	// a key held twice as well (a duplicate value), and the API server
	// evaluates the rule past that: one document, two refusals for the one
	// thing. No document breaks this rule and nothing else.
	"spec.componentImages: self.all(e, self.filter(f, f.key==e.key).size() == 1)": {
		[]cnpgFurtherRefusal{{
			want: `componentImages[1].key: "pgbouncer" is also the key of componentImages[0]; the API takes each key once`,
			breaks: []map[string]any{
				cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2")}, []any{
					map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
					map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:2"},
				}),
			},
			as: crdRefusedBesideSchema(field.ErrorTypeDuplicate),
		}},
		[]map[string]any{
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2")}, []any{
				map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
				map[string]any{"key": "pooler", "image": "registry.example/pgbouncer:1"},
			}),
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2")}, []any{
				map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
			}),
			cnpgFurtherCatalog([]any{cnpgFurtherImage(17, "17.2")}, nil),
			{"images": []any{cnpgFurtherImage(17, "17.2")}, "componentImages": nil},
		},
	},
}

// cnpgFurtherImage is one image of a catalog, of a major version.
func cnpgFurtherImage(major int, tag string) map[string]any {
	return map[string]any{"image": "registry.example/postgresql:" + tag, "major": major}
}

// cnpgFurtherCatalog is the properties of a catalog with the images and,
// unless nil, the component images.
func cnpgFurtherCatalog(images, componentImages []any) map[string]any {
	catalog := map[string]any{"images": images}
	if componentImages != nil {
		catalog["componentImages"] = componentImages
	}
	return catalog
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

// cnpgFurtherSchemaTables is the object of a publication's target that
// publishes the tables of a schema.
var cnpgFurtherSchemaTables = map[string]any{"tablesInSchema": "sales"}

// cnpgFurtherTable is the object of a publication's target that publishes one
// table, with the columns it lists when any are named.
func cnpgFurtherTable(columns ...string) map[string]any {
	table := map[string]any{"name": "orders"}
	if len(columns) > 0 {
		list := make([]any, len(columns))
		for i, column := range columns {
			list[i] = column
		}
		table["columns"] = list
	}
	return map[string]any{"table": table}
}

// cnpgFurtherObjects is the objects of a publication's target.
func cnpgFurtherObjects(objects ...map[string]any) []any {
	list := make([]any, len(objects))
	for i, object := range objects {
		list[i] = object
	}
	return list
}

// cnpgFurtherCheckedRules prepares the CRD of a kind as the API server serves
// it, for a kind whose properties are the object's spec.
func cnpgFurtherCheckedRules(t *testing.T, component string, handler oam.ComponentHandler, plural string) checkedRules {
	t.Helper()
	crd, _ := cnpgFurtherCRD(t, plural)
	version := cnpgv1.SchemeGroupVersion.Version
	return checkedRules{
		create: crdCreateOf(t, crd, version), component: component, handler: handler,
		document: func(props map[string]any) map[string]any {
			return crdDocument(crd, version, map[string]any{"spec": props})
		},
	}
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
// API server with a reason. A checked one is held to the API server's own
// expression validator, run over the linked CRD after its defaults
// (checkedRules.show): it refuses the properties that break the rule, by that
// rule alone, and accepts the ones next to them, and the kind answers the same
// on both. Two refusals are not the rule's alone, and are shown as what they
// are (checkedRules.showRefused): a component image key held twice, which the
// schema refuses too, and an enabled client certificate with no `login`
// authored, on which the rule fails to evaluate. One left as a transition rule
// reads the stored object. A dependency bump that adds or rewords a rule fails
// here until it is classified.
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
			if len(kind.checked) == 0 {
				return
			}
			shown := cnpgFurtherCheckedRules(t, kind.component, kind.handler, kind.crd)
			for rule, checked := range kind.checked {
				t.Run(rule, func(t *testing.T) {
					if strings.Contains(rule, "oldSelf") {
						t.Errorf("rule %q reads the stored object and is listed as checked", rule)
					}
					if len(checked.refusals) == 0 {
						t.Fatalf("rule %q is checked and shown on no refusal", rule)
					}
					for _, refusal := range checked.refusals {
						if refusal.as == nil {
							shown.show(t, crdRuleOf(t, rule), refusal.want, refusal.breaks, checked.keeps)
							continue
						}
						shown.showRefused(t, crdRuleOf(t, rule), refusal.as, refusal.want, refusal.breaks, checked.keeps)
					}
				})
			}
		})
	}
	// Vacuity guard: the walk reads the rules of all seven CRDs.
	if total != 26 || checked != 15 {
		t.Errorf("the seven CRDs declare %d expression rules and the kinds check %d, want 26 and 15", total, checked)
	}
}

// TestCnpgPublication_TargetAsWritten: the three rules of a publication's
// target read which fields the object holds, and the type leaves four authored
// values out: `allTables: false`, an empty `objects`, an empty
// `tablesInSchema` and an empty `columns`. The kind reads the target as the
// object will hold it (validateCnpgPublication), so on such a value its answer
// and the API server's answer for the document as authored differ, both ways.
// This shows each, and that the API server's answer for the object written is
// the kind's.
//
// Left out beside the other field: the API server refuses the document as
// authored, by the rule; the kind builds it, the object does not hold the
// value, and the API server accepts the object.
//
// Left out alone: the API server accepts the document as authored; the kind
// refuses it, and the object the type would write, which the rows of
// TestCnpgFurtherKinds_ExpressionRules author, is the one the API server
// refuses.
func TestCnpgPublication_TargetAsWritten(t *testing.T) {
	shown := cnpgFurtherCheckedRules(t, "cnpg-publication", &CnpgPublicationHandler{}, "publications")
	const (
		targetRule  = "spec.target: (has(self.allTables) && !has(self.objects)) || (!has(self.allTables) && has(self.objects))"
		objectRule  = "spec.target.objects[]: (has(self.tablesInSchema) && !has(self.table)) || (!has(self.tablesInSchema) && has(self.table))"
		columnsRule = "spec.target.objects: !(self.exists(o, has(o.table) && has(o.table.columns)) && self.exists(o, has(o.tablesInSchema)))"
	)
	for _, kind := range cnpgFurtherKinds {
		if kind.component != "cnpg-publication" {
			continue
		}
		for _, rule := range []string{targetRule, objectRule, columnsRule} {
			if _, checked := kind.checked[rule]; !checked {
				t.Fatalf("rule %q is not one cnpg-publication checks; update this test", rule)
			}
		}
	}

	beside := map[string]struct {
		rule            string
		target, written map[string]any
	}{
		"allTables: false beside objects": {
			targetRule,
			map[string]any{"allTables": false, "objects": cnpgFurtherObjects(cnpgFurtherSchemaTables)},
			map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables)},
		},
		"an empty objects beside allTables: true": {
			targetRule,
			map[string]any{"allTables": true, "objects": []any{}},
			map[string]any{"allTables": true},
		},
		"an empty tablesInSchema beside a table": {
			objectRule,
			map[string]any{"objects": cnpgFurtherObjects(map[string]any{"tablesInSchema": "", "table": map[string]any{"name": "orders"}})},
			map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherTable())},
		},
		"an empty columns beside a schema's tables": {
			columnsRule,
			map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables, map[string]any{"table": map[string]any{"name": "orders", "columns": []any{}}})},
			map[string]any{"objects": cnpgFurtherObjects(cnpgFurtherSchemaTables, cnpgFurtherTable())},
		},
	}
	for name, c := range beside {
		t.Run(name, func(t *testing.T) {
			props := cnpgFurtherPublication(c.target)
			only, says := shown.create.only(t, crdRuleOf(t, c.rule))
			only.create(t, shown.document(props)).refusedByRule(t, "the document as authored", says)
			object, err := shown.build(t, props)
			if err != nil {
				t.Fatalf("the kind refuses it: %v", err)
			}
			if got := object["spec"].(map[string]any)["target"]; !reflect.DeepEqual(got, any(c.written)) {
				t.Errorf("the object's target is %v, want %v: the type leaves the value out", got, c.written)
			}
			shown.create.create(t, object).accepted(t, "the object the kind emits")
		})
	}

	alone := map[string]struct {
		target map[string]any
		want   string
	}{
		"allTables: false alone": {
			map[string]any{"allTables": false}, "target: one of allTables: true and objects is required",
		},
		"an empty objects alone": {
			map[string]any{"objects": []any{}}, "target: one of allTables: true and objects is required",
		},
		"an empty tablesInSchema alone": {
			map[string]any{"objects": cnpgFurtherObjects(map[string]any{"tablesInSchema": ""})},
			"target.objects[0]: one of tablesInSchema and table is required",
		},
	}
	for name, c := range alone {
		t.Run(name, func(t *testing.T) {
			props := cnpgFurtherPublication(c.target)
			shown.create.create(t, shown.document(props)).accepted(t, "the document as authored")
			if _, err := shown.build(t, props); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("the kind answers %v, want a refusal mentioning %q", err, c.want)
			}
		})
	}
}
