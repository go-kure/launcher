package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

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

// cnpgClusterNameMaxLength is the longest Cluster name the CloudNativePG
// admission webhook accepts (internal/webhook/v1/cluster_webhook.go in the
// linked v1.30.1, which also requires a DNS-1035 label). A component name is a
// DNS-1123 subdomain of up to 253 characters, so without this check a name the
// operator refuses would build, and one over 63 characters would also be copied
// into the cnpg.io/cluster endpoint selector, past the label-value limit.
const cnpgClusterNameMaxLength = 50

// validateCnpgClusterName refuses a component name CloudNativePG would refuse
// as the Cluster's name: not a DNS-1035 label (a leading digit or a dot), or
// longer than cnpgClusterNameMaxLength.
func validateCnpgClusterName(name string) error {
	if len(validation.IsDNS1035Label(name)) > 0 || len(name) > cnpgClusterNameMaxLength {
		return errors.Errorf("cnpg-cluster name %q: must be a DNS-1035 label of at most %d characters (CloudNativePG rejects longer or dotted cluster names)", name, cnpgClusterNameMaxLength)
	}
	return nil
}

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
// depth or a wrongly typed value is refused rather than dropped, as is an
// authored 0 or false the typed spec would omit and the operator would replace
// with a non-zero default (refuseUncarriedValues).
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
	// Refused here as well as at parse time: endpoints are collected
	// separately, and the name is copied into the selector verbatim.
	if err := validateCnpgClusterName(component.Name); err != nil {
		return nil, err
	}
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
		"imageName":                 str("Container image for the instances, by tag or digest. The operator's own default applies when omitted. A policy registry allowlist applies to an authored image."),
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
// A null is absence at every depth, per the package's null contract, and a
// null is whatever serializes to JSON null: the properties are read as their
// JSON serialization (jsonProperties), a null key is dropped and a null array
// element is refused by path. Everything else is decoded strictly into
// cnpgv1.ClusterSpec, so an unknown key or a wrongly typed value is an error;
// an unknown key is an error even when its value is null.
func (h *CnpgClusterHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	if err := validateCnpgClusterName(component.Name); err != nil {
		return nil, err
	}
	props, raw, err := jsonProperties(component.Properties)
	if err != nil {
		return nil, err
	}

	// Read ahead of the decode, with the helper every kind uses for replicas, so
	// a non-integer or out-of-range value is refused by name. encoding/json
	// matches field names case-insensitively, so "Instances" sets the field
	// too: every spelling the decoder accepts is read, or an authored 0 in
	// another spelling would count as unauthored, and a default would replace
	// it instead of the refusal below.
	instancesAuthored := false
	for _, key := range foldedFieldKeys(props, "instances") {
		_, present, err := parseInt32Field(map[string]any{key: jsonNumberValue(props[key])}, key, "instances")
		if err != nil {
			return nil, err
		}
		instancesAuthored = instancesAuthored || present
	}

	spec, _, err := builtin.DecodeStrictJSON[cnpgv1.ClusterSpec](props)
	if err != nil {
		return nil, errors.Wrap(err, "properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec")
	}
	// The null strip drops a null key at any depth, including one the type
	// does not declare, so a misspelt key authored as null (`storage: {sise:
	// null}`) would vanish. The package's null contract stops at declared keys
	// (pkg/oam/property_validate.go), so the unstripped tree is decoded too,
	// for its unknown-field refusal only: a null on a known field decodes as a
	// no-op, and a null array element was refused by the strip, so this adds
	// no other refusal.
	if _, _, err := builtin.DecodeStrictJSON[cnpgv1.ClusterSpec](raw); err != nil {
		return nil, errors.Wrap(err, "properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec")
	}
	// What was authored is also read back from the decoded spec, the check that
	// cannot drift from the decoder. The map reading stays for the values the
	// decoded spec cannot distinguish from absence: instances 0 and a storage
	// size of "".
	explicitInstances := instancesAuthored || spec.Instances != 0
	// The CRD declares Minimum=1 on ClusterSpec.Instances, so an authored 0
	// would build a Cluster the API server refuses. Stricter than postgresql's
	// replicas, which refuses only a negative count.
	if explicitInstances && spec.Instances < 1 {
		return nil, errors.Errorf("instances: must be >= 1, got %d", spec.Instances)
	}
	if !explicitInstances {
		spec.Instances = cnpgClusterDefaultInstances
	}
	// Checked here, on the spec as decoded, because it is final for every
	// authored value: ApplyPolicy only fills values the document left unset.
	if err := refuseUncarriedValues(props, spec); err != nil {
		return nil, err
	}

	return &CnpgClusterConfig{
		Name:                component.Name,
		Namespace:           namespace,
		Spec:                *spec,
		explicitInstances:   explicitInstances,
		explicitStorageSize: authoredStorageRequest(props) || decodedStorageRequest(&spec.StorageConfiguration),
	}, nil
}

