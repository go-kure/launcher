package components_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func newCnpgCluster(t *testing.T, props map[string]any) *components.CnpgClusterConfig {
	t.Helper()
	cfg, err := (&components.CnpgClusterHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: props}, "data")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg.(*components.CnpgClusterConfig)
}

func cnpgClusterErr(t *testing.T, props map[string]any) error {
	t.Helper()
	_, err := (&components.CnpgClusterHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: props}, "data")
	return err
}

func generateCnpgCluster(t *testing.T, c *components.CnpgClusterConfig) *cnpgv1.Cluster {
	t.Helper()
	objs, err := c.Generate(stack.NewApplication("db", "data", c))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	cluster, ok := (*objs[0]).(*cnpgv1.Cluster)
	if !ok {
		t.Fatalf("Generate: got %T, want *cnpgv1.Cluster", *objs[0])
	}
	return cluster
}

func TestCnpgClusterHandler_CanHandle(t *testing.T) {
	h := &components.CnpgClusterHandler{}
	if !h.CanHandle("cnpg-cluster") {
		t.Error("CanHandle(cnpg-cluster) = false")
	}
	if h.CanHandle("postgresql") {
		t.Error("CanHandle(postgresql) = true")
	}
}

// TestCnpgClusterSchema_Shape pins what makes this a kind component without
// launcher opinions: no platform-reserved property at any depth, no default
// other than the CRD's own instance count, and every structured field open so
// its content reaches the strict decode.
func TestCnpgClusterSchema_Shape(t *testing.T) {
	schema := (&components.CnpgClusterHandler{}).PropertySchema()
	var walk func(path string, p oam.PropertySchema)
	walk = func(path string, p oam.PropertySchema) {
		if p.PlatformReserved {
			t.Errorf("%s: PlatformReserved is set; a lowering rule writing it would be refused as authored", path)
		}
		for k, sub := range p.Properties {
			walk(path+"."+k, sub)
		}
		if p.Items != nil {
			walk(path+"[]", *p.Items)
		}
	}
	for k, p := range schema {
		walk(k, p)
		if p.Default != nil && k != "instances" {
			t.Errorf("%s: declares Default %v; only instances carries one (the CRD default)", k, p.Default)
		}
		if p.Required {
			t.Errorf("%s: Required; no ClusterSpec field is required by this kind", k)
		}
		switch p.Type {
		case oam.PropertyTypeObject:
			if !p.AdditionalProperties {
				t.Errorf("%s: object without AdditionalProperties; its keys would be refused before the strict decode", k)
			}
		case oam.PropertyTypeArray:
			if p.Items == nil || p.Items.Type != oam.PropertyTypeObject || !p.Items.AdditionalProperties {
				t.Errorf("%s: array items must be open objects", k)
			}
		default:
			// Scalars carry no nested structure; the coverage test checks their type.
		}
	}
	if d := schema["instances"].Default; d != 1 {
		t.Errorf("instances Default = %v, want 1", d)
	}
	if d := schema["enablePDB"].Default; d != nil {
		t.Errorf("enablePDB Default = %v, want none (a later trait owns that opinion)", d)
	}
}

// TestCnpgClusterHandler_MinimalWritesNoOpinions: an empty component yields a
// Cluster carrying only identity and the CRD's instance default — none of the
// values postgresql writes (enablePDB, primaryUpdateStrategy, image, size).
func TestCnpgClusterHandler_MinimalWritesNoOpinions(t *testing.T) {
	c := newCnpgCluster(t, map[string]any{})
	if err := c.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	cluster := generateCnpgCluster(t, c)
	if cluster.Name != "db" || cluster.Namespace != "data" {
		t.Errorf("identity = %s/%s, want data/db", cluster.Namespace, cluster.Name)
	}
	if cluster.APIVersion != "postgresql.cnpg.io/v1" || cluster.Kind != "Cluster" {
		t.Errorf("GVK = %s %s", cluster.APIVersion, cluster.Kind)
	}
	want := cnpgv1.ClusterSpec{Instances: 1}
	if !reflect.DeepEqual(cluster.Spec, want) {
		t.Errorf("spec = %+v, want only instances: 1", cluster.Spec)
	}
}

