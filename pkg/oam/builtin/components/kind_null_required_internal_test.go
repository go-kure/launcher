package components

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// A kind component that decodes its properties into an upstream Go type emits
// what that type encodes, and a field the type holds as a pointer or a list
// without omitempty is written as null when nothing was decoded into it. Where
// the CRD requires that field, the object is refused: the API server drops a
// null of a field that is not nullable before it validates, and the field is
// then missing. The decoded value shows the omission (the field is nil), but
// the encoded object does not show it to a reader, who sees the field written.
//
// nullRequiredKinds holds the kinds that have such a field to an answer for
// each, and TestKindComponents_NullRequired shows every answer by running the
// schema validator on the object the kind emits, with the CRD the linked
// module ships: a field the kind refuses is one the validator refuses, and a
// field the kind does not refuse is one the validator accepts, for the reason
// its row gives.
//
// The cert-manager kinds' required list is held to the CRDs by
// TestCertManagerKinds_RequiredMatchCRD, which derives these fields among the
// others from the schemas' required lists and runs no validator: here each is
// shown refused by one. The CloudNativePG kinds have no derived required list,
// and no other test holds them to this set. A new kind of either family joins
// with one entry: its component, the type its properties decode into, its
// handler and its CRD. The test then names every field of the set that has no
// row.

// crdValidation is the schema of one version of one CRD and its validator.
type crdValidation struct {
	schema    *spec.Schema
	validator *validate.SchemaValidator
}

// crdValidationOf reads the CRD in file, a path under the directory of the
// linked module, and builds the validator of the schema of the version named:
// the one the API server builds for a custom resource (k8s.io/kube-openapi's),
// over the schema as the CRD writes it.
func crdValidationOf(t *testing.T, modulePath, file, version string) crdValidation {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(linkedModuleDir(t, modulePath), filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	at := slices.IndexFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool { return v.Name == version })
	if at < 0 || crd.Spec.Versions[at].Schema == nil || crd.Spec.Versions[at].Schema.OpenAPIV3Schema == nil {
		t.Fatalf("%s has no schema for the version %s", file, version)
	}
	raw, err := json.Marshal(crd.Spec.Versions[at].Schema.OpenAPIV3Schema)
	if err != nil {
		t.Fatalf("encode the schema of %s: %v", file, err)
	}
	schema := &spec.Schema{}
	if err := json.Unmarshal(raw, schema); err != nil {
		t.Fatalf("decode the schema of %s: %v", file, err)
	}
	return crdValidation{schema: schema, validator: validate.NewSchemaValidator(schema, nil, "", strfmt.Default)}
}

// refusals validates obj as the API server validates a custom resource at
// creation, and returns what the schema refuses, one message per error.
func (v crdValidation) refusals(obj map[string]any) []string {
	pruneNullsAsAPIServer(obj, v.schema)
	var out []string
	for _, err := range v.validator.Validate(obj).Errors {
		out = append(out, err.Error())
	}
	return out
}

// pruneNullsAsAPIServer stands in for two functions of the API server
// (k8s.io/apiextensions-apiserver v0.37.1), which it runs on a custom resource
// before the schema validation. crdCreate (crd_create_internal_test.go) runs
// the two functions themselves; this test reads the schema with the validator
// of k8s.io/kube-openapi alone and keeps the stand-in:
//
//   - structuraldefaulting.PruneNonNullableNullsWithoutDefaults, called at
//     pkg/apiserver/customresource_handler.go:1435, drops a null of a field
//     that is not nullable and has no default;
//   - structuraldefaulting.Default, called at
//     pkg/apiserver/customresource_handler.go:1244, puts a field's default in
//     the place of such a null.
//
// It does those two things and nothing else of either function: a field that
// is absent is not defaulted. The validation that follows them
// (apiextensionsvalidation.ValidateCustomResource, called at
// pkg/registry/customresource/validator.go:58) is the validator of
// k8s.io/kube-openapi, which is linked and which crdValidation runs.
func pruneNullsAsAPIServer(value any, schema *spec.Schema) {
	if schema == nil {
		return
	}
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			prop, ok := schema.Properties[name]
			if !ok {
				if schema.AdditionalProperties != nil {
					pruneNullsAsAPIServer(child, schema.AdditionalProperties.Schema)
				}
				continue
			}
			if child == nil && !prop.Nullable {
				delete(v, name)
				if prop.Default != nil {
					v[name] = prop.Default
				}
				continue
			}
			pruneNullsAsAPIServer(child, &prop)
		}
	case []any:
		if schema.Items != nil {
			for _, item := range v {
				pruneNullsAsAPIServer(item, schema.Items.Schema)
			}
		}
	}
}

