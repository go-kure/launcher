package components_test

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The seven further kind components of the CloudNativePG API
// (go-kure/launcher#790): cnpg-imagecatalog, cnpg-clusterimagecatalog,
// cnpg-backup, cnpg-scheduledbackup, cnpg-databaserole, cnpg-publication and
// cnpg-subscription. What every kind component does is held by the tests over
// policyFreeKinds, in which each has a row; this file holds their fixtures and
// what is their own.

// cnpgCluster is the Cluster reference five of the kinds require.
func cnpgCluster() map[string]any {
	return map[string]any{"name": "db"}
}

// cnpgImageCatalogFull sets every top-level field of an ImageCatalog's spec,
// every image from the registry ptStrictPolicy allows.
func cnpgImageCatalogFull() map[string]any {
	return map[string]any{
		"images": []any{
			map[string]any{
				"image": "registry.example/postgresql:17.2", "major": 17,
				"extensions": []any{
					map[string]any{
						"name":                   "pgvector",
						"image":                  map[string]any{"reference": "registry.example/pgvector:0.8.0", "pullPolicy": "IfNotPresent"},
						"extension_control_path": []any{"/share"},
						"dynamic_library_path":   []any{"/lib"},
						"ld_library_path":        []any{"/system"},
						"bin_path":               []any{"/bin"},
						"env":                    []any{map[string]any{"name": "PGVECTOR_HOME", "value": "${image_root}/share"}},
					},
					// An extension may name no image: the type writes an empty
					// one, which the API admits.
					map[string]any{"name": "pg-stat"},
				},
			},
			map[string]any{"image": "registry.example/postgresql:16.6", "major": 16},
		},
		"componentImages": []any{
			map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1.24.0"},
			map[string]any{"key": "exporter", "image": "registry.example/exporter:0.17.1"},
		},
	}
}

// cnpgBackupFull sets every top-level field of a Backup's spec, under the
// plugin method, with which the operator's webhook takes the online settings
// too.
func cnpgBackupFull() map[string]any {
	return map[string]any{
		"cluster": cnpgCluster(),
		"target":  "prefer-standby",
		"method":  "plugin",
		"pluginConfiguration": map[string]any{
			"name": "barman-cloud.cloudnative-pg.io", "parameters": map[string]any{"compression": "gzip"},
		},
		"online":              false,
		"onlineConfiguration": map[string]any{"waitForArchive": false, "immediateCheckpoint": true},
	}
}

// cnpgScheduledBackupFull sets every top-level field of a ScheduledBackup's
// spec: a Backup's, and the four of the schedule.
func cnpgScheduledBackupFull() map[string]any {
	full := cnpgBackupFull()
	full["schedule"] = "0 0 3 * * *"
	full["suspend"] = false
	full["immediate"] = true
	full["backupOwnerReference"] = "self"
	return full
}

// cnpgDatabaseRoleMinimal is the least a cnpg-databaserole may author.
func cnpgDatabaseRoleMinimal() map[string]any {
	return map[string]any{"cluster": cnpgCluster(), "name": "app"}
}

// cnpgDatabaseRoleFull sets every top-level field of a DatabaseRole's spec
// that one role can hold. `disablePassword` is null, an unauthored field: the
// API takes it or `passwordSecret`, and the type leaves out a false one, so no
// value of it can stand beside the Secret. cnpgDatabaseRoleWithoutPassword
// authors it.
func cnpgDatabaseRoleFull() map[string]any {
	return map[string]any{
		"cluster":                   cnpgCluster(),
		"name":                      "app",
		"comment":                   "The application's role.",
		"ensure":                    "present",
		"passwordSecret":            map[string]any{"name": "app-password"},
		"disablePassword":           nil,
		"connectionLimit":           20,
		"validUntil":                "2030-01-01T00:00:00Z",
		"inRoles":                   []any{"pg_monitor", "readers"},
		"inherit":                   false,
		"superuser":                 true,
		"createdb":                  true,
		"createrole":                true,
		"login":                     true,
		"replication":               true,
		"bypassrls":                 true,
		"databaseRoleReclaimPolicy": "delete",
		"clientCertificate":         map[string]any{"enabled": true},
	}
}

// cnpgDatabaseRoleWithoutPassword is a role whose password is disabled.
func cnpgDatabaseRoleWithoutPassword() map[string]any {
	role := cnpgDatabaseRoleMinimal()
	role["disablePassword"] = true
	return role
}

// cnpgPublicationOf is a cnpg-publication with the given target.
func cnpgPublicationOf(target map[string]any) map[string]any {
	return map[string]any{"cluster": cnpgCluster(), "name": "pub", "dbname": "app", "target": target}
}

