package oam

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// schemaPolicy is a stub PolicyHandler that declares a property schema. The
// existing policy stubs (transform_test.go) declare none, and the policy position
// is one of the three emission points, so it needs its own schema-carrying fixture.
type schemaPolicy struct{ typ string }

func (h schemaPolicy) CanHandle(t string) bool { return t == h.typ }
func (h schemaPolicy) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}
func (h schemaPolicy) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"components": {Type: PropertyTypeArray, Required: true, Items: &PropertySchema{Type: PropertyTypeString}},
	}
}

// richComponent exercises the parts of the vocabulary the two existing fixtures do
// not reach: enum, number/boolean/integer scalars, a nested closed object, a nested
// open object (AdditionalProperties), and an array of objects.
type richComponent struct{ typ string }

func (h richComponent) CanHandle(t string) bool { return t == h.typ }
func (h richComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (h richComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"strategy": {Type: PropertyTypeString, Enum: []any{"rolling", "recreate"}},
		"replicas": {Type: PropertyTypeInteger},
		"weight":   {Type: PropertyTypeNumber},
		"enabled":  {Type: PropertyTypeBoolean},
		"port":     {Type: PropertyTypeInteger, Enum: []any{80, 443}},
		"resources": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"cpu":    {Type: PropertyTypeString, Required: true},
				"memory": {Type: PropertyTypeString},
			},
		},
		"labels": {
			Type:                 PropertyTypeObject,
			Properties:           map[string]PropertySchema{"tier": {Type: PropertyTypeString}},
			AdditionalProperties: true,
		},
		"env": {
			Type: PropertyTypeArray,
			Items: &PropertySchema{
				Type: PropertyTypeObject,
				Properties: map[string]PropertySchema{
					"name":  {Type: PropertyTypeString, Required: true},
					"value": {Type: PropertyTypeString},
				},
			},
		},
	}
}

func richSchema() map[string]PropertySchema { return richComponent{typ: "rich"}.PropertySchema() }

