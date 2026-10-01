package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPostgresqlConfig_ApplyPolicy_RefusesInvalidDefaults pins the policy
// defaults postgresql used to copy unchecked (go-kure/launcher#623): a replicas
// default below the CRD's Minimum=1, and a storage-size default that does not
// parse or is not positive. Each is refused with the field and its source named.
func TestPostgresqlConfig_ApplyPolicy_RefusesInvalidDefaults(t *testing.T) {
	cases := []struct {
		name   string
		policy *stubPolicy
		want   string
	}{
		{"replicas default zero", &stubPolicy{defaultReplicas: int32ptr(0)}, "replicas: must be >= 1, got 0 from the policy default"},
		{"storage default not a quantity", &stubPolicy{defaultStorageSize: "lots"}, `policy default for storageSize: invalid quantity "lots"`},
		{"storage default zero", &stubPolicy{defaultStorageSize: "0"}, `policy default for storageSize: quantity must be positive, got "0"`},
		{"storage default negative", &stubPolicy{defaultStorageSize: "-1Gi"}, `policy default for storageSize: quantity must be positive, got "-1Gi"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newPostgresqlApp(t, map[string]any{}).ApplyPolicy(tc.policy)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to start with %q", err, tc.want)
			}
		})
	}
	t.Run("an authored value is not replaced by a default", func(t *testing.T) {
		pc := newPostgresqlApp(t, map[string]any{"replicas": 2, "storageSize": "5Gi"})
		if err := pc.ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(0), defaultStorageSize: "lots"}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		generatePostgresql(t, pc)
	})
}

// TestPostgresqlConfig_Generate_RefusesInvalidStorageSize pins the emission
// check on the storage size the Cluster carries: an authored size that does
// not parse or is not positive is refused, as is one on a config built
// without the handler. An authored empty size keeps building.
func TestPostgresqlConfig_Generate_RefusesInvalidStorageSize(t *testing.T) {
	for _, size := range []string{"0", "-1Gi", "lots"} {
		t.Run(size, func(t *testing.T) {
			pc := newPostgresqlApp(t, map[string]any{"storageSize": size})
			_, err := pc.Generate(stack.NewApplication("db", "default", pc))
			want := fmt.Sprintf("storageSize: quantity must be positive, got %q", size)
			if size == "lots" {
				want = `storageSize: invalid quantity "lots"`
			}
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("err = %v, want it to start with %q", err, want)
			}
		})
	}
	t.Run("a directly built config", func(t *testing.T) {
		pc := &components.PostgresqlConfig{Replicas: 1, StorageSize: "0"}
		_, err := pc.Generate(stack.NewApplication("db", "default", pc))
		if err == nil || err.Error() != `storageSize: quantity must be positive, got "0"` {
			t.Errorf("err = %v, want the storage refusal", err)
		}
	})
	t.Run("an empty size builds", func(t *testing.T) {
		generatePostgresql(t, newPostgresqlApp(t, map[string]any{"storageSize": ""}))
	})
}
