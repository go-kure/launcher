package components_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestWebserviceRule_ComponentType(t *testing.T) {
	if got := (components.WebserviceRule{}).ComponentType(); got != "webservice" {
		t.Errorf("ComponentType() = %q, want webservice", got)
	}
}

// TestWebserviceRule_PropertySchemaUnchanged pins webservice's published
// property schema byte for byte. testdata/webservice-property-schema.json was
// captured from the former WebserviceHandler.PropertySchema before webservice
// became a lowering rule; the move must not change what HandlerSchemas
// publishes for "webservice".
func TestWebserviceRule_PropertySchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("testdata/webservice-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	got, err := json.MarshalIndent(components.WebserviceRule{}.PropertySchema(), "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	got = append(got, '\n')
	if !bytes.Equal(got, want) {
		t.Errorf("webservice's property schema changed (%d bytes, want %d); it must stay byte-identical to the former handler's", len(got), len(want))
	}
}

// lowerWebservice runs WebserviceRule.LowerComponent and returns its deployment
// and service members.
func lowerWebservice(t *testing.T, comp *oam.Component) (dep, svc oam.Component) {
	t.Helper()
	res, err := components.WebserviceRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	if len(res.Components) != 2 || len(res.Policies) != 0 || len(res.Traits) != 0 || len(res.Documents) != 0 {
		t.Fatalf("LowerComponent emitted %+v, want exactly two components", res)
	}
	return res.Components[0], res.Components[1]
}

// The rule emits a same-name pair, a deployment then a service. The deployment
// carries the authored properties webservice declares, minus port,
// topologySpread and affinity, plus the main container's one port named http;
// the service has one port named http on the same number and no authored
// selector, so it selects app: <component name>. Annotations reach both, the
// authored map is not written to, and an undeclared key is dropped.
func TestWebserviceRule_EmitsDeploymentAndService(t *testing.T) {
	authored := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"port":           8080,
		"replicas":       2,
		"topologySpread": true,
		"env":            []any{map[string]any{"name": "A", "value": "b"}},
		"notDeclared":    "x",
	}
	before := maps.Clone(authored)
	comp := &oam.Component{
		Name: "web", Type: "webservice", Properties: authored,
		Annotations: map[string]string{"launcher.gokure.dev/tier": "infra"},
	}
	dep, svc := lowerWebservice(t, comp)

	if dep.Name != "web" || dep.Type != "deployment" || svc.Name != "web" || svc.Type != "service" {
		t.Fatalf("emitted %s/%s and %s/%s, want web/deployment and web/service", dep.Name, dep.Type, svc.Name, svc.Type)
	}
	for _, m := range []oam.Component{dep, svc} {
		if !reflect.DeepEqual(m.Annotations, comp.Annotations) {
			t.Errorf("%s annotations = %v, want the authored %v", m.Type, m.Annotations, comp.Annotations)
		}
	}
	if !reflect.DeepEqual(authored, before) {
		t.Errorf("the authored properties were modified: %v", authored)
	}
	for _, k := range []string{"port", "topologySpread", "affinity", "notDeclared"} {
		if _, ok := dep.Properties[k]; ok {
			t.Errorf("deployment properties carry %q; want it removed", k)
		}
	}
	for _, k := range []string{"image", "replicas", "env"} {
		if !reflect.DeepEqual(dep.Properties[k], authored[k]) {
			t.Errorf("deployment %s = %v, want the authored %v", k, dep.Properties[k], authored[k])
		}
	}
	wantDepPorts := []any{map[string]any{"name": "http", "containerPort": 8080}}
	if !reflect.DeepEqual(dep.Properties["ports"], wantDepPorts) {
		t.Errorf("deployment ports = %#v, want %#v", dep.Properties["ports"], wantDepPorts)
	}
	wantSvc := map[string]any{"ports": []any{map[string]any{"name": "http", "port": 8080}}}
	if !reflect.DeepEqual(svc.Properties, wantSvc) {
		t.Errorf("service properties = %#v, want %#v", svc.Properties, wantSvc)
	}

	// port defaults to 80, as the handler's did.
	dep, svc = lowerWebservice(t, &oam.Component{Name: "web", Type: "webservice",
		Properties: map[string]any{"image": "ghcr.io/org/app:v1"}})
	if got := dep.Properties["ports"].([]any)[0].(map[string]any)["containerPort"]; got != 80 {
		t.Errorf("default containerPort = %v, want 80", got)
	}
	if got := svc.Properties["ports"].([]any)[0].(map[string]any)["port"]; got != 80 {
		t.Errorf("default Service port = %v, want 80", got)
	}
}

// The affinity shorthand is evaluated over the component's own app label and
// emitted as the raw corev1 shape, as WorkerRule does.
func TestWebserviceRule_AffinityShorthandBecomesRaw(t *testing.T) {
	dep, _ := lowerWebservice(t, &oam.Component{Name: "web", Type: "webservice", Properties: map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"affinity": map[string]any{"enablePodAntiAffinity": true},
	}})
	want := map[string]any{
		"podAntiAffinity": map[string]any{
			"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
				"weight": int64(100),
				"podAffinityTerm": map[string]any{
					"labelSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
					"topologyKey":   "kubernetes.io/hostname",
				},
			}},
		},
	}
	if !reflect.DeepEqual(dep.Properties["affinity"], want) {
		t.Errorf("emitted affinity = %#v\nwant %#v", dep.Properties["affinity"], want)
	}

	_, err := components.WebserviceRule{}.LowerComponent(&oam.Component{Name: "web", Type: "webservice", Properties: map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"affinity": map[string]any{"nodeSelector": map[string]any{"bad key!": "x"}},
	}}, oam.LoweringContext{})
	if err == nil || !strings.HasPrefix(err.Error(), "affinity: the shorthand evaluates to an affinity the API server would refuse: ") {
		t.Errorf("err = %v, want the shorthand refusal", err)
	}
}

