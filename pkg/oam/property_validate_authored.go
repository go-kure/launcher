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

// ValidateAuthoredProperties checks every authored component's, trait's and
// policy's properties against the schema declared by whatever will consume them,
// so a property no handler declares is a build error rather than a silent no-op.
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
//     PropertySchemaProvider behind it, so its handler-owned properties stay
//     unchecked. Closing that needs CapabilityDefinition to carry a property schema
//     of its own, which is a document-format change and not this check's business.
//     When such a trait is served by a registered handler or lowering rule that
//     declares no schema, the engine-owned keys (engineTraitProperties) are still
//     checked: they are not the handler's to declare — see
//     validateAuthoredTraitAgainst. A type with no registered handler or rule is
//     passed over entirely, per the first bullet.
//
//   - A policy is checked against the schema of the PolicyHandler registered for
//     its type, or of the PolicyLoweringRule claiming it — the same lookup
//     validateEmittedPolicy uses. The built-in policy handlers kurel registers
//     (pkg/oam/builtin/policies) each declare one, so a misspelt or wrongly typed
//     key on a `dependency` or `placement`
//     policy is a build error rather than a setting the handler silently never
//     reads. A handler that declares no schema accepts anything, as at the other
//     positions, and a policy type with nothing registered for it is passed over
//     here: the transform rejects it ("no handler for policy type").
//
// Returns the first error in a deterministic order (components in document order,
// each component's own properties before its traits, then policies in document
// order), so a document with several problems always reports the same one.
//
// It knows no ClusterProfile, so it enforces nested Required on every trait as
// written: a required key missing from a nested object the author wrote is refused
// here even when a capability rendering would supply it. An object the author
// leaves out entirely is not refused, whatever its own Required flag says, as at the
// top level. ValidateAuthoredPropertiesWithCapabilities is the profile-aware
// variant: the rendering merges into nested objects (mergeRenderedProperties,
// go-kure/launcher#750), and the variant accepts a partial nested override that
// relies on it for a required sibling.
func (t *Transformer) ValidateAuthoredProperties(app *Application) error {
	return t.ValidateAuthoredPropertiesWithCapabilities(app, nil)
}

// ValidateAuthoredPropertiesWithCapabilities is ValidateAuthoredProperties for a
// document built against capabilities, the evaluated ClusterProfile's bindings
// (EvaluateProfile). A trait a binding matches, by the lookup resolveCapability makes
// on the properties once validated, is checked for nested Required on its properties
// as the rendering merges into them, not as written: the rendering may supply a
// required key the author left out (go-kure/launcher#765). Every other check is
// ValidateAuthoredProperties', and a trait no binding matches is refused for the
// same nested key, though with several problems the nested Required one may be
// reported after the others. Transform makes the same after-merge check itself, so
// a nested key neither side supplies is refused there too.
func (t *Transformer) ValidateAuthoredPropertiesWithCapabilities(app *Application, capabilities map[string]CapabilityBinding) error {
	if app == nil {
		return nil
	}
	if err := t.enforceDocumentReservations(app); err != nil {
		return err
	}
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		if err := t.validateAuthoredComponent(comp); err != nil {
			return err
		}
		for j := range comp.Traits {
			if err := t.validateAuthoredTrait(comp.Name, &comp.Traits[j], capabilities); err != nil {
				return err
			}
		}
	}
	for i := range app.Spec.Policies {
		if err := t.validateAuthoredPolicy(&app.Spec.Policies[i]); err != nil {
			return err
		}
	}
	return nil
}

