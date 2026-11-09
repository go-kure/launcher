package kurel

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests below pin go-kure/launcher#1230 change 2: a ClusterProfile that
// states a spec.kubernetesVersion below 1.36 has a Service name that starts with
// a digit refused, because the API server refuses it until the
// RelaxedServiceNameValidation feature gate is on by default. The check holds
// the objects a build generates, so it covers a name the author wrote, one the
// Naming hook answered and a default launcher built.

// serviceNameApp is a one-component Application document.
func serviceNameApp(name, typ string, props map[string]any) map[string]any {
	return labelInvariantApp(name, typ, props, "", nil)
}

// serviceNameProfile is a ClusterProfile stating version, or none when "".
func serviceNameProfile(version string) string {
	if version == "" {
		return profileWithSpec(" {}\n")
	}
	return profileWithSpec(fmt.Sprintf("\n  kubernetesVersion: %q\n", version))
}

// buildServiceName runs `kurel build` on app for a profile stating version.
func buildServiceName(t *testing.T, app map[string]any, version string) error {
	t.Helper()
	appYAML, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("marshaling test application: %v", err)
	}
	_, err = buildWithProfile(t, string(appYAML), serviceNameProfile(version))
	return err
}

// serviceNameSource is one way a Service name reaches a build: the component that
// carries it, what the refusal must name, and the same document with a name that
// starts with a letter.
type serviceNameSource struct {
	name      string
	typ       string
	component string
	props     map[string]any
	// control is the same document with the name starting with a letter.
	controlComponent string
	controlProps     map[string]any
	// fragments are what the refusal names besides the gate: the component, the
	// object and the field.
	fragments []string
}

func webserviceProps(extra map[string]any) map[string]any {
	props := map[string]any{"image": "ghcr.io/example/web:v1.0.0", "port": 8080}
	maps.Copy(props, extra)
	return props
}

// cnpgClusterWithService is a cnpg-cluster that asks CloudNativePG for one
// additional Service, whose serviceTemplate states name; the operator creates the
// Service under that name.
func cnpgClusterWithService(name string) map[string]any {
	return map[string]any{
		"storage": map[string]any{"size": "10Gi"},
		"managed": map[string]any{"services": map[string]any{"additional": []any{
			map[string]any{
				"selectorType": "rw",
				"serviceTemplate": map[string]any{
					"metadata": map[string]any{"name": name},
					"spec":     map[string]any{"type": "ClusterIP"},
				},
			},
		}}},
	}
}

