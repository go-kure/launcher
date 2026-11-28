// Package shipped holds the component definitions and models that launcher ships in its
// binary, and registers them on a Transformer (go-kure/launcher#1735).
//
// The content is embedded from content/definitions (ComponentDefinition documents and the
// CRD files they pin by SHA-256) and content/models (Model documents). Definitions load
// first, then models, each in file name order, so a shipped model may name a shipped
// definition's type. Every file loads through the same loader as an application's own, so
// a CRD whose digest differs from the pinned one refuses the load.
//
// A shipped type is never replaced: Set.Clash is how a caller refuses an application's
// definition or model that names one.
package shipped

import (
	"embed"
	"io/fs"
	"path"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/definition"
	"github.com/go-kure/launcher/pkg/oam/model"
)

//go:embed content
var content embed.FS

// Set records the types that were shipped, each with the file that declares it.
type Set struct {
	// origins maps a shipped definition's type name, and the canonical identity
	// (`name@version`) of every shipped definition and model, to its origin.
	origins map[string]string
}

// Load registers the embedded definitions and models on t and returns what it shipped.
func Load(t *oam.Transformer) (*Set, error) {
	definitions, err := fs.Sub(content, "content/definitions")
	if err != nil {
		return nil, err
	}
	models, err := fs.Sub(content, "content/models")
	if err != nil {
		return nil, err
	}
	return LoadFS(t, definitions, models)
}

// LoadFS is Load for definitions and models read from the roots of two file systems. A root
// that does not exist holds nothing. Every *.yaml file directly in definitions is a
// ComponentDefinition, and every one in models is a Model; a file that does not load refuses
// the whole load, naming it.
func LoadFS(t *oam.Transformer, definitions, models fs.FS) (*Set, error) {
	s := &Set{origins: map[string]string{}}
	names, err := yamlFiles(definitions)
	if err != nil {
		return nil, errors.Wrap(err, "shipped definitions")
	}
	for _, name := range names {
		h, err := definition.LoadFS(definitions, name)
		if err != nil {
			return nil, errors.Wrapf(err, "shipped definition %q", name)
		}
		if err := definition.Register(t, h); err != nil {
			return nil, errors.Wrapf(err, "shipped definition %q", name)
		}
		origin := "definition " + name
		s.origins[h.TypeName()] = origin
		s.origins[h.Identity().Canonical().String()] = origin
	}

	names, err = yamlFiles(models)
	if err != nil {
		return nil, errors.Wrap(err, "shipped models")
	}
	registry := model.NewRegistry(t)
	for _, name := range names {
		data, err := fs.ReadFile(models, name)
		if err != nil {
			return nil, errors.Wrapf(err, "shipped model %q", name)
		}
		m, err := model.Load(data)
		if err != nil {
			return nil, errors.Wrapf(err, "shipped model %q", name)
		}
		if err := registry.Register(m); err != nil {
			return nil, errors.Wrapf(err, "shipped model %q", name)
		}
		s.origins[m.Key()] = "model " + name
	}
	return s, nil
}

// Clash returns the refusal for an application's type that is shipped: typeName is the
// authored name a definition declares (empty for a model) and id the identity it declares.
// It reports the first of the two that is shipped, and names the shipped file. A nil Set
// ships nothing.
func (s *Set) Clash(typeName string, id oam.TypeID) error {
	if s == nil {
		return nil
	}
	for _, key := range []string{typeName, id.Canonical().String()} {
		if origin, ok := s.origins[key]; ok && key != "" {
			return errors.Errorf("the type %q is shipped by launcher (%s); an application cannot redefine it", key, origin)
		}
	}
	return nil
}

// yamlFiles lists the *.yaml files directly in the root of fsys, in file name order. A root
// that does not exist holds none.
func yamlFiles(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".yaml" {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
