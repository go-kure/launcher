package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgPublicationHandler handles OAM cnpg-publication components: the
// kind-named projection of a CloudNativePG postgresql.cnpg.io/v1 Publication
// (go-kure/launcher#790), one logical replication publication managed
// declaratively in a database of an existing Cluster, the publisher.
//
// Its properties are exactly the top-level fields of cnpgv1.PublicationSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Publication, named after the component unless `objectName` names it, in the
// build namespace, and nothing else. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type CnpgPublicationHandler struct{}

// CanHandle returns true for the cnpg-publication component type.
func (h *CnpgPublicationHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-publication"
}

// PropertySchema declares every top-level cnpgv1.PublicationSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *CnpgPublicationHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Publication spec."
	return map[string]oam.PropertySchema{
		"cluster": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "cluster: the CloudNativePG Cluster that publishes: {name: <cluster>}, a Cluster of the same namespace. The API refuses a change once the object exists.",
		},
		"name": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "name: the name of the publication inside PostgreSQL. The API refuses a change once the object exists.",
		},
		"dbname": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "dbname: the database of the Cluster the publication is created in. The API refuses a change once the object exists.",
		},
		"parameters": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "parameters: the parameters of the WITH clause of PostgreSQL's CREATE PUBLICATION, a map of strings written as authored and not checked.",
		},
		"target": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "target: what is published, by exactly one of allTables (true: every table of the database, FOR ALL TABLES; once the object exists, the API refuses a change from one authored value to another, not a move to objects or back) and objects (a list, each entry exactly one of tablesInSchema, the name of a schema, and table: name, required, and schema, only and columns). A table's columns cannot be listed where another entry publishes tablesInSchema. Decoded strictly into the CloudNativePG API type: see PublicationTarget in its API reference.",
		},
		"publicationReclaimPolicy": {
			Type:        oam.PropertyTypeString,
			Description: spec + "publicationReclaimPolicy: what happens to the publication when the Publication object is deleted: retain or delete. Unset, the API's default applies: retain.",
		},
	}
}

// cnpgPublicationDefaultedZeroFields lists the PublicationSpec fields on which
// an authored 0, false or "" would be silently replaced, keyed and valued as
// cnpgClusterDefaultedZeroFields is. TestCnpgKindsDefaultedZeroFields_MatchCRD
// derives it from the linked CloudNativePG module.
var cnpgPublicationDefaultedZeroFields = map[string]string{
	"publicationReclaimPolicy": `"retain"`,
}

// cnpgPublicationKind is the cnpg-publication kind: see policyFreeKind. A
// Publication runs no pod and names no image. validate holds the Cluster
// reference and the three expression rules of the CRD that read one document
// (validateCnpgPublication). Its four others compare a field with its value on
// the stored object and say nothing of a new one; they and the API's value
// rules are left to the API server.
var cnpgPublicationKind = &policyFreeKind[cnpgv1.PublicationSpec]{
	upstream: "postgresql.cnpg.io/v1 PublicationSpec",
	required: requiredFields(map[string]string{
		"cluster":                     "the CloudNativePG Cluster that publishes",
		"name":                        "the name of the publication inside PostgreSQL",
		"dbname":                      "the database the publication is created in",
		"target":                      "what is published",
		"target.objects[].table.name": "the name of the table",
	}),
	defaultedZeros: cnpgDefaultedZeros(cnpgPublicationDefaultedZeroFields),
	validate:       validateCnpgPublication,
	build: func(name, namespace string, spec *cnpgv1.PublicationSpec) client.Object {
		publication := kurecnpg.CreatePublication(name, namespace)
		spec.DeepCopyInto(&publication.Spec)
		return publication
	},
}

// validateCnpgPublication refuses a target the Publication CRD refuses on
// creation. The CRD reads which fields the object holds, and the Go type
// leaves out a false `allTables`, an empty `objects`, an empty
// `tablesInSchema` and an empty `columns`, so each is read here as the object
// will hold it: a target publishes all tables or a list of objects and not
// both, an object is a schema's tables or one table and not both, and no table
// lists its columns where another object publishes a schema's tables.
func validateCnpgPublication(spec *cnpgv1.PublicationSpec) error {
	if err := requireCnpgClusterRef(spec.ClusterRef.Name); err != nil {
		return err
	}
	target := spec.Target
	switch {
	case !target.AllTables && len(target.Objects) == 0:
		return errors.New("target: one of allTables: true and objects is required")
	case target.AllTables && len(target.Objects) > 0:
		return errors.New("target: allTables and objects are both set; the API takes exactly one")
	}
	columns, schema := -1, -1
	for i, object := range target.Objects {
		switch {
		case object.TablesInSchema == "" && object.Table == nil:
			return errors.Errorf("target.objects[%d]: one of tablesInSchema and table is required", i)
		case object.TablesInSchema != "" && object.Table != nil:
			return errors.Errorf("target.objects[%d]: tablesInSchema and table are both set; the API takes exactly one", i)
		case object.Table != nil && len(object.Table.Columns) > 0 && columns < 0:
			columns = i
		case object.TablesInSchema != "" && schema < 0:
			schema = i
		}
	}
	if columns >= 0 && schema >= 0 {
		return errors.Errorf("target.objects[%d].table.columns: a column list is not supported in a publication that also publishes tablesInSchema (target.objects[%d])", columns, schema)
	}
	return nil
}

// ToApplicationConfig decodes an OAM cnpg-publication component into its
// config. The object takes the namespace of the application it is generated in.
func (h *CnpgPublicationHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgPublicationKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgPublicationHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-publication")
}

// ComponentObject declares the cnpg-publication kind's Publication.
func (h *CnpgPublicationHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.PublicationKind), oam.ObjectScopeNamespaced
}
