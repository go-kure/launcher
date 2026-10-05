package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// validateCnpgPoolerName refuses a component name CloudNativePG cannot use as
// the Pooler's name: the operator creates a Service with the Pooler's own
// name, so it must be a DNS-1035 label (at most 63 characters, no leading
// digit, no dot), and the name is also copied into the cnpg.io/poolerName
// endpoint selector, a label value with the same 63-character limit.
func validateCnpgPoolerName(name string) error {
	if len(validation.IsDNS1035Label(name)) > 0 {
		return errors.Errorf("cnpg-pooler name %q: must be a DNS-1035 label of at most %d characters (CloudNativePG names the pooler's Service after it)", name, validation.DNS1035LabelMaxLength)
	}
	return nil
}

// refusePoolerNamedAsCluster refuses a Pooler whose cluster reference is the
// Pooler's own name. It is the one text of that refusal: for the object
// (CnpgPoolerConfig.validate) and wherever a pooler's endpoint is answered
// (CnpgPoolerHandler.Endpoints, PostgresqlRule.EndpointsNamed), so no caller
// is handed a selector for a pooler the build refuses.
func refusePoolerNamedAsCluster(pooler, cluster string) error {
	if cluster == pooler {
		return errors.Errorf("cluster.name %q: a pooler cannot have the same name as its cluster", pooler)
	}
	return nil
}

// CnpgPoolerHandler handles OAM cnpg-pooler components: the kind-named,
// full-fidelity projection of a CloudNativePG postgresql.cnpg.io/v1 Pooler.
//
// Its properties are exactly the top-level fields of cnpgv1.PoolerSpec, under
// their json names, and the handler adds nothing to what was authored: unlike
// postgresql's pooler it writes no type, instance count or pool mode of its
// own, so the operator's defaults apply to whatever is omitted. Deep blocks are
// open objects decoded strictly into the typed structs (decodeKindSpec), as in
// cnpg-cluster. TestCnpgPoolerSchema_CoversPoolerSpec keeps the published key
// set equal to the upstream json tags.
type CnpgPoolerHandler struct{}

// CanHandle returns true for the cnpg-pooler component type.
func (h *CnpgPoolerHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-pooler"
}

// Endpoints implements oam.EndpointProvider: the Pooler's PgBouncer pods,
// labelled cnpg.io/poolerName=<pooler name> (the component's object name: the
// OAM component name unless `objectName` or the naming hook names the Pooler
// otherwise), on the PostgreSQL port. It is the pooler endpoint postgresql publishes, which names
// its Pooler <component name>-pooler unless the author or the consumer's naming
// hook names it otherwise: a cnpg-pooler component of that name
// declares an identical selector, so a synthesized ingress allow does not
// change when a pooler moves from one to the other. A pooler whose authored
// `cluster.name` is its own name has no endpoint: it is refused here as the
// build refuses it (refusePoolerNamedAsCluster).
func (h *CnpgPoolerHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	name := component.ObjectName()
	if err := validateCnpgPoolerName(name); err != nil {
		return nil, err
	}
	// The one relation between two names the build refuses (validate): a
	// cluster reference that is the Pooler's own name. Only an authored string
	// is compared; whatever else `cluster` holds is the decode's to refuse.
	// The two keys are matched as the decode matches them, in any case
	// (foldedFieldMaps), so `Cluster: {Name: …}` is read as it is built.
	for _, cluster := range foldedFieldMaps(component.Properties, "cluster") {
		for _, key := range foldedFieldKeys(cluster, "name") {
			clusterName, ok := cluster[key].(string)
			if !ok {
				continue
			}
			if err := refusePoolerNamedAsCluster(name, clusterName); err != nil {
				return nil, err
			}
		}
	}
	return []netpol.Endpoint{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgPoolerNameLabel: name}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(postgresqlPort)},
	}}, nil
}

