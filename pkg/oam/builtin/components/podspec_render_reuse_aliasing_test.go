package components_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// This file extends the render-reuse contract stated in
// render_reuse_aliasing_test.go (renderTwice) from the three scheduling fields
// go-kure/launcher#413 fixed to the rest of the pod spec buildPodSpec projects
// out of a handler config (go-kure/launcher#425): the authored pod-level
// fields, the appended volumes, and the main, init and sidecar containers.
//
// Every mutation below goes through a NESTED reference — a pointer, map or
// slice element one level below what `ps := in.Config.PodSpec` or
// `append(dst, src...)` copies. A top-level write touches only the copy and
// passes against the aliased code; that shape is kept as the named negative
// control TestDeployment_PodSpecShallowMutationPassesEvenWhenAliased at the
// bottom of this file, so it is on record as insufficient.

// podSpecAliasProps authors at least one nested reference in every group
// buildPodSpec projects.
func podSpecAliasProps() map[string]any {
	return map[string]any{
		"image":   "ghcr.io/org/app:v1",
		"command": []any{"/bin/app"},
		"probes": map[string]any{
			"readiness": map[string]any{
				"httpGet": map[string]any{"path": "/ready", "port": 8080},
			},
		},
		"lifecycle": map[string]any{
			"preStop": map[string]any{
				"exec": map[string]any{"command": []any{"sleep", "5"}},
			},
		},
		"securityContext": map[string]any{"allowPrivilegeEscalation": false},

		// Pod-level fields, carried by `ps := in.Config.PodSpec`.
		"terminationGracePeriodSeconds": 15,
		"nodeSelector":                  map[string]any{"tier": "edge"},
		"podSecurityContext":            map[string]any{"fsGroup": 2000},
		"imagePullSecrets":              []any{map[string]any{"name": "regcred"}},
		"hostAliases":                   []any{map[string]any{"ip": "10.1.1.1", "hostnames": []any{"db"}}},

		"volumes": []any{
			map[string]any{
				"name":      "scratch",
				"type":      "emptyDir",
				"mountPath": "/scratch",
				"sizeLimit": "1Gi",
			},
		},
		"initContainers": []any{
			map[string]any{
				"name":    "setup",
				"image":   "busybox:1",
				"command": []any{"sh", "-c", "true"},
			},
		},
		"sidecars": []any{
			map[string]any{
				"name":    "proxy",
				"image":   "envoy:1",
				"command": []any{"envoy"},
			},
		},
	}
}

// deploymentPodSpec returns the pod spec of the first rendered object, which
// every caller here expects to be the Deployment.
func deploymentPodSpec(t *testing.T, objects []*client.Object) *corev1.PodSpec {
	t.Helper()
	dep, ok := (*objects[0]).(*appsv1.Deployment)
	if !ok {
		t.Fatalf("first object is %T, want *appsv1.Deployment", *objects[0])
	}
	return &dep.Spec.Template.Spec
}

// TestDeployment_PodLevelFieldsRenderingTwiceIsUnaffectedByEditingTheFirstRender
// covers the pod-level fields `ps := in.Config.PodSpec` used to share: a struct
// copy leaves every map, pointer and slice backing array in common with the
// config.
func TestDeployment_PodLevelFieldsRenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", podSpecAliasProps(),
		func(objects []*client.Object) {
			ps := deploymentPodSpec(t, objects)
			if ps.NodeSelector == nil || ps.TerminationGracePeriodSeconds == nil ||
				ps.SecurityContext == nil || ps.SecurityContext.FSGroup == nil ||
				len(ps.ImagePullSecrets) != 1 || len(ps.HostAliases) != 1 || len(ps.HostAliases[0].Hostnames) != 1 {
				t.Fatal("first render is missing an authored pod-level field — nothing to alias, so this test cannot prove anything")
			}
			ps.NodeSelector["tier"] = "MUTATED"
			*ps.TerminationGracePeriodSeconds = 1
			*ps.SecurityContext.FSGroup = 1
			ps.ImagePullSecrets[0].Name = "MUTATED"
			ps.HostAliases[0].Hostnames[0] = "MUTATED"
		})

	ps := deploymentPodSpec(t, second)
	if got := ps.NodeSelector["tier"]; got != "edge" {
		t.Errorf("nodeSelector[tier] = %q, want \"edge\" — the first render's edit leaked back into the config", got)
	}
	if ps.TerminationGracePeriodSeconds == nil {
		t.Error("terminationGracePeriodSeconds is gone from the second render")
	} else if got := *ps.TerminationGracePeriodSeconds; got != 15 {
		t.Errorf("terminationGracePeriodSeconds = %d, want 15 — the first render's edit leaked back into the config", got)
	}
	if ps.SecurityContext == nil || ps.SecurityContext.FSGroup == nil {
		t.Error("podSecurityContext.fsGroup is gone from the second render")
	} else if got := *ps.SecurityContext.FSGroup; got != 2000 {
		t.Errorf("podSecurityContext.fsGroup = %d, want 2000 — the first render's edit leaked back into the config", got)
	}
	if len(ps.ImagePullSecrets) != 1 {
		t.Errorf("imagePullSecrets = %v, want one entry", ps.ImagePullSecrets)
	} else if got := ps.ImagePullSecrets[0].Name; got != "regcred" {
		t.Errorf("imagePullSecrets[0].name = %q, want \"regcred\" — the first render's edit leaked back into the config", got)
	}
	if len(ps.HostAliases) != 1 || len(ps.HostAliases[0].Hostnames) != 1 {
		t.Errorf("hostAliases = %v, want one alias with one hostname", ps.HostAliases)
	} else if got := ps.HostAliases[0].Hostnames[0]; got != "db" {
		t.Errorf("hostAliases[0].hostnames[0] = %q, want \"db\" — the first render's edit leaked back into the config", got)
	}
}

