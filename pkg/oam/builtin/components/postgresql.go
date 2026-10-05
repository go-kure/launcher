package components

import (
	"encoding/json"
	"fmt"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// cnpgClusterLabel is the label the CloudNativePG operator stamps on every instance pod of a
// Cluster (value = the Cluster name). cnpgPoolerNameLabel is the label it stamps on every pooler
// (PgBouncer) pod (value = the Pooler name). postgresqlPort is the PostgreSQL/PgBouncer port.
const (
	cnpgClusterLabel    = "cnpg.io/cluster"
	cnpgPoolerNameLabel = "cnpg.io/poolerName"
	postgresqlPort      = 5432
)

// barmanCloudPluginName is the name the barman-cloud plugin registers with CNPG, and so
// the Cluster's plugin entry that archives WAL to a Barman Cloud ObjectStore.
// s3AccessKeyIDKey and s3SecretAccessKeyKey are the keys read from a backup/objectStore
// credentials Secret. kure's retired config-struct layer injected all three; launcher's
// output has always carried them, so they are written explicitly here.
const (
	barmanCloudPluginName = "barman-cloud.cloudnative-pg.io"
	s3AccessKeyIDKey      = "ACCESS_KEY_ID"
	s3SecretAccessKeyKey  = "SECRET_ACCESS_KEY"
)

// PostgresqlRule is the component lowering rule for the "postgresql" component
// type (oam.ComponentLoweringRule). It re-expresses postgresql through the
// CloudNativePG kind components (docs/oam/design-operator-cr-components.md):
//
//   - a `cnpg-cluster` component named like the postgresql component, carrying
//     the Cluster spec postgresql used to write, minus the two values that
//     depend on the environment policy: spec.enablePDB and, when storageSize
//     was not authored, spec.storage.size;
//   - a `cnpg-objectstore` of the same name when objectStore is set, a member
//     of the same same-name sibling group as the Cluster;
//   - a `cnpg-pooler` named `<name>-pooler` when pooler.enabled is true;
//   - a `cnpg-database` named `<name>-<database>` for each databases entry;
//   - when the document already orders its components with a dependency
//     policy, a dependency policy making the pooler and the databases wait for
//     the Cluster (see dependencyPolicy).
//
// LowerComponent first runs postgresql's full parse (Parse, the former
// handler's sequence unchanged), so every input the handler refused is still
// refused with the same cause text. A refusal now surfaces through the
// lowering engine, so it names the component's type and document
// (`component "db" (type "postgresql") in document …: <cause>`).
//
// The two policy-dependent values are written after the policy ran, by a
// post-policy step the rule attaches to the Cluster (oam.Component.AfterPolicy),
// which runs before the authored traits: lowering runs before the environment
// policy, and the Cluster's policy defaults (instances, storage.size) apply only
// to values the document left unset. The step calls
// CnpgClusterConfig.ApplyPostgresqlDefaults.
//
// The authored traits are forwarded unchanged to the `cnpg-cluster`
// component; the annotations go to every component the rule emits.
type PostgresqlRule struct{}

// ComponentType claims the "postgresql" component type at the component
// lowering position. build.go registers this rule via RegisterComponentLowering
// instead of a dispatchable component handler.
func (PostgresqlRule) ComponentType() string { return "postgresql" }

// The properties a postgresql component names its Cluster and its ObjectStore
// with (go-kure/launcher#787), in place of the component name. They end in
// ObjectName, as a database's `objectName` and the workload types' properties
// do: each names the object alone, and `objectStoreName` was once a plugin
// parameter of the Cluster.
const (
	postgresqlClusterNameProperty     = "clusterObjectName"
	postgresqlObjectStoreNameProperty = "objectStoreObjectName"
)

// validatePostgresqlClusterName applies cnpg-cluster's Cluster-name rule
// (validateCnpgClusterName) to the name of the Cluster a postgresql component
// generates, which is also its cnpg.io/cluster selector value, and names this
// kind in the error. component is the component's name: the Cluster's own
// unless `clusterObjectName` or the naming hook names it otherwise. The rule is
// handed the resolved name only, so the refusal of a name that is not the
// component's names both places it can come from, as a kind component's does.
func validatePostgresqlClusterName(component, name string) error {
	if validateCnpgClusterName(name) == nil {
		return nil
	}
	what := "postgresql name"
	if name != component {
		what = postgresqlClusterNameProperty + ` (or the Naming hook's answer for role "` + string(oam.NameRolePostgresqlCluster) + `"): postgresql Cluster name`
	}
	return errors.Errorf("%s %q: must be a DNS-1035 label of at most %d characters (CloudNativePG rejects longer or dotted cluster names)", what, name, cnpgClusterNameMaxLength)
}

// postgresqlClusterMember returns the `cnpg-cluster` member the rule emits for
// component, named as the component is and carrying the name resolved for its
// Cluster (role oam.NameRolePostgresqlCluster): the authored `clusterObjectName`,
// else the consumer hook's, else the component name. The name is held to the
// Cluster-name rule whichever of the three it is. It is the one place the name
// is resolved, for the Cluster LowerComponent emits and for the selector
// EndpointsNamed builds, so the two ask the hook the same request. Every
// reference the rule writes to the Cluster reads the member's ObjectName.
func postgresqlClusterMember(lctx oam.LoweringContext, component *oam.Component) (oam.Component, error) {
	member := oam.Component{Name: component.Name, Type: "cnpg-cluster"}
	if err := nameRoleMember(component, lctx, &member, &CnpgClusterHandler{}, oam.NameRolePostgresqlCluster, postgresqlClusterNameProperty, "Cluster"); err != nil {
		return oam.Component{}, err
	}
	if err := validatePostgresqlClusterName(component.Name, member.ObjectName()); err != nil {
		return oam.Component{}, err
	}
	return member, nil
}

// Endpoints implements oam.EndpointProvider: a postgresql component's data-plane endpoints are
// the CNPG cluster's instance pods (labelled cnpg.io/cluster=<cluster name>: the OAM component
// name unless `clusterObjectName` names the Cluster) on the PostgreSQL port and, when the
// component declares a pooler, the
// pooler (PgBouncer) pods (labelled cnpg.io/poolerName=<pooler name>) on the same port.
// A downstream platform uses these to synthesize the target-side ingress allow(s) without
// hardcoding the operator selectors; a consumer that dials the pooler needs the second endpoint
// because pooler pods carry a different label set and are not matched by the direct-cluster
// selector.
//
// The endpoints are the authored component's: the pooler endpoint names the
// Pooler the rule emits (`poolerName`, else `<name>-pooler`), which carries its
// own endpoint as a cnpg-pooler as well. No consumer naming hook is consulted
// here; EndpointsNamed is this with one.
func (r PostgresqlRule) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	return r.EndpointsNamed(component, oam.LoweringContext{Component: component, Namer: oam.NewNameAllocator()})
}

