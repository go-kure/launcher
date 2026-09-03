package components

import (
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// This file is the shared pod/container builder every container-workload kind
// (webservice, worker, statefulset, daemonset, cronjob) assembles its pod
// template through (go-kure/launcher#342). It writes `corev1.PodSpec` and
// `corev1.Container` fields directly — the upstream struct is the construction
// API — and deliberately calls none of kure's per-kind pod-template
// passthroughs (`Add<Kind>Container`, `Set<Kind>ServiceAccountName`, …), so the
// workload family is already ahead of the kure builder-contract bump tracked in
// go-kure/launcher#361.

// PodSpecConfig holds the authored pod-level fields shared by every workload
// kind. It embeds the real corev1.PodSpec (same structural pattern as
// ResourceRequirements embedding corev1.ResourceRequirements) so there is no
// parallel field list to drift: parsePodSpec populates only the pod-level
// scalar/nested fields it accepts, and buildPodSpec later fills the
// container/volume/scheduling fields the kind handler supplies.
//
// ServiceAccountName, when authored, names an existing ServiceAccount the pod
// runs as; the kind handler then does not generate its per-component
// ServiceAccount (see each handler's Generate). Empty means "use the
// per-component ServiceAccount named after the component", as before.
type PodSpecConfig struct {
	corev1.PodSpec
}

// podSpecPropertyKeys lists every top-level pod-level property parsePodSpec
// reads, in the order corev1.PodSpec declares them. schemaPodSpec publishes
// exactly this set (TestPodSpecSchemaMatchesParser pins the two together),
// minus podSpecJobOnlyKeys for kinds whose pods are not Job pods.
//
// Three keys carry a `pod` prefix because the bare corev1 field name is
// already a property at some kind: `podSecurityContext` and `podResources`
// (container-level securityContext/resources exist on every kind) and
// `podActiveDeadlineSeconds` (cronjob's job-level activeDeadlineSeconds).
var podSpecPropertyKeys = []string{
	"terminationGracePeriodSeconds", "podActiveDeadlineSeconds", "dnsPolicy", "nodeSelector",
	"serviceAccountName", "automountServiceAccountToken", "nodeName", "hostNetwork", "hostPID",
	"hostIPC", "shareProcessNamespace", "podSecurityContext", "imagePullSecrets", "hostname",
	"subdomain", "schedulerName", "hostAliases", "priorityClassName", "dnsConfig", "readinessGates",
	"runtimeClassName", "enableServiceLinks", "preemptionPolicy", "setHostnameAsFQDN", "os",
	"hostUsers", "schedulingGates", "resourceClaims", "podResources", "hostnameOverride",
	"schedulingGroup",
}

// podSpecJobOnlyKeys are the pod-level properties only Job pods may carry:
// apps/v1 validation forbids activeDeadlineSeconds on Deployment, StatefulSet
// and DaemonSet pod templates ("activeDeadlineSeconds in <Kind> is not
// Supported"), so only the cronjob kind exposes and accepts it.
var podSpecJobOnlyKeys = []string{"podActiveDeadlineSeconds"}

// podSpecRejectedKeys are corev1.PodSpec fields an author might reasonably
// write that this schema deliberately does not accept; each is rejected with
// an error naming the reason rather than silently ignored (unknown top-level
// keys on an authored component are otherwise not checked at all today).
var podSpecRejectedKeys = map[string]string{
	// corev1.PodSpec.EphemeralContainers' own doc comment: "cannot be specified
	// when creating a pod" — it needs the pod's `ephemeralcontainers`
	// subresource after creation, a mechanism this package does not have.
	"ephemeralContainers": "ephemeralContainers: not supported — ephemeral containers cannot be declared on a pod template; they are added to a running pod through its ephemeralcontainers subresource",
	// corev1.PodSpec.Priority's doc comment: "When Priority Admission Controller
	// is enabled, it prevents users from setting this field" — the controller
	// is on by default and populates it from priorityClassName.
	"priority": "priority: not authorable — the Priority admission controller (enabled by default) rejects pods that set it and derives it from priorityClassName instead; set priorityClassName",
	// corev1.PodSpec.Overhead's doc comment: "If the RuntimeClass admission
	// controller is enabled, overhead must not be set in Pod create requests".
	"overhead": "overhead: not authorable — the RuntimeClass admission controller (enabled by default) rejects pods that set it and derives it from the RuntimeClass; set runtimeClassName",
	// corev1.PodSpec.DeprecatedServiceAccount: "Deprecated: Use serviceAccountName instead."
	"serviceAccount": "serviceAccount: deprecated alias of serviceAccountName; use serviceAccountName",
}

// parsePodSpec parses the shared pod-level properties (see podSpecPropertyKeys)
// into a PodSpecConfig. jobPods says whether the kind's pods are Job pods,
// which gates podSpecJobOnlyKeys. Every accepted field is validated the way
// real admission validates it where that check is deterministic from the
// authored document alone; cross-field admission rules that need cluster state
// (host ports under hostNetwork, feature gates, RuntimeClass existence) are
// left to the cluster.
func parsePodSpec(props map[string]any, jobPods bool) (PodSpecConfig, error) {
	var cfg PodSpecConfig
	ps := &cfg.PodSpec

	// Sorted so the reported error is stable when several rejected keys are
	// authored at once (map iteration order is randomised).
	rejected := make([]string, 0, len(podSpecRejectedKeys))
	for key := range podSpecRejectedKeys {
		rejected = append(rejected, key)
	}
	slices.Sort(rejected)
	for _, key := range rejected {
		if _, present := props[key]; present {
			return PodSpecConfig{}, errors.New(podSpecRejectedKeys[key])
		}
	}
	if !jobPods {
		for _, key := range podSpecJobOnlyKeys {
			if _, present := props[key]; present {
				return PodSpecConfig{}, errors.Errorf("%s: only Job pods may set activeDeadlineSeconds; apps/v1 validation forbids it on Deployment, StatefulSet, and DaemonSet pod templates", key)
			}
		}
	}

	if i, present, err := parseInt64Field(props, "terminationGracePeriodSeconds", "terminationGracePeriodSeconds"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if i < 0 {
			return PodSpecConfig{}, errors.Errorf("terminationGracePeriodSeconds: must not be negative, got %d", i)
		}
		ps.TerminationGracePeriodSeconds = &i
	}
	// ValidatePodSpec: activeDeadlineSeconds must be between 1 and math.MaxInt32.
	if i, present, err := parseInt64Field(props, "podActiveDeadlineSeconds", "podActiveDeadlineSeconds"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if i < 1 || i > math.MaxInt32 {
			return PodSpecConfig{}, errors.Errorf("podActiveDeadlineSeconds: must be between 1 and %d, got %d", math.MaxInt32, i)
		}
		ps.ActiveDeadlineSeconds = &i
	}
	if v, present, err := parseStringField(props, "dnsPolicy", "dnsPolicy"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		switch corev1.DNSPolicy(v) {
		case corev1.DNSClusterFirstWithHostNet, corev1.DNSClusterFirst, corev1.DNSDefault, corev1.DNSNone:
			ps.DNSPolicy = corev1.DNSPolicy(v)
		default:
			return PodSpecConfig{}, errors.Errorf("dnsPolicy: invalid value %q, must be ClusterFirstWithHostNet, ClusterFirst, Default, or None", v)
		}
	}
	if raw, present, err := parseObjectField(props, "nodeSelector", "nodeSelector"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		ns, err := parseLabelMap(raw, "nodeSelector")
		if err != nil {
			return PodSpecConfig{}, err
		}
		ps.NodeSelector = ns
	}
	if v, present, err := parseStringField(props, "serviceAccountName", "serviceAccountName"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return PodSpecConfig{}, errors.Errorf("serviceAccountName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		ps.ServiceAccountName = v
	}
	if v, err := parseBoolField(props, "automountServiceAccountToken", "automountServiceAccountToken"); err != nil {
		return PodSpecConfig{}, err
	} else if v != nil {
		ps.AutomountServiceAccountToken = v
	}
	if v, present, err := parseStringField(props, "nodeName", "nodeName"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		// ValidatePodSpec runs NameIsDNSSubdomain over spec.nodeName: a Node is
		// an ordinary Kubernetes object, so an invalid value is rejected at
		// admission rather than merely failing to match a node.
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return PodSpecConfig{}, errors.Errorf("nodeName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		ps.NodeName = v
	}
	// hostNetwork/hostPID/hostIPC are plain bools on corev1.PodSpec (false is
	// the API default and omitted from output); the others are *bool so that
	// "not authored" stays distinct from an explicit false.
	for _, f := range []struct {
		key string
		dst *bool
	}{
		{"hostNetwork", &ps.HostNetwork},
		{"hostPID", &ps.HostPID},
		{"hostIPC", &ps.HostIPC},
	} {
		v, err := parseBoolField(props, f.key, f.key)
		if err != nil {
			return PodSpecConfig{}, err
		}
		if v != nil {
			*f.dst = *v
		}
	}
	for _, f := range []struct {
		key string
		dst **bool
	}{
		{"shareProcessNamespace", &ps.ShareProcessNamespace},
		{"enableServiceLinks", &ps.EnableServiceLinks},
		{"setHostnameAsFQDN", &ps.SetHostnameAsFQDN},
		{"hostUsers", &ps.HostUsers},
	} {
		v, err := parseBoolField(props, f.key, f.key)
		if err != nil {
			return PodSpecConfig{}, err
		}
		if v != nil {
			*f.dst = v
		}
	}
	// ValidatePodSpec: "ShareProcessNamespace and HostPID cannot both be enabled".
	if ps.ShareProcessNamespace != nil && *ps.ShareProcessNamespace && ps.HostPID {
		return PodSpecConfig{}, errors.New("shareProcessNamespace and hostPID cannot both be true")
	}
	if raw, present, err := parseObjectField(props, "podSecurityContext", "podSecurityContext"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		sc, err := parsePodSecurityContext(raw, "podSecurityContext")
		if err != nil {
			return PodSpecConfig{}, err
		}
		ps.SecurityContext = sc
	}
	if list, present, err := parseObjectList(props, "imagePullSecrets"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		for i, m := range list {
			label := indexedLabel("imagePullSecrets", i)
			if err := rejectUnknownKeys(m, []string{"name"}, label); err != nil {
				return PodSpecConfig{}, err
			}
			name, err := requireDNS1123Subdomain(m, "name", label)
			if err != nil {
				return PodSpecConfig{}, err
			}
			ps.ImagePullSecrets = append(ps.ImagePullSecrets, corev1.LocalObjectReference{Name: name})
		}
	}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"hostname", &ps.Hostname},
		{"subdomain", &ps.Subdomain},
	} {
		v, present, err := parseStringField(props, f.key, f.key)
		if err != nil {
			return PodSpecConfig{}, err
		}
		if present {
			// ValidatePodSpec validates both as DNS-1123 labels.
			if errs := validation.IsDNS1123Label(v); len(errs) > 0 {
				return PodSpecConfig{}, errors.Errorf("%s: invalid value %q: %s", f.key, v, strings.Join(errs, "; "))
			}
			*f.dst = v
		}
	}
	if v, present, err := parseStringField(props, "schedulerName", "schedulerName"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		ps.SchedulerName = v
	}
	if list, present, err := parseObjectList(props, "hostAliases"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		for i, m := range list {
			label := indexedLabel("hostAliases", i)
			ha, err := parseHostAlias(m, label)
			if err != nil {
				return PodSpecConfig{}, err
			}
			ps.HostAliases = append(ps.HostAliases, ha)
		}
	}
	if v, present, err := parseStringField(props, "priorityClassName", "priorityClassName"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return PodSpecConfig{}, errors.Errorf("priorityClassName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		ps.PriorityClassName = v
	}
	if raw, present, err := parseObjectField(props, "dnsConfig", "dnsConfig"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		dc, err := parsePodDNSConfig(raw, "dnsConfig")
		if err != nil {
			return PodSpecConfig{}, err
		}
		ps.DNSConfig = dc
	}
	// validatePodDNSConfig: with dnsPolicy None, dnsConfig.nameservers must
	// have at least one entry — the pod has no other resolver source.
	if ps.DNSPolicy == corev1.DNSNone && (ps.DNSConfig == nil || len(ps.DNSConfig.Nameservers) == 0) {
		return PodSpecConfig{}, errors.New("dnsPolicy: None requires dnsConfig.nameservers with at least one entry")
	}
	if list, present, err := parseObjectList(props, "readinessGates"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		for i, m := range list {
			label := indexedLabel("readinessGates", i)
			if err := rejectUnknownKeys(m, []string{"conditionType"}, label); err != nil {
				return PodSpecConfig{}, err
			}
			ct, err := requireQualifiedName(m, "conditionType", label)
			if err != nil {
				return PodSpecConfig{}, err
			}
			ps.ReadinessGates = append(ps.ReadinessGates, corev1.PodReadinessGate{ConditionType: corev1.PodConditionType(ct)})
		}
	}
	if v, present, err := parseStringField(props, "runtimeClassName", "runtimeClassName"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return PodSpecConfig{}, errors.Errorf("runtimeClassName: invalid name %q: %s", v, strings.Join(errs, "; "))
		}
		ps.RuntimeClassName = &v
	}
	if v, present, err := parseStringField(props, "preemptionPolicy", "preemptionPolicy"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		switch corev1.PreemptionPolicy(v) {
		case corev1.PreemptLowerPriority, corev1.PreemptNever:
			pp := corev1.PreemptionPolicy(v)
			ps.PreemptionPolicy = &pp
		default:
			return PodSpecConfig{}, errors.Errorf("preemptionPolicy: invalid value %q, must be PreemptLowerPriority or Never", v)
		}
	}
	if raw, present, err := parseObjectField(props, "os", "os"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, []string{"name"}, "os"); err != nil {
			return PodSpecConfig{}, err
		}
		name, present, err := parseStringField(raw, "name", "os.name")
		if err != nil {
			return PodSpecConfig{}, err
		}
		if !present {
			return PodSpecConfig{}, errors.New("os.name: required")
		}
		switch corev1.OSName(name) {
		case corev1.Linux, corev1.Windows:
			ps.OS = &corev1.PodOS{Name: corev1.OSName(name)}
		default:
			return PodSpecConfig{}, errors.Errorf("os.name: invalid value %q, must be linux or windows", name)
		}
	}
	if list, present, err := parseObjectList(props, "schedulingGates"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		seen := map[string]bool{}
		for i, m := range list {
			label := indexedLabel("schedulingGates", i)
			if err := rejectUnknownKeys(m, []string{"name"}, label); err != nil {
				return PodSpecConfig{}, err
			}
			name, err := requireQualifiedName(m, "name", label)
			if err != nil {
				return PodSpecConfig{}, err
			}
			// validateSchedulingGates: gate names must be unique.
			if seen[name] {
				return PodSpecConfig{}, errors.Errorf("%s.name: duplicate scheduling gate %q", label, name)
			}
			seen[name] = true
			ps.SchedulingGates = append(ps.SchedulingGates, corev1.PodSchedulingGate{Name: name})
		}
		// validatePodSpec: "nodeName: cannot be set until all schedulingGates
		// have been cleared" — a gated pod has not been scheduled yet.
		if ps.NodeName != "" && len(ps.SchedulingGates) > 0 {
			return PodSpecConfig{}, errors.New("nodeName: cannot be set together with schedulingGates; a gated pod is not scheduled until every gate is cleared")
		}
	}
	if list, present, err := parseObjectList(props, "resourceClaims"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		seen := map[string]bool{}
		for i, m := range list {
			label := indexedLabel("resourceClaims", i)
			rc, err := parsePodResourceClaim(m, label)
			if err != nil {
				return PodSpecConfig{}, err
			}
			// validatePodResourceClaims: claim names must be unique.
			if seen[rc.Name] {
				return PodSpecConfig{}, errors.Errorf("%s.name: duplicate resource claim %q", label, rc.Name)
			}
			seen[rc.Name] = true
			ps.ResourceClaims = append(ps.ResourceClaims, rc)
		}
	}
	if raw, present, err := parseObjectField(props, "podResources", "podResources"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		// parseResources only type-asserts requests/limits, so close the object
		// here: a typo'd key or a non-object requests/limits value must fail,
		// not silently emit no pod-level resources.
		if err := rejectUnknownKeys(raw, []string{"requests", "limits"}, "podResources"); err != nil {
			return PodSpecConfig{}, err
		}
		for _, k := range []string{"requests", "limits"} {
			if v, ok := raw[k]; ok {
				if _, isObj := v.(map[string]any); !isObj {
					return PodSpecConfig{}, errors.Errorf("podResources.%s: must be an object, got %T", k, v)
				}
			}
		}
		rr, err := parseResources(raw)
		if err != nil {
			return PodSpecConfig{}, errors.Wrap(err, "invalid podResources configuration")
		}
		// validatePodResourceRequirements: pod-level resources accept only
		// cpu, memory and hugepages-<size> — no ephemeral-storage, no
		// extended resources, unlike a container's resources.
		for _, rl := range []corev1.ResourceList{rr.Requests, rr.Limits} {
			for name := range rl {
				if name != corev1.ResourceCPU && name != corev1.ResourceMemory && !isHugePageResourceName(name) {
					return PodSpecConfig{}, errors.Errorf("podResources: %s: pod-level resources support only cpu, memory, and hugepages-<size>", name)
				}
			}
		}
		if len(rr.Requests) > 0 || len(rr.Limits) > 0 {
			ps.Resources = &rr.ResourceRequirements
		}
	}
	if v, present, err := parseStringField(props, "hostnameOverride", "hostnameOverride"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		// ValidatePodSpec's hostnameOverride checks: a DNS-1123 subdomain of at
		// most 64 characters, and mutually exclusive with hostNetwork and
		// setHostnameAsFQDN.
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 || len(v) > 64 {
			return PodSpecConfig{}, errors.Errorf("hostnameOverride: invalid value %q: must be a lowercase RFC 1123 subdomain of at most 64 characters", v)
		}
		if ps.HostNetwork {
			return PodSpecConfig{}, errors.New("hostnameOverride: cannot be set when hostNetwork is true")
		}
		if ps.SetHostnameAsFQDN != nil && *ps.SetHostnameAsFQDN {
			return PodSpecConfig{}, errors.New("hostnameOverride: cannot be set when setHostnameAsFQDN is true")
		}
		ps.HostnameOverride = &v
	}
	if raw, present, err := parseObjectField(props, "schedulingGroup", "schedulingGroup"); err != nil {
		return PodSpecConfig{}, err
	} else if present {
		if err := rejectUnknownKeys(raw, []string{"podGroupName"}, "schedulingGroup"); err != nil {
			return PodSpecConfig{}, err
		}
		name, err := requireDNS1123Subdomain(raw, "podGroupName", "schedulingGroup")
		if err != nil {
			return PodSpecConfig{}, err
		}
		ps.SchedulingGroup = &corev1.PodSchedulingGroup{PodGroupName: &name}
	}

	if err := validatePodOSFields(ps); err != nil {
		return PodSpecConfig{}, err
	}

	return cfg, nil
}

