package components

import (
	"encoding/json"
	"strings"

	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// helmTemplateType is the helmtemplate component type. It prefixes the errors
// the handler and config raise themselves — property decoding and validation,
// and a failed chart render — but not a failure to parse the rendered output,
// which the render code returns unprefixed (chartRender.render).
const helmTemplateType = "helmtemplate"

// helmTemplateValuesKey is the Helm values tree, which the strict decode splits
// off and which reaches the render exactly as authored (see
// helmTemplateObject). helmSecretValuesKey, the sensitive part of the tree, is
// split off the same way, so its content never reaches a decode error.
const helmTemplateValuesKey = "values"

// HelmTemplateHandler handles the kind-named `helmtemplate` component: a Helm
// chart rendered client-side at build time into raw manifests, returned in Helm
// hook order and, for a layout-walking consumer, partitioned into one child
// layout per hook group. It is directly authorable and is also what the helm
// rule lowers to under `delivery: template`; the render and partition code is
// in helmtemplate_render.go. It emits no source CR and no HelmRelease.
type HelmTemplateHandler struct{}

// CanHandle returns true for the helmtemplate component type.
func (h *HelmTemplateHandler) CanHandle(componentType string) bool {
	return componentType == helmTemplateType
}

// PropertySchema declares exactly the keys helmTemplateProperties decodes plus
// values, secretValues and scopeOverrides, so authored-property validation and
// the handler's strict decode admit the same set: source (url, kind), chart,
// version, releaseName, values, secretValues and scopeOverrides. A test ties
// this schema to the struct.
func (h *HelmTemplateHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "Where the chart is fetched from at build time. Only an inline URL: this component neither creates nor references a source CR.",
			Properties: map[string]oam.PropertySchema{
				"url":  {Type: oam.PropertyTypeString, Required: true, Description: "Chart location: an http:// or https:// Helm repository URL, or an oci:// URL that names the chart itself."},
				"kind": {Type: oam.PropertyTypeString, Enum: []any{"HelmRepository", "OCIRepository"}, Description: "Source kind. Inferred from the URL scheme when unset (oci:// is OCIRepository, anything else HelmRepository); when set, it must agree with the scheme."},
			},
		},
		"chart":       {Type: oam.PropertyTypeString, Description: "Chart name within a HelmRepository source, where it is required. Not used for an OCIRepository source, whose URL already names the chart."},
		"version":     {Type: oam.PropertyTypeString, Description: "Chart version to render. Required for an OCIRepository source."},
		"releaseName": {Type: oam.PropertyTypeString, Description: "The render's .Release.Name: a DNS-1123 subdomain of at most 53 characters, as a Helm release name is. Defaults to the release name Flux gives a HelmRelease named after the component: the component name, a name over 53 characters shortened as Flux shortens it (its first 40 characters, '-', and 12 hex digits of its SHA-256)."},
		"values":      {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Helm values tree passed to the client-side render. Must be representable as JSON."},
		helmSecretValuesKey: {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Sensitive part of the Helm values tree, merged over values for the client-side render and written nowhere else by this component; whatever the chart renders from it is in the output in clear form. A path set in both values and secretValues is refused, and so is a key named global below the top level. Must be representable as JSON. An environment policy may forbid it.",
		},
		scopeOverridesKey: scopeOverridesSchema("Explicit scope entries for kinds the chart renders, taking precedence over kure's own guess (not over a kind the Kubernetes API itself scopes; contradicting a CRD the chart renders is an error). A rendered object of a kind stated Namespaced that carries no namespace gets the application namespace; one of a kind stated Cluster is left as rendered."),
	}
}

// helmTemplateProperties is the property surface the strict decode checks,
// values, secretValues and scopeOverrides excepted, which are split off before
// it (see ToApplicationConfig). Any key it does not declare, at any depth, is refused — in
// particular targetNamespace (this terminal renders into the application
// namespace), every property only a Flux-reconciled release reads (interval,
// driftDetection, install, upgrade, valuesFrom, valuesMode), the helm rule's
// delivery switch, and a source reference (source.name, source.namespace).
type helmTemplateProperties struct {
	Source      *helmTemplateSource `json:"source"`
	Chart       string              `json:"chart"`
	Version     string              `json:"version"`
	ReleaseName string              `json:"releaseName"`
}

