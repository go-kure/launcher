package components

import (
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// GitRepositoryHandler handles the kind-named `gitrepository` component: a 1:1
// projection of Flux's GitRepositorySpec. It emits one GitRepository named after
// the component. See fluxsource.go for what the source components share.
type GitRepositoryHandler struct{}

// gitRepositoryDurations are GitRepositorySpec's duration fields. timeout takes no h unit.
var gitRepositoryDurations = []fluxDurationField[sourcev1.GitRepositorySpec]{
	{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *sourcev1.GitRepositorySpec) *metav1.Duration { return &s.Interval }},
	{path: []string{"timeout"}, form: fluxduration.SourceTimeout, get: func(s *sourcev1.GitRepositorySpec) *metav1.Duration { return s.Timeout }},
}

// CanHandle returns true for the gitrepository component type.
func (h *GitRepositoryHandler) CanHandle(componentType string) bool {
	return componentType == "gitrepository"
}

// PropertySchema declares every top-level key of sourcev1.GitRepositorySpec. A
// test ties this key set to the struct.
func (h *GitRepositoryHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"url":                fluxSourceRequiredString("GitRepository spec.url: an http://, https:// or ssh:// URL. Its host (the user of an ssh:// URL dropped) must be in the policy's allowed registries when that list is non-empty."),
		"secretRef":          fluxSourceObject("GitRepository spec.secretRef: the Secret holding the credentials, in the namespace the GitRepository lands in."),
		"provider":           fluxSourceString("GitRepository spec.provider for authentication: generic, aws, azure or github."),
		"serviceAccountName": fluxSourceString("GitRepository spec.serviceAccountName that authenticates the clone (azure and aws providers)."),
		"interval":           fluxSourceString("GitRepository spec.interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms. Defaults to 60m when unset or zero."),
		"timeout":            fluxSourceString("GitRepository spec.timeout for Git operations, as a Flux duration: unsigned, units ms, s, m (no h), e.g. 30s or 5m; 0s or at least 1ms. An hour or more is emitted in minutes, 90m as 90m0s."),
		"ref":                fluxSourceObject("GitRepository spec.ref: the branch, tag, semver range, reference name or commit to check out."),
		"verify":             fluxSourceObject("GitRepository spec.verify: commit signature verification."),
		"proxySecretRef":     fluxSourceObject("GitRepository spec.proxySecretRef: the Secret holding the proxy configuration."),
		"ignore":             fluxSourceString("GitRepository spec.ignore: exclusion patterns in .sourceignore format."),
		"suspend":            fluxSourceBool("GitRepository spec.suspend: stop reconciling the source. Also skips the auto health check."),
		"recurseSubmodules":  fluxSourceBool("GitRepository spec.recurseSubmodules: initialise submodules in the clone."),
		"include": {
			Type:        oam.PropertyTypeArray,
			Description: "GitRepository spec.include: other GitRepositories, in the same namespace, whose artifacts are copied into this one.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "One include (repository, fromPath, toPath)."},
		},
		"sparseCheckout": {
			Type:        oam.PropertyTypeArray,
			Description: "GitRepository spec.sparseCheckout: the directories to check out.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One directory."},
		},
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// sourcev1.GitRepositorySpec: any key GitRepositorySpec does not declare, at any
// depth, and any wrongly typed value is an error. Checks: url is set and starts
// with http://, https:// or ssh://.
func (h *GitRepositoryHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[sourcev1.GitRepositorySpec](component.Properties)
	if err != nil {
		return nil, errors.Errorf("gitrepository: properties do not decode as a GitRepositorySpec: %w", err)
	}
	if err := checkAuthoredFluxDurations("gitrepository", component.Properties, gitRepositoryDurations); err != nil {
		return nil, err
	}
	cfg := &GitRepositoryConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// GitRepositoryConfig implements stack.ApplicationConfig for gitrepository
// components.
type GitRepositoryConfig struct {
	// Name is the component name, and the GitRepository's name.
	Name string
	// Namespace is the application namespace. The GitRepository lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string
	// Spec is the GitRepository spec as authored. Generate copies it and
	// applies the interval default to the copy.
	Spec sourcev1.GitRepositorySpec

	// fluxNS overrides the GitRepository's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace.
	fluxNS string
}

// validate holds the checks shared by the parse path and Generate, which
// repeats them for a config built directly by a library caller.
func (c *GitRepositoryConfig) validate() error {
	if err := checkFluxDurations("gitrepository", &c.Spec, gitRepositoryDurations); err != nil {
		return err
	}
	return checkFluxSourceURL("gitrepository", "url", c.Spec.URL, "http://", "https://", "ssh://")
}

// ApplyPolicy rejects a url whose host is not in the policy's allowed
// registries. For an ssh:// URL the user (git@) is not part of the host.
func (c *GitRepositoryConfig) ApplyPolicy(p oam.Policy) error {
	return enforceFluxSourceHost("gitrepository", "url", c.Spec.URL, p)
}

// SetFluxNamespace moves the GitRepository to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *GitRepositoryConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// EmitsAutoHealthCheck vetoes the auto health check for `suspend: true`, where
// the document tells source-controller not to reconcile the source. Satisfies
// pkg/oam.autoHealthCheckEmitter.
func (c *GitRepositoryConfig) EmitsAutoHealthCheck() bool { return !c.Spec.Suspend }

// Generate emits the GitRepository.
func (c *GitRepositoryConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	repo := fluxcd.CreateGitRepository(c.Name, fluxSourceNamespace(c.Namespace, c.fluxNS))
	// A deep copy, so no render shares a pointer or slice with the config.
	repo.Spec = *c.Spec.DeepCopy()
	defaultFluxSourceInterval(&repo.Spec.Interval)
	return emitFluxSource("gitrepository", repo, repo.Spec.Timeout)
}
