package traits

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// FluxCDPatchesHandler handles OAM fluxcd-patches traits.
//
// Strategic-merge or JSON patches are appended to bundle.Patches, which are
// emitted on the generated Kustomization spec.patches field. Multiple components
// in the same bundle may contribute patches — they accumulate rather than overwrite.
type FluxCDPatchesHandler struct{}

// CanHandle returns true for the "fluxcd-patches" trait type.
func (h *FluxCDPatchesHandler) CanHandle(traitType string) bool {
	return traitType == "fluxcd-patches"
}

// Apply decodes the patches property and appends each patch to bundle.Patches.
func (h *FluxCDPatchesHandler) Apply(trait *oam.Trait, _ *stack.Application, bundle *stack.Bundle) error {
	raw, ok := trait.Properties["patches"]
	if !ok {
		return errors.New("fluxcd-patches: required property 'patches' missing")
	}

	items, ok := raw.([]any)
	if !ok {
		return errors.New("fluxcd-patches: 'patches' must be a list")
	}

	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return errors.Errorf("fluxcd-patches: patch[%d] must be an object", i)
		}

		patchStr, _ := m["patch"].(string)
		if patchStr == "" {
			return errors.Errorf("fluxcd-patches: patch[%d].patch must be a non-empty string", i)
		}

		p := stack.Patch{Patch: patchStr}

		if targetRaw, ok := m["target"]; ok {
			t, ok := targetRaw.(map[string]any)
			if !ok {
				return errors.Errorf("fluxcd-patches: patch[%d].target must be an object", i)
			}
			p.Target = &stack.PatchSelector{
				Group:              stringProp(t, "group"),
				Version:            stringProp(t, "version"),
				Kind:               stringProp(t, "kind"),
				Name:               stringProp(t, "name"),
				Namespace:          stringProp(t, "namespace"),
				LabelSelector:      stringProp(t, "labelSelector"),
				AnnotationSelector: stringProp(t, "annotationSelector"),
			}
		}

		bundle.Patches = append(bundle.Patches, p)
	}

	return nil
}

// stringProp returns the string value of key from m, or "" if absent or not a string.
func stringProp(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
