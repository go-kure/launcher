package components_test

import (
	"strings"
	"testing"
)

// These tests pin the order in which a passthrough component, a chart's render
// and a manifests source apply the three pod checks (go-kure/launcher#1253
// container names, go-kure/launcher#1223 mounts, go-kure/launcher#1236 and
// go-kure/launcher#1237 pod rules): the order the typed pod reader applies them
// in (validateAuthoredPodSpec): container names, then mounts, then the pod
// rules. An object that fails more than one is refused with the same first
// error on every path, and a manifests source checks object by object
// (go-kure/launcher#1297).

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

// poRefusals are the three refusals poSpec provokes on a pod spec at path, from
// the position of the pod spec on: the container-name, the mount and the
// pod-rule refusal.
func poRefusals(path string) (names, mount, rule string) {
	names = `containers[1] "app": container name "app" is also used by ` + path + `.containers[0] "app"`
	mount = `containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs"`
	rule = `initContainers[0] "init": lifecycle: not supported on an init container that is not restartable`
	return names, mount, rule
}

// poPlain is the pod spec of a Deployment with one app container whose mount
// has the given subPath, and a second container named as the first when dup is
// set: at most one refusal, a container-name one for dup and a mount one for an
// absolute subPath, and no pod rule.
func poPlain(dup bool, subPath string) string {
	spec := "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n" + mnMount("    ", "subPath: "+subPath)
	if dup {
		spec += "  - name: app\n    image: registry.example/team/app:1.2.3\n"
	}
	return spec + "volumes:\n  - name: data\n    emptyDir: {}\n"
}

// poTwo is a manifests source holding two Deployments, thing then other, with
// the given pod specs.
func poTwo(first, second string) string {
	d := ivWorkloads[3]
	other := strings.Replace(ptWorkload(d.kind, d.apiVersion, d.path, second), "name: thing", "name: other", 1)
	return ptWorkload(d.kind, d.apiVersion, d.path, first) + "---\n" + other
}

// TestPodRules_ContainerNamesBeforeMountsBeforePodRules: an object that fails
// the container-name check, a mount rule and a pod rule is refused by the
// container-name check; with the names distinct, by the mount rule; with the
// mount allowed too, by the pod rule.
func TestPodRules_ContainerNamesBeforeMountsBeforePodRules(t *testing.T) {
	deployment := ivWorkloads[3]
	names, mount, rule := poRefusals(deployment.path)
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

// TestPodRules_ManifestsChecksInTheTypedOrder: the manifests source refuses an
// object that fails the container-name check, a mount rule and a pod rule with
// the container-name check, as a passthrough component and a chart do; with
// the names distinct, with the mount rule; with the mount allowed too, with the
// pod rule (go-kure/launcher#1297).
func TestPodRules_ManifestsChecksInTheTypedOrder(t *testing.T) {
	d := ivWorkloads[3]
	names, mount, rule := poRefusals(d.path)
	ref := `manifest source: object Deployment "demo/thing": ` + d.path + "."
	run := func(spec string) error {
		_, err := mfTransform("manifests", mfInline(ptWorkload(d.kind, d.apiVersion, d.path, spec)), nil)
		return err
	}

	all := run(poSpec(true))
	trWant(t, all, ref+names)
	if all != nil && (strings.Contains(all.Error(), "subPath") || strings.Contains(all.Error(), "lifecycle")) {
		t.Errorf("err = %v, want only the container-name refusal", all)
	}

	noDup := run(poSpec(false))
	trWant(t, noDup, ref+mount)
	if noDup != nil && strings.Contains(noDup.Error(), "lifecycle") {
		t.Errorf("err = %v, want the mount refusal and not the pod rule", noDup)
	}

	okMount := strings.Replace(poSpec(false), "subPath: /abs", "subPath: sub", 1)
	trWant(t, run(okMount), ref+rule)
}

// TestPodRules_ManifestsOrderIsPerObject: the manifests source applies the
// three checks object by object, as a chart's render does, and not each check
// over every object in turn (go-kure/launcher#1297). Which object is refused
// first is the first object that fails any check, whatever the check.
func TestPodRules_ManifestsOrderIsPerObject(t *testing.T) {
	d := ivWorkloads[3]
	names, mount, _ := poRefusals(d.path)
	run := func(doc string) error {
		_, err := mfTransform("manifests", mfInline(doc), nil)
		return err
	}

	// The first object repeats a container name and the second has a bad mount.
	// Checking mounts over every object first refuses the second object for its
	// mount; per object, the first is refused for its names. Red on a source
	// that checks all mounts before any container name.
	t.Run("first object names, second object mount", func(t *testing.T) {
		err := run(poTwo(poPlain(true, "sub"), poPlain(false, "/abs")))
		trWant(t, err, `manifest source: object Deployment "demo/thing": `+d.path+"."+names)
		if err != nil && strings.Contains(err.Error(), "subPath") {
			t.Errorf("err = %v, want the first object's container-name refusal only", err)
		}
	})

	// The first object has a bad mount and the second repeats a container name.
	// The first object is refused for its mount either way: this case passes on a
	// source that checks all mounts first, and on the typed order, so it does not
	// tell them apart. It fails on a source that checks every container name
	// over all objects before any mount, which refuses the second object.
	t.Run("first object mount, second object names", func(t *testing.T) {
		err := run(poTwo(poPlain(false, "/abs"), poPlain(true, "sub")))
		trWant(t, err, `manifest source: object Deployment "demo/thing": `+d.path+"."+mount)
		if err != nil && strings.Contains(err.Error(), "container name") {
			t.Errorf("err = %v, want the first object's mount refusal only", err)
		}
	})
}
