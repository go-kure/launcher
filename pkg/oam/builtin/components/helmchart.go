package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
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
		"interval":        {Type: oam.PropertyTypeString, Description: "Reconciliation interval as a Go duration (default 60m)."},
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
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return nil, errors.Errorf("helmchart: interval %q is invalid: must be a valid Go duration (e.g. 10m, 1h30m)", cfg.Interval)
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
		cfg.Values = vals
	}
	if vfList, ok := props["valuesFrom"].([]any); ok {
		for i, vf := range vfList {
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
			vfc.Name, _ = m["name"].(string)
			if vfc.Name == "" {
				return nil, errors.Errorf("valuesFrom[%d]: name is required", i)
			}
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
		// Form A: inline source — launcher creates the source CR
		cfg.SourceURL = srcURL

		if srcKind == "" {
			if strings.HasPrefix(srcURL, "oci://") {
				cfg.SourceKind = "OCIRepository"
			} else {
				cfg.SourceKind = "HelmRepository"
			}
		} else {
			cfg.SourceKind = srcKind
		}

		switch cfg.SourceKind {
		case "HelmRepository":
			if strings.HasPrefix(srcURL, "oci://") {
				return nil, errors.Errorf("helmchart: source.kind HelmRepository is incompatible with oci:// URL")
			}
			if !strings.HasPrefix(srcURL, "https://") && !strings.HasPrefix(srcURL, "http://") {
				return nil, errors.Errorf("helmchart: HelmRepository source.url must start with https:// or http://")
			}
			if cfg.Chart == "" {
				return nil, errors.New("helmchart: source.kind HelmRepository requires chart to be specified")
			}
		case "OCIRepository":
			if !strings.HasPrefix(srcURL, "oci://") {
				return nil, errors.Errorf("helmchart: source.kind OCIRepository requires an oci:// URL")
			}
		default:
			return nil, errors.Errorf("helmchart: source.kind %q is not valid for inline source; must be HelmRepository or OCIRepository", cfg.SourceKind)
		}
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
	// Defaults to helm.RenderChart; injectable for testing. Variadic opts matches
	// kure's RenderChart signature (kure v0.2.0-beta.10+, helm.RenderOption) so that
	// helm.RenderChart itself satisfies this field without a wrapper; ensureRendered
	// does not pass any opts yet (see its doc comment).
	renderChart func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error)

	// hookGroups caches the rendered chart's manifests, split by Helm hook phase
	// and weight (via helm.SplitByHookWeight), for delivery: template. Populated
	// by ensureRendered on first call; nil until then (and always nil for
	// delivery: native, which never calls ensureRendered). Not goroutine-safe —
	// concurrent Generate/AugmentLayout calls on the same *HelmchartConfig race
	// on rendered/hookGroups (an identical cache in a downstream consumer's
	// analogous handler isn't goroutine-safe either).
	hookGroups []helm.HookGroup
	// rendered reports whether ensureRendered has already populated hookGroups,
	// so that Generate followed by AugmentLayout (kure's layout walker's usual
	// call order) renders the chart over the network exactly once.
	rendered bool

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
	if c.Delivery == "template" {
		if err := c.ensureRendered(); err != nil {
			return nil, err
		}
		// Flatten hookGroups in execution order. This is the union AugmentLayout's
		// template branch (augmentLayoutTemplate) later repartitions into child
		// layouts for a layout-walking consumer — Generate itself always returns
		// the flat set, which is what keeps kurel build (which never walks a
		// layout.ManifestLayout) and every validator unaffected, and is the
		// premise GenerateCoversAugmentLayout's guard opt-out rests on.
		var objects []*client.Object
		for _, g := range c.hookGroups {
			for _, obj := range g.Resources {
				o := obj
				objects = append(objects, &o)
			}
		}
		return objects, nil
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
				fluxcd.SetHelmRepositoryURL(repo, c.SourceURL)
				fluxcd.SetHelmRepositoryInterval(repo, interval)
				obj := client.Object(repo)
				objects = append(objects, &obj)
			case "OCIRepository":
				repo := fluxcd.CreateOCIRepository(c.Name, c.fluxNamespace())
				fluxcd.SetOCIRepositoryURL(repo, c.SourceURL)
				fluxcd.SetOCIRepositoryInterval(repo, interval)
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

// ensureRendered renders the chart client-side via renderChart on first call
// (for delivery: template only), parses the result into Helm-hook-partitioned
// groups via parseChartManifests, and caches them in hookGroups. Subsequent
// calls are no-ops — rendered guards re-render — so a config used through
// both Generate (which flattens hookGroups into its returned union) and
// AugmentLayout (augmentLayoutTemplate, which repartitions the same groups
// into child layouts) — kure's layout walker's usual call order — renders
// the chart over the network exactly once.
//
// Known limitation: this call passes no release-identity opts, so kure renders with
// its defaults, .Release.Name = "release" and .Release.Namespace = "default" (kure
// pkg/stack/helm/render.go). ToApplicationConfig already rejects releaseName/
// targetNamespace outright for delivery: template (see the validation above), so this
// path is never reached with either set — the rejection, not a silent drop, is today's
// behavior for a chart that needs .Release.Name/.Release.Namespace. kure now exposes
// helm.WithReleaseName/helm.WithNamespace (kure v0.2.0-beta.10+); wiring them through and
// relaxing that validation is a follow-up, not attempted here.
func (c *HelmchartConfig) ensureRendered() error {
	if c.rendered {
		return nil
	}
	renderFn := c.renderChart
	if renderFn == nil {
		renderFn = helm.RenderChart
	}
	chartURL := strings.TrimRight(c.SourceURL, "/") + "/" + c.Chart
	if c.SourceKind == "OCIRepository" {
		chartURL = c.SourceURL // OCI URL already embeds the chart path
	}
	raw, err := renderFn(chartURL, c.Version, c.Values)
	if err != nil {
		return errors.Wrapf(err, "helmchart %q: rendering chart", c.Name)
	}
	groups, err := parseChartManifests(raw)
	if err != nil {
		return err
	}
	c.hookGroups = groups
	c.rendered = true
	return nil
}

// parseChartManifests decodes multi-doc YAML produced by renderChart and
// splits it into Helm hook-phase-and-weight groups via kure's
// helm.SplitByHookWeight.
//
// kure's SplitByHookWeight documents (pkg/stack/helm/hooks.go:35-36) that a
// comma-separated helm.sh/hook annotation (e.g. "pre-install,pre-upgrade") is
// treated as one opaque phase string and sorted into the alphabetical
// "unknown" bucket, which its own phaseOrder (hooks.go:60-75) places *after*
// post-upgrade — inverting the ordering guarantee for exactly the kind of
// resource that annotation exists to order. Likewise a multi-value annotation
// whose tokens are all members of kure's excludedHookPhases (hooks.go:20-26,
// exact-string matched) is never excluded either, for the same reason. Both
// are corrected here, before objects reach SplitByHookWeight, via a grouping-
// only object copy (normalizeHookAnnotationForGrouping) — the original
// objects, with their original unmodified annotations, are what land in
// HookGroup.Resources and therefore in emitted output.
func parseChartManifests(raw []byte) ([]helm.HookGroup, error) {
	objs, err := decodeKubeManifests(raw)
	if err != nil {
		return nil, err
	}

	groupingObjs := make([]client.Object, len(objs))
	origByGroupingObj := make(map[client.Object]client.Object, len(objs))
	for i, obj := range objs {
		normalized := normalizeHookAnnotationForGrouping(obj)
		groupingObjs[i] = normalized
		if normalized != obj {
			origByGroupingObj[normalized] = obj
		}
	}

	groups := helm.SplitByHookWeight(groupingObjs)
	for gi := range groups {
		for ri, r := range groups[gi].Resources {
			if orig, ok := origByGroupingObj[r]; ok {
				groups[gi].Resources[ri] = orig
			}
		}
	}
	return groups, nil
}

// hookPhaseOrder mirrors kure's own phaseOrder priority for the four ordered
// Helm lifecycle phases (kure pkg/stack/helm/hooks.go:60-75) that participate
// in FluxCD Kustomization ordering. "" (main/non-hook) is deliberately
// excluded — a comma-separated annotation is by definition non-empty.
var hookPhaseOrder = map[string]int{
	"pre-install":  0,
	"pre-upgrade":  1,
	"post-install": 2,
	"post-upgrade": 3,
}

// excludedHookPhases mirrors kure's own unexported excludedHookPhases set
// (kure pkg/stack/helm/hooks.go:20-26) — phases with no FluxCD GitOps
// lifecycle equivalent, which SplitByHookWeight drops from its output. Kept
// as a local copy since kure's map is unexported and this package must not
// edit the pinned dependency.
var excludedHookPhases = map[string]bool{
	"pre-delete":    true,
	"post-delete":   true,
	"pre-rollback":  true,
	"post-rollback": true,
	"test":          true,
}

// normalizeHookAnnotationForGrouping returns a client.Object suitable for
// handing to kure's helm.SplitByHookWeight for grouping-key determination.
// Single-value and empty helm.sh/hook annotations are already correct under
// kure's own logic and are returned unchanged (same pointer as obj — callers
// use pointer identity to detect whether a copy was made).
//
// For a comma-separated annotation, every excluded-phase token
// (excludedHookPhases) is first dropped from consideration — an excluded
// token must never influence the grouping decision for an object that is
// otherwise emitted, exactly as it never would if it were the object's only
// hook value. What remains after dropping excluded tokens is then
// classified:
//   - nothing remains (every token was excluded) -> rewritten to a single
//     excluded literal ("test") so kure's own exclusion logic (hooks.go:49)
//     drops the object, instead of it falling into the mis-sorted unknown
//     bucket.
//   - at least one surviving token is a recognized ordered phase
//     (hookPhaseOrder) -> rewritten to the earliest such token by kure's own
//     priority; any unrecognized tokens mixed in are dropped too (they
//     contribute no valid ordering).
//   - every surviving token is an unrecognized custom hook name, and at
//     least one excluded token was dropped to reach that state -> rewritten
//     to just the surviving tokens (comma-joined, original relative order)
//     so the dropped excluded token cannot affect the unknown-bucket
//     grouping key or hookGroupDir's slug.
//   - every original token was already an unrecognized custom hook name (no
//     excluded token was ever present) -> left unchanged verbatim. There is
//     no defined ordering priority among custom names, and kure's existing
//     unknown-bucket fallback is not wrong for this case — rewriting would
//     only reorder or reformat it for no behavioral reason.
func normalizeHookAnnotationForGrouping(obj client.Object) client.Object {
	ann := obj.GetAnnotations()
	hook := ann["helm.sh/hook"]
	if hook == "" || !strings.Contains(hook, ",") {
		return obj
	}

	anyExcluded := false
	var remaining []string
	bestPhase := ""
	bestOrder := -1
	for _, tok := range strings.Split(hook, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if excludedHookPhases[tok] {
			anyExcluded = true
			continue
		}
		remaining = append(remaining, tok)
		if order, ok := hookPhaseOrder[tok]; ok && (bestOrder == -1 || order < bestOrder) {
			bestOrder = order
			bestPhase = tok
		}
	}

	switch {
	case len(remaining) == 0:
		return cloneWithHookAnnotation(obj, ann, "test")
	case bestOrder != -1:
		return cloneWithHookAnnotation(obj, ann, bestPhase)
	case anyExcluded:
		return cloneWithHookAnnotation(obj, ann, strings.Join(remaining, ","))
	default:
		return obj
	}
}

// cloneWithHookAnnotation returns a copy of obj with its helm.sh/hook
// annotation rewritten to newHook, leaving obj itself — and its original
// annotations map — untouched. The copy exists only to steer kure's
// helm.SplitByHookWeight to the correct group; parseChartManifests swaps it
// back out for the original object before returning, so the rewritten
// annotation never reaches emitted output.
func cloneWithHookAnnotation(obj client.Object, ann map[string]string, newHook string) client.Object {
	cp, _ := obj.DeepCopyObject().(client.Object)
	newAnn := make(map[string]string, len(ann))
	maps.Copy(newAnn, ann)
	newAnn["helm.sh/hook"] = newHook
	cp.SetAnnotations(newAnn)
	return cp
}

// hookGroupDir returns a DNS-1123-safe directory-name segment for a
// HookGroup: Phase == "" (main / non-hook resources) maps to "main"; any
// other phase is slugified — lowercased, runs of characters outside
// [a-z0-9-] collapsed to a single "-", leading/trailing "-" trimmed,
// truncated to 40 characters and re-trimmed (the cut can expose a new
// trailing "-"), "unknown" if the result is empty.
//
// Deviates from a downstream consumer's analogous helper, which returns the
// phase verbatim: kure's
// SplitByHookWeight puts a comma-separated or otherwise malformed
// helm.sh/hook annotation into one opaque phase string (kure hooks.go) that
// becomes both a path segment and a literal Kustomization object name via
// kure's createKustomizationForLayout, which validates neither
// (pkg/stack/fluxcd/resource_generator.go). Unsanitized, a phase like
// "pre-install,post-install" breaks path safety and DNS-1123 validity, and
// Helm imposes no length limit on the annotation. Fixed here rather than
// inherited — see augmentLayoutTemplate's doc comment for the write-time
// hazard this avoids.
func hookGroupDir(g helm.HookGroup) string {
	if g.Phase == "" {
		return "main"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(g.Phase) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	const maxSlugLen = 40
	if len(slug) > maxSlugLen {
		slug = strings.TrimRight(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		return "unknown"
	}
	return slug
}

// decodeKubeManifests decodes multi-doc YAML from RenderChart into Kubernetes objects.
// Real YAML parse errors are returned immediately.
// Non-map and empty documents are skipped defensively (kure filters NOTES.txt upstream).
// Mapping documents without apiVersion/kind are an error (broken chart manifest).
func decodeKubeManifests(raw []byte) ([]client.Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var objects []client.Object
	for {
		var rawDoc any
		if err := dec.Decode(&rawDoc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, errors.Wrapf(err, "decoding rendered manifest")
		}
		doc, ok := rawDoc.(map[string]any)
		if !ok || len(doc) == 0 {
			continue // defensive: skip non-map or empty documents
		}
		if doc["apiVersion"] == nil || doc["kind"] == nil {
			return nil, errors.Errorf("rendered document is missing apiVersion or kind: %v", doc)
		}
		objects = append(objects, &unstructured.Unstructured{Object: doc})
	}
	return objects, nil
}

// buildHelmRelease creates a HelmRelease with the shared options applied.
func (c *HelmchartConfig) buildHelmRelease() *helmv2.HelmRelease {
	interval := parseDuration(effectiveInterval(c.Interval))
	hr := fluxcd.CreateHelmRelease(c.Name, c.fluxNamespace())
	fluxcd.SetHelmReleaseInterval(hr, interval)

	if c.ReleaseName != "" {
		fluxcd.SetHelmReleaseReleaseName(hr, c.ReleaseName)
	}
	if c.TargetNamespace != "" {
		fluxcd.SetHelmReleaseTargetNamespace(hr, c.TargetNamespace)
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
		// error ignored: only fails on JSON marshal failure, which can't happen with map[string]any
		_ = fluxcd.SetHelmReleaseValuesFromMap(hr, c.Values)
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
	// too. CreateConfigMap stamps TypeMeta plus common labels/annotations
	// consistently with every other ConfigMap this codebase emits.
	cm := kubernetes.CreateConfigMap(valuesConfigMapName(c.Name), c.fluxNamespace())
	kubernetes.AddConfigMapDataMap(cm, map[string]string{"values.yaml": string(b)})
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
// own flat union into hook-ordered children, step 5's "keep the flat union"
// — is a safe skip for a consumer that never constructs or walks a
// layout.ManifestLayout (e.g. pkg/cmd/kurel's build guard).
func (c *augmentingHelmchartConfig) GenerateCoversAugmentLayout() bool {
	return !c.emitsValuesConfigMap()
}

var _ oam.LayoutAugmentationCoverage = (*augmentingHelmchartConfig)(nil)

// augmentLayoutTemplate handles the AugmentLayout path for delivery:
// template. With at most one hook group, ml.Resources already carries the
// flat union Generate returned and no children are needed. With multiple
// groups, that union is partitioned: ml.Resources is cleared and rebuilt
// solely from c.hookGroups (the same cached render Generate flattened), so
// this relies on ml.Resources containing exactly that render's objects when
// AugmentLayout runs — true today because every trait decorator in this repo
// (traits/decorator.go's decoratorBase-embedding types) only mutates the
// objects its inner Generate returns in place and never appends a new one; a
// trait's own additional resources (e.g. a ConfigMap or Secret) are emitted
// as a separate stack.Application and never merged into this Application's
// ml.Resources (see traits/pruneprotection.go's "narrow scope" doc comment
// for the same convention stated explicitly). A future decorator that broke
// this convention would have its addition silently dropped here. Each group
// becomes a child ManifestLayout written to a numbered sub-directory in
// execution order, chained via DependsOn so kure's FluxCD integrator (in
// FluxIntegratedPerLayout placement) waits for each hook group to reconcile
// healthy before the next.
//
// Children inherit the parent's Mode/FluxPlacement/FileNaming/FilePer — but
// deliberately NOT ApplicationFileMode, left AppFileUnset on every child
// regardless of the parent's own value. kure's walker sets only three of the
// five layout-rule fields on the layout it hands the augmenter; a downstream
// consumer's identical augmenter copies all five verbatim, carrying the same
// latent dangling-reference risk this deviation avoids (fixing that other
// copy is out of this repo's scope). kure's parent-side kustomization writer decides how
// to reference a child from the child's own literal ApplicationFileMode
// field alone, never resolved through a Config fallback: AppFileSingle makes
// it emit a bare "<child.Name>.yaml" sibling-file reference, correct only
// when the child writes its single file into the SAME directory as the
// parent's own kustomization.yaml — true for kure's ordinary same-directory
// children, false here, where the child's own recursive WriteToDisk call
// places that file one directory deeper
// (".../<parent>/<dirName>/<dirName>.yaml"), leaving the parent's
// kustomization.yaml pointing at a file that was never written — a missing
// resources: entry, breaking kubectl kustomize/Flux at that layout. Leaving
// the child's field AppFileUnset instead sends the parent down the
// directory-reference branch for any placement other than
// FluxIntegratedPerLayout (which instead references a Flux Kustomization
// YAML filename); either way, the child's own recursive write — whatever
// ApplicationFileMode it resolves to via its own Config fallback — then
// writes its own self-consistent kustomization.yaml one level down, which
// the parent's reference correctly reaches. The child's FullRepoPath()
// returns the composed namespace unchanged — kure's suffix-dedup
// (namespace already ending in the child's own name) fires by design here,
// the mechanism not a hazard.
//
// Residual gap, documented not fixed: two DIFFERENT Applications with a
// same-named component still collide (component names are unique only
// within one Application, but every emitted Kustomization CR shares one
// controller namespace) — a downstream consumer's identical augmenter
// admits the same gap; inherited here, newly exposed by this repo's own
// template-delivery support. Out of scope; see the components README.
func (c *HelmchartConfig) augmentLayoutTemplate(ml *layout.ManifestLayout) error {
	if err := c.ensureRendered(); err != nil {
		return err
	}
	if len(c.hookGroups) <= 1 {
		return nil
	}
	ml.Resources = nil
	parentPath := ml.FullRepoPath()
	var prevName string
	for i, g := range c.hookGroups {
		dirName := hookGroupChildName(ml.Name, i, g)
		child := &layout.ManifestLayout{
			Name:          dirName,
			Namespace:     parentPath + "/" + dirName,
			Resources:     append([]client.Object(nil), g.Resources...),
			Mode:          ml.Mode,
			FluxPlacement: ml.FluxPlacement,
			FileNaming:    ml.FileNaming,
			FilePer:       ml.FilePer,
			// ApplicationFileMode intentionally omitted (left AppFileUnset) — see the doc comment above.
		}
		if i > 0 {
			child.DependsOn = []string{prevName}
		}
		ml.Children = append(ml.Children, child)
		prevName = dirName
	}
	return nil
}

// hookGroupChildName computes augmentLayoutTemplate's dirName for hook group
// i: "<ml.Name>-<%02d>-<hookGroupDir(g)>". ml.Name is a validated DNS-1123
// subdomain up to 253 characters (pkg/oam/validate.go,
// k8s.io/apimachinery/pkg/util/validation.DNS1123SubdomainMaxLength), so the
// composed name can exceed 253 even with hookGroupDir's own 40-character slug
// cap — additional to that slug-only cap. Truncating the composed string from
// the right is wrong: near a 253-char ml.Name, the fixed numeric+phase suffix
// would be cut away entirely and every group would yield the identical
// dirName — a deterministic collision. So the PREFIX (ml.Name) is capped
// instead, following PR1's valuesConfigMapName precedent: reserve room for a
// short sha256 hash of the full ml.Name so two different long ml.Names are
// vanishingly unlikely to truncate to the same prefix (the same probabilistic
// guarantee as that precedent, not an absolute one).
func hookGroupChildName(mlName string, i int, g helm.HookGroup) string {
	suffix := fmt.Sprintf("-%02d-%s", i, hookGroupDir(g)) // %02d is a minimum width, not a cap
	const maxLen = 253
	if len(mlName)+len(suffix) <= maxLen {
		return mlName + suffix
	}
	maxPrefix := maxLen - len(suffix)
	const hashLen = 8
	sum := sha256.Sum256([]byte(mlName))
	hash := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := max(maxPrefix-hashLen-1, 0) // -1 for the "-" joining prefix and hash
	prefix := strings.TrimRight(mlName[:prefixLen], "-.")
	return prefix + "-" + hash + suffix
}

// valuesConfigMapName returns the name for the values ConfigMap referenced by
// both buildHelmRelease's ValuesReference and AugmentLayout's literal
// resource — the same helper for both call sites so they cannot diverge.
func valuesConfigMapName(name string) string {
	const suffix = "-values"
	maxPrefix := 253 - len(suffix) // 246
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