func TestCnpgClusterHandler_DecodesDeepBlocks(t *testing.T) {
	c := newCnpgCluster(t, map[string]any{
		"instances": 3,
		"imageName": "ghcr.io/cloudnative-pg/postgresql:17",
		"enablePDB": false,
		"storage":   map[string]any{"size": "10Gi", "storageClass": "fast"},
		"postgresql": map[string]any{
			"parameters": map[string]any{"work_mem": "8MB"},
		},
		"bootstrap": map[string]any{
			"initdb": map[string]any{"database": "app", "owner": "app"},
		},
		"resources": map[string]any{
			"requests": map[string]any{"cpu": 1, "memory": "1Gi"},
		},
		"env": []any{map[string]any{"name": "TZ", "value": "UTC"}},
	})
	s := c.Spec
	if s.Instances != 3 || s.ImageName != "ghcr.io/cloudnative-pg/postgresql:17" {
		t.Errorf("instances/imageName = %d/%q", s.Instances, s.ImageName)
	}
	if s.EnablePDB == nil || *s.EnablePDB {
		t.Errorf("enablePDB = %v, want authored false", s.EnablePDB)
	}
	if s.StorageConfiguration.Size != "10Gi" || s.StorageConfiguration.StorageClass == nil || *s.StorageConfiguration.StorageClass != "fast" {
		t.Errorf("storage = %+v", s.StorageConfiguration)
	}
	if s.PostgresConfiguration.Parameters["work_mem"] != "8MB" {
		t.Errorf("postgresql.parameters = %v", s.PostgresConfiguration.Parameters)
	}
	if s.Bootstrap == nil || s.Bootstrap.InitDB == nil || s.Bootstrap.InitDB.Database != "app" {
		t.Errorf("bootstrap = %+v", s.Bootstrap)
	}
	if q := s.Resources.Requests[corev1.ResourceCPU]; q.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("resources.requests.cpu = %s, want 1", q.String())
	}
	if len(s.Env) != 1 || s.Env[0].Name != "TZ" {
		t.Errorf("env = %v", s.Env)
	}
}

// TestCnpgClusterHandler_StrictDecode: a key the linked ClusterSpec does not
// declare, at any depth, and a value of the wrong type are refused rather than
// dropped from the emitted Cluster.
func TestCnpgClusterHandler_StrictDecode(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{"unknown top-level key", map[string]any{"replicas": 3}, `unknown field "replicas"`},
		{"misspelt nested key", map[string]any{"storage": map[string]any{"sise": "1Gi"}}, `unknown field "sise"`},
		{"misspelt deep key", map[string]any{"bootstrap": map[string]any{"initdb": map[string]any{"databse": "app"}}}, `unknown field "databse"`},
		{"unknown key in an array item", map[string]any{"managed": map[string]any{"roles": []any{map[string]any{"name": "a", "logn": true}}}}, `unknown field "logn"`},
		{"wrong nested type", map[string]any{"storage": map[string]any{"size": 5}}, "cannot unmarshal number"},
		{"wrong top-level scalar type", map[string]any{"enablePDB": "true"}, "cannot unmarshal string"},
		{"non-integer instances", map[string]any{"instances": "3"}, "instances"},
		{"negative instances", map[string]any{"instances": -1}, "instances: must be >= 0, got -1"},
		{"fractional instances", map[string]any{"instances": 1.5}, "instances: must be an integer, got float64 1.5 (not a whole number)"},
		{"out-of-range instances", map[string]any{"instances": int64(1) << 40}, "instances: must be an integer between -2147483648 and 2147483647, got 1099511627776"},
		{"instances beyond int64", map[string]any{"instances": uint64(1) << 63}, "instances: must be an integer between -2147483648 and 2147483647, got 9223372036854775808"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cnpgClusterErr(t, tt.props)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err, tt.wantSub)
			}
		})
	}
}

