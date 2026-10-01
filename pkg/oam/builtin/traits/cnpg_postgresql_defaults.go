package traits

import (
	"maps"
	"slices"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// PostgresqlDefaultsHandler handles the engine-only cnpg-postgresql-defaults
// trait. The postgresql lowering rule attaches it to the cnpg-cluster component
// it emits, ahead of the authored traits; a document may not author it (it is
// registered with oam.Transformer.RegisterEngineTrait).
//
// It sets on the Cluster the two values postgresql derived from the environment
// policy, which a lowering rule cannot see: spec.enablePDB from the instance
// count after the policy, and postgresql's 1Gi storage fallback under the
// policy's maximum (components.CnpgClusterConfig.ApplyPostgresqlDefaults). The
// transformer applies the policy to the component config before any trait
// (pkg/oam transform.go, createApplications then applyTraits), so Apply sees
// the count and size the policy decided.
type PostgresqlDefaultsHandler struct{}

// CanHandle returns true for the "cnpg-postgresql-defaults" trait type.
func (h *PostgresqlDefaultsHandler) CanHandle(traitType string) bool {
	return traitType == "cnpg-postgresql-defaults"
}

// PropertySchema declares the trait's properties: none.
func (h *PostgresqlDefaultsHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

// Apply sets the postgresql defaults on the component's Cluster config. Any
// property other than the engine-owned ones is refused, as on the other
// property-less traits. A component whose config is not a cnpg-cluster's is
// refused: the trait would otherwise do nothing.
func (h *PostgresqlDefaultsHandler) Apply(trait *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	if trait != nil {
		for _, key := range slices.Sorted(maps.Keys(trait.Properties)) {
			if !oam.IsEngineTraitProperty(key) {
				return errors.Errorf("cnpg-postgresql-defaults: unknown property %q; the trait takes no properties", key)
			}
		}
	}
	cfg, ok := app.Config.(*components.CnpgClusterConfig)
	if !ok {
		return errors.Errorf("cnpg-postgresql-defaults: component %q is not a cnpg-cluster (config %T)", app.Name, app.Config)
	}
	return cfg.ApplyPostgresqlDefaults()
}