// enforceDocumentReservations runs Transform's first D3 check (enforcePlatformReserved)
// on app before ValidateAuthoredProperties validates it. It has to come first:
// validation normalizes an explicit null under a nested declared object to absence
// (validateObjectProperties), so a reserved key authored as {"config":{"locked":null}}
// would be gone before Transform's own check could see it, and the document would be
// accepted (go-kure/launcher#635).
//
// It reports exactly what Transform reports for a document whose only defect is one
// reserved key; with several, Transform may meet another one first (an ordered
// build applies traits group by group). With lowering rules registered, that is the check at the start of the
// first lowering round (enforceAuthoredReservations, before any rule ran, so the
// LoweringError carries no chain); a non-terminal kind no rule claims is passed over,
// as lowerDocumentOnce passes it over. With none, it is createApplications' component
// check and then applyTraits' trait check, in that order, against the handlers they
// dispatch to. Policies reserve nothing on either path.
func (t *Transformer) enforceDocumentReservations(app *Application) error {
	if t.hasLoweringRules() {
		if app.Kind != terminalDocumentKind {
			if _, ok := t.docLoweringRules[app.Kind]; !ok {
				return nil
			}
		}
		// lower seeds the LoweringError's origin from the metadata, while the round's
		// own check names elements from the document's stamped origin when it has one.
		seed := Origin{Document: app.Metadata.Name, DocumentKind: app.Kind, Namespace: app.Metadata.Namespace}
		docOrigin, _ := app.Origin()
		if docOrigin == (Origin{}) {
			docOrigin = seed
		}
		if err := t.enforceAuthoredReservations(app, docOrigin); err != nil {
			return &LoweringError{Origin: seed, Cause: err}
		}
		return nil
	}
	// The exemptions are createApplications' and applyTraits': a synthesized
	// component, and a sealed trait a checked rule emitted.
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		if comp.synthesized {
			continue
		}
		if err := t.enforceComponentReservations(comp, "properties"); err != nil {
			return &TransformError{Message: fmt.Sprintf("component %q", comp.Name), Cause: err}
		}
	}
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		for j := range comp.Traits {
			if comp.Traits[j].sealed && comp.Traits[j].synthesized {
				continue
			}
			if err := t.enforceTraitReservations(&comp.Traits[j], "properties"); err != nil {
				return &TransformError{Message: fmt.Sprintf("component %q trait %q", comp.Name, comp.Traits[j].Type), Cause: err}
			}
		}
	}
	return nil
}

// validateAuthoredPolicy is validateAuthoredComponent for the policy position, with
// validateEmittedPolicy's lookup: the registered PolicyHandler first, then a
// PolicyLoweringRule claiming the type. Top-level Required is not enforced, exactly
// as for components (see validateAuthoredProperties): each built-in handler already
// rejects a missing property it needs, with a message written for that property.
func (t *Transformer) validateAuthoredPolicy(pol *ApplicationPolicy) error {
	path := fmt.Sprintf("policy %q (type %q): properties", pol.Name, pol.Type)
	if h, ok := t.policyHandlers[pol.Type]; ok {
		return validateAuthoredAgainst(h, pol.Properties, path)
	}
	if rule, ok := t.policyLoweringRules[pol.Type]; ok {
		return validateAuthoredAgainst(rule, pol.Properties, path)
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
		return withUnsupportedFieldHint(h, validateAuthoredComponentAgainst(h, comp.Properties, path))
	}
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		return validateAuthoredAgainst(rule, comp.Properties, path)
	}
	return nil
}

// validateAuthoredComponentAgainst is validateAuthoredAgainst for a terminal
// component handler: a kind component's schema is checked with `objectName`
// folded in, which the engine reads off the component before the handler sees
// it (withObjectNameProperty). A handler that declares no schema accepts
// anything, `objectName` included; the transform still refuses it there on a
// type that takes none.
func validateAuthoredComponentAgainst(handler ComponentHandler, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return validateAuthoredProperties(withObjectNameProperty(handler, p.PropertySchema()), props, path)
}

// unsupportedFieldHinter is implemented by a component handler that adds a
// hint to the refusal of an undeclared top-level key: the helmchart terminal
// points a key of the composite that used to carry its name to `helm`. "" adds
// nothing.
type unsupportedFieldHinter interface {
	UnsupportedFieldHint(key string) string
}

