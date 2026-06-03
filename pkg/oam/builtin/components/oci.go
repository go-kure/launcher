package components

import (
	"strings"
	"time"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// digestPrefix marks an OCI reference as a digest rather than a tag.
const digestPrefix = "sha256:"

// OCIHandler handles the `oci` OAM component type: it emits an OCIRepository
// source CR plus a per-component Flux Kustomization that reconciles the artifact.
// The OCIRepository participates in source dedup (URL+version); the Kustomization
// is always emitted, one per component. Both land in the Flux namespace.
type OCIHandler struct{}

// CanHandle returns true for the oci component type.
func (h *OCIHandler) CanHandle(componentType string) bool { return componentType == "oci" }

// ToApplicationConfig converts an OAM oci component to an OCIConfig.
//
// Properties:
//
//	source:
//	  url: oci://registry.example.com/org/artifact   # required, oci:// scheme
//	version: 1.2.3                                    # required; tag, or sha256:<digest>
//	path: ./                                          # optional, default "./"
//	prune: true                                       # optional, default true
//	interval: 60m                                     # optional, default 60m
//	targetNamespace: my-workload                      # optional
func (h *OCIHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	cfg := &OCIConfig{
		Name:      component.Name,
		Namespace: namespace,
		Path:      "./",
		Prune:     true,
	}

	props := component.Properties

	src, ok := props["source"].(map[string]any)
	if !ok {
		return nil, errors.New("oci: source is required")
	}
	cfg.URL, _ = src["url"].(string)
	if cfg.URL == "" {
		return nil, errors.New("oci: source.url is required")
	}
	if !strings.HasPrefix(cfg.URL, "oci://") {
		return nil, errors.Errorf("oci: source.url %q must use the oci:// scheme", cfg.URL)
	}

	cfg.Version, _ = props["version"].(string)
	if cfg.Version == "" {
		return nil, errors.New("oci: version is required (a tag, or sha256:<digest>)")
	}

	if p, ok := props["path"].(string); ok && p != "" {
		cfg.Path = p
	}
	if pr, ok := props["prune"].(bool); ok {
		cfg.Prune = pr
	}
	cfg.Interval, _ = props["interval"].(string)
	if cfg.Interval != "" {
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return nil, errors.Errorf("oci: interval %q is invalid: must be a valid Go duration (e.g. 10m, 1h30m)", cfg.Interval)
		}
	}
	cfg.TargetNamespace, _ = props["targetNamespace"].(string)

	return cfg, nil
}

// OCIConfig implements stack.ApplicationConfig for oci components.
type OCIConfig struct {
	Name      string
	Namespace string

	URL     string // oci:// artifact URL
	Version string // tag, or sha256:<digest>

	Path            string
	Prune           bool
	Interval        string
	TargetNamespace string

	// dedup state: when another component owns an identical OCIRepository,
	// this config suppresses its own source CR and the Kustomization references
	// the shared source by name instead.
	suppressSource bool
	sharedSrcName  string

	// fluxNS overrides the namespace for the emitted Flux control-plane CRs
	// (OCIRepository, Kustomization). Set by postProcessFluxNamespace via
	// TransformContext.FluxNamespace. Empty means use c.Namespace.
	fluxNS string
}

// ApplyPolicy rejects a disallowed OCI registry host. It reads the allowlist
// through the oam.Policy interface (AllowedRegistries) so any policy
// implementation enforces correctly; an empty allowlist permits all hosts.
func (c *OCIConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforceAllowedURLHosts(c.URL, p.AllowedRegistries())
}

// GetSourceKey returns the dedup key for the OCIRepository source CR. Uses the
// same form as helmchart's OCIRepository ("oci:<url>:<version>") so an oci
// component and a helmchart-over-OCI sharing one artifact dedup together.
// First component wins.
func (c *OCIConfig) GetSourceKey() string {
	return "oci:" + c.URL + ":" + c.Version
}

// GetSourceRefName returns the name used to reference this component's source CR.
func (c *OCIConfig) GetSourceRefName() string { return c.Name }

// SuppressSourceGeneration instructs this config to skip emitting its own
// OCIRepository and reference the named shared source instead.
func (c *OCIConfig) SuppressSourceGeneration(refName string) {
	c.suppressSource = true
	c.sharedSrcName = refName
}

// SetFluxNamespace re-stamps the namespace for the OCIRepository and
// Kustomization. Satisfies pkg/oam.fluxNamespaceSettable.
func (c *OCIConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// fluxNamespace returns the namespace for the emitted Flux control-plane CRs.
func (c *OCIConfig) fluxNamespace() string {
	if c.fluxNS != "" {
		return c.fluxNS
	}
	return c.Namespace
}

// Generate emits the OCIRepository (unless deduped away) and a per-component
// Flux Kustomization referencing it. Both land in the Flux namespace.
func (c *OCIConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	var objects []*client.Object
	interval := parseDuration(effectiveInterval(c.Interval))

	srcName := c.Name
	if c.suppressSource && c.sharedSrcName != "" {
		srcName = c.sharedSrcName
	}

	if !c.suppressSource {
		repo := fluxcd.CreateOCIRepository(c.Name, c.fluxNamespace())
		fluxcd.SetOCIRepositoryURL(repo, c.URL)
		fluxcd.SetOCIRepositoryInterval(repo, interval)
		fluxcd.SetOCIRepositoryReference(repo, ociRef(c.Version))
		obj := client.Object(repo)
		objects = append(objects, &obj)
	}

	kz := fluxcd.CreateKustomization(c.Name, c.fluxNamespace())
	fluxcd.SetKustomizationInterval(kz, interval)
	fluxcd.SetKustomizationPath(kz, c.Path)
	fluxcd.SetKustomizationPrune(kz, c.Prune)
	fluxcd.SetKustomizationSourceRef(kz, kustv1.CrossNamespaceSourceReference{
		Kind: "OCIRepository",
		Name: srcName,
	})
	if c.TargetNamespace != "" {
		fluxcd.SetKustomizationTargetNamespace(kz, c.TargetNamespace)
	}
	obj := client.Object(kz)
	objects = append(objects, &obj)

	return objects, nil
}

// ociRef builds an OCIRepositoryRef from a version string: a sha256: prefix
// selects a digest, otherwise the value is treated as a tag.
func ociRef(version string) *sourcev1.OCIRepositoryRef {
	if strings.HasPrefix(version, digestPrefix) {
		return &sourcev1.OCIRepositoryRef{Digest: version}
	}
	return &sourcev1.OCIRepositoryRef{Tag: version}
}
