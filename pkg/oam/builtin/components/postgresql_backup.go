package components

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/robfig/cron"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds postgresql's `backup` (go-kure/launcher#1001): backups and
// WAL archiving to an S3-compatible object store through the Barman Cloud
// plugin, the one path CloudNativePG keeps once its in-tree Barman support is
// removed. `backup` lowers into the base kinds and adds nothing to them:
//
//   - a `cnpg-objectstore` (the ObjectStore the plugin writes to), a member of
//     the Cluster's same-name sibling group;
//   - the Cluster's barman-cloud plugin entry, the WAL archiver;
//   - a `cnpg-scheduledbackup` (method plugin) per schedules entry.
//
// Its blocks are read strictly: a key it does not read is refused, and the
// object store and its credentials decode strictly into the vendored Barman
// types, so a value lands where those types put it or the build fails.

// postgresqlScheduledBackupNameMaxLength is the longest name a ScheduledBackup
// the rule emits may have. CloudNativePG copies the name into a label of every
// Backup it creates from it, and a label value is at most 63 characters.
const postgresqlScheduledBackupNameMaxLength = 63

// postgresqlRetentionPolicy is the ObjectStore CRD's pattern for
// retentionPolicy: a positive count of days, weeks or months.
var postgresqlRetentionPolicy = regexp.MustCompile(`^[1-9][0-9]*[dwm]$`)

// cnpgScheduledBackupGroupKind is the kind the ScheduledBackup names are
// resolved and claimed for.
var cnpgScheduledBackupGroupKind = cnpgKind("ScheduledBackup")

// postgresqlMovedKeys are the top-level properties `backup` replaced. Each is
// refused, whatever its value, with the key that replaces it.
var postgresqlMovedKeys = map[string]string{
	"objectStore":                     "objectStore: moved to backup.objectStore (its retentionPolicy and serverName to backup.retentionPolicy and backup.serverName; its secretName to backup.objectStore.s3Credentials)",
	postgresqlObjectStoreNameProperty: postgresqlObjectStoreNameProperty + ": moved to backup." + postgresqlObjectStoreNameProperty,
}

// postgresqlBackupMovedKeys are the keys of the former `backup`, which built
// the Cluster's in-tree barmanObjectStore, that the object store now carries.
// retentionPolicy stays, and now sets the ObjectStore's.
var postgresqlBackupMovedKeys = map[string]string{
	"destinationPath": "backup.destinationPath: removed; use backup.objectStore.destinationPath",
	"endpointURL":     "backup.endpointURL: removed; use backup.objectStore.endpointURL",
	"secretName":      "backup.secretName: removed; use backup.objectStore.s3Credentials (accessKeyId and secretAccessKey each reference a key of the Secret)",
}

// postgresqlObjectStoreRefusedKeys are the fields of the vendored
// BarmanObjectStoreConfiguration that an object store of the rule
// (backup.objectStore, recovery.source.objectStore) refuses with a reason;
// path is the store's, and its serverName is set at path's parent. Its other
// fields outside postgresqlObjectStoreKeys are refused as any unknown key is.
func postgresqlObjectStoreRefusedKeys(path string) map[string]string {
	parent := strings.TrimSuffix(path, ".objectStore")
	return map[string]string{
		"serverName":        path + ".serverName: not allowed (the ObjectStore CRD forbids it); use " + parent + ".serverName",
		"azureCredentials":  path + ".azureCredentials: not supported (S3-compatible storage only; the cnpg-objectstore kind takes Azure credentials)",
		"googleCredentials": path + ".googleCredentials: not supported (S3-compatible storage only; the cnpg-objectstore kind takes Google credentials)",
	}
}

// The keys each block of `backup` reads.
var (
	postgresqlBackupKeys         = []string{"objectStore", postgresqlObjectStoreNameProperty, "retentionPolicy", "serverName", "schedules"}
	postgresqlObjectStoreKeys    = []string{"destinationPath", "endpointURL", "endpointCA", "s3Credentials", "wal", "data"}
	postgresqlBackupWALKeys      = []string{"compression", "encryption", "maxParallel"}
	postgresqlBackupDataKeys     = []string{"compression", "encryption", "jobs", "immediateCheckpoint"}
	postgresqlBackupScheduleKeys = []string{"name", "schedule", "immediate", "suspend", "target", "backupOwnerReference"}
)

