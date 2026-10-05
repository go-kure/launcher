package components

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgDatabaseRoleHandler handles OAM cnpg-databaserole components: the
// kind-named projection of a CloudNativePG postgresql.cnpg.io/v1 DatabaseRole
// (go-kure/launcher#790), one PostgreSQL role managed declaratively inside an
// existing Cluster.
//
// Its properties are exactly the top-level fields of cnpgv1.DatabaseRoleSpec,
// under their json names, the fields of the role configuration it embeds
// among them, decoded strictly (decodeKindSpec). It emits the DatabaseRole,
// named after the component unless `objectName` names it, in the build
// namespace, and nothing else: the Secret `passwordSecret` names is not
// created. TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type CnpgDatabaseRoleHandler struct{}

// CanHandle returns true for the cnpg-databaserole component type.
func (h *CnpgDatabaseRoleHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-databaserole"
}

// PropertySchema declares every top-level cnpgv1.DatabaseRoleSpec field by its
// json name. Scalars carry their type; structured fields are open objects
// whose content is checked by the strict decode, not by this schema.
func (h *CnpgDatabaseRoleHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "DatabaseRole spec."
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: spec + desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: spec + desc}
	}
	return map[string]oam.PropertySchema{
		"cluster": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "cluster: the CloudNativePG Cluster the role is managed in: {name: <cluster>}, a Cluster of the same namespace. The API refuses a change once the object exists.",
		},
		"name": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "name: the name of the role inside PostgreSQL. postgres, streaming_replica and every name that starts with pg_ or cnpg_ are reserved. The API refuses a change once the object exists.",
		},
		"comment": str("comment: a description of the role."),
		"ensure":  str("ensure: present. The API refuses absent on a DatabaseRole: a role is removed by deleting the object with databaseRoleReclaimPolicy: delete. Unset, the API's default applies: present."),
		"passwordSecret": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "passwordSecret: the Secret that holds the role's password: {name: <secret>}, a kubernetes.io/basic-auth Secret with a username and a password. Not with disablePassword: true. launcher emits no Secret for it.",
		},
		"connectionLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "connectionLimit: how many concurrent connections the role can make. Unset, the API's default applies: -1, no limit. An authored 0 is refused: the API type omits it, and the default would apply in its place.",
		},
		"validUntil": str("validUntil: the date and time after which the role's password is no longer valid, in RFC 3339 form (\"2030-01-01T00:00:00Z\"), to the second; it is written in UTC. A fraction of a second and the zero time (0001-01-01T00:00:00Z) are refused: the API type cannot write them. Unset, the password never expires."),
		"inRoles": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "inRoles: the existing roles the role is a member of.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "The name of one role."},
		},
		"inherit":                   boolean("inherit: whether the role inherits the privileges of the roles it is a member of. Unset, the API's default applies: true."),
		"disablePassword":           boolean("disablePassword: true, the role's password is set to NULL in PostgreSQL. Not with passwordSecret."),
		"superuser":                 boolean("superuser: whether the role is a superuser. Unset or false, it is not."),
		"createdb":                  boolean("createdb: whether the role may create databases. Unset or false, it may not."),
		"createrole":                boolean("createrole: whether the role may create, alter and drop other roles. Unset or false, it may not."),
		"login":                     boolean("login: whether the role may log in. Unset or false, it may not."),
		"replication":               boolean("replication: whether the role is a replication role. Unset or false, it is not."),
		"bypassrls":                 boolean("bypassrls: whether the role bypasses every row-level security policy. Unset or false, it does not."),
		"databaseRoleReclaimPolicy": str("databaseRoleReclaimPolicy: what happens to the role when the DatabaseRole object is deleted: retain or delete. Unset, the API's default applies: retain."),
		"clientCertificate": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "clientCertificate: has the operator issue and renew a TLS client certificate for the role, in the Secret <object name>-client-cert: enabled (unset, the API's default applies: true). An enabled certificate requires login: true.",
		},
	}
}

