package components

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/machinery/pkg/types"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds postgresql's `recovery` (go-kure/launcher#1001): a new
// Cluster bootstrapped from the base backups and WAL another Cluster wrote to
// an object store through the Barman Cloud plugin, to the end of the WAL or to
// a recovery target (point-in-time recovery). `recovery` lowers into the base
// kinds and adds nothing to them:
//
//   - the Cluster's bootstrap.recovery, whose source is one externalClusters
//     entry the rule writes (postgresqlRecoverySourceName) naming the
//     barman-cloud plugin, the store and the source's folder;
//   - for an inline recovery.source.objectStore, a `cnpg-objectstore` (role
//     oam.NameRolePostgresqlRecoveryObjectStore) the Cluster reads from.
//
// A bootstrap is read only when the Cluster is created: `recovery` on a
// component whose Cluster exists already does nothing. Its blocks are read
// strictly, as backup's are.

// postgresqlRecoveryObjectStorePath is the property path of the inline
// recovery store.
const postgresqlRecoveryObjectStorePath = "recovery.source.objectStore"

// postgresqlRecoverySourceSuffix is the suffix of the externalClusters entry
// the rule writes for `recovery` and of the inline recovery store's default
// name, both "<component>-recovery-source".
const postgresqlRecoverySourceSuffix = "recovery-source"

// The keys each block of `recovery` reads.
var (
	postgresqlRecoveryKeys       = []string{"source", "target", "database", "owner", "secret"}
	postgresqlRecoverySourceKeys = []string{"serverName", "objectStoreName", "objectStore"}
	postgresqlRecoveryTargetKeys = []string{"time", "lsn", "xid", "name", "immediate", "backupID", "timeline", "exclusive"}
)

// postgresqlRecoverySelectors are the recovery target's keys that each say
// where the recovery stops, at most one of which may be set (CloudNativePG's
// webhook: all targets but the timeline are exclusive), in the order a refusal
// names them.
var postgresqlRecoverySelectors = []string{"time", "lsn", "xid", "name", "immediate"}

// postgresqlRecoveryRefusedKeys are the former bootstrap keys `recovery`
// replaced, refused with their replacement.
var postgresqlRecoveryRefusedKeys = map[string]string{
	"recovery": "bootstrap.recovery: moved to recovery (recovery.source names the object store and the folder the cluster is recovered from)",
}

// postgresqlRecoverySourceName is the name of the externalClusters entry the
// rule writes for `recovery`, the one bootstrap.recovery.source names.
func postgresqlRecoverySourceName(component string) string {
	return component + "-" + postgresqlRecoverySourceSuffix
}

// schemaPostgresqlRecovery declares `recovery`, key by key; the inline object
// store is declared as backup.objectStore is.
func schemaPostgresqlRecovery() oam.PropertySchema {
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	store := schemaPostgresqlBackup().Properties["objectStore"]
	store.Description = "The object store the cluster is recovered from, defined here: the component also emits a cnpg-objectstore of this configuration, named <component>-recovery-source. Exactly one of objectStoreName and objectStore."
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Bootstrap the Cluster from the base backups and WAL another cluster wrote to an object store through the Barman Cloud plugin, to the end of its WAL or to a target (point-in-time recovery). A bootstrap is read only when the Cluster is created: recover into a new component beside the source one; set on a component whose Cluster exists already, it changes nothing. Not with bootstrap.pg_basebackup.",
		Properties: map[string]oam.PropertySchema{
			"source": {
				Type:        oam.PropertyTypeObject,
				Description: "Required. Where the backups and WAL are read from.",
				Properties: map[string]oam.PropertySchema{
					"serverName":      {Type: oam.PropertyTypeString, Required: true, Description: "Required, not empty. The folder the source cluster wrote within the store's destinationPath: its backup.serverName, else its Cluster's name."},
					"objectStoreName": str("The name of an existing ObjectStore in the namespace to read from. Exactly one of objectStoreName and objectStore."),
					"objectStore":     store,
				},
			},
			"target": {
				Type:        oam.PropertyTypeObject,
				Description: "Where the recovery stops. Unset, at the end of the WAL. At most one of time, lsn, xid, name and immediate.",
				Properties: map[string]oam.PropertySchema{
					"time":      str("Stop at this time, in RFC 3339 or a PostgreSQL timestamp; one without a time zone is UTC. The base backup is the latest one before it."),
					"lsn":       str("Stop at this WAL location (for example 0/3000060)."),
					"xid":       str("Stop at this transaction ID, a 32-bit integer written as a string. Needs backupID."),
					"name":      str("Stop at this restore point, created with pg_create_restore_point; shorter than 64 bytes. Needs backupID."),
					"immediate": {Type: oam.PropertyTypeBoolean, Description: "Stop as soon as the base backup is consistent. Set at all, false included, it is the target, and needs backupID."},
					"backupID":  str("The ID of the base backup to start from. Required with xid, name or immediate; otherwise chosen by time or lsn, else the latest."),
					"timeline":  str("The timeline to follow: latest, or a positive integer written as a string. Unset, the operator's default."),
					"exclusive": {Type: oam.PropertyTypeBoolean, Description: "true, the recovery stops just before the target instead of just after it."},
				},
			},
			"database": str("The application database. Unset, the operator's default, app."),
			"owner":    str("The owner of the application database. Unset, the database's name."),
			"secret": {
				Type:        oam.PropertyTypeObject,
				Description: "The Secret holding the owner's credentials. Unset, the operator generates one.",
				Properties: map[string]oam.PropertySchema{
					"name": {Type: oam.PropertyTypeString, Required: true, Description: "Required, not empty. The name of the Secret in the namespace."},
				},
			},
		},
	}
}

