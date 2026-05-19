package components_test

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestPostgresqlHandler_CanHandle(t *testing.T) {
	h := &components.PostgresqlHandler{}
	if !h.CanHandle("postgresql") {
		t.Error("expected CanHandle(postgresql) == true")
	}
	if h.CanHandle("webservice") {
		t.Error("expected CanHandle(webservice) == false")
	}
}

func TestPostgresqlHandler_Defaults(t *testing.T) {
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       "db",
		Type:       "postgresql",
		Properties: map[string]any{},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	pc := cfg.(*components.PostgresqlConfig)
	if pc.Provider != "cnpg" {
		t.Errorf("Provider: got %q, want %q", pc.Provider, "cnpg")
	}
	if pc.Version != "16" {
		t.Errorf("Version: got %q, want %q", pc.Version, "16")
	}
	if pc.StorageSize != "1Gi" {
		t.Errorf("StorageSize: got %q, want %q", pc.StorageSize, "1Gi")
	}
	if pc.Replicas != 1 {
		t.Errorf("Replicas: got %d, want 1", pc.Replicas)
	}
}

func TestPostgresqlHandler_InvalidProvider(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{"provider": "zalando"},
	}, "default")
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestPostgresqlHandler_PoolerValidation(t *testing.T) {
	h := &components.PostgresqlHandler{}

	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"pooler": map[string]any{"enabled": true, "type": "invalid"},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid pooler type")
	}

	_, err = h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"pooler": map[string]any{"enabled": true, "poolMode": "invalid"},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid pooler poolMode")
	}
}

