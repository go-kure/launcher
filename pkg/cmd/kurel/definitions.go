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
// nothing. It returns the definitions it registered, in that order, and each
// refusal names the file it is about.
func loadComponentDefinitions(t *oam.Transformer, dir string) ([]*definition.Handler, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "reading definitions directory %q", dir)
	}
	var loaded []*definition.Handler
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "reading %q", path)
		}
		isDefinition, err := holdsDefinition(data)
		if err != nil {
			return nil, errors.Wrapf(err, "%q: failed to parse YAML", path)
		}
		if !isDefinition {
			continue
		}
		h, err := definition.Load(path)
		if err != nil {
			return nil, err
		}
		if err := definition.Register(t, h); err != nil {
			return nil, errors.Wrapf(err, "%q", path)
		}
		loaded = append(loaded, h)
	}
	return loaded, nil
}

// refuseShadowedDefinitions refuses a build in which a model, loaded after the
// definitions, has taken a defined type's name over. A definition's type name is a
// plain key with no identity of its own under that name, and an identity resolves
// before a plain name, so a model whose short name equals it would be built in the
// definition's place, past its CRD check. The refusal names both.
func refuseShadowedDefinitions(t *oam.Transformer, defs []*definition.Handler) error {
	for _, h := range defs {
		id, err := t.ResolveComponentType(h.TypeName())
		if err == nil && id != h.Identity() {
			return errors.Errorf("component definition %q: the type name now resolves to %s, which another document declares, and not to %s; rename one of them",
				h.TypeName(), id, h.Identity())
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
		err := dec.Decode(&probe)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if probe.Kind == definition.Kind {
			return true, nil
		}
	}
}
