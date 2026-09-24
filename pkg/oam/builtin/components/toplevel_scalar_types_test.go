package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The go-kure/launcher#405 audit of top-level `props[...]` reads: each optional
// property below was read with a bare comma-ok assertion (or toInt32's ok), so
// a present value of the wrong type fell back to the default exactly as an
// omitted one would — `topologySpread: "false"` kept the spread constraints on,
// `port: "8080"` built port 80, `prune: "false"` pruned. Schema validation
// refuses these shapes for an authored document; these cases call the handler
// directly, the path that used to take the default.
//
// Each case targets one read, so reverting any one fails its own subtest.

var (
	ociBase = map[string]any{
		"source":  map[string]any{"url": "oci://registry.example.com/org/artifact"},
		"version": "1.2.3",
	}
	imageBase = map[string]any{"image": "ghcr.io/org/app:v1"}
	cronBase  = map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "0 2 * * *"}
)

var wrongTopLevel = []struct {
	kind    string
	handler oam.ComponentHandler
	base    map[string]any
	key     string
	value   any
	want    string
}{
	{"webservice", &components.WebserviceHandler{}, imageBase, "port", "8080", "port: must be an integer, got string"},
	{"daemonset", &components.DaemonsetHandler{}, imageBase, "port", "8080", "port: must be an integer, got string"},
	{"statefulset", &components.StatefulsetHandler{}, imageBase, "port", "8080", "port: must be an integer, got string"},
	{"webservice", &components.WebserviceHandler{}, imageBase, "topologySpread", "false", "topologySpread: must be a boolean, got string"},
	{"worker", &components.WorkerHandler{}, imageBase, "topologySpread", "false", "topologySpread: must be a boolean, got string"},
	{"cronjob", &components.CronjobHandler{}, cronBase, "restartPolicy", 1, "restartPolicy: must be a string, got int"},
	{"statefulset", &components.StatefulsetHandler{}, imageBase, "serviceName", 3, "serviceName: must be a string, got int"},
	{"oci", &components.OCIHandler{}, ociBase, "path", 3, "path: must be a string, got int"},
	{"oci", &components.OCIHandler{}, ociBase, "prune", "false", "prune: must be a boolean, got string"},
	{"oci", &components.OCIHandler{}, ociBase, "interval", 10, "interval: must be a string, got int"},
	{"oci", &components.OCIHandler{}, ociBase, "targetNamespace", true, "targetNamespace: must be a string, got bool"},
}

