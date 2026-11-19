package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestCronjobHandler_CanHandle(t *testing.T) {
	h := &components.CronjobHandler{}
	if !h.CanHandle("cronjob") {
		t.Error("CanHandle(cronjob) = false")
	}
	if h.CanHandle("job") {
		t.Error("CanHandle(job) = true")
	}
}

// cronjobRich is a CronJob spec that sets every top-level field, a job
// template with metadata beside the job's own spec fields, and a pod spec with
// lists of objects, a map, pointers and a quantity written as a number.
const cronjobRich = `schedule: "0 2 * * *"
timeZone: Europe/Brussels
concurrencyPolicy: Forbid
startingDeadlineSeconds: 120
suspend: true
successfulJobsHistoryLimit: 5
failedJobsHistoryLimit: 0
jobTemplate:
  metadata:
    labels:
      team: data
    annotations:
      example.com/owner: data
  spec:
    backoffLimit: 2
    activeDeadlineSeconds: 3600
    template:
      metadata:
        labels:
          tier: batch
      spec:
        restartPolicy: OnFailure
        terminationGracePeriodSeconds: 0
        serviceAccountName: batch
        volumes:
          - name: scratch
            emptyDir: {}
        initContainers:
          - name: init
            image: registry.example/team/init:1.0.0
        containers:
          - name: app
            image: registry.example/team/app:1.2.3
            resources:
              limits:
                cpu: 1
                memory: 512Mi
            volumeMounts:
              - name: scratch
                mountPath: /scratch
`

// cronjobPlain is the smallest cronjob component: a nightly schedule and a job
// template whose pod runs the given pod spec.
func cronjobPlain(podSpec string) string {
	return "schedule: \"0 2 * * *\"\njobTemplate:\n  spec:\n    template:\n      metadata:\n        labels:\n          tier: batch\n      spec:\n        restartPolicy: OnFailure\n" +
		htIndent(podSpec, "        ")
}

// cronjobGenerate generates a cronjob component under the given policies and
// returns the CronJob.
func cronjobGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *batchv1.CronJob {
	t.Helper()
	return generateCoreKindUnder(t, &components.CronjobHandler{}, "cronjob", name, props, policies...).(*batchv1.CronJob)
}

// TestCronjobHandler_EmitsAuthoredSpec: the CronJob is named after the
// component in the build namespace, unlabelled, and its spec is the authored
// one, field for field, with one thing added: the `app` label on the pod
// template of the job template, beside the authored labels.
func TestCronjobHandler_EmitsAuthoredSpec(t *testing.T) {
	var want batchv1.CronJobSpec
	if err := yaml.UnmarshalStrict([]byte(cronjobRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.JobTemplate.Spec.Template.Labels["app"] = "nightly"
	cj := cronjobGenerate(t, "nightly", ptObject(t, cronjobRich), ptStrictPolicy(), nil)
	if cj.APIVersion != "batch/v1" || cj.Kind != "CronJob" {
		t.Errorf("GVK = %s %s, want batch/v1 CronJob", cj.APIVersion, cj.Kind)
	}
	if cj.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", cj.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(cj.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", cj.Spec, want)
	}
	if got := cj.Spec.JobTemplate.Labels; !maps.Equal(got, map[string]string{"team": "data"}) {
		t.Errorf("jobTemplate labels = %v, want the authored ones only", got)
	}
}

// TestCronjobHandler_AppLabel: the pod template of the job template carries
// the `app` label every workload kind gives its pods, with the component's
// label value: the name itself up to 63 characters, its projection beyond. The
// authored labels stay, and no selector is made up.
func TestCronjobHandler_AppLabel(t *testing.T) {
	cj := cronjobGenerate(t, "nightly", ptObject(t, cronjobPlain(htPlainPod)), nil)
	want := map[string]string{"tier": "batch", "app": oam.ComponentLabelValue("nightly")}
	if got := cj.Spec.JobTemplate.Spec.Template.Labels; !maps.Equal(got, want) {
		t.Errorf("template labels = %v, want %v", got, want)
	}
	if len(cj.Spec.JobTemplate.Labels) != 0 {
		t.Errorf("jobTemplate labels = %v, want none: only the pods carry the label", cj.Spec.JobTemplate.Labels)
	}
	if cj.Spec.JobTemplate.Spec.Selector != nil {
		t.Errorf("selector = %+v, want none: the job controller generates it", cj.Spec.JobTemplate.Spec.Selector)
	}
}

