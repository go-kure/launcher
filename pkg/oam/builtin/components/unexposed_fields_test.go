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
			err := oam.NewTransformer(map[string]oam.ComponentHandler{tt.component: tt.handler}, nil).ValidateAuthoredProperties(app)
			if want := `unsupported field "` + tt.field + `"`; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one mentioning %s", err, want)
			}
		})
	}
}