// nullRequiredRow is the answer for one field a kind's type writes unauthored
// in a form the CRD refuses: a required field written as null, or a field
// written as a zero value the CRD's own rule for the value refuses.
type nullRequiredRow struct {
	// path is the field's json path, with [] for a list element, and at the
	// path of the one the properties below hold.
	path, at string
	// omitted returns properties that author the field's parent and leave the
	// field out.
	omitted func() map[string]any

	// Refused: reason is what the kind's refusal says of the field, and
	// authored a value which, written at the field, makes the properties ones
	// the kind builds and the CRD accepts.
	reason   string
	authored any
	// writesZero says the row is of the second set: the type writes the field
	// as its zero value, an empty string or 0, and not as null, and the
	// schema's own rule for the value refuses that one.
	writesZero bool
	// refusesEmpty says the kind refuses an authored empty string at the field
	// as it refuses the omission, where the other kinds leave that value to
	// the API server.
	refusesEmpty bool

	// Not refused: built is the JSON the kind's object carries at the field,
	// and why the reason the CRD accepts the object.
	built, why string
}

// nullRequiredKind is one kind component with a row for every field of the two
// sets TestKindComponents_NullRequired derives for it: the required fields its
// type writes as null, and the fields its type writes as a zero value the CRD
// refuses. A row of the second set says by its comment which list of the kind
// refuses the omission, or by its why what the kind does in its place.
//
// A kind added to the table brings its rows for all three: the required fields
// written as null, the fields written as a refused zero value that the kind
// refuses when they are unauthored, and the ones it answers another way.
type nullRequiredKind struct {
	component  string
	typ        reflect.Type
	handler    oam.ComponentHandler
	modulePath string
	crd        string
	// version is the version of the CRD the kind's object is of, where that
	// is not v1.
	version string
	rows    []nullRequiredRow
}

// crdVersion is the version of the CRD the kind's object is of.
func (k nullRequiredKind) crdVersion() string {
	if k.version == "" {
		return "v1"
	}
	return k.version
}

const nodeSelectorTermsReason = "the node selector terms, of which a node must match one"

// someNodeSelectorTerms is a list of node selector terms the CRDs accept.
func someNodeSelectorTerms() []any {
	return []any{map[string]any{"matchExpressions": []any{
		map[string]any{"key": "kubernetes.io/os", "operator": "In", "values": []any{"linux"}},
	}}}
}

// requiredAffinityWithoutTerms is an affinity whose required node affinity is
// authored without its terms.
func requiredAffinityWithoutTerms() map[string]any {
	return map[string]any{"nodeAffinity": map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{}}}
}

// acmeIssuerWith returns an ACME issuer's properties with the one solver.
func acmeIssuerWith(solver map[string]any) func() map[string]any {
	return func() map[string]any {
		return map[string]any{"acme": map[string]any{
			"server":              "https://acme-v02.api.letsencrypt.org/directory",
			"privateKeySecretRef": map[string]any{"name": "account"},
			"solvers":             []any{solver},
		}}
	}
}

