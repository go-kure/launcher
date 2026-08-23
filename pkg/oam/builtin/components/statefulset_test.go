package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

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
