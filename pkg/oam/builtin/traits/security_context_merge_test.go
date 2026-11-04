package traits_test

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// objStub emits obj, so a test can hand the trait a workload that already
// carries the security contexts a component authored (go-kure/launcher#1171).
// It emits a copy unless share is set; a test that checks the result does not
// alias what was authored sets it and keeps its own pointers into obj.
type objStub struct {
	obj   client.Object
	share bool
}

func (o *objStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	obj := o.obj
	if !o.share {
		obj = o.obj.DeepCopyObject().(client.Object)
	}
	return []*client.Object{&obj}, nil
}

// authoredDeployment is a Deployment "web" whose pod spec is built by mut.
func authoredDeployment(mut func(*corev1.PodSpec)) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "example:latest"}},
		}}},
	}
	if mut != nil {
		mut(&dep.Spec.Template.Spec)
	}
	return dep
}

// runSecurityContextTrait runs the trait over obj and returns the resulting pod
// spec, or the error the build raised.
func runSecurityContextTrait(obj *appsv1.Deployment, props map[string]any) (*corev1.PodSpec, error) {
	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("web", "ns", &objStub{obj: obj})
	if err := h.Apply(&oam.Trait{Type: "security-context", Properties: props}, app, newBundle()); err != nil {
		return nil, err
	}
	resources, err := app.Generate()
	if err != nil {
		return nil, err
	}
	return &(*resources[0]).(*appsv1.Deployment).Spec.Template.Spec, nil
}

func mustApply(t *testing.T, obj *appsv1.Deployment, props map[string]any) *corev1.PodSpec {
	t.Helper()
	spec, err := runSecurityContextTrait(obj, props)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return spec
}

func mustRefuse(t *testing.T, obj *appsv1.Deployment, props map[string]any, want ...string) {
	t.Helper()
	_, err := runSecurityContextTrait(obj, props)
	if err == nil {
		t.Fatalf("expected a refusal naming %q, got none", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %q", err, w)
		}
	}
}

func bptr(b bool) *bool     { return &b }
func i64ptr(n int64) *int64 { return &n }

func hasCap(caps []corev1.Capability, want corev1.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// The reproduction of go-kure/launcher#1171: an authored capability list and a
// Localhost seccomp profile survive the trait, and an explicit runAsNonRoot
// reaches the pod and the container at baseline.
func TestSecurityContextMerge_IssueReproduction(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.Containers[0].SecurityContext = &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add:  []corev1.Capability{"NET_BIND_SERVICE"},
				Drop: []corev1.Capability{"ALL"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type:             corev1.SeccompProfileTypeLocalhost,
				LocalhostProfile: new("profiles/app.json"),
			},
		}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "baseline", "runAsNonRoot": true})

	sc := spec.Containers[0].SecurityContext
	if sc.Capabilities == nil || !hasCap(sc.Capabilities.Add, "NET_BIND_SERVICE") || !hasCap(sc.Capabilities.Drop, "ALL") {
		t.Errorf("authored capabilities lost: %+v", sc.Capabilities)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeLocalhost ||
		sc.SeccompProfile.LocalhostProfile == nil || *sc.SeccompProfile.LocalhostProfile != "profiles/app.json" {
		t.Errorf("authored seccomp profile lost: %+v", sc.SeccompProfile)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("explicit runAsNonRoot missing on the container at baseline")
	}
	if spec.SecurityContext.RunAsNonRoot == nil || !*spec.SecurityContext.RunAsNonRoot {
		t.Error("explicit runAsNonRoot missing on the pod at baseline")
	}
}

func TestSecurityContextMerge_ExplicitRunAsNonRootAtEveryLevel(t *testing.T) {
	for _, level := range []string{"baseline", "privileged"} {
		spec := mustApply(t, authoredDeployment(nil), map[string]any{"psaLevel": level, "runAsNonRoot": true})
		if rn := spec.SecurityContext.RunAsNonRoot; rn == nil || !*rn {
			t.Errorf("%s: pod runAsNonRoot not written", level)
		}
		if rn := spec.Containers[0].SecurityContext.RunAsNonRoot; rn == nil || !*rn {
			t.Errorf("%s: container runAsNonRoot not written", level)
		}
	}
}

// Restricted unions ALL into an authored drop list instead of replacing it.
func TestSecurityContextMerge_RestrictedKeepsAuthoredDrop(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.Containers[0].SecurityContext = &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"NET_RAW"}},
		}
	})
	sc := mustApply(t, dep, map[string]any{"psaLevel": "restricted"}).Containers[0].SecurityContext
	if !hasCap(sc.Capabilities.Drop, "NET_RAW") || !hasCap(sc.Capabilities.Drop, "ALL") {
		t.Errorf("drop = %v, want NET_RAW and ALL", sc.Capabilities.Drop)
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("restricted default allowPrivilegeEscalation=false missing")
	}
}

