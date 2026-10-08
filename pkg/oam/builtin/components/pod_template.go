package components

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components share whose spec carries a pod
// template as its `template` property (go-kure/launcher#790): the pod spec in
// it is held to what the pod kind holds its own to, and the pods a controller
// creates from it carry the `app` label.

// podTemplateSchema is the schema of a `template` property: an open object
// decoded strictly into corev1.PodTemplateSpec.
func podTemplateSchema(desc string) oam.PropertySchema {
	return podObjectSchema(desc, "PodTemplateSpec")
}

// podTemplateDefaultedZeros is the defaulted-zero list of a spec whose pod
// template is its `template` property: the pod spec's list
// (podSpecDefaultedZeros) under template.spec, for tmpl, the decoded template
// (nil when none is authored).
// TestPodTemplateKindsDefaultedZeros_MatchFieldDocs holds its numbers to the
// linked types of each kind that uses it.
func podTemplateDefaultedZeros(tmpl *corev1.PodTemplateSpec) defaultedZeroFields {
	var ps *corev1.PodSpec
	if tmpl != nil {
		ps = &tmpl.Spec
	}
	return podSpecDefaultedZeros("template.spec.", ps)
}

// podTemplateLabelSelectorRequired is the required list of the label selectors
// of a spec whose pod template is its `template` property: the key and the
// operator of each match expression (labelSelectorRequired) of the pod spec's
// selectors (podSpecLabelSelectors) under template.spec.
func podTemplateLabelSelectorRequired() map[string]string {
	return labelSelectorRequired(podSpecLabelSelectors("template.spec.")...)
}

// controllerActiveDeadlineReason is the error text, after the path of the pod
// spec, for an activeDeadlineSeconds on the pod template of a controller that
// keeps its pods running.
const controllerActiveDeadlineReason = "activeDeadlineSeconds: not supported — the API server forbids it on the pod template of a ReplicaSet or ReplicationController, whose pods are restarted and replaced for as long as the controller exists; a pod or a Job may set it"

// validateControllerPodTemplate refuses, with or without an environment
// policy, what the pod template of a ReplicaSet or ReplicationController may
// not hold: what a pod authored through a kind component may not
// (validateAuthoredPodSpec), and activeDeadlineSeconds, which the API server's
// validation of both kinds forbids, as cnpg-pooler refuses it on its template.
// The API's other value rules, restartPolicy among them, are left to the API
// server.
func validateControllerPodTemplate(t *corev1.PodTemplateSpec) error {
	if err := validateAuthoredPodSpec("template.spec.", &t.Spec); err != nil {
		return err
	}
	if t.Spec.ActiveDeadlineSeconds != nil {
		return errors.New("template.spec." + controllerActiveDeadlineReason)
	}
	return nil
}

// podTemplateAppLabels returns the labels of the pods a controller component
// creates: the authored template labels and the `app` label every workload
// kind gives its pods (appLabels), which launcher's traits and Services select
// on. The map is the caller's own; authored is not written.
//
// An authored `app` with another value is refused: kept, the component's
// NetworkPolicies and a Service fronting it would select no pod of it, and
// overwritten, an authored selector on that label would stop matching the
// template. An authored `app` with the component's value is the label itself.
func podTemplateAppLabels(component string, authored map[string]string) (map[string]string, error) {
	want := appLabels(component)
	if got, ok := authored["app"]; ok && got != want["app"] {
		return nil, errors.Errorf("template.metadata.labels.app: %q is not the `app` label of component %q (%q): launcher sets that label on the component's pods, and its traits and Services select them by it; remove the label, or write that value", got, component, want["app"])
	}
	maps.Copy(want, authored)
	return want, nil
}

// refuseSelectorAgainstAppLabel refuses a label selector that matches the
// authored template labels and stops matching once the `app` label is added:
// one that requires `app` to be absent or to have another value. The
// controller would be refused by the API server for a label the author did not
// write. A selector that does not match the authored labels either is the API
// server's to refuse. One that cannot be read as a selector at all (an unknown
// operator, a key or value that is no label) cannot be compared, and is
// refused with the reason apimachinery gives.
func refuseSelectorAgainstAppLabel(component string, selector *metav1.LabelSelector, authored, labelled map[string]string) error {
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return errors.Wrap(err, "selector")
	}
	if !sel.Matches(labels.Set(authored)) || sel.Matches(labels.Set(labelled)) {
		return nil
	}
	return errors.Errorf("selector: rules out the label `app: %s`, which launcher sets on the pods of component %q, so the selector would not match the pod template; select on other labels", labelled["app"], component)
}
