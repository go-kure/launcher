package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// The values a workload kind fills when it generates its object are reported
// as built-in defaults (go-kure/launcher#1580): the cpu and memory requests and
// the memory limit buildResourceRequirements adds to a container, and the
// token automount buildPodSpec switches off. Nothing records them: the
// BuiltinDefaults of each kind (builtin_defaults.go) answers from the config as it stands, with the
// functions generation itself decides by, so the report and the generated
// object cannot disagree. A value the author wrote or a policy default filled
// is in the config, and is therefore never among them.

// tokenAutomountIsBuiltin reports whether buildPodSpec switches the token
// automount off for ps: no service account is named and no automount is set.
func tokenAutomountIsBuiltin(ps corev1.PodSpec) bool {
	return ps.ServiceAccountName == "" && ps.AutomountServiceAccountToken == nil
}

// generateFillPaths returns the paths of the generate-time fills of a workload:
// those of the main container at its own path, those of each init container
// and sidecar with "initContainers[<i>]." or "sidecars[<i>]." in front (the
// paths applyContainerPolicyDefaults reports under), then the pod's token
// automount. A kind without sidecars passes nil.
func generateFillPaths(main ResourceRequirements, inits []InitContainerConfig,
	sidecars []SidecarContainerConfig, pod PodSpecConfig) []string {
	var paths []string
	add := func(res ResourceRequirements, prefix string) {
		for _, f := range builtinResourceFills(res) {
			paths = append(paths, prefix+f.path())
		}
	}
	add(main, "")
	for i := range inits {
		add(inits[i].Resources, fmt.Sprintf("initContainers[%d].", i))
	}
	for i := range sidecars {
		add(sidecars[i].Resources, fmt.Sprintf("sidecars[%d].", i))
	}
	if tokenAutomountIsBuiltin(pod.PodSpec) {
		paths = append(paths, "automountServiceAccountToken")
	}
	return paths
}
