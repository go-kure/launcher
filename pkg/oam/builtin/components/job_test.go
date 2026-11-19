package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestJobHandler_CanHandle(t *testing.T) {
	h := &components.JobHandler{}
	if !h.CanHandle("job") {
		t.Error("CanHandle(job) = false")
	}
	if h.CanHandle("cronjob") {
		t.Error("CanHandle(cronjob) = true")
	}
}

// jobRich is a Job spec that sets the spec-level fields a Job carries, a
// template with metadata beside the labels, and a pod spec with lists of
// objects, a map, pointers, a quantity written as a number and a probe with
// authored non-zero timings.
const jobRich = `parallelism: 2
completions: 4
completionMode: Indexed
backoffLimit: 3
activeDeadlineSeconds: 3600
ttlSecondsAfterFinished: 100
suspend: true
template:
  metadata:
    labels:
      tier: batch
    annotations:
      example.com/scrape: "true"
  spec:
    restartPolicy: Never
    terminationGracePeriodSeconds: 0
    serviceAccountName: batch
    nodeSelector:
      disktype: ssd
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
        readinessProbe:
          exec:
            command: ["true"]
          initialDelaySeconds: 0
          periodSeconds: 5
          timeoutSeconds: 2
          successThreshold: 1
          failureThreshold: 4
`

// jobPlain is the smallest job component: a template whose pod runs the given
// pod spec and never restarts a container in place.
func jobPlain(podSpec string) string {
	return "template:\n  metadata:\n    labels:\n      tier: batch\n  spec:\n    restartPolicy: Never\n" + htIndent(podSpec, "    ")
}

// jobGenerate generates a job component under the given policies and returns
// the Job.
func jobGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *batchv1.Job {
	t.Helper()
	return generateCoreKindUnder(t, &components.JobHandler{}, "job", name, props, policies...).(*batchv1.Job)
}

// TestJobHandler_EmitsAuthoredSpec: the Job is named after the component in
// the build namespace, unlabelled, and its spec is the authored one, field for
// field, with one thing added: the `app` label on the pod template, beside the
// authored labels.
func TestJobHandler_EmitsAuthoredSpec(t *testing.T) {
	var want batchv1.JobSpec
	if err := yaml.UnmarshalStrict([]byte(jobRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "batch"
	job := jobGenerate(t, "batch", ptObject(t, jobRich), ptStrictPolicy(), nil)
	if job.APIVersion != "batch/v1" || job.Kind != "Job" {
		t.Errorf("GVK = %s %s, want batch/v1 Job", job.APIVersion, job.Kind)
	}
	if job.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", job.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(job.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", job.Spec, want)
	}
	if q := job.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; q.String() != "1" {
		t.Errorf("cpu limit = %s, want 1", q.String())
	}
}

// TestJobHandler_AppLabel: the pod template carries the `app` label every
// workload kind gives its pods, with the component's label value: the name
// itself up to 63 characters, its projection beyond. The authored labels stay,
// and no selector is made up.
func TestJobHandler_AppLabel(t *testing.T) {
	for _, name := range []string{"batch", "batch-" + strings.Repeat("a", 40)} {
		job := jobGenerate(t, name, ptObject(t, jobPlain(htPlainPod)), nil)
		want := map[string]string{"tier": "batch", "app": oam.ComponentLabelValue(name)}
		if !maps.Equal(job.Spec.Template.Labels, want) {
			t.Errorf("%s: template labels = %v, want %v", name, job.Spec.Template.Labels, want)
		}
		if job.Spec.Selector != nil || job.Spec.ManualSelector != nil {
			t.Errorf("%s: selector = %+v, manualSelector = %v, want neither: the job controller generates them", name, job.Spec.Selector, job.Spec.ManualSelector)
		}
	}
}

// jobLabelled is a job component whose template carries the given labels, with
// the given selector when it is not nil.
func jobLabelled(selector map[string]any, labels map[string]any) map[string]any {
	metadata := map[string]any{}
	if labels != nil {
		metadata["labels"] = labels
	}
	props := map[string]any{
		"template": map[string]any{
			"metadata": metadata,
			"spec": map[string]any{
				"restartPolicy": "Never",
				"containers": []any{
					map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"},
				},
			},
		},
	}
	if selector != nil {
		props["selector"] = selector
		props["manualSelector"] = true
	}
	return props
}