// PropertySchema declares every top-level cnpgv1.PoolerSpec field by its json
// name. Scalars carry their type; structured fields are open objects whose
// content is checked by the strict decode, not by this schema.
func (h *CnpgPoolerHandler) PropertySchema() map[string]oam.PropertySchema {
	const ref = " Decoded strictly into the CloudNativePG API type: see the PoolerSpec reference in the CloudNativePG documentation for its fields."
	obj := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc + ref}
	}
	return map[string]oam.PropertySchema{
		"cluster":            obj("Required. Reference to the CloudNativePG Cluster the pooler serves: {name: <cluster>}. The name must differ from this pooler's own."),
		"type":               {Type: oam.PropertyTypeString, Description: "Service the pooler forwards to: rw, ro or r. The operator's default (rw) applies when omitted."},
		"instances":          {Type: oam.PropertyTypeInteger, Description: "Number of PgBouncer pods. The operator's default (1) applies when omitted. Not subject to the environment's replica policy."},
		"template":           obj("Pod template for the PgBouncer pods. Environment policy applies to it: host namespaces, hostPath volumes, privilege, capabilities, image registries and cpu/memory maxima."),
		"pgbouncer":          obj("Required. PgBouncer configuration: poolMode, parameters, pg_hba, authentication secrets, image. An empty object selects PgBouncer's defaults. A policy registry allowlist applies to an authored image."),
		"deploymentStrategy": obj("Deployment strategy used to replace the PgBouncer pods."),
		"monitoring":         obj("Monitoring configuration of the pooler."),
		"serviceTemplate":    obj("Template for the Service created for the pooler."),
		"serviceAccountName": {Type: oam.PropertyTypeString, Description: "Existing ServiceAccount for the pooler pods instead of a generated one."},
	}
}

// cnpgPoolerDefaultedZeroFields lists the PoolerSpec fields on which an
// authored 0 or false would be silently replaced, keyed and valued as
// cnpgClusterDefaultedZeroFields is. TestCnpgKindsDefaultedZeroFields_MatchCRD
// derives it from the linked CloudNativePG module.
var cnpgPoolerDefaultedZeroFields = map[string]string{}

