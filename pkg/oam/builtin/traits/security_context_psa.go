package traits

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/kure/pkg/kubernetes"

	"github.com/go-kure/launcher/pkg/errors"
)

// The securityContext rules of the Pod Security Standards
// (https://kubernetes.io/docs/concepts/security/pod-security-standards/, the
// Baseline and Restricted control tables of the current Kubernetes
// documentation, v1.37, read 2026-11-03; the lists below are copied from it),
// checked on the pod spec the security-context trait has merged
// (go-kure/launcher#1171). Merging keeps the contexts a workload authored, so
// without this check a privileged container the trait used to overwrite would
// now reach the output, and the namespace's admission would refuse the pod at
// deploy time instead of the build refusing it.
//
// Only securityContext fields are covered, at pod level and on every container
// and init container. Host namespaces, hostPath, host ports and volume types are
// not (they are not securityContext fields). Ephemeral containers are not
// written by the trait and not checked. kure's ValidatePodSpecPSA is not used:
// it checks host namespaces and a narrower field set, and its restricted rules
// ask for more than the standard does (a pod-level runAsNonRoot).
//
// The policy modelled is the latest one, as pod-security-admission v0.37.2 runs
// it with the 1.35 relaxations for a pod with hostUsers: false (a user
// namespace): baseline procMount, restricted runAsUser 0 and restricted
// runAsNonRoot are skipped for such a pod. Restricted procMount is not: admission
// re-applies the unconditional rule there. A namespace pinned to a policy version
// below 1.35 still refuses these pods at deploy time.

// baselineCapabilities are the capabilities the baseline profile lets a
// container add.
var baselineCapabilities = map[corev1.Capability]bool{
	"AUDIT_WRITE": true, "CHOWN": true, "DAC_OVERRIDE": true, "FOWNER": true,
	"FSETID": true, "KILL": true, "MKNOD": true, "NET_BIND_SERVICE": true,
	"SETFCAP": true, "SETGID": true, "SETPCAP": true, "SETUID": true,
	"SYS_CHROOT": true,
}

// baselineSELinuxTypes are the SELinux types baseline allows; an empty type is
// unset.
var baselineSELinuxTypes = map[string]bool{
	"": true, "container_t": true, "container_init_t": true,
	"container_kvm_t": true, "container_engine_t": true,
}

// baselineSysctls are the sysctls baseline allows a pod to set.
var baselineSysctls = map[string]bool{
	"kernel.shm_rmid_forced":              true,
	"net.ipv4.ip_local_port_range":        true,
	"net.ipv4.ip_unprivileged_port_start": true,
	"net.ipv4.tcp_syncookies":             true,
	"net.ipv4.ping_group_range":           true,
	"net.ipv4.ip_local_reserved_ports":    true,
	"net.ipv4.tcp_keepalive_time":         true,
	"net.ipv4.tcp_fin_timeout":            true,
	"net.ipv4.tcp_keepalive_intvl":        true,
	"net.ipv4.tcp_keepalive_probes":       true,
	"net.ipv4.tcp_rmem":                   true,
	"net.ipv4.tcp_wmem":                   true,
	"net.ipv4.tcp_slow_start_after_idle":  true,
	"net.ipv4.tcp_notsent_lowat":          true,
}

// checkPSALevel returns an error for the first securityContext field of
// podSpec the declared level does not allow. The privileged level allows
// everything. windows skips the three restricted controls Pod Security
// Admission exempts a Windows pod from (seccompProfile required, capabilities
// including the add list, allowPrivilegeEscalation); every other control applies
// to it. A pod with hostUsers: false gets the user-namespace relaxations in the
// header comment. where names the workload.
func checkPSALevel(level kubernetes.PSALevel, podSpec *corev1.PodSpec, windows bool, where string) error {
	if level != kubernetes.PSABaseline && level != kubernetes.PSARestricted {
		return nil
	}
	c := psaCheck{
		level:      level,
		restricted: level == kubernetes.PSARestricted,
		windows:    windows,
		userNS:     podSpec.HostUsers != nil && !*podSpec.HostUsers,
	}
	pod := podSpec.SecurityContext
	if pod == nil {
		pod = &corev1.PodSecurityContext{}
	}
	if err := c.pod(pod, where+", pod"); err != nil {
		return err
	}
	for i := range podSpec.Containers {
		ct := &podSpec.Containers[i]
		if err := c.container(ct.SecurityContext, pod, fmt.Sprintf("%s, container %q", where, ct.Name)); err != nil {
			return err
		}
	}
	for i := range podSpec.InitContainers {
		ct := &podSpec.InitContainers[i]
		if err := c.container(ct.SecurityContext, pod, fmt.Sprintf("%s, init container %q", where, ct.Name)); err != nil {
			return err
		}
	}
	return nil
}

type psaCheck struct {
	level      kubernetes.PSALevel
	restricted bool
	windows    bool
	userNS     bool // hostUsers: false, a pod in a user namespace
}

func (c psaCheck) notAllowed(at, what string) error {
	return errors.Errorf("security-context: %s: securityContext.%s is not allowed at psaLevel %s (Pod Security Standards)", at, what, c.level)
}

func (c psaCheck) required(at, what string) error {
	return errors.Errorf("security-context: %s: securityContext.%s is required at psaLevel %s (Pod Security Standards)", at, what, c.level)
}

