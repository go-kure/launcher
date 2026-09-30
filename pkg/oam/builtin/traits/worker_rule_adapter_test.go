package traits_test

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
// components_test carries the same adapter (worker_rule_adapter_test.go there):
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
	if len(res.Components) != 1 {
		return nil, errors.Errorf("worker rule emitted %d components, want 1", len(res.Components))
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
	synthesized := emitted.Traits[:len(emitted.Traits)-len(comp.Traits)]
	return &workerViaRuleConfig{DeploymentConfig: dep, synthesized: synthesized}, nil
}

// workerViaRuleConfig is the deployment component's config plus the traits the
// worker rule synthesized in front of the authored ones (today at most one,
// topology-spread), applied innermost at Generate as the engine applies them.
type workerViaRuleConfig struct {
	*components.DeploymentConfig
	synthesized []oam.Trait
}

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
	return inner.Config.Generate(app)
}
