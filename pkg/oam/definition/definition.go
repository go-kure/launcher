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
// document names is read, and only from a local file.
package definition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structural "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
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
	var doc document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.Wrapf(err, "parsing component definition %q", path)
	}
	h, err := build(&doc, filepath.Dir(path))
	if err != nil {
		return nil, errors.Wrapf(err, "component definition %q", path)
	}
	return h, nil
}

func build(doc *document, dir string) (*Handler, error) {
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

	raw, err := os.ReadFile(filepath.Join(dir, doc.Spec.Schema.CRD))
	if err != nil {
		return nil, errors.Wrap(err, "reading the pinned CRD")
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != doc.Spec.Schema.SHA256 {
		return nil, errors.Errorf("CRD %q has sha256 %s, the definition pins %q", doc.Spec.Schema.CRD, got, doc.Spec.Schema.SHA256)
	}
	var crd apiextv1.CustomResourceDefinition
	if err := sigsyaml.Unmarshal(raw, &crd); err != nil {
		return nil, errors.Wrapf(err, "parsing CRD %q", doc.Spec.Schema.CRD)
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
	validator, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		return nil, errors.Wrap(err, "building the CRD schema validator")
	}
	spec, ok := internal.Properties["spec"]
	if !ok {
		return nil, errors.New("the CRD schema declares no spec")
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
	// The validator and the pruner read JSON values (int64, float64), not the Go
	// values a YAML decode gives.
	encoded, err := json.Marshal(map[string]any{
		"apiVersion": h.apiVersion,
		"kind":       h.kind,
		"metadata":   map[string]any{"name": component.Name},
		"spec":       component.Properties,
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
	if errs := validation.ValidateCustomResource(nil, object, h.validator); len(errs) > 0 {
		return nil, errors.Wrapf(errs.ToAggregate(), "component %q (%s)", component.Name, h.kind)
	}

	properties := map[string]any{"object": object}
	if h.clusterScoped {
		properties["clusterScoped"] = true
	}
	return (&components.PassthroughHandler{}).ToApplicationConfig(
		&oam.Component{Name: component.Name, Type: "passthrough", Properties: properties}, namespace)
}

// Register makes the definition's type authorable on t and declares its identity, so
// that a model can name the type as a part. It refuses a type name t already holds.
func Register(t *oam.Transformer, h *Handler) error {
	// A declared identity or a registered handler holds the name; the second read
	// covers a handler registered with no identity, which RegisterAuthoredComponent
	// would meet with a panic.
	_, err := t.ResolveComponentType(h.typeName)
	_, registered := t.ComponentHandlerFor(h.typeName)
	if err == nil || registered {
		return errors.Errorf("component definition: the type %q is already declared", h.typeName)
	}
	ids := map[string]oam.TypeID{h.typeName: h.identity}
	if err := t.CheckComponentTypes(ids); err != nil {
		return errors.Wrapf(err, "component definition %q", h.typeName)
	}
	t.RegisterAuthoredComponent(h.typeName, h)
	return errors.Wrapf(t.DeclareComponentTypes(ids), "component definition %q", h.typeName)
}
