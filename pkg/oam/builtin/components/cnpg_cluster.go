package components

import (
	"fmt"
	"maps"
	"math"
	"slices"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// cnpgClusterDefaultInstances is the instance count written when neither the
// document nor a policy default supplies one. It is the CRD's own default
// (`+kubebuilder:default:=1` on ClusterSpec.Instances), not a launcher opinion:
// Instances is tagged `json:"instances"` without omitempty, so an unset value
// would otherwise render as `instances: 0`, which the CRD's Minimum=1 refuses.
// It is also postgresql's `replicas` default, which keeps the two kinds' policy
// behaviour identical.
const cnpgClusterDefaultInstances = 1

// CnpgClusterHandler handles OAM cnpg-cluster components: the kind-named,
// full-fidelity projection of a CloudNativePG postgresql.cnpg.io/v1 Cluster.
//
// Its properties are exactly the top-level fields of cnpgv1.ClusterSpec, under
// their json names, and the handler adds nothing to what was authored: no
// enablePDB, no primaryUpdateStrategy, no image derived from a version, no
// storage-size fallback. The one value it writes unasked is `instances: 1`
// when nothing else sets it (cnpgClusterDefaultInstances). A launcher opinion
// belongs in a trait applied after policy, and a semantic component such as
// postgresql belongs in a lowering rule onto this kind
// (docs/oam/design-operator-cr-components.md).
//
// Deep blocks are published as open objects and decoded strictly into the
// typed cnpgv1 structs (builtin.DecodeStrictJSON), so a misspelt key at any
// depth or a wrongly typed value is refused rather than dropped.
// TestCnpgClusterSchema_CoversClusterSpec keeps the published key set equal to
// the upstream json tags.
type CnpgClusterHandler struct{}

// CanHandle returns true for the cnpg-cluster component type.
func (h *CnpgClusterHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-cluster"
}

// Endpoints implements oam.EndpointProvider: the Cluster's instance pods,
// labelled cnpg.io/cluster=<cluster name> (the OAM component name), on the
// PostgreSQL port. It is the same primary endpoint postgresql publishes; the
// pooler endpoint belongs to a Pooler, which this kind does not emit.
func (h *CnpgClusterHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	return []netpol.Endpoint{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgClusterLabel: component.Name}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(postgresqlPort)},
	}}, nil
}