// pod checks the pod-level securityContext.
func (c psaCheck) pod(sc *corev1.PodSecurityContext, at string) error {
	if w := sc.WindowsOptions; w != nil && w.HostProcess != nil && *w.HostProcess {
		return c.notAllowed(at, "windowsOptions.hostProcess: true")
	}
	if c.restricted && !c.userNS && sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
		return c.notAllowed(at, "runAsNonRoot: false")
	}
	if err := c.common(sc.SELinuxOptions, sc.SeccompProfile, sc.AppArmorProfile, at); err != nil {
		return err
	}
	for _, s := range sc.Sysctls {
		// The name is looked up as authored, as Pod Security Admission does
		// (k8s.io/pod-security-admission policy/check_sysctls.go): the
		// component parser accepts "/" as a separator, but admission does not
		// read it as ".", so a slash spelling is refused here too.
		if !baselineSysctls[s.Name] {
			return c.notAllowed(at, fmt.Sprintf("sysctls: %s", s.Name))
		}
	}
	if c.restricted && !c.userNS && sc.RunAsUser != nil && *sc.RunAsUser == 0 {
		return c.notAllowed(at, "runAsUser: 0")
	}
	return nil
}

// common checks the rules pod level and container level share.
func (c psaCheck) common(se *corev1.SELinuxOptions, sec *corev1.SeccompProfile, aa *corev1.AppArmorProfile, at string) error {
	if se != nil {
		if !baselineSELinuxTypes[se.Type] {
			return c.notAllowed(at, fmt.Sprintf("seLinuxOptions.type: %s", se.Type))
		}
		if se.User != "" || se.Role != "" {
			return c.notAllowed(at, "seLinuxOptions.user and seLinuxOptions.role")
		}
	}
	// Both controls allow an unset profile, RuntimeDefault and Localhost, and
	// nothing else: a value outside that set (Unconfined, or a string a manifest
	// or chart carries that the API would not accept) is refused. The restricted
	// seccomp control replaces the baseline one and exempts a Windows pod
	// entirely, so a Windows pod at restricted is not checked here.
	if sec != nil && !(c.restricted && c.windows) && sec.Type != corev1.SeccompProfileTypeRuntimeDefault && sec.Type != corev1.SeccompProfileTypeLocalhost {
		return c.notAllowed(at, fmt.Sprintf("seccompProfile.type: %s", sec.Type))
	}
	if aa != nil && aa.Type != corev1.AppArmorProfileTypeRuntimeDefault && aa.Type != corev1.AppArmorProfileTypeLocalhost {
		return c.notAllowed(at, fmt.Sprintf("appArmorProfile.type: %s", aa.Type))
	}
	return nil
}

// container checks one container's securityContext. pod is the pod-level
// context the container inherits unset fields from.
func (c psaCheck) container(sc *corev1.SecurityContext, pod *corev1.PodSecurityContext, at string) error {
	if sc == nil {
		sc = &corev1.SecurityContext{}
	}
	if sc.Privileged != nil && *sc.Privileged {
		return c.notAllowed(at, "privileged: true")
	}
	if w := sc.WindowsOptions; w != nil && w.HostProcess != nil && *w.HostProcess {
		return c.notAllowed(at, "windowsOptions.hostProcess: true")
	}
	if c.restricted && !c.userNS {
		nonRoot := pod.RunAsNonRoot
		if sc.RunAsNonRoot != nil {
			nonRoot = sc.RunAsNonRoot
		}
		if nonRoot == nil || !*nonRoot {
			return c.required(at, "runAsNonRoot: true (on the container or the pod)")
		}
	}
	if err := c.common(sc.SELinuxOptions, sc.SeccompProfile, sc.AppArmorProfile, at); err != nil {
		return err
	}
	// Restricted keeps the rule in a user namespace; only baseline relaxes it.
	if sc.ProcMount != nil && *sc.ProcMount != corev1.DefaultProcMount && (c.restricted || !c.userNS) {
		return c.notAllowed(at, fmt.Sprintf("procMount: %s", *sc.ProcMount))
	}
	// The restricted capabilities control replaces the baseline one and exempts a
	// Windows pod, so a Windows pod at restricted has no add-list check.
	if sc.Capabilities != nil && !(c.restricted && c.windows) {
		for _, cp := range sc.Capabilities.Add {
			if !c.capabilityAllowed(cp) {
				return c.notAllowed(at, fmt.Sprintf("capabilities.add: %s", cp))
			}
		}
	}
	if !c.restricted {
		return nil
	}
	if !c.userNS && sc.RunAsUser != nil && *sc.RunAsUser == 0 {
		return c.notAllowed(at, "runAsUser: 0")
	}
	// The three remaining restricted controls exempt a Windows pod.
	if c.windows {
		return nil
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		return c.required(at, "allowPrivilegeEscalation: false")
	}
	if sc.Capabilities == nil || !dropsAll(sc.Capabilities) {
		return c.required(at, "capabilities.drop: [ALL]")
	}
	seccomp := pod.SeccompProfile
	if sc.SeccompProfile != nil {
		seccomp = sc.SeccompProfile
	}
	if seccomp == nil || (seccomp.Type != corev1.SeccompProfileTypeRuntimeDefault && seccomp.Type != corev1.SeccompProfileTypeLocalhost) {
		return c.required(at, "seccompProfile.type: RuntimeDefault or Localhost (on the container or the pod)")
	}
	return nil
}

// capabilityAllowed reports whether a container may add cp: restricted allows
// only NET_BIND_SERVICE, baseline the list above.
func (c psaCheck) capabilityAllowed(cp corev1.Capability) bool {
	if c.restricted {
		return cp == "NET_BIND_SERVICE"
	}
	return baselineCapabilities[cp]
}