// A restricted pod whose authored pod-level seccomp profile is Localhost keeps
// it on the container: the container default follows the pod, it does not
// shadow it with RuntimeDefault.
func TestSecurityContextMerge_RestrictedContainerSeccompFollowsAuthoredPod(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.SecurityContext = &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: new("profiles/app.json"),
		}}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "restricted"})
	if got := spec.SecurityContext.SeccompProfile; got.Type != corev1.SeccompProfileTypeLocalhost {
		t.Errorf("pod seccomp = %+v", got)
	}
	if got := spec.Containers[0].SecurityContext.SeccompProfile; got == nil || got.Type != corev1.SeccompProfileTypeLocalhost {
		t.Errorf("container seccomp = %+v, want the pod's Localhost profile", got)
	}
}

// Fields the trait does not own survive, at pod and container level.
func TestSecurityContextMerge_KeepsFieldsTheTraitDoesNotOwn(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.SecurityContext = &corev1.PodSecurityContext{
			Sysctls:            []corev1.Sysctl{{Name: "net.ipv4.ip_unprivileged_port_start", Value: "80"}},
			SupplementalGroups: []int64{4000},
			SELinuxOptions:     &corev1.SELinuxOptions{Level: "s0:c1,c2"},
		}
		p.Containers[0].SecurityContext = &corev1.SecurityContext{
			RunAsUser:      i64ptr(2000),
			SELinuxOptions: &corev1.SELinuxOptions{Level: "s0:c1,c2"},
			AppArmorProfile: &corev1.AppArmorProfile{
				Type: corev1.AppArmorProfileTypeRuntimeDefault,
			},
		}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "baseline"})
	if len(spec.SecurityContext.Sysctls) != 1 || len(spec.SecurityContext.SupplementalGroups) != 1 || spec.SecurityContext.SELinuxOptions == nil {
		t.Errorf("pod context lost fields: %+v", spec.SecurityContext)
	}
	sc := spec.Containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 2000 || sc.SELinuxOptions == nil || sc.AppArmorProfile == nil {
		t.Errorf("container context lost fields: %+v", sc)
	}
}

// Init containers and sidecars each keep their own authored context; a bare
// container gets the trait's.
func TestSecurityContextMerge_InitAndSidecarKeepTheirOwn(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.Containers = append(p.Containers,
			corev1.Container{Name: "sidecar", Image: "side:1", SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CHOWN"}},
			}})
		p.InitContainers = []corev1.Container{{Name: "init", Image: "busybox", SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: bptr(false),
		}}}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "baseline", "runAsNonRoot": true})
	if sc := spec.Containers[1].SecurityContext; sc.Capabilities == nil || !hasCap(sc.Capabilities.Add, "CHOWN") {
		t.Errorf("sidecar lost its capabilities: %+v", sc)
	}
	if sc := spec.InitContainers[0].SecurityContext; sc.ReadOnlyRootFilesystem == nil || *sc.ReadOnlyRootFilesystem {
		t.Errorf("init container lost readOnlyRootFilesystem=false: %+v", sc)
	}
	for _, c := range append(append([]corev1.Container{}, spec.Containers...), spec.InitContainers...) {
		if c.SecurityContext.RunAsNonRoot == nil || !*c.SecurityContext.RunAsNonRoot {
			t.Errorf("container %q: trait runAsNonRoot missing", c.Name)
		}
	}
	if spec.Containers[0].SecurityContext == spec.Containers[1].SecurityContext {
		t.Error("two containers share one SecurityContext pointer")
	}
}

