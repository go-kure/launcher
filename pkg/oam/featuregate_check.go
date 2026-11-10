package oam

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
		if err := refuseGateBelow(field.Gate, path, target); err != nil {
			return err
		}
	}
	if err := checkGatedPaths(field.GatedPaths, value, path, target); err != nil {
		return err
	}
	if err := checkValueGatedPaths(field.ValueGatedPaths, value, path, target); err != nil {
		return err
	}
	if len(field.ValueGates) > 0 {
		if err := refuseValueGateBelow(field.ValueGates, value, path, target); err != nil {
			return err
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

// refuseGateBelow refuses the authored value at path when gate is off by default at
// target, naming the path, the gate, the release where it turns on and the target.
func refuseGateBelow(gate, path string, target KubernetesVersion) error {
	on, err := gateEnabled(gate, target, nil)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	g, _ := lookupFeatureGate(gate)
	return errors.Errorf("%s: requires the Kubernetes feature gate %s, which is on by default from %s, "+
		"and the ClusterProfile's kubernetesVersion is %s; remove the field or raise spec.kubernetesVersion",
		path, g.Name, g.OnByDefault, target)
}

// refuseValueGateBelow refuses value at path when it is a string that gates names and
// that gate is off by default at target, naming the path, the value, the gate, the
// release where it turns on and the target. Any other value is accepted: a value of
// another type, and a string no key names. The match is exact, as the Enum a
// ValueGates key is held to is (go-kure/launcher#1230).
func refuseValueGateBelow(gates map[string]string, value any, path string, target KubernetesVersion) error {
	s, ok := value.(string)
	if !ok {
		return nil
	}
	gate, gated := gates[s]
	if !gated {
		return nil
	}
	on, err := gateEnabled(gate, target, nil)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	g, _ := lookupFeatureGate(gate)
	return errors.Errorf("%s: value %q requires the Kubernetes feature gate %s, which is on by default from %s, "+
		"and the ClusterProfile's kubernetesVersion is %s; use another value or raise spec.kubernetesVersion",
		path, s, g.Name, g.OnByDefault, target)
}

// checkGatedPaths refuses the first authored value, in sorted path order, that sits at
// one of paths (PropertySchema.GatedPaths) below value and whose gate is off at target.
// A path whose key is absent, null or of the wrong shape on the way is not its
// business, as in checkGatedProperties.
func checkGatedPaths(paths map[string]string, value any, path string, target KubernetesVersion) error {
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		gate := paths[p]
		leaf := func(_ any, at string) error { return refuseGateBelow(gate, at, target) }
		if err := walkGatedPath(strings.Split(p, "."), value, path, leaf); err != nil {
			return err
		}
	}
	return nil
}

// checkValueGatedPaths refuses the first authored value, in sorted path order, that
// sits at one of paths (PropertySchema.ValueGatedPaths) below value, is a string that
// the path's own table names, and whose gate is off at target (go-kure/launcher#1330).
// The walk is checkGatedPaths's, so a key matches case-insensitively and the path the
// refusal names carries the authored spelling. The value match is exact. A null, a
// value that is not a string and a string the table does not name use no gate.
func checkValueGatedPaths(paths map[string]map[string]string, value any, path string, target KubernetesVersion) error {
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		gates := paths[p]
		leaf := func(v any, at string) error { return refuseValueGateBelow(gates, v, at, target) }
		if err := walkGatedPath(strings.Split(p, "."), value, path, leaf); err != nil {
			return err
		}
	}
	return nil
}

// walkGatedPath walks the keys of one gated path down value and calls leaf with the
// value found at its end and the path that names it; a key ending in "[]" names an
// array whose every element is walked further. A key matches the authored keys
// case-insensitively (foldedKeys), and the path it builds names the spelling the author
// wrote. A null value ends the walk with no call.
func walkGatedPath(keys []string, value any, path string, leaf func(value any, path string) error) error {
	if isNullValue(value) {
		return nil
	}
	if len(keys) == 0 {
		return leaf(value, path)
	}
	obj, ok := asObjectValue(value)
	if !ok {
		return nil
	}
	key, each := strings.CutSuffix(keys[0], "[]")
	for _, authored := range foldedKeys(obj, key) {
		next := obj[authored]
		here := path + "." + authored
		if !each {
			if err := walkGatedPath(keys[1:], next, here, leaf); err != nil {
				return err
			}
			continue
		}
		items, ok := asArrayValue(next)
		if !ok {
			continue
		}
		for i, item := range items {
			if err := walkGatedPath(keys[1:], item, fmt.Sprintf("%s[%d]", here, i), leaf); err != nil {
				return err
			}
		}
	}
	return nil
}

// foldedKeys returns, in sorted order, every key of obj that equals key ignoring case.
// An open object is decoded by encoding/json, which matches field names
// case-insensitively, so `HostnameOverride` sets the field `hostnameOverride` does
// (go-kure/launcher#1245). The document may spell one field twice, and which spelling
// the decode keeps is not the author's order, so the check walks all of them: a
// refusal then names an authored spelling whichever one the decode keeps.
func foldedKeys(obj map[string]any, key string) []string {
	var out []string
	for k := range obj {
		if strings.EqualFold(k, key) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
