package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// refuseOmittedPodSpecFields refuses a pod spec that leaves out a field the
// schema of the CRD that embeds it requires and the Go type omits when it is
// empty, so that the object would show the omission and the API server refuse
// it: the action of a container restart rule, the operator of its exit codes,
// and the signer name and key type of a pod certificate source. path is the
// pod spec's, for the refusal.
//
// It is for a kind whose CRD publishes the pod spec's schema. The kinds of the
// built-in pod types do not call it: no linked module shows that the API
// server refuses these omissions on a Pod.
func refuseOmittedPodSpecFields(path string, spec *corev1.PodSpec) error {
	for i := range spec.Containers {
		if err := refuseOmittedRestartRuleFields(fmt.Sprintf("%s.containers[%d]", path, i), spec.Containers[i].RestartPolicyRules); err != nil {
			return err
		}
	}
	for i := range spec.InitContainers {
		if err := refuseOmittedRestartRuleFields(fmt.Sprintf("%s.initContainers[%d]", path, i), spec.InitContainers[i].RestartPolicyRules); err != nil {
			return err
		}
	}
	for i := range spec.EphemeralContainers {
		if err := refuseOmittedRestartRuleFields(fmt.Sprintf("%s.ephemeralContainers[%d]", path, i), spec.EphemeralContainers[i].RestartPolicyRules); err != nil {
			return err
		}
	}
	for i := range spec.Volumes {
		projected := spec.Volumes[i].Projected
		if projected == nil {
			continue
		}
		if err := refuseOmittedPodCertificateFields(fmt.Sprintf("%s.volumes[%d].projected.sources", path, i), projected.Sources); err != nil {
			return err
		}
	}
	return nil
}

// refuseNullPodSpecFields refuses a pod spec that leaves out a field the
// schema of the CRD that embeds it requires and the Go type writes as null
// when nothing was decoded into it: the terms of a required node affinity, the
// priority of an eviction responder, the monitors of a CephFS or an RBD
// volume, and the Secret reference of a ScaleIO volume. The API server drops a
// null of a field that is not nullable before it validates, so the object
// would show the omission and the API server refuse it. An authored empty
// list is written as one and is not refused here. path is the pod spec's, for
// the refusal.
//
// It is for a kind whose CRD publishes the pod spec's schema, as
// refuseOmittedPodSpecFields is. The list of a pod's containers is not held
// here: a kind that requires it writes the list itself.
func refuseNullPodSpecFields(path string, spec *corev1.PodSpec) error {
	if spec.Affinity != nil {
		if err := refuseNullNodeSelectorTerms(path+".affinity.nodeAffinity", spec.Affinity.NodeAffinity); err != nil {
			return err
		}
	}
	for i := range spec.EvictionResponders {
		if spec.EvictionResponders[i].Priority == nil {
			return errors.Errorf("%s.evictionResponders[%d].priority: required (the responder's priority, from 0 to 100000; no default is filled)", path, i)
		}
	}
	for i := range spec.Volumes {
		volume := &spec.Volumes[i]
		switch {
		case volume.RBD != nil && volume.RBD.CephMonitors == nil:
			return errors.Errorf("%s.volumes[%d].rbd.monitors: required (the addresses of the Ceph monitors)", path, i)
		case volume.CephFS != nil && volume.CephFS.Monitors == nil:
			return errors.Errorf("%s.volumes[%d].cephfs.monitors: required (the addresses of the Ceph monitors)", path, i)
		case volume.ScaleIO != nil && volume.ScaleIO.SecretRef == nil:
			return errors.Errorf("%s.volumes[%d].scaleIO.secretRef: required (the Secret that holds the ScaleIO credentials)", path, i)
		}
	}
	return nil
}

// refuseNullNodeSelectorTerms refuses the node affinity at path whose required
// arm is authored without its terms: the Go type writes nodeSelectorTerms:
// null, which the schema of a CRD that embeds the type requires and the API
// server drops before it validates. A nil affinity holds nothing to refuse.
func refuseNullNodeSelectorTerms(path string, affinity *corev1.NodeAffinity) error {
	if affinity == nil {
		return nil
	}
	if required := affinity.RequiredDuringSchedulingIgnoredDuringExecution; required != nil && required.NodeSelectorTerms == nil {
		return errors.Errorf("%s.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms: required (the node selector terms, of which a node must match one)", path)
	}
	return nil
}

// refuseOmittedRestartRuleFields refuses a restart rule of the container at
// path without an action, or with exit codes without an operator.
func refuseOmittedRestartRuleFields(path string, rules []corev1.ContainerRestartRule) error {
	for i, rule := range rules {
		switch {
		case rule.Action == "":
			return errors.Errorf("%s.restartPolicyRules[%d].action: required (what is done when the rule matches, such as Restart)", path, i)
		case rule.ExitCodes != nil && rule.ExitCodes.Operator == "":
			return errors.Errorf("%s.restartPolicyRules[%d].exitCodes.operator: required (how the container's exit code is matched against the values: In or NotIn)", path, i)
		}
	}
	return nil
}

// refuseOmittedPodCertificateFields refuses a pod certificate source, among
// the sources of the projected volume at path, without a signer name or a key
// type.
func refuseOmittedPodCertificateFields(path string, sources []corev1.VolumeProjection) error {
	for i, source := range sources {
		cert := source.PodCertificate
		switch {
		case cert == nil:
		case cert.SignerName == "":
			return errors.Errorf("%s[%d].podCertificate.signerName: required (the signer the certificate is requested from)", path, i)
		case cert.KeyType == "":
			return errors.Errorf("%s[%d].podCertificate.keyType: required (the type of the private key generated for the pod, such as ECDSAP384)", path, i)
		}
	}
	return nil
}
