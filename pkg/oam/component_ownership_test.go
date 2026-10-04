package oam

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

const ownershipKey = "launcher.gokure.dev/component"

// podTemplateLabelsOf returns obj's pod template labels and whether obj is of
// a kind that has a pod template, typed or unstructured.
func podTemplateLabelsOf(t *testing.T, obj client.Object) (map[string]string, bool) {
	t.Helper()
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return o.Spec.Template.Labels, true
	case *appsv1.StatefulSet:
		return o.Spec.Template.Labels, true
	case *appsv1.DaemonSet:
		return o.Spec.Template.Labels, true
	case *batchv1.Job:
		return o.Spec.Template.Labels, true
	case *batchv1.CronJob:
		return o.Spec.JobTemplate.Spec.Template.Labels, true
	case *appsv1.ReplicaSet:
		return o.Spec.Template.Labels, true
	case *corev1.ReplicationController:
		if o.Spec.Template == nil {
			return nil, true
		}
		return o.Spec.Template.Labels, true
	case *corev1.PodTemplate:
		return o.Template.Labels, true
	case *unstructured.Unstructured:
		path := []string{"spec", "template", "metadata", "labels"}
		switch o.GetKind() {
		case "CronJob":
			path = []string{"spec", "jobTemplate", "spec", "template", "metadata", "labels"}
		case "PodTemplate":
			path = []string{"template", "metadata", "labels"}
		}
		labels, _, err := unstructured.NestedStringMap(o.Object, path...)
		if err != nil {
			t.Fatalf("pod template labels of %s %q: %v", o.GetKind(), o.GetName(), err)
		}
		return labels, true
	}
	return nil, false
}

// unstructuredWorkload is an object of kind with an unlabelled pod template
// where the kind holds it: under its spec, under a CronJob's job template, or
// on a PodTemplate itself.
func unstructuredWorkload(apiVersion, kind string) *unstructured.Unstructured {
	template := map[string]any{"spec": map[string]any{"containers": []any{}}}
	if kind == "PodTemplate" {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata":   map[string]any{"name": "w"},
			"template":   template,
		}}
	}
	spec := map[string]any{"template": template}
	if kind == "CronJob" {
		spec = map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": template}}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "w"},
		"spec":       spec,
	}}
}

// unstructuredReplicationController has the selector of its kind: a plain label
// map, here with a key a label selector has as a field of its own. It holds no
// label back.
func unstructuredReplicationController() *unstructured.Unstructured {
	u := unstructuredWorkload("v1", "ReplicationController")
	selector := map[string]any{"app": "web", "matchLabels": "kept"}
	_ = unstructured.SetNestedMap(u.Object, selector, "spec", "selector")
	_ = unstructured.SetNestedMap(u.Object, selector, "spec", "template", "metadata", "labels")
	return u
}

// TestStampComponentLabel_ReplicationControllerWithoutTemplate: a typed
// ReplicationController with no pod template is labelled and otherwise left as
// it is.
func TestStampComponentLabel_ReplicationControllerWithoutTemplate(t *testing.T) {
	rc := &corev1.ReplicationController{}
	if err := stampComponentLabel(rc, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	if got := rc.Labels[ownershipKey]; got != "web" {
		t.Errorf("object label = %q, want web", got)
	}
	if rc.Spec.Template != nil {
		t.Errorf("pod template = %+v, want none", rc.Spec.Template)
	}
}

// TestStampComponentLabel_Workloads: every workload kind and a PodTemplate,
// typed and unstructured, gets the label on the object and on its pod
// template.
func TestStampComponentLabel_Workloads(t *testing.T) {
	workloads := map[string]client.Object{
		"typed Deployment":  &appsv1.Deployment{},
		"typed StatefulSet": &appsv1.StatefulSet{},
		"typed DaemonSet":   &appsv1.DaemonSet{},
		"typed Job":         &batchv1.Job{},
		"typed CronJob":     &batchv1.CronJob{},
		"typed ReplicaSet":  &appsv1.ReplicaSet{},
		"typed ReplicationController": &corev1.ReplicationController{Spec: corev1.ReplicationControllerSpec{
			Selector: map[string]string{"app": "web"},
			Template: &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}}},
		}},
		"typed PodTemplate":                  &corev1.PodTemplate{},
		"unstructured Deployment":            unstructuredWorkload("apps/v1", "Deployment"),
		"unstructured StatefulSet":           unstructuredWorkload("apps/v1", "StatefulSet"),
		"unstructured DaemonSet":             unstructuredWorkload("apps/v1", "DaemonSet"),
		"unstructured Job":                   unstructuredWorkload("batch/v1", "Job"),
		"unstructured CronJob":               unstructuredWorkload("batch/v1", "CronJob"),
		"unstructured ReplicaSet":            unstructuredWorkload("apps/v1", "ReplicaSet"),
		"unstructured ReplicationController": unstructuredReplicationController(),
		"unstructured PodTemplate":           unstructuredWorkload("v1", "PodTemplate"),
	}
	for name, obj := range workloads {
		t.Run(name, func(t *testing.T) {
			if err := stampComponentLabel(obj, ownershipKey, "web"); err != nil {
				t.Fatalf("stampComponentLabel: %v", err)
			}
			if got := obj.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
			labels, ok := podTemplateLabelsOf(t, obj)
			if !ok {
				t.Fatalf("%T is not a workload", obj)
			}
			if got := labels[ownershipKey]; got != "web" {
				t.Errorf("pod template label = %q, want web", got)
			}
		})
	}
}