// issuerNullRequiredRows is the rows of the issuer and clusterissuer kinds,
// which decode one type.
func issuerNullRequiredRows() []nullRequiredRow {
	const terms = ".podTemplate.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms"
	solverPod := func() map[string]any {
		return map[string]any{"spec": map[string]any{"affinity": requiredAffinityWithoutTerms()}}
	}
	return []nullRequiredRow{
		{
			path: "acme.solvers[].dns01.route53.auth.kubernetes", at: "acme.solvers[0].dns01.route53.auth.kubernetes",
			omitted: acmeIssuerWith(map[string]any{"dns01": map[string]any{"route53": map[string]any{"region": "eu-west-1", "auth": map[string]any{}}}}),
			reason:  "the service account token the role is assumed with", authored: map[string]any{"serviceAccountRef": map[string]any{"name": "cert-manager"}},
		},
		{
			path: "acme.solvers[].dns01.route53.auth.kubernetes.serviceAccountRef", at: "acme.solvers[0].dns01.route53.auth.kubernetes.serviceAccountRef",
			omitted: acmeIssuerWith(map[string]any{"dns01": map[string]any{"route53": map[string]any{"region": "eu-west-1", "auth": map[string]any{"kubernetes": map[string]any{}}}}}),
			reason:  "the ServiceAccount a token is requested for", authored: map[string]any{"name": "cert-manager"},
		},
		{
			path: "acme.solvers[].http01.ingress" + terms, at: "acme.solvers[0].http01.ingress" + terms,
			omitted: acmeIssuerWith(map[string]any{"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "nginx", "podTemplate": solverPod()}}}),
			reason:  nodeSelectorTermsReason, authored: someNodeSelectorTerms(),
		},
		{
			path: "acme.solvers[].http01.gatewayHTTPRoute" + terms, at: "acme.solvers[0].http01.gatewayHTTPRoute" + terms,
			omitted: acmeIssuerWith(map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{
				"parentRefs": []any{map[string]any{"name": "public"}}, "podTemplate": solverPod(),
			}}}),
			reason: nodeSelectorTermsReason, authored: someNodeSelectorTerms(),
		},
		// The two rows below are of the second set. Each is refused by the
		// kind's required list, as an omission.
		{
			path: "acme.solvers[].http01.gatewayHTTPRoute.parentRefs[].name", at: "acme.solvers[0].http01.gatewayHTTPRoute.parentRefs[0].name",
			omitted: acmeIssuerWith(map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{
				"parentRefs": []any{map[string]any{"namespace": "gateways"}},
			}}}),
			reason: "the name of the Gateway the route attaches to", authored: "public", writesZero: true,
		},
		{
			path: "vault.auth.aws.role", at: "vault.auth.aws.role",
			omitted: func() map[string]any {
				return map[string]any{"vault": map[string]any{
					"server": "https://vault.example", "path": "pki/sign/example", "auth": map[string]any{"aws": map[string]any{}},
				}}
			},
			reason: "the Vault role to assume", authored: "issuer", writesZero: true,
		},
	}
}

// databaseWith returns a database's properties with the one list, of a single
// object that is named and nothing else.
func databaseWith(list string) func() map[string]any {
	return func() map[string]any {
		return map[string]any{
			"cluster": map[string]any{"name": "db"}, "name": "app", "owner": "app",
			list: []any{map[string]any{"name": "one"}},
		}
	}
}

// databaseEnsureRow is the row of the ensure of a database's list: the kind
// writes the CRD's default where the author wrote none.
func databaseEnsureRow(list string) nullRequiredRow {
	return nullRequiredRow{
		path: list + "[].ensure", at: list + "[0].ensure", omitted: databaseWith(list), writesZero: true,
		built: `"present"`,
		why:   "the kind writes the value itself: an unauthored ensure is emitted as present, the CRD's default, which the API server cannot fill in the place of an empty string",
	}
}

// poolerWith returns a pooler's properties with the pod spec of its template.
// The pod spec lists a container unless it is nil.
func poolerWith(podSpec map[string]any) func() map[string]any {
	return func() map[string]any {
		pod := map[string]any{}
		if podSpec != nil {
			pod["containers"] = []any{map[string]any{"name": "pgbouncer"}}
		}
		for name, value := range podSpec {
			pod[name] = value
		}
		return map[string]any{
			"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{},
			"template": map[string]any{"spec": pod},
		}
	}
}

// clusterWith returns a cluster's properties with the fields of extra.
func clusterWith(extra map[string]any) func() map[string]any {
	return func() map[string]any {
		out := map[string]any{"instances": 1, "storage": map[string]any{"size": "1Gi"}}
		for name, value := range extra {
			out[name] = value
		}
		return out
	}
}

