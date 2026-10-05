package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the cnpg-pooler, cnpg-database and cnpg-objectstore
// kind components share (docs/oam/design-operator-cr-components.md). Their
// strict decode is the one every spec-projecting kind uses (kind_decode.go).

// cnpgDefaultedZeros is a CloudNativePG kind's defaulted-zero list for
// refuseUncarriedSpecValues: fields maps a json path, with [] for an array
// element, to the CRD default the operator applies to an omitted field.
func cnpgDefaultedZeros(fields map[string]string) defaultedZeroFields {
	return defaultedZeroFields{api: "CloudNativePG", defaulter: "operator", fields: fields}
}

// requireCnpgClusterRef refuses a spec.cluster reference with no name: the
// Pooler and Database CRDs require it, and the Go type would encode an
// unauthored one as `cluster: {name: ""}`. The referenced name must also be one
// CloudNativePG accepts for a Cluster (validateCnpgClusterName's rule), or the
// reference could never resolve.
func requireCnpgClusterRef(name string) error {
	if name == "" {
		return errors.New("cluster.name: required (the name of the CloudNativePG Cluster this object belongs to)")
	}
	if len(validation.IsDNS1035Label(name)) > 0 || len(name) > cnpgClusterNameMaxLength {
		return errors.Errorf("cluster.name %q: must be a DNS-1035 label of at most %d characters, as a CloudNativePG Cluster name is", name, cnpgClusterNameMaxLength)
	}
	return nil
}

// enforcePodTemplatePolicy applies the environment policy gates the workload
// kinds apply to their pod to an operator CR's raw pod template, which the
// operator copies into the pods it creates: host namespaces, hostPath volumes,
// the storage maximum on a generic ephemeral volume's claim, the registry
// allowlist on an image volume's reference, the pod-level hostProcess switch,
// pod-level resources, and, for every init and regular container, the registry
// allowlist on an authored image, the cpu and memory maxima, and the
// privileged, hostProcess and capability checks. Those are every field of a
// pod spec that names an image the kubelet pulls, which
// TestImageFields_HeldOrListed derives from the type. Ephemeral
// containers are not policed here: a pod template cannot declare them, so the
// caller refuses them outright. label prefixes each error with the template's
// path; an empty label is a pod spec that is itself what the errors name (the
// pod kind's properties), so nothing is prefixed.
func enforcePodTemplatePolicy(label string, ps *corev1.PodSpec, p oam.Policy) error {
	if ps == nil {
		return nil
	}
	at := func(err error) error {
		if label == "" {
			return err
		}
		return errors.Wrap(err, label)
	}
	under := func(field string) string {
		if label == "" {
			return field
		}
		return label + "." + field
	}
	cfg := PodSpecConfig{PodSpec: *ps}
	if err := enforceHostNamespaces(cfg, p); err != nil {
		return at(err)
	}
	if err := enforceHostPathVolumes(ps.Volumes, p.AllowHostPathVolumes()); err != nil {
		return at(err)
	}
	// A generic ephemeral volume provisions a claim for every pod, so its
	// storage request is capped as cnpg-cluster caps its ephemeralVolumeSource
	// claim.
	for _, v := range ps.Volumes {
		if v.Ephemeral == nil || v.Ephemeral.VolumeClaimTemplate == nil {
			continue
		}
		if q, ok := v.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			where := fmt.Sprintf("volume %q ephemeral.volumeClaimTemplate.spec.resources.requests.storage", v.Name)
			if err := enforceMaxStorageAt(q.String(), p.MaxStorageSize(), where); err != nil {
				return at(err)
			}
		}
	}
	// An image volume mounts an OCI image the kubelet pulls as it pulls a
	// container's, so its reference is held to the registry allowlist as an
	// authored container image is, and read the same way (registryHost). A
	// volume that names no reference names no image, and nothing is checked
	// for it.
	for _, v := range ps.Volumes {
		if v.Image == nil || v.Image.Reference == "" {
			continue
		}
		if err := enforceAllowedRegistries(v.Image.Reference, p.AllowedRegistries()); err != nil {
			return at(errors.Wrap(err, fmt.Sprintf("volume %q image.reference", v.Name)))
		}
	}
	// The pod-level hostProcess and resources checks are enforcePodHostProcess
	// and enforcePodResources, restated so their errors name the template's
	// own fields rather than the workload kinds' podSecurityContext and
	// podResources properties.
	if sc := ps.SecurityContext; !p.AllowPrivileged() && sc != nil && sc.WindowsOptions != nil &&
		sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess {
		return oam.NewPolicyRefusal(oam.RefusalPrivileged, fmt.Sprintf("%s is not allowed by environment policy", under("securityContext.windowsOptions.hostProcess")))
	}
	if r := ps.Resources; r != nil {
		for _, c := range []struct {
			list  corev1.ResourceList
			name  corev1.ResourceName
			max   string
			label string
		}{
			{r.Requests, corev1.ResourceCPU, p.MaxCPU(), "resources cpu request"},
			{r.Limits, corev1.ResourceCPU, p.MaxCPU(), "resources cpu limit"},
			{r.Requests, corev1.ResourceMemory, p.MaxMemory(), "resources memory request"},
			{r.Limits, corev1.ResourceMemory, p.MaxMemory(), "resources memory limit"},
		} {
			if err := enforceMaxResource(quantityString(c.list, c.name), c.max, c.label); err != nil {
				return at(err)
			}
		}
	}
	check := func(kind string, i int, name, image string, res corev1.ResourceRequirements, sc *corev1.SecurityContext) error {
		where := fmt.Sprintf("%s[%d] %q", under(kind), i, name)
		if image != "" {
			if err := enforceAllowedRegistries(image, p.AllowedRegistries()); err != nil {
				return errors.Wrap(err, where)
			}
		}
		if err := enforceMaxContainerResources(res, p); err != nil {
			return errors.Wrap(err, where)
		}
		if err := enforcePrivileged(sc, p.AllowPrivileged()); err != nil {
			return errors.Wrap(err, where)
		}
		if err := enforceContainerCapabilities(sc, p.AllowedContainerCapabilities(), p.ForbiddenContainerCapabilities()); err != nil {
			return errors.Wrap(err, where)
		}
		return nil
	}
	for i, c := range ps.InitContainers {
		if err := check("initContainers", i, c.Name, c.Image, c.Resources, c.SecurityContext); err != nil {
			return err
		}
	}
	for i, c := range ps.Containers {
		if err := check("containers", i, c.Name, c.Image, c.Resources, c.SecurityContext); err != nil {
			return err
		}
	}
	return nil
}

