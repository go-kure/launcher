package oam

import (
	"strings"
	"testing"
)

// The tests below pin go-kure/launcher#1231: a ClusterProfile's spec.featureGates
// overrides the state a gate's release implies, in both directions, and a profile
// that sets none is judged exactly as before.

const profileWithGates = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
%s`

func parseProfileSpec(spec string) (*ClusterProfile, error) {
	return ParseClusterProfile([]byte(strings.Replace(profileWithGates, "%s", spec, 1)))
}

// TestClusterProfile_FeatureGates: the map parses, an unknown gate name is refused
// naming the gates launcher knows, and a map without spec.kubernetesVersion is
// refused, so the no-version path stays "nothing is checked".
func TestClusterProfile_FeatureGates(t *testing.T) {
	p, err := parseProfileSpec("  kubernetesVersion: \"1.34\"\n  featureGates:\n    EnvFiles: true\n    HostnameOverride: false\n")
	if err != nil {
		t.Fatalf("a valid featureGates map: %v", err)
	}
	if got := p.Spec.FeatureGates; len(got) != 2 || !got["EnvFiles"] || got["HostnameOverride"] {
		t.Errorf("FeatureGates = %v, want EnvFiles true and HostnameOverride false", got)
	}

	t.Run("unknown name", func(t *testing.T) {
		_, err := parseProfileSpec("  kubernetesVersion: \"1.34\"\n  featureGates:\n    EnvFile: true\n")
		if err == nil {
			t.Fatal("a gate name the table lacks must be refused")
		}
		for _, want := range []string{"spec.featureGates", `"EnvFile"`, "EnvFiles", "ContainerRestartRules"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})

	t.Run("without a kubernetesVersion", func(t *testing.T) {
		_, err := parseProfileSpec("  featureGates:\n    EnvFiles: true\n")
		if err == nil || !strings.Contains(err.Error(), "spec.featureGates needs spec.kubernetesVersion") {
			t.Fatalf("got %v, want the refusal \"spec.featureGates needs spec.kubernetesVersion\"", err)
		}
	})

	t.Run("an empty map is no map", func(t *testing.T) {
		if _, err := parseProfileSpec("  featureGates: {}\n"); err != nil {
			t.Errorf("an empty featureGates states nothing and needs no version, got %v", err)
		}
	})

	t.Run("a misspelt field is still refused", func(t *testing.T) {
		if _, err := parseProfileSpec("  kubernetesVersion: \"1.34\"\n  featureGate:\n    EnvFiles: true\n"); err == nil {
			t.Error("strict parsing must still refuse featureGate")
		}
	})

	t.Run("a value that is not a bool", func(t *testing.T) {
		if _, err := parseProfileSpec("  kubernetesVersion: \"1.34\"\n  featureGates:\n    EnvFiles: maybe\n"); err == nil {
			t.Error("a gate set to a non-bool must be refused")
		}
	})
}

// TestEvaluateProfile_KeepsFeatureGates: evaluating a profile returns its
// featureGates, so the build reads them from the evaluated profile as it reads the
// version.
func TestEvaluateProfile_KeepsFeatureGates(t *testing.T) {
	p, err := parseProfileSpec("  kubernetesVersion: \"1.34\"\n  featureGates:\n    EnvFiles: true\n  capabilities:\n    nothing: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewTransformer(nil, nil).EvaluateProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Spec.FeatureGates["EnvFiles"] {
		t.Errorf("evaluated FeatureGates = %v, want EnvFiles true", got.Spec.FeatureGates)
	}
}

// TestValidateAuthoredPropertiesForTargetWithGates: an override wins over the
// release in both directions, for a gated property and for a gated value, and no
// override is the existing check.
func TestValidateAuthoredPropertiesForTargetWithGates(t *testing.T) {
	gatedApp := func() *Application {
		app := makeApp("gates")
		app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "gated-policy", Properties: map[string]any{"flag": "x"}}}
		return app
	}
	gatedTransformer := func(gate string) *Transformer {
		tr := NewTransformer(nil, nil)
		tr.RegisterPolicy("gated-policy", gatedPolicyHandler{gate: gate})
		return tr
	}
	valueApp := func() *Application {
		app := makeApp("gates")
		app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "value-gated-policy", Properties: map[string]any{"flag": "on"}}}
		return app
	}
	valueTransformer := func(gate string) *Transformer {
		tr := NewTransformer(nil, nil)
		tr.RegisterPolicy("value-gated-policy", valueGatedPolicyHandler{gate: gate})
		return tr
	}
	v134, v136 := &KubernetesVersion{1, 34}, &KubernetesVersion{1, 36}
	on, off := map[string]bool{"EnvFiles": true}, map[string]bool{"EnvFiles": false}

	t.Run("override on, below the release", func(t *testing.T) {
		if _, err := gatedTransformer("EnvFiles").ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, v134, on); err != nil {
			t.Errorf("a gate set on must accept the field at 1.34, got %v", err)
		}
	})
	t.Run("override off, above the release", func(t *testing.T) {
		_, err := gatedTransformer("EnvFiles").ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, v136, off)
		if err == nil {
			t.Fatal("a gate set off must refuse the field at 1.36")
		}
		for _, want := range []string{`policy "p"`, ".flag", "EnvFiles", "spec.featureGates", "false"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "kubernetesVersion is 1.36") {
			t.Errorf("error %q blames the version, but the override is the cause", err)
		}
	})
	t.Run("an override of another gate changes nothing", func(t *testing.T) {
		_, err := gatedTransformer("EnvFiles").ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, v134, map[string]bool{"HostnameOverride": true})
		if err == nil || !strings.Contains(err.Error(), "kubernetesVersion is 1.34") {
			t.Errorf("got %v, want the unchanged version refusal", err)
		}
	})
	t.Run("no override is the existing check, byte for byte", func(t *testing.T) {
		tr := gatedTransformer("EnvFiles")
		_, want := tr.ValidateAuthoredPropertiesForTarget(gatedApp(), nil, v134)
		_, got := tr.ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, v134, nil)
		if want == nil || got == nil || got.Error() != want.Error() {
			t.Errorf("with nil gates: got %v, want %v", got, want)
		}
		_, got = tr.ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, v134, map[string]bool{})
		if got == nil || got.Error() != want.Error() {
			t.Errorf("with empty gates: got %v, want %v", got, want)
		}
	})
	t.Run("a gated value", func(t *testing.T) {
		tr := valueTransformer("EnvFiles")
		if _, err := tr.ValidateAuthoredPropertiesForTargetWithGates(valueApp(), nil, v134, on); err != nil {
			t.Errorf("a value gate set on must accept the value at 1.34, got %v", err)
		}
		_, err := tr.ValidateAuthoredPropertiesForTargetWithGates(valueApp(), nil, v136, off)
		if err == nil || !strings.Contains(err.Error(), `value "on"`) || !strings.Contains(err.Error(), "spec.featureGates") {
			t.Errorf("a value gate set off must refuse the value naming spec.featureGates, got %v", err)
		}
	})
	t.Run("no version, nothing is checked", func(t *testing.T) {
		if _, err := gatedTransformer("EnvFiles").ValidateAuthoredPropertiesForTargetWithGates(gatedApp(), nil, nil, off); err != nil {
			t.Errorf("no version stated: nothing is consulted, got %v", err)
		}
	})
}

// TestGateTarget_Enabled holds the unexported carrier the checks read: an
// override wins in both directions, and its refusal names the override and not the
// version.
func TestGateTarget_Enabled(t *testing.T) {
	for _, c := range []struct {
		name string
		t    gateTarget
		want bool
	}{
		{"table, below", gateTarget{version: KubernetesVersion{1, 34}}, false},
		{"table, at", gateTarget{version: KubernetesVersion{1, 35}}, true},
		{"override on below", gateTarget{version: KubernetesVersion{1, 34}, overrides: map[string]bool{"EnvFiles": true}}, true},
		{"override off above", gateTarget{version: KubernetesVersion{1, 36}, overrides: map[string]bool{"EnvFiles": false}}, false},
	} {
		got, err := c.t.enabled("EnvFiles")
		if err != nil || got != c.want {
			t.Errorf("%s: enabled(EnvFiles) = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}