func TestTopLevelOptional_WrongTypeIsRejected(t *testing.T) {
	for _, tc := range wrongTopLevel {
		t.Run(tc.kind+"/"+tc.key, func(t *testing.T) {
			err := convert(tc.handler, tc.kind, withProp(tc.base, tc.key, tc.value))
			if err == nil {
				t.Fatalf("%s=%#v converted without error; the default would be built in its place", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// Absent and null still take the default, and the empty-string and enum
// behaviours the old reads had survive the conversion.
func TestTopLevelOptional_PreservedBehaviour(t *testing.T) {
	for _, tc := range wrongTopLevel {
		t.Run(tc.kind+"/"+tc.key+"/null", func(t *testing.T) {
			if err := convert(tc.handler, tc.kind, withProp(tc.base, tc.key, nil)); err != nil {
				t.Fatalf("null %s: %v", tc.key, err)
			}
		})
	}

	t.Run("webservice port defaults to 80", func(t *testing.T) {
		cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(
			&oam.Component{Name: "app", Type: "webservice", Properties: imageBase}, "default")
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.(*components.WebserviceConfig).Port; got != 80 {
			t.Errorf("Port = %d, want 80", got)
		}
	})
	t.Run("webservice port is read", func(t *testing.T) {
		cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(
			&oam.Component{Name: "app", Type: "webservice", Properties: withProp(imageBase, "port", 8080)}, "default")
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.(*components.WebserviceConfig).Port; got != 8080 {
			t.Errorf("Port = %d, want 8080", got)
		}
	})
	t.Run("statefulset empty serviceName falls back to the component name", func(t *testing.T) {
		cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
			&oam.Component{Name: "app", Type: "statefulset", Properties: withProp(imageBase, "serviceName", "")}, "default")
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.(*components.StatefulsetConfig).ServiceName; got != "app" {
			t.Errorf("ServiceName = %q, want the component name", got)
		}
	})
	t.Run("cronjob empty restartPolicy is still refused", func(t *testing.T) {
		err := convert(&components.CronjobHandler{}, "cronjob", withProp(cronBase, "restartPolicy", ""))
		if err == nil || !strings.Contains(err.Error(), "invalid restartPolicy") {
			t.Fatalf("error = %v, want the invalid restartPolicy refusal", err)
		}
	})
	t.Run("webservice topologySpread false disables spreading", func(t *testing.T) {
		cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(
			&oam.Component{Name: "app", Type: "webservice", Properties: withProp(imageBase, "topologySpread", false)}, "default")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.(*components.WebserviceConfig).TopologySpreadDisabled {
			t.Error("TopologySpreadDisabled = false, want true")
		}
	})
	t.Run("oci prune false and empty path", func(t *testing.T) {
		props := withProp(withProp(ociBase, "prune", false), "path", "")
		cfg, err := (&components.OCIHandler{}).ToApplicationConfig(
			&oam.Component{Name: "app", Type: "oci", Properties: props}, "default")
		if err != nil {
			t.Fatal(err)
		}
		oc := cfg.(*components.OCIConfig)
		if oc.Prune {
			t.Error("Prune = true, want false")
		}
		if oc.Path != "./" {
			t.Errorf("Path = %q, want the ./ default", oc.Path)
		}
	})
}

// webservice reads `port` twice: in ToApplicationConfig and again in Endpoints,
// which Transformer.ComponentEndpoints calls with no schema validation first.
// The second read fell back to 80 on a wrong type, declaring an endpoint on a
// port the rejected document never asked for; the two must agree.
func TestWebserviceEndpoints_Port(t *testing.T) {
	h := &components.WebserviceHandler{}
	endpointPort := func(props map[string]any) (int32, error) {
		eps, err := h.Endpoints(&oam.Component{Name: "app", Type: "webservice", Properties: props})
		if err != nil {
			return 0, err
		}
		return eps[0].Ports[0].IntVal, nil
	}

	if _, err := endpointPort(withProp(imageBase, "port", "8080")); err == nil ||
		!strings.Contains(err.Error(), "port: must be an integer, got string") {
		t.Errorf("wrong-typed port: error = %v, want the port refusal", err)
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  int32
	}{
		"absent defaults to 80": {imageBase, 80},
		"null defaults to 80":   {withProp(imageBase, "port", nil), 80},
		"authored is read":      {withProp(imageBase, "port", 8080), 8080},
	} {
		got, err := endpointPort(tc.props)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: port = %d, want %d", name, got, tc.want)
		}
	}
}

// An integer beyond int32 is the one port shape schema validation lets through
// (the schema declares `integer` with no maximum); toInt32's ok used to drop it
// for the default, and it must now be refused on each kind that reads `port`.
func TestPort_OutOfInt32RangeIsRejected(t *testing.T) {
	for _, k := range []struct {
		kind    string
		handler oam.ComponentHandler
	}{
		{"webservice", &components.WebserviceHandler{}},
		{"daemonset", &components.DaemonsetHandler{}},
		{"statefulset", &components.StatefulsetHandler{}},
	} {
		t.Run(k.kind, func(t *testing.T) {
			err := convert(k.handler, k.kind, withProp(imageBase, "port", 5000000000))
			if err == nil || !strings.Contains(err.Error(), "port: must be an integer") {
				t.Fatalf("error = %v, want the port refusal", err)
			}
		})
	}
}
