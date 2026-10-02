package traits_test

import (
	"strings"
	"testing"
)

// A role kind's pvc volume describes a claim the rule now synthesizes as a
// `pvc` trait (go-kure/launcher#702), so the environment's storage-size limit
// reaches that claim through the trait rather than through the deployment
// member, which only references it. The limit must still refuse an oversize
// claim, and still admit one within it.
func TestRoleClaims_MaxStorageSizeReachesTheSynthesizedClaim(t *testing.T) {
	props := func(size string) map[string]any {
		return map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 1,
			"volumes": []any{map[string]any{
				"name": "data", "type": "pvc", "mountPath": "/data", "size": size,
				"storageClass": "standard", "accessModes": []any{"ReadWriteOnce"},
			}},
		}
	}
	policy := &stubPVCPolicy{maxStorageSize: "5Gi"}
	for _, kind := range []string{"webservice", "worker"} {
		t.Run(kind+"/over the limit", func(t *testing.T) {
			err := transformOne(t, kind, props("20Gi"), policy)
			if err == nil {
				t.Fatal("expected the build to fail: the claim requests 20Gi under a 5Gi limit")
			}
			if !strings.Contains(err.Error(), `"app-data"`) {
				t.Errorf("error %q does not name the claim \"app-data\"", err)
			}
		})
		t.Run(kind+"/within the limit", func(t *testing.T) {
			if err := transformOne(t, kind, props("1Gi"), policy); err != nil {
				t.Fatalf("expected the build to succeed, got: %v", err)
			}
		})
	}
}