// Each authored trait goes, by value and in authored order, to the member it
// acts on: the routing traits to the service; prune-protection and
// force-replace to both; everything else — the workload traits, the bundle
// traits (fluxcd-*), and a type the rule does not know — to the deployment.
// topologySpread (absent or true) puts a propertyless topology-spread in front
// of the deployment's traits; false puts none.
func TestWebserviceRule_RoutesTraits(t *testing.T) {
	authored := []oam.Trait{
		{Type: "prune-protection"},
		{Type: "ingress", Properties: map[string]any{"rules": []any{}}},
		{Type: "scaler", Properties: map[string]any{"maxReplicas": 3}},
		{Type: "expose"},
		{Type: "fluxcd-patches"},
		{Type: "httproute"},
		{Type: "force-replace"},
		{Type: "fluxcd-postbuild"},
		{Type: "an-extension-trait"},
	}
	types := func(ts []oam.Trait) []string {
		out := make([]string, len(ts))
		for i := range ts {
			out[i] = ts[i].Type
		}
		return out
	}
	wantSvc := []string{"prune-protection", "ingress", "expose", "httproute", "force-replace"}
	wantDep := []string{"prune-protection", "scaler", "fluxcd-patches", "force-replace", "fluxcd-postbuild", "an-extension-trait"}

	for name, ts := range map[string]any{"absent": "absent", "true": true, "false": false} {
		props := map[string]any{"image": "ghcr.io/org/app:v1"}
		if ts != "absent" {
			props["topologySpread"] = ts
		}
		dep, svc := lowerWebservice(t, &oam.Component{Name: "web", Type: "webservice", Properties: props, Traits: authored})
		want := wantDep
		if ts != false {
			want = append([]string{"topology-spread"}, wantDep...)
			if len(dep.Traits[0].Properties) != 0 {
				t.Errorf("%s: synthesized topology-spread carries properties %v", name, dep.Traits[0].Properties)
			}
		}
		if got := types(dep.Traits); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: deployment traits = %v, want %v", name, got, want)
		}
		if got := types(svc.Traits); !reflect.DeepEqual(got, wantSvc) {
			t.Errorf("%s: service traits = %v, want %v", name, got, wantSvc)
		}
	}

	// Forwarded by value: the properties are the authored ones.
	_, svc := lowerWebservice(t, &oam.Component{Name: "web", Type: "webservice",
		Properties: map[string]any{"image": "ghcr.io/org/app:v1"}, Traits: authored})
	if !reflect.DeepEqual(svc.Traits[1], authored[1]) {
		t.Errorf("forwarded ingress = %+v, want the authored %+v", svc.Traits[1], authored[1])
	}
}

// Every refusal is the former handler's, first failure first, with the same
// cause text: the rule runs webservice's own parse before it emits anything,
// starting with the Service-name check on the component name.
func TestWebserviceRule_RefusesAsTheHandlerDid(t *testing.T) {
	cases := []struct {
		name      string
		component string
		props     map[string]any
		want      string
	}{
		{"name not a Service name", "1web", map[string]any{"image": "nginx:1"}, `name: "1web" is not a valid Service name`},
		{"missing image", "web", map[string]any{}, "required property 'image' missing or not a string"},
		{"bad image", "web", map[string]any{"image": "UPPER CASE"}, `image "UPPER CASE" rejected: could not parse reference: UPPER CASE`},
		{"port out of range", "web", map[string]any{"image": "nginx:1", "port": 0}, "port"},
		{"topologySpread not a bool", "web", map[string]any{"image": "nginx:1", "topologySpread": "false"}, "topologySpread: must be a boolean, got string"},
		{"probe on another port name", "web", map[string]any{"image": "nginx:1",
			"probes": map[string]any{"liveness": map[string]any{"httpGet": map[string]any{"path": "/", "port": "metrics"}}}}, "invalid probe configuration"},
	}
	for _, tc := range cases {
		_, err := components.WebserviceRule{}.LowerComponent(&oam.Component{Name: tc.component, Type: "webservice", Properties: tc.props}, oam.LoweringContext{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

// Endpoints declares the component's own pods on its one port, and refuses a
// wrongly typed port rather than declaring the default.
func TestWebserviceRule_Endpoints(t *testing.T) {
	eps, err := components.WebserviceRule{}.Endpoints(&oam.Component{Name: "web", Type: "webservice",
		Properties: map[string]any{"image": "nginx:1", "port": 8080}})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 || !reflect.DeepEqual(eps[0].PodSelector.MatchLabels, map[string]string{"app": "web"}) ||
		!reflect.DeepEqual(eps[0].Ports, []intstr.IntOrString{intstr.FromInt32(8080)}) {
		t.Errorf("Endpoints = %+v, want app=web on 8080", eps)
	}
	if _, err := (components.WebserviceRule{}).Endpoints(&oam.Component{Name: "web", Type: "webservice",
		Properties: map[string]any{"image": "nginx:1", "port": "8080"}}); err == nil {
		t.Error("Endpoints accepted a string port")
	}
}