// TestStampComponentLabel_OtherObjects: an object that is no workload gets the
// label on itself only, and an unstructured object of a workload's kind in
// another API group is no workload.
func TestStampComponentLabel_OtherObjects(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Labels: map[string]string{"app": "web"}}}
	if err := stampComponentLabel(cm, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	want := map[string]string{"app": "web", ownershipKey: "web"}
	if !reflect.DeepEqual(cm.Labels, want) {
		t.Errorf("labels = %v, want %v", cm.Labels, want)
	}

	foreign := unstructuredWorkload("example.com/v1", "Job")
	if err := stampComponentLabel(foreign, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	if got := foreign.GetLabels()[ownershipKey]; got != "web" {
		t.Errorf("object label = %q, want web", got)
	}
	if _, found, _ := unstructured.NestedMap(foreign.Object, "spec", "template", "metadata"); found {
		t.Error("a Job of another API group got pod template metadata")
	}
}

// TestStampComponentLabel_KeepsAnExistingValue: a value the object or its pod
// template already carries under the key stays. With the key set to one a
// workload selects on, an overwrite would part the selector from the template.
func TestStampComponentLabel_KeepsAnExistingValue(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web-worker"}}}
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web-worker"}}
	dep.Spec.Template.Labels = map[string]string{"app": "web-worker"}
	if err := stampComponentLabel(dep, "app", "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	if got := dep.Labels["app"]; got != "web-worker" {
		t.Errorf("object label = %q, want the authored web-worker", got)
	}
	if got := dep.Spec.Template.Labels["app"]; got != "web-worker" {
		t.Errorf("pod template label = %q, want the authored web-worker", got)
	}

	u := unstructuredWorkload("apps/v1", "Deployment")
	if err := unstructured.SetNestedStringMap(u.Object, map[string]string{ownershipKey: "authored"}, "spec", "template", "metadata", "labels"); err != nil {
		t.Fatal(err)
	}
	if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	labels, _ := podTemplateLabelsOf(t, u)
	if got := labels[ownershipKey]; got != "authored" {
		t.Errorf("unstructured pod template label = %q, want the authored value", got)
	}
}

// TestStampComponentLabel_MalformedPodTemplate: an unstructured workload whose
// pod template labels are not a string map is refused, not rewritten.
func TestStampComponentLabel_MalformedPodTemplate(t *testing.T) {
	u := unstructuredWorkload("apps/v1", "Deployment")
	if err := unstructured.SetNestedField(u.Object, "oops", "spec", "template", "metadata"); err != nil {
		t.Fatal(err)
	}
	err := stampComponentLabel(u, ownershipKey, "web")
	if err == nil || !strings.Contains(err.Error(), `Deployment "w"`) {
		t.Fatalf("error = %v, want one naming the Deployment", err)
	}
}

// TestStampComponentLabel_MalformedPodTemplateLabel: a pod template label that
// is no string is refused too, whether or not the template carries the
// component key already, and when it is the component key's own value.
func TestStampComponentLabel_MalformedPodTemplateLabel(t *testing.T) {
	for name, tc := range map[string]struct {
		labels map[string]any
		label  string
	}{
		"key absent":                 {map[string]any{"app": int64(1)}, "app"},
		"key present":                {map[string]any{ownershipKey: "web", "app": int64(1)}, "app"},
		"the key's own value":        {map[string]any{ownershipKey: int64(1)}, ownershipKey},
		"the first of two, by name":  {map[string]any{"a": int64(1), "b": true}, "a"},
		"a nested object is refused": {map[string]any{"app": map[string]any{}}, "app"},
	} {
		t.Run(name, func(t *testing.T) {
			u := unstructuredWorkload("batch/v1", "CronJob")
			if err := unstructured.SetNestedField(u.Object, tc.labels, "spec", "jobTemplate", "spec", "template", "metadata", "labels"); err != nil {
				t.Fatal(err)
			}
			err := stampComponentLabel(u, ownershipKey, "web")
			if err == nil || !strings.Contains(err.Error(), `CronJob "w"`) || !strings.Contains(err.Error(), `label "`+tc.label+`"`) {
				t.Fatalf("error = %v, want one naming the CronJob and the label %q", err, tc.label)
			}
		})
	}
}

// TestStampComponentLabel_MalformedObjectLabels: an unstructured object whose
// own labels hold a value that is no string is refused with the object and the
// label named, and keeps every label its author wrote. The accessor answers
// nil for such a map, so setting the label through it would replace them all
// with the component's alone.
func TestStampComponentLabel_MalformedObjectLabels(t *testing.T) {
	for name, tc := range map[string]struct {
		metadata any
		want     string
	}{
		"a number beside a string": {map[string]any{"name": "c", "labels": map[string]any{"app": "web", "n": int64(1)}}, `label "n"`},
		"with the key present":     {map[string]any{"name": "c", "labels": map[string]any{ownershipKey: "web", "n": true}}, `label "n"`},
		"the key's own value":      {map[string]any{"name": "c", "labels": map[string]any{ownershipKey: int64(1)}}, `label "` + ownershipKey + `"`},
		"labels that are a list":   {map[string]any{"name": "c", "labels": []any{"app"}}, "labels is a"},
		"metadata that is a text":  {"oops", "metadata is a"},
	} {
		t.Run(name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": tc.metadata}}
			want := u.DeepCopy()
			err := stampComponentLabel(u, ownershipKey, "web")
			if err == nil || !strings.Contains(err.Error(), "ConfigMap") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming the ConfigMap and %s", err, tc.want)
			}
			if !reflect.DeepEqual(u.Object, want.Object) {
				t.Errorf("object = %v, want it as written: %v", u.Object, want.Object)
			}
		})
	}
}

// TestStampComponentLabel_UnstructuredObjectLabels: the label goes into the
// labels as written. Null metadata or labels are absent ones, a null value is
// the empty string the cluster reads it as and stays as written, and an object
// with no content at all gets the label too.
func TestStampComponentLabel_UnstructuredObjectLabels(t *testing.T) {
	for name, tc := range map[string]struct {
		object map[string]any
		want   map[string]any
	}{
		"labels beside others": {
			map[string]any{"metadata": map[string]any{"name": "c", "labels": map[string]any{"app": "web"}}},
			map[string]any{"name": "c", "labels": map[string]any{"app": "web", ownershipKey: "web"}},
		},
		"null labels": {
			map[string]any{"metadata": map[string]any{"name": "c", "labels": nil}},
			map[string]any{"name": "c", "labels": map[string]any{ownershipKey: "web"}},
		},
		"null metadata": {
			map[string]any{"metadata": nil},
			map[string]any{"labels": map[string]any{ownershipKey: "web"}},
		},
		"a null value": {
			map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": nil}}},
			map[string]any{"labels": map[string]any{"app": nil, ownershipKey: "web"}},
		},
		"the key as a null value": {
			map[string]any{"metadata": map[string]any{"labels": map[string]any{ownershipKey: nil}}},
			map[string]any{"labels": map[string]any{ownershipKey: nil}},
		},
		"an authored value": {
			map[string]any{"metadata": map[string]any{"labels": map[string]any{ownershipKey: "authored"}}},
			map[string]any{"labels": map[string]any{ownershipKey: "authored"}},
		},
		"no content": {nil, map[string]any{"labels": map[string]any{ownershipKey: "web"}}},
	} {
		t.Run(name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: tc.object}
			for range 2 {
				if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
					t.Fatalf("stampComponentLabel: %v", err)
				}
			}
			if got := u.Object["metadata"]; !reflect.DeepEqual(got, any(tc.want)) {
				t.Errorf("metadata = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestStampComponentLabel_NullPodTemplateLabelValue: a null label value on a
// pod template is the empty string the cluster reads it as, not a malformed
// one: the template gets the label and keeps the value as written.
func TestStampComponentLabel_NullPodTemplateLabelValue(t *testing.T) {
	u := unstructuredWorkload("apps/v1", "Deployment")
	if err := unstructured.SetNestedField(u.Object, map[string]any{"app": nil}, "spec", "template", "metadata", "labels"); err != nil {
		t.Fatal(err)
	}
	if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	got, _, _ := unstructured.NestedMap(u.Object, "spec", "template", "metadata", "labels")
	if want := map[string]any{"app": nil, ownershipKey: "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pod template labels = %#v, want %#v", got, want)
	}
}

// TestStampComponentLabel_NullPodTemplateMetadata: YAML's explicit null is an
// absent value. A pod template whose metadata or labels are null gets the
// label; a workload with no pod template, or a null one, is left as it is.
// A PodTemplate has no spec around its pod template.
func TestStampComponentLabel_NullPodTemplateMetadata(t *testing.T) {
	for _, kind := range []struct{ apiVersion, kind string }{{"apps/v1", "Deployment"}, {"batch/v1", "Job"}, {"batch/v1", "CronJob"}, {"apps/v1", "ReplicaSet"}, {"v1", "ReplicationController"}, {"v1", "PodTemplate"}} {
		spec := []string{"spec"}
		switch kind.kind {
		case "CronJob":
			spec = []string{"spec", "jobTemplate", "spec"}
		case "PodTemplate":
			spec = nil
		}
		template := append(append([]string(nil), spec...), "template")
		for name, null := range map[string][]string{
			"labels":   append(append([]string(nil), template...), "metadata", "labels"),
			"metadata": append(append([]string(nil), template...), "metadata"),
		} {
			t.Run(kind.kind+" with null "+name, func(t *testing.T) {
				u := unstructuredWorkload(kind.apiVersion, kind.kind)
				if err := unstructured.SetNestedField(u.Object, nil, null...); err != nil {
					t.Fatal(err)
				}
				if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
					t.Fatalf("stampComponentLabel: %v", err)
				}
				labels, _ := podTemplateLabelsOf(t, u)
				if want := map[string]string{ownershipKey: "web"}; !reflect.DeepEqual(labels, want) {
					t.Errorf("pod template labels = %v, want %v", labels, want)
				}
			})
		}
		absent := map[string][]string{"template": template}
		if len(spec) > 0 {
			absent["spec"] = spec[:1]
		}
		for name, null := range absent {
			t.Run(kind.kind+" with null "+name, func(t *testing.T) {
				u := unstructuredWorkload(kind.apiVersion, kind.kind)
				if err := unstructured.SetNestedField(u.Object, nil, null...); err != nil {
					t.Fatal(err)
				}
				want := u.DeepCopy()
				want.SetLabels(map[string]string{ownershipKey: "web"})
				if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
					t.Fatalf("stampComponentLabel: %v", err)
				}
				if !reflect.DeepEqual(u.Object, want.Object) {
					t.Errorf("object = %v, want only its own label added: %v", u.Object, want.Object)
				}
			})
		}
	}
}

