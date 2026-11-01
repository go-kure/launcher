package components_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// endpointWorkload is one workload type that reports an endpoint per main-container port.
type endpointWorkload struct {
	typ string
	h   interface {
		oam.ComponentHandler
		oam.EndpointProvider
	}
}

func endpointWorkloads() []endpointWorkload {
	return []endpointWorkload{
		{"deployment", &components.DeploymentHandler{}},
		{"statefulset", &components.StatefulsetHandler{}},
		{"daemonset", &components.DaemonsetHandler{}},
	}
}

func workloadComponent(typ, name string, extra map[string]any) *oam.Component {
	props := map[string]any{"image": "ghcr.io/example/app:1.0"}
	for k, v := range extra {
		props[k] = v
	}
	return &oam.Component{Name: name, Type: typ, Properties: props}
}

func portEntry(port int, name, protocol string) map[string]any {
	m := map[string]any{"containerPort": port}
	if name != "" {
		m["name"] = name
	}
	if protocol != "" {
		m["protocol"] = protocol
	}
	return m
}

// One endpoint per main-container port, in the authored order: the port's name (empty for an
// unnamed port), its numeric containerPort as the only entry of Ports, its protocol, and the
// selector the workload's pods carry.
func TestWorkloadEndpoints_OnePerPort(t *testing.T) {
	ports := []any{
		portEntry(8080, "http", ""),
		portEntry(53, "dns", "UDP"),
		portEntry(9000, "sctp-x", "SCTP"),
		portEntry(7000, "", ""),
		portEntry(7001, "", "UDP"),
	}
	want := []netpol.Endpoint{
		{Name: "http", Protocol: corev1.ProtocolTCP},
		{Name: "dns", Protocol: corev1.ProtocolUDP},
		{Name: "sctp-x", Protocol: corev1.ProtocolSCTP},
		{Name: "", Protocol: corev1.ProtocolTCP},
		{Name: "", Protocol: corev1.ProtocolUDP},
	}
	wantPorts := []int32{8080, 53, 9000, 7000, 7001}
	for _, k := range endpointWorkloads() {
		t.Run(k.typ, func(t *testing.T) {
			eps, err := k.h.Endpoints(workloadComponent(k.typ, "shop", map[string]any{"ports": ports}))
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			if len(eps) != len(want) {
				t.Fatalf("got %d endpoints, want %d: %+v", len(eps), len(want), eps)
			}
			for i, ep := range eps {
				if ep.Name != want[i].Name || ep.Protocol != want[i].Protocol {
					t.Errorf("endpoint[%d] = name %q protocol %q, want name %q protocol %q", i, ep.Name, ep.Protocol, want[i].Name, want[i].Protocol)
				}
				if len(ep.Ports) != 1 || ep.Ports[0].IntVal != wantPorts[i] || ep.Ports[0].StrVal != "" {
					t.Errorf("endpoint[%d] ports = %v, want the number %d only", i, ep.Ports, wantPorts[i])
				}
				if sel := ep.PodSelector; sel == nil || len(sel.MatchLabels) != 1 || sel.MatchLabels["app"] != "shop" || len(sel.MatchExpressions) != 0 {
					t.Errorf("endpoint[%d] selector = %v, want app=shop only", i, sel)
				}
			}
		})
	}
}

// A component that declares no ports has no endpoint, and one that a sidecar declares ports for
// still has none: a sidecar port is not the component's service surface.
func TestWorkloadEndpoints_NoPortsNoEndpoint(t *testing.T) {
	sidecar := []any{map[string]any{
		"name": "proxy", "image": "ghcr.io/example/proxy:1.0",
		"ports": []any{portEntry(15001, "admin", "")},
	}}
	for _, k := range endpointWorkloads() {
		t.Run(k.typ, func(t *testing.T) {
			for name, extra := range map[string]map[string]any{
				"no ports":      nil,
				"empty ports":   {"ports": []any{}},
				"sidecar ports": {"sidecars": sidecar},
			} {
				eps, err := k.h.Endpoints(workloadComponent(k.typ, "shop", extra))
				if err != nil || len(eps) != 0 {
					t.Errorf("%s: endpoints = %+v, err = %v; want none", name, eps, err)
				}
			}
		})
	}
}

// A job and a cronjob accept `ports` but serve nothing: they report none.
func TestWorkloadEndpoints_BatchKindsReportNone(t *testing.T) {
	for _, typ := range []string{"job", "cronjob"} {
		t.Run(typ, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent("job", &components.JobHandler{})
			tr.RegisterComponent("cronjob", &components.CronjobHandler{})
			props := map[string]any{"ports": []any{portEntry(8080, "http", "")}}
			if typ == "cronjob" {
				props["schedule"] = "0 * * * *"
			}
			eps, err := tr.ComponentEndpoints(workloadComponent(typ, "batch", props))
			if err != nil || len(eps) != 0 {
				t.Errorf("endpoints = %+v, err = %v; want none", eps, err)
			}
		})
	}
}

