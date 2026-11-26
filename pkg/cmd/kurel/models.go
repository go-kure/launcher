package kurel

import (
	"os"
	"path/filepath"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// modelsDirName is the directory next to app.yaml that `kurel build` reads
// component models from.
const modelsDirName = "models"

// loadModels registers on t the configuration-defined component models of one
// build (go-kure/launcher#1570): every *.yaml file of dir in file name order,
// then the files of paths in the order given. A model may name only a model
// registered before it. A dir that does not exist holds no model. Each refusal of
// a model is the library's, with the file it is about.
func loadModels(t *oam.Transformer, dir string, paths []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "reading models directory %q", dir)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	files = append(files, paths...)

	registry := model.NewRegistry(t)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return errors.Wrapf(err, "reading model file %q", file)
		}
		m, err := model.Load(data)
		if err != nil {
			return errors.Wrapf(err, "model file %q", file)
		}
		if err := registry.Register(m); err != nil {
			return errors.Wrapf(err, "model file %q", file)
		}
	}
	return nil
}