// TestCnpgClusterHandler_NullIsAbsence applies the package's null contract: a
// null is absent at every depth, typed or untyped, and a null array element is
// refused by path.
func TestCnpgClusterHandler_NullIsAbsence(t *testing.T) {
	t.Run("top-level nulls are omission", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"instances": nil,
			"imageName": nil,
			"bootstrap": map[string]any(nil),
			"env":       []any(nil),
		})
		if !reflect.DeepEqual(c.Spec, cnpgv1.ClusterSpec{Instances: 1}) {
			t.Errorf("spec = %+v, want the empty-document spec", c.Spec)
		}
	})
	t.Run("nil property map is the empty document", func(t *testing.T) {
		if c := newCnpgCluster(t, nil); !reflect.DeepEqual(c.Spec, cnpgv1.ClusterSpec{Instances: 1}) {
			t.Errorf("spec = %+v, want the empty-document spec", c.Spec)
		}
	})
	t.Run("nested null map value is dropped, not zero-valued", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"postgresql": map[string]any{"parameters": map[string]any{"work_mem": nil, "max_connections": "100"}},
		})
		params := c.Spec.PostgresConfiguration.Parameters
		if _, ok := params["work_mem"]; ok {
			t.Errorf("parameters = %v; a null value must not become an empty string", params)
		}
		if params["max_connections"] != "100" {
			t.Errorf("parameters = %v; sibling lost", params)
		}
	})
	t.Run("null array element is refused", func(t *testing.T) {
		for name, elem := range map[string]any{"untyped": nil, "typed": map[string]any(nil)} {
			err := cnpgClusterErr(t, map[string]any{
				"managed": map[string]any{"roles": []any{map[string]any{"name": "a"}, elem}},
			})
			if err == nil || !strings.Contains(err.Error(), "managed.roles[1]: null is not a valid array element") {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("input is not modified", func(t *testing.T) {
		params := map[string]any{"work_mem": nil}
		newCnpgCluster(t, map[string]any{"postgresql": map[string]any{"parameters": params}})
		if _, ok := params["work_mem"]; !ok {
			t.Error("the authored map lost its null key")
		}
	})
	// A lowering rule assembles properties in Go, with concrete collection
	// types; encoding/json serializes those like the untyped form.
	t.Run("typed map null value is dropped", func(t *testing.T) {
		hundred := "100"
		c := newCnpgCluster(t, map[string]any{
			"postgresql": map[string]any{"parameters": map[string]*string{"work_mem": nil, "max_connections": &hundred}},
		})
		params := generateCnpgCluster(t, c).Spec.PostgresConfiguration.Parameters
		if _, ok := params["work_mem"]; ok {
			t.Errorf("parameters = %v; a typed null value must not become an empty string", params)
		}
		if params["max_connections"] != "100" {
			t.Errorf("parameters = %v; sibling lost", params)
		}
	})
	t.Run("typed slice null element is refused", func(t *testing.T) {
		for name, roles := range map[string]any{
			"slice of maps":            []map[string]any{{"name": "a"}, nil},
			"slice of struct pointers": []*cnpgv1.RoleConfiguration{{Name: "a"}, nil},
		} {
			err := cnpgClusterErr(t, map[string]any{"managed": map[string]any{"roles": roles}})
			if err == nil || !strings.Contains(err.Error(), "managed.roles[1]: null is not a valid array element") {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("typed slice without nulls decodes", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"managed": map[string]any{"roles": []*cnpgv1.RoleConfiguration{{Name: "a"}}},
		})
		if roles := c.Spec.Managed.Roles; len(roles) != 1 || roles[0].Name != "a" {
			t.Errorf("roles = %+v", roles)
		}
	})
	t.Run("byte slice is not taken apart", func(t *testing.T) {
		// encoding/json writes []byte as a base64 string; read as an array it
		// would become a list of numbers and fail to decode into a string.
		c := newCnpgCluster(t, map[string]any{"description": []byte("hi")})
		if c.Spec.Description != "aGk=" {
			t.Errorf("description = %q, want the base64 string encoding/json writes", c.Spec.Description)
		}
	})
	// Null is defined by serialization, so what the handler reads must be what
	// encoding/json emits, whatever Go shape produced it.
	t.Run("pointer-receiver encoder on a typed slice element is honoured", func(t *testing.T) {
		env := []rewritingEnvVar{{Name: "TZ", Value: "UTC"}}
		data, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		var want []corev1.EnvVar
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		if len(want) != 1 || want[0].Value != "encoded-UTC" {
			t.Fatalf("json.Marshal = %s; the fixture no longer exercises the encoder", data)
		}
		c := newCnpgCluster(t, map[string]any{"env": env})
		if !reflect.DeepEqual(c.Spec.Env, want) {
			t.Errorf("env = %+v, want what encoding/json emits: %+v", c.Spec.Env, want)
		}
	})
	t.Run("null behind a pointer is dropped as a map value", func(t *testing.T) {
		var nilString *string
		c := newCnpgCluster(t, map[string]any{
			"postgresql": map[string]any{"parameters": map[string]any{"work_mem": &nilString, "max_connections": "100"}},
		})
		params := c.Spec.PostgresConfiguration.Parameters
		if _, ok := params["work_mem"]; ok {
			t.Errorf("parameters = %v; a value serializing to null must not become an empty string", params)
		}
		if params["max_connections"] != "100" {
			t.Errorf("parameters = %v; sibling lost", params)
		}
	})
	t.Run("null behind a pointer is refused as an array element", func(t *testing.T) {
		var nilMap map[string]any
		err := cnpgClusterErr(t, map[string]any{"env": []any{&nilMap}})
		if err == nil || !strings.Contains(err.Error(), "env[0]: null is not a valid array element") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("raw JSON null is absence", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"postgresql": map[string]any{"parameters": map[string]any{"work_mem": json.RawMessage("null")}},
		})
		if params := c.Spec.PostgresConfiguration.Parameters; len(params) != 0 {
			t.Errorf("parameters = %v; a raw null value must be dropped", params)
		}
		err := cnpgClusterErr(t, map[string]any{"env": []any{json.RawMessage("null")}})
		if err == nil || !strings.Contains(err.Error(), "env[0]: null is not a valid array element") {
			t.Errorf("raw null element: err = %v", err)
		}
	})
	t.Run("a value that does not serialize is refused", func(t *testing.T) {
		for name, v := range map[string]any{"channel": make(chan int), "func": func() {}} {
			err := cnpgClusterErr(t, map[string]any{"description": v})
			if err == nil || !strings.Contains(err.Error(), "properties do not serialize to JSON") {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
}

// rewritingEnvVar encodes through a pointer-receiver MarshalJSON that rewrites
// its value, which encoding/json calls for an addressable slice element.
type rewritingEnvVar struct {
	Name, Value string
}

func (e *rewritingEnvVar) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"name": e.Name, "value": "encoded-" + e.Value})
}

func TestCnpgClusterConfig_ApplyPolicy_Instances(t *testing.T) {
	t.Run("default applies when unauthored", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{})
		if err := c.ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(3)}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.Instances != 3 {
			t.Errorf("instances = %d, want 3", c.Spec.Instances)
		}
	})
	t.Run("authored value wins, even when it equals the fallback", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"instances": 1})
		if err := c.ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(3)}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.Instances != 1 {
			t.Errorf("instances = %d, want authored 1", c.Spec.Instances)
		}
	})
	t.Run("maximum refuses an authored count", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"instances": 5})
		err := c.ApplyPolicy(&stubPolicy{maxReplicas: int32ptr(3)})
		if err == nil || err.Error() != "instances 5 exceeds enforced maximum 3" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("maximum refuses a policy-defaulted count", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{})
		if err := c.ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(4), maxReplicas: int32ptr(3)}); err == nil {
			t.Error("expected the defaulted count to be capped")
		}
	})
}

