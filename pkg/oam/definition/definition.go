// Package definition loads a ComponentDefinition: a component type declared by a
// document and a CustomResourceDefinition file the document pins by SHA-256, with no
// Go code for the type (go-kure/launcher#1704).
//
// A component of such a type takes the custom resource's spec as its properties. The
// whole object is checked offline against the CRD's structural schema, unknown
// fields included, and is then emitted by the passthrough handler, so it meets the
// same per-object rules a passthrough object meets.
//
// The check is structural. The CRD's CEL rules (x-kubernetes-validations) are not
// evaluated and the API server's defaults are not applied. Only the version the
// document names is read, and only from a local file or the file system LoadFS is given.
package definition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structural "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

const (
	// APIVersion and Kind identify a ComponentDefinition document.
	APIVersion = "launcher.gokure.dev/v1alpha1"
	Kind       = "ComponentDefinition"
)

type document struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		// Name is the component type the document declares.
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Emits struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
		} `yaml:"emits"`
		Schema struct {
			// CRD is a path inside the directory of the definition file.
			CRD    string `yaml:"crd"`
			SHA256 string `yaml:"sha256"`
		} `yaml:"schema"`
	} `yaml:"spec"`
}

// Handler is the component handler of one loaded definition.
type Handler struct {
	typeName      string
	identity      oam.TypeID
	apiVersion    string
	kind          string
	clusterScoped bool
	properties    map[string]oam.PropertySchema

	structural *structural.Structural
	validator  validation.SchemaValidator
}

// Load reads the ComponentDefinition at path and the CRD it pins. It refuses a CRD
// whose SHA-256 differs from the pinned one, one that does not define the emitted
// kind in a served version with a structural schema, and a document with a field it
// does not know. The emitted object is namespaced or cluster-scoped as the CRD says.
func Load(path string) (*Handler, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "reading component definition %q", path)
	}
	return parse(data, path, diskCRD(filepath.Dir(path)))
}

// LoadFS is Load for the definition at name in fsys, with the CRD read from fsys too, as a path
// inside the definition's directory. It is how a definition embedded in the binary loads: every
// check of Load runs, the pinned digest included.
func LoadFS(fsys fs.FS, name string) (*Handler, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, errors.Wrapf(err, "reading component definition %q", name)
	}
	return parse(data, name, fsCRD(fsys, path.Dir(name)))
}

// crdReader returns the bytes of the CRD file a definition names, a path relative to the
// definition's directory.
type crdReader func(rel string) ([]byte, error)

// diskCRD reads a CRD from dir, and refuses a path that leaves it, a symlink included.
func diskCRD(dir string) crdReader {
	return func(rel string) ([]byte, error) {
		// The text is local; a symlink can still lead out of the directory.
		crdPath, err := oam.ResolveInside(dir, rel)
		if errors.Is(err, oam.ErrPathOutsideDirectory) {
			return nil, errors.Errorf("spec.schema.crd %q resolves outside the definition's directory", rel)
		}
		if err != nil {
			return nil, errors.Wrap(err, "reading the pinned CRD")
		}
		raw, err := os.ReadFile(crdPath)
		if err != nil {
			return nil, errors.Wrap(err, "reading the pinned CRD")
		}
		return raw, nil
	}
}

// fsCRD reads a CRD from dir of fsys. The path was already refused if it is not local.
func fsCRD(fsys fs.FS, dir string) crdReader {
	return func(rel string) ([]byte, error) {
		raw, err := fs.ReadFile(fsys, path.Join(dir, rel))
		if err != nil {
			return nil, errors.Wrap(err, "reading the pinned CRD")
		}
		return raw, nil
	}
}

// parse decodes the definition document data, which came from the file called name, and builds
// its handler.
func parse(data []byte, name string, readCRD crdReader) (*Handler, error) {
	var doc document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.Wrapf(err, "parsing component definition %q", name)
	}
	// One definition per file: a second document would be dropped unread.
	var next any
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, errors.Errorf("component definition %q: the file holds more than one YAML document; one definition goes in each file", name)
	}
	h, err := build(&doc, readCRD)
	if err != nil {
		return nil, errors.Wrapf(err, "component definition %q", name)
	}
	return h, nil
}

