package components

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apiextensions-apiserver/pkg/apihelpers"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	structuraldefaulting "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	structurallisttype "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	schemaobjectmeta "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/objectmeta"
	structuralpruning "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	"github.com/go-kure/launcher/pkg/oam"
)

// A kind component refuses, when it reads its properties, what the CRD's
// expression rules (x-kubernetes-validations) refuse of one document. Such a
// refusal is a reading of the rule, and a reading can be wrong in the places a
// rule's text does not show: a default the API server fills before it
// evaluates the rule, `has()` on a field the Go type leaves out, a null it
// drops. crdCreate answers for the rule itself: it runs the API server's own
// routines, in the order the API server runs them when a custom resource is
// created (k8s.io/apiextensions-apiserver v0.37.1), over the CRD a linked
// module ships.
//
//  1. The CRD is checked as the API server checks a CRD that is created
//     (ValidateCustomResourceDefinition, which compiles every expression rule
//     among everything else): a CRD it would not serve cannot answer for a
//     document.
//  2. The schema of the version is prepared as it is when the CRD starts being
//     served: converted, made structural, its defaults pruned
//     (pkg/apiserver/customresource_handler.go).
//  3. The document goes through the decode-time pass with strict field
//     validation (unstructuredSchemaCoercer.apply): the fields the schema does
//     not know are pruned and named, a null of a field that is not nullable
//     and has no default is dropped, embedded metadata is coerced.
//  4. The defaults of the schema are filled (unstructuredDefaulter).
//  5. Where the version has the status subresource, the status the document
//     holds is dropped: a create cannot set it (PrepareForCreate of
//     pkg/registry/customresource/strategy.go). An object built from a Go type
//     that always encodes its status holds an empty one. A version without
//     the subresource keeps the status, and it is validated with the rest.
//  6. The object is validated (pkg/registry/customresource/strategy.go,
//     validator.go): the schema, embedded metadata, list sets and maps, then
//     the expression rules, unless a schema error already stands that means
//     the object has not the shape the rules were written for.
//
// What it leaves out, since no rule of a kind reads it: the rules of the
// object's own metadata (its name, its namespace) and the scale subresource.
// An admission webhook is not the API server's and is not run. A transition
// rule, which reads the stored object, is not evaluated on a create by the
// API server either.
type crdCreate struct {
	name       string
	structural *structuralschema.Structural
	schema     apiservervalidation.SchemaValidator
	// dropsStatus says the version has the status subresource.
	dropsStatus bool
}

// crdCreateOf prepares the version of crd as the API server serves it. It
// fails the test on a CRD the API server would refuse.
func crdCreateOf(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition, version string) *crdCreate {
	t.Helper()
	crd = crd.DeepCopy()
	// The defaults the API server fills in a CRD it decodes
	// (pkg/apis/apiextensions/v1/defaults.go): the singular and list names,
	// the conversion strategy.
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(crd)

	// The create of the CRD itself: the strategy's PrepareForCreate
	// (pkg/registry/customresourcedefinition/strategy.go), then its validation.
	internal := &apiextensions.CustomResourceDefinition{}
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(crd, internal, nil); err != nil {
		t.Fatalf("%s: convert the CRD: %v", crd.Name, err)
	}
	internal.Status = apiextensions.CustomResourceDefinitionStatus{}
	internal.Generation = 1
	for _, v := range internal.Spec.Versions {
		if v.Storage {
			internal.Status.StoredVersions = append(internal.Status.StoredVersions, v.Name)
			break
		}
	}
	if errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), internal); len(errs) > 0 {
		t.Fatalf("%s: the API server would refuse the CRD: %v", crd.Name, errs.ToAggregate())
	}

	if !apihelpers.HasVersionServed(crd, version) {
		t.Fatalf("%s does not serve %s", crd.Name, version)
	}
	validation, err := apihelpers.GetSchemaForVersion(crd, version)
	if err != nil || validation == nil || validation.OpenAPIV3Schema == nil {
		t.Fatalf("%s %s has no schema (%v)", crd.Name, version, err)
	}
	converted := &apiextensions.CustomResourceValidation{}
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(validation, converted, nil); err != nil {
		t.Fatalf("%s %s: convert the schema: %v", crd.Name, version, err)
	}
	structural, err := structuralschema.NewStructural(converted.OpenAPIV3Schema)
	if err != nil {
		t.Fatalf("%s %s: the schema is not structural: %v", crd.Name, version, err)
	}
	structural = structural.DeepCopy()
	if err := structuraldefaulting.PruneDefaults(structural); err != nil {
		t.Fatalf("%s %s: prune the defaults: %v", crd.Name, version, err)
	}
	schema, _, err := apiservervalidation.NewSchemaValidator(converted.OpenAPIV3Schema)
	if err != nil {
		t.Fatalf("%s %s: the schema validator: %v", crd.Name, version, err)
	}
	subresources, err := apihelpers.GetSubresourcesForVersion(crd, version)
	if err != nil {
		t.Fatalf("%s %s: the subresources: %v", crd.Name, version, err)
	}
	return &crdCreate{
		name: crd.Name + " " + version, structural: structural, schema: schema,
		dropsStatus: subresources != nil && subresources.Status != nil,
	}
}

