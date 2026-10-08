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
		scopeOverridesKey:                   scopeOverridesSchema("Explicit scope entries for kinds the chart renders, taking precedence over kure's own guess (not over a kind the Kubernetes API itself scopes; contradicting a CRD the chart renders is an error). A rendered object of a kind stated Namespaced that carries no namespace gets the application namespace; one of a kind stated Cluster is left as rendered."),
		oam.HookGroupNamePrefixProperty:     {Type: oam.PropertyTypeString, Description: hookGroupNamePrefixDescription},
		oam.LayoutKustomizationNameProperty: {Type: oam.PropertyTypeString, Description: layoutKustomizationNameDescription},
	}
}

// hookGroupNamePrefixDescription describes hookGroupNamePrefix for the
// helmtemplate component, and for a helm component under delivery: template.
const hookGroupNamePrefixDescription = "Prefix of the names of the component's hook-group layouts, in place of <application>-<component>: each layout, its directory and the Flux Kustomization generated for it under per-layout placement, is named <prefix>-<NN>-<phase>. A DNS-1123 subdomain, used as written and never shortened: a layout name over 63 characters built from it is refused. It must differ from the prefix of every other component of the document."

// layoutKustomizationNameDescription describes layoutKustomizationName for the
// helmtemplate component, and for a helm component under delivery: template.
const layoutKustomizationNameDescription = "Name of the Flux Kustomization generated under per-layout placement for the component's own layout, in place of <bundle>-<component> (over 63 characters, shortened by launcher's own rule with the base library launcher pins, -<component> kept whole up to 52 characters; go-kure/launcher#941 hands it to the rule the base library applies to its own default from go-kure/kure#1030 on, which gives another name, at the next re-pin). It names neither the layout nor its directory, and is not read under per-bundle placement. A DNS-1123 subdomain of at most 63 characters, used as written and never shortened. It must differ from that of every other component of the document."

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
	// The authored prefix of the hook-group layout names; nil when absent or
	// null. A present empty string is an authored prefix, and refused.
	HookGroupNamePrefix *string `json:"hookGroupNamePrefix"`
	// The authored name of the Flux Kustomization of the component's own
	// layout; nil when absent or null. A present empty string is an authored
	// name, and refused.
	LayoutKustomizationName *string `json:"layoutKustomizationName"`
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
	// As authored: the transform checks it when it resolves the prefix
	// (oam.HookGroupNamePrefixSetter), and every name built from it once the
	// policy step has rendered the chart (CheckHookGroupNames). AugmentLayout
	// checks them again.
	if props.HookGroupNamePrefix != nil {
		cfg.HookGroupNamePrefix, cfg.prefixAuthored = *props.HookGroupNamePrefix, true
	}
	// As authored: the transform checks it where it resolves the name
	// (oam.LayoutKustomizationNameSetter).
	if props.LayoutKustomizationName != nil {
		cfg.LayoutKustomizationName, cfg.layoutNameAuthored = *props.LayoutKustomizationName, true
	}
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
	// The transform sets it (SetApplicationName); it leads the default name of
	// every hook-group child layout (hookGroupChildName). Empty on a config built
	// directly, whose default child names then begin with the layout's own name.
	Application string
	// HookGroupNamePrefix is the prefix of the hook-group child layout names in
	// place of the default "<Application>-<layout name>": the author's
	// hookGroupNamePrefix, the answer of the consumer's Naming hook for role
	// "hook-group" (SetHookGroupNamePrefix), or what the builder of a direct
	// config set. Each child is then named "<prefix>-<NN>-<phase>", directory and
	// Flux Kustomization alike. It is never shortened: AugmentLayout refuses a
	// child name built from it that is no DNS-1123 subdomain of at most 63
	// characters, and the transform refuses it before that
	// (CheckHookGroupNames). Empty means the default, whose Kustomization name
	// is shortened to 63 characters.
	HookGroupNamePrefix string
	// LayoutKustomizationName is the name of the Flux Kustomization the base
	// library generates under per-layout placement for the component's own
	// layout, which AugmentLayout sets on that layout
	// (ManifestLayout.KustomizationName) unless the layout already carries one:
	// the author's layoutKustomizationName, the answer of the consumer's Naming
	// hook for role "layout", the default the transform shortened to 63
	// characters (SetLayoutKustomizationName), or what the builder of a direct
	// config set. Empty leaves the base library's default,
	// "<unit>-<layout name>". It names neither the layout nor its directory.
	LayoutKustomizationName string
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
	// (refuseNestedGlobal). The two trees are compared and merged with their Go
	// types, as the render takes them: an object is a map[string]any, and a
	// map of another type at a key both set is a shared path
	// (refuseSharedValuePath). A policy that forbids explicit secrets refuses a
	// config that sets it (ApplyPolicy).
	SecretValues map[string]any
	// ScopeOverrides states the scope of a kind the chart renders, by
	// apiVersion and kind: manifest.ScopeNamespaced or manifest.ScopeCluster,
	// any other value is refused. The namespace stamp reads it
	// (stampRenderedNamespaces): it outranks kure's own table, not a kind the
	// Kubernetes API scopes, and must agree with a CustomResourceDefinition the
	// chart renders for the kind.
	ScopeOverrides map[schema.GroupVersionKind]manifest.ScopeResult

	// prefixAuthored says the author wrote hookGroupNamePrefix, also where what
	// the author wrote is the empty string, which the transform refuses.
	prefixAuthored bool
	// layoutNameAuthored says the author wrote layoutKustomizationName, also
	// where what the author wrote is the empty string, which the transform
	// refuses.
	layoutNameAuthored bool

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

