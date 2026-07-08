package components

import (
	"fmt"

	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PostgresqlHandler handles OAM postgresql components.
type PostgresqlHandler struct{}

// CanHandle returns true for postgresql component type.
func (h *PostgresqlHandler) CanHandle(componentType string) bool {
	return componentType == "postgresql"
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
		"resources":         schemaResources(),
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

// ToApplicationConfig converts an OAM postgresql component to a PostgresqlConfig.
func (h *PostgresqlHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	config := &PostgresqlConfig{
		Name:      component.Name,
		Namespace: namespace,
	}

	props := component.Properties

	config.Provider = "cnpg"
	if provider, ok := props["provider"].(string); ok {
		switch provider {
		case "cnpg":
			config.Provider = provider
		default:
			return nil, errors.Errorf("unsupported postgresql provider %q, supported: cnpg", provider)
		}
	}

	config.Version = "16"
	if version, ok := props["version"].(string); ok {
		config.Version = version
	}

	config.StorageSize = "1Gi"
	if size, ok := props["storageSize"].(string); ok {
		config.StorageSize = size
	}
	config.explicitStorageSize = props["storageSize"] != nil

	config.Replicas = parseReplicas(props, 1)
	config.explicitReplicas = hasExplicitReplicas(props)

	if resources, ok := props["resources"].(map[string]any); ok {
		config.Resources = parseResources(resources)
	}
	config.explicitResources = resourceExplicitFlags(props)

	if backup, ok := props["backup"].(map[string]any); ok {
		if v, ok := backup["retentionPolicy"].(string); ok {
			config.BackupRetentionPolicy = v
		}
		if v, ok := backup["destinationPath"].(string); ok {
			config.BackupDestinationPath = v
		}
		if v, ok := backup["endpointURL"].(string); ok {
			config.BackupEndpointURL = v
		}
		if v, ok := backup["secretName"].(string); ok {
			config.BackupSecretName = v
		}
	}

	if monitoring, ok := props["monitoring"].(map[string]any); ok {
		if enabled, ok := monitoring["enabled"].(bool); ok {
			config.MonitoringEnabled = enabled
		}
		if cqList, ok := monitoring["customQueries"].([]any); ok {
			for i, cq := range cqList {
				cqMap, ok := cq.(map[string]any)
				if !ok {
					continue
				}
				name, _ := cqMap["name"].(string)
				key, _ := cqMap["key"].(string)
				if name == "" || key == "" {
					return nil, errors.Errorf("monitoring.customQueries[%d]: both 'name' and 'key' are required", i)
				}
				config.MonitoringCustomQueries = append(config.MonitoringCustomQueries, CustomQueryRef{Name: name, Key: key})
			}
		}
	}

	if pooler, ok := props["pooler"].(map[string]any); ok {
		if enabled, ok := pooler["enabled"].(bool); ok {
			config.PoolerEnabled = enabled
		}
		config.PoolerInstances = 3
		if v := pooler["instances"]; v != nil {
			n, ok := toInt32(v)
			if !ok {
				return nil, errors.Errorf("invalid pooler instances value: %v", v)
			}
			config.PoolerInstances = n
		}
		if typ, ok := pooler["type"].(string); ok {
			switch typ {
			case "rw", "ro":
				config.PoolerType = typ
			default:
				return nil, errors.Errorf("unsupported pooler type %q, supported: rw, ro", typ)
			}
		} else {
			config.PoolerType = "rw"
		}
		if mode, ok := pooler["poolMode"].(string); ok {
			switch PoolMode(mode) {
			case PoolModeSession, PoolModeTransaction, PoolModeStatement:
				config.PoolerPoolMode = PoolMode(mode)
			default:
				return nil, errors.Errorf("unsupported pooler pool mode %q, supported: session, transaction, statement", mode)
			}
		} else {
			config.PoolerPoolMode = PoolModeSession
		}
		if params, ok := pooler["parameters"].(map[string]any); ok {
			config.PoolerParameters = stringMap(params)
		}
	}

	if bootstrap, ok := props["bootstrap"].(map[string]any); ok {
		_, hasRecovery := bootstrap["recovery"].(map[string]any)
		_, hasPgBasebackup := bootstrap["pg_basebackup"].(map[string]any)
		if hasRecovery && hasPgBasebackup {
			return nil, errors.New("bootstrap: recovery and pg_basebackup are mutually exclusive")
		}
		if recovery, ok := bootstrap["recovery"].(map[string]any); ok {
			if source, ok := recovery["source"].(string); ok {
				config.BootstrapRecoverySource = source
			}
		}
		if pgbb, ok := bootstrap["pg_basebackup"].(map[string]any); ok {
			if source, ok := pgbb["source"].(string); ok {
				config.BootstrapPgBasebackupSource = source
			}
		}
	}

	if ecList, ok := props["externalClusters"].([]any); ok {
		for _, ec := range ecList {
			ecMap, ok := ec.(map[string]any)
			if !ok {
				continue
			}
			ext := ExternalCluster{}
			if n, ok := ecMap["name"].(string); ok {
				ext.Name = n
			}
			if bos, ok := ecMap["barmanObjectStore"].(map[string]any); ok {
				ext.BarmanObjectStore = bos
			}
			if cp, ok := ecMap["connectionParameters"].(map[string]any); ok {
				ext.ConnectionParameters = stringMap(cp)
			}
			if ext.Name != "" {
				config.ExternalClusters = append(config.ExternalClusters, ext)
			}
		}
	}

	if replication, ok := props["replication"].(map[string]any); ok {
		if sync, ok := replication["synchronous"].(map[string]any); ok {
			if method, ok := sync["method"].(string); ok {
				switch method {
				case "any", "first":
					config.SynchronousMethod = method
				default:
					return nil, errors.Errorf("unsupported replication synchronous method %q, supported: any, first", method)
				}
			}
			config.SynchronousNumber = 1
			if v := sync["number"]; v != nil {
				n, ok := toInt32(v)
				if !ok {
					return nil, errors.Errorf("invalid replication synchronous number value: %v", v)
				}
				config.SynchronousNumber = n
			}
			if config.SynchronousNumber < 0 {
				return nil, errors.Errorf("replication synchronous number must be >= 0, got %d", config.SynchronousNumber)
			}
			if dd, ok := sync["dataDurability"].(string); ok {
				switch dd {
				case "required", "preferred":
					config.SynchronousDataDurability = dd
				default:
					return nil, errors.Errorf("unsupported replication synchronous dataDurability %q, supported: required, preferred", dd)
				}
			}
		}
	}

	if pg, ok := props["postgresql"].(map[string]any); ok {
		if params, ok := pg["parameters"].(map[string]any); ok {
			config.PostgresqlParameters = stringMap(params)
		}
	}

	if img, ok := props["imageName"].(string); ok {
		config.ImageName = img
	}

	if im, ok := props["inheritedMetadata"].(map[string]any); ok {
		if labels, ok := im["labels"].(map[string]any); ok {
			config.InheritedLabels = stringMap(labels)
		}
		if annotations, ok := im["annotations"].(map[string]any); ok {
			config.InheritedAnnotations = stringMap(annotations)
		}
	}

	if roleList, ok := props["managedRoles"].([]any); ok {
		for i, r := range roleList {
			rMap, ok := r.(map[string]any)
			if !ok {
				return nil, errors.Errorf("managedRoles[%d]: must be an object", i)
			}
			name, _ := rMap["name"].(string)
			if name == "" {
				return nil, errors.Errorf("managedRoles[%d]: 'name' is required", i)
			}
			role := ManagedRoleConfig{Name: name}
			if ensure, ok := rMap["ensure"].(string); ok {
				switch ensure {
				case "present", "absent":
					role.Ensure = ensure
				default:
					return nil, errors.Errorf("managedRoles[%d]: unsupported ensure %q, supported: present, absent", i, ensure)
				}
			}
			if v, ok := rMap["login"].(bool); ok {
				role.Login = v
			}
			if v, ok := rMap["superuser"].(bool); ok {
				role.Superuser = v
			}
			if v, ok := rMap["createdb"].(bool); ok {
				role.CreateDB = v
			}
			if v, ok := rMap["createrole"].(bool); ok {
				role.CreateRole = v
			}
			if v, ok := rMap["replication"].(bool); ok {
				role.Replication = v
			}
			if v, ok := rMap["inherit"].(bool); ok {
				role.Inherit = &v
			}
			if v := rMap["connectionLimit"]; v != nil {
				n, ok := toInt32(v)
				if !ok {
					return nil, errors.Errorf("managedRoles[%d]: invalid connectionLimit value: %v", i, v)
				}
				n64 := int64(n)
				role.ConnectionLimit = &n64
			}
			if v, ok := rMap["passwordSecret"].(string); ok {
				role.PasswordSecret = v
			}
			if v, ok := rMap["comment"].(string); ok {
				role.Comment = v
			}
			if inRoles, ok := rMap["inRoles"].([]any); ok {
				for _, ir := range inRoles {
					if s, ok := ir.(string); ok {
						role.InRoles = append(role.InRoles, s)
					}
				}
			}
			config.ManagedRoles = append(config.ManagedRoles, role)
		}
	}

	if osMap, ok := props["objectStore"].(map[string]any); ok {
		dp, _ := osMap["destinationPath"].(string)
		if dp == "" {
			return nil, errors.New("objectStore: 'destinationPath' is required")
		}
		os := &ObjectStoreConfig{DestinationPath: dp}
		if eu, ok := osMap["endpointURL"].(string); ok {
			os.EndpointURL = eu
		}
		if sn, ok := osMap["secretName"].(string); ok {
			os.SecretName = sn
		}
		if rp, ok := osMap["retentionPolicy"].(string); ok {
			os.RetentionPolicy = rp
		}
		if sv, ok := osMap["serverName"].(string); ok {
			os.ServerName = sv
		}
		config.ObjectStore = os
	}

	if dbList, ok := props["databases"].([]any); ok {
		for i, d := range dbList {
			dMap, ok := d.(map[string]any)
			if !ok {
				return nil, errors.Errorf("databases[%d]: must be an object", i)
			}
			name, _ := dMap["name"].(string)
			if name == "" {
				return nil, errors.Errorf("databases[%d]: 'name' is required", i)
			}
			owner, _ := dMap["owner"].(string)
			if owner == "" {
				return nil, errors.Errorf("databases[%d]: 'owner' is required", i)
			}
			entry := DatabaseEntry{Name: name, Owner: owner}
			if ensure, ok := dMap["ensure"].(string); ok {
				switch ensure {
				case "present", "absent":
					entry.Ensure = ensure
				default:
					return nil, errors.Errorf("databases[%d]: unsupported ensure %q, supported: present, absent", i, ensure)
				}
			}
			if rp, ok := dMap["databaseReclaimPolicy"].(string); ok {
				switch rp {
				case "retain", "delete":
					entry.ReclaimPolicy = rp
				default:
					return nil, errors.Errorf("databases[%d]: unsupported databaseReclaimPolicy %q, supported: retain, delete", i, rp)
				}
			}
			if extList, ok := dMap["extensions"].([]any); ok {
				for j, e := range extList {
					eMap, ok := e.(map[string]any)
					if !ok {
						continue
					}
					extName, _ := eMap["name"].(string)
					if extName == "" {
						return nil, errors.Errorf("databases[%d].extensions[%d]: 'name' is required", i, j)
					}
					ext := DatabaseExtension{Name: extName}
					if ensure, ok := eMap["ensure"].(string); ok {
						switch ensure {
						case "present", "absent":
							ext.Ensure = ensure
						default:
							return nil, errors.Errorf("databases[%d].extensions[%d]: unsupported ensure %q, supported: present, absent", i, j, ensure)
						}
					}
					entry.Extensions = append(entry.Extensions, ext)
				}
			}
			config.Databases = append(config.Databases, entry)
		}
	}

	if affinity, ok := props["affinity"].(map[string]any); ok {
		config.AffinityEnabled = true
		config.AffinityEnablePodAntiAffinity = true
		if enabled, ok := affinity["enablePodAntiAffinity"].(bool); ok {
			config.AffinityEnablePodAntiAffinity = enabled
		}
		config.AffinityTopologyKey = "kubernetes.io/hostname"
		if tk, ok := affinity["topologyKey"].(string); ok {
			config.AffinityTopologyKey = tk
		}
		if paat, ok := affinity["podAntiAffinityType"].(string); ok {
			config.AffinityPodAntiAffinityType = paat
		}
		if ns, ok := affinity["nodeSelector"].(map[string]any); ok {
			config.AffinityNodeSelector = stringMap(ns)
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
	explicitResources   explicitResourceFlags
	explicitStorageSize bool
}

// ApplyPolicy applies defaults then enforces limits from the policy.
// Faithful port of crane's PostgresqlConfig.ApplyPolicy: enforces replicas, resources, storageSize only.
func (c *PostgresqlConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}

	c.Replicas = applyDefaultReplicas(c.Replicas, c.explicitReplicas, p.DefaultReplicas())
	if !c.explicitResources.cpuRequest {
		c.Resources.CPURequest = applyDefaultResource(c.Resources.CPURequest, p.DefaultCPURequest())
	}
	if !c.explicitResources.memoryRequest {
		c.Resources.MemoryRequest = applyDefaultResource(c.Resources.MemoryRequest, p.DefaultMemoryRequest())
	}
	if !c.explicitResources.cpuLimit {
		c.Resources.CPULimit = applyDefaultResource(c.Resources.CPULimit, p.DefaultCPULimit())
	}
	if !c.explicitResources.memoryLimit {
		c.Resources.MemoryLimit = applyDefaultResource(c.Resources.MemoryLimit, p.DefaultMemoryLimit())
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
	if err := enforceMaxResource(c.Resources.CPURequest, p.MaxCPU(), "cpu request"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.CPULimit, p.MaxCPU(), "cpu limit"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.MemoryRequest, p.MaxMemory(), "memory request"); err != nil {
		return err
	}
	if err := enforceMaxResource(c.Resources.MemoryLimit, p.MaxMemory(), "memory limit"); err != nil {
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

	opts := &kurecnpg.ClusterOptions{
		Instances:            c.Replicas,
		ImageName:            imageName,
		StorageSize:          c.StorageSize,
		InheritedLabels:      c.InheritedLabels,
		InheritedAnnotations: c.InheritedAnnotations,
		PostgresParams:       c.PostgresqlParameters,
	}

	if c.Resources.CPURequest != "" || c.Resources.MemoryRequest != "" ||
		c.Resources.CPULimit != "" || c.Resources.MemoryLimit != "" {
		opts.Resources = &kurecnpg.ResourceOptions{
			RequestsCPU:    c.Resources.CPURequest,
			RequestsMemory: c.Resources.MemoryRequest,
			LimitsCPU:      c.Resources.CPULimit,
			LimitsMemory:   c.Resources.MemoryLimit,
		}
	}

	if c.BackupRetentionPolicy != "" || c.BackupDestinationPath != "" {
		backup := &kurecnpg.BackupOptions{
			DestinationPath: c.BackupDestinationPath,
			EndpointURL:     c.BackupEndpointURL,
			RetentionPolicy: c.BackupRetentionPolicy,
		}
		if c.BackupSecretName != "" {
			backup.S3Credentials = &kurecnpg.S3CredentialOptions{SecretName: c.BackupSecretName}
		}
		opts.Backup = backup
	}

	if c.MonitoringEnabled {
		mon := &kurecnpg.MonitoringOptions{EnablePodMonitor: true}
		for _, cq := range c.MonitoringCustomQueries {
			mon.CustomQueriesConfigMap = append(mon.CustomQueriesConfigMap, kurecnpg.ConfigMapKeyRefOptions{
				Name: cq.Name,
				Key:  cq.Key,
			})
		}
		opts.Monitoring = mon
	}

	if c.BootstrapRecoverySource != "" || c.BootstrapPgBasebackupSource != "" {
		opts.Bootstrap = &kurecnpg.BootstrapOptions{
			RecoverySource:     c.BootstrapRecoverySource,
			PgBasebackupSource: c.BootstrapPgBasebackupSource,
		}
	}

	if len(c.ExternalClusters) > 0 {
		ecs := make([]kurecnpg.ExternalClusterOptions, len(c.ExternalClusters))
		for i, ec := range c.ExternalClusters {
			ecs[i] = kurecnpg.ExternalClusterOptions{
				Name:                 ec.Name,
				ConnectionParameters: ec.ConnectionParameters,
				BarmanObjectStore:    ec.BarmanObjectStore,
			}
		}
		opts.ExternalClusters = ecs
	}

	if c.SynchronousMethod != "" {
		opts.Synchronous = &kurecnpg.SynchronousOptions{
			Method:         c.SynchronousMethod,
			Number:         c.SynchronousNumber,
			DataDurability: c.SynchronousDataDurability,
		}
	}

	if c.ObjectStore != nil {
		opts.ObjectStoreName = app.Name
	}

	if c.AffinityEnabled {
		opts.Affinity = &kurecnpg.AffinityOptions{
			EnablePodAntiAffinity: c.AffinityEnablePodAntiAffinity,
			TopologyKey:           c.AffinityTopologyKey,
			PodAntiAffinityType:   c.AffinityPodAntiAffinityType,
			NodeSelector:          c.AffinityNodeSelector,
		}
	}

	if len(c.ManagedRoles) > 0 {
		roles := make([]kurecnpg.ManagedRoleOptions, len(c.ManagedRoles))
		for i, role := range c.ManagedRoles {
			roles[i] = kurecnpg.ManagedRoleOptions{
				Name:            role.Name,
				Ensure:          role.Ensure,
				Comment:         role.Comment,
				Login:           role.Login,
				Superuser:       role.Superuser,
				CreateDB:        role.CreateDB,
				CreateRole:      role.CreateRole,
				Replication:     role.Replication,
				Inherit:         role.Inherit,
				ConnectionLimit: role.ConnectionLimit,
				PasswordSecret:  role.PasswordSecret,
				InRoles:         role.InRoles,
			}
		}
		opts.ManagedRoles = roles
	}

	return kurecnpg.Cluster(&kurecnpg.ClusterConfig{
		Name:      app.Name,
		Namespace: app.Namespace,
		Options:   opts,
	})
}

func (c *PostgresqlConfig) createPooler(app *stack.Application) client.Object {
	pgBouncer := &kurecnpg.PgBouncerOptions{
		PoolMode: string(c.PoolerPoolMode),
	}
	if len(c.PoolerParameters) > 0 {
		pgBouncer.Parameters = c.PoolerParameters
	}
	return kurecnpg.Pooler(&kurecnpg.PoolerConfig{
		Name:      app.Name + "-pooler",
		Namespace: app.Namespace,
		Options: &kurecnpg.PoolerOptions{
			ClusterName: app.Name,
			Instances:   c.PoolerInstances,
			Type:        c.PoolerType,
			PgBouncer:   pgBouncer,
		},
	})
}

func (c *PostgresqlConfig) createObjectStore(app *stack.Application) client.Object {
	return kurecnpg.ObjectStore(&kurecnpg.ObjectStoreConfig{
		Name:      app.Name,
		Namespace: app.Namespace,
		Options: &kurecnpg.ObjectStoreOptions{
			DestinationPath: c.ObjectStore.DestinationPath,
			EndpointURL:     c.ObjectStore.EndpointURL,
			ServerName:      c.ObjectStore.ServerName,
			SecretName:      c.ObjectStore.SecretName,
			RetentionPolicy: c.ObjectStore.RetentionPolicy,
		},
	})
}

func (c *PostgresqlConfig) createDatabase(app *stack.Application, db DatabaseEntry) client.Object {
	exts := make([]kurecnpg.ExtensionOptions, len(db.Extensions))
	for i, ext := range db.Extensions {
		exts[i] = kurecnpg.ExtensionOptions{Name: ext.Name, Ensure: ext.Ensure}
	}
	return kurecnpg.Database(&kurecnpg.DatabaseConfig{
		Name:      app.Name + "-" + db.Name,
		Namespace: app.Namespace,
		Options: &kurecnpg.DatabaseOptions{
			ClusterName:   app.Name,
			DBName:        db.Name,
			Owner:         db.Owner,
			ReclaimPolicy: db.ReclaimPolicy,
			Ensure:        db.Ensure,
			Extensions:    exts,
		},
	})
}
