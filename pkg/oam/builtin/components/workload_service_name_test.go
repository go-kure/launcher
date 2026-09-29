package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// go-kure/launcher#546: every workload kind that emits a Service holds the Service's name to the
// rule the API server applies to it, a DNS-1035 label, as the service kind already does. A
// component name only has to be a DNS-1123 subdomain, so without this check a name such as
// "1api" (and, for statefulset, any authored serviceName) built a Service the cluster rejects on
// apply.

// invalidServiceNames are valid component names (DNS-1123 subdomains) that are not valid Service
// names.
var invalidServiceNames = []struct {
	name  string
	value string
}{
	{"dot", "api.v1"},
	{"longer than 63 characters", strings.Repeat("a", 64)},
	{"leading digit", "1api"},
}

// longestServiceName is a DNS-1035 label at the 63-character limit.
func longestServiceName(t *testing.T) string {
	t.Helper()
	name := "a" + strings.Repeat("b-", 30) + "cd"
	if len(name) != 63 {
		t.Fatalf("test name is %d characters, want 63", len(name))
	}
	return name
}

// serviceEmittingCases are the workload kinds that name a Service after the component, with the
// minimal properties under which they emit it.
var serviceEmittingCases = []struct {
	typ     string
	handler oam.ComponentHandler
	props   func() map[string]any
}{
	{"webservice", &components.WebserviceHandler{}, func() map[string]any {
		return map[string]any{"image": "ghcr.io/org/app:v1"}
	}},
	{"daemonset", &components.DaemonsetHandler{}, func() map[string]any {
		return map[string]any{"image": "ghcr.io/org/app:v1", "port": 9090}
	}},
	{"statefulset", &components.StatefulsetHandler{}, func() map[string]any {
		return map[string]any{"image": "ghcr.io/org/app:v1", "port": 5432}
	}},
}

func wantServiceNameError(t *testing.T, err error, field, value string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %s: %q refused as an invalid Service name", field, value)
	}
	for _, want := range []string{field + `: "` + value + `"`, "is not a valid Service name", "DNS-1035 label"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// buildComponent runs a component through ToApplicationConfig and Generate under its own name,
// the way the transformer does.
func buildComponent(t *testing.T, h oam.ComponentHandler, name, typ string, props map[string]any) []*client.Object {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig(%q): %v", typ, name, err)
	}
	objs, err := cfg.Generate(stack.NewApplication(name, "default", cfg))
	if err != nil {
		t.Fatalf("%s: Generate(%q): %v", typ, name, err)
	}
	return objs
}

func servicesIn(objs []*client.Object) []*corev1.Service {
	var out []*corev1.Service
	for _, o := range objs {
		if svc, ok := (*o).(*corev1.Service); ok {
			out = append(out, svc)
		}
	}
	return out
}

// A component name that is not a valid Service name is refused at conversion by every kind that
// names a Service after it (statefulset through its defaulted serviceName).
func TestWorkloadHandlers_RejectInvalidServiceName(t *testing.T) {
	for _, tc := range serviceEmittingCases {
		for _, n := range invalidServiceNames {
			t.Run(tc.typ+"/"+n.name, func(t *testing.T) {
				_, err := tc.handler.ToApplicationConfig(
					&oam.Component{Name: n.value, Type: tc.typ, Properties: tc.props()}, "default")
				wantServiceNameError(t, err, "name", n.value)
			})
		}
	}
}

// A name at the DNS-1035 limit still builds, and is the emitted Service's name.
func TestWorkloadHandlers_AcceptLongestValidServiceName(t *testing.T) {
	name := longestServiceName(t)
	for _, tc := range serviceEmittingCases {
		t.Run(tc.typ, func(t *testing.T) {
			svcs := servicesIn(buildComponent(t, tc.handler, name, tc.typ, tc.props()))
			if len(svcs) != 1 || svcs[0].Name != name {
				t.Fatalf("Services = %v, want exactly one named %q", svcs, name)
			}
		})
	}
}

// Generate names webservice's and daemonset's Service after the Application, which a library
// caller builds itself, so it applies the same rule to the name it actually emits.
func TestWorkloadConfigs_GenerateRejectsInvalidServiceName(t *testing.T) {
	for _, tc := range serviceEmittingCases {
		if tc.typ == "statefulset" {
			continue // names its Service by ServiceName, not the Application; covered below
		}
		t.Run(tc.typ, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(
				&oam.Component{Name: "api", Type: tc.typ, Properties: tc.props()}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			// "1api" passes the main container's DNS-1123 label check, so only the Service-name
			// rule can refuse it.
			_, err = cfg.Generate(stack.NewApplication("1api", "default", cfg))
			wantServiceNameError(t, err, "name", "1api")
		})
	}
}

