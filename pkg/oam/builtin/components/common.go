package components

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
)

// ValidateImageRef validates a container image reference.
// It rejects untagged images and images using the :latest tag.
// Digest references are always accepted.
func ValidateImageRef(image string) error {
	ref, err := name.ParseReference(image)
	if err != nil {
		return errors.Errorf("image %q rejected: %w", image, err)
	}

	switch r := ref.(type) {
	case name.Digest:
		return nil
	case name.Tag:
		if r.TagStr() != "latest" {
			return nil
		}
		if hasExplicitLatestTag(image) {
			return errors.Errorf("image %q rejected: :latest tag not allowed; use an explicit version tag or digest", image)
		}
		return errors.Errorf("image %q rejected: no tag or digest specified; use an explicit version tag or digest", image)
	}
	return nil
}

func hasExplicitLatestTag(image string) bool {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	return strings.HasSuffix(image, ":latest")
}

// --- Property type helpers (inlined from the downstream runtime's proputil) ---

func toInt32(v any) (int32, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true
	case int:
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true
	case int32:
		return n, true
	case int64:
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true
	default:
		return 0, false
	}
}

// toInt64 mirrors toInt32 for the *int64 fields corev1 uses for UID/GID
// (SecurityContext.RunAsUser/RunAsGroup) and probe termination grace period
// (Probe.TerminationGracePeriodSeconds). Kept local to this package with the
// same (value, bool) shape as toInt32 rather than reusing
// traits.toInt64(v)(int64,error): that sibling package's callers want a
// descriptive error to wrap; this file's existing "present but wrong type is
// silently skipped" convention (see every `if v, n := m[key]; n { if i, ok :=
// toInt32(v); ok { ... } }` in parseProbe) is what every other optional
// numeric field here already follows.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		// float64 cannot exactly represent math.MaxInt64 (nearest representable
		// value rounds up to 2^63), so the upper bound must be a strict "<"
		// against the rounded constant — the same overflow-safe comparison
		// idiom Go's standard library uses for float-to-int64 conversions.
		// math.MinInt64 (-2^63) IS exactly representable, so "<" is correct
		// there too (n == MinInt64 must still be accepted).
		if n < math.MinInt64 || n >= math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

func stringMap(m map[string]any) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// --- Data types ---

// ResourceRequirements projects the real corev1.ResourceRequirements directly
// (same structural pattern as ProbeConfig holding *corev1.Probe fields, and
// parseEnvVarSource/parseSecurityContext above) rather than a hand-rolled
// parallel struct, so every resource name — cpu, memory, ephemeral-storage,
// hugepages-2Mi, an extended resource like nvidia.com/gpu, or any future
// corev1.ResourceList entry — round-trips through Requests/Limits unmodified.
// Policy defaulting/enforcement (ApplyPolicy in each kind file;
// applyDefaultQuantity/enforceMaxResource in enforce.go) still targets cpu/
// memory specifically; which individual resource names were explicitly
// authored (vs. left for policy to default) is read directly off map-key
// presence in Requests/Limits — see applyDefaultQuantity's doc comment.
type ResourceRequirements struct {
	corev1.ResourceRequirements
}

// ProbeConfig holds parsed probe configuration for a container.
type ProbeConfig struct {
	Readiness *corev1.Probe
	Liveness  *corev1.Probe
	Startup   *corev1.Probe
}

// InitContainerConfig represents a parsed init container from OAM.
type InitContainerConfig struct {
	Name         string
	Image        string
	Command      []string
	Args         []string
	Env          []corev1.EnvVar
	Resources    ResourceRequirements
	VolumeMounts []corev1.VolumeMount
}

// SidecarContainerConfig holds the parsed OAM fields for a sidecar container.
type SidecarContainerConfig struct {
	Name         string
	Image        string
	Command      []string
	Args         []string
	Env          []corev1.EnvVar
	Resources    ResourceRequirements
	VolumeMounts []corev1.VolumeMount
	Ports        []corev1.ContainerPort
}

// AffinityConfig holds parsed affinity/anti-affinity configuration from OAM properties.
type AffinityConfig struct {
	EnablePodAntiAffinity bool
	TopologyKey           string
	PodAntiAffinityType   string
	NodeSelector          map[string]string
}

// PVCConfig holds configuration for a PersistentVolumeClaim to be generated.
type PVCConfig struct {
	Name         string
	Size         string
	StorageClass string
	AccessModes  []string
}

// ParsedVolumes holds the results of parsing volume definitions from OAM properties.
type ParsedVolumes struct {
	Volumes []corev1.Volume
	Mounts  []corev1.VolumeMount
	PVCs    []PVCConfig
}

// TolerationConfig represents a toleration parsed from OAM properties.
type TolerationConfig struct {
	Key      string
	Operator string
	Value    string
	Effect   string
}

// --- Parsers ---

func parseEnv(props map[string]any) ([]corev1.EnvVar, error) {
	var envVars []corev1.EnvVar
	if envList, ok := props["env"].([]any); ok {
		for _, e := range envList {
			if envMap, ok := e.(map[string]any); ok {
				envName, _ := envMap["name"].(string)
				if envName == "" {
					continue
				}
				value, _ := envMap["value"].(string)
				vf, hasValueFrom := envMap["valueFrom"].(map[string]any)
				// Mirrors corev1.EnvVar.ValueFrom's own doc comment: "Cannot be
				// used if value is not empty." An empty `value: ""` alongside
				// valueFrom is not rejected (matches upstream validation exactly).
				if value != "" && hasValueFrom {
					return nil, errors.Errorf("env %q: value and valueFrom are mutually exclusive (valueFrom cannot be used if value is not empty)", envName)
				}
				ev := corev1.EnvVar{Name: envName}
				if hasValueFrom {
					src, err := parseEnvVarSource(vf)
					if err != nil {
						return nil, errors.Errorf("env %q: %w", envName, err)
					}
					ev.ValueFrom = src
				} else {
					ev.Value = value
				}
				envVars = append(envVars, ev)
			}
		}
	}
	return envVars, nil
}

