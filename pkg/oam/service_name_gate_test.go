package oam

import (
	"encoding/json"
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

// withAdditionalServices sets spec.managed.services.additional on u: one entry
// for each name, whose serviceTemplate states that metadata.name. CloudNativePG
// creates a Service under each of them.
func withAdditionalServices(u *unstructured.Unstructured, names ...string) *unstructured.Unstructured {
	items := make([]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{
			"selectorType": "rw",
			"serviceTemplate": map[string]any{
				"metadata": map[string]any{"name": n},
				"spec":     map[string]any{"type": "ClusterIP"},
			},
		})
	}
	u.Object["spec"] = map[string]any{"managed": map[string]any{"services": map[string]any{"additional": items}}}
	return u
}

// withGoBuiltAdditionalServices is withAdditionalServices with the list built the
// way Go code builds it: a []map[string]any, and a Go int beside the names.
func withGoBuiltAdditionalServices(u *unstructured.Unstructured, names ...string) *unstructured.Unstructured {
	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{
			"selectorType": "rw",
			"serviceTemplate": map[string]any{
				"metadata": map[string]any{"name": n},
				"spec":     map[string]any{"type": "ClusterIP", "ports": 1},
			},
		})
	}
	u.Object["spec"] = map[string]any{"managed": map[string]any{"services": map[string]any{"additional": items}}}
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
			refused: []string{`Service metadata.name "1api"`}},
		{name: "an unstructured Service", object: svcObject("v1", "Service", "1api"),
			refused: []string{`Service metadata.name "1api"`}},
		{name: "a member of a List", object: list,
			refused: []string{`Service metadata.name "1member"`}},
		{name: "a Pooler", object: svcObject("postgresql.cnpg.io/v1", "Pooler", "1pool"),
			refused: []string{`Pooler metadata.name "1pool"`}},
		{name: "a Prometheus", object: withServiceName(svcObject("monitoring.coreos.com/v1", "Prometheus", "prom"), "1prom"),
			refused: []string{`Prometheus "prom"`, "spec.serviceName", `"1prom"`}},
		{name: "an Alertmanager", object: withServiceName(svcObject("monitoring.coreos.com/v1", "Alertmanager", "am"), "1am"),
			refused: []string{`Alertmanager "am"`, "spec.serviceName", `"1am"`}},
		{name: "a ThanosRuler", object: withServiceName(svcObject("monitoring.coreos.com/v1", "ThanosRuler", "tr"), "1tr"),
			refused: []string{`ThanosRuler "tr"`, "spec.serviceName", `"1tr"`}},
		{name: "a PrometheusAgent", object: withServiceName(svcObject("monitoring.coreos.com/v1alpha1", "PrometheusAgent", "agent"), "1agent"),
			refused: []string{`PrometheusAgent "agent"`, "spec.serviceName", `"1agent"`}},
		// CloudNativePG creates a Service under the name each additional service's
		// serviceTemplate states; the Cluster's own name is a DNS-1035 label.
		{name: "a Cluster's additional service", object: withAdditionalServices(svcObject("postgresql.cnpg.io/v1", "Cluster", "db"), "1extra"),
			refused: []string{`Cluster "db"`, "spec.managed.services.additional[0].serviceTemplate.metadata.name", `"1extra"`}},
		{name: "a Cluster's second additional service", object: withAdditionalServices(svcObject("postgresql.cnpg.io/v1", "Cluster", "db"), "extra", "1second"),
			refused: []string{`Cluster "db"`, "spec.managed.services.additional[1].serviceTemplate.metadata.name", `"1second"`}},
		// A builder that makes the list in Go holds a []map[string]any, not the
		// []any a decoder returns; the writer serialises both as the same JSON array.
		{name: "a Cluster's additional service in a Go-built list", object: withGoBuiltAdditionalServices(svcObject("postgresql.cnpg.io/v1", "Cluster", "db"), "1extra"),
			refused: []string{`Cluster "db"`, "spec.managed.services.additional[0].serviceTemplate.metadata.name", `"1extra"`}},

		// What passes at 1.35.
		{name: "a PrometheusAgent whose serviceName starts with a letter", object: withServiceName(svcObject("monitoring.coreos.com/v1alpha1", "PrometheusAgent", "agent"), "agent1")},
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
		{name: "a Cluster whose additional services start with a letter", object: withAdditionalServices(svcObject("postgresql.cnpg.io/v1", "Cluster", "db"), "extra", "second1")},
		{name: "a Cluster with no additional services", object: svcObject("postgresql.cnpg.io/v1", "Cluster", "db")},
		{name: "a Cluster of another group", object: withAdditionalServices(svcObject("example.com/v1", "Cluster", "db"), "1extra")},
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