// cnpgPoolerKind and cnpgDatabaseKind are the kinds the Pooler and Database
// names are resolved and claimed for.
var (
	cnpgPoolerKind   = schema.GroupKind{Group: cnpgv1.SchemeGroupVersion.Group, Kind: cnpgv1.PoolerKind}
	cnpgDatabaseKind = schema.GroupKind{Group: cnpgv1.SchemeGroupVersion.Group, Kind: cnpgv1.DatabaseKind}
)

// postgresqlPoolerName resolves the name of the Pooler the rule emits for
// component (role oam.NameRolePooler): the authored `poolerName`, else the
// consumer hook's, else `<name>-pooler`. It is the one place the name is
// resolved, for the Pooler LowerComponent emits and for the selector
// EndpointsNamed builds, so the two ask the hook the same request. It reads the
// raw property, as poolerEnabled does.
//
// The name is held to the Pooler's own rule here (validateCnpgPoolerName). An
// authored name and the hook's answer were held to it when they were resolved,
// so what this refuses is the default: the Cluster-name rule no longer bounds
// `<name>-pooler` once the Cluster is named apart from the component, and the
// refusal says what settles it.
func postgresqlPoolerName(lctx oam.LoweringContext, component *oam.Component) (string, error) {
	spec := oam.NameSpec{Role: oam.NameRolePooler, Kind: cnpgPoolerKind}
	authored, present, err := parseRawStringField(component.Properties, "poolerName", "poolerName")
	if err != nil {
		return "", err
	}
	if present {
		spec.Property, spec.Authored = "poolerName", authored
	}
	lctx.Component = component
	name, err := lctx.ResolveName(component.Name, "pooler", spec)
	if err != nil {
		return "", err
	}
	if err := validateCnpgPoolerName(name); err != nil {
		return "", errors.Errorf("%s; the default derives from the component name: set poolerName to name the Pooler otherwise", err.Error())
	}
	return name, nil
}

// EndpointsNamed implements oam.NamedEndpointProvider: Endpoints, with the
// cluster endpoint's selector carrying the name lctx resolves for the Cluster
// (postgresqlClusterMember) and the pooler endpoint's the one it resolves for
// the Pooler (postgresqlPoolerName): the consumer hook's where the caller set
// one and the author wrote no `clusterObjectName` or `poolerName`.
func (PostgresqlRule) EndpointsNamed(component *oam.Component, lctx oam.LoweringContext) ([]netpol.Endpoint, error) {
	// The selector carries the Cluster's name verbatim, resolved as
	// LowerComponent resolves it, so a name the Cluster cannot have is refused
	// here too, as cnpg-cluster refuses it.
	cluster, err := postgresqlClusterMember(lctx, component)
	if err != nil {
		return nil, err
	}
	eps := []netpol.Endpoint{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgClusterLabel: cluster.ObjectName()}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(postgresqlPort)},
	}}
	enabled, err := poolerEnabled(component)
	if err != nil {
		return nil, err
	}
	if enabled {
		// The selector value is the Pooler's name, resolved and held to the
		// Pooler's rule as LowerComponent resolves and holds it.
		poolerName, err := postgresqlPoolerName(lctx, component)
		if err != nil {
			return nil, errors.Wrapf(err, "pooler")
		}
		// The Pooler refers to the Cluster by the name resolved above, and a
		// Pooler that carries that name itself is refused where it is built
		// (CnpgPoolerConfig.validate): refused here in the same words.
		if err := refusePoolerNamedAsCluster(poolerName, cluster.ObjectName()); err != nil {
			return nil, err
		}
		eps = append(eps, netpol.Endpoint{
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgPoolerNameLabel: poolerName}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(postgresqlPort)},
		})
	}
	return eps, nil
}

// poolerEnabled reports whether the component declares an enabled pooler. It reads the raw
// property (mirroring ToApplicationConfig's pooler parse) so Endpoints stays a lightweight,
// side-effect-free view that does not require a full config build. A wrongly typed
// pooler or pooler.enabled is an error here too, not "no pooler".
func poolerEnabled(component *oam.Component) (bool, error) {
	pooler, present, err := parseObjectField(component.Properties, "pooler", "pooler")
	if err != nil || !present {
		return false, err
	}
	enabled, err := parseBoolField(pooler, "enabled", "pooler.enabled")
	if err != nil || enabled == nil {
		return false, err
	}
	return *enabled, nil
}

