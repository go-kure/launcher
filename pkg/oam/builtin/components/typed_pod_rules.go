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

// validateManifestPodRules holds the pod spec of every typed workload object of
// a manifests source to the rules the typed pod readers apply to an authored
// pod spec and the mount rules do not cover: a container name used by more than
// one init or regular container (validateObjectContainerNames,
// go-kure/launcher#1253), then validateObjectPodRules. They hold whatever the
// environment policy, and what they refuse names the object. Which objects are
// read, and which pass unread, is as for validateManifestMounts.
func validateManifestPodRules(objs []client.Object) error {
	for _, obj := range objs {
		if err := validateObjectContainerNames(obj); err != nil {
			return errors.Wrapf(err, "manifest source: object %s", renderedObjectRef(obj))
		}
		if err := validateObjectPodRules(obj); err != nil {
			return errors.Wrapf(err, "manifest source: object %s", renderedObjectRef(obj))
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
