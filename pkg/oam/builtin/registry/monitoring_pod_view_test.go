package registry_test

import (
	"reflect"
	"slices"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind holds
// components.MonitoringPodSpec to the registry (go-kure/launcher#974): every
// registered component type whose object is of the Prometheus operator's API
// and lists containers in its spec, the mark of an object the operator builds
// pods from, has its case, and every other one of that API has none. The set
// is read from the registry and the operator's types, not from a list kept
// here, so a monitoring workload kind registered without a case fails, by its
// type.
func TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := monitoringv1.AddToScheme(scheme); err != nil {
		t.Fatalf("register the Prometheus operator's v1 types: %v", err)
	}
	var workloads []string
	for typ, h := range registry.ComponentHandlers() {
		provider, ok := h.(oam.ComponentObjectProvider)
		if !ok {
			continue
		}
		kind, _ := provider.ComponentObject()
		if kind.Group != monitoringv1.SchemeGroupVersion.Group {
			continue
		}
		obj := newMonitoringObject(t, scheme, typ, kind.Kind)
		workload := listsContainers(reflect.TypeOf(obj).Elem())
		if workload {
			workloads = append(workloads, typ)
		}
		view, prefix, got := components.MonitoringPodSpec(obj)
		switch {
		case workload && (!got || view == nil || prefix != "spec"):
			t.Errorf("%s: its %s lists containers, so the Prometheus operator builds pods from it, and MonitoringPodSpec has no case for it (= %v, %q, %v); add one from the kind's monitoringWorkload", typ, kind.Kind, view, prefix, got)
		case !workload && got:
			t.Errorf("%s: its %s lists no containers, and MonitoringPodSpec gives it a view", typ, kind.Kind)
		}
	}
	// The walk found the kinds it is to hold: without one, the test above
	// holds nothing.
	for _, want := range []string{"alertmanager", "prometheus", "thanosruler"} {
		if !slices.Contains(workloads, want) {
			t.Errorf("monitoring workload kinds found: %v; want %s among them: the walk is broken", workloads, want)
		}
	}
}

// newMonitoringObject returns a new object of the operator's kind as scheme
// knows it, failing where scheme knows no such kind: a kind of another
// version of the API is then added to scheme here.
func newMonitoringObject(t *testing.T, scheme *runtime.Scheme, typ, kind string) client.Object {
	t.Helper()
	gvk := monitoringv1.SchemeGroupVersion.WithKind(kind)
	raw, err := scheme.New(gvk)
	if err != nil {
		t.Fatalf("%s: its object %s is not of the Prometheus operator's v1 types: %v; register its API version in this test", typ, gvk, err)
	}
	obj, ok := raw.(client.Object)
	if !ok {
		t.Fatalf("%s: %T is no client.Object", typ, raw)
	}
	return obj
}

// listsContainers says whether the object type's spec has a field of
// containers, the list the operator merges into the pods it builds.
func listsContainers(typ reflect.Type) bool {
	spec, ok := typ.FieldByName("Spec")
	if !ok || spec.Type.Kind() != reflect.Struct {
		return false
	}
	field, ok := spec.Type.FieldByName("Containers")
	return ok && field.Type == reflect.TypeFor[[]corev1.Container]()
}
