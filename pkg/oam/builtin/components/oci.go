package components

import (
	"strings"
	"time"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
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

// PropertySchema declares the oci component's user-facing properties.
func (h *OCIHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "OCIRepository source of the artifact to reconcile.",
			Properties: map[string]oam.PropertySchema{
				"url": {Type: oam.PropertyTypeString, Required: true, Description: "OCI artifact URL (must use the oci:// scheme)."},
			},
		},
		"version":         {Type: oam.PropertyTypeString, Required: true, Description: "Artifact version to reconcile: a tag or sha256:<digest>."},
		"path":            {Type: oam.PropertyTypeString, Default: "./", Description: "Path within the artifact that the Kustomization reconciles."},
		"prune":           {Type: oam.PropertyTypeBoolean, Default: true, Description: "Whether the Kustomization prunes resources removed from the source."},
		"interval":        {Type: oam.PropertyTypeString, Description: "Reconciliation interval as a Go duration (default 60m)."},
		"targetNamespace": {Type: oam.PropertyTypeString, Description: "Namespace into which the Kustomization applies resources."},
		"wait":            {Type: oam.PropertyTypeBoolean, Description: "Set the Kustomization's spec.wait: Flux waits for every resource it applies to become ready before reporting the Kustomization ready. Unset or false emits nothing. Cannot be combined with a non-empty healthChecks, which kustomize-controller ignores when wait is true."},
		"healthChecks": {
			Type:        oam.PropertyTypeArray,
			Description: "Objects listed, in authored order, in the Kustomization's spec.healthChecks: Flux reports the Kustomization ready only once these are ready. The component delivers an opaque artifact, so the list is authored, never derived. An empty list emits nothing. Cannot be combined with wait: true.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One object Flux checks for readiness.",
				Properties: map[string]oam.PropertySchema{
					"apiVersion": {Type: oam.PropertyTypeString, Required: true, Description: "API version of the object, including its group (e.g. apps/v1)."},
					"kind":       {Type: oam.PropertyTypeString, Required: true, Description: "Kind of the object (e.g. Deployment)."},
					"name":       {Type: oam.PropertyTypeString, Required: true, Description: "Name of the object."},
					"namespace":  {Type: oam.PropertyTypeString, Description: "Namespace of the object; omit for a cluster-scoped kind."},
				},
			},
		},
	}
}

// ociHealthCheckKeys is the accepted key set of one `healthChecks` entry, the
// fields of Flux's NamespacedObjectKindReference. Any other key is refused.
var ociHealthCheckKeys = []string{"apiVersion", "kind", "name", "namespace"}

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
//	wait: false                                       # optional, no default; true sets spec.wait
//	healthChecks:                                     # optional, not with wait: true; sets spec.healthChecks, in order
//	  - apiVersion: apps/v1                           # required
//	    kind: Deployment                              # required
//	    name: my-workload                             # required
//	    namespace: my-workload                        # optional; omit for a cluster-scoped kind
//
// wait and healthChecks are opt-in (go-kure/launcher#432): a document authoring
// neither, wait: false, or an empty healthChecks list builds the same
// Kustomization it always did. wait: true together with a non-empty
// healthChecks is refused, because kustomize-controller ignores healthChecks
// when wait is true.
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
	// A wrongly typed url or version is named as a type error, not reported
	// missing (go-kure/launcher#453).
	srcURL, present, err := parseStringField(src, "url", "oci: source.url")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("oci: source.url is required")
	}
	cfg.URL = srcURL
	if !strings.HasPrefix(cfg.URL, "oci://") {
		return nil, errors.Errorf("oci: source.url %q must use the oci:// scheme", cfg.URL)
	}

	version, present, err := parseStringField(props, "version", "oci: version")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("oci: version is required (a tag, or sha256:<digest>)")
	}
	cfg.Version = version

	if p, present, err := parseStringField(props, "path", "path"); err != nil {
		return nil, err
	} else if present {
		cfg.Path = p
	}
	if pr, err := parseBoolField(props, "prune", "prune"); err != nil {
		return nil, err
	} else if pr != nil {
		cfg.Prune = *pr
	}
	interval, _, err := parseStringField(props, "interval", "interval")
	if err != nil {
		return nil, err
	}
	cfg.Interval = interval
	if cfg.Interval != "" {
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return nil, errors.Errorf("oci: interval %q is invalid: must be a valid Go duration (e.g. 10m, 1h30m)", cfg.Interval)
		}
	}
	targetNamespace, _, err := parseStringField(props, "targetNamespace", "targetNamespace")
	if err != nil {
		return nil, err
	}
	cfg.TargetNamespace = targetNamespace

	if w, err := parseBoolField(props, "wait", "wait"); err != nil {
		return nil, err
	} else if w != nil {
		cfg.Wait = *w
	}
	healthChecks, err := parseOCIHealthChecks(props)
	if err != nil {
		return nil, err
	}
	cfg.HealthChecks = healthChecks
	if cfg.Wait && len(cfg.HealthChecks) > 0 {
		return nil, errors.New("oci: wait: true and healthChecks are mutually exclusive: kustomize-controller ignores healthChecks when wait is true, so the listed checks would never run; drop wait to check only the listed objects, or drop healthChecks to wait for everything applied")
	}

	return cfg, nil
}