// PropertySchema declares the postgresql component's top-level user-facing
// properties. The CNPG-shaped sub-objects (backup, pooler, bootstrap, databases,
// …) are deep and K8s-adjacent, so they are kept open (AdditionalProperties)
// rather than modeled field-by-field, so a database's `objectName` is declared
// by no entry of its own. TestPostgresqlRule_PropertySchemaUnchanged pins it
// byte for byte.
func (PostgresqlRule) PropertySchema() map[string]oam.PropertySchema {
	// The CNPG-shaped sub-objects are kept open; each reuses the same open shape
	// but carries its own description, so a per-key helper supplies the prose.
	openObj := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	openArr := func(desc, itemDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type:        oam.PropertyTypeArray,
			Description: desc,
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: itemDesc},
		}
	}
	schema := map[string]oam.PropertySchema{
		"provider":          {Type: oam.PropertyTypeString, Default: "cnpg", Enum: []any{"cnpg"}, Description: "Database provider (only cnpg is supported)."},
		"version":           {Type: oam.PropertyTypeString, Default: "16", Description: "PostgreSQL major version for the cluster image. An empty string is refused; omit the property to take the default."},
		"storageSize":       {Type: oam.PropertyTypeString, Default: "1Gi", Description: "Persistent storage size requested for each instance."},
		"replicas":          {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of PostgreSQL instances in the cluster."},
		"imageName":         {Type: oam.PropertyTypeString, Description: "Override for the container image (defaults to the CloudNativePG image for the version)."},
		"resources":         schemaResources(false),
		"backup":            openObj("Barman object-store backup settings (retentionPolicy, destinationPath, endpointURL, secretName)."),
		"monitoring":        openObj("Monitoring settings, including the PodMonitor toggle and custom queries."),
		"pooler":            openObj("PgBouncer connection pooler settings (enabled, instances, type, poolMode, parameters)."),
		"poolerName":        {Type: oam.PropertyTypeString, Description: "Name of the generated Pooler, a DNS-1035 label used as written (default: the component name followed by -pooler). Only with pooler.enabled: true."},
		"bootstrap":         openObj("Cluster bootstrap source (recovery or pg_basebackup)."),
		"replication":       openObj("Synchronous replication settings (method, number, dataDurability)."),
		"postgresql":        openObj("PostgreSQL server settings, including the parameters map."),
		"inheritedMetadata": openObj("Labels and annotations propagated to the generated resources."),
		"objectStore":       openObj("Barman Cloud ObjectStore settings for backups (destinationPath, endpointURL, secretName, retentionPolicy, serverName)."),
		"affinity":          openObj("Pod affinity and anti-affinity scheduling settings."),
		"externalClusters":  openArr("External clusters referenced for bootstrap or replica sources.", "A single external cluster definition."),
		"managedRoles":      openArr("Database roles created and reconciled by the operator.", "A single managed role definition."),
		"databases":         openArr("Databases created and reconciled within the cluster.", "A single database definition."),
	}
	// The names of the Cluster and the ObjectStore (go-kure/launcher#787).
	schema[postgresqlClusterNameProperty] = oam.PropertySchema{Type: oam.PropertyTypeString, Description: "Name of the generated Cluster, in place of the component name, used as written; a DNS-1035 label of at most 50 characters. The Pooler's and each Database's reference to the Cluster and the endpoint selector follow it; the default names of the Pooler and the Databases keep the component name. CloudNativePG derives the Cluster's Services and Secrets from this name, and the default backup path moves with it. Renaming an existing Cluster creates a new one: the old one is pruned with its data unless it is protected."}
	schema[postgresqlObjectStoreNameProperty] = oam.PropertySchema{Type: oam.PropertyTypeString, Description: "Name of the generated ObjectStore, in place of the component name, used as written. The Cluster's backup plugin names the store by it. Only with objectStore."}
	return schema
}

// Parse is postgresql's full parse, the former handler's ToApplicationConfig
// sequence unchanged: it validates component as a postgresql component and
// returns what it authored, with postgresql's defaults filled in. LowerComponent
// runs it after it has named the Cluster; it is exported for the tests that pin
// the parse.
//
// No naming hook is consulted here: the Cluster is named by the authored
// `clusterObjectName`, else by the component, and that name is held to the
// Cluster-name rule first, as LowerComponent holds the one it resolved.
func (r PostgresqlRule) Parse(component *oam.Component) (*PostgresqlConfig, error) {
	if _, err := postgresqlClusterMember(oam.LoweringContext{}, component); err != nil {
		return nil, err
	}
	return r.parse(component)
}

