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
// here even when a capability rendering would supply it. An optional object the
// author leaves out entirely is not checked, and a Required one is refused as a
// missing key, as at the top level. ValidateAuthoredPropertiesWithCapabilities is the profile-aware
// variant: the rendering merges into nested objects (mergeRenderedProperties,
// go-kure/launcher#750), and the variant accepts a partial nested override that
// relies on it for a required sibling.
//
// It returns the document it validated: a copy of app whose property trees carry
// what validation settled on (a nested null stripped, a named scalar or a typed
// collection normalized to the plain Go shape a handler reads). app itself is never
// edited. Hand the returned document to Transform: Transform does not strip or
// normalize authored properties itself, so the copy is the only form of the
// document that has been through the strip (go-kure/launcher#635, go-kure/launcher#1282). On an
// error the document is nil, and app is as it was authored.
func (t *Transformer) ValidateAuthoredProperties(app *Application) (*Application, error) {
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
// a nested key neither side supplies is refused there too. Like
// ValidateAuthoredProperties it returns the validated copy and never edits app.
func (t *Transformer) ValidateAuthoredPropertiesWithCapabilities(app *Application, capabilities map[string]CapabilityBinding) (*Application, error) {
	own, _, err := t.validateAuthoredCopy(app, capabilities)
	return own, err
}

// validateAuthoredCopy validates a private copy of app and returns it, with the same
// copy resolved to plain type names. The two share their property maps, so what
// validation writes into the resolved form lands in the copy, whose Type fields
// stay as authored. app is not edited. A nil app is nil, nil, nil. On an error the
// copy is not returned.
func (t *Transformer) validateAuthoredCopy(app *Application, capabilities map[string]CapabilityBinding) (own, resolved *Application, err error) {
	if app == nil {
		return nil, nil, nil
	}
	own = copyAuthoredDocument(app)
	resolved, err = t.resolveAuthoredTypes(own)
	if err != nil {
		return nil, nil, err
	}
	if err := t.validateResolvedAuthored(resolved, capabilities); err != nil {
		return nil, nil, err
	}
	return own, resolved, nil
}

// copyAuthoredDocument returns app with its own component, trait and policy slices
// and its own copy of every property tree (copyPropertyMap), which is everything
// authored validation writes. Everything else is shared with app and only read,
// a pointer or a struct value in a property tree included: validation's own code reads
// through either and writes through neither, for a value under an open object
// (TestValidateAuthoredProperties_NeverWritesThroughAPointerOrStruct). A formatting
// method that a value supplies (String, Error or Format) runs when an error message
// formats the value, and that method is outside this promise. So the sharing concerns a caller who edits
// the returned copy, which the README states (go-kure/launcher#1365).
func copyAuthoredDocument(app *Application) *Application {
	out := *app
	out.Spec.Components = slices.Clone(app.Spec.Components)
	for i := range out.Spec.Components {
		comp := &out.Spec.Components[i]
		comp.Properties = copyPropertyMap(comp.Properties)
		comp.Traits = slices.Clone(comp.Traits)
		for j := range comp.Traits {
			comp.Traits[j].Properties = copyPropertyMap(comp.Traits[j].Properties)
		}
	}
	out.Spec.Policies = slices.Clone(app.Spec.Policies)
	for i := range out.Spec.Policies {
		out.Spec.Policies[i].Properties = copyPropertyMap(out.Spec.Policies[i].Properties)
	}
	return &out
}

// resolveAuthoredTypes returns app with every declared type identity (configmap@v1)
// replaced by the plain name its handler is registered under, as Transform does on
// entry; the Type fields of app are not changed. The authored checks look handlers
// up by that plain name, so they run on the result. The property maps are shared
// with app, so validation writes into them as it does for a plain-name component:
// the caller passes it a copy (validateAuthoredCopy). A reference that does not
// resolve is a ValidationError whose Cause is the resolver's error, so errors.Is
// sees through it.
func (t *Transformer) resolveAuthoredTypes(app *Application) (*Application, error) {
	resolved, err := t.resolveTypes(app)
	if err != nil {
		return nil, typeValidationError(err)
	}
	return resolved, nil
}

// validateResolvedAuthored is the body ValidateAuthoredPropertiesWithCapabilities
// and ValidateAuthoredPropertiesForTarget share, on an app resolveAuthoredTypes
// has already resolved.
func (t *Transformer) validateResolvedAuthored(app *Application, capabilities map[string]CapabilityBinding) error {
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
		if err := t.enforceComponentReservations(comp, "properties", PropertyError{Position: "component", Component: comp.Name}); err != nil {
			return &TransformError{Message: fmt.Sprintf("component %q", comp.Name), Cause: err}
		}
	}
	for i := range app.Spec.Components {
		comp := &app.Spec.Components[i]
		for j := range comp.Traits {
			if comp.Traits[j].sealed && comp.Traits[j].synthesized {
				continue
			}
			if err := t.enforceTraitReservations(&comp.Traits[j], "properties", PropertyError{Position: "trait", Component: comp.Name, Trait: comp.Traits[j].Type}); err != nil {
				return &TransformError{Message: fmt.Sprintf("component %q trait %q", comp.Name, comp.Traits[j].Type), Cause: err}
			}
		}
	}
	return nil
}