// ToApplicationConfig decodes an OAM cnpg-pooler component into a
// CnpgPoolerConfig, under the same null contract and strict decode as
// cnpg-cluster. The fields the Pooler CRD requires must be authored:
// cluster.name, and pgbouncer, which the Go type would otherwise encode as
// null.
func (h *CnpgPoolerHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	name := component.ObjectName()
	if err := validateCnpgPoolerName(name); err != nil {
		return nil, err
	}
	spec, props, err := decodeKindSpec[cnpgv1.PoolerSpec](component.Properties, "postgresql.cnpg.io/v1 PoolerSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, cnpgDefaultedZeros(cnpgPoolerDefaultedZeroFields)); err != nil {
		return nil, err
	}
	cfg := &CnpgPoolerConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if err := cfg.validate(name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CnpgPoolerConfig implements stack.ApplicationConfig for cnpg-pooler
// components. Spec is the decoded PoolerSpec exactly as authored.
type CnpgPoolerConfig struct {
	Name string
	// ObjectName names the Pooler (oam.Component.ObjectName). Empty for the
	// application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Pooler
	// (oam.Component.ObjectMetadata). They go on the Pooler's own metadata:
	// its pod template is the authored spec.template.
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      cnpgv1.PoolerSpec
}

// validate refuses a spec the Pooler CRD or CloudNativePG's webhook would
// refuse for a reason the strict decode cannot see: no cluster reference, a
// cluster named like the pooler itself, or no pgbouncer block, and a template
// declaring a pod field the workload kinds refuse: ephemeral containers,
// activeDeadlineSeconds, priority or overhead. Of the template's pod spec it
// also refuses a field the CRD requires that the Go type leaves out when it is
// empty (refuseOmittedPodSpecFields) or writes as null when it is unauthored
// (refuseNullPodSpecFields). The images the Pooler names are held to the tag
// rule (validateImageRefs).
func (c *CnpgPoolerConfig) validate(name string) error {
	if err := requireCnpgClusterRef(c.Spec.Cluster.Name); err != nil {
		return err
	}
	if err := refusePoolerNamedAsCluster(name, c.Spec.Cluster.Name); err != nil {
		return err
	}
	if c.Spec.PgBouncer == nil {
		return errors.New("pgbouncer: required (an empty object selects PgBouncer's defaults)")
	}
	if err := c.validateImageRefs(); err != nil {
		return err
	}
	// As the workload kinds refuse them (podSpecRejectedKeys,
	// podSpecJobOnlyKeys): the operator copies the template into its
	// Deployment, whose pod template admission refuses ephemeral containers and
	// activeDeadlineSeconds, and whose pods get priority and overhead from the
	// default Priority and RuntimeClass admission controllers, which refuse a
	// pod whose authored value differs from the one they derive.
	if t := c.Spec.Template; t != nil {
		ps := &t.Spec
		switch {
		case len(ps.EphemeralContainers) > 0:
			return errors.New("template.spec." + podSpecRejectedKeys["ephemeralContainers"])
		case ps.ActiveDeadlineSeconds != nil:
			return errors.New("template.spec.activeDeadlineSeconds: " + podSpecJobOnlyReason)
		case ps.Priority != nil:
			return errors.New("template.spec." + podSpecRejectedKeys["priority"])
		case len(ps.Overhead) > 0:
			return errors.New("template.spec." + podSpecRejectedKeys["overhead"])
		}
		// The Pooler CRD publishes the pod spec's schema, and with it the
		// fields that schema requires and the Go type leaves out when empty.
		if err := refuseOmittedPodSpecFields("template.spec", ps); err != nil {
			return err
		}
		// And the fields that schema requires and the Go type writes as null.
		if err := refuseNullPodSpecFields("template.spec", ps); err != nil {
			return err
		}
	}
	return nil
}

// ApplyPolicy enforces environment policy on what the Pooler runs: the pod
// template, as the workload kinds police their pod (enforcePodTemplatePolicy),
// and the registry allowlist on an authored pgbouncer.image. It fills no
// default.
//
// The instance count is deliberately not policed, neither a replica default
// nor a maximum: postgresql applies no policy to its pooler, so a maximum here
// would refuse, once postgresql is lowered onto this kind, a document that
// builds today.
func (c *CnpgPoolerConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	if pgb := c.Spec.PgBouncer; pgb != nil && pgb.Image != "" {
		if err := enforceAllowedRegistries(pgb.Image, p.AllowedRegistries()); err != nil {
			return errors.Wrap(err, "pgbouncer.image")
		}
	}
	if t := c.Spec.Template; t != nil {
		if err := enforcePodTemplatePolicy("template.spec", &t.Spec, p); err != nil {
			return err
		}
	}
	return nil
}

// validateImageRefs holds the images the Pooler names to ValidateImageRef, with
// or without an environment policy: pgbouncer.image, and in the template the
// image of each init and regular container and the reference of each image
// volume. No untagged image and no :latest, as for a workload's container.
//
// A field that names no image is not checked, as the registry allowlist does
// not check it (ApplyPolicy): the operator supplies the PgBouncer image, so a
// template container without one is the ordinary form here, where the pod
// kinds refuse it (validateAuthoredPodSpec).
func (c *CnpgPoolerConfig) validateImageRefs() error {
	if pgb := c.Spec.PgBouncer; pgb != nil && pgb.Image != "" {
		if err := ValidateImageRef(pgb.Image); err != nil {
			return errors.Wrap(err, "pgbouncer.image")
		}
	}
	t := c.Spec.Template
	if t == nil {
		return nil
	}
	for i, ctr := range t.Spec.InitContainers {
		if ctr.Image == "" {
			continue
		}
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "template.spec.initContainers[%d] %q", i, ctr.Name)
		}
	}
	for i, ctr := range t.Spec.Containers {
		if ctr.Image == "" {
			continue
		}
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "template.spec.containers[%d] %q", i, ctr.Name)
		}
	}
	return validateImageVolumeRefs("template.spec.", &t.Spec)
}

// Generate emits the Pooler: kure's identity-only constructor plus a deep copy
// of the spec. The parse-time refusals are repeated on what is emitted, since
// the config is exported and the Pooler is named by ObjectName, else from
// app.Name. The template's
// pod and container resources get admission's request/limit and hugepages
// checks (validatePodTemplateResources), as cnpg-cluster's do.
//
// A template that lists no containers is written with containers: []. The Go
// type cannot omit the template's spec and encodes an unset list as null,
// which the API server prunes before checking the CRD's required containers,
// so a metadata-only template would be refused. To the operator an empty list
// means what an omitted spec does: it adds its pgbouncer container either way.
func (c *CnpgPoolerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.ObjectName, app.Name)
	if err := validateCnpgPoolerName(name); err != nil {
		return nil, err
	}
	if err := c.validate(name); err != nil {
		return nil, err
	}
	if t := c.Spec.Template; t != nil {
		if err := validatePodTemplateResources("template.spec", &t.Spec); err != nil {
			return nil, err
		}
	}
	pooler := kurecnpg.CreatePooler(name, app.Namespace)
	c.Spec.DeepCopyInto(&pooler.Spec)
	if t := pooler.Spec.Template; t != nil && t.Spec.Containers == nil {
		t.Spec.Containers = []corev1.Container{}
	}
	return kindObject(pooler, c.Metadata)
}
