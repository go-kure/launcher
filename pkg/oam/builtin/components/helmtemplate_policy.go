package components

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ApplyPolicy enforces environment policy on a chart rendered at build time,
// in two steps. A nil policy checks nothing and fetches nothing.
//
// First the host the chart is fetched from is held to the policy's allowed
// registries, as the oci, crd and manifests components hold their own URLs
// (enforceFluxSourceHost). A refused source fails here, before any request:
// only an allowed source is rendered. The Helm registry client that pulls an
// oci:// chart always takes the URL's first segment as the registry, so that
// segment is the host checked.
//
// Then the chart is rendered — the one render Generate and AugmentLayout
// return (chartRender.render) — and every object it emits is checked
// (enforceRenderedObjectPolicy). A violation names the rendered object and the
// path of what it refuses; the transform adds the component's name. An object
// hook grouping drops is never emitted and is not checked.
//
// Not covered: the archive a Helm repository's index names and any redirect,
// which kure's renderer follows to whatever host they point at; and a pod a
// custom resource's controller creates.
func (c *HelmTemplateConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	src, err := c.source()
	if err != nil {
		return err
	}
	if err := enforceFluxSourceHost(helmTemplateType, "source.url", src.URL, p); err != nil {
		return err
	}
	if err := c.render(c.renderChart, helmTemplateType, c.Name, src); err != nil {
		return err
	}
	for _, g := range c.hookGroups {
		for _, obj := range g.Resources {
			if err := enforceRenderedObjectPolicy(obj, p); err != nil {
				return errors.Wrapf(err, "%s: rendered %s", helmTemplateType, renderedObjectRef(obj))
			}
		}
	}
	return nil
}

// renderedObjectRef names a rendered object in an error: its kind and its
// name, namespace-qualified when it carries one.
func renderedObjectRef(obj client.Object) string {
	name := obj.GetName()
	if ns := obj.GetNamespace(); ns != "" {
		name = ns + "/" + name
	}
	return fmt.Sprintf("%s %q", obj.GetObjectKind().GroupVersionKind().Kind, name)
}

// enforceRenderedObjectPolicy holds one rendered object to the policy an
// authored workload is held to. An object that runs no pod passes.
//
// A workload — a Pod, a PodTemplate, a ReplicationController, a Deployment,
// StatefulSet, DaemonSet or ReplicaSet, a Job or CronJob — has its pod spec
// checked by enforcePodTemplatePolicy (host namespaces, hostPath volumes, the
// storage and resource maxima, and per init and regular container the registry
// allowlist, the cpu and memory maxima, and the privileged, hostProcess and
// capability gates), and every init and regular container's image by
// ValidateImageRef: no untagged image and no :latest. Ephemeral containers are
// refused as the workload kinds refuse them: the API server accepts none on a
// created pod. The storage a claim requests — a PersistentVolumeClaim's, and
// each of a StatefulSet's claim templates' — is held to the storage maximum,
// as the persistentvolumeclaim and statefulset kinds hold theirs.
//
// What reached the build untyped and may hold a workload is refused, since
// nothing in it can be read: a workload kind in an API version kure's scheme
// does not register (apps/v1beta2, batch/v1beta1) or an item of a list whose
// kind it does not, and a list left inside such a list, whose own items the
// parser does not unpack. A list is told by a top-level items array, as
// apimachinery tells one, so a custom resource that names a field so is
// refused there too; rendered on its own the parser already reads it as a list.
func enforceRenderedObjectPolicy(obj client.Object, p oam.Policy) error {
	if err := enforceRenderedClaims(obj, p); err != nil {
		return err
	}
	path, ps := renderedPodSpec(obj)
	if ps == nil {
		u, ok := obj.(*unstructured.Unstructured)
		switch {
		case !ok:
			return nil
		case u.IsList():
			return errors.New("the object has a top-level items list and sits inside a list of an unregistered kind, so it is read as a list whose objects cannot be checked against environment policy")
		case isWorkloadKind(u):
			return errors.Errorf("apiVersion %q is not one whose pod spec this build can read, so the object cannot be checked against environment policy", u.GetAPIVersion())
		}
		return nil
	}
	if len(ps.EphemeralContainers) > 0 {
		return errors.New(path + "." + podSpecRejectedKeys["ephemeralContainers"])
	}
	if err := enforcePodTemplatePolicy(path, ps, p); err != nil {
		return err
	}
	for i, ctr := range ps.InitContainers {
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "%s.initContainers[%d] %q", path, i, ctr.Name)
		}
	}
	for i, ctr := range ps.Containers {
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "%s.containers[%d] %q", path, i, ctr.Name)
		}
	}
	return nil
}

// enforceRenderedClaims holds the storage a rendered claim requests to the
// policy's storage maximum: a PersistentVolumeClaim's own request, and that of
// each claim template of a StatefulSet. Any other object passes.
func enforceRenderedClaims(obj client.Object, p oam.Policy) error {
	check := func(where string, spec *corev1.PersistentVolumeClaimSpec) error {
		q, ok := spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			return nil
		}
		return enforceMaxResource(q.String(), p.MaxStorageSize(), where+".resources.requests.storage")
	}
	switch o := obj.(type) {
	case *corev1.PersistentVolumeClaim:
		return check("spec", &o.Spec)
	case *appsv1.StatefulSet:
		for i := range o.Spec.VolumeClaimTemplates {
			vct := &o.Spec.VolumeClaimTemplates[i]
			if err := check(fmt.Sprintf("spec.volumeClaimTemplates[%d] %q spec", i, vct.Name), &vct.Spec); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderedPodSpec returns the pod spec a typed workload object runs and its
// path within the object, or a nil spec for any other object.
func renderedPodSpec(obj client.Object) (string, *corev1.PodSpec) {
	const template = "spec.template.spec"
	switch o := obj.(type) {
	case *corev1.Pod:
		return "spec", &o.Spec
	case *corev1.PodTemplate:
		return "template.spec", &o.Template.Spec
	case *corev1.ReplicationController:
		if o.Spec.Template == nil {
			return "", nil
		}
		return template, &o.Spec.Template.Spec
	case *appsv1.Deployment:
		return template, &o.Spec.Template.Spec
	case *appsv1.StatefulSet:
		return template, &o.Spec.Template.Spec
	case *appsv1.DaemonSet:
		return template, &o.Spec.Template.Spec
	case *appsv1.ReplicaSet:
		return template, &o.Spec.Template.Spec
	case *batchv1.Job:
		return template, &o.Spec.Template.Spec
	case *batchv1.CronJob:
		return "spec.jobTemplate.spec.template.spec", &o.Spec.JobTemplate.Spec.Template.Spec
	}
	return "", nil
}

// workloadGroups and workloadKinds are the API groups and kinds of the
// objects renderedPodSpec and enforceRenderedClaims read, in any version.
var (
	workloadGroups = map[string]bool{"": true, "apps": true, "batch": true, "extensions": true}
	workloadKinds  = map[string]bool{
		"Pod": true, "PodTemplate": true, "ReplicationController": true, "Deployment": true, "StatefulSet": true,
		"DaemonSet": true, "ReplicaSet": true, "Job": true, "CronJob": true, "PersistentVolumeClaim": true,
	}
)

// isWorkloadKind reports whether u is one of the checked kinds in one of the
// built-in workload groups, whatever its version.
func isWorkloadKind(u *unstructured.Unstructured) bool {
	gvk := u.GroupVersionKind()
	return workloadGroups[gvk.Group] && workloadKinds[gvk.Kind]
}
