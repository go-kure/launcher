package components_test

import (
	"fmt"
	"strings"
	"testing"
)

// These tests cover go-kure/launcher#1236: the pod, podtemplate, replicaset,
// replicationcontroller and manifests readers refuse a postStart or preStop
// sleep longer than the pod's terminationGracePeriodSeconds (30 when it sets
// none) or below zero, as the app.yaml components do (go-kure/launcher#1228).
// Upstream's validateSleepAction refuses `sleep.Seconds < 0 ||
// sleep.Seconds > gracePeriod`, so a sleep equal to the grace period is admitted.

// lsHook is a container's lifecycle with one sleep hook, at a container's own
// depth.
func lsHook(hook string, seconds int) string {
	return fmt.Sprintf("    lifecycle:\n      %s:\n        sleep:\n          seconds: %d\n", hook, seconds)
}

// lsPlacements say where the hook sits: on the regular container or on a
// restartable init container (a native sidecar). label is the container's
// name in a refusal, after the pod spec's path.
var lsPlacements = []struct {
	name, label string
	spec        func(grace, hook string) string
}{
	{"container", `containers[0] "app": `, func(grace, hook string) string { return grace + htPlainPod + hook }},
	{"native sidecar", `initContainers[0] "nat": `, func(grace, hook string) string {
		return grace + htPlainPod + "initContainers:\n  - name: nat\n    image: registry.example/team/nat:1.0.0\n    restartPolicy: Always\n" + hook
	}},
}

// lsCases: grace is the authored terminationGracePeriodSeconds line ("" for
// none), limit what the refusal names.
var lsRefused = []struct {
	name, grace string
	seconds     int
	tail        string
}{
	{"default grace period is 30", "", 31, "must not exceed terminationGracePeriodSeconds (30)"},
	{"authored grace period", "terminationGracePeriodSeconds: 10\n", 11, "must not exceed terminationGracePeriodSeconds (10)"},
	{"a large sleep", "terminationGracePeriodSeconds: 120\n", 600, "must not exceed terminationGracePeriodSeconds (120)"},
	{"grace period zero admits no positive sleep", "terminationGracePeriodSeconds: 0\n", 1, "must not exceed terminationGracePeriodSeconds (0)"},
	{"negative", "", -1, "must be non-negative"},
}

var lsAccepted = []struct {
	name, grace string
	seconds     int
}{
	{"default grace period, shorter", "", 29},
	{"default grace period, equal", "", 30},
	{"authored grace period, equal", "terminationGracePeriodSeconds: 10\n", 10},
	{"an authored grace period replaces the default", "terminationGracePeriodSeconds: 120\n", 60},
	{"zero sleep with grace period zero", "terminationGracePeriodSeconds: 0\n", 0},
	{"zero sleep", "", 0},
}

// TestTypedLifecycleSleep_RefusedOnTheKinds: a sleep the rule refuses is
// refused when the pod, podtemplate, replicaset or replicationcontroller
// component is read, on the main container and on a native sidecar, for both
// hooks, with no policy applied. The error names the container, hook and
// the limit.
func TestTypedLifecycleSleep_RefusedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, pl := range lsPlacements {
			for _, hook := range []string{"postStart", "preStop"} {
				for _, tc := range lsRefused {
					t.Run(k.typ+"/"+pl.name+"/"+hook+"/"+tc.name, func(t *testing.T) {
						_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(pl.spec(tc.grace, lsHook(hook, tc.seconds)))), nil)
						if err == nil {
							t.Fatal("built, want the sleep refused")
						}
						want := fmt.Sprintf("%s%slifecycle.%s.sleep.seconds: %d %s", k.prefix, pl.label, hook, tc.seconds, tc.tail)
						if !strings.Contains(err.Error(), want) {
							t.Errorf("err = %v\nwant one holding %q", err, want)
						}
					})
				}
			}
		}
	}
}

// TestTypedLifecycleSleep_AcceptedOnTheKinds: a sleep within the grace period
// builds on each kind, so no input the API server accepts is refused.
func TestTypedLifecycleSleep_AcceptedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, pl := range lsPlacements {
			for _, hook := range []string{"postStart", "preStop"} {
				for _, tc := range lsAccepted {
					t.Run(k.typ+"/"+pl.name+"/"+hook+"/"+tc.name, func(t *testing.T) {
						if objs, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(pl.spec(tc.grace, lsHook(hook, tc.seconds)))), nil); err != nil || len(objs) != 1 {
							t.Errorf("objects %v, err %v; want the one object", objs, err)
						}
					})
				}
			}
		}
	}
}

