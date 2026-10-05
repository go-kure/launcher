package traits_test

import (
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// clusterRoleRule lowers a "cluster-reader" component to a deployment, after
// resolving the name of a ClusterRole it generates for it: a cluster-scoped
// object, which the rule says with NameSpec.ClusterScoped.
type clusterRoleRule struct{}

func (clusterRoleRule) ComponentType() string { return "cluster-reader" }

func (clusterRoleRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	spec := oam.NameSpec{
		Role:          oam.NameRoleRBAC,
		Kind:          schema.GroupKind{Group: rbacv1.GroupName, Kind: "ClusterRole"},
		ClusterScoped: true,
	}
	if _, err := lctx.ResolveName(comp.Name, "reader", spec); err != nil {
		return oam.LoweringResult{}, err
	}
	return oam.LoweringResult{Components: []oam.Component{{
		Name: comp.Name, Type: "deployment", Properties: map[string]any{"image": "nginx:1.25"},
	}}}, nil
}

// A lowering rule and the rbac trait say "no namespace" the same way
// (go-kure/launcher#787), so a ClusterRole a rule names and one the trait
// generates under the same name meet in the one claim space and are refused
// with both named, in whatever namespace the document is transformed. Lowering
// runs before any trait is applied, so the rule's name is always the first of
// the two; the order of the components changes nothing.
func TestRBAC_ClusterRoleAgainstALoweredOne(t *testing.T) {
	agent := oam.Component{Name: "agent", Type: "cluster-reader", Properties: map[string]any{}}
	web := func(name string) oam.Component {
		return oam.Component{
			Name: "web", Type: "deployment",
			Properties: map[string]any{"image": "nginx:1.25", "serviceAccountName": "web-runner"},
			Traits: []oam.Trait{{Type: "rbac", Properties: map[string]any{
				"name":        name,
				"clusterWide": true,
				"rules": []any{map[string]any{
					"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"},
				}},
			}}},
		}
	}
	transform := func(namespace string, comps ...oam.Component) error {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": &components.DeploymentHandler{}}, nil)
		tr.RegisterBuiltinTrait("rbac", &traits.RBACHandler{})
		tr.RegisterComponentLowering(clusterRoleRule{})
		_, err := tr.Transform(&oam.Application{
			APIVersion: oam.SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   oam.Metadata{Name: "shop", Namespace: namespace},
			Spec:       oam.ApplicationSpec{Components: comps},
		}, oam.TransformContext{Namespace: namespace})
		return err
	}

	const want = `name collision: ClusterRole.rbac.authorization.k8s.io "agent-reader" is named by ` +
		`component "agent" (role "rbac", its default) and by ` +
		`component "web" traits[0] "rbac" (role "rbac", set by name); give one of them another name`
	for _, tc := range []struct {
		name  string
		comps []oam.Component
	}{
		{"the rule's component first", []oam.Component{agent, web("agent-reader")}},
		{"the trait's component first", []oam.Component{web("agent-reader"), agent}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, namespace := range []string{"default", "prod"} {
				err := transform(namespace, tc.comps...)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("namespace %s: err = %v\nwant one containing %s", namespace, err, want)
				}
			}
		})
	}

	// The control: under another name the two are two objects.
	if err := transform("default", agent, web("web-reader")); err != nil {
		t.Fatalf("a ClusterRole of another name: %v", err)
	}
}
