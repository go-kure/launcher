package oam

import (
	"fmt"
	"maps"
	"slices"

	"github.com/go-kure/launcher/pkg/errors"
)

// This file is the authored half of what property_validate.go does for emitted
// elements. Until now nothing checked an AUTHORED component's or trait's property
// map against the schema its handler declares: the document envelope decodes with
// KnownFields(true) (parser.go), but every Properties field is map[string]any, so
// strictness stopped at the envelope and each handler read the keys it knew and
// dropped the rest. A misspelled or stale property was accepted and silently
// discarded — see go-kure/launcher#408, and the seven individual instances
// go-kure/launcher#403 had to patch one key at a time for want of this general check.
//
// It also repairs the document-format lifecycle argument in
// docs/oam/design-gvk.md § Document-Format Lifecycle, which rests its additive test
// on a strictness that did not exist: while an undeclared key was accepted, every
// property ever added to an existing component kind was technically breaking,
// because a document could already have been authoring that key to no effect.

// ValidateAuthoredProperties checks every authored component's and trait's
// properties against the schema declared by whatever will consume them, so a
// property no handler declares is a build error rather than a silent no-op.
//
// Three positions are treated differently, each for a reason:
//
//   - Components and traits are checked. A type with no registered handler and no
//     lowering rule claiming it is passed over, exactly as validateEmittedComponent
//     passes one over: there is no schema to check against. That is not a hole —
//     an unknown type is rejected separately, by validate()'s type allowlists before
//     this runs and by validateSettled once the lowering fixpoint has settled.
//
//   - A custom trait type supplied through --capability-def is passed over for the
//     same reason and is worth naming explicitly: a CapabilityDefinition declares
//     that the type EXISTS, not what properties it accepts. There is no
//     PropertySchemaProvider behind it, so its properties stay unchecked. Closing
//     that needs CapabilityDefinition to carry a property schema of its own, which
//     is a document-format change and not this check's business.
//
//   - Policies are passed over deliberately, and this one is a property of the
//     model rather than a gap. ApplicationPolicy is documented pass-through
//     (types.go), no production code registers a PolicyHandler at all, and a policy
//     is handed to the runtime unchanged. Launcher declares no schema for any policy
//     type, so there is nothing to check a policy's properties against and rejecting
//     an undeclared key there would reject every policy ever written.
//
// Returns the first error in a deterministic order (components in document order,
// each component's own properties before its traits), so a document with several
// problems always reports the same one.
func (t *Transformer) ValidateAuthoredProperties(app *Application) error {
	if app == nil {
		return nil
	}
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		if err := t.validateAuthoredComponent(comp); err != nil {
			return err
		}
		for j := range comp.Traits {
			if err := t.validateAuthoredTrait(comp.Name, &comp.Traits[j]); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateAuthoredComponent is validateEmittedComponent for an authored component:
// same schema lookup, including the fallback to a ComponentLoweringRule claiming the
// type when no terminal handler does, so an authored component of a lowerable type is
// checked against the rule that will consume it.
func (t *Transformer) validateAuthoredComponent(comp *Component) error {
	path := fmt.Sprintf("component %q (type %q): properties", comp.Name, comp.Type)
	if h, ok := t.componentHandlers[comp.Type]; ok {
		return validateAuthoredAgainst(h, comp.Properties, path)
	}
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		return validateAuthoredAgainst(rule, comp.Properties, path)
	}
	return nil
}

// validateAuthoredTrait is validateAuthoredComponent for the trait position. The
// component name is carried into the path because a trait has no name of its own and
// the same trait type may appear on several components.
//
// It differs from the component position in one way: the engine itself reads a
// property off every authored trait, whatever the handler declares — see
// engineTraitProperties.
func (t *Transformer) validateAuthoredTrait(componentName string, trait *Trait) error {
	path := fmt.Sprintf("component %q: trait %q: properties", componentName, trait.Type)
	if h, ok := t.traitHandlers[trait.Type]; ok {
		return validateAuthoredTraitAgainst(h, trait.Properties, path)
	}
	if rule, ok := t.traitLoweringRules[trait.Type]; ok {
		return validateAuthoredTraitAgainst(rule, trait.Properties, path)
	}
	return nil
}

// engineTraitProperties are properties the TRANSFORM ENGINE reads off an authored
// trait directly, independently of the trait's handler. They are legal on every
// trait, so the authored check has to accept them even though most handlers do not
// declare them — otherwise this check rejects documents that build correctly today.
//
// `scope` selects which ClusterProfile capability binding the trait resolves
// against: buildCapabilityKey (transform.go) builds "<type>.<scope>" for EVERY trait
// type, falling back to the bare type key, and both resolveCapability call sites
// (applyTraits, and the lowering fixpoint) go through it. Three handlers — expose,
// ingress and httproute — happen to declare `scope` in their own schema, but for an
// unrelated reason: they use it to disambiguate sub-application names. That
// coincidence is why the gap was invisible until the authored path was checked at
// all. Authoring `scope` on any other trait (`pvc`, `certificate`, …) worked before
// this file existed and must keep working.
//
// The declared type is deliberately string, matching what buildCapabilityKey
// requires and what those three handlers already declare: a non-string `scope` is
// silently ignored today, which is precisely the class of silent drop this check
// exists to eliminate, and rejecting it here makes every trait behave the way expose,
// ingress and httproute already did.
//
// Measured by grepping every non-test file of pkg/oam ITSELF — the engine, not
// pkg/oam/builtin/..., whose `Properties["…"]` hits are handlers reading properties
// they declare — for any string-literal index into a property map. transform.go:986
// is the only one. Add to this map rather than special-casing a call site if that
// ever changes.
var engineTraitProperties = map[string]PropertySchema{
	"scope": {
		Type:        PropertyTypeString,
		Description: "Selects the scoped ClusterProfile capability binding \"<traitType>.<scope>\", falling back to the unscoped binding when no scoped one is declared.",
	},
}

// validateAuthoredTraitAgainst is validateAuthoredAgainst with engineTraitProperties
// folded into the handler's schema.
func validateAuthoredTraitAgainst(handler any, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateAuthoredProperties(withEngineTraitProperties(p.PropertySchema()), props, path)
}

// withEngineTraitProperties returns schema plus any engine-read property it does not
// already declare. The handler's own declaration wins when both describe a key, so a
// handler that documents `scope` for its own purposes (expose, ingress, httproute)
// keeps its own description and constraints.
//
// The handler's map is never mutated — PropertySchema() may return a shared or cached
// map, and writing into it would leak this addition into HandlerSchemas() and every
// other consumer.
func withEngineTraitProperties(schema map[string]PropertySchema) map[string]PropertySchema {
	undeclared := 0
	for key := range engineTraitProperties {
		if _, declared := schema[key]; !declared {
			undeclared++
		}
	}
	if undeclared == 0 {
		return schema
	}
	out := make(map[string]PropertySchema, len(schema)+undeclared)
	maps.Copy(out, schema)
	for key, field := range engineTraitProperties {
		if _, declared := out[key]; !declared {
			out[key] = field
		}
	}
	return out
}

// validateAuthoredAgainst validates props against handler's schema, if handler
// declares one. PropertySchemaProvider is optional at every position, so a handler
// that declares nothing accepts anything — unchanged from what validateEmittedProperties
// allows on the emission side.
func validateAuthoredAgainst(handler any, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateAuthoredProperties(p.PropertySchema(), props, path)
}

// validateAuthoredProperties is validateProperties minus the top-level Required
// sweep: it rejects a key the schema does not declare and checks the shape of every
// key it does, but never reports a declared Required field that the document omits.
//
// Required is deliberately not enforced HERE, and only here — the nested case still
// is, because validatePropertyValue recurses into validateObjectProperties for a
// declared object field and that path is unchanged. The distinction is not
// cosmetic:
//
//   - A trait's top-level properties are not complete at this point. ClusterProfile
//     capability rendering merges into them later (applyTraits → resolveCapability),
//     so a required property the platform supplies is legitimately absent from what
//     the user wrote. Enforcing Required here would reject exactly the
//     capability-aware traits the profile exists to complete — the same spurious
//     "is required" failure recorded against the emitted path in
//     validateEmittedDocument's note on forwarded traits.
//   - A nested object, by contrast, is authored whole: capability rendering merges
//     at the top level of a trait's property map, never inside one of its object
//     values. So a required field of an object the user did write is genuinely
//     missing, and reporting it names the line they wrote.
//   - Nothing is lost at the component top level either. A handler that needs a
//     property already fails without it, with a message written for that property;
//     a second gate here would only change which error surfaces first.
//
// The undeclared-key half is what this check exists for, and it applies at every
// level: nested objects are covered by validateObjectProperties through
// validatePropertyValue, under the same AdditionalProperties rule the emitted path
// uses.
//
// Values are written back for the same reason validateObjectProperties writes them
// back: validatePropertyValue may normalize an array or object into a fresh
// []any/map[string]any, and the handler downstream must see the shape validation
// actually checked.
func validateAuthoredProperties(schema map[string]PropertySchema, props map[string]any, path string) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		field, ok := schema[key]
		if !ok {
			return errors.Errorf("%s: unsupported field %q (allowed: %s)", path, key, declaredFields(schema))
		}
		normalized, err := validatePropertyValue(field, props[key], path+"."+key)
		if err != nil {
			return err
		}
		props[key] = normalized
	}
	return nil
}