var nullRequiredKinds = []nullRequiredKind{
	{
		component: "issuer", typ: reflect.TypeFor[certv1.IssuerSpec](), handler: &IssuerHandler{},
		modulePath: certManagerModulePath, crd: certManagerCRDs + "issuers.yaml", rows: issuerNullRequiredRows(),
	},
	{
		component: "clusterissuer", typ: reflect.TypeFor[certv1.IssuerSpec](), handler: &ClusterIssuerHandler{},
		modulePath: certManagerModulePath, crd: certManagerCRDs + "clusterissuers.yaml", rows: issuerNullRequiredRows(),
	},
	{
		component: "replicationsource", typ: reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](), handler: &ReplicationSourceHandler{},
		modulePath: volsyncModulePath, crd: volsyncCRDs + "replicationsources.yaml", version: "v1alpha1",
		rows: volsyncNullRequiredRows("rclone", "restic", "rsyncTLS", "syncthing"),
	},
	{
		component: "replicationdestination", typ: reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](), handler: &ReplicationDestinationHandler{},
		modulePath: volsyncModulePath, crd: volsyncCRDs + "replicationdestinations.yaml", version: "v1alpha1",
		rows: volsyncNullRequiredRows("rclone", "restic", "rsyncTLS"),
	},
	// The next three kinds have no required field their type writes as null
	// in the linked versions: their rows are of the second set.
	{
		component: "certificate", typ: reflect.TypeFor[certv1.CertificateSpec](), handler: &CertificateHandler{},
		modulePath: certManagerModulePath, crd: certManagerCRDs + "certificates.yaml",
		rows: []nullRequiredRow{
			// Refused by the kind's required list, as an omission.
			{
				path: "additionalOutputFormats[].type", at: "additionalOutputFormats[0].type",
				omitted: func() map[string]any {
					return map[string]any{
						"secretName": "tls", "issuerRef": map[string]any{"name": "ca"}, "dnsNames": []any{"example.com"},
						"additionalOutputFormats": []any{map[string]any{}},
					}
				},
				reason: "the format: DER or CombinedPEM", authored: "DER", writesZero: true,
			},
		},
	},
	{
		component: "cnpg-database", typ: reflect.TypeFor[cnpgv1.DatabaseSpec](), handler: &CnpgDatabaseHandler{},
		modulePath: cnpgModulePath, crd: cnpgCRDs + "databases.yaml",
		rows: []nullRequiredRow{
			databaseEnsureRow("extensions"), databaseEnsureRow("fdws"), databaseEnsureRow("schemas"), databaseEnsureRow("servers"),
		},
	},
	{
		component: "cnpg-objectstore", typ: reflect.TypeFor[barmanv1.ObjectStoreSpec](), handler: &CnpgObjectStoreHandler{},
		modulePath: barmanCloudModulePath, crd: "config/crd/bases/barmancloud.cnpg.io_objectstores.yaml",
		rows: []nullRequiredRow{
			// Refused by the kind's own check of the decoded value, which
			// cannot tell an authored empty path from none: both are refused.
			{
				path: "configuration.destinationPath", at: "configuration.destinationPath",
				omitted: func() map[string]any { return map[string]any{"configuration": map[string]any{}} },
				reason:  "the object store path backups and WAL are written to", authored: "s3://backups/db",
				writesZero: true, refusesEmpty: true,
			},
		},
	},
	{
		component: "cnpg-pooler", typ: reflect.TypeFor[cnpgv1.PoolerSpec](), handler: &CnpgPoolerHandler{},
		modulePath: cnpgModulePath, crd: cnpgCRDs + "poolers.yaml",
		rows: []nullRequiredRow{
			{
				path: "pgbouncer", at: "pgbouncer",
				omitted: func() map[string]any { return map[string]any{"cluster": map[string]any{"name": "db"}} },
				reason:  "an empty object selects PgBouncer's defaults", authored: map[string]any{},
			},
			{
				path:    "template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms",
				at:      "template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms",
				omitted: poolerWith(map[string]any{"affinity": requiredAffinityWithoutTerms()}),
				reason:  nodeSelectorTermsReason, authored: someNodeSelectorTerms(),
			},
			{
				path: "template.spec.evictionResponders[].priority", at: "template.spec.evictionResponders[0].priority",
				omitted: poolerWith(map[string]any{"evictionResponders": []any{map[string]any{"name": "example.com/drain"}}}),
				reason:  "the responder's priority, from 0 to 100000; no default is filled", authored: 10000,
			},
			{
				path: "template.spec.volumes[].cephfs.monitors", at: "template.spec.volumes[0].cephfs.monitors",
				omitted: poolerWith(map[string]any{"volumes": []any{map[string]any{"name": "data", "cephfs": map[string]any{}}}}),
				reason:  "the addresses of the Ceph monitors", authored: []any{"10.0.0.1:6789"},
			},
			{
				path: "template.spec.volumes[].rbd.monitors", at: "template.spec.volumes[0].rbd.monitors",
				omitted: poolerWith(map[string]any{"volumes": []any{map[string]any{"name": "data", "rbd": map[string]any{"image": "data"}}}}),
				reason:  "the addresses of the Ceph monitors", authored: []any{"10.0.0.1:6789"},
			},
			{
				path: "template.spec.volumes[].scaleIO.secretRef", at: "template.spec.volumes[0].scaleIO.secretRef",
				omitted: poolerWith(map[string]any{"volumes": []any{map[string]any{"name": "data", "scaleIO": map[string]any{"gateway": "https://gateway.example", "system": "storage"}}}}),
				reason:  "the Secret that holds the ScaleIO credentials", authored: map[string]any{"name": "scaleio"},
			},
			{
				path: "template.spec.containers", at: "template.spec.containers",
				omitted: poolerWith(nil),
				built:   "[]",
				why:     "the kind writes the list itself: a template that lists no container is emitted with containers: [], which the CRD accepts and the operator reads as it reads an omitted spec, adding its own container",
			},
			// Refused by cnpgPoolerRequired.
			{
				path: "pgbouncer.imageCatalogRef.key", at: "pgbouncer.imageCatalogRef.key",
				omitted: func() map[string]any {
					return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{
						"imageCatalogRef": map[string]any{"apiGroup": "postgresql.cnpg.io", "kind": "ImageCatalog", "name": "poolers"},
					}}
				},
				reason: "the key of the image in the catalog", authored: "pgbouncer", writesZero: true,
			},
		},
	},
	{
		component: "cnpg-cluster", typ: reflect.TypeFor[cnpgv1.ClusterSpec](), handler: &CnpgClusterHandler{},
		modulePath: cnpgModulePath, crd: cnpgCRDs + "clusters.yaml",
		rows: []nullRequiredRow{
			{
				path:    "affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms",
				at:      "affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms",
				omitted: clusterWith(map[string]any{"affinity": requiredAffinityWithoutTerms()}),
				reason:  nodeSelectorTermsReason, authored: someNodeSelectorTerms(),
			},
			{
				path: "bootstrap.initdb.import.databases", at: "bootstrap.initdb.import.databases",
				omitted: clusterWith(map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"import": map[string]any{
					"type": "microservice", "source": map[string]any{"externalCluster": "origin"},
				}}}}),
				reason: "the databases to import", authored: []any{"app"},
			},
			{
				path: "replicationSlots.synchronizeReplicas.enabled", at: "replicationSlots.synchronizeReplicas.enabled",
				omitted: clusterWith(map[string]any{"replicationSlots": map[string]any{"synchronizeReplicas": map[string]any{}}}),
				built:   "null",
				why:     "the CRD defaults the field (to true), and the API server puts a field's default in the place of the null it drops",
			},
			{
				path: "instances", at: "instances", writesZero: true,
				omitted: func() map[string]any { return map[string]any{"storage": map[string]any{"size": "1Gi"}} },
				built:   "1",
				why:     "the kind writes the value itself: an unauthored instances is emitted as 1, the CRD's default, which the API server cannot fill in the place of a 0",
			},
			// The rows below are refused by cnpgClusterRequired.
			{
				path: "backup.barmanObjectStore.destinationPath", at: "backup.barmanObjectStore.destinationPath",
				omitted: clusterWith(map[string]any{"backup": map[string]any{"barmanObjectStore": map[string]any{}}}),
				reason:  "the object store path backups and WAL are written to", authored: "s3://backups/db", writesZero: true,
			},
			{
				path: "bootstrap.initdb.import.type", at: "bootstrap.initdb.import.type",
				omitted: clusterWith(map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"import": map[string]any{
					"databases": []any{"app"}, "source": map[string]any{"externalCluster": "origin"},
				}}}}),
				reason: "how the databases are imported: microservice or monolith", authored: "microservice", writesZero: true,
			},
			{
				path: "bootstrap.pg_basebackup.source", at: "bootstrap.pg_basebackup.source",
				omitted: clusterWith(map[string]any{"bootstrap": map[string]any{"pg_basebackup": map[string]any{}}}),
				reason:  "the name of the external cluster the base backup is taken from", authored: "origin", writesZero: true,
			},
			{
				path: "externalClusters[].barmanObjectStore.destinationPath", at: "externalClusters[0].barmanObjectStore.destinationPath",
				omitted: clusterWith(map[string]any{"externalClusters": []any{map[string]any{"name": "origin", "barmanObjectStore": map[string]any{}}}}),
				reason:  "the object store path the cluster's backups and WAL are read from", authored: "s3://backups/origin", writesZero: true,
			},
			{
				path: "managed.services.additional[].selectorType", at: "managed.services.additional[0].selectorType",
				omitted: clusterWith(map[string]any{"managed": map[string]any{"services": map[string]any{"additional": []any{
					map[string]any{"serviceTemplate": map[string]any{"metadata": map[string]any{"name": "fast-extra"}}},
				}}}}),
				reason: "the instances the service selects: rw, r or ro", authored: "rw", writesZero: true,
			},
			{
				path: "podSelectorRefs[].name", at: "podSelectorRefs[0].name",
				omitted: clusterWith(map[string]any{"podSelectorRefs": []any{
					map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "client"}}},
				}}),
				reason: "the name pg_hba rules refer to the selector by", authored: "clients", writesZero: true,
			},
			{
				path: "postgresql.extensions[].env[].name", at: "postgresql.extensions[0].env[0].name",
				omitted: clusterWith(map[string]any{"postgresql": map[string]any{"extensions": []any{map[string]any{
					"name": "vector", "image": map[string]any{"reference": "registry.example/vector:1"},
					"env": []any{map[string]any{"value": "on"}},
				}}}}),
				reason: "the name of the environment variable", authored: "VECTOR_MODE", writesZero: true,
			},
			{
				path: "postgresql.extensions[].env[].value", at: "postgresql.extensions[0].env[0].value",
				omitted: clusterWith(map[string]any{"postgresql": map[string]any{"extensions": []any{map[string]any{
					"name": "vector", "image": map[string]any{"reference": "registry.example/vector:1"},
					"env": []any{map[string]any{"name": "VECTOR_MODE"}},
				}}}}),
				reason: "the value of the environment variable", authored: "on", writesZero: true,
			},
			{
				path: "postgresql.extensions[].name", at: "postgresql.extensions[0].name",
				omitted: clusterWith(map[string]any{"postgresql": map[string]any{"extensions": []any{map[string]any{
					"image": map[string]any{"reference": "registry.example/vector:1"},
				}}}}),
				reason: "the name of the extension", authored: "vector", writesZero: true,
			},
			{
				path: "postgresql.synchronous.method", at: "postgresql.synchronous.method",
				omitted: clusterWith(map[string]any{"postgresql": map[string]any{"synchronous": map[string]any{"number": 1}}}),
				reason:  "how the synchronous standbys are chosen: any or first", authored: "any", writesZero: true,
			},
			{
				path: "replica.source", at: "replica.source",
				omitted: clusterWith(map[string]any{"replica": map[string]any{"enabled": true}}),
				reason:  "the name of the external cluster this one replicates", authored: "origin", writesZero: true,
			},
		},
	},
}