func build(doc *document, readCRD crdReader) (*Handler, error) {
	if doc.APIVersion != APIVersion || doc.Kind != Kind {
		return nil, errors.Errorf("want apiVersion %q and kind %q, got %q and %q", APIVersion, Kind, doc.APIVersion, doc.Kind)
	}
	if doc.Metadata.Name == "" {
		return nil, errors.New("metadata.name is required")
	}
	group, version, ok := strings.Cut(doc.Spec.Emits.APIVersion, "/")
	if !ok || doc.Spec.Emits.Kind == "" {
		return nil, errors.New("spec.emits needs an apiVersion of the form group/version and a kind")
	}
	if !filepath.IsLocal(doc.Spec.Schema.CRD) {
		return nil, errors.Errorf("spec.schema.crd %q must be a relative path inside the definition's directory", doc.Spec.Schema.CRD)
	}

	raw, err := readCRD(doc.Spec.Schema.CRD)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != doc.Spec.Schema.SHA256 {
		return nil, errors.Errorf("CRD %q has sha256 %s, the definition pins %q", doc.Spec.Schema.CRD, got, doc.Spec.Schema.SHA256)
	}
	// One document: the parse below reads the first and would leave a second unread.
	crdStream := yaml.NewDecoder(bytes.NewReader(raw))
	var first, second yaml.Node
	if err := crdStream.Decode(&first); err == nil {
		if err := crdStream.Decode(&second); !errors.Is(err, io.EOF) {
			return nil, errors.Errorf("CRD %q holds more than one YAML document; pin a file with the one CustomResourceDefinition", doc.Spec.Schema.CRD)
		}
	}
	var crd apiextv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(raw, &crd); err != nil {
		return nil, errors.Wrapf(err, "parsing CRD %q", doc.Spec.Schema.CRD)
	}
	if crd.APIVersion != apiextv1.SchemeGroupVersion.String() || crd.Kind != "CustomResourceDefinition" {
		return nil, errors.Errorf("%q is %s %s, not a %s CustomResourceDefinition",
			doc.Spec.Schema.CRD, crd.APIVersion, crd.Kind, apiextv1.SchemeGroupVersion)
	}
	if crd.Spec.Group != group || crd.Spec.Names.Kind != doc.Spec.Emits.Kind {
		return nil, errors.Errorf("CRD defines %s in group %s, spec.emits names %s in group %s",
			crd.Spec.Names.Kind, crd.Spec.Group, doc.Spec.Emits.Kind, group)
	}
	i := slices.IndexFunc(crd.Spec.Versions, func(v apiextv1.CustomResourceDefinitionVersion) bool {
		return v.Name == version && v.Served
	})
	if i < 0 {
		return nil, errors.Errorf("CRD does not serve version %q", version)
	}
	if crd.Spec.Versions[i].Schema == nil || crd.Spec.Versions[i].Schema.OpenAPIV3Schema == nil {
		return nil, errors.Errorf("CRD version %q has no schema", version)
	}

	internal := &apiext.JSONSchemaProps{}
	if err := apiextv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[i].Schema.OpenAPIV3Schema, internal, nil); err != nil {
		return nil, errors.Wrap(err, "converting the CRD schema")
	}
	s, err := structural.NewStructural(internal)
	if err != nil {
		return nil, errors.Wrap(err, "reading the CRD schema as a structural schema")
	}
	if errs := structural.ValidateStructural(nil, s); len(errs) > 0 {
		return nil, errors.Wrap(errs.ToAggregate(), "the CRD schema is not structural")
	}
	// The API server checks the apiVersion, kind and metadata of such a field apart from
	// the schema, a check this loader does not run, so the CRD is refused, not half checked.
	if path, found := embeddedResource(s, ""); found {
		return nil, errors.Errorf("the CRD schema marks %s x-kubernetes-embedded-resource, whose apiVersion, kind and metadata checks a component definition does not run", path)
	}
	validator, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		return nil, errors.Wrap(err, "building the CRD schema validator")
	}
	spec, ok := internal.Properties["spec"]
	if !ok {
		return nil, errors.New("the CRD schema declares no spec")
	}
	// The top level is published as a closed set of properties, and the property
	// check refuses any key outside it before the CRD validator runs.
	if spec.AdditionalProperties != nil && (spec.AdditionalProperties.Allows || spec.AdditionalProperties.Schema != nil) ||
		spec.XPreserveUnknownFields != nil && *spec.XPreserveUnknownFields {
		return nil, errors.New("the CRD's spec is open (additionalProperties or x-kubernetes-preserve-unknown-fields), and a component definition publishes the spec's declared properties only; declare them in the CRD")
	}

	return &Handler{
		typeName:      doc.Metadata.Name,
		identity:      oam.TypeID{Name: strings.ToLower(doc.Spec.Emits.Kind) + "." + group, Version: version},
		apiVersion:    doc.Spec.Emits.APIVersion,
		kind:          doc.Spec.Emits.Kind,
		clusterScoped: crd.Spec.Scope == apiextv1.ClusterScoped,
		properties:    topLevelProperties(spec),
		structural:    s,
		validator:     validator,
	}, nil
}