// AuthoredHookGroupNamePrefix implements oam.HookGroupNamePrefixSetter: the
// prefix the author wrote in hookGroupNamePrefix. A config built directly with
// HookGroupNamePrefix set answers as if its builder were the author, so a
// transform it is handed to checks and claims that prefix and asks no hook.
func (c *HelmTemplateConfig) AuthoredHookGroupNamePrefix() (string, bool) {
	return c.HookGroupNamePrefix, c.prefixAuthored || c.HookGroupNamePrefix != ""
}

// SetHookGroupNamePrefix implements oam.HookGroupNamePrefixSetter: the
// transform hands over the prefix it resolved when that is not the default.
func (c *HelmTemplateConfig) SetHookGroupNamePrefix(prefix string) { c.HookGroupNamePrefix = prefix }

// AuthoredLayoutKustomizationName implements oam.LayoutKustomizationNameSetter:
// the name the author wrote in layoutKustomizationName. A config built
// directly with LayoutKustomizationName set answers as if its builder were the
// author, so a transform it is handed to checks and claims that name and asks
// no hook.
func (c *HelmTemplateConfig) AuthoredLayoutKustomizationName() (string, bool) {
	return c.LayoutKustomizationName, c.layoutNameAuthored || c.LayoutKustomizationName != ""
}

// SetLayoutKustomizationName implements oam.LayoutKustomizationNameSetter:
// the transform hands over the name it resolved when that is not the base
// library's own default.
func (c *HelmTemplateConfig) SetLayoutKustomizationName(name string) {
	c.LayoutKustomizationName = name
}

// AugmentLayout repartitions the render Generate returned flat into one child
// layout per Helm hook group, chained in execution order
// (chartRender.partition) and named after c.HookGroupNamePrefix, or by default
// after c.Application and ml. A chart with at most one hook group is not
// partitioned. A child name built from c.HookGroupNamePrefix that cannot be a
// Flux Kustomization's is refused, and ml is then left as it was: one over 63
// characters with an *oam.HookGroupNameError. A transform returns that refusal
// before any layout is built (CheckHookGroupNames), so this one is met by a
// config built directly, or one whose prefix was set after its transform.
//
// Whatever the number of hook groups, c.LayoutKustomizationName, when set,
// becomes ml's KustomizationName, unless ml already carries one.
func (c *HelmTemplateConfig) AugmentLayout(ml *layout.ManifestLayout) error {
	if err := c.ensureRendered(); err != nil {
		return err
	}
	if err := c.partition(c.hookGroupNaming(), ml); err != nil {
		return err
	}
	if c.LayoutKustomizationName != "" && ml.KustomizationName == "" {
		ml.KustomizationName = c.LayoutKustomizationName
	}
	return nil
}

// hookGroupNaming is what c names its hook-group child layouts by.
func (c *HelmTemplateConfig) hookGroupNaming() hookGroupNaming {
	return hookGroupNaming{component: c.Name, application: c.Application, prefix: c.HookGroupNamePrefix}
}

// CheckHookGroupNames implements oam.HookGroupNameChecker: the refusal
// AugmentLayout would return for a child name built from
// c.HookGroupNamePrefix, once the chart is rendered. It renders nothing, and
// returns nil while nothing is rendered. The transform calls it after
// ApplyPolicy, which has rendered the chart: the transform's policy is never
// nil. A caller that applied a nil policy itself, which renders nothing, gets
// nil here and the refusal from AugmentLayout.
func (c *HelmTemplateConfig) CheckHookGroupNames() error {
	if !c.rendered {
		return nil
	}
	return c.hookGroupNaming().check(c.childSuffixes())
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
	_ oam.HookGroupNamePrefixSetter  = (*HelmTemplateConfig)(nil)
	_ oam.HookGroupNameChecker       = (*HelmTemplateConfig)(nil)

	_ oam.LayoutKustomizationNameSetter = (*HelmTemplateConfig)(nil)
)