// postgresqlSecretKeyRefsRequired are the fields of the Secret key references
// in backup.objectStore that the API requires and the Barman types write
// whether or not they were authored: a reference without them would be
// emitted with an empty name or key.
var postgresqlSecretKeyRefsRequired = func() map[string]string {
	out := map[string]string{}
	for _, ref := range []string{"endpointCA", "s3Credentials.accessKeyId", "s3Credentials.secretAccessKey", "s3Credentials.region", "s3Credentials.sessionToken"} {
		out[ref+".name"] = "the name of the Secret"
		out[ref+".key"] = "the key of the Secret that holds the value"
	}
	return out
}()

// UnsupportedFieldHint gives the document check's refusal of a top-level key
// the replacement of a key `backup` moved (postgresqlMovedKeys).
func (PostgresqlRule) UnsupportedFieldHint(key string) string {
	return postgresqlMovedKeys[key]
}

// NestedUnsupportedFieldHint gives the document check's refusal of a key of
// `backup`, of backup.objectStore or of recovery.source.objectStore the reason
// the parser refuses it with.
func (PostgresqlRule) NestedUnsupportedFieldHint(parents []string, key string) string {
	switch path := strings.Join(parents, "."); path {
	case "backup":
		return postgresqlBackupMovedKeys[key]
	case "backup.objectStore", postgresqlRecoveryObjectStorePath:
		return postgresqlObjectStoreRefusedKeys(path)[key]
	}
	return ""
}

// postgresqlSixFieldForms names the six-field schedules a five-field one
// may have meant, each only if it parses: read as a crontab line, a second
// 0 put first; read seconds first as the operator reads it, any day of the
// week put last. The second is the schedule already parsed, so it always
// parses.
func postgresqlSixFieldForms(fields []string) string {
	var forms []string
	for _, f := range []struct{ form, reading string }{
		{"0 " + strings.Join(fields, " "), "as a crontab line"},
		{strings.Join(fields, " ") + " *", "as the operator reads it"},
	} {
		if _, err := cron.Parse(f.form); err == nil {
			forms = append(forms, fmt.Sprintf("%q %s", f.form, f.reading))
		}
	}
	return " (" + strings.Join(forms, ", or ") + ")"
}

// postgresqlScheduleCycleFirst and postgresqlScheduleCycleLast bound the fixed
// Gregorian cycle the look-ahead is decided over: 400 years are 146097 days, 20871 weeks, so months, days and
// weekdays repeat and one cycle decides every year. Fixed years keep the
// build free of the clock.
const postgresqlScheduleCycleFirst, postgresqlScheduleCycleLast = 2000, 2399

// postgresqlScheduleStop says why the operator would stop on a schedule that
// parses, or "" when it would not. The operator (CloudNativePG's
// ScheduledBackup controller) asks robfig/cron for the next time after now;
// a zero time records the NoSchedule event and returns without a requeue,
// before the immediate backup and the next one are considered. robfig/cron
// searches from the next whole second through the end of the year five
// years on, and returns zero past it.
//
// An `@every` schedule's next time is the current one plus its delay, never
// zero, so it is admitted, as is any other kind nothing is proven of. A
// seconds, minutes, hours or months field that selects no value (`,`) never
// matches, so it is refused without a search. An empty day of month or day of
// week can still fire (ORed with an unstarred other day field), so it goes to
// the search.
//
// The search is exact, in UTC: for each year of the cycle it asks for the
// next time after its December 31st 23:59:58. Any instant whose next second
// falls in that year searches at least as far, so a time found there is one
// every instant of the year finds. A year with none starts a gap the operator
// stops in; the first gap start of the cycle is named (a year whose
// predecessor finds a time, the cycle's last year preceding its first), so
// the message names no clock year.
func postgresqlScheduleStop(sched cron.Schedule) string {
	spec, ok := sched.(*cron.SpecSchedule)
	if !ok {
		return ""
	}
	for _, f := range []struct {
		name string
		bits uint64
	}{{"seconds", spec.Second}, {"minutes", spec.Minute}, {"hours", spec.Hour}, {"months", spec.Month}} {
		if f.bits == 0 {
			return "the " + f.name + " field selects no value, so its time never comes and the operator records NoSchedule and takes no backup for it"
		}
	}
	var none [postgresqlScheduleCycleLast - postgresqlScheduleCycleFirst + 1]bool
	for i := range none {
		year := postgresqlScheduleCycleFirst + i
		none[i] = spec.Next(time.Date(year, time.December, 31, 23, 59, 58, 0, time.UTC)).IsZero()
	}
	for i := range none {
		if none[i] && !none[(i+len(none)-1)%len(none)] {
			year := postgresqlScheduleCycleFirst + i
			return fmt.Sprintf("no time from the end of %d through the end of %d (UTC), longer than the five years the operator looks ahead, so the operator records NoSchedule there and takes no further backup for it; choose a schedule whose time comes at least every five years", year, year+5)
		}
	}
	if none[0] {
		return "its time never comes, so the operator records NoSchedule for it and takes no backup, not even the immediate one"
	}
	return ""
}

