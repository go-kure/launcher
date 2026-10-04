package traits_test

import (
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/fluxcd"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The two annotations as kustomize-controller reads them, written out
// literally rather than read from a constant: a test that shared the
// library's constant would stay green if the constant drifted from Flux.
const (
	fluxPruneKey      = "kustomize.toolkit.fluxcd.io/prune"
	fluxPruneDisabled = "disabled"
	fluxForceKey      = "kustomize.toolkit.fluxcd.io/force"
	fluxForceEnabled  = "enabled"
)

// deliveryTestLabelKey is the component label the test transforms with, so an
// object in the layout names the component that owns it.
const deliveryTestLabelKey = "example.test/component"

// augmentingHandler is a component whose config emits one ConfigMap from
// Generate and adds two more in AugmentLayout: one to its own layout, one in a
// child layout. Those are the objects no Generate returns.
type augmentingHandler struct{}

func (augmentingHandler) CanHandle(t string) bool { return t == "augmenting" }

func (augmentingHandler) ToApplicationConfig(c *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	return &namespacedAugmenter{name: c.Name, namespace: namespace}, nil
}

type namespacedAugmenter struct{ name, namespace string }

func (a *namespacedAugmenter) configMap(suffix string) client.Object {
	cm := newTestConfigMap(a.name + "-" + suffix)
	cm.SetNamespace(a.namespace)
	return cm
}

func (a *namespacedAugmenter) Generate(_ *stack.Application) ([]*client.Object, error) {
	obj := a.configMap("generated")
	return []*client.Object{&obj}, nil
}

func (a *namespacedAugmenter) AugmentLayout(ml *layout.ManifestLayout) error {
	ml.Resources = append(ml.Resources, a.configMap("augmented"))
	ml.Children = append(ml.Children, &layout.ManifestLayout{
		Name:      "child",
		Resources: []client.Object{a.configMap("augmented-child")},
	})
	return nil
}

// bundlesWithApplications returns every bundle under n that holds applications.
func bundlesWithApplications(n *stack.Node) []*stack.Bundle {
	if n == nil {
		return nil
	}
	var out []*stack.Bundle
	var visit func(b *stack.Bundle)
	visit = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		if len(b.Applications) > 0 {
			out = append(out, b)
		}
		for _, c := range b.Children {
			visit(c)
		}
	}
	visit(n.Bundle)
	for _, c := range n.Children {
		out = append(out, bundlesWithApplications(c)...)
	}
	return out
}