// cnpgClusterDefaultedZeroFields lists the ClusterSpec fields on which an
// authored 0 or false would be silently replaced: non-pointer and omitempty,
// so the value is omitted when the Cluster is encoded, with a CRD default that
// is not zero, so the API server then applies that default instead. Each key
// is a json path with [] for an array element; each value is the CRD default,
// quoted in the refusal. A field whose default is itself zero or false
// (minSyncReplicas), or that has none (managed.roles[].login), is not listed:
// omitting its zero leaves the same value. TestCnpgClusterDefaultedZeroFields_MatchCRD derives this set
// from the linked CloudNativePG module, so a bump that changes it fails CI.
var cnpgClusterDefaultedZeroFields = map[string]string{
	"managed.roles[].connectionLimit": "-1",
	"postgresGID":                     "26",
	"postgresUID":                     "26",
	"probes.liveness.isolationCheck.connectionTimeout": "1000",
	"probes.liveness.isolationCheck.requestTimeout":    "1000",
	"replicationSlots.updateInterval":                  "30",
	"startDelay":                                       "3600",
	"stopDelay":                                        "1800",
	"switchoverDelay":                                  "3600",
}

// refuseUncarriedValues refuses an authored value that the typed spec decodes
// but the emitted Cluster would not carry with its meaning: a 0 or false on a
// cnpgClusterDefaultedZeroFields field, which the API server would replace
// with the CRD default. The spec is encoded as Generate's Cluster will be and
// the authored tree (jsonProperties' output) is walked against it, so a value
// is refused only when it is actually missing from the encoding.
//
// Two spellings of one field in the same object are refused as well:
// encoding/json keeps only one of them, dropping the other silently.
func refuseUncarriedValues(authored map[string]any, spec *cnpgv1.ClusterSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return errors.Wrap(err, "internal: encode the decoded ClusterSpec")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var encoded any
	if err := dec.Decode(&encoded); err != nil {
		return errors.Wrap(err, "internal: decode the encoded ClusterSpec")
	}
	return compareCarried(authored, encoded, "", "")
}

// compareCarried walks authored against encoded, descending only where both
// sides are objects or both are arrays (arrays align by index). path is the
// authored spelling with indices, for the error; field is the same position in
// the encoding's json names with [] for an index, the form
// cnpgClusterDefaultedZeroFields is keyed by. A leaf present in encoded in any
// spelling or type (a Quantity written as a number, say) is carried. A leaf
// absent from encoded is refused when it is a numeric zero or false on a
// cnpgClusterDefaultedZeroFields field; elsewhere omitting the zero leaves
// the same value, and an authored empty string is not
// refused at all (storage.size "" is a value the kind supports,
// authoredStorageRequest).
func compareCarried(authored, encoded any, path, field string) error {
	switch a := authored.(type) {
	case map[string]any:
		e, ok := encoded.(map[string]any)
		if !ok {
			return nil
		}
		join := func(base, k string) string {
			if base == "" {
				return k
			}
			return base + "." + k
		}
		// claimed maps an encoded key to the authored path that matched it.
		// unmatched holds the authored keys with nothing in encoded: only a
		// struct field can be omitted, so two of them that fold together are
		// two spellings of one field, whichever value the decoder kept.
		claimed := make(map[string]string, len(a))
		var unmatched []string
		for _, k := range slices.Sorted(maps.Keys(a)) {
			child := join(path, k)
			ek, present := encodedKey(e, k)
			if !present {
				for _, prev := range unmatched {
					if strings.EqualFold(prev, k) {
						return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, join(path, prev))
					}
				}
				unmatched = append(unmatched, k)
				if def, defaulted := defaultedZeroField(join(field, k)); defaulted && isOmittedZero(a[k]) {
					return errors.Errorf("%s: %v cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default %s)", child, a[k], def)
				}
				continue
			}
			if other, dup := claimed[ek]; dup {
				return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, other)
			}
			claimed[ek] = child
			if err := compareCarried(a[k], e[ek], child, join(field, ek)); err != nil {
				return err
			}
		}
	case []any:
		e, ok := encoded.([]any)
		if !ok {
			return nil
		}
		for i := range min(len(a), len(e)) {
			if err := compareCarried(a[i], e[i], fmt.Sprintf("%s[%d]", path, i), field+"[]"); err != nil {
				return err
			}
		}
	}
	return nil
}

