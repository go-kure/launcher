package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// findJob returns the *batchv1.Job from objs, failing the test if none is
// present — the job counterpart of findCronJob above.
func findJob(t *testing.T, objs []*client.Object) *batchv1.Job {
	t.Helper()
	for _, obj := range objs {
		if j, ok := (*obj).(*batchv1.Job); ok {
			return j
		}
	}
	t.Fatal("Job not found")
	return nil
}

// generateJob builds a job component from props and returns the generated Job
// alongside every object generated with it.
func generateJob(t *testing.T, props map[string]any) (*batchv1.Job, []*client.Object) {
	t.Helper()
	full := map[string]any{"image": "ghcr.io/org/batch:v1.0.0"}
	for k, v := range props {
		full[k] = v
	}
	h := &components.JobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "batch", Type: "job", Properties: full,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("batch", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return findJob(t, objs), objs
}

// jobError builds a job component from props and returns the error
// ToApplicationConfig refused it with, failing the test if it accepted.
func jobError(t *testing.T, props map[string]any) string {
	t.Helper()
	full := map[string]any{"image": "ghcr.io/org/batch:v1.0.0"}
	for k, v := range props {
		full[k] = v
	}
	h := &components.JobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "batch", Type: "job", Properties: full,
	}, "default")
	if err == nil {
		t.Fatalf("ToApplicationConfig accepted %v, want an error", props)
	}
	return err.Error()
}

func TestJobHandler_CanHandle(t *testing.T) {
	h := &components.JobHandler{}
	if !h.CanHandle("job") {
		t.Error("expected true for job")
	}
	if h.CanHandle("cronjob") {
		t.Error("expected false for cronjob")
	}
}

func TestJobHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.JobHandler{}
	if _, err := h.ToApplicationConfig(&oam.Component{
		Name: "batch", Type: "job", Properties: map[string]any{},
	}, "default"); err == nil {
		t.Fatal("expected error for missing image")
	}
}

// TestJobHandler_Generate_Shape pins what the generated Job looks like before
// any JobSpec-level property is authored: the constructor's `app:` annotation is
// dropped, both label maps are the component's own, the restart policy defaults
// to OnFailure (the Job API rejects the Always that pod defaulting would
// otherwise supply), and Spec.Selector is left unset for the job controller to
// generate.
func TestJobHandler_Generate_Shape(t *testing.T) {
	job, objs := generateJob(t, nil)

	if job.Annotations != nil {
		t.Errorf("Annotations = %v, want nil", job.Annotations)
	}
	if got := job.Labels["app"]; got != "batch" {
		t.Errorf("Labels[app] = %q, want %q", got, "batch")
	}
	if got := job.Spec.Template.Labels["app"]; got != "batch" {
		t.Errorf("Spec.Template.Labels[app] = %q, want %q", got, "batch")
	}
	if job.Spec.Selector != nil {
		t.Errorf("Spec.Selector = %v, want nil — the job controller generates it", job.Spec.Selector)
	}
	if job.Spec.ManualSelector != nil {
		t.Errorf("Spec.ManualSelector = %v, want nil", job.Spec.ManualSelector)
	}
	if got := job.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyOnFailure {
		t.Errorf("RestartPolicy = %q, want %q", got, corev1.RestartPolicyOnFailure)
	}
	if got := len(job.Spec.Template.Spec.Containers); got != 1 {
		t.Fatalf("Containers = %d, want 1", got)
	}
	if got := job.Spec.Template.Spec.Containers[0].Image; got != "ghcr.io/org/batch:v1.0.0" {
		t.Errorf("Image = %q, want the authored image", got)
	}

	var sawServiceAccount bool
	for _, obj := range objs {
		if _, ok := (*obj).(*corev1.ServiceAccount); ok {
			sawServiceAccount = true
		}
	}
	if !sawServiceAccount {
		t.Error("Generate did not emit a ServiceAccount")
	}

	// Nothing unauthored may reach the spec: every JobSpec-level pointer stays
	// nil so the emitted YAML carries no key the author did not write.
	if job.Spec.BackoffLimit != nil || job.Spec.Completions != nil || job.Spec.Parallelism != nil ||
		job.Spec.CompletionMode != nil || job.Spec.Suspend != nil || job.Spec.ManagedBy != nil ||
		job.Spec.SuccessPolicy != nil || job.Spec.PodReplacementPolicy != nil {
		t.Errorf("unauthored JobSpec fields were written: %+v", job.Spec)
	}
}

func TestJobHandler_RestartPolicy_Never(t *testing.T) {
	job, _ := generateJob(t, map[string]any{"restartPolicy": "Never"})
	if got := job.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want %q", got, corev1.RestartPolicyNever)
	}
	if msg := jobError(t, map[string]any{"restartPolicy": "Always"}); !strings.Contains(msg, "restartPolicy") {
		t.Errorf("error = %q, want it to name restartPolicy", msg)
	}
	// A mistyped or empty value must be refused, not read as an omission that
	// silently builds the OnFailure default. Both were reachable while this key
	// was read with a bare type assertion.
	if msg := jobError(t, map[string]any{"restartPolicy": 3}); !strings.Contains(msg, "restartPolicy: must be a string") {
		t.Errorf("error = %q, want it to reject a non-string restartPolicy", msg)
	}
	if msg := jobError(t, map[string]any{"restartPolicy": ""}); !strings.Contains(msg, "restartPolicy") {
		t.Errorf("error = %q, want it to reject an empty restartPolicy", msg)
	}
}

// TestJobHandler_SuspendWritesJobSpec is the job half of the `suspend` key
// split: the job component's `suspend` is batchv1.JobSpec.Suspend, a different
// API field from the CronJobSpec.Suspend the cronjob component publishes under
// the same name. The cronjob half is
// TestCronjobHandler_SuspendWritesCronJobSpecOnly.
func TestJobHandler_SuspendWritesJobSpec(t *testing.T) {
	job, _ := generateJob(t, map[string]any{"suspend": true})
	if job.Spec.Suspend == nil {
		t.Fatal("Spec.Suspend = nil, want true")
	}
	if !*job.Spec.Suspend {
		t.Error("Spec.Suspend = false, want true")
	}
}

