package components

import (
	"testing"

	"github.com/go-kure/kure/pkg/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
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

// readEverythingPolicy is the policy that checks nothing, with a replica and a
// storage maximum: with overPolicyEverywhere, each thing the rendered-object
// check reads from a typed object is something it refuses.
type readEverythingPolicy struct {
	oam.NoopPolicy
}

func (*readEverythingPolicy) MaxReplicas() *int32    { n := int32(3); return &n }
func (*readEverythingPolicy) MaxStorageSize() string { return "1Gi" }

// overPolicyEverywhere writes into a new typed object what
// readEverythingPolicy refuses, in each place this test knows the
// rendered-object check to read: an ephemeral container in the pod spec (which
// the check refuses under any policy), a storage request over the maximum on a
// claim and on each claim template, a capacity over it on a PersistentVolume,
// and a replica maximum over the policy's on a HorizontalPodAutoscaler. An
// object of any other type is left as it was made.
func overPolicyEverywhere(obj client.Object) {
	over := corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}
	switch o := obj.(type) {
	case *corev1.ReplicationController:
		// Its template is a pointer, nil in a new object.
		o.Spec.Template = &corev1.PodTemplateSpec{}
	case *corev1.PersistentVolumeClaim:
		o.Spec.Resources.Requests = over
	case *appsv1.StatefulSet:
		o.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{Requests: over},
		}}}
	case *corev1.PersistentVolume:
		o.Spec.Capacity = over
	case *autoscalingv2.HorizontalPodAutoscaler:
		o.Spec.MaxReplicas = 9
	}
	if _, ps := renderedPodSpec(obj); ps != nil {
		ps.EphemeralContainers = []corev1.EphemeralContainer{{}}
	}
}

// TestPolicyReadsKind_CoversEveryKindReadAsItsGoType: the same, for the kinds
// kure's scheme registers, which reach the check as their Go types. Each is
// made, filled with what the policy refuses wherever the check is known to
// read (overPolicyEverywhere), and put to the check itself: a kind it then
// refuses is one policyReadsKind names, and policyReadsKind names no
// registered kind the check lets through, which passes unread on passthrough
// and would be refused as a target.manifest for nothing.
//
// The claim and the PersistentVolume are each refused through one arm of the
// check alone, so a check that stopped reading either fails here.
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
		overPolicyEverywhere(obj)
		refused := enforceRenderedObjectPolicy(obj, &readEverythingPolicy{}) != nil
		if refused {
			read++
		}
		if got := policyReadsKind(gvk); got != refused {
			t.Errorf("policyReadsKind(%s) = %t, and the rendered-object check refuses that Go type filled over the policy: %t", gvk, got, refused)
		}
	}
	if read < 11 {
		t.Errorf("the check refused %d registered kinds as Go types, want at least 11: the probe above no longer sees them", read)
	}
}