// schemaPostgresqlBackup declares `backup`. The blocks launcher defines are
// declared key by key; the vendored values inside the object store (the
// credentials and the CA reference) are open objects the parser decodes
// strictly into the Barman types.
func schemaPostgresqlBackup() oam.PropertySchema {
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	const secretKeyRef = "a reference to one key of a Secret in the namespace, {name, key}"
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Backups and WAL archiving to an S3-compatible object store through the Barman Cloud plugin. Set, the component also emits a cnpg-objectstore and the Cluster's barman-cloud plugin entry archives WAL to it; each schedules entry emits a cnpg-scheduledbackup. Unset, nothing is backed up. Needs the CloudNativePG operator 1.26 or later and the Barman Cloud plugin with its CRD installed.",
		Properties: map[string]oam.PropertySchema{
			// objectStore, and its destinationPath and s3Credentials, are not
			// Required here though the parser requires them: the document check
			// reports a missing Required key before an undeclared one, and a
			// `backup` of before (destinationPath, secretName, no objectStore),
			// or an object store of Azure or Google credentials, must be refused
			// with the hint that names the key's replacement or reason.
			"objectStore": {
				Type:        oam.PropertyTypeObject,
				Description: "Required. Where backups and WAL are written: the configuration of the ObjectStore the component emits.",
				Properties: map[string]oam.PropertySchema{
					"destinationPath": {Type: oam.PropertyTypeString, Description: "Required, not empty. The object store path backups and WAL are written to (for example s3://bucket/path)."},
					"endpointURL":     str("The endpoint of an S3-compatible service, in place of the AWS endpoint discovery."),
					"endpointCA":      {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "The CA bundle the endpoint's TLS certificate is checked against: " + secretKeyRef + "."},
					"s3Credentials": {
						Type: oam.PropertyTypeObject, AdditionalProperties: true,
						Description: "Required. Exactly one way to authenticate: accessKeyId and secretAccessKey, each " + secretKeyRef + " (region and sessionToken, references alike, may be set beside them), or inheritFromIAMRole: true. Decoded strictly into the Barman Cloud S3Credentials type.",
					},
					"wal": {
						Type:        oam.PropertyTypeObject,
						Description: "How WAL files are archived.",
						Properties: map[string]oam.PropertySchema{
							"compression": str("Compression of each WAL file: bzip2, gzip, lz4, snappy, xz or zstd. Unset, none."),
							"encryption":  str("Server-side encryption: AES256 or aws:kms. Unset, the bucket's policy."),
							"maxParallel": {Type: oam.PropertyTypeInteger, Description: "How many WAL files are archived or restored at once, at least 1."},
						},
					},
					"data": {
						Type:        oam.PropertyTypeObject,
						Description: "How base backups are written.",
						Properties: map[string]oam.PropertySchema{
							"compression":         str("Compression of the base backup: bzip2, gzip, lz4 or snappy. Unset, none."),
							"encryption":          str("Server-side encryption: AES256 or aws:kms. Unset, the bucket's policy."),
							"jobs":                {Type: oam.PropertyTypeInteger, Description: "How many files are uploaded at once, at least 1."},
							"immediateCheckpoint": {Type: oam.PropertyTypeBoolean, Description: "true, a backup starts with an immediate checkpoint instead of a spread one."},
						},
					},
				},
			},
			postgresqlObjectStoreNameProperty: str("Name of the generated ObjectStore, in place of the component name, used as written. The Cluster's plugin entry names the store by it."),
			"retentionPolicy":                 str("How long backups are kept, as <n><d|w|m> (for example 30d). Set on the ObjectStore."),
			"serverName":                      str("The folder of this Cluster's backups and WAL within destinationPath. Unset, the Cluster name."),
			"schedules": {
				Type:        oam.PropertyTypeArray,
				Description: "Base backups on a schedule. Without one only WAL is archived, and a point-in-time recovery then needs a base backup taken otherwise (a cnpg-backup).",
				Items: &oam.PropertySchema{
					Type:        oam.PropertyTypeObject,
					Description: "One schedule: a cnpg-scheduledbackup named <component>-<name>, of method plugin.",
					Properties: map[string]oam.PropertySchema{
						"name":                 {Type: oam.PropertyTypeString, Required: true, Description: "Required, unique among the schedules. The ScheduledBackup is named <component>-<name>, at most 63 characters."},
						"schedule":             {Type: oam.PropertyTypeString, Required: true, Description: "Required. When a backup is taken, as a cron expression of six fields, the first of which is the seconds (\"0 0 3 * * *\" is every day at 03:00). It must parse as the operator parses it, and its time must come within the operator's five-year look-ahead from every instant (computed in UTC), since the operator otherwise records NoSchedule and takes no further backup: February the 30th, a field of the seconds, minutes, hours or months that selects no value, and February the 29th (no time from the end of 2096 through 2101) are refused. An @every schedule is accepted."},
						"immediate":            {Type: oam.PropertyTypeBoolean, Description: "Whether a first backup is taken as soon as the ScheduledBackup is created. Default true."},
						"suspend":              {Type: oam.PropertyTypeBoolean, Description: "true, no backup is scheduled until it is set back."},
						"target":               {Type: oam.PropertyTypeString, Enum: []any{"primary", "prefer-standby"}, Description: "Which instance takes the backup: primary or prefer-standby. Unset, the operator's default."},
						"backupOwnerReference": {Type: oam.PropertyTypeString, Enum: []any{"none", "self", "cluster"}, Description: "The owner of each Backup the schedule creates: none, self (the ScheduledBackup, so its Backups go with it) or cluster. Default self."},
					},
				},
			},
		},
	}
}