// cnpgDatabaseRoleDefaultedZeroFields lists the DatabaseRoleSpec fields on
// which an authored 0, false or "" would be silently replaced, keyed and
// valued as cnpgClusterDefaultedZeroFields is.
// TestCnpgKindsDefaultedZeroFields_MatchCRD derives it from the linked
// CloudNativePG module.
var cnpgDatabaseRoleDefaultedZeroFields = map[string]string{
	"connectionLimit":           "-1",
	"databaseRoleReclaimPolicy": `"retain"`,
	"ensure":                    `"present"`,
}

// cnpgDatabaseRoleKind is the cnpg-databaserole kind: see policyFreeKind. A
// DatabaseRole runs no pod and names no image, and its password is a
// reference to a Secret, never a value. The CRD's two expression rules that
// compare a field with its value on the stored object say nothing of a new
// one; they and the API's value rules are left to the API server. Its other
// eight are validate's.
var cnpgDatabaseRoleKind = &policyFreeKind[cnpgv1.DatabaseRoleSpec]{
	upstream: "postgresql.cnpg.io/v1 DatabaseRoleSpec",
	required: requiredFields(
		map[string]string{
			"name":    "the name of the role inside PostgreSQL",
			"cluster": "the CloudNativePG Cluster the role is managed in",
		},
		refNamesRequired("Secret", "passwordSecret"),
	),
	defaultedZeros: cnpgDefaultedZeros(cnpgDatabaseRoleDefaultedZeroFields),
	validate:       validateCnpgDatabaseRole,
	build: func(name, namespace string, spec *cnpgv1.DatabaseRoleSpec) client.Object {
		role := kurecnpg.CreateDatabaseRole(name, namespace)
		spec.DeepCopyInto(&role.Spec)
		return role
	},
}

// validateCnpgDatabaseRole refuses a spec the DatabaseRole CRD refuses on
// creation: each of its eight expression rules that read one document only.
// They are comparisons of authored fields with fixed strings the CRD itself
// states, and a document that breaks one can never be admitted, so the build
// says so, as cnpg-database does for its reserved names. A client certificate
// is enabled where its block is authored and `enabled` is not false: the API
// fills true into an omitted one before it evaluates the rule.
//
// It also refuses a validUntil the object would not carry as authored
// (refuseCnpgValidUntilWritten): the zero instant, which metav1.Time writes as
// null, which the API server drops, so the password would never expire, and an
// instant metav1.Time writes in UTC outside the years the CRD's date-time
// format admits, though the authored offset may keep it in range
// (9999-12-31T23:00:00-02:00). A fraction of a second is refused before the
// decode (refuseCnpgValidUntilFraction), which keeps nine digits of it only.
func validateCnpgDatabaseRole(spec *cnpgv1.DatabaseRoleSpec) error {
	if err := requireCnpgClusterRef(spec.ClusterRef.Name); err != nil {
		return err
	}
	switch name := spec.Name; {
	case name == "":
		return errors.New("name: empty, the DatabaseRole CRD refuses a role with no name")
	case name == "postgres" || name == "streaming_replica":
		return errors.Errorf("name %q: reserved, the DatabaseRole CRD refuses it", name)
	case strings.HasPrefix(name, "pg_"):
		return errors.Errorf("name %q: a name that starts with pg_ is reserved by PostgreSQL, the DatabaseRole CRD refuses it", name)
	case strings.HasPrefix(name, "cnpg_"):
		return errors.Errorf("name %q: a name that starts with cnpg_ is reserved by the operator, the DatabaseRole CRD refuses it", name)
	}
	if spec.Ensure == cnpgv1.EnsureAbsent {
		return errors.New("ensure: absent is not supported for a DatabaseRole, the CRD refuses it; delete the object with databaseRoleReclaimPolicy: delete instead")
	}
	if spec.PasswordSecret != nil && spec.DisablePassword {
		return errors.New("passwordSecret and disablePassword: true are both set; the API takes at most one")
	}
	if cert := spec.ClientCertificate; cert != nil && (cert.Enabled == nil || *cert.Enabled) && !spec.Login {
		return errors.New("clientCertificate: an enabled client certificate requires login: true (enabled is true unless it is authored false)")
	}
	if spec.ValidUntil != nil {
		return refuseCnpgValidUntilWritten(spec.ValidUntil)
	}
	return nil
}