// The type's own parse runs first: a bad or repeated port name, or a component the parse refuses
// for any other reason, has no endpoint and is refused in the parse's words, whether or not it
// declares ports.
func TestWorkloadEndpoints_RefusedInTheParsesWords(t *testing.T) {
	for _, k := range endpointWorkloads() {
		t.Run(k.typ, func(t *testing.T) {
			for name, extra := range map[string]map[string]any{
				"bad port name":      {"ports": []any{portEntry(80, "Http", "")}},
				"repeated port name": {"ports": []any{portEntry(80, "web", ""), portEntry(81, "web", "")}},
				"repeated port":      {"ports": []any{portEntry(80, "a", ""), portEntry(80, "b", "")}},
				"bad protocol":       {"ports": []any{portEntry(80, "web", "ICMP")}},
				"unknown property":   {"nonsense": true},
			} {
				comp := workloadComponent(k.typ, "shop", extra)
				_, buildErr := k.h.ToApplicationConfig(comp, "default")
				eps, err := k.h.Endpoints(comp)
				if name == "unknown property" {
					// Unknown top-level keys are the schema check's, not the parse's: both accept.
					if buildErr != nil || err != nil {
						t.Errorf("%s: ToApplicationConfig err = %v, Endpoints err = %v; want both to accept", name, buildErr, err)
					}
					continue
				}
				if buildErr == nil || err == nil || err.Error() != buildErr.Error() || eps != nil {
					t.Errorf("%s: ToApplicationConfig err = %v\nEndpoints = %+v, %v\nwant no endpoint and the parse's refusal", name, buildErr, eps, err)
				}
			}
			// A component refused for something other than ports.
			bad := &oam.Component{Name: "shop", Type: k.typ, Properties: map[string]any{}}
			_, buildErr := k.h.ToApplicationConfig(bad, "default")
			eps, err := k.h.Endpoints(bad)
			if buildErr == nil || err == nil || err.Error() != buildErr.Error() || eps != nil {
				t.Errorf("no image: ToApplicationConfig err = %v\nEndpoints = %+v, %v\nwant no endpoint and the parse's refusal", buildErr, eps, err)
			}
		})
	}
}

// The selector is the component's pod label, never the object name: with `objectName` set the
// endpoint still selects app=<component name>. Asked through the transformer, which gives the
// handler the object name it gives the build.
func TestWorkloadEndpoints_SelectorIgnoresObjectName(t *testing.T) {
	for _, k := range endpointWorkloads() {
		t.Run(k.typ, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent(k.typ, k.h)
			comp := workloadComponent(k.typ, "shop", map[string]any{
				"objectName": "shop-primary",
				"ports":      []any{portEntry(8080, "http", "")},
			})
			eps, err := tr.ComponentEndpoints(comp)
			if err != nil || len(eps) != 1 {
				t.Fatalf("endpoints = %+v, err = %v; want one", eps, err)
			}
			if got := eps[0].PodSelector.MatchLabels["app"]; got != "shop" {
				t.Errorf("selector app = %q, want shop", got)
			}
			if _, ok := comp.Properties["objectName"]; !ok {
				t.Error("the caller's component lost its objectName")
			}
		})
	}
}

// A consumer's Naming hook is asked for a workload's object name, as the transform asks it, and
// the answer does not reach the selector: the endpoint still selects app=<component name>.
func TestWorkloadEndpoints_NamingHookIsPure(t *testing.T) {
	for _, k := range endpointWorkloads() {
		t.Run(k.typ, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent(k.typ, k.h)
			var asked []oam.NameRequest
			hook := func(r oam.NameRequest) (string, bool) {
				asked = append(asked, r)
				return "hooked-" + r.Component, true
			}
			comp := workloadComponent(k.typ, "shop", map[string]any{"ports": []any{portEntry(8080, "http", "")}})
			eps, err := tr.ComponentEndpointsNamed("app", comp, hook)
			if err != nil || len(eps) != 1 {
				t.Fatalf("endpoints = %+v, err = %v; want one", eps, err)
			}
			if got := eps[0].PodSelector.MatchLabels["app"]; got != "shop" {
				t.Errorf("selector app = %q, want shop", got)
			}
			if len(asked) == 0 {
				t.Fatal("the hook was not asked for the object name")
			}
			for _, r := range asked {
				if r.Application != "app" || r.Component != "shop" {
					t.Errorf("hook asked %+v, want application app and component shop", r)
				}
			}
			if _, ok := comp.Properties["objectName"]; ok {
				t.Error("the caller's component was given an objectName")
			}
		})
	}
}

// A worker lowers into a deployment with no ports, so it reports none (go-kure/launcher#225).
func TestWorkloadEndpoints_LoweredWorkerReportsNone(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponentLowering(components.WorkerRule{})
	eps, err := tr.ComponentEndpoints(workloadComponent("worker", "jobs", nil))
	if err != nil || len(eps) != 0 {
		t.Errorf("endpoints = %+v, err = %v; want none", eps, err)
	}
}

// A direct Endpoints call on a component that still holds `objectName`, `labels` or `annotations`
// gives the answer a direct ToApplicationConfig call gives.
func TestWorkloadEndpoints_DirectCallOnAuthoredMetadata(t *testing.T) {
	for _, k := range endpointWorkloads() {
		for _, key := range []string{"objectName", "labels", "annotations"} {
			t.Run(k.typ+"/"+key, func(t *testing.T) {
				var value any = map[string]any{"team": "data"}
				if key == "objectName" {
					value = "other"
				}
				comp := workloadComponent(k.typ, "shop", map[string]any{key: value, "ports": []any{portEntry(8080, "http", "")}})
				_, buildErr := k.h.ToApplicationConfig(comp, "default")
				eps, err := k.h.Endpoints(comp)
				if (buildErr == nil) != (err == nil) || (err != nil && !strings.Contains(err.Error(), buildErr.Error())) {
					t.Fatalf("ToApplicationConfig err = %v, Endpoints err = %v; want the same answer", buildErr, err)
				}
				if err == nil && len(eps) != 1 {
					t.Errorf("endpoints = %+v, want one", eps)
				}
			})
		}
	}
}
