package components

import (
	"slices"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
)

// TestJobSpecSchemaMatchesParser pins the published JobSpec-level schema to the
// key slices the parser reads and rejects against, at every nesting level —
// the same guard daemonset_spec_internal_test.go applies to its own fragment.
// Without it a key added to one half and not the other reads as correct at
// both: the schema consumer accepts a document the parser then refuses, or the
// parser accepts a key no schema documents.
func TestJobSpecSchemaMatchesParser(t *testing.T) {
	s := schemaJobSpec(false)

	top := make([]string, 0, len(s))
	for k := range s {
		top = append(top, k)
	}
	slices.Sort(top)
	want := slices.Clone(jobSpecPropertyKeys)
	slices.Sort(want)
	if !slices.Equal(top, want) {
		t.Errorf("schemaJobSpec keys = %v, want %v", top, want)
	}

	assertKeysAt(t, s, "successPolicy", jobSuccessPolicyKeys)

	rules := s["successPolicy"].Properties["rules"]
	if !rules.Required {
		t.Error("successPolicy.rules.Required = false, want true — parseJobSuccessPolicy demands it")
	}
	if rules.Items == nil {
		t.Fatal("successPolicy.rules.Items = nil, want the per-rule schema")
	}
	itemKeys := make([]string, 0, len(rules.Items.Properties))
	for k := range rules.Items.Properties {
		itemKeys = append(itemKeys, k)
	}
	slices.Sort(itemKeys)
	wantRuleKeys := slices.Clone(jobSuccessPolicyRuleKeys)
	slices.Sort(wantRuleKeys)
	if !slices.Equal(itemKeys, wantRuleKeys) {
		t.Errorf("successPolicy.rules item keys = %v, want %v", itemKeys, wantRuleKeys)
	}

	for k, node := range s {
		if node.Description == "" {
			t.Errorf("schemaJobSpec[%q]: Description is empty", k)
		}
	}
}

// TestSchemaJobSpec_HasNoSuspendKey is the collision guard behind the one
// deliberate asymmetry in this fragment. batchv1 carries two Suspend fields —
// JobSpec.Suspend and CronJobSpec.Suspend — and the cronjob component publishes
// the CronJobSpec one under `suspend`. Since PropertySchema() merges this
// fragment with maps.Copy AFTER its own literal map, a `suspend` added here
// would overwrite cronjob's entry and make one authored key write two different
// API fields. The job component publishes its own JobSpec-level `suspend`
// instead; see TestCronjobHandler_SuspendWritesCronJobSpecOnly for the
// behavioural half.
func TestSchemaJobSpec_HasNoSuspendKey(t *testing.T) {
	if _, ok := schemaJobSpec(false)["suspend"]; ok {
		t.Error("schemaJobSpec publishes \"suspend\"; it must not — that key is the cronjob component's CronJobSpec.Suspend, and sharing it would write one authored value into two API fields")
	}
	if slices.Contains(jobSpecPropertyKeys, "suspend") {
		t.Error("jobSpecPropertyKeys contains \"suspend\"; see above")
	}
}