func serviceNameSources() []serviceNameSource {
	svcPorts := func(extra map[string]any) map[string]any {
		props := map[string]any{"ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}}}
		maps.Copy(props, extra)
		return props
	}
	monitoring := func(typ, kind, component, field string, extra map[string]any) serviceNameSource {
		bad := func(name string) map[string]any {
			props := map[string]any{"serviceName": name}
			maps.Copy(props, extra)
			return props
		}
		return serviceNameSource{
			name: typ + " serviceName", typ: typ, component: component, props: bad("1" + component),
			controlComponent: component, controlProps: bad(component + "1"),
			fragments: []string{`component "` + component + `"`, kind + ` "` + component + `"`, field, `"1` + component + `"`},
		}
	}
	return []serviceNameSource{
		{name: "a cnpg-cluster additional service's name", typ: "cnpg-cluster", component: "db",
			props:            cnpgClusterWithService("1extra"),
			controlComponent: "db", controlProps: cnpgClusterWithService("extra1"),
			fragments: []string{`component "db"`, `Cluster "db"`, "spec.managed.services.additional[0].serviceTemplate.metadata.name", `"1extra"`}},
		{name: "a service component's name", typ: "service", component: "1api", props: svcPorts(nil),
			controlComponent: "api1", controlProps: svcPorts(nil),
			fragments: []string{`component "1api"`, `Service metadata.name "1api"`}},
		{name: "a service component's objectName", typ: "service", component: "api", props: svcPorts(map[string]any{"objectName": "1api"}),
			controlComponent: "api", controlProps: svcPorts(map[string]any{"objectName": "api1"}),
			fragments: []string{`component "api"`, `Service metadata.name "1api"`}},
		{name: "a webservice component's name", typ: "webservice", component: "1web", props: webserviceProps(nil),
			controlComponent: "web1", controlProps: webserviceProps(nil),
			fragments: []string{`component "1web"`, `Service metadata.name "1web"`}},
		{name: "a webservice's serviceObjectName", typ: "webservice", component: "web", props: webserviceProps(map[string]any{"serviceObjectName": "1entry"}),
			controlComponent: "web", controlProps: webserviceProps(map[string]any{"serviceObjectName": "entry1"}),
			fragments: []string{`component "web"`, `Service metadata.name "1entry"`}},
		monitoring("alertmanager", "Alertmanager", "am", "spec.serviceName", map[string]any{"version": "v0.28.1", "replicas": 1}),
		monitoring("prometheus", "Prometheus", "prom", "spec.serviceName", nil),
		monitoring("thanosruler", "ThanosRuler", "tr", "spec.serviceName", map[string]any{
			"queryEndpoints": []any{"dnssrv+_http._tcp.thanos-query.monitoring.svc"}}),
		{name: "a cnpg-pooler component's name", typ: "cnpg-pooler", component: "1pool",
			props:            map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}},
			controlComponent: "pool1", controlProps: map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}},
			fragments: []string{`component "1pool"`, `Pooler metadata.name "1pool"`}},
		{name: "a postgresql poolerName", typ: "postgresql", component: "db",
			props:            map[string]any{"version": "16", "storageSize": "10Gi", "pooler": map[string]any{"enabled": true}, "poolerName": "1edge"},
			controlComponent: "db", controlProps: map[string]any{"version": "16", "storageSize": "10Gi", "pooler": map[string]any{"enabled": true}, "poolerName": "edge1"},
			fragments: []string{`component "db"`, `Pooler metadata.name "1edge"`}},
		// The default <name>-pooler is built by launcher, never written by the author.
		{name: "a postgresql pooler's default name", typ: "postgresql", component: "1db",
			props:            map[string]any{"version": "16", "storageSize": "10Gi", "pooler": map[string]any{"enabled": true}, "clusterObjectName": "db"},
			controlComponent: "db1", controlProps: map[string]any{"version": "16", "storageSize": "10Gi", "pooler": map[string]any{"enabled": true}},
			fragments: []string{`component "1db"`, `Pooler metadata.name "1db-pooler"`}},
	}
}

// TestServiceNameGate_EverySource: each way a Service name reaches a build is
// refused at 1.35 naming the owning component, the object and the gate; accepted
// at 1.36, above it and with no version stated; and its letter-first twin is
// accepted at 1.35, which shows the refusal is about the first character.
func TestServiceNameGate_EverySource(t *testing.T) {
	for _, s := range serviceNameSources() {
		t.Run(s.name, func(t *testing.T) {
			app := serviceNameApp(s.component, s.typ, s.props)

			err := buildServiceName(t, app, "1.35")
			if err == nil {
				t.Fatal("1.35: want the Service name refused")
			}
			var v *oam.ViolationError
			if !errors.As(err, &v) {
				t.Errorf("1.35: error %v does not hold a *oam.ViolationError", err)
			}
			for _, want := range append([]string{"RelaxedServiceNameValidation", "1.36", "kubernetesVersion is 1.35"}, s.fragments...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("1.35: refusal %q does not name %q", err, want)
				}
			}
			for _, version := range []string{"1.36", "1.37", "v1.40.2", ""} {
				if err := buildServiceName(t, app, version); err != nil {
					t.Errorf("version %q: want the document built, got %v", version, err)
				}
			}
			control := serviceNameApp(s.controlComponent, s.typ, s.controlProps)
			if err := buildServiceName(t, control, "1.35"); err != nil {
				t.Errorf("1.35 with a name starting with a letter: want the document built, got %v", err)
			}
		})
	}
}

// serviceNameHookDoc is a webservice named web.
func serviceNameHookDoc(t *testing.T) string {
	t.Helper()
	out, err := yaml.Marshal(serviceNameApp("web", "webservice", webserviceProps(nil)))
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	return string(out)
}

// generateForTarget transforms appYAML under ctx and generates it, returning the
// first error of either step.
func generateForTarget(t *testing.T, appYAML string, ctx oam.TransformContext) error {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	validated, err := transformer.ValidateAuthoredProperties(app)
	if err != nil {
		t.Fatalf("validating: %v", err)
	}
	ctx.Domain = kurelDomain
	cluster, err := transformer.Transform(validated, ctx)
	if err != nil {
		return err
	}
	_, err = oam.GenerateApplications(cluster)
	return err
}

