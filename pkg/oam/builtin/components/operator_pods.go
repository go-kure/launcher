package components

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// What the kinds of the Prometheus operator's API refuse of the pods the
// operator builds from their spec, where the API admits the spec and the
// operator or the API then refuses the StatefulSet or the pods. The checks name
// no kind: each kind passes what its pods are, the names and paths the
// operator gives its own volumes, mounts and ports and the names it derives
// from the object's, and cites where its operator code does so.

// reservedPattern is a family of names the operator gives something of its
// own, as a numbered series of rule ConfigMaps, and what a name of it is,
// worded to follow "is".
type reservedPattern struct {
	re   *regexp.Regexp
	what string
}

// reservedAs says what a name is where the operator reserves it, by fixed
// name or by pattern.
func reservedAs(fixed map[string]string, patterns []reservedPattern, name string) (string, bool) {
	if what, ok := fixed[name]; ok {
		return what, true
	}
	for _, p := range patterns {
		if p.re.MatchString(name) {
			return p.what, true
		}
	}
	return "", false
}

// validateOperatorPortName refuses a portName the API refuses where the
// operator writes it: written says it names a container port or a governing
// Service's port, and reserved holds the names of the ports the operator adds
// beside it there, each with what it is, worded to follow "is". A container
// port's name and a target port name must be an IANA service name, and port
// names are unique within a container and within a Service. Empty, the CRD
// defaults it to web.
func validateOperatorPortName(name string, written bool, reserved map[string]string) error {
	if name == "" || !written {
		return nil
	}
	if errs := validation.IsValidPortName(name); len(errs) > 0 {
		return errors.Errorf("portName: %q is not a valid port name: %s; the Prometheus operator names the web port with it, which the API then refuses", name, strings.Join(errs, "; "))
	}
	if what, ok := reserved[name]; ok {
		return errors.Errorf("portName: %q is the name of %s, and the API refuses a port name twice; name the web port otherwise", name, what)
	}
	return nil
}

// refuseNegativeReplicas refuses a replica count below 0, which the CRD
// admits; consequence says what the operator then does with it, and what to
// write instead.
func refuseNegativeReplicas(replicas *int32, consequence string) error {
	if replicas != nil && *replicas < 0 {
		return errors.Errorf("replicas: %d is below 0: %s", *replicas, consequence)
	}
	return nil
}

// validateOperatorStorage refuses a storage the operator builds into a volume
// claim the API refuses. The operator uses the first arm set of emptyDir,
// ephemeral and volumeClaimTemplate, and an emptyDir where storage is unset:
//   - an ephemeral volume's claim template is used as written, and the API
//     refuses one without access modes or a storage request;
//   - for the claim template arm, which a storage with no arm set selects, it
//     defaults access modes to ReadWriteOnce where none are written (an empty
//     list is not serialized, so it is none) and copies the resources as
//     written, so a claim without a storage request is refused when the
//     StatefulSet controller creates it.
//
// The claim template's name, which the operator mounts the data volume under
// whatever arm is in use, is held by validateOperatorObjectName, which knows
// the name the operator gives the volume.
func validateOperatorStorage(s *monitoringv1.StorageSpec) error {
	if s == nil || s.EmptyDir != nil {
		return nil
	}
	if s.Ephemeral != nil {
		t := s.Ephemeral.VolumeClaimTemplate
		if t == nil {
			return errors.New("storage.ephemeral.volumeClaimTemplate: required: the API refuses an ephemeral volume without one")
		}
		if len(t.Spec.AccessModes) == 0 {
			return errors.New("storage.ephemeral.volumeClaimTemplate.spec.accessModes: required: the API refuses an ephemeral volume's claim without access modes")
		}
		q, ok := t.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			return errors.New("storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage: required: the API refuses an ephemeral volume's claim without a storage request")
		}
		return positiveOperatorStorage("storage.ephemeral.volumeClaimTemplate", q)
	}
	q, ok := s.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]
	if !ok {
		return errors.New("storage.volumeClaimTemplate.spec.resources.requests.storage: required where neither storage.emptyDir nor storage.ephemeral is set: the Prometheus operator then claims the data volume from this template as written, and the API refuses a claim without a storage request")
	}
	return positiveOperatorStorage("storage.volumeClaimTemplate", q)
}