// PostgresqlRecovery is what PostgresqlRule.Parse reads from `recovery`.
type PostgresqlRecovery struct {
	// ServerName is the source's folder within the store. It is always
	// written: the plugin would otherwise read the new Cluster's own folder.
	ServerName string
	// ObjectStoreName names an existing ObjectStore; empty for an inline one.
	ObjectStoreName string
	// ObjectStore is the inline recovery.source.objectStore; nil for a
	// reference.
	ObjectStore *barmanapi.BarmanObjectStoreConfiguration
	// ObjectName is the name LowerComponent resolved for the inline store
	// (role oam.NameRolePostgresqlRecoveryObjectStore); Parse resolves none.
	ObjectName string
	// Target is nil for a recovery to the end of the WAL.
	Target *cnpgv1.RecoveryTarget
	// Database, Owner and Secret are empty when not authored.
	Database string
	Owner    string
	Secret   string
}

// parsePostgresqlRecovery reads `recovery` from a postgresql component's
// properties. It returns nil when `recovery` is not set.
func parsePostgresqlRecovery(props map[string]any) (*PostgresqlRecovery, error) {
	raw, present, err := parseObjectField(props, "recovery", "recovery")
	if err != nil || !present {
		return nil, err
	}
	if err := rejectUnknownKeys(raw, postgresqlRecoveryKeys, "recovery"); err != nil {
		return nil, err
	}
	recovery := &PostgresqlRecovery{}
	if err := recovery.parseSource(raw); err != nil {
		return nil, err
	}
	if recovery.Target, err = parsePostgresqlRecoveryTarget(raw); err != nil {
		return nil, err
	}
	// Each is omitted from the Cluster when empty, and the operator's default
	// then applies: an authored "" is refused rather than dropped.
	for _, f := range []struct {
		key string
		dst *string
	}{{"database", &recovery.Database}, {"owner", &recovery.Owner}} {
		if *f.dst, err = postgresqlNonEmptyString(raw, f.key, "recovery."+f.key, "omit it for the operator's default"); err != nil {
			return nil, err
		}
	}
	if secret, present, err := parseObjectField(raw, "secret", "recovery.secret"); err != nil {
		return nil, err
	} else if present {
		if err := rejectUnknownKeys(secret, []string{"name"}, "recovery.secret"); err != nil {
			return nil, err
		}
		if recovery.Secret, err = postgresqlNonEmptyString(secret, "name", "recovery.secret.name", "the Secret holding the owner's credentials"); err != nil {
			return nil, err
		}
		if recovery.Secret == "" {
			return nil, errors.New("recovery.secret.name: required (the Secret holding the owner's credentials)")
		}
	}
	return recovery, nil
}

// postgresqlNonEmptyString reads the string at key, refusing an authored ""
// with why; it returns "" when the key is not authored.
func postgresqlNonEmptyString(raw map[string]any, key, label, why string) (string, error) {
	v, present, err := parseRawStringField(raw, key, label)
	if err != nil {
		return "", err
	}
	if present && v == "" {
		return "", errors.Errorf("%s: must not be empty (%s)", label, why)
	}
	return v, nil
}

