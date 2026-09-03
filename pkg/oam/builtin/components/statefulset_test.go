package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestStatefulsetHandler_CanHandle(t *testing.T) {
	h := &components.StatefulsetHandler{}
	if !h.CanHandle("statefulset") {
		t.Error("expected true for statefulset")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

func TestStatefulsetHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "db",
		Type:       "statefulset",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestStatefulsetHandler_Generate_BasicResources(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"port":  5432,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundSTS, foundSVC, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.StatefulSet:
			foundSTS = true
		case *corev1.Service:
			foundSVC = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundSTS {
		t.Error("expected StatefulSet")
	}
	if !foundSVC {
		t.Error("expected headless Service")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestStatefulsetHandler_VolumeClaimTemplates(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "10Gi",
					"mountPath": "/var/lib/data",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var sts *appsv1.StatefulSet
	for _, obj := range objects {
		if s, ok := (*obj).(*appsv1.StatefulSet); ok {
			sts = s
		}
	}
	if sts == nil {
		t.Fatal("expected StatefulSet in output")
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 {
		t.Errorf("expected 1 VCT, got %d", len(sts.Spec.VolumeClaimTemplates))
	}
}

// TestStatefulsetHandler_GeneratedClaimSpec walks the whole path — properties,
// ToApplicationConfig, Generate — and asserts the PersistentVolumeClaimSpec
// that actually lands in the StatefulSet. Every other claim test in this
// package stops at the parser or calls VolumeClaimSpecConfig.apply directly, so
// nothing covered the two lines in createStatefulSet that carry the projection
// into the output: the resource.MustParse of the short `size` spelling, and the
// vct.Spec.apply that writes everything else. Deleting either left the suite
// green.
//
// Two entries, one per storage spelling, because they reach the generated claim
// by different routes: `size` through kure's CreateVolumeClaimTemplate options,
// `resources.requests.storage` through apply's merge.
func TestStatefulsetHandler_GeneratedClaimSpec(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":         "data",
					"mountPath":    "/var/lib/data",
					"storageClass": "fast",
					"accessModes":  []any{"ReadWriteOncePod"},
					"resources": map[string]any{
						"requests": map[string]any{"storage": "10Gi"},
						"limits":   map[string]any{"storage": "20Gi"},
					},
					"selector": map[string]any{
						"matchLabels": map[string]any{"tier": "fast"},
					},
					"volumeMode": "Filesystem",
					"dataSourceRef": map[string]any{
						"apiGroup": "snapshot.storage.k8s.io",
						"kind":     "VolumeSnapshot",
						"name":     "seed",
					},
					"volumeAttributesClassName": "gold",
				},
				map[string]any{
					"name":      "wal",
					"mountPath": "/var/lib/wal",
					"size":      "5Gi",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var sts *appsv1.StatefulSet
	for _, obj := range objects {
		if s, ok := (*obj).(*appsv1.StatefulSet); ok {
			sts = s
		}
	}
	if sts == nil {
		t.Fatal("expected StatefulSet in output")
	}
	if len(sts.Spec.VolumeClaimTemplates) != 2 {
		t.Fatalf("got %d claim templates, want 2", len(sts.Spec.VolumeClaimTemplates))
	}

	// Entry 0: the long spelling plus every field the projection adds.
	data := sts.Spec.VolumeClaimTemplates[0]
	if data.Name != "data" {
		t.Errorf("templates[0].Name = %q, want %q", data.Name, "data")
	}
	if got := data.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Errorf("data requests.storage = %v, want 10Gi", &got)
	}
	if got := data.Spec.Resources.Limits[corev1.ResourceStorage]; got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("data limits.storage = %v, want 20Gi", &got)
	}
	if data.Spec.StorageClassName == nil || *data.Spec.StorageClassName != "fast" {
		t.Errorf("data storageClassName = %v, want fast", data.Spec.StorageClassName)
	}
	if !slices.Equal(data.Spec.AccessModes, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}) {
		t.Errorf("data accessModes = %v, want [ReadWriteOncePod]", data.Spec.AccessModes)
	}
	if data.Spec.Selector == nil {
		t.Fatal("data selector is nil")
	}
	if data.Spec.Selector.MatchLabels["tier"] != "fast" {
		t.Errorf("data selector.matchLabels = %v, want tier=fast", data.Spec.Selector.MatchLabels)
	}
	if data.Spec.VolumeMode == nil || *data.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		t.Errorf("data volumeMode = %v, want Filesystem", data.Spec.VolumeMode)
	}
	if data.Spec.DataSourceRef == nil {
		t.Fatal("data dataSourceRef is nil")
	}
	if data.Spec.DataSourceRef.Kind != "VolumeSnapshot" || data.Spec.DataSourceRef.Name != "seed" {
		t.Errorf("data dataSourceRef = %+v, want VolumeSnapshot/seed", data.Spec.DataSourceRef)
	}
	if data.Spec.DataSourceRef.APIGroup == nil || *data.Spec.DataSourceRef.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("data dataSourceRef.apiGroup = %v, want snapshot.storage.k8s.io", data.Spec.DataSourceRef.APIGroup)
	}
	// launcher never mirrors dataSourceRef into the legacy dataSource field:
	// the apiserver does that itself, and only when namespace is unspecified
	// (k8s.io/api core/v1 types.go, PersistentVolumeClaimSpec).
	if data.Spec.DataSource != nil {
		t.Errorf("data dataSource = %+v, want nil — mirroring is the apiserver's job", data.Spec.DataSource)
	}
	if data.Spec.VolumeAttributesClassName == nil || *data.Spec.VolumeAttributesClassName != "gold" {
		t.Errorf("data volumeAttributesClassName = %v, want gold", data.Spec.VolumeAttributesClassName)
	}

	// Entry 1: the short spelling reaches the generated claim, and the
	// unauthored claim-spec fields stay at their kure defaults.
	wal := sts.Spec.VolumeClaimTemplates[1]
	if wal.Name != "wal" {
		t.Errorf("templates[1].Name = %q, want %q", wal.Name, "wal")
	}
	if got := wal.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("wal requests.storage = %v, want 5Gi", &got)
	}
	if wal.Spec.Selector != nil {
		t.Errorf("wal selector = %v, want nil", wal.Spec.Selector)
	}
	if wal.Spec.VolumeMode != nil {
		t.Errorf("wal volumeMode = %v, want nil", wal.Spec.VolumeMode)
	}
	if !slices.Equal(wal.Spec.AccessModes, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}) {
		t.Errorf("wal accessModes = %v, want the [ReadWriteOnce] default", wal.Spec.AccessModes)
	}
}