// validateAuthoredPolicy is validateAuthoredComponent for the policy position, with
// validateEmittedPolicy's lookup: the registered PolicyHandler first, then a
// PolicyLoweringRule claiming the type. Top-level Required is enforced as written,
// as for a component (see validateAuthoredProperties): a policy has no capability
// merge that could supply a key.
func (t *Transformer) validateAuthoredPolicy(pol *ApplicationPolicy) error {
	path := fmt.Sprintf("policy %q (type %q): properties", pol.Name, pol.Type)
	return locate(t.validateAuthoredPolicyAt(pol, path), path, PropertyError{Position: "policy", Policy: pol.Name})
}

func (t *Transformer) validateAuthoredPolicyAt(pol *ApplicationPolicy, path string) error {
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
	return locate(t.validateAuthoredComponentAt(comp, path), path, PropertyError{Position: "component", Component: comp.Name})
}

func (t *Transformer) validateAuthoredComponentAt(comp *Component, path string) error {
	if h, ok := t.componentHandlers[comp.Type]; ok {
		return withUnsupportedFieldHint(h, validateAuthoredComponentAgainst(h, comp.Properties, path))
	}
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		return withUnsupportedFieldHint(rule, validateAuthoredAgainst(rule, comp.Properties, path))
	}
	return nil
}

// validateAuthoredComponentAgainst is validateAuthoredAgainst for a terminal
// component handler: a kind component's schema is checked with `objectName`,
// `labels` and `annotations` folded in, which the engine reads off the
// component before the handler sees them (withObjectProperties). A handler that
// declares no schema accepts anything, `objectName` included; the transform
// still refuses it there on a type that takes none.
func validateAuthoredComponentAgainst(handler ComponentHandler, props map[string]any, path string) error {
	p, ok := handler.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	schema := withObjectProperties(handler, p.PropertySchema())
	if d, ok := handler.(ComponentCapabilityDefaults); ok {
		// The capability fills these keys when the author leaves them out, so their
		// absence is not refused here (ComponentCapabilityDefaults).
		_, keys := d.CapabilityDefaults()
		schema = withoutRequired(schema, keys)
	}
	return validateAuthoredTopLevel(schema, exclusiveProperties(handler), props, path)
}

// withoutRequired returns schema with Required cleared on keys. schema is never
// mutated, as in withEngineTraitProperties.
func withoutRequired(schema map[string]PropertySchema, keys []string) map[string]PropertySchema {
	out := maps.Clone(schema)
	for _, key := range keys {
		if field, ok := out[key]; ok {
			field.Required = false
			out[key] = field
		}
	}
	return out
}

// unsupportedFieldHinter is implemented by a component handler, a component
// lowering rule or a trait handler or lowering rule that adds a hint to the
// refusal of an undeclared top-level key: the helmchart terminal points a key
// of the composite that used to carry its name to `helm`, and a hand-parsed
// kind gives the reason it refuses a field of the Kubernetes type it projects.
// "" adds nothing. A key refused below the top level is never passed to it:
// see nestedUnsupportedFieldHinter.
type unsupportedFieldHinter interface {
	UnsupportedFieldHint(key string) string
}