// unsupportedFieldError is validateAuthoredProperties' refusal of an undeclared
// top-level key.
type unsupportedFieldError struct {
	path, key, allowed string
}

func (e *unsupportedFieldError) Error() string {
	return fmt.Sprintf("%s: unsupported field %q (allowed: %s)", e.path, e.key, e.allowed)
}

// withUnsupportedFieldHint appends handler's hint to err when err refuses an
// undeclared top-level key and handler has a hint for that key. Any other err
// is returned unchanged.
func withUnsupportedFieldHint(handler any, err error) error {
	h, ok := handler.(unsupportedFieldHinter)
	var uerr *unsupportedFieldError
	if !ok || !errors.As(err, &uerr) {
		return err
	}
	if hint := h.UnsupportedFieldHint(uerr.key); hint != "" {
		return errors.Errorf("%w; %s", err, hint)
	}
	return err
}

// validateAuthoredTrait is validateAuthoredComponent for the trait position. The
// component name is carried into the path because a trait has no name of its own and
// the same trait type may appear on several components.
//
// It differs from the component position in one way: the engine itself reads a
// property off every authored trait, whatever the handler declares — see
// engineTraitProperties.
//
// Given capabilities, a trait whose handler declares a schema is checked against it
// with nested Required cleared (relaxObjectRequired), and then for nested Required on
// the properties merged as resolveCapability merges them (checkNestedRequired), so a
// required key the rendering supplies is not refused (go-kure/launcher#765). A trait
// no binding matches is checked on its properties as written.
func (t *Transformer) validateAuthoredTrait(componentName string, trait *Trait, capabilities map[string]CapabilityBinding) error {
	path := fmt.Sprintf("component %q: trait %q: properties", componentName, trait.Type)
	var handler any
	if h, ok := t.traitHandlers[trait.Type]; ok {
		handler = h
	} else if rule, ok := t.traitLoweringRules[trait.Type]; ok {
		handler = rule
	} else {
		return nil
	}
	p, declares := handler.(PropertySchemaProvider)
	if !declares || len(capabilities) == 0 {
		return validateAuthoredTraitAgainst(handler, trait.Properties, path)
	}
	schema := withEngineTraitProperties(p.PropertySchema())
	if err := validateAuthoredProperties(relaxObjectRequired(schema), trait.Properties, path); err != nil {
		return err
	}
	// Looked up only after validation, which normalizes what the author wrote: a scope
	// of a named string type matches its binding here, as it does in Transform.
	merged, matchedKey, _ := resolveCapability(*trait, capabilities)
	return checkNestedRequired(schema, merged.Properties, path, matchedKey)
}