// validatePodOSFields mirrors the pod-level half of corev1.PodSpec.OS's
// contract (k8s.io/api core/v1 types.go, PodSpec.OS doc comment; enforced by
// validateLinux/validateWindows in k8s.io/kubernetes pkg/apis/core/validation):
// with os.name windows the Linux-only pod fields must be unset, with os.name
// linux securityContext.windowsOptions must be unset. Container-level fields
// are checked by validateContainerOSFields once the containers are assembled
// in buildPodSpec. No os → no constraint.
func validatePodOSFields(ps *corev1.PodSpec) error {
	if ps.OS == nil {
		return nil
	}
	sc := ps.SecurityContext
	switch ps.OS.Name {
	case corev1.Linux:
		if sc != nil && sc.WindowsOptions != nil {
			return errors.New("os.name linux: podSecurityContext.windowsOptions must be unset")
		}
	case corev1.Windows:
		set := []struct {
			key string
			set bool
		}{
			{"hostPID", ps.HostPID},
			{"hostIPC", ps.HostIPC},
			{"hostUsers", ps.HostUsers != nil},
			{"shareProcessNamespace", ps.ShareProcessNamespace != nil},
			{"podResources", ps.Resources != nil},
			{"podSecurityContext.appArmorProfile", sc != nil && sc.AppArmorProfile != nil},
			{"podSecurityContext.seLinuxOptions", sc != nil && sc.SELinuxOptions != nil},
			{"podSecurityContext.seccompProfile", sc != nil && sc.SeccompProfile != nil},
			{"podSecurityContext.fsGroup", sc != nil && sc.FSGroup != nil},
			{"podSecurityContext.fsGroupChangePolicy", sc != nil && sc.FSGroupChangePolicy != nil},
			{"podSecurityContext.sysctls", sc != nil && len(sc.Sysctls) > 0},
			{"podSecurityContext.runAsUser", sc != nil && sc.RunAsUser != nil},
			{"podSecurityContext.runAsGroup", sc != nil && sc.RunAsGroup != nil},
			{"podSecurityContext.supplementalGroups", sc != nil && sc.SupplementalGroups != nil},
			{"podSecurityContext.supplementalGroupsPolicy", sc != nil && sc.SupplementalGroupsPolicy != nil},
			{"podSecurityContext.seLinuxChangePolicy", sc != nil && sc.SELinuxChangePolicy != nil},
		}
		for _, f := range set {
			if f.set {
				return errors.Errorf("os.name windows: %s must be unset", f.key)
			}
		}
	}
	return nil
}