func TestPostgresqlHandler_BootstrapMutualExclusion(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"bootstrap": map[string]any{
				"recovery":      map[string]any{"source": "src"},
				"pg_basebackup": map[string]any{"source": "src"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for mutually exclusive bootstrap options")
	}
}

func TestPostgresqlHandler_BootstrapRecovery(t *testing.T) {
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"bootstrap": map[string]any{
				"recovery": map[string]any{"source": "backup-src"},
			},
			"externalClusters": []any{
				map[string]any{
					"name": "backup-src",
					"barmanObjectStore": map[string]any{
						"destinationPath": "s3://backups/",
					},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	pc := cfg.(*components.PostgresqlConfig)
	if pc.BootstrapRecoverySource != "backup-src" {
		t.Errorf("BootstrapRecoverySource: got %q, want %q", pc.BootstrapRecoverySource, "backup-src")
	}
	if len(pc.ExternalClusters) != 1 {
		t.Fatalf("ExternalClusters: got %d, want 1", len(pc.ExternalClusters))
	}
	if pc.ExternalClusters[0].Name != "backup-src" {
		t.Errorf("ExternalClusters[0].Name: got %q", pc.ExternalClusters[0].Name)
	}
	if pc.ExternalClusters[0].BarmanObjectStore == nil {
		t.Error("ExternalClusters[0].BarmanObjectStore: expected non-nil")
	}
}

func TestPostgresqlHandler_BootstrapRecovery_ConnectionParameters(t *testing.T) {
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"bootstrap": map[string]any{
				"recovery": map[string]any{"source": "backup-cluster"},
			},
			"externalClusters": []any{
				map[string]any{
					"name": "backup-cluster",
					"connectionParameters": map[string]any{
						"host": "db.example.com",
					},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	pc := cfg.(*components.PostgresqlConfig)
	if pc.BootstrapRecoverySource != "backup-cluster" {
		t.Errorf("BootstrapRecoverySource: got %q", pc.BootstrapRecoverySource)
	}
	if len(pc.ExternalClusters) != 1 || pc.ExternalClusters[0].ConnectionParameters["host"] != "db.example.com" {
		t.Errorf("ExternalClusters[0].ConnectionParameters: got %v", pc.ExternalClusters[0].ConnectionParameters)
	}
}

func TestPostgresqlHandler_SynchronousValidation(t *testing.T) {
	h := &components.PostgresqlHandler{}

	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"replication": map[string]any{
				"synchronous": map[string]any{"method": "bad"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid synchronous method")
	}

	_, err = h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"replication": map[string]any{
				"synchronous": map[string]any{"method": "any", "dataDurability": "bad"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid dataDurability")
	}
}

func TestPostgresqlHandler_ObjectStore_MissingDestinationPath(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"objectStore": map[string]any{"endpointURL": "https://s3.example.com"},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for missing objectStore.destinationPath")
	}
}

func TestPostgresqlHandler_Databases_MissingName(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"databases": []any{
				map[string]any{"owner": "myuser"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for missing databases[0].name")
	}
}

func TestPostgresqlHandler_Databases_MissingOwner(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"databases": []any{
				map[string]any{"name": "mydb"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for missing databases[0].owner")
	}
}

func TestPostgresqlHandler_Databases_InvalidEnsure(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"databases": []any{
				map[string]any{"name": "mydb", "owner": "u", "ensure": "wrong"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid databases[0].ensure")
	}
}

func TestPostgresqlHandler_Databases_InvalidReclaimPolicy(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"databases": []any{
				map[string]any{"name": "mydb", "owner": "u", "databaseReclaimPolicy": "wrong"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid databases[0].databaseReclaimPolicy")
	}
}

func TestPostgresqlHandler_ManagedRoles_MissingName(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"managedRoles": []any{
				map[string]any{"login": true},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for missing managedRoles[0].name")
	}
}

func TestPostgresqlHandler_ManagedRoles_InvalidEnsure(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"managedRoles": []any{
				map[string]any{"name": "myrole", "ensure": "wrong"},
			},
		},
	}, "default")
	if err == nil {
		t.Error("expected error for invalid managedRoles[0].ensure")
	}
}

func TestPostgresqlHandler_MonitoringCustomQueries_MissingName(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"monitoring": map[string]any{
				"enabled": true,
				"customQueries": []any{
					map[string]any{"key": "queries"},
				},
			},
		},
	}, "default")
	if err == nil || !strings.Contains(err.Error(), "both 'name' and 'key' are required") {
		t.Errorf("expected error containing 'both name and key are required', got: %v", err)
	}
}

func TestPostgresqlHandler_MonitoringCustomQueries_MissingKey(t *testing.T) {
	h := &components.PostgresqlHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"monitoring": map[string]any{
				"enabled": true,
				"customQueries": []any{
					map[string]any{"name": "pg-stat"},
				},
			},
		},
	}, "default")
	if err == nil || !strings.Contains(err.Error(), "both 'name' and 'key' are required") {
		t.Errorf("expected error containing 'both name and key are required', got: %v", err)
	}
}

func TestPostgresqlHandler_ValidProperties(t *testing.T) {
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"version":     "15",
			"storageSize": "50Gi",
			"replicas":    float64(3),
			"imageName":   "ghcr.io/tensorchord/pgvecto-rs:pg16-v0.3.0",
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "500m", "memory": "512Mi"},
				"limits":   map[string]any{"cpu": "2", "memory": "4Gi"},
			},
			"postgresql": map[string]any{
				"parameters": map[string]any{"shared_buffers": "256MB"},
			},
			"inheritedMetadata": map[string]any{
				"labels":      map[string]any{"team": "backend"},
				"annotations": map[string]any{"backup.velero.io/backup-volumes": "data"},
			},
			"pooler": map[string]any{
				"enabled":    true,
				"instances":  float64(2),
				"type":       "ro",
				"poolMode":   "transaction",
				"parameters": map[string]any{"max_client_conn": "500"},
			},
			"backup": map[string]any{
				"destinationPath": "s3://my-bucket/backups",
				"endpointURL":     "https://s3.example.com",
				"secretName":      "backup-creds",
				"retentionPolicy": "30d",
			},
			"monitoring": map[string]any{
				"enabled": true,
				"customQueries": []any{
					map[string]any{"name": "pg-stat", "key": "queries"},
				},
			},
			"replication": map[string]any{
				"synchronous": map[string]any{
					"method":         "any",
					"number":         float64(1),
					"dataDurability": "required",
				},
			},
			"affinity": map[string]any{
				"enablePodAntiAffinity": true,
				"topologyKey":           "kubernetes.io/hostname",
				"podAntiAffinityType":   "required",
				"nodeSelector":          map[string]any{"workload-type": "database"},
			},
			"managedRoles": []any{
				map[string]any{
					"name":            "app_user",
					"login":           true,
					"passwordSecret":  "app-db-creds",
					"comment":         "App user",
					"connectionLimit": float64(10),
					"inRoles":         []any{"pg_read_all_data"},
				},
			},
			"objectStore": map[string]any{
				"destinationPath": "s3://my-bucket/postgres/",
				"serverName":      "my-server",
				"retentionPolicy": "30d",
			},
			"databases": []any{
				map[string]any{
					"name":                  "mydb",
					"owner":                 "myapp",
					"ensure":                "present",
					"databaseReclaimPolicy": "retain",
					"extensions": []any{
						map[string]any{"name": "pg_stat_statements"},
						map[string]any{"name": "pgvector", "ensure": "absent"},
					},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	pc := cfg.(*components.PostgresqlConfig)
	if pc.Version != "15" {
		t.Errorf("Version: got %q", pc.Version)
	}
	if pc.StorageSize != "50Gi" {
		t.Errorf("StorageSize: got %q", pc.StorageSize)
	}
	if pc.Replicas != 3 {
		t.Errorf("Replicas: got %d", pc.Replicas)
	}
	if pc.ImageName != "ghcr.io/tensorchord/pgvecto-rs:pg16-v0.3.0" {
		t.Errorf("ImageName: got %q", pc.ImageName)
	}
	if pc.Resources.CPURequest != "500m" {
		t.Errorf("CPURequest: got %q", pc.Resources.CPURequest)
	}
	if pc.PostgresqlParameters["shared_buffers"] != "256MB" {
		t.Errorf("PostgresqlParameters: got %v", pc.PostgresqlParameters)
	}
	if pc.InheritedLabels["team"] != "backend" {
		t.Errorf("InheritedLabels: got %v", pc.InheritedLabels)
	}
	if !pc.PoolerEnabled || pc.PoolerType != "ro" {
		t.Errorf("Pooler: enabled=%v type=%q", pc.PoolerEnabled, pc.PoolerType)
	}
	if pc.BackupDestinationPath != "s3://my-bucket/backups" {
		t.Errorf("BackupDestinationPath: got %q", pc.BackupDestinationPath)
	}
	if !pc.MonitoringEnabled || len(pc.MonitoringCustomQueries) != 1 {
		t.Errorf("Monitoring: enabled=%v queries=%d", pc.MonitoringEnabled, len(pc.MonitoringCustomQueries))
	}
	if pc.SynchronousMethod != "any" || pc.SynchronousDataDurability != "required" {
		t.Errorf("Synchronous: method=%q durability=%q", pc.SynchronousMethod, pc.SynchronousDataDurability)
	}
	if !pc.AffinityEnabled || pc.AffinityTopologyKey != "kubernetes.io/hostname" {
		t.Errorf("Affinity: enabled=%v topologyKey=%q", pc.AffinityEnabled, pc.AffinityTopologyKey)
	}
	if len(pc.ManagedRoles) != 1 || pc.ManagedRoles[0].Name != "app_user" {
		t.Errorf("ManagedRoles: %v", pc.ManagedRoles)
	}
	if pc.ObjectStore == nil || pc.ObjectStore.ServerName != "my-server" {
		t.Errorf("ObjectStore: %v", pc.ObjectStore)
	}
	if len(pc.Databases) != 1 || len(pc.Databases[0].Extensions) != 2 {
		t.Errorf("Databases: %v", pc.Databases)
	}
}

func TestPostgresqlConfig_Generate_WithResources(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "500m", "memory": "1Gi"},
			"limits":   map[string]any{"cpu": "2", "memory": "4Gi"},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster, ok := (*objs[0]).(*cnpgv1.Cluster)
	if !ok {
		t.Fatalf("expected *cnpgv1.Cluster")
	}
	if cluster.Spec.Resources.Requests == nil {
		t.Error("expected non-nil resources.requests")
	}
}