// cnpgPublicationFull sets every top-level field of a Publication's spec. Its
// target is a list of objects, one of each form; no table lists its columns,
// which the API refuses beside a schema's tables.
func cnpgPublicationFull() map[string]any {
	full := cnpgPublicationOf(map[string]any{"objects": []any{
		map[string]any{"tablesInSchema": "sales"},
		map[string]any{"table": map[string]any{"name": "orders", "schema": "public", "only": true}},
	}})
	full["parameters"] = map[string]any{"publish": "insert,update"}
	full["publicationReclaimPolicy"] = "delete"
	return full
}

// cnpgSubscriptionMinimal is the least a cnpg-subscription may author.
func cnpgSubscriptionMinimal() map[string]any {
	return map[string]any{
		"cluster": cnpgCluster(), "name": "sub", "dbname": "app",
		"publicationName": "pub", "externalClusterName": "origin",
	}
}

// cnpgSubscriptionFull sets every top-level field of a Subscription's spec.
func cnpgSubscriptionFull() map[string]any {
	full := cnpgSubscriptionMinimal()
	full["parameters"] = map[string]any{"copy_data": "false"}
	full["publicationDBName"] = "orders"
	full["subscriptionReclaimPolicy"] = "delete"
	return full
}

// cnpgImageCatalogReaches is what the copy walk must reach in a catalog built
// from cnpgImageCatalogFull (TestPolicyFreeKinds_GenerateCopies).
var cnpgImageCatalogReaches = []string{
	".Spec.Images", ".Spec.Images[0].Extensions", ".Spec.Images[0].Extensions[0].ExtensionControlPath",
	".Spec.Images[0].Extensions[0].DynamicLibraryPath", ".Spec.Images[0].Extensions[0].LdLibraryPath",
	".Spec.Images[0].Extensions[0].BinPath", ".Spec.Images[0].Extensions[0].Env", ".Spec.ComponentImages",
}

// cnpgRefusal is one refused document of TestPolicyFreeKinds_Refusals: the
// properties, and a fragment of the error they must yield.
type cnpgRefusal struct {
	name  string
	props map[string]any
	want  string
}

// cnpgWith is a copy of props with one more property; props itself is left as
// it was, so one base serves every case built from it.
func cnpgWith(props map[string]any, name string, value any) map[string]any {
	out := maps.Clone(props)
	out[name] = value
	return out
}

// cnpgWithout is a copy of props without one property.
func cnpgWithout(props map[string]any, name string) map[string]any {
	out := maps.Clone(props)
	delete(out, name)
	return out
}

// cnpgImages is a catalog of the given images.
func cnpgImages(images ...any) map[string]any {
	return map[string]any{"images": images}
}

// cnpgImage is one image of a catalog, from the registry ptStrictPolicy allows.
func cnpgImage(major int, extensions ...any) map[string]any {
	image := map[string]any{"image": "registry.example/postgresql:" + strconv.Itoa(major), "major": major}
	if len(extensions) > 0 {
		image["extensions"] = extensions
	}
	return image
}