// cronjobLabelled is a cronjob component whose pod template carries the given
// labels.
func cronjobLabelled(labels map[string]any) map[string]any {
	metadata := map[string]any{}
	if labels != nil {
		metadata["labels"] = labels
	}
	return map[string]any{
		"schedule": "0 2 * * *",
		"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{
			"metadata": metadata,
			"spec": map[string]any{
				"restartPolicy": "OnFailure",
				"containers":    []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}},
			},
		}}},
	}
}

// TestCronjobHandler_AuthoredAppLabel: an authored `app` label on the pod
// template is kept when it is the component's own label value and refused,
// naming the component and the path, when it is another. One on the job
// template's own metadata labels the Job object, which nothing selects, and
// stays as authored.
func TestCronjobHandler_AuthoredAppLabel(t *testing.T) {
	value := oam.ComponentLabelValue("nightly")
	cj := cronjobGenerate(t, "nightly", cronjobLabelled(map[string]any{"app": value}), nil)
	if want := map[string]string{"app": value}; !maps.Equal(cj.Spec.JobTemplate.Spec.Template.Labels, want) {
		t.Errorf("template labels = %v, want %v", cj.Spec.JobTemplate.Spec.Template.Labels, want)
	}
	for name, authored := range map[string]string{"another value": "frontend", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.CronjobHandler{}, "cronjob", "nightly", cronjobLabelled(map[string]any{"tier": "batch", "app": authored}))
			want := "jobTemplate.spec.template.metadata.labels.app: " + `"` + authored + `"` + " is not the `app` label of component \"nightly\""
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one mentioning %q", err, want)
			}
		})
	}
	props := cronjobLabelled(map[string]any{"tier": "batch"})
	props["jobTemplate"].(map[string]any)["metadata"] = map[string]any{"labels": map[string]any{"app": "elsewhere"}}
	cj = cronjobGenerate(t, "nightly", props, nil)
	if got := cj.Spec.JobTemplate.Labels["app"]; got != "elsewhere" {
		t.Errorf("jobTemplate app label = %q, want the authored one kept", got)
	}
}

// TestCronjobHandler_SelectorRefused: the API server always refuses a selector
// on the job template, which it generates, and manualSelector true. They are
// refused here, naming the path; manualSelector false is what the API accepts.
func TestCronjobHandler_SelectorRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]any
		want  string // "" when the component builds
	}{
		"selector":             {map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "batch"}}}, "jobTemplate.spec.selector: not supported"},
		"manualSelector true":  {map[string]any{"manualSelector": true}, "jobTemplate.spec.manualSelector: not supported"},
		"manualSelector false": {map[string]any{"manualSelector": false}, ""},
		"selector and manual":  {map[string]any{"manualSelector": true, "selector": map[string]any{"matchLabels": map[string]any{"tier": "batch"}}}, "jobTemplate.spec.selector: not supported"},
		"another job field":    {map[string]any{"backoffLimit": 1}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			props := cronjobLabelled(map[string]any{"tier": "batch"})
			maps.Copy(props["jobTemplate"].(map[string]any)["spec"].(map[string]any), tc.extra)
			err := coreKindErr(&components.CronjobHandler{}, "cronjob", "nightly", props)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCronjobHandler_FillsNothing: a policy that carries resource defaults
// fills none of them, no history limit and no restart policy is made up, and a
// pod with no service account keeps the cluster's default for the token mount.
func TestCronjobHandler_FillsNothing(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultStorageSize: "1Gi",
	}
	props := ptObject(t, "schedule: \"0 2 * * *\"\njobTemplate:\n  spec:\n    template:\n      spec:\n"+htIndent(htPlainPod, "        "))
	cj := cronjobGenerate(t, "nightly", props, defaults)
	if cj.Spec.SuccessfulJobsHistoryLimit != nil || cj.Spec.FailedJobsHistoryLimit != nil {
		t.Errorf("history limits = %v, %v; want them unset as authored", cj.Spec.SuccessfulJobsHistoryLimit, cj.Spec.FailedJobsHistoryLimit)
	}
	spec := cj.Spec.JobTemplate.Spec.Template.Spec
	if res := spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the cronjob kind fills no policy default", res)
	}
	if spec.RestartPolicy != "" || spec.AutomountServiceAccountToken != nil {
		t.Errorf("restartPolicy = %q, automountServiceAccountToken = %v; want both unset as authored", spec.RestartPolicy, spec.AutomountServiceAccountToken)
	}
}

