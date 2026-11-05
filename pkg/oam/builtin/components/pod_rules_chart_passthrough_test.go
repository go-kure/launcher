package components_test

import (
	"fmt"
	"strings"
	"testing"
)

// These tests cover go-kure/launcher#1238: an object a passthrough component
// holds, and one a chart renders, is held to the two pod rules the manifests
// source and the typed pod readers apply, as to the mount rules
// (go-kure/launcher#1241): a lifecycle sleep below zero or above the grace
// period (go-kure/launcher#1236) and a lifecycle or probe on an init container
// that is not restartable (go-kure/launcher#1237).

// prSleepSpec is the pod spec of workload w with one sleep hook on placement
// pl, under the authored grace line ("" for none). The grace line goes first:
// the hook is built at a container's depth, so nothing may sit between it and
// the container (go-kure/launcher#1249).
func prSleepSpec(w struct{ kind, apiVersion, path, podSpec string }, pl int, grace, hook string, seconds int) string {
	return grace + w.podSpec + strings.TrimPrefix(lsPlacements[pl].spec("", lsHook(hook, seconds)), htPlainPod)
}

// prInitSpec is the pod spec of workload w with an init container carrying
// field, restartable when restart is "Always".
func prInitSpec(w struct{ kind, apiVersion, path, podSpec string }, field, restart string) string {
	return w.podSpec + strings.TrimPrefix(riInit(field, restart), htPlainPod)
}

// TestPodRules_SleepPassthroughAndChartsOnEveryWorkloadKind: an object of each
// kind whose pod spec the shared check reads is refused on a sleep above the
// grace period, on a container and on a native sidecar, for both hooks, with
// no policy applied; a sleep equal to the grace period builds. The refusal
// names the object and the path of the hook.
func TestPodRules_SleepPassthroughAndChartsOnEveryWorkloadKind(t *testing.T) {
	for _, p := range mcPaths {
		for _, w := range ivWorkloads {
			for i, pl := range lsPlacements {
				for _, hook := range []string{"postStart", "preStop"} {
					t.Run(p.name+"/"+w.kind+"/"+pl.name+"/"+hook, func(t *testing.T) {
						err := p.run(t, ptWorkload(w.kind, w.apiVersion, w.path, prSleepSpec(w, i, "", hook, 31)), nil)
						trWant(t, err, p.ref(w.kind)+w.path+"."+pl.label+"lifecycle."+hook+".sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)")

						if err := p.run(t, ptWorkload(w.kind, w.apiVersion, w.path, prSleepSpec(w, i, "", hook, 30)), nil); err != nil {
							t.Errorf("a sleep equal to the grace period: %v", err)
						}
					})
				}
			}
		}
	}
}

// TestPodRules_SleepPassthroughAndChartsEveryLimit: each limit the typed
// readers apply, on a Deployment: the default grace period, an authored one, a
// grace period of zero, a negative sleep, and the sleeps within the limit.
func TestPodRules_SleepPassthroughAndChartsEveryLimit(t *testing.T) {
	deployment := ivWorkloads[3]
	for _, p := range mcPaths {
		for _, tc := range lsRefused {
			t.Run(p.name+"/refused: "+tc.name, func(t *testing.T) {
				err := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prSleepSpec(deployment, 0, tc.grace, "preStop", tc.seconds)), nil)
				trWant(t, err, p.ref("Deployment")+deployment.path+"."+lsPlacements[0].label+fmt.Sprintf("lifecycle.preStop.sleep.seconds: %d %s", tc.seconds, tc.tail))
			})
		}
		for _, tc := range lsAccepted {
			t.Run(p.name+"/accepted: "+tc.name, func(t *testing.T) {
				if err := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prSleepSpec(deployment, 0, tc.grace, "preStop", tc.seconds)), nil); err != nil {
					t.Errorf("err = %v, want the object built", err)
				}
			})
		}
	}
}