// TestApplyJobSpec_CopiesRatherThanAliases pins that nothing applyJobSpec writes
// stays reachable from the config it was written from. A handler config outlives
// one Generate call and Generate may run twice, so an aliased pointer would let
// a writer through the generated object change what the next Generate emits.
//
// The probe is deliberately per-field rather than one representative field: the
// defect is one missing copy, not a missing policy, so a check that only walks
// backoffLimit passes with ten of eleven fields aliased.
func TestApplyJobSpec_CopiesRatherThanAliases(t *testing.T) {
	backoff := int32(3)
	completions := int32(5)
	parallelism := int32(2)
	deadline := int64(60)
	ttl := int32(10)
	mode := batchv1.IndexedCompletion
	perIndex := int32(1)
	maxFailed := int32(4)
	replacement := batchv1.Failed
	managedBy := "example.com/controller"
	count := int32(2)
	cfg := JobSpecConfig{
		BackoffLimit:            &backoff,
		Completions:             &completions,
		Parallelism:             &parallelism,
		ActiveDeadlineSeconds:   &deadline,
		TTLSecondsAfterFinished: &ttl,
		CompletionMode:          &mode,
		BackoffLimitPerIndex:    &perIndex,
		MaxFailedIndexes:        &maxFailed,
		PodReplacementPolicy:    &replacement,
		ManagedBy:               &managedBy,
		SuccessPolicy: &batchv1.SuccessPolicy{
			Rules: []batchv1.SuccessPolicyRule{{SucceededCount: &count}},
		},
	}

	var spec batchv1.JobSpec
	applyJobSpec(&spec, cfg)

	// Every scalar pointer must address different storage than the config's.
	if spec.BackoffLimit == cfg.BackoffLimit {
		t.Error("backoffLimit: spec aliases the config pointer")
	}
	if spec.Completions == cfg.Completions {
		t.Error("completions: spec aliases the config pointer")
	}
	if spec.Parallelism == cfg.Parallelism {
		t.Error("parallelism: spec aliases the config pointer")
	}
	if spec.ActiveDeadlineSeconds == cfg.ActiveDeadlineSeconds {
		t.Error("activeDeadlineSeconds: spec aliases the config pointer")
	}
	if spec.TTLSecondsAfterFinished == cfg.TTLSecondsAfterFinished {
		t.Error("ttlSecondsAfterFinished: spec aliases the config pointer")
	}
	if spec.CompletionMode == cfg.CompletionMode {
		t.Error("completionMode: spec aliases the config pointer")
	}
	if spec.BackoffLimitPerIndex == cfg.BackoffLimitPerIndex {
		t.Error("backoffLimitPerIndex: spec aliases the config pointer")
	}
	if spec.MaxFailedIndexes == cfg.MaxFailedIndexes {
		t.Error("maxFailedIndexes: spec aliases the config pointer")
	}
	if spec.PodReplacementPolicy == cfg.PodReplacementPolicy {
		t.Error("podReplacementPolicy: spec aliases the config pointer")
	}
	if spec.ManagedBy == cfg.ManagedBy {
		t.Error("managedBy: spec aliases the config pointer")
	}
	if spec.SuccessPolicy == cfg.SuccessPolicy {
		t.Fatal("successPolicy: spec aliases the config pointer")
	}
	// A pointer copy of a SuccessPolicy would still share the rules slice, so
	// the nested pointer is checked too — the exact aliasing a shallow copy
	// leaves behind.
	if &spec.SuccessPolicy.Rules[0] == &cfg.SuccessPolicy.Rules[0] {
		t.Fatal("successPolicy.rules: spec shares the config's rules backing array")
	}
	if spec.SuccessPolicy.Rules[0].SucceededCount == cfg.SuccessPolicy.Rules[0].SucceededCount {
		t.Fatal("successPolicy.rules[0].succeededCount: spec aliases the config pointer")
	}

	// The values must still have arrived, and writing through the object must
	// leave the config as it was.
	if *spec.BackoffLimit != 3 || *spec.ManagedBy != "example.com/controller" || *spec.SuccessPolicy.Rules[0].SucceededCount != 2 {
		t.Fatalf("applyJobSpec did not carry the values: %+v", spec)
	}
	*spec.BackoffLimit = 99
	*spec.ManagedBy = "example.com/other"
	*spec.SuccessPolicy.Rules[0].SucceededCount = 99
	spec.SuccessPolicy.Rules[0].SucceededIndexes = nil
	if backoff != 3 {
		t.Errorf("writing spec.BackoffLimit changed the config: got %d, want 3", backoff)
	}
	if managedBy != "example.com/controller" {
		t.Errorf("writing spec.ManagedBy changed the config: got %q", managedBy)
	}
	if count != 2 {
		t.Errorf("writing spec.SuccessPolicy changed the config: got %d, want 2", count)
	}
}

// TestApplyJobSpec_LeavesUnauthoredFieldsUntouched pins the presence gate: a nil
// field in the config must not overwrite whatever the constructor left on the
// spec, which is what keeps output for documents authoring none of these keys
// byte-identical.
func TestApplyJobSpec_LeavesUnauthoredFieldsUntouched(t *testing.T) {
	existing := int32(7)
	spec := batchv1.JobSpec{BackoffLimit: &existing}
	applyJobSpec(&spec, JobSpecConfig{})
	if spec.BackoffLimit != &existing {
		t.Error("applyJobSpec replaced an unauthored field")
	}
	if spec.SuccessPolicy != nil || spec.ManagedBy != nil || spec.MaxFailedIndexes != nil {
		t.Errorf("applyJobSpec wrote unauthored fields: %+v", spec)
	}
}
