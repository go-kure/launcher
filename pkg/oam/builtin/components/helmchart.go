package components

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// HelmchartHandler handles OAM helmchart components.
type HelmchartHandler struct {
	// ValuesMode sets the registration-time default for the valuesMode
	// property (inline or configMap) when a component does not set it
	// explicitly. Empty means "inline". Lets a downstream platform flip the
	// whole fleet's default without editing every OAM document.
	ValuesMode string
}

// CanHandle returns true for helmchart component type.
func (h *HelmchartHandler) CanHandle(componentType string) bool {
	return componentType == "helmchart"
}

// PropertySchema declares the helmchart component's user-facing properties. The
// Helm `values` tree and the Flux-shaped source/driftDetection/install/upgrade
// blocks are kept open (AdditionalProperties) rather than modeled field-by-field.
func (h *HelmchartHandler) PropertySchema() map[string]oam.PropertySchema {
	openObject := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	// valuesModeDefault mirrors the resolution order ToApplicationConfig uses
	// when the component itself does not set valuesMode: the handler's
	// registration-time default, else "inline". Computed here (not a static
	// "inline") so schema consumers that materialize defaults for absent
	// properties (e.g. applyDefinitionSchema) report the effective default,
	// not a value that ignores h.ValuesMode.
	valuesModeDefault := h.ValuesMode
	if valuesModeDefault == "" {
		valuesModeDefault = "inline"
	}
	return map[string]oam.PropertySchema{
		"chart":           {Type: oam.PropertyTypeString, Description: "Chart name within a HelmRepository source."},
		"version":         {Type: oam.PropertyTypeString, Description: "Chart version to install."},
		"delivery":        {Type: oam.PropertyTypeString, Default: "native", Enum: []any{"native", "template"}, Description: "Delivery mode: native emits a HelmRelease, template renders the chart client-side."},
		"interval":        {Type: oam.PropertyTypeString, Description: "Reconciliation interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms (default 60m)."},
		"releaseName":     {Type: oam.PropertyTypeString, Description: "Helm release name (defaults to the component name)."},
		"targetNamespace": {Type: oam.PropertyTypeString, Description: "Namespace into which the HelmRelease installs resources."},
		"source":          {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "Chart source: an inline url, or a reference (name/kind) to an existing source CR."},
		"values":          openObject("Helm values tree passed to the release."),
		"valuesMode":      {Type: oam.PropertyTypeString, Default: valuesModeDefault, Enum: []any{"inline", "configMap"}, Description: "How Helm values are delivered: inline sets HelmRelease.spec.values directly, configMap externalizes them into a referenced ConfigMap. Not supported under delivery: template."},
		"driftDetection":  openObject("Flux drift detection settings (mode: enabled, warn, or disabled)."),
		"install":         openObject("Helm install options (e.g. crds: Skip, Create, or CreateReplace)."),
		"upgrade":         openObject("Helm upgrade options (e.g. crds: Skip, Create, or CreateReplace)."),
		"valuesFrom":      {Type: oam.PropertyTypeArray, Description: "References to ConfigMaps or Secrets supplying additional Helm values.", Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single ConfigMap or Secret values reference (kind, name, valuesKey, targetPath)."}},
	}
}

// ToApplicationConfig converts an OAM helmchart component to a HelmchartConfig.
func (h *HelmchartHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	cfg := &HelmchartConfig{
		Name:        component.Name,
		Namespace:   namespace,
		renderChart: helm.RenderChart,
	}

	props := component.Properties

	cfg.Chart, _ = props["chart"].(string)
	cfg.Version, _ = props["version"].(string)
	cfg.Delivery, _ = props["delivery"].(string)
	cfg.Interval, _ = props["interval"].(string)
	if cfg.Interval != "" {
		if err := validateFluxInterval("helmchart", cfg.Interval); err != nil {
			return nil, err
		}
	}
	cfg.ReleaseName, _ = props["releaseName"].(string)
	cfg.TargetNamespace, _ = props["targetNamespace"].(string)

	if dd, ok := props["driftDetection"].(map[string]any); ok {
		mode, _ := dd["mode"].(string)
		switch mode {
		case "enabled", "warn", "disabled":
			cfg.DriftMode = mode
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: driftDetection.mode %q is invalid; must be enabled, warn, or disabled", mode)
		}
	}
	if install, ok := props["install"].(map[string]any); ok {
		crds, _ := install["crds"].(string)
		switch crds {
		case "Skip", "Create", "CreateReplace":
			cfg.InstallCRDs = crds
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: install.crds %q is invalid; must be Skip, Create, or CreateReplace", crds)
		}
	}
	if upgrade, ok := props["upgrade"].(map[string]any); ok {
		crds, _ := upgrade["crds"].(string)
		switch crds {
		case "Skip", "Create", "CreateReplace":
			cfg.UpgradeCRDs = crds
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: upgrade.crds %q is invalid; must be Skip, Create, or CreateReplace", crds)
		}
	}
	if vals, ok := props["values"].(map[string]any); ok {
		// values is an open object: property validation checks that the key is
		// a map and stops there, so the contents arrive exactly as yaml.v3
		// decoded them. yaml.v3 resolves `.nan` and `.inf` to non-finite
		// float64s, which encoding/json refuses to marshal — and under the
		// release-1 builder contract the kure setter that inlines this map
		// (fluxcd.SetHelmReleaseValuesFromMap, called from buildHelmRelease)
		// panics on a marshal failure instead of returning an error, its own
		// doc telling a caller whose map can hold such a value to marshal it
		// first. buildHelmRelease has no error to return; this parse does.
		// Checked for every valuesMode, not just the inlining one: the mode is
		// still rewritten after this point (an inherited handler default under
		// delivery: template becomes inline, see ToApplicationConfig), so
		// whether a document is accepted must not depend on it. Generate
		// re-checks the same thing at the emission boundary, which is what
		// covers a config built directly rather than parsed.
		if _, err := json.Marshal(vals); err != nil {
			return nil, errors.Errorf("helmchart: values is not representable as JSON: %w", err)
		}
		cfg.Values = vals
	}
	if vfList, ok := props["valuesFrom"].([]any); ok {
		for i, vf := range vfList {
			vf = nullElem(vf)
			m, ok := vf.(map[string]any)
			if !ok {
				return nil, errors.Errorf("valuesFrom[%d]: expected object, got %T", i, vf)
			}
			vfc := helmv2.ValuesReference{}
			vfc.Kind, _ = m["kind"].(string)
			switch vfc.Kind {
			case "ConfigMap", "Secret":
				// ok
			default:
				return nil, errors.Errorf("valuesFrom[%d]: kind %q is invalid; must be ConfigMap or Secret", i, vfc.Kind)
			}
			name, err := requiredStringField(m, "name", fmt.Sprintf("valuesFrom[%d]", i))
			if err != nil {
				return nil, err
			}
			vfc.Name = name
			vfc.ValuesKey, _ = m["valuesKey"].(string)
			vfc.TargetPath, _ = m["targetPath"].(string)
			cfg.ValuesFrom = append(cfg.ValuesFrom, vfc)
		}
	}

	// Validate delivery
	switch cfg.Delivery {
	case "", "native":
		// ok
	case "template":
		// ok — template-specific validation follows after source block parsing
	default:
		return nil, errors.Errorf("helmchart: unsupported delivery %q; supported values: native, template", cfg.Delivery)
	}

	// Resolve valuesMode: component property → handler registration-time
	// default (h.ValuesMode) → "inline". Mirrors PropertySchema's computed
	// Default above so the resolved value and the reported default agree.
	// valuesModeExplicit tracks whether the component itself set the
	// property, as opposed to inheriting h.ValuesMode — the template-delivery
	// rejection below must fire only on an explicit request; a fleet-wide
	// handler default of "configMap" must not break every template-delivery
	// component that never mentioned valuesMode.
	//
	// This depends on props arriving exactly as the document author wrote it:
	// PropertySchema.Default (schema.go) is documentation for schema
	// consumers, e.g. a downstream validator or doc generator — nothing in
	// this package's own call path (ToApplicationConfig's only caller here is
	// Transformer, transform.go:626, via the unmodified component.Properties)
	// pre-populates an absent property from it before invoking the handler.
	// A caller that DOES materialize schema defaults into props ahead of this
	// call would make valuesModeExplicit see an inherited default as
	// authored, silently defeating the template-delivery fallback below.
	cfg.ValuesMode, _ = props["valuesMode"].(string)
	valuesModeExplicit := cfg.ValuesMode != ""
	if cfg.ValuesMode == "" {
		cfg.ValuesMode = h.ValuesMode
	}
	if cfg.ValuesMode == "" {
		cfg.ValuesMode = "inline"
	}
	switch cfg.ValuesMode {
	case "inline", "configMap":
		// ok
	default:
		return nil, errors.Errorf("helmchart: unsupported valuesMode %q; supported values: inline, configMap", cfg.ValuesMode)
	}

	// Parse source block
	src, ok := props["source"].(map[string]any)
	if !ok {
		return nil, errors.New("helmchart: source is required")
	}
	srcURL, _ := src["url"].(string)
	srcName, _ := src["name"].(string)
	srcKind, _ := src["kind"].(string)
	srcNamespace, _ := src["namespace"].(string)

	if srcURL != "" && srcName != "" {
		return nil, errors.New("helmchart: source.url and source.name are mutually exclusive")
	}
	if srcURL == "" && srcName == "" {
		return nil, errors.New("helmchart: source requires either source.url (inline) or source.name (reference)")
	}

	if srcURL != "" {
		// Form A: inline source — launcher creates the source CR. The kind
		// inference and scheme checks are shared with the helmtemplate terminal.
		cfg.SourceURL = srcURL
		kind, err := inlineChartSourceKind("helmchart", srcURL, srcKind, cfg.Chart)
		if err != nil {
			return nil, err
		}
		cfg.SourceKind = kind
	} else {
		// Form B: reference existing source CR
		if srcKind == "" {
			return nil, errors.New("helmchart: source.kind is required when source.name is set")
		}
		switch srcKind {
		case "HelmRepository", "OCIRepository", "HelmChart":
			// ok
		default:
			return nil, errors.Errorf("helmchart: source.kind %q is not valid for source reference; must be HelmRepository, OCIRepository, or HelmChart", srcKind)
		}
		if srcKind == "HelmRepository" && cfg.Chart == "" {
			return nil, errors.New("helmchart: source.kind HelmRepository requires chart to be specified")
		}
		cfg.SourceRefName = srcName
		cfg.SourceRefKind = srcKind
		cfg.SourceRefNamespace = srcNamespace
	}

	// Template-specific validation (requires source block to be parsed above)
	if cfg.Delivery == "template" {
		if cfg.SourceRefName != "" {
			return nil, errors.New("helmchart: delivery: template requires an inline source URL; source.name is not supported")
		}
		if cfg.SourceKind == "OCIRepository" && cfg.Version == "" {
			return nil, errors.New("helmchart: delivery: template with OCIRepository requires version to be set")
		}
		if len(cfg.ValuesFrom) > 0 {
			return nil, errors.New("helmchart: delivery: template does not support valuesFrom (cluster-side values are not resolvable at build time)")
		}
		if cfg.ValuesMode == "configMap" {
			if valuesModeExplicit {
				return nil, errors.New("helmchart: delivery: template does not support valuesMode: configMap (values are baked into the client-side render at build time, not resolved from a cluster-side ConfigMap)")
			}
			// Inherited from the handler's fleet-wide default, not requested
			// by this component — template delivery has no configMap mode to
			// honor, so fall back to inline rather than rejecting every
			// template-delivery component under a configMap-default handler.
			cfg.ValuesMode = "inline"
		}
		if cfg.ReleaseName != "" {
			return nil, errors.New("helmchart: delivery: template does not support releaseName")
		}
		if cfg.TargetNamespace != "" {
			return nil, errors.New("helmchart: delivery: template does not support targetNamespace")
		}
		if cfg.Interval != "" {
			return nil, errors.New("helmchart: delivery: template does not support interval")
		}
		if cfg.DriftMode != "" {
			return nil, errors.New("helmchart: delivery: template does not support driftDetection")
		}
		if cfg.InstallCRDs != "" || cfg.UpgradeCRDs != "" {
			return nil, errors.New("helmchart: delivery: template does not support install.crds / upgrade.crds")
		}
	}

	return wrapIfHelmchartAugmenter(cfg), nil
}

// wrapIfHelmchartAugmenter returns cfg wrapped in augmentingHelmchartConfig
// whenever AugmentLayout would do anything at all: valuesMode: configMap with
// at least one value to externalize (emitsValuesConfigMap), or delivery:
// template (its AugmentLayout — augmentLayoutTemplate — repartitions the
// rendered chart into hook-ordered child layouts; a no-op when there is at
// most one hook group, but that is only knowable after the network render
// inside Generate, too late for this config-construction-time wrap — see the
// package doc / README for the resulting on-disk-path caveat). kure's layout
// walker type-asserts layout.LayoutAugmenter by PRESENCE
// (pkg/stack/layout/walker.go) to decide whether an app gets its own
// flat-bundle sub-layout or merges into its parent's — a structural decision,
// not a side effect with a safe no-op default. So *HelmchartConfig itself
// must stay free of the AugmentLayout method (a config that needs neither
// branch must not satisfy the interface), and the wrapper is applied
// conditionally here rather than unconditionally. See also
// traits.wrapIfAugmenter, which forwards this presence through trait
// decorators generically.
//
// Both wrapped cases satisfy layout.LayoutAugmenter identically, but differ
// in whether Generate's own output is already a complete superset of what
// AugmentLayout adds: delivery: template's AugmentLayout only repartitions
// Generate's flat union (nothing new), while valuesMode: configMap's adds a
// values ConfigMap Generate never emits itself. pkg/cmd/kurel's build guard
// (rejectLayoutAugmenters), which never constructs or walks a
// layout.ManifestLayout, consults GenerateCoversAugmentLayout below to tell
// the two apart — a config for which skipping AugmentLayout loses nothing is
// let through; one for which it would (the fail-closed default) is rejected.
func wrapIfHelmchartAugmenter(cfg *HelmchartConfig) stack.ApplicationConfig {
	if cfg.emitsValuesConfigMap() || cfg.Delivery == "template" {
		return &augmentingHelmchartConfig{cfg}
	}
	return cfg
}

// HelmchartConfig implements stack.ApplicationConfig for helmchart components.
type HelmchartConfig struct {
	Name      string
	Namespace string
	Chart     string
	Version   string
	Delivery  string

	// ValuesMode selects how Values reaches the HelmRelease: "inline" sets
	// spec.values directly (default, today's behaviour), "configMap"
	// externalizes Values into a referenced ConfigMap via valuesFrom.
	// Resolved by ToApplicationConfig (component property → handler default
	// → "inline"); always one of those two values once set.
	ValuesMode string

	// Form A: inline source — URL is set, launcher creates the source CR.
	SourceURL  string
	SourceKind string // "HelmRepository" or "OCIRepository"

	// Form B: reference — name is set, source CR already exists.
	SourceRefName      string
	SourceRefKind      string // "HelmRepository", "OCIRepository", or "HelmChart"
	SourceRefNamespace string

	// HelmRelease options
	Interval        string
	ReleaseName     string
	TargetNamespace string
	DriftMode       string
	InstallCRDs     string
	UpgradeCRDs     string
	Values          map[string]any
	ValuesFrom      []helmv2.ValuesReference

	// dedup state (Form A only)
	suppressSource bool
	sharedSrcName  string

	// renderChart is the function used to render Helm charts in template delivery mode.
	// Defaults to helm.RenderChart; injectable for testing (see renderChartFunc).
	renderChart renderChartFunc

	// chartRender caches the delivery: template render, split into Helm hook
	// groups — the same implementation the helmtemplate terminal runs
	// (helmtemplate_render.go). Populated by ensureRendered on first call;
	// delivery: native never renders.
	chartRender

	// fluxNS overrides the namespace for emitted Flux control-plane CRs
	// (HelmRelease, HelmRepository, OCIRepository). Set by postProcessFluxNamespace
	// via TransformContext.FluxNamespace. Empty means use c.Namespace.
	fluxNS string
}

// ApplyPolicy is a no-op for helmchart (Helm releases have no resource-limit policy).
func (c *HelmchartConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// GetSourceKey returns the dedup key for Form A sources.
// For HelmRepository: "helm:<url>". For OCIRepository: "oci:<url>:<version>".
// Returns "" for Form B (reference) and for template delivery (no source CR emitted)
// so the dedup loop skips this config.
// First component wins when multiple components share the same source key.
func (c *HelmchartConfig) GetSourceKey() string {
	if c.SourceURL == "" || c.Delivery == "template" {
		return ""
	}
	if c.SourceKind == "OCIRepository" {
		return "oci:" + c.SourceURL + ":" + c.Version
	}
	return "helm:" + c.SourceURL
}

// GetSourceRefName returns the name to use when referencing this component's source CR.
func (c *HelmchartConfig) GetSourceRefName() string { return c.Name }

// SuppressSourceGeneration instructs this config to skip emitting its own source CR
// and reference the named shared source instead.
func (c *HelmchartConfig) SuppressSourceGeneration(refName string) {
	c.suppressSource = true
	c.sharedSrcName = refName
}

// fluxNamespace returns the namespace for Flux control-plane CRs.
func (c *HelmchartConfig) fluxNamespace() string {
	if c.fluxNS != "" {
		return c.fluxNS
	}
	return c.Namespace
}

// SetFluxNamespace re-stamps the Flux control-plane namespace for HelmRelease,
// HelmRepository, and OCIRepository. Satisfies pkg/oam.fluxNamespaceSettable.
func (c *HelmchartConfig) SetFluxNamespace(ns string) {
	c.fluxNS = ns
}

// EmitsAutoHealthCheck reports whether this component emits a HelmRelease that
// the auto health-check can reference. Template delivery renders manifests
// client-side and emits no HelmRelease, so no HelmRelease health check should
// be synthesized. Satisfies pkg/oam.autoHealthCheckEmitter.
func (c *HelmchartConfig) EmitsAutoHealthCheck() bool {
	return c.Delivery != "template"
}

// Generate produces the Kubernetes objects for this helmchart component.
// For delivery: template, renders the chart client-side and returns raw manifests.
// For delivery: native (default), emits a source CR (Form A only) and a HelmRelease.
func (c *HelmchartConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Re-check at the emission boundary what ToApplicationConfig already checked
	// at parse time. This type and its Values field are exported, so a config
	// built directly by a library consumer — never parsed — reaches
	// buildHelmRelease below, where kure's fluxcd.SetHelmReleaseValuesFromMap
	// *panics* on a value encoding/json refuses (a non-finite float) instead of
	// returning an error. Generate has an error to return and is the one entry
	// point every emission path goes through, so the crash becomes a diagnosable
	// failure here. Same shape as passthrough's validateEmittableObject, which is
	// likewise called from both the parse path and Generate for exactly this
	// reason.
	if len(c.Values) > 0 {
		if _, err := json.Marshal(c.Values); err != nil {
			return nil, errors.Errorf("helmchart %q: values is not representable as JSON: %w", c.Name, err)
		}
	}
	if c.Delivery == "template" {
		if err := c.ensureRendered(); err != nil {
			return nil, err
		}
		// The hook groups flattened in execution order — the union AugmentLayout's
		// template branch (augmentLayoutTemplate) later repartitions; see
		// chartRender.objects.
		return c.objects(), nil
	}

	var objects []*client.Object
	interval := parseDuration(effectiveInterval(c.Interval))

	if c.SourceURL != "" {
		// Form A: inline source
		srcName := c.Name
		if c.suppressSource && c.sharedSrcName != "" {
			srcName = c.sharedSrcName
		}

		if !c.suppressSource {
			switch c.SourceKind {
			case "HelmRepository":
				repo := fluxcd.CreateHelmRepository(c.Name, c.fluxNamespace())
				repo.Spec.URL = c.SourceURL
				repo.Spec.Interval = interval
				obj := client.Object(repo)
				objects = append(objects, &obj)
			case "OCIRepository":
				repo := fluxcd.CreateOCIRepository(c.Name, c.fluxNamespace())
				repo.Spec.URL = c.SourceURL
				repo.Spec.Interval = interval
				if c.Version != "" {
					fluxcd.SetOCIRepositoryReference(repo, &sourcev1.OCIRepositoryRef{Tag: c.Version})
				}
				obj := client.Object(repo)
				objects = append(objects, &obj)
			}
		}

		hr := c.buildHelmRelease()
		switch c.SourceKind {
		case "HelmRepository":
			fluxcd.SetHelmReleaseChart(hr, &helmv2.HelmChartTemplate{
				Spec: helmv2.HelmChartTemplateSpec{
					Chart:   c.Chart,
					Version: c.Version,
					SourceRef: helmv2.CrossNamespaceObjectReference{
						Kind: "HelmRepository",
						Name: srcName,
					},
				},
			})
		case "OCIRepository":
			fluxcd.SetHelmReleaseChartRef(hr, &helmv2.CrossNamespaceSourceReference{
				Kind: "OCIRepository",
				Name: srcName,
			})
		}
		obj := client.Object(hr)
		objects = append(objects, &obj)
	} else {
		// Form B: reference existing source CR
		hr := c.buildHelmRelease()
		switch c.SourceRefKind {
		case "HelmRepository":
			fluxcd.SetHelmReleaseChart(hr, &helmv2.HelmChartTemplate{
				Spec: helmv2.HelmChartTemplateSpec{
					Chart:   c.Chart,
					Version: c.Version,
					SourceRef: helmv2.CrossNamespaceObjectReference{
						Kind:      "HelmRepository",
						Name:      c.SourceRefName,
						Namespace: c.SourceRefNamespace,
					},
				},
			})
		default: // "OCIRepository" or "HelmChart"
			fluxcd.SetHelmReleaseChartRef(hr, &helmv2.CrossNamespaceSourceReference{
				Kind:      c.SourceRefKind,
				Name:      c.SourceRefName,
				Namespace: c.SourceRefNamespace,
			})
		}
		obj := client.Object(hr)
		objects = append(objects, &obj)
	}

	return objects, nil
}

// ensureRendered renders the chart client-side for delivery: template, once,
// through the chartRender this config shares with the helmtemplate terminal —
// see chartRender.render for the caching and for the release-identity
// limitation ToApplicationConfig's delivery: template validation above
// accounts for.
func (c *HelmchartConfig) ensureRendered() error {
	return c.render(c.renderChart, "helmchart", c.Name, chartSource{
		URL:     c.SourceURL,
		Kind:    c.SourceKind,
		Chart:   c.Chart,
		Version: c.Version,
		Values:  c.Values,
	})
}

// buildHelmRelease creates a HelmRelease with the shared options applied.
func (c *HelmchartConfig) buildHelmRelease() *helmv2.HelmRelease {
	interval := parseDuration(effectiveInterval(c.Interval))
	hr := fluxcd.CreateHelmRelease(c.Name, c.fluxNamespace())
	hr.Spec.Interval = interval

	if c.ReleaseName != "" {
		hr.Spec.ReleaseName = c.ReleaseName
	}
	if c.TargetNamespace != "" {
		hr.Spec.TargetNamespace = c.TargetNamespace
	}
	if c.DriftMode != "" {
		fluxcd.SetHelmReleaseDriftDetection(hr, fluxcd.CreateDriftDetection(helmv2.DriftDetectionMode(c.DriftMode)))
	}
	if c.InstallCRDs != "" {
		fluxcd.SetHelmReleaseInstallCRDs(hr, helmv2.CRDsPolicy(c.InstallCRDs))
	}
	if c.UpgradeCRDs != "" {
		fluxcd.SetHelmReleaseUpgradeCRDs(hr, helmv2.CRDsPolicy(c.UpgradeCRDs))
	}
	// Confirmed merge order (helm-controller's internal/controller/helmrelease_controller.go
	// calls chartutil.ChartValuesFromReferences(ctx, ..., obj.GetValues(), obj.Spec.ValuesFrom...),
	// github.com/fluxcd/pkg/chartutil: valuesFrom entries are merged in list
	// order into a working set, then spec.values is merged on top last
	// (chartutil.MergeMaps(result, values) — the second argument's scalars
	// win on conflict). So: under "inline" mode, spec.values is set and wins
	// over any user valuesFrom entry (c.ValuesFrom below) on overlapping
	// keys. Under "configMap" mode, spec.values stays empty and the
	// generated ref is itself a valuesFrom entry added here, before the
	// user's own c.ValuesFrom loop — so user entries, appearing later in
	// spec.valuesFrom, win over the generated ref on overlapping keys.
	if c.emitsValuesConfigMap() {
		fluxcd.AddHelmReleaseValuesFrom(hr, helmv2.ValuesReference{
			Kind:      "ConfigMap",
			Name:      valuesConfigMapName(c.Name),
			ValuesKey: "values.yaml",
		})
	} else if len(c.Values) > 0 { // "inline" (or "configMap" with nothing to externalize)
		fluxcd.SetHelmReleaseValuesFromMap(hr, c.Values)
	}
	for _, vf := range c.ValuesFrom {
		fluxcd.AddHelmReleaseValuesFrom(hr, vf)
	}
	return hr
}

func effectiveInterval(interval string) string {
	if interval == "" {
		return "60m"
	}
	return interval
}

func parseDuration(s string) metav1.Duration {
	d, _ := time.ParseDuration(s)
	return metav1.Duration{Duration: d}
}

// validateFluxInterval refuses an authored interval the Flux CRDs this
// component emits would reject at apply time. The interval reaches them through
// parseDuration, as a metav1.Duration that serializes as Duration.String(), so
// the emitted form is checked as well as the authored one.
func validateFluxInterval(component, interval string) error {
	err := fluxduration.ValidateEmitted(interval)
	if err == nil {
		return nil
	}
	var re *fluxduration.ResolutionError
	if errors.As(err, &re) {
		return errors.Errorf("%s: interval %q is invalid: it would be emitted as %q, below Flux's millisecond resolution (use 0s or at least 1ms)", component, interval, re.Emitted)
	}
	return errors.Errorf("%s: interval %q is invalid: must be a Flux duration (unsigned; units ms, s, m, h; e.g. 10m, 1h30m)", component, interval)
}

// augmentingHelmchartConfig wraps *HelmchartConfig to add AugmentLayout
// without *HelmchartConfig itself satisfying layout.LayoutAugmenter. See
// wrapIfHelmchartAugmenter for why the method must live on a separate,
// conditionally-applied type rather than directly on *HelmchartConfig.
// Embedding a pointer promotes the full *HelmchartConfig method set, so
// every existing consumer (all of which go through interfaces, never a
// concrete *HelmchartConfig type assertion) keeps working unchanged.
type augmentingHelmchartConfig struct {
	*HelmchartConfig
}

// AugmentLayout attaches the resources this config's Generate output alone
// cannot express: native delivery under valuesMode: configMap emits the
// values.yaml ConfigMap that buildHelmRelease's generated valuesFrom entry
// references (emitsValuesConfigMap branch); delivery: template repartitions
// the rendered chart into hook-ordered child layouts (augmentLayoutTemplate).
// wrapIfHelmchartAugmenter only wraps a config for which one of these two
// branches actually does something, so exactly one of them ever fires for a
// given instance.
func (c *augmentingHelmchartConfig) AugmentLayout(ml *layout.ManifestLayout) error {
	if c.Delivery == "template" {
		return c.augmentLayoutTemplate(ml)
	}
	if !c.emitsValuesConfigMap() {
		return nil
	}
	b, err := yaml.Marshal(c.Values)
	if err != nil {
		return errors.Wrapf(err, "helmchart %q: marshaling values for configMap valuesMode", c.Name)
	}
	// A literal, statically-named ConfigMap — not ml.ExtraFiles or
	// ml.ConfigMapGenerators (kustomize's configMapGenerator hash-suffixes
	// the emitted name, and kustomize's builtin name-reference table has
	// no HelmRelease entry, so that suffix is never rewritten into
	// HelmRelease.spec.valuesFrom[].name). A literal resource has no
	// suffix to go stale, so the ValuesReference set up in
	// buildHelmRelease always resolves.
	//
	// Namespace is mandatory, not cosmetic: Flux's ValuesReference has no
	// namespace field of its own and resolves only within the referring
	// HelmRelease's own namespace, so an unset namespace here would
	// silently break resolution whenever SetFluxNamespace is used.
	//
	// Built via kubernetes.CreateConfigMap (this repo's established
	// constructor, pkg/oam/builtin/traits/configmap.go) rather than a bare
	// &corev1.ConfigMap{} literal: the literal form leaves TypeMeta zero-
	// valued, and json.Marshal's `omitempty` on TypeMeta's fields then
	// drops apiVersion/kind from the serialized manifest entirely — both
	// kubectl apply and kustomize build reject the result, and the
	// on-disk filename derivation (which reads the GVK's Kind) breaks
	// too. Since go-kure/kure's builder-contract-release-1 (beta.11),
	// CreateConfigMap no longer stamps labels/annotations itself, so they
	// are set here explicitly — matching every other ConfigMap this
	// codebase emits (traits/configmap.go). The emitted metadata is not
	// identical to beta.10's: the label is the component's label value
	// (appLabels: the component name, projected when it exceeds 63
	// characters, go-kure/launcher#572) rather than this ConfigMap's own
	// name, and there is no `app` annotation. Both deltas are inventoried in
	// the package README.
	cm := kubernetes.CreateConfigMap(valuesConfigMapName(c.Name), c.fluxNamespace())
	cm.Labels = appLabels(c.Name)
	cm.Annotations = nil
	kubernetes.AddConfigMapData(cm, "values.yaml", string(b))
	ml.Resources = append(ml.Resources, cm)
	return nil
}

// emitsValuesConfigMap reports whether this config's AugmentLayout emits the
// values ConfigMap that buildHelmRelease's generated valuesFrom entry
// references: valuesMode: configMap with at least one value to externalize.
// Extracted so wrapIfHelmchartAugmenter, buildHelmRelease, and AugmentLayout
// share one predicate instead of three copies that could drift. Deliberately
// checks the resource-adding condition itself, not Delivery: Delivery ==
// "template" and !emitsValuesConfigMap() are equivalent today (see
// ToApplicationConfig's template-specific validation above — an explicit
// valuesMode: configMap under delivery: template is a hard error, and an
// inherited handler default is silently rewritten to inline), but only the
// predicate form stays fail-closed if that rejection ever loosens.
func (c *HelmchartConfig) emitsValuesConfigMap() bool {
	return c.ValuesMode == "configMap" && len(c.Values) > 0
}

// GenerateCoversAugmentLayout implements oam.LayoutAugmentationCoverage.
// AugmentLayout adds a resource Generate's own output does not already
// contain only when emitsValuesConfigMap is true (the values ConfigMap);
// every other case this config is ever wrapped for — delivery: template,
// whose AugmentLayout (augmentLayoutTemplate) only repartitions Generate's
// own flat union into hook-ordered children, preserving the flat-output
// invariant that Generate's returned union already contains everything
// AugmentLayout would otherwise add — is a safe skip for a consumer that
// never constructs or walks a
// layout.ManifestLayout (e.g. pkg/cmd/kurel's build guard).
func (c *augmentingHelmchartConfig) GenerateCoversAugmentLayout() bool {
	return !c.emitsValuesConfigMap()
}

var _ oam.LayoutAugmentationCoverage = (*augmentingHelmchartConfig)(nil)

// augmentLayoutTemplate handles the AugmentLayout path for delivery:
// template: it renders the chart (once) and repartitions the render into one
// child layout per Helm hook group — chartRender.partition, the same code the
// helmtemplate terminal's AugmentLayout runs, whose doc comment carries the
// child naming, placement and file-mode rules and the residual
// cross-Application collision gap.
func (c *HelmchartConfig) augmentLayoutTemplate(ml *layout.ManifestLayout) error {
	if err := c.ensureRendered(); err != nil {
		return err
	}
	c.partition(ml)
	return nil
}

// valuesConfigMapName returns the name for the values ConfigMap referenced by
// both buildHelmRelease's ValuesReference and AugmentLayout's literal
// resource — the same helper for both call sites so they cannot diverge.
func valuesConfigMapName(name string) string {
	return boundedResourceName(name, "-values")
}

// boundedResourceName appends suffix to name and keeps the result a legal
// DNS-1123 subdomain name (at most 253 bytes) for any valid component name
// (validate.go admits DNS-1123 subdomains of up to 253 bytes). A name that
// fits is name+suffix; one that does not keeps a truncated prefix of name, a
// short digest of the full name, and suffix intact. valuesConfigMapName (this
// composite) and helmReleaseValuesConfigMapName (the helmrelease terminal)
// share it so both follow one scheme; suffix must itself be DNS-1123-legal,
// start with "-" and end in an alphanumeric, and be short enough to leave room
// for the digest.
func boundedResourceName(name, suffix string) string {
	maxPrefix := 253 - len(suffix)
	if len(name) <= maxPrefix {
		return name + suffix
	}
	// A plain truncation to maxPrefix characters would map any two distinct
	// valid component names (up to 253 chars — validate.go's DNS-1123
	// subdomain max) that share the same first maxPrefix characters to the
	// identical ConfigMap name. Full-name uniqueness (validate.go's
	// duplicate-component-name check) does not protect against this — it
	// compares full names, not truncated prefixes — so two such components
	// in one Application would silently share (and one clobber) the other's
	// values ConfigMap. Reserve room for a short content hash of the full
	// name so a truncated name stays unique to the name it came from.
	const hashLen = 8
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := maxPrefix - hashLen - 1 // -1 for the "-" joining prefix and hash
	prefix := strings.TrimRight(name[:prefixLen], "-.")
	return prefix + "-" + hash + suffix
}
