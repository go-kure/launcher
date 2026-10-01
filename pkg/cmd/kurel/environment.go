package kurel

import (
	"bytes"
	stderrors "errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"
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
// environment. Both paths are relative to the environments file's directory and may
// not leave it, textually (validateBindingPath) or through a symlink (containedPath).
type environmentBinding struct {
	Name    string `yaml:"name"`
	Profile string `yaml:"profile"`
	Values  string `yaml:"values,omitempty"`
}

// resolveEnvironment turns --environment into the --profile/--values it stands
// for. It is a no-op when --environment is not passed; the flag set already rejects
// --environment combined with --profile or --values, so filling both fields here
// never overrides an explicit flag. Presence is read from flags, not from the
// values, so an explicitly empty --environment or --environments (an unset
// variable in a wrapper script) is an error rather than silently ignored.
func resolveEnvironment(opts *buildOptions, appDir string, flags *pflag.FlagSet) error {
	if !flags.Changed("environment") {
		if flags.Changed("environments") {
			return errors.New("--environments requires --environment")
		}
		return nil
	}
	if opts.environment == "" {
		return errors.New("--environment requires a non-empty name")
	}
	if flags.Changed("environments") && opts.environmentsPath == "" {
		return errors.New("--environments requires a non-empty path")
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

	baseDir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return errors.Wrapf(err, "resolving the directory of environments file %q", path)
	}
	profilePath, err := containedPath(baseDir, opts.environment, "profile", binding.Profile)
	if err != nil {
		return err
	}
	opts.profilePath = profilePath
	if binding.Values != "" {
		valuesPath, err := containedPath(baseDir, opts.environment, "values", binding.Values)
		if err != nil {
			return err
		}
		opts.valuesPath = valuesPath
	}
	return nil
}

// containedPath joins rel onto baseDir (already symlink-free), resolves symlinks,
// and rejects a target outside baseDir. validateBindingPath only checks the text of
// the path; a symlink such as profiles -> /elsewhere passes that check, so the
// resolved target is what gets checked and returned for build to read.
func containedPath(baseDir, env, field, rel string) (string, error) {
	target, err := filepath.EvalSymlinks(filepath.Join(baseDir, rel))
	if err != nil {
		return "", errors.Wrapf(err, "environment %q: resolving %s %q", env, field, rel)
	}
	inner, err := filepath.Rel(baseDir, target)
	if err != nil || inner == ".." || strings.HasPrefix(inner, ".."+string(filepath.Separator)) {
		return "", errors.Errorf("environment %q: %s %q resolves outside the environments file's directory", env, field, rel)
	}
	return target, nil
}

// parseEnvironmentSet decodes an EnvironmentSet document in strict mode and validates it.
func parseEnvironmentSet(data []byte) (*environmentSet, error) {
	var doc environmentSet
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, errors.Wrap(err, "decoding")
	}
	// One document per file: a trailing `---` document would otherwise be skipped
	// unread, silently dropping whatever it declares.
	var extra yaml.Node
	if err := dec.Decode(&extra); !stderrors.Is(err, io.EOF) {
		if err != nil {
			return nil, errors.Wrap(err, "decoding")
		}
		return nil, errors.New("expected a single YAML document, found more than one")
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
		if err := validateBindingPath(env.Name, "profile", env.Profile); err != nil {
			return nil, err
		}
		if err := validateBindingPath(env.Name, "values", env.Values); err != nil {
			return nil, err
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

// validateBindingPath rejects an absolute path or one with a ".." element, per
// AGENTS.md's "reject paths that escape the working directory". Unlike --profile and
// --values, these paths come from file content, and the default environments.yaml
// sits inside the package directory, so they are not the operator's own command
// line. An empty path (an omitted values) passes.
func validateBindingPath(env, field, p string) error {
	if filepath.IsAbs(p) {
		return errors.Errorf("environment %q: %s must be relative to the environments file, got %q", env, field, p)
	}
	for _, elem := range strings.Split(filepath.ToSlash(p), "/") {
		if elem == ".." {
			return errors.Errorf("environment %q: %s must not contain \"..\", got %q", env, field, p)
		}
	}
	return nil
}
