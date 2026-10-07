package components

import (
	"fmt"
	"maps"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components share whose object makes the
// Prometheus operator run pods (go-kure/launcher#790): alertmanager. The
// operator builds a StatefulSet from the object's spec, so the fields of that
// spec that shape the pods are held as a workload kind's own are: each kind
// maps its spec into one monitoringWorkload, and the two functions below read
// that value, with or without an environment policy
// (validateMonitoringWorkload) and under one (enforceMonitoringWorkloadPolicy).
//
// What is held is what the object's author wrote. What the operator adds to
// the pods on its own is not in the object and is not held: its
// config-reloader containers, the arguments it derives. Where the spec names
// no image, the operator chooses the one the pods run; under a policy with
// allowed registries that choice cannot be held, so the unset image is
// refused. The operator's code is not in the linked module and was not read.

// monitoringWorkloadDefaultedZeros is a workload kind's defaulted-zero list for
// refuseUncarriedSpecValues: the probe fields of the containers its spec lists
// (podSpecDefaultedZeros), on which an authored 0 cannot be carried, and own,
// the fields of the operator's own types that the encoding omits when empty and
// to which the CRD gives another default, each mapped to that default as its
// JSON literal. A string default of the Kubernetes pod types is not listed,
// as the pod kinds list none.
func monitoringWorkloadDefaultedZeros(own map[string]string) defaultedZeroFields {
	fields := podSpecDefaultedZeros("").fields
	maps.Copy(fields, own)
	return defaultedZeroFields{api: "Prometheus operator", defaulter: "API server", fields: fields}
}

// fieldValue is one authored string with the path of the property that holds
// it.
type fieldValue struct {
	path, value string
}

// fieldResources is one authored resource block with the path of the property
// that holds it.
type fieldResources struct {
	path      string
	resources corev1.ResourceRequirements
}

// monitoringWorkload is what one such object asks the operator to run, in the
// terms the environment policy is stated in. It shares the spec's own slices
// and maps: both functions only read it.
type monitoringWorkload struct {
	// pod holds the fields of the spec that the policy on a pod reads, under the
	// pod spec's own names, and no other field: the operator copies them into
	// the pod template of its StatefulSet. A container listed under the name of
	// one the operator generates is a patch of that container, and is held like
	// any other.
	pod corev1.PodSpec
	// images are the full image references the spec names outside pod. An
	// empty value names no image and is not listed.
	images []fieldValue
	// unsetImages are the paths of the image fields the spec leaves unset or
	// empty where the operator then runs an image of its own choosing.
	unsetImages []string
	// replicas is the number of pods the spec asks for in total, nil where it
	// leaves the number to the operator. replicasPath names the property, or
	// the properties, it was read from.
	replicas     *int64
	replicasPath string
	// storage is the spec's storage block, nil where it has none.
	storage *monitoringv1.StorageSpec
	// resources are the resource blocks the spec names outside pod: the ones of
	// the containers the operator generates.
	resources []fieldResources
	// literals are the paths of the credentials the spec holds in the clear.
	literals []string
}

// validateMonitoringWorkload refuses what the build refuses of a workload with
// or without an environment policy: an image reference without a tag or a
// digest, or tagged latest (ValidateImageRef), on every image the workload
// names, and a resource block whose request exceeds its limit
// (validateCnpgResources). A container or an image volume that names no image
// is not checked: the operator supplies the image of the containers it
// generates, so a listed container without one is the ordinary form of a
// patch.
func validateMonitoringWorkload(w monitoringWorkload) error {
	for _, image := range w.images {
		if err := ValidateImageRef(image.value); err != nil {
			return errors.Wrap(err, image.path)
		}
	}
	for _, list := range []struct {
		name       string
		containers []corev1.Container
	}{{"initContainers", w.pod.InitContainers}, {"containers", w.pod.Containers}} {
		for i, c := range list.containers {
			where := fmt.Sprintf("%s[%d] %q", list.name, i, c.Name)
			if c.Image != "" {
				if err := ValidateImageRef(c.Image); err != nil {
					return errors.Wrap(err, where)
				}
			}
			if err := validateCnpgResources(where, c.Resources); err != nil {
				return err
			}
		}
	}
	if err := validateImageVolumeRefs("", &w.pod); err != nil {
		return err
	}
	for _, r := range w.resources {
		if err := validateResourcesAt(r.path, r.resources); err != nil {
			return err
		}
	}
	return nil
}

// validateResourcesAt is validateCnpgResources for a resource block the spec
// names by path, which ends in the property `resources`. The checks name the
// block by that word themselves, so the error is prefixed with what holds the
// block, and with nothing where the spec itself does.
func validateResourcesAt(path string, r corev1.ResourceRequirements) error {
	holder := strings.TrimSuffix(strings.TrimSuffix(path, "resources"), ".")
	if holder == "" {
		if err := validateHugePagesHaveCPUOrMemory("resources", r.Requests, r.Limits); err != nil {
			return err
		}
		return validateResourceRequestLimit(r.Requests, r.Limits)
	}
	return validateCnpgResources(holder, r)
}

// enforceMonitoringWorkloadPolicy holds the workload to the environment policy
// p, which is never nil. It refuses or passes, and fills no default:
//
//   - a credential in the clear, under a policy that forbids explicit secrets;
//   - an image outside the allowed registries;
//   - an image field left unset, under a policy that lists allowed
//     registries: the operator would choose the image, and no allowlist
//     reaches that choice;
//   - more pods than the replica maximum;
//   - a claim that requests more than the storage maximum, in the storage
//     block's volumeClaimTemplate or in the claim template of its ephemeral
//     volume;
//   - a resource block over the cpu or memory maximum;
//   - the pod fields, as a workload kind's pod template is held
//     (enforcePodTemplatePolicy): host namespaces, hostPath volumes, privilege
//     and capabilities, and the images, resources and claims of the listed
//     containers and volumes.
//
// The size limit of an emptyDir is not held, here or on any kind. No replica,
// resource or storage default of the policy is applied: the operator, not
// launcher, decides what an omitted field means.
func enforceMonitoringWorkloadPolicy(w monitoringWorkload, p oam.Policy) error {
	if len(w.literals) > 0 && !oam.ExplicitSecretsAllowed(p) {
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("%s: holds a credential in the object, and the environment policy forbids explicit secrets; name the key of a Secret created out of band instead", w.literals[0]))
	}
	allowed := p.AllowedRegistries()
	for _, image := range w.images {
		if err := enforceAllowedRegistries(image.value, allowed); err != nil {
			return errors.Wrap(err, image.path)
		}
	}
	if len(allowed) > 0 && len(w.unsetImages) > 0 {
		return oam.NewPolicyRefusal(oam.RefusalRegistry, fmt.Sprintf("%s: unset, so the Prometheus operator chooses the image the pods run, which the allowed registries %v cannot hold; name an image from one of them", w.unsetImages[0], allowed))
	}
	if max := p.MaxReplicas(); w.replicas != nil && max != nil && *w.replicas > int64(*max) {
		return oam.NewPolicyRefusal(oam.RefusalReplicaMaximum, fmt.Sprintf("%s %d exceeds enforced maximum %d", w.replicasPath, *w.replicas, *max))
	}
	if s := w.storage; s != nil {
		claims := []fieldResources{{"storage.volumeClaimTemplate.spec.resources.requests.storage", corev1.ResourceRequirements{Requests: s.VolumeClaimTemplate.Spec.Resources.Requests}}}
		if e := s.Ephemeral; e != nil && e.VolumeClaimTemplate != nil {
			claims = append(claims, fieldResources{"storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage", corev1.ResourceRequirements{Requests: e.VolumeClaimTemplate.Spec.Resources.Requests}})
		}
		for _, claim := range claims {
			if q, ok := claim.resources.Requests[corev1.ResourceStorage]; ok {
				if err := enforceMaxStorageAt(q.String(), p.MaxStorageSize(), claim.path); err != nil {
					return err
				}
			}
		}
	}
	for _, r := range w.resources {
		if err := enforceMaxContainerResources(r.resources, p); err != nil {
			return errors.Wrap(err, r.path)
		}
	}
	return enforcePodTemplatePolicy("", &w.pod, p)
}
