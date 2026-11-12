package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// webservice and worker accept `tolerations` and `topologySpreadConstraints`
// and hand them to their deployment member unchanged, as `deployment` takes
// them (go-kure/launcher#1473, ruled in go-kure/launcher#1252).

// roleCase runs one role's rule over a component. lower returns the deployment
// member, or the error the role's own parse raised before lowering.
type roleCase struct {
	name  string
	typ   string
	lower func(*oam.Component) (oam.Component, error)
}

func roleCases() []roleCase {
	return []roleCase{
		{"webservice", "webservice", func(c *oam.Component) (oam.Component, error) {
			res, err := components.WebserviceRule{}.LowerComponent(c, oam.LoweringContext{})
			if err != nil {
				return oam.Component{}, err
			}
			return res.Components[0], nil
		}},
		{"worker", "worker", func(c *oam.Component) (oam.Component, error) {
			res, err := components.WorkerRule{}.LowerComponent(c, oam.LoweringContext{})
			if err != nil {
				return oam.Component{}, err
			}
			return res.Components[0], nil
		}},
	}
}

// An authored toleration reaches the deployment member as authored, including
// a tolerationSeconds of 0, which differs from an unset one.
func TestRolePlacement_AuthoredPlacementReachesDeployment(t *testing.T) {
	for _, rc := range roleCases() {
		t.Run(rc.name, func(t *testing.T) {
			tolerations := []any{
				map[string]any{"key": "dedicated", "operator": "Equal", "value": "batch", "effect": "NoExecute", "tolerationSeconds": 0},
				map[string]any{"operator": "Exists"},
			}
			spread := []any{map[string]any{
				"maxSkew": 2, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
			}}
			dep, err := rc.lower(&oam.Component{Name: "app", Type: rc.typ, Properties: map[string]any{
				"image":                     "ghcr.io/org/app:v1",
				"topologySpread":            false,
				"tolerations":               tolerations,
				"topologySpreadConstraints": spread,
			}})
			if err != nil {
				t.Fatalf("LowerComponent: %v", err)
			}
			if got := dep.Properties["tolerations"]; !reflect.DeepEqual(got, tolerations) {
				t.Errorf("deployment tolerations = %v, want the authored %v", got, tolerations)
			}
			if got := dep.Properties["topologySpreadConstraints"]; !reflect.DeepEqual(got, spread) {
				t.Errorf("deployment topologySpreadConstraints = %v, want the authored %v", got, spread)
			}
		})
	}
}

// The role's own parse refuses a bad toleration or constraint before lowering,
// naming the property path and the rule, not a path of the deployment member.
func TestRolePlacement_BadValueRefusedByRole(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{"tolerationSeconds without NoExecute",
			map[string]any{"tolerations": []any{map[string]any{"key": "k", "operator": "Exists", "effect": "NoSchedule", "tolerationSeconds": 30}}},
			[]string{"toleration[0].effect", "must be 'NoExecute' when tolerationSeconds is set"}},
		{"constraint without maxSkew",
			map[string]any{"topologySpreadConstraints": []any{map[string]any{"topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "DoNotSchedule"}}},
			[]string{"topologySpreadConstraints[0].maxSkew", "required"}},
	}
	for _, rc := range roleCases() {
		for _, tc := range cases {
			t.Run(rc.name+"/"+tc.name, func(t *testing.T) {
				props := map[string]any{"image": "ghcr.io/org/app:v1", "topologySpread": false}
				for k, v := range tc.props {
					props[k] = v
				}
				_, err := rc.lower(&oam.Component{Name: "app", Type: rc.typ, Properties: props})
				if err == nil {
					t.Fatalf("LowerComponent built; want a refusal containing %q", tc.want)
				}
				for _, w := range tc.want {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("err = %v, want it to contain %q", err, w)
					}
				}
			})
		}
	}
}