// embeddedResource finds a field marked x-kubernetes-embedded-resource and returns its
// path (an array item is [], a map value {}); the root is "the schema root".
func embeddedResource(s *structural.Structural, path string) (string, bool) {
	if s.XEmbeddedResource {
		if path == "" {
			return "the schema root", true
		}
		return path, true
	}
	if s.Items != nil {
		if p, ok := embeddedResource(s.Items, path+"[]"); ok {
			return p, true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		prop := s.Properties[name]
		child := name
		if path != "" {
			child = path + "." + name
		}
		if p, ok := embeddedResource(&prop, child); ok {
			return p, true
		}
	}
	if ap := s.AdditionalProperties; ap != nil && ap.Structural != nil {
		return embeddedResource(ap.Structural, path+"{}")
	}
	return "", false
}

// topLevelProperties publishes the top level of the CRD's spec. The nested shape is
// left open here and checked in full by the CRD validator. A property of no declared
// type (the CRD leaves it to x-kubernetes-preserve-unknown-fields) is published as
// one that accepts any value.
func topLevelProperties(spec apiext.JSONSchemaProps) map[string]oam.PropertySchema {
	out := make(map[string]oam.PropertySchema, len(spec.Properties))
	for name, p := range spec.Properties {
		field := oam.PropertySchema{Description: p.Description, Required: slices.Contains(spec.Required, name)}
		switch {
		case p.XIntOrString:
			field.Types = []oam.PropertyType{oam.PropertyTypeString, oam.PropertyTypeInteger}
		case p.Type == "object":
			field.Type, field.AdditionalProperties = oam.PropertyTypeObject, true
		default:
			field.Type = oam.PropertyType(p.Type)
		}
		out[name] = field
	}
	return out
}

// TypeName is the component type the document declares.
func (h *Handler) TypeName() string { return h.typeName }

// Identity is the type identity a model names the type by: <kind>.<group>@<version>.
func (h *Handler) Identity() oam.TypeID { return h.identity }

// CanHandle claims the definition's own type.
func (h *Handler) CanHandle(componentType string) bool { return componentType == h.typeName }

// PropertySchema is the published top level of the CRD's spec.
func (h *Handler) PropertySchema() map[string]oam.PropertySchema { return h.properties }

// ToApplicationConfig checks the properties as the custom resource's spec and emits
// the object through the passthrough handler.
func (h *Handler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	// A component that authors no properties has the empty spec, never a null one.
	spec := component.Properties
	if spec == nil {
		spec = map[string]any{}
	}
	// The validator and the pruner read JSON values (int64, float64), not the Go
	// values a YAML decode gives.
	encoded, err := json.Marshal(map[string]any{
		"apiVersion": h.apiVersion,
		"kind":       h.kind,
		"metadata":   map[string]any{"name": component.Name},
		"spec":       spec,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "component %q: encoding the properties", component.Name)
	}
	var object map[string]any
	if err := utiljson.Unmarshal(encoded, &object); err != nil {
		return nil, errors.Wrapf(err, "component %q: decoding the properties", component.Name)
	}

	// Pruning removes what it reports, so it runs on its own copy.
	var pruned map[string]any
	if err := utiljson.Unmarshal(encoded, &pruned); err != nil {
		return nil, errors.Wrapf(err, "component %q: decoding the properties", component.Name)
	}
	unknown := pruning.PruneWithOptions(pruned, h.structural, true,
		structural.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
	if len(unknown) > 0 {
		return nil, errors.Errorf("component %q (%s): fields the CRD does not declare: %s",
			component.Name, h.kind, strings.Join(unknown, ", "))
	}
	// The API server checks list-type map and set uniqueness apart from the schema.
	errs := validation.ValidateCustomResource(nil, object, h.validator)
	errs = append(errs, listtype.ValidateListSetsAndMaps(nil, h.structural, object)...)
	if len(errs) > 0 {
		return nil, errors.Wrapf(errs.ToAggregate(), "component %q (%s)", component.Name, h.kind)
	}

	// What is emitted is what was authored: the checks above ran on a JSON-converted
	// copy, which rounds an integer beyond int64. Passthrough keeps every number exact.
	emitted := map[string]any{
		"apiVersion": h.apiVersion,
		"kind":       h.kind,
		"metadata":   map[string]any{"name": component.Name},
		"spec":       spec,
	}
	// Always stated, false included: unset, passthrough would let its own table of
	// kinds decide a scope the pinned CRD has already settled.
	properties := map[string]any{"object": emitted, "clusterScoped": h.clusterScoped}
	return (&components.PassthroughHandler{}).ToApplicationConfig(
		&oam.Component{Name: component.Name, Type: "passthrough", Properties: properties}, namespace)
}

// Register makes the definition's type authorable on t and declares its identity, so
// that a model can name the type as a part. It refuses a type name t already holds, and an
// identity whose short or qualified name is a handler or a lowering rule registered under it
// that no declared type claims.
func Register(t *oam.Transformer, h *Handler) (err error) {
	// A declared identity or a registered handler holds the name.
	held, resolveErr := t.ResolveComponentType(h.typeName)
	_, registered := t.ComponentHandlerFor(h.typeName)
	if resolveErr == nil {
		// A declared type answers to the name, a model's short name for one: say which.
		return errors.Errorf("component definition: the type %q is already declared, as %s", h.typeName, held)
	}
	if registered {
		return errors.Errorf("component definition: the type %q is already declared", h.typeName)
	}
	// The identity is also known by its short name and by its qualified name, which would
	// take over a plain key of that name, a handler or a lowering rule, that no declared
	// type claims. A declared type that holds the name is another type or another version,
	// and the resolver and CheckComponentTypes judge that.
	short := h.identity.ShortName()
	if holdsUnclaimedPlainKey(t, short) {
		return errors.Errorf("component definition %q: the short name %q of %s is a type that is already registered", h.typeName, short, h.identity)
	}
	if name := h.identity.Name; name != short && holdsUnclaimedPlainKey(t, name) {
		return errors.Errorf("component definition %q: the qualified name %q of %s is a type that is already registered", h.typeName, name, h.identity)
	}
	ids := map[string]oam.TypeID{h.typeName: h.identity}
	if err := t.CheckComponentTypes(ids); err != nil {
		return errors.Wrapf(err, "component definition %q", h.typeName)
	}
	// The Transformer panics on a name it holds with no declared identity, such as
	// a lowering rule a library caller registered.
	defer func() {
		if p := recover(); p != nil {
			err = errors.Errorf("component definition %q: %v", h.typeName, p)
		}
	}()
	t.RegisterAuthoredComponent(h.typeName, h)
	return errors.Wrapf(t.DeclareComponentTypes(ids), "component definition %q", h.typeName)
}

// holdsUnclaimedPlainKey reports whether a handler or a lowering rule is registered under name
// and no declared type claims name. A name a declared type claims resolves to that type, or is
// reported ambiguous, and so never reads as another's (the same test as the model registry's).
func holdsUnclaimedPlainKey(t *oam.Transformer, name string) bool {
	_, handler := t.ComponentHandlerFor(name)
	_, rule := t.ComponentLoweringRuleFor(name)
	if !handler && !rule {
		return false
	}
	_, err := t.ResolveComponentType(name)
	return errors.Is(err, oam.ErrTypeReferenceUnknown)
}