// cnpgFurtherKindRefusals lists, per kind, the documents the build refuses
// with no policy applied: a field its CRD requires and the type would write
// empty, a rule of its CRD the build checks, and what the strict decode
// refuses.
func cnpgFurtherKindRefusals() map[string][]cnpgRefusal {
	const notA = "properties do not decode into a "
	catalog := []cnpgRefusal{
		{"no properties", nil, "images: required"},
		{"null images", map[string]any{"images": nil}, "images: required"},
		{"image without a reference", cnpgImages(map[string]any{"major": 17}), "images[0].image: required"},
		{"image without a major version", cnpgImages(cnpgImage(17), map[string]any{"image": "registry.example/postgresql:16"}), "images[1].major: required"},
		{"extension without a name", cnpgImages(cnpgImage(17, map[string]any{"image": map[string]any{"reference": "registry.example/pgvector:1"}})), "images[0].extensions[0].name: required"},
		{"extension variable without a name", cnpgImages(cnpgImage(17, map[string]any{"name": "pgvector", "env": []any{map[string]any{"value": "x"}}})), "images[0].extensions[0].env[0].name: required"},
		{"extension variable without a value", cnpgImages(cnpgImage(17, map[string]any{"name": "pgvector", "env": []any{map[string]any{"name": "X"}}})), "images[0].extensions[0].env[0].value: required"},
		{"component image without a key", cnpgWith(cnpgImages(cnpgImage(17)), "componentImages", []any{map[string]any{"image": "registry.example/pgbouncer:1"}}), "componentImages[0].key: required"},
		{"component image without an image", cnpgWith(cnpgImages(cnpgImage(17)), "componentImages", []any{map[string]any{"key": "pgbouncer"}}), "componentImages[0].image: required"},
		{"a major version twice", cnpgImages(cnpgImage(17), cnpgImage(16), cnpgImage(17)), "images[2].major: 17 is also the major version of images[0]"},
		{"a key twice", cnpgWith(cnpgImages(cnpgImage(17)), "componentImages", []any{
			map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
			map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:2"},
		}), `componentImages[1].key: "pgbouncer" is also the key of componentImages[0]`},
		{"unknown key", cnpgWith(cnpgImages(cnpgImage(17)), "catalog", map[string]any{}), notA + "postgresql.cnpg.io/v1 ImageCatalogSpec"},
		{"the object's spec", map[string]any{"spec": cnpgImages(cnpgImage(17))}, notA},
		{"image sub-key", cnpgImages(cnpgWith(cnpgImage(17), "tag", "17.2")), notA},
		{"major version a string", cnpgImages(map[string]any{"image": "registry.example/postgresql:17", "major": "17"}), notA},
		{"extension image a string", cnpgImages(cnpgImage(17, map[string]any{"name": "pgvector", "image": "registry.example/pgvector:1"})), notA},
		{"null image", cnpgImages(cnpgImage(17), nil), "images[1]"},
		{"two spellings", cnpgWith(cnpgImages(cnpgImage(17)), "Images", []any{}), "sets the same field as"},
	}

	backup := map[string]any{"cluster": cnpgCluster()}
	backupCases := func(minimal map[string]any, upstream string) []cnpgRefusal {
		return []cnpgRefusal{
			{"no cluster", cnpgWithout(minimal, "cluster"), "cluster: required"},
			{"null cluster", cnpgWith(minimal, "cluster", nil), "cluster: required"},
			{"cluster without a name", cnpgWith(minimal, "cluster", map[string]any{}), "cluster.name: required"},
			{"empty cluster name", cnpgWith(minimal, "cluster", map[string]any{"name": ""}), "cluster.name: required"},
			{"cluster name no Cluster can have", cnpgWith(minimal, "cluster", map[string]any{"name": "Not_A_Label"}), `cluster.name "Not_A_Label": must be a DNS-1035 label`},
			{"plugin without a name", cnpgWith(minimal, "pluginConfiguration", map[string]any{"parameters": map[string]any{"a": "b"}}), "pluginConfiguration.name: required"},
			// The type omits an empty method, and the operator would apply its default.
			{"an empty method", cnpgWith(minimal, "method", ""),
				`method: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "barmanObjectStore")`},
			{"unknown key", cnpgWith(minimal, "retention", "30d"), notA + upstream},
			{"the object's spec", map[string]any{"spec": minimal}, notA},
			{"cluster sub-key", cnpgWith(minimal, "cluster", map[string]any{"name": "db", "namespace": "other"}), notA},
			{"online a string", cnpgWith(minimal, "online", "true"), notA},
			{"plugin parameter not a string", cnpgWith(minimal, "pluginConfiguration", map[string]any{"name": "p", "parameters": map[string]any{"level": 3}}), notA},
			{"online configuration sub-key", cnpgWith(minimal, "onlineConfiguration", map[string]any{"waitForWAL": true}), notA},
			{"two spellings", cnpgWith(minimal, "Cluster", cnpgCluster()), "sets the same field as"},
		}
	}
	scheduled := cnpgWith(backup, "schedule", "0 0 3 * * *")

	role := cnpgDatabaseRoleMinimal()
	publication := cnpgPublicationOf(map[string]any{"allTables": true})
	objects := func(objects ...any) map[string]any {
		return cnpgPublicationOf(map[string]any{"objects": objects})
	}
	table := func(name string, extra ...string) map[string]any {
		t := map[string]any{"name": name}
		if len(extra) > 0 {
			columns := make([]any, len(extra))
			for i, c := range extra {
				columns[i] = c
			}
			t["columns"] = columns
		}
		return map[string]any{"table": t}
	}
	subscription := cnpgSubscriptionMinimal()

	return map[string][]cnpgRefusal{
		"cnpg-imagecatalog":        catalog,
		"cnpg-clusterimagecatalog": catalog,
		"cnpg-backup":              backupCases(backup, "postgresql.cnpg.io/v1 BackupSpec"),
		"cnpg-scheduledbackup": append(backupCases(scheduled, "postgresql.cnpg.io/v1 ScheduledBackupSpec"),
			cnpgRefusal{"no schedule", backup, "schedule: required"},
			cnpgRefusal{"null schedule", cnpgWith(backup, "schedule", nil), "schedule: required"},
			cnpgRefusal{"suspend a string", cnpgWith(scheduled, "suspend", "no"), notA},
			cnpgRefusal{"an empty owner reference", cnpgWith(scheduled, "backupOwnerReference", ""),
				`backupOwnerReference: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "none")`},
		),
		"cnpg-databaserole": {
			{"no cluster", cnpgWithout(role, "cluster"), "cluster: required"},
			{"cluster without a name", cnpgWith(role, "cluster", map[string]any{}), "cluster.name: required"},
			{"no name", cnpgWithout(role, "name"), "name: required"},
			{"null name", cnpgWith(role, "name", nil), "name: required"},
			{"password secret without a name", cnpgWith(role, "passwordSecret", map[string]any{}), "passwordSecret.name: required"},
			// The eight rules of the CRD that read one document.
			{"an empty name", cnpgWith(role, "name", ""), "name: empty, the DatabaseRole CRD refuses a role with no name"},
			{"the superuser's name", cnpgWith(role, "name", "postgres"), `name "postgres": reserved, the DatabaseRole CRD refuses it`},
			{"the replication user's name", cnpgWith(role, "name", "streaming_replica"), `name "streaming_replica": reserved`},
			{"a name PostgreSQL reserves", cnpgWith(role, "name", "pg_app"), `name "pg_app": a name that starts with pg_ is reserved by PostgreSQL`},
			{"a name the operator reserves", cnpgWith(role, "name", "cnpg_app"), `name "cnpg_app": a name that starts with cnpg_ is reserved by the operator`},
			{"ensure absent", cnpgWith(role, "ensure", "absent"), "ensure: absent is not supported for a DatabaseRole"},
			{"a password and none", cnpgWith(cnpgWith(role, "passwordSecret", map[string]any{"name": "app-password"}), "disablePassword", true), "passwordSecret and disablePassword: true are both set"},
			{"a certificate without login", cnpgWith(role, "clientCertificate", map[string]any{"enabled": true}), "clientCertificate: an enabled client certificate requires login: true"},
			{"a certificate enabled by default, without login", cnpgWith(role, "clientCertificate", map[string]any{}), "clientCertificate: an enabled client certificate requires login: true"},
			{"a certificate with login false", cnpgWith(cnpgWith(role, "clientCertificate", map[string]any{}), "login", false), "clientCertificate: an enabled client certificate requires login: true"},
			// The type omits a zero limit, and the operator would apply -1.
			{"a connection limit of zero", cnpgWith(role, "connectionLimit", 0), "connectionLimit: 0 cannot be carried by the CloudNativePG API types"},
			// So it omits an empty ensure and reclaim policy, and the operator would apply theirs.
			{"an empty ensure", cnpgWith(role, "ensure", ""),
				`ensure: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "present")`},
			{"an empty reclaim policy", cnpgWith(role, "databaseRoleReclaimPolicy", ""),
				`databaseRoleReclaimPolicy: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "retain")`},
			// The type writes the zero time as null and a time to the second.
			{"validUntil the zero time", cnpgWith(role, "validUntil", "0001-01-01T00:00:00Z"), "validUntil: the zero time cannot be carried by the CloudNativePG API types"},
			{"validUntil the zero time with an offset", cnpgWith(role, "validUntil", "0001-01-01T02:00:00+02:00"), "validUntil: the zero time cannot be carried"},
			// The type writes the time in UTC, where an offset can move it out of four-digit years.
			{"validUntil past the year 9999 in UTC", cnpgWith(role, "validUntil", "9999-12-31T23:00:00-02:00"), "validUntil: the CloudNativePG API types write it as 10000-01-01T01:00:00Z, which the DatabaseRole CRD's date-time format refuses"},
			{"validUntil before the year 0 in UTC", cnpgWith(role, "validUntil", "0000-01-01T00:30:00+01:00"), "validUntil: the CloudNativePG API types write it as -0001-12-31T23:30:00Z, which the DatabaseRole CRD's date-time format refuses"},
			{"validUntil with a fraction of a second", cnpgWith(role, "validUntil", "2030-01-01T02:00:00.5+02:00"), `validUntil "2030-01-01T02:00:00.5+02:00": a fraction of a second cannot be carried by the CloudNativePG API types`},
			// The decode keeps nine digits of a fraction: the tenth is read from the authored string.
			{"validUntil with a fraction past the ninth digit", cnpgWith(role, "validUntil", "2030-01-01T00:00:00.0000000001Z"), `validUntil "2030-01-01T00:00:00.0000000001Z": a fraction of a second cannot be carried`},
			// The decode's parse admits a comma before the fraction and an hour of one digit.
			{"validUntil with a fraction after a comma", cnpgWith(role, "validUntil", "2030-01-01T00:00:00,5Z"), `validUntil "2030-01-01T00:00:00,5Z": a fraction of a second cannot be carried`},
			{"validUntil with a one-digit hour and a fraction", cnpgWith(role, "validUntil", "2030-01-01T0:00:00.5Z"), `validUntil "2030-01-01T0:00:00.5Z": a fraction of a second cannot be carried`},
			{"validUntil with a one-digit hour and a fraction past the ninth digit", cnpgWith(role, "validUntil", "2030-01-01T0:00:00.0000000001Z"), `validUntil "2030-01-01T0:00:00.0000000001Z": a fraction of a second cannot be carried`},
			// The decode matches a key in another case to the field, and so does the check.
			{"validUntil spelled in another case, with a fraction", cnpgWith(role, "ValidUntil", "2030-01-01T00:00:00.5Z"), `validUntil "2030-01-01T00:00:00.5Z": a fraction of a second cannot be carried`},
			{"unknown key", cnpgWith(role, "password", "hunter2"), notA + "postgresql.cnpg.io/v1 DatabaseRoleSpec"},
			{"the object's spec", map[string]any{"spec": role}, notA},
			{"the embedded type's name", cnpgWith(role, "RoleConfiguration", map[string]any{"login": true}), notA},
			{"validUntil not a time", cnpgWith(role, "validUntil", "next year"), notA},
			{"inRoles a string", cnpgWith(role, "inRoles", "readers"), notA},
			{"login a string", cnpgWith(role, "login", "yes"), notA},
			{"connection limit a string", cnpgWith(role, "connectionLimit", "20"), notA},
			{"password secret sub-key", cnpgWith(role, "passwordSecret", map[string]any{"name": "app-password", "key": "password"}), notA},
			{"null role", cnpgWith(role, "inRoles", []any{"readers", nil}), "inRoles[1]"},
			{"two spellings", cnpgWith(role, "Name", "other"), "sets the same field as"},
		},
		"cnpg-publication": {
			{"no cluster", cnpgWithout(publication, "cluster"), "cluster: required"},
			{"cluster without a name", cnpgWith(publication, "cluster", map[string]any{}), "cluster.name: required"},
			{"no name", cnpgWithout(publication, "name"), "name: required"},
			{"no database", cnpgWithout(publication, "dbname"), "dbname: required"},
			{"no target", cnpgWithout(publication, "target"), "target: required"},
			{"null target", cnpgWith(publication, "target", nil), "target: required"},
			{"table without a name", objects(map[string]any{"table": map[string]any{"schema": "public"}}), "target.objects[0].table.name: required"},
			// The three rules of the CRD that read one document.
			{"an empty target", cnpgPublicationOf(map[string]any{}), "target: one of allTables: true and objects is required"},
			{"allTables false and no object", cnpgPublicationOf(map[string]any{"allTables": false}), "target: one of allTables: true and objects is required"},
			{"an empty list of objects", cnpgPublicationOf(map[string]any{"objects": []any{}}), "target: one of allTables: true and objects is required"},
			{"all tables and objects", cnpgPublicationOf(map[string]any{"allTables": true, "objects": []any{table("orders")}}), "target: allTables and objects are both set"},
			{"an empty object", objects(table("orders"), map[string]any{}), "target.objects[1]: one of tablesInSchema and table is required"},
			{"an object of both forms", objects(map[string]any{"tablesInSchema": "sales", "table": map[string]any{"name": "orders"}}), "target.objects[0]: tablesInSchema and table are both set"},
			{"columns beside a schema's tables", objects(map[string]any{"tablesInSchema": "sales"}, table("orders", "id", "total")), "target.objects[1].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[0])"},
			{"columns before a schema's tables", objects(table("orders", "id"), map[string]any{"tablesInSchema": "sales"}), "target.objects[0].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[1])"},
			{"an empty reclaim policy", cnpgWith(publication, "publicationReclaimPolicy", ""),
				`publicationReclaimPolicy: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "retain")`},
			{"unknown key", cnpgWith(publication, "tables", []any{}), notA + "postgresql.cnpg.io/v1 PublicationSpec"},
			{"the object's spec", map[string]any{"spec": publication}, notA},
			{"target sub-key", cnpgPublicationOf(map[string]any{"allTables": true, "schemas": []any{"sales"}}), notA},
			{"allTables a string", cnpgPublicationOf(map[string]any{"allTables": "true"}), notA},
			{"parameter not a string", cnpgWith(publication, "parameters", map[string]any{"publish_via_partition_root": true}), notA},
			{"null object", objects(table("orders"), nil), "target.objects[1]"},
			{"two spellings", cnpgWith(publication, "dbName", "other"), "sets the same field as"},
		},
		"cnpg-subscription": {
			{"no cluster", cnpgWithout(subscription, "cluster"), "cluster: required"},
			{"cluster without a name", cnpgWith(subscription, "cluster", map[string]any{}), "cluster.name: required"},
			{"no name", cnpgWithout(subscription, "name"), "name: required"},
			{"no database", cnpgWithout(subscription, "dbname"), "dbname: required"},
			{"no publication", cnpgWithout(subscription, "publicationName"), "publicationName: required"},
			{"null publication", cnpgWith(subscription, "publicationName", nil), "publicationName: required"},
			{"no external cluster", cnpgWithout(subscription, "externalClusterName"), "externalClusterName: required"},
			{"an empty reclaim policy", cnpgWith(subscription, "subscriptionReclaimPolicy", ""),
				`subscriptionReclaimPolicy: "" cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default "retain")`},
			{"unknown key", cnpgWith(subscription, "connection", "host=origin"), notA + "postgresql.cnpg.io/v1 SubscriptionSpec"},
			{"the object's spec", map[string]any{"spec": subscription}, notA},
			{"cluster a string", cnpgWith(subscription, "cluster", "db"), notA},
			{"parameter not a string", cnpgWith(subscription, "parameters", map[string]any{"copy_data": false}), notA},
			{"two spellings", cnpgWith(subscription, "publicationname", "other"), "sets the same field as"},
		},
	}
}