// TestJobHandler_AuthoredAppLabel: an authored template label `app` is kept
// when it is the component's own label value and refused, naming the component,
// when it is another: traits and Services select the component's pods by that
// label, so it cannot say something else.
func TestJobHandler_AuthoredAppLabel(t *testing.T) {
	value := oam.ComponentLabelValue("batch")
	job := jobGenerate(t, "batch", jobLabelled(nil, map[string]any{"app": value}), nil)
	if want := map[string]string{"app": value}; !maps.Equal(job.Spec.Template.Labels, want) {
		t.Errorf("template labels = %v, want %v", job.Spec.Template.Labels, want)
	}
	for name, authored := range map[string]string{"another value": "frontend", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.JobHandler{}, "job", "batch", jobLabelled(nil, map[string]any{"tier": "batch", "app": authored}))
			want := "template.metadata.labels.app: " + `"` + authored + `"` + " is not the `app` label of component \"batch\""
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one mentioning %q", err, want)
			}
		})
	}
}

// TestJobHandler_SelectorAndTheAppLabel: a Job takes a selector only as the
// author's own (manualSelector). One that matches the authored template labels
// and stops matching once the `app` label is on them is refused. One on the
// `app` label's own value is satisfied by the label launcher adds. One that
// does not match the authored labels either is the API server's to refuse. One
// that cannot be read as a selector is refused with apimachinery's reason.
func TestJobHandler_SelectorAndTheAppLabel(t *testing.T) {
	const rulesOut = "selector: rules out the label `app: batch`, which launcher sets on the pods of component \"batch\""
	expr := func(op string, values ...any) map[string]any {
		e := map[string]any{"key": "app", "operator": op}
		if values != nil {
			e["values"] = values
		}
		return map[string]any{"matchExpressions": []any{e}}
	}
	for name, tc := range map[string]struct {
		selector map[string]any
		labels   map[string]any
		want     string // "" when the component builds
	}{
		"app must not exist":              {expr("DoesNotExist"), map[string]any{"tier": "batch"}, rulesOut},
		"app not in the label value":      {expr("NotIn", "batch"), map[string]any{"tier": "batch"}, rulesOut},
		"app not in another value":        {expr("NotIn", "db"), map[string]any{"tier": "batch"}, ""},
		"app is the label value":          {map[string]any{"matchLabels": map[string]any{"app": "batch"}}, nil, ""},
		"no match on the authored labels": {map[string]any{"matchLabels": map[string]any{"tier": "db"}}, map[string]any{"tier": "batch"}, ""},
		"an unknown operator":             {expr("Bogus"), map[string]any{"tier": "batch"}, `selector: "Bogus" is not a valid label selector operator`},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.JobHandler{}, "job", "batch", jobLabelled(tc.selector, tc.labels))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestJobHandler_FillsNothing: a policy that carries resource defaults fills
// none of them, a pod with no service account keeps the cluster's default for
// the token mount, and no restart policy is made up: the Job API refuses a
// template without one when the object is applied.
func TestJobHandler_FillsNothing(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultStorageSize: "1Gi",
	}
	props := ptObject(t, "template:\n  metadata:\n    labels:\n      tier: batch\n  spec:\n"+htIndent(htPlainPod, "    "))
	job := jobGenerate(t, "batch", props, defaults)
	spec := job.Spec.Template.Spec
	if res := spec.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the job kind fills no policy default", res)
	}
	if spec.RestartPolicy != "" {
		t.Errorf("restartPolicy = %q, want it unset as authored", spec.RestartPolicy)
	}
	if spec.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want it unset as authored", *spec.AutomountServiceAccountToken)
	}
}

// TestJobHandler_Refusals: what the component refuses when it is read, with no
// policy involved: a property that is not a JobSpec field, at any depth; an
// unauthored template; and what the pod kind refuses of a pod spec, by its
// path under the template.
func TestJobHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a batch/v1 JobSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	// with returns a component whose pod spec is the plain one plus extra.
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"restartPolicy": "Never", "containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "batch"}}, "spec": spec}}
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
		"no properties":        {nil, "template.spec.containers: required"},
		"no template":          {map[string]any{"backoffLimit": 1}, "template.spec.containers: required"},
		"a workload's image":   {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"a cronjob's field":    {top(map[string]any{"schedule": "0 2 * * *"}), notASpec},
		"restartPolicy at top": {top(map[string]any{"restartPolicy": "Never"}), notASpec},
		"backoffLimit string":  {top(map[string]any{"backoffLimit": "three"}), notASpec},
		"template sub-key":     {with(map[string]any{"containerz": []any{}}), notASpec},
		"two spellings":        {top(map[string]any{"backoffLimit": 1, "BackoffLimit": 2}), "sets the same field as"},
		"ephemeral containers": {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}),
			"template.spec.ephemeralContainers: not supported"},
		"priority": {with(map[string]any{"priority": 1000}), "template.spec.priority: not authorable"},
		"overhead": {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), "template.spec.overhead: not authorable"},
		"untagged image": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app"}}}),
			`template.spec.containers[0] "app": image "registry.example/team/app" rejected: no tag or digest specified`},
		"latest init image": {with(map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "busybox:latest"}}}),
			`template.spec.initContainers[0] "init": image "busybox:latest" rejected: :latest tag not allowed`},
		"probe timing of zero": {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3",
			"livenessProbe": map[string]any{"exec": map[string]any{"command": []any{"true"}}, "timeoutSeconds": 0}}}}),
			"template.spec.containers[0].livenessProbe.timeoutSeconds: 0 cannot be carried by the Kubernetes API types"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.JobHandler{}, "job", "batch", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestJobHandler_ActiveDeadlineSeconds: a Job may set it, on the spec and on
