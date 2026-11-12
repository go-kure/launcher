package kurel

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The tests below pin go-kure/launcher#1231 through `kurel build` and the library
// route: a profile's spec.featureGates overrides what its kubernetesVersion implies,
// for the authored-property check and for the Service name check on the generated
// objects.

// TestBuildCommand_FeatureGates: a gate set on lets a build accept a field the
// release would refuse, a gate set off refuses a field the release would accept,
// and what an accepted document renders to does not depend on the override.
func TestBuildCommand_FeatureGates(t *testing.T) {
	const props = `
        hostnameOverride: web.example.com
`
	doc := gateAppYAML("deployment", props)
	build := func(spec string) ([]map[string]any, error) {
		return buildWithProfile(t, doc, profileWithSpec(spec))
	}

	// HostnameOverride is on by default from 1.35.
	if _, err := build("\n  kubernetesVersion: \"1.34\"\n"); err == nil {
		t.Fatal("1.34 without an override must refuse hostnameOverride")
	}
	base, err := build("\n  kubernetesVersion: \"1.35\"\n")
	if err != nil {
		t.Fatalf("1.35: %v", err)
	}

	got, err := build("\n  kubernetesVersion: \"1.34\"\n  featureGates:\n    HostnameOverride: true\n")
	if err != nil {
		t.Fatalf("1.34 with HostnameOverride set on must build, got %v", err)
	}
	if !reflect.DeepEqual(got, base) {
		t.Errorf("an override changed what the document renders to:\n got  %v\n base %v", summarize(got), summarize(base))
	}

	_, err = build("\n  kubernetesVersion: \"1.36\"\n  featureGates:\n    HostnameOverride: false\n")
	if err == nil {
		t.Fatal("1.36 with HostnameOverride set off must refuse hostnameOverride")
	}
	for _, want := range []string{"HostnameOverride", "spec.featureGates"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	// An override of a gate this document does not use changes nothing.
	if _, err := build("\n  kubernetesVersion: \"1.34\"\n  featureGates:\n    EnvFiles: true\n"); err == nil {
		t.Error("an override of another gate must leave the 1.34 refusal in place")
	}

	// A profile that sets the map without a version is refused at the parse.
	_, err = build("\n  featureGates:\n    HostnameOverride: true\n")
	if err == nil || !strings.Contains(err.Error(), "spec.featureGates needs spec.kubernetesVersion") {
		t.Errorf("got %v, want the refusal naming both fields", err)
	}
}

// TestServiceNameGate_FeatureGateOverride: RelaxedServiceNameValidation, which no
// schema names and code checks on the generated objects, follows the override too,
// carried by TransformContext.FeatureGates.
func TestServiceNameGate_FeatureGateOverride(t *testing.T) {
	doc := serviceNameHookDoc(t)
	hook := renameBy(map[string]string{"workload-service web": "1web"})
	v135 := oam.KubernetesVersion{Major: 1, Minor: 35}
	v136 := oam.KubernetesVersion{Major: 1, Minor: 36}
	gate := "RelaxedServiceNameValidation"

	if err := generateForTarget(t, doc, oam.TransformContext{Naming: hook, TargetVersion: &v135, FeatureGates: map[string]bool{gate: true}}); err != nil {
		t.Errorf("1.35 with the gate set on must accept the digit-first name, got %v", err)
	}
	err := generateForTarget(t, doc, oam.TransformContext{Naming: hook, TargetVersion: &v136, FeatureGates: map[string]bool{gate: false}})
	if err == nil {
		t.Fatal("1.36 with the gate set off must refuse the digit-first name")
	}
	for _, want := range []string{`Service metadata.name "1web"`, gate, "spec.featureGates"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	// Without a target nothing is checked, overrides or not.
	if err := generateForTarget(t, doc, oam.TransformContext{Naming: hook, FeatureGates: map[string]bool{gate: false}}); err != nil {
		t.Errorf("no TargetVersion: %v", err)
	}
}
