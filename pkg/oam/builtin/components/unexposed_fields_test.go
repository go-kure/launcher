package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestNewlyRequiredKeys_AreRefusedWhenOmitted holds each key the API began to
// require with the kure v0.2.0-beta.25 pin (go-kure/launcher#1263): the kind
// refuses the object that leaves it out, and builds the object that
// carries it.
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

// TestAlertmanager_ConfigFileSystemModeAllowAllIsRefused holds the one value of
// alertmanagerConfigFileSystemMode the kind does not author (go-kure/launcher#1300):
// AllowAll lets every AlertmanagerConfig object read the files of the alertmanager
// container. The error names the field and the reason. The other two values build.
func TestAlertmanager_ConfigFileSystemModeAllowAllIsRefused(t *testing.T) {
	mode := func(value string) map[string]any {
		return map[string]any{"alertmanagerConfigFileSystemMode": value}
	}
	err := coreKindErr(&components.AlertmanagerHandler{}, "alertmanager", "main", mode("AllowAll"))
	for _, want := range []string{"alertmanagerConfigFileSystemMode", "AllowAll", "not authorable", "DenyAll"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one mentioning %q", err, want)
		}
	}
	for _, value := range []string{"DenyAll", "FromAlertmanagerConfigMatcherStrategy"} {
		if err := coreKindErr(&components.AlertmanagerHandler{}, "alertmanager", "main", mode(value)); err != nil {
			t.Errorf("mode %s: %v, want it built", value, err)
		}
	}
}