// TestPodRules_SleepNegativeGraceOnlyOnABarePod: Kubernetes defaults a negative
// terminationGracePeriodSeconds on a Pod to 1 and not on a pod template, so a
// bare Pod is held to a limit of 1 and a Deployment to the authored value.
func TestPodRules_SleepNegativeGraceOnlyOnABarePod(t *testing.T) {
	const grace = "terminationGracePeriodSeconds: -1\n"
	pod, deployment := ivWorkloads[0], ivWorkloads[3]
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			if err := p.run(t, ptWorkload(pod.kind, pod.apiVersion, pod.path, prSleepSpec(pod, 0, grace, "preStop", 1)), nil); err != nil {
				t.Errorf("a sleep of 1 on a Pod with grace -1 (defaulted to 1): %v, want it built", err)
			}
			trWant(t, p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prSleepSpec(deployment, 0, grace, "preStop", 1)), nil),
				p.ref("Deployment")+deployment.path+"."+lsPlacements[0].label+"lifecycle.preStop.sleep.seconds: 1 must not exceed terminationGracePeriodSeconds (-1)")
		})
	}
}

// TestPodRules_InitFieldsPassthroughAndChartsOnEveryWorkloadKind: lifecycle and
// each probe on an init container that is not restartable are refused on each
// kind, with no policy applied, naming the object, the container and the
// field; the same field on a restartable init container builds.
func TestPodRules_InitFieldsPassthroughAndChartsOnEveryWorkloadKind(t *testing.T) {
	for _, p := range mcPaths {
		for _, w := range ivWorkloads {
			for _, f := range riFields {
				t.Run(p.name+"/"+w.kind+"/"+f.key, func(t *testing.T) {
					err := p.run(t, ptWorkload(w.kind, w.apiVersion, w.path, prInitSpec(w, f.yaml, "")), nil)
					trWant(t, err, p.ref(w.kind)+w.path+`.initContainers[0] "init": `+f.key+": not supported on an init container that is not restartable — "+f.reason)

					if err := p.run(t, ptWorkload(w.kind, w.apiVersion, w.path, prInitSpec(w, f.yaml, "Always")), nil); err != nil {
						t.Errorf("a restartable init container: %v", err)
					}
				})
			}
		}
	}
}

// TestPodRules_InitFieldsBeforeTheSleepRule: the regular-init rule runs first.
// A regular init container with an over-long sleep is refused by the
// regular-init sentence, a restartable one by the sleep rule.
func TestPodRules_InitFieldsBeforeTheSleepRule(t *testing.T) {
	deployment := ivWorkloads[3]
	long := "    lifecycle:\n      preStop:\n        sleep:\n          seconds: 31\n"
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			err := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prInitSpec(deployment, long, "")), nil)
			trWant(t, err, p.ref("Deployment")+deployment.path+`.initContainers[0] "init": lifecycle: not supported on an init container that is not restartable — `+riLifecycleReason)

			err = p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prInitSpec(deployment, long, "Always")), nil)
			trWant(t, err, p.ref("Deployment")+deployment.path+`.initContainers[0] "init": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`)

			// A sleep on the app container too: the sleep rule would refuse that
			// one and does not skip it, so only the order decides which refusal
			// comes. The regular-init rule comes first.
			both := deployment.podSpec + long + strings.TrimPrefix(riInit(long, ""), htPlainPod)
			err = p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, both), nil)
			trWant(t, err, p.ref("Deployment")+deployment.path+`.initContainers[0] "init": lifecycle: not supported on an init container that is not restartable — `+riLifecycleReason)
		})
	}
}

// TestPodRules_PassthroughAndChartsRefusedUnderAPolicy: neither rule is the
// policy's: each refusal comes with a policy applied.
func TestPodRules_PassthroughAndChartsRefusedUnderAPolicy(t *testing.T) {
	deployment := ivWorkloads[3]
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			err := p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prSleepSpec(deployment, 0, "", "preStop", 31)), rcStrict)
			trWant(t, err, p.ref("Deployment")+deployment.path+"."+lsPlacements[0].label+"lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)")

			err = p.run(t, ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, prInitSpec(deployment, riFields[1].yaml, "")), rcStrict)
			trWant(t, err, p.ref("Deployment")+deployment.path+`.initContainers[0] "init": livenessProbe: not supported on an init container that is not restartable`)
		})
	}
}