// PostgresqlBackup is what PostgresqlRule.Parse reads from `backup`.
type PostgresqlBackup struct {
	// ObjectStore is backup.objectStore, decoded into the vendored type.
	ObjectStore barmanapi.BarmanObjectStoreConfiguration
	// ObjectName is the name LowerComponent resolved for the ObjectStore (role
	// oam.NameRolePostgresqlObjectStore), which the Cluster's plugin entry
	// names. Empty for the component's name: Parse resolves none.
	ObjectName string
	// RetentionPolicy is the ObjectStore's retentionPolicy; empty for none.
	RetentionPolicy string
	// ServerName is the plugin's serverName parameter; empty for the
	// plugin's default, the Cluster name.
	ServerName string
	Schedules  []PostgresqlBackupSchedule
}

// PostgresqlBackupSchedule is one backup.schedules entry, with its defaults
// filled in.
type PostgresqlBackupSchedule struct {
	Name     string
	Schedule string
	// Immediate defaults to true: a new Cluster has a base backup at once.
	Immediate bool
	// Suspend is nil when it was not authored.
	Suspend *bool
	// Target is empty for the operator's default.
	Target string
	// BackupOwnerReference defaults to "self": a Backup goes with the
	// schedule that created it.
	BackupOwnerReference string
}