// properties returns the row's properties, each call its own copy: the
// functions above share what they nest.
func (row nullRequiredRow) properties(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(row.omitted())
	if err != nil {
		t.Fatalf("encode the properties: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode the properties: %v", err)
	}
	return out
}

// fieldAt returns the object that holds the field at the path at under node,
// and the field's name. Every element of the path but the last is an object,
// or the indexed element of a list of them.
func fieldAt(t *testing.T, node map[string]any, at string) (map[string]any, string) {
	t.Helper()
	segments := strings.Split(at, ".")
	for _, segment := range segments[:len(segments)-1] {
		name, index, indexed := strings.Cut(segment, "[")
		next := node[name]
		if indexed {
			i, err := strconv.Atoi(strings.TrimSuffix(index, "]"))
			items, _ := next.([]any)
			if err != nil || i >= len(items) {
				t.Fatalf("%s: no element %s of %s", at, index, name)
			}
			next = items[i]
		}
		object, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("%s: %s is no object", at, segment)
		}
		node = object
	}
	return node, segments[len(segments)-1]
}

// object builds the component of the kind with the properties and returns
// the one object it emits, as the JSON it is written as.
func (k nullRequiredKind) object(props map[string]any) (map[string]any, error) {
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: k.component, Properties: props}, "data")
	if err != nil {
		return nil, err
	}
	objs, err := cfg.Generate(stack.NewApplication("fast", "data", cfg))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(*objs[0])
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

