package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The environment set's kind and the file name `kurel build --environment` looks for
// next to app.yaml when --environments is not given. Both are named only here, so a
// rename touches this block alone.
const (
	environmentSetKind   = "EnvironmentSet"
	environmentsFileName = "environments.yaml"
)

// environmentSet is a build-time document binding environment names to a
// ClusterProfile and an optional values file (go-kure/launcher#291). It is a
// deployer input, not part of the package: kurel.yaml stays the package author's
// public API, and a profile belongs to whoever operates the target cluster.
type environmentSet struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Metadata   environmentSetMetadata `yaml:"metadata,omitempty"`
	Spec       environmentSetSpec     `yaml:"spec"`
}

type environmentSetMetadata struct {
	Name string `yaml:"name,omitempty"`
}

type environmentSetSpec struct {
	Environments []environmentBinding `yaml:"environments"`
}

// environmentBinding is one named profile+values pair. Profile is required; Values
// is optional so an application without a kurel.yaml can still be bound to an
// environment. Relative paths resolve against the environments file's directory.
type environmentBinding struct {
	Name    string `yaml:"name"`
	Profile string `yaml:"profile"`
	Values  string `yaml:"values,omitempty"`
}

// resolveEnvironment turns --environment into the --profile/--values it stands
// for. It is a no-op when --environment is unset; the flag set already rejects
// --environment combined with --profile or --values, so filling both fields here
// never overrides an explicit flag.
func resolveEnvironment(opts *buildOptions, appDir string) error {
	if opts.environment == "" {
		if opts.environmentsPath != "" {
			return errors.New("--environments requires --environment")
		}
		return nil
	}

	path := opts.environmentsPath
	if path == "" {
		path = filepath.Join(appDir, environmentsFileName)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return errors.Wrapf(err, "reading environments file %q", path)
	}
	doc, err := parseEnvironmentSet(data)
	if err != nil {
		return errors.Wrapf(err, "parsing environments file %q", path)
	}

	binding, ok := doc.lookup(opts.environment)
	if !ok {
		return errors.Errorf("environment %q is not declared in %q (declared: %s)",
			opts.environment, path, strings.Join(doc.names(), ", "))
	}

	baseDir := filepath.Dir(path)
	opts.profilePath = resolveRelative(baseDir, binding.Profile)
	if binding.Values != "" {
		opts.valuesPath = resolveRelative(baseDir, binding.Values)
	}
	return nil
}

// parseEnvironmentSet decodes an EnvironmentSet document in strict mode and validates it.
func parseEnvironmentSet(data []byte) (*environmentSet, error) {
	var doc environmentSet
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.Wrap(err, "decoding")
	}

	if doc.APIVersion != oam.SupportedAPIVersion {
		return nil, errors.Errorf("unsupported apiVersion %q, expected %q", doc.APIVersion, oam.SupportedAPIVersion)
	}
	if doc.Kind != environmentSetKind {
		return nil, errors.Errorf("expected kind %s, got %q", environmentSetKind, doc.Kind)
	}
	if len(doc.Spec.Environments) == 0 {
		return nil, errors.New("spec.environments declares no environments")
	}

	seen := make(map[string]bool, len(doc.Spec.Environments))
	for i, env := range doc.Spec.Environments {
		if env.Name == "" {
			return nil, errors.Errorf("spec.environments[%d].name is required", i)
		}
		if errs := validation.IsDNS1123Label(env.Name); len(errs) > 0 {
			return nil, errors.Errorf("spec.environments[%d].name %q is not a DNS-1123 label: %s",
				i, env.Name, strings.Join(errs, "; "))
		}
		if seen[env.Name] {
			return nil, errors.Errorf("duplicate environment name %q", env.Name)
		}
		seen[env.Name] = true
		if env.Profile == "" {
			return nil, errors.Errorf("environment %q: profile is required", env.Name)
		}
	}
	return &doc, nil
}

func (d *environmentSet) lookup(name string) (environmentBinding, bool) {
	for _, env := range d.Spec.Environments {
		if env.Name == name {
			return env, true
		}
	}
	return environmentBinding{}, false
}

func (d *environmentSet) names() []string {
	names := make([]string, 0, len(d.Spec.Environments))
	for _, env := range d.Spec.Environments {
		names = append(names, env.Name)
	}
	sort.Strings(names)
	return names
}

func resolveRelative(baseDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}
