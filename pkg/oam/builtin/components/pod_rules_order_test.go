package components_test

import (
	"strings"
	"testing"
)

// These tests pin the order in which a passthrough component and a chart's
// render apply the three pod checks (go-kure/launcher#1253 container names,
// go-kure/launcher#1223 mounts, go-kure/launcher#1236 and go-kure/launcher#1237
// pod rules): the order the typed pod reader applies them in
// (validateAuthoredPodSpec): container names, then mounts, then the pod rules.
// An object that fails more than one is refused with the same first error on
// either path.

// poSpec is the pod spec of a Deployment with a regular init container that
// carries a lifecycle (a pod-rule refusal), an app container whose mount has an
// absolute subPath (a mount refusal), and a second container named as the
// first when dup is set (a container-name refusal).
func poSpec(dup bool) string {
	spec := "initContainers:\n  - name: init\n    image: registry.example/team/init:1.0.0\n" + riFields[0].yaml +
		"containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n" + mnMount("    ", "subPath: /abs")
	if dup {
		spec += "  - name: app\n    image: registry.example/team/app:1.2.3\n"
	}
	return spec + "volumes:\n  - name: data\n    emptyDir: {}\n"
}

// TestPodRules_ContainerNamesBeforeMountsBeforePodRules: an object that fails
// the container-name check, a mount rule and a pod rule is refused by the
// container-name check; with the names distinct, by the mount rule; with the
// mount allowed too, by the pod rule.
func TestPodRules_ContainerNamesBeforeMountsBeforePodRules(t *testing.T) {
	deployment := ivWorkloads[3]
	names := `containers[1] "app": container name "app" is also used by ` + deployment.path + `.containers[0] "app"`
	const (
		mount = `containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs"`
		rule  = `initContainers[0] "init": lifecycle: not supported on an init container that is not restartable`
	)
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			all := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, poSpec(true)), nil)
			trWant(t, all, p.ref("Deployment")+deployment.path+"."+names)
			if all != nil && (strings.Contains(all.Error(), "subPath") || strings.Contains(all.Error(), "lifecycle")) {
				t.Errorf("err = %v, want only the container-name refusal", all)
			}

			noDup := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, poSpec(false)), nil)
			trWant(t, noDup, p.ref("Deployment")+deployment.path+"."+mount)
			if noDup != nil && strings.Contains(noDup.Error(), "lifecycle") {
				t.Errorf("err = %v, want the mount refusal and not the pod rule", noDup)
			}

			okMount := strings.Replace(poSpec(false), "subPath: /abs", "subPath: sub", 1)
			trWant(t, p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, okMount), nil),
				p.ref("Deployment")+deployment.path+"."+rule)
		})
	}
}