// cnpgFurtherKind returns the row of policyFreeKinds for a component.
func cnpgFurtherKind(t *testing.T, component string) policyFreeKind {
	t.Helper()
	for _, kind := range policyFreeKinds {
		if kind.component == component {
			return kind
		}
	}
	t.Fatalf("policyFreeKinds has no row for %s", component)
	return policyFreeKind{}
}

// cnpgCatalogSpec builds a catalog of either kind and returns its spec.
func cnpgCatalogSpec(t *testing.T, component string, props map[string]any) cnpgv1.ImageCatalogSpec {
	t.Helper()
	switch catalog := cnpgFurtherKind(t, component).generate(t, "pg", props).(type) {
	case *cnpgv1.ImageCatalog:
		return catalog.Spec
	case *cnpgv1.ClusterImageCatalog:
		return catalog.Spec
	default:
		t.Fatalf("%s built a %T", component, catalog)
		return cnpgv1.ImageCatalogSpec{}
	}
}

// TestCnpgFurtherKinds_AuthoredValuesArriveTyped reads authored values back
// from the typed objects: lists keep their order, an authored false behind a
// pointer is kept, and the values the build refuses on one field are accepted
// where the API takes them.
func TestCnpgFurtherKinds_AuthoredValuesArriveTyped(t *testing.T) {
	for _, component := range []string{"cnpg-imagecatalog", "cnpg-clusterimagecatalog"} {
		spec := cnpgCatalogSpec(t, component, cnpgImageCatalogFull())
		if len(spec.Images) != 2 || spec.Images[0].Major != 17 || spec.Images[1].Major != 16 {
			t.Fatalf("%s: images = %+v, want majors 17 and 16 in authored order", component, spec.Images)
		}
		extensions := spec.Images[0].Extensions
		if len(extensions) != 2 || extensions[0].ImageVolumeSource.Reference != "registry.example/pgvector:0.8.0" || extensions[0].ImageVolumeSource.PullPolicy != "IfNotPresent" {
			t.Errorf("%s: extensions = %+v, want the authored image volume on the first", component, extensions)
		}
		if len(extensions) == 2 && extensions[1].ImageVolumeSource.Reference != "" {
			t.Errorf("%s: an extension that names no image holds %q", component, extensions[1].ImageVolumeSource.Reference)
		}
		if keys := []string{spec.ComponentImages[0].Key, spec.ComponentImages[1].Key}; !slices.Equal(keys, []string{"pgbouncer", "exporter"}) {
			t.Errorf("%s: component images = %v, want them in authored order", component, keys)
		}
	}

	backup := cnpgFurtherKind(t, "cnpg-backup").generate(t, "nightly", cnpgBackupFull()).(*cnpgv1.Backup)
	if backup.Spec.Online == nil || *backup.Spec.Online {
		t.Errorf("online = %v, want the authored false", backup.Spec.Online)
	}
	if oc := backup.Spec.OnlineConfiguration; oc == nil || oc.WaitForArchive == nil || *oc.WaitForArchive || oc.ImmediateCheckpoint == nil || !*oc.ImmediateCheckpoint {
		t.Errorf("onlineConfiguration = %+v, want waitForArchive false and immediateCheckpoint true", oc)
	}
	if backup.Spec.Cluster.Name != "db" || backup.Spec.Method != cnpgv1.BackupMethodPlugin || backup.Spec.Target != cnpgv1.BackupTargetStandby {
		t.Errorf("backup spec = %+v, want cluster db, the plugin method and the standby target", backup.Spec)
	}

	scheduled := cnpgFurtherKind(t, "cnpg-scheduledbackup").generate(t, "nightly", cnpgScheduledBackupFull()).(*cnpgv1.ScheduledBackup)
	if scheduled.Spec.Suspend == nil || *scheduled.Spec.Suspend {
		t.Errorf("suspend = %v, want the authored false", scheduled.Spec.Suspend)
	}
	if scheduled.Spec.Immediate == nil || !*scheduled.Spec.Immediate || scheduled.Spec.Schedule != "0 0 3 * * *" {
		t.Errorf("scheduled backup spec = %+v, want immediate and the authored schedule", scheduled.Spec)
	}

	roles := cnpgFurtherKind(t, "cnpg-databaserole")
	role := roles.generate(t, "app", cnpgDatabaseRoleFull()).(*cnpgv1.DatabaseRole)
	if role.Spec.Inherit == nil || *role.Spec.Inherit {
		t.Errorf("inherit = %v, want the authored false", role.Spec.Inherit)
	}
	if role.Spec.ConnectionLimit != 20 || !slices.Equal(role.Spec.InRoles, []string{"pg_monitor", "readers"}) {
		t.Errorf("connectionLimit = %d, inRoles = %v; want 20 and the roles in authored order", role.Spec.ConnectionLimit, role.Spec.InRoles)
	}
	if want := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC); role.Spec.ValidUntil == nil || !role.Spec.ValidUntil.Time.Equal(want) {
		t.Errorf("validUntil = %v, want %v", role.Spec.ValidUntil, want)
	}
	// A fraction of zeros is no fraction to cut off, and an instant whose year
	// in UTC has four digits is written as authored, whatever its offset.
	for authored, want := range map[string]time.Time{
		"2030-01-01T00:00:00.000Z":        time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		"2030-01-01T00:00:00,0000000000Z": time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		"9999-12-31T23:59:59Z":            time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		"9999-12-31T23:00:00+02:00":       time.Date(9999, 12, 31, 21, 0, 0, 0, time.UTC),
	} {
		accepted := roles.generate(t, "app", cnpgWith(cnpgDatabaseRoleMinimal(), "validUntil", authored)).(*cnpgv1.DatabaseRole)
		if accepted.Spec.ValidUntil == nil || !accepted.Spec.ValidUntil.Time.Equal(want) {
			t.Errorf("validUntil %q = %v, want %v", authored, accepted.Spec.ValidUntil, want)
		}
	}
	if role.Spec.PasswordSecret == nil || role.Spec.PasswordSecret.Name != "app-password" || role.Spec.DisablePassword {
		t.Errorf("passwordSecret = %+v, disablePassword = %v; want the authored Secret and no disabled password", role.Spec.PasswordSecret, role.Spec.DisablePassword)
	}
	if without := roles.generate(t, "app", cnpgDatabaseRoleWithoutPassword()).(*cnpgv1.DatabaseRole); !without.Spec.DisablePassword || without.Spec.PasswordSecret != nil {
		t.Errorf("disablePassword = %v, passwordSecret = %+v; want a disabled password and no Secret", without.Spec.DisablePassword, without.Spec.PasswordSecret)
	}
	// -1 is the API's own default, no limit; only a zero cannot be carried.
	if unlimited := roles.generate(t, "app", cnpgWith(cnpgDatabaseRoleMinimal(), "connectionLimit", -1)).(*cnpgv1.DatabaseRole); unlimited.Spec.ConnectionLimit != -1 {
		t.Errorf("connectionLimit = %d, want the authored -1", unlimited.Spec.ConnectionLimit)
	}
	// A certificate that is authored off asks nothing of login.
	off := roles.generate(t, "app", cnpgWith(cnpgDatabaseRoleMinimal(), "clientCertificate", map[string]any{"enabled": false})).(*cnpgv1.DatabaseRole)
	if cert := off.Spec.ClientCertificate; cert == nil || cert.Enabled == nil || *cert.Enabled {
		t.Errorf("clientCertificate = %+v, want the authored enabled: false", cert)
	}
	// A reserved name is reserved as a whole name or as a prefix, not inside one.
	if named := roles.generate(t, "app", cnpgWith(cnpgDatabaseRoleMinimal(), "name", "app_pg_postgres")).(*cnpgv1.DatabaseRole); named.Spec.Name != "app_pg_postgres" {
		t.Errorf("name = %q, want the authored one", named.Spec.Name)
	}

	publications := cnpgFurtherKind(t, "cnpg-publication")
	publication := publications.generate(t, "pub", cnpgPublicationFull()).(*cnpgv1.Publication)
	if objs := publication.Spec.Target.Objects; len(objs) != 2 || objs[0].TablesInSchema != "sales" || objs[1].Table == nil || objs[1].Table.Name != "orders" || !objs[1].Table.Only {
		t.Errorf("target.objects = %+v, want the schema then the table, in authored order", objs)
	}
	// An authored allTables: false says what an omitted one says, so it is
	// accepted beside objects and left out.
	explicit := cnpgPublicationOf(map[string]any{"allTables": false, "objects": []any{map[string]any{"tablesInSchema": "sales"}}})
	if built := publications.generate(t, "pub", explicit).(*cnpgv1.Publication); built.Spec.Target.AllTables || len(built.Spec.Target.Objects) != 1 {
		t.Errorf("target = %+v, want the one authored object and no allTables", built.Spec.Target)
	}
	// Columns are refused beside a schema's tables only.
	columns := cnpgPublicationOf(map[string]any{"objects": []any{
		map[string]any{"table": map[string]any{"name": "orders", "columns": []any{"id", "total"}}},
		map[string]any{"table": map[string]any{"name": "customers"}},
	}})
	if built := publications.generate(t, "pub", columns).(*cnpgv1.Publication); !slices.Equal(built.Spec.Target.Objects[0].Table.Columns, []string{"id", "total"}) {
		t.Errorf("columns = %v, want them in authored order", built.Spec.Target.Objects[0].Table.Columns)
	}

	subscription := cnpgFurtherKind(t, "cnpg-subscription").generate(t, "sub", cnpgSubscriptionFull()).(*cnpgv1.Subscription)
	if s := subscription.Spec; s.ClusterRef.Name != "db" || s.PublicationName != "pub" || s.PublicationDBName != "orders" || s.ExternalClusterName != "origin" || s.Parameters["copy_data"] != "false" {
		t.Errorf("subscription spec = %+v, want the authored values", s)
	}
}