// TestStampComponentLabel_PodTemplate: a PodTemplate's pod template is a field
// of the object itself. A value the template already carries stays, the
// object's own label is added beside it, and a second call changes nothing.
func TestStampComponentLabel_PodTemplate(t *testing.T) {
	typed := &corev1.PodTemplate{}
	typed.Template.Labels = map[string]string{ownershipKey: "authored"}
	u := unstructuredWorkload("v1", "PodTemplate")
	if err := unstructured.SetNestedStringMap(u.Object, map[string]string{ownershipKey: "authored"}, "template", "metadata", "labels"); err != nil {
		t.Fatal(err)
	}
	for name, obj := range map[string]client.Object{"typed": typed, "unstructured": u} {
		t.Run(name, func(t *testing.T) {
			for range 2 {
				if err := stampComponentLabel(obj, ownershipKey, "web"); err != nil {
					t.Fatalf("stampComponentLabel: %v", err)
				}
			}
			if got := obj.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
			labels, _ := podTemplateLabelsOf(t, obj)
			if want := map[string]string{ownershipKey: "authored"}; !reflect.DeepEqual(labels, want) {
				t.Errorf("pod template labels = %v, want the authored value kept: %v", labels, want)
			}
		})
	}
}

// TestStampComponentLabel_PodTemplateHasNoSelector: a PodTemplate has no
// selector, so a top-level field of that name holds no label back, as nothing
// does on the typed kind.
func TestStampComponentLabel_PodTemplateHasNoSelector(t *testing.T) {
	u := unstructuredWorkload("v1", "PodTemplate")
	u.Object["selector"] = map[string]any{
		"matchExpressions": []any{map[string]any{"key": ownershipKey, "operator": "DoesNotExist"}},
	}
	if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
		t.Fatalf("stampComponentLabel: %v", err)
	}
	labels, _ := podTemplateLabelsOf(t, u)
	if want := map[string]string{ownershipKey: "web"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("pod template labels = %v, want %v", labels, want)
	}
}

// TestStampComponentLabel_SharedLabelMap: a workload built with one label map
// for its own labels, its selector and its pod template keeps that map as it
// is. The label goes into maps of the object's and the template's own, so the
// selector never gains the key: a selector is immutable on a workload the
// cluster already runs.
func TestStampComponentLabel_SharedLabelMap(t *testing.T) {
	want := map[string]string{"app": "web"}

	t.Run("typed Deployment", func(t *testing.T) {
		shared := map[string]string{"app": "web"}
		dep := &appsv1.Deployment{}
		dep.Labels = shared
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: shared}
		dep.Spec.Template.Labels = shared
		if err := stampComponentLabel(dep, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
		if got := dep.Labels[ownershipKey]; got != "web" {
			t.Errorf("object label = %q, want web", got)
		}
		if got := dep.Spec.Template.Labels[ownershipKey]; got != "web" {
			t.Errorf("pod template label = %q, want web", got)
		}
		if !reflect.DeepEqual(dep.Spec.Selector.MatchLabels, want) {
			t.Errorf("selector = %v, want it as written: %v", dep.Spec.Selector.MatchLabels, want)
		}
		if !reflect.DeepEqual(shared, want) {
			t.Errorf("the shared map = %v, want it as written: %v", shared, want)
		}
	})

	t.Run("typed ReplicationController", func(t *testing.T) {
		shared := map[string]string{"app": "web"}
		rc := &corev1.ReplicationController{}
		rc.Spec.Selector = shared
		rc.Spec.Template = &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: shared}}
		if err := stampComponentLabel(rc, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
		if got := rc.Spec.Template.Labels[ownershipKey]; got != "web" {
			t.Errorf("pod template label = %q, want web", got)
		}
		if !reflect.DeepEqual(rc.Spec.Selector, want) {
			t.Errorf("selector = %v, want it as written: %v", rc.Spec.Selector, want)
		}
	})

	t.Run("unstructured Deployment", func(t *testing.T) {
		shared := map[string]any{"app": "web"}
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]any{"name": "w", "labels": shared},
			"spec": map[string]any{
				"selector": map[string]any{"matchLabels": shared},
				"template": map[string]any{"metadata": map[string]any{"labels": shared}},
			},
		}}
		if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
		if got := u.GetLabels()[ownershipKey]; got != "web" {
			t.Errorf("object label = %q, want web", got)
		}
		podLabels, _ := podTemplateLabelsOf(t, u)
		if got := podLabels[ownershipKey]; got != "web" {
			t.Errorf("pod template label = %q, want web", got)
		}
		selector, _, err := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(selector, want) {
			t.Errorf("selector = %v, want it as written: %v", selector, want)
		}
		if len(shared) != 1 {
			t.Errorf("the shared map = %v, want it as written", shared)
		}
	})
}

// excludingSelector matches pods labelled app=web that do not carry the
// component key: the selector of a workload whose author rules the key out.
func excludingSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchLabels:      map[string]string{"app": "web"},
		MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: metav1.LabelSelectorOpDoesNotExist}},
	}
}

// TestStampComponentLabel_SelectorThatRulesTheLabelOut: a workload whose own
// selector matches its pod template, and would not with the label, keeps the
// template as written: the API server refuses a workload whose selector does not
// match its template. The object itself is still labelled.
func TestStampComponentLabel_SelectorThatRulesTheLabelOut(t *testing.T) {
	podLabels := func() map[string]string { return map[string]string{"app": "web"} }
	dep := &appsv1.Deployment{}
	dep.Spec.Selector, dep.Spec.Template.Labels = excludingSelector(), podLabels()
	sts := &appsv1.StatefulSet{}
	sts.Spec.Selector, sts.Spec.Template.Labels = excludingSelector(), podLabels()
	ds := &appsv1.DaemonSet{}
	ds.Spec.Selector, ds.Spec.Template.Labels = excludingSelector(), podLabels()
	job := &batchv1.Job{}
	job.Spec.Selector, job.Spec.Template.Labels = excludingSelector(), podLabels()
	cron := &batchv1.CronJob{}
	cron.Spec.JobTemplate.Spec.Selector, cron.Spec.JobTemplate.Spec.Template.Labels = excludingSelector(), podLabels()
	rs := &appsv1.ReplicaSet{}
	rs.Spec.Selector, rs.Spec.Template.Labels = excludingSelector(), podLabels()

	rawSelector, err := runtime.DefaultUnstructuredConverter.ToUnstructured(excludingSelector())
	if err != nil {
		t.Fatal(err)
	}
	unstructuredWith := func(apiVersion, kind string, spec ...string) *unstructured.Unstructured {
		u := unstructuredWorkload(apiVersion, kind)
		if err := unstructured.SetNestedMap(u.Object, rawSelector, append(append([]string(nil), spec...), "selector")...); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedStringMap(u.Object, podLabels(), append(append([]string(nil), spec...), "template", "metadata", "labels")...); err != nil {
			t.Fatal(err)
		}
		return u
	}

	for name, obj := range map[string]client.Object{
		"typed Deployment":        dep,
		"typed StatefulSet":       sts,
		"typed DaemonSet":         ds,
		"typed Job":               job,
		"typed CronJob":           cron,
		"typed ReplicaSet":        rs,
		"unstructured Deployment": unstructuredWith("apps/v1", "Deployment", "spec"),
		"unstructured CronJob":    unstructuredWith("batch/v1", "CronJob", "spec", "jobTemplate", "spec"),
		"unstructured ReplicaSet": unstructuredWith("apps/v1", "ReplicaSet", "spec"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := stampComponentLabel(obj, ownershipKey, "web"); err != nil {
				t.Fatalf("stampComponentLabel: %v", err)
			}
			if got := obj.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("object label = %q, want web", got)
			}
			labels, _ := podTemplateLabelsOf(t, obj)
			if !reflect.DeepEqual(labels, podLabels()) {
				t.Errorf("pod template labels = %v, want them as written: the selector rules the key out", labels)
			}
		})
	}
}