// parsePostgresqlBackup reads `backup` from a postgresql component's
// properties, and refuses the top-level keys it replaced. It returns nil when
// `backup` is not set.
func parsePostgresqlBackup(props map[string]any) (*PostgresqlBackup, error) {
	if err := refusedProperty(props, postgresqlMovedKeys); err != nil {
		return nil, err
	}
	raw, present, err := parseObjectField(props, "backup", "backup")
	if err != nil || !present {
		return nil, err
	}
	if err := refusedProperty(raw, postgresqlBackupMovedKeys); err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(raw, postgresqlBackupKeys, "backup"); err != nil {
		return nil, err
	}
	backup := &PostgresqlBackup{}
	store, err := parsePostgresqlObjectStore(raw, "backup.objectStore", "the object store backups and WAL are written to")
	if err != nil {
		return nil, err
	}
	backup.ObjectStore = *store
	// The name itself is read and resolved by LowerComponent; here only its
	// type.
	if _, _, err := parseRawStringField(raw, postgresqlObjectStoreNameProperty, "backup."+postgresqlObjectStoreNameProperty); err != nil {
		return nil, err
	}
	if policy, present, err := parseRawStringField(raw, "retentionPolicy", "backup.retentionPolicy"); err != nil {
		return nil, err
	} else if present {
		if !postgresqlRetentionPolicy.MatchString(policy) {
			return nil, errors.Errorf("backup.retentionPolicy %q: must be a positive count of days, weeks or months, as <n><d|w|m> (for example 30d)", policy)
		}
		backup.RetentionPolicy = policy
	}
	if backup.ServerName, _, err = parseRawStringField(raw, "serverName", "backup.serverName"); err != nil {
		return nil, err
	}
	if backup.Schedules, err = parsePostgresqlBackupSchedules(raw); err != nil {
		return nil, err
	}
	return backup, nil
}

// parsePostgresqlObjectStore reads the objectStore of parent, at path, strictly
// into the vendored BarmanObjectStoreConfiguration, and refuses what the
// ObjectStore or the plugin would refuse at apply for a reason the decode
// cannot see: no destination path, and S3 credentials other than exactly one
// way to authenticate, held to the Barman types' own check. role says what
// the store is for; the caller has made sure it is present when it may be
// absent.
func parsePostgresqlObjectStore(parent map[string]any, path, role string) (*barmanapi.BarmanObjectStoreConfiguration, error) {
	raw, present, err := parseObjectField(parent, "objectStore", path)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.Errorf("%s: required (%s)", path, role)
	}
	if err := refusedProperty(raw, postgresqlObjectStoreRefusedKeys(path)); err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(raw, postgresqlObjectStoreKeys, path); err != nil {
		return nil, err
	}
	for _, block := range []struct {
		key  string
		keys []string
	}{{"wal", postgresqlBackupWALKeys}, {"data", postgresqlBackupDataKeys}} {
		m, present, err := parseObjectField(raw, block.key, path+"."+block.key)
		if err != nil {
			return nil, err
		}
		if present {
			if err := rejectUnknownKeys(m, block.keys, path+"."+block.key); err != nil {
				return nil, err
			}
		}
	}
	store, authored, err := decodeKindSpec[barmanapi.BarmanObjectStoreConfiguration](raw, "Barman Cloud BarmanObjectStoreConfiguration")
	if err != nil {
		return nil, errors.Wrap(err, path)
	}
	if err := refuseUncarriedSpecValues(authored, store, cnpgDefaultedZeros(nil)); err != nil {
		return nil, errors.Wrap(err, path)
	}
	if err := refuseUnauthoredRequired(authored, postgresqlSecretKeyRefsRequired); err != nil {
		return nil, errors.Wrap(err, path)
	}
	pathRole := strings.Replace(role, "object store", "object store path", 1)
	if _, present := authored["destinationPath"]; !present {
		return nil, errors.Errorf("%s.destinationPath: required (%s)", path, pathRole)
	}
	if store.DestinationPath == "" {
		return nil, errors.Errorf("%s.destinationPath: must not be empty (%s)", path, pathRole)
	}
	if store.AWS == nil {
		return nil, errors.New(path + ".s3Credentials: required (accessKeyId and secretAccessKey, or inheritFromIAMRole: true)")
	}
	steps := strings.Split(path, ".")
	if errs := store.AWS.ValidateAwsCredentials(field.NewPath(steps[0], steps[1:]...).Child("s3Credentials")); len(errs) > 0 {
		return nil, errors.Errorf("%s: %s", errs[0].Field, errs[0].Detail)
	}
	if err := checkPostgresqlObjectStoreValues(raw, store, path); err != nil {
		return nil, err
	}
	return store, nil
}