// relaxObjectRequired returns schema with Required cleared on every field of an
// object reached from the top level through object fields alone, the objects a
// capability rendering merges into. The top level keeps its flags (authored
// validation ignores them), and so does everything under an array's Items: a list
// is authored whole, never merged into, so a required key of one of its elements is
// genuinely missing when the author leaves it out. schema is never mutated, as in
// withEngineTraitProperties.
func relaxObjectRequired(schema map[string]PropertySchema) map[string]PropertySchema {
	out := make(map[string]PropertySchema, len(schema))
	for key, field := range schema {
		if field.Type == PropertyTypeObject && len(field.Properties) > 0 {
			field.Properties = relaxObjectRequired(field.Properties)
			for k, sub := range field.Properties {
				sub.Required = false
				field.Properties[k] = sub
			}
		}
		out[key] = field
	}
	return out
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
// requires and what all three of those handlers already declare in their own schemas
// (expose_rule.go:144, ingress.go:130, httproute.go:111). What this adds is
// enforcement of that declaration on the AUTHORED path. A non-string `scope` written
// on an `ingress` or `httproute` trait was silently ignored before — both read it
// with a comma-ok assertion (ingress.go:192, httproute.go:154), so a `scope: 3` built
// clean and resolved the unscoped binding — which is precisely the class of silent
// drop this check exists to eliminate. Only `expose` was already rejected, and only
// indirectly: it lowers into `ingress`/`httproute`, so the emitted-path check caught
// the non-string after lowering (`emitted trait "ingress": properties.scope:
// expected string, got int`) rather than at the line the author wrote.
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

// IsEngineTraitProperty reports whether key is one of engineTraitProperties: a
// property the transform engine reads off every trait itself, legal on every trait
// whatever its handler declares. A handler that refuses keys it does not read must
// let these through — the engine consumes them before Apply (buildCapabilityKey),
// so refusing one there would reject a document the authored check accepted.
func IsEngineTraitProperty(key string) bool {
	_, ok := engineTraitProperties[key]
	return ok
}

// validateAuthoredTraitAgainst is validateAuthoredAgainst with engineTraitProperties
// folded into the handler's schema.
//
// Engine-owned keys are checked whether or not the handler declares a schema
// (go-kure/launcher#426). They are not the handler's to declare, so a handler that
// implements no PropertySchemaProvider cannot opt them out of their type: without
// this, a trait registered through RegisterTrait or RegisterTraitLowering with no
// schema accepted `scope: 3`, which buildCapabilityKey then ignored, resolving the
// unscoped capability binding with no diagnostic. The handler's own keys stay
// unchecked in that case — there is nothing to check them against.
func validateAuthoredTraitAgainst(handler any, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return validateEngineTraitProperties(props, path)
	}
	return validateAuthoredProperties(withEngineTraitProperties(p.PropertySchema()), props, path)
}

// validateEngineTraitProperties checks only the engineTraitProperties keys present
// in props, for a trait whose handler declares no schema. Any other key is left
// alone. Values are written back exactly as validateAuthoredProperties writes them
// back, and in the same sorted order, so a document with several problems always
// reports the same one.
func validateEngineTraitProperties(props map[string]any, path string) error {
	for _, key := range slices.Sorted(maps.Keys(engineTraitProperties)) {
		value, present := props[key]
		if !present {
			continue
		}
		normalized, err := validatePropertyValue(engineTraitProperties[key], value, path+"."+key)
		if err != nil {
			return err
		}
		props[key] = normalized
	}
	return nil
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
// Required is deliberately not enforced HERE, at the top level — the nested case
// is enforced by whatever schema the caller passes, because validatePropertyValue
// recurses into validateObjectProperties for a declared object field. The
// distinction is not cosmetic:
//
//   - A trait's top-level properties are not complete at this point. ClusterProfile
//     capability rendering merges into them later (applyTraits → resolveCapability),
//     so a required property the platform supplies is legitimately absent from what
//     the user wrote. Enforcing Required here would reject exactly the
//     capability-aware traits the profile exists to complete — the same spurious
//     "is required" failure recorded against the emitted path in
//     validateEmittedDocument's note on forwarded traits.
//   - A nested object's Required is checked here, as written, when no capability
//     binding matches the trait: nothing will merge into it, so a required field of
//     an object the user wrote is genuinely missing, and reporting it names the line
//     they wrote. When a binding matches, the rendering merges into nested objects
//     too (mergeRenderedProperties, go-kure/launcher#750), so validateAuthoredTrait
//     passes a schema with nested Required cleared (relaxObjectRequired) and checks
//     the merged properties instead (checkNestedRequired, go-kure/launcher#765), as
//     applyTraits and the lowering fixpoint do after their own merge.
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
			return &unsupportedFieldError{path: path, key: key, allowed: declaredFields(schema)}
		}
		normalized, err := validatePropertyValue(field, props[key], path+"."+key)
		if err != nil {
			return err
		}
		props[key] = normalized
	}
	return nil
}
