package components

import (
	"testing"

	"github.com/go-kure/kure/pkg/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// policyReadsKindCandidates are the groups and kinds policyReadsKind is held
// to the rendered-object check on: every one kure's scheme registers, every
// pairing of workloadGroups and workloadKinds, the HorizontalPodAutoscaler,
// and a few the check has nothing to read from, a custom resource named like a
// workload among them.
func policyReadsKindCandidates(t *testing.T) map[schema.GroupKind]bool {
	t.Helper()
	if err := kubernetes.RegisterSchemes(); err != nil {
		t.Fatalf("RegisterSchemes: %v", err)
	}
	out := map[schema.GroupKind]bool{
		{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}: true,
		{Group: "", Kind: "ConfigMap"}:                          true,
		{Group: "", Kind: "Secret"}:                             true,
		{Group: "argoproj.io", Kind: "Application"}:             true,
		{Group: "example.io", Kind: "Deployment"}:               true,
		{Group: "example.io", Kind: "HorizontalPodAutoscaler"}:  true,
	}
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		out[gvk.GroupKind()] = true
	}
	for group := range workloadGroups {
		for kind := range workloadKinds {
			out[schema.GroupKind{Group: group, Kind: kind}] = true
		}
	}
	return out
}

// TestPolicyReadsKind_IsWhatTheRenderedObjectCheckReads holds policyReadsKind
// to enforceRenderedObjectPolicy, which it speaks for where an object's
// content is not at hand (enforceTargetManifest).
//
// A bare object of a group and kind, in a version kure's scheme does not
// register, reaches the check as it arrived. The check refuses it exactly when
// it has something to read from that kind and cannot: a workload, a claim or a
// PersistentVolume (no pod spec this build can read), a HorizontalPodAutoscaler
// (no replica maximum it can read). Any other object passes unread. So what
// the check refuses there is the set policyReadsKind must name, in both
// directions.
func TestPolicyReadsKind_IsWhatTheRenderedObjectCheckReads(t *testing.T) {
	policy := &maxReplicasPolicy{max: 3}
	read := 0
	for gk := range policyReadsKindCandidates(t) {
		gvk := gk.WithVersion("v0unregistered")
		if kubernetes.Scheme.Recognizes(gvk) {
			t.Fatalf("%s is registered: the probe needs a version that is not", gvk)
		}
		bare := &unstructured.Unstructured{}
		bare.SetGroupVersionKind(gvk)
		refused := enforceRenderedObjectPolicy(bare, policy) != nil
		if refused {
			read++
		}
		if got := policyReadsKind(gvk); got != refused {
			t.Errorf("policyReadsKind(%s) = %t, and the rendered-object check refuses a bare object of that kind: %t", gk, got, refused)
		}
	}
	// The eleven workloadKinds in the core group alone, and the autoscaler.
	if read < 12 {
		t.Errorf("the check refused %d bare kinds, want at least 12: the probe above no longer sees them", read)
	}
}

// TestPolicyReadsKind_CoversEveryKindReadAsItsGoType: the same, for the kinds
// kure's scheme registers, which reach the check as their Go types. A kind the
// check reads a pod spec, a claim, a volume or a replica maximum from is one
// policyReadsKind names, and it names no registered kind the check reads
// nothing from: that one passes unread on passthrough, and would be refused
// as a target.manifest for nothing.
func TestPolicyReadsKind_CoversEveryKindReadAsItsGoType(t *testing.T) {
	if err := kubernetes.RegisterSchemes(); err != nil {
		t.Fatalf("RegisterSchemes: %v", err)
	}
	read := 0
	for gvk := range kubernetes.Scheme.AllKnownTypes() {
		made, err := kubernetes.Scheme.New(gvk)
		if err != nil {
			t.Fatalf("Scheme.New(%s): %v", gvk, err)
		}
		obj, ok := made.(client.Object)
		if !ok {
			continue
		}
		// A ReplicationController's template is a pointer, nil in a new object.
		if rc, ok := obj.(*corev1.ReplicationController); ok {
			rc.Spec.Template = &corev1.PodTemplateSpec{}
		}
		_, podSpec := renderedPodSpec(obj)
		_, isClaim := obj.(*corev1.PersistentVolumeClaim)
		_, hasClaimTemplates := obj.(*appsv1.StatefulSet)
		_, isVolume := obj.(*corev1.PersistentVolume)
		_, isAutoscaler := obj.(*autoscalingv2.HorizontalPodAutoscaler)
		typed := podSpec != nil || isClaim || hasClaimTemplates || isVolume || isAutoscaler
		if typed {
			read++
		}
		if got := policyReadsKind(gvk); got != typed {
			t.Errorf("policyReadsKind(%s) = %t, and the rendered-object check reads that Go type: %t", gvk, got, typed)
		}
	}
	if read < 11 {
		t.Errorf("the check read %d registered kinds as Go types, want at least 11: the probe above no longer sees them", read)
	}
}
