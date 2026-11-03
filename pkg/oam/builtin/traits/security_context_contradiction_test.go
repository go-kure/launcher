package traits_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// The merged context is checked for combinations Kubernetes refuses or the
// kubelet cannot start, at every psaLevel (go-kure/launcher#1171). The
// component parser checks the authored context before the trait runs, so it
// cannot see a contradiction the trait introduces by adding
// allowPrivilegeEscalation or runAsNonRoot.
func TestSecurityContextMerge_RefusesContradictions(t *testing.T) {
	root := int64(0)
	cases := []struct {
		name  string
		mut   func(*corev1.PodSpec)
		props map[string]any
		want  []string // empty: the pod is admitted
	}{
		{
			name: "trait allowPrivilegeEscalation false with an authored privileged container, baseline",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: bptr(true)}
			},
			props: map[string]any{"psaLevel": "baseline", "allowPrivilegeEscalation": false},
			want:  []string{"the trait sets allowPrivilegeEscalation: false", "privileged: true"},
		},
		{
			name: "trait allowPrivilegeEscalation false with an authored privileged container, privileged level",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: bptr(true)}
			},
			props: map[string]any{"psaLevel": "privileged", "allowPrivilegeEscalation": false},
			want:  []string{"the trait sets allowPrivilegeEscalation: false", "privileged: true"},
		},
		{
			name: "the restricted default allowPrivilegeEscalation false with an authored privileged container",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: bptr(true)}
			},
			props: map[string]any{"psaLevel": "restricted"},
			want:  []string{"psaLevel restricted sets allowPrivilegeEscalation: false", "privileged: true"},
		},
		{
			name: "the same contradiction on an init container",
			mut: func(p *corev1.PodSpec) {
				p.InitContainers = []corev1.Container{{Name: "init", Image: "example:latest", SecurityContext: &corev1.SecurityContext{Privileged: bptr(true)}}}
			},
			props: map[string]any{"psaLevel": "baseline", "allowPrivilegeEscalation": false},
			want:  []string{`init container "init"`, "privileged: true"},
		},
		{
			name: "trait allowPrivilegeEscalation false with an added CAP_SYS_ADMIN",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CAP_SYS_ADMIN"}}}
			},
			props: map[string]any{"psaLevel": "privileged", "allowPrivilegeEscalation": false},
			want:  []string{"the trait sets allowPrivilegeEscalation: false", "CAP_SYS_ADMIN"},
		},
		{
			name:  "trait allowPrivilegeEscalation false with a plain container is admitted",
			mut:   nil,
			props: map[string]any{"psaLevel": "baseline", "allowPrivilegeEscalation": false},
		},
		{
			name: "trait allowPrivilegeEscalation false with SYS_ADMIN (not the CAP_ spelling) is not this check",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_ADMIN"}}}
			},
			props: map[string]any{"psaLevel": "privileged", "allowPrivilegeEscalation": false},
		},
		{
			name: "an authored privileged container with no trait allowPrivilegeEscalation is admitted at baseline",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: bptr(true)}
			},
			props: map[string]any{"psaLevel": "privileged"},
		},
		{
			name:  "trait runAsNonRoot true with an authored container runAsUser 0, baseline",
			mut:   func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: &root} },
			props: map[string]any{"psaLevel": "baseline", "runAsNonRoot": true},
			want:  []string{"the trait sets runAsNonRoot: true", "the workload sets runAsUser: 0 on the container"},
		},
		{
			name:  "trait runAsNonRoot true with an authored container runAsUser 0, privileged level",
			mut:   func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: &root} },
			props: map[string]any{"psaLevel": "privileged", "runAsNonRoot": true},
			want:  []string{"the trait sets runAsNonRoot: true", "runAsUser: 0"},
		},
		{
			name:  "trait runAsNonRoot true with an authored pod runAsUser 0 inherited by the container",
			mut:   func(p *corev1.PodSpec) { p.SecurityContext = &corev1.PodSecurityContext{RunAsUser: &root} },
			props: map[string]any{"psaLevel": "baseline", "runAsNonRoot": true},
			want:  []string{"the trait sets runAsNonRoot: true", "the workload sets runAsUser: 0 on the pod"},
		},
		{
			// The sources are the other way round: the workload's pod asks for
			// non-root and the trait supplies UID 0. Baseline supplies neither.
			name:  "authored pod runAsNonRoot true with a trait runAsUser 0",
			mut:   func(p *corev1.PodSpec) { p.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: bptr(true)} },
			props: map[string]any{"psaLevel": "baseline", "runAsUser": 0},
			want:  []string{"the workload sets runAsNonRoot: true on the pod", "the trait sets runAsUser: 0"},
		},
		{
			name: "trait runAsNonRoot true with an init container runAsUser 0",
			mut: func(p *corev1.PodSpec) {
				p.InitContainers = []corev1.Container{{Name: "init", Image: "example:latest", SecurityContext: &corev1.SecurityContext{RunAsUser: &root}}}
			},
			props: map[string]any{"psaLevel": "baseline", "runAsNonRoot": true},
			want:  []string{`init container "init"`, "runAsUser: 0"},
		},
		{
			name:  "the restricted default runAsNonRoot true with an authored container runAsUser 0",
			mut:   func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: &root} },
			props: map[string]any{"psaLevel": "restricted"},
			want:  []string{"psaLevel restricted sets runAsNonRoot: true", "runAsUser: 0"},
		},
		{
			name:  "trait runAsNonRoot false with an authored container runAsUser 0 is admitted",
			mut:   func(p *corev1.PodSpec) { p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: &root} },
			props: map[string]any{"psaLevel": "baseline", "runAsNonRoot": false},
		},
		{
			name: "trait runAsNonRoot true with a non-zero authored runAsUser is admitted",
			mut: func(p *corev1.PodSpec) {
				p.Containers[0].SecurityContext = &corev1.SecurityContext{RunAsUser: i64ptr(1000)}
			},
			props: map[string]any{"psaLevel": "baseline", "runAsNonRoot": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dep := authoredDeployment(tc.mut)
			if len(tc.want) > 0 {
				mustRefuse(t, dep, tc.props, tc.want...)
				return
			}
			mustApply(t, dep, tc.props)
		})
	}
}