// validateContainerOSFields is the container-level half of the PodSpec.OS
// contract (see validatePodOSFields), applied to every init, main and sidecar
// container once buildPodSpec has assembled them.
func validateContainerOSFields(ps *corev1.PodSpec) error {
	if ps.OS == nil {
		return nil
	}
	check := func(list string, containers []corev1.Container) error {
		for i, c := range containers {
			sc := c.SecurityContext
			if sc == nil {
				continue
			}
			label := indexedLabel(list, i) + ".securityContext"
			switch ps.OS.Name {
			case corev1.Linux:
				if sc.WindowsOptions != nil {
					return errors.Errorf("os.name linux: %s.windowsOptions must be unset", label)
				}
			case corev1.Windows:
				set := []struct {
					key string
					set bool
				}{
					{"appArmorProfile", sc.AppArmorProfile != nil},
					{"seLinuxOptions", sc.SELinuxOptions != nil},
					{"seccompProfile", sc.SeccompProfile != nil},
					{"capabilities", sc.Capabilities != nil},
					{"readOnlyRootFilesystem", sc.ReadOnlyRootFilesystem != nil},
					{"privileged", sc.Privileged != nil},
					{"allowPrivilegeEscalation", sc.AllowPrivilegeEscalation != nil},
					{"procMount", sc.ProcMount != nil},
					{"runAsUser", sc.RunAsUser != nil},
					{"runAsGroup", sc.RunAsGroup != nil},
				}
				for _, f := range set {
					if f.set {
						return errors.Errorf("os.name windows: %s.%s must be unset", label, f.key)
					}
				}
			}
		}
		return nil
	}
	if err := check("initContainers", ps.InitContainers); err != nil {
		return err
	}
	return check("containers", ps.Containers)
}