// parseEnvVarSource parses a `valueFrom` object into the real corev1.EnvVarSource
// (same structural pattern as ProbeConfig holding *corev1.Probe directly): exactly
// one of secretKeyRef, configMapKeyRef, fieldRef, resourceFieldRef, or fileKeyRef.
func parseEnvVarSource(vf map[string]any) (*corev1.EnvVarSource, error) {
	_, hasSecret := vf["secretKeyRef"].(map[string]any)
	_, hasConfigMap := vf["configMapKeyRef"].(map[string]any)
	_, hasFieldRef := vf["fieldRef"].(map[string]any)
	_, hasResourceFieldRef := vf["resourceFieldRef"].(map[string]any)
	_, hasFileKeyRef := vf["fileKeyRef"].(map[string]any)
	count := 0
	for _, present := range []bool{hasSecret, hasConfigMap, hasFieldRef, hasResourceFieldRef, hasFileKeyRef} {
		if present {
			count++
		}
	}
	if count > 1 {
		return nil, errors.Errorf("valueFrom: secretKeyRef, configMapKeyRef, fieldRef, resourceFieldRef, and fileKeyRef are mutually exclusive")
	}

	src := &corev1.EnvVarSource{}
	if skr, ok := vf["secretKeyRef"].(map[string]any); ok {
		if n, key, ok := parseNameKey(skr); ok {
			sel := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: n}, Key: key}
			if opt, ok := skr["optional"].(bool); ok {
				sel.Optional = &opt
			}
			src.SecretKeyRef = sel
		}
	}
	if cmr, ok := vf["configMapKeyRef"].(map[string]any); ok {
		if n, key, ok := parseNameKey(cmr); ok {
			sel := &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: n}, Key: key}
			if opt, ok := cmr["optional"].(bool); ok {
				sel.Optional = &opt
			}
			src.ConfigMapKeyRef = sel
		}
	}
	if fr, ok := vf["fieldRef"].(map[string]any); ok {
		ref, err := parseFieldRef(fr)
		if err != nil {
			return nil, err
		}
		src.FieldRef = ref
	}
	if rfr, ok := vf["resourceFieldRef"].(map[string]any); ok {
		ref, err := parseResourceFieldRef(rfr)
		if err != nil {
			return nil, err
		}
		src.ResourceFieldRef = ref
	}
	if fkr, ok := vf["fileKeyRef"].(map[string]any); ok {
		ref, err := parseFileKeyRef(fkr)
		if err != nil {
			return nil, err
		}
		src.FileKeyRef = ref
	}
	if src.SecretKeyRef == nil && src.ConfigMapKeyRef == nil && src.FieldRef == nil && src.ResourceFieldRef == nil && src.FileKeyRef == nil {
		return nil, errors.Errorf("invalid valueFrom: must contain a valid secretKeyRef, configMapKeyRef, fieldRef, resourceFieldRef, or fileKeyRef")
	}
	return src, nil
}

// parseFileKeyRef parses a `fileKeyRef` object into corev1.FileKeySelector.
// volumeName/path/key are all `+required` on the real corev1 type (unlike
// secretKeyRef/configMapKeyRef's parseNameKey, which silently skips on a
// missing name/key) — a partially-specified fileKeyRef cannot resolve to any
// of the other four value sources either, so it is a hard validation error.
func parseFileKeyRef(m map[string]any) (*corev1.FileKeySelector, error) {
	volumeName, _ := m["volumeName"].(string)
	path, _ := m["path"].(string)
	key, _ := m["key"].(string)
	if volumeName == "" || path == "" || key == "" {
		return nil, errors.Errorf("fileKeyRef: volumeName, path, and key are all required")
	}
	sel := &corev1.FileKeySelector{VolumeName: volumeName, Path: path, Key: key}
	if opt, ok := m["optional"].(bool); ok {
		sel.Optional = &opt
	}
	return sel, nil
}

// parseNameKey extracts the "name"/"key" string pair shared by secretKeyRef and
// configMapKeyRef. Returns ok=false when either is missing or empty (preserving
// the pre-existing behavior: no "optional" support, both fields required).
func parseNameKey(m map[string]any) (name, key string, ok bool) {
	n, _ := m["name"].(string)
	k, _ := m["key"].(string)
	if n == "" || k == "" {
		return "", "", false
	}
	return n, k, true
}

// parseFieldRef parses a `valueFrom.fieldRef` object into a corev1.ObjectFieldSelector.
func parseFieldRef(m map[string]any) (*corev1.ObjectFieldSelector, error) {
	path, _ := m["fieldPath"].(string)
	if path == "" {
		return nil, errors.Errorf("fieldRef: fieldPath is required")
	}
	ref := &corev1.ObjectFieldSelector{FieldPath: path}
	if av, ok := m["apiVersion"].(string); ok && av != "" {
		ref.APIVersion = av
	}
	return ref, nil
}

// parseResourceFieldRef parses a `valueFrom.resourceFieldRef` object into a
// corev1.ResourceFieldSelector.
func parseResourceFieldRef(m map[string]any) (*corev1.ResourceFieldSelector, error) {
	res, _ := m["resource"].(string)
	if res == "" {
		return nil, errors.Errorf("resourceFieldRef: resource is required")
	}
	ref := &corev1.ResourceFieldSelector{Resource: res}
	if cn, ok := m["containerName"].(string); ok && cn != "" {
		ref.ContainerName = cn
	}
	if dv, ok := m["divisor"].(string); ok && dv != "" {
		qty, err := resource.ParseQuantity(dv)
		if err != nil {
			return nil, errors.Errorf("resourceFieldRef: invalid divisor %q: %w", dv, err)
		}
		ref.Divisor = qty
	}
	return ref, nil
}

// parseEnvFrom parses the `envFrom` array: bulk-import a ConfigMap's or Secret's
// keys as environment variables, mirroring corev1.EnvFromSource directly (same
// structural pattern as parseEnvVarSource above).
func parseEnvFrom(props map[string]any) ([]corev1.EnvFromSource, error) {
	raw, ok := props["envFrom"].([]any)
	if !ok {
		return nil, nil
	}
	var out []corev1.EnvFromSource
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("envFrom[%d]: expected object, got %T", i, item)
		}
		_, hasConfigMap := m["configMapRef"].(map[string]any)
		_, hasSecret := m["secretRef"].(map[string]any)
		if hasConfigMap == hasSecret {
			return nil, errors.Errorf("envFrom[%d]: must specify exactly one of configMapRef or secretRef", i)
		}
		src := corev1.EnvFromSource{}
		if prefix, ok := m["prefix"].(string); ok {
			src.Prefix = prefix
		}
		if cm, ok := m["configMapRef"].(map[string]any); ok {
			name, _ := cm["name"].(string)
			if name == "" {
				return nil, errors.Errorf("envFrom[%d].configMapRef: name is required", i)
			}
			ref := &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name}}
			if opt, ok := cm["optional"].(bool); ok {
				ref.Optional = &opt
			}
			src.ConfigMapRef = ref
		}
		if sec, ok := m["secretRef"].(map[string]any); ok {
			name, _ := sec["name"].(string)
			if name == "" {
				return nil, errors.Errorf("envFrom[%d].secretRef: name is required", i)
			}
			ref := &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name}}
			if opt, ok := sec["optional"].(bool); ok {
				ref.Optional = &opt
			}
			src.SecretRef = ref
		}
		out = append(out, src)
	}
	return out, nil
}

