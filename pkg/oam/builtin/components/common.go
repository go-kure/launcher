package components

import (
	"fmt"
	"math"
	gopath "path"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

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
// descriptive error to wrap, which is exactly what this file's own
// parseInt32Field/parseInt64Field wrappers below now do too — a present key
// whose value toInt32/toInt64 cannot convert is a malformed-input error, not
// a silently-ignored absence (a present-but-wrong-type value was previously
// treated as though the key were absent at every call site in this file;
// that silently discarded the author's intent — e.g. a mistyped
// securityContext.runAsUser fell back to the container image's own default
// user, which may be root).
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

// decodedQuantityString converts a decoded YAML/JSON scalar into the string
// form resource.ParseQuantity expects. A bare numeric literal (`cpu: 1`,
// `memory: 0.5`) is valid Kubernetes Quantity input, not just a quoted string
// — corev1.Quantity.UnmarshalJSON only strips surrounding quotes *if
// present* and otherwise parses the raw literal directly, so `1` and `"1"`
// are equivalent on the wire. Suffixed forms ("500m", "2Gi") only ever arrive
// as strings; a bare number is always unsuffixed, so integer/decimal
// formatting (not scientific notation) is always the right rendering.
// (Named distinctly from enforce.go's quantityString, which formats an
// *already-parsed* Quantity out of a ResourceList — opposite direction.)
func decodedQuantityString(v any) (string, bool) {
	switch n := v.(type) {
	case string:
		return n, true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", false
		}
		return strconv.FormatFloat(n, 'f', -1, 64), true
	case int:
		return strconv.FormatInt(int64(n), 10), true
	case int32:
		return strconv.FormatInt(int64(n), 10), true
	case int64:
		return strconv.FormatInt(n, 10), true
	default:
		return "", false
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
	skr, hasSecret, err := parseObjectField(vf, "secretKeyRef", "valueFrom.secretKeyRef")
	if err != nil {
		return nil, err
	}
	cmr, hasConfigMap, err := parseObjectField(vf, "configMapKeyRef", "valueFrom.configMapKeyRef")
	if err != nil {
		return nil, err
	}
	fr, hasFieldRef, err := parseObjectField(vf, "fieldRef", "valueFrom.fieldRef")
	if err != nil {
		return nil, err
	}
	rfr, hasResourceFieldRef, err := parseObjectField(vf, "resourceFieldRef", "valueFrom.resourceFieldRef")
	if err != nil {
		return nil, err
	}
	fkr, hasFileKeyRef, err := parseObjectField(vf, "fileKeyRef", "valueFrom.fileKeyRef")
	if err != nil {
		return nil, err
	}
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
	if hasSecret {
		if n, key, ok := parseNameKey(skr); ok {
			sel := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: n}, Key: key}
			opt, err := parseBoolField(skr, "optional", "secretKeyRef.optional")
			if err != nil {
				return nil, err
			}
			sel.Optional = opt
			src.SecretKeyRef = sel
		}
	}
	if hasConfigMap {
		if n, key, ok := parseNameKey(cmr); ok {
			sel := &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: n}, Key: key}
			opt, err := parseBoolField(cmr, "optional", "configMapKeyRef.optional")
			if err != nil {
				return nil, err
			}
			sel.Optional = opt
			src.ConfigMapKeyRef = sel
		}
	}
	if hasFieldRef {
		ref, err := parseFieldRef(fr)
		if err != nil {
			return nil, err
		}
		src.FieldRef = ref
	}
	if hasResourceFieldRef {
		ref, err := parseResourceFieldRef(rfr)
		if err != nil {
			return nil, err
		}
		src.ResourceFieldRef = ref
	}
	if hasFileKeyRef {
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
// validateRelativePath rejects an absolute path or one containing a ".."
// backstep component, per AGENTS.md's "reject paths that escape the working
// directory" convention. label identifies the field in the returned error.
func validateRelativePath(label, path string) error {
	if gopath.IsAbs(path) {
		return errors.Errorf("%s: path must be relative, got %q", label, path)
	}
	for _, elem := range strings.Split(path, "/") {
		if elem == ".." {
			return errors.Errorf("%s: path must not contain \"..\", got %q", label, path)
		}
	}
	return nil
}

// NOTE on a deliberately deferred cross-check: this function validates
// volumeName's own shape (a DNS-1123 label) but never checks it against the
// component's actual `volumes` list, because parseEnv (and therefore this
// function, reached through parseValueFrom) runs before parseVolumes in
// every one of its 7 call sites (5 kind handlers plus parseInitContainers
// and parseSidecars) and has no access to the parsed volume set. A shallow
// name-only cross-check would also be incomplete on its own: real
// FileKeySelector semantics need the referenced volume to be an Image
// volume (corev1.VolumeSource.Image) specifically, and this schema's
// parseVolumes supports only hostPath/emptyDir/pvc/configMap/secret — no
// "image" case exists yet, so no fileKeyRef.volumeName can ever resolve to a
// conformant target today regardless of a name match. Closing this gap
// needs Image-volume schema support first, then threading the resulting
// volume-name set through parseEnv's call chain (or a post-hoc validation
// pass once env and volumes are both parsed) — out of scope for this
// shared-schema-fidelity PR; see the launcher#278 ledger.
func parseFileKeyRef(m map[string]any) (*corev1.FileKeySelector, error) {
	volumeName, _ := m["volumeName"].(string)
	path, _ := m["path"].(string)
	key, _ := m["key"].(string)
	if volumeName == "" || path == "" || key == "" {
		return nil, errors.Errorf("fileKeyRef: volumeName, path, and key are all required")
	}
	// Mirrors real admission's validateFileKeySelector exactly: volumeName
	// must be a valid DNS-1123 label (it names a pod volume, which has that
	// same name constraint), key must be a valid (relaxed) env var name, and
	// path must not contain a ".." backstep — this file's own
	// validateRelativePath is stricter still (also rejects an absolute path,
	// an existing project-level policy per AGENTS.md, not a real-admission
	// requirement).
	if errs := validation.IsDNS1123Label(volumeName); len(errs) > 0 {
		return nil, errors.Errorf("fileKeyRef.volumeName: invalid volume name %q: %s", volumeName, strings.Join(errs, "; "))
	}
	if errs := validation.IsRelaxedEnvVarName(key); len(errs) > 0 {
		return nil, errors.Errorf("fileKeyRef.key: invalid key %q: %s", key, strings.Join(errs, "; "))
	}
	if err := validateRelativePath("fileKeyRef.path", path); err != nil {
		return nil, err
	}
	sel := &corev1.FileKeySelector{VolumeName: volumeName, Path: path, Key: key}
	opt, err := parseBoolField(m, "optional", "fileKeyRef.optional")
	if err != nil {
		return nil, err
	}
	sel.Optional = opt
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

// validEnvFieldPaths is the exact set of non-subscripted field paths real
// Kubernetes admission accepts for a container env var fieldRef (mirrors
// validEnvDownwardAPIFieldPathExpressions,
// k8s.io/kubernetes/pkg/apis/core/validation/validation.go) — a strict
// subset of what the downward API *volume* form accepts: status.phase,
// spec.restartPolicy, spec.schedulerName, and bare metadata.labels/
// metadata.annotations (without a subscript) are volume-only and rejected
// here.
var validEnvFieldPaths = map[string]bool{
	"metadata.name":           true,
	"metadata.namespace":      true,
	"metadata.uid":            true,
	"spec.nodeName":           true,
	"spec.serviceAccountName": true,
	"status.hostIP":           true,
	"status.hostIPs":          true,
	"status.podIP":            true,
	"status.podIPs":           true,
}

// splitSubscriptedFieldPath mirrors k8s.io/kubernetes/pkg/fieldpath's
// SplitMaybeSubscriptedPath byte-for-byte: a fieldPath of the form
// "base['key']" splits into ("base", "key", true); anything else returns
// (fieldPath, "", false). Not reused from upstream because that package is
// internal to k8s.io/kubernetes, which this module does not otherwise
// depend on.
func splitSubscriptedFieldPath(path string) (base, key string, ok bool) {
	if !strings.HasSuffix(path, "']") {
		return path, "", false
	}
	trimmed := strings.TrimSuffix(path, "']")
	parts := strings.SplitN(trimmed, "['", 2)
	if len(parts) < 2 || parts[0] == "" {
		return path, "", false
	}
	return parts[0], parts[1], true
}

// validateFieldPath applies the same two-stage rule real Kubernetes
// admission uses for a container env var fieldRef: either an exact match in
// validEnvFieldPaths, or a metadata.labels['KEY']/metadata.annotations['KEY']
// subscript whose key is a valid qualified name (an annotation key is
// matched case-insensitively, mirroring validateObjectFieldSelector's own
// strings.ToLower(subscript) call — labels are not lowercased).
func validateFieldPath(path string) error {
	if base, key, ok := splitSubscriptedFieldPath(path); ok {
		switch base {
		case "metadata.annotations":
			if errs := validation.IsQualifiedName(strings.ToLower(key)); len(errs) > 0 {
				return errors.Errorf("fieldRef: invalid annotation key %q in fieldPath %q: %s", key, path, strings.Join(errs, "; "))
			}
			return nil
		case "metadata.labels":
			if errs := validation.IsQualifiedName(key); len(errs) > 0 {
				return errors.Errorf("fieldRef: invalid label key %q in fieldPath %q: %s", key, path, strings.Join(errs, "; "))
			}
			return nil
		default:
			return errors.Errorf("fieldRef: fieldPath %q does not support a subscript", path)
		}
	}
	if !validEnvFieldPaths[path] {
		return errors.Errorf("fieldRef: unsupported fieldPath %q", path)
	}
	return nil
}

// parseFieldRef parses a `valueFrom.fieldRef` object into a corev1.ObjectFieldSelector.
func parseFieldRef(m map[string]any) (*corev1.ObjectFieldSelector, error) {
	path, _ := m["fieldPath"].(string)
	if path == "" {
		return nil, errors.Errorf("fieldRef: fieldPath is required")
	}
	if err := validateFieldPath(path); err != nil {
		return nil, err
	}
	ref := &corev1.ObjectFieldSelector{FieldPath: path}
	if av, ok := m["apiVersion"].(string); ok && av != "" {
		// corev1.ObjectFieldSelector.APIVersion "defaults to v1" (field doc
		// comment) because v1 is the only field-label conversion Kubernetes has
		// ever shipped for the downward API; any other value builds but is
		// rejected by admission.
		if av != "v1" {
			return nil, errors.Errorf("fieldRef: apiVersion must be \"v1\", got %q", av)
		}
		ref.APIVersion = av
	}
	return ref, nil
}

// validResourceFieldSelectors is the exact set of resourceFieldRef.resource
// values real Kubernetes admission accepts, combined with a
// requests.hugepages-*/limits.hugepages-* prefix family (mirrors
// validContainerResourceFieldPathExpressions +
// validContainerResourceFieldPathPrefixesWithDownwardAPIHugePages,
// k8s.io/kubernetes/pkg/apis/core/validation/validation.go). Unlike envFrom's
// object-name fields, this is NOT open to arbitrary extended resources —
// "limits.nvidia.com/gpu" builds but is rejected by admission; the downward
// API can only project these four resource families.
var validResourceFieldSelectors = map[string]bool{
	"limits.cpu": true, "limits.memory": true, "limits.ephemeral-storage": true,
	"requests.cpu": true, "requests.memory": true, "requests.ephemeral-storage": true,
}

// validDownwardAPIDivisorCPU and validDownwardAPIDivisor are the canonical
// divisor strings real Kubernetes admission accepts for a resourceFieldRef,
// by resource family (mirrors validContainerResourceDivisorForCPU and
// validContainerResourceDivisorFor{Memory,EphemeralStorage,HugePages} —
// memory/ephemeral-storage/hugepages all share one 13-value set). A divisor
// is checked against these only when it is non-zero: an unset or
// zero-valued divisor (e.g. authored as "0") is treated as absent, exactly
// as validateContainerResourceDivisor's own Cmp-to-the-zero-value early
// return does — real admission does NOT reject a zero divisor the way a
// naive "must be non-zero" check would.
var validDownwardAPIDivisorCPU = map[string]bool{"1m": true, "1": true}
var validDownwardAPIDivisor = map[string]bool{
	"1":  true,
	"1k": true, "1M": true, "1G": true, "1T": true, "1P": true, "1E": true,
	"1Ki": true, "1Mi": true, "1Gi": true, "1Ti": true, "1Pi": true, "1Ei": true,
}

// parseResourceFieldRef parses a `valueFrom.resourceFieldRef` object into a
// corev1.ResourceFieldSelector.
func parseResourceFieldRef(m map[string]any) (*corev1.ResourceFieldSelector, error) {
	res, _ := m["resource"].(string)
	if res == "" {
		return nil, errors.Errorf("resourceFieldRef: resource is required")
	}
	if !validResourceFieldSelectors[res] && !strings.HasPrefix(res, "requests.hugepages-") && !strings.HasPrefix(res, "limits.hugepages-") {
		return nil, errors.Errorf("resourceFieldRef: unsupported resource %q (must be one of limits.cpu, limits.memory, limits.ephemeral-storage, requests.cpu, requests.memory, requests.ephemeral-storage, or a requests.hugepages-<size>/limits.hugepages-<size> selector)", res)
	}
	ref := &corev1.ResourceFieldSelector{Resource: res}
	if cn, present, err := parseStringField(m, "containerName", "resourceFieldRef.containerName"); err != nil {
		return nil, err
	} else if present {
		// Syntax only: a container name must be a DNS-1123 label
		// (ValidateDNS1123Label, used for every corev1.Container.Name).
		// Whether cn actually names a container that exists in this pod is
		// deliberately NOT checked here — that needs the full set of sibling
		// container/initContainer/sidecar names, which no caller in this
		// package threads through to a single env var's parsing (parseEnv is
		// invoked per-component, before sidecars/initContainers are even
		// parsed); real admission doesn't check it either (only the
		// volume-projection form of resourceFieldRef requires containerName
		// at all — validateContainerResourceFieldSelector's ContainerName
		// check is gated on `volume`, which is false for an env var), so an
		// unresolvable target only surfaces later, as a kubelet-time
		// CreateContainerConfigError when the downward API can't find the
		// named container.
		if errs := validation.IsDNS1123Label(cn); len(errs) > 0 {
			return nil, errors.Errorf("resourceFieldRef.containerName: invalid container name %q: %s", cn, strings.Join(errs, "; "))
		}
		ref.ContainerName = cn
	}
	if dv, present, err := parseStringField(m, "divisor", "resourceFieldRef.divisor"); err != nil {
		return nil, err
	} else if present {
		qty, err := resource.ParseQuantity(dv)
		if err != nil {
			return nil, errors.Errorf("resourceFieldRef: invalid divisor %q: %w", dv, err)
		}
		if qty.Sign() != 0 {
			allowed := validDownwardAPIDivisor
			if res == "limits.cpu" || res == "requests.cpu" {
				allowed = validDownwardAPIDivisorCPU
			}
			if !allowed[qty.String()] {
				return nil, errors.Errorf("resourceFieldRef: divisor %q is not a supported unit for resource %q", dv, res)
			}
		}
		ref.Divisor = qty
	}
	return ref, nil
}

// parseEnvFrom parses the `envFrom` array: bulk-import a ConfigMap's or Secret's
// keys as environment variables, mirroring corev1.EnvFromSource directly (same
// structural pattern as parseEnvVarSource above).
func parseEnvFrom(props map[string]any) ([]corev1.EnvFromSource, error) {
	v, present := props["envFrom"]
	if !present {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.Errorf("envFrom: must be an array, got %T", v)
	}
	var out []corev1.EnvFromSource
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("envFrom[%d]: expected object, got %T", i, item)
		}
		cm, hasConfigMap, err := parseObjectField(m, "configMapRef", fmt.Sprintf("envFrom[%d].configMapRef", i))
		if err != nil {
			return nil, err
		}
		sec, hasSecret, err := parseObjectField(m, "secretRef", fmt.Sprintf("envFrom[%d].secretRef", i))
		if err != nil {
			return nil, err
		}
		if hasConfigMap == hasSecret {
			return nil, errors.Errorf("envFrom[%d]: must specify exactly one of configMapRef or secretRef", i)
		}
		src := corev1.EnvFromSource{}
		if prefix, present, err := parseStringField(m, "prefix", fmt.Sprintf("envFrom[%d].prefix", i)); err != nil {
			return nil, err
		} else if present {
			// corev1.EnvFromSource.Prefix's field doc comment: "May consist of
			// any printable ASCII characters except '='" — not a C-identifier
			// restriction (the final env var name is prefix+key, and only that
			// concatenation need be a valid identifier; the prefix alone does
			// not).
			for _, r := range prefix {
				if r == '=' || r < 0x20 || r > 0x7e {
					return nil, errors.Errorf("envFrom[%d].prefix: must consist of printable ASCII characters other than '=', got %q", i, prefix)
				}
			}
			src.Prefix = prefix
		}
		if hasConfigMap {
			name, _ := cm["name"].(string)
			if name == "" {
				return nil, errors.Errorf("envFrom[%d].configMapRef: name is required", i)
			}
			// Matches ValidateConfigMapName (= apimachineryvalidation.NameIsDNSSubdomain):
			// every Kubernetes object name, ConfigMap included, must be a valid
			// DNS-1123 subdomain.
			if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
				return nil, errors.Errorf("envFrom[%d].configMapRef.name: invalid name %q: %s", i, name, strings.Join(errs, "; "))
			}
			ref := &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name}}
			opt, err := parseBoolField(cm, "optional", fmt.Sprintf("envFrom[%d].configMapRef.optional", i))
			if err != nil {
				return nil, err
			}
			ref.Optional = opt
			src.ConfigMapRef = ref
		}
		if hasSecret {
			name, _ := sec["name"].(string)
			if name == "" {
				return nil, errors.Errorf("envFrom[%d].secretRef: name is required", i)
			}
			// Matches ValidateSecretName (= apimachineryvalidation.NameIsDNSSubdomain):
			// every Kubernetes object name, Secret included, must be a valid
			// DNS-1123 subdomain.
			if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
				return nil, errors.Errorf("envFrom[%d].secretRef.name: invalid name %q: %s", i, name, strings.Join(errs, "; "))
			}
			ref := &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: name}}
			opt, err := parseBoolField(sec, "optional", fmt.Sprintf("envFrom[%d].secretRef.optional", i))
			if err != nil {
				return nil, err
			}
			ref.Optional = opt
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
//
// Deliberately NOT covered: corev1.ResourceRequirements.Claims (Dynamic
// Resource Allocation) — see schemaResources' doc comment for the rationale
// (genuinely feature-gated in the pinned k8s.io/api version, and meaningless
// without pod-level PodSpec.ResourceClaims wiring this component doesn't have).
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
	if err := validateResourceRequestLimit(req.Requests, req.Limits); err != nil {
		return ResourceRequirements{}, err
	}
	return req, nil
}

