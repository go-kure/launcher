package oam

import (
	"slices"
	"strings"
	"testing"
)

// TestParseKubernetesVersion pins the accepted forms of spec.kubernetesVersion
// (go-kure/launcher#1183): major.minor, with a leading v and a patch part read
// and ignored, the forms `helm template --kube-version` takes.
func TestParseKubernetesVersion(t *testing.T) {
	good := map[string]KubernetesVersion{
		"1.35":    {1, 35},
		"v1.35":   {1, 35},
		"1.35.2":  {1, 35},
		"v1.34.0": {1, 34},
		"1.0":     {1, 0},
		"1.100":   {1, 100},
	}
	for in, want := range good {
		got, err := ParseKubernetesVersion(in)
		if err != nil {
			t.Errorf("ParseKubernetesVersion(%q): unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseKubernetesVersion(%q) = %v, want %v", in, got, want)
		}
	}
	bad := []string{
		"", "v", "1", "v1", "1.", "1.x", "x.1", "1.35.", "1.35.2.1",
		"1.34abc", "1.35-rc1", "1.35+build", "1.35.2-gke.1", " 1.35", "1.35 ",
		"1.+5", "1.-5", "01.35", "1.035", "vv1.35", "V1.35",
		"2.0", "0.35", "1.99999999999999999999",
	}
	for _, in := range bad {
		if v, err := ParseKubernetesVersion(in); err == nil {
			t.Errorf("ParseKubernetesVersion(%q) = %v, want an error", in, v)
		}
	}
}

func TestParseKubernetesVersion_MajorRefusalNamesTheMajor(t *testing.T) {
	_, err := ParseKubernetesVersion("2.1")
	if err == nil || !strings.Contains(err.Error(), "major version must be 1") {
		t.Fatalf("got %v, want a refusal naming the major version", err)
	}
}

func TestKubernetesVersion_AtLeastAndString(t *testing.T) {
	v := KubernetesVersion{1, 35}
	for _, c := range []struct {
		o    KubernetesVersion
		want bool
	}{
		{KubernetesVersion{1, 34}, true},
		{KubernetesVersion{1, 35}, true},
		{KubernetesVersion{1, 36}, false},
		{KubernetesVersion{2, 0}, false},
		{KubernetesVersion{0, 99}, true},
	} {
		if got := v.AtLeast(c.o); got != c.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", v, c.o, got, c.want)
		}
	}
	if got := v.String(); got != "1.35" {
		t.Errorf("String() = %q, want 1.35", got)
	}
}

// TestFeatureGateTable_RowsAreWellFormed: every row names itself once, has a
// real release and cites its source. The agreement with the built-in schemas is
// held in pkg/cmd/kurel (TestFeatureGates_TableAndSchemasAgree), which can see them.
func TestFeatureGateTable_RowsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range featureGates {
		if g.Name == "" || seen[g.Name] {
			t.Errorf("gate row %+v: empty or repeated name", g)
		}
		seen[g.Name] = true
		if g.OnByDefault.Major != 1 || g.OnByDefault.Minor < 1 {
			t.Errorf("gate %s: OnByDefault %v is not a Kubernetes 1.N release", g.Name, g.OnByDefault)
		}
		if !strings.HasPrefix(g.Source, "https://") {
			t.Errorf("gate %s: Source %q does not cite where the row was read", g.Name, g.Source)
		}
	}
	if got := FeatureGateNames(); len(got) != len(featureGates) {
		t.Errorf("FeatureGateNames() = %v, want %d names", got, len(featureGates))
	}
	if _, ok := FeatureGateOnByDefault("NoSuchGate"); ok {
		t.Error("FeatureGateOnByDefault reported a gate the table lacks")
	}
}