// parseResources parses the OAM "resources" property into a
// corev1.ResourceRequirements-backed ResourceRequirements. Every key of
// requests/limits — "cpu", "memory", or any other resource name such as
// "ephemeral-storage" or an extended resource like "nvidia.com/gpu" — is
// parsed as a resource.Quantity, matching how a real
// corev1.Container.Resources.{Requests,Limits} round-trips through the
// Kubernetes API. Returns an error if any authored quantity string fails to
// parse (validation that previously happened later, at build time, in
// buildResourceRequirements/kubernetes.SetResourceRequestCPU etc.).
func parseResources(resources map[string]any) (ResourceRequirements, error) {
	var req ResourceRequirements
	if requests, ok := resources["requests"].(map[string]any); ok {
		rl, err := parseResourceList(requests)
		if err != nil {
			return ResourceRequirements{}, errors.Errorf("resources.requests: %w", err)
		}
		req.Requests = rl
	}
	if limits, ok := resources["limits"].(map[string]any); ok {
		rl, err := parseResourceList(limits)
		if err != nil {
			return ResourceRequirements{}, errors.Errorf("resources.limits: %w", err)
		}
		req.Limits = rl
	}
	return req, nil
}

// parseResourceList parses every string-valued entry of m as a
// corev1.ResourceName -> resource.Quantity pair. Returns nil (not an empty
// non-nil map) when m has no string-valued entries, so a caller comparing
// against a zero-value ResourceRequirements{} still sees an absent section as
// absent — matching applyDefaultQuantity's map-key-presence convention.
func parseResourceList(m map[string]any) (corev1.ResourceList, error) {
	var rl corev1.ResourceList
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return nil, errors.Errorf("%s: invalid quantity %q: %w", k, s, err)
		}
		if rl == nil {
			rl = corev1.ResourceList{}
		}
		rl[corev1.ResourceName(k)] = q
	}
	return rl, nil
}

func parseCommand(props map[string]any) []string {
	var command []string
	if cmd, ok := props["command"].([]any); ok {
		for _, c := range cmd {
			if s, ok := c.(string); ok {
				command = append(command, s)
			}
		}
	}
	return command
}

func parseArgs(props map[string]any) []string {
	var args []string
	if argList, ok := props["args"].([]any); ok {
		for _, a := range argList {
			if s, ok := a.(string); ok {
				args = append(args, s)
			}
		}
	}
	return args
}

func parseReplicas(props map[string]any, defaultVal int32) int32 {
	if n, ok := toInt32(props["replicas"]); ok {
		return n
	}
	return defaultVal
}

func hasExplicitReplicas(props map[string]any) bool {
	_, ok := toInt32(props["replicas"])
	return ok
}

func parseProbes(props map[string]any) (ProbeConfig, error) {
	var config ProbeConfig
	probes, ok := props["probes"].(map[string]any)
	if !ok {
		return config, nil
	}
	if r, ok := probes["readiness"].(map[string]any); ok {
		p, err := parseProbe(r)
		if err != nil {
			return config, errors.Errorf("readiness probe: %w", err)
		}
		config.Readiness = p
	}
	if l, ok := probes["liveness"].(map[string]any); ok {
		p, err := parseProbe(l)
		if err != nil {
			return config, errors.Errorf("liveness probe: %w", err)
		}
		config.Liveness = p
	}
	if s, ok := probes["startup"].(map[string]any); ok {
		p, err := parseProbe(s)
		if err != nil {
			return config, errors.Errorf("startup probe: %w", err)
		}
		config.Startup = p
	}
	return config, nil
}

func countProbeHandlers(m map[string]any) int {
	count := 0
	for _, key := range []string{"httpGet", "tcpSocket", "exec", "grpc"} {
		if _, ok := m[key].(map[string]any); ok {
			count++
		}
	}
	return count
}

func parseProbe(m map[string]any) (*corev1.Probe, error) {
	if countProbeHandlers(m) > 1 {
		return nil, errors.Errorf("probe must specify exactly one handler, but multiple were provided")
	}

	probe := &corev1.Probe{}
	hasHandler := false

	if httpGet, ok := m["httpGet"].(map[string]any); ok {
		port, err := parsePort(httpGet["port"])
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		handler := &corev1.HTTPGetAction{}
		if path, ok := httpGet["path"].(string); ok {
			handler.Path = path
		}
		handler.Port = port
		if host, ok := httpGet["host"].(string); ok && host != "" {
			handler.Host = host
		}
		if scheme, ok := httpGet["scheme"].(string); ok {
			s := corev1.URIScheme(strings.ToUpper(scheme))
			if s != corev1.URISchemeHTTP && s != corev1.URISchemeHTTPS {
				return nil, errors.Errorf("httpGet handler: unsupported scheme %q, must be HTTP or HTTPS", scheme)
			}
			handler.Scheme = s
		}
		if headers, ok := httpGet["httpHeaders"].([]any); ok {
			for _, h := range headers {
				if hm, ok := h.(map[string]any); ok {
					hname, _ := hm["name"].(string)
					value, _ := hm["value"].(string)
					if hname != "" {
						handler.HTTPHeaders = append(handler.HTTPHeaders, corev1.HTTPHeader{Name: hname, Value: value})
					}
				}
			}
		}
		probe.HTTPGet = handler
		hasHandler = true
	} else if tcpSocket, ok := m["tcpSocket"].(map[string]any); ok {
		port, err := parsePort(tcpSocket["port"])
		if err != nil {
			return nil, errors.Errorf("tcpSocket handler: %w", err)
		}
		probe.TCPSocket = &corev1.TCPSocketAction{Port: port}
		hasHandler = true
	} else if execCmd, ok := m["exec"].(map[string]any); ok {
		if cmd, ok := execCmd["command"].([]any); ok {
			var command []string
			for _, c := range cmd {
				if s, ok := c.(string); ok {
					command = append(command, s)
				}
			}
			if len(command) > 0 {
				probe.Exec = &corev1.ExecAction{Command: command}
				hasHandler = true
			}
		}
	} else if grpc, ok := m["grpc"].(map[string]any); ok {
		handler := &corev1.GRPCAction{}
		port, err := parsePort(grpc["port"])
		if err != nil {
			return nil, errors.Errorf("grpc handler: %w", err)
		}
		if port.Type == intstr.String {
			return nil, errors.Errorf("grpc handler: port must be numeric, got named port %q", port.StrVal)
		}
		handler.Port = port.IntVal
		if svc, ok := grpc["service"].(string); ok {
			handler.Service = &svc
		}
		probe.GRPC = handler
		hasHandler = true
	}

	if !hasHandler {
		return nil, nil
	}

	if v, n := m["initialDelaySeconds"]; n {
		if i, ok := toInt32(v); ok {
			probe.InitialDelaySeconds = i
		}
	}
	if v, n := m["periodSeconds"]; n {
		if i, ok := toInt32(v); ok {
			probe.PeriodSeconds = i
		}
	}
	if v, n := m["timeoutSeconds"]; n {
		if i, ok := toInt32(v); ok {
			probe.TimeoutSeconds = i
		}
	}
	if v, n := m["successThreshold"]; n {
		if i, ok := toInt32(v); ok {
			probe.SuccessThreshold = i
		}
	}
	if v, n := m["failureThreshold"]; n {
		if i, ok := toInt32(v); ok {
			probe.FailureThreshold = i
		}
	}
	if v, n := m["terminationGracePeriodSeconds"]; n {
		if i, ok := toInt64(v); ok {
			if i < 0 {
				return nil, errors.Errorf("terminationGracePeriodSeconds: must not be negative, got %d", i)
			}
			probe.TerminationGracePeriodSeconds = &i
		}
	}

	return probe, nil
}

