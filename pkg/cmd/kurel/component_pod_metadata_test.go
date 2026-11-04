package kurel

import (
	"maps"
	"reflect"
	"strings"
	"testing"
)

// The pod template metadata an author sets with `podMetadata` on the five
// controller kinds (go-kure/launcher#1178) and on the webservice and worker
// (go-kure/launcher#1216). The properties `labels` and
// `annotations` of the same component stay on the object (component_object_
// metadata_test.go); these tests hold that `podMetadata` reaches the pod
// template, and only it.

const (
	podMetadataLabelKey, podMetadataLabelValue           = "example.com/team", "payments"
	podMetadataAnnotationKey, podMetadataAnnotationValue = "example.com/owner", "payments team, room 4"
)

// podMetadataKinds maps each kind that takes `podMetadata` to the object kind it
// emits and the path of its pod template.
var podMetadataKinds = []struct {
	typ, kind string
	template  []string
	selector  bool
	job       bool
}{
	{typ: "deployment", kind: "Deployment", template: []string{"spec", "template"}, selector: true},
	// The webservice and worker lower to a deployment member and take the
	// property as it does (go-kure/launcher#1216).
	{typ: "webservice", kind: "Deployment", template: []string{"spec", "template"}, selector: true},
	{typ: "worker", kind: "Deployment", template: []string{"spec", "template"}, selector: true},
	{typ: "statefulset", kind: "StatefulSet", template: []string{"spec", "template"}, selector: true},
	{typ: "daemonset", kind: "DaemonSet", template: []string{"spec", "template"}, selector: true},
	{typ: "job", kind: "Job", template: []string{"spec", "template"}, job: true},
	{typ: "cronjob", kind: "CronJob", template: []string{"spec", "jobTemplate", "spec", "template"}, job: true},
}

func podMetadataProps(typ string, podMetadata any) map[string]any {
	props := maps.Clone(componentLabelFixtures[typ].props)
	if props == nil {
		props = map[string]any{}
	}
	if podMetadata != nil {
		props["podMetadata"] = podMetadata
	}
	return props
}

func nestedMap(m map[string]any, path ...string) map[string]any {
	for _, key := range path {
		next, _ := m[key].(map[string]any)
		if next == nil {
			return nil
		}
		m = next
	}
	return m
}

// kindDoc returns the emitted object of kind, or fails the test.
func kindDoc(t *testing.T, docs []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		if k, _ := doc["kind"].(string); k == kind {
			return doc
		}
	}
	t.Fatalf("no %s among the emitted objects", kind)
	return nil
}

// Every kind that takes it puts the authored labels and annotations on its
// pod template beside its own, and nowhere else: the object, the selector and
// the other objects are what the render without them gives.
func TestPodMetadata_ReachesThePodTemplateOnly(t *testing.T) {
	for _, k := range podMetadataKinds {
		t.Run(k.typ, func(t *testing.T) {
			plain := kindDoc(t, renderLabelInvariant(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, nil), "", nil)), k.kind)
			docs := renderLabelInvariant(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, map[string]any{
				"labels":      map[string]any{podMetadataLabelKey: podMetadataLabelValue},
				"annotations": map[string]any{podMetadataAnnotationKey: podMetadataAnnotationValue},
			}), "", nil))
			doc := kindDoc(t, docs, k.kind)

			tmpl := nestedMap(doc, append(k.template, "metadata")...)
			if got := tmpl["labels"].(map[string]any)[podMetadataLabelKey]; got != podMetadataLabelValue {
				t.Errorf("the pod template carries labels %v, want %s=%s", tmpl["labels"], podMetadataLabelKey, podMetadataLabelValue)
			}
			if got := tmpl["annotations"].(map[string]any)[podMetadataAnnotationKey]; got != podMetadataAnnotationValue {
				t.Errorf("the pod template carries annotations %v, want %s=%q", tmpl["annotations"], podMetadataAnnotationKey, podMetadataAnnotationValue)
			}
			// The kind's own labels stay on the template.
			plainLabels := nestedMap(plain, append(k.template, "metadata")...)["labels"].(map[string]any)
			for key, want := range plainLabels {
				if got := tmpl["labels"].(map[string]any)[key]; got != want {
					t.Errorf("the pod template lost its label %s=%v (now %v)", key, want, got)
				}
			}
			if meta := nestedMap(doc, "metadata"); meta["labels"].(map[string]any)[podMetadataLabelKey] != nil || meta["annotations"] != nil {
				t.Errorf("the object carries labels %v and annotations %v, want none of the pod template ones", meta["labels"], meta["annotations"])
			}
			if k.selector {
				if sel := nestedMap(doc, "spec", "selector", "matchLabels"); sel[podMetadataLabelKey] != nil {
					t.Errorf("the selector is %v, want no authored label in it", sel)
				}
			}
			// Taking the authored keys back off leaves the render without them.
			delete(tmpl["labels"].(map[string]any), podMetadataLabelKey)
			delete(tmpl, "annotations")
			if !reflect.DeepEqual(doc, plain) {
				t.Errorf("the %s differs from the render without podMetadata in more than the authored keys:\n got %v\nwant %v", k.kind, doc, plain)
			}
		})
	}
}