// parseObjectList reads an optional array-of-objects property: absent is
// (nil, false, nil); a present non-array, or any non-object element, is an
// error rather than silently skipped.
func parseObjectList(props map[string]any, key string) ([]map[string]any, bool, error) {
	v, present := props[key]
	if !present {
		return nil, false, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, errors.Errorf("%s: must be an array, got %T", key, v)
	}
	out := make([]map[string]any, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false, errors.Errorf("%s: must be an object, got %T", indexedLabel(key, i), item)
		}
		out = append(out, m)
	}
	return out, true, nil
}

// parseStringList reads an optional array-of-strings field with the same
// present/absent/wrong-type contract as parseObjectList.
func parseStringList(raw map[string]any, key, label string) ([]string, bool, error) {
	v, present := raw[key]
	if !present {
		return nil, false, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, errors.Errorf("%s: must be an array, got %T", label, v)
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false, errors.Errorf("%s: must be a string, got %T", indexedLabel(label, i), item)
		}
		out = append(out, s)
	}
	return out, true, nil
}

func indexedLabel(label string, i int) string {
	return label + "[" + strconv.Itoa(i) + "]"
}

// sysctlNameRe mirrors the API's SysctlContainSlashFmt: dot- or
// slash-separated segments of lowercase alphanumerics, each segment allowing
// interior dashes and underscores. k8s.io/api does not export the pattern, so
// it is restated here rather than depending on k8s.io/kubernetes.
var sysctlNameRe = regexp.MustCompile(`^([a-z0-9]([-_a-z0-9]*[a-z0-9])?[./])*[a-z0-9]([-_a-z0-9]*[a-z0-9])?$`)

