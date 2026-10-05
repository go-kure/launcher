package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgBackupHandler handles OAM cnpg-backup components: the kind-named
// projection of a CloudNativePG postgresql.cnpg.io/v1 Backup
// (go-kure/launcher#790), the request for one backup of a Cluster.
//
// Its properties are exactly the top-level fields of cnpgv1.BackupSpec, under
// their json names, decoded strictly (decodeKindSpec). It emits the Backup,
// named after the component unless `objectName` names it, in the build
// namespace, and nothing else. A Backup is a one-shot request: the operator
// takes the backup once, and the API refuses every later change of the spec.
// Applied again from a repository the object is unchanged and nothing runs; a
// backup that recurs is a cnpg-scheduledbackup.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CnpgBackupHandler struct{}

// CanHandle returns true for the cnpg-backup component type.
func (h *CnpgBackupHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-backup"
}

// PropertySchema declares every top-level cnpgv1.BackupSpec field by its json
// name. Structured fields are open objects whose content is checked by the
// strict decode, not by this schema.
func (h *CnpgBackupHandler) PropertySchema() map[string]oam.PropertySchema {
	return cnpgBackupSchema("Backup", "the backup is taken of")
}

// cnpgBackupDefaultedZeroFields lists the BackupSpec fields on which an
// authored 0, false or "" would be silently replaced, keyed and valued as
// cnpgClusterDefaultedZeroFields is. TestCnpgKindsDefaultedZeroFields_MatchCRD
// derives it from the linked CloudNativePG module.
var cnpgBackupDefaultedZeroFields = map[string]string{
	"method": `"barmanObjectStore"`,
}

// cnpgBackupKind is the cnpg-backup kind: see policyFreeKind. A Backup runs no
// pod of its own and names no image: the operator takes the backup in the
// Cluster's pods. validate holds the Cluster reference to a name a Cluster can
// have. The CRD's one expression rule refuses a change of the stored spec,
// which a build does not have; it, the API's value rules and the operator's
// webhook (that the plugin method comes with a plugin configuration, that the
// online settings do not come with the barmanObjectStore method) are left to
// them.
var cnpgBackupKind = &policyFreeKind[cnpgv1.BackupSpec]{
	upstream:       "postgresql.cnpg.io/v1 BackupSpec",
	required:       cnpgBackupRequired,
	defaultedZeros: cnpgDefaultedZeros(cnpgBackupDefaultedZeroFields),
	validate: func(spec *cnpgv1.BackupSpec) error {
		return requireCnpgClusterRef(spec.Cluster.Name)
	},
	build: func(name, namespace string, spec *cnpgv1.BackupSpec) client.Object {
		backup := kurecnpg.CreateBackup(name, namespace)
		spec.DeepCopyInto(&backup.Spec)
		return backup
	},
}

// ToApplicationConfig decodes an OAM cnpg-backup component into its config.
// The object takes the namespace of the application it is generated in.
func (h *CnpgBackupHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgBackupKind.config(component)
}

// cnpgBackupSchema returns the properties the cnpg-backup and
// cnpg-scheduledbackup kinds share: the fields of cnpgv1.BackupSpec, each of
// which cnpgv1.ScheduledBackupSpec holds too, under the same json name and of
// the same type. kind names the object in each description ("Backup"), and
// cluster ends the description of its Cluster reference.
func cnpgBackupSchema(kind, cluster string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	const decoded = " Decoded strictly into the CloudNativePG API type: see "
	return map[string]oam.PropertySchema{
		"cluster": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "cluster: the CloudNativePG Cluster " + cluster + ": {name: <cluster>}, a Cluster of the same namespace.",
		},
		"target": {
			Type:        oam.PropertyTypeString,
			Description: spec + "target: the instance the backup is taken on: primary or prefer-standby. Unset, the Cluster's own backup target applies.",
		},
		"method": {
			Type:        oam.PropertyTypeString,
			Description: spec + "method: how the backup is taken: barmanObjectStore, volumeSnapshot or plugin. Unset, the API's default applies: barmanObjectStore.",
		},
		"pluginConfiguration": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "pluginConfiguration: the plugin that takes the backup, with the plugin method: name (required) and parameters (a map of strings, passed to the plugin as authored)." + decoded + "BackupPluginConfiguration in its API reference.",
		},
		"online": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "online: with the volumeSnapshot method, whether the backup is online (true, hot) or offline (false, cold). Unset, the Cluster's own setting applies.",
		},
		"onlineConfiguration": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "onlineConfiguration: how an online volume snapshot is taken: waitForArchive (unset, the API's default applies: true) and immediateCheckpoint. Unset, the Cluster's own settings apply." + decoded + "OnlineConfiguration in its API reference.",
		},
	}
}

// cnpgBackupRequired is the required list of the cnpg-backup kind: the fields
// the Backup CRD requires that the Go types write whether or not they were
// authored. TestCnpgFurtherKinds_RequiredMatchCRD holds the list to the CRD.
var cnpgBackupRequired = requiredFields(
	map[string]string{
		"cluster":      "the CloudNativePG Cluster the backup is taken of",
		"cluster.name": "the name of that Cluster",
	},
	cnpgBackupPluginRequired,
)

// cnpgBackupPluginRequired is the required list of a backup's plugin
// configuration, which is optional itself: of one that is authored, the API
// requires the name, which the Go type would write empty.
var cnpgBackupPluginRequired = map[string]string{
	"pluginConfiguration.name": "the name of the plugin that takes the backup",
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgBackupHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-backup")
}

// ComponentObject declares the cnpg-backup kind's Backup.
func (h *CnpgBackupHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.BackupKind), oam.ObjectScopeNamespaced
}
