package components_test

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// ociViaRule presents components.OCIRule as the oam.ComponentHandler the former
// OCIHandler was, so the handler-shaped tests pinning oci's behaviour keep
// running against the production path, one step at a time as the engine takes
// them: the rule's LowerComponent, then OCIRepositoryHandler and
// FluxcdKustomizationHandler for the two components it emits.
//
// It drives the rule without a document, so the component is the only consumer
// of its source and lowers to the same-name pair. The shared case, and the
// routing of authored traits and annotations, are pinned on LowerComponent's
// result (oci_test) and through the engine (kurel's build tests).
type ociViaRule struct{}

func (ociViaRule) CanHandle(componentType string) bool { return componentType == "oci" }

func (ociViaRule) PropertySchema() map[string]oam.PropertySchema {
	return components.OCIRule{}.PropertySchema()
}

func (ociViaRule) ToApplicationConfig(comp *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	res, err := components.OCIRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		return nil, err
	}
	if len(res.Components) != 2 || res.Components[0].Type != "ocirepository" || res.Components[1].Type != "fluxcd-kustomization" {
		return nil, errors.Errorf("oci rule emitted %+v, want an ocirepository and a fluxcd-kustomization", res.Components)
	}
	cfg, err := (&components.OCIRepositoryHandler{}).ToApplicationConfig(&res.Components[0], namespace)
	if err != nil {
		return nil, err
	}
	source, ok := cfg.(*components.OCIRepositoryConfig)
	if !ok {
		return nil, errors.Errorf("ocirepository handler returned %T, want *components.OCIRepositoryConfig", cfg)
	}
	cfg, err = (&components.FluxcdKustomizationHandler{}).ToApplicationConfig(&res.Components[1], namespace)
	if err != nil {
		return nil, err
	}
	kustomization, ok := cfg.(*components.FluxcdKustomizationConfig)
	if !ok {
		return nil, errors.Errorf("fluxcd-kustomization handler returned %T, want *components.FluxcdKustomizationConfig", cfg)
	}
	return &ociViaRuleConfig{source: source, kustomization: kustomization}, nil
}

// ociViaRuleConfig is the ocirepository member's config and the
// fluxcd-kustomization member's, answering as the sibling group does.
type ociViaRuleConfig struct {
	source        *components.OCIRepositoryConfig
	kustomization *components.FluxcdKustomizationConfig
}

func (c *ociViaRuleConfig) ApplyPolicy(p oam.Policy) error {
	if err := c.source.ApplyPolicy(p); err != nil {
		return err
	}
	return c.kustomization.ApplyPolicy(p)
}

func (c *ociViaRuleConfig) SetFluxNamespace(ns string) {
	c.source.SetFluxNamespace(ns)
	c.kustomization.SetFluxNamespace(ns)
}

// Generate generates the members in member order: the OCIRepository, then the
// Kustomization.
func (c *ociViaRuleConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	source, err := c.source.Generate(app)
	if err != nil {
		return nil, err
	}
	kustomization, err := c.kustomization.Generate(app)
	if err != nil {
		return nil, err
	}
	return append(source, kustomization...), nil
}