// TestJobHandler_SharedJobSpecFields_Projected covers the six JobSpec-level
// properties the job component shares with cronjob's jobTemplate, proving the
// shared parser/applier reaches a Job spec directly and not only a
// jobTemplate's.
func TestJobHandler_SharedJobSpecFields_Projected(t *testing.T) {
	job, _ := generateJob(t, map[string]any{
		"backoffLimit":            2,
		"completions":             4,
		"parallelism":             3,
		"activeDeadlineSeconds":   120,
		"ttlSecondsAfterFinished": 30,
		"completionMode":          "Indexed",
	})
	if got := job.Spec.BackoffLimit; got == nil || *got != 2 {
		t.Errorf("BackoffLimit = %v, want 2", got)
	}
	if got := job.Spec.Completions; got == nil || *got != 4 {
		t.Errorf("Completions = %v, want 4", got)
	}
	if got := job.Spec.Parallelism; got == nil || *got != 3 {
		t.Errorf("Parallelism = %v, want 3", got)
	}
	if got := job.Spec.ActiveDeadlineSeconds; got == nil || *got != 120 {
		t.Errorf("ActiveDeadlineSeconds = %v, want 120", got)
	}
	if got := job.Spec.TTLSecondsAfterFinished; got == nil || *got != 30 {
		t.Errorf("TTLSecondsAfterFinished = %v, want 30", got)
	}
	if got := job.Spec.CompletionMode; got == nil || *got != batchv1.IndexedCompletion {
		t.Errorf("CompletionMode = %v, want Indexed", got)
	}
}

// TestJobHandler_IndexedJobSpecFields_Projected covers the five JobSpec-level
// properties added with this component. All but managedBy and
// podReplacementPolicy need completionMode: Indexed, so they are authored
// together.
func TestJobHandler_IndexedJobSpecFields_Projected(t *testing.T) {
	job, _ := generateJob(t, map[string]any{
		"completionMode":       "Indexed",
		"completions":          10,
		"backoffLimitPerIndex": 1,
		"maxFailedIndexes":     3,
		"podReplacementPolicy": "TerminatingOrFailed",
		"managedBy":            "example.com/custom-controller",
		"successPolicy": map[string]any{
			"rules": []any{
				map[string]any{"succeededIndexes": "0-4", "succeededCount": 3},
				map[string]any{"succeededCount": 8},
			},
		},
	})
	if got := job.Spec.BackoffLimitPerIndex; got == nil || *got != 1 {
		t.Errorf("BackoffLimitPerIndex = %v, want 1", got)
	}
	if got := job.Spec.MaxFailedIndexes; got == nil || *got != 3 {
		t.Errorf("MaxFailedIndexes = %v, want 3", got)
	}
	if got := job.Spec.PodReplacementPolicy; got == nil || *got != batchv1.TerminatingOrFailed {
		t.Errorf("PodReplacementPolicy = %v, want TerminatingOrFailed", got)
	}
	if got := job.Spec.ManagedBy; got == nil || *got != "example.com/custom-controller" {
		t.Errorf("ManagedBy = %v, want example.com/custom-controller", got)
	}
	sp := job.Spec.SuccessPolicy
	if sp == nil {
		t.Fatal("SuccessPolicy = nil, want two rules")
	}
	if len(sp.Rules) != 2 {
		t.Fatalf("SuccessPolicy.Rules = %d, want 2", len(sp.Rules))
	}
	if got := sp.Rules[0].SucceededIndexes; got == nil || *got != "0-4" {
		t.Errorf("Rules[0].SucceededIndexes = %v, want 0-4", got)
	}
	if got := sp.Rules[0].SucceededCount; got == nil || *got != 3 {
		t.Errorf("Rules[0].SucceededCount = %v, want 3", got)
	}
	if sp.Rules[1].SucceededIndexes != nil {
		t.Errorf("Rules[1].SucceededIndexes = %v, want nil", sp.Rules[1].SucceededIndexes)
	}
	if got := sp.Rules[1].SucceededCount; got == nil || *got != 8 {
		t.Errorf("Rules[1].SucceededCount = %v, want 8", got)
	}
}

// TestJobHandler_SelectorProperties_Rejected pins the two builder-managed
// properties. The Job selector is generated by the job controller from a unique
// per-job label; a hand-written one adopts other jobs' pods, which is why this
// component refuses the key with an explaining message rather than leaving it
// undeclared for a generic "unrecognized key".
func TestJobHandler_SelectorProperties_Rejected(t *testing.T) {
	cases := []struct {
		key        string
		value      any
		wantSubstr string
	}{
		{"selector", map[string]any{"matchLabels": map[string]any{"app": "batch"}}, "not authorable"},
		{"manualSelector", true, "not authorable"},
		// createJob replaces the pod template wholesale from the component's
		// own properties, so an authored one is not merged, it is discarded.
		// The deployment kind rejects this key for the same reason.
		{"template", map[string]any{"spec": map[string]any{}}, "not authorable"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			msg := jobError(t, map[string]any{tc.key: tc.value})
			if !strings.Contains(msg, tc.key) || !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("error = %q, want it to name %q and %q", msg, tc.key, tc.wantSubstr)
			}
		})
	}
}

// TestJobHandler_PropertySchema_Keys pins the published contract: every
// JobSpec-level key is present and `suspend` is the job component's own.
func TestJobHandler_PropertySchema_Keys(t *testing.T) {
	schema := (&components.JobHandler{}).PropertySchema()

	for _, key := range []string{
		"image", "restartPolicy", "suspend",
		"backoffLimit", "completions", "parallelism", "activeDeadlineSeconds",
		"ttlSecondsAfterFinished", "completionMode", "backoffLimitPerIndex",
		"maxFailedIndexes", "podReplacementPolicy", "managedBy", "successPolicy",
		"podFailurePolicy",
	} {
		node, ok := schema[key]
		if !ok {
			t.Errorf("PropertySchema() missing key %q", key)
			continue
		}
		if node.Description == "" {
			t.Errorf("%s: Description is empty", key)
		}
	}
	if _, ok := schema["selector"]; ok {
		t.Error("PropertySchema() publishes selector; it is builder-managed")
	}
	if got := schema["suspend"].Type; got != oam.PropertyTypeBoolean {
		t.Errorf("suspend: Type = %q, want boolean", got)
	}
	if got := schema["schedule"]; got.Type != "" {
		t.Error("PropertySchema() publishes schedule; that is the cronjob component's key")
	}
}

