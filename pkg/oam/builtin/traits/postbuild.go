package traits

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PostBuildHandler handles OAM fluxcd-postbuild traits.
//
// When applied, bundle.PostBuild is set so that the generated Kustomization
// carries a spec.postBuild section for variable substitution. Last writer wins
// when multiple components in the same bundle declare this trait.
type PostBuildHandler struct{}

// CanHandle returns true for the "fluxcd-postbuild" trait type.
func (h *PostBuildHandler) CanHandle(traitType string) bool {
	return traitType == "fluxcd-postbuild"
}

// PropertySchema declares the fluxcd-postbuild trait's user-facing properties.
// `substitute` is an open map of string→string.
func (h *PostBuildHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"substitute": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Literal key/value variables substituted into the Kustomization at post-build."},
		"substituteFrom": {
			Type:        oam.PropertyTypeArray,
			Description: "References to ConfigMaps or Secrets whose keys supply post-build substitution variables.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A ConfigMap or Secret providing post-build substitution variables.",
				Properties: map[string]oam.PropertySchema{
					"kind":     {Type: oam.PropertyTypeString, Required: true, Enum: []any{"ConfigMap", "Secret"}, Description: "Source kind (ConfigMap or Secret)."},
					"name":     {Type: oam.PropertyTypeString, Required: true, Description: "Name of the ConfigMap or Secret to read variables from."},
					"optional": {Type: oam.PropertyTypeBoolean, Description: "Whether a missing source is tolerated instead of failing the build."},
				},
			},
		},
	}
}

// Apply decodes the postBuild properties and sets bundle.PostBuild.
func (h *PostBuildHandler) Apply(trait *oam.Trait, _ *stack.Application, bundle *stack.Bundle) error {
	pb := &stack.PostBuild{}

	if raw, ok := trait.Properties["substitute"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return errors.New("fluxcd-postbuild: 'substitute' must be a map")
		}
		pb.Substitute = make(map[string]string, len(m))
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return errors.Errorf("fluxcd-postbuild: substitute[%q] must be a string", k)
			}
			pb.Substitute[k] = s
		}
	}

	if raw, ok := trait.Properties["substituteFrom"]; ok {
		items, ok := raw.([]any)
		if !ok {
			return errors.New("fluxcd-postbuild: 'substituteFrom' must be a list")
		}
		for i, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				return errors.Errorf("fluxcd-postbuild: substituteFrom[%d] must be an object", i)
			}

			kind, _ := m["kind"].(string)
			if kind == "" {
				return errors.Errorf("fluxcd-postbuild: substituteFrom[%d].kind must be a non-empty string", i)
			}
			if kind != "ConfigMap" && kind != "Secret" {
				return errors.Errorf("fluxcd-postbuild: substituteFrom[%d].kind must be ConfigMap or Secret, got %q", i, kind)
			}

			name, _ := m["name"].(string)
			if name == "" {
				return errors.Errorf("fluxcd-postbuild: substituteFrom[%d].name must be a non-empty string", i)
			}

			optional, _ := m["optional"].(bool)

			pb.SubstituteFrom = append(pb.SubstituteFrom, stack.SubstituteRef{
				Kind:     kind,
				Name:     name,
				Optional: optional,
			})
		}
	}

	if len(pb.Substitute) == 0 && len(pb.SubstituteFrom) == 0 {
		return errors.New("fluxcd-postbuild: at least one of 'substitute' or 'substituteFrom' must be set")
	}

	bundle.PostBuild = pb
	return nil
}
