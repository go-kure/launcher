package components

import (
	"maps"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgScheduledBackupHandler handles OAM cnpg-scheduledbackup components: the
// kind-named projection of a CloudNativePG postgresql.cnpg.io/v1
// ScheduledBackup (go-kure/launcher#790), the schedule on which the operator
// creates the Backups of a Cluster.
//
// Its properties are exactly the top-level fields of
// cnpgv1.ScheduledBackupSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ScheduledBackup, named after the component
// unless `objectName` names it, in the build namespace, and nothing else: the
// Backups are the operator's. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type CnpgScheduledBackupHandler struct{}

// CanHandle returns true for the cnpg-scheduledbackup component type.
func (h *CnpgScheduledBackupHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-scheduledbackup"
}

// PropertySchema declares every top-level cnpgv1.ScheduledBackupSpec field by
// its json name: the fields of a Backup's spec (cnpgBackupSchema) and the four
// of the schedule. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *CnpgScheduledBackupHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ScheduledBackup spec."
	schema := cnpgBackupSchema("ScheduledBackup", "the backups are taken of")
	maps.Copy(schema, map[string]oam.PropertySchema{
		"schedule": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "schedule: when a backup is taken, as a cron expression of six fields, the first of which is the seconds (\"0 0 3 * * *\" is every day at 03:00). The operator reads it; launcher does not parse it.",
		},
		"suspend": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "suspend: true, no backup is scheduled until it is set back. Unset or false, the schedule runs.",
		},
		"immediate": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "immediate: true, a first backup is taken as soon as the object is created, before the schedule's first time. Unset or false, the first backup is the schedule's.",
		},
		"backupOwnerReference": {
			Type:        oam.PropertyTypeString,
			Description: spec + "backupOwnerReference: the owner the operator sets on each Backup it creates: none, self (the ScheduledBackup) or cluster (the Cluster). Unset, the API's default applies: none.",
		},
	})
	return schema
}

// cnpgScheduledBackupDefaultedZeroFields lists the ScheduledBackupSpec fields
// on which an authored 0, false or "" would be silently replaced, keyed and
// valued as cnpgClusterDefaultedZeroFields is.
// TestCnpgKindsDefaultedZeroFields_MatchCRD derives it from the linked
// CloudNativePG module.
var cnpgScheduledBackupDefaultedZeroFields = map[string]string{
	"backupOwnerReference": `"none"`,
	"method":               `"barmanObjectStore"`,
}

// cnpgScheduledBackupKind is the cnpg-scheduledbackup kind: see
// policyFreeKind. A ScheduledBackup runs no pod of its own and names no image.
// validate holds the Cluster reference to a name a Cluster can have. The CRD's
// one expression rule refuses a change of the stored Cluster reference, which
// a build does not have; it, the API's value rules and the operator's webhook
// (that the schedule parses, with only a warning for one that has other than
// six fields, and that the online settings do not come with the
// barmanObjectStore method) are left to them.
var cnpgScheduledBackupKind = &policyFreeKind[cnpgv1.ScheduledBackupSpec]{
	upstream: "postgresql.cnpg.io/v1 ScheduledBackupSpec",
	required: requiredFields(
		map[string]string{
			"schedule":     "when a backup is taken, as a cron expression",
			"cluster":      "the CloudNativePG Cluster the backups are taken of",
			"cluster.name": "the name of that Cluster",
		},
		cnpgBackupPluginRequired,
	),
	defaultedZeros: cnpgDefaultedZeros(cnpgScheduledBackupDefaultedZeroFields),
	validate: func(spec *cnpgv1.ScheduledBackupSpec) error {
		return requireCnpgClusterRef(spec.Cluster.Name)
	},
	build: func(name, namespace string, spec *cnpgv1.ScheduledBackupSpec) client.Object {
		scheduled := kurecnpg.CreateScheduledBackup(name, namespace)
		spec.DeepCopyInto(&scheduled.Spec)
		return scheduled
	},
}

// ToApplicationConfig decodes an OAM cnpg-scheduledbackup component into its
// config. The object takes the namespace of the application it is generated in.
func (h *CnpgScheduledBackupHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgScheduledBackupKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgScheduledBackupHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-scheduledbackup")
}

// ComponentObject declares the cnpg-scheduledbackup kind's ScheduledBackup.
func (h *CnpgScheduledBackupHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind("ScheduledBackup"), oam.ObjectScopeNamespaced
}
