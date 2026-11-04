package components

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds the `podMetadata` property of the controller kind components
// whose spec carries a pod template the author does not write whole: deployment,
// statefulset, daemonset, job and cronjob (go-kure/launcher#1178), and the
// property the webservice and worker rules hand to their deployment member
// (go-kure/launcher#1216). The labels and
// annotations of a component's object go on the object's own metadata
// (oam.ObjectMetadata), and the pod template keeps the labels its kind gives it;
// `podMetadata` is the one input that reaches the pod template's metadata.
//
// The kind writes its labels first, the authored ones are added beside them, and
// an authored key the kind set to another value is refused (mergePodMetadata),
// as oam.ObjectMetadata.ApplyTo does for the object. The selector of the kind is
// a map of its own (appLabels, deploymentComponentLabels), so nothing authored
// here reaches it.
//
// What is left to the checks that already read every generated pod template
// (pkg/oam, component_label_check.go): the keys a consumer reserves
// (TransformContext.ReservedMetadataKeys) and the value of the component label
// key.

// podMetadataProperty is the property an author sets the pod template's labels
// and annotations with.
const podMetadataProperty = "podMetadata"

// PodMetadata is the labels and annotations an author gave the pod template of a
// controller kind component. The zero value is none.
type PodMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// jobControllerLabelKeys are the pod template labels the Job controller owns:
// the API server generates the selector of a Job from them. A job or cronjob
// component refuses them (parsePodMetadata), since a value authored here would
// meet the generated selector at admission. The refusal can be loosened later
// without invalidating a document; the other way round it could not.
var jobControllerLabelKeys = []string{
	"batch.kubernetes.io/controller-uid",
	"batch.kubernetes.io/job-name",
	"controller-uid",
	"job-name",
}

// schemaPodMetadata is the declaration of the `podMetadata` property. The two
// maps are open (a label key is the author's), the object around them is not:
// a key beside `labels` and `annotations` is refused.
func schemaPodMetadata(kind string) oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject,
		Description: "Labels and annotations of the " + kind + "'s pod template, so of every pod it creates. " +
			"They go on the pod template only, never on the object (use `labels` and `annotations` for that) and never in the selector. " +
			"A change to either is a pod template change: the controller's update strategy decides whether it reaches existing pods (a CronJob only changes the Jobs it creates later; the API server refuses the change on a Job that is not suspended). Refused: a key or value that is no valid label or annotation, " +
			"annotations over the size the API server accepts, `app` with another value than the component's, " +
			"and the component label key with another value than the component's. A key the platform reserves is refused as on any pod template.",
		Properties: map[string]oam.PropertySchema{
			"labels":      {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Labels for the pod template, as a map of strings."},
			"annotations": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Annotations for the pod template, as a map of strings."},
		},
	}
}

