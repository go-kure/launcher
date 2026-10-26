package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin postgresql's `backup` (go-kure/launcher#1001): what it
// refuses, in what words, and where each field lands in the members it emits.

// backupWith is a `backup` of the least object store, with keys on top; a key
// of store is set on the object store instead.
func backupWith(store map[string]any, keys map[string]any) map[string]any {
	objectStore := postgresqlTestStore()
	for k, v := range store {
		objectStore[k] = v
	}
	backup := map[string]any{"objectStore": objectStore}
	for k, v := range keys {
		backup[k] = v
	}
	return map[string]any{"backup": backup}
}

// scheduleWith is a `backup` of one schedule "daily", with keys on top of it.
func scheduleWith(keys map[string]any) map[string]any {
	entry := map[string]any{"name": "daily", "schedule": "0 0 0 * * *"}
	for k, v := range keys {
		entry[k] = v
	}
	return backupWith(nil, map[string]any{"schedules": []any{entry}})
}

func TestPostgresqlBackup_Refusals(t *testing.T) {
	ref := func(name, key string) map[string]any { return map[string]any{"name": name, "key": key} }
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		// The top-level keys backup replaced, whatever their value.
		{"a top-level objectStore", map[string]any{"objectStore": map[string]any{"destinationPath": "s3://b"}},
			"objectStore: moved to backup.objectStore (its retentionPolicy and serverName to backup.retentionPolicy and backup.serverName; its secretName to backup.objectStore.s3Credentials)"},
		{"a null top-level objectStore", map[string]any{"objectStore": nil},
			"objectStore: moved to backup.objectStore"},
		{"a top-level objectStoreObjectName", map[string]any{"objectStoreObjectName": "store"},
			"objectStoreObjectName: moved to backup.objectStoreObjectName"},

		// backup's keys of before.
		{"backup.destinationPath", map[string]any{"backup": map[string]any{"destinationPath": "s3://b"}},
			"backup.destinationPath: removed; use backup.objectStore.destinationPath"},
		{"backup.endpointURL", map[string]any{"backup": map[string]any{"endpointURL": "https://s3.example"}},
			"backup.endpointURL: removed; use backup.objectStore.endpointURL"},
		{"backup.secretName", map[string]any{"backup": map[string]any{"secretName": "creds"}},
			"backup.secretName: removed; use backup.objectStore.s3Credentials (accessKeyId and secretAccessKey each reference a key of the Secret)"},
		{"an unknown key of backup", backupWith(nil, map[string]any{"tags": map[string]any{}}),
			`backup: unrecognized key "tags"`},

		// The object store.
		{"no object store", map[string]any{"backup": map[string]any{"retentionPolicy": "30d"}},
			"backup.objectStore: required (the object store backups and WAL are written to)"},
		{"the object store's serverName", backupWith(map[string]any{"serverName": "x"}, nil),
			"backup.objectStore.serverName: not allowed (the ObjectStore CRD forbids it); use backup.serverName"},
		{"Azure credentials", backupWith(map[string]any{"azureCredentials": map[string]any{}}, nil),
			"backup.objectStore.azureCredentials: not supported (S3-compatible storage only; the cnpg-objectstore kind takes Azure credentials)"},
		{"Google credentials", backupWith(map[string]any{"googleCredentials": map[string]any{}}, nil),
			"backup.objectStore.googleCredentials: not supported"},
		{"an unknown key of the object store", backupWith(map[string]any{"historyTags": map[string]any{}}, nil),
			`backup.objectStore: unrecognized key "historyTags"`},
		{"an unknown key of wal", backupWith(map[string]any{"wal": map[string]any{"archiveAdditionalCommandArgs": []any{}}}, nil),
			`backup.objectStore.wal: unrecognized key "archiveAdditionalCommandArgs"`},
		{"an unknown key of data", backupWith(map[string]any{"data": map[string]any{"additionalCommandArgs": []any{}}}, nil),
			`backup.objectStore.data: unrecognized key "additionalCommandArgs"`},
		{"no path", backupWith(map[string]any{"destinationPath": nil}, nil),
			"backup.objectStore.destinationPath: required (the object store path backups and WAL are written to)"},
		{"an empty path", backupWith(map[string]any{"destinationPath": ""}, nil),
			"backup.objectStore.destinationPath: must not be empty (the object store path backups and WAL are written to)"},
		{"no credentials", backupWith(map[string]any{"s3Credentials": nil}, nil),
			"backup.objectStore.s3Credentials: required (accessKeyId and secretAccessKey, or inheritFromIAMRole: true)"},
		{"an access key without its secret", backupWith(map[string]any{"s3Credentials": map[string]any{"accessKeyId": ref("creds", "ID")}}, nil),
			"backup.objectStore.s3Credentials"},
		{"keys and an IAM role at once", backupWith(map[string]any{"s3Credentials": map[string]any{
			"accessKeyId": ref("creds", "ID"), "secretAccessKey": ref("creds", "KEY"), "inheritFromIAMRole": true,
		}}, nil), "backup.objectStore.s3Credentials"},
		{"a key reference without its key", backupWith(map[string]any{"s3Credentials": map[string]any{
			"accessKeyId": map[string]any{"name": "creds"}, "secretAccessKey": ref("creds", "KEY"),
		}}, nil), "s3Credentials.accessKeyId.key"},
		{"a CA reference without its name", backupWith(map[string]any{"endpointCA": map[string]any{"key": "ca.crt"}}, nil),
			"endpointCA.name"},
		{"an unknown key of the credentials", backupWith(map[string]any{"s3Credentials": map[string]any{"inheritFromIAMRole": true, "token": "x"}}, nil),
			`unknown field "token"`},

		// The wal and data values the ObjectStore CRD bounds, which the
		// strict decode alone does not hold.
		{"a WAL compression of no enum value", backupWith(map[string]any{"wal": map[string]any{"compression": "brotli"}}, nil),
			`backup.objectStore.wal.compression "brotli": must be one of bzip2, gzip, lz4, snappy, xz, zstd`},
		{"a data compression only WAL takes", backupWith(map[string]any{"data": map[string]any{"compression": "zstd"}}, nil),
			`backup.objectStore.data.compression "zstd": must be one of bzip2, gzip, lz4, snappy`},
		{"an empty WAL compression", backupWith(map[string]any{"wal": map[string]any{"compression": ""}}, nil),
			`backup.objectStore.wal.compression "": must be one of`},
		{"a WAL encryption of no enum value", backupWith(map[string]any{"wal": map[string]any{"encryption": "aes256"}}, nil),
			`backup.objectStore.wal.encryption "aes256": must be one of AES256, aws:kms`},
		{"a data encryption of no enum value", backupWith(map[string]any{"data": map[string]any{"encryption": "kms"}}, nil),
			`backup.objectStore.data.encryption "kms": must be one of AES256, aws:kms`},
		{"a WAL parallelism of zero", backupWith(map[string]any{"wal": map[string]any{"maxParallel": 0}}, nil),
			"backup.objectStore.wal.maxParallel 0: must be at least 1"},
		{"a negative WAL parallelism", backupWith(map[string]any{"wal": map[string]any{"maxParallel": -2}}, nil),
			"backup.objectStore.wal.maxParallel -2: must be at least 1"},
		{"no data jobs", backupWith(map[string]any{"data": map[string]any{"jobs": 0}}, nil),
			"backup.objectStore.data.jobs 0: must be at least 1"},

		// The retention policy.
		{"a retention policy of no unit", backupWith(nil, map[string]any{"retentionPolicy": "30"}),
			`backup.retentionPolicy "30": must be a positive count of days, weeks or months, as <n><d|w|m> (for example 30d)`},
		{"a retention policy of years", backupWith(nil, map[string]any{"retentionPolicy": "1y"}),
			`backup.retentionPolicy "1y": must be a positive count`},
		{"a retention policy of zero days", backupWith(nil, map[string]any{"retentionPolicy": "0d"}),
			`backup.retentionPolicy "0d": must be a positive count`},

		// The schedules.
		{"a schedule without a name", backupWith(nil, map[string]any{"schedules": []any{map[string]any{"schedule": "@daily"}}}),
			"backup.schedules[0].name: required (the ScheduledBackup is named <component>-<name>)"},
		{"a schedule of an empty name", scheduleWith(map[string]any{"name": ""}),
			"backup.schedules[0].name: required"},
		{"two schedules of one name", backupWith(nil, map[string]any{"schedules": []any{
			map[string]any{"name": "daily", "schedule": "@daily"}, map[string]any{"name": "daily", "schedule": "@weekly"},
		}}), `backup.schedules[1].name "daily": already the name of backup.schedules[0]; each schedule needs a name of its own`},
		{"a schedule without its cron expression", backupWith(nil, map[string]any{"schedules": []any{map[string]any{"name": "daily"}}}),
			"backup.schedules[0].schedule: required (when a backup is taken, as a cron expression of six fields)"},
		{"a schedule that is no cron expression", scheduleWith(map[string]any{"schedule": "every night"}),
			`backup.schedules[0].schedule "every night": not a cron expression the operator accepts: `},
		{"a crontab line of five fields", scheduleWith(map[string]any{"schedule": "0 3 * * *"}),
			`backup.schedules[0].schedule "0 3 * * *": five fields, which the operator reads seconds first (seconds, minutes, hours, day of month, month), not as a crontab line; write six fields, seconds first ("0 0 3 * * *" as a crontab line, or "0 3 * * * *" as the operator reads it)`},
		{"five fields of which only the seconds-first reading parses", scheduleWith(map[string]any{"schedule": "0 45 * * *"}),
			`backup.schedules[0].schedule "0 45 * * *": five fields, which the operator reads seconds first (seconds, minutes, hours, day of month, month), not as a crontab line; write six fields, seconds first ("0 45 * * * *" as the operator reads it)`},
		{"a schedule of a minute out of range", scheduleWith(map[string]any{"schedule": "0 61 * * * *"}),
			`backup.schedules[0].schedule "0 61 * * * *": not a cron expression the operator accepts: `},

		// Schedules that parse but that the operator finds no time for
		// within its five-year look-ahead (go-kure/launcher#1032).
		{"February the 30th", scheduleWith(map[string]any{"schedule": "0 0 3 30 2 *"}),
			`backup.schedules[0].schedule "0 0 3 30 2 *": its time never comes, so the operator records NoSchedule for it`},
		// The next Friday January the 1st after 2010 is 2016.
		{"a Friday January the 1st at midnight", scheduleWith(map[string]any{"schedule": "0 0 0 */31 1 FRI"}),
			`backup.schedules[0].schedule "0 0 0 */31 1 FRI": no time from the end of 2010 through the end of 2015 (UTC), longer than the five years the operator looks ahead, so the operator records NoSchedule there and takes no further backup for it; choose a schedule whose time comes at least every five years`},
		{"a Friday January the 1st at 03:00", scheduleWith(map[string]any{"schedule": "0 0 3 */31 1 FRI"}),
			"no time from the end of 2010 through the end of 2015 (UTC)"},
		// The gap starts at the cycle's first year: its predecessor is the
		// cycle's last.
		{"a Wednesday March the 1st", scheduleWith(map[string]any{"schedule": "0 0 0 */31 3 WED"}),
			"no time from the end of 2000 through the end of 2005 (UTC)"},
		// 2100 is no leap year, and 2104 is more than five years past 2098.
		{"February the 29th", scheduleWith(map[string]any{"schedule": "0 0 3 29 2 *"}),
			"no time from the end of 2096 through the end of 2101 (UTC)"},
		{"February the 29th, twice", scheduleWith(map[string]any{"schedule": "0 0,30 3 29 2 *"}),
			"no time from the end of 2096 through the end of 2101 (UTC)"},
		// A starred day of the week is ANDed: a Sunday February the 29th.
		{"a Sunday February the 29th", scheduleWith(map[string]any{"schedule": "0 0 0 29 2 */7"}),
			"no time from the end of 2004 through the end of 2009 (UTC)"},
		{"seconds that select no value", scheduleWith(map[string]any{"schedule": ", * * * * *"}),
			`backup.schedules[0].schedule ", * * * * *": the seconds field selects no value, so its time never comes`},
		{"minutes that select no value", scheduleWith(map[string]any{"schedule": "0 , * * * *"}),
			"the minutes field selects no value"},
		{"hours that select no value", scheduleWith(map[string]any{"schedule": "0 0 , * * *"}),
			"the hours field selects no value"},
		{"months that select no value", scheduleWith(map[string]any{"schedule": "0 0 0 * , *"}),
			"the months field selects no value"},
		// An empty day of month ANDed with a starred day of the week.
		{"days of the month that select no value", scheduleWith(map[string]any{"schedule": "0 0 0 , * *"}),
			`backup.schedules[0].schedule "0 0 0 , * *": its time never comes`},

		{"a target of neither", scheduleWith(map[string]any{"target": "standby"}),
			`backup.schedules[0].target "standby": must be primary or prefer-standby`},
		{"an owner reference of neither", scheduleWith(map[string]any{"backupOwnerReference": "owner"}),
			`backup.schedules[0].backupOwnerReference "owner": must be none, self or cluster`},
		{"a schedule's method", scheduleWith(map[string]any{"method": "volumeSnapshot"}),
			`backup.schedules[0]: unrecognized key "method"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := &oam.Component{Name: "db", Type: "postgresql", Properties: tc.props}
			_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// A ScheduledBackup's name is held to 63 characters, the default's included:
// CloudNativePG copies it into a label of each Backup.
func TestPostgresqlBackup_ScheduledBackupNameLength(t *testing.T) {
	component := strings.Repeat("a", 50)
	for _, tc := range []struct {
		schedule string
		refused  bool
	}{
		{strings.Repeat("b", 12), false}, // 63
		{strings.Repeat("b", 13), true},  // 64
	} {
		props := backupWith(nil, map[string]any{"schedules": []any{map[string]any{"name": tc.schedule, "schedule": "@daily"}}})
		props["clusterObjectName"] = "pg"
		comp := &oam.Component{Name: component, Type: "postgresql", Properties: props}
		_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
		name := component + "-" + tc.schedule
		want := `backup.schedules[0] "` + tc.schedule + `": ScheduledBackup name "` + name + `" is 64 characters long, and CloudNativePG copies it into a label of each Backup, which holds at most 63; shorten the component or the schedule name`
		switch {
		case tc.refused && (err == nil || err.Error() != want):
			t.Errorf("a %d-character name: err = %v\nwant %q", len(name), err, want)
		case !tc.refused && err != nil:
			t.Errorf("a %d-character name was refused: %v", len(name), err)
		}
	}
}

// A default past 253 characters is shortened before the 63-character check:
// the refusal still names the component or the schedule name, not the Naming
// hook, as what to shorten.
func TestPostgresqlBackup_ScheduledBackupShortenedDefault(t *testing.T) {
	component, schedule := strings.Repeat("a", 200), strings.Repeat("b", 60)
	props := backupWith(nil, map[string]any{"schedules": []any{map[string]any{"name": schedule, "schedule": "@daily"}}})
	props["clusterObjectName"] = "pg"
	comp := &oam.Component{Name: component, Type: "postgresql", Properties: props}
	_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
	want := "is 253 characters long, and CloudNativePG copies it into a label of each Backup, which holds at most 63; shorten the component or the schedule name"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("err = %v\nwant one ending %q", err, want)
	}
}

// A ScheduledBackup is a member: a schedule whose default name another member
// already takes is refused, naming the schedule.
func TestPostgresqlBackup_ScheduledBackupNameCollision(t *testing.T) {
	props := backupWith(nil, map[string]any{"schedules": []any{map[string]any{"name": "pooler", "schedule": "@daily"}}})
	props["pooler"] = map[string]any{"enabled": true}
	comp := &oam.Component{Name: "db", Type: "postgresql", Properties: props}
	_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
	if err == nil || !strings.Contains(err.Error(), `backup.schedules[0] "pooler"`) || !strings.Contains(err.Error(), "db-pooler") {
		t.Errorf("err = %v, want a refusal of backup.schedules[0] \"pooler\" naming db-pooler", err)
	}
}

// Each field of `backup` lands where the plan places it: the object store's
// configuration and the retention policy on the ObjectStore, its name and the
// server name on the Cluster's plugin entry, each schedule on a ScheduledBackup
// of the Cluster through the plugin.
func TestPostgresqlBackup_Lowering(t *testing.T) {
	props := backupWith(map[string]any{
		"endpointURL": "https://s3.example",
		"endpointCA":  map[string]any{"name": "s3-ca", "key": "ca.crt"},
		"wal":         map[string]any{"compression": "zstd", "encryption": "AES256", "maxParallel": 4},
		"data":        map[string]any{"compression": "lz4", "encryption": "aws:kms", "jobs": 2, "immediateCheckpoint": true},
	}, map[string]any{
		"objectStoreObjectName": "db-store",
		"retentionPolicy":       "4w",
		"serverName":            "db-v2",
		"schedules": []any{
			map[string]any{"name": "daily", "schedule": "0 0 1 * * *"},
			map[string]any{"name": "manual", "schedule": "@monthly", "immediate": false, "suspend": false, "target": "primary", "backupOwnerReference": "none"},
		},
	})
	props["clusterObjectName"] = "pg"
	res := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: props}, nil)

	members := map[string]oam.Component{}
	for _, c := range res.Components {
		members[c.Type+"/"+c.Name] = c
	}
	at := func(member string, path ...string) any {
		t.Helper()
		c, ok := members[member]
		if !ok {
			t.Fatalf("no member %s among %v", member, memberNames(res))
		}
		var node any = c.Properties
		for _, key := range path {
			m, _ := node.(map[string]any)
			node = m[key]
		}
		return node
	}
	for _, tc := range []struct {
		member string
		path   []string
		want   any
	}{
		{"cnpg-objectstore/db", []string{"configuration", "destinationPath"}, "s3://bucket/db/"},
		{"cnpg-objectstore/db", []string{"configuration", "endpointURL"}, "https://s3.example"},
		{"cnpg-objectstore/db", []string{"configuration", "endpointCA"}, map[string]any{"name": "s3-ca", "key": "ca.crt"}},
		{"cnpg-objectstore/db", []string{"configuration", "s3Credentials", "inheritFromIAMRole"}, true},
		{"cnpg-objectstore/db", []string{"configuration", "wal"}, map[string]any{"compression": "zstd", "encryption": "AES256", "maxParallel": int64(4)}},
		{"cnpg-objectstore/db", []string{"configuration", "data"}, map[string]any{"compression": "lz4", "encryption": "aws:kms", "jobs": int64(2), "immediateCheckpoint": true}},
		{"cnpg-objectstore/db", []string{"configuration", "serverName"}, nil},
		{"cnpg-objectstore/db", []string{"retentionPolicy"}, "4w"},
		{"cnpg-cluster/db", []string{"backup"}, nil},
		{"cnpg-cluster/db", []string{"plugins"}, []any{map[string]any{
			"name": "barman-cloud.cloudnative-pg.io", "isWALArchiver": true,
			"parameters": map[string]any{"barmanObjectName": "db-store", "serverName": "db-v2"},
		}}},
		{"cnpg-scheduledbackup/db-daily", nil, map[string]any{
			"schedule": "0 0 1 * * *", "immediate": true, "cluster": map[string]any{"name": "pg"},
			"backupOwnerReference": "self", "method": "plugin",
			"pluginConfiguration": map[string]any{"name": "barman-cloud.cloudnative-pg.io"},
		}},
		{"cnpg-scheduledbackup/db-manual", nil, map[string]any{
			"schedule": "@monthly", "immediate": false, "suspend": false, "cluster": map[string]any{"name": "pg"},
			"backupOwnerReference": "none", "target": "primary", "method": "plugin",
			"pluginConfiguration": map[string]any{"name": "barman-cloud.cloudnative-pg.io"},
		}},
	} {
		if got := at(tc.member, tc.path...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s at %v = %#v, want %#v", tc.member, tc.path, got, tc.want)
		}
	}
	if got := members["cnpg-objectstore/db"].ObjectName(); got != "db-store" {
		t.Errorf("the ObjectStore is named %q, want db-store", got)
	}
}

// TestPostgresqlBackup_SchedulesThatComeAdmitted pins that a schedule whose
// time comes within five years from every instant, or an @every one, lowers,
// carried as authored (go-kure/launcher#1032).
func TestPostgresqlBackup_SchedulesThatComeAdmitted(t *testing.T) {
	for _, schedule := range []string{
		"0 0 0 1 1 *", // once a year, at its first second
		"@yearly",
		"0 0 3 , * MON", // an empty day of month ORed with Mondays: every Monday
		"@every 1h",
		"@every 87600h",    // ten years, but its next time is never zero
		"59 59 23 31 12 *", // once a year, at its last second
		"0 0 3 28 2 *",     // February the 28th
	} {
		t.Run(schedule, func(t *testing.T) {
			res := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: scheduleWith(map[string]any{"schedule": schedule})}, nil)
			for _, c := range res.Components {
				if c.Type == "cnpg-scheduledbackup" {
					if got := c.Properties["schedule"]; got != schedule {
						t.Fatalf("schedule = %v, want %q", got, schedule)
					}
					return
				}
			}
			t.Fatalf("no cnpg-scheduledbackup among %v", memberNames(res))
		})
	}
}

// TestPostgresqlBackup_ScheduleRefusalRepeats pins that the look-ahead's
// refusal reads the same on every build: it is decided over fixed years, not
// from the clock.
func TestPostgresqlBackup_ScheduleRefusalRepeats(t *testing.T) {
	lower := func() string {
		comp := &oam.Component{Name: "db", Type: "postgresql", Properties: scheduleWith(map[string]any{"schedule": "0 0 3 29 2 *"})}
		_, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
		if err == nil {
			t.Fatal("February the 29th lowered")
		}
		return err.Error()
	}
	if first, second := lower(), lower(); first != second {
		t.Errorf("the refusal differs between builds:\n%s\n%s", first, second)
	}
}

// The bounded wal and data values admit their lower bound, and a null one is
// absence, not a value to check: each lowers, and a null key is not carried.
func TestPostgresqlBackup_BoundedValuesAdmitted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wal, data map[string]any
		wantWAL   any
		wantData  any
	}{
		{"the minimum of 1", map[string]any{"maxParallel": 1}, map[string]any{"jobs": 1},
			map[string]any{"maxParallel": int64(1)}, map[string]any{"jobs": int64(1)}},
		{"null values", map[string]any{"compression": nil, "maxParallel": nil}, map[string]any{"encryption": nil, "jobs": nil},
			nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := backupWith(map[string]any{"wal": tc.wal, "data": tc.data}, nil)
			res := lowerPostgresql(t, &oam.Component{Name: "db", Type: "postgresql", Properties: props}, nil)
			var store *oam.Component
			for i, c := range res.Components {
				if c.Type == "cnpg-objectstore" {
					store = &res.Components[i]
				}
			}
			if store == nil {
				t.Fatalf("no cnpg-objectstore among %v", memberNames(res))
			}
			cfg, _ := store.Properties["configuration"].(map[string]any)
			for key, want := range map[string]any{"wal": tc.wantWAL, "data": tc.wantData} {
				got := cfg[key]
				if m, ok := got.(map[string]any); ok && len(m) == 0 {
					got = nil
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("configuration.%s = %#v, want %#v", key, got, want)
				}
			}
		})
	}
}

// The document check names the replacement of a key `backup` replaced, so an
// author reading the schema's refusal learns where it went.
func TestPostgresqlBackup_UnsupportedFieldHints(t *testing.T) {
	rule := components.PostgresqlRule{}
	for _, tc := range []struct {
		parents []string
		key     string
		want    string
	}{
		{nil, "objectStore", "objectStore: moved to backup.objectStore"},
		{nil, "objectStoreObjectName", "objectStoreObjectName: moved to backup.objectStoreObjectName"},
		{nil, "storge", ""},
		{[]string{"backup"}, "destinationPath", "backup.destinationPath: removed; use backup.objectStore.destinationPath"},
		{[]string{"backup"}, "endpointURL", "backup.endpointURL: removed; use backup.objectStore.endpointURL"},
		{[]string{"backup"}, "secretName", "backup.secretName: removed; use backup.objectStore.s3Credentials"},
		{[]string{"backup"}, "tags", ""},
		{[]string{"backup", "objectStore"}, "serverName", "backup.objectStore.serverName: not allowed (the ObjectStore CRD forbids it); use backup.serverName"},
		{[]string{"backup", "objectStore"}, "azureCredentials", "backup.objectStore.azureCredentials: not supported"},
		{[]string{"pooler"}, "destinationPath", ""},
	} {
		var got string
		if tc.parents == nil {
			got = rule.UnsupportedFieldHint(tc.key)
		} else {
			got = rule.NestedUnsupportedFieldHint(tc.parents, tc.key)
		}
		if tc.want == "" && got != "" || !strings.HasPrefix(got, tc.want) {
			t.Errorf("hint for %v %q = %q, want one starting %q", tc.parents, tc.key, got, tc.want)
		}
	}
}