// positiveOperatorStorage refuses the storage request of a claim template in
// use that is not above 0: the operator passes it on as written, and the API
// refuses a claim whose storage request is not positive
// (ValidatePersistentVolumeClaimSpec, k8s.io/kubernetes
// pkg/apis/core/validation/validation.go).
func positiveOperatorStorage(template string, q resource.Quantity) error {
	if q.Sign() <= 0 {
		return errors.Errorf("%s.spec.resources.requests.storage: %s is not above 0: the Prometheus operator claims the data volume with it as written, and the API refuses a claim whose storage request is not positive; request more", template, q.String())
	}
	return nil
}

// invalidDNS1123Characters is what the operator replaces in a name it derives
// a volume's from (pkg/k8s/resource_namer.go at prometheus-operator v0.94.1).
var invalidDNS1123Characters = regexp.MustCompile("[^-a-z0-9]+")

// operatorSourceVolume is the name the operator gives the volume of an entry
// of secrets or configMaps: the prefix and the entry, lower-cased, each run of
// other characters than a-z, 0-9 and - replaced by -, trimmed of - and cut to
// 63 characters (ResourceNamer.DNS1123Label, pkg/k8s/resource_namer.go at
// prometheus-operator v0.94.1).
func operatorSourceVolume(prefix, entry string) string {
	name := strings.Trim(invalidDNS1123Characters.ReplaceAllString(strings.ToLower(prefix+"-"+entry), "-"), "-")
	if len(name) > validation.DNS1123LabelMaxLength {
		name = name[:validation.DNS1123LabelMaxLength]
	}
	return name
}

// operatorPodVolumes is what the operator puts into the pods' volumes, and
// what the spec adds: generated and patterns are the volumes it adds under a
// name it chooses, each with what it is, worded to follow "is"; secrets and
// configMaps the entries it adds a volume for each of (operatorSourceVolume).
// The data volume is not among them: validateOperatorObjectName holds it.
type operatorPodVolumes struct {
	generated           map[string]string
	patterns            []reservedPattern
	secrets, configMaps []string
	storage             *monitoringv1.StorageSpec
	volumes             []corev1.Volume
}

// refuseGeneratedVolumes refuses an entry of volumes named as a volume the
// operator adds to the pods: it appends the listed volumes after its own, and
// the API refuses a pod with two volumes of one name.
//
// Two entries of secrets, or of configMaps, whose volumes the operator gives
// one name are refused for the same reason: it adds a volume for each, so the
// pods would have two of that name. An entry whose volume name is not a
// DNS-1123 label once cut to 63 characters (one cut after a -) is refused: the
// operator checks the name after the cut and fails the reconcile
// (ResourceNamer.DNS1123Label; pkg/alertmanager/statefulset.go:640-643, and
// pkg/prometheus/common.go:289-292 and :311-314, returned at
// pkg/prometheus/server/statefulset.go:181-184 at v0.94.1). On the claim
// template arm, a claim template named as a volume the operator adds is
// refused: the StatefulSet controller replaces the pod's volume of that name
// with the claim, so the pods would not get the operator's volume.
func refuseGeneratedVolumes(v operatorPodVolumes) error {
	generated := make(map[string]string, len(v.generated))
	maps.Copy(generated, v.generated)
	for _, source := range []struct {
		field, prefix string
		names         []string
	}{{"secrets", "secret", v.secrets}, {"configMaps", "configmap", v.configMaps}} {
		for i, entry := range source.names {
			volume := operatorSourceVolume(source.prefix, entry)
			if errs := validation.IsDNS1123Label(volume); len(errs) > 0 {
				return errors.Errorf("%s[%d] %q: the Prometheus operator names its volume %q, which is not a DNS-1123 label: %s; the operator then fails to build the pods; list a name whose first 63 characters, with the prefix, end in a letter or a digit", source.field, i, entry, volume, strings.Join(errs, "; "))
			}
			if what, ok := reservedAs(generated, v.patterns, volume); ok {
				return errors.Errorf("%s[%d] %q: the Prometheus operator names its volume %q, which is %s, and the API refuses a pod with two volumes of one name; list each %s once, under names that differ in lower case and in their runs of a-z, 0-9 and -", source.field, i, entry, volume, what, source.prefix)
			}
			generated[volume] = fmt.Sprintf("the volume the Prometheus operator adds for %s[%d]", source.field, i)
		}
	}
	if s := v.storage; s != nil && s.EmptyDir == nil && s.Ephemeral == nil {
		if what, ok := reservedAs(generated, v.patterns, s.VolumeClaimTemplate.Name); ok {
			return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q is %s, and the StatefulSet controller replaces the pod's volume of the claim template's name with the claim, so the pods would not get it; name the claim template otherwise", s.VolumeClaimTemplate.Name, what)
		}
	}
	for i, vol := range v.volumes {
		if what, ok := reservedAs(generated, v.patterns, vol.Name); ok {
			return errors.Errorf("volumes[%d] %q: the name is %s; name the volume otherwise", i, vol.Name, what)
		}
	}
	return nil
}