// TestJobHandler_JobSpecValidation_Table walks the cross-field and per-field
// rules ported from ValidateJobSpec. Each case is a document Kubernetes' own API
// server would reject at apply time, refused here at build time instead.
func TestJobHandler_JobSpecValidation_Table(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		wantSubstr string
	}{
		// Both substrings name their field. An unqualified "requires
		// completionMode: Indexed" would be satisfied by either arm, so the
		// maxFailedIndexes case would pass even with no maxFailedIndexes rule at
		// all — it authors backoffLimitPerIndex too, because maxFailedIndexes
		// without it is refused earlier by a different rule.
		{
			"backoffLimitPerIndex outside Indexed",
			map[string]any{"backoffLimitPerIndex": 1},
			"backoffLimitPerIndex: requires completionMode: Indexed",
		},
		{
			"maxFailedIndexes outside Indexed",
			map[string]any{"backoffLimitPerIndex": 1, "maxFailedIndexes": 1},
			"maxFailedIndexes: requires completionMode: Indexed",
		},
		{
			"successPolicy outside Indexed",
			map[string]any{"successPolicy": map[string]any{"rules": []any{map[string]any{"succeededCount": 1}}}},
			"successPolicy: requires completionMode: Indexed",
		},
		{
			"maxFailedIndexes without backoffLimitPerIndex",
			map[string]any{"completionMode": "Indexed", "completions": 5, "maxFailedIndexes": 2},
			"backoffLimitPerIndex: required when maxFailedIndexes is specified",
		},
		{
			"maxFailedIndexes above completions",
			map[string]any{"completionMode": "Indexed", "completions": 5, "backoffLimitPerIndex": 1, "maxFailedIndexes": 6},
			"must be <= completions (5)",
		},
		{
			"negative backoffLimitPerIndex",
			map[string]any{"completionMode": "Indexed", "completions": 5, "backoffLimitPerIndex": -1},
			"backoffLimitPerIndex: must be >= 0",
		},
		{
			"negative maxFailedIndexes",
			map[string]any{"completionMode": "Indexed", "completions": 5, "backoffLimitPerIndex": 1, "maxFailedIndexes": -1},
			"maxFailedIndexes: must be >= 0",
		},
		{
			"unknown podReplacementPolicy",
			map[string]any{"podReplacementPolicy": "Whenever"},
			"podReplacementPolicy: invalid value",
		},
		{
			// "" must be refused, not treated as an omission: parseStringField
			// reports an authored empty string as absent, so reading this key
			// through it would drop the authored value and emit a Job with no
			// podReplacementPolicy at all. Upstream's supported set has no
			// empty member, so the document would not have applied either.
			"empty podReplacementPolicy",
			map[string]any{"podReplacementPolicy": ""},
			"podReplacementPolicy: invalid value",
		},
		{
			"non-string podReplacementPolicy",
			map[string]any{"podReplacementPolicy": 3},
			"podReplacementPolicy: must be a string",
		},
		{
			"managedBy without a domain prefix",
			map[string]any{"managedBy": "controller"},
			"must be a domain-prefixed path",
		},
		{
			"managedBy empty",
			map[string]any{"managedBy": ""},
			"must be a domain-prefixed path",
		},
		{
			"managedBy above 63 characters",
			map[string]any{"managedBy": "example.com/" + strings.Repeat("a", 52)},
			"at most 63 characters",
		},
		{
			"successPolicy with no rules key",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{}},
			"successPolicy.rules: required",
		},
		{
			"successPolicy with an empty rules list",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{}}},
			"at least one rule",
		},
		{
			"successPolicy rule with neither field",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{}}}},
			"at least one of succeededIndexes or succeededCount",
		},
		{
			// "" denotes no indexes at all, so without an explicit refusal it
			// would satisfy the "at least one of" check above while naming
			// nothing, and validateJobIndexesFormat("") returns (0, nil) rather
			// than an error — a rule that can never be satisfied, accepted.
			"successPolicy rule with an empty succeededIndexes",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededIndexes": ""}}}},
			"succeededIndexes: must not be empty",
		},
		{
			"successPolicy rule with an unknown key",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededCount": 1, "bogus": 2}}}},
			`unrecognized key "bogus"`,
		},
		{
			"succeededCount above completions",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededCount": 6}}}},
			"must be <= completions (5)",
		},
		{
			"succeededIndexes index at completions",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededIndexes": "5"}}}},
			"must be less than completions (5)",
		},
		{
			"succeededIndexes out of order",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededIndexes": "3,1"}}}},
			"increasing order",
		},
		{
			"succeededIndexes with a non-numeric fragment",
			map[string]any{"completionMode": "Indexed", "completions": 5, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededIndexes": "a-b"}}}},
			"is not a decimal index",
		},
		{
			"succeededCount above the indexes it is scoped to",
			map[string]any{"completionMode": "Indexed", "completions": 9, "successPolicy": map[string]any{"rules": []any{map[string]any{"succeededIndexes": "0-1", "succeededCount": 3}}}},
			"indexes named by succeededIndexes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := jobError(t, tc.props); !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.wantSubstr)
			}
		})
	}
}

