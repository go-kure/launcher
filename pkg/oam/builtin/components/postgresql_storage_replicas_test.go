package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPostgresqlConfig_ApplyPolicy_RefusesInvalidDefaults pins the policy
// defaults postgresql used to copy unchecked (go-kure/launcher#623): a replicas
// default below the CRD's Minimum=1, and a storage-size default that does not
// parse or is not positive. Each is refused with the field and its source named.
// The policy applies to the Cluster component the rule emits, so the fields are
// named as the Cluster names them (instances, storage.size).
func TestPostgresqlConfig_ApplyPolicy_RefusesInvalidDefaults(t *testing.T) {
	cases := []struct {
		name   string
		policy *stubPolicy
		want   string
	}{
		{"replicas default zero", &stubPolicy{defaultReplicas: int32ptr(0)}, "instances: must be >= 1, got 0 from the policy default"},
		{"storage default not a quantity", &stubPolicy{defaultStorageSize: "lots"}, `policy default for storage.size: invalid quantity "lots"`},
		{"storage default zero", &stubPolicy{defaultStorageSize: "0"}, `policy default for storage.size: quantity must be positive, got "0"`},
		{"storage default negative", &stubPolicy{defaultStorageSize: "-1Gi"}, `policy default for storage.size: quantity must be positive, got "-1Gi"`},
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

// TestPostgresqlRule_RefusesInvalidStorageSize pins the check on an authored
// storage size: one that does not parse or is not positive is refused with
// postgresql's text, as is an empty one, which built a Cluster with no size
// that CloudNativePG's webhook refuses ("Size not configured",
// cluster_webhook.go).
func TestPostgresqlRule_RefusesInvalidStorageSize(t *testing.T) {
	for _, size := range []string{"0", "-1Gi", "lots"} {
		t.Run(size, func(t *testing.T) {
			_, err := postgresqlViaRule{}.ToApplicationConfig(&oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"storageSize": size}}, "default")
			want := fmt.Sprintf("storageSize: quantity must be positive, got %q", size)
			if size == "lots" {
				want = `storageSize: invalid quantity "lots"`
			}
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("err = %v, want it to start with %q", err, want)
			}
		})
	}
	t.Run("an empty size", func(t *testing.T) {
		_, err := postgresqlViaRule{}.ToApplicationConfig(&oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"storageSize": ""}}, "default")
		const want = "storageSize: must not be empty; omit it to take the policy default or 1Gi"
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
}