// parseSource reads recovery.source: the source's folder and exactly one of a
// reference to an existing store and an inline one.
func (r *PostgresqlRecovery) parseSource(recovery map[string]any) error {
	const path = "recovery.source"
	src, present, err := parseObjectField(recovery, "source", path)
	if err != nil {
		return err
	}
	if !present {
		return errors.New(path + ": required (the object store and the folder the cluster is recovered from)")
	}
	if err := rejectUnknownKeys(src, postgresqlRecoverySourceKeys, path); err != nil {
		return err
	}
	const folder = "the folder the source cluster wrote within the store: its backup.serverName, else its Cluster's name"
	if r.ServerName, err = postgresqlNonEmptyString(src, "serverName", path+".serverName", folder); err != nil {
		return err
	}
	if r.ServerName == "" {
		return errors.New(path + ".serverName: required (" + folder + ")")
	}
	_, byName := authoredValue(src, "objectStoreName")
	_, inline := authoredValue(src, "objectStore")
	switch {
	case byName && inline:
		return errors.New(path + ": objectStoreName and objectStore are mutually exclusive; name an existing ObjectStore, or define one here")
	case !byName && !inline:
		return errors.New(path + ": one of objectStoreName (an existing ObjectStore in the namespace) and objectStore (one defined here) is required")
	case byName:
		r.ObjectStoreName, err = postgresqlNonEmptyString(src, "objectStoreName", path+".objectStoreName", "the existing ObjectStore the cluster is recovered from")
		return err
	}
	r.ObjectStore, err = parsePostgresqlObjectStore(src, postgresqlRecoveryObjectStorePath, "the object store the cluster is recovered from")
	return err
}

// parsePostgresqlRecoveryTarget reads recovery.target into the vendored
// RecoveryTarget, and refuses what CloudNativePG's webhook refuses without
// looking at the cluster (validateRecoveryTarget): more than one target, a
// time or LSN that does not parse, an xid that is not a 32-bit integer, a
// restore point name PostgreSQL cannot hold, a target found by backup ID
// only without one, and a timeline that is neither latest nor positive. It
// returns nil when no target is set.
func parsePostgresqlRecoveryTarget(recovery map[string]any) (*cnpgv1.RecoveryTarget, error) {
	const path = "recovery.target"
	raw, present, err := parseObjectField(recovery, "target", path)
	if err != nil || !present {
		return nil, err
	}
	if err := rejectUnknownKeys(raw, postgresqlRecoveryTargetKeys, path); err != nil {
		return nil, err
	}
	// Each string is omitted from the Cluster when empty, which would leave the
	// recovery running to the end of the WAL: an authored "" is refused.
	str := func(key string) (string, error) {
		return postgresqlNonEmptyString(raw, key, path+"."+key, "omitted when empty, the recovery would not stop there; remove the key")
	}
	var t cnpgv1.RecoveryTarget
	for _, f := range []struct {
		key string
		dst *string
	}{{"time", &t.TargetTime}, {"lsn", &t.TargetLSN}, {"xid", &t.TargetXID}, {"name", &t.TargetName}, {"backupID", &t.BackupID}, {"timeline", &t.TargetTLI}} {
		if *f.dst, err = str(f.key); err != nil {
			return nil, err
		}
	}
	if t.TargetImmediate, err = parseBoolField(raw, "immediate", path+".immediate"); err != nil {
		return nil, err
	}
	if t.Exclusive, err = parseBoolField(raw, "exclusive", path+".exclusive"); err != nil {
		return nil, err
	}

	// immediate counts whenever it is set, false included, as the webhook
	// counts it.
	var selectors []string
	for _, key := range postgresqlRecoverySelectors {
		if _, present := authoredValue(raw, key); present {
			selectors = append(selectors, key)
		}
	}
	if len(selectors) > 1 {
		return nil, errors.Errorf("%s: %s are mutually exclusive; set at most one", path, strings.Join(selectors, ", "))
	}
	if t.TargetTime != "" {
		if _, err := types.ParseTargetTime(nil, t.TargetTime); err != nil {
			return nil, errors.Errorf("%s.time %q: not a time the operator accepts (RFC 3339, or a PostgreSQL timestamp)", path, t.TargetTime)
		}
	}
	if t.TargetLSN != "" {
		if _, err := types.LSN(t.TargetLSN).Parse(); err != nil {
			return nil, errors.Errorf("%s.lsn %q: not a WAL location (for example 0/3000060)", path, t.TargetLSN)
		}
	}
	// PostgreSQL reads the xid as a 32-bit TransactionId.
	if t.TargetXID != "" {
		if _, err := strconv.ParseUint(t.TargetXID, 10, 32); err != nil {
			return nil, errors.Errorf("%s.xid %q: must be a transaction ID, a non-negative 32-bit integer", path, t.TargetXID)
		}
	}
	if len(t.TargetName) >= 64 {
		return nil, errors.Errorf("%s.name %q: must be shorter than 64 bytes (PostgreSQL's limit on a restore point name)", path, t.TargetName)
	}
	if strings.ContainsFunc(t.TargetName, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, errors.Errorf("%s.name %q: must not contain control characters", path, t.TargetName)
	}
	// The plugin finds the base backup from the catalog by time or LSN only.
	if t.BackupID == "" && len(selectors) == 1 && (selectors[0] == "xid" || selectors[0] == "name" || selectors[0] == "immediate") {
		return nil, errors.Errorf("%s.backupID: required with %s.%s (the base backup is found by time or lsn only)", path, path, selectors[0])
	}
	if t.TargetTLI != "" && t.TargetTLI != "latest" {
		if tli, err := strconv.Atoi(t.TargetTLI); err != nil || tli < 1 {
			return nil, errors.Errorf("%s.timeline %q: must be latest or a positive integer", path, t.TargetTLI)
		}
	}
	if t == (cnpgv1.RecoveryTarget{}) {
		return nil, nil
	}
	return &t, nil
}

