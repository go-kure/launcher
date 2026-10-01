package components_test

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// webserviceViaRule presents components.WebserviceRule as the
// oam.ComponentHandler the former WebserviceHandler was, so the handler-shaped
// tests pinning webservice's behaviour keep running against the production
// path, one step at a time as the engine takes them: the rule's
// LowerComponent, then DeploymentHandler and ServiceHandler for the two
// components it emits, then the synthesized topology-spread trait
// (traits.TopologySpreadHandler) on the deployment member when the rule
// attached one. The config it returns is the deployment member's own, so
// ApplyPolicy, ServiceAccountName, NonRWXClaim and EmitsAutoHealthCheck are
// DeploymentConfig's; ServicePort and ServicePortName are the service
// member's, as the sibling group answers them.
//
// Authored traits are not applied here: the tests using this adapter as a
// handler pass components without traits, and the trait routing is pinned on
// LowerComponent's result and through the engine (traits_test).
type webserviceViaRule struct{}

func (webserviceViaRule) CanHandle(componentType string) bool { return componentType == "webservice" }

func (webserviceViaRule) PropertySchema() map[string]oam.PropertySchema {
	return components.WebserviceRule{}.PropertySchema()
}

func (webserviceViaRule) Endpoints(comp *oam.Component) ([]netpol.Endpoint, error) {
	return components.WebserviceRule{}.Endpoints(comp)
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
	return &webserviceViaRuleConfig{
		DeploymentConfig:       dep,
		service:                svc,
		synthesized:            synthesized,
		Port:                   svc.ServicePort(),
		TopologySpreadDisabled: len(synthesized) == 0,
	}, nil
}

// webserviceViaRuleConfig is the deployment member's config, the service
// member's, and the traits the rule synthesized in front of the deployment
// member's (today at most one, topology-spread). Port and
// TopologySpreadDisabled restate the two opinions the former WebserviceConfig
// carried as fields, read back from what the rule emitted.
type webserviceViaRuleConfig struct {
	*components.DeploymentConfig
	service     *components.ServiceConfig
	synthesized []oam.Trait

	Port                   int32
	TopologySpreadDisabled bool
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