// parseOCIHealthChecks reads the optional `healthChecks` list. Absent, null or
// empty yields nil. Each entry must be an object with only the keys in
// ociHealthCheckKeys; apiVersion, kind and name are required non-empty
// strings — without the group in apiVersion the entry does not identify the
// object Flux is meant to check — and namespace is an optional string, left
// out for a cluster-scoped kind.
func parseOCIHealthChecks(props map[string]any) ([]meta.NamespacedObjectKindReference, error) {
	entries, _, err := parseObjectList(props, "healthChecks")
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	refs := make([]meta.NamespacedObjectKindReference, 0, len(entries))
	for i, m := range entries {
		label := indexedLabel("healthChecks", i)
		if err := rejectUnknownKeys(m, ociHealthCheckKeys, label); err != nil {
			return nil, err
		}
		apiVersion, err := requiredStringField(m, "apiVersion", label)
		if err != nil {
			return nil, err
		}
		kind, err := requiredStringField(m, "kind", label)
		if err != nil {
			return nil, err
		}
		name, err := requiredStringField(m, "name", label)
		if err != nil {
			return nil, err
		}
		namespace, _, err := parseStringField(m, "namespace", label+".namespace")
		if err != nil {
			return nil, err
		}
		refs = append(refs, meta.NamespacedObjectKindReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
		})
	}
	return refs, nil
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

	// Wait sets the Kustomization's spec.wait; false emits nothing.
	Wait bool
	// HealthChecks become the Kustomization's spec.healthChecks, in order.
	// Never non-empty while Wait is true: kustomize-controller ignores
	// healthChecks when wait is true, so ToApplicationConfig refuses the pair.
	HealthChecks []meta.NamespacedObjectKindReference

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
		repo.Spec.URL = c.URL
		repo.Spec.Interval = interval
		fluxcd.SetOCIRepositoryReference(repo, ociRef(c.Version))
		obj := client.Object(repo)
		objects = append(objects, &obj)
	}

	kz := fluxcd.CreateKustomization(c.Name, c.fluxNamespace())
	kz.Spec.Interval = interval
	kz.Spec.Path = c.Path
	kz.Spec.Prune = c.Prune
	kz.Spec.SourceRef = kustv1.CrossNamespaceSourceReference{
		Kind: "OCIRepository",
		Name: srcName,
	}
	if c.TargetNamespace != "" {
		kz.Spec.TargetNamespace = c.TargetNamespace
	}
	// kure has no Kustomization wait setter. spec.wait is omitempty, so false
	// leaves the emitted document unchanged either way.
	if c.Wait {
		kz.Spec.Wait = true
	}
	// Appended one by one onto the fresh Kustomization's nil slice, so a render
	// never shares a backing array with the config (Generate may run again).
	for _, ref := range c.HealthChecks {
		fluxcd.AddKustomizationHealthCheck(kz, ref)
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