// parse is Parse past the naming of the Cluster, which the caller has resolved
// and checked.
func (PostgresqlRule) parse(component *oam.Component) (*PostgresqlConfig, error) {
	config := &PostgresqlConfig{
		Name: component.Name,
	}

	props := component.Properties

	// Every optional read below refuses a wrongly typed value by path instead of
	// treating it as absent (go-kure/launcher#512). Optional strings go through
	// parseRawStringField, which keeps an explicit "" as a value: the enums must
	// still reach their switch to refuse it, version refuses it by name, and the
	// other free-form strings were always copied through as authored. The
	// exceptions are affinity.topologyKey, read with parseStringField as
	// parseAffinity reads it, and affinity.podAntiAffinityType, read through
	// authoredValue directly (see each); required strings use parseStringField
	// and a presence check. A null is absence, except as a resource quantity
	// (see parseResourceList).
	config.Provider = "cnpg"
	if provider, present, err := parseRawStringField(props, "provider", "provider"); err != nil {
		return nil, err
	} else if present {
		switch provider {
		case "cnpg":
			config.Provider = provider
		default:
			return nil, errors.Errorf("unsupported postgresql provider %q, supported: cnpg", provider)
		}
	}

	// An authored "" is refused rather than read as omitted: it is more likely a
	// templating slip than a request for the default, and kept as a value it
	// formatted the default image with an empty tag, which only the image pull
	// rejected (go-kure/launcher#539). Refused before imageName is read, so the
	// document's validity does not depend on whether imageName overrides the tag.
	config.Version = "16"
	if version, present, err := parseRawStringField(props, "version", "version"); err != nil {
		return nil, err
	} else if present {
		if version == "" {
			return nil, errors.Errorf("version: must not be empty; omit it to default to %q", "16")
		}
		config.Version = version
	}

	config.StorageSize = "1Gi"
	// explicitStorageSize takes the parser's presence, not `props[...] != nil`: a
	// typed nil is a non-nil interface and used to count as authored, which kept
	// the 1Gi fallback over a policy default.
	size, sizePresent, err := parseRawStringField(props, "storageSize", "storageSize")
	if err != nil {
		return nil, err
	}
	if sizePresent {
		config.StorageSize = size
	}
	config.explicitStorageSize = sizePresent

	replicas, replicasAuthored, err := parseReplicas(props, 1)
	if err != nil {
		return nil, err
	}
	config.Replicas = replicas
	config.explicitReplicas = replicasAuthored

	if resources, present, err := parseObjectField(props, "resources", "resources"); err != nil {
		return nil, err
	} else if present {
		// Every name the shared parser admits (cpu, memory, ephemeral-storage,
		// hugepages-<size>, qualified extended resources) is forwarded onto the
		// Cluster's spec.resources by cnpgResourceList (go-kure/launcher#484).
		r, err := parseResources(resources)
		if err != nil {
			return nil, errors.Wrap(err, "invalid resources configuration")
		}
		config.Resources = r
	}

	backup, present, err := parseObjectField(props, "backup", "backup")
	if err != nil {
		return nil, err
	}
	if present {
		for _, f := range []struct {
			key string
			dst *string
		}{
			{"retentionPolicy", &config.BackupRetentionPolicy},
			{"destinationPath", &config.BackupDestinationPath},
			{"endpointURL", &config.BackupEndpointURL},
			{"secretName", &config.BackupSecretName},
		} {
			v, present, err := parseRawStringField(backup, f.key, "backup."+f.key)
			if err != nil {
				return nil, err
			}
			if present {
				*f.dst = v
			}
		}
		// A retention policy is what makes the lowering build the Cluster's
		// backup.barmanObjectStore, whose destinationPath the CRD requires and
		// bounds. Left out, the Cluster would carry "" there, a value the author
		// did not write and the API server refuses. An authored empty one is a
		// value, and refusing it is left to the API server.
		if _, authored := authoredValue(backup, "destinationPath"); !authored && config.BackupRetentionPolicy != "" {
			return nil, errors.New("backup.destinationPath: required (the object store path backups and WAL are written to)")
		}
	}

	monitoring, present, err := parseObjectField(props, "monitoring", "monitoring")
	if err != nil {
		return nil, err
	}
	if present {
		enabled, err := parseBoolField(monitoring, "enabled", "monitoring.enabled")
		if err != nil {
			return nil, err
		}
		if enabled != nil {
			config.MonitoringEnabled = *enabled
		}
		cqList, _, err := parseObjectListField(monitoring, "customQueries", "monitoring.customQueries")
		if err != nil {
			return nil, err
		}
		for i, cqMap := range cqList {
			label := fmt.Sprintf("monitoring.customQueries[%d]", i)
			name, _, err := parseStringField(cqMap, "name", label+".name")
			if err != nil {
				return nil, err
			}
			key, _, err := parseStringField(cqMap, "key", label+".key")
			if err != nil {
				return nil, err
			}
			if name == "" || key == "" {
				return nil, errors.Errorf("%s: both 'name' and 'key' are required", label)
			}
			config.MonitoringCustomQueries = append(config.MonitoringCustomQueries, CustomQueryRef{Name: name, Key: key})
		}
	}

	pooler, present, err := parseObjectField(props, "pooler", "pooler")
	if err != nil {
		return nil, err
	}
	if present {
		enabled, err := parseBoolField(pooler, "enabled", "pooler.enabled")
		if err != nil {
			return nil, err
		}
		if enabled != nil {
			config.PoolerEnabled = *enabled
		}
		config.PoolerInstances = 3
		if v, present := authoredValue(pooler, "instances"); present {
			n, ok := toInt32(v)
			if !ok {
				return nil, errors.Errorf("invalid pooler instances value: %v", v)
			}
			config.PoolerInstances = n
		}
		config.PoolerType = "rw"
		if typ, present, err := parseRawStringField(pooler, "type", "pooler.type"); err != nil {
			return nil, err
		} else if present {
			switch typ {
			case "rw", "ro":
				config.PoolerType = typ
			default:
				return nil, errors.Errorf("unsupported pooler type %q, supported: rw, ro", typ)
			}
		}
		config.PoolerPoolMode = PoolModeSession
		if mode, present, err := parseRawStringField(pooler, "poolMode", "pooler.poolMode"); err != nil {
			return nil, err
		} else if present {
			switch PoolMode(mode) {
			case PoolModeSession, PoolModeTransaction, PoolModeStatement:
				config.PoolerPoolMode = PoolMode(mode)
			default:
				return nil, errors.Errorf("unsupported pooler pool mode %q, supported: session, transaction, statement", mode)
			}
		}
		if params, present, err := parseObjectField(pooler, "parameters", "pooler.parameters"); err != nil {
			return nil, err
		} else if present {
			if config.PoolerParameters, err = stringMapStrict(params, "pooler.parameters"); err != nil {
				return nil, err
			}
		}
	}
	// The name itself is read and resolved by postgresqlPoolerName; here only
	// its type, and that there is a Pooler to name.
	if _, present, err := parseRawStringField(props, "poolerName", "poolerName"); err != nil {
		return nil, err
	} else if present && !config.PoolerEnabled {
		return nil, errors.New("poolerName: names the Pooler, and pooler.enabled is not true; remove it, or enable the pooler")
	}

	bootstrap, present, err := parseObjectField(props, "bootstrap", "bootstrap")
	if err != nil {
		return nil, err
	}
	if present {
		recovery, hasRecovery, err := parseObjectField(bootstrap, "recovery", "bootstrap.recovery")
		if err != nil {
			return nil, err
		}
		pgbb, hasPgBasebackup, err := parseObjectField(bootstrap, "pg_basebackup", "bootstrap.pg_basebackup")
		if err != nil {
			return nil, err
		}
		if hasRecovery && hasPgBasebackup {
			return nil, errors.New("bootstrap: recovery and pg_basebackup are mutually exclusive")
		}
		if hasRecovery {
			if config.BootstrapRecoverySource, _, err = parseRawStringField(recovery, "source", "bootstrap.recovery.source"); err != nil {
				return nil, err
			}
		}
		if hasPgBasebackup {
			if config.BootstrapPgBasebackupSource, _, err = parseRawStringField(pgbb, "source", "bootstrap.pg_basebackup.source"); err != nil {
				return nil, err
			}
		}
	}

	ecList, _, err := parseObjectListField(props, "externalClusters", "externalClusters")
	if err != nil {
		return nil, err
	}
	for i, ecMap := range ecList {
		label := fmt.Sprintf("externalClusters[%d]", i)
		ext := ExternalCluster{}
		// A nameless entry is refused rather than skipped: it used to be dropped
		// from the cluster without a word, the same silent loss as a wrong type.
		name, present, err := parseStringField(ecMap, "name", label+".name")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'name' is required", label)
		}
		ext.Name = name
		if bos, present, err := parseObjectField(ecMap, "barmanObjectStore", label+".barmanObjectStore"); err != nil {
			return nil, err
		} else if present {
			// As for backup.destinationPath above: an authored barmanObjectStore
			// is lowered whole, and without the path the Cluster would carry "".
			// The map is decoded into the upstream type (toBarmanObjectStore),
			// which reads the key in any spelling, so any spelling is authored.
			if !authoredInAnySpelling(bos, "destinationPath") {
				return nil, errors.Errorf("%s.barmanObjectStore.destinationPath: required (the object store path the cluster's backups and WAL are read from)", label)
			}
			ext.BarmanObjectStore = bos
		}
		if cp, present, err := parseObjectField(ecMap, "connectionParameters", label+".connectionParameters"); err != nil {
			return nil, err
		} else if present {
			if ext.ConnectionParameters, err = stringMapStrict(cp, label+".connectionParameters"); err != nil {
				return nil, err
			}
		}
		config.ExternalClusters = append(config.ExternalClusters, ext)
	}

	replication, present, err := parseObjectField(props, "replication", "replication")
	if err != nil {
		return nil, err
	}
	if present {
		sync, present, err := parseObjectField(replication, "synchronous", "replication.synchronous")
		if err != nil {
			return nil, err
		}
		if present {
			if method, present, err := parseRawStringField(sync, "method", "replication.synchronous.method"); err != nil {
				return nil, err
			} else if present {
				switch method {
				case "any", "first":
					config.SynchronousMethod = method
				default:
					return nil, errors.Errorf("unsupported replication synchronous method %q, supported: any, first", method)
				}
			}
			config.SynchronousNumber = 1
			if v, present := authoredValue(sync, "number"); present {
				n, ok := toInt32(v)
				if !ok {
					return nil, errors.Errorf("invalid replication synchronous number value: %v", v)
				}
				config.SynchronousNumber = n
			}
			if config.SynchronousNumber < 0 {
				return nil, errors.Errorf("replication synchronous number must be >= 0, got %d", config.SynchronousNumber)
			}
			if dd, present, err := parseRawStringField(sync, "dataDurability", "replication.synchronous.dataDurability"); err != nil {
				return nil, err
			} else if present {
				switch dd {
				case "required", "preferred":
					config.SynchronousDataDurability = dd
				default:
					return nil, errors.Errorf("unsupported replication synchronous dataDurability %q, supported: required, preferred", dd)
				}
			}
		}
	}

	pg, present, err := parseObjectField(props, "postgresql", "postgresql")
	if err != nil {
		return nil, err
	}
	if present {
		if params, present, err := parseObjectField(pg, "parameters", "postgresql.parameters"); err != nil {
			return nil, err
		} else if present {
			if config.PostgresqlParameters, err = stringMapStrict(params, "postgresql.parameters"); err != nil {
				return nil, err
			}
		}
	}

	if config.ImageName, _, err = parseRawStringField(props, "imageName", "imageName"); err != nil {
		return nil, err
	}

	im, present, err := parseObjectField(props, "inheritedMetadata", "inheritedMetadata")
	if err != nil {
		return nil, err
	}
	if present {
		// A non-string value is refused, not dropped: these become label and
		// annotation values on the generated resources, where a dropped key is
		// a different cluster state from the one the author wrote
		// (go-kure/launcher#466).
		if labels, present, err := parseObjectField(im, "labels", "inheritedMetadata.labels"); err != nil {
			return nil, err
		} else if present {
			if config.InheritedLabels, err = stringMapStrict(labels, "inheritedMetadata.labels"); err != nil {
				return nil, err
			}
		}
		if annotations, present, err := parseObjectField(im, "annotations", "inheritedMetadata.annotations"); err != nil {
			return nil, err
		} else if present {
			if config.InheritedAnnotations, err = stringMapStrict(annotations, "inheritedMetadata.annotations"); err != nil {
				return nil, err
			}
		}
	}

	roleList, _, err := parseObjectListField(props, "managedRoles", "managedRoles")
	if err != nil {
		return nil, err
	}
	for i, rMap := range roleList {
		label := fmt.Sprintf("managedRoles[%d]", i)
		name, present, err := parseStringField(rMap, "name", label+".name")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'name' is required", label)
		}
		role := ManagedRoleConfig{Name: name}
		if ensure, present, err := parseRawStringField(rMap, "ensure", label+".ensure"); err != nil {
			return nil, err
		} else if present {
			switch ensure {
			case "present", "absent":
				role.Ensure = ensure
			default:
				return nil, errors.Errorf("%s: unsupported ensure %q, supported: present, absent", label, ensure)
			}
		}
		for _, f := range []struct {
			key string
			dst *bool
		}{
			{"login", &role.Login},
			{"superuser", &role.Superuser},
			{"createdb", &role.CreateDB},
			{"createrole", &role.CreateRole},
			{"replication", &role.Replication},
		} {
			v, err := parseBoolField(rMap, f.key, label+"."+f.key)
			if err != nil {
				return nil, err
			}
			if v != nil {
				*f.dst = *v
			}
		}
		if role.Inherit, err = parseBoolField(rMap, "inherit", label+".inherit"); err != nil {
			return nil, err
		}
		if v, present := authoredValue(rMap, "connectionLimit"); present {
			n, ok := toInt32(v)
			if !ok {
				return nil, errors.Errorf("%s: invalid connectionLimit value: %v", label, v)
			}
			if n == 0 {
				return nil, postgresqlZeroConnectionLimit(label)
			}
			n64 := int64(n)
			role.ConnectionLimit = &n64
		}
		if role.PasswordSecret, _, err = parseRawStringField(rMap, "passwordSecret", label+".passwordSecret"); err != nil {
			return nil, err
		}
		if role.Comment, _, err = parseRawStringField(rMap, "comment", label+".comment"); err != nil {
			return nil, err
		}
		if role.InRoles, _, err = parseStringList(rMap, "inRoles", label+".inRoles"); err != nil {
			return nil, err
		}
		config.ManagedRoles = append(config.ManagedRoles, role)
	}

	osMap, present, err := parseObjectField(props, "objectStore", "objectStore")
	if err != nil {
		return nil, err
	}
	if present {
		dp, present, err := parseStringField(osMap, "destinationPath", "objectStore.destinationPath")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.New("objectStore: 'destinationPath' is required")
		}
		os := &ObjectStoreConfig{DestinationPath: dp}
		for _, f := range []struct {
			key string
			dst *string
		}{
			{"endpointURL", &os.EndpointURL},
			{"secretName", &os.SecretName},
			{"retentionPolicy", &os.RetentionPolicy},
			{"serverName", &os.ServerName},
		} {
			if *f.dst, _, err = parseRawStringField(osMap, f.key, "objectStore."+f.key); err != nil {
				return nil, err
			}
		}
		config.ObjectStore = os
	}
	// The name itself is read and resolved by LowerComponent; here only its
	// type, and that there is an ObjectStore to name.
	if _, named, err := parseRawStringField(props, postgresqlObjectStoreNameProperty, postgresqlObjectStoreNameProperty); err != nil {
		return nil, err
	} else if named && config.ObjectStore == nil {
		return nil, errors.Errorf("%s: names the ObjectStore, and objectStore is not set; remove it, or set objectStore", postgresqlObjectStoreNameProperty)
	}

	dbList, _, err := parseObjectListField(props, "databases", "databases")
	if err != nil {
		return nil, err
	}
	firstDatabase := map[string]int{}
	for i, dMap := range dbList {
		label := fmt.Sprintf("databases[%d]", i)
		name, present, err := parseStringField(dMap, "name", label+".name")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'name' is required", label)
		}
		// Each entry generates one Database object named after it, so a
		// repeated name would be one object authored twice: the Flux build
		// cannot hold both, and kubectl keeps the last.
		if first, seen := firstDatabase[name]; seen {
			return nil, errors.Errorf("%s: repeats the name %q of databases[%d]; each database is one object", label, name, first)
		}
		firstDatabase[name] = i
		owner, present, err := parseStringField(dMap, "owner", label+".owner")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'owner' is required", label)
		}
		entry := DatabaseEntry{Name: name, Owner: owner}
		// An explicit "" is kept: it is an authored name, and is refused when
		// the name is resolved.
		if entry.ObjectName, entry.explicitObjectName, err = parseRawStringField(dMap, "objectName", label+".objectName"); err != nil {
			return nil, err
		}
		if ensure, present, err := parseRawStringField(dMap, "ensure", label+".ensure"); err != nil {
			return nil, err
		} else if present {
			switch ensure {
			case "present", "absent":
				entry.Ensure = ensure
			default:
				return nil, errors.Errorf("%s: unsupported ensure %q, supported: present, absent", label, ensure)
			}
		}
		if rp, present, err := parseRawStringField(dMap, "databaseReclaimPolicy", label+".databaseReclaimPolicy"); err != nil {
			return nil, err
		} else if present {
			switch rp {
			case "retain", "delete":
				entry.ReclaimPolicy = rp
			default:
				return nil, errors.Errorf("%s: unsupported databaseReclaimPolicy %q, supported: retain, delete", label, rp)
			}
		}
		extList, _, err := parseObjectListField(dMap, "extensions", label+".extensions")
		if err != nil {
			return nil, err
		}
		for j, eMap := range extList {
			extLabel := fmt.Sprintf("%s.extensions[%d]", label, j)
			extName, present, err := parseStringField(eMap, "name", extLabel+".name")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s: 'name' is required", extLabel)
			}
			ext := DatabaseExtension{Name: extName}
			if ensure, present, err := parseRawStringField(eMap, "ensure", extLabel+".ensure"); err != nil {
				return nil, err
			} else if present {
				switch ensure {
				case "present", "absent":
					ext.Ensure = ensure
				default:
					return nil, errors.Errorf("%s: unsupported ensure %q, supported: present, absent", extLabel, ensure)
				}
			}
			entry.Extensions = append(entry.Extensions, ext)
		}
		config.Databases = append(config.Databases, entry)
	}

	// Presence-reporting reads rather than bare comma-ok: an affinity block, or a
	// sub-field within it, authored with the wrong type is refused by name instead
	// of being silently discarded while the emitted cluster keeps a default
	// (go-kure/launcher#448). The defaults and the podAntiAffinityType validation
	// below match the shared parseAffinity in common.go, which this handler does
	// not call because postgresql carries its own AffinityConfig shape.
	//
	// parseObjectField: the envelope is where the two nil shapes used to
	// disagree, in opposite directions. An UNTYPED nil (`affinity:` with no
	// value) used to fail parseObjectField's assertion and become a hard error
	// — yet pkg/oam's own validatePropertyValue reads a null under an optional
	// property as absence and passes it, so that document validated and then
	// failed to convert. A TYPED nil (an unset map from a Go lowering rule)
	// satisfied the same assertion with a nil map, reported present, and
	// switched pod anti-affinity ON with every default — a scheduling
	// constraint nobody authored, from a value no document can distinguish
	// from the first. go-kure/launcher#394 and go-kure/launcher#444 folded
	// both null shapes into authoredValue, which every helper (including
	// parseObjectField) now routes through, so parseObjectField reads both
	// as absence directly — the dedicated optionalObject wrapper this call
	// used is removed.
	affinityRaw, affinityPresent, err := parseObjectField(props, "affinity", "affinity")
	if err != nil {
		return nil, err
	}
	if affinityPresent {
		config.AffinityEnabled = true
		config.AffinityEnablePodAntiAffinity = true
		config.AffinityTopologyKey = "kubernetes.io/hostname"
		config.AffinityPodAntiAffinityType = "preferred"

		enabled, err := parseBoolField(affinityRaw, "enablePodAntiAffinity", "affinity.enablePodAntiAffinity")
		if err != nil {
			return nil, err
		}
		if enabled != nil {
			config.AffinityEnablePodAntiAffinity = *enabled
		}

		topologyKey, present, err := parseStringField(affinityRaw, "topologyKey", "affinity.topologyKey")
		if err != nil {
			return nil, err
		}
		if present {
			config.AffinityTopologyKey = topologyKey
		}

		// Deliberately not parseStringField: it reports an explicit empty string as
		// absent, and an empty podAntiAffinityType has to reach the switch below to
		// be refused, the way parseAffinity refuses it. authoredValue, not a bare
		// map lookup: the raw lookup reports `podAntiAffinityType: null` as
		// present with a nil value, which then failed the string assertion —
		// treating a null sub-field as a type error, contradicting the nested-null-
		// is-absence contract go-kure/launcher#444 established for every other sub-field in this
		// block.
		if v, present := authoredValue(affinityRaw, "podAntiAffinityType"); present {
			paat, isString := v.(string)
			if !isString {
				return nil, errors.Errorf("affinity.podAntiAffinityType: must be a string, got %T", v)
			}
			config.AffinityPodAntiAffinityType = paat
		}
		switch config.AffinityPodAntiAffinityType {
		case "preferred", "required":
		default:
			return nil, errors.Errorf("affinity.podAntiAffinityType: invalid value %q: must be \"preferred\" or \"required\"", config.AffinityPodAntiAffinityType)
		}

		nodeSelector, present, err := parseObjectField(affinityRaw, "nodeSelector", "affinity.nodeSelector")
		if err != nil {
			return nil, err
		}
		if present {
			// Strict by design: this block's whole purpose is that a wrongly-typed
			// sub-field is refused by name rather than discarded, and a non-string
			// nodeSelector VALUE is exactly that. The former lenient reader dropped
			// it silently, so the reject-the-envelope check added above sat directly
			// on top of a silent discard of its own contents. A null value is still
			// absence and is left out of the selector.
			config.AffinityNodeSelector, err = stringMapStrict(nodeSelector, "affinity.nodeSelector")
			if err != nil {
				return nil, err
			}
		}
	}

	return config, nil
}