// A daemonset without a port emits no Service, so its name is not held to the Service-name rule.
// "1api" is a valid container name (a DNS-1123 label) but not a valid Service name.
func TestDaemonsetHandler_NoPort_AcceptsNonServiceName(t *testing.T) {
	for _, props := range []map[string]any{
		{"image": "ghcr.io/org/app:v1"},
		{"image": "ghcr.io/org/app:v1", "port": 0},
	} {
		objs := buildComponent(t, &components.DaemonsetHandler{}, "1api", "daemonset", props)
		if svcs := servicesIn(objs); len(svcs) != 0 {
			t.Errorf("props %v: Services = %v, want none", props, svcs)
		}
	}
}

// An authored serviceName is the headless Service's name, so it is held to the same rule and the
// error names the property.
func TestStatefulsetHandler_RejectsInvalidAuthoredServiceName(t *testing.T) {
	h := &components.StatefulsetHandler{}
	for _, n := range invalidServiceNames {
		t.Run(n.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{Name: "db", Type: "statefulset", Properties: map[string]any{
				"image":       "ghcr.io/org/app:v1",
				"serviceName": n.value,
			}}, "default")
			wantServiceNameError(t, err, "serviceName", n.value)
		})
	}
}

// Only the Service's name is constrained: a component name that is not a valid Service name
// builds once serviceName names the Service validly, and a serviceName at the limit builds.
func TestStatefulsetHandler_AuthoredServiceNameDecouplesComponentName(t *testing.T) {
	longest := longestServiceName(t)
	for _, tc := range []struct{ component, serviceName string }{
		{"1api", "db"},
		{"db", longest},
	} {
		objs := buildComponent(t, &components.StatefulsetHandler{}, tc.component, "statefulset", map[string]any{
			"image":       "ghcr.io/org/app:v1",
			"serviceName": tc.serviceName,
		})
		svcs := servicesIn(objs)
		if len(svcs) != 1 || svcs[0].Name != tc.serviceName {
			t.Fatalf("%s: Services = %v, want exactly one named %q", tc.component, svcs, tc.serviceName)
		}
		for _, o := range objs {
			if sts, ok := (*o).(*appsv1.StatefulSet); ok && sts.Spec.ServiceName != tc.serviceName {
				t.Errorf("%s: StatefulSet serviceName = %q, want %q", tc.component, sts.Spec.ServiceName, tc.serviceName)
			}
		}
	}
}

// A nameless config (converted without a component name, as a library caller may) is not refused
// at conversion: webservice and daemonset name their Service after the Application at Generate,
// which checks that name instead.
func TestWorkloadHandlers_NamelessConfigServiceNamedAfterApplication(t *testing.T) {
	for _, tc := range serviceEmittingCases {
		if tc.typ == "statefulset" {
			continue // names its Service by ServiceName; see the test below
		}
		t.Run(tc.typ, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(
				&oam.Component{Name: "", Type: tc.typ, Properties: tc.props()}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			objs, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if svcs := servicesIn(objs); len(svcs) != 1 || svcs[0].Name != "app" {
				t.Errorf("Services = %v, want exactly one named \"app\"", svcs)
			}
		})
	}
}

// A nameless statefulset without serviceName has an empty ServiceName, which used to emit a
// Service with no name; Generate refuses it instead.
func TestStatefulsetConfig_NamelessWithoutServiceNameRefused(t *testing.T) {
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
		Name: "", Type: "statefulset", Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
	wantServiceNameError(t, err, "serviceName", "")
}

// Generate names the headless Service by the config's ServiceName, which a library caller may set
// itself, so it applies the rule to that name too.
func TestStatefulsetConfig_GenerateRejectsInvalidServiceName(t *testing.T) {
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
		Name: "db", Type: "statefulset", Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	sc, ok := cfg.(*components.StatefulsetConfig)
	if !ok {
		t.Fatalf("expected *StatefulsetConfig, got %T", cfg)
	}
	sc.ServiceName = "api.v1"
	_, err = cfg.Generate(stack.NewApplication("db", "default", cfg))
	wantServiceNameError(t, err, "serviceName", "api.v1")
}
