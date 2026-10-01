package components

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// helmType is the helm component type. It prefixes every error the rule raises
// itself.
const helmType = "helm"

// helmSourceDigestLen is how many hex digits of the source identity's SHA-256
// a generated source's name carries.
const helmSourceDigestLen = 10

// helmPassthroughKeys are the helm properties forwarded verbatim to the
// helmrelease terminal under delivery: flux. They are split off before the
// strict decode, so their shape is the terminal's to check, and none of them
// reaches a generated source. values is forwarded under delivery: template
// too; every other one is refused there (helmFluxOnlyKeys).
var helmPassthroughKeys = []string{"values", "interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

// helmFluxOnlyKeys are the passthrough keys only a HelmRelease reads. delivery:
// template refuses each one, in this order, naming the key.
var helmFluxOnlyKeys = []string{"interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

// HelmRule lowers a "helm" component (D1 component position,
// oam.ComponentLoweringRule) to the kind-named Flux terminals, the role-named
// layer of the helmchart composite's split (go-kure/launcher#336):
//
//   - delivery: flux (the default) emits a `helmrelease` component carrying the
//     authored name, traits and annotations. With an inline source.url it also
//     emits the source the release reads: a `helmrepository` for an http(s)://
//     URL, an `ocirepository` for an oci:// URL (ref.tag = version).
//   - delivery: template emits a `helmtemplate` component carrying the authored
//     name, traits and annotations, with the URL inline. No source is emitted:
//     the chart is rendered at build time.
//
// A generated source is named <document>-source-<digest>, the digest taken over
// the source's content identity (helmSourceIdentity), and claimed through
// NameAllocator.NameOrAdopt: helm components of one document that share a URL
// (and, for OCI, a version) share one source, and the second one only
// references it. Two documents never share or collide on a generated source,
// because the document name is part of its name. The source uses its terminal's
// own interval default, never the release interval, so the identity says
// everything about its content.
//
// With source.name the release references an existing source of kind
// HelmRepository (chart.spec.sourceRef), OCIRepository or HelmChart (chartRef),
// optionally in source.namespace, and nothing else is emitted.
//
// The properties decode strictly: a key the schema does not declare, at any
// depth of source, is refused. delivery: template refuses every key only a
// HelmRelease reads (helmFluxOnlyKeys), a source reference, valuesMode:
// configMap, and an OCI source without a version, each with a helm: message
// naming what the author wrote rather than the terminal it would reach.
// valuesMode is forwarded only when authored; the rule has no registration-time
// default.
type HelmRule struct{}

// ComponentType claims the "helm" component type at the component lowering
// position. build.go registers this rule via RegisterComponentLowering; no
// dispatchable handler exists for "helm".
func (HelmRule) ComponentType() string { return helmType }

// PropertySchema declares the helm component's properties: exactly the keys
// helmProperties decodes plus helmPassthroughKeys. A test ties the two.
func (HelmRule) PropertySchema() map[string]oam.PropertySchema {
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	object := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	return map[string]oam.PropertySchema{
		"chart":    str("Chart name within a HelmRepository source, where it is required. Refused with an OCIRepository or HelmChart source, which already name the chart."),
		"version":  str("Chart version. For an inline oci:// source it is the OCIRepository's ref.tag, and required under delivery: template. Refused with a referenced OCIRepository or HelmChart source, which already pin it."),
		"delivery": {Type: oam.PropertyTypeString, Default: "flux", Enum: []any{"flux", "template"}, Description: "flux emits a HelmRelease (plus its source for an inline URL); template renders the chart client-side at build time."},
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "Chart source: an inline url (the source is generated, and shared by helm components of the document with the same URL and, for OCI, version), or a reference (name, kind, namespace) to an existing source CR.",
			Properties: map[string]oam.PropertySchema{
				"url":       str("Inline chart location: an http:// or https:// Helm repository URL, or an oci:// URL naming the chart. Mutually exclusive with name."),
				"kind":      {Type: oam.PropertyTypeString, Enum: []any{"HelmRepository", "OCIRepository", "HelmChart"}, Description: "Source kind. With url it is inferred from the scheme when unset and must agree with it (HelmRepository or OCIRepository); with name it is required."},
				"name":      str("Name of an existing source CR to reference. Mutually exclusive with url; not supported under delivery: template."),
				"namespace": str("Namespace of the referenced source CR. Only with name."),
			},
		},
		"values":          object("Helm values tree. Must be representable as JSON."),
		"valuesMode":      {Type: oam.PropertyTypeString, Enum: []any{"inline", "configMap"}, Description: "How values reach the HelmRelease: inline keeps them in spec.values; configMap moves them into a generated ConfigMap. Unset means inline. configMap is refused under delivery: template."},
		"interval":        str("HelmRelease spec.interval as a Flux duration (default 60m). The generated source keeps its own default. Refused under delivery: template."),
		"releaseName":     str("HelmRelease spec.releaseName. Refused under delivery: template."),
		"targetNamespace": str("HelmRelease spec.targetNamespace. Refused under delivery: template."),
		"driftDetection":  object("HelmRelease spec.driftDetection. Refused under delivery: template."),
		"install":         object("HelmRelease spec.install: Helm install options. Refused under delivery: template."),
		"upgrade":         object("HelmRelease spec.upgrade: Helm upgrade options. Refused under delivery: template."),
		"valuesFrom": {Type: oam.PropertyTypeArray, Description: "HelmRelease spec.valuesFrom: ConfigMaps or Secrets supplying values. Refused under delivery: template.", Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "One values reference (kind, name, valuesKey, targetPath, optional).",
		}},
	}
}