// TestCronjobHandler_Refusals: what the component refuses when it is read, with
// no policy involved: a property that is not a CronJobSpec field, at any
// depth; an unauthored schedule or job template; a schedule that is none; a
// time zone the API server refuses; and what the pod kind refuses of a pod
// spec, by its path under the job template.
func TestCronjobHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a batch/v1 CronJobSpec"
	const podPath = "jobTemplate.spec.template.spec."
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	// with returns a component whose pod spec is the plain one plus extra.
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"restartPolicy": "OnFailure", "containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"schedule": "0 2 * * *",
			"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"tier": "batch"}}, "spec": spec}}},
		}
	}
	top := func(extra map[string]any) map[string]any {
		props := with(nil)
		maps.Copy(props, extra)
		return props
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":      {nil, "schedule: required"},
		"no schedule":        {map[string]any{"jobTemplate": with(nil)["jobTemplate"]}, "schedule: required"},
		"no job template":    {map[string]any{"schedule": "0 2 * * *"}, podPath + "containers: required"},
		"a job's field":      {top(map[string]any{"backoffLimit": 1}), notASpec},
		"a workload's image": {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"restartPolicy":      {top(map[string]any{"restartPolicy": "Never"}), notASpec},
		"a history limit":    {top(map[string]any{"successfulJobsHistoryLimit": "three"}), notASpec},
		"template sub-key":   {with(map[string]any{"containerz": []any{}}), notASpec},
		"two spellings":      {top(map[string]any{"suspend": true, "Suspend": false}), "sets the same field as"},
		"an empty concurrencyPolicy": {top(map[string]any{"concurrencyPolicy": ""}),
			"concurrencyPolicy: \"\" cannot be carried by the Kubernetes API types"},
		"a bad schedule":    {top(map[string]any{"schedule": "nightly"}), `invalid cron schedule "nightly"`},
		"time zone Local":   {top(map[string]any{"timeZone": "Local"}), "timeZone: \"Local\" is not a valid IANA time zone name"},
		"time zone empty":   {top(map[string]any{"timeZone": ""}), "timeZone: must not be an empty string"},
		"time zone unknown": {top(map[string]any{"timeZone": "Mars/Olympus"}), "timeZone: \"Mars/Olympus\" is not a valid IANA time zone name"},
		"ephemeral containers": {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}),
			podPath + "ephemeralContainers: not supported"},
		"priority": {with(map[string]any{"priority": 1000}), podPath + "priority: not authorable"},
		"overhead": {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), podPath + "overhead: not authorable"},
		"untagged image": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app"}}}),
			podPath + `containers[0] "app": image "registry.example/team/app" rejected: no tag or digest specified`},
		"probe timing of zero": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3",
			"livenessProbe": map[string]any{"exec": map[string]any{"command": []any{"true"}}, "timeoutSeconds": 0}}}}),
			podPath + "containers[0].livenessProbe.timeoutSeconds: 0 cannot be carried by the Kubernetes API types"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.CronjobHandler{}, "cronjob", "nightly", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCronjobHandler_ScheduleForms: the forms of schedule the API server
// reads are accepted: five fields, an @-descriptor, and @every with a duration.
func TestCronjobHandler_ScheduleForms(t *testing.T) {
	for _, schedule := range []string{"0 2 * * *", "@daily", "@hourly", "@every 1h30m"} {
		props := cronjobLabelled(map[string]any{"tier": "batch"})
		props["schedule"] = schedule
		if err := coreKindErr(&components.CronjobHandler{}, "cronjob", "nightly", props); err != nil {
			t.Errorf("schedule %q refused: %v", schedule, err)
		}
	}
	props := cronjobLabelled(map[string]any{"tier": "batch"})
	props["schedule"] = "@every nope"
	if err := coreKindErr(&components.CronjobHandler{}, "cronjob", "nightly", props); err == nil || !strings.Contains(err.Error(), "invalid @every duration") {
		t.Errorf("schedule \"@every nope\": err = %v, want the duration refusal", err)
	}
}