// helmTemplateSource is the inline chart source: a URL, and optionally the
// kind it must agree with.
type helmTemplateSource struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// ToApplicationConfig decodes the component's properties strictly, with
// scopeOverrides, values and secretValues split off first, and checks the
// inline source: source.url required, the kind inferred from or checked against
// the URL scheme, chart required for a HelmRepository, version required for an
// OCIRepository, values and secretValues each an object that encodes as JSON,
// with no path set in both and no key named global below the top level of
// secretValues, and the release name, authored or defaulted from
// the component name as Flux defaults a HelmRelease's, a valid Helm release
// name (templateReleaseName). scopeOverrides is read as the manifests component
// reads its own (parseScopeOverrides), with the same refusals of a malformed
// entry.
func (h *HelmTemplateHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	overrides, rest, err := parseScopeOverrides(component.Properties)
	if err != nil {
		return nil, errors.Errorf("%s: %w", helmTemplateType, err)
	}
	props, owned, err := builtin.DecodeStrictJSON[helmTemplateProperties](rest, helmTemplateValuesKey, helmSecretValuesKey)
	if err != nil {
		return nil, errors.Errorf("%s: properties do not decode: %w", helmTemplateType, err)
	}
	values, err := helmTemplateObject(owned, helmTemplateValuesKey)
	if err != nil {
		return nil, err
	}
	secretValues, err := helmTemplateObject(owned, helmSecretValuesKey)
	if err != nil {
		return nil, err
	}
	if props.Source == nil {
		return nil, errors.Errorf("%s: source is required", helmTemplateType)
	}
	cfg := &HelmTemplateConfig{
		Name:         component.Name,
		Namespace:    namespace,
		SourceURL:    props.Source.URL,
		SourceKind:   props.Source.Kind,
		Chart:        props.Chart,
		Version:      props.Version,
		ReleaseName:  props.ReleaseName,
		Values:       values,
		SecretValues: secretValues,
		renderChart:  helm.RenderChart,

		ScopeOverrides: overrides,
	}
	src, err := cfg.source()
	if err != nil {
		return nil, err
	}
	// Record the resolved kind and release name.
	cfg.SourceKind = src.Kind
	cfg.ReleaseName = src.ReleaseName
	return cfg, nil
}

// helmTemplateObject reads key, values or secretValues, from the keys split off
// before the strict decode. Absent, or null (typed or not), means none; anything
// else must be an object, and a refusal names the value's type, never the
// value. The map is kept exactly as authored, with the YAML-decoded value
// types, rather than taken from the strict decoder, whose json.Number numbers a chart template would compare and
// print differently. The owned split matches keys case-insensitively, so two
// spellings of the key are refused rather than one silently winning.
func helmTemplateObject(owned map[string]any, key string) (map[string]any, error) {
	var found []any
	for k, v := range owned {
		if strings.EqualFold(k, key) {
			found = append(found, v)
		}
	}
	switch {
	case len(found) == 0:
		return nil, nil
	case len(found) > 1:
		return nil, errors.Errorf("%s: %s is given more than once", helmTemplateType, key)
	case oam.IsNullValue(found[0]):
		return nil, nil
	}
	m, ok := found[0].(map[string]any)
	if !ok {
		return nil, errors.Errorf("%s: %s: must be an object, got %T", helmTemplateType, key, found[0])
	}
	return m, nil
}

// HelmTemplateConfig implements stack.ApplicationConfig for helmtemplate
// components. Generate renders the chart and returns every manifest flat, in
// Helm hook execution order; AugmentLayout repartitions the same render into
// one child layout per hook group.
type HelmTemplateConfig struct {
	// Name is the component name.
	Name string
	// Application is the name of the OAM application the component belongs to.
	// The transform sets it (SetApplicationName); it leads the name of every
	// hook-group child layout (hookGroupChildName). Empty on a config built
	// directly, whose child names then begin with the layout's own name.
	Application string
	// Namespace is the application namespace: the render's .Release.Namespace,
	// and the namespace given to a namespaced rendered object that carries none
	// (stampRenderedNamespaces). Empty leaves .Release.Namespace at kure's
	// default, "default", and stamps nothing.
	Namespace string
	// ReleaseName is the render's .Release.Name, a valid Helm release name.
	// ToApplicationConfig records the name it resolved; empty on a config built
	// directly means the default derived from Name, as Flux derives a
	// HelmRelease's release name (templateReleaseName). With Name empty too,
	// the config is refused.
	ReleaseName string

	// SourceURL is where the chart is fetched from: an http(s):// Helm
	// repository URL, or an oci:// URL naming the chart.
	SourceURL string
	// SourceKind is "HelmRepository" or "OCIRepository". ToApplicationConfig
	// records the kind it resolved; empty on a config built directly means
	// inferred from SourceURL's scheme.
	SourceKind string
	// Chart is the chart name within a HelmRepository source; unused for an
	// OCIRepository source.
	Chart string
	// Version is the chart version; required for an OCIRepository source.
	Version string
	// Values is the Helm values tree handed to the render as-is. It must be
	// representable as JSON.
	Values map[string]any
	// SecretValues is the sensitive part of the values tree
	// (go-kure/launcher#786), merged over Values for the render and kept out of
	// every error this config returns. It must be representable as JSON, share
	// no path with Values and carry no key named global below its top level
	// (refuseNestedGlobal). A policy that forbids explicit secrets refuses a
	// config that sets it (ApplyPolicy).
	SecretValues map[string]any
	// ScopeOverrides states the scope of a kind the chart renders, by
	// apiVersion and kind: manifest.ScopeNamespaced or manifest.ScopeCluster,
	// any other value is refused. The namespace stamp reads it
	// (stampRenderedNamespaces): it outranks kure's own table, not a kind the
	// Kubernetes API scopes, and must agree with a CustomResourceDefinition the
	// chart renders for the kind.
	ScopeOverrides map[schema.GroupVersionKind]manifest.ScopeResult

	// renderChart renders the chart. ToApplicationConfig sets helm.RenderChart,
	// which a nil value also means; tests inject a stub.
	renderChart renderChartFunc

	// chartRender caches the render and its hook groups, shared by Generate
	// and AugmentLayout.
	chartRender
}