// encoded returns the spec the kind's type encodes for the properties: what
// the kind would emit as the object's spec if it refused nothing.
func (k nullRequiredKind) encoded(t *testing.T, props map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("encode the properties: %v", err)
	}
	decoded := reflect.New(k.typ)
	if err := json.Unmarshal(raw, decoded.Interface()); err != nil {
		t.Fatalf("decode the properties into %s: %v", k.typ, err)
	}
	if raw, err = json.Marshal(decoded.Interface()); err != nil {
		t.Fatalf("encode %s: %v", k.typ, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode the encoded %s: %v", k.typ, err)
	}
	return out
}

// TestKindComponents_NullRequired derives two sets for each kind of
// nullRequiredKinds, of the fields its type writes when they are unauthored:
// the ones its CRD requires that are written as null, and the ones written as
// a zero value, an empty string or 0, that the CRD's own rule for the value
// refuses (a minimum length, a pattern, an enumeration, a minimum). It holds
// the kind to a row for each field of each set, and shows every row with the
// CRD's validator, after the two steps the API server takes on a null before
// it validates (pruneNullsAsAPIServer).
//
// A refused row: the kind refuses the properties that leave the field out, by
// the field's path. With the field authored the kind builds them, into an
// object the validator accepts, whose spec is what the type encodes for the
// properties. The spec the type encodes for the properties without the field,
// in that object, is then what the kind would emit, and the validator refuses
// it for that field alone. Where the field is a list, an authored empty one is
// built, written as [] and accepted by the validator. Where it is of the
// second set, an authored empty string is built and the validator refuses the
// object for that field alone, unless the row says the kind refuses that too.
//
// A row that is not refused: the kind builds the properties that leave the
// field out, its object carries at the field what the row says, and the
// validator accepts the object.
func TestKindComponents_NullRequired(t *testing.T) {
	for _, kind := range nullRequiredKinds {
		t.Run(kind.component, func(t *testing.T) {
			file := filepath.Join(linkedModuleDir(t, kind.modulePath), filepath.FromSlash(kind.crd))
			props, required := crdSpecProperties(t, file, kind.crdVersion())
			var null, zero []string
			walkKindFields(kind.typ, func(f kindField) bool { return required[f.path] }, func(f kindField) {
				prop, ok := props[f.path]
				switch {
				case !ok || !f.writtenUnauthored():
				case f.encodesNull():
					if required[f.path] {
						null = append(null, f.path)
					}
				case crdRefusesZero(prop, f.field.Type) != "":
					zero = append(zero, f.path)
				}
			})
			slices.Sort(null)
			slices.Sort(zero)
			var answeredNull, answeredZero []string
			for _, row := range kind.rows {
				if row.writesZero {
					answeredZero = append(answeredZero, row.path)
					continue
				}
				answeredNull = append(answeredNull, row.path)
			}
			slices.Sort(answeredNull)
			slices.Sort(answeredZero)
			if !slices.Equal(answeredNull, null) {
				t.Errorf("required by the CRD and written as null by the type:\n  derived  %v\n  answered %v", null, answeredNull)
			}
			if !slices.Equal(answeredZero, zero) {
				t.Errorf("written by the type as a zero value the CRD refuses:\n  derived  %v\n  answered %v", zero, answeredZero)
			}

			crd := crdValidationOf(t, kind.modulePath, kind.crd, kind.crdVersion())
			for _, row := range kind.rows {
				t.Run(row.at, func(t *testing.T) {
					if (row.reason == "") == (row.why == "") {
						t.Fatalf("a row is refused, with what the refusal says, or is not, with the reason")
					}
					if row.reason == "" {
						kind.showNotRefused(t, crd, row)
						return
					}
					kind.showRefused(t, crd, row)
				})
			}
		})
	}
}