func parsePort(v any) (intstr.IntOrString, error) {
	switch p := v.(type) {
	case float64:
		return validateNumericPort(int64(p))
	case int:
		return validateNumericPort(int64(p))
	case int32:
		return validateNumericPort(int64(p))
	case int64:
		return validateNumericPort(p)
	case string:
		if p == "" {
			return intstr.IntOrString{}, errors.Errorf("port must not be an empty string")
		}
		return intstr.FromString(p), nil
	default:
		return intstr.IntOrString{}, errors.Errorf("unsupported port type: %T", v)
	}
}

func validateNumericPort(port int64) (intstr.IntOrString, error) {
	if port < 1 || port > 65535 {
		return intstr.IntOrString{}, errors.Errorf("port %d out of valid range 1-65535", port)
	}
	return intstr.FromInt32(int32(port)), nil
}

// parseLifecycle parses the `lifecycle` object: postStart/preStop hooks run by
// the kubelet around the container's own lifecycle (not to be confused with the
// probes above, which are periodic health checks).
func parseLifecycle(props map[string]any) (*corev1.Lifecycle, error) {
	raw, ok := props["lifecycle"].(map[string]any)
	if !ok {
		return nil, nil
	}
	lc := &corev1.Lifecycle{}
	if ps, ok := raw["postStart"].(map[string]any); ok {
		h, err := parseLifecycleHandler(ps)
		if err != nil {
			return nil, errors.Errorf("lifecycle.postStart: %w", err)
		}
		lc.PostStart = h
	}
	if ps, ok := raw["preStop"].(map[string]any); ok {
		h, err := parseLifecycleHandler(ps)
		if err != nil {
			return nil, errors.Errorf("lifecycle.preStop: %w", err)
		}
		lc.PreStop = h
	}
	if lc.PostStart == nil && lc.PreStop == nil {
		return nil, nil
	}
	return lc, nil
}

// parseLifecycleHandler parses one postStart/preStop handler: exec, httpGet, or
// sleep. tcpSocket is deliberately not supported here — corev1 documents it as
// "NOT supported as a LifecycleHandler ... lifecycle hooks will fail at runtime
// when it is specified", kept on the Go type only for backward compatibility, so
// accepting it would let an OAM author write a handler that always fails.
func parseLifecycleHandler(m map[string]any) (*corev1.LifecycleHandler, error) {
	count := 0
	for _, key := range []string{"httpGet", "exec", "sleep"} {
		if _, ok := m[key].(map[string]any); ok {
			count++
		}
	}
	if count > 1 {
		return nil, errors.Errorf("must specify exactly one of httpGet, exec, or sleep")
	}

	handler := &corev1.LifecycleHandler{}
	if httpGet, ok := m["httpGet"].(map[string]any); ok {
		port, err := parsePort(httpGet["port"])
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		h := &corev1.HTTPGetAction{Port: port}
		if path, ok := httpGet["path"].(string); ok {
			h.Path = path
		}
		if host, ok := httpGet["host"].(string); ok && host != "" {
			h.Host = host
		}
		if scheme, ok := httpGet["scheme"].(string); ok {
			s := corev1.URIScheme(strings.ToUpper(scheme))
			if s != corev1.URISchemeHTTP && s != corev1.URISchemeHTTPS {
				return nil, errors.Errorf("httpGet handler: unsupported scheme %q, must be HTTP or HTTPS", scheme)
			}
			h.Scheme = s
		}
		if headers, ok := httpGet["httpHeaders"].([]any); ok {
			for _, hdr := range headers {
				if hm, ok := hdr.(map[string]any); ok {
					hname, _ := hm["name"].(string)
					value, _ := hm["value"].(string)
					if hname != "" {
						h.HTTPHeaders = append(h.HTTPHeaders, corev1.HTTPHeader{Name: hname, Value: value})
					}
				}
			}
		}
		handler.HTTPGet = h
		return handler, nil
	}
	if execCmd, ok := m["exec"].(map[string]any); ok {
		var command []string
		if cmd, ok := execCmd["command"].([]any); ok {
			for _, c := range cmd {
				if s, ok := c.(string); ok {
					command = append(command, s)
				}
			}
		}
		if len(command) == 0 {
			return nil, errors.Errorf("exec handler: command must not be empty")
		}
		handler.Exec = &corev1.ExecAction{Command: command}
		return handler, nil
	}
	if sleep, ok := m["sleep"].(map[string]any); ok {
		seconds, ok := toInt64(sleep["seconds"])
		if !ok {
			return nil, errors.Errorf("sleep handler: seconds is required and must be an integer")
		}
		handler.Sleep = &corev1.SleepAction{Seconds: seconds}
		return handler, nil
	}
	return nil, errors.Errorf("must specify exactly one of httpGet, exec, or sleep")
}