// PropertySchema declares every top-level cnpgv1.ClusterSpec field by its json
// name. Scalars carry their type; structured fields are open objects or arrays
// whose content is checked by the strict decode, not by this schema.
func (h *CnpgClusterHandler) PropertySchema() map[string]oam.PropertySchema {
	const ref = " Decoded strictly into the CloudNativePG API type: see the ClusterSpec reference in the CloudNativePG documentation for its fields."
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	integer := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
	}
	obj := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc + ref}
	}
	arr := func(desc, itemDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type:        oam.PropertyTypeArray,
			Description: desc + ref,
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: itemDesc},
		}
	}
	return map[string]oam.PropertySchema{
		"description":               str("Description of this PostgreSQL cluster."),
		"inheritedMetadata":         obj("Labels and annotations inherited by every object related to the Cluster."),
		"imageName":                 str("Container image for the instances, by tag or digest. The operator's own default applies when omitted."),
		"imageCatalogRef":           obj("Reference to an ImageCatalog or ClusterImageCatalog entry selecting the image by PostgreSQL major version."),
		"imagePullPolicy":           str("Image pull policy: Always, Never or IfNotPresent."),
		"schedulerName":             str("Kubernetes scheduler that places the instance pods."),
		"postgresUID":               integer("UID of the postgres user inside the image."),
		"postgresGID":               integer("GID of the postgres user inside the image."),
		"instances":                 {Type: oam.PropertyTypeInteger, Default: cnpgClusterDefaultInstances, Description: "Number of PostgreSQL instances. When omitted, a policy default applies, else 1 (the CRD's own default); a policy maximum caps it."},
		"minSyncReplicas":           integer("Minimum number of instances required in synchronous replication with the primary."),
		"maxSyncReplicas":           integer("Target number of synchronous replicas in the quorum."),
		"postgresql":                obj("PostgreSQL server configuration: parameters, pg_hba, pg_ident, shared_preload_libraries, synchronous replication and related settings."),
		"podSelectorRefs":           arr("Named pod label selectors that pg_hba rules can reference.", "A single named pod selector reference."),
		"replicationSlots":          obj("Replication slot management for high availability and user slots."),
		"bootstrap":                 obj("How the cluster is created: initdb, recovery or pg_basebackup."),
		"replica":                   obj("Replica cluster configuration."),
		"superuserSecret":           obj("Secret holding the superuser password."),
		"enableSuperuserAccess":     boolean("Whether the operator manages the postgres superuser password from superuserSecret."),
		"certificates":              obj("CA and certificate configuration for server and client TLS."),
		"imagePullSecrets":          arr("Pull secrets used for the instance images.", "A single pull secret reference."),
		"storage":                   obj("Storage for the PGDATA volume (size, storageClass, pvcTemplate). A policy storage default fills size when neither size nor pvcTemplate.resources.requests.storage is authored; a policy maximum caps the effective request."),
		"serviceAccountTemplate":    obj("Template for the ServiceAccount the operator generates."),
		"serviceAccountName":        str("Existing ServiceAccount to use instead of a generated one."),
		"walStorage":                obj("Separate storage for the WAL volume. A policy storage maximum caps its effective request."),
		"ephemeralVolumeSource":     obj("Source of the ephemeral volume used for temporary data. A policy storage maximum caps a volumeClaimTemplate's storage request."),
		"startDelay":                integer("Seconds an instance is allowed to start up."),
		"stopDelay":                 integer("Seconds an instance is allowed to shut down gracefully."),
		"smartShutdownTimeout":      integer("Seconds reserved for a smart shutdown to complete."),
		"switchoverDelay":           integer("Seconds a primary is allowed to shut down during a switchover."),
		"failoverDelay":             integer("Seconds to wait before triggering a failover after the primary is detected unhealthy."),
		"livenessProbeTimeout":      integer("Seconds an instance is allowed to respond to the liveness probe."),
		"affinity":                  obj("CloudNativePG affinity configuration: pod anti-affinity, node selector, tolerations and raw affinity terms."),
		"topologySpreadConstraints": arr("Topology spread constraints for the instance pods.", "A single corev1 topology spread constraint."),
		"resources":                 obj("Resource requests and limits for every instance pod. Policy cpu/memory defaults fill unset entries and policy maxima cap them."),
		"ephemeralVolumesSizeLimit": obj("Size limits for the shm and temporary-data ephemeral volumes."),
		"priorityClassName":         str("PriorityClass for the instance pods."),
		"primaryUpdateStrategy":     str("How the primary is updated after the replicas: unsupervised or supervised."),
		"primaryUpdateMethod":       str("How the primary is updated: switchover or restart."),
		"backup":                    obj("Backup configuration."),
		"nodeMaintenanceWindow":     obj("Maintenance window for the Kubernetes nodes."),
		"monitoring":                obj("Monitoring configuration."),
		"externalClusters":          arr("External clusters referenced for bootstrap or replica sources.", "A single external cluster definition."),
		"logLevel":                  str("Instance log level: error, warning, info, debug or trace."),
		"projectedVolumeTemplate":   obj("Projected volume mounted under /projected in every instance pod."),
		"env":                       arr("Environment variables for the instance pods.", "A single corev1 environment variable."),
		"envFrom":                   arr("Environment variable sources for the instance pods.", "A single corev1 environment variable source."),
		"managed":                   obj("PostgreSQL objects managed by the instance manager: roles and services."),
		"seccompProfile":            obj("Seccomp profile applied to every pod and container."),
		"podSecurityContext":        obj("Pod security context overriding the operator's default. windowsOptions.hostProcess is refused unless policy allows privileged workloads."),
		"securityContext":           obj("Container security context overriding the operator's default. privileged, windowsOptions.hostProcess and capabilities.add are checked against policy."),
		"tablespaces":               arr("Tablespace definitions. A policy storage maximum caps each tablespace's effective storage request.", "A single tablespace definition."),
		"enablePDB":                 boolean("Whether the operator manages PodDisruptionBudgets. No default is written; the operator's own default applies when omitted."),
		"plugins":                   arr("Plugins loaded by the cluster.", "A single plugin configuration."),
		"probes":                    obj("Configuration of the probes injected into the instance pods."),
		"primaryLease":              obj("Timings of the Lease used for primary election."),
	}
}

