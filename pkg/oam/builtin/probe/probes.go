package probe

import (
	"strings"

	"github.com/go-kure/launcher/pkg/oam"
)

const image = "ghcr.io/org/app:v1"

func component(name, typ string, props map[string]any) []oam.Component {
	p := map[string]any{"image": image}
	for k, v := range props {
		p[k] = v
	}
	return []oam.Component{{Name: name, Type: typ, Properties: p}}
}

// resourceFills are the generation-time fills of one container: the requests,
// the memory limit mirroring the request and, with the probe policy, the limit
// of the cpu. prefix is "" for the main container and "sidecars[0]." and the
// like for the others.
func resourceFills(comp, prefix string) []Fill {
	return []Fill{
		{Component: comp, Path: prefix + "resources.requests.cpu", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: "37m", ValueWithoutPolicy: "100m"},
		{Component: comp, Path: prefix + "resources.requests.memory", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: "73Mi", ValueWithoutPolicy: "128Mi"},
		{Component: comp, Path: prefix + "resources.limits.memory", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: "331Mi", ValueWithoutPolicy: "128Mi"},
		{Component: comp, Path: prefix + "resources.limits.cpu", WithPolicy: oam.SourcePolicy, ValueWithPolicy: "211m"},
	}
}

func automount(comp string) Fill {
	return Fill{Component: comp, Path: "automountServiceAccountToken", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
		ValueWithPolicy: false, ValueWithoutPolicy: false}
}

func replicas(comp string) Fill {
	return Fill{Component: comp, Path: "replicas", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin,
		ValueWithPolicy: 2, ValueWithoutPolicy: 1}
}

func protocol(comp, path string) Fill {
	return Fill{Component: comp, Path: path, WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
		ValueWithPolicy: "TCP", ValueWithoutPolicy: "TCP"}
}

// others is a second value an author can write for a fill, chosen by the end of
// its path. Writing it must change the generated objects, which is what shows the
// path reaches them at all: the value the library would have used gives the same
// objects whether or not the library applies it.
var others = map[string]any{
	"replicas":                     5,
	"resources.requests.cpu":       "20m",
	"resources.requests.memory":    "64Mi",
	"resources.limits.memory":      "400Mi",
	"resources.limits.cpu":         "300m",
	"automountServiceAccountToken": true,
	"protocol":                     "UDP",
	"restartPolicy":                "Never",
	"status":                       "False",
	"successfulJobsHistoryLimit":   7,
	"failedJobsHistoryLimit":       5,
	"accessModes":                  []any{"ReadWriteMany"},
	"strategy.type":                "RollingUpdate",
}

// withOthers sets Other on every fill that has a value and an entry in others.
func withOthers(probes []Probe) []Probe {
	for i := range probes {
		for j, f := range probes[i].Fills {
			if (f.ValueWithPolicy == nil && f.ValueWithoutPolicy == nil) || f.Other != nil {
				continue
			}
			for suffix, v := range others {
				if strings.HasSuffix(f.Path, suffix) {
					probes[i].Fills[j].Other = cloneValue(v)
				}
			}
		}
	}
	return probes
}