// validateResourceRequestLimit applies the request/limit cross-check real
// Kubernetes admission enforces for every resource name (mirrors
// validateResourceRequirements' Requests-side loop,
// k8s.io/kubernetes/pkg/apis/core/validation/validation.go), split by
// IsOvercommitAllowed:
//
// For a resource IsOvercommitAllowed disallows — every hugepages-<size>
// resource, and every extended resource — when both a request and a limit
// are present they must be exactly equal, and a request alone is rejected
// outright ("Limit must be set for non overcommitable resources") since —
// unlike cpu/memory, which get a request defaulted from a limit — nothing
// defaults a missing limit from a request. Any resource name not in
// standardContainerResourceNames (cpu/memory/ephemeral-storage) is exactly
// this non-overcommitable set once validateContainerResourceName has already
// run on every key (called from parseResourceList before this), so the two
// are: a hugepages-<size> name, or a "/"-qualified extended resource.
//
// For a standardContainerResourceNames entry (cpu/memory/ephemeral-storage,
// where IsOvercommitAllowed is true), a request may be lower than its limit —
// that's the point of overcommit — but not higher: real admission still
// rejects request > limit for these. Either may also be absent independently
// of the other; only the non-overcommitable set above requires both.
//
// A limit set alone, with no matching request, is deliberately NOT rejected
// here for either case: the real apiserver's defaulter copies limit into
// request before validation ever runs, so a limit-only author input is
// admission-valid and this parser has no matching case to reject.
func validateResourceRequestLimit(requests, limits corev1.ResourceList) error {
	for name, reqQty := range requests {
		limQty, ok := limits[name]
		if standardContainerResourceNames[name] {
			if ok && reqQty.Cmp(limQty) > 0 {
				return errors.Errorf("resources: %s: request %s must not exceed limit %s", name, reqQty.String(), limQty.String())
			}
			continue
		}
		if !ok {
			return errors.Errorf("resources: %s: limit must be set when request is set (extended and hugepages resources cannot be overcommitted)", name)
		}
		if reqQty.Cmp(limQty) != 0 {
			return errors.Errorf("resources: %s: request %s must equal limit %s (extended and hugepages resources cannot be overcommitted)", name, reqQty.String(), limQty.String())
		}
	}
	return nil
}

