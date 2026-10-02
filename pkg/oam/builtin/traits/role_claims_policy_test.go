package traits_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
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

// A role rule reads the ClusterProfile `pvc` capability for the claims its
// volumes describe (go-kure/launcher#746), so TransformWithPolicy lists the key
// as consumed exactly when a volume generates a claim, as it lists the key an
// authored pvc trait resolves against. A volume that references an existing
// claim generates none and reads nothing.
func TestRoleClaims_PVCCapabilityIsConsumed(t *testing.T) {
	caps := map[string]oam.CapabilityBinding{"pvc": {Rendering: map[string]any{"storageClassName": "platform-ssd"}}}
	volume := map[string]map[string]any{
		"generated claim": {"name": "data", "type": "pvc", "mountPath": "/data", "size": "1Gi"},
		"claim reference": {"name": "data", "type": "pvc", "mountPath": "/data", "claimName": "existing"},
	}
	want := map[string][]string{"generated claim": {"pvc"}, "claim reference": nil}
	for _, kind := range []string{"webservice", "worker"} {
		for name, vol := range volume {
			t.Run(kind+"/"+name, func(t *testing.T) {
				app := &oam.Application{
					APIVersion: oam.SupportedAPIVersion,
					Kind:       "Application",
					Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
					Spec: oam.ApplicationSpec{Components: []oam.Component{{
						Name: "app", Type: kind,
						Properties: map[string]any{"image": "ghcr.io/org/app:v1", "replicas": 1, "volumes": []any{vol}},
					}}},
				}
				_, result, err := nonRWXScalerTransformer().TransformWithPolicy(app, oam.TransformContext{Capabilities: caps})
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if got := result.ConsumedCapabilities; !reflect.DeepEqual(got, want[name]) {
					t.Errorf("consumed capabilities %v, want %v", got, want[name])
				}
			})
		}
	}
}