// A document that writes a key the kind sets to another value, a key the Job
// controller owns, or a shape the property does not take is refused with a reason.
func TestPodMetadata_Refusals(t *testing.T) {
	cases := []struct {
		name        string
		podMetadata any
		jobOnly     bool
		want        string
	}{
		{"app label with another value", map[string]any{"labels": map[string]any{"app": "other"}}, false, `podMetadata.labels["app"]`},
		{"unknown key beside labels", map[string]any{"selector": map[string]any{}}, false, `unsupported field "selector"`},
		{"label value not a string", map[string]any{"labels": map[string]any{"example.com/n": 3}}, false, "podMetadata.labels"},
		{"invalid label key", map[string]any{"labels": map[string]any{"-bad": "x"}}, false, "podMetadata.labels"},
		{"invalid annotation key", map[string]any{"annotations": map[string]any{"bad key": "x"}}, false, "podMetadata.annotations"},
		{"annotations over the size limit", map[string]any{"annotations": map[string]any{"example.com/big": strings.Repeat("x", 256*1024+1)}}, false, "limit"},
		{"job-name", map[string]any{"labels": map[string]any{"job-name": "x"}}, true, "Job controller"},
		{"batch.kubernetes.io/controller-uid", map[string]any{"labels": map[string]any{"batch.kubernetes.io/controller-uid": "x"}}, true, "Job controller"},
		{"not an object", "labels", false, "podMetadata"},
	}
	for _, k := range podMetadataKinds {
		for _, c := range cases {
			if c.jobOnly && !k.job {
				continue
			}
			t.Run(k.typ+"/"+c.name, func(t *testing.T) {
				_, err := renderLabelInvariantErr(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, c.podMetadata), "", nil))
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err = %v\nwant a refusal naming %q", err, c.want)
				}
			})
		}
	}
}

// The Job controller keys are refused on a Job and a CronJob only: on the
// others they are ordinary labels.
func TestPodMetadata_JobKeysAreOrdinaryElsewhere(t *testing.T) {
	for _, k := range podMetadataKinds {
		if k.job {
			continue
		}
		t.Run(k.typ, func(t *testing.T) {
			docs := renderLabelInvariant(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, map[string]any{
				"labels": map[string]any{"job-name": "nightly"},
			}), "", nil))
			tmpl := nestedMap(kindDoc(t, docs, k.kind), append(k.template, "metadata", "labels")...)
			if tmpl["job-name"] != "nightly" {
				t.Errorf("the pod template carries labels %v, want job-name=nightly", tmpl)
			}
		})
	}
}

