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

// rawWorkload is a component of the type typ ("deployment", "statefulset" or "daemonset") as
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

// rawBatch is a job or cronjob component (typ) as the kinds take it since they
// took the upstream spec (go-kure/launcher#1629): a pod template with one
// container and the restartPolicy a Job's pod needs, under `jobTemplate.spec` for
// a cronjob. props adds top-level properties (a cronjob's schedule). The kinds
// fill nothing, so a probe built on it holds no fill of the kinds' own.
func rawBatch(name, typ string, props map[string]any) []oam.Component {
	pod := map[string]any{
		"restartPolicy": "OnFailure",
		"containers":    []any{map[string]any{"name": "app", "image": image}},
	}
	tpl := map[string]any{"spec": pod}
	p := map[string]any{}
	if typ == "cronjob" {
		p["jobTemplate"] = map[string]any{"spec": map[string]any{"template": tpl}}
	} else {
		p["template"] = tpl
	}
	for k, v := range props {
		p[k] = v
	}
	return []oam.Component{{Name: name, Type: typ, Properties: p}}
}

// withTopLevel adds top-level properties to the first component of comps, which
// a raw workload takes beside its selector and template.
func withTopLevel(comps []oam.Component, props map[string]any) []oam.Component {
	for k, v := range props {
		comps[0].Properties[k] = v
	}
	return comps
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

func replicas(comp string) Fill {
	return Fill{Component: comp, Path: "replicas", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin,
		ValueWithPolicy: 2, ValueWithoutPolicy: 1}
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

// Probes returns the probe documents of the raw workload kinds (deployment,
// statefulset, daemonset, job and cronjob; they fill nothing), the lowered types
// and the traits, with the fills observed when each runs with the probe policy and
// without one. What they show is what these documents trigger: a fill under a
// condition no document meets is not in them.
func Probes() []Probe {
	ports := func() []any { return []any{map[string]any{"containerPort": 8080}} }

	return withOthers(append([]Probe{
		// The raw deployment, daemonset, statefulset, job and cronjob fill nothing
		// (go-kure/launcher#1629): the report of these documents holds no entry,
		// and a fill that came back would show as a change of that report.
		{Name: "deployment", Components: rawWorkload("deployment", "app", nil, nil)},
		{Name: "deployment with an init container, a second container and ports", Components: rawWorkload("deployment", "app",
			nil, rawContainersAndInit(ports))},
		{Name: "daemonset", Components: rawWorkload("daemonset", "app", nil, nil)},
		{Name: "daemonset with an init container, a second container and ports", Components: rawWorkload("daemonset", "app",
			nil, rawContainersAndInit(ports))},
		{Name: "statefulset", Components: rawWorkload("statefulset", "app", nil, nil)},
		{Name: "statefulset with an init container, a second container and ports", Components: rawWorkload("statefulset", "app",
			nil, rawContainersAndInit(ports))},
		{Name: "statefulset with a claim template", Components: withTopLevel(rawWorkload("statefulset", "app", nil, nil), map[string]any{
			"serviceName": "app-headless",
			"volumeClaimTemplates": []any{map[string]any{
				"metadata": map[string]any{"name": "data"},
				"spec": map[string]any{
					"accessModes": []any{"ReadWriteOnce"},
					"resources":   map[string]any{"requests": map[string]any{"storage": "1Gi"}},
				},
			}},
		})},
		{Name: "job", Components: rawBatch("app", "job", nil)},
		{Name: "cronjob", Components: rawBatch("app", "cronjob", map[string]any{"schedule": "0 2 * * *"})},
	}, append(loweredProbes(), traitProbes()...)...))
}