// A sysctl name is checked as authored, as Pod Security Admission does
// (k8s.io/pod-security-admission policy/check_sysctls.go): the component parser
// accepts "/" as a separator, but admission does not read it as ".", so only
// the dotted spelling of a safe sysctl is allowed at baseline and restricted.
func TestSecurityContextMerge_SysctlSeparators(t *testing.T) {
	sysctlPod := func(name string) func(*corev1.PodSpec) {
		return func(p *corev1.PodSpec) {
			p.SecurityContext = &corev1.PodSecurityContext{Sysctls: []corev1.Sysctl{{Name: name, Value: "1"}}}
		}
	}
	for _, level := range []string{"baseline", "restricted"} {
		t.Run(level+" allows the dotted spelling of a safe sysctl", func(t *testing.T) {
			name := "net.ipv4.tcp_syncookies"
			spec := mustApply(t, authoredDeployment(sysctlPod(name)), map[string]any{"psaLevel": level})
			if got := spec.SecurityContext.Sysctls[0].Name; got != name {
				t.Errorf("sysctl name = %q, want the authored %q", got, name)
			}
		})
		t.Run(level+" refuses the slash and mixed spellings of a safe sysctl", func(t *testing.T) {
			for _, name := range []string{"net/ipv4/tcp_syncookies", "net.ipv4/tcp_syncookies"} {
				mustRefuse(t, authoredDeployment(sysctlPod(name)), map[string]any{"psaLevel": level}, "sysctls: "+name)
			}
		})
		t.Run(level+" refuses an unsafe sysctl in both spellings", func(t *testing.T) {
			for _, name := range []string{"kernel.msgmax", "kernel/msgmax"} {
				mustRefuse(t, authoredDeployment(sysctlPod(name)), map[string]any{"psaLevel": level}, "sysctls: "+name)
			}
		})
	}
	t.Run("privileged admits the slash spelling as authored", func(t *testing.T) {
		name := "net/ipv4/tcp_syncookies"
		spec := mustApply(t, authoredDeployment(sysctlPod(name)), map[string]any{"psaLevel": "privileged"})
		if got := spec.SecurityContext.Sysctls[0].Name; got != name {
			t.Errorf("sysctl name = %q, want the authored %q", got, name)
		}
	})
}