// TestStampComponentLabel_SelectorThatAllowsTheLabel is the control: a selector
// that names the key without ruling this value out, one that does not match the
// template in the first place, and one that does not parse do not hold the label
// back.
func TestStampComponentLabel_SelectorThatAllowsTheLabel(t *testing.T) {
	for name, selector := range map[string]*metav1.LabelSelector{
		"another value ruled out": {MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: metav1.LabelSelectorOpNotIn, Values: []string{"other"}}}},
		"no match before":         {MatchLabels: map[string]string{"app": "elsewhere"}, MatchExpressions: excludingSelector().MatchExpressions},
		"invalid":                 {MatchExpressions: []metav1.LabelSelectorRequirement{{Key: ownershipKey, Operator: "Sometimes"}}},
		"empty":                   {},
	} {
		t.Run(name, func(t *testing.T) {
			dep := &appsv1.Deployment{}
			dep.Spec.Selector = selector
			dep.Spec.Template.Labels = map[string]string{"app": "web"}
			if err := stampComponentLabel(dep, ownershipKey, "web"); err != nil {
				t.Fatalf("stampComponentLabel: %v", err)
			}
			if got := dep.Spec.Template.Labels[ownershipKey]; got != "web" {
				t.Errorf("pod template label = %q, want web", got)
			}
		})
	}
}

// TestStampComponentLabel_UnstructuredHelmReleaseNulls: a null spec or
// postRenderers on an unstructured HelmRelease is an absent one.
func TestStampComponentLabel_UnstructuredHelmReleaseNulls(t *testing.T) {
	for name, spec := range map[string]any{
		"null postRenderers": map[string]any{"postRenderers": nil},
		"null spec":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "helm.toolkit.fluxcd.io/v2",
				"kind":       "HelmRelease",
				"metadata":   map[string]any{"name": "r"},
				"spec":       spec,
			}}
			if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
				t.Fatalf("stampComponentLabel: %v", err)
			}
			entries, _, err := unstructured.NestedSlice(u.Object, "spec", "postRenderers")
			if err != nil || len(entries) != 1 {
				t.Fatalf("spec.postRenderers = %v (%v), want the label's one entry", entries, err)
			}
		})
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "helm.toolkit.fluxcd.io/v2",
		"kind":       "HelmRelease",
		"metadata":   map[string]any{"name": "r"},
		"spec":       map[string]any{"postRenderers": "oops"},
	}}
	if err := stampComponentLabel(u, ownershipKey, "web"); err == nil || !strings.Contains(err.Error(), `HelmRelease "r"`) {
		t.Fatalf("error = %v, want one naming the HelmRelease", err)
	}
}

func authoredPostRenderer() helmv2.PostRenderer {
	return helmv2.PostRenderer{Kustomize: &helmv2.Kustomize{Images: []kustomize.Image{{Name: "nginx", NewTag: "1.27"}}}}
}

// assertComponentPostRenderer checks pr is the component label post-renderer:
// one patch per workload kind and one for PodTemplate, each a strategic merge
// setting only the label on the kind's pod template, and one for a bare Pod
// setting it on the Pod itself, the value a string whatever it looks like.
func assertComponentPostRenderer(t *testing.T, pr helmv2.PostRenderer, key, value string) {
	t.Helper()
	if pr.Kustomize == nil || len(pr.Kustomize.Images) != 0 {
		t.Fatalf("post-renderer = %+v, want kustomize patches only", pr)
	}
	wantTargets := []kustomize.Selector{
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Group: "batch", Version: "v1", Kind: "Job"},
		{Group: "batch", Version: "v1", Kind: "CronJob"},
		{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
		{Version: "v1", Kind: "ReplicationController"},
		{Version: "v1", Kind: "PodTemplate"},
		{Version: "v1", Kind: "Pod"},
	}
	wantAPIVersions := []string{"apps/v1", "apps/v1", "apps/v1", "batch/v1", "batch/v1", "apps/v1", "v1", "v1", "v1"}
	if len(pr.Kustomize.Patches) != len(wantTargets) {
		t.Fatalf("patches = %d, want %d", len(pr.Kustomize.Patches), len(wantTargets))
	}
	for i, p := range pr.Kustomize.Patches {
		if p.Target == nil || *p.Target != wantTargets[i] {
			t.Errorf("patch %d target = %+v, want %+v", i, p.Target, wantTargets[i])
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(p.Patch), &doc); err != nil {
			t.Fatalf("patch %d is not YAML: %v\n%s", i, err, p.Patch)
		}
		path := []string{"spec", "template", "metadata", "labels"}
		switch wantTargets[i].Kind {
		case "CronJob":
			path = []string{"spec", "jobTemplate", "spec", "template", "metadata", "labels"}
		case "PodTemplate":
			path = []string{"template", "metadata", "labels"}
			if _, found := doc["spec"]; found {
				t.Errorf("patch %d has a spec, want the PodTemplate's template only\n%s", i, p.Patch)
			}
		case "Pod":
			path = []string{"metadata", "labels"}
			if _, found := doc["spec"]; found {
				t.Errorf("patch %d has a spec, want the Pod's own labels only\n%s", i, p.Patch)
			}
		}
		labels, found, err := unstructured.NestedMap(doc, path...)
		if err != nil || !found {
			t.Fatalf("patch %d has no %s: %v\n%s", i, strings.Join(path, "."), err, p.Patch)
		}
		if got, ok := labels[key].(string); !ok || got != value || len(labels) != 1 {
			t.Errorf("patch %d labels = %#v, want only %s: %q as a string", i, labels, key, value)
		}
		if doc["kind"] != wantTargets[i].Kind {
			t.Errorf("patch %d kind = %v, want %s", i, doc["kind"], wantTargets[i].Kind)
		}
		if doc["apiVersion"] != wantAPIVersions[i] {
			t.Errorf("patch %d apiVersion = %v, want %s", i, doc["apiVersion"], wantAPIVersions[i])
		}
	}
}