// ToApplicationConfig decodes an OAM cnpg-cluster component into a
// CnpgClusterConfig.
//
// A null is absence at every depth, per the package's null contract (see
// withoutNullsAtDepth): a null key is dropped before decoding, and a null array
// element is refused by path. Everything else is decoded strictly into
// cnpgv1.ClusterSpec, so an unknown key or a wrongly typed value is an error.
func (h *CnpgClusterHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	normalized, err := withoutNullsAtDepth(component.Properties, "")
	if err != nil {
		return nil, err
	}
	props, _ := normalized.(map[string]any)

	// Read ahead of the decode, with the helper every kind uses for replicas, so
	// a non-integer or out-of-range value is refused by name and a negative one
	// gets the same refusal postgresql's replicas does.
	_, instancesAuthored, err := parseInt32Field(props, "instances", "instances")
	if err != nil {
		return nil, err
	}

	spec, _, err := builtin.DecodeStrictJSON[cnpgv1.ClusterSpec](props)
	if err != nil {
		return nil, errors.Wrap(err, "properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec")
	}
	// encoding/json matches field names case-insensitively, so a key spelt
	// differently from the published one ("Instances", "storage.Size") still
	// sets the field. What was authored is therefore also read back from the
	// decoded spec, or a policy default would overwrite a value the Cluster
	// was about to carry. The map reading stays for the one value the decoded
	// spec cannot distinguish from absence: an explicit storage size of "".
	if spec.Instances < 0 {
		return nil, errors.Errorf("instances: must be >= 0, got %d", spec.Instances)
	}
	explicitInstances := instancesAuthored || spec.Instances != 0
	if !explicitInstances {
		spec.Instances = cnpgClusterDefaultInstances
	}

	return &CnpgClusterConfig{
		Name:                component.Name,
		Namespace:           namespace,
		Spec:                *spec,
		explicitInstances:   explicitInstances,
		explicitStorageSize: authoredStorageRequest(props) || decodedStorageRequest(&spec.StorageConfiguration),
	}, nil
}

// decodedStorageRequest reports whether the decoded PGDATA storage block
// carries a size in either spelling authoredStorageRequest reads.
func decodedStorageRequest(sc *cnpgv1.StorageConfiguration) bool {
	if sc.Size != "" {
		return true
	}
	if sc.PersistentVolumeClaimTemplate == nil {
		return false
	}
	_, ok := sc.PersistentVolumeClaimTemplate.Resources.Requests[corev1.ResourceStorage]
	return ok
}

// authoredStorageRequest reports whether the document itself chose the PGDATA
// volume's size, in either spelling CNPG reads: storage.size, or
// storage.pvcTemplate.resources.requests.storage. It is postgresql's
// explicitStorageSize mapped onto the Cluster's shape. The second spelling
// counts because CNPG writes a set size over the template's request, so a
// policy default filled into size would silently replace an authored template
// size.
func authoredStorageRequest(props map[string]any) bool {
	storage, ok := props["storage"].(map[string]any)
	if !ok {
		return false
	}
	if _, present := authoredValue(storage, "size"); present {
		return true
	}
	tmpl, ok := storage["pvcTemplate"].(map[string]any)
	if !ok {
		return false
	}
	res, ok := tmpl["resources"].(map[string]any)
	if !ok {
		return false
	}
	req, ok := res["requests"].(map[string]any)
	if !ok {
		return false
	}
	_, present := authoredValue(req, string(corev1.ResourceStorage))
	return present
}

