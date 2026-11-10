package model

import (
	"bytes"
	stderrors "errors"
	"io"
	"reflect"
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
// name and line, and so do YAML anchors, aliases to an anchor defined earlier and
// merge keys (`<<` as a mapping key; as a value it is the string `<<`). An alias
// whose anchor is not defined earlier in the document is refused
// by the YAML parser, with its own error and no line. A YAML type
// error, such as a list where a string belongs, names the line of the field and
// not its path. An explicit tag that does not fit its value, such as `!!bool notabool`,
// names its line when exactly one tagged value is at fault. A null entry in a list of
// strings or of objects with fixed fields
// fails with its path, and so does a free-form mapping that holds two keys YAML
// reads as one. Load returns the first
// failure, and a nil model with it. The YAML, apiVersion and kind checks come
// first, then the null and duplicate-key checks, then the metadata, then spec.celEnv,
// then the inputs in alphabetical order of their names (a Go map keeps no document
// order), then the values and the parts in document order.
func Load(data []byte) (*Model, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("model: empty input")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))

	// Read the document as a node tree first. yaml.v3 does not see a duplicate key
	// when one key is an alias, and it panics on a merge key next to a sequence key.
	// The format has no use for either, so the tree is checked before the struct decode.
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
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
	if err := refuseIndirection(&root); err != nil {
		return nil, err
	}

	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	var m Model
	if err := strict.Decode(&m); err != nil {
		if located := locateTagError(&root, err); located != nil {
			return nil, located
		}
		return nil, errors.Errorf("model: %w", err)
	}

	if m.APIVersion != oam.SupportedAPIVersion {
		return nil, errors.Errorf("model: apiVersion %q, want %q", m.APIVersion, oam.SupportedAPIVersion)
	}
	if m.Kind != Kind {
		return nil, errors.Errorf("model: kind %q, want %q", m.Kind, Kind)
	}
	if len(root.Content) == 1 {
		if err := refuseNullElements(root.Content[0], reflect.TypeOf(m), ""); err != nil {
			return nil, err
		}
	}
	if err := validate(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// refuseIndirection walks a YAML node tree and fails on the first anchor, alias or
// merge key, with its line. It reads the whole document, so it also reaches a value that
// yaml.v3 skips later, such as the value under a null key of a map with string keys. The
// failure is a YAML-level one and does not wrap ErrInvalid. A merge key is `<<` as a
// mapping key. The same plain `<<` as a value is the string `<<`, although yaml.v3 gives
// it the !!merge tag in every position, so only the key positions of a mapping are tested.
func refuseIndirection(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.AliasNode:
		return errors.Errorf("model: line %d: YAML anchors, aliases and merge keys are not supported", n.Line)
	case n.Anchor != "":
		return errors.Errorf("model: line %d: YAML anchors, aliases and merge keys are not supported", n.Line)
	}
	for i, c := range n.Content {
		isKey := n.Kind == yaml.MappingNode && i%2 == 0
		if isKey && c.Kind == yaml.ScalarNode && c.Value == "<<" && c.ShortTag() == "!!merge" {
			return errors.Errorf("model: line %d: YAML anchors, aliases and merge keys are not supported", c.Line)
		}
		if err := refuseIndirection(c); err != nil {
			return err
		}
	}
	return nil
}

// locateTagError adds a line to the failure of a YAML explicit tag that does not fit its
// value, such as `!!bool notabool` or a `!!binary` payload that is not base64. yaml.v3
// reports such a failure with no position. The strict decode stops at the first failing
// node it reaches, and it never reaches a value it skips, such as the value under a null
// key or under an unknown field. A walk cannot tell which nodes the decode reaches. So it
// names a line only when exactly one tagged scalar fails to decode, and that scalar's
// message is the message of the strict decode. With no such scalar, or with two or more,
// it returns nil and the caller keeps the error as it is. A type error of the strict
// decode already names its line and is left alone.
func locateTagError(root *yaml.Node, err error) error {
	var typeErr *yaml.TypeError
	if stderrors.As(err, &typeErr) {
		return nil
	}
	var failing []*yaml.Node
	var failure string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && n.Style&yaml.TaggedStyle != 0 {
			var v any
			if derr := n.Decode(&v); derr != nil {
				failing = append(failing, n)
				failure = strings.TrimPrefix(derr.Error(), "yaml: ")
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(root)
	if len(failing) != 1 || failure != strings.TrimPrefix(err.Error(), "yaml: ") {
		return nil
	}
	return errors.Errorf("model: line %d: %s", failing[0].Line, failure)
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
	// Exactly one of type and types is set here. The single type field is empty when
	// types is used, which means "look at types". An entry of types has no such
	// meaning, so an empty entry is refused like any unknown name.
	declared := s.Types
	if s.Type != "" {
		declared = []oam.PropertyType{s.Type}
	}
	for _, t := range declared {
		switch t {
		case oam.PropertyTypeString, oam.PropertyTypeInteger, oam.PropertyTypeBoolean,
			oam.PropertyTypeNumber, oam.PropertyTypeArray, oam.PropertyTypeObject:
		default:
			return invalid(path, "type %q is not string, integer, boolean, number, array or object", t)
		}
	}
	if err := validateUnion(path, s.Types); err != nil {
		return err
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

// validateUnion checks the shape of a types list the way pkg/oam does when a value
// reaches the schema: at least two members, no member twice, and only scalar members.
// An array or object member needs Properties or Items of its own, which a union cannot
// carry. Without this check Load accepts a schema that rejects every value. An empty
// list means the schema uses type, which the caller has already checked.
func validateUnion(path string, types []oam.PropertyType) error {
	if len(types) == 0 {
		return nil
	}
	if len(types) < 2 {
		return invalid(path, "types lists %d member, want at least two (use type for one)", len(types))
	}
	seen := make(map[oam.PropertyType]bool, len(types))
	for _, t := range types {
		switch t {
		case oam.PropertyTypeString, oam.PropertyTypeInteger, oam.PropertyTypeNumber, oam.PropertyTypeBoolean:
		default:
			return invalid(path, "types member %q is not a scalar type (string, integer, number or boolean)", t)
		}
		if seen[t] {
			return invalid(path, "types lists %q twice", t)
		}
		seen[t] = true
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
