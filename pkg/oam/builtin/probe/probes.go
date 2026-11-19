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

// rawWorkload is a component of the type typ ("deployment" or "daemonset") as
// the kind takes it since it took the upstream spec (go-kure/launcher#1629): a
// selector and a pod template whose first container is the one container adds
// fields to, and whose pod spec pod adds fields to (a key of pod replaces the
// one of the same name, `containers` included). The kind fills nothing, so a
// probe built on it holds no fill of the kind's own.
func rawWorkload(typ, name string, container, pod map[string]any) []oam.Component {
	main := map[string]any{"name": "app", "image": image}
	for k, v := range container {
		main[k] = v
	}
	spec := map[string]any{"containers": []any{main}}
	for k, v := range pod {
		spec[k] = v
	}
	return []oam.Component{{Name: name, Type: typ, Properties: map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
		"template": map[string]any{"spec": spec},
	}}}
}

// rawContainersAndInit is the pod spec part of a raw workload probe with an
// init container and a second container, each declaring the ports ports builds.
func rawContainersAndInit(ports func() []any) map[string]any {
	return map[string]any{
		"initContainers": []any{map[string]any{"name": "setup", "image": image, "restartPolicy": "Always", "ports": ports()}},
		"containers": []any{
			map[string]any{"name": "app", "image": image, "ports": ports()},
			map[string]any{"name": "proxy", "image": image, "ports": ports()},
		},
	}
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
		// The raw deployment and daemonset fill nothing (go-kure/launcher#1629):
		// the report of these documents holds no entry, and a fill that came back
		// would show as a change of that report.
		{Name: "deployment", Components: rawWorkload("deployment", "app", nil, nil)},
		{Name: "deployment with an init container, a second container and ports", Components: rawWorkload("deployment", "app",
			nil, rawContainersAndInit(ports))},
		{Name: "daemonset", Components: rawWorkload("daemonset", "app", nil, nil)},
		{Name: "daemonset with an init container, a second container and ports", Components: rawWorkload("daemonset", "app",
			nil, rawContainersAndInit(ports))},
		{Name: "statefulset", Components: component("app", "statefulset", map[string]any{"serviceName": "app-headless"}),
			Fills: join([]Fill{replicas("app"), automount("app")}, resourceFills("app", ""))},
		{Name: "statefulset with containers and claim templates", Components: component("app", "statefulset", map[string]any{
			"serviceName": "app-headless", "ports": containers["ports"], "initContainers": containers["initContainers"], "sidecars": containers["sidecars"],
			"volumeClaimTemplates": []any{map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"}},
		}), Fills: join([]Fill{replicas("app"), automount("app"), accessModes("app", "volumeClaimTemplates[0].accessModes")},
			resourceFills("app", ""), containerFills("app", true))},
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