// TestStampComponentLabel_HelmRelease: a HelmRelease gets the post-renderer
// after the authored ones, once however often it is stamped, and the label on
// itself.
func TestStampComponentLabel_HelmRelease(t *testing.T) {
	hr := &helmv2.HelmRelease{}
	hr.Spec.PostRenderers = []helmv2.PostRenderer{authoredPostRenderer()}
	for range 2 {
		if err := stampComponentLabel(hr, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
	}
	if got := hr.Labels[ownershipKey]; got != "web" {
		t.Errorf("object label = %q, want web", got)
	}
	if len(hr.Spec.PostRenderers) != 2 {
		t.Fatalf("postRenderers = %d, want the authored one and the label's", len(hr.Spec.PostRenderers))
	}
	if !reflect.DeepEqual(hr.Spec.PostRenderers[0], authoredPostRenderer()) {
		t.Errorf("first post-renderer = %+v, want the authored one unchanged", hr.Spec.PostRenderers[0])
	}
	assertComponentPostRenderer(t, hr.Spec.PostRenderers[1], ownershipKey, "web")
}

// TestStampComponentLabel_HelmReleaseValueStaysAString: a component named like
// a YAML number or boolean still patches a string label value.
func TestStampComponentLabel_HelmReleaseValueStaysAString(t *testing.T) {
	for _, value := range []string{"123", "true", "null", "1e3"} {
		hr := &helmv2.HelmRelease{}
		if err := stampComponentLabel(hr, ownershipKey, value); err != nil {
			t.Fatalf("stampComponentLabel(%q): %v", value, err)
		}
		if len(hr.Spec.PostRenderers) != 1 {
			t.Fatalf("postRenderers = %d, want 1", len(hr.Spec.PostRenderers))
		}
		assertComponentPostRenderer(t, hr.Spec.PostRenderers[0], ownershipKey, value)
	}
}

// TestStampComponentLabel_UnstructuredHelmRelease: a HelmRelease a component
// hands out unstructured gets the same post-renderer, once.
func TestStampComponentLabel_UnstructuredHelmRelease(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "helm.toolkit.fluxcd.io/v2",
		"kind":       "HelmRelease",
		"metadata":   map[string]any{"name": "r"},
		"spec": map[string]any{
			"postRenderers": []any{map[string]any{"kustomize": map[string]any{"images": []any{map[string]any{"name": "nginx", "newTag": "1.27"}}}}},
		},
	}}
	for range 2 {
		if err := stampComponentLabel(u, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
	}
	raw, err := yaml.Marshal(u.Object)
	if err != nil {
		t.Fatal(err)
	}
	var hr helmv2.HelmRelease
	if err := yaml.Unmarshal(raw, &hr); err != nil {
		t.Fatalf("stamped object is no HelmRelease: %v\n%s", err, raw)
	}
	if len(hr.Spec.PostRenderers) != 2 {
		t.Fatalf("postRenderers = %d, want the authored one and the label's\n%s", len(hr.Spec.PostRenderers), raw)
	}
	if !reflect.DeepEqual(hr.Spec.PostRenderers[0], authoredPostRenderer()) {
		t.Errorf("first post-renderer = %+v, want the authored one unchanged", hr.Spec.PostRenderers[0])
	}
	assertComponentPostRenderer(t, hr.Spec.PostRenderers[1], ownershipKey, "web")
}

// TestStampComponentLabel_ListMembers: a list envelope stands for its members
// when Flux applies it (appliedObjects), so each member gets the label as an
// object handed out on its own does: a workload on its pod template too, a
// HelmRelease with its post-renderer, and the member of a list inside a list.
// A member that is no object stays as written.
func TestStampComponentLabel_ListMembers(t *testing.T) {
	for name, kind := range map[string]string{
		"a List":                        "List",
		"an envelope only Flux expands": "Widget",
	} {
		t.Run(name, func(t *testing.T) {
			deployment := unstructuredWorkload("apps/v1", "Deployment")
			configMap := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c", "labels": map[string]any{"app": "web"}}}
			release := map[string]any{"apiVersion": "helm.toolkit.fluxcd.io/v2", "kind": "HelmRelease", "metadata": map[string]any{"name": "r"}}
			list := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       kind,
				"items":      []any{deployment.Object, configMap, "no object", release},
			}}
			for range 2 {
				if err := stampComponentLabel(list, ownershipKey, "web"); err != nil {
					t.Fatalf("stampComponentLabel: %v", err)
				}
			}
			if got := deployment.GetLabels()[ownershipKey]; got != "web" {
				t.Errorf("Deployment label = %q, want web", got)
			}
			if labels, _ := podTemplateLabelsOf(t, deployment); labels[ownershipKey] != "web" {
				t.Errorf("Deployment pod template labels = %v, want the component's", labels)
			}
			wantLabels := map[string]any{"app": "web", ownershipKey: "web"}
			if got := configMap["metadata"].(map[string]any)["labels"]; !reflect.DeepEqual(got, any(wantLabels)) {
				t.Errorf("ConfigMap labels = %v, want %v", got, wantLabels)
			}
			if got := list.Object["items"].([]any)[2]; got != "no object" {
				t.Errorf("member that is no object = %v, want it as written", got)
			}
			postRenderers, _, err := unstructured.NestedSlice(release, "spec", "postRenderers")
			if err != nil || len(postRenderers) != 1 {
				t.Errorf("HelmRelease postRenderers = %v (%v), want the label's alone", postRenderers, err)
			}
		})
	}

	t.Run("a list inside a List", func(t *testing.T) {
		inner := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "n"}}
		list := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "List",
			"items":      []any{map[string]any{"apiVersion": "v1", "kind": "ConfigMapList", "items": []any{inner}}},
		}}
		if err := stampComponentLabel(list, ownershipKey, "web"); err != nil {
			t.Fatalf("stampComponentLabel: %v", err)
		}
		if got, _, _ := unstructured.NestedString(inner, "metadata", "labels", ownershipKey); got != "web" {
			t.Errorf("inner member label = %q, want web", got)
		}
	})

	t.Run("a member with a malformed label", func(t *testing.T) {
		member := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "c", "labels": map[string]any{"n": int64(1)}}}
		list := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{member}}}
		err := stampComponentLabel(list, ownershipKey, "web")
		if err == nil || !strings.Contains(err.Error(), `ConfigMap "c"`) || !strings.Contains(err.Error(), `label "n"`) {
			t.Fatalf("error = %v, want one naming the ConfigMap and its label", err)
		}
	})
}

// ownershipObjectsConfig hands out the objects it holds.
type ownershipObjectsConfig struct{ objects []client.Object }

func (c *ownershipObjectsConfig) Generate(*stack.Application) ([]*client.Object, error) {
	out := make([]*client.Object, 0, len(c.objects)+1)
	for i := range c.objects {
		out = append(out, &c.objects[i])
	}
	return append(out, nil), nil // a nil entry is skipped, as generateBundle skips one
}

// ownershipAugmenter also adds a Deployment to its layout, in a child.
type ownershipAugmenter struct {
	ownershipObjectsConfig
	covers bool
}

func (c *ownershipAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.Children = append(l.Children, &layout.ManifestLayout{Name: "hook", Resources: []client.Object{&appsv1.Deployment{}}})
	return nil
}

func (c *ownershipAugmenter) GenerateCoversAugmentLayout() bool { return c.covers }

type ownershipIntentAugmenter struct{ ownershipAugmenter }

func (c *ownershipIntentAugmenter) WantsOwnLayout() bool { return false }

