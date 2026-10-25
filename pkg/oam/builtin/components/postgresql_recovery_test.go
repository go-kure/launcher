package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// recoveryStore is a valid inline recovery.source.objectStore with key set to
// value on top; recoveryFrom a valid recovery reading an existing store, with
// key set to value on top.
func recoveryStore(key string, value any) map[string]any {
	return map[string]any{"destinationPath": "s3://bucket/origin", "s3Credentials": map[string]any{"inheritFromIAMRole": true}, key: value}
}

func recoveryFrom(key string, value any) map[string]any {
	return map[string]any{"source": map[string]any{"serverName": "origin", "objectStoreName": "origin"}, key: value}
}

// recoveryTarget is recoveryFrom with the target t.
func recoveryTarget(t map[string]any) map[string]any {
	return recoveryFrom("target", t)
}

// TestPostgresqlRecovery_Refusals: each of the plan's refusals of `recovery`,
// by its text, and the refusals of CloudNativePG's webhook that need no
// cluster (validateRecoveryTarget), at parse time.
func TestPostgresqlRecovery_Refusals(t *testing.T) {
	backupID := "20261020T030000"
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"an unknown key", map[string]any{"recovery": recoveryFrom("backup", "x")}, `recovery: unrecognized key "backup"`},
		{"no source", map[string]any{"recovery": map[string]any{}}, "recovery.source: required"},
		{"an unknown source key", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStoreName": "origin", "endpointURL": "x"}}}, `recovery.source: unrecognized key "endpointURL"`},
		{"no serverName", map[string]any{"recovery": map[string]any{"source": map[string]any{"objectStoreName": "origin"}}}, "recovery.source.serverName: required"},
		{"an empty serverName", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "", "objectStoreName": "origin"}}}, "recovery.source.serverName: must not be empty"},
		{"neither store", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin"}}}, "recovery.source: one of objectStoreName (an existing ObjectStore in the namespace) and objectStore (one defined here) is required"},
		{"both stores", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStoreName": "origin", "objectStore": recoveryStore("endpointURL", "https://s3.example")}}}, "recovery.source: objectStoreName and objectStore are mutually exclusive"},
		{"an empty objectStoreName", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStoreName": ""}}}, "recovery.source.objectStoreName: must not be empty"},
		{"an inline store without a path", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStore": map[string]any{"s3Credentials": map[string]any{"inheritFromIAMRole": true}}}}}, "recovery.source.objectStore.destinationPath: required (the object store path the cluster is recovered from)"},
		{"an inline store's serverName", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStore": recoveryStore("serverName", "origin")}}}, "recovery.source.objectStore.serverName: not allowed (the ObjectStore CRD forbids it); use recovery.source.serverName"},
		{"an inline store's Azure credentials", map[string]any{"recovery": map[string]any{"source": map[string]any{"serverName": "origin", "objectStore": recoveryStore("azureCredentials", map[string]any{})}}}, "recovery.source.objectStore.azureCredentials: not supported (S3-compatible storage only"},
		{"with pg_basebackup", map[string]any{
			"recovery":         recoveryFrom("database", "app"),
			"bootstrap":        map[string]any{"pg_basebackup": map[string]any{"source": "origin"}},
			"externalClusters": []any{map[string]any{"name": "origin", "connectionParameters": map[string]any{"host": "h"}}},
		}, "recovery: not with bootstrap.pg_basebackup"},
		{"an externalClusters entry of the source's name", map[string]any{
			"recovery":         recoveryFrom("database", "app"),
			"externalClusters": []any{map[string]any{"name": "db-recovery-source", "connectionParameters": map[string]any{"host": "h"}}},
		}, `externalClusters[0].name "db-recovery-source": the name of the entry recovery's source is written as`},

		{"an unknown target key", map[string]any{"recovery": recoveryTarget(map[string]any{"tli": "1"})}, `recovery.target: unrecognized key "tli"`},
		{"two targets", map[string]any{"recovery": recoveryTarget(map[string]any{"time": "2026-10-20T03:00:00Z", "lsn": "0/3000060"})}, "recovery.target: time, lsn are mutually exclusive; set at most one"},
		{"immediate false is a target", map[string]any{"recovery": recoveryTarget(map[string]any{"lsn": "0/3000060", "immediate": false})}, "recovery.target: lsn, immediate are mutually exclusive"},
		{"a time that does not parse", map[string]any{"recovery": recoveryTarget(map[string]any{"time": "yesterday"})}, `recovery.target.time "yesterday": not a time`},
		{"an LSN that does not parse", map[string]any{"recovery": recoveryTarget(map[string]any{"lsn": "3000060"})}, `recovery.target.lsn "3000060": not a WAL location`},
		{"an xid that is no integer", map[string]any{"recovery": recoveryTarget(map[string]any{"xid": "x1", "backupID": backupID})}, `recovery.target.xid "x1": must be a transaction ID`},
		{"an xid above 32 bits", map[string]any{"recovery": recoveryTarget(map[string]any{"xid": "4294967296", "backupID": backupID})}, `recovery.target.xid "4294967296"`},
		{"a 64-byte name", map[string]any{"recovery": recoveryTarget(map[string]any{"name": strings.Repeat("n", 64), "backupID": backupID})}, "must be shorter than 64 bytes"},
		{"a name with a control character", map[string]any{"recovery": recoveryTarget(map[string]any{"name": "a\tb", "backupID": backupID})}, "must not contain control characters"},
		{"an xid without backupID", map[string]any{"recovery": recoveryTarget(map[string]any{"xid": "100"})}, "recovery.target.backupID: required with recovery.target.xid"},
		{"a name without backupID", map[string]any{"recovery": recoveryTarget(map[string]any{"name": "before"})}, "recovery.target.backupID: required with recovery.target.name"},
		{"immediate without backupID", map[string]any{"recovery": recoveryTarget(map[string]any{"immediate": true})}, "recovery.target.backupID: required with recovery.target.immediate"},
		{"timeline 0", map[string]any{"recovery": recoveryTarget(map[string]any{"timeline": "0"})}, `recovery.target.timeline "0": must be latest or a positive integer`},
		{"timeline current", map[string]any{"recovery": recoveryTarget(map[string]any{"timeline": "current"})}, `recovery.target.timeline "current"`},
		{"an empty target string", map[string]any{"recovery": recoveryTarget(map[string]any{"lsn": ""})}, "recovery.target.lsn: must not be empty"},

		{"an empty database", map[string]any{"recovery": recoveryFrom("database", "")}, "recovery.database: must not be empty"},
		{"an empty owner", map[string]any{"recovery": recoveryFrom("owner", "")}, "recovery.owner: must not be empty"},
		{"a secret without a name", map[string]any{"recovery": recoveryFrom("secret", map[string]any{})}, "recovery.secret.name: required"},
		{"a secret with an empty name", map[string]any{"recovery": recoveryFrom("secret", map[string]any{"name": ""})}, "recovery.secret.name: must not be empty"},
		{"a secret's unknown key", map[string]any{"recovery": recoveryFrom("secret", map[string]any{"name": "s", "key": "k"})}, `recovery.secret: unrecognized key "key"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestPostgresqlRecovery_Lowering: `recovery` lowers into the Cluster's
// bootstrap.recovery, whose source is the externalClusters entry the rule
// writes for the barman-cloud plugin, and for an inline store into a
// cnpg-objectstore of that configuration, which the entry names.
func TestPostgresqlRecovery_Lowering(t *testing.T) {
	immediate, exclusive := false, true
	cases := []struct {
		name      string
		recovery  map[string]any
		bootstrap cnpgv1.BootstrapRecovery
		store     string
	}{
		{
			name:      "to the end of the WAL, from an existing store",
			recovery:  recoveryFrom("owner", "app"),
			bootstrap: cnpgv1.BootstrapRecovery{Source: "db-recovery-source", Owner: "app"},
			store:     "origin",
		},
		{
			name: "to a time, from an inline store",
			recovery: map[string]any{
				"source": map[string]any{"serverName": "origin", "objectStore": recoveryStore("endpointURL", "https://s3.example")},
				"target": map[string]any{"time": "2026-10-20 03:00:00", "timeline": "latest"},
			},
			bootstrap: cnpgv1.BootstrapRecovery{Source: "db-recovery-source", RecoveryTarget: &cnpgv1.RecoveryTarget{TargetTime: "2026-10-20 03:00:00", TargetTLI: "latest"}},
			store:     "db-recovery-source",
		},
		{
			// immediate counts whenever it is set, false included.
			name:      "immediate false, with a backup ID",
			recovery:  recoveryTarget(map[string]any{"immediate": false, "backupID": "20261020T030000", "exclusive": true}),
			bootstrap: cnpgv1.BootstrapRecovery{Source: "db-recovery-source", RecoveryTarget: &cnpgv1.RecoveryTarget{TargetImmediate: &immediate, BackupID: "20261020T030000", Exclusive: &exclusive}},
			store:     "origin",
		},
		{
			name: "to an xid, into a database of an owner with its secret",
			recovery: map[string]any{
				"source":   map[string]any{"serverName": "origin", "objectStoreName": "origin"},
				"target":   map[string]any{"xid": "4294967295", "backupID": "20261020T030000"},
				"database": "orders", "owner": "orders_owner", "secret": map[string]any{"name": "orders-creds"},
			},
			bootstrap: cnpgv1.BootstrapRecovery{
				Source: "db-recovery-source", Database: "orders", Owner: "orders_owner", Secret: &cnpgv1.LocalObjectReference{Name: "orders-creds"},
				RecoveryTarget: &cnpgv1.RecoveryTarget{TargetXID: "4294967295", BackupID: "20261020T030000"},
			},
			store: "origin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := newPostgresqlApp(t, map[string]any{"recovery": tc.recovery})
			objs := generatePostgresql(t, pc)
			cluster := (*objs[0]).(*cnpgv1.Cluster)
			if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
				t.Fatal("no bootstrap.recovery")
			}
			if got := *cluster.Spec.Bootstrap.Recovery; !reflect.DeepEqual(got, tc.bootstrap) {
				t.Errorf("bootstrap.recovery = %+v, want %+v", got, tc.bootstrap)
			}
			want := []cnpgv1.ExternalCluster{{Name: "db-recovery-source", PluginConfiguration: &cnpgv1.PluginConfiguration{
				Name:       "barman-cloud.cloudnative-pg.io",
				Parameters: map[string]string{"barmanObjectName": tc.store, "serverName": "origin"},
			}}}
			if !reflect.DeepEqual(cluster.Spec.ExternalClusters, want) {
				t.Errorf("externalClusters = %+v, want %+v", cluster.Spec.ExternalClusters, want)
			}

			comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"recovery": tc.recovery}}
			res := lowerPostgresql(t, &comp, nil)
			var stores []string
			for _, c := range res.Components {
				if c.Type == "cnpg-objectstore" {
					stores = append(stores, c.Name)
				}
			}
			wantStores := []string(nil)
			if tc.store == "db-recovery-source" {
				wantStores = []string{"db-recovery-source"}
			}
			if !slices.Equal(stores, wantStores) {
				t.Errorf("emitted ObjectStores %v, want %v", stores, wantStores)
			}
		})
	}
}

// TestPostgresqlRecovery_StoreNameTaken: the inline store is a component of
// its own, so a document component of its name is refused.
func TestPostgresqlRecovery_StoreNameTaken(t *testing.T) {
	comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"recovery": map[string]any{
		"source": map[string]any{"serverName": "origin", "objectStore": recoveryStore("endpointURL", "https://s3.example")},
	}}}
	doc := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{comp, {Name: "db-recovery-source", Type: "webservice"}}}}
	_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
	const want = `recovery.source.objectStore: generates component "db-recovery-source", which is already the name of component "db-recovery-source" (type "webservice") in the document; rename one of them`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// TestPostgresqlRecovery_ArchiveIntoSource: a cluster that would archive its
// WAL into the folder it is recovered from is refused where launcher sees
// both: its own backup store, an inline store of the same endpoint and path,
// and a store another component of the document emits, each read in the
// folder the source names. Another folder, or a store outside the document,
// is admitted.
func TestPostgresqlRecovery_ArchiveIntoSource(t *testing.T) {
	iam := map[string]any{"inheritFromIAMRole": true}
	backup := func(serverName string) map[string]any {
		b := map[string]any{"objectStore": map[string]any{"destinationPath": "s3://bucket/pg", "endpointURL": "https://s3.example", "s3Credentials": iam}}
		if serverName != "" {
			b["serverName"] = serverName
		}
		return b
	}
	source := func(serverName, store string) map[string]any {
		return map[string]any{"source": map[string]any{"serverName": serverName, "objectStoreName": store}}
	}
	inline := func(serverName, path string) map[string]any {
		return map[string]any{"source": map[string]any{"serverName": serverName, "objectStore": map[string]any{"destinationPath": path, "endpointURL": "https://s3.example", "s3Credentials": iam}}}
	}
	other := []oam.Component{
		{Name: "old", Type: "postgresql", Properties: map[string]any{"backup": map[string]any{"objectStore": map[string]any{"destinationPath": "s3://bucket/pg", "endpointURL": "https://s3.example", "s3Credentials": iam}}}},
		{Name: "shared", Type: "cnpg-objectstore", Properties: map[string]any{"objectName": "shared-store", "configuration": map[string]any{"destinationPath": "s3://bucket/pg", "endpointURL": "https://s3.example", "s3Credentials": iam}}},
	}
	const refused = "recovery.source: the cluster would archive its WAL into the folder it is recovered from"
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"its own store, its own folder", map[string]any{"backup": backup(""), "recovery": source("db", "db")}, refused},
		{"its own store, a named folder", map[string]any{"backup": backup("v2"), "recovery": source("v2", "db")}, refused},
		{"its own store, another folder", map[string]any{"backup": backup("v2"), "recovery": source("db", "db")}, ""},
		{"an inline store of the same path", map[string]any{"backup": backup(""), "recovery": inline("db", "s3://bucket/pg")}, refused},
		{"an inline store of another path", map[string]any{"backup": backup(""), "recovery": inline("db", "s3://bucket/other")}, ""},
		{"another postgresql's store", map[string]any{"backup": backup(""), "recovery": source("db", "old")}, refused},
		{"a cnpg-objectstore by its objectName", map[string]any{"backup": backup(""), "recovery": source("db", "shared-store")}, refused},
		{"a cnpg-objectstore by its component name", map[string]any{"backup": backup(""), "recovery": source("db", "shared")}, ""},
		{"a store outside the document", map[string]any{"backup": backup(""), "recovery": source("db", "elsewhere")}, ""},
		{"no backup", map[string]any{"recovery": source("db", "old")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp := oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			doc := &oam.Application{Spec: oam.ApplicationSpec{Components: append([]oam.Component{comp}, other...)}}
			_, err := components.PostgresqlRule{}.LowerComponent(&comp, oam.LoweringContext{Document: doc, Namer: oam.NewNameAllocator()})
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
				t.Errorf("err = %v, want one starting %q", err, tc.want)
			}
		})
	}
}

// TestPostgresqlRecovery_StoreBundleTraits: where the document orders its
// components the inline store is in a group of its own before the Cluster's,
// so it carries the bundle traits as well as the Cluster and the members'
// group; where it does not, it shares the Cluster's bundle and carries none.
// An object trait reaches it either way.
func TestPostgresqlRecovery_StoreBundleTraits(t *testing.T) {
	comp := oam.Component{Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"pooler": map[string]any{"enabled": true},
			"recovery": map[string]any{
				"source": map[string]any{"serverName": "origin", "objectStore": recoveryStore("endpointURL", "https://s3.example")},
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
	everyObject := []string{"cnpg-cluster/db", "cnpg-objectstore/db-recovery-source", "cnpg-pooler/db-pooler"}

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
	if got, want := carriers(res, "fluxcd-patches"), everyObject; !slices.Equal(got, want) {
		t.Errorf("ordered: bundle trait on %v, want %v: the store's group, the Cluster's and the members'", got, want)
	}
	if got := carriers(res, "prune-protection"); !slices.Equal(got, everyObject) {
		t.Errorf("ordered: object trait on %v, want %v", got, everyObject)
	}
	// The store takes no dependency rule: the Cluster is ordered after it.
	want := []oam.ApplicationPolicy{{Name: "db-dependencies", Type: "dependency", Properties: map[string]any{
		"rules": []any{
			map[string]any{"component": "db-pooler", "dependsOn": []any{"db"}},
			map[string]any{"component": "api", "dependsOn": []any{"db-pooler"}},
		},
	}}}
	if !reflect.DeepEqual(res.Policies, want) {
		t.Errorf("policies = %+v, want %+v", res.Policies, want)
	}
}