// storeName is the name of the ObjectStore the Cluster is recovered from: the
// one referenced, or the inline one's as LowerComponent resolved it, else the
// default.
func (r *PostgresqlRecovery) storeName(component string) string {
	switch {
	case r.ObjectStoreName != "":
		return r.ObjectStoreName
	case r.ObjectName != "":
		return r.ObjectName
	}
	return postgresqlRecoverySourceName(component)
}

// bootstrap is the Cluster's bootstrap.recovery, whose source is the
// externalClusters entry externalCluster writes.
func (r *PostgresqlRecovery) bootstrap(component string) *cnpgv1.BootstrapRecovery {
	out := &cnpgv1.BootstrapRecovery{
		Source:   postgresqlRecoverySourceName(component),
		Database: r.Database,
		Owner:    r.Owner,
	}
	if r.Target != nil {
		target := *r.Target
		out.RecoveryTarget = &target
	}
	if r.Secret != "" {
		out.Secret = &cnpgv1.LocalObjectReference{Name: r.Secret}
	}
	return out
}

// externalCluster is the externalClusters entry the recovery reads through:
// the barman-cloud plugin, the store and the source's folder. serverName is
// always written: the plugin would otherwise default it to the new Cluster's
// name and read the wrong folder.
func (r *PostgresqlRecovery) externalCluster(component string) cnpgv1.ExternalCluster {
	return cnpgv1.ExternalCluster{
		Name: postgresqlRecoverySourceName(component),
		PluginConfiguration: &cnpgv1.PluginConfiguration{
			Name: barmanCloudPluginName,
			Parameters: map[string]string{
				"barmanObjectName": r.storeName(component),
				"serverName":       r.ServerName,
			},
		},
	}
}

// postgresqlRecoveryStoreMember returns the cnpg-objectstore the rule emits
// for an inline recovery.source.objectStore, named by the naming hook (role
// oam.NameRolePostgresqlRecoveryObjectStore), else
// "<component>-recovery-source", and reserved as a component of that name. It
// sets the resolved name on recovery, which the Cluster's recovery source
// names the store by.
func postgresqlRecoveryStoreMember(comp *oam.Component, lctx oam.LoweringContext, recovery *PostgresqlRecovery) (oam.Component, error) {
	const what = postgresqlRecoveryObjectStorePath
	lctx.Component = comp
	spec := memberNameSpec(&CnpgObjectStoreHandler{}, oam.NameRolePostgresqlRecoveryObjectStore)
	name, err := lctx.ResolveName(comp.Name, postgresqlRecoverySourceSuffix, spec)
	if err != nil {
		return oam.Component{}, errors.Wrapf(err, "%s", what)
	}
	if err := postgresqlReserveMember(lctx, name, what); err != nil {
		return oam.Component{}, err
	}
	recovery.ObjectName = name
	props, err := specProperties(barmanv1.ObjectStoreSpec{Configuration: *recovery.ObjectStore.DeepCopy()})
	if err != nil {
		return oam.Component{}, err
	}
	return oam.Component{Name: name, Type: "cnpg-objectstore", Properties: props, Annotations: maps.Clone(comp.Annotations)}, nil
}

