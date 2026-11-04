package components

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestCheckLifecycleSleep holds the helper to upstream's validateSleepAction:
// a sleep below zero or above the grace period is refused, a sleep equal to it
// is admitted, an unauthored grace period is 30, and only a regular container or
// a restartable init container is read, since a regular init container cannot
// carry a lifecycle (refuseRegularInitFields). Negative seconds cannot reach
// the helper through an app.yaml document, whose parser bounds the value at 0,
// so they are pinned here.
func TestCheckLifecycleSleep(t *testing.T) {
	alwaysPolicy := corev1.ContainerRestartPolicyAlways
	always := &alwaysPolicy
	sleep := func(seconds int64) *corev1.LifecycleHandler {
		return &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: seconds}}
	}
	cases := []struct {
		name  string
		grace *int64
		init  []corev1.Container
		main  []corev1.Container
		// want is the refusal after the label, "" for a spec the rule accepts.
		want string
		// label is the container the refusal names, after the prefix.
		label string
		// bare marks a Pod object, whose negative grace period Kubernetes
		// defaults to 1 before validation; a pod template gets no such default.
		bare bool
	}{
		{name: "nothing to check", main: []corev1.Container{{Name: "app"}}},
		{name: "equal to the default grace period", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(30)}}}},
		{name: "zero", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(0)}}}},
		{name: "above the default grace period", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(31)}}},
			label: `containers[0] "app"`, want: "lifecycle.preStop.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30); Kubernetes refuses the pod otherwise"},
		{name: "postStart is read too", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PostStart: sleep(31)}}},
			label: `containers[0] "app"`, want: "lifecycle.postStart.sleep.seconds: 31 must not exceed terminationGracePeriodSeconds (30)"},
		{name: "an authored grace period replaces the default", grace: i64(120), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(60)}}}},
		{name: "above an authored grace period", grace: i64(10), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(11)}}},
			label: `containers[0] "app"`, want: "must not exceed terminationGracePeriodSeconds (10)"},
		{name: "grace period zero admits no positive sleep", grace: i64(0), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(1)}}},
			label: `containers[0] "app"`, want: "must not exceed terminationGracePeriodSeconds (0)"},
		{name: "negative", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(-1)}}},
			label: `containers[0] "app"`, want: "lifecycle.preStop.sleep.seconds: -1 must be non-negative; Kubernetes refuses the pod otherwise"},
		{name: "negative under an authored grace period", grace: i64(10), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PostStart: sleep(-5)}}},
			label: `containers[0] "app"`, want: "lifecycle.postStart.sleep.seconds: -5 must be non-negative"},
		{name: "bare Pod, negative grace period is defaulted to 1", bare: true, grace: i64(-1), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(1)}}}},
		{name: "bare Pod, sleep above the defaulted grace period", bare: true, grace: i64(-1), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(2)}}},
			label: `containers[0] "app"`, want: "must not exceed terminationGracePeriodSeconds (1)"},
		{name: "pod template, negative grace period is kept", grace: i64(-1), main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(1)}}},
			label: `containers[0] "app"`, want: "must not exceed terminationGracePeriodSeconds (-1)"},
		{name: "second container is named by its index", main: []corev1.Container{{Name: "app"}, {Name: "side", Lifecycle: &corev1.Lifecycle{PreStop: sleep(31)}}},
			label: `containers[1] "side"`, want: "must not exceed terminationGracePeriodSeconds (30)"},
		{name: "restartable init container is read", init: []corev1.Container{{Name: "nat", RestartPolicy: always, Lifecycle: &corev1.Lifecycle{PreStop: sleep(31)}}}, main: []corev1.Container{{Name: "app"}},
			label: `initContainers[0] "nat"`, want: "must not exceed terminationGracePeriodSeconds (30)"},
		{name: "restartable init container within the grace period", init: []corev1.Container{{Name: "nat", RestartPolicy: always, Lifecycle: &corev1.Lifecycle{PreStop: sleep(30)}}}, main: []corev1.Container{{Name: "app"}}},
		{name: "regular init container is not read", init: []corev1.Container{{Name: "i", Lifecycle: &corev1.Lifecycle{PreStop: sleep(999)}}}, main: []corev1.Container{{Name: "app"}}},
		{name: "a lifecycle with no sleep", main: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"true"}}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := &corev1.PodSpec{TerminationGracePeriodSeconds: tc.grace, InitContainers: tc.init, Containers: tc.main}
			err := checkLifecycleSleep("spec.template.spec.", ps, tc.bare)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			want := "spec.template.spec." + tc.label + ": "
			if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one starting %q and holding %q", err, want, tc.want)
			}
		})
	}

	t.Run("no prefix for a pod spec that is the object's own", func(t *testing.T) {
		ps := &corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Lifecycle: &corev1.Lifecycle{PreStop: sleep(31)}}}}
		err := checkLifecycleSleep("", ps, false)
		if err == nil || !strings.HasPrefix(err.Error(), `containers[0] "app": lifecycle.preStop.sleep.seconds: 31`) {
			t.Fatalf("err = %v", err)
		}
	})
}