// TestPodRules_PassthroughAndChartsLeaveOtherObjectsAlone: an object that runs
// no pod is not read.
func TestPodRules_PassthroughAndChartsLeaveOtherObjectsAlone(t *testing.T) {
	doc := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\ndata:\n  seconds: \"31\"\n"
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			if err := p.run(t, doc, nil); err != nil {
				t.Errorf("err = %v, want the object built", err)
			}
		})
	}
}

// TestPodRules_ChartHooks: an object of a hook phase the build keeps is held to
// both rules as any rendered object is; one of a phase it drops is never
// emitted and is not read.
func TestPodRules_ChartHooks(t *testing.T) {
	job := func(hook, podSpec string) string {
		return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: probe\n  annotations:\n    helm.sh/hook: " + hook + "\nspec:\n  template:\n    spec:\n" + htIndent("restartPolicy: Never\n"+podSpec, "      ")
	}
	run := func(t *testing.T, hook, podSpec string) error {
		srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{
			"deployment.yaml": htPolicyDeployment(htPlainPod),
			"job.yaml":        job(hook, podSpec),
		})
		_, err := htTransform(srvURL, nil)
		return err
	}
	sleepy := htPlainPod + strings.TrimPrefix(lsPlacements[0].spec("", lsHook("preStop", 31)), htPlainPod)
	regularInit := htPlainPod + strings.TrimPrefix(riInit(riFields[0].yaml, ""), htPlainPod)
	t.Run("sleep, pre-install is emitted", func(t *testing.T) {
		trWant(t, run(t, "pre-install", sleepy), `helmtemplate "web": rendered Job "demo/probe": spec.template.spec.containers[0] "app": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)`)
	})
	t.Run("regular init, pre-install is emitted", func(t *testing.T) {
		trWant(t, run(t, "pre-install", regularInit), `helmtemplate "web": rendered Job "demo/probe": spec.template.spec.initContainers[0] "init": lifecycle: not supported on an init container that is not restartable`)
	})
	t.Run("test is dropped", func(t *testing.T) {
		if err := run(t, "test", sleepy); err != nil {
			t.Errorf("err = %v, want the chart built", err)
		}
	})
}

// TestPodRules_UnregisteredWorkloadVersionNotRead: a workload in an API version
// kure's scheme does not register has no typed pod spec, so neither rule reads
// it; the sleep is never the reason it is refused.
func TestPodRules_UnregisteredWorkloadVersionNotRead(t *testing.T) {
	doc := "apiVersion: apps/v1beta2\nkind: Deployment\nmetadata:\n  name: thing\nspec:\n  template:\n    spec:\n" +
		htIndent(prSleepSpec(ivWorkloads[3], 0, "", "preStop", 31), "      ")
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			err := p.run(t, doc, nil)
			if err == nil || !strings.Contains(err.Error(), `apiVersion "apps/v1beta2" is not one whose pod spec this build can read`) {
				t.Fatalf("err = %v, want it refused as unreadable", err)
			}
			if strings.Contains(err.Error(), "sleep") {
				t.Errorf("err = %v, want the sleep not named", err)
			}
		})
	}
}

// TestPodRules_ManifestsErrorTextIsPinned: the manifests source's refusal text
// is the one it had before the check was shared, byte for byte from the object
// on: the wrap, the object, the path, the container and the rule. It holds
// before and after go-kure/launcher#1238.
func TestPodRules_ManifestsErrorTextIsPinned(t *testing.T) {
	deployment := ivWorkloads[3]
	for name, tc := range map[string]struct{ spec, want string }{
		"sleep": {
			prSleepSpec(deployment, 0, "", "preStop", 31),
			`manifest source: object Deployment "demo/thing": spec.template.spec.containers[0] "app": lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30); Kubernetes refuses the pod otherwise`,
		},
		"regular init": {
			prInitSpec(deployment, riFields[0].yaml, ""),
			`manifest source: object Deployment "demo/thing": spec.template.spec.initContainers[0] "init": lifecycle: not supported on an init container that is not restartable — ` + riLifecycleReason,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mfTransform("manifests", mfInline(ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, tc.spec)), nil)
			if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant one ending in %q", err, tc.want)
			}
		})
	}
}
