package components_test

import (
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
		// podFailurePolicy has to be refused explicitly rather than merely left
		// out of the schema: validateProperties runs on emitted elements only,
		// never on authored documents, so an unread key is dropped in silence.
		{"podFailurePolicy", map[string]any{"rules": []any{}}, "go-kure/launcher#345"},
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
// JobSpec-level key is present, `suspend` is the job component's own, and
// podFailurePolicy is absent — it belongs to go-kure/launcher#345. Absence alone
// refuses nothing on an authored document; the refusal is jobSpecRejectedKeys,
// covered by TestJobHandler_SelectorProperties_Rejected.
func TestJobHandler_PropertySchema_Keys(t *testing.T) {
	schema := (&components.JobHandler{}).PropertySchema()

	for _, key := range []string{
		"image", "restartPolicy", "suspend",
		"backoffLimit", "completions", "parallelism", "activeDeadlineSeconds",
		"ttlSecondsAfterFinished", "completionMode", "backoffLimitPerIndex",
		"maxFailedIndexes", "podReplacementPolicy", "managedBy", "successPolicy",
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
	if _, ok := schema["podFailurePolicy"]; ok {
		t.Error("PropertySchema() publishes podFailurePolicy; go-kure/launcher#345 owns it")
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
