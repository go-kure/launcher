package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Every workload handler, and the initContainers/sidecars entry parsers, read
// `resources` with a bare comma-ok type assertion, so a present value of the
// wrong type was indistinguishable from an absent key: `resources: "big"`
// converted cleanly and emitted a container with no requests or limits at all
// (go-kure/launcher#405). The same held one level down for `requests` and
// `limits`. An authored document is refused earlier, by schema validation; these
// cases call the handler directly, which is the path that used to drop the value.
//
// Each case is paired with its absent counterpart: before the fix both inputs
// produced the same config, so an absence-only test cannot tell the two apart.

// resourceKinds is every component kind that reads a top-level `resources`.
var resourceKinds = []struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
}{
	{"webservice", &components.WebserviceHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}},
	{"worker", &components.WorkerHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"deployment", &components.DeploymentHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"cronjob", &components.CronjobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "0 2 * * *"}},
	{"job", &components.JobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"postgresql", &components.PostgresqlHandler{}, map[string]any{}},
}

// wrongResources is one wrongly typed value per shape an author plausibly writes,
// each with the fragment the error must carry to name the offending field.
var wrongResources = []struct {
	name  string
	value any
	want  string
}{
	{"resources as a string", "big", "resources: must be an object, got string"},
	{"resources as a number", 3, "resources: must be an object, got int"},
	{"resources as a list", []any{"cpu"}, "resources: must be an object, got []interface {}"},
	{"requests as a string", map[string]any{"requests": "big"}, "resources.requests: must be an object, got string"},
	{"limits as a list", map[string]any{"limits": []any{"1"}}, "resources.limits: must be an object, got []interface {}"},
}

func withProp(base map[string]any, key string, value any) map[string]any {
	props := make(map[string]any, len(base)+1)
	for k, v := range base {
		props[k] = v
	}
	props[key] = value
	return props
}

func convert(h oam.ComponentHandler, kind string, props map[string]any) error {
	_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	return err
}

func TestResources_WrongTypeIsRejected(t *testing.T) {
	for _, k := range resourceKinds {
		for _, tc := range wrongResources {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				err := convert(k.handler, k.name, withProp(k.props, "resources", tc.value))
				if err == nil {
					t.Fatalf("resources=%#v converted without error; the authored requests and limits would be dropped", tc.value)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
				}
			})
		}
	}
}

func TestResources_AbsentOrNullIsAbsent(t *testing.T) {
	for _, k := range resourceKinds {
		t.Run(k.name+"/absent", func(t *testing.T) {
			if err := convert(k.handler, k.name, k.props); err != nil {
				t.Fatalf("absent resources: %v", err)
			}
		})
		t.Run(k.name+"/null", func(t *testing.T) {
			if err := convert(k.handler, k.name, withProp(k.props, "resources", nil)); err != nil {
				t.Fatalf("null resources: %v", err)
			}
		})
		t.Run(k.name+"/null requests", func(t *testing.T) {
			if err := convert(k.handler, k.name, withProp(k.props, "resources", map[string]any{"requests": nil})); err != nil {
				t.Fatalf("null resources.requests: %v", err)
			}
		})
	}
}

// containerEntries are the two per-entry parsers that read `resources` off the
// entry map rather than the component's properties, so they are reachable from
// every kind that accepts initContainers/sidecars, whatever its top-level
// resources says. webservice stands in for those kinds: the entry parsers are
// shared.
var containerEntries = []struct {
	key   string
	label string
}{
	{"initContainers", `initContainers[0] "setup"`},
	{"sidecars", `sidecars[0] "setup"`},
}

func TestContainerEntryResources_WrongTypeIsRejected(t *testing.T) {
	base := map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}
	for _, e := range containerEntries {
		for _, tc := range wrongResources {
			t.Run(e.key+"/"+tc.name, func(t *testing.T) {
				entry := map[string]any{"name": "setup", "image": "busybox:1", "resources": tc.value}
				err := convert(&components.WebserviceHandler{}, "webservice", withProp(base, e.key, []any{entry}))
				if err == nil {
					t.Fatalf("%s entry resources=%#v converted without error; the container would carry no requests or limits", e.key, tc.value)
				}
				want := e.label + ": " + tc.want
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), want)
				}
			})
		}
	}
}

func TestContainerEntryResources_WellFormedIsKept(t *testing.T) {
	base := map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}
	for _, e := range containerEntries {
		t.Run(e.key, func(t *testing.T) {
			entry := map[string]any{"name": "setup", "image": "busybox:1", "resources": map[string]any{
				"requests": map[string]any{"cpu": "100m"},
				"limits":   map[string]any{"memory": "64Mi"},
			}}
			cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(
				&oam.Component{Name: "app", Type: "webservice", Properties: withProp(base, e.key, []any{entry})}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			wc := cfg.(*components.WebserviceConfig)
			var r components.ResourceRequirements
			switch e.key {
			case "initContainers":
				r = wc.InitContainers[0].Resources
			default:
				r = wc.Sidecars[0].Resources
			}
			if got := r.Requests.Cpu().String(); got != "100m" {
				t.Errorf("requests.cpu = %s, want 100m", got)
			}
			if got := r.Limits.Memory().String(); got != "64Mi" {
				t.Errorf("limits.memory = %s, want 64Mi", got)
			}
		})
	}
}