// defaultedZeroField returns the CRD default of the
// cnpgClusterDefaultedZeroFields entry at field, matched case-insensitively:
// the parent segments are json names from the encoding, but the leaf is the
// authored key, with nothing in the encoding to take its json name from, and
// encoding/json folds field names.
func defaultedZeroField(field string) (string, bool) {
	for _, known := range slices.Sorted(maps.Keys(cnpgClusterDefaultedZeroFields)) {
		if strings.EqualFold(known, field) {
			return cnpgClusterDefaultedZeroFields[known], true
		}
	}
	return "", false
}

// encodedKey returns the key of e that the authored key k was decoded into:
// k itself, else a case-insensitive match, the folding encoding/json applies
// to struct field names. A Go map encodes every entry under the key it was
// decoded from, so the exact lookup always hits for a map key and the fold is
// reached only for a struct field.
func encodedKey(e map[string]any, k string) (string, bool) {
	if _, ok := e[k]; ok {
		return k, true
	}
	for _, ek := range slices.Sorted(maps.Keys(e)) {
		if strings.EqualFold(ek, k) {
			return ek, true
		}
	}
	return "", false
}

// isOmittedZero reports whether an authored leaf is a value omitempty drops:
// false, or a number whose digits are all zero (0, -0, 0.0, 0e5), decided on
// the literal so no float conversion can round a non-zero value to zero.
func isOmittedZero(v any) bool {
	switch x := v.(type) {
	case bool:
		return !x
	case json.Number:
		mantissa, _, _ := strings.Cut(strings.ToLower(x.String()), "e")
		return strings.Trim(mantissa, "-0.") == ""
	default:
		return false
	}
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
//
// Each struct field is looked up in every spelling the decoder accepts
// (foldedFieldKeys), so storage.Size: "" counts as authored exactly as
// storage.size: "" does. The requests key is a ResourceList map key, which
// encoding/json matches exactly, so "storage" there is read as spelt.
func authoredStorageRequest(props map[string]any) bool {
	for _, storage := range foldedFieldMaps(props, "storage") {
		for _, key := range foldedFieldKeys(storage, "size") {
			if _, present := authoredValue(storage, key); present {
				return true
			}
		}
		for _, tmpl := range foldedFieldMaps(storage, "pvcTemplate") {
			for _, res := range foldedFieldMaps(tmpl, "resources") {
				for _, req := range foldedFieldMaps(res, "requests") {
					if _, present := authoredValue(req, string(corev1.ResourceStorage)); present {
						return true
					}
				}
			}
		}
	}
	return false
}

// foldedFieldKeys returns the keys of m that encoding/json decodes into the
// struct field whose json name is field: the exact spelling first, then each
// case-insensitive variant (strings.EqualFold, the folding encoding/json
// uses) in sorted order. Authorship read off the raw map must consult all of
// them, or a spelling the decoder accepted would count as unauthored.
func foldedFieldKeys(m map[string]any, field string) []string {
	var keys []string
	if _, ok := m[field]; ok {
		keys = append(keys, field)
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != field && strings.EqualFold(k, field) {
			keys = append(keys, k)
		}
	}
	return keys
}

// foldedFieldMaps returns the object values of m under every spelling of
// field (foldedFieldKeys). encoding/json merges them all into the one field.
func foldedFieldMaps(m map[string]any, field string) []map[string]any {
	var out []map[string]any
	for _, k := range foldedFieldKeys(m, field) {
		if sub, ok := m[k].(map[string]any); ok {
			out = append(out, sub)
		}
	}
	return out
}

// jsonProperties returns props as encoding/json serializes them, with the
// package's null contract applied. The contract defines null by serialization
// ("A value that serializes to JSON null is absent, at every depth"), so the
// properties are marshalled and decoded back rather than inspected in Go: a
// direct caller's typed collections, pointers, json.RawMessage and custom
// encoders (pointer receivers included, where encoding/json calls them) all
// come back as the JSON the strict decode would have read, and a null is a
// plain nil. This serves a direct caller only: a lowering rule's output is
// checked by the engine before dispatch, which accepts JSON-shaped values
// (string-keyed maps, slices, scalars) and refuses a typed API struct. Numbers
// decode as json.Number, which the strict decode re-marshals verbatim, so no
// precision is lost. A value encoding/json cannot serialize is refused.
//
// It also returns the same tree before the strip (raw), whose null keys the
// caller still needs to check against the type's declared fields.
func jsonProperties(props map[string]any) (stripped, raw map[string]any, err error) {
	data, err := json.Marshal(props)
	if err != nil {
		return nil, nil, errors.Wrap(err, "properties do not serialize to JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, nil, errors.Wrap(err, "internal: decode the properties' own JSON")
	}
	normalized, err := withoutNullsAtDepth(tree, "")
	if err != nil {
		return nil, nil, err
	}
	// A nil property map serializes to null; it reads as an empty map, which
	// decodes as an empty spec.
	stripped, _ = normalized.(map[string]any)
	raw, _ = tree.(map[string]any)
	return stripped, raw, nil
}

// jsonNumberValue returns a json.Number as the Go number the property parsers
// accept: an int64 when it is one, else a uint64 (so an out-of-range refusal
// quotes the integer exactly, not rounded), else a float64. A number
// encoding/json wrote only fails Float64 when out of range, where the value is
// ±Inf and the parser refuses it as not finite. Any other value is returned
// unchanged.
func jsonNumberValue(value any) any {
	n, ok := value.(json.Number)
	if !ok {
		return value
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	if u, err := strconv.ParseUint(n.String(), 10, 64); err == nil {
		return u
	}
	f, _ := n.Float64()
	return f
}

// withoutNullsAtDepth applies the package's null contract ("A value that
// serializes to JSON null is absent, at every depth, on every path; a null is
// never a member of any Items type") to a decoded JSON tree before the strict
// decode. The strict decode cannot do it: encoding/json reads a null map value
// as the element's zero value (`parameters: {work_mem: null}` would become
// `work_mem: ""`) and a null array element as a zero-valued entry.
//
// value is what jsonProperties decoded, so it holds only map[string]any,
// []any, scalars and a plain nil for every null. A null object key is dropped;
// a null array element is refused with the same message pkg/oam's property
// validator uses. Keys are visited in sorted order so the first refusal is
// deterministic.
func withoutNullsAtDepth(value any, path string) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if v[k] == nil {
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
			if e == nil {
				return nil, errors.Errorf("%s: null is not a valid array element", elemPath)
			}
			nv, err := withoutNullsAtDepth(e, elemPath)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
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
// Neither can fire on a postgresql-shaped Cluster. The registry allowlist the
// workload kinds apply to their image also applies to imageName when it is
// set; postgresql does not enforce it, so this one addition can refuse a
// postgresql-shaped Cluster whose image comes from a registry outside the list.
func (c *CnpgClusterConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	if c.Spec.Instances > math.MaxInt32 || c.Spec.Instances < math.MinInt32 {
		return errors.Errorf("instances %d is out of range", c.Spec.Instances)
	}
	instances := applyDefaultReplicas(int32(c.Spec.Instances), c.explicitInstances, p.DefaultReplicas()) //nolint:gosec // range checked above
	// An authored count was checked at parse time; a policy default below the
	// CRD's minimum is refused the same way rather than emitted.
	if instances < 1 {
		if !c.explicitInstances && p.DefaultReplicas() != nil {
			return errors.Errorf("instances: must be >= 1, got %d from the policy default", instances)
		}
		return errors.Errorf("instances: must be >= 1, got %d", instances)
	}
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
	// Only an authored image is checked: without imageName the operator runs its
	// own default image, which the document did not choose.
	if c.Spec.ImageName != "" {
		if err := enforceAllowedRegistries(c.Spec.ImageName, p.AllowedRegistries()); err != nil {
			return errors.Wrap(err, "imageName")
		}
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
	// The config is exported, so a caller can build it without
	// ToApplicationConfig. The two parse-time refusals that guard what
	// CloudNativePG admits are repeated on what is emitted, as the workload
	// kinds repeat their name check: the Cluster is named from app.Name.
	if err := validateCnpgClusterName(app.Name); err != nil {
		return nil, err
	}
	if c.Spec.Instances < 1 {
		return nil, errors.Errorf("instances: must be >= 1, got %d", c.Spec.Instances)
	}
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