// TestJobHandler_HighCompletionsRules covers the three bounds that only apply
// above completionsSoftLimit (100000) with backoffLimitPerIndex set. They are
// separated from the table above because each needs a six-figure completions
// value, which would otherwise make every neighbouring case harder to read.
func TestJobHandler_HighCompletionsRules(t *testing.T) {
	base := map[string]any{"completionMode": "Indexed", "completions": 100001, "backoffLimitPerIndex": 1}
	with := func(extra map[string]any) map[string]any {
		props := map[string]any{}
		for k, v := range base {
			props[k] = v
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}

	if msg := jobError(t, with(nil)); !strings.Contains(msg, "maxFailedIndexes: required when completions is above 100000") {
		t.Errorf("error = %q, want the required-maxFailedIndexes message", msg)
	}
	if msg := jobError(t, with(map[string]any{"maxFailedIndexes": 10001})); !strings.Contains(msg, "must be <= 10000 when completions is above 100000") {
		t.Errorf("error = %q, want the maxFailedIndexes ceiling message", msg)
	}
	if msg := jobError(t, with(map[string]any{"maxFailedIndexes": 10000, "parallelism": 10001})); !strings.Contains(msg, "parallelism: must be <= 10000 when completions is above 100000") {
		t.Errorf("error = %q, want the parallelism ceiling message", msg)
	}

	// The same document within the bounds must still build, so the three
	// messages above are refusals of the value, not of the shape.
	job, _ := generateJob(t, with(map[string]any{"maxFailedIndexes": 10000, "parallelism": 10000}))
	if got := job.Spec.MaxFailedIndexes; got == nil || *got != 10000 {
		t.Errorf("MaxFailedIndexes = %v, want 10000", got)
	}
}

// TestJobHandler_DottedComponentName_Refused pins the one place the component
// name's three destinations disagree. `metadata.name` and the `app:` label both
// accept a dotted name — validateComponent enforces a DNS-1123 *subdomain* — but
// corev1.Container.Name is a DNS-1123 *label*, which forbids dots. So a valid
// component name used to build a valid-looking Job the API server refuses at
// admission, naming a field the author never wrote.
//
// This test goes through Generate rather than ToApplicationConfig: the name
// checked is the one actually emitted, not the one parsed.
func TestJobHandler_DottedComponentName_Refused(t *testing.T) {
	cfg, err := (&components.JobHandler{}).ToApplicationConfig(&oam.Component{
		Name: "batch.worker", Type: "job",
		Properties: map[string]any{"image": "ghcr.io/org/batch:v1.0.0"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	_, err = cfg.Generate(stack.NewApplication("batch.worker", "default", cfg))
	if err == nil {
		t.Fatal("Generate accepted the dotted component name 'batch.worker', want a refusal — " +
			"the emitted container name would be rejected at admission")
	}
	for _, want := range []string{"batch.worker", "container name", "DNS-1123 label"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	// The undotted name must still build, so the refusal is of the dot and not
	// of every name that reaches this path. generateJob fails the test itself
	// if Generate refuses "batch".
	generateJob(t, nil)
}

// TestJobHandler_CronJobOnlyProperties_Rejected covers the retyped-cronjob case.
// A job component and a cronjob component differ by exactly these six
// properties, so changing `type: cronjob` to `type: job` on an existing document
// leaves every one of them behind. Left undeclared they would each be dropped in
// silence — validateProperties runs on emitted elements only, never on authored
// documents — turning a schedule into a Job that runs once, immediately, at
// apply time. That is the shape this test exists to prevent: not a missing
// field, but work executed at a time nobody asked for.
func TestJobHandler_CronJobOnlyProperties_Rejected(t *testing.T) {
	cases := []struct {
		key        string
		value      any
		wantSubstr string
	}{
		{"schedule", "*/5 * * * *", "cronjob component"},
		{"timeZone", "Europe/Brussels", "cronjob"},
		{"concurrencyPolicy", "Forbid", "cronjob"},
		// Each of these three names the job-level property that does the
		// nearest equivalent job, so the message is a redirection rather than
		// only a refusal.
		{"startingDeadlineSeconds", 30, "activeDeadlineSeconds"},
		{"successfulJobsHistoryLimit", 3, "ttlSecondsAfterFinished"},
		{"failedJobsHistoryLimit", 1, "ttlSecondsAfterFinished"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			msg := jobError(t, map[string]any{tc.key: tc.value})
			if !strings.Contains(msg, tc.key) || !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("error = %q, want it to name %q and %q", msg, tc.key, tc.wantSubstr)
			}
		})
	}
}

// TestJobHandler_SuspendVetoesAutoHealthCheck is the job counterpart of
// deployment's paused veto. A suspended job creates no pods, so it reaches
// neither Complete nor Failed and a Kustomization waiting on it would block for
// as long as the document says to stay suspended.
//
// The pipeline reaches this method by type assertion, so the assertion below is
// the load-bearing half: a method whose name or signature drifts stops being
// seen there without any call site failing to compile.
func TestJobHandler_SuspendVetoesAutoHealthCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  bool
	}{
		{"suspend true vetoes the check", map[string]any{"suspend": true}, false},
		{"suspend false keeps it", map[string]any{"suspend": false}, true},
		{"suspend unauthored keeps it", map[string]any{}, true},
		// parallelism: 0 reaches the same dead end by a different route — zero
		// is the maximum pods the job may run, so no completion ever accrues.
		// Unlike suspension it does not stop the ActiveDeadlineSeconds timer,
		// so a deadline makes Failed reachable and the check meaningful again.
		{"zero parallelism vetoes the check", map[string]any{"parallelism": 0}, false},
		{"zero parallelism with a deadline keeps it", map[string]any{"parallelism": 0, "activeDeadlineSeconds": 60}, true},
		{"nonzero parallelism keeps it", map[string]any{"parallelism": 1}, true},
		{"zero parallelism on a suspended job still vetoes", map[string]any{"parallelism": 0, "activeDeadlineSeconds": 60, "suspend": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := map[string]any{"image": "ghcr.io/org/batch:v1.0.0"}
			for k, v := range tc.props {
				full[k] = v
			}
			cfg, err := (&components.JobHandler{}).ToApplicationConfig(&oam.Component{
				Name: "batch", Type: "job", Properties: full,
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			e, ok := cfg.(interface{ EmitsAutoHealthCheck() bool })
			if !ok {
				t.Fatal("JobConfig does not satisfy the autoHealthCheckEmitter shape the transform asserts on")
			}
			if got := e.EmitsAutoHealthCheck(); got != tc.want {
				t.Errorf("EmitsAutoHealthCheck() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestJobHandler_ExplicitNullReadsAsOmission pins the null handling on the
// properties go-kure/launcher#344 introduced. `key: null` in YAML is an author
// writing "leave this unset" — pkg/oam's own validatePropertyValue reads it that
// way, and the optional* wrappers in common.go exist to make the parsers agree.
// Without them a null reaches a typed helper and is refused as a type error, so
// a document that says nothing is rejected while the same document with the key
// deleted builds.
//
// backoffLimitPerIndex and maxFailedIndexes carry the sharper half: both are
// refused outright unless completionMode is Indexed, so reading a null as a
// value rather than an omission would make `backoffLimitPerIndex: null` fail
// with a cross-field message about a field the author explicitly declined to
// set.
func TestJobHandler_ExplicitNullReadsAsOmission(t *testing.T) {
	props := map[string]any{
		"restartPolicy":        nil,
		"suspend":              nil,
		"backoffLimitPerIndex": nil,
		"maxFailedIndexes":     nil,
		"podReplacementPolicy": nil,
		"managedBy":            nil,
		"successPolicy":        nil,
	}
	job, _ := generateJob(t, props)

	if got := job.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyOnFailure {
		t.Errorf("RestartPolicy = %q, want the OnFailure default a null must not disturb", got)
	}
	if got := job.Spec.Suspend; got != nil {
		t.Errorf("Suspend = %v, want nil", *got)
	}
	if got := job.Spec.BackoffLimitPerIndex; got != nil {
		t.Errorf("BackoffLimitPerIndex = %v, want nil", *got)
	}
	if got := job.Spec.MaxFailedIndexes; got != nil {
		t.Errorf("MaxFailedIndexes = %v, want nil", *got)
	}
	if got := job.Spec.PodReplacementPolicy; got != nil {
		t.Errorf("PodReplacementPolicy = %q, want nil", *got)
	}
	if got := job.Spec.ManagedBy; got != nil {
		t.Errorf("ManagedBy = %q, want nil", *got)
	}
	if got := job.Spec.SuccessPolicy; got != nil {
		t.Errorf("SuccessPolicy = %+v, want nil", got)
	}
}

// TestJobHandler_SuccessPolicyMemberNullsReadAsOmission covers the two keys
// nested inside a successPolicy rule, which the loop above cannot reach: a null
// at the top level omits the whole policy, so the rule members are only
// exercised by a policy that is authored. Each rule needs at least one of the
// two, so the cases are one-sided rather than both-null.
func TestJobHandler_SuccessPolicyMemberNullsReadAsOmission(t *testing.T) {
	rule := func(members map[string]any) map[string]any {
		return map[string]any{
			"completionMode": "Indexed",
			"completions":    4,
			"successPolicy":  map[string]any{"rules": []any{members}},
		}
	}

	t.Run("null succeededCount", func(t *testing.T) {
		job, _ := generateJob(t, rule(map[string]any{"succeededIndexes": "0-1", "succeededCount": nil}))
		got := job.Spec.SuccessPolicy
		if got == nil || len(got.Rules) != 1 {
			t.Fatalf("SuccessPolicy = %+v, want one rule", got)
		}
		if got.Rules[0].SucceededCount != nil {
			t.Errorf("SucceededCount = %v, want nil", *got.Rules[0].SucceededCount)
		}
		if got.Rules[0].SucceededIndexes == nil || *got.Rules[0].SucceededIndexes != "0-1" {
			t.Errorf("SucceededIndexes = %v, want 0-1", got.Rules[0].SucceededIndexes)
		}
	})

	t.Run("null succeededIndexes", func(t *testing.T) {
		job, _ := generateJob(t, rule(map[string]any{"succeededIndexes": nil, "succeededCount": 2}))
		got := job.Spec.SuccessPolicy
		if got == nil || len(got.Rules) != 1 {
			t.Fatalf("SuccessPolicy = %+v, want one rule", got)
		}
		if got.Rules[0].SucceededIndexes != nil {
			t.Errorf("SucceededIndexes = %q, want nil", *got.Rules[0].SucceededIndexes)
		}
		if got.Rules[0].SucceededCount == nil || *got.Rules[0].SucceededCount != 2 {
			t.Errorf("SucceededCount = %v, want 2", got.Rules[0].SucceededCount)
		}
	})
}

// jobGenerateError builds a job component from props, generates it, and returns
// the error Generate refused it with — failing the test if either step
// unexpectedly succeeded. jobError above only exercises ToApplicationConfig, so
// it cannot see the checks that run against the built pod template.
func jobGenerateError(t *testing.T, props map[string]any) string {
	t.Helper()
	full := map[string]any{"image": "ghcr.io/org/batch:v1.0.0"}
	for k, v := range props {
		full[k] = v
	}
	cfg, err := (&components.JobHandler{}).ToApplicationConfig(&oam.Component{
		Name: "batch", Type: "job", Properties: full,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig refused the document before Generate could: %v", err)
	}
	if _, err := cfg.Generate(stack.NewApplication("batch", "default", cfg)); err != nil {
		return err.Error()
	}
	t.Fatal("Generate accepted the document, want a refusal")
	return ""
}

// podFailurePolicyProps wraps rules in the surrounding properties every accepted
// podFailurePolicy document needs: restartPolicy: Never, which upstream requires
// alongside the field and which this component would otherwise default to
// OnFailure.
func podFailurePolicyProps(rules []any, extra map[string]any) map[string]any {
	props := map[string]any{
		"restartPolicy":    "Never",
		"podFailurePolicy": map[string]any{"rules": rules},
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

// TestJobHandler_PodFailurePolicy_RoundTrip pins what an accepted policy emits
// (go-kure/launcher#345): both rule shapes, the containerName scoping, and the
// per-field values reaching batchv1.PodFailurePolicy unchanged.
func TestJobHandler_PodFailurePolicy_RoundTrip(t *testing.T) {
	job, _ := generateJob(t, podFailurePolicyProps([]any{
		map[string]any{
			"action": "FailJob",
			"onExitCodes": map[string]any{
				"containerName": "batch",
				"operator":      "In",
				"values":        []any{1, 42, 137},
			},
		},
		map[string]any{
			"action": "Ignore",
			"onPodConditions": []any{
				map[string]any{"type": "DisruptionTarget", "status": "True"},
			},
		},
	}, nil))

	pfp := job.Spec.PodFailurePolicy
	if pfp == nil || len(pfp.Rules) != 2 {
		t.Fatalf("PodFailurePolicy = %+v, want two rules", pfp)
	}

	first := pfp.Rules[0]
	if first.Action != batchv1.PodFailurePolicyActionFailJob {
		t.Errorf("Rules[0].Action = %q, want FailJob", first.Action)
	}
	if first.OnExitCodes == nil {
		t.Fatal("Rules[0].OnExitCodes is nil, want the exit-code requirement")
	}
	if first.OnExitCodes.ContainerName == nil || *first.OnExitCodes.ContainerName != "batch" {
		t.Errorf("Rules[0].OnExitCodes.ContainerName = %v, want \"batch\"", first.OnExitCodes.ContainerName)
	}
	if first.OnExitCodes.Operator != batchv1.PodFailurePolicyOnExitCodesOpIn {
		t.Errorf("Rules[0].OnExitCodes.Operator = %q, want In", first.OnExitCodes.Operator)
	}
	if got := first.OnExitCodes.Values; len(got) != 3 || got[0] != 1 || got[1] != 42 || got[2] != 137 {
		t.Errorf("Rules[0].OnExitCodes.Values = %v, want [1 42 137]", got)
	}
	if len(first.OnPodConditions) != 0 {
		t.Errorf("Rules[0].OnPodConditions = %v, want none", first.OnPodConditions)
	}

	second := pfp.Rules[1]
	if second.Action != batchv1.PodFailurePolicyActionIgnore {
		t.Errorf("Rules[1].Action = %q, want Ignore", second.Action)
	}
	if second.OnExitCodes != nil {
		t.Errorf("Rules[1].OnExitCodes = %+v, want nil", second.OnExitCodes)
	}
	if len(second.OnPodConditions) != 1 {
		t.Fatalf("Rules[1].OnPodConditions = %v, want one pattern", second.OnPodConditions)
	}
	if got := second.OnPodConditions[0]; got.Type != corev1.DisruptionTarget || got.Status != corev1.ConditionTrue {
		t.Errorf("Rules[1].OnPodConditions[0] = %+v, want DisruptionTarget/True", got)
	}

	// The policy is not written unless authored: an omitempty pointer field
	// left non-nil unconditionally would add `podFailurePolicy: null` to every
	// generated Job.
	plain, _ := generateJob(t, nil)
	if plain.Spec.PodFailurePolicy != nil {
		t.Errorf("PodFailurePolicy = %+v on a document that never authored it, want nil", plain.Spec.PodFailurePolicy)
	}
}

// TestJobHandler_PodFailurePolicy_EmptyRules pins the one place this parser is
// deliberately no stricter than upstream. validatePodFailurePolicy has no
// "at least one rule" check — unlike validateSuccessPolicy, which does — and the
// field's own doc states only the cap, so there is no documented contract to
// follow past the API server the way succeededIndexes has one. An empty list is
// also not inert: a non-nil policy pins podReplacementPolicy and restartPolicy
// on its own.
func TestJobHandler_PodFailurePolicy_EmptyRules(t *testing.T) {
	job, _ := generateJob(t, podFailurePolicyProps([]any{}, nil))
	pfp := job.Spec.PodFailurePolicy
	if pfp == nil {
		t.Fatal("PodFailurePolicy is nil, want an empty-ruled policy")
	}
	// Non-nil, not merely empty: batchv1.PodFailurePolicy.Rules carries no
	// omitempty, so a nil slice would marshal as `rules: null` — a shape the
	// author did not write.
	if pfp.Rules == nil {
		t.Error("PodFailurePolicy.Rules is nil, want an empty slice so it marshals as `rules: []`")
	}
	if len(pfp.Rules) != 0 {
		t.Errorf("PodFailurePolicy.Rules = %v, want none", pfp.Rules)
	}
}

// TestJobHandler_PodFailurePolicyValidation_Table walks the podFailurePolicy
// rules ported from validatePodFailurePolicy / validatePodFailurePolicyRule.
// Each case is a document Kubernetes' own API server would reject at apply
// time, refused here at build time instead.
func TestJobHandler_PodFailurePolicyValidation_Table(t *testing.T) {
	exitCodes := func(extra map[string]any) []any {
		req := map[string]any{"operator": "In", "values": []any{1}}
		for k, v := range extra {
			req[k] = v
		}
		return []any{map[string]any{"action": "FailJob", "onExitCodes": req}}
	}

	cases := []struct {
		name       string
		props      map[string]any
		wantSubstr string
	}{
		{
			"unknown top-level key",
			map[string]any{"restartPolicy": "Never", "podFailurePolicy": map[string]any{"rules": []any{}, "bogus": 1}},
			`podFailurePolicy: unrecognized key "bogus"`,
		},
		{
			"missing rules key",
			map[string]any{"restartPolicy": "Never", "podFailurePolicy": map[string]any{}},
			"podFailurePolicy.rules: required",
		},
		{
			"rules not an array",
			map[string]any{"restartPolicy": "Never", "podFailurePolicy": map[string]any{"rules": map[string]any{}}},
			"podFailurePolicy.rules: must be an array",
		},
		{
			"rule not an object",
			podFailurePolicyProps([]any{"FailJob"}, nil),
			"podFailurePolicy.rules[0]: must be an object",
		},
		{
			"rule with an unknown key",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "bogus": 1}}, nil),
			`podFailurePolicy.rules[0]: unrecognized key "bogus"`,
		},
		{
			"missing action",
			podFailurePolicyProps([]any{map[string]any{"onPodConditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True"}}}}, nil),
			"podFailurePolicy.rules[0].action: required",
		},
		{
			// "" must be refused rather than defaulted: upstream reports an
			// empty action as Required, and parseStringField would have
			// reported it as absent.
			"empty action",
			podFailurePolicyProps([]any{map[string]any{"action": ""}}, nil),
			"podFailurePolicy.rules[0].action: required",
		},
		{
			"non-string action",
			podFailurePolicyProps([]any{map[string]any{"action": 3}}, nil),
			"podFailurePolicy.rules[0].action: must be a string",
		},
		{
			"unknown action",
			podFailurePolicyProps([]any{map[string]any{"action": "Retry"}}, nil),
			"podFailurePolicy.rules[0].action: invalid value \"Retry\"",
		},
		{
			"rule with neither onExitCodes nor onPodConditions",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob"}}, nil),
			"exactly one of onExitCodes or onPodConditions is required",
		},
		{
			// An authored empty list counts as neither, here and upstream:
			// both test len(), not nil-ness.
			"rule with an empty onPodConditions",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{}}}, nil),
			"exactly one of onExitCodes or onPodConditions is required",
		},
		{
			"rule with both",
			podFailurePolicyProps([]any{map[string]any{
				"action":          "FailJob",
				"onExitCodes":     map[string]any{"operator": "In", "values": []any{1}},
				"onPodConditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True"}},
			}}, nil),
			"mutually exclusive",
		},
		{
			"FailIndex without backoffLimitPerIndex",
			podFailurePolicyProps(
				[]any{map[string]any{"action": "FailIndex", "onExitCodes": map[string]any{"operator": "In", "values": []any{1}}}},
				map[string]any{"completionMode": "Indexed", "completions": 4},
			),
			"requires backoffLimitPerIndex",
		},
		{
			"podReplacementPolicy other than Failed",
			podFailurePolicyProps(exitCodes(nil), map[string]any{"podReplacementPolicy": "TerminatingOrFailed"}),
			`podReplacementPolicy: must be "Failed" when podFailurePolicy is set`,
		},
		{
			"onExitCodes with an unknown key",
			podFailurePolicyProps(exitCodes(map[string]any{"bogus": 1}), nil),
			`podFailurePolicy.rules[0].onExitCodes: unrecognized key "bogus"`,
		},
		{
			"missing operator",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onExitCodes": map[string]any{"values": []any{1}}}}, nil),
			"onExitCodes.operator: required",
		},
		{
			"unknown operator",
			podFailurePolicyProps(exitCodes(map[string]any{"operator": "OneOf"}), nil),
			"onExitCodes.operator: invalid value \"OneOf\"",
		},
		{
			// Reported as missing rather than invalid, the same split upstream
			// makes: field.Required for "", field.NotSupported for the rest.
			"empty operator",
			podFailurePolicyProps(exitCodes(map[string]any{"operator": ""}), nil),
			"onExitCodes.operator: required",
		},
		{
			// "" would read as absent through parseStringField, leaving the
			// rule scoped to every container instead of the one the author
			// named — a widening, not a refusal.
			"empty containerName",
			podFailurePolicyProps(exitCodes(map[string]any{"containerName": ""}), nil),
			"onExitCodes.containerName: must not be empty",
		},
		{
			"missing values",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onExitCodes": map[string]any{"operator": "In"}}}, nil),
			"onExitCodes.values: required",
		},
		{
			"empty values",
			podFailurePolicyProps(exitCodes(map[string]any{"values": []any{}}), nil),
			"onExitCodes.values: at least one exit code is required",
		},
		{
			"non-integer value",
			podFailurePolicyProps(exitCodes(map[string]any{"values": []any{"1"}}), nil),
			"onExitCodes.values[0]: must be an integer",
		},
		{
			// A container that exits 0 succeeded and is excluded before the
			// operator is applied, so 0 under In names a requirement nothing
			// can satisfy. Legal under NotIn, covered below.
			"zero under the In operator",
			podFailurePolicyProps(exitCodes(map[string]any{"values": []any{0, 1}}), nil),
			"0 must not be listed",
		},
		{
			"duplicate values",
			podFailurePolicyProps(exitCodes(map[string]any{"values": []any{1, 1}}), nil),
			"duplicate exit code 1",
		},
		{
			"unordered values",
			podFailurePolicyProps(exitCodes(map[string]any{"values": []any{2, 1}}), nil),
			"must be in increasing order",
		},
		{
			"onPodConditions not an array",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": map[string]any{}}}, nil),
			"onPodConditions: must be an array",
		},
		{
			"onPodConditions entry with an unknown key",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True", "bogus": 1}}}}, nil),
			`onPodConditions[0]: unrecognized key "bogus"`,
		},
		{
			"onPodConditions entry missing type",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"status": "True"}}}}, nil),
			"onPodConditions[0].type: required",
		},
		{
			"onPodConditions entry with a malformed type",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "not a name", "status": "True"}}}}, nil),
			"is not a qualified name",
		},
		{
			"onPodConditions entry with an empty type",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "", "status": "True"}}}}, nil),
			"onPodConditions[0].type: required",
		},
		{
			// Nothing in this package defaults status, and upstream — which
			// runs after API-server defaulting — reports an empty one as
			// Required, so it is refused here rather than emitted empty.
			"onPodConditions entry missing status",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "DisruptionTarget"}}}}, nil),
			"onPodConditions[0].status: required",
		},
		{
			"onPodConditions entry with an unknown status",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "DisruptionTarget", "status": "Maybe"}}}}, nil),
			"onPodConditions[0].status: invalid value \"Maybe\"",
		},
		{
			"onPodConditions entry with an empty status",
			podFailurePolicyProps([]any{map[string]any{"action": "FailJob", "onPodConditions": []any{map[string]any{"type": "DisruptionTarget", "status": ""}}}}, nil),
			"onPodConditions[0].status: required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := jobError(t, tc.props); !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.wantSubstr)
			}
		})
	}
}