func TestCnpgClusterConfig_ApplyPolicy_Resources(t *testing.T) {
	t.Run("defaults fill only unset entries", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"resources": map[string]any{"requests": map[string]any{"cpu": "250m"}},
		})
		p := &stubPolicy{defaultCPURequest: "100m", defaultMemoryRequest: "256Mi", defaultCPULimit: "1", defaultMemoryLimit: "512Mi"}
		if err := c.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		r := c.Spec.Resources
		for _, chk := range []struct {
			list corev1.ResourceList
			name corev1.ResourceName
			want string
		}{
			{r.Requests, corev1.ResourceCPU, "250m"},
			{r.Requests, corev1.ResourceMemory, "256Mi"},
			{r.Limits, corev1.ResourceCPU, "1"},
			{r.Limits, corev1.ResourceMemory, "512Mi"},
		} {
			if q := chk.list[chk.name]; q.String() != chk.want {
				t.Errorf("%s = %s, want %s", chk.name, q.String(), chk.want)
			}
		}
	})
	t.Run("invalid policy default is an error", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{})
		if err := c.ApplyPolicy(&stubPolicy{defaultCPURequest: "lots"}); err == nil {
			t.Error("expected an invalid policy quantity to fail")
		}
	})
	for _, tt := range []struct {
		name    string
		res     map[string]any
		policy  *stubPolicy
		wantErr string
	}{
		{"cpu request", map[string]any{"requests": map[string]any{"cpu": "2"}}, &stubPolicy{maxCPU: "1"}, `cpu request "2" exceeds enforced maximum "1"`},
		{"cpu limit", map[string]any{"limits": map[string]any{"cpu": "2"}}, &stubPolicy{maxCPU: "1"}, `cpu limit "2" exceeds enforced maximum "1"`},
		{"memory request", map[string]any{"requests": map[string]any{"memory": "2Gi"}}, &stubPolicy{maxMemory: "1Gi"}, `memory request "2Gi" exceeds enforced maximum "1Gi"`},
		{"memory limit", map[string]any{"limits": map[string]any{"memory": "2Gi"}}, &stubPolicy{maxMemory: "1Gi"}, `memory limit "2Gi" exceeds enforced maximum "1Gi"`},
		{"policy-defaulted value", map[string]any{}, &stubPolicy{defaultMemoryLimit: "2Gi", maxMemory: "1Gi"}, `memory limit "2Gi" exceeds enforced maximum "1Gi"`},
	} {
		t.Run("max "+tt.name, func(t *testing.T) {
			c := newCnpgCluster(t, map[string]any{"resources": tt.res})
			err := c.ApplyPolicy(tt.policy)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCnpgClusterConfig_ApplyPolicy_StorageDefault(t *testing.T) {
	p := &stubPolicy{defaultStorageSize: "20Gi"}
	t.Run("fills an unauthored size", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"storage": map[string]any{"storageClass": "fast"}})
		if err := c.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.StorageConfiguration.Size != "20Gi" {
			t.Errorf("size = %q, want 20Gi", c.Spec.StorageConfiguration.Size)
		}
	})
	t.Run("authored size wins", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"storage": map[string]any{"size": "5Gi"}})
		if err := c.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.StorageConfiguration.Size != "5Gi" {
			t.Errorf("size = %q, want 5Gi", c.Spec.StorageConfiguration.Size)
		}
	})
	t.Run("authored template request is not overridden", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"storage": map[string]any{
			"pvcTemplate": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "7Gi"}}},
		}})
		if err := c.ApplyPolicy(p); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.StorageConfiguration.Size != "" {
			t.Errorf("size = %q; a policy default would replace the authored template request", c.Spec.StorageConfiguration.Size)
		}
	})
	// encoding/json accepts a case-variant field name, so the authored check
	// must agree with the decoder or the default overwrites the decoded value.
	t.Run("case-variant spelling counts as authored", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"Instances": 2, "storage": map[string]any{"Size": "5Gi"}})
		if err := c.ApplyPolicy(&stubPolicy{defaultStorageSize: "20Gi", defaultReplicas: int32ptr(3)}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.StorageConfiguration.Size != "5Gi" || c.Spec.Instances != 2 {
			t.Errorf("size/instances = %q/%d, want the decoded 5Gi/2", c.Spec.StorageConfiguration.Size, c.Spec.Instances)
		}
	})
	// The decoded spec cannot tell an authored "" size or 0 instances from
	// absence, so the raw-map check itself must accept every spelling.
	t.Run("case-variant empty size and zero instances count as authored", func(t *testing.T) {
		for name, props := range map[string]map[string]any{
			"storage.Size":         {"Instances": 0, "storage": map[string]any{"Size": ""}},
			"Storage.size":         {"INSTANCES": 0, "Storage": map[string]any{"size": ""}},
			"Storage.PvcTemplate":  {"instances": 0, "Storage": map[string]any{"PvcTemplate": map[string]any{"Resources": map[string]any{"Requests": map[string]any{"storage": "7Gi"}}}}},
			"exact, for reference": {"instances": 0, "storage": map[string]any{"size": ""}},
		} {
			c := newCnpgCluster(t, props)
			if err := c.ApplyPolicy(&stubPolicy{defaultStorageSize: "20Gi", defaultReplicas: int32ptr(3)}); err != nil {
				t.Fatalf("%s: ApplyPolicy: %v", name, err)
			}
			if c.Spec.StorageConfiguration.Size != "" || c.Spec.Instances != 0 {
				t.Errorf("%s: size/instances = %q/%d; a policy default replaced an authored value", name, c.Spec.StorageConfiguration.Size, c.Spec.Instances)
			}
		}
	})
	t.Run("case-variant non-integer instances is refused by name", func(t *testing.T) {
		if err := cnpgClusterErr(t, map[string]any{"Instances": 1.5}); err == nil ||
			!strings.HasPrefix(err.Error(), "instances: ") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("case-variant negative instances is refused", func(t *testing.T) {
		if err := cnpgClusterErr(t, map[string]any{"Instances": -1}); err == nil ||
			!strings.Contains(err.Error(), "instances: must be >= 0, got -1") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no fallback without a policy default", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{})
		if err := c.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if c.Spec.StorageConfiguration.Size != "" {
			t.Errorf("size = %q, want unset", c.Spec.StorageConfiguration.Size)
		}
	})
}

func TestCnpgClusterConfig_ApplyPolicy_StorageMaximum(t *testing.T) {
	tmpl := func(size string) map[string]any {
		return map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": size}}}
	}
	tests := []struct {
		name    string
		props   map[string]any
		policy  *stubPolicy
		wantErr string
	}{
		{"storage.size", map[string]any{"storage": map[string]any{"size": "20Gi"}}, &stubPolicy{maxStorageSize: "10Gi"}, `storage.size "20Gi" exceeds enforced maximum "10Gi"`},
		{"storage template", map[string]any{"storage": map[string]any{"pvcTemplate": tmpl("20Gi")}}, &stubPolicy{maxStorageSize: "10Gi"}, `storage.pvcTemplate.resources.requests.storage "20Gi" exceeds enforced maximum "10Gi"`},
		{"policy-defaulted size", map[string]any{}, &stubPolicy{defaultStorageSize: "20Gi", maxStorageSize: "10Gi"}, `storage.size "20Gi" exceeds enforced maximum "10Gi"`},
		{"walStorage", map[string]any{"walStorage": map[string]any{"size": "20Gi"}}, &stubPolicy{maxStorageSize: "10Gi"}, `walStorage.size "20Gi" exceeds enforced maximum "10Gi"`},
		{"tablespace", map[string]any{"tablespaces": []any{
			map[string]any{"name": "a", "storage": map[string]any{"size": "1Gi"}},
			map[string]any{"name": "b", "storage": map[string]any{"pvcTemplate": tmpl("20Gi")}},
		}}, &stubPolicy{maxStorageSize: "10Gi"}, `tablespaces[1].storage.pvcTemplate.resources.requests.storage "20Gi" exceeds enforced maximum "10Gi"`},
		{"ephemeral volume", map[string]any{"ephemeralVolumeSource": map[string]any{
			"volumeClaimTemplate": map[string]any{"spec": tmpl("20Gi")},
		}}, &stubPolicy{maxStorageSize: "10Gi"}, `ephemeralVolumeSource.volumeClaimTemplate.spec.resources.requests.storage "20Gi" exceeds enforced maximum "10Gi"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newCnpgCluster(t, tt.props).ApplyPolicy(tt.policy)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	t.Run("within the maximum passes", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"storage": map[string]any{"size": "10Gi"}, "walStorage": map[string]any{"size": "1Gi"}})
		if err := c.ApplyPolicy(&stubPolicy{maxStorageSize: "10Gi"}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
	})
}

func TestCnpgClusterConfig_ApplyPolicy_SecurityContext(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		policy  *stubPolicy
		wantErr string
	}{
		{"privileged", map[string]any{"securityContext": map[string]any{"privileged": true}}, &stubPolicy{}, "securityContext.privileged is not allowed by environment policy"},
		{"container hostProcess", map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}}, &stubPolicy{}, "securityContext.windowsOptions.hostProcess is not allowed by environment policy"},
		{"pod hostProcess", map[string]any{"podSecurityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}}, &stubPolicy{}, "podSecurityContext.windowsOptions.hostProcess is not allowed by environment policy"},
		{"forbidden capability", map[string]any{"securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}}, &stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}, `securityContext.capabilities.add: "NET_ADMIN" is forbidden by environment policy`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newCnpgCluster(t, tt.props).ApplyPolicy(tt.policy)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	t.Run("allowed by policy", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{
			"securityContext":    map[string]any{"privileged": true},
			"podSecurityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}},
		})
		if err := c.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
	})
}

func TestCnpgClusterConfig_ApplyPolicy_AllowedRegistries(t *testing.T) {
	t.Run("imageName from a disallowed registry is refused", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"imageName": "docker.io/library/postgres:16"})
		err := c.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
		if want := `imageName: image "docker.io/library/postgres:16" is not from an allowed registry [ghcr.io]`; err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
	t.Run("imageName from an allowed registry passes", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{"imageName": "ghcr.io/cloudnative-pg/postgresql:16"})
		if err := c.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
	})
	t.Run("unset imageName is not checked", func(t *testing.T) {
		// The operator's default image is not one the document chose.
		c := newCnpgCluster(t, map[string]any{})
		if err := c.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"registry.invalid"}}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
	})
}

func TestCnpgClusterConfig_Generate_HugePagesNeedCPUOrMemory(t *testing.T) {
	hp := map[string]any{"hugepages-2Mi": "2Mi"}
	c := newCnpgCluster(t, map[string]any{"resources": map[string]any{"requests": hp, "limits": hp}})
	if _, err := c.Generate(stack.NewApplication("db", "data", c)); err == nil ||
		!strings.Contains(err.Error(), "hugepages require cpu or memory") {
		t.Errorf("err = %v", err)
	}
	// A policy default supplying memory satisfies the rule, as for postgresql.
	if err := c.ApplyPolicy(&stubPolicy{defaultMemoryRequest: "256Mi"}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	generateCnpgCluster(t, c)
}

// TestCnpgClusterConfig_Generate_DoesNotAlias: the generated Cluster shares no
// map or pointer with the config, so editing it cannot change the next render.
func TestCnpgClusterConfig_Generate_DoesNotAlias(t *testing.T) {
	c := newCnpgCluster(t, map[string]any{
		"inheritedMetadata": map[string]any{"labels": map[string]any{"team": "a"}},
		"enablePDB":         true,
	})
	first := generateCnpgCluster(t, c)
	first.Spec.InheritedMetadata.Labels["team"] = "edited"
	*first.Spec.EnablePDB = false
	second := generateCnpgCluster(t, c)
	if second.Spec.InheritedMetadata.Labels["team"] != "a" || !*second.Spec.EnablePDB {
		t.Errorf("second render changed after editing the first: %+v", second.Spec)
	}
}

// TestCnpgClusterHandler_Endpoints: the primary endpoint is postgresql's, so a
// consumer's synthesized ingress allow does not change when a postgresql
// component is expressed as a cnpg-cluster.
func TestCnpgClusterHandler_Endpoints(t *testing.T) {
	comp := &oam.Component{Name: "orders-db", Type: "cnpg-cluster"}
	got, err := (&components.CnpgClusterHandler{}).Endpoints(comp)
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	want, err := (&components.PostgresqlHandler{}).Endpoints(&oam.Component{Name: "orders-db", Type: "postgresql"})
	if err != nil {
		t.Fatalf("postgresql Endpoints: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("endpoints = %+v, want postgresql's %+v", got, want)
	}
	if len(got) != 1 || got[0].PodSelector.MatchLabels["cnpg.io/cluster"] != "orders-db" || got[0].Ports[0].IntVal != 5432 {
		t.Errorf("endpoints = %+v, want cnpg.io/cluster=orders-db on 5432", got)
	}
}