// A bad podMetadata on a webservice or a worker is refused by the role's own
// parse, naming the property and the component, as the rest of the role's
// properties are (go-kure/launcher#1216).
func TestPodMetadata_RoleRefusalNamesTheProperty(t *testing.T) {
	for _, typ := range []string{"webservice", "worker"} {
		t.Run(typ, func(t *testing.T) {
			_, err := renderLabelInvariantErr(t, labelInvariantApp("widget", typ, podMetadataProps(typ, map[string]any{
				"labels": map[string]any{"-bad": "x"},
			}), "", nil))
			if err == nil {
				t.Fatal("want a key that is no valid label refused")
			}
			for _, want := range []string{"widget", `podMetadata.labels`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v\nwant it to name %q", err, want)
				}
			}
		})
	}
}

// The Service a webservice generates selects on the component label alone, so an
// authored label is on the pods and not in the Service's selector.
func TestPodMetadata_WebserviceServiceSelectorUnchanged(t *testing.T) {
	plain := kindDoc(t, renderLabelInvariant(t, labelInvariantApp("widget", "webservice", podMetadataProps("webservice", nil), "", nil)), "Service")
	docs := renderLabelInvariant(t, labelInvariantApp("widget", "webservice", podMetadataProps("webservice", map[string]any{
		"labels": map[string]any{podMetadataLabelKey: podMetadataLabelValue},
	}), "", nil))
	if got := kindDoc(t, docs, "Service"); !reflect.DeepEqual(got, plain) {
		t.Errorf("the Service differs from the render without podMetadata:\n got %v\nwant %v", got, plain)
	}
}

// The component label is the platform's: an authored value other than the
// component's is refused on every kind (the check that reads each generated pod
// template), and the component's own value is accepted.
func TestPodMetadata_ComponentLabelIsHeld(t *testing.T) {
	for _, k := range podMetadataKinds {
		t.Run(k.typ, func(t *testing.T) {
			_, err := renderLabelInvariantErr(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, map[string]any{
				"labels": map[string]any{kurelComponentLabel: "other"},
			}), "", nil))
			if err == nil || !strings.Contains(err.Error(), kurelComponentLabel) {
				t.Fatalf("err = %v\nwant the component label with another value refused", err)
			}
			docs := renderLabelInvariant(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, map[string]any{
				"labels": map[string]any{kurelComponentLabel: "widget"},
			}), "", nil))
			labels := nestedMap(kindDoc(t, docs, k.kind), append(k.template, "metadata", "labels")...)
			if labels[kurelComponentLabel] != "widget" {
				t.Errorf("the pod template carries labels %v, want %s=widget", labels, kurelComponentLabel)
			}
		})
	}
}

// A trait writes the pod spec only, so a pod template key the author set is
// what the render without the trait has.
func TestPodMetadata_TraitsLeaveAuthoredKeys(t *testing.T) {
	podMetadata := map[string]any{
		"labels":      map[string]any{podMetadataLabelKey: podMetadataLabelValue},
		"annotations": map[string]any{podMetadataAnnotationKey: podMetadataAnnotationValue},
	}
	for _, k := range podMetadataKinds {
		t.Run(k.typ, func(t *testing.T) {
			docs := renderLabelInvariant(t, labelInvariantApp("widget", k.typ, podMetadataProps(k.typ, podMetadata), "security-context", map[string]any{"psaLevel": "baseline"}))
			tmpl := nestedMap(kindDoc(t, docs, k.kind), append(k.template, "metadata")...)
			if tmpl["labels"].(map[string]any)[podMetadataLabelKey] != podMetadataLabelValue ||
				tmpl["annotations"].(map[string]any)[podMetadataAnnotationKey] != podMetadataAnnotationValue {
				t.Errorf("the pod template carries labels %v and annotations %v, want the authored ones", tmpl["labels"], tmpl["annotations"])
			}
		})
	}
}