// TestTypedLifecycleSleep_NegativeGraceOnABarePod: Kubernetes defaults a
// negative terminationGracePeriodSeconds on a Pod to 1 before validation
// (SetDefaults_Pod) and does not on a pod template, so a bare Pod is held to a
// limit of 1 and a template to the authored negative value, which admits no
// sleep above zero.
func TestTypedLifecycleSleep_NegativeGraceOnABarePod(t *testing.T) {
	const grace = "terminationGracePeriodSeconds: -1\n"
	for _, k := range mnKinds {
		t.Run(k.typ, func(t *testing.T) {
			build := func(seconds int) error {
				_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(lsPlacements[0].spec(grace, lsHook("preStop", seconds)))), nil)
				return err
			}
			if k.typ == "pod" {
				if err := build(1); err != nil {
					t.Errorf("a sleep of 1 on a Pod with grace -1 (defaulted to 1): %v, want it built", err)
				}
				trWant(t, build(2), k.prefix+lsPlacements[0].label+"lifecycle.preStop.sleep.seconds: 2 must not exceed terminationGracePeriodSeconds (1)")
				return
			}
			trWant(t, build(1), k.prefix+lsPlacements[0].label+"lifecycle.preStop.sleep.seconds: 1 must not exceed terminationGracePeriodSeconds (-1)")
		})
	}
	for _, w := range ivWorkloads {
		t.Run("manifests/"+w.kind, func(t *testing.T) {
			build := func(seconds int) error {
				spec := w.podSpec + strings.TrimPrefix(lsPlacements[0].spec(grace, lsHook("preStop", seconds)), htPlainPod)
				_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, spec)), nil)
				return err
			}
			if w.kind == "Pod" {
				if err := build(1); err != nil {
					t.Errorf("a sleep of 1 on a Pod with grace -1 (defaulted to 1): %v, want it built", err)
				}
				trWant(t, build(2), "terminationGracePeriodSeconds (1)")
				return
			}
			trWant(t, build(1), "terminationGracePeriodSeconds (-1)")
		})
	}
}

// TestTypedLifecycleSleep_RefusedUnderAPolicy: the rule is not the policy's:
// the same refusal comes with a policy applied.
func TestTypedLifecycleSleep_RefusedUnderAPolicy(t *testing.T) {
	for _, k := range mnKinds {
		t.Run(k.typ, func(t *testing.T) {
			_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(lsPlacements[0].spec("", lsHook("preStop", 31)))), rcStrict())
			trWant(t, err, k.prefix+lsPlacements[0].label+"lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)")
		})
	}
}

// TestTypedLifecycleSleep_ManifestsOnEveryWorkloadKind: an inline manifests
// source holding an object of any kind whose pod spec the shared check reads is
// refused on a sleep above the grace period, with no policy applied, naming the
// object and the path of its pod spec; a sleep equal to the grace period builds.
func TestTypedLifecycleSleep_ManifestsOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		for _, pl := range lsPlacements {
			t.Run(w.kind+"/"+pl.name, func(t *testing.T) {
				bad := w.podSpec + strings.TrimPrefix(pl.spec("", lsHook("preStop", 31)), htPlainPod)
				_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, bad)), nil)
				if err == nil {
					t.Fatal("built, want the sleep refused")
				}
				for _, want := range []string{"manifest source: object " + w.kind, w.path + "." + pl.label + "lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want one holding %q", err, want)
					}
				}

				good := w.podSpec + strings.TrimPrefix(pl.spec("", lsHook("preStop", 30)), htPlainPod)
				if objs, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, good)), nil); err != nil || len(objs) != 1 {
					t.Errorf("a sleep equal to the grace period: objects %v, err %v; want the one %s", objs, err, w.kind)
				}
			})
		}
	}
}

// TestTypedLifecycleSleep_ManifestsRefusedUnderAPolicy: the same refusal with a
// policy applied, and it is no policy class.
func TestTypedLifecycleSleep_ManifestsRefusedUnderAPolicy(t *testing.T) {
	doc := ptDeployment(lsPlacements[0].spec("", lsHook("postStart", 31)))
	_, err := mfTransform("manifests", mfInline(doc), rcStrict())
	trWant(t, err, `spec.template.spec.containers[0] "app": lifecycle.postStart.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`)
}

// TestTypedLifecycleSleep_ObjectsWithoutAPodSpecPass: a source holding only
// objects that run no pod is unaffected.
func TestTypedLifecycleSleep_ObjectsWithoutAPodSpecPass(t *testing.T) {
	doc := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  seconds: \"31\"\n"
	if objs, err := mfTransform("manifests", mfInline(doc), nil); err != nil || len(objs) != 1 {
		t.Errorf("objects %v, err %v; want the one ConfigMap", objs, err)
	}
}
