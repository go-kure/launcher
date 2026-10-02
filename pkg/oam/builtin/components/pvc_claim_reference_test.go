package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Tests for a pvc volume authoring claimName: it mounts an existing claim and
// generates none, while accessModes still drives the non-RWX constraints
// (go-kure/launcher#702).

func claimRefProps(vol map[string]any, extra map[string]any) map[string]any {
	v := map[string]any{"name": "data", "type": "pvc", "mountPath": "/data", "claimName": "shared-data"}
	for k, val := range vol {
		v[k] = val
	}
	props := map[string]any{"image": "ghcr.io/org/app:v1", "volumes": []any{v}}
	for k, val := range extra {
		props[k] = val
	}
	return props
}

func claimNames(t *testing.T, objects []*client.Object) (volumes map[string]string, generated []string) {
	t.Helper()
	volumes = map[string]string{}
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *corev1.PersistentVolumeClaim:
			generated = append(generated, o.Name)
		case *appsv1.Deployment:
			for _, v := range o.Spec.Template.Spec.Volumes {
				if v.PersistentVolumeClaim != nil {
					volumes[v.Name] = v.PersistentVolumeClaim.ClaimName
				}
			}
		}
	}
	return volumes, generated
}

func TestPVCVolume_ClaimName_ReferencesWithoutGenerating(t *testing.T) {
	dep, objects := generateDeployment(t, "app", claimRefProps(nil, nil))
	vols, generated := claimNames(t, objects)
	if len(generated) != 0 {
		t.Errorf("generated claims = %v, want none for a claimName reference", generated)
	}
	if got := vols["data"]; got != "shared-data" {
		t.Errorf("volume data claimName = %q, want the authored, unqualified %q", got, "shared-data")
	}
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want Recreate: a reference defaults to ReadWriteOnce", dep.Spec.Strategy.Type)
	}
	cfg := deploymentConfig(t, "app", claimRefProps(nil, nil))
	if got := cfg.(interface{ NonRWXClaim() string }).NonRWXClaim(); got != "shared-data" {
		t.Errorf("NonRWXClaim = %q, want the referenced claim %q", got, "shared-data")
	}
}

func TestPVCVolume_ClaimName_RWXIsNotConstrained(t *testing.T) {
	props := claimRefProps(map[string]any{"accessModes": []any{"ReadWriteMany"}}, map[string]any{"replicas": 3})
	dep, _ := generateDeployment(t, "app", props)
	if dep.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		t.Error("Strategy.Type = Recreate, want it left alone for a ReadWriteMany reference")
	}
	cfg := deploymentConfig(t, "app", props)
	if got := cfg.(interface{ NonRWXClaim() string }).NonRWXClaim(); got != "" {
		t.Errorf("NonRWXClaim = %q, want empty for a ReadWriteMany reference", got)
	}
}

func TestPVCVolume_ClaimName_NonRWXRefusesReplicas(t *testing.T) {
	cfg := deploymentConfig(t, "app", claimRefProps(nil, map[string]any{"replicas": 2}))
	_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err == nil || !strings.Contains(err.Error(), "at most one replica") {
		t.Fatalf("Generate error = %v, want the non-RWX replicas refusal", err)
	}
}

// A referenced claim that shares its name with a generated claim's pod-local
// name must not be rewritten by qualification, and Generate stays idempotent.
func TestPVCVolume_ClaimName_NotQualified(t *testing.T) {
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"volumes": []any{
			map[string]any{"name": "data", "type": "pvc", "mountPath": "/data", "size": "1Gi"},
			map[string]any{"name": "other", "type": "pvc", "mountPath": "/other", "claimName": "data"},
		},
	}
	cfg := deploymentConfig(t, "app", props)
	for run := 1; run <= 2; run++ {
		objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err != nil {
			t.Fatalf("Generate run %d: %v", run, err)
		}
		vols, generated := claimNames(t, objects)
		if len(generated) != 1 || generated[0] != "app-data" {
			t.Errorf("run %d: generated claims = %v, want [app-data]", run, generated)
		}
		if vols["data"] != "app-data" || vols["other"] != "data" {
			t.Errorf("run %d: volume claims = %v, want data->app-data and other->data", run, vols)
		}
	}
}

func TestPVCVolume_ClaimName_OtherKindsGenerateNoClaim(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		handler interface {
			ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
		}
		extra map[string]any
	}{
		{"statefulset", &components.StatefulsetHandler{}, nil},
		{"daemonset", &components.DaemonsetHandler{}, nil},
		{"job", &components.JobHandler{}, nil},
		{"cronjob", &components.CronjobHandler{}, map[string]any{"schedule": "0 2 * * *"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: tc.kind, Properties: claimRefProps(nil, tc.extra)}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, obj := range objects {
				if pvc, ok := (*obj).(*corev1.PersistentVolumeClaim); ok {
					t.Errorf("generated claim %q, want none for a claimName reference", pvc.Name)
				}
			}
		})
	}
}

func TestPVCVolume_ClaimName_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		vol  map[string]any
		want string
	}{
		{"size", map[string]any{"size": "1Gi"}, "size cannot be set with claimName"},
		{"storageClass", map[string]any{"storageClass": "fast"}, "storageClass cannot be set with claimName"},
		{"explicit empty storageClass", map[string]any{"storageClass": ""}, "storageClass cannot be set with claimName"},
		{"empty claimName", map[string]any{"claimName": ""}, "invalid claimName"},
		{"invalid claimName", map[string]any{"claimName": "Bad_Name"}, "invalid claimName"},
		{"non-string claimName", map[string]any{"claimName": 7}, "claimName"},
		{"bad accessModes", map[string]any{"accessModes": []any{"Sometimes"}}, "accessMode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.DeploymentHandler{}
			_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: claimRefProps(tc.vol, nil)}, "default")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestBuildPVC_RefusesClaimReference(t *testing.T) {
	_, err := components.BuildPVC(components.PVCConfig{Name: "data", ClaimName: "shared-data"}, "default", nil)
	if err == nil || !strings.Contains(err.Error(), "references the existing claim") {
		t.Fatalf("BuildPVC error = %v, want a refusal for a claim reference", err)
	}
}