// TestDeployment_VolumesRenderingTwiceIsUnaffectedByEditingTheFirstRender covers
// `ps.Volumes = append(ps.Volumes, in.Volumes...)`: the append copies each
// Volume struct but not the *...VolumeSource pointer inside it.
func TestDeployment_VolumesRenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", podSpecAliasProps(),
		func(objects []*client.Object) {
			ps := deploymentPodSpec(t, objects)
			if len(ps.Volumes) != 1 || ps.Volumes[0].EmptyDir == nil {
				t.Fatal("first render has no emptyDir volume — nothing to alias, so this test cannot prove anything")
			}
			ps.Volumes[0].EmptyDir.Medium = corev1.StorageMediumMemory
			ps.Volumes[0].EmptyDir.SizeLimit = nil
		})

	ps := deploymentPodSpec(t, second)
	if len(ps.Volumes) != 1 || ps.Volumes[0].EmptyDir == nil {
		t.Fatalf("volumes = %v, want one emptyDir volume", ps.Volumes)
	}
	if got := ps.Volumes[0].EmptyDir.Medium; got != corev1.StorageMediumDefault {
		t.Errorf("volumes[0].emptyDir.medium = %q, want the default — the first render's edit leaked back into the config", got)
	}
	if ps.Volumes[0].EmptyDir.SizeLimit == nil {
		t.Error("volumes[0].emptyDir.sizeLimit is gone from the second render — the first render's edit leaked back into the config")
	} else if got := ps.Volumes[0].EmptyDir.SizeLimit.String(); got != "1Gi" {
		t.Errorf("volumes[0].emptyDir.sizeLimit = %q, want 1Gi", got)
	}
}