// TestJobHandler_PodFailurePolicy_Bounds covers the three caps, kept out of the
// table above because each needs a generated list that would bury its
// neighbours.
func TestJobHandler_PodFailurePolicy_Bounds(t *testing.T) {
	condition := func(i int) any {
		return map[string]any{"type": fmt.Sprintf("Condition%d", i), "status": "True"}
	}

	t.Run("more than 20 rules", func(t *testing.T) {
		rules := make([]any, 21)
		for i := range rules {
			rules[i] = map[string]any{"action": "Ignore", "onPodConditions": []any{condition(i)}}
		}
		if msg := jobError(t, podFailurePolicyProps(rules, nil)); !strings.Contains(msg, "at most 20 rules") {
			t.Errorf("error = %q, want the rule-count cap", msg)
		}
	})

	t.Run("more than 20 onPodConditions patterns", func(t *testing.T) {
		patterns := make([]any, 21)
		for i := range patterns {
			patterns[i] = condition(i)
		}
		props := podFailurePolicyProps([]any{map[string]any{"action": "Ignore", "onPodConditions": patterns}}, nil)
		if msg := jobError(t, props); !strings.Contains(msg, "at most 20 patterns") {
			t.Errorf("error = %q, want the pattern-count cap", msg)
		}
	})

	t.Run("more than 255 exit codes", func(t *testing.T) {
		values := make([]any, 256)
		for i := range values {
			values[i] = i + 1
		}
		props := podFailurePolicyProps([]any{map[string]any{
			"action":      "FailJob",
			"onExitCodes": map[string]any{"operator": "In", "values": values},
		}}, nil)
		if msg := jobError(t, props); !strings.Contains(msg, "at most 255 exit codes") {
			t.Errorf("error = %q, want the exit-code cap", msg)
		}
	})

	// The same shape one under the cap must build, so the message above is a
	// refusal of the size, not of the shape.
	values := make([]any, 255)
	for i := range values {
		values[i] = i + 1
	}
	job, _ := generateJob(t, podFailurePolicyProps([]any{map[string]any{
		"action":      "FailJob",
		"onExitCodes": map[string]any{"operator": "In", "values": values},
	}}, nil))
	got := job.Spec.PodFailurePolicy
	if got == nil || len(got.Rules) != 1 || got.Rules[0].OnExitCodes == nil {
		t.Fatalf("PodFailurePolicy = %+v, want one onExitCodes rule", got)
	}
	if n := len(got.Rules[0].OnExitCodes.Values); n != 255 {
		t.Errorf("len(Values) = %d, want 255", n)
	}
}