// showRefused shows a row the kind refuses.
func (k nullRequiredKind) showRefused(t *testing.T, crd crdValidation, row nullRequiredRow) {
	t.Helper()
	_, err := k.object(row.properties(t))
	if want := row.at + ": required (" + row.reason + ")"; err == nil || err.Error() != want {
		t.Errorf("without the field: %v, want the refusal %q", err, want)
	}

	with := row.properties(t)
	parent, name := fieldAt(t, with, row.at)
	if _, held := parent[name]; held {
		t.Fatalf("the properties hold %s; they must leave it out", row.at)
	}
	parent[name] = row.authored
	object, err := k.object(with)
	if err != nil {
		t.Fatalf("with the field authored: %v", err)
	}
	if !reflect.DeepEqual(object["spec"], any(k.encoded(t, with))) {
		t.Fatalf("with the field authored, the kind's spec is not what the type encodes:\n  kind %v\n  type %v", object["spec"], k.encoded(t, with))
	}
	if refused := crd.refusals(object); len(refused) > 0 {
		t.Fatalf("with the field authored, the CRD refuses the object: %v", refused)
	}

	// The decode folds field names, so the field under another spelling of
	// its key is authored too: the kind builds the same object.
	spelled := row.properties(t)
	parent, name = fieldAt(t, spelled, row.at)
	other := strings.ToUpper(name)
	if other == name {
		t.Fatalf("%s has no other spelling to author it under", name)
	}
	parent[other] = row.authored
	respelled, err := k.object(spelled)
	if err != nil {
		t.Fatalf("with the field authored as %s: %v, want it built", other, err)
	}
	if !reflect.DeepEqual(respelled["spec"], object["spec"]) {
		t.Errorf("with the field authored as %s, the kind's spec is not the one it builds for %s:\n  %s %v\n  %s %v", other, name, other, respelled["spec"], name, object["spec"])
	}

	// An authored empty list is a value: the kind writes it as one.
	if _, list := row.authored.([]any); list {
		empty := row.properties(t)
		parent, name = fieldAt(t, empty, row.at)
		parent[name] = []any{}
		object, err := k.object(empty)
		if err != nil {
			t.Fatalf("with an authored empty list: %v", err)
		}
		parent, name = fieldAt(t, object["spec"].(map[string]any), row.at)
		if written, ok := parent[name].([]any); !ok || len(written) != 0 {
			t.Errorf("with an authored empty list, the object carries %s as %v, want []", row.at, parent[name])
		}
		if refused := crd.refusals(object); len(refused) > 0 {
			t.Errorf("with an authored empty list, the CRD refuses the object: %v", refused)
		}
	}

	// An authored empty string is a value too: the kind builds it, and the
	// refusal of it is left to the API server. One kind reads the decoded
	// value, where the two cannot be told apart, and refuses both.
	if row.writesZero {
		empty := row.properties(t)
		parent, name = fieldAt(t, empty, row.at)
		parent[name] = ""
		object, err := k.object(empty)
		switch {
		case row.refusesEmpty:
			if want := row.at + ": required (" + row.reason + ")"; err == nil || err.Error() != want {
				t.Errorf("with an authored empty string: %v, want the refusal %q", err, want)
			}
		case err != nil:
			t.Fatalf("with an authored empty string: %v", err)
		default:
			if refused, want := crd.refusals(object), "spec."+row.at+" in body "; len(refused) != 1 || !strings.HasPrefix(refused[0], want) {
				t.Errorf("with an authored empty string, the CRD refuses %v, want one refusal of spec.%s", refused, row.at)
			}
		}
	}

	spec := k.encoded(t, row.properties(t))
	parent, name = fieldAt(t, spec, row.at)
	written, held := parent[name]
	switch {
	case !held:
		t.Fatalf("the type leaves %s out; the object would show the omission", row.at)
	case row.writesZero && written != "":
		t.Fatalf("the type writes %s as %v, want an empty string", row.at, written)
	case !row.writesZero && written != nil:
		t.Fatalf("the type writes %s as %v, want null", row.at, written)
	}
	object["spec"] = spec
	field := "spec." + row.at
	refused := crd.refusals(object)
	if len(refused) != 1 || !strings.HasPrefix(refused[0], field+" in body ") {
		t.Errorf("without the field, the CRD refuses %v, want one refusal of %s", refused, field)
	}
}

// showNotRefused shows a row the kind does not refuse.
func (k nullRequiredKind) showNotRefused(t *testing.T, crd crdValidation, row nullRequiredRow) {
	t.Helper()
	object, err := k.object(row.properties(t))
	if err != nil {
		t.Fatalf("without the field: %v, want it built (%s)", err, row.why)
	}
	spec, _ := object["spec"].(map[string]any)
	parent, name := fieldAt(t, spec, row.at)
	written, held := parent[name]
	raw, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("encode %s: %v", row.at, err)
	}
	if !held || string(raw) != row.built {
		t.Errorf("the object carries %s as %s (held: %v), want %s", row.at, raw, held, row.built)
	}
	if refused := crd.refusals(object); len(refused) > 0 {
		t.Errorf("the CRD refuses the object: %v; the row says it does not (%s)", refused, row.why)
	}
}
