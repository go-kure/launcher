package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgSubscriptionHandler handles OAM cnpg-subscription components: the
// kind-named projection of a CloudNativePG postgresql.cnpg.io/v1 Subscription
// (go-kure/launcher#790), one logical replication subscription managed
// declaratively in a database of an existing Cluster, the subscriber.
//
// Its properties are exactly the top-level fields of cnpgv1.SubscriptionSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Subscription, named after the component unless `objectName` names it, in
// the build namespace, and nothing else. The publisher is an entry of the
// subscriber Cluster's `externalClusters`, named by `externalClusterName`:
// that the Cluster holds such an entry is not checked.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CnpgSubscriptionHandler struct{}

// CanHandle returns true for the cnpg-subscription component type.
func (h *CnpgSubscriptionHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-subscription"
}

// PropertySchema declares every top-level cnpgv1.SubscriptionSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *CnpgSubscriptionHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Subscription spec."
	return map[string]oam.PropertySchema{
		"cluster": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "cluster: the CloudNativePG Cluster that subscribes: {name: <cluster>}, a Cluster of the same namespace. The API refuses a change once the object exists.",
		},
		"name": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "name: the name of the subscription inside PostgreSQL. The API refuses a change once the object exists.",
		},
		"dbname": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "dbname: the database of the Cluster the subscription is created in. The API refuses a change once the object exists.",
		},
		"parameters": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "parameters: the parameters of the WITH clause of PostgreSQL's CREATE SUBSCRIPTION, a map of strings written as authored and not checked.",
		},
		"publicationName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "publicationName: the name of the publication inside the publisher's database.",
		},
		"publicationDBName": {
			Type:        oam.PropertyTypeString,
			Description: spec + "publicationDBName: the database of the publisher that holds the publication. Unset, the one in the external cluster's definition applies.",
		},
		"externalClusterName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "externalClusterName: the name of the external cluster that publishes, as the subscriber Cluster's externalClusters defines it.",
		},
		"subscriptionReclaimPolicy": {
			Type:        oam.PropertyTypeString,
			Description: spec + "subscriptionReclaimPolicy: what happens to the subscription when the Subscription object is deleted: retain or delete. Unset, the API's default applies: retain.",
		},
	}
}

// cnpgSubscriptionDefaultedZeroFields lists the SubscriptionSpec fields on
// which an authored 0, false or "" would be silently replaced, keyed and
// valued as cnpgClusterDefaultedZeroFields is.
// TestCnpgKindsDefaultedZeroFields_MatchCRD derives it from the linked
// CloudNativePG module.
var cnpgSubscriptionDefaultedZeroFields = map[string]string{
	"subscriptionReclaimPolicy": `"retain"`,
}

// cnpgSubscriptionKind is the cnpg-subscription kind: see policyFreeKind. A
// Subscription runs no pod and names no image; the publisher's address and
// credentials are the external cluster's, in the Cluster. validate holds the
// Cluster reference to a name a Cluster can have. The CRD's three expression
// rules compare a field with its value on the stored object and say nothing
// of a new one; they and the API's value rules are left to the API server.
var cnpgSubscriptionKind = &policyFreeKind[cnpgv1.SubscriptionSpec]{
	upstream: "postgresql.cnpg.io/v1 SubscriptionSpec",
	required: requiredFields(map[string]string{
		"cluster":             "the CloudNativePG Cluster that subscribes",
		"name":                "the name of the subscription inside PostgreSQL",
		"dbname":              "the database the subscription is created in",
		"publicationName":     "the name of the publication inside the publisher's database",
		"externalClusterName": "the name of the external cluster that publishes",
	}),
	defaultedZeros: cnpgDefaultedZeros(cnpgSubscriptionDefaultedZeroFields),
	validate: func(spec *cnpgv1.SubscriptionSpec) error {
		return requireCnpgClusterRef(spec.ClusterRef.Name)
	},
	build: func(name, namespace string, spec *cnpgv1.SubscriptionSpec) client.Object {
		subscription := kurecnpg.CreateSubscription(name, namespace)
		spec.DeepCopyInto(&subscription.Spec)
		return subscription
	},
}

// ToApplicationConfig decodes an OAM cnpg-subscription component into its
// config. The object takes the namespace of the application it is generated in.
func (h *CnpgSubscriptionHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgSubscriptionKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgSubscriptionHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-subscription")
}

// ComponentObject declares the cnpg-subscription kind's Subscription.
func (h *CnpgSubscriptionHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.SubscriptionKind), oam.ObjectScopeNamespaced
}
