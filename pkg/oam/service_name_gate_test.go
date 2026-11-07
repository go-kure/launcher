package oam

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These tests pin go-kure/launcher#1230 change 2: below the release where the
// RelaxedServiceNameValidation feature gate is on by default, the API server
// refuses a Service whose name starts with a digit, so generating one for a
// ClusterProfile that states such a release is refused. The check sits in the
// ownership wrapper (ownedConfig.check), which sees every object an application
// generates with the component that owns it.

func serviceNameTarget(minor int) *KubernetesVersion {
	return &KubernetesVersion{Major: 1, Minor: minor}
}

// TestServiceNameGate_Row pins the table row: the gate turns on by default at
// 1.36 (kubernetes.io feature-gates page: Default false from 1.34 to 1.35, true
// from 1.36), and the code lists it as a gate it checks itself.
func TestServiceNameGate_Row(t *testing.T) {
	on, ok := FeatureGateOnByDefault("RelaxedServiceNameValidation")
	if !ok {
		t.Fatal("the lifecycle table has no RelaxedServiceNameValidation row")
	}
	if want := (KubernetesVersion{Major: 1, Minor: 36}); on != want {
		t.Errorf("RelaxedServiceNameValidation is on by default at %s, want %s", on, want)
	}
	if !slices.Contains(CodeCheckedGates(), "RelaxedServiceNameValidation") {
		t.Errorf("CodeCheckedGates() = %v, want it to list RelaxedServiceNameValidation", CodeCheckedGates())
	}
}

// TestServiceNameGateProblem: the rule on a name, apart from any object. The
// first byte decides; the rest of the name is the DNS label rule's, which the
// handlers apply first.
func TestServiceNameGateProblem(t *testing.T) {
	cases := []struct {
		name   string
		svc    string
		target KubernetesVersion
		// refused is true when the name must be refused.
		refused bool
	}{
		{name: "a digit below the release", svc: "1api", target: KubernetesVersion{1, 35}, refused: true},
		{name: "a digit well below the release", svc: "9x", target: KubernetesVersion{1, 20}, refused: true},
		{name: "a digit at the release", svc: "1api", target: KubernetesVersion{1, 36}},
		{name: "a digit above the release", svc: "1api", target: KubernetesVersion{1, 40}},
		{name: "a digit above the release in the next major", svc: "1api", target: KubernetesVersion{2, 0}},
		{name: "a letter below the release", svc: "api1", target: KubernetesVersion{1, 35}},
		{name: "a name that is only a digit below the release", svc: "7", target: KubernetesVersion{1, 35}, refused: true},
		{name: "a hyphen first is the label rule's, not this one", svc: "-api", target: KubernetesVersion{1, 35}},
		{name: "an empty name", svc: "", target: KubernetesVersion{1, 35}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := serviceNameGateProblem("name", c.svc, c.target)
			if c.refused != (err != nil) {
				t.Fatalf("serviceNameGateProblem(%q, %s) = %v, want refused=%v", c.svc, c.target, err, c.refused)
			}
			if err == nil {
				return
			}
			for _, want := range []string{`"` + c.svc + `"`, "RelaxedServiceNameValidation", "1.36", "kubernetesVersion is " + c.target.String(), "start the name with a letter or raise spec.kubernetesVersion"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

// svcObject is an unstructured object of apiVersion and kind named name.
func svcObject(apiVersion, kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"name": name},
	}}
}

// withServiceName sets spec.serviceName on u.
func withServiceName(u *unstructured.Unstructured, name string) *unstructured.Unstructured {
	u.Object["spec"] = map[string]any{"serviceName": name}
	return u
}

// generateAtTarget generates an application whose config emits objs, wrapped for
// component (and entry) with the Kubernetes release target, and with no reserved
// keys and no object kind rules.
func generateAtTarget(component, entry string, target *KubernetesVersion, objs ...client.Object) error {
	cfg := wrapOwnedEntryConfigTarget(&ownershipObjectsConfig{objects: objs}, component, entry, ownershipKey, nil, nil, target)
	_, err := stack.NewApplication("web", "ns", cfg).Generate()
	return err
}

// wantServiceNameRefusal fails unless err is a *ViolationError of the owner, with
// no policy class (the rule is the library's own, not the environment policy's),
// whose text holds every fragment.
func wantServiceNameRefusal(t *testing.T, err error, owner string, fragments ...string) {
	t.Helper()
	var v *ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error = %v, want a *ViolationError", err)
	}
	if v.Component != owner || v.Class != RefusalUnclassified {
		t.Errorf("violation = component %q class %q, want %q and no class: %v", v.Component, v.Class, owner, err)
	}
	for _, f := range append([]string{"RelaxedServiceNameValidation", "1.36", "kubernetesVersion is 1.35"}, fragments...) {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("refusal %q lacks %q", err, f)
		}
	}
}

