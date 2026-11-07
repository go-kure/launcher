package model

import (
	"bytes"
	stderrors "errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ErrInvalid wraps every failure of a decoded model document that breaks a rule
// of the format. A YAML, apiVersion or kind failure does not wrap it.
var ErrInvalid = errors.New("invalid model")

// Load decodes one model document and checks its shape. It never compiles or
// evaluates an expression. Decoding is strict: an unknown field fails with its
// name and line. Load returns the first failure in document order, and a nil
// model with it.
func Load(data []byte) (*Model, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("model: empty input")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var m Model
	if err := dec.Decode(&m); err != nil {
		if stderrors.Is(err, io.EOF) {
			return nil, errors.New("model: empty input")
		}
		return nil, errors.Errorf("model: %w", err)
	}
	var next yaml.Node
	switch err := dec.Decode(&next); {
	case err == nil:
		return nil, errors.New("model: the input holds more than one document, want one document")
	case !stderrors.Is(err, io.EOF):
		return nil, errors.Errorf("model: %w", err)
	}

	if m.APIVersion != oam.SupportedAPIVersion {
		return nil, errors.Errorf("model: apiVersion %q, want %q", m.APIVersion, oam.SupportedAPIVersion)
	}
	if m.Kind != Kind {
		return nil, errors.Errorf("model: kind %q, want %q", m.Kind, Kind)
	}
	if err := validate(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// identRe is a CEL identifier. Input, value, part and loop-variable names are
// read in expressions as `inputs.<name>`, `values.<name>` and `parts.<id>`, so a
// hyphen or a leading digit would make them unreachable.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// versionRe is the version of a model: v<major>[.<minor>[.<patch>]]. The type
// grammar also allows a Kubernetes API version (v1beta1), which a model never takes.
var versionRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,2}$`)

// celReserved holds the words CEL reserves. None may name an input, value, part
// or loop variable.
var celReserved = map[string]bool{
	"as": true, "break": true, "const": true, "continue": true, "else": true,
	"false": true, "for": true, "function": true, "if": true, "import": true,
	"in": true, "let": true, "loop": true, "namespace": true, "null": true,
	"package": true, "return": true, "true": true, "var": true, "void": true,
	"while": true,
}

// scopeNames are the variables the CEL environment of a model defines. A loop
// variable may not hide one of them.
var scopeNames = map[string]bool{"inputs": true, "self": true, "values": true, "parts": true}

func invalid(path, msg string, args ...any) error {
	return errors.Errorf("%w: %s: %s", ErrInvalid, path, errors.Errorf(msg, args...))
}

func checkIdent(path, name string) error {
	if !identRe.MatchString(name) {
		return invalid(path, "%q is not an identifier (letters, digits and underscores, not starting with a digit)", name)
	}
	if celReserved[name] {
		return invalid(path, "%q is a reserved CEL word", name)
	}
	return nil
}

func validate(m *Model) error {
	if err := validateMetadata(&m.Metadata); err != nil {
		return err
	}
	if m.Spec.CELEnv == "" {
		return invalid("spec.celEnv", "is required, want %q", CELEnvV1)
	}
	if m.Spec.CELEnv != CELEnvV1 {
		return invalid("spec.celEnv", "%q is not a known environment, want %q", m.Spec.CELEnv, CELEnvV1)
	}
	if err := validateInputs(m.Spec.Inputs); err != nil {
		return err
	}
	if err := validateValues(m.Spec.Values); err != nil {
		return err
	}
	return validateParts(m.Spec.Parts)
}

func validateMetadata(md *Metadata) error {
	if md.Name == "" {
		return invalid("metadata.name", "is required")
	}
	// The name is checked alone first, so a failure names the right field.
	if _, err := oam.ValidateTypeIdentity(md.Name + "@v1"); err != nil {
		return invalid("metadata.name", "%w", err)
	}
	if !strings.Contains(md.Name, ".") {
		return invalid("metadata.name", "%q has no domain, want <name>.<domain>", md.Name)
	}
	if md.Version == "" {
		return invalid("metadata.version", "is required")
	}
	if !versionRe.MatchString(md.Version) {
		return invalid("metadata.version", "%q is not v<major>[.<minor>[.<patch>]]", md.Version)
	}
	if _, err := oam.ValidateTypeIdentity(md.Name + "@" + md.Version); err != nil {
		return invalid("metadata.version", "%w", err)
	}
	return nil
}

func validateInputs(inputs map[string]oam.PropertySchema) error {
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := "spec.inputs." + name
		if err := checkIdent(path, name); err != nil {
			return err
		}
		if err := validateSchema(path, inputs[name]); err != nil {
			return err
		}
	}
	return nil
}

// validateSchema checks that a declared schema names a known type. It looks at
// the type vocabulary only. The value rules stay with pkg/oam.
func validateSchema(path string, s oam.PropertySchema) error {
	switch {
	case s.Type == "" && len(s.Types) == 0:
		return invalid(path, "type is required")
	case s.Type != "" && len(s.Types) > 0:
		return invalid(path, "type and types are both set, want one")
	}
	for _, t := range append([]oam.PropertyType{s.Type}, s.Types...) {
		switch t {
		case "", oam.PropertyTypeString, oam.PropertyTypeInteger, oam.PropertyTypeBoolean,
			oam.PropertyTypeNumber, oam.PropertyTypeArray, oam.PropertyTypeObject:
		default:
			return invalid(path, "type %q is not string, integer, boolean, number, array or object", t)
		}
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := validateSchema(path+".properties."+k, s.Properties[k]); err != nil {
			return err
		}
	}
	if s.Items != nil {
		return validateSchema(path+".items", *s.Items)
	}
	return nil
}

func validateValues(values []Value) error {
	seen := make(map[string]bool, len(values))
	for i, v := range values {
		base := "spec.values[" + strconv.Itoa(i) + "]"
		if err := checkIdent(base+".name", v.Name); err != nil {
			return err
		}
		if seen[v.Name] {
			return invalid(base+".name", "%q is used twice", v.Name)
		}
		seen[v.Name] = true
		if v.Expr == "" {
			return invalid(base+".expr", "is required")
		}
	}
	return nil
}

func validateParts(parts []Part) error {
	if len(parts) == 0 {
		return invalid("spec.parts", "a model needs at least one part")
	}
	seen := make(map[string]bool, len(parts))
	for i, p := range parts {
		base := "spec.parts[" + strconv.Itoa(i) + "]"
		if err := checkIdent(base+".id", p.ID); err != nil {
			return err
		}
		if seen[p.ID] {
			return invalid(base+".id", "%q is used twice", p.ID)
		}
		seen[p.ID] = true
		if err := checkFullIdentity(base+".type", p.Type); err != nil {
			return err
		}
		if p.ForEach != nil {
			if err := checkIdent(base+".forEach.as", p.ForEach.As); err != nil {
				return err
			}
			if scopeNames[p.ForEach.As] {
				return invalid(base+".forEach.as", "%q hides a variable of the model environment", p.ForEach.As)
			}
			if p.ForEach.In == "" {
				return invalid(base+".forEach.in", "is required")
			}
		}
		for j, t := range p.Traits {
			if err := checkFullIdentity(base+".traits["+strconv.Itoa(j)+"].type", t.Type); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkFullIdentity refuses a type that is not `name@version`. A short name and a
// missing version are both refused, so a model pins what it emits.
func checkFullIdentity(path, s string) error {
	if s == "" {
		return invalid(path, "is required, want name@version")
	}
	if _, err := oam.ValidateTypeIdentity(s); err != nil {
		return invalid(path, "%w", err)
	}
	return nil
}
