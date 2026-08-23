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

func TestDaemonsetHandler_CanHandle(t *testing.T) {
	h := &components.DaemonsetHandler{}
	if !h.CanHandle("daemonset") {
		t.Error("expected true for daemonset")
	}
	if h.CanHandle("worker") {
		t.Error("expected false for worker")
	}
}

func TestDaemonsetHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "agent",
		Type:       "daemonset",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestDaemonsetHandler_Generate_ResourceTypes(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDS, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.DaemonSet:
			foundDS = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDS {
		t.Error("expected DaemonSet")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestDaemonsetHandler_NoReplicas(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	// DaemonsetConfig must not implement MaxReplicas enforcement (no replicas field)
	if _, ok := cfg.(interface{ Replicas() int32 }); ok {
		t.Error("DaemonsetConfig should not expose Replicas()")
	}
}

func TestDaemonsetConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
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

func TestDaemonsetConfig_ApplyPolicy_AllowedRegistries(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "docker.io/library/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{allowedRegistries: []string{"ghcr.io"}}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error for disallowed registry")
	}
}

func TestDaemonsetHandler_Tolerations_NonStringKey_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":    123,
					"effect": "NoSchedule",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for non-string toleration key")
	}
}

func TestDaemonsetHandler_Tolerations_InvalidOperator_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "Contains",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid toleration operator")
	}
}

func TestDaemonsetHandler_Tolerations_InvalidEffect_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":    "node-role.kubernetes.io/control-plane",
					"effect": "NoRun",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid toleration effect")
	}
}

func TestDaemonsetHandler_WithTolerations(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "Exists",
					"effect":   "NoSchedule",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if ds, ok := (*obj).(*appsv1.DaemonSet); ok {
			if len(ds.Spec.Template.Spec.Tolerations) != 1 {
				t.Errorf("expected 1 toleration, got %d", len(ds.Spec.Template.Spec.Tolerations))
			}
			return
		}
	}
	t.Error("DaemonSet not found")
}

func TestDaemonsetConfig_WithPort(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"port":  9090,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	dc, ok := cfg.(*components.DaemonsetConfig)
	if !ok {
		t.Fatalf("expected *DaemonsetConfig, got %T", cfg)
	}
	if dc.Port != 9090 {
		t.Errorf("Port = %d, want 9090", dc.Port)
	}
	if dc.ServicePort() != 9090 {
		t.Errorf("ServicePort() = %d, want 9090", dc.ServicePort())
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDS, foundSvc, foundSA bool
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *appsv1.DaemonSet:
			foundDS = true
			// Verify container port
			if len(o.Spec.Template.Spec.Containers) == 0 {
				t.Fatal("no containers in DaemonSet")
			}
			ports := o.Spec.Template.Spec.Containers[0].Ports
			if len(ports) == 0 || ports[0].ContainerPort != 9090 {
				t.Errorf("container port = %v, want [{http 9090}]", ports)
			}
		case *corev1.Service:
			foundSvc = true
			if len(o.Spec.Ports) == 0 || o.Spec.Ports[0].Port != 9090 {
				t.Errorf("service port = %v, want 9090", o.Spec.Ports)
			}
			if o.Spec.Type != corev1.ServiceTypeClusterIP {
				t.Errorf("service type = %q, want ClusterIP", o.Spec.Type)
			}
			if o.Name != "agent" {
				t.Errorf("service name = %q, want \"agent\"", o.Name)
			}
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDS {
		t.Error("expected DaemonSet")
	}
	if !foundSvc {
		t.Error("expected Service when port > 0")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
	// Verify object order: DaemonSet → Service → ServiceAccount
	if len(objects) < 3 {
		t.Fatalf("expected at least 3 objects, got %d", len(objects))
	}
	if _, ok := (*objects[0]).(*appsv1.DaemonSet); !ok {
		t.Errorf("objects[0] = %T, want *DaemonSet", *objects[0])
	}
	if _, ok := (*objects[1]).(*corev1.Service); !ok {
		t.Errorf("objects[1] = %T, want *Service", *objects[1])
	}
	if _, ok := (*objects[2]).(*corev1.ServiceAccount); !ok {
		t.Errorf("objects[2] = %T, want *ServiceAccount", *objects[2])
	}
}

func TestDaemonsetConfig_WithoutPort(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if _, ok := (*obj).(*corev1.Service); ok {
			t.Error("expected no Service when port is not set")
		}
	}
	if len(objects) != 2 {
		t.Errorf("expected 2 objects (DaemonSet + ServiceAccount), got %d", len(objects))
	}
}

func TestDaemonsetHandler_ServicePortName_IsHttp(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "node-exporter",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "prom/node-exporter:v1.0.0",
			"port":  float64(9100),
		},
	}, "monitoring")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("node-exporter", "monitoring", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, objPtr := range objects {
		switch obj := (*objPtr).(type) {
		case *corev1.Service:
			for _, p := range obj.Spec.Ports {
				if p.Name != "http" {
					t.Errorf("Service port name = %q, want %q", p.Name, "http")
				}
			}
		case *appsv1.DaemonSet:
			for _, c := range obj.Spec.Template.Spec.Containers {
				for _, p := range c.Ports {
					if p.Name != "http" {
						t.Errorf("DaemonSet container port name = %q, want %q", p.Name, "http")
					}
				}
			}
		}
	}
}