// standardContainerResourceNames is the fixed set of unqualified (no "/")
// resource names a container may request/limit directly, beyond the
// hugepages-<size> family (mirrors standardContainerResources,
// k8s.io/kubernetes/pkg/apis/core/helper/helpers.go — the container-specific
// subset of IsStandardResourceName's broader, quota-only set, which also
// allows unqualified names like "pods" that make no sense on a container).
var standardContainerResourceNames = map[corev1.ResourceName]bool{
	corev1.ResourceCPU:              true,
	corev1.ResourceMemory:           true,
	corev1.ResourceEphemeralStorage: true,
}

// validateContainerResourceName applies the same rule real Kubernetes
// admission uses for a corev1.Container.Resources key (mirrors
// ValidateContainerResourceName, k8s.io/kubernetes/pkg/apis/core/validation):
// an unqualified name (no "/") must be one of the three standard container
// resources or a hugepages-<size> name — validation.IsQualifiedName alone
// accepts any unqualified token (e.g. "foo"), which Kubernetes actually
// reserves for its own native resources and rejects from a workload author.
// A qualified name (has "/") is rejected as an extended resource if it
// either claims to be native by containing "kubernetes.io/" (mirrors
// IsNativeResource) or is formatted as a "requests.<name>" quota alias
// (mirrors IsExtendedResourceName's requests.-prefix rejection); both
// conditions are checked independently, not jointly.
func validateContainerResourceName(k string) error {
	if !strings.Contains(k, "/") {
		if standardContainerResourceNames[corev1.ResourceName(k)] || isHugePageResourceName(corev1.ResourceName(k)) {
			return nil
		}
		return errors.Errorf("%s: must be a standard container resource (cpu, memory, ephemeral-storage), a hugepages-<size> name, or a fully qualified extended resource name (e.g. \"example.com/foo\")", k)
	}
	if strings.Contains(k, "kubernetes.io/") {
		return errors.Errorf("%s: extended resource name must not contain %q", k, "kubernetes.io/")
	}
	if strings.HasPrefix(k, "requests.") {
		return errors.Errorf("%s: extended resource name must not start with %q", k, "requests.")
	}
	return nil
}