// TestJobHandler_PodFailurePolicy_ZeroUnderNotIn is the counterpart to the
// zero-under-In case in the table: upstream forbids 0 only for the In operator,
// so refusing it under NotIn would be stricter than the API for no reason.
func TestJobHandler_PodFailurePolicy_ZeroUnderNotIn(t *testing.T) {
	job, _ := generateJob(t, podFailurePolicyProps([]any{map[string]any{
		"action":      "FailJob",
		"onExitCodes": map[string]any{"operator": "NotIn", "values": []any{0, 1}},
	}}, nil))
	got := job.Spec.PodFailurePolicy
	if got == nil || len(got.Rules) != 1 || got.Rules[0].OnExitCodes == nil {
		t.Fatalf("PodFailurePolicy = %+v, want one onExitCodes rule", got)
	}
	if v := got.Rules[0].OnExitCodes.Values; len(v) != 2 || v[0] != 0 || v[1] != 1 {
		t.Errorf("Values = %v, want [0 1]", v)
	}
}

// TestJobHandler_PodFailurePolicy_TemplateRules covers the two checks that need
// the built pod template rather than the property map: the restart policy
// upstream requires alongside a podFailurePolicy, and containerName naming a
// container the template actually carries. Both run at Generate, against what is
// emitted — which is why they are unreachable through jobError.
func TestJobHandler_PodFailurePolicy_TemplateRules(t *testing.T) {
	rules := []any{map[string]any{
		"action":      "FailJob",
		"onExitCodes": map[string]any{"operator": "In", "values": []any{1}},
	}}

	t.Run("default restartPolicy", func(t *testing.T) {
		// No restartPolicy authored, so the component's own OnFailure default
		// lands on the template — a Job the API server refuses.
		props := map[string]any{"podFailurePolicy": map[string]any{"rules": rules}}
		msg := jobGenerateError(t, props)
		if !strings.Contains(msg, `restartPolicy: must be "Never"`) || !strings.Contains(msg, "OnFailure") {
			t.Errorf("error = %q, want it to name the required Never and the OnFailure it got", msg)
		}
	})

	t.Run("authored OnFailure", func(t *testing.T) {
		props := map[string]any{"restartPolicy": "OnFailure", "podFailurePolicy": map[string]any{"rules": rules}}
		if msg := jobGenerateError(t, props); !strings.Contains(msg, `restartPolicy: must be "Never"`) {
			t.Errorf("error = %q, want the restartPolicy refusal", msg)
		}
	})

	t.Run("containerName naming no container", func(t *testing.T) {
		props := podFailurePolicyProps([]any{map[string]any{
			"action":      "FailJob",
			"onExitCodes": map[string]any{"containerName": "sidecar", "operator": "In", "values": []any{1}},
		}}, nil)
		msg := jobGenerateError(t, props)
		if !strings.Contains(msg, "names no container in this component") {
			t.Errorf("error = %q, want the containerName refusal", msg)
		}
		// The message lists what is available, so an author can see the name
		// they meant. "batch" is this component's own container.
		if !strings.Contains(msg, `"batch"`) {
			t.Errorf("error = %q, want it to list the container names the template carries", msg)
		}
	})

	t.Run("containerName naming an initContainer", func(t *testing.T) {
		// Init containers count too, upstream and here: the exit-code check
		// reads .status.initContainerStatuses alongside .status.containerStatuses.
		props := podFailurePolicyProps([]any{map[string]any{
			"action":      "FailJob",
			"onExitCodes": map[string]any{"containerName": "setup", "operator": "In", "values": []any{1}},
		}}, map[string]any{
			"initContainers": []any{map[string]any{"name": "setup", "image": "ghcr.io/org/setup:v1.0.0"}},
		})
		job, _ := generateJob(t, props)
		got := job.Spec.PodFailurePolicy
		if got == nil || len(got.Rules) != 1 || got.Rules[0].OnExitCodes == nil {
			t.Fatalf("PodFailurePolicy = %+v, want one onExitCodes rule", got)
		}
		if name := got.Rules[0].OnExitCodes.ContainerName; name == nil || *name != "setup" {
			t.Errorf("ContainerName = %v, want \"setup\"", name)
		}
	})
}
