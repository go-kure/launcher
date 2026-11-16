package registry_test

import (
	"sort"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The security-context trait fills the properties its psaLevel profile
// supplies where neither the trait nor the workload set them
// (go-kure/launcher#1580). It wraps its component's config and adds no
// sub-application, so the runtime reads its fills once the build is done.

func securityTrait(props map[string]any) oam.Trait {
	p := map[string]any{}
	for k, v := range props {
		p[k] = v
	}
	return oam.Trait{Type: "security-context", Properties: p}
}

// securityFills returns the paths the report holds under the trait as built-in
// fills, with each source checked to name the trait. A property the author
// wrote on the trait is held as authored and is no fill.
func securityFills(t *testing.T, comp oam.Component) []string {
	t.Helper()
	prov, _ := policyProvenanceFor(t, &oam.NoopPolicy{}, comp)
	return builtinSecurityFills(t, prov)
}

func builtinSecurityFills(t *testing.T, prov *oam.Provenance) []string {
	t.Helper()
	var got []string
	for path, src := range entriesOf(prov, owner{component: "app", trait: "security-context"}) {
		if src.Kind != oam.SourceBuiltin {
			continue
		}
		if want := (oam.Source{Kind: oam.SourceBuiltin, Ref: "security-context." + path}); src != want {
			t.Errorf("%s = %+v, want %+v", path, src, want)
		}
		got = append(got, path)
	}
	sort.Strings(got)
	return got
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDecoratorFills_SecurityContextReportsWhatTheLevelFills(t *testing.T) {
	all := []string{"allowPrivilegeEscalation", "readOnlyRootFilesystem", "runAsNonRoot"}
	restricted := map[string]any{"psaLevel": "restricted"}
	cases := []struct {
		name string
		comp oam.Component
		want []string
	}{
		{"restricted on a workload that authors nothing", spreadDeployment(nil, securityTrait(restricted)), all},
		{"baseline adds nothing", spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "baseline"})), nil},
		{"privileged adds nothing", spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "privileged"})), nil},
		// A Windows pod accepts runAsNonRoot alone, so that is all the profile fills.
		{"a Windows pod", spreadDeployment(map[string]any{"os": map[string]any{"name": "windows"}}, securityTrait(restricted)),
			[]string{"runAsNonRoot"}},
		// A property the trait sets is the trait's, not a fill.
		{"the trait sets runAsNonRoot", spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "restricted", "runAsNonRoot": true})),
			[]string{"allowPrivilegeEscalation", "readOnlyRootFilesystem"}},
		{"the trait sets all three", spreadDeployment(nil, securityTrait(map[string]any{
			"psaLevel": "restricted", "runAsNonRoot": true, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true})), nil},
		// A value the workload authored stays, and is no fill.
		{"the workload authors allowPrivilegeEscalation", spreadDeployment(map[string]any{
			"securityContext": map[string]any{"allowPrivilegeEscalation": false}}, securityTrait(restricted)),
			[]string{"readOnlyRootFilesystem", "runAsNonRoot"}},
		{"the workload authors readOnlyRootFilesystem false", spreadDeployment(map[string]any{
			"securityContext": map[string]any{"readOnlyRootFilesystem": false}}, securityTrait(restricted)),
			[]string{"allowPrivilegeEscalation", "runAsNonRoot"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := securityFills(t, tc.comp); !sameStrings(got, tc.want) {
				t.Errorf("the report holds %v under the trait, want %v", got, tc.want)
			}
		})
	}
}

// Authoring the value the profile fills gives the objects the fill gives, so
// the report names a value the library really writes.
func TestDecoratorFills_SecurityContextFillsMatchWhatAnAuthorWritesForTheSameValue(t *testing.T) {
	left := spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "restricted"}))
	right := spreadDeployment(nil, securityTrait(map[string]any{
		"psaLevel": "restricted", "runAsNonRoot": true, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true}))
	_, a := policyProvenanceFor(t, &oam.NoopPolicy{}, left)
	_, b := policyProvenanceFor(t, &oam.NoopPolicy{}, right)
	if string(a) != string(b) {
		t.Errorf("the objects of the trait's own values differ from the objects of the profile's fills")
	}
}

// The report changes nothing the transform produces.
func TestDecoratorFills_SecurityContextReportDoesNotChangeTheOutput(t *testing.T) {
	for name, comp := range map[string]oam.Component{
		"restricted": spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "restricted"})),
		"baseline":   spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "baseline"})),
	} {
		t.Run(name, func(t *testing.T) {
			p := &oam.NoopPolicy{}
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
			}
			cluster, _, err := newTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: p})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			_, reported := policyProvenanceFor(t, p, comp)
			if string(manifestsOf(t, cluster)) != string(reported) {
				t.Errorf("the generated objects differ with the report on")
			}
		})
	}
}

// Generate refuses runAsNonRoot: false at restricted, so the build fails and
// the report holds nothing for it.
func TestDecoratorFills_ARefusedSecurityContextReportsNothing(t *testing.T) {
	comp := spreadDeployment(nil, securityTrait(map[string]any{"psaLevel": "restricted", "runAsNonRoot": false}))
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	_, _, prov, err := newTransformer().TransformWithProvenance(app, oam.TransformContext{Namespace: "default", Policy: &oam.NoopPolicy{}})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	if got := builtinSecurityFills(t, prov); len(got) != 0 {
		t.Errorf("the report holds the fills %v for a trait Generate refuses", got)
	}
}