// withoutNullsAtDepth applies the package's null contract ("A value that
// serializes to JSON null is absent, at every depth, on every path; a null is
// never a member of any Items type") to a whole authored value before it is
// decoded. The strict decode cannot do it: encoding/json reads a null map
// value as the element's zero value (`parameters: {work_mem: null}` would
// become `work_mem: ""`) and a null array element as a zero-valued entry.
//
// A null object key is dropped; a null array element is refused with the same
// message pkg/oam's property validator uses. The input is not modified. Keys
// are visited in sorted order so the first refusal is deterministic.
func withoutNullsAtDepth(value any, path string) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if isExplicitNull(v[k]) {
				continue
			}
			child := k
			if path != "" {
				child = path + "." + k
			}
			nv, err := withoutNullsAtDepth(v[k], child)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			elemPath := fmt.Sprintf("%s[%d]", path, i)
			if isExplicitNull(e) {
				return nil, errors.Errorf("%s: null is not a valid array element", elemPath)
			}
			nv, err := withoutNullsAtDepth(e, elemPath)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case nil:
		// A nil (or absent) property map decodes as an empty spec.
		return map[string]any{}, nil
	default:
		return value, nil
	}
}

// CnpgClusterConfig implements stack.ApplicationConfig for cnpg-cluster
// components. Spec is the decoded ClusterSpec exactly as authored, apart from
// the instance default and whatever ApplyPolicy fills in.
type CnpgClusterConfig struct {
	Name      string
	Namespace string
	Spec      cnpgv1.ClusterSpec

	explicitInstances   bool
	explicitStorageSize bool
}

// ApplyPolicy applies policy defaults, then enforces policy limits. The order
// and semantics are postgresql's (PostgresqlConfig.ApplyPolicy) mapped onto
// the Cluster's fields, so a postgresql component lowered onto this kind can
// produce the same output:
//
//   - postgresql `replicas`           -> spec.instances
//   - postgresql `resources` cpu/mem  -> spec.resources (the same corev1 shape)
//   - postgresql `storageSize`        -> spec.storage.size
//
// On top of that mapping, the maximum storage size caps every volume the
// Cluster requests (storage, walStorage, each tablespace, and the ephemeral
// volume's claim template), each read in whichever spelling authored it, as
// the workload kinds cap every claim they emit. The security gates that the
// workload kinds apply to a container securityContext apply to the Cluster's
// securityContext and podSecurityContext, which postgresql does not expose.
// Neither addition can fire on a postgresql-shaped Cluster.
func (c *CnpgClusterConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	if c.Spec.Instances > math.MaxInt32 || c.Spec.Instances < math.MinInt32 {
		return errors.Errorf("instances %d is out of range", c.Spec.Instances)
	}
	instances := applyDefaultReplicas(int32(c.Spec.Instances), c.explicitInstances, p.DefaultReplicas()) //nolint:gosec // range checked above
	c.Spec.Instances = int(instances)
	if err := applyDefaultQuantity(&c.Spec.Resources.Requests, corev1.ResourceCPU, p.DefaultCPURequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Spec.Resources.Requests, corev1.ResourceMemory, p.DefaultMemoryRequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Spec.Resources.Limits, corev1.ResourceCPU, p.DefaultCPULimit()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Spec.Resources.Limits, corev1.ResourceMemory, p.DefaultMemoryLimit()); err != nil {
		return err
	}
	// storage.size precedence: authored (either spelling) > policy default.
	// Unlike postgresql there is no "1Gi" handler fallback; with neither, the
	// field stays unset for the operator to validate.
	if !c.explicitStorageSize && p.DefaultStorageSize() != "" {
		c.Spec.StorageConfiguration.Size = p.DefaultStorageSize()
	}

	if maxInstances := p.MaxReplicas(); maxInstances != nil && instances > *maxInstances {
		return errors.Errorf("instances %d exceeds enforced maximum %d", instances, *maxInstances)
	}
	// The direct form, as in postgresql: Generate copies spec.resources onto the
	// Cluster unchanged, so there is no intrinsic default tier to enforce
	// against.
	for _, chk := range []struct {
		list  corev1.ResourceList
		name  corev1.ResourceName
		max   string
		label string
	}{
		{c.Spec.Resources.Requests, corev1.ResourceCPU, p.MaxCPU(), "cpu request"},
		{c.Spec.Resources.Limits, corev1.ResourceCPU, p.MaxCPU(), "cpu limit"},
		{c.Spec.Resources.Requests, corev1.ResourceMemory, p.MaxMemory(), "memory request"},
		{c.Spec.Resources.Limits, corev1.ResourceMemory, p.MaxMemory(), "memory limit"},
	} {
		if err := enforceMaxResource(quantityString(chk.list, chk.name), chk.max, chk.label); err != nil {
			return err
		}
	}
	if err := c.enforceMaxStorage(p.MaxStorageSize()); err != nil {
		return err
	}

	if err := enforcePrivileged(c.Spec.SecurityContext, p.AllowPrivileged()); err != nil {
		return err
	}
	if err := enforceContainerCapabilities(c.Spec.SecurityContext, p.AllowedContainerCapabilities(), p.ForbiddenContainerCapabilities()); err != nil {
		return err
	}
	if psc := c.Spec.PodSecurityContext; psc != nil && !p.AllowPrivileged() {
		if wo := psc.WindowsOptions; wo != nil && wo.HostProcess != nil && *wo.HostProcess {
			return errors.New("podSecurityContext.windowsOptions.hostProcess is not allowed by environment policy")
		}
	}
	return nil
}

