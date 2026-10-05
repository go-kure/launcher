package components

import (
	"fmt"
	"math"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
//
// Before either step, a config that sets SecretValues is refused under a policy
// that forbids explicit secrets (oam.ExplicitSecretPolicy); a policy that does
// not implement that interface allows it. A violation on a rendered object is
// reported as for any chart: it names the object and quotes what the policy
// refuses in it (an image reference, a quantity), which the chart may have
// rendered from a sensitive value.
func (c *HelmTemplateConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	if len(c.SecretValues) > 0 && !oam.ExplicitSecretsAllowed(p) {
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("%s: %s is set and the environment policy forbids explicit secrets; have the chart read a Secret created out of band instead", helmTemplateType, helmSecretValuesKey))
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
// authored workload is held to. An object of a kind not named here passes.
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
// as the persistentvolumeclaim and statefulset kinds hold theirs, and the
// replica count a controller or a HorizontalPodAutoscaler asks for to the
// replica maximum (enforceRenderedReplicas). A PersistentVolume is held to
// what the persistentvolume kind holds its own to
// (enforcePersistentVolumePolicy): a hostPath or local source needs the policy
// to allow hostPath volumes, and spec.capacity.storage is held to the storage
// maximum.
//
// What reached the check untyped and may hold a workload is refused, since
// nothing in it can be read: a checked kind in an API version kure's scheme
// does not register (apps/v1beta2, batch/v1beta1), and an object with a
// top-level items array, which is a list to what applies it, as apimachinery
// tells one, whatever its kind. The decode of a chart's render and of a
// manifests source returns no such object: it replaces a list by its items,
// each read as a document of its own, and refuses an items array on a kind
// that is no list (refuseItemsOnNoList). The arm holds an object that reaches
// the check any other way.
func enforceRenderedObjectPolicy(obj client.Object, p oam.Policy) error {
	if err := enforceRenderedClaims(obj, p); err != nil {
		return err
	}
	if err := enforceRenderedReplicas(obj, p); err != nil {
		return err
	}
	if pv, ok := obj.(*corev1.PersistentVolume); ok {
		return enforcePersistentVolumePolicy("spec.", &pv.Spec, p)
	}
	path, ps := renderedPodSpec(obj)
	if ps == nil {
		u, ok := obj.(*unstructured.Unstructured)
		switch {
		case !ok:
			return nil
		case u.IsList():
			return oam.NewPolicyRefusal(oam.RefusalUnreadableObject, "the object has a top-level items list, so what applies it reads it as a list, whose objects cannot be checked against environment policy")
		case isWorkloadGVK(u.GroupVersionKind()):
			return oam.NewPolicyRefusal(oam.RefusalUnreadableObject, fmt.Sprintf("apiVersion %q is not one whose pod spec this build can read, so the object cannot be checked against environment policy", u.GetAPIVersion()))
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
		return enforceMaxStorageAt(q.String(), p.MaxStorageSize(), where+".resources.requests.storage")
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

// enforceRenderedReplicas holds the replica count a rendered object asks for
// to the policy's replica maximum, as the deployment and statefulset kinds and
// the scaler trait hold theirs: spec.replicas of a Deployment, StatefulSet,
// ReplicaSet or ReplicationController — one when unset, the API server's
// default — and spec.maxReplicas of a HorizontalPodAutoscaler. That field sits
// at the same path in every version of the kind, so one kure's scheme does not
// register (autoscaling/v1) is read as it arrived; a value there that is not
// an integer is refused, since it cannot be compared. Any other object passes,
// and so does every object under a policy with no maximum.
func enforceRenderedReplicas(obj client.Object, p oam.Policy) error {
	limit := p.MaxReplicas()
	if limit == nil {
		return nil
	}
	check := func(field string, n int32) error {
		if err := enforceMaxReplicas(n, limit); err != nil {
			return errors.Wrap(err, field)
		}
		return nil
	}
	replicas := func(r *int32) error {
		if r == nil {
			return check("spec.replicas", 1)
		}
		return check("spec.replicas", *r)
	}
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return replicas(o.Spec.Replicas)
	case *appsv1.StatefulSet:
		return replicas(o.Spec.Replicas)
	case *appsv1.ReplicaSet:
		return replicas(o.Spec.Replicas)
	case *corev1.ReplicationController:
		return replicas(o.Spec.Replicas)
	case *autoscalingv2.HorizontalPodAutoscaler:
		return check("spec.maxReplicas", o.Spec.MaxReplicas)
	case *unstructured.Unstructured:
		if gvk := o.GroupVersionKind(); gvk.Group != "autoscaling" || gvk.Kind != "HorizontalPodAutoscaler" {
			return nil
		}
		n, found, err := unstructured.NestedInt64(o.Object, "spec", "maxReplicas")
		if err != nil || !found || n > math.MaxInt32 || n < math.MinInt32 {
			return oam.NewPolicyRefusal(oam.RefusalUnreadableObject, "spec.maxReplicas is not an integer this build can read, so the object cannot be checked against environment policy")
		}
		return check("spec.maxReplicas", int32(n))
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
// objects renderedPodSpec, enforceRenderedClaims and the PersistentVolume
// check read, in any version.
//
// They are also the kinds whose documents are refused when they set a field
// the Go type does not declare (undeclared_fields.go), since this check reads
// that type and cannot see such a field. A kind this check starts to read as
// its Go type belongs here; TestUndeclaredFields_RefuseSetIsWhatThePolicyReads
// holds the two together. A HorizontalPodAutoscaler is not one of them: its one
// checked field is read from the unstructured object too, so a document with
// an undeclared field is kept as written and still checked.
var (
	workloadGroups = map[string]bool{"": true, "apps": true, "batch": true, "extensions": true}
	workloadKinds  = map[string]bool{
		"Pod": true, "PodTemplate": true, "ReplicationController": true, "Deployment": true, "StatefulSet": true,
		"DaemonSet": true, "ReplicaSet": true, "Job": true, "CronJob": true, "PersistentVolumeClaim": true,
		"PersistentVolume": true,
	}
)

// isWorkloadGVK reports whether gvk is one of the checked kinds in one of the
// built-in workload groups, whatever its version.
func isWorkloadGVK(gvk schema.GroupVersionKind) bool {
	return workloadGroups[gvk.Group] && workloadKinds[gvk.Kind]
}
