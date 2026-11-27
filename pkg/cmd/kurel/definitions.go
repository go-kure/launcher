package kurel

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/definition"
)

// loadComponentDefinitions registers on t the component types that the
// ComponentDefinition documents in dir declare (go-kure/launcher#1704): every
// *.yaml file of dir in file name order whose kind is ComponentDefinition. A file of
// another kind is left to its own loader, and a dir that does not exist declares
// nothing. Each refusal names the file it is about.
func loadComponentDefinitions(t *oam.Transformer, dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "reading definitions directory %q", dir)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrapf(err, "reading %q", path)
		}
		isDefinition, err := holdsDefinition(data)
		if err != nil {
			return errors.Wrapf(err, "%q: failed to parse YAML", path)
		}
		if !isDefinition {
			continue
		}
		h, err := definition.Load(path)
		if err != nil {
			return err
		}
		if err := definition.Register(t, h); err != nil {
			return errors.Wrapf(err, "%q", path)
		}
	}
	return nil
}

// holdsDefinition reports whether any YAML document of data is a
// ComponentDefinition, so that one in a later document is handed to Load, which
// refuses the file, and never skipped.
func holdsDefinition(data []byte) (bool, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var probe struct {
			Kind string `yaml:"kind"`
		}
		if err := dec.Decode(&probe); err == io.EOF {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if probe.Kind == definition.Kind {
			return true, nil
		}
	}
}
