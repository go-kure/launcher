package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// go-kure/launcher#1265: prometheus-operator v0.95.0 and metallb v0.16.2 added
// fields that no kind publishes yet. Each is listed as excluded in
// coreKindSchemas (and as unfixtured in TestPolicyFreeKinds_FullCoversEveryField)
// with that issue as its reason. "Excluded" must be true: the schema leaves
// the key out, and the check of the authored properties, which kurel build runs
// first, refuses it as an unsupported field.
// TestNewlyRequiredKeys_AreRefusedWhenOmitted holds each key the API began to
// require with the kure v0.2.0-beta.25 pin (go-kure/launcher#1263): the kind
// refuses the object that leaves it out, and builds the object that
// carries it. preferredNodeSelectors is called on the handler, as the document
// check refuses it first (go-kure/launcher#1265).
func TestNewlyRequiredKeys_AreRefusedWhenOmitted(t *testing.T) {
	expression := map[string]any{"key": "tier", "operator": "In", "values": []any{"a"}}
	node := func(entry map[string]any) map[string]any {
		return map[string]any{"preferredNodeSelectors": []any{entry}}
	}
	for _, tt := range []struct {
		name      string
		component string
		handler   oam.ComponentHandler
		props     map[string]any
		want      string
	}{
		{"telegram.botToken.key", "alertmanager", &components.AlertmanagerHandler{},
			map[string]any{"alertmanagerConfiguration": map[string]any{"global": map[string]any{"telegram": map[string]any{
				"botToken": map[string]any{"name": "telegram"}}}}},
			"alertmanagerConfiguration.global.telegram.botToken.key"},
		{"preferredNodeSelectors[].preference", "metallb-l2advertisement", &components.MetalLBL2AdvertisementHandler{},
			node(map[string]any{"weight": 1}), "preferredNodeSelectors[0].preference"},
		{"preferredNodeSelectors[].preference.matchExpressions[].key", "metallb-l2advertisement", &components.MetalLBL2AdvertisementHandler{},
			node(map[string]any{"weight": 1, "preference": map[string]any{"matchExpressions": []any{map[string]any{"operator": "In", "values": []any{"a"}}}}}),
			"preferredNodeSelectors[0].preference.matchExpressions[0].key"},
		{"preferredNodeSelectors[].preference.matchExpressions[].operator", "metallb-l2advertisement", &components.MetalLBL2AdvertisementHandler{},
			node(map[string]any{"weight": 1, "preference": map[string]any{"matchExpressions": []any{map[string]any{"key": "tier"}}}}),
			"preferredNodeSelectors[0].preference.matchExpressions[0].operator"},
		{"preferredNodeSelectors[].weight", "metallb-l2advertisement", &components.MetalLBL2AdvertisementHandler{},
			node(map[string]any{"preference": map[string]any{"matchExpressions": []any{expression}}}), "preferredNodeSelectors[0].weight"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := coreKindErr(tt.handler, tt.component, "main", tt.props); err == nil || !strings.Contains(err.Error(), tt.want+": required") {
				t.Errorf("err = %v, want one saying %s: required", err, tt.want)
			}
		})
	}
	// The control: the same entries with every key build.
	full := node(map[string]any{"weight": 1, "preference": map[string]any{"matchExpressions": []any{expression}}})
	if err := coreKindErr(&components.MetalLBL2AdvertisementHandler{}, "metallb-l2advertisement", "main", full); err != nil {
		t.Errorf("an entry with weight, key and operator: %v, want it built", err)
	}
	telegram := map[string]any{"alertmanagerConfiguration": map[string]any{"global": map[string]any{"telegram": map[string]any{
		"botToken": map[string]any{"name": "telegram", "key": "token"}}}}}
	if err := coreKindErr(&components.AlertmanagerHandler{}, "alertmanager", "main", telegram); err != nil {
		t.Errorf("a botToken with name and key: %v, want it built", err)
	}
}

func TestUnexposedUpstreamFields_AreRefusedAsUnsupported(t *testing.T) {
	for _, tt := range []struct {
		component string
		handler   oam.ComponentHandler
		field     string
		value     any
	}{
		{"prometheus", &components.PrometheusHandler{}, "extraScrapeMetrics", true},
		{"podmonitor", &components.PodMonitorHandler{}, "extraScrapeMetrics", true},
		{"servicemonitor", &components.ServiceMonitorHandler{}, "extraScrapeMetrics", true},
		{"prometheus-probe", &components.PrometheusProbeHandler{}, "extraScrapeMetrics", true},
		{"alertmanager", &components.AlertmanagerHandler{}, "alertmanagerConfigFileSystemMode", "0640"},
		{"metallb-bgpadvertisement", &components.MetalLBBGPAdvertisementHandler{}, "asPathPrepend", 65001},
		{"metallb-l2advertisement", &components.MetalLBL2AdvertisementHandler{}, "preferredNodeSelectors", []any{}},
	} {
		t.Run(tt.component+"/"+tt.field, func(t *testing.T) {
			type schemaHandler interface {
				PropertySchema() map[string]oam.PropertySchema
			}
			if _, published := tt.handler.(schemaHandler).PropertySchema()[tt.field]; published {
				t.Fatalf("the %s schema publishes %q, which coreKindSchemas lists as excluded", tt.component, tt.field)
			}
			app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{{
				Name: "main", Type: tt.component, Properties: map[string]any{tt.field: tt.value},
			}}}}
			_, err := oam.NewTransformer(map[string]oam.ComponentHandler{tt.component: tt.handler}, nil).ValidateAuthoredProperties(app)
			if want := `unsupported field "` + tt.field + `"`; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one mentioning %s", err, want)
			}
		})
	}
}
