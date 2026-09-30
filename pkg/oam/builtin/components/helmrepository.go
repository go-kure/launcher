package components

import (
	"strings"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// HelmRepositoryHandler handles the kind-named `helmrepository` component: a 1:1
// projection of Flux's HelmRepositorySpec. It emits one HelmRepository named
// after the component. See fluxsource.go for what the source components share.
type HelmRepositoryHandler struct{}

// CanHandle returns true for the helmrepository component type.
func (h *HelmRepositoryHandler) CanHandle(componentType string) bool {
	return componentType == "helmrepository"
}

// PropertySchema declares every top-level key of sourcev1.HelmRepositorySpec. A
// test ties this key set to the struct.
func (h *HelmRepositoryHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"url":             fluxSourceRequiredString("HelmRepository spec.url: an http://, https:// or oci:// URL, and oci:// when type is oci. Its host must be in the policy's allowed registries when that list is non-empty; an oci:// URL must then also name its registry explicitly, as localhost or a host containing . or : (Flux reads any other first segment as a Docker Hub namespace)."),
		"secretRef":       fluxSourceObject("HelmRepository spec.secretRef: the Secret holding the repository credentials, in the namespace the HelmRepository lands in."),
		"certSecretRef":   fluxSourceObject("HelmRepository spec.certSecretRef: the Secret holding a client certificate and/or CA certificate."),
		"passCredentials": fluxSourceBool("HelmRepository spec.passCredentials: pass the secretRef credentials to chart hosts other than the url's."),
		"interval":        fluxSourceString("HelmRepository spec.interval as a duration (e.g. 10m); defaults to 60m when unset or zero, except with type oci."),
		"insecure":        fluxSourceBool("HelmRepository spec.insecure: allow a non-TLS registry (type oci only)."),
		"timeout":         fluxSourceString("HelmRepository spec.timeout for the index fetch or OCI operations, as a duration."),
		"suspend":         fluxSourceBool("HelmRepository spec.suspend: stop reconciling the repository. Also skips the auto health check."),
		"accessFrom":      fluxSourceObject("HelmRepository spec.accessFrom: the cross-namespace access control list."),
		"type":            fluxSourceString("HelmRepository spec.type: default, or oci for an OCI registry of charts (a static object with no interval default and no auto health check)."),
		"provider":        fluxSourceString("HelmRepository spec.provider for OCI authentication: generic, aws, azure or gcp (type oci only)."),
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// sourcev1.HelmRepositorySpec: any key HelmRepositorySpec does not declare, at
// any depth, and any wrongly typed value is an error. Checks: url is set and
// starts with http://, https:// or oci://, and with oci:// under type: oci.
func (h *HelmRepositoryHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[sourcev1.HelmRepositorySpec](component.Properties)
	if err != nil {
		return nil, errors.Errorf("helmrepository: properties do not decode as a HelmRepositorySpec: %w", err)
	}
	cfg := &HelmRepositoryConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// HelmRepositoryConfig implements stack.ApplicationConfig for helmrepository
// components.
type HelmRepositoryConfig struct {
	// Name is the component name, and the HelmRepository's name.
	Name string
	// Namespace is the application namespace. The HelmRepository lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string
	// Spec is the HelmRepository spec as authored. Generate copies it and
	// applies the interval default to the copy.
	Spec sourcev1.HelmRepositorySpec

	// fluxNS overrides the HelmRepository's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace.
	fluxNS string
}

// isOCI reports whether the repository is an OCI registry of charts.
func (c *HelmRepositoryConfig) isOCI() bool {
	return c.Spec.Type == sourcev1.HelmRepositoryTypeOCI
}

// validate holds the checks shared by the parse path and Generate. Generate
// repeats them because this type and its fields are exported: a config built
// directly by a library caller never went through ToApplicationConfig.
func (c *HelmRepositoryConfig) validate() error {
	if c.isOCI() {
		return checkFluxSourceURL("helmrepository", "url", c.Spec.URL, "oci://")
	}
	return checkFluxSourceURL("helmrepository", "url", c.Spec.URL, "http://", "https://", "oci://")
}

// ApplyPolicy rejects a url whose host is not in the policy's allowed
// registries. An oci:// url, whatever the type, must also name its registry
// explicitly under a non-empty allowlist (enforceFluxSourceOCIHost); it may stop
// at the registry, since Flux appends the chart name.
func (c *HelmRepositoryConfig) ApplyPolicy(p oam.Policy) error {
	if strings.HasPrefix(c.Spec.URL, "oci://") {
		return enforceFluxSourceOCIHost("helmrepository", "url", c.Spec.URL, false, p)
	}
	return enforceFluxSourceHost("helmrepository", "url", c.Spec.URL, p)
}

// SetFluxNamespace moves the HelmRepository to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *HelmRepositoryConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// EmitsAutoHealthCheck vetoes the auto health check for `suspend: true`, where
// the document tells source-controller not to reconcile the repository, and for
// `type: oci`, which Flux treats as a static object: it is not fetched into an
// artifact, so there is no reconcile to wait on. Satisfies
// pkg/oam.autoHealthCheckEmitter.
func (c *HelmRepositoryConfig) EmitsAutoHealthCheck() bool {
	return !c.Spec.Suspend && !c.isOCI()
}

// Generate emits the HelmRepository.
func (c *HelmRepositoryConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	repo := fluxcd.CreateHelmRepository(c.Name, fluxSourceNamespace(c.Namespace, c.fluxNS))
	// A deep copy, so no render shares a pointer or slice with the config.
	repo.Spec = *c.Spec.DeepCopy()
	// Flux does not poll an OCI HelmRepository, so it gets no default.
	if !c.isOCI() {
		defaultFluxSourceInterval(&repo.Spec.Interval)
	}
	obj := client.Object(repo)
	return []*client.Object{&obj}, nil
}