// the pod template alike; only the controllers that keep their pods running
// may not.
func TestJobHandler_ActiveDeadlineSeconds(t *testing.T) {
	props := ptObject(t, "activeDeadlineSeconds: 600\n"+jobPlain(htPlainPod+"activeDeadlineSeconds: 300\n"))
	job := jobGenerate(t, "batch", props, ptStrictPolicy())
	if got := job.Spec.ActiveDeadlineSeconds; got == nil || *got != 600 {
		t.Errorf("spec.activeDeadlineSeconds = %v, want 600", got)
	}
	if got := job.Spec.Template.Spec.ActiveDeadlineSeconds; got == nil || *got != 300 {
		t.Errorf("template.spec.activeDeadlineSeconds = %v, want 300", got)
	}
}

// TestJobHandler_NameChecks: the Job's name is held to the length the API
// server creates one under, and an Indexed job to the hostname its last pod
// takes, at read time and again when the object is generated.
func TestJobHandler_NameChecks(t *testing.T) {
	long := strings.Repeat("a", 64)
	err := coreKindErr(&components.JobHandler{}, "job", long, ptObject(t, jobPlain(htPlainPod)))
	if err == nil || !strings.Contains(err.Error(), "at most 63 characters") {
		t.Fatalf("a 64-character name: err = %v, want the name length refusal", err)
	}
	indexed := ptObject(t, "completionMode: Indexed\ncompletions: 100\n"+jobPlain(htPlainPod))
	// 58 characters, so the hostname "<name>-99" is 61: a DNS-1123 label.
	if err := coreKindErr(&components.JobHandler{}, "job", strings.Repeat("a", 58), indexed); err != nil {
		t.Errorf("an Indexed job whose last pod hostname fits: %v", err)
	}
	// 61 characters, so the hostname "<name>-99" is 64: too long.
	err = coreKindErr(&components.JobHandler{}, "job", strings.Repeat("a", 61), indexed)
	if err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("an Indexed job whose last pod hostname is too long: err = %v, want the hostname refusal", err)
	}
}

// TestJobHandler_Policy: a Job is held to the policy as every pod template is,
// by the path of the field under the template, and no replica maximum applies
// to it.
func TestJobHandler_Policy(t *testing.T) {
	zero := ptStrictPolicy()
	zero.maxReplicas = int32ptr(0)
	if _, err := pvTransform("job", &components.JobHandler{}, ptObject(t, jobPlain(htPlainPod)), pvPolicy(zero)); err != nil {
		t.Errorf("a maximum of 0 replicas refused a Job: %v", err)
	}
	for name, tc := range map[string]struct{ spec, want string }{
		"privileged": {htPlainPod + "    securityContext:\n      privileged: true\n",
			`template.spec.containers[0] "app": securityContext.privileged is not allowed by environment policy`},
		"registry": {"containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n",
			`template.spec.containers[0] "app": image "other.example/team/app:1.2.3" is not from an allowed registry`},
		"cpu": {htPlainPod + "    resources:\n      limits:\n        cpu: 4\n",
			`template.spec.containers[0] "app": cpu limit "4" exceeds enforced maximum "2"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("job", &components.JobHandler{}, ptObject(t, jobPlain(tc.spec)), pvPolicy(ptStrictPolicy()))
			htWantViolation(t, err, `component "web": `+tc.want)
		})
	}
}

// TestJobConfig_ServiceAccountName: the Job reports the account its pods run
// as to the traits that bind identity to it: the template's serviceAccountName,
// the deprecated serviceAccount where that one is unset, and "" for the
// namespace's default account. It always runs pods.
func TestJobConfig_ServiceAccountName(t *testing.T) {
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
			cfg, err := (&components.JobHandler{}).ToApplicationConfig(
				&oam.Component{Name: "batch", Type: "job", Properties: ptObject(t, jobPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the job config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}