// The values the ObjectStore CRD admits for the wal and data blocks' fields
// (the kubebuilder markers of the vendored Barman Cloud types).
var (
	postgresqlWALCompressions  = []string{"bzip2", "gzip", "lz4", "snappy", "xz", "zstd"}
	postgresqlDataCompressions = []string{"bzip2", "gzip", "lz4", "snappy"}
	postgresqlEncryptions      = []string{"AES256", "aws:kms"}
)

// checkPostgresqlObjectStoreValues refuses an authored wal or data value the
// ObjectStore CRD refuses: the strict decode holds a value to its Go type, a
// plain string or int, and not to the CRD's enum or minimum. An authored
// value the type omits when zero (`maxParallel: 0`, `compression: ""`) is
// refused the same way, rather than dropped from the output.
func checkPostgresqlObjectStoreValues(raw map[string]any, store *barmanapi.BarmanObjectStoreConfiguration, path string) error {
	authored := func(block, key string) bool {
		m, _ := raw[block].(map[string]any)
		_, present := authoredValue(m, key)
		return present
	}
	oneOf := func(key, v string, allowed []string) error {
		if slices.Contains(allowed, v) {
			return nil
		}
		return errors.Errorf("%s.%s %q: must be one of %s", path, key, v, strings.Join(allowed, ", "))
	}
	atLeastOne := func(key string, v int) error {
		if v >= 1 {
			return nil
		}
		return errors.Errorf("%s.%s %d: must be at least 1", path, key, v)
	}
	var wal barmanapi.WalBackupConfiguration
	if store.Wal != nil {
		wal = *store.Wal
	}
	var data barmanapi.DataBackupConfiguration
	if store.Data != nil {
		data = *store.Data
	}
	jobs := 0
	if data.Jobs != nil {
		jobs = int(*data.Jobs)
	}
	for _, check := range []struct {
		block, key string
		err        func() error
	}{
		{"wal", "compression", func() error { return oneOf("wal.compression", string(wal.Compression), postgresqlWALCompressions) }},
		{"wal", "encryption", func() error { return oneOf("wal.encryption", string(wal.Encryption), postgresqlEncryptions) }},
		{"wal", "maxParallel", func() error { return atLeastOne("wal.maxParallel", wal.MaxParallel) }},
		{"data", "compression", func() error { return oneOf("data.compression", string(data.Compression), postgresqlDataCompressions) }},
		{"data", "encryption", func() error { return oneOf("data.encryption", string(data.Encryption), postgresqlEncryptions) }},
		{"data", "jobs", func() error { return atLeastOne("data.jobs", jobs) }},
	} {
		if !authored(check.block, check.key) {
			continue
		}
		if err := check.err(); err != nil {
			return err
		}
	}
	return nil
}