// refuseCnpgValidUntilWritten refuses a validUntil the object cannot carry as
// decoded: the time as metav1.Time writes it must be a date-time the CRD's
// format admits (strfmt.IsDateTime, the check of the API server's schema
// validator) and the instant the decode read.
func refuseCnpgValidUntilWritten(until *metav1.Time) error {
	data, err := until.MarshalJSON()
	if err != nil {
		return errors.Wrap(err, "validUntil")
	}
	if string(data) == "null" {
		return errors.New("validUntil: the zero time cannot be carried by the CloudNativePG API types, which write it as no expiry; leave validUntil out for a password that never expires")
	}
	written := strings.Trim(string(data), `"`)
	if !strfmt.IsDateTime(written) {
		return errors.Errorf("validUntil: the CloudNativePG API types write it as %s, which the DatabaseRole CRD's date-time format refuses", written)
	}
	if back, err := time.Parse(time.RFC3339, written); err != nil || !back.Equal(until.Time) {
		return errors.Errorf("validUntil: the CloudNativePG API types write it as %s, which is not the instant authored", written)
	}
	return nil
}

// cnpgValidUntilFraction matches the fraction of the seconds of a time as the
// decode's parse, time.Parse with time.RFC3339, admits one: an hour of one
// digit or two, and a fraction after a period or a comma.
var cnpgValidUntilFraction = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[Tt]\d{1,2}:\d{2}:\d{2}[.,](\d+)`)

// refuseCnpgValidUntilFraction refuses an authored validUntil with a fraction
// of a second other than zero: metav1.Time writes the time to the second, so
// the fraction would be cut off. It reads the authored string, not the decoded
// time, because the decode keeps nine digits of a fraction and drops the rest:
// a fraction past the ninth digit is not in the decoded value. The string is
// the one the decode reads: the properties go through the same JSON
// normalization (jsonProperties), and encoding/json picks the key, so a key
// spelled in another case is read as the decode reads it. A value that is not
// such a time, and properties that do not serialize, are left to the decode to
// refuse.
func refuseCnpgValidUntilFraction(props map[string]any) error {
	authored, ok := cnpgAuthoredValidUntil(props)
	if !ok {
		return nil
	}
	if m := cnpgValidUntilFraction.FindStringSubmatch(authored); m != nil && strings.Trim(m[1], "0") != "" {
		return errors.Errorf("validUntil %q: a fraction of a second cannot be carried by the CloudNativePG API types, which write the time to the second", authored)
	}
	return nil
}

// cnpgAuthoredValidUntil returns the string the decode reads as validUntil,
// and false where there is none: no such field, a value that is not a string,
// or properties that do not serialize, each of which the decode answers.
func cnpgAuthoredValidUntil(props map[string]any) (string, bool) {
	stripped, _, err := jsonProperties(props)
	if err != nil {
		return "", false
	}
	data, err := json.Marshal(stripped)
	if err != nil {
		return "", false
	}
	var decoded struct {
		ValidUntil any `json:"validUntil"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", false
	}
	authored, ok := decoded.ValidUntil.(string)
	return authored, ok
}

// ToApplicationConfig decodes an OAM cnpg-databaserole component into its
// config. The object takes the namespace of the application it is generated in.
func (h *CnpgDatabaseRoleHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	if err := refuseCnpgValidUntilFraction(component.Properties); err != nil {
		return nil, err
	}
	return cnpgDatabaseRoleKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgDatabaseRoleHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-databaserole")
}

// ComponentObject declares the cnpg-databaserole kind's DatabaseRole.
func (h *CnpgDatabaseRoleHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind("DatabaseRole"), oam.ObjectScopeNamespaced
}
