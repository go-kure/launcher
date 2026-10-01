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

// postgresqlViaRule presents components.PostgresqlRule as the
// oam.ComponentHandler the former PostgresqlHandler was, so the handler-shaped
// tests pinning postgresql's behaviour keep running against the production
// path, one step at a time as the engine takes them: the rule's
// LowerComponent, then the CNPG kind handler for each component it emits, the
// policy on each of their configs, then the synthesized
// cnpg-postgresql-defaults trait (traits.PostgresqlDefaultsHandler) on the
// Cluster before Generate.
//
// The config it returns embeds what PostgresqlRule.Parse read, so the tests
// reading parsed fields keep reading them; those fields are not updated by
// ApplyPolicy, which acts on the emitted configs only.
type postgresqlViaRule struct{}

func (postgresqlViaRule) CanHandle(componentType string) bool { return componentType == "postgresql" }

func (postgresqlViaRule) PropertySchema() map[string]oam.PropertySchema {
	return components.PostgresqlRule{}.PropertySchema()
}

func (postgresqlViaRule) Endpoints(comp *oam.Component) ([]netpol.Endpoint, error) {
	return components.PostgresqlRule{}.Endpoints(comp)
}

func (postgresqlViaRule) ToApplicationConfig(comp *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	parsed, err := components.PostgresqlRule{}.Parse(comp)
	if err != nil {
		return nil, err
	}
	res, err := components.PostgresqlRule{}.LowerComponent(comp, oam.LoweringContext{Namer: oam.NewNameAllocator()})
	if err != nil {
		return nil, err
	}
	handlers := map[string]oam.ComponentHandler{
		"cnpg-cluster":     &components.CnpgClusterHandler{},
		"cnpg-objectstore": &components.CnpgObjectStoreHandler{},
		"cnpg-pooler":      &components.CnpgPoolerHandler{},
		"cnpg-database":    &components.CnpgDatabaseHandler{},
	}
	out := &postgresqlViaRuleConfig{PostgresqlConfig: parsed}
	for i := range res.Components {
		emitted := res.Components[i]
		h, ok := handlers[emitted.Type]
		if !ok {
			return nil, errors.Errorf("postgresql rule emitted an unexpected %q component", emitted.Type)
		}
		cfg, err := h.ToApplicationConfig(&emitted, namespace)
		if err != nil {
			return nil, err
		}
		m := postgresqlMember{name: emitted.Name, config: cfg}
		if emitted.Type == "cnpg-cluster" {
			m.synthesized = emitted.Traits[:len(emitted.Traits)-len(comp.Traits)]
		}
		out.members = append(out.members, m)
	}
	return out, nil
}

// postgresqlViaRuleConfig is the configs of the components the rule emitted, in
// emission order, generated as one component.
type postgresqlViaRuleConfig struct {
	*components.PostgresqlConfig
	members []postgresqlMember
}

type postgresqlMember struct {
	name        string
	config      stack.ApplicationConfig
	synthesized []oam.Trait
}

// cluster is the Cluster component's config.
func (c *postgresqlViaRuleConfig) cluster() *components.CnpgClusterConfig {
	return c.members[0].config.(*components.CnpgClusterConfig)
}

func (c *postgresqlViaRuleConfig) ApplyPolicy(p oam.Policy) error {
	for _, m := range c.members {
		if e, ok := m.config.(oam.Enforceable); ok {
			if err := e.ApplyPolicy(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// Generate generates every member under its own name; the first, the Cluster,
// is named by app as the rule names it after the component.
func (c *postgresqlViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	var objs []*client.Object
	for i, m := range c.members {
		name := m.name
		if i == 0 {
			name = app.Name
		}
		cfg := m.config
		// The trait sets the Cluster's defaults on the config it is handed, so
		// it gets a copy: a second Generate starts from the same config, as
		// the engine's single transform does.
		if cc, ok := cfg.(*components.CnpgClusterConfig); ok && len(m.synthesized) > 0 {
			cp := *cc
			cfg = &cp
		}
		inner := stack.NewApplication(name, app.Namespace, cfg)
		for j := range m.synthesized {
			if m.synthesized[j].Type != "cnpg-postgresql-defaults" {
				return nil, errors.Errorf("postgresql rule synthesized an unexpected %q trait", m.synthesized[j].Type)
			}
			if err := (&traits.PostgresqlDefaultsHandler{}).Apply(&m.synthesized[j], inner, nil); err != nil {
				return nil, err
			}
		}
		got, err := inner.Config.Generate(inner)
		if err != nil {
			return nil, err
		}
		objs = append(objs, got...)
	}
	return objs, nil
}