// TestOwnedConfig_Generate: the wrapper labels what its config generates for a
// component, leaves an application-owned config's objects alone, and reports the
// owner.
func TestOwnedConfig_Generate(t *testing.T) {
	inner := &ownershipObjectsConfig{objects: []client.Object{&appsv1.Deployment{}, &corev1.ConfigMap{}}}
	app := stack.NewApplication("web-worker", "ns", wrapOwnedConfig(inner, "web", ownershipKey))
	objs, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 3 || objs[2] != nil {
		t.Fatalf("objects = %d, want the config's own three entries in order", len(objs))
	}
	for _, p := range objs[:2] {
		if got := (*p).GetLabels()[ownershipKey]; got != "web" {
			t.Errorf("%T label = %q, want web", *p, got)
		}
	}
	if got := inner.objects[0].(*appsv1.Deployment).Spec.Template.Labels[ownershipKey]; got != "web" {
		t.Errorf("pod template label = %q, want web", got)
	}
	if got := app.Config.(ComponentNamed).ComponentName(); got != "web" {
		t.Errorf("ComponentName = %q, want web", got)
	}
	unwrapped := app.Config.(interface {
		WrappedApplicationConfig() stack.ApplicationConfig
	}).WrappedApplicationConfig()
	if unwrapped != stack.ApplicationConfig(inner) {
		t.Errorf("WrappedApplicationConfig = %T, want the wrapped config", unwrapped)
	}

	shared := &ownershipObjectsConfig{objects: []client.Object{&corev1.ConfigMap{}}}
	sharedApp := stack.NewApplication("shop-source-1", "ns", wrapOwnedConfig(shared, "", ownershipKey))
	objs, err = sharedApp.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if labels := (*objs[0]).GetLabels(); len(labels) != 0 {
		t.Errorf("application-owned object labels = %v, want none", labels)
	}
	if got := sharedApp.Config.(ComponentNamed).ComponentName(); got != "" {
		t.Errorf("ComponentName = %q, want none", got)
	}
}

// TestOwnedConfig_Layout: the wrapper is a layout augmenter, and an intent
// augmenter, exactly when its config is one (kure's walker reads both by
// presence), and labels what the augmenter adds.
func TestOwnedConfig_Layout(t *testing.T) {
	plain := wrapOwnedConfig(&ownershipObjectsConfig{}, "web", ownershipKey)
	if _, ok := plain.(layout.LayoutAugmenter); ok {
		t.Error("a wrapped non-augmenter is a LayoutAugmenter")
	}
	if cov, ok := plain.(LayoutAugmentationCoverage); ok && cov.GenerateCoversAugmentLayout() {
		t.Error("a wrapped non-augmenter claims layout coverage")
	}

	for _, covers := range []bool{false, true} {
		wrapped := wrapOwnedConfig(&ownershipAugmenter{covers: covers}, "web", ownershipKey)
		aug, ok := wrapped.(layout.LayoutAugmenter)
		if !ok {
			t.Fatal("a wrapped augmenter is no LayoutAugmenter")
		}
		if _, ok := wrapped.(layout.LayoutIntentAugmenter); ok {
			t.Error("a wrapped augmenter without intent is a LayoutIntentAugmenter")
		}
		if got := wrapped.(LayoutAugmentationCoverage).GenerateCoversAugmentLayout(); got != covers {
			t.Errorf("GenerateCoversAugmentLayout = %v, want the inner %v", got, covers)
		}
		l := &layout.ManifestLayout{Resources: []client.Object{&corev1.ConfigMap{}}}
		if err := aug.AugmentLayout(l); err != nil {
			t.Fatalf("AugmentLayout: %v", err)
		}
		if got := l.Resources[0].GetLabels()[ownershipKey]; got != "web" {
			t.Errorf("layout resource label = %q, want web", got)
		}
		added := l.Children[0].Resources[0].(*appsv1.Deployment)
		if added.Labels[ownershipKey] != "web" || added.Spec.Template.Labels[ownershipKey] != "web" {
			t.Errorf("augmenter-added Deployment labels = %v / %v, want web on both", added.Labels, added.Spec.Template.Labels)
		}
	}

	intent, ok := wrapOwnedConfig(&ownershipIntentAugmenter{}, "web", ownershipKey).(layout.LayoutIntentAugmenter)
	if !ok {
		t.Fatal("a wrapped intent augmenter is no LayoutIntentAugmenter")
	}
	if intent.WantsOwnLayout() {
		t.Error("WantsOwnLayout = true, want the inner false")
	}
}

// configContract is what the ownership wrapper does with one type the code
// asserts on an application's config: the wrapper method that forwards it, or
// why it need not be forwarded.
type configContract struct {
	method       string
	notForwarded string
}

// configContracts lists every type asserted on an application's config in
// launcher's own code, and the two kure asserts. The wrapper is the outermost
// config of every application once the transform returns, so a contract read
// after that must be forwarded, and one that is not must say why.
var configContracts = map[string]configContract{
	"stack.Validator":              {method: "Validate"},
	"layout.LayoutAugmenter":       {method: "AugmentLayout"},
	"layout.LayoutIntentAugmenter": {method: "WantsOwnLayout"},
	"LayoutAugmentationCoverage":   {method: "GenerateCoversAugmentLayout"},
	"ComponentNamed":               {method: "ComponentName"},
	"fluxNamespaceSettable":        {method: "SetFluxNamespace"},
	"fluxNamespaceReader":          {method: "FluxNamespaceReads"},
	"servicePortProvider":          {method: "ServicePort"},
	"serviceBackendNamer":          {method: "BackendServiceName"},
	"servicePortNamer":             {method: "ServicePortName"},
	"siblingServicePortNamer":      {method: "ServicePortName"},
	"ServiceAccountNamer":          {method: "ServiceAccountName"},
	"nonRWXClaimer":                {method: "NonRWXClaim"},
	"siblingNonRWXClaimer":         {method: "NonRWXClaim"},
	"serviceRoutingTargeter":       {method: "ServiceRoutingTarget"},
	"podTemplateLabeler":           {method: "PodTemplateLabels"},
	"identityPortMapper":           {method: "IdentityTargetPorts"},

	"Enforceable":               {notForwarded: "asserted as each config is created, before the wrap"},
	"SourceDeduplicatable":      {notForwarded: "asserted on component configs before any trait runs"},
	"trafficSourceCollector":    {notForwarded: "a trait sub-application contract the NetworkPolicy synthesis reads before the wrap"},
	"backendRefTargetCollector": {notForwarded: "a trait sub-application contract the NetworkPolicy synthesis reads before the wrap"},
	"fluxNamespaceInput":        {notForwarded: "a trait sub-application contract the Flux namespace pass reads before the wrap"},
	"*siblingGroupConfig":       {notForwarded: "the engine's own type, asserted while it builds the group"},
	"componentOwner":            {notForwarded: "the wrapper itself"},
}

// onConfigSelector reports whether assertion is made on a selector named
// Config: app.Config.(T), or the app.Config.(type) of a type switch.
func onConfigSelector(assertion *ast.TypeAssertExpr) bool {
	if assertion == nil {
		return false
	}
	sel, ok := assertion.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Config"
}

// typeSwitchSubject returns the x.(type) of a type switch, written bare or as
// the right side of "v := x.(type)".
func typeSwitchSubject(s *ast.TypeSwitchStmt) *ast.TypeAssertExpr {
	var x ast.Expr
	switch assign := s.Assign.(type) {
	case *ast.ExprStmt:
		x = assign.X
	case *ast.AssignStmt:
		if len(assign.Rhs) == 1 {
			x = assign.Rhs[0]
		}
	}
	subject, _ := x.(*ast.TypeAssertExpr)
	return subject
}