// postgresqlFolder is where Barman reads and writes one cluster's backups and
// WAL: it addresses an archive by the endpoint, the destination path and the
// server name together.
type postgresqlFolder struct {
	endpointURL, destinationPath, serverName string
}

func (f postgresqlFolder) String() string {
	return fmt.Sprintf("endpointURL %q, destinationPath %q, serverName %q", f.endpointURL, f.destinationPath, f.serverName)
}

// postgresqlArchivesIntoSource refuses a component that would archive its WAL
// into the folder it is recovered from, where launcher can see both: the
// recovered Cluster's WAL would land among the source's. It compares the
// backup folder (backup.objectStore and backup.serverName, else the name of
// the Cluster, cluster) with the recovery source's, read from the inline
// store or from the store objectStoreName names when the document emits it:
// this component's backup store, another postgresql component's, or a
// cnpg-objectstore component. The plugin checks the destination before it
// archives (CheckWalArchiveDestination); a store outside the document is left
// to that check.
//
// Another component's store is known by the name its document gives it (its
// objectStoreObjectName or objectName, else its component name): a naming
// hook's answer for it is resolved when that component is lowered, and is not
// seen here.
func postgresqlArchivesIntoSource(comp *oam.Component, doc *oam.Application, c *PostgresqlConfig, cluster string) error {
	if c.Backup == nil || c.Recovery == nil {
		return nil
	}
	archive := postgresqlFolder{c.Backup.ObjectStore.EndpointURL, c.Backup.ObjectStore.DestinationPath, c.Backup.ServerName}
	if archive.serverName == "" {
		archive.serverName = cluster
	}
	source := c.Recovery.ObjectStore
	if name := c.Recovery.ObjectStoreName; name != "" {
		own := c.Backup.ObjectName
		if own == "" {
			own = comp.Name
		}
		if name == own {
			source = &c.Backup.ObjectStore
		} else {
			source = postgresqlDocumentStore(doc, comp.Name, name)
		}
	}
	if source == nil {
		return nil
	}
	from := postgresqlFolder{source.EndpointURL, source.DestinationPath, c.Recovery.ServerName}
	if from != archive {
		return nil
	}
	return errors.Errorf("recovery.source: the cluster would archive its WAL into the folder it is recovered from (%s), where the source cluster's backups and WAL are; set backup.serverName, or another backup.objectStore destinationPath", archive)
}

// postgresqlDocumentStore returns the configuration of the ObjectStore named
// name that a component of doc other than self emits, or nil: a postgresql
// component's backup store or a cnpg-objectstore component, each known by the
// name the document gives it. A component that does not parse is skipped: it
// is refused when it is lowered.
func postgresqlDocumentStore(doc *oam.Application, self, name string) *barmanapi.BarmanObjectStoreConfiguration {
	if doc == nil {
		return nil
	}
	for _, other := range doc.Spec.Components {
		if other.Name == self {
			continue
		}
		switch other.Type {
		case "postgresql":
			backup, err := parsePostgresqlBackup(other.Properties)
			if err != nil || backup == nil {
				continue
			}
			raw, _ := other.Properties["backup"].(map[string]any)
			storeName := other.Name
			if authored, _, err := parseRawStringField(raw, postgresqlObjectStoreNameProperty, ""); err == nil && authored != "" {
				storeName = authored
			}
			if storeName == name {
				return &backup.ObjectStore
			}
		case "cnpg-objectstore":
			storeName := other.Name
			if authored, ok := other.Properties[oam.ObjectNameProperty].(string); ok && authored != "" {
				storeName = authored
			}
			if storeName != name {
				continue
			}
			// objectName is the engine's, not the spec's.
			props := maps.Clone(other.Properties)
			delete(props, oam.ObjectNameProperty)
			spec, _, err := decodeKindSpec[barmanv1.ObjectStoreSpec](props, "barmancloud.cnpg.io/v1 ObjectStoreSpec")
			if err != nil {
				continue
			}
			return &spec.Configuration
		}
	}
	return nil
}
