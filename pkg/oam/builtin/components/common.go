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

// EnvVar represents an environment variable.
type EnvVar struct {
	Name      string
	Value     string
	ValueFrom *EnvVarSource
}

// EnvVarSource represents a source for the value of an EnvVar.
type EnvVarSource struct {
	SecretKeyRef    *KeySelector
	ConfigMapKeyRef *KeySelector
}

// KeySelector selects a key from a ConfigMap or Secret.
type KeySelector struct {
	Name string
	Key  string
}

// ResourceRequirements represents CPU/memory requirements.
type ResourceRequirements struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// explicitResourceFlags tracks which resource fields were explicitly set in OAM.
type explicitResourceFlags struct {
	cpuRequest    bool
	memoryRequest bool
	cpuLimit      bool
	memoryLimit   bool
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
	Env          []EnvVar
	Resources    ResourceRequirements
	VolumeMounts []corev1.VolumeMount
}

// SidecarContainerConfig holds the parsed OAM fields for a sidecar container.
type SidecarContainerConfig struct {
	Name         string
	Image        string
	Command      []string
	Args         []string
	Env          []EnvVar
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

func parseEnv(props map[string]any) ([]EnvVar, error) {
	var envVars []EnvVar
	if envList, ok := props["env"].([]any); ok {
		for _, e := range envList {
			if envMap, ok := e.(map[string]any); ok {
				envName, _ := envMap["name"].(string)
				if envName == "" {
					continue
				}
				ev := EnvVar{Name: envName}
				if vf, ok := envMap["valueFrom"].(map[string]any); ok {
					src, err := parseEnvVarSource(vf)
					if err != nil {
						return nil, errors.Errorf("env %q: %w", envName, err)
					}
					ev.ValueFrom = src
				} else {
					ev.Value, _ = envMap["value"].(string)
				}
				envVars = append(envVars, ev)
			}
		}
	}
	return envVars, nil
}

func parseEnvVarSource(vf map[string]any) (*EnvVarSource, error) {
	src := &EnvVarSource{}
	_, hasSecret := vf["secretKeyRef"].(map[string]any)
	_, hasConfigMap := vf["configMapKeyRef"].(map[string]any)
	if hasSecret && hasConfigMap {
		return nil, errors.Errorf("valueFrom: secretKeyRef and configMapKeyRef are mutually exclusive")
	}
	if skr, ok := vf["secretKeyRef"].(map[string]any); ok {
		src.SecretKeyRef = parseKeySelector(skr)
	}
	if cmr, ok := vf["configMapKeyRef"].(map[string]any); ok {
		src.ConfigMapKeyRef = parseKeySelector(cmr)
	}
	if src.SecretKeyRef == nil && src.ConfigMapKeyRef == nil {
		return nil, errors.Errorf("invalid valueFrom: must contain a valid secretKeyRef or configMapKeyRef with both name and key")
	}
	return src, nil
}

func parseKeySelector(m map[string]any) *KeySelector {
	n, _ := m["name"].(string)
	key, _ := m["key"].(string)
	if n == "" || key == "" {
		return nil
	}
	return &KeySelector{Name: n, Key: key}
}

func parseResources(resources map[string]any) ResourceRequirements {
	var req ResourceRequirements
	if requests, ok := resources["requests"].(map[string]any); ok {
		if cpu, ok := requests["cpu"].(string); ok {
			req.CPURequest = cpu
		}
		if memory, ok := requests["memory"].(string); ok {
			req.MemoryRequest = memory
		}
	}
	if limits, ok := resources["limits"].(map[string]any); ok {
		if cpu, ok := limits["cpu"].(string); ok {
			req.CPULimit = cpu
		}
		if memory, ok := limits["memory"].(string); ok {
			req.MemoryLimit = memory
		}
	}
	return req
}

func resourceExplicitFlags(props map[string]any) explicitResourceFlags {
	var flags explicitResourceFlags
	resources, ok := props["resources"].(map[string]any)
	if !ok {
		return flags
	}
	if requests, ok := resources["requests"].(map[string]any); ok {
		_, flags.cpuRequest = requests["cpu"].(string)
		_, flags.memoryRequest = requests["memory"].(string)
	}
	if limits, ok := resources["limits"].(map[string]any); ok {
		_, flags.cpuLimit = limits["cpu"].(string)
		_, flags.memoryLimit = limits["memory"].(string)
	}
	return flags
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
			ic.Resources = parseResources(resources)
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
			sc.Resources = parseResources(resources)
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

func buildEnvVars(envs []EnvVar) []corev1.EnvVar {
	var result []corev1.EnvVar
	for _, env := range envs {
		k8sEnv := corev1.EnvVar{Name: env.Name}
		if env.ValueFrom != nil {
			k8sEnv.ValueFrom = &corev1.EnvVarSource{}
			if env.ValueFrom.SecretKeyRef != nil {
				k8sEnv.ValueFrom.SecretKeyRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: env.ValueFrom.SecretKeyRef.Name},
					Key:                  env.ValueFrom.SecretKeyRef.Key,
				}
			}
			if env.ValueFrom.ConfigMapKeyRef != nil {
				k8sEnv.ValueFrom.ConfigMapKeyRef = &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: env.ValueFrom.ConfigMapKeyRef.Name},
					Key:                  env.ValueFrom.ConfigMapKeyRef.Key,
				}
			}
		} else {
			k8sEnv.Value = env.Value
		}
		result = append(result, k8sEnv)
	}
	return result
}