// assertedConfigTypes parses every non-test Go file under each root and returns
// the types asserted on a selector named Config, by a type assertion
// (app.Config.(T)) or as a case of a type switch (switch app.Config.(type)),
// with the package qualifier of launcher's own oam package dropped. It reads
// syntax only: a config first copied to a variable, or passed to a function,
// and asserted there is not seen.
func assertedConfigTypes(t *testing.T, roots ...string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			record := func(typ ast.Expr) {
				name := strings.TrimPrefix(types.ExprString(typ), "oam.")
				found[name] = append(found[name], fset.Position(typ.Pos()).String())
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.TypeAssertExpr:
					// A type switch's own x.(type) has no Type; its cases are read below.
					if n.Type != nil && onConfigSelector(n) {
						record(n.Type)
					}
				case *ast.TypeSwitchStmt:
					if !onConfigSelector(typeSwitchSubject(n)) {
						return true
					}
					for _, stmt := range n.Body.List {
						for _, typ := range stmt.(*ast.CaseClause).List {
							if ident, ok := typ.(*ast.Ident); ok && ident.Name == "nil" {
								continue
							}
							record(typ)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	return found
}

// TestOwnedConfig_ForwardsEveryConfigContract fails when the code asserts or
// switches on a type directly on an application's config (app.Config) that
// configContracts does not account for, and when a contract it calls forwarded
// has no method on the wrapper. A new optional config interface read that way
// therefore cannot be added without deciding whether the wrapper, the outermost
// config after the transform, forwards it. A read through a variable or a
// function parameter is outside what the scan sees (assertedConfigTypes): the
// ones the code has today are all made before the wrap.
func TestOwnedConfig_ForwardsEveryConfigContract(t *testing.T) {
	asserted := assertedConfigTypes(t, ".", filepath.Join("..", "cmd", "kurel"))
	if len(asserted) < 10 {
		t.Fatalf("found %d asserted config types, want the code's own: the scan is not reading the sources", len(asserted))
	}
	var unknown []string
	for name, sites := range asserted {
		if _, ok := configContracts[name]; !ok {
			unknown = append(unknown, name+" ("+sites[0]+")")
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("config types asserted but not in configContracts; forward each in ownedConfig or record why not:\n  %s", strings.Join(unknown, "\n  "))
	}

	// The fullest wrapper: one around an intent augmenter.
	full := reflect.TypeOf(wrapOwnedConfig(&ownershipIntentAugmenter{}, "web", ownershipKey))
	for name, contract := range configContracts {
		if (contract.method == "") == (contract.notForwarded == "") {
			t.Errorf("configContracts[%q] must name a method or a reason, not both or neither", name)
			continue
		}
		if contract.method == "" {
			continue
		}
		if _, ok := full.MethodByName(contract.method); !ok {
			t.Errorf("the wrapper has no %s method to forward %s", contract.method, name)
		}
	}
}

// ownershipForwards answers every forwarded contract with a recognisable value.
type ownershipForwards struct {
	ownershipObjectsConfig
	fluxNS string
}

func (c *ownershipForwards) Validate() error                    { return errOwnershipValidate }
func (c *ownershipForwards) SetFluxNamespace(ns string)         { c.fluxNS = ns }
func (c *ownershipForwards) ServicePort() int32                 { return 8080 }
func (c *ownershipForwards) BackendServiceName() string         { return "svc" }
func (c *ownershipForwards) NonRWXClaim() string                { return "claim" }
func (c *ownershipForwards) IdentityTargetPorts() bool          { return true }
func (c *ownershipForwards) ServicePortName() (string, bool)    { return "http", true }
func (c *ownershipForwards) ServiceAccountName() (string, bool) { return "sa", true }
func (c *ownershipForwards) PodTemplateLabels() map[string]string {
	return map[string]string{"app": "web"}
}
func (c *ownershipForwards) FluxNamespaceReads() ([]string, []string) {
	return []string{"cm"}, []string{"secret"}
}
func (c *ownershipForwards) ServiceRoutingTarget(ports []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, ports
}

var errOwnershipValidate = &TransformError{Message: "inner validate"}

// TestOwnedConfig_Forwards: each forwarded contract answers as the wrapped
// config does, and as "not one" over a config that implements none.
func TestOwnedConfig_Forwards(t *testing.T) {
	inner := &ownershipForwards{}
	w, ok := wrapOwnedConfig(inner, "web", ownershipKey).(*ownedConfig)
	if !ok {
		t.Fatalf("wrapOwnedConfig over a non-augmenter = %T, want *ownedConfig", w)
	}
	if err := w.Validate(); !errors.Is(err, errOwnershipValidate) {
		t.Errorf("Validate = %v, want the inner error", err)
	}
	w.SetFluxNamespace("flux-system")
	if inner.fluxNS != "flux-system" {
		t.Errorf("SetFluxNamespace did not reach the inner config")
	}
	if cms, secrets := w.FluxNamespaceReads(); !reflect.DeepEqual(cms, []string{"cm"}) || !reflect.DeepEqual(secrets, []string{"secret"}) {
		t.Errorf("FluxNamespaceReads = %v, %v", cms, secrets)
	}
	if w.ServicePort() != 8080 || w.BackendServiceName() != "svc" || w.NonRWXClaim() != "claim" || !w.IdentityTargetPorts() {
		t.Errorf("scalar forwards = %d %q %q %v", w.ServicePort(), w.BackendServiceName(), w.NonRWXClaim(), w.IdentityTargetPorts())
	}
	if name, known := w.ServicePortName(); name != "http" || !known {
		t.Errorf("ServicePortName = %q, %v", name, known)
	}
	if name, pods := w.ServiceAccountName(); name != "sa" || !pods {
		t.Errorf("ServiceAccountName = %q, %v", name, pods)
	}
	if got := w.PodTemplateLabels(); !reflect.DeepEqual(got, map[string]string{"app": "web"}) {
		t.Errorf("PodTemplateLabels = %v", got)
	}
	if sel, ports := w.ServiceRoutingTarget([]intstr.IntOrString{intstr.FromInt32(80)}); sel == nil || len(ports) != 1 {
		t.Errorf("ServiceRoutingTarget = %v, %v", sel, ports)
	}

	bare := wrapOwnedConfig(&ownershipObjectsConfig{}, "web", ownershipKey).(*ownedConfig)
	if err := bare.Validate(); err != nil {
		t.Errorf("Validate over a config with none = %v", err)
	}
	bare.SetFluxNamespace("flux-system")
	if cms, secrets := bare.FluxNamespaceReads(); cms != nil || secrets != nil {
		t.Errorf("FluxNamespaceReads = %v, %v, want none", cms, secrets)
	}
	if bare.ServicePort() != 0 || bare.BackendServiceName() != "" || bare.NonRWXClaim() != "" || bare.IdentityTargetPorts() || bare.PodTemplateLabels() != nil {
		t.Error("a config implementing no contract answers a non-zero value through the wrapper")
	}
	if name, known := bare.ServicePortName(); name != "" || known {
		t.Errorf("ServicePortName = %q, %v, want none", name, known)
	}
	if name, pods := bare.ServiceAccountName(); name != "" || pods {
		t.Errorf("ServiceAccountName = %q, %v, want none", name, pods)
	}
	if sel, ports := bare.ServiceRoutingTarget(nil); sel != nil || ports != nil {
		t.Errorf("ServiceRoutingTarget = %v, %v, want none", sel, ports)
	}
}

// partRule lowers a "parted" component into a webservice of its own name and a
// worker named "<name>-part".
type partRule struct{}

func (partRule) ComponentType() string { return "parted" }

func (partRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{
		{Name: comp.Name, Type: "webservice", Traits: comp.Traits},
		{Name: comp.Name + "-part", Type: "worker"},
	}}, nil
}

// hoistedSourceRule lowers a "sourced" component into a webservice ordered
// after a helmrepository every "sourced" component of the document shares: a
// generated source the application bundle holds.
type hoistedSourceRule struct{}

func (hoistedSourceRule) ComponentType() string { return "sourced" }

// A rule that declares a schema emits generated components: only such a source
// is one the application bundle holds (orderComponents).
func (hoistedSourceRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (hoistedSourceRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	leaf := Component{Name: comp.Name, Type: "webservice"}
	leaf.OrderAfter("charts")
	adopted, err := lctx.Namer.EmitOrAdopt("charts", "source|charts", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	if adopted {
		return LoweringResult{Components: []Component{leaf}}, nil
	}
	return LoweringResult{Components: []Component{leaf, {Name: "charts", Type: "helmrepository"}}}, nil
}

// routedTraitHandler adds a sub-application the inbound NetworkPolicy synthesis
// collects: traffic to its component on port 80, and to an external backend
// Service when the trait names one.
type routedTraitHandler struct{}

func (routedTraitHandler) CanHandle(t string) bool { return t == "routed" }

func (routedTraitHandler) Apply(trait *Trait, app *stack.Application, bundle *stack.Bundle) error {
	cfg := &extBackendStub{component: app.Name, sources: []netpol.TrafficSource{{Namespace: "ingress"}}}
	if svc, _ := trait.Properties["external"].(string); svc != "" {
		cfg.targets = []netpol.BackendTarget{{
			ServiceName: svc,
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": svc}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(8443)},
		}}
	}
	bundle.Applications = append(bundle.Applications,
		stack.NewApplication(app.Name+"-route", app.Namespace, &routedConfig{extBackendStub: cfg}))
	return nil
}

type routedConfig struct{ *extBackendStub }

func (c *routedConfig) BackendPorts() []intstr.IntOrString {
	return []intstr.IntOrString{intstr.FromInt32(80)}
}

func ownershipTransformer() *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice":     &configMapPropertyHandler{typ: "webservice"},
		"statefulset":    &configMapPropertyHandler{typ: "statefulset"},
		"worker":         &configMapPropertyHandler{typ: "worker"},
		"helmrepository": &configMapPropertyHandler{typ: "helmrepository"},
	}, map[string]TraitHandler{"settings": settingsTraitHandler{}, "routed": routedTraitHandler{}})
	tr.RegisterComponentLowering(siblingRule{})
	tr.RegisterComponentLowering(partRule{})
	tr.RegisterComponentLowering(hoistedSourceRule{})
	return tr
}

// generatedByName transforms app, generates it, and returns each application by
// name.
func generatedByName(t *testing.T, tr *Transformer, app *Application, ctx TransformContext) map[string]GeneratedApplication {
	t.Helper()
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, err := tr.Transform(app, ctx)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	out := map[string]GeneratedApplication{}
	for _, a := range apps {
		if _, dup := out[a.Name]; dup {
			t.Fatalf("two applications named %q", a.Name)
		}
		out[a.Name] = a
	}
	return out
}

// assertOwner checks the application reports component and that each of its
// objects carries exactly that label value; an empty component means no label.
func assertOwner(t *testing.T, apps map[string]GeneratedApplication, name, key, component string) {
	t.Helper()
	a, ok := apps[name]
	if !ok {
		names := make([]string, 0, len(apps))
		for n := range apps {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("no application %q; have %v", name, names)
	}
	if a.Component != component {
		t.Errorf("application %q Component = %q, want %q", name, a.Component, component)
	}
	if len(a.Objects) == 0 {
		t.Fatalf("application %q generated no object", name)
	}
	for _, p := range a.Objects {
		got, has := (*p).GetLabels()[key]
		if component == "" && has {
			t.Errorf("application %q object %q carries %s=%q, want no component label", name, (*p).GetName(), key, got)
		}
		if component != "" && got != ComponentLabelValue(component) {
			t.Errorf("application %q object %q %s = %q, want %q", name, (*p).GetName(), key, got, ComponentLabelValue(component))
		}
	}
}

// TestTransform_ComponentOwnership is go-kure/launcher#788: every application
// of a transformed document reports its authored component and labels its
// objects with it, whatever produced the application; an application the
// document as a whole owns reports none and labels nothing.
func TestTransform_ComponentOwnership(t *testing.T) {
	tr := ownershipTransformer()
	app := makeApp("shop",
		Component{Name: "web", Type: "webservice", Traits: []Trait{
			{Type: "settings", Properties: map[string]any{"name": "web-settings"}},
			{Type: "routed", Properties: map[string]any{"external": "legacy"}},
		}},
		Component{Name: "api", Type: "parted", Traits: []Trait{{Type: "settings", Properties: map[string]any{"name": "api-settings"}}}},
		Component{Name: "db", Type: "grouped"},
		Component{Name: "one", Type: "sourced"},
		Component{Name: "two", Type: "sourced"},
	)
	ctx := TransformContext{
		Domain: "launcher.gokure.dev",
		EgressPeers: map[string][]netpol.EgressPeer{"web": {{
			Namespace:   "data",
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "pg"}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
		}}},
		IngressPeers: map[string][]netpol.IngressPeer{"api-part": {{
			Endpoint: netpol.Endpoint{
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "part"}},
				Ports:       []intstr.IntOrString{intstr.FromInt32(9000)},
			},
			Sources: []netpol.TrafficSource{{Namespace: "edge", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "gw"}}}},
		}}},
	}
	apps := generatedByName(t, tr, app, ctx)

	for name, component := range map[string]string{
		"web":                             "web", // a component's own application
		"web-settings":                    "web", // a trait sub-application
		"web-allow-ingress-traffic":       "web", // synthesized NetworkPolicies
		"web-allow-egress-traffic":        "web",
		"api":                             "api", // lowered, same name
		"api-part":                        "api", // lowered, named differently
		"api-settings":                    "api", // a trait a lowering rule forwarded
		"api-part-allow-endpoint-ingress": "api", // a policy for a lowered part
		"db":                              "db",  // a sibling group
		"one":                             "one",
		"two":                             "two",
		"charts":                          "", // the generated source both share
		"legacy-allow-ingress-traffic":    "", // an external backend's policy
	} {
		assertOwner(t, apps, name, ownershipKey, component)
	}
}