// enforceMaxStorage caps the effective request of every volume the Cluster
// asks CNPG to create. The error names the spelling the value came from.
func (c *CnpgClusterConfig) enforceMaxStorage(maxSize string) error {
	if maxSize == "" {
		return nil
	}
	type volume struct {
		size, label string
	}
	vols := []volume{}
	add := func(sc *cnpgv1.StorageConfiguration, path string) {
		if size, label := cnpgStorageRequest(sc, path); size != "" {
			vols = append(vols, volume{size, label})
		}
	}
	add(&c.Spec.StorageConfiguration, "storage")
	add(c.Spec.WalStorage, "walStorage")
	for i := range c.Spec.Tablespaces {
		add(&c.Spec.Tablespaces[i].Storage, fmt.Sprintf("tablespaces[%d].storage", i))
	}
	if evs := c.Spec.EphemeralVolumeSource; evs != nil && evs.VolumeClaimTemplate != nil {
		if q, ok := evs.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			vols = append(vols, volume{q.String(), "ephemeralVolumeSource.volumeClaimTemplate.spec.resources.requests.storage"})
		}
	}
	for _, v := range vols {
		if err := enforceMaxResource(v.size, maxSize, v.label); err != nil {
			return err
		}
	}
	return nil
}

// cnpgStorageRequest returns the size a StorageConfiguration requests and the
// path of the spelling that supplied it: size when set, since CNPG writes it
// over the template, else the pvcTemplate's storage request. Reading size alone
// would let the template spelling bypass a maximum, the defect
// VolumeClaimTemplate.effectiveStorageRequest documents for statefulset.
func cnpgStorageRequest(sc *cnpgv1.StorageConfiguration, path string) (string, string) {
	if sc == nil {
		return "", ""
	}
	if sc.Size != "" {
		return sc.Size, path + ".size"
	}
	if sc.PersistentVolumeClaimTemplate != nil {
		if q, ok := sc.PersistentVolumeClaimTemplate.Resources.Requests[corev1.ResourceStorage]; ok {
			return q.String(), path + ".pvcTemplate.resources.requests.storage"
		}
	}
	return "", ""
}

// Generate emits the Cluster: kure's identity-only constructor plus a deep copy
// of the spec, so the generated object shares no pointer, map or slice with
// the config and a second Generate is unaffected by edits to the first result.
func (c *CnpgClusterConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// As in postgresql, checked on the resources the Cluster actually carries:
	// CNPG copies this block onto the instance pods unchanged, so a
	// hugepages-only block would build a Cluster whose pods admission refuses.
	if err := validateHugePagesHaveCPUOrMemory("resources", c.Spec.Resources.Requests, c.Spec.Resources.Limits); err != nil {
		return nil, err
	}
	cluster := kurecnpg.CreateCluster(app.Name, app.Namespace)
	c.Spec.DeepCopyInto(&cluster.Spec)
	obj := client.Object(cluster)
	return []*client.Object{&obj}, nil
}