// isValidSysctlName mirrors validation.IsValidSysctlName: at most
// SysctlMaxLength (253) characters and matching the sysctl name grammar.
func isValidSysctlName(name string) bool {
	if len(name) > 253 {
		return false
	}
	return sysctlNameRe.MatchString(name)
}

// requireDNS1123Subdomain reads a required string field and validates it as a
// DNS-1123 subdomain (the object-name rule every Kubernetes name follows).
func requireDNS1123Subdomain(raw map[string]any, key, label string) (string, error) {
	v, present, err := parseStringField(raw, key, label+"."+key)
	if err != nil {
		return "", err
	}
	if !present {
		return "", errors.Errorf("%s.%s: required", label, key)
	}
	if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
		return "", errors.Errorf("%s.%s: invalid name %q: %s", label, key, v, strings.Join(errs, "; "))
	}
	return v, nil
}

// requireQualifiedName reads a required string field and validates it as a
// Kubernetes qualified name (optional DNS-subdomain prefix + "/" + name).
func requireQualifiedName(raw map[string]any, key, label string) (string, error) {
	v, present, err := parseStringField(raw, key, label+"."+key)
	if err != nil {
		return "", err
	}
	if !present {
		return "", errors.Errorf("%s.%s: required", label, key)
	}
	if errs := validation.IsQualifiedName(v); len(errs) > 0 {
		return "", errors.Errorf("%s.%s: invalid value %q: %s", label, key, v, strings.Join(errs, "; "))
	}
	return v, nil
}

// parseLabelMap parses a map of label key → label value (nodeSelector), with
// every key a qualified name and every value a valid label value, mirroring
// ValidateLabels. A non-string value is an error, not silently dropped.
func parseLabelMap(raw map[string]any, label string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		s, ok := raw[k].(string)
		if !ok {
			return nil, errors.Errorf("%s.%s: must be a string, got %T", label, k, raw[k])
		}
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, errors.Errorf("%s: invalid label key %q: %s", label, k, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(s); len(errs) > 0 {
			return nil, errors.Errorf("%s.%s: invalid label value %q: %s", label, k, s, strings.Join(errs, "; "))
		}
		out[k] = s
	}
	return out, nil
}

// parseHostAlias parses one `hostAliases` entry (validateHostAliases: a valid
// IP plus at least one DNS-1123 subdomain hostname).
func parseHostAlias(m map[string]any, label string) (corev1.HostAlias, error) {
	if err := rejectUnknownKeys(m, []string{"ip", "hostnames"}, label); err != nil {
		return corev1.HostAlias{}, err
	}
	ip, present, err := parseStringField(m, "ip", label+".ip")
	if err != nil {
		return corev1.HostAlias{}, err
	}
	if !present {
		return corev1.HostAlias{}, errors.Errorf("%s.ip: required", label)
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		return corev1.HostAlias{}, errors.Errorf("%s.ip: invalid IP address %q", label, ip)
	}
	hostnames, present, err := parseStringList(m, "hostnames", label+".hostnames")
	if err != nil {
		return corev1.HostAlias{}, err
	}
	if !present || len(hostnames) == 0 {
		return corev1.HostAlias{}, errors.Errorf("%s.hostnames: at least one hostname is required", label)
	}
	for i, h := range hostnames {
		if errs := validation.IsDNS1123Subdomain(h); len(errs) > 0 {
			return corev1.HostAlias{}, errors.Errorf("%s: invalid hostname %q: %s", indexedLabel(label+".hostnames", i), h, strings.Join(errs, "; "))
		}
	}
	return corev1.HostAlias{IP: ip, Hostnames: hostnames}, nil
}

