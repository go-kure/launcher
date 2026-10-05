package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin helmReleaseName on the helm rule driven directly
// (go-kure/launcher#787): it names the HelmRelease object, the helmrelease
// member keeps the component's name, and nothing the member carries changes.

func helmReleaseProps(extra map[string]any) map[string]any {
	props := map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com"}}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

func TestHelmRule_HelmReleaseName(t *testing.T) {
	plain := componentByType(t, lowerHelm(t, helmLowering("shop"), "web", helmReleaseProps(nil)), "helmrelease")
	if plain.Name != "web" || plain.ObjectName() != "web" {
		t.Fatalf("without helmReleaseName the member is %q with object %q, want web for both", plain.Name, plain.ObjectName())
	}

	for _, tt := range []struct {
		name  string
		extra map[string]any
	}{
		{"alone", map[string]any{"helmReleaseName": "shop-web"}},
		{"beside releaseName", map[string]any{"helmReleaseName": "shop-web", "releaseName": "podinfo"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := componentByType(t, lowerHelm(t, helmLowering("shop"), "web", helmReleaseProps(tt.extra)), "helmrelease")
			if release.Name != "web" {
				t.Errorf("the member is named %q, want it to keep the component's name", release.Name)
			}
			if got := release.ObjectName(); got != "shop-web" {
				t.Errorf("the HelmRelease is named %q, want shop-web", got)
			}
			// The property reaches no member, and the release name is the authored
			// one or left to the terminal's default, taken from the member's name.
			want := map[string]any{}
			for k, v := range plain.Properties {
				want[k] = v
			}
			if name, ok := tt.extra["releaseName"]; ok {
				want["releaseName"] = name
			}
			if !reflect.DeepEqual(release.Properties, want) {
				t.Errorf("the member's properties are %v, want %v", release.Properties, want)
			}
		})
	}
}

func TestHelmRule_HelmReleaseNameRefusals(t *testing.T) {
	for _, tt := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"under delivery: template", map[string]any{"delivery": "template", "helmReleaseName": "shop-web"},
			"helm: delivery: template does not support helmReleaseName (the chart is rendered at build time, so no HelmRelease is generated; releaseName names the render's release)"},
		{"no string", map[string]any{"helmReleaseName": 5}, "helmReleaseName"},
		{"empty", map[string]any{"helmReleaseName": ""},
			`helm: naming the HelmRelease: helmReleaseName "" cannot be the name for role "helm-release": it is empty; write a valid name, or leave the property out for the default "web"`},
		{"no subdomain", map[string]any{"helmReleaseName": "Not_A_Name"},
			`helm: naming the HelmRelease: helmReleaseName "Not_A_Name" cannot be the name for role "helm-release": not a valid DNS-1123 subdomain: `},
	} {
		t.Run(tt.name, func(t *testing.T) {
			comp := &oam.Component{Name: "web", Type: "helm", Properties: helmReleaseProps(tt.extra)}
			_, err := components.HelmRule{}.LowerComponent(comp, helmLowering("shop"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tt.want)
			}
		})
	}
}

// The property is in the schema, so a document that writes it passes property
// validation, and the schema says which of the two release names is which.
func TestHelmRule_SchemaDeclaresHelmReleaseName(t *testing.T) {
	schema := components.HelmRule{}.PropertySchema()
	object, ok := schema["helmReleaseName"]
	if !ok {
		t.Fatal("the helm schema does not declare helmReleaseName")
	}
	if !strings.Contains(object.Description, "It is not the Helm release name") {
		t.Errorf("helmReleaseName's description does not tell it from releaseName: %q", object.Description)
	}
	if got := schema["releaseName"].Description; !strings.Contains(got, "helmReleaseName is the name of the HelmRelease object") {
		t.Errorf("releaseName's description does not tell it from helmReleaseName: %q", got)
	}
}
