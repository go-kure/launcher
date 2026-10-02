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
// ApplyPolicy, ServiceAccountName, NonRWXClaim and EmitsAutoHealthCheck are
// DeploymentConfig's; only Generate is wrapped, to apply that trait.
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
// synthesized in front of the authored ones (today at most one,
// topology-spread), applied innermost at Generate as the engine applies them.
type workerViaRuleConfig struct {
	*components.DeploymentConfig
	serviceAccount stack.ApplicationConfig
	synthesized    []oam.Trait
}

// Generate generates the members as the sibling group does: each member's
// first object in member order (Deployment, ServiceAccount), then the rest of
// the deployment member's objects (its claims).
func (c *workerViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	inner := stack.NewApplication(app.Name, app.Namespace, c.DeploymentConfig)
	for i := range c.synthesized {
		if c.synthesized[i].Type != "topology-spread" {
			return nil, errors.Errorf("worker rule synthesized an unexpected %q trait", c.synthesized[i].Type)
		}
		if err := (&traits.TopologySpreadHandler{}).Apply(&c.synthesized[i], inner, nil); err != nil {
			return nil, err
		}
	}
	dep, err := inner.Config.Generate(app)
	if err != nil || c.serviceAccount == nil {
		return dep, err
	}
	sa, err := c.serviceAccount.Generate(app)
	if err != nil {
		return nil, err
	}
	objs := append([]*client.Object{dep[0]}, sa...)
	return append(objs, dep[1:]...), nil
}