func TestGateEnabled(t *testing.T) {
	// EnvFiles is on by default from 1.35.
	for _, c := range []struct {
		v    KubernetesVersion
		want bool
	}{
		{KubernetesVersion{1, 33}, false},
		{KubernetesVersion{1, 34}, false},
		{KubernetesVersion{1, 35}, true},
		{KubernetesVersion{1, 36}, true},
		{KubernetesVersion{1, 99}, true},
	} {
		got, err := gateEnabled("EnvFiles", c.v, nil)
		if err != nil || got != c.want {
			t.Errorf("gateEnabled(EnvFiles, %v) = %v, %v; want %v", c.v, got, err, c.want)
		}
	}
	// An override wins over the table in both directions (go-kure/launcher#1231 will
	// feed it from the profile; nothing does yet).
	if on, err := gateEnabled("EnvFiles", KubernetesVersion{1, 30}, map[string]bool{"EnvFiles": true}); err != nil || !on {
		t.Errorf("override on: got %v, %v", on, err)
	}
	if on, err := gateEnabled("EnvFiles", KubernetesVersion{1, 40}, map[string]bool{"EnvFiles": false}); err != nil || on {
		t.Errorf("override off: got %v, %v", on, err)
	}
	// A gate the table lacks is a launcher defect, never a pass.
	if _, err := gateEnabled("NoSuchGate", KubernetesVersion{1, 40}, nil); err == nil {
		t.Error("an unknown gate must be an error")
	}
}

// TestFeatureGateTable_PinnedToUpstream pins every row of the lifecycle table to
// the version upstream turns the gate on by default. The versions below are
// literals, never read from the table under test: a row that moves by a release
// fails here. Source for every row: the page "Feature gates in Kubernetes v1.37"
// at https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/,
// read on 2026-11-05; each expected version is the first row with Default true.
func TestFeatureGateTable_PinnedToUpstream(t *testing.T) {
	for _, c := range []struct {
		gate string
		on   KubernetesVersion // first release with the gate on by default
	}{
		{"EnvFiles", KubernetesVersion{1, 35}},              // Alpha 1.34 (false), Beta 1.35 (true)
		{"ContainerRestartRules", KubernetesVersion{1, 35}}, // Alpha 1.34 (false), Beta 1.35 (true)
		{"PodLevelResources", KubernetesVersion{1, 34}},     // Alpha 1.32-1.33 (false), Beta 1.34 (true)
		{"HostnameOverride", KubernetesVersion{1, 35}},      // Alpha 1.34 (false), Beta 1.35-1.36 (true), Stable 1.37
		// Alpha 1.33 (false), Beta 1.34 (true), Stable 1.35; page source rows 1412-1414.
		{"PreferSameTrafficDistribution", KubernetesVersion{1, 34}},
		// Alpha 1.34-1.35 (Default false), Beta 1.36 (true), Stable 1.37; a Service name may
		// start with a digit only with it on (go-kure/launcher#1230 change 2).
		{"RelaxedServiceNameValidation", KubernetesVersion{1, 36}},
	} {
		below := KubernetesVersion{c.on.Major, c.on.Minor - 1}
		above := KubernetesVersion{c.on.Major, c.on.Minor + 1}
		for _, w := range []struct {
			v    KubernetesVersion
			want bool
		}{{below, false}, {c.on, true}, {above, true}} {
			got, err := gateEnabled(c.gate, w.v, nil)
			if err != nil || got != w.want {
				t.Errorf("gateEnabled(%s, %v) = %v, %v; want %v", c.gate, w.v, got, err, w.want)
			}
		}
	}
	// The pinned set is the table's set: a row added to the table needs a pin here.
	pinned := []string{"EnvFiles", "ContainerRestartRules", "PodLevelResources", "HostnameOverride", "PreferSameTrafficDistribution", "RelaxedServiceNameValidation"}
	got := FeatureGateNames()
	slices.Sort(got)
	slices.Sort(pinned)
	if !slices.Equal(got, pinned) {
		t.Errorf("FeatureGateNames() = %v, pinned = %v: pin every row of the table", got, pinned)
	}
}

