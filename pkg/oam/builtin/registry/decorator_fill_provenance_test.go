package registry_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests hold the provenance report to the fills of a decorating trait
// (go-kure/launcher#1580): a trait that wraps its component's config adds no
// sub-application, so the runtime reads its fills once the build is done, under
// the trait's own owner. The topology-spread trait fills the constraints a
// Deployment gets from its replica count.

const spreadPath = "topologySpreadConstraints"

func spreadDeployment(props map[string]any, traits ...oam.Trait) oam.Component {
	p := map[string]any{"image": "registry.example/app:1"}
	for k, v := range props {
		p[k] = v
	}
	return oam.Component{Name: "app", Type: "deployment", Properties: p, Traits: traits}
}

func spreadTrait() oam.Trait {
	return oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
}

func TestDecoratorFills_TopologySpreadReportsTheConstraintsItWrites(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 5}}
	cases := []struct {
		name string
		comp oam.Component
		want bool
	}{
		{"three replicas", spreadDeployment(map[string]any{"replicas": 3}, spreadTrait()), true},
		{"two replicas", spreadDeployment(map[string]any{"replicas": 2}, spreadTrait()), true},
		// BuildTopologySpreadConstraints returns nil at one replica: nothing is filled.
		{"one replica", spreadDeployment(map[string]any{"replicas": 1}, spreadTrait()), false},
		// The count is the scaler's minimum, a trait that comes after this one.
		{"scaler minimum of two, applied after the trait", spreadDeployment(nil, spreadTrait(), scaler), true},
		{"scaler minimum of two, applied before the trait", spreadDeployment(nil, scaler, spreadTrait()), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &oam.NoopPolicy{}, tc.comp)
			got, ok := entriesOf(prov, owner{component: "app", trait: "topology-spread"})[spreadPath]
			want := oam.Source{Kind: oam.SourceBuiltin, Ref: "topology-spread.topologySpreadConstraints"}
			switch {
			case tc.want && got != want:
				t.Errorf("%s = %+v (held: %v), want %+v", spreadPath, got, ok, want)
			case !tc.want && ok:
				t.Errorf("%s = %+v, want no entry: the trait writes no constraint", spreadPath, got)
			}
		})
	}
}

// The component's own fills are not read a second time under a trait that
// leaves the component's config alone: the scaler adds a sub-application and
// reports its own fills, and none of the deployment's.
func TestDecoratorFills_ATraitThatDoesNotWrapReportsNothingOfTheComponents(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 5}}
	prov, _ := policyProvenanceFor(t, &oam.NoopPolicy{}, spreadDeployment(nil, scaler))
	got := entriesOf(prov, owner{component: "app", trait: "scaler"})
	if _, ok := got["resources.requests.cpu"]; ok {
		t.Errorf("the deployment's fill was read under the scaler: %v", got)
	}
	if _, ok := got["cpuUtilization"]; !ok {
		t.Errorf("the scaler's own fill is gone: %v", got)
	}
}

// The report changes nothing the transform produces: the objects generated
// with it on equal those generated with it off, so reading a decorator's fills
// leaves its config as it was.
func TestDecoratorFills_ReportDoesNotChangeTheOutput(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 5}}
	for name, comp := range map[string]oam.Component{
		"three replicas": spreadDeployment(map[string]any{"replicas": 3}, spreadTrait()),
		"one replica":    spreadDeployment(map[string]any{"replicas": 1}, spreadTrait()),
		"under a scaler": spreadDeployment(nil, spreadTrait(), scaler),
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