func join(groups ...[]Fill) []Fill {
	var out []Fill
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// Probes returns the probe documents of the handler-built workload kinds, the lowered types and the traits
// (deployment, statefulset, daemonset, job and cronjob), with the fills observed when each runs with the probe policy and
// without one. What they show is what these documents trigger: a fill under a
// condition no document meets is not in them.
func Probes() []Probe {
	ports := func() []any { return []any{map[string]any{"containerPort": 8080}} }
	containers := map[string]any{
		"ports":          ports(),
		"initContainers": []any{map[string]any{"name": "setup", "image": image, "restartPolicy": "Always", "ports": ports()}},
		"sidecars":       []any{map[string]any{"name": "proxy", "image": image, "ports": ports()}},
	}
	containerFills := func(comp string, sidecars bool) []Fill {
		fills := join(
			[]Fill{protocol(comp, "ports[0].protocol"), protocol(comp, "initContainers[0].ports[0].protocol")},
			resourceFills(comp, "initContainers[0]."),
		)
		if sidecars {
			fills = join(fills, []Fill{protocol(comp, "sidecars[0].ports[0].protocol")}, resourceFills(comp, "sidecars[0]."))
		}
		return fills
	}
	claimVolume := []any{map[string]any{"name": "data", "type": "pvc", "claimName": "data", "mountPath": "/data"}}
	accessModes := func(comp, path string) Fill {
		return Fill{Component: comp, Path: path, WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: []any{"ReadWriteOnce"}, ValueWithoutPolicy: []any{"ReadWriteOnce"}}
	}
	failurePolicy := map[string]any{"rules": []any{map[string]any{
		"action":          "Ignore",
		"onPodConditions": []any{map[string]any{"type": "DisruptionTarget"}},
	}}}
	status := func(comp string) Fill {
		return Fill{Component: comp, Path: "podFailurePolicy.rules[0].onPodConditions[0].status", WithPolicy: oam.SourceBuiltin,
			WithoutPolicy: oam.SourceBuiltin, ValueWithPolicy: "True", ValueWithoutPolicy: "True"}
	}
	restart := func(comp string) Fill {
		return Fill{Component: comp, Path: "restartPolicy", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: "OnFailure", ValueWithoutPolicy: "OnFailure"}
	}

	return withOthers(append([]Probe{
		{Name: "deployment", Components: component("app", "deployment", nil),
			Fills: join([]Fill{replicas("app"), automount("app")}, resourceFills("app", ""))},
		{Name: "deployment with an init container, a sidecar and ports", Components: component("app", "deployment", containers),
			Fills: join([]Fill{replicas("app"), automount("app")}, resourceFills("app", ""), containerFills("app", true))},
		{Name: "deployment with a claim reference", Components: component("app", "deployment", map[string]any{"volumes": claimVolume}),
			RefusedWithPolicy: "a non-RWX PVC allows at most one replica",
			Fills: join([]Fill{
				// Replicas: the claim allows at most one, so the second value is 0. No value
				// on strategy.type, which the claim pins to Recreate (any other value is
				// refused), nor on accessModes, since the referenced claim is not
				// generated and no object shows the access mode: check B could not tell a
				// right value from a wrong one on either.
				{Component: "app", Path: "replicas", WithoutPolicy: oam.SourceBuiltin, ValueWithoutPolicy: 1, Other: 0},
				{Component: "app", Path: "automountServiceAccountToken", WithoutPolicy: oam.SourceBuiltin, ValueWithoutPolicy: false},
				{Component: "app", Path: "strategy.type", WithoutPolicy: oam.SourceBuiltin},
				{Component: "app", Path: "volumes[0].accessModes", WithoutPolicy: oam.SourceBuiltin},
			}, withoutPolicyOnly(resourceFills("app", "")))},
		{Name: "statefulset", Components: component("app", "statefulset", map[string]any{"serviceName": "app-headless"}),
			Fills: join([]Fill{replicas("app"), automount("app")}, resourceFills("app", ""))},
		{Name: "statefulset with containers and claim templates", Components: component("app", "statefulset", map[string]any{
			"serviceName": "app-headless", "ports": containers["ports"], "initContainers": containers["initContainers"], "sidecars": containers["sidecars"],
			"volumeClaimTemplates": []any{map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"}},
		}), Fills: join([]Fill{replicas("app"), automount("app"), accessModes("app", "volumeClaimTemplates[0].accessModes")},
			resourceFills("app", ""), containerFills("app", true))},
		{Name: "daemonset", Components: component("app", "daemonset", nil),
			Fills: join([]Fill{automount("app")}, resourceFills("app", ""))},
		{Name: "daemonset with containers", Components: component("app", "daemonset", containers),
			Fills: join([]Fill{automount("app")}, resourceFills("app", ""), containerFills("app", true))},
		{Name: "job", Components: component("app", "job", nil),
			Fills: join([]Fill{restart("app"), automount("app")}, resourceFills("app", ""))},
		{Name: "job with a pod failure policy", Components: component("app", "job", map[string]any{"restartPolicy": "Never", "podFailurePolicy": failurePolicy}),
			Fills: join([]Fill{status("app"), automount("app")}, resourceFills("app", ""))},
		{Name: "cronjob", Components: component("app", "cronjob", map[string]any{"schedule": "0 2 * * *"}),
			Fills: join([]Fill{restart("app"), automount("app"),
				{Component: "app", Path: "successfulJobsHistoryLimit", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin, ValueWithPolicy: 3, ValueWithoutPolicy: 3},
				{Component: "app", Path: "failedJobsHistoryLimit", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin, ValueWithPolicy: 1, ValueWithoutPolicy: 1},
			}, resourceFills("app", "")),
			LeftToCluster: map[string]string{"concurrencyPolicy": "the handler omits it and the cluster applies Allow"}},
	}, append(loweredProbes(), traitProbes()...)...))
}

// withoutPolicyOnly keeps the fills a document with no policy shows, as that
// state's expectation only.
func withoutPolicyOnly(fills []Fill) []Fill {
	var out []Fill
	for _, f := range fills {
		if f.WithoutPolicy == "" {
			continue
		}
		f.WithPolicy, f.ValueWithPolicy = "", nil
		out = append(out, f)
	}
	return out
}