func TestValidateProperties_TopLevel(t *testing.T) {
	schema := schemaComponent{typ: "webservice"}.PropertySchema() // image: string, required

	tests := []struct {
		name    string
		props   map[string]any
		wantErr string // "" means accept
	}{
		{name: "valid", props: map[string]any{"image": "nginx"}},
		{name: "missing required", props: map[string]any{}, wantErr: `"image" is required`},
		{name: "nil required is absent", props: map[string]any{"image": nil}, wantErr: `"image" is required`},
		{name: "wrong type", props: map[string]any{"image": 7}, wantErr: "expected string, got int"},
		{
			name:    "undeclared key rejected at top level",
			props:   map[string]any{"image": "nginx", "imagee": "typo"},
			wantErr: `unsupported field "imagee" (allowed: image)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProperties(schema, tc.props, "properties")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected acceptance, got error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateProperties_RichVocabulary(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name: "fully populated document-shaped value",
			props: map[string]any{
				"strategy":  "rolling",
				"replicas":  3,
				"weight":    1.5,
				"enabled":   true,
				"port":      443,
				"resources": map[string]any{"cpu": "100m", "memory": "128Mi"},
				"labels":    map[string]any{"tier": "web", "anything": "goes"},
				"env":       []any{map[string]any{"name": "PORT", "value": "8080"}},
			},
		},
		{
			name:  "go-native slice and map, not decoder-shaped",
			props: map[string]any{"labels": map[string]string{"tier": "web"}},
		},
		{
			name:  "yaml float for an integer field",
			props: map[string]any{"replicas": float64(3)},
		},
		{
			name:    "fractional float is not an integer",
			props:   map[string]any{"replicas": 3.5},
			wantErr: "expected integer, got float64",
		},
		{
			name:    "enum member not allowed",
			props:   map[string]any{"strategy": "bluegreen"},
			wantErr: "not in allowed set",
		},
		{
			name:  "numeric enum matches across int and float",
			props: map[string]any{"port": float64(80)},
		},
		{
			name:    "numeric enum still rejects a non-member",
			props:   map[string]any{"port": 8080},
			wantErr: "not in allowed set",
		},
		{
			name:    "nested required field missing",
			props:   map[string]any{"resources": map[string]any{"memory": "128Mi"}},
			wantErr: `properties.resources: "cpu" is required`,
		},
		{
			name:    "nested undeclared key rejected when object is closed",
			props:   map[string]any{"resources": map[string]any{"cpu": "100m", "gpu": "1"}},
			wantErr: `properties.resources: unsupported field "gpu"`,
		},
		{
			name:    "array element validated against Items",
			props:   map[string]any{"env": []any{map[string]any{"value": "8080"}}},
			wantErr: `properties.env[0]: "name" is required`,
		},
		{
			name:    "array element path is indexed",
			props:   map[string]any{"env": []any{map[string]any{"name": "A"}, map[string]any{"name": 2}}},
			wantErr: "properties.env[1].name: expected string, got int",
		},
		{
			name:    "object where array expected",
			props:   map[string]any{"env": map[string]any{"name": "A"}},
			wantErr: "expected array, got map[string]interface {}",
		},
		{
			name:    "scalar where object expected",
			props:   map[string]any{"resources": "100m"},
			wantErr: "expected object, got string",
		},
		{
			name:    "boolean type enforced",
			props:   map[string]any{"enabled": "true"},
			wantErr: "expected boolean, got string",
		},
		{
			name:  "number accepts an integer",
			props: map[string]any{"weight": 2},
		},
		{
			name:    "number rejects a string",
			props:   map[string]any{"weight": "2"},
			wantErr: "expected number, got string",
		},
		{
			name:  "nil under an optional field is tolerated",
			props: map[string]any{"replicas": nil},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProperties(richSchema(), tc.props, "properties")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected acceptance, got error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidatePropertyValue_UnsupportedSchemaType proves a handler bug (a type
// outside the PropertyType vocabulary) fails loudly instead of silently leaving the
// field unvalidated forever.
func TestValidatePropertyValue_UnsupportedSchemaType(t *testing.T) {
	schema := map[string]PropertySchema{"weird": {Type: PropertyType("strng")}}
	err := validateProperties(schema, map[string]any{"weird": "x"}, "properties")
	if err == nil || !strings.Contains(err.Error(), "unsupported property type") {
		t.Fatalf("expected an unsupported-property-type error, got: %v", err)
	}
}

// TestValidateProperties_UntypedSchemaChecksEnumOnly covers a schema built by a call
// site that leaves Type empty (the flat capability vocabulary permits that).
func TestValidateProperties_UntypedSchemaChecksEnumOnly(t *testing.T) {
	schema := map[string]PropertySchema{"mode": {Enum: []any{"a", "b"}}}
	if err := validateProperties(schema, map[string]any{"mode": "a"}, "properties"); err != nil {
		t.Fatalf("expected acceptance of an enum member, got: %v", err)
	}
	if err := validateProperties(schema, map[string]any{"mode": "c"}, "properties"); err == nil {
		t.Fatal("expected rejection of a non-member")
	}
}

// TestValidateProperties_DeterministicMessage proves the reported problem does not
// depend on Go's map iteration order: two required fields are missing and the same
// one is always named.
func TestValidateProperties_DeterministicMessage(t *testing.T) {
	schema := map[string]PropertySchema{
		"alpha": {Type: PropertyTypeString, Required: true},
		"zulu":  {Type: PropertyTypeString, Required: true},
	}
	for i := range 50 {
		err := validateProperties(schema, map[string]any{}, "properties")
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), `"alpha" is required`) {
			t.Fatalf("run %d: expected the first sorted required field, got: %v", i, err)
		}
	}
}

func newSchemaTransformer() *Transformer {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": schemaComponent{typ: "webservice"},
			"plain":      plainComponent{typ: "plain"},
			"rich":       richComponent{typ: "rich"},
		},
		map[string]TraitHandler{
			"pvc": schemaTrait{typ: "pvc"},
		},
	)
	tr.RegisterPolicy("dependency", schemaPolicy{typ: "dependency"})
	return tr
}

func TestValidateEmittedComponent(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted component to be accepted, got: %v", err)
	}

	// No registered handler: lowerable or not yet settled, nothing to check against.
	if err := tr.validateEmittedComponent(&Component{
		Name: "shop", Type: "web-and-cache", Properties: map[string]any{"anything": 1},
	}); err != nil {
		t.Fatalf("expected an unregistered component type to be passed over, got: %v", err)
	}

	// Registered handler that declares no schema: accepts anything.
	if err := tr.validateEmittedComponent(&Component{
		Name: "p", Type: "plain", Properties: map[string]any{"whatever": 1},
	}); err != nil {
		t.Fatalf("expected a schema-less handler to accept anything, got: %v", err)
	}

	err := tr.validateEmittedComponent(&Component{
		Name: "web", Type: "webservice", Properties: map[string]any{"image": 7},
	})
	if err == nil {
		t.Fatal("expected a type violation in an emitted component to be rejected")
	}
	// The message names the emitted element, since the caller supplies only the
	// AUTHORED origin around it.
	for _, want := range []string{`emitted component "web"`, `(type "webservice")`, "properties.image", "expected string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestValidateEmittedTrait(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteOnce"}},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted trait to be accepted, got: %v", err)
	}

	// A rule assembling its output in Go writes []string, not []any.
	if err := tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []string{"ReadWriteOnce"}},
	}); err != nil {
		t.Fatalf("expected a Go-native []string to be accepted for an array field, got: %v", err)
	}

	if err := tr.validateEmittedTrait(&Trait{
		Type: "expose-plus", Properties: map[string]any{"anything": 1},
	}); err != nil {
		t.Fatalf("expected an unregistered trait type to be passed over, got: %v", err)
	}

	err := tr.validateEmittedTrait(&Trait{Type: "pvc", Properties: map[string]any{"accessModes": []any{"ReadWriteOnce"}}})
	if err == nil {
		t.Fatal("expected a missing required trait property to be rejected")
	}
	if !strings.Contains(err.Error(), `emitted trait "pvc"`) || !strings.Contains(err.Error(), `"size" is required`) {
		t.Errorf("unexpected message: %v", err)
	}

	err = tr.validateEmittedTrait(&Trait{
		Type: "pvc", Properties: map[string]any{"size": "1Gi", "accessModes": []any{1}},
	})
	if err == nil || !strings.Contains(err.Error(), "properties.accessModes[0]: expected string") {
		t.Fatalf("expected an indexed item-type error, got: %v", err)
	}
}

func TestValidateEmittedPolicy(t *testing.T) {
	tr := newSchemaTransformer()

	if err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "db-first", Type: "dependency", Properties: map[string]any{"components": []any{"db"}},
	}); err != nil {
		t.Fatalf("expected a schema-conformant emitted policy to be accepted, got: %v", err)
	}

	if err := tr.validateEmittedPolicy(&ApplicationPolicy{
		Name: "anything", Type: "placement", Properties: map[string]any{"zone": "eu"},
	}); err != nil {
		t.Fatalf("expected an unregistered policy type to be passed over, got: %v", err)
	}

	err := tr.validateEmittedPolicy(&ApplicationPolicy{Name: "db-first", Type: "dependency"})
	if err == nil {
		t.Fatal("expected a missing required policy property to be rejected")
	}
	if !strings.Contains(err.Error(), `emitted policy "db-first" (type "dependency")`) {
		t.Errorf("unexpected message: %v", err)
	}
}

// badComponentRule emits one component of a registered, schema-carrying type with
// properties the schema rejects — the case emission-time validation exists for.
type badComponentRule struct{ props map[string]any }

func (badComponentRule) ComponentType() string { return "web-and-cache" }

func (r badComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name: comp.Name + "-web", Type: "webservice", Properties: r.props,
	}}}, nil
}

// TestLower_EmittedComponentIsValidated is the integration proof: a rule's bad
// output fails the run at emission time, and the error leads with the AUTHORED
// origin (D7) with the emitted detail second.
func TestLower_EmittedComponentIsValidated(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "shop", Type: "web-and-cache", Properties: map[string]any{}}},
		},
	}

	// Accepted output settles normally.
	trOK := newSchemaTransformer()
	trOK.RegisterComponentLowering(badComponentRule{props: map[string]any{"image": "nginx"}})
	if _, err := trOK.lower(app, TransformContext{}); err != nil {
		t.Fatalf("expected conformant rule output to lower cleanly, got: %v", err)
	}

	// Rejected output fails the run.
	trBad := newSchemaTransformer()
	trBad.RegisterComponentLowering(badComponentRule{props: map[string]any{"image": "nginx", "imagee": "typo"}})
	_, err := trBad.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected lower to reject a rule emitting schema-violating properties")
	}
	var lerr *LoweringError
	if !stderrors.As(err, &lerr) {
		t.Fatalf("expected a *LoweringError, got %T: %v", err, err)
	}
	// runLowering attributes a LoweringError to the document being expanded; the
	// failing element's own authored origin is carried in the wrapped cause, which
	// Error() prints right after it.
	if lerr.Origin.Document != "myapp" || lerr.Origin.DocumentKind != "Application" {
		t.Errorf("expected the authored document origin, got %+v", lerr.Origin)
	}
	msg := err.Error()
	for _, want := range []string{`component "shop" (type "web-and-cache")`, `emitted component "shop-web"`, `unsupported field "imagee"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}