// parsePostgresqlBackupSchedules reads backup.schedules, filling in each
// entry's defaults.
func parsePostgresqlBackupSchedules(backup map[string]any) ([]PostgresqlBackupSchedule, error) {
	list, _, err := parseObjectListField(backup, "schedules", "backup.schedules")
	if err != nil {
		return nil, err
	}
	var out []PostgresqlBackupSchedule
	first := map[string]int{}
	for i, raw := range list {
		label := fmt.Sprintf("backup.schedules[%d]", i)
		if err := rejectUnknownKeys(raw, postgresqlBackupScheduleKeys, label); err != nil {
			return nil, err
		}
		s := PostgresqlBackupSchedule{Immediate: true, BackupOwnerReference: "self"}
		name, present, err := parseStringField(raw, "name", label+".name")
		if err != nil {
			return nil, err
		}
		if !present || name == "" {
			return nil, errors.Errorf("%s.name: required (the ScheduledBackup is named <component>-<name>)", label)
		}
		if j, dup := first[name]; dup {
			return nil, errors.Errorf("%s.name %q: already the name of backup.schedules[%d]; each schedule needs a name of its own", label, name, j)
		}
		first[name] = i
		s.Name = name
		schedule, present, err := parseStringField(raw, "schedule", label+".schedule")
		if err != nil {
			return nil, err
		}
		if !present || schedule == "" {
			return nil, errors.Errorf("%s.schedule: required (when a backup is taken, as a cron expression of six fields)", label)
		}
		// The parser CloudNativePG's ScheduledBackup webhook validates the
		// schedule with: robfig/cron at the version the vendored CloudNativePG
		// module requires, so a schedule refused here is one the webhook
		// refuses. Its pin in go.mod follows that module's when the vendored
		// CloudNativePG moves.
		parsed, err := cron.Parse(schedule)
		if err != nil {
			return nil, errors.Errorf("%s.schedule %q: not a cron expression the operator accepts: %s", label, schedule, err.Error())
		}
		// Five fields parse, seconds first (the day of the week is the one
		// optional field), and are only a warning at the webhook, which Flux
		// does not show: a crontab line `0 3 * * *` would back up at minute 3
		// of every hour, not at 03:00. It is refused, naming each six-field
		// form that parses: the crontab reading (a second 0 put first) and
		// the seconds-first one (any day of the week put last). A descriptor
		// (`@daily`) is one field and means what it says.
		if fields := strings.Fields(schedule); len(fields) == 5 {
			return nil, errors.Errorf("%s.schedule %q: five fields, which the operator reads seconds first (seconds, minutes, hours, day of month, month), not as a crontab line; write six fields, seconds first%s",
				label, schedule, postgresqlSixFieldForms(fields))
		}
		// A schedule the operator finds no time for stops its ScheduledBackup
		// for good (go-kure/launcher#1032): see postgresqlScheduleStop.
		if stop := postgresqlScheduleStop(parsed); stop != "" {
			return nil, errors.Errorf("%s.schedule %q: %s", label, schedule, stop)
		}
		s.Schedule = schedule
		if v, err := parseBoolField(raw, "immediate", label+".immediate"); err != nil {
			return nil, err
		} else if v != nil {
			s.Immediate = *v
		}
		if s.Suspend, err = parseBoolField(raw, "suspend", label+".suspend"); err != nil {
			return nil, err
		}
		if v, present, err := parseRawStringField(raw, "target", label+".target"); err != nil {
			return nil, err
		} else if present {
			if v != string(cnpgv1.BackupTargetPrimary) && v != string(cnpgv1.BackupTargetStandby) {
				return nil, errors.Errorf("%s.target %q: must be primary or prefer-standby", label, v)
			}
			s.Target = v
		}
		if v, present, err := parseRawStringField(raw, "backupOwnerReference", label+".backupOwnerReference"); err != nil {
			return nil, err
		} else if present {
			if !slices.Contains([]string{"none", "self", "cluster"}, v) {
				return nil, errors.Errorf("%s.backupOwnerReference %q: must be none, self or cluster", label, v)
			}
			s.BackupOwnerReference = v
		}
		out = append(out, s)
	}
	return out, nil
}

// objectStoreSpec is the spec of the ObjectStore the rule emits for backup.
func (b *PostgresqlBackup) objectStoreSpec() barmanv1.ObjectStoreSpec {
	return barmanv1.ObjectStoreSpec{
		Configuration:   *b.ObjectStore.DeepCopy(),
		RetentionPolicy: b.RetentionPolicy,
	}
}

// pluginEntry is the Cluster's barman-cloud plugin entry, which archives WAL
// to the ObjectStore the rule emits, by the name resolved for it (the
// component's name unless LowerComponent resolved another). The plugin reads the
// store from barmanObjectName and the folder from serverName, defaulting to
// the Cluster name.
func (b *PostgresqlBackup) pluginEntry(component string) cnpgv1.PluginConfiguration {
	isWALArchiver := true
	storeName := b.ObjectName
	if storeName == "" {
		storeName = component
	}
	params := map[string]string{"barmanObjectName": storeName}
	if b.ServerName != "" {
		params["serverName"] = b.ServerName
	}
	return cnpgv1.PluginConfiguration{
		Name:          barmanCloudPluginName,
		IsWALArchiver: &isWALArchiver,
		Parameters:    params,
	}
}