func TestStatefulsetHandler_VolumeClaimTemplate_MissingName(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"size":      "10Gi",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing VCT name")
	}
}

func TestStatefulsetHandler_VolumeClaimTemplate_InvalidSize(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "not-a-quantity",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid VCT size")
	}
}

func TestStatefulsetConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":    "ghcr.io/org/postgres:v15",
			"replicas": 5,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxReplicas: int32ptr(3)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestStatefulsetConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy should be a no-op, got: %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_VCTStorageSize(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "100Gi",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxStorageSize: "10Gi"}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when VCT size exceeds max")
	}
}

func TestStatefulsetHandler_WithSharedPodFields(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":      "ghcr.io/org/postgres:v15",
			"workingDir": "/var/lib/postgresql",
			"envFrom": []any{
				map[string]any{"secretRef": map[string]any{"name": "db-secret"}},
			},
			"securityContext": map[string]any{
				"runAsUser": int64(999),
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if sts, ok := (*obj).(*appsv1.StatefulSet); ok {
			c := sts.Spec.Template.Spec.Containers[0]
			if c.WorkingDir != "/var/lib/postgresql" {
				t.Errorf("expected workingDir, got %q", c.WorkingDir)
			}
			if len(c.EnvFrom) != 1 {
				t.Errorf("expected 1 envFrom entry, got %d", len(c.EnvFrom))
			}
			if c.SecurityContext == nil || c.SecurityContext.RunAsUser == nil || *c.SecurityContext.RunAsUser != 999 {
				t.Error("expected runAsUser=999")
			}
			return
		}
	}
	t.Error("StatefulSet not found in output")
}