// TestDeployment_ContainersRenderingTwiceIsUnaffectedByEditingTheFirstRender
// covers the three container appends. Each built container carries the
// config's own command slice, probe and lifecycle pointers and security
// context fields, so the dereference into ps.Containers / ps.InitContainers
// shares all of them.
func TestDeployment_ContainersRenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", podSpecAliasProps(),
		func(objects []*client.Object) {
			ps := deploymentPodSpec(t, objects)
			if len(ps.Containers) != 2 || len(ps.InitContainers) != 1 {
				t.Fatalf("first render has %d containers and %d init containers, want 2 and 1", len(ps.Containers), len(ps.InitContainers))
			}
			main := &ps.Containers[0]
			if len(main.Command) != 1 || main.ReadinessProbe == nil || main.ReadinessProbe.HTTPGet == nil ||
				main.Lifecycle == nil || main.Lifecycle.PreStop == nil || main.Lifecycle.PreStop.Exec == nil ||
				len(main.Lifecycle.PreStop.Exec.Command) != 2 ||
				main.SecurityContext == nil || main.SecurityContext.AllowPrivilegeEscalation == nil {
				t.Fatal("first render's main container is missing an authored nested field — nothing to alias, so this test cannot prove anything")
			}
			if len(ps.InitContainers[0].Command) != 3 || len(ps.Containers[1].Command) != 1 {
				t.Fatal("first render's init/sidecar container has no authored command — nothing to alias, so this test cannot prove anything")
			}
			main.Command[0] = "MUTATED"
			main.ReadinessProbe.HTTPGet.Path = "/MUTATED"
			main.Lifecycle.PreStop.Exec.Command[1] = "999"
			*main.SecurityContext.AllowPrivilegeEscalation = true
			ps.InitContainers[0].Command[2] = "MUTATED"
			ps.Containers[1].Command[0] = "MUTATED"
		})

	ps := deploymentPodSpec(t, second)
	if len(ps.Containers) != 2 || len(ps.InitContainers) != 1 {
		t.Fatalf("second render has %d containers and %d init containers, want 2 and 1", len(ps.Containers), len(ps.InitContainers))
	}
	main := ps.Containers[0]
	if len(main.Command) != 1 || main.Command[0] != "/bin/app" {
		t.Errorf("main command = %v, want [/bin/app] — the first render's edit leaked back into the config", main.Command)
	}
	if main.ReadinessProbe == nil || main.ReadinessProbe.HTTPGet == nil {
		t.Error("main readinessProbe.httpGet is gone from the second render")
	} else if got := main.ReadinessProbe.HTTPGet.Path; got != "/ready" {
		t.Errorf("main readinessProbe.httpGet.path = %q, want \"/ready\" — the first render's edit leaked back into the config", got)
	}
	if main.Lifecycle == nil || main.Lifecycle.PreStop == nil || main.Lifecycle.PreStop.Exec == nil {
		t.Error("main lifecycle.preStop.exec is gone from the second render")
	} else if got := main.Lifecycle.PreStop.Exec.Command; len(got) != 2 || got[1] != "5" {
		t.Errorf("main lifecycle.preStop.exec.command = %v, want [sleep 5] — the first render's edit leaked back into the config", got)
	}
	if main.SecurityContext == nil || main.SecurityContext.AllowPrivilegeEscalation == nil {
		t.Error("main securityContext.allowPrivilegeEscalation is gone from the second render")
	} else if *main.SecurityContext.AllowPrivilegeEscalation {
		t.Error("main securityContext.allowPrivilegeEscalation = true, want false — the first render's edit leaked back into the config")
	}
	if got := ps.InitContainers[0].Command; len(got) != 3 || got[2] != "true" {
		t.Errorf("initContainers[0].command = %v, want [sh -c true] — the first render's edit leaked back into the config", got)
	}
	if got := ps.Containers[1].Command; len(got) != 1 || got[0] != "envoy" {
		t.Errorf("sidecar command = %v, want [envoy] — the first render's edit leaked back into the config", got)
	}
}

// TestDeployment_PodSpecShallowMutationPassesEvenWhenAliased is a negative
// control, not a guarantee. Each write replaces a value held directly in a
// struct the projection already copies — a pod-level pointer field swapped for
// a new pointer, a top-level field of an appended Volume or Container — so it
// passes whether or not the nested references are shared, and it passed
// against the aliased code. It is on record so the obvious-looking version of
// the three tests above is not mistaken for one.
//
// If it ever FAILS, the element slices or the PodSpec struct itself are shared,
// which is a broader regression than the nested-reference tests target.
func TestDeployment_PodSpecShallowMutationPassesEvenWhenAliased(t *testing.T) {
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", podSpecAliasProps(),
		func(objects []*client.Object) {
			ps := deploymentPodSpec(t, objects)
			if len(ps.Volumes) == 0 || len(ps.Containers) == 0 || len(ps.InitContainers) == 0 {
				t.Fatal("first render has no volume, container or init container — nothing to mutate, so this control cannot prove anything")
			}
			one := int64(1)
			ps.TerminationGracePeriodSeconds = &one
			ps.Volumes[0].Name = "mutated-shallow"
			ps.Containers[0].Image = "MUTATED-SHALLOW"
			ps.InitContainers[0].Image = "MUTATED-SHALLOW"
		})

	ps := deploymentPodSpec(t, second)
	if len(ps.Volumes) == 0 || len(ps.Containers) == 0 || len(ps.InitContainers) == 0 {
		t.Fatal("second render has no volume, container or init container — the slices themselves were lost")
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 15 {
		t.Errorf("terminationGracePeriodSeconds = %v, want 15 — the PodSpec struct itself is shared", ps.TerminationGracePeriodSeconds)
	}
	if got := ps.Volumes[0].Name; got != "scratch" {
		t.Errorf("volumes[0].name = %q, want \"scratch\" — the element structs are shared", got)
	}
	if got := ps.Containers[0].Image; got != "ghcr.io/org/app:v1" {
		t.Errorf("containers[0].image = %q, want the authored image — the element structs are shared", got)
	}
	if got := ps.InitContainers[0].Image; got != "busybox:1" {
		t.Errorf("initContainers[0].image = %q, want \"busybox:1\" — the element structs are shared", got)
	}
}
