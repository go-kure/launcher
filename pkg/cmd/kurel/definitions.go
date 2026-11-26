package kurel

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/definition"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// loadTypeDocuments registers on t the types that documents in dir declare
// (go-kure/launcher#102 spike): every ComponentDefinition first, then every
// Model, so a model may name a defined type. Within each, files load in name
// order, so a model that names another model sorts after it. Other kinds are left
// to their own loaders. A missing dir declares nothing.
func loadTypeDocuments(t *oam.Transformer, dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "reading definitions directory %q", dir)
	}

	var definitions, models []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrapf(err, "reading %q", path)
		}
		var probe struct {
			Kind string `yaml:"kind"`
		}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return errors.Wrapf(err, "%q: failed to parse YAML", path)
		}
		switch probe.Kind {
		case definition.Kind:
			definitions = append(definitions, path)
		case "Model":
			models = append(models, path)
		}
	}

	for _, path := range definitions {
		h, err := definition.Load(path)
		if err != nil {
			return err
		}
		if err := definition.Register(t, h); err != nil {
			return errors.Wrapf(err, "%q", path)
		}
	}

	registry := model.NewRegistry(t)
	for _, path := range models {
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrapf(err, "reading %q", path)
		}
		m, err := model.Load(data)
		if err != nil {
			return errors.Wrapf(err, "%q", path)
		}
		if err := registry.Register(m); err != nil {
			return errors.Wrapf(err, "%q", path)
		}
	}
	return nil
}
