package components

import (
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

// This file holds what the kind components share whose type holds a label
// selector decoded strictly, a metav1.LabelSelector or the copy of it Cilium's
// API declares (go-kure/launcher#790). The Go type writes a match expression's
// key and operator whether or not they were authored, and decodes any string
// as an operator, so the strict decode refuses nothing of an expression.
//
// Two checks follow it. labelSelectorRequired refuses an unauthored key or
// operator, read from what was authored. validateLabelSelector refuses, on the
// decoded value, what the API server's validation of a Kubernetes object
// refuses of an expression whatever the object and its options: see there.
// TestLabelSelectorKinds_CoverEverySelector holds every kind's type to the two,
// path by path, and lists the selectors left out with the reason.

// labelSelectorRequired is the required list of the label selectors under the
// paths at ("selector", "ingress[].from[].podSelector"): the key and the
// operator of each match expression (requiredfields.LabelSelector).
func labelSelectorRequired(at ...string) map[string]string {
	out := map[string]string{}
	for _, path := range at {
		maps.Copy(out, requiredfields.LabelSelector(path))
	}
	return out
}

// validateLabelSelector refuses a match expression of sel, the label selector
// at the path at, that the API server refuses on every Kubernetes object it
// validates a selector of: an operator that is none of the four, In or NotIn
// without a value, and Exists or DoesNotExist with one. A nil selector holds
// nothing to refuse.
//
// These are the rules ValidateLabelSelectorRequirement applies under every
// option the API server passes it (k8s.io/apimachinery
// pkg/apis/meta/v1/validation); TestValidateLabelSelector_MatchesAPIMachinery
// holds the two together on the linked module. That function judges more, and
// this one leaves it to the API server, as every kind leaves the API's value
// rules: that a key is a label name, that the keys and values of matchLabels
// are label names and values, and, on objects that still check it, that an
// expression's values are label values.
func validateLabelSelector(at string, sel *metav1.LabelSelector) error {
	if sel == nil {
		return nil
	}
	for i, expr := range sel.MatchExpressions {
		switch expr.Operator {
		case metav1.LabelSelectorOpIn, metav1.LabelSelectorOpNotIn:
			if len(expr.Values) == 0 {
				return errors.Errorf("%s.matchExpressions[%d].values: required with the operator %s (at least one value)", at, i, expr.Operator)
			}
		case metav1.LabelSelectorOpExists, metav1.LabelSelectorOpDoesNotExist:
			if len(expr.Values) > 0 {
				return errors.Errorf("%s.matchExpressions[%d].values: not allowed with the operator %s (it takes no value)", at, i, expr.Operator)
			}
		default:
			return errors.Errorf("%s.matchExpressions[%d].operator: %q is not a label selector operator (In, NotIn, Exists or DoesNotExist)", at, i, expr.Operator)
		}
	}
	return nil
}

// podSpecLabelSelectors lists the label selectors of a corev1.PodSpec whose
// fields sit under prefix in the authored properties ("" for the pod kind,
// "template.spec." for a pod template), as required-list paths: the label and
// namespace selectors of every pod affinity and anti-affinity term, the
// selector of a topology spread constraint, of a projected cluster trust
// bundle and of a generic ephemeral volume's claim.
func podSpecLabelSelectors(prefix string) []string {
	var paths []string
	for _, affinity := range []string{"podAffinity", "podAntiAffinity"} {
		at := prefix + "affinity." + affinity
		for _, term := range []string{
			at + ".requiredDuringSchedulingIgnoredDuringExecution[]",
			at + ".preferredDuringSchedulingIgnoredDuringExecution[].podAffinityTerm",
		} {
			paths = append(paths, term+".labelSelector", term+".namespaceSelector")
		}
	}
	return append(paths,
		prefix+"topologySpreadConstraints[].labelSelector",
		prefix+"volumes[].projected.sources[].clusterTrustBundle.labelSelector",
		prefix+"volumes[].ephemeral.volumeClaimTemplate.spec.selector",
	)
}

// validatePodSpecLabelSelectors holds every label selector of ps to
// validateLabelSelector: the ones podSpecLabelSelectors lists, under the same
// prefix. The API server validates each as a label selector
// (validatePodAffinityTerm, validateTopologySpreadConstraints,
// validateProjectionSources and ValidatePersistentVolumeClaimSpec in
// pkg/apis/core/validation, Kubernetes v1.37.1).
func validatePodSpecLabelSelectors(prefix string, ps *corev1.PodSpec) error {
	if a := ps.Affinity; a != nil {
		if p := a.PodAffinity; p != nil {
			if err := validatePodAffinityTermSelectors(prefix+"affinity.podAffinity", p.RequiredDuringSchedulingIgnoredDuringExecution, p.PreferredDuringSchedulingIgnoredDuringExecution); err != nil {
				return err
			}
		}
		if p := a.PodAntiAffinity; p != nil {
			if err := validatePodAffinityTermSelectors(prefix+"affinity.podAntiAffinity", p.RequiredDuringSchedulingIgnoredDuringExecution, p.PreferredDuringSchedulingIgnoredDuringExecution); err != nil {
				return err
			}
		}
	}
	for i := range ps.TopologySpreadConstraints {
		if err := validateLabelSelector(fmt.Sprintf("%stopologySpreadConstraints[%d].labelSelector", prefix, i), ps.TopologySpreadConstraints[i].LabelSelector); err != nil {
			return err
		}
	}
	for i := range ps.Volumes {
		volume := &ps.Volumes[i]
		if volume.Projected != nil {
			for j, source := range volume.Projected.Sources {
				if source.ClusterTrustBundle == nil {
					continue
				}
				if err := validateLabelSelector(fmt.Sprintf("%svolumes[%d].projected.sources[%d].clusterTrustBundle.labelSelector", prefix, i, j), source.ClusterTrustBundle.LabelSelector); err != nil {
					return err
				}
			}
		}
		if volume.Ephemeral != nil && volume.Ephemeral.VolumeClaimTemplate != nil {
			if err := validateLabelSelector(fmt.Sprintf("%svolumes[%d].ephemeral.volumeClaimTemplate.spec.selector", prefix, i), volume.Ephemeral.VolumeClaimTemplate.Spec.Selector); err != nil {
				return err
			}
		}
	}
	return nil
}

// validatePodAffinityTermSelectors holds the label and namespace selectors of
// the terms of one pod affinity or anti-affinity, at the path at, to
// validateLabelSelector.
func validatePodAffinityTermSelectors(at string, required []corev1.PodAffinityTerm, preferred []corev1.WeightedPodAffinityTerm) error {
	term := func(at string, t *corev1.PodAffinityTerm) error {
		if err := validateLabelSelector(at+".labelSelector", t.LabelSelector); err != nil {
			return err
		}
		return validateLabelSelector(at+".namespaceSelector", t.NamespaceSelector)
	}
	for i := range required {
		if err := term(fmt.Sprintf("%s.requiredDuringSchedulingIgnoredDuringExecution[%d]", at, i), &required[i]); err != nil {
			return err
		}
	}
	for i := range preferred {
		if err := term(fmt.Sprintf("%s.preferredDuringSchedulingIgnoredDuringExecution[%d].podAffinityTerm", at, i), &preferred[i].PodAffinityTerm); err != nil {
			return err
		}
	}
	return nil
}