// isHugePageResourceName reports whether name is a "hugepages-<size>"
// resource name (mirrors helper.IsHugePageResourceName).
func isHugePageResourceName(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), "hugepages-")
}

// validateHugePageQuantity checks that q is an integer multiple of the page
// size encoded in name's "hugepages-<size>" suffix (mirrors
// helper.IsHugePageResourceValueDivisible) — Kubernetes rejects a hugepages
// request/limit that is merely a whole number of bytes if it is not also a
// whole number of that specific page size (e.g. hugepages-2Mi: 3Mi is a
// whole number of bytes but not a multiple of the 2Mi page size).
func validateHugePageQuantity(name corev1.ResourceName, q resource.Quantity) error {
	pageSize, err := resource.ParseQuantity(strings.TrimPrefix(string(name), "hugepages-"))
	if err != nil || pageSize.Sign() <= 0 || pageSize.MilliValue()%1000 != 0 {
		return errors.Errorf("%s: invalid hugepage resource name", name)
	}
	if q.Value()%pageSize.Value() != 0 {
		return errors.Errorf("%s: quantity %s must be an integer multiple of the page size %s", name, q.String(), pageSize.String())
	}
	return nil
}

// parseResourceList parses every string-valued entry of m as a
// corev1.ResourceName -> resource.Quantity pair. Returns nil (not an empty
// non-nil map) when m has no string-valued entries, so a caller comparing
// against a zero-value ResourceRequirements{} still sees an absent section as
// absent — matching applyDefaultQuantity's map-key-presence convention.
func parseResourceList(m map[string]any) (corev1.ResourceList, error) {
	var rl corev1.ResourceList
	for k, v := range m {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, errors.Errorf("%s: invalid resource name: %s", k, strings.Join(errs, "; "))
		}
		if err := validateContainerResourceName(k); err != nil {
			return nil, err
		}
		s, ok := decodedQuantityString(v)
		if !ok {
			return nil, errors.Errorf("%s: quantity must be a string or number, got %T", k, v)
		}
		q, err := resource.ParseQuantity(s)
		if err != nil {
			return nil, errors.Errorf("%s: invalid quantity %q: %w", k, s, err)
		}
		if q.Sign() < 0 {
			return nil, errors.Errorf("%s: quantity must not be negative, got %q", k, s)
		}
		if !isFractionalResourceName(corev1.ResourceName(k)) && q.MilliValue()%1000 != 0 {
			return nil, errors.Errorf("%s: extended resource quantities must be whole numbers, got %q", k, s)
		}
		if isHugePageResourceName(corev1.ResourceName(k)) {
			if err := validateHugePageQuantity(corev1.ResourceName(k), q); err != nil {
				return nil, err
			}
		}
		if rl == nil {
			rl = corev1.ResourceList{}
		}
		rl[corev1.ResourceName(k)] = q
	}
	return rl, nil
}