// PoolMode identifies the PgBouncer connection pooling mode.
type PoolMode string

const (
	PoolModeSession     PoolMode = "session"
	PoolModeTransaction PoolMode = "transaction"
	PoolModeStatement   PoolMode = "statement"
)

// CustomQueryRef references a ConfigMap containing custom monitoring queries.
type CustomQueryRef struct {
	Name string
	Key  string
}

// ExternalCluster defines an external cluster for bootstrap or replica sources.
type ExternalCluster struct {
	Name                 string
	BarmanObjectStore    map[string]any
	ConnectionParameters map[string]string
}

// ManagedRoleConfig holds config for a single CNPG managed role.
type ManagedRoleConfig struct {
	Name            string
	Ensure          string
	Login           bool
	Superuser       bool
	CreateDB        bool
	CreateRole      bool
	Replication     bool
	Inherit         *bool
	ConnectionLimit *int64
	PasswordSecret  string
	Comment         string
	InRoles         []string
}

// ObjectStoreConfig holds config for a CNPG ObjectStore CR.
type ObjectStoreConfig struct {
	// ObjectName is the name LowerComponent resolved for the ObjectStore (role
	// oam.NameRolePostgresqlObjectStore), which the Cluster's plugin entry
	// names. Empty for the component's name: Parse resolves none.
	ObjectName      string
	DestinationPath string
	EndpointURL     string
	SecretName      string
	RetentionPolicy string
	ServerName      string
}

