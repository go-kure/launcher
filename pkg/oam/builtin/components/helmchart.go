package components

import (
	"bytes"
	"io"
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
// only when AugmentLayout would actually do something: today that's
// valuesMode: configMap with at least one value to externalize. kure's layout
// walker type-asserts layout.LayoutAugmenter by PRESENCE
// (pkg/stack/layout/walker.go) to decide whether an app gets its own
// flat-bundle sub-layout or merges into its parent's — a structural decision,
// not a side effect with a safe no-op default. So *HelmchartConfig itself
// must stay free of the AugmentLayout method (a hook-free, inline-values
// config must not satisfy the interface), and the wrapper is applied
// conditionally here rather than unconditionally. See also
// traits.wrapIfAugmenter, which forwards this presence through trait
// decorators generically.
//
// delivery: template does NOT gate this yet: AugmentLayout's Helm
// hook-group repartitioning for template delivery is PR2's work, not
// implemented in this PR. Wrapping a template-delivery config now would make
// it satisfy LayoutAugmenter with an effectively no-op AugmentLayout, which
// still triggers the walker's presence-based flat-bundle relocation for zero
// benefit. A future PR should add `|| cfg.Delivery == "template"` back to the
// condition below in the same change that implements the template branch in
// AugmentLayout — not before.
func wrapIfHelmchartAugmenter(cfg *HelmchartConfig) stack.ApplicationConfig {
	if cfg.ValuesMode == "configMap" && len(cfg.Values) > 0 {
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
	// helm.RenderChart itself satisfies this field without a wrapper; generateTemplate
	// does not pass any opts yet (see its doc comment).
	renderChart func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error)

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
		return c.generateTemplate()
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

// generateTemplate renders the chart client-side via helm.RenderChart and returns the
// resulting Kubernetes manifests as individual objects.
//
// Known limitation: this call passes no release-identity opts, so kure renders with
// its defaults, .Release.Name = "release" and .Release.Namespace = "default" (kure
// pkg/stack/helm/render.go). ToApplicationConfig already rejects releaseName/
// targetNamespace outright for delivery: template (see the validation above), so this
// path is never reached with either set — the rejection, not a silent drop, is today's
// behavior for a chart that needs .Release.Name/.Release.Namespace. kure now exposes
// helm.WithReleaseName/helm.WithNamespace (kure v0.2.0-beta.10+); wiring them through and
// relaxing that validation is a follow-up, not attempted here.
func (c *HelmchartConfig) generateTemplate() ([]*client.Object, error) {
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
		return nil, errors.Wrapf(err, "helmchart %q: rendering chart", c.Name)
	}
	return decodeKubeManifests(raw)
}

// decodeKubeManifests decodes multi-doc YAML from RenderChart into Kubernetes objects.
// Real YAML parse errors are returned immediately.
// Non-map and empty documents are skipped defensively (kure filters NOTES.txt upstream).
// Mapping documents without apiVersion/kind are an error (broken chart manifest).
func decodeKubeManifests(raw []byte) ([]*client.Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var objects []*client.Object
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
		u := &unstructured.Unstructured{Object: doc}
		obj := client.Object(u)
		objects = append(objects, &obj)
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
	if len(c.Values) > 0 {
		switch c.ValuesMode {
		case "configMap":
			fluxcd.AddHelmReleaseValuesFrom(hr, helmv2.ValuesReference{
				Kind:      "ConfigMap",
				Name:      valuesConfigMapName(c.Name),
				ValuesKey: "values.yaml",
			})
		default: // "inline"
			// error ignored: only fails on JSON marshal failure, which can't happen with map[string]any
			_ = fluxcd.SetHelmReleaseValuesFromMap(hr, c.Values)
		}
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
// cannot express. Today: native delivery under valuesMode: configMap emits
// the values.yaml ConfigMap that buildHelmRelease's generated valuesFrom
// entry references. (Template delivery's hook-group repartitioning is a
// follow-up PR; wrapIfHelmchartAugmenter does not yet gate on delivery:
// template — see that function's doc comment — so this method is not called
// for template delivery until that PR both implements the branch here and
// restores the gate.)
func (c *augmentingHelmchartConfig) AugmentLayout(ml *layout.ManifestLayout) error {
	if c.ValuesMode == "configMap" && len(c.Values) > 0 {
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
	}
	return nil
}

// valuesConfigMapName returns the name for the values ConfigMap referenced by
// both buildHelmRelease's ValuesReference and AugmentLayout's literal
// resource — the same helper for both call sites so they cannot diverge.
func valuesConfigMapName(name string) string {
	const suffix = "-values"
	maxPrefix := 253 - len(suffix) // 246
	if len(name) > maxPrefix {
		name = name[:maxPrefix]
		name = strings.TrimRight(name, "-.")
	}
	return name + suffix
}
