package traits_test

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// registerWebservice registers "webservice" on tr as kurel build does: the
// components.WebserviceRule lowering rule, plus the two terminal kinds it emits
// ("deployment", "service") and the "topology-spread" trait it synthesizes.
// Tests that build a webservice through a Transformer use it, so they run the
// production path — the same-name sibling group, its trait routing and its
// NetworkPolicy synthesis — rather than a handler stand-in.
func registerWebservice(tr *oam.Transformer) {
	tr.RegisterComponentLowering(components.WebserviceRule{})
	tr.RegisterComponent("deployment", &components.DeploymentHandler{})
	tr.RegisterComponent("service", &components.ServiceHandler{})
	tr.RegisterBuiltinTrait("topology-spread", &traits.TopologySpreadHandler{})
}

// webserviceViaRule presents components.WebserviceRule as the
// oam.ComponentHandler the former WebserviceHandler was, for the tests that
// call a handler directly rather than through a Transformer: the rule's
// LowerComponent, then DeploymentHandler and ServiceHandler for the two
// components it emits, then the synthesized topology-spread trait on the
// deployment member when the rule attached one. The config it returns is the
// deployment member's own, so ServiceAccountName, NonRWXClaim and
// EmitsAutoHealthCheck are DeploymentConfig's; ServicePort and ServicePortName
// are the service member's, as the sibling group answers them.
//
// components_test carries the same adapter (webservice_rule_adapter_test.go
// there): the two external test packages cannot share a test file.
type webserviceViaRule struct{}

func (webserviceViaRule) CanHandle(componentType string) bool { return componentType == "webservice" }

func (webserviceViaRule) PropertySchema() map[string]oam.PropertySchema {
	return components.WebserviceRule{}.PropertySchema()
}

func (webserviceViaRule) ToApplicationConfig(comp *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	res, err := components.WebserviceRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		return nil, err
	}
	if len(res.Components) != 2 || res.Components[0].Type != "deployment" || res.Components[1].Type != "service" {
		return nil, errors.Errorf("webservice rule emitted %d components, want a deployment and a service", len(res.Components))
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
	svcComp := res.Components[1]
	cfg, err = (&components.ServiceHandler{}).ToApplicationConfig(&svcComp, namespace)
	if err != nil {
		return nil, err
	}
	svc, ok := cfg.(*components.ServiceConfig)
	if !ok {
		return nil, errors.Errorf("service handler returned %T, want *components.ServiceConfig", cfg)
	}
	var synthesized []oam.Trait
	for _, t := range emitted.Traits {
		if t.Type == "topology-spread" {
			synthesized = append(synthesized, t)
		}
	}
	return &webserviceViaRuleConfig{DeploymentConfig: dep, service: svc, synthesized: synthesized}, nil
}

// webserviceViaRuleConfig is the deployment member's config, the service
// member's, and the traits the rule synthesized in front of the deployment
// member's (today at most one, topology-spread).
type webserviceViaRuleConfig struct {
	*components.DeploymentConfig
	service     *components.ServiceConfig
	synthesized []oam.Trait
}

func (c *webserviceViaRuleConfig) ServicePort() int32 { return c.service.ServicePort() }

func (c *webserviceViaRuleConfig) ServicePortName() (string, bool) {
	return c.service.ServicePortName()
}

// Generate generates both members as the sibling group does: each member's
// first object in member order (Deployment, Service), then the rest of each
// member's objects (the deployment member's ServiceAccount and claims).
func (c *webserviceViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	inner := stack.NewApplication(app.Name, app.Namespace, c.DeploymentConfig)
	for i := range c.synthesized {
		if err := (&traits.TopologySpreadHandler{}).Apply(&c.synthesized[i], inner, nil); err != nil {
			return nil, err
		}
	}
	dep, err := inner.Config.Generate(app)
	if err != nil {
		return nil, err
	}
	svc, err := c.service.Generate(app)
	if err != nil {
		return nil, err
	}
	var objs []*client.Object
	if len(dep) > 0 {
		objs = append(objs, dep[0])
	}
	if len(svc) > 0 {
		objs = append(objs, svc[0])
	}
	if len(dep) > 1 {
		objs = append(objs, dep[1:]...)
	}
	if len(svc) > 1 {
		objs = append(objs, svc[1:]...)
	}
	return objs, nil
}
