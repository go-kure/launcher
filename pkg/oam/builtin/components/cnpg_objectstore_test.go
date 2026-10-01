package components_test

import (
	"reflect"
	"strings"
	"testing"

	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// minimalObjectStore is the smallest cnpg-objectstore the ObjectStore CRD
// admits.
func minimalObjectStore() map[string]any {
	return map[string]any{"configuration": map[string]any{"destinationPath": "s3://backups/db"}}
}

func cnpgObjectStoreErr(t *testing.T, props map[string]any) error {
	t.Helper()
	_, err := (&components.CnpgObjectStoreHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-store", Type: "cnpg-objectstore", Properties: props}, "data")
	return err
}

func newCnpgObjectStore(t *testing.T, props map[string]any) *components.CnpgObjectStoreConfig {
	t.Helper()
	cfg, err := (&components.CnpgObjectStoreHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-store", Type: "cnpg-objectstore", Properties: props}, "data")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg.(*components.CnpgObjectStoreConfig)
}

func generateCnpgObjectStore(t *testing.T, c *components.CnpgObjectStoreConfig) *barmanv1.ObjectStore {
	t.Helper()
	objs, err := c.Generate(stack.NewApplication("db-store", "data", c))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	store, ok := (*objs[0]).(*barmanv1.ObjectStore)
	if !ok {
		t.Fatalf("Generate: got %T, want *barmanv1.ObjectStore", *objs[0])
	}
	return store
}

func TestCnpgObjectStoreHandler_CanHandle(t *testing.T) {
	h := &components.CnpgObjectStoreHandler{}
	if !h.CanHandle("cnpg-objectstore") {
		t.Error("CanHandle(cnpg-objectstore) = false")
	}
	if h.CanHandle("cnpg-cluster") {
		t.Error("CanHandle(cnpg-cluster) = true")
	}
}

// TestCnpgObjectStoreHandler_MinimalWritesNoOpinions: the smallest admitted
// component yields an ObjectStore carrying only identity and the destination
// path — none of the credential key names postgresql writes.
func TestCnpgObjectStoreHandler_MinimalWritesNoOpinions(t *testing.T) {
	c := newCnpgObjectStore(t, minimalObjectStore())
	if err := c.ApplyPolicy(&stubPolicy{maxCPU: "1", maxMemory: "1Gi"}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	store := generateCnpgObjectStore(t, c)
	if store.Name != "db-store" || store.Namespace != "data" {
		t.Errorf("identity = %s/%s, want data/db-store", store.Namespace, store.Name)
	}
	if store.APIVersion != "barmancloud.cnpg.io/v1" || store.Kind != "ObjectStore" {
		t.Errorf("GVK = %s %s", store.APIVersion, store.Kind)
	}
	want := barmanv1.ObjectStoreSpec{Configuration: barmanapi.BarmanObjectStoreConfiguration{DestinationPath: "s3://backups/db"}}
	if !reflect.DeepEqual(store.Spec, want) {
		t.Errorf("spec = %+v, want only the destination path", store.Spec)
	}
}

func TestCnpgObjectStoreHandler_DecodesDeepBlocks(t *testing.T) {
	s := newCnpgObjectStore(t, map[string]any{
		"configuration": map[string]any{
			"destinationPath": "s3://backups/db",
			"endpointURL":     "https://s3.example",
			"s3Credentials": map[string]any{
				"accessKeyId":     map[string]any{"name": "creds", "key": "id"},
				"secretAccessKey": map[string]any{"name": "creds", "key": "secret"},
			},
			"wal": map[string]any{"compression": "gzip"},
		},
		"retentionPolicy": "30d",
		"instanceSidecarConfiguration": map[string]any{
			"retentionPolicyIntervalSeconds": 600,
			"resources":                      map[string]any{"limits": map[string]any{"memory": "256Mi"}},
		},
	}).Spec
	cfg := s.Configuration
	if cfg.EndpointURL != "https://s3.example" || cfg.AWS == nil || cfg.AWS.AccessKeyIDReference.Key != "id" ||
		cfg.Wal == nil || cfg.Wal.Compression != "gzip" {
		t.Errorf("configuration = %+v", cfg)
	}
	if s.RetentionPolicy != "30d" || s.InstanceSidecarConfiguration.RetentionPolicyIntervalSeconds != 600 {
		t.Errorf("retention = %q/%d", s.RetentionPolicy, s.InstanceSidecarConfiguration.RetentionPolicyIntervalSeconds)
	}
}

// TestCnpgObjectStoreHandler_Refusals: the destination path the ObjectStore
// CRD requires must be authored, a 0 the plugin would replace with its default
// is refused by path, and the strict decode refuses what the type does not
// declare.
func TestCnpgObjectStoreHandler_Refusals(t *testing.T) {
	for _, tt := range []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{"no configuration", map[string]any{}, "configuration.destinationPath: required"},
		{"empty destination path", map[string]any{"configuration": map[string]any{"destinationPath": ""}}, "configuration.destinationPath: required"},
		{"server name", map[string]any{"configuration": map[string]any{"destinationPath": "s3://b", "serverName": "db-v2"}}, "configuration.serverName: not allowed on an ObjectStore (set the serverName plugin parameter in the Cluster that uses it)"},
		{"retention interval 0", map[string]any{
			"configuration":                minimalObjectStore()["configuration"],
			"instanceSidecarConfiguration": map[string]any{"retentionPolicyIntervalSeconds": 0},
		}, "instanceSidecarConfiguration.retentionPolicyIntervalSeconds: 0 cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default 1800)"},
		{"unknown top-level key", map[string]any{"configuration": minimalObjectStore()["configuration"], "retention": "30d"}, `unknown field "retention"`},
		{"misspelt nested key", map[string]any{"configuration": map[string]any{"destinationPath": "s3://b", "endpointUrl2": "x"}}, `unknown field "endpointUrl2"`},
		{"wrong scalar type", map[string]any{"configuration": minimalObjectStore()["configuration"], "retentionPolicy": 30}, "cannot unmarshal number"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := cnpgObjectStoreErr(t, tt.props); err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestCnpgObjectStoreConfig_ApplyPolicy(t *testing.T) {
	sidecar := func(res map[string]any) map[string]any {
		return map[string]any{
			"configuration":                minimalObjectStore()["configuration"],
			"instanceSidecarConfiguration": map[string]any{"resources": res},
		}
	}
	for _, tt := range []struct {
		name    string
		res     map[string]any
		policy  *stubPolicy
		wantErr string
	}{
		{"cpu limit above the maximum", map[string]any{"limits": map[string]any{"cpu": "2"}}, &stubPolicy{maxCPU: "1"},
			`instanceSidecarConfiguration.resources: cpu limit "2" exceeds enforced maximum "1"`},
		{"cpu request above the maximum", map[string]any{"requests": map[string]any{"cpu": "2"}}, &stubPolicy{maxCPU: "1"},
			`instanceSidecarConfiguration.resources: cpu request "2" exceeds enforced maximum "1"`},
		{"memory limit above the maximum", map[string]any{"limits": map[string]any{"memory": "2Gi"}}, &stubPolicy{maxMemory: "1Gi"},
			`instanceSidecarConfiguration.resources: memory limit "2Gi" exceeds enforced maximum "1Gi"`},
		{"memory request above the maximum", map[string]any{"requests": map[string]any{"memory": "2Gi"}}, &stubPolicy{maxMemory: "1Gi"},
			`instanceSidecarConfiguration.resources: memory request "2Gi" exceeds enforced maximum "1Gi"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := newCnpgObjectStore(t, sidecar(tt.res)).ApplyPolicy(tt.policy)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	t.Run("at the maximum, and no default filled", func(t *testing.T) {
		c := newCnpgObjectStore(t, sidecar(map[string]any{"limits": map[string]any{"cpu": "1", "memory": "1Gi"}}))
		if err := c.ApplyPolicy(&stubPolicy{maxCPU: "1", maxMemory: "1Gi", defaultCPURequest: "100m", defaultMemoryRequest: "64Mi"}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if len(c.Spec.InstanceSidecarConfiguration.Resources.Requests) != 0 {
			t.Errorf("requests = %v, want none filled", c.Spec.InstanceSidecarConfiguration.Resources.Requests)
		}
	})
}

// TestCnpgObjectStoreConfig_GenerateRevalidates pins the emission-boundary
// repeat of the parse-time refusal, for a config built directly in Go.
func TestCnpgObjectStoreConfig_GenerateRevalidates(t *testing.T) {
	c := &components.CnpgObjectStoreConfig{Name: "db-store", Namespace: "data"}
	_, err := c.Generate(stack.NewApplication("db-store", "data", c))
	if err == nil || !strings.Contains(err.Error(), "configuration.destinationPath: required") {
		t.Errorf("err = %v, want the destination path refusal", err)
	}
}

// TestCnpgObjectStoreConfig_Generate_DoesNotAlias: the generated ObjectStore
// shares no pointer with the config.
func TestCnpgObjectStoreConfig_Generate_DoesNotAlias(t *testing.T) {
	c := newCnpgObjectStore(t, map[string]any{
		"configuration": map[string]any{"destinationPath": "s3://b", "wal": map[string]any{"compression": "gzip"}},
	})
	first := generateCnpgObjectStore(t, c)
	first.Spec.Configuration.Wal.Compression = "bzip2"
	if second := generateCnpgObjectStore(t, c); second.Spec.Configuration.Wal.Compression != "gzip" {
		t.Errorf("second render changed after editing the first: %+v", second.Spec.Configuration.Wal)
	}
}