// TestCronjobHandler_ZerosTheTypeCarries: a history limit of 0 is carried (the
// limits are pointers), and suspend false is too.
func TestCronjobHandler_ZerosTheTypeCarries(t *testing.T) {
	props := ptObject(t, cronjobPlain(htPlainPod))
	props["successfulJobsHistoryLimit"] = 0
	props["failedJobsHistoryLimit"] = 0
	props["suspend"] = false
	cj := cronjobGenerate(t, "nightly", props, ptStrictPolicy())
	for name, got := range map[string]*int32{"successful": cj.Spec.SuccessfulJobsHistoryLimit, "failed": cj.Spec.FailedJobsHistoryLimit} {
		if got == nil || *got != 0 {
			t.Errorf("%s history limit = %v, want an authored 0", name, got)
		}
	}
	if cj.Spec.Suspend == nil || *cj.Spec.Suspend {
		t.Errorf("suspend = %v, want an authored false", cj.Spec.Suspend)
	}
}

// TestCronjobHandler_NameChecks: the CronJob's name is held to the length the
// API server creates one under, at read time.
func TestCronjobHandler_NameChecks(t *testing.T) {
	props := ptObject(t, cronjobPlain(htPlainPod))
	if err := coreKindErr(&components.CronjobHandler{}, "cronjob", strings.Repeat("a", 52), props); err != nil {
		t.Errorf("a 52-character name: %v", err)
	}
	err := coreKindErr(&components.CronjobHandler{}, "cronjob", strings.Repeat("a", 53), props)
	if err == nil || !strings.Contains(err.Error(), "at most 52 characters") {
		t.Fatalf("a 53-character name: err = %v, want the name length refusal", err)
	}
}

// TestCronjobHandler_Policy: a CronJob is held to the policy as every pod
// template is, by the path of the field under the job template, and no replica
// maximum applies to it.
func TestCronjobHandler_Policy(t *testing.T) {
	zero := ptStrictPolicy()
	zero.maxReplicas = int32ptr(0)
	if _, err := pvTransform("cronjob", &components.CronjobHandler{}, ptObject(t, cronjobPlain(htPlainPod)), pvPolicy(zero)); err != nil {
		t.Errorf("a maximum of 0 replicas refused a CronJob: %v", err)
	}
	const podPath = "jobTemplate.spec.template.spec."
	for name, tc := range map[string]struct{ spec, want string }{
		"privileged": {htPlainPod + "    securityContext:\n      privileged: true\n",
			podPath + `containers[0] "app": securityContext.privileged is not allowed by environment policy`},
		"registry": {"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n",
			podPath + `containers[0] "app": image "other.example/team/app:1.2.3" is not from an allowed registry`},
		"cpu": {htPlainPod + "    resources:\n      limits:\n        cpu: 4\n",
			podPath + `containers[0] "app": cpu limit "4" exceeds enforced maximum "2"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("cronjob", &components.CronjobHandler{}, ptObject(t, cronjobPlain(tc.spec)), pvPolicy(ptStrictPolicy()))
			htWantViolation(t, err, `component "web": `+tc.want)
		})
	}
}

// TestCronjobConfig_ServiceAccountName: the CronJob reports the account its
// pods run as to the traits that bind identity to it: the template's
// serviceAccountName, the deprecated serviceAccount where that one is unset,
// and "" for the namespace's default account. It always runs pods.
func TestCronjobConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		podSpec string
		want    string
	}{
		"none authored":      {htPlainPod, ""},
		"serviceAccountName": {htPlainPod + "serviceAccountName: batch\n", "batch"},
		"deprecated alias":   {htPlainPod + "serviceAccount: legacy\n", "legacy"},
		"both":               {htPlainPod + "serviceAccountName: batch\nserviceAccount: legacy\n", "batch"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (&components.CronjobHandler{}).ToApplicationConfig(
				&oam.Component{Name: "nightly", Type: "cronjob", Properties: ptObject(t, cronjobPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the cronjob config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}
