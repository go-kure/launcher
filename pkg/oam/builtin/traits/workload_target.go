package traits

import (
	"reflect"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// workloadPodSpec returns the pod spec of obj when it is a workload the
// pod-spec traits write to (security-context, a configmap mount, an
// external-secret injection), and nil for any other object. It is the one
// place that says which kinds those are, so the three traits cannot disagree
// (go-kure/launcher#794, item 14).
//
// Only a typed object is read: one a launcher kind builds, a manifests source
// yields or a helmtemplate chart renders. A workload passed through as raw,
// unstructured output (passthrough) is not one of these types and is not
// inspected. Neither is a PodTemplate, which is stored and never run, nor a
// custom resource a controller turns into pods.
func workloadPodSpec(obj client.Object) *corev1.PodSpec {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return &o.Spec.Template.Spec
	case *appsv1.StatefulSet:
		return &o.Spec.Template.Spec
	case *appsv1.DaemonSet:
		return &o.Spec.Template.Spec
	case *batchv1.Job:
		return &o.Spec.Template.Spec
	case *batchv1.CronJob:
		return &o.Spec.JobTemplate.Spec.Template.Spec
	case *corev1.Pod:
		return &o.Spec
	case *appsv1.ReplicaSet:
		return &o.Spec.Template.Spec
	case *corev1.ReplicationController:
		// The template is a pointer; one without it has no pod spec to write.
		if o.Spec.Template == nil {
			return nil
		}
		return &o.Spec.Template.Spec
	default:
		return nil
	}
}

// workloadRef is a workload's pod spec with the kind and name of the object
// that holds it, for an error that has to say which object to change.
type workloadRef struct {
	podSpec    *corev1.PodSpec
	kind, name string
}

// workloadRefs returns every workload among objects (workloadPodSpec), in
// their order. Every other object is skipped: a trait applies to the
// workloads a component generates and leaves the rest of its output alone.
func workloadRefs(objects []*client.Object) []workloadRef {
	var refs []workloadRef
	for _, objPtr := range objects {
		if objPtr == nil {
			continue
		}
		if podSpec := workloadPodSpec(*objPtr); podSpec != nil {
			// The Go type names the kind even when the object carries no TypeMeta.
			t := reflect.TypeOf(*objPtr)
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			refs = append(refs, workloadRef{podSpec: podSpec, kind: t.Name(), name: (*objPtr).GetName()})
		}
	}
	return refs
}

// workloadPodSpecs returns the pod spec of every workload among objects
// (workloadRefs).
func workloadPodSpecs(objects []*client.Object) []*corev1.PodSpec {
	var specs []*corev1.PodSpec
	for _, w := range workloadRefs(objects) {
		specs = append(specs, w.podSpec)
	}
	return specs
}

// noWorkloadError is the refusal of a pod-spec trait on a component that
// generates no workload (workloadPodSpecs found none): what the author asked
// for would otherwise be accepted and applied to nothing. trait names the
// trait and properties the properties that have nowhere to go; hint, when not
// empty, says what the author can do instead.
func noWorkloadError(trait string, properties []string, component, hint string) error {
	verb := "applies"
	if len(properties) > 1 {
		verb = "apply"
	}
	msg := trait + ": component \"" + component + "\" generates no workload the trait can act on, so " +
		wordList(properties) + " " + verb + " to nothing; the trait writes to the pod spec of a Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob or Pod that a launcher kind builds, a manifests source yields or a helmtemplate chart renders, and an object passed through as raw, unstructured output (passthrough) is not inspected"
	if hint != "" {
		msg += "; " + hint
	}
	return errors.New(msg)
}

// decoratedComponent names the component a decorator's refusal is about: the
// one its inner config names (oam.ComponentNamed, forwarded by decoratorBase),
// else the application generated, which carries the component's name unless a
// consumer named it apart.
func decoratedComponent(d decoratorBase, app *stack.Application) string {
	if name := d.ComponentName(); name != "" {
		return name
	}
	if app != nil {
		return app.Name
	}
	return ""
}

// wordList joins words as prose: "a", "a and b", "a, b and c".
func wordList(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	last := len(words) - 1
	return strings.Join(words[:last], ", ") + " and " + words[last]
}
