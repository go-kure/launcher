package components

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// validateObjectPodRules holds the pod spec of one typed workload object to the
// two pod rules and returns its refusal bare, for the caller to name the object
// in: a lifecycle or probe on an init container that is not restartable
// (refuseRegularInitFields, go-kure/launcher#1237), then a lifecycle sleep
// below zero or above the grace period (checkLifecycleSleep,
// go-kure/launcher#1236). The first runs first because the second skips a
// regular init container, so a sleep there is refused as the field it is.
// An object that runs no pod, and one that reached the build untyped, passes,
// as for validateObjectMounts.
func validateObjectPodRules(obj client.Object) error {
	path, ps := renderedPodSpec(obj)
	if ps == nil {
		return nil
	}
	prefix := path + "."
	_, bare := obj.(*corev1.Pod)
	if err := refuseRegularInitFields(prefix, ps); err != nil {
		return err
	}
	return checkLifecycleSleep(prefix, ps, bare)
}

// validateManifestObjects holds the pod spec of every typed workload object of
// a manifests source to the checks the typed pod readers apply to an authored
// pod spec, object by object and in their order: a container name used by more
// than one init or regular container (validateObjectContainerNames,
// go-kure/launcher#1253), then the mount rules (validateObjectMounts,
// go-kure/launcher#1223), then validateObjectPodRules. A chart's render does the
// same per object, so an object that fails more than one check gets the same
// first error from either, and the first object that fails any check is the one
// refused (go-kure/launcher#1297). They hold whatever the environment policy,
// and what they refuse names the object. An object that runs no pod, and one
// that reached the build untyped, passes, as for validateObjectMounts.
func validateManifestObjects(objs []client.Object) error {
	for _, obj := range objs {
		for _, check := range []func(client.Object) error{
			validateObjectContainerNames,
			validateObjectMounts,
			validateObjectPodRules,
		} {
			if err := check(obj); err != nil {
				return errors.Wrapf(err, "manifest source: object %s", renderedObjectRef(obj))
			}
		}
	}
	return nil
}

// validateObjectContainerNames holds the pod spec of one typed workload object
// to checkPodSpecContainerNames and returns its refusal bare, for the caller to
// name the object in. It reads the objects validateObjectMounts reads: an
// object that runs no pod, and one that reached the build untyped, passes, and
// the policy check refuses the latter as unreadable.
func validateObjectContainerNames(obj client.Object) error {
	path, ps := renderedPodSpec(obj)
	if ps == nil {
		return nil
	}
	return checkPodSpecContainerNames(path+".", ps)
}
