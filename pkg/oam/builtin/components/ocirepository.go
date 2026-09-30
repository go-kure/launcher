package components

import (
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// OCIRepositoryHandler handles the kind-named `ocirepository` component: a 1:1
// projection of Flux's OCIRepositorySpec. It emits one OCIRepository named after
// the component, and no Kustomization (that is what the `oci` component adds).
// See fluxsource.go for what the source components share.
type OCIRepositoryHandler struct{}

// CanHandle returns true for the ocirepository component type.
func (h *OCIRepositoryHandler) CanHandle(componentType string) bool {
	return componentType == "ocirepository"
}

// PropertySchema declares every top-level key of sourcev1.OCIRepositorySpec. A
// test ties this key set to the struct.
func (h *OCIRepositoryHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"url":                fluxSourceRequiredString("OCIRepository spec.url: an oci:// artifact repository URL. Its host must be in the policy's allowed registries when that list is non-empty."),
		"ref":                fluxSourceObject("OCIRepository spec.ref: the tag, semver range or digest to pull (Flux defaults to the latest tag)."),
		"layerSelector":      fluxSourceObject("OCIRepository spec.layerSelector: which artifact layer to extract or copy."),
		"provider":           fluxSourceString("OCIRepository spec.provider for authentication: generic, aws, azure or gcp."),
		"secretRef":          fluxSourceObject("OCIRepository spec.secretRef: the registry login Secret, in the namespace the OCIRepository lands in."),
		"verify":             fluxSourceObject("OCIRepository spec.verify: signature verification of the artifact."),
		"serviceAccountName": fluxSourceString("OCIRepository spec.serviceAccountName whose image pull secrets authenticate the pull."),
		"certSecretRef":      fluxSourceObject("OCIRepository spec.certSecretRef: the Secret holding a client certificate and/or CA certificate."),
		"proxySecretRef":     fluxSourceObject("OCIRepository spec.proxySecretRef: the Secret holding the proxy configuration."),
		"interval":           fluxSourceString("OCIRepository spec.interval as a duration (e.g. 10m); defaults to 60m when unset or zero."),
		"timeout":            fluxSourceString("OCIRepository spec.timeout for remote operations, as a duration."),
		"ignore":             fluxSourceString("OCIRepository spec.ignore: exclusion patterns in .sourceignore format."),
		"insecure":           fluxSourceBool("OCIRepository spec.insecure: allow a non-TLS registry."),
		"suspend":            fluxSourceBool("OCIRepository spec.suspend: stop reconciling the source. Also skips the auto health check."),
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// sourcev1.OCIRepositorySpec: any key OCIRepositorySpec does not declare, at any
// depth, and any wrongly typed value is an error. Checks: url is set and starts
// with oci://.
func (h *OCIRepositoryHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[sourcev1.OCIRepositorySpec](component.Properties)
	if err != nil {
		return nil, errors.Errorf("ocirepository: properties do not decode as an OCIRepositorySpec: %w", err)
	}
	cfg := &OCIRepositoryConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// OCIRepositoryConfig implements stack.ApplicationConfig for ocirepository
// components.
type OCIRepositoryConfig struct {
	// Name is the component name, and the OCIRepository's name.
	Name string
	// Namespace is the application namespace. The OCIRepository lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string
	// Spec is the OCIRepository spec as authored. Generate copies it and
	// applies the interval default to the copy.
	Spec sourcev1.OCIRepositorySpec

	// fluxNS overrides the OCIRepository's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace.
	fluxNS string
}

// validate holds the checks shared by the parse path and Generate, which
// repeats them for a config built directly by a library caller.
func (c *OCIRepositoryConfig) validate() error {
	return checkFluxSourceURL("ocirepository", "url", c.Spec.URL, "oci://")
}

// ApplyPolicy rejects a url whose registry host is not in the policy's allowed
// registries.
func (c *OCIRepositoryConfig) ApplyPolicy(p oam.Policy) error {
	return enforceFluxSourceHost("ocirepository", "url", c.Spec.URL, p)
}

// SetFluxNamespace moves the OCIRepository to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *OCIRepositoryConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// EmitsAutoHealthCheck vetoes the auto health check for `suspend: true`, where
// the document tells source-controller not to reconcile the source. Satisfies
// pkg/oam.autoHealthCheckEmitter.
func (c *OCIRepositoryConfig) EmitsAutoHealthCheck() bool { return !c.Spec.Suspend }

// Generate emits the OCIRepository.
func (c *OCIRepositoryConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	repo := fluxcd.CreateOCIRepository(c.Name, fluxSourceNamespace(c.Namespace, c.fluxNS))
	// A deep copy, so no render shares a pointer or slice with the config.
	repo.Spec = *c.Spec.DeepCopy()
	defaultFluxSourceInterval(&repo.Spec.Interval)
	obj := client.Object(repo)
	return []*client.Object{&obj}, nil
}