// nestedUnsupportedFieldHinter is unsupportedFieldHinter for a key refused
// below the top level, inside an object the type declares. parents are the
// declared keys from the top level down to that object, array items unnamed:
// `initContainers[0].probes` is parents ["initContainers"], key "probes". A
// hand-parsed kind gives the reason the parser of that object refuses the key
// with. "" adds nothing. It is a method of its own so that a hinter written
// for top-level keys is never asked about a nested key of the same name.
type nestedUnsupportedFieldHinter interface {
	NestedUnsupportedFieldHint(parents []string, key string) string
}

// newUnsupportedField is the refusal of an undeclared key, at any depth:
// validateAuthoredProperties' at the top level, validateObjectProperties'
// below it. Its parents are the declared keys above the refused one, nil at the
// top level; each object level that returns the error prepends its own key
// (withUnsupportedFieldParent).
func newUnsupportedField(path, key string, schema map[string]PropertySchema) *PropertyError {
	allowed := declaredFields(schema)
	e := propertyErrorf(PropertyErrorUnsupportedField, path, "unsupported field %q (allowed: %s)", key, allowed).withKey(key)
	e.allowed = allowed
	return e
}

// withUnsupportedFieldParent records key as the parent of the undeclared key
// err refuses, when it refuses one, and returns err.
func withUnsupportedFieldParent(err error, key string) error {
	var uerr *PropertyError
	if errors.As(err, &uerr) && uerr.Kind == PropertyErrorUnsupportedField {
		uerr.parents = append([]string{key}, uerr.parents...)
	}
	return err
}

