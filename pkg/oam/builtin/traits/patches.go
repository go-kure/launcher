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

// PropertySchema declares the fluxcd-patches trait's user-facing properties. Each
// patch carries a strategic-merge/JSON6902 body plus an optional kustomize target
// selector. Both the patch item and its target selector are closed objects: only the
// enumerated fields are accepted and unknown keys are rejected.
func (h *FluxCDPatchesHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"patches": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Strategic-merge or JSON6902 patches applied to the generated Kustomization.",
			Items: &oam.PropertySchema{
				Type:                 oam.PropertyTypeObject,
				AdditionalProperties: false,
				Description:          "A single patch with its body and optional target selector.",
				Properties: map[string]oam.PropertySchema{
					"patch": {Type: oam.PropertyTypeString, Required: true, Description: "Patch body as a strategic-merge YAML or JSON6902 document."},
					"target": {
						Type:                 oam.PropertyTypeObject,
						AdditionalProperties: false,
						Description:          "Kustomize target selector restricting which resources the patch applies to.",
						Properties: map[string]oam.PropertySchema{
							"group":              {Type: oam.PropertyTypeString, Description: "API group of the resources to patch."},
							"version":            {Type: oam.PropertyTypeString, Description: "API version of the resources to patch."},
							"kind":               {Type: oam.PropertyTypeString, Description: "Kind of the resources to patch."},
							"name":               {Type: oam.PropertyTypeString, Description: "Name of the resource to patch."},
							"namespace":          {Type: oam.PropertyTypeString, Description: "Namespace of the resources to patch."},
							"labelSelector":      {Type: oam.PropertyTypeString, Description: "Label selector restricting the resources to patch."},
							"annotationSelector": {Type: oam.PropertyTypeString, Description: "Annotation selector restricting the resources to patch."},
						},
					},
				},
			},
		},
	}
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
			sel := &stack.PatchSelector{}
			fields := []struct {
				key string
				dst *string
			}{
				{"group", &sel.Group},
				{"version", &sel.Version},
				{"kind", &sel.Kind},
				{"name", &sel.Name},
				{"namespace", &sel.Namespace},
				{"labelSelector", &sel.LabelSelector},
				{"annotationSelector", &sel.AnnotationSelector},
			}
			for _, f := range fields {
				v, err := strictTargetString(t, f.key, i)
				if err != nil {
					return err
				}
				*f.dst = v
			}
			p.Target = sel
		}

		bundle.Patches = append(bundle.Patches, p)
	}

	return nil
}

// strictTargetString returns the string value of key from t.
// Returns an error if the value is present but not a string.
func strictTargetString(t map[string]any, key string, idx int) (string, error) {
	v, ok := t[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.Errorf("fluxcd-patches: patch[%d].target.%s: expected string, got %T", idx, key, v)
	}
	return s, nil
}