// TestOwnedConfig_ServiceNameGate_GoBuiltListPasses: the Go-built list passes as
// the decoded one does: from the release, with no target, and below it when the
// names start with a letter. The check is called directly: an object that passes
// goes on to a stage that deep-copies it, which a Go-built list does not survive.
func TestOwnedConfig_ServiceNameGate_GoBuiltListPasses(t *testing.T) {
	cluster := func(name string) client.Object {
		return withGoBuiltAdditionalServices(svcObject("postgresql.cnpg.io/v1", "Cluster", "db"), name)
	}
	cases := []struct {
		name   string
		target *KubernetesVersion
		object client.Object
	}{
		{name: "1.35, a name with a letter first", target: serviceNameTarget(35), object: cluster("extra")},
		{name: "1.36, a name with a digit first", target: serviceNameTarget(36), object: cluster("1extra")},
		{name: "no target, a name with a digit first", target: nil, object: cluster("1extra")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &ownedConfig{component: "web", entry: "web", target: c.target}
			if err := o.checkServiceNames(c.object); err != nil {
				t.Errorf("want the Go-built list accepted, got %v", err)
			}
		})
	}
}

// TestOwnedConfig_ServiceNameGate_UnreadableObjectIsRefused: an object of a kind
// that names a Service, whose fields do not encode as JSON (a value no writer can
// write), is refused below the release as a violation of its owner, naming the
// object. A name the check cannot read is not a name it can take as absent. From
// the release the rule has no say, and the object is left to the writer.
//
// The check is called directly, on a wrapper that owns the document as a whole
// (no component), where no other check reads the object: an owning component's
// label check refuses an object that does not encode, too, and an object that
// passes goes on to a later stage that deep-copies it, which a channel does not
// survive.
func TestOwnedConfig_ServiceNameGate_UnreadableObjectIsRefused(t *testing.T) {
	unreadable := func(group, kind string) client.Object {
		u := svcObject(group+"/v1", kind, "db")
		u.Object["spec"] = map[string]any{"unwritable": make(chan int)}
		return u
	}
	for _, kind := range []string{"Cluster", "Pooler"} {
		t.Run(kind, func(t *testing.T) {
			owner := &ownedConfig{entry: "shop-source", target: serviceNameTarget(35)}
			err := owner.checkServiceNames(unreadable("postgresql.cnpg.io", kind))
			if err == nil {
				t.Fatal("want the unreadable object refused at 1.35")
			}
			var v *ViolationError
			if !errors.As(err, &v) || v.Component != "shop-source" {
				t.Fatalf("error = %v, want a *ViolationError of shop-source", err)
			}
			for _, f := range []string{kind + ` "db"`, "does not encode as JSON"} {
				if !strings.Contains(err.Error(), f) {
					t.Errorf("refusal %q lacks %q", err, f)
				}
			}
			for _, target := range []*KubernetesVersion{serviceNameTarget(36), serviceNameTarget(99)} {
				o := &ownedConfig{component: "web", entry: "web", target: target}
				if err := o.checkServiceNames(unreadable("postgresql.cnpg.io", kind)); err != nil {
					t.Errorf("target %v: the rule has no say, got %v", target, err)
				}
			}
		})
	}
	// A kind that names no Service is none of this check's business.
	o := &ownedConfig{component: "web", entry: "web", target: serviceNameTarget(35)}
	if err := o.checkServiceNames(unreadable("example.com", "Cluster")); err != nil {
		t.Errorf("a kind that names no Service: %v", err)
	}
}

// TestOwnedConfig_ServiceNameGate_MetadataAsWritten: the name of a Service or a
// Pooler is the one the writer writes. An unstructured object whose metadata is a
// Go map[string]string serialises as ordinary JSON, but the object's own GetName
// reads "" from it, so the name is read from the written form, in the refusal as
// in the check. The check is called directly, on a wrapper that owns the
// document as a whole: a passing object would go on to a stage that deep-copies
// it, which a map[string]string does not survive.
func TestOwnedConfig_ServiceNameGate_MetadataAsWritten(t *testing.T) {
	type metadataCase struct{ name, apiVersion, kind, bad, good string }
	cases := []metadataCase{
		{name: "a Service", apiVersion: "v1", kind: "Service", bad: "1api", good: "api1"},
		{name: "a Pooler", apiVersion: "postgresql.cnpg.io/v1", kind: "Pooler", bad: "1pool", good: "pool1"},
	}
	for _, c := range cases {
		goMetadata := func(name string) client.Object {
			return &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": c.apiVersion, "kind": c.kind,
				"metadata": map[string]string{"name": name},
			}}
		}
		t.Run(c.name, func(t *testing.T) {
			o := &ownedConfig{entry: "shop-source", target: serviceNameTarget(35)}
			// The same object with metadata a decoder would return is refused: the control.
			err := o.checkServiceNames(svcObject(c.apiVersion, c.kind, c.bad))
			if err == nil {
				t.Fatal("control: want the object with map[string]any metadata refused at 1.35")
			}
			err = o.checkServiceNames(goMetadata(c.bad))
			if err == nil {
				t.Fatal("want the object with map[string]string metadata refused at 1.35")
			}
			wantServiceNameRefusal(t, err, "shop-source", c.kind+` metadata.name "`+c.bad+`"`)
			if err := o.checkServiceNames(goMetadata(c.good)); err != nil {
				t.Errorf("a name with a letter first: want the object accepted, got %v", err)
			}
		})
	}
}