// parsePodDNSConfig parses the `dnsConfig` object (validatePodDNSConfig: at
// most 3 nameservers, each a valid IP; at most 32 searches, each a DNS-1123
// subdomain; every option has a name).
func parsePodDNSConfig(raw map[string]any, label string) (*corev1.PodDNSConfig, error) {
	if err := rejectUnknownKeys(raw, []string{"nameservers", "searches", "options"}, label); err != nil {
		return nil, err
	}
	dc := &corev1.PodDNSConfig{}
	if ns, present, err := parseStringList(raw, "nameservers", label+".nameservers"); err != nil {
		return nil, err
	} else if present {
		if len(ns) > 3 {
			return nil, errors.Errorf("%s.nameservers: at most 3 nameservers are allowed, got %d", label, len(ns))
		}
		for i, s := range ns {
			if _, err := netip.ParseAddr(s); err != nil {
				return nil, errors.Errorf("%s: invalid IP address %q", indexedLabel(label+".nameservers", i), s)
			}
		}
		dc.Nameservers = ns
	}
	if searches, present, err := parseStringList(raw, "searches", label+".searches"); err != nil {
		return nil, err
	} else if present {
		if len(searches) > 32 {
			return nil, errors.Errorf("%s.searches: at most 32 search paths are allowed, got %d", label, len(searches))
		}
		// validateDNSConfig also caps the whole list: the resolv.conf search
		// line is at most MaxDNSSearchListChars (2048) characters including
		// the spaces between entries, so 32 individually valid domains can
		// still be rejected at admission.
		if total := len(strings.Join(searches, " ")); total > 2048 {
			return nil, errors.Errorf("%s.searches: the search list must be at most 2048 characters including separating spaces, got %d", label, total)
		}
		for i, s := range searches {
			if errs := validation.IsDNS1123Subdomain(strings.TrimSuffix(s, ".")); len(errs) > 0 {
				return nil, errors.Errorf("%s: invalid search path %q: %s", indexedLabel(label+".searches", i), s, strings.Join(errs, "; "))
			}
		}
		dc.Searches = searches
	}
	if opts, present, err := parseObjectList(raw, "options"); err != nil {
		return nil, errors.Errorf("%s.%w", label, err)
	} else if present {
		for i, m := range opts {
			optLabel := indexedLabel(label+".options", i)
			if err := rejectUnknownKeys(m, []string{"name", "value"}, optLabel); err != nil {
				return nil, err
			}
			name, present, err := parseStringField(m, "name", optLabel+".name")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.name: required", optLabel)
			}
			opt := corev1.PodDNSConfigOption{Name: name}
			if v, exists := m["value"]; exists {
				s, ok := v.(string)
				if !ok {
					return nil, errors.Errorf("%s.value: must be a string, got %T", optLabel, v)
				}
				opt.Value = &s
			}
			dc.Options = append(dc.Options, opt)
		}
	}
	return dc, nil
}

// parsePodResourceClaim parses one `resourceClaims` entry
// (validatePodResourceClaim: a DNS-1123 label name and exactly one of
// resourceClaimName / resourceClaimTemplateName).
func parsePodResourceClaim(m map[string]any, label string) (corev1.PodResourceClaim, error) {
	if err := rejectUnknownKeys(m, []string{"name", "resourceClaimName", "resourceClaimTemplateName"}, label); err != nil {
		return corev1.PodResourceClaim{}, err
	}
	name, present, err := parseStringField(m, "name", label+".name")
	if err != nil {
		return corev1.PodResourceClaim{}, err
	}
	if !present {
		return corev1.PodResourceClaim{}, errors.Errorf("%s.name: required", label)
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return corev1.PodResourceClaim{}, errors.Errorf("%s.name: invalid name %q: %s", label, name, strings.Join(errs, "; "))
	}
	rc := corev1.PodResourceClaim{Name: name}
	claimName, hasClaim, err := parseStringField(m, "resourceClaimName", label+".resourceClaimName")
	if err != nil {
		return corev1.PodResourceClaim{}, err
	}
	templateName, hasTemplate, err := parseStringField(m, "resourceClaimTemplateName", label+".resourceClaimTemplateName")
	if err != nil {
		return corev1.PodResourceClaim{}, err
	}
	if hasClaim == hasTemplate {
		return corev1.PodResourceClaim{}, errors.Errorf("%s: exactly one of resourceClaimName or resourceClaimTemplateName must be set", label)
	}
	if hasClaim {
		if errs := validation.IsDNS1123Subdomain(claimName); len(errs) > 0 {
			return corev1.PodResourceClaim{}, errors.Errorf("%s.resourceClaimName: invalid name %q: %s", label, claimName, strings.Join(errs, "; "))
		}
		rc.ResourceClaimName = &claimName
	} else {
		if errs := validation.IsDNS1123Subdomain(templateName); len(errs) > 0 {
			return corev1.PodResourceClaim{}, errors.Errorf("%s.resourceClaimTemplateName: invalid name %q: %s", label, templateName, strings.Join(errs, "; "))
		}
		rc.ResourceClaimTemplateName = &templateName
	}
	return rc, nil
}