func buildResourceRequirements(res ResourceRequirements) (corev1.ResourceRequirements, error) {
	rr := kubernetes.CreateResourceRequirements()

	cpuRequest := res.CPURequest
	if cpuRequest == "" {
		cpuRequest = "100m"
	}
	memoryRequest := res.MemoryRequest
	if memoryRequest == "" {
		memoryRequest = "128Mi"
	}

	if err := kubernetes.SetResourceRequestCPU(rr, cpuRequest); err != nil {
		return corev1.ResourceRequirements{}, err
	}
	if err := kubernetes.SetResourceRequestMemory(rr, memoryRequest); err != nil {
		return corev1.ResourceRequirements{}, err
	}
	if res.CPULimit != "" {
		if err := kubernetes.SetResourceLimitCPU(rr, res.CPULimit); err != nil {
			return corev1.ResourceRequirements{}, err
		}
	}
	memoryLimit := res.MemoryLimit
	if memoryLimit == "" {
		memoryLimit = memoryRequest
	}
	if err := kubernetes.SetResourceLimitMemory(rr, memoryLimit); err != nil {
		return corev1.ResourceRequirements{}, err
	}

	return *rr, nil
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
	rr, err := buildResourceRequirements(ic.Resources)
	if err != nil {
		return nil, errors.Errorf("init container %q resources: %w", ic.Name, err)
	}
	kubernetes.SetContainerResources(container, rr)
	for _, env := range buildEnvVars(ic.Env) {
		kubernetes.AddContainerEnv(container, env)
	}
	for _, m := range ic.VolumeMounts {
		kubernetes.AddContainerVolumeMount(container, m)
	}
	return container, nil
}

func buildSidecarContainer(sc SidecarContainerConfig) (*corev1.Container, error) {
	container := kubernetes.CreateContainer(sc.Name, sc.Image, sc.Command, sc.Args)
	rr, err := buildResourceRequirements(sc.Resources)
	if err != nil {
		return nil, errors.Errorf("sidecar container %q resources: %w", sc.Name, err)
	}
	kubernetes.SetContainerResources(container, rr)
	for _, p := range sc.Ports {
		kubernetes.AddContainerPort(container, p)
	}
	for _, env := range buildEnvVars(sc.Env) {
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
