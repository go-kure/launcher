package components

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MonitoringPodSpec returns the pod view of obj where obj is the object of a
// monitoring workload kind, one from which the Prometheus operator builds pods
// (go-kure/launcher#974): the fields of its spec that the operator copies into
// the pod template of the StatefulSet it builds, under the pod spec's own
// names, as the kind's policy checks read them (monitoringWorkload.pod). The
// second result is the prefix the view's paths sit under in obj, "spec": a
// field at containers[0].securityContext in the view is at
// spec.containers[0].securityContext in obj. The third says whether obj is
// such an object; where it is not, the view is nil and the prefix empty.
//
// The view holds what the author wrote, and only that: containers,
// initContainers, volumes, securityContext, hostNetwork where the kind's spec
// has it (a ThanosRuler's has none), dnsPolicy, and the nameservers of
// dnsConfig. A listed container named for one the operator generates is a
// patch the operator merges into that container, and is in the view as
// written. The containers the operator generates are not in the view: their
// images, arguments and security context are the operator's, and are not read
// here.
//
// The view is a deep copy: a change to it does not reach obj. It covers the
// typed operator objects, by pointer; an unstructured object is converted to
// its typed object first. Each monitoring workload kind adds its case here
// from the same mapping its policy checks use, so the view and the checks
// cannot differ; TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind
// (pkg/oam/builtin/registry) fails on a registered kind without one.
func MonitoringPodSpec(obj client.Object) (*corev1.PodSpec, string, bool) {
	switch o := obj.(type) {
	case *monitoringv1.Alertmanager:
		if o != nil {
			return monitoringPodView(alertmanagerWorkload(&o.Spec))
		}
	case *monitoringv1.Prometheus:
		if o != nil {
			return monitoringPodView(prometheusWorkload(&o.Spec))
		}
	case *monitoringv1.ThanosRuler:
		if o != nil {
			return monitoringPodView(thanosRulerWorkload(&o.Spec))
		}
	}
	return nil, "", false
}

// monitoringPodView is MonitoringPodSpec's result for the workload w: its pod
// fields, copied, under the prefix spec.
func monitoringPodView(w monitoringWorkload) (*corev1.PodSpec, string, bool) {
	return w.pod.DeepCopy(), "spec", true
}