// TestOwnedConfig_ServiceNameGate: each object that names a Service, at 1.35.
// A core Service names it in metadata.name. A CloudNativePG Pooler and the
// Prometheus operator's three workloads emit a custom resource, and an operator
// creates the Service from it, so the name is read from the field it comes from.
func TestOwnedConfig_ServiceNameGate(t *testing.T) {
	list := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List", "metadata": map[string]any{},
		"items": []any{svcObject("v1", "Service", "1member").Object},
	}}

	cases := []struct {
		name    string
		object  client.Object
		refused []string // fragments of the refusal; nil means the object passes
	}{
		{name: "a typed Service", object: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "1api"}},
			refused: []string{`Service "1api"`}},
		{name: "an unstructured Service", object: svcObject("v1", "Service", "1api"),
			refused: []string{`Service "1api"`}},
		{name: "a member of a List", object: list,
			refused: []string{`Service "1member"`}},
		{name: "a Pooler", object: svcObject("postgresql.cnpg.io/v1", "Pooler", "1pool"),
			refused: []string{`Pooler "1pool"`, "metadata.name"}},
		{name: "a Prometheus", object: withServiceName(svcObject("monitoring.coreos.com/v1", "Prometheus", "prom"), "1prom"),
			refused: []string{`Prometheus "prom"`, "spec.serviceName", `"1prom"`}},
		{name: "an Alertmanager", object: withServiceName(svcObject("monitoring.coreos.com/v1", "Alertmanager", "am"), "1am"),
			refused: []string{`Alertmanager "am"`, "spec.serviceName", `"1am"`}},
		{name: "a ThanosRuler", object: withServiceName(svcObject("monitoring.coreos.com/v1", "ThanosRuler", "tr"), "1tr"),
			refused: []string{`ThanosRuler "tr"`, "spec.serviceName", `"1tr"`}},

		// What passes at 1.35.
		{name: "a Service named with a letter first", object: svcObject("v1", "Service", "api1")},
		{name: "a Prometheus with no serviceName", object: svcObject("monitoring.coreos.com/v1", "Prometheus", "prom")},
		{name: "a Prometheus whose serviceName starts with a letter", object: withServiceName(svcObject("monitoring.coreos.com/v1", "Prometheus", "prom"), "prom1")},
		{name: "a Pooler named with a letter first", object: svcObject("postgresql.cnpg.io/v1", "Pooler", "pool1")},
		// A StatefulSet's serviceName is a reference the API validates as a DNS-1123
		// label (kubernetes v1.35 apps validation), which admits a digit; the
		// governing Service is its own object, held when it is generated.
		{name: "a StatefulSet serviceName", object: &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "1db"}, Spec: appsv1.StatefulSetSpec{ServiceName: "1gov"}}},
		{name: "a Deployment named with a digit", object: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "1web"}}},
		{name: "a ConfigMap named with a digit", object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "1cm"}}},
		// Another group's kind of the same name is not the Prometheus operator's.
		{name: "a Prometheus of another group", object: withServiceName(svcObject("example.com/v1", "Prometheus", "prom"), "1prom")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := generateAtTarget("web", "web", serviceNameTarget(35), c.object)
			if c.refused == nil {
				if err != nil {
					t.Fatalf("want the object accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want the object refused at 1.35")
			}
			wantServiceNameRefusal(t, err, "web", c.refused...)
		})
	}
}

// TestOwnedConfig_ServiceNameGate_ReleaseAndNoTarget: the same Service is refused
// at 1.35 only. At 1.36, above it, and with no target (a caller that leaves
// TransformContext.TargetVersion unset) nothing is checked.
func TestOwnedConfig_ServiceNameGate_ReleaseAndNoTarget(t *testing.T) {
	obj := func() client.Object { return svcObject("v1", "Service", "1api") }
	if err := generateAtTarget("web", "web", serviceNameTarget(35), obj()); err == nil {
		t.Error("1.35: want the Service refused")
	}
	for _, target := range []*KubernetesVersion{serviceNameTarget(36), serviceNameTarget(37), serviceNameTarget(99), nil} {
		name := "no target"
		if target != nil {
			name = target.String()
		}
		if err := generateAtTarget("web", "web", target, obj()); err != nil {
			t.Errorf("%s: want the Service accepted, got %v", name, err)
		}
	}
}

// TestOwnedConfig_ServiceNameGate_NoOtherDutyToHold: an application with no
// reserved metadata keys, no object kind rules and no owning component is the
// case the wrapper's early returns skip every other check for. The Service is
// held all the same, and the refusal names the entry the application came from.
func TestOwnedConfig_ServiceNameGate_NoOtherDutyToHold(t *testing.T) {
	err := generateAtTarget("", "shop-source", serviceNameTarget(35), svcObject("v1", "Service", "1api"))
	if err == nil {
		t.Fatal("want the Service of a document-owned application refused at 1.35")
	}
	wantServiceNameRefusal(t, err, "shop-source", `Service "1api"`)

	// The same wrapper with a target below the release and an object that passes.
	if err := generateAtTarget("", "shop-source", serviceNameTarget(35), svcObject("v1", "Service", "api1")); err != nil {
		t.Errorf("a Service named with a letter: %v", err)
	}
}

// serviceAugmenter is a config whose layout augmentation adds a Service.
type serviceAugmenter struct {
	ownershipObjectsConfig
	service client.Object
}

func (c *serviceAugmenter) AugmentLayout(l *layout.ManifestLayout) error {
	l.Children = append(l.Children, &layout.ManifestLayout{Name: "added", Resources: []client.Object{c.service}})
	return nil
}

func (c *serviceAugmenter) GenerateCoversAugmentLayout() bool { return false }

// TestOwnedConfig_ServiceNameGate_LayoutAddedService: a Service the wrapped
// config adds in AugmentLayout is held to the rule as one it generates is.
func TestOwnedConfig_ServiceNameGate_LayoutAddedService(t *testing.T) {
	cfg := wrapOwnedEntryConfigTarget(&serviceAugmenter{service: svcObject("v1", "Service", "1api")}, "web", "web", ownershipKey, nil, nil, serviceNameTarget(35))
	augmenter, ok := cfg.(layout.LayoutAugmenter)
	if !ok {
		t.Fatalf("%T is not a layout augmenter", cfg)
	}
	err := augmenter.AugmentLayout(&layout.ManifestLayout{Name: "root"})
	if err == nil {
		t.Fatal("want the Service added by AugmentLayout refused at 1.35")
	}
	wantServiceNameRefusal(t, err, "web", `Service "1api"`)
}