// TestServiceNameGate_HookAnswer: a name the Naming hook answers for the
// webservice's Service is held as the author's own is. Transform succeeds, since
// the objects do not exist yet; generating them is refused at 1.35.
func TestServiceNameGate_HookAnswer(t *testing.T) {
	doc := serviceNameHookDoc(t)
	hook := renameBy(map[string]string{"workload-service web": "1web"})
	v135 := oam.KubernetesVersion{Major: 1, Minor: 35}
	v136 := oam.KubernetesVersion{Major: 1, Minor: 36}

	err := generateForTarget(t, doc, oam.TransformContext{Naming: hook, TargetVersion: &v135})
	if err == nil {
		t.Fatal("1.35: want the hook's Service name refused")
	}
	for _, want := range []string{`component "web"`, `Service metadata.name "1web"`, "RelaxedServiceNameValidation", "kubernetesVersion is 1.35"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if err := generateForTarget(t, doc, oam.TransformContext{Naming: hook, TargetVersion: &v136}); err != nil {
		t.Errorf("1.36: %v", err)
	}
	// A caller that leaves TargetVersion unset is not checked.
	if err := generateForTarget(t, doc, oam.TransformContext{Naming: hook}); err != nil {
		t.Errorf("no TargetVersion: %v", err)
	}
	// A hook that answers a name starting with a letter passes at 1.35.
	letter := renameBy(map[string]string{"workload-service web": "web1"})
	if err := generateForTarget(t, doc, oam.TransformContext{Naming: letter, TargetVersion: &v135}); err != nil {
		t.Errorf("1.35 with a name starting with a letter: %v", err)
	}
}

// serviceNamePropertyNames are the properties a handler's schema declares to
// name a Service, an object that is one or the Pooler whose Service CloudNativePG
// names after it.
var serviceNamePropertyNames = []string{"serviceName", "serviceObjectName", "poolerName"}

// serviceNameExempt are the component types that declare a Service-name property
// without emitting a Service or a custom resource an operator makes one from: a
// StatefulSet's serviceName is a reference, validated as a DNS-1123 label by the
// API server at every release (kubernetes v1.35 apps validation), and the
// governing Service is a service component of its own.
var serviceNameExempt = []string{"statefulset"}

// serviceNameScanSkip are the component types the coverage scan below does not
// render, each with the reason its fixture cannot be built here. A type that
// fails to render and is not listed fails the scan: a render error that the scan
// passed over would leave the scan counting fewer objects than it appears to,
// and the positive control would no longer say what it was written to say. It is
// empty because every built-in fixture renders at the name the scan gives it.
var serviceNameScanSkip = map[string]string{}

// serviceNameScanProps replace a type's shared fixture for the scan alone, each
// with the reason. The scan counts a spec.serviceName only when it is set, and
// the StatefulSet fixture authors none (the component has no default, so the
// field stays empty), so without this row the StatefulSet would render and
// carry no Service name.
//
// The cnpg-cluster fixture asks for no additional service, so the scan gives it
// one: the Cluster names a Service only through spec.managed.services.additional.
var serviceNameScanProps = map[string]map[string]any{
	"statefulset":  workloadProps(map[string]any{"serviceName": "web"}),
	"cnpg-cluster": cnpgClusterWithService("extra"),
}

// serviceNameBearers are the objects the scan must find that name a Service, as
// "component type/kind": the Service of service and of webservice (a lowering
// rule, which lowers to a service component), a Pooler, and a StatefulSet that
// authors serviceName. Each row is asserted by name, so a fixture that stops
// rendering one fails on that row and not on a count.
var serviceNameBearers = []string{
	"cnpg-cluster/Cluster",
	"cnpg-pooler/Pooler",
	"service/Service",
	"statefulset/StatefulSet",
	"webservice/Service",
}

// TestServiceNameGate_EveryBuiltinKindIsCovered derives the kinds that carry a
// Service name from the built-in component types, handlers and lowering rules
// alike, not from a list kept beside them: every object any built-in fixture
// renders that is a Service, a Pooler or sets spec.serviceName. A handler whose
// schema declares a Service-name property must also be a source the table above
// exercises (or be exempt with its reason); that check runs on handlers only,
// because a lowering rule lowers to handler types whose schemas it already
// covers. Each rendered kind must be held by
// the check, so a new kind that names a Service fails here until the gate holds
// it, and an object not listed in serviceNameBearers fails by its type and kind.
func TestServiceNameGate_EveryBuiltinKindIsCovered(t *testing.T) {
	tested := map[string]bool{}
	for _, s := range serviceNameSources() {
		tested[s.typ] = true
	}
	// The kinds the check holds are the code's own list, not one kept here: dropping
	// a row of it leaves the kind a fixture renders unheld, and this test fails on it.
	known := map[schema.GroupKind]bool{}
	for _, gk := range oam.ServiceNameCheckedKinds() {
		known[gk] = true
	}
	exemptKind := map[string]bool{"StatefulSet": true}

	handlers := builtinComponentHandlers()
	types := map[string]bool{}
	for typ := range handlers {
		types[typ] = true
	}
	for typ := range builtinComponentLoweringRules() { // webservice, worker, helm, postgresql and oci are rules, not handlers
		types[typ] = true
	}
	found := map[string]bool{}
	for _, typ := range slices.Sorted(maps.Keys(types)) {
		if p, ok := handlers[typ].(oam.PropertySchemaProvider); ok {
			for _, name := range serviceNamePropertyNames {
				if _, declared := p.PropertySchema()[name]; declared && !tested[typ] && !slices.Contains(serviceNameExempt, typ) {
					t.Errorf("component type %q declares %q, a Service name, and is neither a source of TestServiceNameGate_EverySource nor exempt", typ, name)
				}
			}
		}
		fx, has := componentLabelFixtures[typ]
		if !has {
			t.Errorf("component type %q has no fixture in componentLabelFixtures", typ)
			continue
		}
		props := fx.props
		if fx.propsFor != nil {
			props = fx.propsFor(t)
		}
		if scan, ok := serviceNameScanProps[typ]; ok {
			props = scan
		}
		name := "widget"
		if fx.apiName != "" {
			name = fx.apiName
		}
		if _, skipped := serviceNameScanSkip[typ]; skipped {
			continue
		}
		docs, err := renderLabelInvariantErr(t, serviceNameApp(name, typ, props))
		if err != nil {
			t.Errorf("component type %q does not render for the scan: %v; fix its fixture, or list it in serviceNameScanSkip with the reason", typ, err)
			continue
		}
		for _, doc := range docs {
			kind, _ := doc["kind"].(string)
			apiVersion, _ := doc["apiVersion"].(string)
			group, _, hasGroup := strings.Cut(apiVersion, "/") // "v1" has no group: the core group is ""
			if !hasGroup {
				group = ""
			}
			gk := schema.GroupKind{Group: group, Kind: kind}
			spec, _ := doc["spec"].(map[string]any)
			serviceName, _ := spec["serviceName"].(string)
			managed, _ := spec["managed"].(map[string]any)
			services, _ := managed["services"].(map[string]any)
			additional, _ := services["additional"].([]any)
			// An unset serviceName names nothing, and neither does a Cluster with no
			// additional Services.
			if kind != "Service" && kind != "Pooler" && serviceName == "" && len(additional) == 0 {
				continue
			}
			key := typ + "/" + kind
			found[key] = true
			if !slices.Contains(serviceNameBearers, key) {
				t.Errorf("component type %q renders a %s that names a Service and is not in serviceNameBearers: add %q there once the Service name check holds it", typ, kind, key)
			}
			if !known[gk] && !exemptKind[kind] {
				t.Errorf("component type %q renders a %s that names a Service (group %q, kind %q), which the Service name check does not hold: add it to the check's table, or exempt it with its reason", typ, kind, gk.Group, kind)
			}
		}
	}
	// The positive control: each expected bearer is rendered, so an empty or
	// partial scan cannot read as covered, and a fixture that stops rendering one
	// fails on that row by name.
	for _, key := range serviceNameBearers {
		if !found[key] {
			t.Errorf("the scan did not find %q, an object that names a Service; its fixture no longer renders it", key)
		}
	}
	t.Logf("scan: %d bearers by type/kind, %d tested sources", len(found), len(tested))
}
