package traits_test

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// kindOnlyPolicy restricts nothing but the kinds of object, through
// oam.ObjectKindPolicy.
type kindOnlyPolicy struct {
	oam.NoopPolicy
	forbidden    []schema.GroupKind
	allowCluster bool
}

func (p *kindOnlyPolicy) AllowedObjectKinds() []schema.GroupKind   { return nil }
func (p *kindOnlyPolicy) ForbiddenObjectKinds() []schema.GroupKind { return p.forbidden }
func (p *kindOnlyPolicy) AllowClusterScopedObjects() bool          { return p.allowCluster }

// TestObjectKindPolicy_TraitObjects: the objects a trait adds, in a
// sub-application of its own, are held to the object kind policy (go-kure/launcher#922)
// as the component's own are, and the refusal names the component the trait
// runs on: the rbac trait's Role under a policy that forbids the RBAC group,
// and its ClusterRole under one that does not allow cluster-scoped objects.
func TestObjectKindPolicy_TraitObjects(t *testing.T) {
	generate := func(clusterWide bool, policy oam.Policy) error {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": &components.DeploymentHandler{}}, nil)
		tr.RegisterBuiltinTrait("rbac", &traits.RBACHandler{})
		cluster, err := tr.Transform(&oam.Application{
			APIVersion: oam.SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   oam.Metadata{Name: "shop"},
			Spec: oam.ApplicationSpec{Components: []oam.Component{{
				Name: "web", Type: "deployment",
				Properties: map[string]any{"image": "nginx:1.25", "serviceAccountName": "web-runner"},
				Traits: []oam.Trait{{Type: "rbac", Properties: map[string]any{
					"clusterWide": clusterWide,
					"rules": []any{map[string]any{
						"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"},
					}},
				}}},
			}}},
		}, oam.TransformContext{Namespace: "demo", Policy: policy})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		_, err = oam.GenerateApplications(cluster)
		return err
	}
	refused := func(t *testing.T, err error, fragments ...string) {
		t.Helper()
		var v *oam.ViolationError
		if !errors.As(err, &v) || v.Component != "web" || v.Class != oam.RefusalObjectKind {
			t.Fatalf("error = %v, want component web's violation of class %q", err, oam.RefusalObjectKind)
		}
		for _, f := range fragments {
			if !strings.Contains(err.Error(), f) {
				t.Errorf("refusal %q lacks %q", err, f)
			}
		}
	}

	rbac := &kindOnlyPolicy{forbidden: []schema.GroupKind{{Group: "rbac.authorization.k8s.io", Kind: "*"}}, allowCluster: true}
	refused(t, generate(false, rbac), "(rbac.authorization.k8s.io/Role", "forbids the kind")
	refused(t, generate(true, &kindOnlyPolicy{}), "(rbac.authorization.k8s.io/ClusterRole", "cluster-scoped")
	if err := generate(false, &kindOnlyPolicy{}); err != nil {
		t.Errorf("GenerateApplications = %v, want a namespaced Role built where cluster-scoped objects are not allowed", err)
	}
}