func TestPostgresqlConfig_Generate_WithBackup(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"backup": map[string]any{
			"destinationPath": "s3://my-bucket/backups",
			"retentionPolicy": "30d",
			"secretName":      "backup-creds",
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Backup == nil {
		t.Fatal("expected non-nil Backup")
	}
	if cluster.Spec.Backup.RetentionPolicy != "30d" {
		t.Errorf("Backup.RetentionPolicy: got %q", cluster.Spec.Backup.RetentionPolicy)
	}
}

func TestPostgresqlConfig_Generate_WithMonitoring(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"monitoring": map[string]any{
			"enabled": true,
			"customQueries": []any{
				map[string]any{"name": "pg-stat", "key": "queries"},
			},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Monitoring == nil {
		t.Fatal("expected non-nil Monitoring")
	}
	if len(cluster.Spec.Monitoring.CustomQueriesConfigMap) != 1 {
		t.Errorf("customQueriesConfigMap: got %d", len(cluster.Spec.Monitoring.CustomQueriesConfigMap))
	}
}

func TestPostgresqlConfig_Generate_WithSynchronous(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"replication": map[string]any{
			"synchronous": map[string]any{
				"method":         "any",
				"number":         float64(2),
				"dataDurability": "required",
			},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.PostgresConfiguration.Synchronous == nil {
		t.Fatal("expected non-nil Synchronous")
	}
	if cluster.Spec.PostgresConfiguration.Synchronous.Number != 2 {
		t.Errorf("Synchronous.Number: got %d", cluster.Spec.PostgresConfiguration.Synchronous.Number)
	}
}

func TestPostgresqlConfig_Generate_WithManagedRoles(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"managedRoles": []any{
			map[string]any{
				"name":  "app_user",
				"login": true,
			},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Managed == nil || len(cluster.Spec.Managed.Roles) != 1 {
		t.Errorf("expected 1 managed role, got %v", cluster.Spec.Managed)
	}
}

func TestPostgresqlConfig_Generate_WithAffinity(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"affinity": map[string]any{
			"enablePodAntiAffinity": true,
			"topologyKey":           "kubernetes.io/hostname",
			"podAntiAffinityType":   "required",
			"nodeSelector":          map[string]any{"workload-type": "database"},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Affinity.TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("Affinity.TopologyKey: got %q", cluster.Spec.Affinity.TopologyKey)
	}
}

func TestPostgresqlConfig_Generate_WithBootstrap(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"bootstrap": map[string]any{
			"recovery": map[string]any{"source": "backup-cluster"},
		},
		"externalClusters": []any{
			map[string]any{"name": "backup-cluster"},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cluster := (*objs[0]).(*cnpgv1.Cluster)
	if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
		t.Fatal("expected non-nil Bootstrap.Recovery")
	}
	if cluster.Spec.Bootstrap.Recovery.Source != "backup-cluster" {
		t.Errorf("Bootstrap.Recovery.Source: got %q", cluster.Spec.Bootstrap.Recovery.Source)
	}
}

func newPostgresqlApp(t *testing.T, props map[string]any) *components.PostgresqlConfig {
	t.Helper()
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       "db",
		Type:       "postgresql",
		Properties: props,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg.(*components.PostgresqlConfig)
}

func generatePostgresql(t *testing.T, pc *components.PostgresqlConfig) []*client.Object {
	t.Helper()
	app := stack.NewApplication("db", "default", pc)
	objs, err := pc.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return objs
}

func TestPostgresqlConfig_Generate_Minimal(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{})
	objs := generatePostgresql(t, pc)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if _, ok := (*objs[0]).(*cnpgv1.Cluster); !ok {
		t.Errorf("expected *cnpgv1.Cluster, got %T", *objs[0])
	}
}

func TestPostgresqlConfig_Generate_WithPooler(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"pooler": map[string]any{"enabled": true, "instances": float64(2), "type": "rw"},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	if _, ok := (*objs[0]).(*cnpgv1.Cluster); !ok {
		t.Errorf("objs[0]: expected *cnpgv1.Cluster, got %T", *objs[0])
	}
	if _, ok := (*objs[1]).(*cnpgv1.Pooler); !ok {
		t.Errorf("objs[1]: expected *cnpgv1.Pooler, got %T", *objs[1])
	}
}

func TestPostgresqlConfig_Generate_WithObjectStore(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"objectStore": map[string]any{
			"destinationPath": "s3://my-bucket/postgres/",
			"secretName":      "backup-creds",
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}

	cluster, ok := (*objs[0]).(*cnpgv1.Cluster)
	if !ok {
		t.Fatalf("objs[0]: expected *cnpgv1.Cluster, got %T", *objs[0])
	}
	if len(cluster.Spec.Plugins) == 0 {
		t.Fatal("cluster.Spec.Plugins: expected at least 1 entry")
	}
	if cluster.Spec.Plugins[0].Name != "barman-cloud.barmancloud.cnpg.io" {
		t.Errorf("Plugins[0].Name: got %q", cluster.Spec.Plugins[0].Name)
	}
	if cluster.Spec.Plugins[0].Parameters["objectStoreName"] != "db" {
		t.Errorf("Plugins[0].Parameters[objectStoreName]: got %q", cluster.Spec.Plugins[0].Parameters["objectStoreName"])
	}

	if _, ok := (*objs[1]).(*barmanv1.ObjectStore); !ok {
		t.Errorf("objs[1]: expected *barmanv1.ObjectStore, got %T", *objs[1])
	}
}

func TestPostgresqlConfig_Generate_WithDatabase(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"databases": []any{
			map[string]any{"name": "mydb", "owner": "myapp"},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}

	db, ok := (*objs[1]).(*cnpgv1.Database)
	if !ok {
		t.Fatalf("objs[1]: expected *cnpgv1.Database, got %T", *objs[1])
	}
	if db.Name != "db-mydb" {
		t.Errorf("Database.Name: got %q, want %q", db.Name, "db-mydb")
	}
	if db.Spec.ClusterRef.Name != "db" {
		t.Errorf("Database.Spec.ClusterRef.Name: got %q, want %q", db.Spec.ClusterRef.Name, "db")
	}
	if db.Spec.Name != "mydb" {
		t.Errorf("Database.Spec.Name: got %q, want %q", db.Spec.Name, "mydb")
	}
	if db.Spec.Owner != "myapp" {
		t.Errorf("Database.Spec.Owner: got %q, want %q", db.Spec.Owner, "myapp")
	}
}

func TestPostgresqlConfig_Generate_CombinedOrder(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"pooler": map[string]any{"enabled": true},
		"objectStore": map[string]any{
			"destinationPath": "s3://my-bucket/postgres/",
		},
		"databases": []any{
			map[string]any{"name": "mydb", "owner": "myapp"},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 4 {
		t.Fatalf("expected 4 objects, got %d", len(objs))
	}
	if _, ok := (*objs[0]).(*cnpgv1.Cluster); !ok {
		t.Errorf("objs[0]: expected *cnpgv1.Cluster, got %T", *objs[0])
	}
	if _, ok := (*objs[1]).(*cnpgv1.Pooler); !ok {
		t.Errorf("objs[1]: expected *cnpgv1.Pooler, got %T", *objs[1])
	}
	if _, ok := (*objs[2]).(*barmanv1.ObjectStore); !ok {
		t.Errorf("objs[2]: expected *barmanv1.ObjectStore, got %T", *objs[2])
	}
	if _, ok := (*objs[3]).(*cnpgv1.Database); !ok {
		t.Errorf("objs[3]: expected *cnpgv1.Database, got %T", *objs[3])
	}
}

func TestPostgresqlConfig_ApplyPolicy_EnforcesStorageSize(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{"storageSize": "10Gi"})
	enforceable := stack.ApplicationConfig(pc).(oam.Enforceable)
	p := &stubPolicy{maxStorageSize: "5Gi"}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when storageSize exceeds max")
	}
}

func TestPostgresqlConfig_ApplyPolicy_EnforcesReplicas(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{"replicas": float64(5)})
	enforceable := stack.ApplicationConfig(pc).(oam.Enforceable)
	p := &stubPolicy{maxReplicas: int32ptr(3)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestPostgresqlConfig_ApplyPolicy_DefaultsReplicas(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{})
	enforceable := stack.ApplicationConfig(pc).(oam.Enforceable)
	p := &stubPolicy{defaultReplicas: int32ptr(2)}
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	if pc.Replicas != 2 {
		t.Errorf("Replicas after defaulting: got %d, want 2", pc.Replicas)
	}
}

func TestPostgresqlConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{})
	enforceable := stack.ApplicationConfig(pc).(oam.Enforceable)
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy should be a no-op, got: %v", err)
	}
}

