package components_test

import (
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// roleMemberType is the settled type of the workload member `webservice` and
// `worker` lower into (go-kure/launcher#1629). It is the reshaped parser's own
// type, so that the kind-named `deployment` is free to take the upstream spec.
const roleMemberType = "role-deployment"

func roleRules() map[string]oam.ComponentLoweringRule {
	return map[string]oam.ComponentLoweringRule{
		"webservice": components.WebserviceRule{},
		"worker":     components.WorkerRule{},
	}
}

// TestRoleRules_LowerIntoTheRoleMember: the first member each role emits is of
// the role member type, with the properties the role forwarded.
func TestRoleRules_LowerIntoTheRoleMember(t *testing.T) {
	for name, rule := range roleRules() {
		res, err := rule.LowerComponent(&oam.Component{Name: "app", Type: name, Properties: map[string]any{"image": "ghcr.io/org/app:v1"}}, oam.LoweringContext{})
		if err != nil {
			t.Fatalf("%s: LowerComponent: %v", name, err)
		}
		if len(res.Components) == 0 {
			t.Fatalf("%s: no member emitted", name)
		}
		if got := res.Components[0].Type; got != roleMemberType {
			t.Errorf("%s: first member type = %q, want %q", name, got, roleMemberType)
		}
		if got := res.Components[0].Name; got != "app" {
			t.Errorf("%s: first member name = %q, want the component's own", name, got)
		}
	}
}

// TestRoleRules_DeclareTheRoleMemberAsTarget: the engine refuses a declared
// target with no handler, so the declaration must name the type the rule
// really emits, and no longer name `deployment`.
func TestRoleRules_DeclareTheRoleMemberAsTarget(t *testing.T) {
	for name, rule := range roleRules() {
		d, ok := rule.(oam.LoweringTargetDeclarer)
		if !ok {
			t.Fatalf("%s: does not declare its lowering targets", name)
		}
		types := d.LoweringTargets().ComponentTypes
		if !slices.Contains(types, roleMemberType) {
			t.Errorf("%s: declared component targets %v, want %q among them", name, types, roleMemberType)
		}
		if slices.Contains(types, "deployment") {
			t.Errorf("%s: declared component targets %v still name the kind-named deployment", name, types)
		}
	}
}

// TestRoleRules_KeyMapNamesTheRoleMember: every place the published key map
// sends a value to the workload member names the role member type.
func TestRoleRules_KeyMapNamesTheRoleMember(t *testing.T) {
	for name, rule := range roleRules() {
		keyed, ok := rule.(interface {
			LoweringKeys() map[string][]oam.LoweredKey
		})
		if !ok {
			t.Fatalf("%s: publishes no lowering key map", name)
		}
		sawRole := false
		for key, places := range keyed.LoweringKeys() {
			for _, p := range places {
				if p.Member == "deployment" {
					t.Errorf("%s: key %q lands in member %q, want %q", name, key, p.Member, roleMemberType)
				}
				if p.Member == roleMemberType {
					sawRole = true
				}
			}
		}
		if !sawRole {
			t.Errorf("%s: no key lands in the role member %q", name, roleMemberType)
		}
	}
}