// parsePodSecurityContext parses the pod-level `podSecurityContext` object
// onto a real corev1.PodSecurityContext. The seccompProfile / seLinuxOptions /
// appArmorProfile sub-objects share their parsers with the container-level
// securityContext (parseSecurityContext) so the two cannot drift.
func parsePodSecurityContext(raw map[string]any, label string) (*corev1.PodSecurityContext, error) {
	if err := rejectUnknownKeys(raw, []string{
		"seLinuxOptions", "windowsOptions", "runAsUser", "runAsGroup", "runAsNonRoot",
		"supplementalGroups", "supplementalGroupsPolicy", "fsGroup", "sysctls",
		"fsGroupChangePolicy", "seccompProfile", "appArmorProfile", "seLinuxChangePolicy",
	}, label); err != nil {
		return nil, err
	}
	sc := &corev1.PodSecurityContext{}
	set := false

	for _, f := range []struct {
		key string
		dst **int64
	}{
		{"runAsUser", &sc.RunAsUser},
		{"runAsGroup", &sc.RunAsGroup},
		{"fsGroup", &sc.FSGroup},
	} {
		i, present, err := parseInt64Field(raw, f.key, label+"."+f.key)
		if err != nil {
			return nil, err
		}
		if present {
			if i < 0 {
				return nil, errors.Errorf("%s.%s: must not be negative, got %d", label, f.key, i)
			}
			v := i
			*f.dst = &v
			set = true
		}
	}
	if v, err := parseBoolField(raw, "runAsNonRoot", label+".runAsNonRoot"); err != nil {
		return nil, err
	} else if v != nil {
		sc.RunAsNonRoot = v
		set = true
	}
	// Same kubelet-time contradiction the container-level parser rejects.
	if sc.RunAsUser != nil && *sc.RunAsUser == 0 && sc.RunAsNonRoot != nil && *sc.RunAsNonRoot {
		return nil, errors.Errorf("%s: runAsUser must not be 0 when runAsNonRoot is true", label)
	}
	if v, present := raw["supplementalGroups"]; present {
		arr, ok := v.([]any)
		if !ok {
			return nil, errors.Errorf("%s.supplementalGroups: must be an array, got %T", label, v)
		}
		groups := make([]int64, 0, len(arr))
		for i, item := range arr {
			g, ok := toInt64(item)
			if !ok {
				return nil, errors.Errorf("%s: must be an integer, got %T", indexedLabel(label+".supplementalGroups", i), item)
			}
			if g < 0 {
				return nil, errors.Errorf("%s: must not be negative, got %d", indexedLabel(label+".supplementalGroups", i), g)
			}
			groups = append(groups, g)
		}
		sc.SupplementalGroups = groups
		set = true
	}
	if v, present, err := parseStringField(raw, "supplementalGroupsPolicy", label+".supplementalGroupsPolicy"); err != nil {
		return nil, err
	} else if present {
		switch corev1.SupplementalGroupsPolicy(v) {
		case corev1.SupplementalGroupsPolicyMerge, corev1.SupplementalGroupsPolicyStrict:
			p := corev1.SupplementalGroupsPolicy(v)
			sc.SupplementalGroupsPolicy = &p
			set = true
		default:
			return nil, errors.Errorf("%s.supplementalGroupsPolicy: invalid value %q, must be Merge or Strict", label, v)
		}
	}
	if v, present, err := parseStringField(raw, "fsGroupChangePolicy", label+".fsGroupChangePolicy"); err != nil {
		return nil, err
	} else if present {
		switch corev1.PodFSGroupChangePolicy(v) {
		case corev1.FSGroupChangeOnRootMismatch, corev1.FSGroupChangeAlways:
			p := corev1.PodFSGroupChangePolicy(v)
			sc.FSGroupChangePolicy = &p
			set = true
		default:
			return nil, errors.Errorf("%s.fsGroupChangePolicy: invalid value %q, must be OnRootMismatch or Always", label, v)
		}
	}
	if v, present, err := parseStringField(raw, "seLinuxChangePolicy", label+".seLinuxChangePolicy"); err != nil {
		return nil, err
	} else if present {
		switch corev1.PodSELinuxChangePolicy(v) {
		case corev1.SELinuxChangePolicyMountOption, corev1.SELinuxChangePolicyRecursive:
			p := corev1.PodSELinuxChangePolicy(v)
			sc.SELinuxChangePolicy = &p
			set = true
		default:
			return nil, errors.Errorf("%s.seLinuxChangePolicy: invalid value %q, must be MountOption or Recursive", label, v)
		}
	}
	if list, present, err := parseObjectList(raw, "sysctls"); err != nil {
		return nil, errors.Errorf("%s.%w", label, err)
	} else if present {
		seenSysctls := map[string]bool{}
		for i, m := range list {
			sysLabel := indexedLabel(label+".sysctls", i)
			if err := rejectUnknownKeys(m, []string{"name", "value"}, sysLabel); err != nil {
				return nil, err
			}
			name, present, err := parseStringField(m, "name", sysLabel+".name")
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Errorf("%s.name: required", sysLabel)
			}
			// validateSysctls: the name must match the kernel-parameter
			// grammar (dot- or slash-separated lowercase segments, at most
			// 253 characters) and must not repeat within one
			// PodSecurityContext.
			if !isValidSysctlName(name) {
				return nil, errors.Errorf("%s.name: invalid sysctl name %q: must be at most 253 characters of dot- or slash-separated lowercase alphanumeric segments (e.g. net.core.somaxconn)", sysLabel, name)
			}
			if seenSysctls[name] {
				return nil, errors.Errorf("%s.name: duplicate sysctl %q", sysLabel, name)
			}
			seenSysctls[name] = true
			value, ok := m["value"].(string)
			if !ok {
				return nil, errors.Errorf("%s.value: required and must be a string", sysLabel)
			}
			sc.Sysctls = append(sc.Sysctls, corev1.Sysctl{Name: name, Value: value})
		}
		set = true
	}
	if v, present := raw["seccompProfile"]; present {
		sp, err := parseSeccompProfile(v, label+".seccompProfile")
		if err != nil {
			return nil, err
		}
		sc.SeccompProfile = sp
		set = true
	}
	if v, present := raw["seLinuxOptions"]; present {
		se, err := parseSELinuxOptions(v, label+".seLinuxOptions")
		if err != nil {
			return nil, err
		}
		if se != nil {
			sc.SELinuxOptions = se
			set = true
		}
	}
	if v, present := raw["appArmorProfile"]; present {
		ap, err := parseAppArmorProfile(v, label+".appArmorProfile")
		if err != nil {
			return nil, err
		}
		sc.AppArmorProfile = ap
		set = true
	}
	if raw, present, err := parseObjectField(raw, "windowsOptions", label+".windowsOptions"); err != nil {
		return nil, err
	} else if present {
		wo, err := parseWindowsOptions(raw, label+".windowsOptions")
		if err != nil {
			return nil, err
		}
		if wo != nil {
			sc.WindowsOptions = wo
			set = true
		}
	}

	if !set {
		return nil, nil
	}
	return sc, nil
}

// parseWindowsOptions parses a `windowsOptions` object; returns nil when no
// field was authored.
func parseWindowsOptions(raw map[string]any, label string) (*corev1.WindowsSecurityContextOptions, error) {
	if err := rejectUnknownKeys(raw, []string{"gmsaCredentialSpecName", "gmsaCredentialSpec", "runAsUserName", "hostProcess"}, label); err != nil {
		return nil, err
	}
	wo := &corev1.WindowsSecurityContextOptions{}
	set := false
	for _, f := range []struct {
		key string
		dst **string
	}{
		{"gmsaCredentialSpecName", &wo.GMSACredentialSpecName},
		{"gmsaCredentialSpec", &wo.GMSACredentialSpec},
		{"runAsUserName", &wo.RunAsUserName},
	} {
		v, present, err := parseStringField(raw, f.key, label+"."+f.key)
		if err != nil {
			return nil, err
		}
		if present {
			s := v
			*f.dst = &s
			set = true
		}
	}
	if v, err := parseBoolField(raw, "hostProcess", label+".hostProcess"); err != nil {
		return nil, err
	} else if v != nil {
		wo.HostProcess = v
		set = true
	}
	if !set {
		return nil, nil
	}
	return wo, nil
}