// operatorContainerMounts is what the operator mounts in one of the containers
// it generates, and what the spec adds there under field: generated and
// patterns are the paths it mounts a volume at whatever else the spec says,
// each with what it is, worded to follow "is"; secrets and configMaps the
// entries it mounts under root/secrets/<name> and root/configmaps/<name>.
type operatorContainerMounts struct {
	field               string
	generated           map[string]string
	patterns            []reservedPattern
	root                string
	secrets, configMaps []string
	mounts              []corev1.VolumeMount
}

// refuseGeneratedMounts refuses an entry of the field at a path the operator
// mounts a volume at in that container: it appends the field's entries to its
// own mounts, and the API refuses a container with two mounts at one path.
func refuseGeneratedMounts(m operatorContainerMounts) error {
	generated := make(map[string]string, len(m.generated))
	maps.Copy(generated, m.generated)
	for i, s := range m.secrets {
		generated[path.Join(m.root, "secrets", s)] = fmt.Sprintf("the path the Prometheus operator mounts secrets[%d] at", i)
	}
	for i, c := range m.configMaps {
		generated[path.Join(m.root, "configmaps", c)] = fmt.Sprintf("the path the Prometheus operator mounts configMaps[%d] at", i)
	}
	for i, vm := range m.mounts {
		if what, ok := reservedAs(generated, m.patterns, vm.MountPath); ok {
			return errors.Errorf("%s[%d] %q: the mount path is %s, and the API refuses a container with two mounts at one path; mount the volume elsewhere", m.field, i, vm.MountPath, what)
		}
	}
	return nil
}

// derivedName is a name the operator derives from the object's, which the API
// requires to be a DNS-1123 label, and what it names, worded to precede the
// name.
type derivedName struct {
	what, value string
}

// operatorObjectName is what validateOperatorObjectName reads: kind is the
// object's kind, and label the component type that names it in an error; name
// is the one the object takes and componentName the component's, to say where
// the name came from; dataVolume the name the operator gives the data volume;
// derived the other names it derives that must be DNS-1123 labels, as the
// hostname of the last pod; statefulSet the longest name of a StatefulSet the
// operator creates with pods, empty where none has any.
type operatorObjectName struct {
	kind, label         string
	name, componentName string
	dataVolume          string
	derived             []derivedName
	statefulSet         string
	storage             *monitoringv1.StorageSpec
	volumes             []corev1.Volume
}

// maxRevisedStatefulSetName is the longest StatefulSet name whose pods the API
// admits: the StatefulSet controller labels each pod controller-revision-hash
// with the name of the revision it runs, the StatefulSet's name, a - and a
// hash of up to 10 characters (setPodRevision and newVersionedStatefulSetPod,
// pkg/controller/statefulset/stateful_set_utils.go:506-544;
// ControllerRevisionName and HashControllerRevision,
// pkg/controller/history/controller_history.go:95-101 and :145, at Kubernetes
// v1.37.1), and the API refuses a label value over 63 characters.
const maxRevisedStatefulSetName = content.LabelValueMaxLength - 1 - 10

