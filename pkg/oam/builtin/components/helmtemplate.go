package components

import (
	"encoding/json"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
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
// values and secretValues, so authored-property validation and the handler's
// strict decode admit the same set: source (url, kind), chart, version, values
// and secretValues. A test ties this schema to the struct.
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
		"chart":   {Type: oam.PropertyTypeString, Description: "Chart name within a HelmRepository source, where it is required. Not used for an OCIRepository source, whose URL already names the chart."},
		"version": {Type: oam.PropertyTypeString, Description: "Chart version to render. Required for an OCIRepository source."},
		"values":  {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Helm values tree passed to the client-side render. Must be representable as JSON."},
		helmSecretValuesKey: {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Sensitive part of the Helm values tree, merged over values for the client-side render and written nowhere else by this component; whatever the chart renders from it is in the output in clear form. A path set in both values and secretValues is refused. Must be representable as JSON. An environment policy may forbid it.",
		},
	}
}

// helmTemplateProperties is the property surface the strict decode checks,
// values and secretValues excepted. Any key it does not declare, at any depth, is refused — in
// particular a release identity (releaseName, targetNamespace; this terminal
// renders into the application namespace under kure's default release name),
// every property only a Flux-reconciled release reads (interval,
// driftDetection, install, upgrade, valuesFrom, valuesMode), the helm rule's
// delivery switch, and a source reference (source.name, source.namespace).
type helmTemplateProperties struct {
	Source  *helmTemplateSource `json:"source"`
	Chart   string              `json:"chart"`
	Version string              `json:"version"`
}

// helmTemplateSource is the inline chart source: a URL, and optionally the
// kind it must agree with.
type helmTemplateSource struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// ToApplicationConfig decodes the component's properties strictly, with values
// and secretValues split off first, and checks the inline source: source.url
// required, the kind inferred from or checked against the URL scheme, chart
// required for a HelmRepository, version required for an OCIRepository, values
// and secretValues each an object that encodes as JSON, with no path set in
// both.
func (h *HelmTemplateHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	props, owned, err := builtin.DecodeStrictJSON[helmTemplateProperties](component.Properties, helmTemplateValuesKey, helmSecretValuesKey)
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
		Values:       values,
		SecretValues: secretValues,
		renderChart:  helm.RenderChart,
	}
	src, err := cfg.source()
	if err != nil {
		return nil, err
	}
	// Record the resolved kind.
	cfg.SourceKind = src.Kind
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
	// Namespace is the application namespace, the render's .Release.Namespace;
	// empty leaves kure's default, "default". The release name is always
	// kure's default, "release": this terminal declares no releaseName.
	Namespace string

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
	// every error this config returns. It must be representable as JSON and
	// share no path with Values. A policy that forbids explicit secrets refuses
	// a config that sets it (ApplyPolicy).
	SecretValues map[string]any

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
		// Neither message carries a value: the encoding error is not wrapped.
		if _, err := json.Marshal(c.SecretValues); err != nil {
			return chartSource{}, errors.Errorf("%s: %s is not representable as JSON", helmTemplateType, helmSecretValuesKey)
		}
		if err := refuseSharedValuePath(helmTemplateType, c.Values, c.SecretValues); err != nil {
			return chartSource{}, err
		}
	}
	return chartSource{URL: c.SourceURL, Kind: kind, Chart: c.Chart, Version: c.Version, Values: c.Values, SecretValues: c.SecretValues, Namespace: c.Namespace}, nil
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