// source checks c and returns what the render fetches. ToApplicationConfig
// runs it on the parsed document; ensureRendered runs it again because this
// type and its fields are exported, so a config built directly never went
// through the handler.
func (c *HelmTemplateConfig) source() (chartSource, error) {
	if c.SourceURL == "" {
		return chartSource{}, errors.Errorf("%s: source.url is required", helmTemplateType)
	}
	kind, err := inlineChartSourceKind(helmTemplateType, c.SourceURL, c.SourceKind, c.Chart)
	if err != nil {
		return chartSource{}, err
	}
	if kind == "OCIRepository" && c.Version == "" {
		return chartSource{}, errors.Errorf("%s: an OCIRepository source requires version to be set", helmTemplateType)
	}
	if _, err := json.Marshal(c.Values); err != nil {
		return chartSource{}, errors.Errorf("%s: values is not representable as JSON: %w", helmTemplateType, err)
	}
	if len(c.SecretValues) > 0 {
		// No message carries a value: the encoding error is not wrapped.
		if err := refuseNestedGlobal(helmTemplateType, c.SecretValues); err != nil {
			return chartSource{}, err
		}
		if err := refuseSharedValuePath(helmTemplateType, c.Values, c.SecretValues); err != nil {
			return chartSource{}, err
		}
	}
	releaseName, err := templateReleaseName(helmTemplateType, c.ReleaseName, c.Name)
	if err != nil {
		return chartSource{}, err
	}
	for gvk, scope := range c.ScopeOverrides {
		if scope != manifest.ScopeNamespaced && scope != manifest.ScopeCluster {
			return chartSource{}, errors.Errorf("%s: the scope override for %s %s is neither Namespaced nor Cluster", helmTemplateType, gvk.GroupVersion().String(), gvk.Kind)
		}
	}
	return chartSource{URL: c.SourceURL, Kind: kind, Chart: c.Chart, Version: c.Version, Values: c.Values, SecretValues: c.SecretValues, Namespace: c.Namespace, ReleaseName: releaseName, ScopeOverrides: c.ScopeOverrides}, nil
}

// ensureRendered checks c and renders its chart, once: Generate and
// AugmentLayout share the render (chartRender.render).
func (c *HelmTemplateConfig) ensureRendered() error {
	src, err := c.source()
	if err != nil {
		return err
	}
	return c.render(c.renderChart, helmTemplateType, c.Name, src)
}

// Generate renders the chart and returns every manifest it emits flat, in
// Helm hook execution order (chartRender.objects). Objects in a phase kure
// has no GitOps equivalent for (pre-delete, post-delete, pre-rollback,
// post-rollback, test) are dropped.
func (c *HelmTemplateConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.ensureRendered(); err != nil {
		return nil, err
	}
	return c.objects(), nil
}

// SetApplicationName implements oam.ApplicationNameSetter: the transform hands
// over the name of the application the component belongs to, which the
// hook-group child layout names then begin with.
func (c *HelmTemplateConfig) SetApplicationName(name string) { c.Application = name }

// AugmentLayout repartitions the render Generate returned flat into one child
// layout per Helm hook group, chained in execution order
// (chartRender.partition) and named after c.Application and ml. A chart with at
// most one hook group leaves ml unchanged.
func (c *HelmTemplateConfig) AugmentLayout(ml *layout.ManifestLayout) error {
	if err := c.ensureRendered(); err != nil {
		return err
	}
	c.partition(c.Application, ml)
	return nil
}

// GenerateCoversAugmentLayout implements oam.LayoutAugmentationCoverage and is
// always true: AugmentLayout only repartitions Generate's own flat union into
// hook-ordered children and adds no resource, so a consumer that never walks a
// layout.ManifestLayout (pkg/cmd/kurel's build guard) loses nothing by
// skipping it.
func (c *HelmTemplateConfig) GenerateCoversAugmentLayout() bool { return true }

var (
	_ layout.LayoutAugmenter         = (*HelmTemplateConfig)(nil)
	_ oam.LayoutAugmentationCoverage = (*HelmTemplateConfig)(nil)
	_ oam.ApplicationNameSetter      = (*HelmTemplateConfig)(nil)
)