// withUnsupportedFieldHint appends handler's hint to err when err refuses an
// undeclared key and handler has a hint for that key: UnsupportedFieldHint for
// a top-level key, NestedUnsupportedFieldHint for one below it. Any other err
// is returned unchanged.
func withUnsupportedFieldHint(handler any, err error) error {
	var uerr *PropertyError
	if !errors.As(err, &uerr) || uerr.Kind != PropertyErrorUnsupportedField {
		return err
	}
	var hint string
	if len(uerr.parents) == 0 {
		if h, ok := handler.(unsupportedFieldHinter); ok {
			hint = h.UnsupportedFieldHint(uerr.key)
		}
	} else if h, ok := handler.(nestedUnsupportedFieldHinter); ok {
		hint = h.NestedUnsupportedFieldHint(slices.Clone(uerr.parents), uerr.key)
	}
	if hint != "" {
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
// with Required cleared, top level and nested (relaxObjectRequired), and then for
// Required on the properties merged as resolveCapability merges them
// (checkNestedRequired), so a required key the rendering supplies is not refused
// (go-kure/launcher#765, go-kure/launcher#1463). A trait no binding matches is
// checked on its properties as written.
func (t *Transformer) validateAuthoredTrait(componentName string, trait *Trait, capabilities map[string]CapabilityBinding) error {
	path := fmt.Sprintf("component %q: trait %q: properties", componentName, trait.Type)
	return locate(t.validateAuthoredTraitAt(trait, capabilities, path), path, PropertyError{Position: "trait", Component: componentName, Trait: trait.Type})
}

func (t *Transformer) validateAuthoredTraitAt(trait *Trait, capabilities map[string]CapabilityBinding, path string) error {
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
		return withUnsupportedFieldHint(handler, validateAuthoredTraitAgainst(handler, trait.Properties, path))
	}
	schema := withEngineTraitProperties(p.PropertySchema())
	exclusive := exclusiveProperties(handler)
	// The groups are held to the schema as declared, before relaxation clears the
	// Required that makes a group malformed, and before a value the rendering would
	// otherwise be checked first on.
	if err := checkObjectGroups(schema, exclusive, path); err != nil {
		return withUnsupportedFieldHint(handler, err)
	}
	if err := validateAuthoredTopLevel(relaxObjectRequired(schema), exclusive, trait.Properties, path); err != nil {
		return withUnsupportedFieldHint(handler, err)
	}
	// Looked up only after validation, which normalizes what the author wrote: a scope
	// of a named string type matches its binding here, as it does in Transform.
	merged, matchedKey, _ := resolveCapability(*trait, capabilities, nil)
	return checkNestedRequired(schema, exclusive, merged.Properties, path, matchedKey)
}

// relaxObjectRequired returns schema with Required cleared at the top level, on
// every field of an object reached from it through object fields alone, the objects a
// capability rendering merges into, and on that object's exclusive groups: the
// rendering may supply a group's key too. Everything under an array's Items keeps its
// flags: a list is authored whole, never merged into, so a required key of one of its
// elements is genuinely missing when the author leaves it out. schema is never
// mutated, as in withEngineTraitProperties.
func relaxObjectRequired(schema map[string]PropertySchema) map[string]PropertySchema {
	out := make(map[string]PropertySchema, len(schema))
	for key, field := range schema {
		field.Required = false
		if field.Type == PropertyTypeObject && len(field.Properties) > 0 {
			field.Properties = relaxObjectRequired(field.Properties)
			for k, sub := range field.Properties {
				sub.Required = false
				field.Properties[k] = sub
			}
			field.Exclusive = slices.Clone(field.Exclusive)
			for i := range field.Exclusive {
				field.Exclusive[i].Required = false
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
	return validateAuthoredTopLevel(withEngineTraitProperties(p.PropertySchema()), exclusiveProperties(handler), props, path)
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
	return validateAuthoredTopLevel(p.PropertySchema(), exclusiveProperties(handler), props, path)
}

// validateAuthoredProperties is validateProperties for an authored document: it
// rejects a key the schema does not declare, checks the shape of every key it does,
// then reports the first declared Required key the document omits, at the top level
// and nested (go-kure/launcher#1463). A null counts as absent.
//
// Required is enforced as written, with one exception: a trait a capability binding
// matches. ClusterProfile capability rendering merges into a trait's properties later
// (applyTraits → resolveCapability), at the top level and into nested objects
// (mergeRenderedProperties, go-kure/launcher#750), so a required property the
// platform supplies is legitimately absent from what the user wrote. For such a trait
// validateAuthoredTrait passes a schema with Required cleared (relaxObjectRequired)
// and checks the merged properties instead (checkNestedRequired, go-kure/launcher#765
// for nested keys and go-kure/launcher#1463 for the top level), as applyTraits and
// the lowering fixpoint do after their own merge. A component key a
// ComponentCapabilityDefaults handler lists is left to the capability the same way
// (validateAuthoredComponentAgainst). A key an author leaves out of a component or a
// policy is refused here, naming the key, before a handler could refuse it with its
// own message at transform time.
//
// At the top level the undeclared-key half is checked first, so a misspelt key is
// reported with the allowed list before the key it should have been is reported
// missing. Inside a nested object the order is the other way, as on the emitted path:
// validateObjectProperties reports a missing Required key first. The undeclared-key
// rule applies at every level: nested objects are covered by validateObjectProperties
// through validatePropertyValue, under the same AdditionalProperties rule the emitted
// path uses.
//
// Values are written back for the same reason validateObjectProperties writes them
// back: validatePropertyValue may normalize an array or object into a fresh
// []any/map[string]any, and the handler downstream must see the shape validation
// actually checked.
//
// It checks no top-level exclusive groups; validateAuthoredTopLevel takes them.
func validateAuthoredProperties(schema map[string]PropertySchema, props map[string]any, path string) error {
	return validateAuthoredTopLevel(schema, nil, props, path)
}

// validateAuthoredTopLevel is validateAuthoredProperties with the handler's top-level
// exclusive groups (ExclusivePropertiesProvider). A malformed group anywhere in the
// schema, under an object or array props leaves out included, is reported first, as
// the schema's error (checkObjectGroups); then every key is checked, then
// Required (checkTopLevelRequired); the groups are held to at most one last, and
// their Required is not enforced: a group's "at least one" is a rule of the whole
// document that the capability merge may complete.
func validateAuthoredTopLevel(schema map[string]PropertySchema, exclusive []ExclusiveGroup, props map[string]any, path string) error {
	if err := checkObjectGroups(schema, exclusive, path); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(props)) {
		field, ok := schema[key]
		if !ok {
			return newUnsupportedField(path, key, schema)
		}
		normalized, err := validatePropertyValue(field, props[key], path+"."+key)
		if err != nil {
			return withUnsupportedFieldParent(err, key)
		}
		props[key] = normalized
	}
	if err := checkTopLevelRequired(schema, props, path, ""); err != nil {
		return err
	}
	return checkExclusive(exclusive, schema, props, false, path, "")
}