// helmProperties is what the strict decode checks; helmPassthroughKeys are
// split off before it.
type helmProperties struct {
	Chart      string      `json:"chart"`
	Version    string      `json:"version"`
	Delivery   string      `json:"delivery"`
	ValuesMode string      `json:"valuesMode"`
	Source     *helmSource `json:"source"`
}

// helmSource is an inline URL (with an optional kind) or a reference to an
// existing source CR (name, kind, optional namespace).
type helmSource struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// LowerComponent decodes comp as a helm component and emits its terminal
// components (see HelmRule).
func (HelmRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	props, passthrough, err := decodeHelm(comp.Properties)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	switch props.Delivery {
	case "", "flux":
		return lowerHelmFlux(comp, lctx, props, passthrough)
	case "template":
		return lowerHelmTemplate(comp, props, passthrough)
	default:
		return oam.LoweringResult{}, errors.Errorf("%s: unsupported delivery %q; supported values: flux, template", helmType, props.Delivery)
	}
}

// decodeHelm decodes props strictly and checks what holds for every delivery:
// a source with exactly one of url and name, and a known valuesMode. The
// passthrough keys come back in their own map under their declared spelling,
// without nulls (absent). The strict decode matches keys case-insensitively, as
// encoding/json does, so two spellings of one key are refused rather than one
// silently winning.
func decodeHelm(src map[string]any) (*helmProperties, map[string]any, error) {
	props, owned, err := builtin.DecodeStrictJSON[helmProperties](src, helmPassthroughKeys...)
	if err != nil {
		return nil, nil, errors.Errorf("%s: properties do not decode: %w", helmType, err)
	}
	passthrough := make(map[string]any, len(owned))
	written := make(map[string]string, len(owned))
	for _, k := range slices.Sorted(maps.Keys(owned)) {
		i := slices.IndexFunc(helmPassthroughKeys, func(key string) bool { return strings.EqualFold(key, k) })
		key := helmPassthroughKeys[i] // owned holds only keys that fold onto one of these
		if prior, dup := written[key]; dup {
			return nil, nil, errors.Errorf("%s: %s is given more than once (as %s and %s)", helmType, key, prior, k)
		}
		written[key] = k
		if v := owned[k]; v != nil {
			passthrough[key] = v
		}
	}
	if props.Source == nil {
		return nil, nil, errors.Errorf("%s: source is required", helmType)
	}
	switch s := props.Source; {
	case s.URL != "" && s.Name != "":
		return nil, nil, errors.Errorf("%s: source.url and source.name are mutually exclusive", helmType)
	case s.URL == "" && s.Name == "":
		return nil, nil, errors.Errorf("%s: source requires either source.url (inline) or source.name (reference)", helmType)
	case s.URL != "" && s.Namespace != "":
		return nil, nil, errors.Errorf("%s: source.namespace is only valid with source.name", helmType)
	}
	switch props.ValuesMode {
	case "", "inline", "configMap":
	default:
		return nil, nil, errors.Errorf("%s: unsupported valuesMode %q; supported values: inline, configMap", helmType, props.ValuesMode)
	}
	return props, passthrough, nil
}