// DatabaseEntry holds config for a single CNPG Database CR.
type DatabaseEntry struct {
	Name          string
	Owner         string
	Ensure        string
	ReclaimPolicy string
	Extensions    []DatabaseExtension
	// ObjectName is the authored `objectName`, the name of the Database object
	// when the author wrote one (explicitObjectName); the default is
	// `<component>-<Name>`.
	ObjectName         string
	explicitObjectName bool
}

// DatabaseExtension holds config for a single extension within a Database CR.
type DatabaseExtension struct {
	Name   string
	Ensure string
}

// PostgresqlConfig is what PostgresqlRule.Parse reads from a postgresql
// component, with postgresql's defaults filled in. The rule builds the specs of
// the components it emits from it.
type PostgresqlConfig struct {
	Name        string
	Provider    string
	Version     string
	StorageSize string
	Replicas    int32
	Resources   ResourceRequirements
	ImageName   string

	BackupRetentionPolicy string
	BackupDestinationPath string
	BackupEndpointURL     string
	BackupSecretName      string

	MonitoringEnabled       bool
	MonitoringCustomQueries []CustomQueryRef

	PoolerEnabled    bool
	PoolerInstances  int32
	PoolerType       string
	PoolerPoolMode   PoolMode
	PoolerParameters map[string]string

	BootstrapRecoverySource     string
	BootstrapPgBasebackupSource string
	ExternalClusters            []ExternalCluster

	SynchronousMethod         string
	SynchronousNumber         int32
	SynchronousDataDurability string

	PostgresqlParameters map[string]string

	InheritedLabels      map[string]string
	InheritedAnnotations map[string]string

	ManagedRoles []ManagedRoleConfig
	ObjectStore  *ObjectStoreConfig
	Databases    []DatabaseEntry

	AffinityEnabled               bool
	AffinityEnablePodAntiAffinity bool
	AffinityTopologyKey           string
	AffinityPodAntiAffinityType   string
	AffinityNodeSelector          map[string]string

	explicitReplicas    bool
	explicitStorageSize bool
}