// validateCnpgResources applies admission's checks on a resource block the
// operator copies onto a pod or container unchanged, as cnpg-cluster's
// Generate does: hugepages need cpu or memory, and a request may not exceed
// its limit (extended and hugepages resources need the two equal). Without it
// the build would emit a CR whose pods admission refuses.
func validateCnpgResources(label string, r corev1.ResourceRequirements) error {
	if err := validateHugePagesHaveCPUOrMemory("resources", r.Requests, r.Limits); err != nil {
		return errors.Wrap(err, label)
	}
	if err := validateResourceRequestLimit(r.Requests, r.Limits); err != nil {
		return errors.Wrap(err, label)
	}
	return nil
}

// validatePodTemplateResources runs validateCnpgResources on a raw pod
// template's pod-level resources and on every init and regular container.
func validatePodTemplateResources(label string, ps *corev1.PodSpec) error {
	if ps.Resources != nil {
		if err := validateCnpgResources(label, *ps.Resources); err != nil {
			return err
		}
	}
	for i, c := range ps.InitContainers {
		if err := validateCnpgResources(fmt.Sprintf("%s.initContainers[%d] %q", label, i, c.Name), c.Resources); err != nil {
			return err
		}
	}
	for i, c := range ps.Containers {
		if err := validateCnpgResources(fmt.Sprintf("%s.containers[%d] %q", label, i, c.Name), c.Resources); err != nil {
			return err
		}
	}
	return nil
}

// enforceMaxContainerResources caps the cpu and memory requests and limits of
// a corev1.ResourceRequirements as written, the direct form cnpg-cluster uses:
// the operator copies the block unchanged, so there is no default tier.
func enforceMaxContainerResources(res corev1.ResourceRequirements, p oam.Policy) error {
	for _, chk := range []struct {
		list  corev1.ResourceList
		name  corev1.ResourceName
		max   string
		label string
	}{
		{res.Requests, corev1.ResourceCPU, p.MaxCPU(), "cpu request"},
		{res.Limits, corev1.ResourceCPU, p.MaxCPU(), "cpu limit"},
		{res.Requests, corev1.ResourceMemory, p.MaxMemory(), "memory request"},
		{res.Limits, corev1.ResourceMemory, p.MaxMemory(), "memory limit"},
	} {
		if err := enforceMaxResource(quantityString(chk.list, chk.name), chk.max, chk.label); err != nil {
			return err
		}
	}
	return nil
}