// scheduledBackupSpec is the spec of the ScheduledBackup the rule emits for s,
// taking its backups of the Cluster named cluster through the plugin. The
// plugin takes no parameter here: it reads the store from the Cluster's entry.
func (s PostgresqlBackupSchedule) scheduledBackupSpec(cluster string) cnpgv1.ScheduledBackupSpec {
	immediate := s.Immediate
	spec := cnpgv1.ScheduledBackupSpec{
		Schedule:             s.Schedule,
		Immediate:            &immediate,
		Cluster:              cnpgv1.LocalObjectReference{Name: cluster},
		BackupOwnerReference: s.BackupOwnerReference,
		Target:               cnpgv1.BackupTarget(s.Target),
		Method:               cnpgv1.BackupMethodPlugin,
		PluginConfiguration:  &cnpgv1.BackupPluginConfiguration{Name: barmanCloudPluginName},
	}
	if s.Suspend != nil {
		suspend := *s.Suspend
		spec.Suspend = &suspend
	}
	return spec
}

// postgresqlObjectStoreMember returns the ObjectStore member the rule emits
// for backup: a member of the Cluster's same-name sibling group, its object
// named like the component unless backup.objectStoreObjectName or the naming
// hook says otherwise. It sets the resolved name on backup, which the
// Cluster's plugin entry names the store by.
func postgresqlObjectStoreMember(comp *oam.Component, lctx oam.LoweringContext, backup *PostgresqlBackup) (oam.Component, error) {
	store := oam.Component{Name: comp.Name, Type: "cnpg-objectstore", Annotations: maps.Clone(comp.Annotations)}
	raw, _, err := parseObjectField(comp.Properties, "backup", "backup")
	if err != nil {
		return oam.Component{}, err
	}
	if err := nameRoleMemberAt(comp, lctx, &store, &CnpgObjectStoreHandler{}, oam.NameRolePostgresqlObjectStore, raw, postgresqlObjectStoreNameProperty, "backup."+postgresqlObjectStoreNameProperty, "ObjectStore"); err != nil {
		return oam.Component{}, err
	}
	backup.ObjectName = store.ObjectName()
	if store.Properties, err = specProperties(backup.objectStoreSpec()); err != nil {
		return oam.Component{}, err
	}
	return store, nil
}

// postgresqlScheduledBackupName resolves the name of the ScheduledBackup the
// rule emits for backup.schedules[i] (role
// oam.NameRolePostgresqlScheduledBackup): the naming hook's, else
// `<component>-<name>`, as a database's. Whichever it is, it is held to 63
// characters: CloudNativePG copies it into a label of each Backup.
func postgresqlScheduledBackupName(comp *oam.Component, lctx oam.LoweringContext, i int, s PostgresqlBackupSchedule) (string, error) {
	what := fmt.Sprintf("backup.schedules[%d] %q", i, s.Name)
	lctx.Component = comp
	name, err := lctx.ResolveName(comp.Name, s.Name, oam.NameSpec{Role: oam.NameRolePostgresqlScheduledBackup, Kind: cnpgScheduledBackupGroupKind})
	if err != nil {
		return "", errors.Wrapf(err, "%s", what)
	}
	if len(name) > postgresqlScheduledBackupNameMaxLength {
		// The rule is handed the resolved name only: one that is not the
		// default (built as ResolveName builds it, shortened past 253
		// characters) is the Naming hook's, which neither the component nor
		// the schedule name shortens.
		fix := "shorten the component or the schedule name"
		if name != oam.ShortenNameWithSuffix(comp.Name, "-"+s.Name, oam.ShortenLimitSubdomain) {
			fix = `shorten the Naming hook's answer for role "` + string(oam.NameRolePostgresqlScheduledBackup) + `"`
		}
		return "", errors.Errorf("%s: ScheduledBackup name %q is %d characters long, and CloudNativePG copies it into a label of each Backup, which holds at most %d; %s",
			what, name, len(name), postgresqlScheduledBackupNameMaxLength, fix)
	}
	return name, nil
}