// postgresqlZeroConnectionLimit refuses a managed role's connectionLimit of 0.
// CloudNativePG's connectionLimit is omitempty with a CRD default of -1, so a 0
// is dropped from the Cluster and the role gets no limit (cnpg-cluster refuses
// it the same way, cnpgClusterDefaultedZeroFields).
func postgresqlZeroConnectionLimit(label string) error {
	return errors.Errorf("%s.connectionLimit: 0 cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default -1, no limit); set login: false to keep the role from connecting", label)
}

// cnpgResourceList deep-copies every entry of rl — each name the shared parser admitted, not
// just cpu/memory (go-kure/launcher#484) — so the Cluster spec shares no Quantity state with
// the config. It returns nil for an empty or nil list so an unset side renders as absent.
func cnpgResourceList(rl corev1.ResourceList) corev1.ResourceList {
	if len(rl) == 0 {
		return nil
	}
	out := make(corev1.ResourceList, len(rl))
	for name, q := range rl {
		out[name] = q.DeepCopy()
	}
	return out
}

// s3Credentials references the access-key pair in secretName under the key names
// launcher has always emitted (ACCESS_KEY_ID / SECRET_ACCESS_KEY).
func s3Credentials(secretName string) *barmanapi.S3Credentials {
	return &barmanapi.S3Credentials{
		AccessKeyIDReference: &machineryapi.SecretKeySelector{
			LocalObjectReference: machineryapi.LocalObjectReference{Name: secretName},
			Key:                  s3AccessKeyIDKey,
		},
		SecretAccessKeyReference: &machineryapi.SecretKeySelector{
			LocalObjectReference: machineryapi.LocalObjectReference{Name: secretName},
			Key:                  s3SecretAccessKeyKey,
		},
	}
}

// toBarmanObjectStore converts an authored externalClusters[].barmanObjectStore map into the
// upstream Barman configuration by a JSON round-trip.
func toBarmanObjectStore(m map[string]any) (*barmanapi.BarmanObjectStoreConfiguration, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, "marshal barman object store")
	}
	var bos barmanapi.BarmanObjectStoreConfiguration
	if err := json.Unmarshal(data, &bos); err != nil {
		return nil, errors.Wrap(err, "unmarshal barman object store")
	}
	return &bos, nil
}
