package oam

import (
	"fmt"
	"maps"
	"slices"

	"github.com/go-kure/launcher/pkg/errors"
)

// ValidateAuthoredPropertiesForTarget is ValidateAuthoredPropertiesWithCapabilities
// for a document built for a cluster at target, the evaluated ClusterProfile's
// spec.kubernetesVersion (ClusterProfileSpec.TargetVersion). Everything the
// capability variant checks is checked first and reported first; then each authored
// value whose schema names a Gate (PropertySchema.Gate) that is off by default at
// target is refused, with the property's path, the gate, the release where it turns
// on and the target (go-kure/launcher#1183).
//
// A nil target is an unknown version: no gate is consulted, and the result is the
// capability variant's, so a document built without a version is judged as it was
// before the version existed. The gate pass reads the document and changes nothing.
// Like ValidateAuthoredProperties it returns the validated copy and never edits app.
func (t *Transformer) ValidateAuthoredPropertiesForTarget(app *Application, capabilities map[string]CapabilityBinding, target *KubernetesVersion) (*Application, error) {
	// The gate pass below looks schemas up by plain type name, so it reads the
	// resolved document too (a declared type identity becomes its plain name).
	own, resolved, err := t.validateAuthoredCopy(app, capabilities)
	if err != nil || own == nil {
		return nil, err
	}
	if target == nil {
		return own, nil
	}
	for i := range resolved.Spec.Components {
		comp := &resolved.Spec.Components[i]
		path := fmt.Sprintf("component %q (type %q): properties", comp.Name, comp.Type)
		if err := checkGatedProperties(t.componentSchemaOf(comp.Type), comp.Properties, path, *target); err != nil {
			return nil, err
		}
		for j := range comp.Traits {
			trait := &comp.Traits[j]
			path := fmt.Sprintf("component %q: trait %q: properties", comp.Name, trait.Type)
			if err := checkGatedProperties(t.traitSchemaOf(trait.Type), trait.Properties, path, *target); err != nil {
				return nil, err
			}
		}
	}
	for i := range resolved.Spec.Policies {
		pol := &resolved.Spec.Policies[i]
		path := fmt.Sprintf("policy %q (type %q): properties", pol.Name, pol.Type)
		if err := checkGatedProperties(t.policySchemaOf(pol.Type), pol.Properties, path, *target); err != nil {
			return nil, err
		}
	}
	return own, nil
}

// componentSchemaOf returns the property schema the authored validation checks a
// component of type typ against: the terminal handler's, or the lowering rule's
// when no handler claims the type (validateAuthoredComponent's lookup). nil when
// neither declares one.
func (t *Transformer) componentSchemaOf(typ string) map[string]PropertySchema {
	if h, ok := t.componentHandlers[typ]; ok {
		return schemaOf(h)
	}
	if rule, ok := t.componentLoweringRules[typ]; ok {
		return schemaOf(rule)
	}
	return nil
}

// traitSchemaOf is componentSchemaOf for a trait (validateAuthoredTrait's lookup).
func (t *Transformer) traitSchemaOf(typ string) map[string]PropertySchema {
	if h, ok := t.traitHandlers[typ]; ok {
		return schemaOf(h)
	}
	if rule, ok := t.traitLoweringRules[typ]; ok {
		return schemaOf(rule)
	}
	return nil
}

// policySchemaOf is componentSchemaOf for a policy (validateAuthoredPolicy's lookup).
func (t *Transformer) policySchemaOf(typ string) map[string]PropertySchema {
	if h, ok := t.policyHandlers[typ]; ok {
		return schemaOf(h)
	}
	if rule, ok := t.policyLoweringRules[typ]; ok {
		return schemaOf(rule)
	}
	return nil
}

func schemaOf(handler any) map[string]PropertySchema {
	if p, ok := handler.(PropertySchemaProvider); ok {
		return p.PropertySchema()
	}
	return nil
}

// checkGatedProperties walks props against schema and returns the first authored
// value, in sorted key order, whose Gate is off at target. A key the schema does not
// declare is not its business (the authored validation refused it already), and
// neither is a value of the wrong shape.
func checkGatedProperties(schema map[string]PropertySchema, props map[string]any, path string, target KubernetesVersion) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		field, ok := schema[key]
		if !ok {
			continue
		}
		if err := checkGatedValue(field, props[key], path+"."+key, target); err != nil {
			return err
		}
	}
	return nil
}

func checkGatedValue(field PropertySchema, value any, path string, target KubernetesVersion) error {
	// A null is absent at every depth (the null contract, property_validate.go),
	// so it uses no gate.
	if isNullValue(value) {
		return nil
	}
	if field.Gate != "" {
		on, err := gateEnabled(field.Gate, target, nil)
		if err != nil {
			return err
		}
		if !on {
			g, _ := lookupFeatureGate(field.Gate)
			return errors.Errorf("%s: requires the Kubernetes feature gate %s, which is on by default from %s, "+
				"and the ClusterProfile's kubernetesVersion is %s; remove the field or raise spec.kubernetesVersion",
				path, g.Name, g.OnByDefault, target)
		}
	}
	if obj, ok := asObjectValue(value); ok && len(field.Properties) > 0 {
		return checkGatedProperties(field.Properties, obj, path, target)
	}
	if items, ok := asArrayValue(value); ok && field.Items != nil {
		for i, item := range items {
			if err := checkGatedValue(*field.Items, item, fmt.Sprintf("%s[%d]", path, i), target); err != nil {
				return err
			}
		}
	}
	return nil
}
