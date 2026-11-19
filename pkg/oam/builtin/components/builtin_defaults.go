package components

import (
	"fmt"
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// BuiltinDefaults of the five handler-built workload kinds (deployment,
// statefulset, daemonset, job, cronjob): the values launcher's own parser filled
// because the author wrote none (go-kure/launcher#1580). The parsers record the
// paths they filled, and each method answers from that record and the config's
// state when the transform asks, so:
//
//   - reading changes nothing, and reading twice, or after ApplyPolicy ran twice,
//     gives the same answer; the first ApplyPolicy may remove a path from the
//     report, because the policy then reports a default there;
//   - a path is reported by one source only: where ApplyPolicy reported a policy
//     default at the path, the value that stands is the policy's and the built-in
//     fill is not reported (oam.BuiltinDefaultReporter);
//   - a fill that no longer happens is not reported: under a scaler the
//     HorizontalPodAutoscaler owns the replica count, and the kind is built
//     without one.
//
// A config built without a parse, by a test or a caller of its own, has recorded
// no parse-time fill and reports none; it still reports the fills made at
// generation (builtin_defaults_generate.go), which it reads from its own state.

var (
	_ oam.BuiltinDefaultReporter = (*RoleDeploymentConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*StatefulsetConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*DaemonsetConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*JobConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*CronjobConfig)(nil)
)

// builtinRef names a fill by its kind and its path, as the generation-time fills
// do: "deployment.ports[0].protocol".
func builtinRef(kind, path string) string {
	return kind + "." + path
}

// builtinReport turns the paths a config filled into its report, leaving out each
// path the policy reported a default at and each path twice.
func builtinReport(kind string, paths []string, policy []oam.PolicyDefault) []oam.BuiltinDefault {
	var out []oam.BuiltinDefault
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] || slices.ContainsFunc(policy, func(d oam.PolicyDefault) bool { return d.Path == p }) {
			continue
		}
		seen[p] = true
		out = append(out, oam.BuiltinDefault{Path: p, Ref: builtinRef(kind, p)})
	}
	return out
}

// containerFills are the paths the pod's init containers and sidecars had filled,
// from the entry's own record to the path the author would have written.
func containerFills(inits []InitContainerConfig, sidecars []SidecarContainerConfig) []string {
	var paths []string
	for i, ic := range inits {
		for _, p := range ic.portFills {
			paths = append(paths, fmt.Sprintf("initContainers[%d].%s", i, p))
		}
	}
	for i, sc := range sidecars {
		for _, p := range sc.portFills {
			paths = append(paths, fmt.Sprintf("sidecars[%d].%s", i, p))
		}
	}
	return paths
}

// claimFills are the `volumes[i].accessModes` paths whose ReadWriteOnce the parser
// filled. Only a pvc volume that references an existing claim reaches a pod kind
// (parsePodVolumes refuses one that would generate a claim).
func claimFills(pvcs []PVCConfig) []string {
	var paths []string
	for _, pvc := range pvcs {
		if pvc.accessModesFilled {
			paths = append(paths, fmt.Sprintf("volumes[%d].accessModes", pvc.volumeIndex))
		}
	}
	return paths
}

// withoutReplicasUnderScaler drops the `replicas` fill of a kind whose count the
// HorizontalPodAutoscaler owns: it is built without spec.replicas, so no fill
// happens.
func withoutReplicasUnderScaler(paths []string, scaled bool) []string {
	if !scaled {
		return paths
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return p == "replicas" })
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter.
func (c *RoleDeploymentConfig) BuiltinDefaults() []oam.BuiltinDefault {
	paths := withoutReplicasUnderScaler(c.parseFills, c.scaled)
	paths = append(slices.Clone(paths), containerFills(c.InitContainers, c.Sidecars)...)
	paths = append(paths, claimFills(c.PVCs)...)
	if nonRWXFillsStrategy(c.PVCs, c.DeploymentSpec.Strategy) {
		paths = append(paths, "strategy.type")
	}
	paths = append(paths, generateFillPaths(c.Resources, c.InitContainers, c.Sidecars, c.PodSpec)...)
	return builtinReport("deployment", paths, c.PolicyDefaults())
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter.
func (c *StatefulsetConfig) BuiltinDefaults() []oam.BuiltinDefault {
	paths := withoutReplicasUnderScaler(c.parseFills, c.scaled)
	paths = append(slices.Clone(paths), containerFills(c.InitContainers, c.Sidecars)...)
	paths = append(paths, claimFills(c.PVCs)...)
	for _, vct := range c.VolumeClaimTemplates {
		if vct.accessModesFilled {
			paths = append(paths, fmt.Sprintf("volumeClaimTemplates[%d].accessModes", vct.index))
		}
	}
	paths = append(paths, generateFillPaths(c.Resources, c.InitContainers, c.Sidecars, c.PodSpec)...)
	return builtinReport("statefulset", paths, c.PolicyDefaults())
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter.
func (c *DaemonsetConfig) BuiltinDefaults() []oam.BuiltinDefault {
	paths := append(slices.Clone(c.parseFills), containerFills(c.InitContainers, c.Sidecars)...)
	paths = append(paths, claimFills(c.PVCs)...)
	paths = append(paths, generateFillPaths(c.Resources, c.InitContainers, c.Sidecars, c.PodSpec)...)
	return builtinReport("daemonset", paths, c.PolicyDefaults())
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter.
func (c *JobConfig) BuiltinDefaults() []oam.BuiltinDefault {
	paths := append(slices.Clone(c.parseFills), containerFills(c.InitContainers, nil)...)
	paths = append(paths, claimFills(c.PVCs)...)
	paths = append(paths, generateFillPaths(c.Resources, c.InitContainers, nil, c.PodSpec)...)
	return builtinReport("job", paths, c.PolicyDefaults())
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter.
func (c *CronjobConfig) BuiltinDefaults() []oam.BuiltinDefault {
	paths := append(slices.Clone(c.parseFills), containerFills(c.InitContainers, nil)...)
	paths = append(paths, claimFills(c.PVCs)...)
	paths = append(paths, generateFillPaths(c.Resources, c.InitContainers, nil, c.PodSpec)...)
	return builtinReport("cronjob", paths, c.PolicyDefaults())
}
