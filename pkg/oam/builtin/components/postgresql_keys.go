package components

import "github.com/go-kure/launcher/pkg/oam"

// The member types the postgresql rule's map names.
const (
	postgresqlClusterMemberType         = "cnpg-cluster"
	postgresqlObjectStoreMemberType     = "cnpg-objectstore"
	postgresqlPoolerMemberType          = "cnpg-pooler"
	postgresqlDatabaseMemberType        = "cnpg-database"
	postgresqlScheduledBackupMemberType = "cnpg-scheduledbackup"
)

// LoweringKeys implements oam.LoweringKeyDeclarer. The rule forwards no key as
// written: it parses each and builds the specs of its members, so the places are
// declared here, beside those builders (PostgresqlConfig.clusterSpec,
// poolerSpec, databaseSpec, objectStoreSpec, scheduledBackupSpec, and the
// recovery builders), and no place is Unchanged.
//
// A place names a member type and no instance: backup.objectStore and an inline
// recovery.source.objectStore each reach a cnpg-objectstore of their own, and
// each database and schedule is a component of its own.
func (PostgresqlRule) LoweringKeys() map[string][]oam.LoweredKey {
	at := func(member string, paths ...string) []oam.LoweredKey {
		places := make([]oam.LoweredKey, 0, len(paths))
		for _, path := range paths {
			places = append(places, oam.LoweredKey{Member: member, Path: path})
		}
		return places
	}
	cluster := func(paths ...string) []oam.LoweredKey { return at(postgresqlClusterMemberType, paths...) }
	join := func(groups ...[]oam.LoweredKey) []oam.LoweredKey {
		var places []oam.LoweredKey
		for _, g := range groups {
			places = append(places, g...)
		}
		return places
	}

	// version only forms the image where imageName names none.
	version := cluster("imageName")
	version[0].When = []oam.LoweredWhen{{Key: "imageName", Values: []string{""}}}

	return map[string][]oam.LoweredKey{
		// One value is admitted, and it is written nowhere.
		"provider": nil,
		// Names the Pooler.
		"poolerName": nil,
		// Names the Cluster, and the members that refer to it name it too.
		postgresqlClusterNameProperty: join(
			at(postgresqlPoolerMemberType, "cluster"),
			at(postgresqlDatabaseMemberType, "cluster"),
			at(postgresqlScheduledBackupMemberType, "cluster"),
		),

		"version":           version,
		"imageName":         cluster("imageName"),
		"storageSize":       cluster("storage.size"),
		"replicas":          cluster("instances"),
		"resources":         cluster("resources"),
		"inheritedMetadata": cluster("inheritedMetadata"),
		"monitoring":        cluster("monitoring"),
		"affinity":          cluster("affinity"),
		"postgresql":        cluster("postgresql.parameters"),
		"replication":       cluster("postgresql.synchronous"),
		"managedRoles":      cluster("managed.roles"),
		"externalClusters":  cluster("externalClusters"),
		"bootstrap":         cluster("bootstrap"),
		"recovery": join(
			cluster("bootstrap.recovery", "externalClusters"),
			at(postgresqlObjectStoreMemberType, "configuration"),
		),
		"backup": join(
			cluster("plugins"),
			at(postgresqlObjectStoreMemberType, "configuration", "retentionPolicy"),
			at(postgresqlScheduledBackupMemberType, "schedule", "immediate", "suspend", "target", "backupOwnerReference"),
		),
		"pooler":    at(postgresqlPoolerMemberType, "instances", "type", "pgbouncer"),
		"databases": at(postgresqlDatabaseMemberType, "name", "owner", "ensure", "databaseReclaimPolicy", "extensions"),
	}
}