// TestOwnedConfig_ServiceNameGate_NamePrintedOnce: a Service and a Pooler hold the
// name in metadata.name, so the refusal names the kind and the field and prints the
// name once. The owner "web" is a different value, so it adds no second hit.
func TestOwnedConfig_ServiceNameGate_NamePrintedOnce(t *testing.T) {
	cases := []struct{ name, apiVersion, kind, bad string }{
		{name: "a Service", apiVersion: "v1", kind: "Service", bad: "1api"},
		{name: "a Pooler", apiVersion: "postgresql.cnpg.io/v1", kind: "Pooler", bad: "1api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &ownedConfig{entry: "web", target: serviceNameTarget(35)}
			err := o.checkServiceNames(svcObject(c.apiVersion, c.kind, c.bad))
			if err == nil {
				t.Fatal("want the object refused at 1.35")
			}
			if n := strings.Count(err.Error(), `"`+c.bad+`"`); n != 1 {
				t.Errorf("the name %q appears %d times in %q, want once", c.bad, n, err)
			}
			if want := c.kind + ` metadata.name "` + c.bad + `": a Service name`; !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q lacks %q", err, want)
			}
		})
	}
}

// TestOwnedConfig_ServiceNameGate_KindAsWritten: the kind of an unstructured
// object is the one the writer writes. A kind or an apiVersion held as a
// json.RawMessage reads "" from the object's own accessors, but serialises as the
// string it holds, so the bearer is picked from the written form. A kind that is
// not a string once encoded names no Service this check can read: it is skipped,
// as it was, and the object kind policy owns it. The check is called directly,
// on a wrapper that owns the document as a whole: a passing object would go on to
// a stage that deep-copies it, which a json.RawMessage does not survive.
func TestOwnedConfig_ServiceNameGate_KindAsWritten(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(`"` + s + `"`) }
	type kindCase struct {
		name, apiVersion, kind, bad, good string
		rawKind, rawAPIVersion            bool
	}
	cases := []kindCase{
		{name: "a Service with a raw kind", apiVersion: "v1", kind: "Service", bad: "1api", good: "api1", rawKind: true},
		{name: "a Service with a raw apiVersion", apiVersion: "v1", kind: "Service", bad: "1api", good: "api1", rawAPIVersion: true},
		{name: "a Pooler with a raw kind", apiVersion: "postgresql.cnpg.io/v1", kind: "Pooler", bad: "1pool", good: "pool1", rawKind: true},
		{name: "a Pooler with a raw apiVersion", apiVersion: "postgresql.cnpg.io/v1", kind: "Pooler", bad: "1pool", good: "pool1", rawAPIVersion: true},
		{name: "a Pooler with both raw", apiVersion: "postgresql.cnpg.io/v1", kind: "Pooler", bad: "1pool", good: "pool1", rawKind: true, rawAPIVersion: true},
	}
	for _, c := range cases {
		build := func(name string) client.Object {
			u := svcObject(c.apiVersion, c.kind, name)
			if c.rawKind {
				u.Object["kind"] = raw(c.kind)
			}
			if c.rawAPIVersion {
				u.Object["apiVersion"] = raw(c.apiVersion)
			}
			return u
		}
		t.Run(c.name, func(t *testing.T) {
			o := &ownedConfig{entry: "shop-source", target: serviceNameTarget(35)}
			// The same object with strings is refused: the control.
			if err := o.checkServiceNames(svcObject(c.apiVersion, c.kind, c.bad)); err == nil {
				t.Fatal("control: want the object with string kind and apiVersion refused at 1.35")
			}
			err := o.checkServiceNames(build(c.bad))
			if err == nil {
				t.Fatal("want the object with a raw kind or apiVersion refused at 1.35")
			}
			wantServiceNameRefusal(t, err, "shop-source", c.kind+` metadata.name "`+c.bad+`"`)
			if err := o.checkServiceNames(build(c.good)); err != nil {
				t.Errorf("a name with a letter first: want the object accepted, got %v", err)
			}
		})
	}
	t.Run("a kind that is not a string once encoded is skipped", func(t *testing.T) {
		o := &ownedConfig{entry: "shop-source", target: serviceNameTarget(35)}
		u := svcObject("v1", "Service", "1api")
		u.Object["kind"] = json.RawMessage(`7`)
		if err := o.checkServiceNames(u); err != nil {
			t.Errorf("a kind with no string once encoded names no Service this check can read: got %v", err)
		}
	})
}