// parseSecurityContext parses the `securityContext` object into a real
// corev1.SecurityContext (same structural pattern as ProbeConfig/parseEnvVarSource
// above): runAsUser/runAsGroup/runAsNonRoot, readOnlyRootFilesystem,
// allowPrivilegeEscalation, privileged, capabilities add/drop, seccompProfile,
// seLinuxOptions, appArmorProfile, and procMount. Deliberately NOT covered:
// windowsOptions — this project targets Linux-only podman/distroless images per
// meta/CLAUDE.md ("Container runtime: podman", "Base image: distroless"); Windows
// containers are out of scope for this project entirely, not just this field.
// (procMount was deferred alongside windowsOptions in round 1 under the same
// "alpha feature" rationale; that premise was factually wrong — in the pinned
// k8s.io/api v0.36.3, corev1.ProcMountType's constants carry no +featureGate
// annotation, unlike FileKeyRef's explicit "+featureGate=EnvFiles" — i.e.
// procMount is unconditional/GA, not alpha, so round 2 adds it below.)
//
// IMPORTANT interaction: setting ANY field here makes the built container's
// SecurityContext non-nil. A downstream runtime's admission-time
// securityContextMutator (see traits/security_context.go's doc comment) only
// backfills its restricted defaults when SecurityContext is nil — so authoring
// one field here (e.g. just runAsUser) silently opts the container out of that
// backfill for every OTHER SecurityContext field too, unlike the `security-context`
// trait, which always fills a complete PSA-consistent context. Callers that want
// a safe partial override should use the `security-context` trait instead; this
// property is for raw, full-fidelity authoring. If the `security-context` trait is
// also applied to the same component, the trait's Generate()-time pass runs after
// this component's own Generate() and unconditionally overwrites
// container.SecurityContext (traits/security_context.go:190) — the trait always
// wins when both are used together.
func parseSecurityContext(props map[string]any) (*corev1.SecurityContext, error) {
	raw, ok := props["securityContext"].(map[string]any)
	if !ok {
		return nil, nil
	}
	sc := &corev1.SecurityContext{}
	set := false

	if v, n := raw["runAsUser"]; n {
		if i, ok := toInt64(v); ok {
			sc.RunAsUser = &i
			set = true
		}
	}
	if v, n := raw["runAsGroup"]; n {
		if i, ok := toInt64(v); ok {
			sc.RunAsGroup = &i
			set = true
		}
	}
	if v, ok := raw["runAsNonRoot"].(bool); ok {
		sc.RunAsNonRoot = &v
		set = true
	}
	if v, ok := raw["readOnlyRootFilesystem"].(bool); ok {
		sc.ReadOnlyRootFilesystem = &v
		set = true
	}
	if v, ok := raw["allowPrivilegeEscalation"].(bool); ok {
		sc.AllowPrivilegeEscalation = &v
		set = true
	}
	if v, ok := raw["privileged"].(bool); ok {
		sc.Privileged = &v
		set = true
	}
	if capsRaw, ok := raw["capabilities"].(map[string]any); ok {
		caps := &corev1.Capabilities{}
		if add, ok := capsRaw["add"].([]any); ok {
			for _, a := range add {
				if s, ok := a.(string); ok && s != "" {
					caps.Add = append(caps.Add, corev1.Capability(s))
				}
			}
		}
		if drop, ok := capsRaw["drop"].([]any); ok {
			for _, d := range drop {
				if s, ok := d.(string); ok && s != "" {
					caps.Drop = append(caps.Drop, corev1.Capability(s))
				}
			}
		}
		if len(caps.Add) > 0 || len(caps.Drop) > 0 {
			sc.Capabilities = caps
			set = true
		}
	}
	if spRaw, ok := raw["seccompProfile"].(map[string]any); ok {
		typ, _ := spRaw["type"].(string)
		switch corev1.SeccompProfileType(typ) {
		case corev1.SeccompProfileTypeRuntimeDefault, corev1.SeccompProfileTypeUnconfined:
			sc.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileType(typ)}
			set = true
		case corev1.SeccompProfileTypeLocalhost:
			profile, ok := spRaw["localhostProfile"].(string)
			if !ok || profile == "" {
				return nil, errors.Errorf("securityContext.seccompProfile: localhostProfile is required when type is %q", typ)
			}
			sc.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: &profile}
			set = true
		case "":
			// no type specified: nothing to apply.
		default:
			return nil, errors.Errorf("securityContext.seccompProfile: invalid type %q, must be Localhost, RuntimeDefault, or Unconfined", typ)
		}
	}
	if seRaw, ok := raw["seLinuxOptions"].(map[string]any); ok {
		se := &corev1.SELinuxOptions{}
		anySet := false
		if v, ok := seRaw["user"].(string); ok && v != "" {
			se.User = v
			anySet = true
		}
		if v, ok := seRaw["role"].(string); ok && v != "" {
			se.Role = v
			anySet = true
		}
		if v, ok := seRaw["type"].(string); ok && v != "" {
			se.Type = v
			anySet = true
		}
		if v, ok := seRaw["level"].(string); ok && v != "" {
			se.Level = v
			anySet = true
		}
		if anySet {
			sc.SELinuxOptions = se
			set = true
		}
	}
	if apRaw, ok := raw["appArmorProfile"].(map[string]any); ok {
		typ, _ := apRaw["type"].(string)
		switch corev1.AppArmorProfileType(typ) {
		case corev1.AppArmorProfileTypeRuntimeDefault, corev1.AppArmorProfileTypeUnconfined:
			sc.AppArmorProfile = &corev1.AppArmorProfile{Type: corev1.AppArmorProfileType(typ)}
			set = true
		case corev1.AppArmorProfileTypeLocalhost:
			profile, ok := apRaw["localhostProfile"].(string)
			if !ok || profile == "" {
				return nil, errors.Errorf("securityContext.appArmorProfile: localhostProfile is required when type is %q", typ)
			}
			sc.AppArmorProfile = &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeLocalhost, LocalhostProfile: &profile}
			set = true
		case "":
			// no type specified: nothing to apply.
		default:
			return nil, errors.Errorf("securityContext.appArmorProfile: invalid type %q, must be Localhost, RuntimeDefault, or Unconfined", typ)
		}
	}
	if pm, ok := raw["procMount"].(string); ok && pm != "" {
		switch corev1.ProcMountType(pm) {
		case corev1.DefaultProcMount, corev1.UnmaskedProcMount:
			t := corev1.ProcMountType(pm)
			sc.ProcMount = &t
			set = true
		default:
			return nil, errors.Errorf("securityContext.procMount: invalid value %q, must be Default or Unmasked", pm)
		}
	}

	if !set {
		return nil, nil
	}
	return sc, nil
}