// lowerHelmFlux emits the helmrelease component and, for an inline URL, the
// source it references (unless another helm component of the document already
// emitted the same one).
func lowerHelmFlux(comp *oam.Component, lctx oam.LoweringContext, props *helmProperties, passthrough map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	release := make(map[string]any, len(passthrough)+2)
	for k, v := range passthrough {
		release[k] = v
	}
	if props.ValuesMode != "" {
		release["valuesMode"] = props.ValuesMode
	}

	kind := src.Kind
	if src.URL != "" {
		var err error
		if kind, err = inlineChartSourceKind(helmType, src.URL, src.Kind, props.Chart); err != nil {
			return oam.LoweringResult{}, err
		}
	} else {
		switch kind {
		case "":
			return oam.LoweringResult{}, errors.Errorf("%s: source.kind is required when source.name is set", helmType)
		case "HelmRepository", "OCIRepository", "HelmChart":
		default:
			return oam.LoweringResult{}, errors.Errorf("%s: source.kind %q is not valid for a source reference; must be HelmRepository, OCIRepository, or HelmChart", helmType, kind)
		}
		if kind == "HelmRepository" && props.Chart == "" {
			return oam.LoweringResult{}, errors.Errorf("%s: source.kind HelmRepository requires chart to be specified", helmType)
		}
	}
	// An OCIRepository or HelmChart source already names the chart, and a
	// chartRef has no version: an inline OCI version becomes the generated
	// source's ref.tag, and a referenced source pins its own.
	if kind != "HelmRepository" {
		if props.Chart != "" {
			return oam.LoweringResult{}, errors.Errorf("%s: chart is not used with source.kind %s, which already names the chart", helmType, kind)
		}
		if props.Version != "" && src.URL == "" {
			return oam.LoweringResult{}, errors.Errorf("%s: version is not used with a referenced source.kind %s, which already pins the chart version", helmType, kind)
		}
	}

	// Every refusal is above: the source name is claimed only for a component
	// that lowers.
	var result oam.LoweringResult
	ref := map[string]any{"kind": kind, "name": src.Name}
	if src.Namespace != "" {
		ref["namespace"] = src.Namespace
	}
	if src.URL != "" {
		source, adopted, err := helmGeneratedSource(lctx, kind, src.URL, props.Version)
		if err != nil {
			return oam.LoweringResult{}, err
		}
		ref["name"] = source.Name
		if !adopted {
			result.Components = append(result.Components, source)
		}
	}
	if kind == "HelmRepository" {
		spec := map[string]any{"chart": props.Chart, "sourceRef": ref}
		if props.Version != "" {
			spec["version"] = props.Version
		}
		release["chart"] = map[string]any{"spec": spec}
	} else {
		release["chartRef"] = ref
	}

	result.Components = append(result.Components, oam.Component{
		Name:        comp.Name,
		Type:        "helmrelease",
		Properties:  release,
		Traits:      comp.Traits,
		Annotations: comp.Annotations,
	})
	return result, nil
}

// helmGeneratedSource names and builds the source component for an inline URL.
// adopted reports that another helm component of the document already emitted
// it: the caller then references it by name without emitting it again.
func helmGeneratedSource(lctx oam.LoweringContext, kind, url, version string) (oam.Component, bool, error) {
	identity := helmSourceIdentity(kind, url, version)
	sum := sha256.Sum256([]byte(identity))
	digest := hex.EncodeToString(sum[:])[:helmSourceDigestLen]
	name, adopted, err := lctx.Namer.NameOrAdopt(lctx.Origin.Document, "source-"+digest, identity, lctx.Origin)
	if err != nil {
		return oam.Component{}, false, errors.Wrapf(err, "%s: naming the generated source", helmType)
	}
	source := oam.Component{Name: name}
	switch kind {
	case "HelmRepository":
		source.Type = "helmrepository"
		source.Properties = map[string]any{"url": url}
	default: // OCIRepository, the only other kind inlineChartSourceKind returns
		source.Type = "ocirepository"
		source.Properties = map[string]any{"url": url}
		if version != "" {
			source.Properties["ref"] = map[string]any{"tag": version}
		}
	}
	return source, adopted, nil
}

// helmSourceIdentity is a generated source's content identity: every input
// that shapes it. It equals the helmchart composite's dedup key
// (HelmchartConfig.GetSourceKey).
func helmSourceIdentity(kind, url, version string) string {
	if kind == "OCIRepository" {
		return "oci:" + url + ":" + version
	}
	return "helm:" + url
}

// lowerHelmTemplate emits the helmtemplate component after refusing what a
// client-side render cannot honour.
func lowerHelmTemplate(comp *oam.Component, props *helmProperties, passthrough map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	if src.Name != "" {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: template requires an inline source URL; source.name is not supported", helmType)
	}
	for _, key := range helmFluxOnlyKeys {
		if _, ok := passthrough[key]; ok {
			return oam.LoweringResult{}, errors.Errorf("%s: delivery: template does not support %s (only a HelmRelease reads it)", helmType, key)
		}
	}
	if props.ValuesMode == "configMap" {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: template does not support valuesMode: configMap (values are baked into the client-side render at build time, not resolved from a cluster-side ConfigMap)", helmType)
	}
	kind, err := inlineChartSourceKind(helmType, src.URL, src.Kind, props.Chart)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	if kind == "OCIRepository" {
		if props.Version == "" {
			return oam.LoweringResult{}, errors.Errorf("%s: delivery: template with an OCIRepository source requires version to be set", helmType)
		}
		if props.Chart != "" {
			return oam.LoweringResult{}, errors.Errorf("%s: chart is not used with source.kind %s, which already names the chart", helmType, kind)
		}
	}

	rendered := map[string]any{"source": map[string]any{"url": src.URL, "kind": kind}}
	if props.Chart != "" {
		rendered["chart"] = props.Chart
	}
	if props.Version != "" {
		rendered["version"] = props.Version
	}
	if values, ok := passthrough["values"]; ok {
		rendered["values"] = values
	}
	return oam.LoweringResult{Components: []oam.Component{{
		Name:        comp.Name,
		Type:        helmTemplateType,
		Properties:  rendered,
		Traits:      comp.Traits,
		Annotations: comp.Annotations,
	}}}, nil
}