// parsePodMetadata reads the `podMetadata` property off props. job says the
// component's pods belong to a Job, whose controller owns four label keys
// (jobControllerLabelKeys). An absent or null property, and a null entry, is
// none. Keys are read in sorted order, so the one refused is the same on every
// build.
func parsePodMetadata(props map[string]any, job bool) (PodMetadata, error) {
	raw, present, err := parseObjectField(props, podMetadataProperty, podMetadataProperty)
	if err != nil || !present {
		return PodMetadata{}, err
	}
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		if key != "labels" && key != "annotations" {
			return PodMetadata{}, errors.Errorf("%s.%s: not a property of %s, which holds labels and annotations", podMetadataProperty, key, podMetadataProperty)
		}
	}
	var m PodMetadata
	if rawLabels, present, err := parseObjectField(raw, "labels", podMetadataProperty+".labels"); err != nil {
		return PodMetadata{}, err
	} else if present {
		if m.Labels, err = parseLabelMap(rawLabels, podMetadataProperty+".labels"); err != nil {
			return PodMetadata{}, err
		}
	}
	if rawAnnotations, present, err := parseObjectField(raw, "annotations", podMetadataProperty+".annotations"); err != nil {
		return PodMetadata{}, err
	} else if present {
		if m.Annotations, err = stringMapStrict(rawAnnotations, podMetadataProperty+".annotations"); err != nil {
			return PodMetadata{}, err
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.Annotations)) {
		// The API server validates an annotation key in lower case
		// (ValidateAnnotations, k8s.io/apimachinery/pkg/api/validation).
		if errs := validation.IsQualifiedName(strings.ToLower(key)); len(errs) > 0 {
			return PodMetadata{}, errors.Errorf("%s.annotations[%q]: not a valid annotation key: %s", podMetadataProperty, key, strings.Join(errs, "; "))
		}
	}
	if size := podAnnotationsSize(m.Annotations); size > apivalidation.TotalAnnotationSizeLimitB {
		return PodMetadata{}, errors.Errorf("%s.annotations: the keys and values hold %d bytes, over the %d-byte limit of an object's annotations", podMetadataProperty, size, apivalidation.TotalAnnotationSizeLimitB)
	}
	if job {
		for _, key := range jobControllerLabelKeys {
			if _, has := m.Labels[key]; has {
				return PodMetadata{}, errors.Errorf("%s.labels[%q]: the Job controller owns this label: the API server generates the selector of a Job from it; remove the label", podMetadataProperty, key)
			}
		}
	}
	return m, nil
}

// applyTo adds the authored labels and annotations to t, beside the ones the kind
// set. A key the kind set to another value is refused, and the annotations are
// held to the API server's size limit again with the ones the kind set. The maps
// t held are left as they are: t gets maps of its own, since a config may use one
// label map for an object, its selector and its pod template. A refusal leaves t
// as it was.
func (m PodMetadata) applyTo(t *corev1.PodTemplateSpec) error {
	labels, err := mergePodMetadata(t.Labels, m.Labels, "labels")
	if err != nil {
		return err
	}
	annotations, err := mergePodMetadata(t.Annotations, m.Annotations, "annotations")
	if err != nil {
		return err
	}
	if len(m.Annotations) > 0 {
		if size := podAnnotationsSize(annotations); size > apivalidation.TotalAnnotationSizeLimitB {
			return errors.Errorf("%s.annotations: with the annotations the component sets on its pod template, the keys and values hold %d bytes, over the %d-byte limit of an object's annotations", podMetadataProperty, size, apivalidation.TotalAnnotationSizeLimitB)
		}
	}
	if len(m.Labels) > 0 {
		t.Labels = labels
	}
	if len(m.Annotations) > 0 {
		t.Annotations = annotations
	}
	return nil
}

// labelsWith returns the labels the kind gave its pods with the authored ones
// added: what applyTo leaves on the pod template, for a caller that reads the
// labels without a template. The kind's own win, as applyTo refuses any other
// value.
func (m PodMetadata) labelsWith(own map[string]string) map[string]string {
	out := make(map[string]string, len(own)+len(m.Labels))
	maps.Copy(out, m.Labels)
	maps.Copy(out, own)
	return out
}

// mergePodMetadata returns a copy of own with authored added. Keys are read in
// sorted order, so the one refused is the same on every build.
func mergePodMetadata(own, authored map[string]string, field string) (map[string]string, error) {
	merged := make(map[string]string, len(own)+len(authored))
	maps.Copy(merged, own)
	for _, key := range slices.Sorted(maps.Keys(authored)) {
		if set, has := own[key]; has && set != authored[key] {
			return nil, errors.Errorf("%s.%s[%q]: %q is not the value the component sets on its pod template (%q); remove the key, or write that value", podMetadataProperty, field, key, authored[key], set)
		}
		merged[key] = authored[key]
	}
	return merged, nil
}

// podAnnotationsSize is the size the API server holds annotations to: the bytes
// of every key and value (ValidateAnnotations).
func podAnnotationsSize(annotations map[string]string) int {
	var size int
	for key, value := range annotations {
		size += len(key) + len(value)
	}
	return size
}
