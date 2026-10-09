package components_test

import (
	"reflect"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The pod view of a monitoring workload kind's object (go-kure/launcher#974):
// what MonitoringPodSpec returns for each kind, for objects of no such kind,
// and that the view is a copy. Every registered kind having a case is
// TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind
// (pkg/oam/builtin/registry).

// TestMonitoringPodSpec_Kinds: for each monitoring workload kind, an object
// generated from authored pod fields yields them in the view, at the paths
// they have under spec, and nothing the author did not write.
func TestMonitoringPodSpec_Kinds(t *testing.T) {
	privileged := true
	sidecar := map[string]any{
		"name":            "sidecar",
		"image":           "registry.example/sidecar:v1.0.0",
		"securityContext": map[string]any{"privileged": privileged},
	}
	for _, tc := range []struct {
		name string
		obj  func(t *testing.T) client.Object
		// want is the view obj must yield, read from obj's own spec so each
		// field sits where it does under spec.
		want func(obj client.Object) corev1.PodSpec
	}{
		{
			name: "alertmanager: a privileged container, hostNetwork and a pod securityContext",
			obj: func(t *testing.T) client.Object {
				return alertmanagerOf(t, map[string]any{
					"image":           amImage,
					"version":         amVersion,
					"containers":      []any{sidecar},
					"hostNetwork":     true,
					"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 1000},
				})
			},
			want: func(obj client.Object) corev1.PodSpec {
				spec := obj.(*monitoringv1.Alertmanager).Spec
				return corev1.PodSpec{Containers: spec.Containers, HostNetwork: true, SecurityContext: spec.SecurityContext}
			},
		},
		{
			name: "alertmanager: init containers, volumes, a patch of the operator's container and DNS",
			obj: func(t *testing.T) client.Object {
				return alertmanagerOf(t, map[string]any{
					"image":          amImage,
					"version":        amVersion,
					"containers":     []any{map[string]any{"name": "alertmanager", "securityContext": map[string]any{"readOnlyRootFilesystem": true}}},
					"initContainers": []any{map[string]any{"name": "setup", "image": "registry.example/setup:v1.0.0"}},
					"volumes":        []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}},
					"dnsPolicy":      "None",
					"dnsConfig":      map[string]any{"nameservers": []any{"10.0.0.10"}, "searches": []any{"example.internal"}},
				})
			},
			want: func(obj client.Object) corev1.PodSpec {
				spec := obj.(*monitoringv1.Alertmanager).Spec
				return corev1.PodSpec{
					Containers:     spec.Containers,
					InitContainers: spec.InitContainers,
					Volumes:        spec.Volumes,
					DNSPolicy:      corev1.DNSNone,
					// The view carries the nameservers only: the searches
					// and options are not read by the pod checks.
					DNSConfig: &corev1.PodDNSConfig{Nameservers: []string{"10.0.0.10"}},
				}
			},
		},
		{
			name: "prometheus: a privileged container, hostNetwork, a pod securityContext, init containers and volumes",
			obj: func(t *testing.T) client.Object {
				return prometheusOf(t, map[string]any{
					"containers":      []any{sidecar, map[string]any{"name": "prometheus", "securityContext": map[string]any{"readOnlyRootFilesystem": true}}},
					"initContainers":  []any{map[string]any{"name": "setup", "image": "registry.example/setup:v1.0.0"}},
					"volumes":         []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}},
					"hostNetwork":     true,
					"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 1000},
					"dnsPolicy":       "None",
					"dnsConfig":       map[string]any{"nameservers": []any{"10.0.0.10"}, "searches": []any{"example.internal"}},
				})
			},
			want: func(obj client.Object) corev1.PodSpec {
				spec := obj.(*monitoringv1.Prometheus).Spec
				return corev1.PodSpec{
					Containers:      spec.Containers,
					InitContainers:  spec.InitContainers,
					Volumes:         spec.Volumes,
					HostNetwork:     true,
					SecurityContext: spec.SecurityContext,
					DNSPolicy:       corev1.DNSNone,
					DNSConfig:       &corev1.PodDNSConfig{Nameservers: []string{"10.0.0.10"}},
				}
			},
		},
		{
			// The spec has no hostNetwork, so the view's is never set.
			name: "thanosruler: a privileged container, a pod securityContext, init containers, volumes and DNS",
			obj: func(t *testing.T) client.Object {
				return thanosRulerOf(t, map[string]any{
					"containers":      []any{sidecar, map[string]any{"name": "thanos-ruler", "securityContext": map[string]any{"readOnlyRootFilesystem": true}}},
					"initContainers":  []any{map[string]any{"name": "setup", "image": "registry.example/setup:v1.0.0"}},
					"volumes":         []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}},
					"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 1000},
					"dnsPolicy":       "None",
					"dnsConfig":       map[string]any{"nameservers": []any{"10.0.0.10"}, "searches": []any{"example.internal"}},
				})
			},
			want: func(obj client.Object) corev1.PodSpec {
				spec := obj.(*monitoringv1.ThanosRuler).Spec
				return corev1.PodSpec{
					Containers:      spec.Containers,
					InitContainers:  spec.InitContainers,
					Volumes:         spec.Volumes,
					SecurityContext: spec.SecurityContext,
					DNSPolicy:       corev1.DNSNone,
					DNSConfig:       &corev1.PodDNSConfig{Nameservers: []string{"10.0.0.10"}},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := tc.obj(t)
			view, prefix, ok := components.MonitoringPodSpec(obj)
			if !ok || prefix != "spec" || view == nil {
				t.Fatalf("MonitoringPodSpec = %v, %q, %v; want a view under spec, and true", view, prefix, ok)
			}
			if want := tc.want(obj); !reflect.DeepEqual(*view, want) {
				t.Errorf("view = %+v\nwant   %+v", *view, want)
			}
		})
	}

	// The acceptance case read field by field, so a failure names the field.
	am := alertmanagerOf(t, map[string]any{
		"image":           amImage,
		"version":         amVersion,
		"containers":      []any{sidecar},
		"hostNetwork":     true,
		"securityContext": map[string]any{"runAsNonRoot": true},
	})
	view, _, _ := components.MonitoringPodSpec(am)
	if len(view.Containers) != 1 || view.Containers[0].SecurityContext == nil || view.Containers[0].SecurityContext.Privileged == nil || !*view.Containers[0].SecurityContext.Privileged {
		t.Errorf("containers = %+v, want the privileged sidecar at containers[0]", view.Containers)
	}
	if !view.HostNetwork {
		t.Error("hostNetwork = false, want true")
	}
	if view.SecurityContext == nil || view.SecurityContext.RunAsNonRoot == nil || !*view.SecurityContext.RunAsNonRoot {
		t.Errorf("securityContext = %+v, want runAsNonRoot true", view.SecurityContext)
	}
}