// TestCnpgImageCatalogs_ImagesAreHeldToTheAllowedRegistries: a catalog runs no
// pod, and every image it names is one the operator runs for a Cluster, so each
// of the three fields that name an image is held to the allowed registries, by
// a refusal of the registry class that names the field.
func TestCnpgImageCatalogs_ImagesAreHeldToTheAllowedRegistries(t *testing.T) {
	const other = "other.example/postgresql:17.2"
	extension := func(reference string) map[string]any {
		return map[string]any{"name": "pgvector", "image": map[string]any{"reference": reference}}
	}
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"an image", cnpgImages(cnpgWith(cnpgImage(17), "image", other)), "images[0].image"},
		{"a later image", cnpgImages(cnpgImage(17), cnpgWith(cnpgImage(16), "image", other)), "images[1].image"},
		// An image that names no registry host is one of Docker Hub.
		{"an image without a registry host", cnpgImages(cnpgWith(cnpgImage(17), "image", "postgres:17")), "images[0].image"},
		{"an extension's image", cnpgImages(cnpgImage(17, extension("registry.example/pgvector:1"), extension(other))), "images[0].extensions[1].image.reference"},
		{"a component image", cnpgWith(cnpgImages(cnpgImage(17)), "componentImages", []any{
			map[string]any{"key": "pgbouncer", "image": "registry.example/pgbouncer:1"},
			map[string]any{"key": "exporter", "image": other},
		}), "componentImages[1].image"},
	}
	for _, component := range []string{"cnpg-imagecatalog", "cnpg-clusterimagecatalog"} {
		kind := cnpgFurtherKind(t, component)
		for _, tc := range cases {
			t.Run(component+"/"+tc.name, func(t *testing.T) {
				_, err := pvTransform(component, kind.handler, tc.props, rcStrict())
				rcWantClass(t, err, oam.RefusalRegistry)
				if err == nil || !strings.Contains(err.Error(), tc.want+": ") {
					t.Errorf("err = %v, want it to name %s", err, tc.want)
				}
				// A policy that lists no registry allows every one, and so does
				// a build under no policy.
				for name, policy := range map[string]oam.Policy{"no allowed registries": &stubPolicy{}, "no policy": nil} {
					if _, err := pvTransform(component, kind.handler, tc.props, policy); err != nil {
						t.Errorf("%s: %v, want the catalog built", name, err)
					}
				}
			})
		}
	}
}
