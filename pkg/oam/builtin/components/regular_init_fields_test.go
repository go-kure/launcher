package components_test

import (
	"strings"
	"testing"
)

// riFields are the four fields Kubernetes forbids on an init container that is
// not restartable (validateInitContainers in upstream's core validation):
// lifecycle and the three probes. yaml is the field at an init container's own
// depth; reason is the sentence the typed refusal ends with.
var riFields = []struct{ key, yaml, reason string }{
	{"lifecycle", "    lifecycle:\n      preStop:\n        exec:\n          command: [\"true\"]\n", riLifecycleReason},
	{"livenessProbe", "    livenessProbe:\n      exec:\n        command: [\"true\"]\n", riProbeReason},
	{"readinessProbe", "    readinessProbe:\n      exec:\n        command: [\"true\"]\n", riProbeReason},
	{"startupProbe", "    startupProbe:\n      exec:\n        command: [\"true\"]\n", riProbeReason},
}

const (
	riProbeReason     = "Kubernetes forbids probes on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar"
	riLifecycleReason = "Kubernetes forbids lifecycle hooks on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar"
)

// riInit is the pod spec of htPlainPod with one init container named init that
// carries field and, when restart is not empty, restartPolicy: restart.
func riInit(field, restart string) string {
	ctr := "initContainers:\n  - name: init\n    image: registry.example/team/init:1.0.0\n"
	if restart != "" {
		ctr += "    restartPolicy: " + restart + "\n"
	}
	return htPlainPod + ctr + field
}

// TestRegularInitFields_RefusedOnTheKinds: lifecycle and each probe on an init
// container that is not restartable are refused when the pod, podtemplate,
// replicaset or replicationcontroller component is read, with no policy
// applied, naming the container and the field and ending in the Kubernetes
// rule (go-kure/launcher#1237).
func TestRegularInitFields_RefusedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, f := range riFields {
			for _, restart := range []string{"", "Never", "OnFailure"} {
				t.Run(k.typ+"/"+f.key+"/restart="+restart, func(t *testing.T) {
					_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(riInit(f.yaml, restart))), nil)
					if err == nil {
						t.Fatal("built, want the field refused")
					}
					want := k.prefix + `initContainers[0] "init": ` + f.key + ": not supported on an init container that is not restartable — " + f.reason
					if !strings.HasSuffix(err.Error(), want) {
						t.Errorf("err = %v\nwant one ending in %q", err, want)
					}
				})
			}
		}
	}
}

// TestRegularInitFields_AcceptedOnARestartableInit: the same field on an init
// container with restartPolicy: Always (a native sidecar) builds on each kind,
// and a regular init container without the fields is untouched.
func TestRegularInitFields_AcceptedOnARestartableInit(t *testing.T) {
	for _, k := range mnKinds {
		for _, f := range riFields {
			t.Run(k.typ+"/"+f.key, func(t *testing.T) {
				if objs, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(riInit(f.yaml, "Always"))), nil); err != nil || len(objs) != 1 {
					t.Errorf("objects %v, err %v; want the one object", objs, err)
				}
			})
		}
		t.Run(k.typ+"/plain regular init", func(t *testing.T) {
			if objs, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(riInit("", ""))), nil); err != nil || len(objs) != 1 {
				t.Errorf("objects %v, err %v; want the one object", objs, err)
			}
		})
	}
}

// TestRegularInitFields_RefusedUnderAPolicy: the rule is not the policy's: the
// same refusal comes with a policy applied.
func TestRegularInitFields_RefusedUnderAPolicy(t *testing.T) {
	for _, k := range mnKinds {
		t.Run(k.typ, func(t *testing.T) {
			_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(riInit(riFields[0].yaml, ""))), rcStrict())
			trWant(t, err, k.prefix+`initContainers[0] "init": lifecycle: not supported on an init container that is not restartable`)
		})
	}
}

// TestRegularInitFields_ManifestsOnEveryWorkloadKind: an inline manifests
// source holding an object of any kind whose pod spec the shared check reads is
// refused on each of the four fields, with no policy applied, naming the object
// and the path of its pod spec; the same object with a restartable init
// container builds.
func TestRegularInitFields_ManifestsOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		for _, f := range riFields {
			t.Run(w.kind+"/"+f.key, func(t *testing.T) {
				bad := w.podSpec + strings.TrimPrefix(riInit(f.yaml, ""), htPlainPod)
				_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, bad)), nil)
				if err == nil {
					t.Fatal("built, want the field refused")
				}
				for _, want := range []string{"manifest source: object " + w.kind, w.path + `.initContainers[0] "init": ` + f.key + ": not supported on an init container that is not restartable — " + f.reason} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want one holding %q", err, want)
					}
				}

				good := w.podSpec + strings.TrimPrefix(riInit(f.yaml, "Always"), htPlainPod)
				if objs, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, good)), nil); err != nil || len(objs) != 1 {
					t.Errorf("a restartable init container: objects %v, err %v; want the one %s", objs, err, w.kind)
				}
			})
		}
	}
}

// TestRegularInitFields_ManifestsRefusedUnderAPolicy: the same refusal with a
// policy applied, and it is no policy class.
func TestRegularInitFields_ManifestsRefusedUnderAPolicy(t *testing.T) {
	_, err := mfTransform("manifests", mfInline(ptDeployment(riInit(riFields[1].yaml, ""))), rcStrict())
	trWant(t, err, `spec.template.spec.initContainers[0] "init": livenessProbe: not supported on an init container that is not restartable`)
}
