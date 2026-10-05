package components

import (
	"maps"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgDatabaseHandler handles OAM cnpg-database components: the kind-named,
// full-fidelity projection of a CloudNativePG postgresql.cnpg.io/v1 Database,
// one PostgreSQL database managed declaratively inside an existing Cluster.
//
// Its properties are exactly the top-level fields of cnpgv1.DatabaseSpec,
// under their json names, decoded strictly as cnpg-cluster's are, and the
// handler adds nothing to what was authored beyond the CRD's own default for
// an unauthored schema, extension, fdw or server ensure, which the Go type
// cannot omit (cnpgDatabaseAlwaysEncodedDefaults). A Database runs no workload, so no
// environment policy applies to it. TestCnpgDatabaseSchema_CoversDatabaseSpec
// keeps the published key set equal to the upstream json tags.
type CnpgDatabaseHandler struct{}

// CanHandle returns true for the cnpg-database component type.
func (h *CnpgDatabaseHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-database"
}

// PropertySchema declares every top-level cnpgv1.DatabaseSpec field by its
// json name. Scalars carry their type; structured fields are open objects
// whose content is checked by the strict decode, not by this schema.
func (h *CnpgDatabaseHandler) PropertySchema() map[string]oam.PropertySchema {
	const ref = " Decoded strictly into the CloudNativePG API type: see the DatabaseSpec reference in the CloudNativePG documentation for its fields."
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
	}
	arr := func(desc, itemDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type:        oam.PropertyTypeArray,
			Description: desc + ref,
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: itemDesc},
		}
	}
	return map[string]oam.PropertySchema{
		"cluster":               {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Required. Reference to the CloudNativePG Cluster hosting the database: {name: <cluster>}." + ref},
		"ensure":                str("Whether the database is present or absent. The operator's default (present) applies when omitted."),
		"name":                  str("Required. Name of the database inside PostgreSQL. postgres, template0 and template1 are reserved."),
		"owner":                 str("Required. Role that owns the database."),
		"template":              str("Template database the database is created from."),
		"encoding":              str("Character encoding of the database."),
		"locale":                str("Locale of the database."),
		"localeProvider":        str("Locale provider: libc, icu or builtin."),
		"localeCollate":         str("LC_COLLATE of the database."),
		"localeCType":           str("LC_CTYPE of the database."),
		"icuLocale":             str("ICU locale, with the icu locale provider."),
		"icuRules":              str("Additional ICU collation rules, with the icu locale provider."),
		"builtinLocale":         str("Locale of the builtin locale provider."),
		"collationVersion":      str("Collation version of the database."),
		"isTemplate":            boolean("Whether the database can be cloned as a template."),
		"allowConnections":      boolean("Whether the database accepts connections."),
		"connectionLimit":       {Type: oam.PropertyTypeInteger, Description: "Maximum concurrent connections to the database; -1 is unlimited."},
		"tablespace":            str("Default tablespace of the database."),
		"databaseReclaimPolicy": str("What happens to the database when the Database object is deleted: retain or delete. The operator's default (retain) applies when omitted."),
		"schemas":               arr("Schemas managed in the database.", "A single schema."),
		"extensions":            arr("Extensions managed in the database.", "A single extension."),
		"fdws":                  arr("Foreign data wrappers managed in the database.", "A single foreign data wrapper."),
		"servers":               arr("Foreign servers managed in the database.", "A single foreign server."),
	}
}

// cnpgDatabaseDefaultedZeroFields lists the DatabaseSpec fields on which an
// authored 0 or false would be silently replaced, keyed and valued as
// cnpgClusterDefaultedZeroFields is. TestCnpgKindsDefaultedZeroFields_MatchCRD
// derives it from the linked CloudNativePG module.
var cnpgDatabaseDefaultedZeroFields = map[string]string{}

// cnpgDatabaseAlwaysEncodedDefaults lists the DatabaseSpec fields the Go type
// always encodes (no omitempty) although the CRD gives them a default: an
// unauthored one would reach the API server as "", which the CRD's enum
// refuses, instead of being defaulted. Generate writes the CRD default there,
// exactly what the API server would have applied to an omitted field. Keyed by
// json path with [] for an array element; TestCnpgKindsAlwaysEncodedDefaults_MatchCRD
// derives it from the linked CloudNativePG module.
var cnpgDatabaseAlwaysEncodedDefaults = map[string]string{
	"schemas[].ensure":    string(cnpgv1.EnsurePresent),
	"extensions[].ensure": string(cnpgv1.EnsurePresent),
	"fdws[].ensure":       string(cnpgv1.EnsurePresent),
	"servers[].ensure":    string(cnpgv1.EnsurePresent),
}