// TestTransform_ComponentOwnership_Key: the label key is ComponentLabelKey,
// else the domain's, else the default domain's: the key the synthesized
// NetworkPolicies select.
func TestTransform_ComponentOwnership_Key(t *testing.T) {
	for _, tc := range []struct {
		ctx TransformContext
		key string
	}{
		{TransformContext{}, ComponentLabel},
		{TransformContext{Domain: "example.org"}, "example.org/component"},
		{TransformContext{Domain: "example.org", ComponentLabelKey: "example.org/owner"}, "example.org/owner"},
	} {
		apps := generatedByName(t, ownershipTransformer(), makeApp("shop", Component{Name: "web", Type: "webservice"}), tc.ctx)
		assertOwner(t, apps, "web", tc.key, "web")
	}
}

// TestTransform_ComponentOwnership_LongName: the label value is the component's
// projected label value, the one a synthesized policy selects.
func TestTransform_ComponentOwnership_LongName(t *testing.T) {
	name := longNetpolComponentName(t, "web")
	apps := generatedByName(t, ownershipTransformer(), makeApp("shop", Component{Name: name, Type: "webservice"}), TransformContext{})
	assertOwner(t, apps, name, ComponentLabel, name)
	if got := (*apps[name].Objects[0]).GetLabels()[ComponentLabel]; got == name {
		t.Errorf("label value = the %d-character name, want its projection", len(name))
	}
}

// TestGenerateApplications_UntransformedConfig: an application a caller adds
// itself keeps reporting ComponentNamed, else its name.
func TestGenerateApplications_UntransformedConfig(t *testing.T) {
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: &stack.Bundle{Applications: []*stack.Application{
		stack.NewApplication("plain", "ns", &namedConfigMapConfig{name: "plain", namespace: "ns"}),
		stack.NewApplication("sub", "ns", &settingsConfig{namedConfigMapConfig{name: "sub", namespace: "ns"}, "web"}),
	}}}}
	apps, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	if apps[0].Component != "plain" || apps[1].Component != "web" {
		t.Errorf("components = %q, %q, want plain, web", apps[0].Component, apps[1].Component)
	}
}