// validateOperatorObjectName refuses an object name the operator's objects
// cannot be named after, and a data volume the pods would not get. The API
// refuses a volume name and a pod hostname that is not a DNS-1123 label: at
// most 63 characters, and no dot; and a pod whose controller-revision-hash
// label is over 63 characters, so a StatefulSet name over
// maxRevisedStatefulSetName.
//
// The claim template's name, where it is set, is the name the operator mounts
// the data volume under, whatever arm is in use. On the claim template arm it
// names the claim too; beside emptyDir or ephemeral the operator creates the
// volume under its own name, so another name leaves the mount without a
// volume.
//
// An entry of volumes under the data volume's name is refused: beside emptyDir
// or ephemeral the API refuses a pod with two volumes of one name, and on the
// claim template arm the StatefulSet controller replaces the entry with the
// claim, so the volume authored there would not be the one the pods get.
func validateOperatorObjectName(n operatorObjectName) error {
	refuse := func(rule string, args ...any) error {
		if n.name == n.componentName {
			return errors.Errorf("%s %q: the component name is the %s's name, and "+rule, append([]any{n.label, n.name, n.kind}, args...)...)
		}
		return errors.Errorf("%s: %q is not a valid name for this %s: "+rule, append([]any{objectNameField, n.name, n.kind}, args...)...)
	}
	s := n.storage
	claimArm := s != nil && s.EmptyDir == nil && s.Ephemeral == nil
	volume := n.dataVolume
	if claimArm && s.VolumeClaimTemplate.Name != "" {
		volume = s.VolumeClaimTemplate.Name
	}
	if volume == n.dataVolume {
		if errs := validation.IsDNS1123Label(n.dataVolume); len(errs) > 0 {
			return refuse("the Prometheus operator names the data volume %q, which must be a DNS-1123 label: %s", n.dataVolume, strings.Join(errs, "; "))
		}
	} else if errs := validation.IsDNS1123Label(volume); len(errs) > 0 {
		return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q is not a DNS-1123 label: %s; the Prometheus operator names the data volume with it, which the API then refuses", volume, strings.Join(errs, "; "))
	}
	if s != nil && !claimArm && s.VolumeClaimTemplate.Name != "" && s.VolumeClaimTemplate.Name != n.dataVolume {
		return errors.Errorf("storage.volumeClaimTemplate.metadata.name: %q beside storage.emptyDir or storage.ephemeral: the Prometheus operator mounts the data volume under this name, but creates it from the arm in use as %q, so the pods would mount a volume they do not have; leave it unset", s.VolumeClaimTemplate.Name, n.dataVolume)
	}
	for _, d := range n.derived {
		if errs := validation.IsDNS1123Label(d.value); len(errs) > 0 {
			return refuse(d.what+" %q, which must be a DNS-1123 label: %s", d.value, strings.Join(errs, "; "))
		}
	}
	if len(n.statefulSet) > maxRevisedStatefulSetName {
		return refuse("the Prometheus operator names the StatefulSet %q, of %d characters, and the StatefulSet controller labels each of its pods controller-revision-hash with that name, a - and a hash of up to 10 characters, which the API refuses beyond %d characters; the StatefulSet's name must be at most %d", n.statefulSet, len(n.statefulSet), content.LabelValueMaxLength, maxRevisedStatefulSetName)
	}
	for i, v := range n.volumes {
		if v.Name != volume {
			continue
		}
		if claimArm {
			return errors.Errorf("volumes[%d] %q: the name is the data volume's claim template's, and the StatefulSet controller replaces a volume of that name with the claim, so the pods would not get this one; name the volume otherwise", i, v.Name)
		}
		return errors.Errorf("volumes[%d] %q: the name is the data volume's, which the Prometheus operator adds to the pods, and the API refuses a pod with two volumes of one name; name the volume otherwise", i, v.Name)
	}
	return nil
}