// The result shares no pointer, or anything a pointer reaches, with the
// contexts the component authored, with the trait's own properties, or between
// the pod and its containers. The stub hands the trait the authored object
// itself and the test keeps its pointers into it, then writes through the
// result's pointees: a merge that kept an authored context, or a trait value, as
// it was would let the write show through.
func TestSecurityContextMerge_SharesNothingWithItsInputs(t *testing.T) {
	authoredPod := &corev1.PodSecurityContext{
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		Sysctls:        []corev1.Sysctl{{Name: "net.ipv4.tcp_syncookies", Value: "1"}},
	}
	authoredApp := &corev1.SecurityContext{
		RunAsUser:    i64ptr(10),
		Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"NET_RAW"}},
	}
	authoredInit := &corev1.SecurityContext{RunAsUser: i64ptr(10)}
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.SecurityContext = authoredPod
		p.Containers[0].SecurityContext = authoredApp
		p.InitContainers = []corev1.Container{{Name: "init", Image: "b", SecurityContext: authoredInit}}
	})

	h := &traits.SecurityContextHandler{}
	app := stack.NewApplication("web", "ns", &objStub{obj: dep, share: true})
	props := map[string]any{"psaLevel": "restricted", "runAsNonRoot": true, "runAsGroup": int64(20)}
	if err := h.Apply(&oam.Trait{Type: "security-context", Properties: props}, app, newBundle()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	spec := &(*resources[0]).(*appsv1.Deployment).Spec.Template.Spec

	// Mutate every pointee the result holds.
	scribble := func(sc *corev1.SecurityContext) {
		*sc.RunAsUser = 99
		*sc.RunAsNonRoot = false
		*sc.AllowPrivilegeEscalation = true
		sc.Capabilities.Drop[0] = "SYS_ADMIN"
		sc.SeccompProfile.Type = corev1.SeccompProfileTypeUnconfined
	}
	scribble(spec.Containers[0].SecurityContext)
	if spec.InitContainers[0].SecurityContext.RunAsUser == nil {
		t.Fatal("init container lost its authored runAsUser")
	}
	*spec.InitContainers[0].SecurityContext.RunAsUser = 99
	*spec.InitContainers[0].SecurityContext.RunAsNonRoot = false
	spec.SecurityContext.SeccompProfile.Type = corev1.SeccompProfileTypeUnconfined
	spec.SecurityContext.Sysctls[0].Value = "0"
	*spec.SecurityContext.RunAsGroup = 99

	if *authoredApp.RunAsUser != 10 || authoredApp.Capabilities.Drop[0] != "NET_RAW" {
		t.Errorf("the authored container context shows the write: %+v", authoredApp)
	}
	if *authoredInit.RunAsUser != 10 {
		t.Errorf("the authored init container context shows the write: %+v", authoredInit)
	}
	if authoredPod.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || authoredPod.Sysctls[0].Value != "1" {
		t.Errorf("the authored pod context shows the write: %+v", authoredPod)
	}

	// The write on the app container did not reach a value the init container or
	// the pod holds: each got its own copy of the trait's runAsNonRoot and the
	// restricted defaults.
	if rn := spec.SecurityContext.RunAsNonRoot; rn == nil || !*rn {
		t.Errorf("the pod runAsNonRoot shows a write made on a container: %v", rn)
	}
	if aps := spec.InitContainers[0].SecurityContext.AllowPrivilegeEscalation; aps == nil || *aps {
		t.Errorf("the init container allowPrivilegeEscalation shows a write made on the app container: %v", aps)
	}
	if got := spec.InitContainers[0].SecurityContext.Capabilities.Drop; len(got) != 1 || got[0] != "ALL" {
		t.Errorf("the init container capabilities show a write made on the app container: %v", got)
	}
}