// TestServiceNameBearers_HoldEveryMonitoringWorkload: every Prometheus operator
// kind whose object holds pod metadata (operatorMetadataKinds), which are the
// workload kinds, carries spec.serviceName, so each is a bearer. A kind added to
// that table without a row here is a Service name nobody reads.
func TestServiceNameBearers_HoldEveryMonitoringWorkload(t *testing.T) {
	bearers := map[string]bool{}
	for _, k := range ServiceNameCheckedKinds() {
		bearers[k.Group+"/"+k.Kind] = true
	}
	seen := 0
	for _, k := range operatorMetadataKinds {
		if k.group != monitoringGroup || !slices.Equal(k.holder.path, []string{"spec", "podMetadata"}) {
			continue
		}
		seen++
		if !bearers[k.group+"/"+k.kind] {
			t.Errorf("%s %s holds pod metadata, so it is a workload with spec.serviceName, and is not a Service name bearer", k.group, k.kind)
		}
	}
	if seen != 4 {
		t.Errorf("read %d monitoring workload kinds from operatorMetadataKinds, want 4 (Prometheus, PrometheusAgent, Alertmanager, ThanosRuler)", seen)
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
	wantServiceNameRefusal(t, err, "shop-source", `Service metadata.name "1api"`)

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
	// No component and no other duty to hold: the target alone keeps the wrapper
	// from returning early, so the owner is the entry.
	cfg := wrapOwnedEntryConfigTarget(&serviceAugmenter{service: svcObject("v1", "Service", "1api")}, "", "web", ownershipKey, nil, nil, serviceNameTarget(35))
	augmenter, ok := cfg.(layout.LayoutAugmenter)
	if !ok {
		t.Fatalf("%T is not a layout augmenter", cfg)
	}
	err := augmenter.AugmentLayout(&layout.ManifestLayout{Name: "root"})
	if err == nil {
		t.Fatal("want the Service added by AugmentLayout refused at 1.35")
	}
	wantServiceNameRefusal(t, err, "web", `Service metadata.name "1api"`)
}

// TestOwnedConfig_ServiceNameGate_ListGrownByAugmenter: a Service an augmenter
// appends to a list its own Generate returned is held to the rule though the list
// was on the layout before the augmenter ran, with and without object kind rules
// (the Service rule is read again beside them, not only when they are in force).
func TestOwnedConfig_ServiceNameGate_ListGrownByAugmenter(t *testing.T) {
	list := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List",
			"items": []any{svcObject("v1", "ConfigMap", "cm").Object},
		}}
	}
	cases := []struct {
		name  string
		kinds func(t *testing.T) *objectKindRules
	}{
		{name: "no object kind rules", kinds: func(*testing.T) *objectKindRules { return nil }},
		{name: "object kind rules that allow the member", kinds: func(t *testing.T) *objectKindRules { return mustKindRules(t, &kindPolicy{}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			grow := func(member string, target *KubernetesVersion) error {
				cfg := wrapOwnedEntryConfigTarget(&listGrowingAugmenter{
					ownershipObjectsConfig: ownershipObjectsConfig{objects: []client.Object{list()}},
					member:                 svcObject("v1", "Service", member).Object,
				}, "web", "web", ownershipKey, nil, c.kinds(t), target)
				objs, err := cfg.Generate(stack.NewApplication("web", "ns", cfg))
				if err != nil {
					t.Fatalf("Generate = %v, want the list of a ConfigMap generated", err)
				}
				l := &layout.ManifestLayout{}
				for _, p := range objs {
					if p != nil {
						l.Resources = append(l.Resources, *p)
					}
				}
				return cfg.(layout.LayoutAugmenter).AugmentLayout(l)
			}

			err := grow("1api", serviceNameTarget(35))
			if err == nil {
				t.Fatal("want the Service appended to the list refused at 1.35")
			}
			wantServiceNameRefusal(t, err, "web", `Service metadata.name "1api"`)

			// The controls: a name starting with a letter, and a release that allows a digit.
			if err := grow("api1", serviceNameTarget(35)); err != nil {
				t.Errorf("a Service named with a letter appended: %v", err)
			}
			if err := grow("1api", serviceNameTarget(36)); err != nil {
				t.Errorf("1.36: a Service named with a digit appended: %v", err)
			}
		})
	}
}
