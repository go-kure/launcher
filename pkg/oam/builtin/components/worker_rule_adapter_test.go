package components_test

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// workerViaRule presents components.WorkerRule as the oam.ComponentHandler the
// former WorkerHandler was, so the handler-shaped tests pinning worker's
// behaviour keep running against the production path, one step at a time
// exactly as the engine takes them: the rule's LowerComponent, then
// DeploymentHandler for the component it emits, then the synthesized
// topology-spread trait (traits.TopologySpreadHandler) when the rule attached
// one. The config it returns is the deployment component's own, so
// ApplyPolicy, ServiceAccountName and NonRWXClaim are DeploymentConfig's; only Generate is wrapped, to apply that trait.
//
// traits_test carries the same adapter (worker_rule_adapter_test.go there):
// the two external test packages cannot share a test file.
type workerViaRule struct{}

func (workerViaRule) CanHandle(componentType string) bool { return componentType == "worker" }

func (workerViaRule) PropertySchema() map[string]oam.PropertySchema {
	return components.WorkerRule{}.PropertySchema()
}

func (workerViaRule) ToApplicationConfig(comp *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	res, err := components.WorkerRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		return nil, err
	}
	n := len(res.Components)
	if n < 1 || n > 2 || res.Components[0].Type != "deployment" || (n == 2 && res.Components[1].Type != "serviceaccount") {
		return nil, errors.Errorf("worker rule emitted %d components, want a deployment and at most a serviceaccount", n)
	}
	emitted := res.Components[0]
	cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(&emitted, namespace)
	if err != nil {
		return nil, err
	}
	dep, ok := cfg.(*components.DeploymentConfig)
	if !ok {
		return nil, errors.Errorf("deployment handler returned %T, want *components.DeploymentConfig", cfg)
	}
	var sa stack.ApplicationConfig
	if n == 2 {
		if sa, err = (&components.ServiceAccountHandler{}).ToApplicationConfig(&res.Components[1], namespace); err != nil {
			return nil, err
		}
	}
	synthesized := emitted.Traits[:len(emitted.Traits)-len(comp.Traits)]
	return &workerViaRuleConfig{DeploymentConfig: dep, serviceAccount: sa, synthesized: synthesized}, nil
}

// workerViaRuleConfig is the deployment component's config, the serviceaccount
// member's when the rule emitted one, and the traits the worker rule
// synthesized in front of the authored ones (topology-spread, and one `pvc`
// trait per claim its pvc volumes describe), applied at Generate as the engine
// applies them.
type workerViaRuleConfig struct {
	*components.DeploymentConfig
	serviceAccount stack.ApplicationConfig
	synthesized    []oam.Trait
}

// Generate generates the members as the sibling group does: each member's
// first object in member order (Deployment, ServiceAccount), then the rest of
// the deployment member's objects, then the claims of the synthesized `pvc`
// traits, which follow their component.
func (c *workerViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	inner := stack.NewApplication(app.Name, app.Namespace, c.DeploymentConfig)
	subApps, err := applySynthesizedTraits("worker", c.synthesized, inner)
	if err != nil {
		return nil, err
	}
	dep, err := inner.Config.Generate(app)
	if err != nil {
		return nil, err
	}
	objs := []*client.Object{dep[0]}
	if c.serviceAccount != nil {
		sa, err := c.serviceAccount.Generate(app)
		if err != nil {
			return nil, err
		}
		objs = append(objs, sa...)
	}
	objs = append(objs, dep[1:]...)
	claims, err := generateSubApplications(subApps)
	if err != nil {
		return nil, err
	}
	return append(objs, claims...), nil
}

// applySynthesizedTraits applies the traits a role rule synthesized on its
// deployment member as the engine does: topology-spread decorates the member,
// and each `pvc` trait becomes a sub-application carrying its claim. It returns
// those sub-applications for generateSubApplications.
func applySynthesizedTraits(rule string, synthesized []oam.Trait, inner *stack.Application) ([]*stack.Application, error) {
	bundle := &stack.Bundle{}
	for i := range synthesized {
		var err error
		switch synthesized[i].Type {
		case "topology-spread":
			err = (&traits.TopologySpreadHandler{}).Apply(&synthesized[i], inner, nil)
		case "pvc":
			err = (&traits.PVCHandler{}).Apply(&synthesized[i], inner, bundle)
		default:
			err = errors.Errorf("%s rule synthesized an unexpected %q trait", rule, synthesized[i].Type)
		}
		if err != nil {
			return nil, err
		}
	}
	return bundle.Applications, nil
}

// generateSubApplications generates each trait sub-application in order.
func generateSubApplications(apps []*stack.Application) ([]*client.Object, error) {
	var objs []*client.Object
	for _, a := range apps {
		o, err := a.Config.Generate(a)
		if err != nil {
			return nil, err
		}
		objs = append(objs, o...)
	}
	return objs, nil
}
