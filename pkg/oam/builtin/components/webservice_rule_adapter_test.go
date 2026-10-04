package components_test

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// webserviceViaRule presents components.WebserviceRule as the
// oam.ComponentHandler the former WebserviceHandler was, so the handler-shaped
// tests pinning webservice's behaviour keep running against the production
// path, one step at a time as the engine takes them: the rule's
// LowerComponent, then DeploymentHandler and ServiceHandler for the two
// components it emits, then the synthesized topology-spread and pvc traits
// (traits.TopologySpreadHandler, traits.PVCHandler) on the deployment member
// when the rule attached them. The config it returns is the deployment member's own, so
// ApplyPolicy, ServiceAccountName and NonRWXClaim are DeploymentConfig's; ServicePort and ServicePortName are the service
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
	n := len(res.Components)
	if n < 2 || n > 3 || res.Components[0].Type != "deployment" || res.Components[1].Type != "service" || (n == 3 && res.Components[2].Type != "serviceaccount") {
		return nil, errors.Errorf("webservice rule emitted %d components, want a deployment, a service and at most a serviceaccount", n)
	}
	var sa stack.ApplicationConfig
	if n == 3 {
		if sa, err = (&components.ServiceAccountHandler{}).ToApplicationConfig(&res.Components[2], namespace); err != nil {
			return nil, err
		}
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
	spread := false
	for _, t := range emitted.Traits {
		if t.Type == "topology-spread" || t.Type == "pvc" {
			synthesized = append(synthesized, t)
			spread = spread || t.Type == "topology-spread"
		}
	}
	return &webserviceViaRuleConfig{
		DeploymentConfig:       dep,
		service:                svc,
		serviceAccount:         sa,
		synthesized:            synthesized,
		Port:                   svc.ServicePort(),
		TopologySpreadDisabled: !spread,
	}, nil
}

// webserviceViaRuleConfig is the deployment member's config, the service
// member's, the serviceaccount member's when the rule emitted one, and the
// traits the rule synthesized in front of the deployment member's
// (topology-spread, and one `pvc` trait per claim its pvc volumes describe).
// Port and TopologySpreadDisabled restate the two
// opinions the former WebserviceConfig carried as fields, read back from what
// the rule emitted.
type webserviceViaRuleConfig struct {
	*components.DeploymentConfig
	service        *components.ServiceConfig
	serviceAccount stack.ApplicationConfig
	synthesized    []oam.Trait

	Port                   int32
	TopologySpreadDisabled bool
}

func (c *webserviceViaRuleConfig) ServicePort() int32 { return c.service.ServicePort() }

func (c *webserviceViaRuleConfig) ServicePortName() (string, bool) {
	return c.service.ServicePortName()
}

// Generate generates the members as the sibling group does: each member's
// first object in member order (Deployment, Service, ServiceAccount), then the
// rest of each member's objects, then the claims of the synthesized `pvc`
// traits, which follow their component.
func (c *webserviceViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	inner := stack.NewApplication(app.Name, app.Namespace, c.DeploymentConfig)
	subApps, err := applySynthesizedTraits("webservice", c.synthesized, inner)
	if err != nil {
		return nil, err
	}
	dep, err := inner.Config.Generate(app)
	if err != nil {
		return nil, err
	}
	svc, err := c.service.Generate(app)
	if err != nil {
		return nil, err
	}
	var sa []*client.Object
	if c.serviceAccount != nil {
		if sa, err = c.serviceAccount.Generate(app); err != nil {
			return nil, err
		}
	}
	var objs []*client.Object
	for _, member := range [][]*client.Object{dep, svc, sa} {
		if len(member) > 0 {
			objs = append(objs, member[0])
		}
	}
	for _, member := range [][]*client.Object{dep, svc, sa} {
		if len(member) > 1 {
			objs = append(objs, member[1:]...)
		}
	}
	claims, err := generateSubApplications(subApps)
	if err != nil {
		return nil, err
	}
	return append(objs, claims...), nil
}