// isFractionalResourceName reports whether name is one of the four standard
// resource names Kubernetes allows a fractional quantity for. Every other
// resource name — an extended resource such as "nvidia.com/gpu", or a
// hugepages- entry, which is always a whole multiple of its page size — must
// be a whole number.
func isFractionalResourceName(name corev1.ResourceName) bool {
	switch name {
	case corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceStorage, corev1.ResourceEphemeralStorage:
		return true
	default:
		return false
	}
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

// namedPortsAllowed is false for a component kind whose main container never
// declares any port (worker, cronjob): the kubelet resolves a named httpGet/
// tcpSocket port by looking it up in that same container's own declared
// Ports, so a string port can never resolve there and is rejected outright
// rather than authoring a probe/lifecycle hook that is guaranteed to fail at
// runtime. Kinds that do declare a main-container port pass the port's own
// presence (e.g. `c.Port > 0`) through here instead of a blanket true/false,
// since the port is itself optional on some of those kinds (daemonset,
// statefulset) — see each ToApplicationConfig call site. matchName is the
// exact `ports[].name` the kind's builder actually declares (e.g. "http",
// "tcp"); when namedPortsAllowed is true a named port must equal matchName,
// since the kubelet resolves it only against a name the container itself
// declares — a syntactically valid but undeclared name (e.g. "metrics" on a
// component whose only declared port is "http") would build successfully but
// fail to resolve at runtime. matchName is ignored when namedPortsAllowed is
// false, and passed "" by the grpc handler (see parseProbe), which rejects
// every named port regardless of name with its own message.
func parseProbes(props map[string]any, namedPortsAllowed bool, matchName string) (ProbeConfig, error) {
	var config ProbeConfig
	probes, ok := props["probes"].(map[string]any)
	if !ok {
		return config, nil
	}
	if r, ok := probes["readiness"].(map[string]any); ok {
		p, err := parseProbe(r, "readiness", namedPortsAllowed, matchName)
		if err != nil {
			return config, errors.Errorf("readiness probe: %w", err)
		}
		config.Readiness = p
	}
	if l, ok := probes["liveness"].(map[string]any); ok {
		p, err := parseProbe(l, "liveness", namedPortsAllowed, matchName)
		if err != nil {
			return config, errors.Errorf("liveness probe: %w", err)
		}
		config.Liveness = p
	}
	if s, ok := probes["startup"].(map[string]any); ok {
		p, err := parseProbe(s, "startup", namedPortsAllowed, matchName)
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

// kind is "readiness", "liveness", or "startup" — needed only to enforce
// terminationGracePeriodSeconds' cross-field constraint (see below); every
// other field's validation is identical across probe kinds. namedPortsAllowed
// and matchName are documented on parseProbes above.
func parseProbe(m map[string]any, kind string, namedPortsAllowed bool, matchName string) (*corev1.Probe, error) {
	if countProbeHandlers(m) > 1 {
		return nil, errors.Errorf("probe must specify exactly one handler, but multiple were provided")
	}

	probe := &corev1.Probe{}
	hasHandler := false

	if httpGet, ok := m["httpGet"].(map[string]any); ok {
		port, err := parsePort(httpGet["port"], namedPortsAllowed, matchName)
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		handler := &corev1.HTTPGetAction{}
		// path is optional: real Kubernetes defaults an empty
		// HTTPGetAction.Path to "/" before validation ever sees it
		// (k8s.io/kubernetes/pkg/apis/core/v1/defaults.go's
		// SetDefaults_HTTPGetAction, wired for both probes and lifecycle
		// hooks by zz_generated.defaults.go), so omitting it here matches
		// upstream exactly rather than rejecting a manifest a real cluster
		// would accept.
		if path, present, err := parseStringField(httpGet, "path", "httpGet.path"); err != nil {
			return nil, err
		} else if present {
			handler.Path = path
		}
		handler.Port = port
		// host has no format validation in real admission (validateHTTPGetAction
		// only rejects a non-empty host when protocol is HTTP2) — a mistyped
		// value is still rejected, but an unusual-looking host string is not.
		if host, present, err := parseStringField(httpGet, "host", "httpGet.host"); err != nil {
			return nil, err
		} else if present {
			handler.Host = host
		}
		if scheme, present, err := parseStringField(httpGet, "scheme", "httpGet.scheme"); err != nil {
			return nil, err
		} else if present {
			s := corev1.URIScheme(strings.ToUpper(scheme))
			if s != corev1.URISchemeHTTP && s != corev1.URISchemeHTTPS {
				return nil, errors.Errorf("httpGet handler: unsupported scheme %q, must be HTTP or HTTPS", scheme)
			}
			handler.Scheme = s
		}
		headers, err := parseHTTPHeaders(httpGet, "httpHeaders")
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		handler.HTTPHeaders = headers
		probe.HTTPGet = handler
		hasHandler = true
	} else if tcpSocket, ok := m["tcpSocket"].(map[string]any); ok {
		port, err := parsePort(tcpSocket["port"], namedPortsAllowed, matchName)
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
		// grpc's port is always numeric regardless of namedPortsAllowed — the
		// check right below rejects a named port unconditionally, with its own
		// message, so this always passes true/"" (any syntactically valid
		// name) to reach that check rather than being intercepted earlier by
		// parsePort's own named-port rejection or its matchName filter.
		port, err := parsePort(grpc["port"], true, "")
		if err != nil {
			return nil, errors.Errorf("grpc handler: %w", err)
		}
		if port.Type == intstr.String {
			return nil, errors.Errorf("grpc handler: port must be numeric, got named port %q", port.StrVal)
		}
		handler.Port = port.IntVal
		if svc, ok := grpc["service"].(string); ok {
			// Mirrors validateGRPCService (k8s.io/kubernetes/pkg/apis/core/validation):
			// the gRPC health-checking service name is not DNS-1123 formatted, but
			// admission still caps it at maxGRPCServiceNameLength (63) — an unbounded
			// string here builds successfully but is rejected at probe admission.
			if len(svc) > 63 {
				return nil, errors.Errorf("grpc handler: service name must be no more than 63 characters, got %d", len(svc))
			}
			handler.Service = &svc
		}
		probe.GRPC = handler
		hasHandler = true
	}

	if !hasHandler {
		return nil, nil
	}

	if i, present, err := parseInt32Field(m, "initialDelaySeconds", "initialDelaySeconds"); err != nil {
		return nil, err
	} else if present {
		if i < 0 {
			return nil, errors.Errorf("initialDelaySeconds: must not be negative, got %d", i)
		}
		probe.InitialDelaySeconds = i
	}
	if i, present, err := parseInt32Field(m, "periodSeconds", "periodSeconds"); err != nil {
		return nil, err
	} else if present {
		if i < 1 {
			return nil, errors.Errorf("periodSeconds: must be at least 1, got %d", i)
		}
		probe.PeriodSeconds = i
	}
	if i, present, err := parseInt32Field(m, "timeoutSeconds", "timeoutSeconds"); err != nil {
		return nil, err
	} else if present {
		if i < 1 {
			return nil, errors.Errorf("timeoutSeconds: must be at least 1, got %d", i)
		}
		probe.TimeoutSeconds = i
	}
	if i, present, err := parseInt32Field(m, "successThreshold", "successThreshold"); err != nil {
		return nil, err
	} else if present {
		if i < 1 {
			return nil, errors.Errorf("successThreshold: must be at least 1, got %d", i)
		}
		// Real Kubernetes admission (validateLivenessProbe/validateStartupProbe)
		// rejects anything but 1 here: a liveness/startup probe's only two
		// outcomes are "still healthy" and "restart", so requiring more than
		// one consecutive success to reset that state has no defined meaning.
		// Only readiness probes may set this above 1.
		if i != 1 && kind != "readiness" {
			return nil, errors.Errorf("successThreshold: must be 1 for a %s probe, got %d", kind, i)
		}
		probe.SuccessThreshold = i
	}
	if i, present, err := parseInt32Field(m, "failureThreshold", "failureThreshold"); err != nil {
		return nil, err
	} else if present {
		if i < 1 {
			return nil, errors.Errorf("failureThreshold: must be at least 1, got %d", i)
		}
		probe.FailureThreshold = i
	}
	if i, present, err := parseInt64Field(m, "terminationGracePeriodSeconds", "terminationGracePeriodSeconds"); err != nil {
		return nil, err
	} else if present {
		// Cross-field constraint, so it's checked ahead of the bounds check:
		// a failed readiness probe never terminates anything (it only marks
		// the pod not-ready and pulls it from Service endpoints), so this
		// field has nothing to apply to on a readiness probe — only
		// liveness and startup probe failures kill the container.
		if kind == "readiness" {
			return nil, errors.Errorf("terminationGracePeriodSeconds: not permitted on a readiness probe, only liveness and startup probes support it")
		}
		if i < 1 {
			return nil, errors.Errorf("terminationGracePeriodSeconds: must be at least 1, got %d", i)
		}
		probe.TerminationGracePeriodSeconds = &i
	}

	return probe, nil
}

// parseHTTPHeaders parses an httpGet handler's `httpHeaders` array (shared by
// both the probe and lifecycle httpGet handlers) into []corev1.HTTPHeader,
// rejecting a malformed entry instead of silently dropping or coercing it: a
// non-object entry, a missing/empty/invalid name (matching
// validateHTTPGetAction's own validation.IsHTTPHeaderName(name) check), or a
// present-but-non-string value would otherwise fail a failed type assertion
// silently — e.g. {name: Authorization, value: 123} previously became
// {Name: "Authorization", Value: ""}, silently corrupting the authored
// header instead of surfacing the author's mistake. A missing value key
// (as opposed to one present with the wrong type) still defaults to "",
// since corev1.HTTPHeader.Value has no meaningful zero-value distinction
// from an intentionally-empty header value.
func parseHTTPHeaders(raw map[string]any, key string) ([]corev1.HTTPHeader, error) {
	v, present := raw[key]
	if !present {
		return nil, nil
	}
	headers, ok := v.([]any)
	if !ok {
		return nil, errors.Errorf("%s: must be an array, got %T", key, v)
	}
	var out []corev1.HTTPHeader
	for i, h := range headers {
		hm, ok := h.(map[string]any)
		if !ok {
			return nil, errors.Errorf("httpHeaders[%d]: must be an object with name and value", i)
		}
		name, ok := hm["name"].(string)
		if !ok || name == "" {
			return nil, errors.Errorf("httpHeaders[%d].name: must be a non-empty string", i)
		}
		if errs := validation.IsHTTPHeaderName(name); len(errs) > 0 {
			return nil, errors.Errorf("httpHeaders[%d].name: invalid header name %q: %s", i, name, strings.Join(errs, "; "))
		}
		value := ""
		if v, present := hm["value"]; present {
			s, ok := v.(string)
			if !ok {
				return nil, errors.Errorf("httpHeaders[%d].value: must be a string, got %T", i, v)
			}
			value = s
		}
		out = append(out, corev1.HTTPHeader{Name: name, Value: value})
	}
	return out, nil
}

// namedPortsAllowed rejects the string form outright before the name-syntax
// check below: the kubelet resolves a named port only against that same
// container's own declared Ports, so on a component kind whose main
// container never declares any port (worker, cronjob — see parseProbes),
// a string port is guaranteed unresolvable at runtime, not just possibly
// wrong. matchName, when non-empty, further restricts an allowed named port
// to that exact declared name — a syntactically valid but different name
// (e.g. "metrics" where the container's only declared port is "http") is
// just as unresolvable at runtime as a named port on a portless component,
// so it is rejected the same way. matchName is ignored when namedPortsAllowed
// is false, and "" plus namedPortsAllowed=true means "any syntactically
// valid name" (used only by the grpc handler, which rejects every named
// port afterward regardless of name with its own message).
func parsePort(v any, namedPortsAllowed bool, matchName string) (intstr.IntOrString, error) {
	switch p := v.(type) {
	case float64:
		if math.IsNaN(p) || math.IsInf(p, 0) || p != math.Trunc(p) {
			return intstr.IntOrString{}, errors.Errorf("port must be an integer, got %v", p)
		}
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
		if !namedPortsAllowed {
			return intstr.IntOrString{}, errors.Errorf("named port %q is not supported here: this component declares no container ports for the kubelet to resolve the name against — use a numeric port instead", p)
		}
		// A string port names a container's declared `ports[].name`, not an
		// arbitrary label — real admission (ValidatePortNumOrName) checks it
		// with validation.IsValidPortName, which is far stricter than
		// "nonempty": lowercase [-a-z0-9] only, at least one letter, no
		// leading/trailing/adjacent hyphen, max 15 chars. Applies uniformly to
		// every parsePort call site (probe httpGet/tcpSocket/grpc, lifecycle
		// httpGet), matching ValidatePortNumOrName's own single shared use
		// across all of them upstream.
		if errs := validation.IsValidPortName(p); len(errs) > 0 {
			return intstr.IntOrString{}, errors.Errorf("invalid port name %q: %s", p, strings.Join(errs, "; "))
		}
		if matchName != "" && p != matchName {
			return intstr.IntOrString{}, errors.Errorf("named port %q does not match this component's declared container port %q: the kubelet resolves a named port only against a name the container itself declares", p, matchName)
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
// probes above, which are periodic health checks). namedPortsAllowed and
// matchName are documented on parseProbes above — the same "resolves only
// against this container's own declared Ports, under this exact name"
// reasoning applies identically here.
func parseLifecycle(props map[string]any, namedPortsAllowed bool, matchName string) (*corev1.Lifecycle, error) {
	v, present := props["lifecycle"]
	if !present {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, errors.Errorf("lifecycle: must be an object, got %T", v)
	}
	lc := &corev1.Lifecycle{}
	if v, present := raw["postStart"]; present {
		ps, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("lifecycle.postStart: must be an object, got %T", v)
		}
		h, err := parseLifecycleHandler(ps, namedPortsAllowed, matchName)
		if err != nil {
			return nil, errors.Errorf("lifecycle.postStart: %w", err)
		}
		lc.PostStart = h
	}
	if v, present := raw["preStop"]; present {
		ps, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("lifecycle.preStop: must be an object, got %T", v)
		}
		h, err := parseLifecycleHandler(ps, namedPortsAllowed, matchName)
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
func parseLifecycleHandler(m map[string]any, namedPortsAllowed bool, matchName string) (*corev1.LifecycleHandler, error) {
	// Rejected unconditionally, not folded into the count below: the count
	// only tracks the three keys this parser actually builds a handler from,
	// so a hook that pairs tcpSocket with e.g. a valid exec previously slipped
	// through as "single handler" and silently built the exec handler alone,
	// dropping the disallowed tcpSocket instead of rejecting the hook.
	if _, ok := m["tcpSocket"].(map[string]any); ok {
		return nil, errors.Errorf("tcpSocket is not supported as a lifecycle handler")
	}
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
		port, err := parsePort(httpGet["port"], namedPortsAllowed, matchName)
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		h := &corev1.HTTPGetAction{Port: port}
		// Same upstream-defaulting rationale as parseProbe's httpGet block
		// above: path/host/scheme are all optional, but a present-and-wrong-
		// type value is still a malformed-input error.
		if path, present, err := parseStringField(httpGet, "path", "httpGet.path"); err != nil {
			return nil, err
		} else if present {
			h.Path = path
		}
		if host, present, err := parseStringField(httpGet, "host", "httpGet.host"); err != nil {
			return nil, err
		} else if present {
			h.Host = host
		}
		if scheme, present, err := parseStringField(httpGet, "scheme", "httpGet.scheme"); err != nil {
			return nil, err
		} else if present {
			s := corev1.URIScheme(strings.ToUpper(scheme))
			if s != corev1.URISchemeHTTP && s != corev1.URISchemeHTTPS {
				return nil, errors.Errorf("httpGet handler: unsupported scheme %q, must be HTTP or HTTPS", scheme)
			}
			h.Scheme = s
		}
		headers, err := parseHTTPHeaders(httpGet, "httpHeaders")
		if err != nil {
			return nil, errors.Errorf("httpGet handler: %w", err)
		}
		h.HTTPHeaders = headers
		handler.HTTPGet = h
		return handler, nil
	}
	if execCmd, ok := m["exec"].(map[string]any); ok {
		var command []string
		if cmd, ok := execCmd["command"].([]any); ok {
			for i, c := range cmd {
				s, ok := c.(string)
				if !ok {
					return nil, errors.Errorf("exec handler: command[%d] must be a string, got %T", i, c)
				}
				command = append(command, s)
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
		if seconds < 0 {
			return nil, errors.Errorf("sleep handler: seconds must not be negative, got %d", seconds)
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
// parseBoolField extracts an optional bool from raw[key], erroring if key is
// present with a non-bool value rather than silently skipping it — a mistyped
// value (e.g. a quoted `"false"`) must not silently fall back to whatever
// default applies when the field is left unset while looking like the
// authored value was honored. label is the dotted field path used in the
// error message, kept separate from key so nested callers (e.g. envFrom[i])
// can report a fully-qualified path.
func parseBoolField(raw map[string]any, key, label string) (*bool, error) {
	v, present := raw[key]
	if !present {
		return nil, nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil, errors.Errorf("%s: must be a boolean, got %T", label, v)
	}
	return &b, nil
}

// parseInt32Field mirrors parseBoolField for toInt32-convertible fields.
func parseInt32Field(raw map[string]any, key, label string) (int32, bool, error) {
	v, present := raw[key]
	if !present {
		return 0, false, nil
	}
	i, ok := toInt32(v)
	if !ok {
		return 0, false, errors.Errorf("%s: must be an integer, got %T", label, v)
	}
	return i, true, nil
}

// parseInt64Field mirrors parseBoolField for toInt64-convertible fields.
func parseInt64Field(raw map[string]any, key, label string) (int64, bool, error) {
	v, present := raw[key]
	if !present {
		return 0, false, nil
	}
	i, ok := toInt64(v)
	if !ok {
		return 0, false, errors.Errorf("%s: must be an integer, got %T", label, v)
	}
	return i, true, nil
}

// parseStringField mirrors parseBoolField for optional string fields, erroring
// on a present-but-non-string value. An explicitly empty string is treated the
// same as absent (ok=false, no error) — several callers (e.g. seLinuxOptions'
// four sub-fields) already used "present, non-empty" as their notion of "set"
// before this helper existed, and an author-supplied "" is a reasonable way to
// opt back out rather than a malformed value.
func parseStringField(raw map[string]any, key, label string) (string, bool, error) {
	v, present := raw[key]
	if !present {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, errors.Errorf("%s: must be a string, got %T", label, v)
	}
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}

// parseObjectField mirrors parseStringField for object-typed fields — used
// where a bare `v.(map[string]any), ok` type assertion would silently treat
// a present-but-wrong-type value the same as absent.
func parseObjectField(raw map[string]any, key, label string) (map[string]any, bool, error) {
	v, present := raw[key]
	if !present {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false, errors.Errorf("%s: must be an object, got %T", label, v)
	}
	return m, true, nil
}

// parseCapabilityList parses a `capabilities.add`/`capabilities.drop` array:
// present-but-non-array is an error, same present/absent/wrong-type contract
// as parseBoolField, as is any non-string element. An empty-string element is
// silently skipped rather than rejected — real admission places no format
// constraint on a Capability string at all (a bare `type Capability string`,
// no dedicated validation function in k8s.io/kubernetes's validation
// package), so this file does not invent one for a merely-empty entry.
func parseCapabilityList(raw map[string]any, key, label string) ([]corev1.Capability, error) {
	v, present := raw[key]
	if !present {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.Errorf("%s: must be an array, got %T", label, v)
	}
	var out []corev1.Capability
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, errors.Errorf("%s[%d]: must be a string, got %T", label, i, item)
		}
		if s != "" {
			out = append(out, corev1.Capability(s))
		}
	}
	return out, nil
}

func parseSecurityContext(props map[string]any) (*corev1.SecurityContext, error) {
	v, present := props["securityContext"]
	if !present {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, errors.Errorf("securityContext: must be an object, got %T", v)
	}
	sc := &corev1.SecurityContext{}
	set := false

	if i, present, err := parseInt64Field(raw, "runAsUser", "securityContext.runAsUser"); err != nil {
		return nil, err
	} else if present {
		if i < 0 {
			return nil, errors.Errorf("securityContext.runAsUser: must not be negative, got %d", i)
		}
		sc.RunAsUser = &i
		set = true
	}
	if i, present, err := parseInt64Field(raw, "runAsGroup", "securityContext.runAsGroup"); err != nil {
		return nil, err
	} else if present {
		if i < 0 {
			return nil, errors.Errorf("securityContext.runAsGroup: must not be negative, got %d", i)
		}
		sc.RunAsGroup = &i
		set = true
	}
	if v, err := parseBoolField(raw, "runAsNonRoot", "securityContext.runAsNonRoot"); err != nil {
		return nil, err
	} else if v != nil {
		sc.RunAsNonRoot = v
		set = true
	}
	// A container authored with both an explicit root UID and runAsNonRoot
	// builds fine and is admitted by the API server, but always fails at
	// container-start time: the kubelet's verifyRunAsNonRoot check
	// (pkg/kubelet/kuberuntime/security_context.go) deterministically rejects
	// it as "container's runAsUser breaks non-root policy", a
	// CreateContainerConfigError every time, not a possible-failure condition —
	// so it is caught here instead of shipping a workload guaranteed never to
	// start.
	if sc.RunAsUser != nil && *sc.RunAsUser == 0 && sc.RunAsNonRoot != nil && *sc.RunAsNonRoot {
		return nil, errors.Errorf("securityContext: runAsUser must not be 0 when runAsNonRoot is true")
	}
	if v, err := parseBoolField(raw, "readOnlyRootFilesystem", "securityContext.readOnlyRootFilesystem"); err != nil {
		return nil, err
	} else if v != nil {
		sc.ReadOnlyRootFilesystem = v
		set = true
	}
	if v, err := parseBoolField(raw, "allowPrivilegeEscalation", "securityContext.allowPrivilegeEscalation"); err != nil {
		return nil, err
	} else if v != nil {
		sc.AllowPrivilegeEscalation = v
		set = true
	}
	if v, err := parseBoolField(raw, "privileged", "securityContext.privileged"); err != nil {
		return nil, err
	} else if v != nil {
		sc.Privileged = v
		set = true
	}
	if v, present := raw["capabilities"]; present {
		capsRaw, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("securityContext.capabilities: must be an object, got %T", v)
		}
		caps := &corev1.Capabilities{}
		add, err := parseCapabilityList(capsRaw, "add", "securityContext.capabilities.add")
		if err != nil {
			return nil, err
		}
		caps.Add = add
		drop, err := parseCapabilityList(capsRaw, "drop", "securityContext.capabilities.drop")
		if err != nil {
			return nil, err
		}
		caps.Drop = drop
		if len(caps.Add) > 0 || len(caps.Drop) > 0 {
			sc.Capabilities = caps
			set = true
		}
	}
	if v, present := raw["seccompProfile"]; present {
		spRaw, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("securityContext.seccompProfile: must be an object, got %T", v)
		}
		typ, _ := spRaw["type"].(string)
		switch corev1.SeccompProfileType(typ) {
		case corev1.SeccompProfileTypeRuntimeDefault, corev1.SeccompProfileTypeUnconfined:
			if profile, ok := spRaw["localhostProfile"].(string); ok && profile != "" {
				return nil, errors.Errorf("securityContext.seccompProfile: localhostProfile is only valid when type is Localhost, got type %q", typ)
			}
			sc.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileType(typ)}
			set = true
		case corev1.SeccompProfileTypeLocalhost:
			profile, ok := spRaw["localhostProfile"].(string)
			if !ok || profile == "" {
				return nil, errors.Errorf("securityContext.seccompProfile: localhostProfile is required when type is %q", typ)
			}
			// corev1.SeccompProfile.LocalhostProfile's field doc comment: "Must be
			// a descending path, relative to the kubelet's configured seccomp
			// profile location."
			if err := validateRelativePath("securityContext.seccompProfile.localhostProfile", profile); err != nil {
				return nil, err
			}
			sc.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: &profile}
			set = true
		case "":
			// Real admission's validateSeccompProfileType returns
			// field.Required "type is required when seccompProfile is set" —
			// an empty type is only reachable here because the author wrote a
			// seccompProfile object at all (the outer `if spRaw, ok := ...`
			// already gated on that), so treating it as a silent no-op would
			// drop an authored profile (e.g. a localhostProfile with a typo'd
			// or missing type key) without telling the author their intent
			// was discarded.
			return nil, errors.Errorf("securityContext.seccompProfile: type is required when seccompProfile is set")
		default:
			return nil, errors.Errorf("securityContext.seccompProfile: invalid type %q, must be Localhost, RuntimeDefault, or Unconfined", typ)
		}
	}
	if v, present := raw["seLinuxOptions"]; present {
		seRaw, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("securityContext.seLinuxOptions: must be an object, got %T", v)
		}
		se := &corev1.SELinuxOptions{}
		anySet := false
		if v, present, err := parseStringField(seRaw, "user", "securityContext.seLinuxOptions.user"); err != nil {
			return nil, err
		} else if present {
			se.User = v
			anySet = true
		}
		if v, present, err := parseStringField(seRaw, "role", "securityContext.seLinuxOptions.role"); err != nil {
			return nil, err
		} else if present {
			se.Role = v
			anySet = true
		}
		if v, present, err := parseStringField(seRaw, "type", "securityContext.seLinuxOptions.type"); err != nil {
			return nil, err
		} else if present {
			se.Type = v
			anySet = true
		}
		if v, present, err := parseStringField(seRaw, "level", "securityContext.seLinuxOptions.level"); err != nil {
			return nil, err
		} else if present {
			se.Level = v
			anySet = true
		}
		if anySet {
			sc.SELinuxOptions = se
			set = true
		}
	}
	if v, present := raw["appArmorProfile"]; present {
		apRaw, ok := v.(map[string]any)
		if !ok {
			return nil, errors.Errorf("securityContext.appArmorProfile: must be an object, got %T", v)
		}
		typ, _ := apRaw["type"].(string)
		switch corev1.AppArmorProfileType(typ) {
		case corev1.AppArmorProfileTypeRuntimeDefault, corev1.AppArmorProfileTypeUnconfined:
			if profile, ok := apRaw["localhostProfile"].(string); ok && profile != "" {
				return nil, errors.Errorf("securityContext.appArmorProfile: localhostProfile is only valid when type is Localhost, got type %q", typ)
			}
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
			// Mirrors ValidateAppArmorProfileField's own field.Required "type
			// is required when appArmorProfile is set" — same reasoning as
			// the seccompProfile case above.
			return nil, errors.Errorf("securityContext.appArmorProfile: type is required when appArmorProfile is set")
		default:
			return nil, errors.Errorf("securityContext.appArmorProfile: invalid type %q, must be Localhost, RuntimeDefault, or Unconfined", typ)
		}
	}
	if pm, present, err := parseStringField(raw, "procMount", "securityContext.procMount"); err != nil {
		return nil, err
	} else if present {
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
		// Every corev1.Volume.Name, regardless of source type, must be a
		// valid DNS-1123 label (mirrors validateVolumes' shared name check,
		// k8s.io/kubernetes/pkg/apis/core/validation) — an invalid name
		// (e.g. containing "/") builds successfully but is rejected at Pod
		// admission.
		if errs := validation.IsDNS1123Label(volName); len(errs) > 0 {
			return result, errors.Errorf("volume: invalid name %q: %s", volName, strings.Join(errs, "; "))
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
