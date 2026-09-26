package components

import (
	"encoding/json"
	"fmt"
	"sort"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// barmanCloudPluginName is the CNPG plugin entry that archives WAL to a Barman Cloud
// ObjectStore. s3AccessKeyIDKey and s3SecretAccessKeyKey are the keys read from a
// backup/objectStore credentials Secret. kure's retired config-struct layer injected all
// three; launcher's output has always carried them, so they are written explicitly here.
const (
	barmanCloudPluginName = "barman-cloud.barmancloud.cnpg.io"
	s3AccessKeyIDKey      = "ACCESS_KEY_ID"
	s3SecretAccessKeyKey  = "SECRET_ACCESS_KEY"
)

// PostgresqlHandler handles OAM postgresql components.
type PostgresqlHandler struct{}

// CanHandle returns true for postgresql component type.
func (h *PostgresqlHandler) CanHandle(componentType string) bool {
	return componentType == "postgresql"
}

// Endpoints implements oam.EndpointProvider: a postgresql component's data-plane endpoints are
// the CNPG cluster's instance pods (labelled cnpg.io/cluster=<cluster name>, which equals the
// OAM component name) on the PostgreSQL port and, when the component declares a pooler, the
// pooler (PgBouncer) pods (labelled cnpg.io/poolerName=<cluster name>-pooler) on the same port.
// A downstream platform uses these to synthesize the target-side ingress allow(s) without
// hardcoding the operator selectors; a consumer that dials the pooler needs the second endpoint
// because pooler pods carry a different label set and are not matched by the direct-cluster
// selector.
func (h *PostgresqlHandler) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	eps := []netpol.Endpoint{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgClusterLabel: component.Name}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(postgresqlPort)},
	}}
	// The pooler resource name mirrors createPooler: <component name>-pooler.
	enabled, err := poolerEnabled(component)
	if err != nil {
		return nil, err
	}
	if enabled {
		eps = append(eps, netpol.Endpoint{
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{cnpgPoolerNameLabel: component.Name + "-pooler"}},
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
// rather than modeled field-by-field.
func (h *PostgresqlHandler) PropertySchema() map[string]oam.PropertySchema {
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
	return map[string]oam.PropertySchema{
		"provider":          {Type: oam.PropertyTypeString, Default: "cnpg", Enum: []any{"cnpg"}, Description: "Database provider (only cnpg is supported)."},
		"version":           {Type: oam.PropertyTypeString, Default: "16", Description: "PostgreSQL major version for the cluster image."},
		"storageSize":       {Type: oam.PropertyTypeString, Default: "1Gi", Description: "Persistent storage size requested for each instance."},
		"replicas":          {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of PostgreSQL instances in the cluster."},
		"imageName":         {Type: oam.PropertyTypeString, Description: "Override for the container image (defaults to the CloudNativePG image for the version)."},
		"resources":         schemaResources(false),
		"backup":            openObj("Barman object-store backup settings (retentionPolicy, destinationPath, endpointURL, secretName)."),
		"monitoring":        openObj("Monitoring settings, including the PodMonitor toggle and custom queries."),
		"pooler":            openObj("PgBouncer connection pooler settings (enabled, instances, type, poolMode, parameters)."),
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
}

// unsupportedResourceNames returns any resource name in rl other than cpu/memory, sorted for a
// deterministic error message. The shared `resources` schema (schemaResources) accepts any named
// resource — e.g. "ephemeral-storage", "nvidia.com/gpu" — for every workload kind, forwarded
// directly onto a real corev1.Container for the seven direct workload kinds. postgresql forwards
// cpu/memory only (cnpgResourceList): the CNPG builder it originally went through had fields for
// nothing else, and the restriction was kept when that builder was retired so that document
// validity did not change; lifting it is a separate, additive format change. Anything else would
// be silently dropped when createCluster builds the CNPG Cluster; rejecting it here, at parse
// time, surfaces that loudly instead.
func unsupportedResourceNames(rl corev1.ResourceList) []string {
	var names []string
	for name := range rl {
		if name != corev1.ResourceCPU && name != corev1.ResourceMemory {
			names = append(names, string(name))
		}
	}
	sort.Strings(names)
	return names
}

// ToApplicationConfig converts an OAM postgresql component to a PostgresqlConfig.
func (h *PostgresqlHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &PostgresqlConfig{
		Name:      component.Name,
		Namespace: namespace,
	}

	props := component.Properties

	// Every optional read below refuses a wrongly typed value by path instead of
	// treating it as absent (go-kure/launcher#512). Strings go through
	// parseRawStringField, which keeps an explicit "" as a value: the enums must
	// still reach their switch to refuse it, and the free-form strings were always
	// copied through as authored. A null is absence throughout.
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

	config.Version = "16"
	if version, present, err := parseRawStringField(props, "version", "version"); err != nil {
		return nil, err
	} else if present {
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
		r, err := parseResources(resources)
		if err != nil {
			return nil, errors.Wrap(err, "invalid resources configuration")
		}
		if extra := unsupportedResourceNames(r.Requests); len(extra) > 0 {
			return nil, errors.Errorf("resources.requests: postgresql only supports cpu/memory, got unsupported name(s) %v", extra)
		}
		if extra := unsupportedResourceNames(r.Limits); len(extra) > 0 {
			return nil, errors.Errorf("resources.limits: postgresql only supports cpu/memory, got unsupported name(s) %v", extra)
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

	dbList, _, err := parseObjectListField(props, "databases", "databases")
	if err != nil {
		return nil, err
	}
	for i, dMap := range dbList {
		label := fmt.Sprintf("databases[%d]", i)
		name, present, err := parseStringField(dMap, "name", label+".name")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'name' is required", label)
		}
		owner, present, err := parseStringField(dMap, "owner", label+".owner")
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.Errorf("%s: 'owner' is required", label)
		}
		entry := DatabaseEntry{Name: name, Owner: owner}
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
	// from the first. go-kure/launcher#394/#444 folded both null shapes into
	// authoredValue, which every helper (including parseObjectField) now
	// routes through, so parseObjectField reads both as absence directly —
	// the dedicated optionalObject wrapper this call used is removed.
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
		// is-absence contract #444 established for every other sub-field in this
		// block (and every other kind in the package).
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
}

// DatabaseExtension holds config for a single extension within a Database CR.
type DatabaseExtension struct {
	Name   string
	Ensure string
}

// PostgresqlConfig implements stack.ApplicationConfig for postgresql components.
type PostgresqlConfig struct {
	Name        string
	Namespace   string
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

// ApplyPolicy applies defaults then enforces limits from the policy.
// Faithful port of the downstream runtime's PostgresqlConfig.ApplyPolicy: enforces replicas, resources, storageSize only.
func (c *PostgresqlConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	c.Replicas = applyDefaultReplicas(c.Replicas, c.explicitReplicas, p.DefaultReplicas())
	if err := applyDefaultQuantity(&c.Resources.Requests, corev1.ResourceCPU, p.DefaultCPURequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Requests, corev1.ResourceMemory, p.DefaultMemoryRequest()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Limits, corev1.ResourceCPU, p.DefaultCPULimit()); err != nil {
		return err
	}
	if err := applyDefaultQuantity(&c.Resources.Limits, corev1.ResourceMemory, p.DefaultMemoryLimit()); err != nil {
		return err
	}
	// StorageSize precedence: authored > policy default > "1Gi" handler default.
	// The parse-time fallback is already "1Gi", so let a policy default override it
	// only when the user did not author a value.
	if !c.explicitStorageSize && p.DefaultStorageSize() != "" {
		c.StorageSize = p.DefaultStorageSize()
	}

	if err := enforceMaxReplicas(c.Replicas, p.MaxReplicas()); err != nil {
		return err
	}
	// Direct form kept deliberately (not enforceMaxResources): createCluster
	// forwards c.Resources' cpu/memory entries straight onto the Cluster spec
	// (cnpgResourceList, below) and never calls buildResourceRequirements, so
	// there is no intrinsic-default tier here for c.Resources to diverge from.
	if err := enforceMaxResource(quantityString(c.Resources.Requests, corev1.ResourceCPU), p.MaxCPU(), "cpu request"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Limits, corev1.ResourceCPU), p.MaxCPU(), "cpu limit"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Requests, corev1.ResourceMemory), p.MaxMemory(), "memory request"); err != nil {
		return err
	}
	if err := enforceMaxResource(quantityString(c.Resources.Limits, corev1.ResourceMemory), p.MaxMemory(), "memory limit"); err != nil {
		return err
	}
	if err := enforceMaxStorageSize(c.StorageSize, p.MaxStorageSize()); err != nil {
		return err
	}

	return nil
}

// Generate creates CloudNative-PG resources: Cluster, optional Pooler, ObjectStore, and Database CRs.
func (c *PostgresqlConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	cluster, err := c.createCluster(app)
	if err != nil {
		return nil, err
	}
	resources := []*client.Object{&cluster}

	if c.PoolerEnabled {
		pooler := c.createPooler(app)
		resources = append(resources, &pooler)
	}

	if c.ObjectStore != nil {
		os := c.createObjectStore(app)
		resources = append(resources, &os)
	}

	for _, db := range c.Databases {
		dbRes := c.createDatabase(app, db)
		resources = append(resources, &dbRes)
	}

	return resources, nil
}

func (c *PostgresqlConfig) createCluster(app *stack.Application) (client.Object, error) {
	imageName := c.ImageName
	if imageName == "" {
		imageName = fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", c.Version)
	}

	// kure's generated constructor carries identity only; every spec value below is
	// written here. enablePDB (from the instance count) and primaryUpdateStrategy
	// were injected by kure's retired config-struct layer and are kept explicit so
	// the emitted Cluster is unchanged.
	enablePDB := c.Replicas > 1
	cluster := kurecnpg.CreateCluster(app.Name, app.Namespace)
	cluster.Spec = cnpgv1.ClusterSpec{
		Instances:             int(c.Replicas),
		ImageName:             imageName,
		EnablePDB:             &enablePDB,
		PrimaryUpdateStrategy: cnpgv1.PrimaryUpdateStrategyUnsupervised,
		StorageConfiguration:  cnpgv1.StorageConfiguration{Size: c.StorageSize},
	}

	// Left nil when both maps are empty: a non-nil empty block renders
	// `inheritedMetadata: {}`.
	if len(c.InheritedLabels) > 0 || len(c.InheritedAnnotations) > 0 {
		cluster.Spec.InheritedMetadata = &cnpgv1.EmbeddedObjectMetadata{
			Labels:      c.InheritedLabels,
			Annotations: c.InheritedAnnotations,
		}
	}

	cluster.Spec.Resources = corev1.ResourceRequirements{
		Requests: cnpgResourceList(c.Resources.Requests),
		Limits:   cnpgResourceList(c.Resources.Limits),
	}

	if c.BackupRetentionPolicy != "" || c.BackupDestinationPath != "" {
		bos := &barmanapi.BarmanObjectStoreConfiguration{
			DestinationPath: c.BackupDestinationPath,
			EndpointURL:     c.BackupEndpointURL,
		}
		if c.BackupSecretName != "" {
			bos.AWS = s3Credentials(c.BackupSecretName)
		}
		cluster.Spec.Backup = &cnpgv1.BackupConfiguration{
			RetentionPolicy:   c.BackupRetentionPolicy,
			BarmanObjectStore: bos,
		}
	}

	if c.MonitoringEnabled {
		mon := &cnpgv1.MonitoringConfiguration{EnablePodMonitor: true} //nolint:staticcheck // SA1019: EnablePodMonitor is still the only upstream opt-in for operator-created PodMonitors
		for _, cq := range c.MonitoringCustomQueries {
			mon.CustomQueriesConfigMap = append(mon.CustomQueriesConfigMap, cnpgv1.ConfigMapKeySelector{
				LocalObjectReference: machineryapi.LocalObjectReference{Name: cq.Name},
				Key:                  cq.Key,
			})
		}
		cluster.Spec.Monitoring = mon
	}

	// ToApplicationConfig refuses both sources together; recovery still wins here
	// to match the retired builder should that guard ever move.
	switch {
	case c.BootstrapRecoverySource != "":
		cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			Recovery: &cnpgv1.BootstrapRecovery{Source: c.BootstrapRecoverySource},
		}
	case c.BootstrapPgBasebackupSource != "":
		cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			PgBaseBackup: &cnpgv1.BootstrapPgBaseBackup{Source: c.BootstrapPgBasebackupSource},
		}
	}

	if len(c.ExternalClusters) > 0 {
		ecs := make([]cnpgv1.ExternalCluster, 0, len(c.ExternalClusters))
		for _, ec := range c.ExternalClusters {
			ext := cnpgv1.ExternalCluster{
				Name:                 ec.Name,
				ConnectionParameters: ec.ConnectionParameters,
			}
			if ec.BarmanObjectStore != nil {
				bos, err := toBarmanObjectStore(ec.BarmanObjectStore)
				if err != nil {
					return nil, errors.Wrapf(err, "external cluster %q", ec.Name)
				}
				ext.BarmanObjectStore = bos
			}
			ecs = append(ecs, ext)
		}
		cluster.Spec.ExternalClusters = ecs
	}

	if len(c.PostgresqlParameters) > 0 {
		cluster.Spec.PostgresConfiguration.Parameters = c.PostgresqlParameters
	}
	if c.SynchronousMethod != "" {
		sync := &cnpgv1.SynchronousReplicaConfiguration{
			Method: cnpgv1.SynchronousReplicaConfigurationMethod(c.SynchronousMethod),
			Number: int(c.SynchronousNumber),
		}
		if c.SynchronousDataDurability != "" {
			sync.DataDurability = cnpgv1.DataDurabilityLevel(c.SynchronousDataDurability)
		}
		cluster.Spec.PostgresConfiguration.Synchronous = sync
	}

	// An objectStore component archives WAL through the barman-cloud plugin, pointed
	// at the ObjectStore createObjectStore emits under the same name.
	if c.ObjectStore != nil {
		isWALArchiver := true
		cluster.Spec.Plugins = []cnpgv1.PluginConfiguration{{
			Name:          barmanCloudPluginName,
			IsWALArchiver: &isWALArchiver,
			Parameters:    map[string]string{"objectStoreName": app.Name},
		}}
	}

	if c.AffinityEnabled {
		// Always written, false included: CNPG reads a nil enablePodAntiAffinity
		// as enabled.
		enablePAA := c.AffinityEnablePodAntiAffinity
		cluster.Spec.Affinity = cnpgv1.AffinityConfiguration{
			EnablePodAntiAffinity: &enablePAA,
			TopologyKey:           c.AffinityTopologyKey,
			PodAntiAffinityType:   c.AffinityPodAntiAffinityType,
			NodeSelector:          c.AffinityNodeSelector,
		}
	}

	for _, role := range c.ManagedRoles {
		rc := cnpgv1.RoleConfiguration{
			Name:        role.Name,
			Comment:     role.Comment,
			Login:       role.Login,
			Superuser:   role.Superuser,
			CreateDB:    role.CreateDB,
			CreateRole:  role.CreateRole,
			Replication: role.Replication,
			Inherit:     role.Inherit,
			InRoles:     role.InRoles,
		}
		// A nil limit stays 0 (omitempty) so the operator applies its own default.
		if role.ConnectionLimit != nil {
			rc.ConnectionLimit = *role.ConnectionLimit
		}
		// Only "absent" is written; "present" and unset leave the field for the
		// operator to default.
		if role.Ensure == "absent" {
			rc.Ensure = cnpgv1.EnsureAbsent
		}
		if role.PasswordSecret != "" {
			rc.PasswordSecret = &cnpgv1.LocalObjectReference{Name: role.PasswordSecret}
		}
		kurecnpg.AddClusterManagedRole(cluster, rc)
	}

	return cluster, nil
}

// cnpgResourceList copies the cpu and memory entries of rl, the only resource names the
// postgresql component forwards (see unsupportedResourceNames). It returns nil when neither
// is present so an unset side renders as absent.
func cnpgResourceList(rl corev1.ResourceList) corev1.ResourceList {
	var out corev1.ResourceList
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		q, ok := rl[name]
		if !ok {
			continue
		}
		if out == nil {
			out = corev1.ResourceList{}
		}
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

func (c *PostgresqlConfig) createPooler(app *stack.Application) client.Object {
	// pgbouncer is required upstream (no omitempty), so it is always set, empty
	// when nothing is authored.
	pgBouncer := &cnpgv1.PgBouncerSpec{}
	if c.PoolerPoolMode != "" {
		pgBouncer.PoolMode = cnpgv1.PgBouncerPoolMode(c.PoolerPoolMode)
	}
	if len(c.PoolerParameters) > 0 {
		pgBouncer.Parameters = c.PoolerParameters
	}

	// Anything other than "ro" is written as rw, the value launcher has always
	// emitted for an unset type.
	poolerType := cnpgv1.PoolerTypeRW
	if c.PoolerType == "ro" {
		poolerType = cnpgv1.PoolerTypeRO
	}

	pooler := kurecnpg.CreatePooler(app.Name+"-pooler", app.Namespace)
	pooler.Spec = cnpgv1.PoolerSpec{
		Cluster:   cnpgv1.LocalObjectReference{Name: app.Name},
		Type:      poolerType,
		PgBouncer: pgBouncer,
	}
	// A non-positive count is omitted so the operator default applies.
	if c.PoolerInstances > 0 {
		instances := c.PoolerInstances
		pooler.Spec.Instances = &instances
	}
	return pooler
}

func (c *PostgresqlConfig) createObjectStore(app *stack.Application) client.Object {
	store := kurecnpg.CreateObjectStore(app.Name, app.Namespace)
	store.Spec = barmanv1.ObjectStoreSpec{
		Configuration: barmanapi.BarmanObjectStoreConfiguration{
			DestinationPath: c.ObjectStore.DestinationPath,
			EndpointURL:     c.ObjectStore.EndpointURL,
			ServerName:      c.ObjectStore.ServerName,
		},
		RetentionPolicy: c.ObjectStore.RetentionPolicy,
	}
	if c.ObjectStore.SecretName != "" {
		kurecnpg.SetObjectStoreS3Credentials(store, s3Credentials(c.ObjectStore.SecretName))
	}
	return store
}

func (c *PostgresqlConfig) createDatabase(app *stack.Application, db DatabaseEntry) client.Object {
	database := kurecnpg.CreateDatabase(app.Name+"-"+db.Name, app.Namespace)
	database.Spec = cnpgv1.DatabaseSpec{
		ClusterRef: corev1.LocalObjectReference{Name: app.Name},
		Name:       db.Name,
		Owner:      db.Owner,
	}
	// Only the non-default values are written; "present"/"retain" and unset leave
	// the field for the operator to default.
	if db.Ensure == "absent" {
		database.Spec.Ensure = cnpgv1.EnsureAbsent
	}
	if db.ReclaimPolicy == "delete" {
		database.Spec.ReclaimPolicy = cnpgv1.DatabaseReclaimDelete
	}
	// An extension's ensure is always written — present unless authored absent —
	// matching what launcher has always emitted.
	for _, ext := range db.Extensions {
		ensure := cnpgv1.EnsurePresent
		if ext.Ensure == "absent" {
			ensure = cnpgv1.EnsureAbsent
		}
		kurecnpg.AddDatabaseExtension(database, cnpgv1.ExtensionSpec{
			DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: ext.Name, Ensure: ensure},
		})
	}
	return database
}