// A trait property and a different authored value is a refusal that names
// the field, both values and where the authored value sits.
func TestSecurityContextMerge_ConflictIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*corev1.PodSpec)
		props map[string]any
		want  []string
	}{
		{"pod runAsUser", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{RunAsUser: i64ptr(2000)}
		}, map[string]any{"psaLevel": "baseline", "runAsUser": int64(1000)}, []string{"runAsUser", "1000", "2000", `Deployment "web"`}},
		{"pod fsGroup", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{FSGroup: i64ptr(5)}
		}, map[string]any{"psaLevel": "baseline", "fsGroup": int64(6)}, []string{"fsGroup", "5", "6"}},
		{"container readOnlyRootFilesystem", func(p *corev1.PodSpec) {
			p.Containers[0].SecurityContext = &corev1.SecurityContext{ReadOnlyRootFilesystem: bptr(false)}
		}, map[string]any{"psaLevel": "baseline", "readOnlyRootFilesystem": true}, []string{"readOnlyRootFilesystem", `container "app"`}},
		{"container runAsNonRoot", func(p *corev1.PodSpec) {
			p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsNonRoot: bptr(false)}
		}, map[string]any{"psaLevel": "baseline", "runAsNonRoot": true}, []string{"runAsNonRoot", `container "app"`}},
		{"init container allowPrivilegeEscalation", func(p *corev1.PodSpec) {
			p.InitContainers = []corev1.Container{{Name: "init", Image: "b", SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: bptr(true)}}}
		}, map[string]any{"psaLevel": "baseline", "allowPrivilegeEscalation": false}, []string{"allowPrivilegeEscalation", `init container "init"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustRefuse(t, authoredDeployment(tc.mut), tc.props, append(tc.want, "security-context", `"web"`)...)
		})
	}
}

func TestSecurityContextMerge_EqualValuesAreAccepted(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.SecurityContext = &corev1.PodSecurityContext{RunAsUser: i64ptr(1000)}
		p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: i64ptr(1000), RunAsNonRoot: bptr(true)}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "baseline", "runAsUser": int64(1000), "runAsNonRoot": true})
	if got := *spec.Containers[0].SecurityContext.RunAsUser; got != 1000 {
		t.Errorf("runAsUser = %d", got)
	}
}

// A trait pod-level runAsUser/runAsGroup is overridden at run time by a
// different container-level value, so that is the same refusal. An equal value
// passes, and a container that leaves the field unset inherits it.
func TestSecurityContextMerge_ContainerValueShadowingATraitPodProperty(t *testing.T) {
	shadow := func(field string, v int64) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			sc := &corev1.SecurityContext{}
			if field == "runAsUser" {
				sc.RunAsUser = &v
			} else {
				sc.RunAsGroup = &v
			}
			p.Containers[0].SecurityContext = sc
		}
	}
	props := map[string]any{"psaLevel": "baseline", "runAsUser": int64(1000), "runAsGroup": int64(1000)}

	mustRefuse(t, authoredDeployment(shadow("runAsUser", 2000)), props, "runAsUser", "1000", "2000", `container "app"`)
	mustRefuse(t, authoredDeployment(shadow("runAsGroup", 2000)), props, "runAsGroup", "1000", "2000", `container "app"`)
	mustApply(t, authoredDeployment(shadow("runAsUser", 1000)), props)
	mustApply(t, authoredDeployment(nil), props)

	// An init container shadows the pod value the same way.
	init := func(p *corev1.PodSpec) {
		p.InitContainers = []corev1.Container{{Name: "init", Image: "b", SecurityContext: &corev1.SecurityContext{RunAsUser: i64ptr(0)}}}
	}
	mustRefuse(t, authoredDeployment(init), props, "runAsUser", `init container "init"`)
}

// The level check runs on the merged result at baseline and restricted and
// never at privileged (go-kure/launcher#1171, Pod Security Standards).
func TestSecurityContextMerge_LevelMatrix(t *testing.T) {
	withContainerSC := func(sc *corev1.SecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = sc }
	}
	cases := []struct {
		name        string
		mut         func(*corev1.PodSpec)
		baseline    bool // accepted at baseline
		restricted  bool // accepted at restricted
		wantMessage string
	}{
		{"privileged true", withContainerSC(&corev1.SecurityContext{Privileged: bptr(true)}), false, false, "privileged"},
		{"NET_BIND_SERVICE", withContainerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"NET_BIND_SERVICE"}}}), true, true, ""},
		{"CHOWN", withContainerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CHOWN"}}}), true, false, "CHOWN"},
		{"SYS_ADMIN", withContainerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}}), false, false, "SYS_ADMIN"},
		{"hostProcess", withContainerSC(&corev1.SecurityContext{WindowsOptions: &corev1.WindowsSecurityContextOptions{HostProcess: bptr(true)}}), false, false, "hostProcess"},
		{"pod hostProcess", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{WindowsOptions: &corev1.WindowsSecurityContextOptions{HostProcess: bptr(true)}}
		}, false, false, "hostProcess"},
		{"procMount Unmasked", withContainerSC(&corev1.SecurityContext{ProcMount: new(corev1.UnmaskedProcMount)}), false, false, "procMount"},
		{"seccomp Unconfined", withContainerSC(&corev1.SecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}}), false, false, "Unconfined"},
		{"pod seccomp Unconfined", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}}
		}, false, false, "Unconfined"},
		{"appArmor Unconfined", withContainerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}}), false, false, "appArmorProfile"},
		{"seccomp unknown type", withContainerSC(&corev1.SecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: "Bogus"}}), false, false, "Bogus"},
		{"appArmor unknown type", withContainerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: "Bogus"}}), false, false, "Bogus"},
		{"pod appArmor unknown type", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: "Bogus"}}
		}, false, false, "Bogus"},
		{"appArmor Localhost", withContainerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeLocalhost, LocalhostProfile: new("profile")}}), true, true, ""},
		{"seLinux user", withContainerSC(&corev1.SecurityContext{SELinuxOptions: &corev1.SELinuxOptions{User: "root"}}), false, false, "seLinuxOptions"},
		{"seLinux role", withContainerSC(&corev1.SecurityContext{SELinuxOptions: &corev1.SELinuxOptions{Role: "r"}}), false, false, "seLinuxOptions"},
		{"seLinux type container_t", withContainerSC(&corev1.SecurityContext{SELinuxOptions: &corev1.SELinuxOptions{Type: "container_t"}}), true, true, ""},
		{"seLinux type spc_t", withContainerSC(&corev1.SecurityContext{SELinuxOptions: &corev1.SELinuxOptions{Type: "spc_t"}}), false, false, "seLinuxOptions"},
		{"unsafe sysctl", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{Sysctls: []corev1.Sysctl{{Name: "kernel.msgmax", Value: "1"}}}
		}, false, false, "kernel.msgmax"},
		{"safe sysctl", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{Sysctls: []corev1.Sysctl{{Name: "net.ipv4.tcp_syncookies", Value: "1"}}}
		}, true, true, ""},
		{"every safe sysctl of the standard", func(p *corev1.PodSpec) {
			var all []corev1.Sysctl
			for _, n := range []string{
				"kernel.shm_rmid_forced", "net.ipv4.ip_local_port_range", "net.ipv4.ip_unprivileged_port_start",
				"net.ipv4.tcp_syncookies", "net.ipv4.ping_group_range", "net.ipv4.ip_local_reserved_ports",
				"net.ipv4.tcp_keepalive_time", "net.ipv4.tcp_fin_timeout", "net.ipv4.tcp_keepalive_intvl",
				"net.ipv4.tcp_keepalive_probes", "net.ipv4.tcp_rmem", "net.ipv4.tcp_wmem",
				"net.ipv4.tcp_slow_start_after_idle", "net.ipv4.tcp_notsent_lowat",
			} {
				all = append(all, corev1.Sysctl{Name: n, Value: "1"})
			}
			p.SecurityContext = &corev1.PodSecurityContext{Sysctls: all}
		}, true, true, ""},
		{"init container privileged", func(p *corev1.PodSpec) {
			p.InitContainers = []corev1.Container{{Name: "init", Image: "b", SecurityContext: &corev1.SecurityContext{Privileged: bptr(true)}}}
		}, false, false, `init container "init"`},
		{"sidecar SYS_ADMIN", func(p *corev1.PodSpec) {
			p.Containers = append(p.Containers, corev1.Container{Name: "side", Image: "s", SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}}})
		}, false, false, `container "side"`},
		// Restricted only.
		{"allowPrivilegeEscalation true", withContainerSC(&corev1.SecurityContext{AllowPrivilegeEscalation: bptr(true)}), true, false, "allowPrivilegeEscalation"},
		{"runAsNonRoot false", withContainerSC(&corev1.SecurityContext{RunAsNonRoot: bptr(false)}), true, false, "runAsNonRoot"},
		{"pod runAsNonRoot false", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: bptr(false)}
		}, true, false, "runAsNonRoot"},
		{"runAsUser 0", withContainerSC(&corev1.SecurityContext{RunAsUser: i64ptr(0)}), true, false, "runAsUser"},
		{"pod runAsUser 0", func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{RunAsUser: i64ptr(0)}
		}, true, false, "runAsUser"},
	}
	for _, tc := range cases {
		for _, level := range []struct {
			name string
			ok   bool
		}{{"baseline", tc.baseline}, {"restricted", tc.restricted}, {"privileged", true}} {
			t.Run(tc.name+"/"+level.name, func(t *testing.T) {
				_, err := runSecurityContextTrait(authoredDeployment(tc.mut), map[string]any{"psaLevel": level.name})
				switch {
				case level.ok && err != nil:
					t.Fatalf("expected the build to pass at %s, got: %v", level.name, err)
				case !level.ok && err == nil:
					t.Fatalf("expected a refusal at %s, got none", level.name)
				case !level.ok && !strings.Contains(err.Error(), tc.wantMessage):
					t.Errorf("error %q does not name %q", err, tc.wantMessage)
				}
			})
		}
	}
}

// A pod with hostUsers: false runs in a user namespace, and Pod Security
// Admission (policy 1.35 and later) relaxes three controls for it: the baseline
// procMount check, and the restricted runAsUser and runAsNonRoot checks. The
// restricted procMount check overrides the baseline one and is not relaxed, and
// no other control changes. A pod with hostUsers: true or unset gets none of it.
// The restricted trait fills runAsNonRoot: true where the workload sets none, so
// the relaxed cases author runAsNonRoot: false themselves.
func TestSecurityContextMerge_HostUsersRelaxations(t *testing.T) {
	withHostUsers := func(v *bool, mut func(*corev1.PodSpec)) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			p.HostUsers = v
			mut(p)
		}
	}
	containerSC := func(sc *corev1.SecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = sc }
	}
	podSC := func(sc *corev1.PodSecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) { p.SecurityContext = sc }
	}
	both := func(pod *corev1.PodSecurityContext, ct *corev1.SecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			p.SecurityContext = pod
			p.Containers[0].SecurityContext = ct
		}
	}
	userNS, hostNS := bptr(false), bptr(true)
	unmasked := corev1.UnmaskedProcMount
	cases := []struct {
		name        string
		mut         func(*corev1.PodSpec)
		baseline    bool // accepted at baseline
		restricted  bool // accepted at restricted
		wantMessage string
	}{
		{"hostUsers false, procMount Unmasked", withHostUsers(userNS, containerSC(&corev1.SecurityContext{ProcMount: &unmasked})), true, false, "procMount"},
		{"hostUsers true, procMount Unmasked", withHostUsers(hostNS, containerSC(&corev1.SecurityContext{ProcMount: &unmasked})), false, false, "procMount"},
		{"hostUsers false, init container procMount Unmasked", withHostUsers(userNS, func(p *corev1.PodSpec) {
			p.InitContainers = []corev1.Container{{Name: "init", Image: "b", SecurityContext: &corev1.SecurityContext{ProcMount: &unmasked}}}
		}), true, false, `init container "init"`},
		{"hostUsers false, container runAsUser 0 and runAsNonRoot false", withHostUsers(userNS, containerSC(&corev1.SecurityContext{RunAsUser: i64ptr(0), RunAsNonRoot: bptr(false)})), true, true, ""},
		{"hostUsers true, container runAsUser 0 and runAsNonRoot false", withHostUsers(hostNS, containerSC(&corev1.SecurityContext{RunAsUser: i64ptr(0), RunAsNonRoot: bptr(false)})), true, false, "runAsNonRoot"},
		{"hostUsers false, pod runAsUser 0, container runAsNonRoot false", withHostUsers(userNS, both(
			&corev1.PodSecurityContext{RunAsUser: i64ptr(0)}, &corev1.SecurityContext{RunAsNonRoot: bptr(false)})), true, true, ""},
		{"hostUsers true, pod runAsUser 0, container runAsNonRoot false", withHostUsers(hostNS, both(
			&corev1.PodSecurityContext{RunAsUser: i64ptr(0)}, &corev1.SecurityContext{RunAsNonRoot: bptr(false)})), true, false, "runAsUser"},
		{"hostUsers false, container runAsNonRoot false", withHostUsers(userNS, containerSC(&corev1.SecurityContext{RunAsNonRoot: bptr(false)})), true, true, ""},
		{"hostUsers false, pod runAsNonRoot false", withHostUsers(userNS, podSC(&corev1.PodSecurityContext{RunAsNonRoot: bptr(false)})), true, true, ""},
		{"hostUsers true, pod runAsNonRoot false", withHostUsers(hostNS, podSC(&corev1.PodSecurityContext{RunAsNonRoot: bptr(false)})), true, false, "runAsNonRoot"},
		// Nothing else is relaxed.
		{"hostUsers false, privileged", withHostUsers(userNS, containerSC(&corev1.SecurityContext{Privileged: bptr(true), AllowPrivilegeEscalation: bptr(true)})), false, false, "securityContext.privileged: true"},
		{"hostUsers false, SYS_ADMIN", withHostUsers(userNS, containerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}})), false, false, "SYS_ADMIN"},
		{"hostUsers false, CHOWN", withHostUsers(userNS, containerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CHOWN"}}})), true, false, "CHOWN"},
		{"hostUsers false, seccomp Unconfined", withHostUsers(userNS, containerSC(&corev1.SecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}})), false, false, "Unconfined"},
		{"hostUsers false, appArmor Unconfined", withHostUsers(userNS, containerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}})), false, false, "appArmorProfile"},
		{"hostUsers false, hostProcess", withHostUsers(userNS, containerSC(&corev1.SecurityContext{WindowsOptions: &corev1.WindowsSecurityContextOptions{HostProcess: bptr(true)}})), false, false, "hostProcess"},
		{"hostUsers false, allowPrivilegeEscalation true", withHostUsers(userNS, containerSC(&corev1.SecurityContext{AllowPrivilegeEscalation: bptr(true)})), true, false, "allowPrivilegeEscalation"},
	}
	for _, tc := range cases {
		for _, level := range []struct {
			name string
			ok   bool
		}{{"baseline", tc.baseline}, {"restricted", tc.restricted}, {"privileged", true}} {
			t.Run(tc.name+"/"+level.name, func(t *testing.T) {
				_, err := runSecurityContextTrait(authoredDeployment(tc.mut), map[string]any{"psaLevel": level.name})
				switch {
				case level.ok && err != nil:
					t.Fatalf("expected the build to pass at %s, got: %v", level.name, err)
				case !level.ok && err == nil:
					t.Fatalf("expected a refusal at %s, got none", level.name)
				case !level.ok && !strings.Contains(err.Error(), tc.wantMessage):
					t.Errorf("error %q does not name %q", err, tc.wantMessage)
				}
			})
		}
	}
}

// The kubelet refuses UID 0 under runAsNonRoot: true in a user namespace too, so
// the trait's contradiction check holds at every level whatever hostUsers says.
func TestSecurityContextMerge_HostUsersKeepsTheUIDContradiction(t *testing.T) {
	dep := func() *appsv1.Deployment {
		return authoredDeployment(func(p *corev1.PodSpec) {
			p.HostUsers = bptr(false)
			p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: i64ptr(0), RunAsNonRoot: bptr(true)}
		})
	}
	for _, level := range []string{"baseline", "restricted", "privileged"} {
		t.Run(level, func(t *testing.T) {
			mustRefuse(t, dep(), map[string]any{"psaLevel": level}, "UID 0")
		})
	}
}

// A Windows pod is exempt from exactly three restricted controls (seccomp
// required, capabilities including the add list, allowPrivilegeEscalation), as
// in Pod Security Admission. Every other control applies to it.
func TestSecurityContextMerge_WindowsPodControls(t *testing.T) {
	windows := func(mut func(*corev1.PodSpec)) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			p.OS = &corev1.PodOS{Name: corev1.Windows}
			mut(p)
		}
	}
	containerSC := func(sc *corev1.SecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = sc }
	}
	podSC := func(sc *corev1.PodSecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) { p.SecurityContext = sc }
	}
	unmasked := corev1.UnmaskedProcMount
	localhost := "profiles/app"
	cases := []struct {
		name        string
		mut         func(*corev1.PodSpec)
		baseline    bool // accepted at baseline
		restricted  bool // accepted at restricted
		wantMessage string
	}{
		{"container appArmor Unconfined", windows(containerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}})), false, false, "appArmorProfile"},
		{"pod appArmor Unconfined", windows(podSC(&corev1.PodSecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}})), false, false, "appArmorProfile"},
		{"container appArmor Localhost", windows(containerSC(&corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeLocalhost, LocalhostProfile: &localhost}})), true, true, ""},
		{"container SELinux spc_t", windows(containerSC(&corev1.SecurityContext{SELinuxOptions: &corev1.SELinuxOptions{Type: "spc_t"}})), false, false, "seLinuxOptions"},
		{"pod unsafe sysctl", windows(podSC(&corev1.PodSecurityContext{Sysctls: []corev1.Sysctl{{Name: "kernel.msgmax", Value: "65536"}}})), false, false, "sysctls"},
		{"container procMount Unmasked", windows(containerSC(&corev1.SecurityContext{ProcMount: &unmasked})), false, false, "procMount"},
		// The restricted seccomp control replaces the baseline one and exempts a
		// Windows pod whole, so only baseline refuses Unconfined there.
		{"container seccomp Unconfined", windows(containerSC(&corev1.SecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}})), false, true, "seccompProfile"},
		{"pod seccomp Unconfined", windows(podSC(&corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}})), false, true, "seccompProfile"},
		{"container privileged", windows(containerSC(&corev1.SecurityContext{Privileged: bptr(true)})), false, false, "privileged"},
		// The capabilities add list is a restricted control, so restricted skips it
		// on Windows and baseline keeps it.
		{"container capabilities.add SYS_ADMIN", windows(containerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}})), false, true, "SYS_ADMIN"},
		{"container capabilities.add CHOWN", windows(containerSC(&corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CHOWN"}}})), true, true, ""},
		{"container allowPrivilegeEscalation true", windows(containerSC(&corev1.SecurityContext{AllowPrivilegeEscalation: bptr(true)})), true, true, ""},
		// hostUsers false relaxes a Windows pod on its own terms.
		{"hostUsers false, procMount Unmasked", windows(func(p *corev1.PodSpec) {
			p.HostUsers = bptr(false)
			p.Containers[0].SecurityContext = &corev1.SecurityContext{ProcMount: &unmasked}
		}), true, false, "procMount"},
		{"hostUsers false, appArmor Unconfined", windows(func(p *corev1.PodSpec) {
			p.HostUsers = bptr(false)
			p.Containers[0].SecurityContext = &corev1.SecurityContext{AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined}}
		}), false, false, "appArmorProfile"},
	}
	for _, tc := range cases {
		for _, level := range []struct {
			name string
			ok   bool
		}{{"baseline", tc.baseline}, {"restricted", tc.restricted}, {"privileged", true}} {
			t.Run(tc.name+"/"+level.name, func(t *testing.T) {
				_, err := runSecurityContextTrait(authoredDeployment(tc.mut), map[string]any{"psaLevel": level.name})
				switch {
				case level.ok && err != nil:
					t.Fatalf("expected the build to pass at %s, got: %v", level.name, err)
				case !level.ok && err == nil:
					t.Fatalf("expected a refusal at %s, got none", level.name)
				case !level.ok && !strings.Contains(err.Error(), tc.wantMessage):
					t.Errorf("error %q does not name %q", err, tc.wantMessage)
				}
			})
		}
	}
}

// The runAsNonRoot: false trait property is refused at restricted unless the pod has
// hostUsers: false, where admission allows it (go-kure/launcher#1201). The pod
// spec is known only when the trait generates, so the refusal names the workload.
func TestSecurityContextMerge_RunAsNonRootPropertyNeedsUserNamespace(t *testing.T) {
	withPod := func(hostUsers *bool, windows bool, ct *corev1.SecurityContext) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			p.HostUsers = hostUsers
			if windows {
				p.OS = &corev1.PodOS{Name: corev1.Windows}
			}
			p.Containers[0].SecurityContext = ct
		}
	}
	userNS, hostNS := bptr(false), bptr(true)
	cases := []struct {
		name  string
		level string
		mut   func(*corev1.PodSpec)
		ok    bool
	}{
		{"restricted, hostUsers false", "restricted", withPod(userNS, false, nil), true},
		{"restricted, hostUsers false, container runAsUser 0", "restricted", withPod(userNS, false, &corev1.SecurityContext{RunAsUser: i64ptr(0)}), true},
		{"restricted, hostUsers false, Windows", "restricted", withPod(userNS, true, nil), true},
		{"restricted, hostUsers true", "restricted", withPod(hostNS, false, nil), false},
		{"restricted, hostUsers unset", "restricted", withPod(nil, false, nil), false},
		{"restricted, hostUsers unset, Windows", "restricted", withPod(nil, true, nil), false},
		{"baseline, hostUsers unset", "baseline", withPod(nil, false, nil), true},
		{"privileged, hostUsers unset", "privileged", withPod(nil, false, nil), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"psaLevel": tc.level, "runAsNonRoot": false}
			if !tc.ok {
				mustRefuse(t, authoredDeployment(tc.mut), props,
					"runAsNonRoot: false is incompatible with psaLevel restricted", `"web"`, "hostUsers: false")
				return
			}
			spec := mustApply(t, authoredDeployment(tc.mut), props)
			if sc := spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || *sc.RunAsNonRoot {
				t.Errorf("pod runAsNonRoot = %+v, want false", sc)
			}
			if sc := spec.Containers[0].SecurityContext; sc == nil || sc.RunAsNonRoot == nil || *sc.RunAsNonRoot {
				t.Errorf("container runAsNonRoot = %+v, want false", sc)
			}
		})
	}
}

// The runAsNonRoot: false refusal needs the pod spec, so it comes from the build, not
// from applying the trait; allowPrivilegeEscalation has no such relaxation and
// is still refused when the trait is applied.
func TestSecurityContextHandler_RunAsNonRootRefusalComesFromGenerate(t *testing.T) {
	apply := func(props map[string]any) (*stack.Application, error) {
		app := stack.NewApplication("web", "ns", &objStub{obj: authoredDeployment(nil)})
		return app, (&traits.SecurityContextHandler{}).Apply(&oam.Trait{Type: "security-context", Properties: props}, app, newBundle())
	}
	app, err := apply(map[string]any{"psaLevel": "restricted", "runAsNonRoot": false})
	if err != nil {
		t.Fatalf("Apply refused runAsNonRoot: false before the pod spec was known: %v", err)
	}
	if _, err := app.Generate(); err == nil || !strings.Contains(err.Error(), "incompatible with psaLevel restricted") {
		t.Fatalf("Generate error = %v, want the restricted incompatibility", err)
	}
	if _, err := apply(map[string]any{"psaLevel": "restricted", "allowPrivilegeEscalation": true}); err == nil || !strings.Contains(err.Error(), "incompatible with psaLevel restricted") {
		t.Fatalf("Apply error = %v, want the restricted incompatibility for allowPrivilegeEscalation", err)
	}
}

// The refusal names the level and the component, kind, name and container so a
// chart or manifests user can tell what to change.
func TestSecurityContextMerge_LevelRefusalNamesItsSource(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: bptr(true)}
	})
	mustRefuse(t, dep, map[string]any{"psaLevel": "baseline"},
		"security-context", `"web"`, "Deployment", `container "app"`, "privileged", "baseline")
}

// The missing drop list is filled in and ALL is never refused as absent.
func TestSecurityContextMerge_RestrictedWithAuthoredSecurityContextStillPasses(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: i64ptr(1000)}
	})
	sc := mustApply(t, dep, map[string]any{"psaLevel": "restricted"}).Containers[0].SecurityContext
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Errorf("authored runAsUser lost: %+v", sc)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("restricted default runAsNonRoot missing")
	}
}

// A Windows pod keeps its authored windowsOptions and gets only runAsNonRoot.
func TestSecurityContextMerge_WindowsKeepsWindowsOptions(t *testing.T) {
	dep := authoredDeployment(func(p *corev1.PodSpec) {
		p.OS = &corev1.PodOS{Name: corev1.Windows}
		p.Containers[0].SecurityContext = &corev1.SecurityContext{
			WindowsOptions: &corev1.WindowsSecurityContextOptions{RunAsUserName: new("ContainerUser")},
		}
	})
	spec := mustApply(t, dep, map[string]any{"psaLevel": "restricted"})
	sc := spec.Containers[0].SecurityContext
	if sc.WindowsOptions == nil || sc.WindowsOptions.RunAsUserName == nil || *sc.WindowsOptions.RunAsUserName != "ContainerUser" {
		t.Errorf("windowsOptions lost: %+v", sc)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("windows runAsNonRoot missing")
	}
	if sc.SeccompProfile != nil || sc.Capabilities != nil || sc.AllowPrivilegeEscalation != nil {
		t.Errorf("Linux-only fields written on a Windows container: %+v", sc)
	}
}
