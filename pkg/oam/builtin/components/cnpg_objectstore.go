package components

import (
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgObjectStoreHandler handles OAM cnpg-objectstore components: the
// kind-named, full-fidelity projection of a Barman Cloud plugin
// barmancloud.cnpg.io/v1 ObjectStore, the backup and WAL archive destination a
// CloudNativePG Cluster references through the plugin.
//
// Its properties are exactly the top-level fields of barmanv1.ObjectStoreSpec,
// under their json names, decoded strictly as cnpg-cluster's are, and the
// handler adds nothing to what was authored: unlike postgresql's object store
// it derives no credential key names of its own.
// TestCnpgObjectStoreSchema_CoversObjectStoreSpec keeps the published key set
// equal to the upstream json tags.
type CnpgObjectStoreHandler struct{}

// CanHandle returns true for the cnpg-objectstore component type.
func (h *CnpgObjectStoreHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-objectstore"
}

// PropertySchema declares every top-level barmanv1.ObjectStoreSpec field by
// its json name. Scalars carry their type; structured fields are open objects
// whose content is checked by the strict decode, not by this schema.
func (h *CnpgObjectStoreHandler) PropertySchema() map[string]oam.PropertySchema {
	const ref = " Decoded strictly into the Barman Cloud plugin API type: see the ObjectStoreSpec reference in the plugin's documentation for its fields."
	return map[string]oam.PropertySchema{
		"configuration":                {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Required. The object store: destinationPath (required), endpointURL, the s3Credentials, azureCredentials or googleCredentials, and the wal and data backup settings." + ref},
		"retentionPolicy":              {Type: oam.PropertyTypeString, Description: "How long backups are kept, as <n><d|w|m> (for example 30d)."},
		"instanceSidecarConfiguration": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Configuration of the plugin sidecar in the Cluster's instance pods: env, resources, retentionPolicyIntervalSeconds, additionalContainerArgs, logLevel. A policy cpu/memory maximum caps its resources." + ref},
	}
}

// cnpgObjectStoreDefaultedZeroFields lists the ObjectStoreSpec fields on which
// an authored 0 or false would be silently replaced, keyed and valued as
// cnpgClusterDefaultedZeroFields is. TestCnpgKindsDefaultedZeroFields_MatchCRD
// derives it from the linked Barman Cloud plugin module.
var cnpgObjectStoreDefaultedZeroFields = map[string]string{
	"instanceSidecarConfiguration.retentionPolicyIntervalSeconds": "1800",
}

// ToApplicationConfig decodes an OAM cnpg-objectstore component into a
// CnpgObjectStoreConfig, under the same null contract and strict decode as
// cnpg-cluster. configuration.destinationPath, which the ObjectStore CRD
// requires, must be authored and non-empty: the Go type would otherwise encode
// it as an empty string.
func (h *CnpgObjectStoreHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[barmanv1.ObjectStoreSpec](component.Properties, "barmancloud.cnpg.io/v1 ObjectStoreSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, cnpgDefaultedZeros(cnpgObjectStoreDefaultedZeroFields)); err != nil {
		return nil, err
	}
	cfg := &CnpgObjectStoreConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CnpgObjectStoreConfig implements stack.ApplicationConfig for
// cnpg-objectstore components. Spec is the decoded ObjectStoreSpec exactly as
// authored.
type CnpgObjectStoreConfig struct {
	Name      string
	Namespace string
	Spec      barmanv1.ObjectStoreSpec
}

// validate refuses a spec the ObjectStore CRD would refuse for a reason the
// strict decode cannot see: no destination path, or a server name, which the
// shared Barman type carries but the plugin's CRD forbids on an ObjectStore.
func (c *CnpgObjectStoreConfig) validate() error {
	if c.Spec.Configuration.DestinationPath == "" {
		return errors.New("configuration.destinationPath: required (the object store path backups and WAL are written to)")
	}
	if c.Spec.Configuration.ServerName != "" {
		return errors.New("configuration.serverName: not allowed on an ObjectStore (set the serverName plugin parameter in the Cluster that uses it)")
	}
	return nil
}

// ApplyPolicy caps the cpu and memory requests and limits of the plugin
// sidecar, which runs in every instance pod of a Cluster that uses this store,
// at the policy maxima, as cnpg-cluster caps the instance containers. It fills
// no default.
func (c *CnpgObjectStoreConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	if err := enforceMaxContainerResources(c.Spec.InstanceSidecarConfiguration.Resources, p); err != nil {
		return errors.Wrap(err, "instanceSidecarConfiguration.resources")
	}
	return nil
}

// Generate emits the ObjectStore: kure's identity-only constructor plus a deep
// copy of the spec. The parse-time refusals are repeated, since the config is
// exported, and the sidecar's resources get admission's request/limit and
// hugepages checks (validateCnpgResources): the plugin copies them onto a
// container in every instance pod.
func (c *CnpgObjectStoreConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if err := validateCnpgResources("instanceSidecarConfiguration", c.Spec.InstanceSidecarConfiguration.Resources); err != nil {
		return nil, err
	}
	store := kurecnpg.CreateObjectStore(app.Name, app.Namespace)
	c.Spec.DeepCopyInto(&store.Spec)
	obj := client.Object(store)
	return []*client.Object{&obj}, nil
}