// TestStatefulsetHandler_NamedProbePort_WithPort_Accepted and
// TestStatefulsetHandler_NamedProbePort_WithoutPort_Error cover launcher#278
// wave-11 finding 5: statefulset's main container is only named "tcp" when
// `port` is set, mirroring daemonset's identical conditional shape (see
// TestDaemonsetHandler_NamedProbePort_WithPort_Accepted).
func TestStatefulsetHandler_NamedProbePort_WithPort_Accepted(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"port":  5432,
			"probes": map[string]any{
				"readiness": map[string]any{
					"tcpSocket": map[string]any{"port": "tcp"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("db", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// TestStatefulsetHandler_NamedProbePort_Mismatch_Error covers launcher#278
// wave-12 finding 3: with a port configured, statefulset names it "tcp" —
// "http" (webservice/daemonset's own name) is syntactically valid but not
// what this container declares, so it must be rejected too.
func TestStatefulsetHandler_NamedProbePort_Mismatch_Error(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"port":  5432,
			"probes": map[string]any{
				"readiness": map[string]any{
					"tcpSocket": map[string]any{"port": "http"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named port that does not match statefulset's declared \"tcp\" container port")
	}
}

func TestStatefulsetHandler_NamedProbePort_WithoutPort_Error(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"probes": map[string]any{
				"readiness": map[string]any{
					"tcpSocket": map[string]any{"port": "tcp"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named probe port when no port is configured")
	}
}

func TestStatefulsetConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"securityContext": map[string]any{
				"privileged": true,
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when privileged=true and policy disallows it")
	}
}

// TestStatefulsetConfig_ApplyPolicy_HostPathDenied is statefulset's sibling of
// TestWebserviceConfig_ApplyPolicy_HostPathDenied (launcher#284, P1) — the
// same shared ApplyPolicy gap, same shared enforceHostPathVolumes fix.
func TestStatefulsetConfig_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumes": []any{
				map[string]any{
					"name":      "logs",
					"type":      "hostPath",
					"mountPath": "/var/log",
					"path":      "/var/log/app",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowHostPathVols: false}); err == nil {
		t.Error("expected error when a hostPath volume is authored and policy disallows it")
	}
}

// TestStatefulsetConfig_ApplyPolicy_CapabilityAddDenied is statefulset's
// sibling of TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied
// (go-kure/launcher#305) — the same shared ApplyPolicy gap, same shared
// enforceContainerCapabilities fix.
func TestStatefulsetConfig_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"securityContext": map[string]any{
				"capabilities": map[string]any{
					"add": []any{"NET_ADMIN"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when capabilities.add includes a forbidden capability")
	}
}

// TestStatefulsetConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault is
// statefulset's sibling of the two webservice
// TestWebserviceConfig_ApplyPolicy_Max{CPU,Memory}_AgainstIntrinsicDefault
// cases (launcher#251) — proving enforceMaxResources is actually wired into
// StatefulsetConfig.ApplyPolicy, not just added to enforce.go.
func TestStatefulsetConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault(t *testing.T) {
	cases := []struct {
		name   string
		policy stubPolicy
	}{
		{"cpu", stubPolicy{maxCPU: "50m"}},
		{"memory", stubPolicy{maxMemory: "64Mi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.StatefulsetHandler{}
			cfg, err := h.ToApplicationConfig(&oam.Component{
				Name: "db",
				Type: "statefulset",
				Properties: map[string]any{
					"image": "ghcr.io/org/postgres:v15",
				},
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			enforceable := cfg.(oam.Enforceable)
			err = enforceable.ApplyPolicy(&tc.policy)
			if err == nil {
				t.Fatal("expected error when the intrinsic default exceeds the enforced maximum")
			}
			if !strings.Contains(err.Error(), "generated default") {
				t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
			}
		})
	}
}

// The eight tests below are statefulset's siblings of the
// TestWebserviceConfig_ApplyPolicy_InitContainer*/Sidecar* tests
// (go-kure/launcher#312) — same shared ApplyPolicy gap, same
// enforceExtraContainer fix.

func TestStatefulsetConfig_ApplyPolicy_InitContainerResourcesDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the init container's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_InitContainerRegistryDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "docker.io/x/y:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("expected error when the init container's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_InitContainerPrivilegedDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{"privileged": true},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when the init container is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_InitContainerCapabilitiesDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{
						"capabilities": map[string]any{"add": []any{"NET_ADMIN"}},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when the init container adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_SidecarResourcesDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{"name": "sidecar", "image": "ghcr.io/org/sidecar:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the sidecar's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_SidecarRegistryDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{"name": "sidecar", "image": "docker.io/x/y:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("expected error when the sidecar's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_SidecarPrivilegedDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name": "sidecar", "image": "ghcr.io/org/sidecar:v1",
					"securityContext": map[string]any{"privileged": true},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when the sidecar is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestStatefulsetConfig_ApplyPolicy_SidecarCapabilitiesDenied(t *testing.T) {
	h := &components.StatefulsetHandler{}
	component := &oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":     "ghcr.io/org/postgres:v15",
			"port":      5432,
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name": "sidecar", "image": "ghcr.io/org/sidecar:v1",
					"securityContext": map[string]any{
						"capabilities": map[string]any{"add": []any{"NET_ADMIN"}},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when the sidecar adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

// TestStatefulsetConfig_ApplyPolicy_MaxStorageSize covers both spellings of the
// requested size. Enforcement read VolumeClaimTemplate.Size directly, which the
// long resources.requests.storage spelling leaves empty, and enforceMaxResource
// treats an empty current value as nothing to check — so authoring the long
// spelling bypassed MaxStorageSize entirely.
func TestStatefulsetConfig_ApplyPolicy_MaxStorageSize(t *testing.T) {
	entry := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "data", "mountPath": "/data"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name    string
		vct     map[string]any
		wantErr bool
	}{
		{"short spelling over the limit", entry(map[string]any{"size": "20Gi"}), true},
		{"short spelling under the limit", entry(map[string]any{"size": "1Gi"}), false},
		{
			"long spelling over the limit",
			entry(map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "20Gi"}}}),
			true,
		},
		{
			"long spelling under the limit",
			entry(map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}),
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.StatefulsetHandler{}
			component := &oam.Component{
				Name: "app",
				Type: "statefulset",
				Properties: map[string]any{
					"image":                "ghcr.io/org/app:v1",
					"serviceName":          "app",
					"volumeClaimTemplates": []any{tc.vct},
				},
			}
			cfg, err := h.ToApplicationConfig(component, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			p := &stubPolicy{}
			p.maxStorageSize = "5Gi"
			err = cfg.(oam.Enforceable).ApplyPolicy(p)
			if tc.wantErr && err == nil {
				t.Fatal("ApplyPolicy succeeded, want the enforced maximum to reject it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ApplyPolicy: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "exceeds enforced maximum") {
				t.Errorf("error = %q, want it to name the enforced maximum", err)
			}
		})
	}
}