// crdRule names one expression rule of a CRD: the path of the value it is
// declared on (a property under its name, a list element under [], a map value
// under {}, the object itself under the empty path) and its text.
type crdRule struct {
	path, rule string
}

func (r crdRule) String() string { return r.path + ": " + r.rule }

// only returns c with every expression rule but one removed from its schema,
// and what the API server says of a document that rule is false for: its
// message, or "failed rule: " and its text. A document the result refuses by
// an expression rule is refused by that rule, whatever else it would break.
func (c *crdCreate) only(t *testing.T, kept crdRule) (*crdCreate, string) {
	t.Helper()
	structural := c.structural.DeepCopy()
	var found []string
	keepOneRule(structural, "", kept, &found)
	if len(found) != 1 {
		t.Fatalf("%s declares the rule %q %d times, want once", c.name, kept, len(found))
	}
	return &crdCreate{name: c.name + " with the one rule " + kept.String(), structural: structural, schema: c.schema, dropsStatus: c.dropsStatus}, found[0]
}

// keepOneRule removes every expression rule under s, at the path at, but kept,
// and appends to found what the API server answers when kept is false.
func keepOneRule(s *structuralschema.Structural, at string, kept crdRule, found *[]string) {
	rules := s.XValidations
	s.XValidations = nil
	for _, rule := range rules {
		if at != kept.path || rule.Rule != kept.rule {
			continue
		}
		s.XValidations = append(s.XValidations, rule)
		// ruleMessageOrDefault of pkg/apiserver/schema/cel/validation.go.
		says := "failed rule: " + strings.TrimSpace(rule.Rule)
		if rule.Message != "" {
			says = strings.TrimSpace(rule.Message)
		}
		if rule.MessageExpression != "" {
			says = ""
		}
		*found = append(*found, says)
	}
	for name, prop := range s.Properties {
		child := name
		if at != "" {
			child = at + "." + name
		}
		keepOneRule(&prop, child, kept, found)
		s.Properties[name] = prop
	}
	if s.Items != nil {
		keepOneRule(s.Items, at+"[]", kept, found)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Structural != nil {
		keepOneRule(s.AdditionalProperties.Structural, at+"{}", kept, found)
	}
}

// crdAnswer is what the API server answers the create of one document.
type crdAnswer struct {
	// unknown is the fields the schema does not know. A request with strict
	// field validation is refused for them before anything is validated.
	unknown []string
	// schema is what the schema, embedded metadata and the list sets and maps
	// refuse.
	schema field.ErrorList
	// rules is what the expression rules refuse: a rule that is false, and a
	// rule that fails to evaluate (it reads a field the document does not
	// hold, for one), which the API server refuses too.
	rules field.ErrorList
	// skipped says the expression rules were not evaluated, because a schema
	// error stands that the API server does not evaluate them past.
	skipped bool
	// object is the document as it was validated: pruned and defaulted.
	object map[string]any
}

// refused says the API server refuses the document.
func (a crdAnswer) refused() bool {
	return len(a.unknown)+len(a.schema)+len(a.rules) > 0
}

func (a crdAnswer) String() string {
	var out []string
	for _, path := range a.unknown {
		out = append(out, path+": unknown field")
	}
	for _, err := range a.schema {
		out = append(out, "schema: "+err.Error())
	}
	for _, err := range a.rules {
		out = append(out, "rule: "+err.Error())
	}
	if a.skipped {
		out = append(out, "the expression rules were not evaluated")
	}
	if len(out) == 0 {
		return "accepted"
	}
	return strings.Join(out, "; ")
}

// crdBlockingErrors are the schema errors after which the API server does not
// evaluate the expression rules: hasBlockingErr of
// pkg/registry/customresource/strategy.go.
var crdBlockingErrors = map[field.ErrorType]bool{
	field.ErrorTypeNotSupported: true,
	field.ErrorTypeRequired:     true,
	field.ErrorTypeTooLong:      true,
	field.ErrorTypeTooMany:      true,
	field.ErrorTypeTypeInvalid:  true,
}

// create answers the create of doc, a whole object: apiVersion, kind, metadata
// and what the CRD holds beside them. doc is not changed. Its numbers may be
// of any Go type: it is encoded and decoded as the API server decodes a
// request body, a whole number as an int64.
func (c *crdCreate) create(t *testing.T, doc map[string]any) crdAnswer {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the document: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode the document: %v", err)
	}
	answer := crdAnswer{object: object}

	// The decode-time pass. The schema does not describe apiVersion, kind and
	// the object's metadata, which are set aside and put back.
	apiVersion, kind := object["apiVersion"], object["kind"]
	meta, hasMeta, unknown, err := schemaobjectmeta.GetObjectMetaWithOptions(object, schemaobjectmeta.ObjectMetaOptions{ReturnUnknownFieldPaths: true})
	if err != nil {
		t.Fatalf("the document's metadata: %v", err)
	}
	answer.unknown = append(unknown, structuralpruning.PruneWithOptions(object, c.structural, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})...)
	structuraldefaulting.PruneNonNullableNullsWithoutDefaults(object, c.structural)
	coerceErr, paths := schemaobjectmeta.CoerceWithOptions(nil, object, c.structural, false, schemaobjectmeta.CoerceOptions{ReturnUnknownFieldPaths: true})
	if coerceErr != nil {
		answer.schema = append(answer.schema, coerceErr)
		return answer
	}
	answer.unknown = append(answer.unknown, paths...)
	if apiVersion != nil {
		object["apiVersion"] = apiVersion
	}
	if kind != nil {
		object["kind"] = kind
	}
	if hasMeta {
		if err := schemaobjectmeta.SetObjectMeta(object, meta); err != nil {
			t.Fatalf("put the document's metadata back: %v", err)
		}
	}
	if len(answer.unknown) > 0 {
		return answer
	}

	structuraldefaulting.Default(object, c.structural)

	if c.dropsStatus {
		delete(object, "status")
	}

	ctx := context.Background()
	answer.schema = apiservervalidation.ValidateCustomResource(nil, object, c.schema)
	answer.schema = append(answer.schema, schemaobjectmeta.Validate(ctx, nil, object, c.structural, false)...)
	answer.schema = append(answer.schema, structurallisttype.ValidateListSetsAndMaps(nil, c.structural, object)...)
	for _, err := range answer.schema {
		if crdBlockingErrors[err.Type] {
			answer.skipped = true
		}
	}
	if !answer.skipped {
		answer.rules, _ = cel.NewValidator(c.structural, true, celconfig.PerCallLimit).Validate(ctx, nil, c.structural, object, nil, celconfig.RuntimeCELCostBudget)
	}
	return answer
}