func parseVolumes(props map[string]any) (ParsedVolumes, error) {
	var result ParsedVolumes
	volList, ok := props["volumes"].([]any)
	if !ok {
		return result, nil
	}
	for _, v := range volList {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		volName, _ := m["name"].(string)
		volType, _ := m["type"].(string)
		mountPath, _ := m["mountPath"].(string)
		if volName == "" || mountPath == "" {
			continue
		}
		readOnly, _ := m["readOnly"].(bool)

		switch volType {
		case "hostPath":
			path, _ := m["path"].(string)
			if path == "" {
				continue
			}
			hostPathType := corev1.HostPathUnset
			result.Volumes = append(result.Volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: path, Type: &hostPathType},
				},
			})
		case "emptyDir":
			vol := corev1.Volume{
				Name:         volName,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}
			if sizeLimit, ok := m["sizeLimit"].(string); ok && sizeLimit != "" {
				qty, err := resource.ParseQuantity(sizeLimit)
				if err != nil {
					return result, errors.Errorf("volume %q: invalid emptyDir sizeLimit %q: %w", volName, sizeLimit, err)
				}
				vol.EmptyDir.SizeLimit = &qty
			}
			result.Volumes = append(result.Volumes, vol)
		case "pvc":
			size, _ := m["size"].(string)
			if size == "" {
				continue
			}
			if _, err := resource.ParseQuantity(size); err != nil {
				return result, errors.Errorf("volume %q: invalid PVC size %q: %w", volName, size, err)
			}
			storageClass, _ := m["storageClass"].(string)
			accessModes, err := parseAccessModes(m)
			if err != nil {
				return result, errors.Errorf("volume %q: %w", volName, err)
			}
			result.Volumes = append(result.Volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: volName,
						ReadOnly:  readOnly,
					},
				},
			})
			result.PVCs = append(result.PVCs, PVCConfig{
				Name:         volName,
				Size:         size,
				StorageClass: storageClass,
				AccessModes:  accessModes,
			})
		case "configMap":
			cmName, _ := m["configMapName"].(string)
			if cmName == "" {
				continue
			}
			result.Volumes = append(result.Volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
					},
				},
			})
		case "secret":
			secretName, _ := m["secretName"].(string)
			if secretName == "" {
				continue
			}
			result.Volumes = append(result.Volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: secretName},
				},
			})
		default:
			continue
		}

		result.Mounts = append(result.Mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: mountPath,
			ReadOnly:  readOnly,
		})
	}
	return result, nil
}

var validAccessModes = map[string]bool{
	string(corev1.ReadWriteOnce):    true,
	string(corev1.ReadOnlyMany):     true,
	string(corev1.ReadWriteMany):    true,
	string(corev1.ReadWriteOncePod): true,
}

func hasNonRWXPVC(pvcs []PVCConfig) bool {
	for _, pvc := range pvcs {
		for _, mode := range pvc.AccessModes {
			if mode == string(corev1.ReadWriteOnce) || mode == string(corev1.ReadWriteOncePod) {
				return true
			}
		}
	}
	return false
}

func parseAccessModes(m map[string]any) ([]string, error) {
	if modes, ok := m["accessModes"].([]any); ok && len(modes) > 0 {
		var result []string
		for _, mode := range modes {
			if s, ok := mode.(string); ok && s != "" {
				if !validAccessModes[s] {
					return nil, errors.Errorf("invalid accessMode %q", s)
				}
				result = append(result, s)
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}
	return []string{string(corev1.ReadWriteOnce)}, nil
}

func parseInitContainers(props map[string]any) ([]InitContainerConfig, error) {
	raw, ok := props["initContainers"].([]any)
	if !ok {
		return nil, nil
	}
	var out []InitContainerConfig
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("initContainers[%d]: expected object, got %T", i, item)
		}
		ic := InitContainerConfig{}
		ic.Name, _ = m["name"].(string)
		if ic.Name == "" {
			return nil, errors.Errorf("initContainers[%d]: name is required", i)
		}
		ic.Image, _ = m["image"].(string)
		if ic.Image == "" {
			return nil, errors.Errorf("initContainers[%d] %q: image is required", i, ic.Name)
		}
		if err := ValidateImageRef(ic.Image); err != nil {
			return nil, errors.Errorf("initContainers[%d] %q: %w", i, ic.Name, err)
		}
		ic.Command = parseCommand(m)
		ic.Args = parseArgs(m)
		env, err := parseEnv(m)
		if err != nil {
			return nil, errors.Errorf("initContainers[%d] %q: %w", i, ic.Name, err)
		}
		ic.Env = env
		if resources, ok := m["resources"].(map[string]any); ok {
			r, err := parseResources(resources)
			if err != nil {
				return nil, errors.Errorf("initContainers[%d] %q: %w", i, ic.Name, err)
			}
			ic.Resources = r
		}
		mounts, err := parseVolumeMountList(m, fmt.Sprintf("initContainers[%d] %q", i, ic.Name))
		if err != nil {
			return nil, err
		}
		ic.VolumeMounts = mounts
		out = append(out, ic)
	}
	return out, nil
}

func parseSidecars(props map[string]any) ([]SidecarContainerConfig, error) {
	raw, ok := props["sidecars"].([]any)
	if !ok {
		return nil, nil
	}
	var out []SidecarContainerConfig
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("sidecars[%d]: expected object, got %T", i, item)
		}
		sc := SidecarContainerConfig{}
		sc.Name, _ = m["name"].(string)
		if sc.Name == "" {
			return nil, errors.Errorf("sidecars[%d]: name is required", i)
		}
		sc.Image, _ = m["image"].(string)
		if sc.Image == "" {
			return nil, errors.Errorf("sidecars[%d] %q: image is required", i, sc.Name)
		}
		if err := ValidateImageRef(sc.Image); err != nil {
			return nil, errors.Errorf("sidecars[%d] %q: %w", i, sc.Name, err)
		}
		sc.Command = parseCommand(m)
		sc.Args = parseArgs(m)
		env, err := parseEnv(m)
		if err != nil {
			return nil, errors.Errorf("sidecars[%d] %q: %w", i, sc.Name, err)
		}
		sc.Env = env
		if resources, ok := m["resources"].(map[string]any); ok {
			r, err := parseResources(resources)
			if err != nil {
				return nil, errors.Errorf("sidecars[%d] %q: %w", i, sc.Name, err)
			}
			sc.Resources = r
		}
		mounts, err := parseVolumeMountList(m, fmt.Sprintf("sidecars[%d] %q", i, sc.Name))
		if err != nil {
			return nil, err
		}
		sc.VolumeMounts = mounts
		if rawPorts, ok := m["ports"].([]any); ok {
			for j, rp := range rawPorts {
				pm, ok := rp.(map[string]any)
				if !ok {
					return nil, errors.Errorf("sidecars[%d] %q: ports[%d]: expected object, got %T", i, sc.Name, j, rp)
				}
				pname, _ := pm["name"].(string)
				var port int32
				if n, ok := toInt32(pm["containerPort"]); ok {
					port = n
				}
				if port == 0 {
					return nil, errors.Errorf("sidecars[%d] %q: ports[%d]: containerPort is required", i, sc.Name, j)
				}
				cp := corev1.ContainerPort{
					ContainerPort: port,
					Protocol:      corev1.ProtocolTCP,
				}
				if pname != "" {
					cp.Name = pname
				}
				if proto, ok := pm["protocol"].(string); ok && proto != "" {
					cp.Protocol = corev1.Protocol(proto)
				}
				sc.Ports = append(sc.Ports, cp)
			}
		}
		out = append(out, sc)
	}
	return out, nil
}