const profileWithVersion = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  kubernetesVersion: %s
`

func TestClusterProfile_KubernetesVersion(t *testing.T) {
	parse := func(version string) (*ClusterProfile, error) {
		doc := strings.Replace(profileWithVersion, "%s", version, 1)
		return ParseClusterProfile([]byte(doc))
	}

	for in, want := range map[string]KubernetesVersion{
		`"1.35"`:    {1, 35},
		`"v1.34.2"`: {1, 34},
		`"1.99"`:    {1, 99},
	} {
		p, err := parse(in)
		if err != nil {
			t.Fatalf("kubernetesVersion %s: %v", in, err)
		}
		got, ok := p.Spec.TargetVersion()
		if !ok || got != want {
			t.Errorf("kubernetesVersion %s: TargetVersion() = %v, %v; want %v", in, got, ok, want)
		}
	}

	// An unquoted 1.30 is a YAML float, but it decodes into the string field as its
	// source text, so the trailing zero survives and no quoting is needed.
	if p, err := parse("1.30"); err != nil || p.Spec.KubernetesVersion != "1.30" {
		t.Errorf("unquoted 1.30: %v, %v", p, err)
	}

	for _, in := range []string{`"1"`, `"abc"`, `"2.0"`, `"1.34abc"`, `"v"`} {
		_, err := parse(in)
		if err == nil {
			t.Errorf("kubernetesVersion %s: want an error", in)
			continue
		}
		if !strings.Contains(err.Error(), "kubernetesVersion") {
			t.Errorf("kubernetesVersion %s: error %q does not name the field", in, err)
		}
	}

	// Absent is unknown, not an error, and not a zero version.
	p, err := ParseClusterProfile([]byte("apiVersion: launcher.gokure.dev/v1alpha1\nkind: ClusterProfile\nmetadata:\n  name: test-cluster\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Spec.TargetVersion(); ok {
		t.Errorf("absent kubernetesVersion read as %v", v)
	}

	// Strict parsing still refuses any other unknown spec field.
	if _, err := ParseClusterProfile([]byte(strings.Replace(profileWithVersion, "kubernetesVersion: %s", "kubernetesVersoin: \"1.35\"", 1))); err == nil {
		t.Error("a misspelt spec field must still be refused")
	}
}

// valueGatedPolicyHandler is a terminal policy handler whose one string property
// takes two values and gates only "on" (PropertySchema.ValueGates).
type valueGatedPolicyHandler struct{ gate string }

func (h valueGatedPolicyHandler) CanHandle(policyType string) bool {
	return policyType == "value-gated-policy"
}
func (h valueGatedPolicyHandler) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}
func (h valueGatedPolicyHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"flag": {
		Type:       PropertyTypeString,
		Enum:       []any{"on", "off"},
		ValueGates: map[string]string{"on": h.gate},
	}}
}

// TestValidateAuthoredPropertiesForTarget_ValueGates: a value named in
// PropertySchema.ValueGates is refused below its gate's release and a sibling value
// of the same property is not (go-kure/launcher#1230); the refusal names the path,
// the value, the gate and both releases. A gate the table lacks is an error when the
// gated value is authored, and a value of the wrong shape is left to the authored
// validation.
func TestValidateAuthoredPropertiesForTarget_ValueGates(t *testing.T) {
	newApp := func(flag any) *Application {
		app := makeApp("gates")
		app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "value-gated-policy", Properties: map[string]any{"flag": flag}}}
		return app
	}
	newTransformer := func(gate string) *Transformer {
		tr := NewTransformer(nil, nil)
		tr.RegisterPolicy("value-gated-policy", valueGatedPolicyHandler{gate: gate})
		return tr
	}

	t.Run("gated value below the release", func(t *testing.T) {
		_, err := newTransformer("EnvFiles").ValidateAuthoredPropertiesForTarget(newApp("on"), nil, &KubernetesVersion{1, 34})
		if err == nil {
			t.Fatal("a value gated on EnvFiles must be refused at 1.34")
		}
		for _, want := range []string{`policy "p" (type "value-gated-policy")`, ".flag", `value "on"`, "EnvFiles", "from 1.35", "kubernetesVersion is 1.34"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
	t.Run("gated value at the release and with no version", func(t *testing.T) {
		tr := newTransformer("EnvFiles")
		if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp("on"), nil, &KubernetesVersion{1, 35}); err != nil {
			t.Errorf("at 1.35: want accepted, got %v", err)
		}
		if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp("on"), nil, nil); err != nil {
			t.Errorf("no version stated: want accepted, got %v", err)
		}
	})
	t.Run("sibling value is never refused", func(t *testing.T) {
		if _, err := newTransformer("EnvFiles").ValidateAuthoredPropertiesForTarget(newApp("off"), nil, &KubernetesVersion{1, 1}); err != nil {
			t.Errorf("an ungated value must pass any version, got %v", err)
		}
	})
	t.Run("unknown gate", func(t *testing.T) {
		tr := newTransformer("NoSuchGate")
		_, err := tr.ValidateAuthoredPropertiesForTarget(newApp("on"), nil, &KubernetesVersion{1, 40})
		if err == nil || !strings.Contains(err.Error(), "NoSuchGate") {
			t.Errorf("a value gate the table lacks must be an error naming it, got %v", err)
		}
		if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp("on"), nil, nil); err != nil {
			t.Errorf("no version stated: nothing is consulted, got %v", err)
		}
	})
	t.Run("wrong shape is not this pass's business", func(t *testing.T) {
		if _, err := newTransformer("EnvFiles").ValidateAuthoredPropertiesForTarget(newApp(7), nil, &KubernetesVersion{1, 34}); err != nil && strings.Contains(err.Error(), "EnvFiles") {
			t.Errorf("a non-string value must not trip a value gate, got %v", err)
		}
	})
}

// gatedPolicySchema is the schema the two policy fakes below publish: one string
// property carrying gate.
func gatedPolicySchema(gate string) map[string]PropertySchema {
	return map[string]PropertySchema{"flag": {Type: PropertyTypeString, Gate: gate}}
}

// gatedPolicyHandler is a terminal policy handler whose schema names a gate.
type gatedPolicyHandler struct{ gate string }

func (h gatedPolicyHandler) CanHandle(policyType string) bool { return policyType == "gated-policy" }
func (h gatedPolicyHandler) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}
func (h gatedPolicyHandler) PropertySchema() map[string]PropertySchema {
	return gatedPolicySchema(h.gate)
}

// gatedPolicyLoweringRule is the lowering-rule counterpart: a policy type claimed by
// a rule and no handler.
type gatedPolicyLoweringRule struct{ gate string }

func (r gatedPolicyLoweringRule) PolicyType() string { return "gated-policy" }
func (r gatedPolicyLoweringRule) LowerPolicy(pol *ApplicationPolicy, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "dependency", Properties: map[string]any{}}}}, nil
}
func (r gatedPolicyLoweringRule) PropertySchema() map[string]PropertySchema {
	return gatedPolicySchema(r.gate)
}

// TestValidateAuthoredPropertiesForTarget_Policies: the gate pass walks policies the
// way the authored validation looks them up (handler first, lowering rule second),
// so a policy property that names a gate is refused below that gate's release, and a
// gate the table lacks is an error at any stated version and ignored without one.
func TestValidateAuthoredPropertiesForTarget_Policies(t *testing.T) {
	registrations := map[string]func(tr *Transformer, gate string){
		"handler":       func(tr *Transformer, gate string) { tr.RegisterPolicy("gated-policy", gatedPolicyHandler{gate: gate}) },
		"lowering rule": func(tr *Transformer, gate string) { tr.RegisterPolicyLowering(gatedPolicyLoweringRule{gate: gate}) },
	}
	for name, register := range registrations {
		newApp := func() *Application {
			app := makeApp("gates")
			app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "gated-policy", Properties: map[string]any{"flag": "on"}}}
			return app
		}

		t.Run(name+"/below the release", func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			register(tr, "EnvFiles")
			_, err := tr.ValidateAuthoredPropertiesForTarget(newApp(), nil, &KubernetesVersion{1, 34})
			if err == nil {
				t.Fatal("a policy property gated on EnvFiles must be refused at 1.34")
			}
			for _, want := range []string{`policy "p" (type "gated-policy")`, ".flag", "EnvFiles", "from 1.35", "kubernetesVersion is 1.34"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})

		t.Run(name+"/at the release and with no version", func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			register(tr, "EnvFiles")
			if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp(), nil, &KubernetesVersion{1, 35}); err != nil {
				t.Errorf("at 1.35: want accepted, got %v", err)
			}
			if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp(), nil, nil); err != nil {
				t.Errorf("no version stated: want accepted, got %v", err)
			}
		})

		t.Run(name+"/unknown gate", func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			register(tr, "NoSuchGate")
			_, err := tr.ValidateAuthoredPropertiesForTarget(newApp(), nil, &KubernetesVersion{1, 40})
			if err == nil || !strings.Contains(err.Error(), "NoSuchGate") {
				t.Errorf("an unknown gate on a policy must be an error naming it, got %v", err)
			}
			if _, err := tr.ValidateAuthoredPropertiesForTarget(newApp(), nil, nil); err != nil {
				t.Errorf("no version stated: no gate is consulted, got %v", err)
			}
		})
	}
}