// TestMonitoringPodSpec_NotAMonitoringWorkload: an object of no monitoring
// workload kind, of the operator's API or another, a nil object and a nil
// object of each monitoring workload kind each give no view, no prefix and
// false.
func TestMonitoringPodSpec_NotAMonitoringWorkload(t *testing.T) {
	for name, obj := range map[string]client.Object{
		"a Deployment":       &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{HostNetwork: true}}}},
		"a PodMonitor":       &monitoringv1.PodMonitor{},
		"a nil object":       nil,
		"a nil Alertmanager": (*monitoringv1.Alertmanager)(nil),
		"a nil Prometheus":   (*monitoringv1.Prometheus)(nil),
		"a nil ThanosRuler":  (*monitoringv1.ThanosRuler)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			view, prefix, ok := components.MonitoringPodSpec(obj)
			if view != nil || prefix != "" || ok {
				t.Errorf("MonitoringPodSpec = %v, %q, %v; want nil, \"\", false", view, prefix, ok)
			}
		})
	}
}

// TestMonitoringPodSpec_ViewIsACopy: changing every part of the view, down to
// the values a container's security context and a volume point at, leaves the
// object as it was.
func TestMonitoringPodSpec_ViewIsACopy(t *testing.T) {
	am := alertmanagerOf(t, map[string]any{
		"image":           amImage,
		"version":         amVersion,
		"containers":      []any{map[string]any{"name": "sidecar", "image": "registry.example/sidecar:v1.0.0", "securityContext": map[string]any{"privileged": false}}},
		"initContainers":  []any{map[string]any{"name": "setup", "image": "registry.example/setup:v1.0.0"}},
		"volumes":         []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}},
		"securityContext": map[string]any{"runAsNonRoot": true},
		"dnsPolicy":       "None",
		"dnsConfig":       map[string]any{"nameservers": []any{"10.0.0.10"}},
	})
	before := am.DeepCopy()

	view, _, ok := components.MonitoringPodSpec(am)
	if !ok {
		t.Fatal("MonitoringPodSpec: not a monitoring workload")
	}
	// Each pointer's target is changed in place, not replaced: a view that
	// shared a pointer with the object would carry the change into it.
	*view.Containers[0].SecurityContext.Privileged = true
	view.Containers[0].Image = "elsewhere.example/sidecar:v2.0.0"
	view.Containers = append(view.Containers, corev1.Container{Name: "added"})
	view.InitContainers[0].Name = "renamed"
	view.Volumes[0].EmptyDir.Medium = corev1.StorageMediumMemory
	view.Volumes[0].HostPath = &corev1.HostPathVolumeSource{Path: "/"}
	*view.SecurityContext.RunAsNonRoot = false
	view.HostNetwork = true
	view.DNSConfig.Nameservers[0] = "192.0.2.1"

	if !reflect.DeepEqual(am, before) {
		t.Errorf("changing the view changed the object:\n got %+v\nwant %+v", am.Spec, before.Spec)
	}
}