// TestDaemonsetHandler_NamedProbePort_WithPort_Accepted and
// TestDaemonsetHandler_NamedProbePort_WithoutPort_Error cover launcher#278
// wave-11 finding 5: daemonset's main container is only named "http" when
// `port` is set (see TestDaemonsetConfig_WithoutPort above) — so unlike
// worker/cronjob, whether a named probe/lifecycle port resolves depends on
// the specific component instance, not the kind alone.
func TestDaemonsetHandler_NamedProbePort_WithPort_Accepted(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"port":  9090,
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/healthz"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("agent", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// TestDaemonsetHandler_NamedProbePort_Mismatch_Error covers launcher#278
// wave-12 finding 3: with a port configured, daemonset names it "http" —
// "tcp" (statefulset's own name) is syntactically valid but not what this
// container declares, so it must be rejected too.
func TestDaemonsetHandler_NamedProbePort_Mismatch_Error(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"port":  9090,
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"port": "tcp", "path": "/healthz"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named port that does not match daemonset's declared \"http\" container port")
	}
}

func TestDaemonsetHandler_NamedProbePort_WithoutPort_Error(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/healthz"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named probe port when no port is configured")
	}
}

func TestDaemonsetHandler_WithSharedPodFields(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image":      "ghcr.io/org/agent:v1.0.0",
			"workingDir": "/agent",
			"envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "agent-cfg"}},
			},
			"lifecycle": map[string]any{
				"postStart": map[string]any{
					"httpGet": map[string]any{"path": "/started", "port": 8080},
				},
			},
			"securityContext": map[string]any{
				"privileged": true,
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if ds, ok := (*obj).(*appsv1.DaemonSet); ok {
			c := ds.Spec.Template.Spec.Containers[0]
			if c.WorkingDir != "/agent" {
				t.Errorf("expected workingDir, got %q", c.WorkingDir)
			}
			if len(c.EnvFrom) != 1 {
				t.Errorf("expected 1 envFrom entry, got %d", len(c.EnvFrom))
			}
			if c.Lifecycle == nil || c.Lifecycle.PostStart == nil || c.Lifecycle.PostStart.HTTPGet == nil {
				t.Error("expected postStart.httpGet lifecycle hook")
			}
			if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
				t.Error("expected privileged=true")
			}
			return
		}
	}
	t.Error("DaemonSet not found in output")
}

func TestDaemonsetConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
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
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

// TestDaemonsetConfig_ApplyPolicy_HostPathDenied is daemonset's sibling of
// TestWebserviceConfig_ApplyPolicy_HostPathDenied (launcher#284, P1) — the
// same shared ApplyPolicy gap, same shared enforceHostPathVolumes fix.
func TestDaemonsetConfig_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
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

// TestDaemonsetConfig_ApplyPolicy_CapabilityAddDenied is daemonset's sibling
// of TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied
// (go-kure/launcher#305) — the same shared ApplyPolicy gap, same shared
// enforceContainerCapabilities fix.
func TestDaemonsetConfig_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
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

// TestDaemonsetConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault is
// daemonset's sibling of the two webservice
// TestWebserviceConfig_ApplyPolicy_Max{CPU,Memory}_AgainstIntrinsicDefault
// cases (launcher#251) — proving enforceMaxResources is actually wired into
// DaemonsetConfig.ApplyPolicy, not just added to enforce.go.
func TestDaemonsetConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault(t *testing.T) {
	cases := []struct {
		name   string
		policy stubPolicy
	}{
		{"cpu", stubPolicy{maxCPU: "50m"}},
		{"memory", stubPolicy{maxMemory: "64Mi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.DaemonsetHandler{}
			cfg, err := h.ToApplicationConfig(&oam.Component{
				Name: "agent",
				Type: "daemonset",
				Properties: map[string]any{
					"image": "ghcr.io/org/agent:v1.0.0",
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
