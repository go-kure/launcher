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
// "1api" built a Service the cluster rejects on apply. statefulset's serviceName names a Service
// too, authored beside it, and is held to the same rule. Since go-kure/launcher#690 daemonset and
// statefulset emit no Service, so their component names are not constrained.

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
	{"webservice", webserviceViaRule{}, func() map[string]any {
		return map[string]any{"image": "ghcr.io/org/app:v1"}
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
// names a Service after it.
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

// Generate names webservice's Service after the Application, which a library caller builds
// itself, so it applies the same rule to the name it actually emits.
func TestWorkloadConfigs_GenerateRejectsInvalidServiceName(t *testing.T) {
	for _, tc := range serviceEmittingCases {
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

// daemonset and statefulset emit no Service (go-kure/launcher#690), so their names are not held
// to the Service-name rule, with or without container ports. "1api" is a valid container name (a
// DNS-1123 label) but not a valid Service name.
func TestWorkloadKinds_NoService_AcceptsNonServiceName(t *testing.T) {
	ports := []any{map[string]any{"name": "http", "containerPort": 9090}}
	for _, tc := range []struct {
		typ     string
		handler oam.ComponentHandler
		props   map[string]any
	}{
		{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
		{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "ports": ports}},
		{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
		{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "ports": ports}},
	} {
		objs := buildComponent(t, tc.handler, "1api", tc.typ, tc.props)
		if svcs := servicesIn(objs); len(svcs) != 0 {
			t.Errorf("%s %v: Services = %v, want none", tc.typ, tc.props, svcs)
		}
	}
}

// An authored serviceName names the governing Service, so it is held to the same rule and the
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

// serviceName is projected as authored and decouples nothing from the component name, which no
// longer names a Service: a component name that is not a valid Service name builds, a serviceName
// at the limit builds, and neither emits a Service.
func TestStatefulsetHandler_AuthoredServiceNameProjected(t *testing.T) {
	longest := longestServiceName(t)
	for _, tc := range []struct{ component, serviceName string }{
		{"1api", "db"},
		{"db", longest},
	} {
		objs := buildComponent(t, &components.StatefulsetHandler{}, tc.component, "statefulset", map[string]any{
			"image":       "ghcr.io/org/app:v1",
			"serviceName": tc.serviceName,
		})
		if svcs := servicesIn(objs); len(svcs) != 0 {
			t.Errorf("%s: Services = %v, want none", tc.component, svcs)
		}
		if got := statefulSetServiceName(t, objs); got != tc.serviceName {
			t.Errorf("%s: StatefulSet serviceName = %q, want %q", tc.component, got, tc.serviceName)
		}
	}
}

// serviceName has no default (go-kure/launcher#690): unset, spec.serviceName stays empty, for a
// named and a nameless (library-converted) config alike.
func TestStatefulsetHandler_NoServiceName_LeavesItEmpty(t *testing.T) {
	for _, name := range []string{"db", ""} {
		cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
			Name: name, Type: "statefulset", Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
		}, "default")
		if err != nil {
			t.Fatalf("%q: ToApplicationConfig: %v", name, err)
		}
		objs, err := cfg.Generate(stack.NewApplication("db", "default", cfg))
		if err != nil {
			t.Fatalf("%q: Generate: %v", name, err)
		}
		if got := statefulSetServiceName(t, objs); got != "" {
			t.Errorf("%q: StatefulSet serviceName = %q, want empty", name, got)
		}
	}
}

func statefulSetServiceName(t *testing.T, objs []*client.Object) string {
	t.Helper()
	for _, o := range objs {
		if sts, ok := (*o).(*appsv1.StatefulSet); ok {
			return sts.Spec.ServiceName
		}
	}
	t.Fatal("no StatefulSet emitted")
	return ""
}

// Generate projects the config's ServiceName, which a library caller may set itself, so it applies
// the rule to a non-empty one too.
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