func parseVolumeMountList(m map[string]any, prefix string) ([]corev1.VolumeMount, error) {
	raw, ok := m["volumeMounts"].([]any)
	if !ok {
		return nil, nil
	}
	var out []corev1.VolumeMount
	for i, v := range raw {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("%s: volumeMounts[%d] expected object, got %T", prefix, i, v)
		}
		n, _ := mm["name"].(string)
		mountPath, _ := mm["mountPath"].(string)
		if n == "" || mountPath == "" {
			return nil, errors.Errorf("%s: volumeMounts[%d]: name and mountPath are required", prefix, i)
		}
		vm := corev1.VolumeMount{Name: n, MountPath: mountPath}
		if ro, ok := mm["readOnly"].(bool); ok {
			vm.ReadOnly = ro
		}
		if sp, ok := mm["subPath"].(string); ok && sp != "" {
			vm.SubPath = sp
		}
		out = append(out, vm)
	}
	return out, nil
}

func parseAffinity(props map[string]any) (AffinityConfig, error) {
	raw, ok := props["affinity"].(map[string]any)
	if !ok {
		return AffinityConfig{}, nil
	}
	cfg := AffinityConfig{
		TopologyKey:         "kubernetes.io/hostname",
		PodAntiAffinityType: "preferred",
	}
	if v, ok := raw["enablePodAntiAffinity"].(bool); ok {
		cfg.EnablePodAntiAffinity = v
	}
	if v, ok := raw["topologyKey"].(string); ok && v != "" {
		cfg.TopologyKey = v
	}
	if v, ok := raw["podAntiAffinityType"].(string); ok {
		cfg.PodAntiAffinityType = v
	}
	switch cfg.PodAntiAffinityType {
	case "preferred", "required":
	default:
		return AffinityConfig{}, errors.Errorf("invalid podAntiAffinityType %q: must be \"preferred\" or \"required\"", cfg.PodAntiAffinityType)
	}
	if ns, ok := raw["nodeSelector"].(map[string]any); ok {
		cfg.NodeSelector = stringMap(ns)
	}
	return cfg, nil
}

func parseTolerations(props map[string]any) ([]corev1.Toleration, error) {
	tolList, ok := props["tolerations"].([]any)
	if !ok {
		return nil, nil
	}
	tolerations := make([]corev1.Toleration, 0, len(tolList))
	for i, t := range tolList {
		m, ok := t.(map[string]any)
		if !ok {
			return nil, errors.Errorf("toleration[%d]: must be a mapping", i)
		}
		tol := corev1.Toleration{}

		if raw, exists := m["key"]; exists {
			keyStr, ok := raw.(string)
			if !ok {
				return nil, errors.Errorf("toleration[%d].key: must be a string, got %T", i, raw)
			}
			tol.Key = keyStr
		}

		if raw, exists := m["operator"]; exists {
			opStr, ok := raw.(string)
			if !ok {
				return nil, errors.Errorf("toleration[%d].operator: must be a string, got %T", i, raw)
			}
			switch corev1.TolerationOperator(opStr) {
			case corev1.TolerationOpExists, corev1.TolerationOpEqual:
				tol.Operator = corev1.TolerationOperator(opStr)
			default:
				return nil, errors.Errorf("toleration[%d].operator: invalid value %q, must be 'Exists' or 'Equal'", i, opStr)
			}
		} else if tol.Key == "" {
			tol.Operator = corev1.TolerationOpExists
		} else {
			tol.Operator = corev1.TolerationOpEqual
		}

		if raw, exists := m["value"]; exists {
			valStr, ok := raw.(string)
			if !ok {
				return nil, errors.Errorf("toleration[%d].value: must be a string, got %T", i, raw)
			}
			tol.Value = valStr
		}

		if raw, exists := m["effect"]; exists {
			effStr, ok := raw.(string)
			if !ok {
				return nil, errors.Errorf("toleration[%d].effect: must be a string, got %T", i, raw)
			}
			switch corev1.TaintEffect(effStr) {
			case corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute, "":
				tol.Effect = corev1.TaintEffect(effStr)
			default:
				return nil, errors.Errorf("toleration[%d].effect: invalid value %q", i, effStr)
			}
		}

		tolerations = append(tolerations, tol)
	}
	return tolerations, nil
}

func parseHistoryLimit(field string, v any) (int32, error) {
	switch n := v.(type) {
	case int:
		if n < 0 || n > math.MaxInt32 {
			return 0, errors.Errorf("%s: must be between 0 and %d, got %d", field, math.MaxInt32, n)
		}
		return int32(n), nil //nolint:gosec
	case float64:
		if n != float64(int64(n)) {
			return 0, errors.Errorf("%s: must be an integer, got %g", field, n)
		}
		if n < 0 || n > math.MaxInt32 {
			return 0, errors.Errorf("%s: must be between 0 and %d, got %g", field, math.MaxInt32, n)
		}
		return int32(n), nil
	default:
		return 0, errors.Errorf("%s: must be an integer, got %T", field, v)
	}
}

// --- Builders ---

// buildResourceRequirements returns res's corev1.ResourceRequirements with
// this package's absolute fallback defaults applied: 100m CPU request and
// 128Mi memory request when unset, and a memory limit equal to the
// (possibly just-defaulted) memory request when no memory limit was set.
// This is a distinct, lower-priority tier from the environment-policy
// defaults ApplyPolicy already applied to the main container's Resources
// before this call — init/sidecar containers have no ApplyPolicy equivalent,
// so this is their only defaulting. Requests/Limits are deep-copied before
// mutation so this never aliases the caller's maps (res is shared with the
// ResourceRequirements still held by the container's Config).
func buildResourceRequirements(res ResourceRequirements) corev1.ResourceRequirements {
	rr := res.ResourceRequirements
	rr.Requests = rr.Requests.DeepCopy()
	rr.Limits = rr.Limits.DeepCopy()
	if rr.Requests == nil {
		rr.Requests = corev1.ResourceList{}
	}
	if _, ok := rr.Requests[corev1.ResourceCPU]; !ok {
		rr.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	}
	if _, ok := rr.Requests[corev1.ResourceMemory]; !ok {
		rr.Requests[corev1.ResourceMemory] = resource.MustParse("128Mi")
	}
	if rr.Limits == nil {
		rr.Limits = corev1.ResourceList{}
	}
	if _, ok := rr.Limits[corev1.ResourceMemory]; !ok {
		rr.Limits[corev1.ResourceMemory] = rr.Requests[corev1.ResourceMemory]
	}
	return rr
}