// --- Builders ---

// mainContainerInput carries the parsed main-container fields every kind
// handler holds on its own config struct; buildMainContainer turns them into
// the pod's first (main) container.
type mainContainerInput struct {
	Image           string
	Command         []string
	Args            []string
	Resources       ResourceRequirements
	Ports           []corev1.ContainerPort
	Env             []corev1.EnvVar
	EnvFrom         []corev1.EnvFromSource
	Probes          ProbeConfig
	WorkingDir      string
	Lifecycle       *corev1.Lifecycle
	SecurityContext *corev1.SecurityContext
	VolumeMounts    []corev1.VolumeMount
}

// buildMainContainer builds the main container of every workload kind.
//
// go-kure/launcher#361: this is the one remaining kubernetes.CreateContainer call in
// the workload family (buildInitContainer/buildSidecarContainer share the same
// constructor). It stays until the kure builder-contract release-1 adoption
// swaps it for a corev1.Container literal, because the constructor still
// injects `imagePullPolicy: IfNotPresent` and placeholder resources into the
// goldens — recording that delta belongs to go-kure/launcher#361, not here.
func buildMainContainer(name string, in mainContainerInput) *corev1.Container {
	container := kubernetes.CreateContainer(name, in.Image, in.Command, in.Args)
	container.Resources = buildResourceRequirements(in.Resources)
	container.Ports = append(container.Ports, in.Ports...)
	container.Env = append(container.Env, in.Env...)
	container.EnvFrom = append(container.EnvFrom, in.EnvFrom...)
	applyProbes(container, in.Probes)
	if in.WorkingDir != "" {
		container.WorkingDir = in.WorkingDir
	}
	if in.Lifecycle != nil {
		container.Lifecycle = in.Lifecycle
	}
	if in.SecurityContext != nil {
		sc := *in.SecurityContext
		container.SecurityContext = &sc
	}
	container.VolumeMounts = append(container.VolumeMounts, in.VolumeMounts...)
	return container
}

// podSpecInput is everything a kind handler contributes to its pod template
// beyond the shared PodSpecConfig: the containers, the volumes, and the
// scheduling/lifecycle fields it exposes as its own properties.
type podSpecInput struct {
	Config PodSpecConfig
	// DefaultServiceAccountName is the per-component ServiceAccount the pod
	// runs as when Config.ServiceAccountName is not authored.
	DefaultServiceAccountName string
	MainContainer             *corev1.Container
	InitContainers            []InitContainerConfig
	Sidecars                  []SidecarContainerConfig
	Volumes                   []corev1.Volume
	Tolerations               []corev1.Toleration
	TopologySpreadConstraints []corev1.TopologySpreadConstraint
	Affinity                  *corev1.Affinity
	// RestartPolicy is written only when non-empty (cronjob); the other kinds
	// leave it to the API default.
	RestartPolicy corev1.RestartPolicy
}

// buildPodSpec assembles the complete corev1.PodSpec for a workload's pod
// template: the authored pod-level fields (in.Config) plus the containers,
// volumes and scheduling inputs the kind handler supplies. Init containers
// precede the main container in declaration order; sidecars follow it in
// spec.containers — kube preserves declaration order on the pod spec and
// kustomize build output stays stable.
func buildPodSpec(in podSpecInput) (corev1.PodSpec, error) {
	ps := in.Config.PodSpec
	for _, ic := range in.InitContainers {
		initContainer, err := buildInitContainer(ic)
		if err != nil {
			return corev1.PodSpec{}, err
		}
		ps.InitContainers = append(ps.InitContainers, *initContainer)
	}
	if in.MainContainer == nil {
		return corev1.PodSpec{}, errors.New("pod template requires a main container")
	}
	ps.Containers = append(ps.Containers, *in.MainContainer)
	for _, sc := range in.Sidecars {
		sidecarContainer, err := buildSidecarContainer(sc)
		if err != nil {
			return corev1.PodSpec{}, err
		}
		ps.Containers = append(ps.Containers, *sidecarContainer)
	}
	ps.Volumes = append(ps.Volumes, in.Volumes...)
	ps.Tolerations = append(ps.Tolerations, in.Tolerations...)
	ps.TopologySpreadConstraints = append(ps.TopologySpreadConstraints, in.TopologySpreadConstraints...)
	if in.Affinity != nil {
		ps.Affinity = in.Affinity
	}
	if in.RestartPolicy != "" {
		ps.RestartPolicy = in.RestartPolicy
	}
	if ps.ServiceAccountName == "" {
		ps.ServiceAccountName = in.DefaultServiceAccountName
	}
	if err := validateContainerOSFields(&ps); err != nil {
		return corev1.PodSpec{}, err
	}
	return ps, nil
}

// effectiveServiceAccountName returns the ServiceAccount a workload's pods run
// as: the authored serviceAccountName when set, else the per-component
// ServiceAccount named after the component. Traits that bind RBAC to the
// workload's identity (the rbac trait) read this through the
// oam.ServiceAccountNamer interface each kind config implements.
func effectiveServiceAccountName(cfg PodSpecConfig, componentName string) string {
	if cfg.ServiceAccountName != "" {
		return cfg.ServiceAccountName
	}
	return componentName
}

// generatesServiceAccount reports whether the kind handler should emit its
// per-component ServiceAccount: only when no serviceAccountName was authored.
func generatesServiceAccount(cfg PodSpecConfig) bool {
	return cfg.ServiceAccountName == ""
}