// refusedByRule fails the test unless the answer is a refusal by one expression
// rule and nothing else, saying what the API server says of that rule (says,
// from only; empty where the rule computes its message). c must hold that one
// rule (only), so that the refusal is the rule's.
func (a crdAnswer) refusedByRule(t *testing.T, what, says string) {
	t.Helper()
	if len(a.unknown) > 0 || len(a.schema) > 0 || a.skipped || len(a.rules) != 1 {
		t.Errorf("%s: the API server answers %q, want one refusal by the rule and nothing else", what, a)
		return
	}
	if says != "" && a.rules[0].Detail != says {
		t.Errorf("%s: the API server answers %q, want the rule's own refusal %q (another answer is a rule that failed to evaluate)", what, a, says)
	}
}

// accepted fails the test unless the API server accepts the document.
func (a crdAnswer) accepted(t *testing.T, what string) {
	t.Helper()
	if a.refused() || a.skipped {
		t.Errorf("%s: the API server answers %q, want it accepted", what, a)
	}
}

// crdDocument is the object an author would send the API server: the CRD's
// kind in the version, named, with the fields of body beside its metadata (a
// kind's properties under `spec`, for most).
func crdDocument(crd *apiextensionsv1.CustomResourceDefinition, version string, body map[string]any) map[string]any {
	meta := map[string]any{"name": "fast"}
	if crd.Spec.Scope == apiextensionsv1.NamespaceScoped {
		meta["namespace"] = "apps"
	}
	doc := map[string]any{
		"apiVersion": fmt.Sprintf("%s/%s", crd.Spec.Group, version),
		"kind":       crd.Spec.Names.Kind,
		"metadata":   meta,
	}
	maps.Copy(doc, body)
	return doc
}