// TestDeliveryIntent_FluxWorkflowAnnotatesEverythingAComponentOwns is the
// behaviour the two traits had before go-kure/launcher#782, shown where it now
// comes from. The traits state an intent on the application; the base
// library's Flux workflow turns it into the per-object annotations. Every
// object a component owns is covered: a sibling group's members (the
// webservice's Deployment, Service and ServiceAccount), a trait
// sub-application (the rbac Role and RoleBinding), and what a layout augmenter
// adds outside Generate. A component without the traits gets none.
func TestDeliveryIntent_FluxWorkflowAnnotatesEverythingAComponentOwns(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	registerWebservice(tr)
	tr.RegisterComponent("augmenting", augmentingHandler{})
	tr.RegisterBuiltinTrait("rbac", &traits.RBACHandler{})
	tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
	tr.RegisterBuiltinTrait("force-replace", &traits.ForceReplaceHandler{})

	cluster, err := tr.Transform(&oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{
					{Type: "prune-protection"},
					*rbacPodsTrait(),
					{Type: "force-replace"},
				},
			},
			{Name: "chart", Type: "augmenting", Traits: []oam.Trait{{Type: "prune-protection"}}},
			{Name: "plain", Type: "deployment", Properties: map[string]any{"image": "nginx:1.25"}},
		}},
	}, oam.TransformContext{Namespace: "default", ComponentLabelKey: deliveryTestLabelKey})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	// Launcher's own output carries no annotation: the intent is on the
	// applications, the objects are as the components generate them.
	for _, b := range bundlesWithApplications(cluster.Node) {
		for _, app := range b.Applications {
			assertNoFluxObjectKeys(t, generatedObjects(t, app)...)
		}
		b.SourceRef = &stack.SourceRef{Kind: "OCIRepository", Name: b.Name, URL: "oci://registry.example/" + b.Name, Tag: "v1"}
	}

	ml, err := fluxcd.NewWorkflowEngine().GetLayoutIntegrator().CreateLayoutWithResources(cluster, layout.DefaultLayoutRules())
	if err != nil {
		t.Fatalf("CreateLayoutWithResources: %v", err)
	}

	want := map[string]map[string]string{
		"web":   {fluxPruneKey: fluxPruneDisabled, fluxForceKey: fluxForceEnabled},
		"chart": {fluxPruneKey: fluxPruneDisabled},
		"plain": {},
	}
	kinds := map[string][]string{}
	for _, o := range layoutObjects(ml) {
		component, owned := o.GetLabels()[deliveryTestLabelKey]
		if !owned {
			continue // a Flux object the workflow generated
		}
		wantAnn, known := want[component]
		if !known {
			t.Errorf("%T %q is labelled for component %q, which the document does not have", o, o.GetName(), component)
			continue
		}
		kinds[component] = append(kinds[component], o.GetObjectKind().GroupVersionKind().Kind+"/"+o.GetName())
		for _, key := range []string{fluxPruneKey, fluxForceKey} {
			got, present := o.GetAnnotations()[key]
			if wantValue, wanted := wantAnn[key]; wanted != present || got != wantValue {
				t.Errorf("component %q, %T %q: annotation %s = %q (present %v), want %q (present %v)",
					component, o, o.GetName(), key, got, present, wantValue, wanted)
			}
		}
	}

	for component, wantObjects := range map[string][]string{
		"web":   {"Deployment/web", "Role/web", "RoleBinding/web", "Service/web", "ServiceAccount/web"},
		"chart": {"ConfigMap/chart-augmented", "ConfigMap/chart-augmented-child", "ConfigMap/chart-generated"},
		"plain": {"Deployment/plain"},
	} {
		got := slices.Sorted(slices.Values(kinds[component]))
		if !slices.Equal(got, wantObjects) {
			t.Errorf("component %q owns %v in the layout, want %v: the annotation check above did not see what it claims to cover",
				component, got, wantObjects)
		}
	}
}

// TestDeliveryIntent_SiblingGroupTakesAnyMembersIntent pins the rule for a
// sibling group, whose members are delivered as one application: the group
// takes each intent that any member has. A lowering rule that forwards a trait
// to one member only therefore covers the whole group, which is the ticket's
// rule that every object the component owns is covered (go-kure/launcher#782).
func TestDeliveryIntent_SiblingGroupTakesAnyMembersIntent(t *testing.T) {
	cases := []struct {
		name      string
		depTraits []oam.Trait
		svcTraits []oam.Trait
		want      stack.DeliveryIntent
	}{
		{name: "no member", want: stack.DeliveryIntent{}},
		{name: "deployment member only", depTraits: []oam.Trait{{Type: "prune-protection"}},
			want: stack.DeliveryIntent{PruneProtection: true}},
		{name: "service member only", svcTraits: []oam.Trait{{Type: "force-replace"}},
			want: stack.DeliveryIntent{ForceReplace: true}},
		{name: "one intent each", depTraits: []oam.Trait{{Type: "prune-protection"}}, svcTraits: []oam.Trait{{Type: "force-replace"}},
			want: stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent("deployment", &components.DeploymentHandler{})
			tr.RegisterComponent("service", &components.ServiceHandler{})
			tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
			tr.RegisterBuiltinTrait("force-replace", &traits.ForceReplaceHandler{})
			tr.RegisterComponentLowering(ownPodsRule{ports: identityPorts, depTraits: tc.depTraits})

			cluster, err := tr.Transform(&oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
				Spec: oam.ApplicationSpec{Components: []oam.Component{
					{Name: "web", Type: "own-pods", Traits: tc.svcTraits},
				}},
			}, oam.TransformContext{Namespace: "default"})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			var groups []*stack.Application
			for _, b := range bundlesWithApplications(cluster.Node) {
				for _, app := range b.Applications {
					if app.Name == "web" {
						groups = append(groups, app)
					}
				}
			}
			if len(groups) != 1 {
				t.Fatalf("the cluster holds %d applications named web, want the one sibling group", len(groups))
			}
			if got := groups[0].Delivery; got != tc.want {
				t.Errorf("group Delivery = %+v, want %+v", got, tc.want)
			}
		})
	}
}