func TestPostgresqlConfig_Generate_WithPoolerParameters(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"pooler": map[string]any{
			"enabled": true,
			"parameters": map[string]any{
				"max_client_conn":   "100",
				"default_pool_size": "20",
			},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	pooler, ok := (*objs[1]).(*cnpgv1.Pooler)
	if !ok {
		t.Fatalf("objs[1]: expected *cnpgv1.Pooler, got %T", *objs[1])
	}
	if pooler.Spec.PgBouncer == nil || len(pooler.Spec.PgBouncer.Parameters) == 0 {
		t.Error("expected non-empty PgBouncer.Parameters")
	}
}

func TestPostgresqlConfig_Generate_WithDatabaseExtensions(t *testing.T) {
	pc := newPostgresqlApp(t, map[string]any{
		"databases": []any{
			map[string]any{
				"name":  "mydb",
				"owner": "myapp",
				"extensions": []any{
					map[string]any{"name": "postgis", "ensure": "present"},
					map[string]any{"name": "uuid-ossp", "ensure": "present"},
				},
			},
		},
	})
	objs := generatePostgresql(t, pc)
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	db, ok := (*objs[1]).(*cnpgv1.Database)
	if !ok {
		t.Fatalf("objs[1]: expected *cnpgv1.Database, got %T", *objs[1])
	}
	if len(db.Spec.Extensions) != 2 {
		t.Errorf("Extensions: expected 2, got %d", len(db.Spec.Extensions))
	}
}