// checkedRules shows the expression rules a kind component checks against the
// API server's answer for them.
type checkedRules struct {
	create    *crdCreate
	component string
	handler   oam.ComponentHandler
	// document makes, of a component's properties, the object an author would
	// send the API server for them.
	document func(props map[string]any) map[string]any
}

// build returns the object the kind builds for the properties, as the JSON it
// is written as, or the kind's refusal. The properties are a copy, with the
// numbers a document decodes into.
func (c checkedRules) build(t *testing.T, props map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := stdjson.Marshal(props)
	if err != nil {
		t.Fatalf("encode the properties: %v", err)
	}
	var copied map[string]any
	if err := stdjson.Unmarshal(raw, &copied); err != nil {
		t.Fatalf("decode the properties: %v", err)
	}
	cfg, err := c.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: c.component, Properties: copied}, "apps")
	if err != nil {
		return nil, err
	}
	objs, err := cfg.Generate(stack.NewApplication("fast", "apps", cfg))
	if err != nil {
		return nil, err
	}
	if len(objs) != 1 {
		t.Fatalf("the kind emits %d objects, want one", len(objs))
	}
	if raw, err = stdjson.Marshal(*objs[0]); err != nil {
		t.Fatalf("encode the object: %v", err)
	}
	var object map[string]any
	if err := stdjson.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode the object: %v", err)
	}
	return object, nil
}

// show holds one checked rule to the API server, on properties that break it
// and on properties next to them that keep it, which differ in what the rule
// reads. There must be some of each.
//
// breaks: with every other expression rule taken out of the schema, the API
// server refuses the document, by the rule's own refusal and nothing else; and
// the kind refuses the properties, saying want.
//
// keeps: the API server, with every rule, accepts the document; the kind
// builds the properties; and the API server accepts the object the kind emits.
func (c checkedRules) show(t *testing.T, rule crdRule, want string, breaks, keeps []map[string]any) {
	t.Helper()
	if len(breaks) == 0 || len(keeps) == 0 {
		t.Fatalf("rule %q is shown on %d documents that break it and %d that keep it, want some of each", rule, len(breaks), len(keeps))
	}
	only, says := c.create.only(t, rule)
	for i, props := range breaks {
		what := fmt.Sprintf("breaking document %d", i)
		only.create(t, c.document(props)).refusedByRule(t, what, says)
		if _, err := c.build(t, props); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the kind answers %v, want a refusal mentioning %q", what, err, want)
		}
	}
	for i, props := range keeps {
		what := fmt.Sprintf("neighbouring document %d", i)
		c.create.create(t, c.document(props)).accepted(t, what)
		object, err := c.build(t, props)
		if err != nil {
			t.Errorf("%s: the kind refuses it (%v), and the API server accepts it", what, err)
			continue
		}
		c.create.create(t, object).accepted(t, "the object the kind emits for "+what)
	}
}

// crdRuleOf reads a rule as the listing tests write one: the path it is
// declared on, ": ", its text.
func crdRuleOf(t *testing.T, listed string) crdRule {
	t.Helper()
	path, rule, ok := strings.Cut(listed, ": ")
	if !ok {
		t.Fatalf("%q is no rule written as its path, a colon and its text", listed)
	}
	return crdRule{path: path, rule: rule}
}