// refuseEmptyAlwaysEncodedDefaults refuses an authored "" on a field of
// cnpgDatabaseAlwaysEncodedDefaults. The Go type cannot tell it from an
// unauthored one, so Generate would write the CRD default over it, while the
// CRD's enum refuses it as written. authored is the stripped property tree
// decodeKindSpec returns; keys match case-insensitively, as the decode's do.
func refuseEmptyAlwaysEncodedDefaults(authored map[string]any) error {
	for _, path := range slices.Sorted(maps.Keys(cnpgDatabaseAlwaysEncodedDefaults)) {
		list, field, ok := strings.Cut(path, "[].")
		if !ok {
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(authored)) {
			if !strings.EqualFold(k, list) {
				continue
			}
			items, _ := authored[k].([]any)
			for i, item := range items {
				m, _ := item.(map[string]any)
				for _, fk := range slices.Sorted(maps.Keys(m)) {
					if strings.EqualFold(fk, field) && m[fk] == "" {
						return errors.Errorf(`%s[%d].%s: "" is refused by the Database CRD, whose enum is present or absent; omit the field for the operator's default, %s`,
							k, i, fk, cnpgDatabaseAlwaysEncodedDefaults[path])
					}
				}
			}
		}
	}
	return nil
}

// fillAlwaysEncodedDefaults writes the CRD default into each field of
// cnpgDatabaseAlwaysEncodedDefaults left empty. An authored "" never reaches
// it (refuseEmptyAlwaysEncodedDefaults), so an empty field is unauthored, or
// empty in a config built directly in Go, where the two cannot be told apart.
func fillAlwaysEncodedDefaults(s *cnpgv1.DatabaseSpec) {
	fill := func(o *cnpgv1.DatabaseObjectSpec) {
		if o.Ensure == "" {
			o.Ensure = cnpgv1.EnsurePresent
		}
	}
	for i := range s.Schemas {
		fill(&s.Schemas[i].DatabaseObjectSpec)
	}
	for i := range s.Extensions {
		fill(&s.Extensions[i].DatabaseObjectSpec)
	}
	for i := range s.FDWs {
		fill(&s.FDWs[i].DatabaseObjectSpec)
	}
	for i := range s.Servers {
		fill(&s.Servers[i].DatabaseObjectSpec)
	}
}

// cnpgReservedDatabaseNames are the database names the Database CRD refuses.
var cnpgReservedDatabaseNames = map[string]bool{"postgres": true, "template0": true, "template1": true}

// ToApplicationConfig decodes an OAM cnpg-database component into a
// CnpgDatabaseConfig, under the same null contract and strict decode as
// cnpg-cluster. The fields the Database CRD requires must be authored and
// non-empty: cluster.name, name and owner, which the Go type would otherwise
// encode as empty strings.
func (h *CnpgDatabaseHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[cnpgv1.DatabaseSpec](component.Properties, "postgresql.cnpg.io/v1 DatabaseSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, cnpgDefaultedZeros(cnpgDatabaseDefaultedZeroFields)); err != nil {
		return nil, err
	}
	if err := refuseEmptyAlwaysEncodedDefaults(props); err != nil {
		return nil, err
	}
	cfg := &CnpgDatabaseConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CnpgDatabaseConfig implements stack.ApplicationConfig for cnpg-database
// components. Spec is the decoded DatabaseSpec exactly as authored.
type CnpgDatabaseConfig struct {
	Name string
	// ObjectName names the Database object (oam.Component.ObjectName); the
	// PostgreSQL database is Spec.Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Database object
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      cnpgv1.DatabaseSpec
}

// validate refuses a spec the Database CRD would refuse, or the operator could
// not reconcile, for a reason the strict decode cannot see.
func (c *CnpgDatabaseConfig) validate() error {
	if err := requireCnpgClusterRef(c.Spec.ClusterRef.Name); err != nil {
		return err
	}
	if c.Spec.Name == "" {
		return errors.New("name: required (the name of the database inside PostgreSQL)")
	}
	if cnpgReservedDatabaseNames[c.Spec.Name] {
		return errors.Errorf("name %q: reserved by PostgreSQL, the Database CRD refuses it", c.Spec.Name)
	}
	if c.Spec.Owner == "" {
		return errors.New("owner: required (the role that owns the database)")
	}
	return nil
}

// ApplyPolicy is a no-op: a Database runs no pod, image or storage of its own,
// so no environment policy dimension applies to it.
func (c *CnpgDatabaseConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the Database: kure's identity-only constructor plus a deep
// copy of the spec, with the CRD default written into each always-encoded
// field left empty (cnpgDatabaseAlwaysEncodedDefaults). The parse-time
// refusals are repeated, since the config is exported.
func (c *CnpgDatabaseConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	database := kurecnpg.CreateDatabase(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&database.Spec)
	fillAlwaysEncodedDefaults(&database.Spec)
	return kindObject(database, c.Metadata)
}