func applyProbes(container *corev1.Container, probes ProbeConfig) {
	if probes.Readiness != nil {
		container.ReadinessProbe = probes.Readiness
	}
	if probes.Liveness != nil {
		container.LivenessProbe = probes.Liveness
	}
	if probes.Startup != nil {
		container.StartupProbe = probes.Startup
	}
}

// buildTopologySpreadConstraints returns topology spread constraints for
// Deployments with multiple replicas. Returns nil when replicas <= 1.
func buildTopologySpreadConstraints(replicas int32, selectorLabels map[string]string) []corev1.TopologySpreadConstraint {
	if replicas <= 1 {
		return nil
	}
	ls := &metav1.LabelSelector{MatchLabels: selectorLabels}
	constraints := []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     ls,
		},
	}
	if replicas >= 3 {
		constraints = append(constraints, corev1.TopologySpreadConstraint{
			MaxSkew:           1,
			TopologyKey:       "topology.kubernetes.io/zone",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     ls,
		})
	}
	return constraints
}

func buildAffinity(cfg AffinityConfig, selectorLabels map[string]string) *corev1.Affinity {
	if !cfg.EnablePodAntiAffinity && len(cfg.NodeSelector) == 0 {
		return nil
	}
	affinity := &corev1.Affinity{}
	if cfg.EnablePodAntiAffinity {
		term := corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{MatchLabels: selectorLabels},
			TopologyKey:   cfg.TopologyKey,
		}
		if cfg.PodAntiAffinityType == "required" {
			affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{term},
			}
		} else {
			affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
					{Weight: 100, PodAffinityTerm: term},
				},
			}
		}
	}
	if len(cfg.NodeSelector) > 0 {
		keys := make([]string, 0, len(cfg.NodeSelector))
		for k := range cfg.NodeSelector {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var reqs []corev1.NodeSelectorRequirement
		for _, k := range keys {
			reqs = append(reqs, corev1.NodeSelectorRequirement{
				Key:      k,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{cfg.NodeSelector[k]},
			})
		}
		affinity.NodeAffinity = &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: reqs}},
			},
		}
	}
	return affinity
}

func buildInitContainer(ic InitContainerConfig) (*corev1.Container, error) {
	container := kubernetes.CreateContainer(ic.Name, ic.Image, ic.Command, ic.Args)
	kubernetes.SetContainerResources(container, buildResourceRequirements(ic.Resources))
	for _, env := range ic.Env {
		kubernetes.AddContainerEnv(container, env)
	}
	for _, m := range ic.VolumeMounts {
		kubernetes.AddContainerVolumeMount(container, m)
	}
	return container, nil
}

func buildSidecarContainer(sc SidecarContainerConfig) (*corev1.Container, error) {
	container := kubernetes.CreateContainer(sc.Name, sc.Image, sc.Command, sc.Args)
	kubernetes.SetContainerResources(container, buildResourceRequirements(sc.Resources))
	for _, p := range sc.Ports {
		kubernetes.AddContainerPort(container, p)
	}
	for _, env := range sc.Env {
		kubernetes.AddContainerEnv(container, env)
	}
	for _, m := range sc.VolumeMounts {
		kubernetes.AddContainerVolumeMount(container, m)
	}
	return container, nil
}

// createServiceAccount creates a ServiceAccount with automountServiceAccountToken disabled
// (PSA restricted profile compliance). Clears the default annotation added by the kure builder.
func createServiceAccount(name, namespace string, labels map[string]string) *corev1.ServiceAccount {
	sa := kubernetes.CreateServiceAccount(name, namespace)
	sa.Labels = labels
	sa.Annotations = nil
	kubernetes.SetServiceAccountAutomountToken(sa, false)
	return sa
}

// VolumeClaimTemplate represents a PVC template for a StatefulSet.
type VolumeClaimTemplate struct {
	Name         string
	StorageClass string
	Size         string
	AccessModes  []string
	MountPath    string
}

// parseVolumeClaimTemplates parses volumeClaimTemplates from OAM properties.
func parseVolumeClaimTemplates(props map[string]any) ([]VolumeClaimTemplate, error) {
	vctList, ok := props["volumeClaimTemplates"].([]any)
	if !ok {
		return nil, nil
	}
	var vcts []VolumeClaimTemplate
	for _, v := range vctList {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("volumeClaimTemplates: each entry must be a mapping")
		}
		vct := VolumeClaimTemplate{}
		vct.Name, _ = m["name"].(string)
		vct.StorageClass, _ = m["storageClass"].(string)
		vct.Size, _ = m["size"].(string)
		vct.MountPath, _ = m["mountPath"].(string)
		accessModes, err := parseAccessModes(m)
		if err != nil {
			return nil, errors.Wrapf(err, "volumeClaimTemplate %q", vct.Name)
		}
		vct.AccessModes = accessModes
		if vct.Name == "" {
			return nil, errors.New("volumeClaimTemplate entry missing required field 'name'")
		}
		if vct.Size == "" {
			return nil, errors.Errorf("volumeClaimTemplate %q missing required field 'size'", vct.Name)
		}
		if vct.MountPath == "" {
			return nil, errors.Errorf("volumeClaimTemplate %q missing required field 'mountPath'", vct.Name)
		}
		if _, err := resource.ParseQuantity(vct.Size); err != nil {
			return nil, errors.Errorf("volumeClaimTemplate %q: invalid size %q: %w", vct.Name, vct.Size, err)
		}
		vcts = append(vcts, vct)
	}
	return vcts, nil
}

// BuildPVC creates a PersistentVolumeClaim from a PVCConfig.
func BuildPVC(pvc PVCConfig, namespace string, labels map[string]string) (*corev1.PersistentVolumeClaim, error) {
	qty, err := resource.ParseQuantity(pvc.Size)
	if err != nil {
		return nil, errors.Errorf("PVC %q: invalid size %q: %w", pvc.Name, pvc.Size, err)
	}

	claim := kubernetes.CreatePersistentVolumeClaim(pvc.Name, namespace)
	claim.Labels = labels
	claim.Annotations = nil
	claim.Spec.VolumeMode = nil
	kubernetes.SetPVCResources(claim, corev1.VolumeResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
	})
	for _, m := range pvc.AccessModes {
		kubernetes.AddPVCAccessMode(claim, corev1.PersistentVolumeAccessMode(m))
	}
	if pvc.StorageClass != "" {
		kubernetes.SetPVCStorageClassName(claim, pvc.StorageClass)
	}
	return claim, nil
}
