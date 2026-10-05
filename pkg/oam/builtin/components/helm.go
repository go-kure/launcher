package components

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// helmChartContentMediaType is the media type of a Helm chart's content layer
// in an OCI artifact; a generated OCIRepository selects it.
const helmChartContentMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// helmPassthroughKeys are the helm properties forwarded verbatim to the
// helmrelease terminal under delivery: flux. They are split off before the
// strict decode, so their shape is the terminal's to check, and none of them
// reaches a generated source. values and releaseName are forwarded to the
// helmtemplate terminal under delivery: template too; every other one is
// refused there (helmFluxOnlyKeys).
var helmPassthroughKeys = []string{"values", "interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

// helmFluxOnlyKeys are the passthrough keys only a HelmRelease reads. delivery:
// template refuses each one, in this order, naming the key.
var helmFluxOnlyKeys = []string{"interval", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

// helmSecretValuesKey is the property holding the sensitive part of the values
// tree. Like the passthrough keys it is split off before the strict decode, so
// its content never reaches a decode error; unlike them it is never forwarded to
// the helmrelease (see helmSecretValuesTrait).
const helmSecretValuesKey = "secretValues"

// helmOwnedKeys are the keys decodeHelm splits off before the strict decode:
// the passthrough keys, secretValues, and scopeOverrides, which only a
// client-side render reads (go-kure/launcher#794, item 11). scopeOverrides is
// forwarded to the helmtemplate terminal under delivery: template and refused
// under delivery: flux.
var helmOwnedKeys = append(slices.Clone(helmPassthroughKeys), helmSecretValuesKey, scopeOverridesKey)

// HelmRule lowers a "helm" component (D1 component position,
// oam.ComponentLoweringRule) to the kind-named Flux terminals, the role-named
// successor to the removed helmchart composite (go-kure/launcher#336,
// go-kure/launcher#350):
//
//   - delivery: flux (the default) emits a `helmrelease` component carrying the
//     authored name, traits and annotations. With an inline source it also
//     emits the source the release reads: a `helmrepository` for an http(s)://
//     URL, an `ocirepository` for an oci:// URL (ref.tag = version), a
//     `gitrepository` for source.kind GitRepository (url plus exactly one
//     source.ref field), or a `bucket` for source.kind Bucket (endpoint and
//     bucketName instead of a url).
//   - delivery: template emits a `helmtemplate` component carrying the authored
//     name, traits and annotations, with the URL inline and any authored
//     releaseName. No source is emitted: the chart is rendered at build time.
//
// A generated source is named <document>-source-<digest>, the digest taken over
// the source's content identity (helmSourceIdentity), and resolved through
// LoweringContext.ResolveSharedName under the role oam.NameRoleHelmSource: helm
// components of one document that share a
// source identity (the URL, with the version for OCI and the ref for Git, or a
// Bucket's location) share one source, and the second one only references it. Two documents never share or collide on a generated source,
// because the document name is part of its name. The source uses its terminal's
// own interval default, never the release interval, so the identity says
// everything about its content. The Naming hook is asked for that name once for
// the document, with no component. source.name beside the inline source names
// the generated source instead (go-kure/launcher#787): the name is the
// component's own choice, so a component that writes it does not share the
// source of one that does not (unless it writes that source's own name: one
// name for one identity is one source), two components that write the same
// name for the same identity share one, and the same name for two identities
// is refused.
// The source is a component of the lowered document under its name, so it
// cannot take the name of another component, its own consumer included.
//
// With source.name alone the release references an existing source, optionally
// in source.namespace, and nothing else is emitted: a HelmRepository,
// GitRepository or Bucket through chart.spec.sourceRef, with chart naming the
// chart (its path in a GitRepository or Bucket artifact), or an OCIRepository
// or HelmChart through chartRef.
//
// The properties decode strictly: a key the schema does not declare, at any
// depth of source, is refused. delivery: template refuses every key only a
// HelmRelease reads (helmFluxOnlyKeys), source.name (it generates no source and
// reads none from the cluster), valuesMode: configMap, valuesConfigMapName and
// valuesSecretName, and an OCI source without a version, each with a helm:
// message naming what the author wrote rather than the terminal it would reach.
// delivery: flux refuses scopeOverrides, which only a client-side render reads:
// under delivery: template it is checked as the manifests component checks its
// own (parseScopeOverrides) and forwarded to the helmtemplate
// (go-kure/launcher#794, item 11).
// valuesMode has no registration-time default and is never forwarded: under
// configMap with non-empty values the rule moves the values into a configmap
// trait on the helmrelease and prepends a valuesFrom entry for it
// (helmValuesConfigMap).
//
// The values ConfigMap and the values Secret are named by the author
// (valuesConfigMapName, valuesSecretName), else by the Naming hook (roles
// oam.NameRoleValuesConfigMap and oam.NameRoleValuesSecret), else
// <component>-values-<hash> and <component>-secret-values-<hash>
// (go-kure/launcher#787). A name that is not the default is used as written,
// with no hash. Either property is refused where it names nothing: under
// delivery: template, valuesConfigMapName without valuesMode: configMap, and
// either one when the tree it names the object of is empty.
//
// secretValues is the sensitive part of the values tree (go-kure/launcher#786)
// and is never written into the HelmRelease or a ConfigMap. Under delivery: flux
// it becomes a secret trait on the helmrelease and a valuesFrom entry of kind
// Secret, placed after the values ConfigMap's entry and before the authored ones
// (helmSecretValuesTrait). Under delivery: template it is forwarded to the
// helmtemplate, which merges it over values for the render. A path set in both
// values and secretValues is refused, so the result does not depend on which of
// the two a delivery or values mode would let win. A key named global below the
// top level of secretValues is refused under either delivery, since Helm can
// print its value in a warning (refuseNestedGlobal). No message the rule raises
// carries a value of secretValues.
type HelmRule struct{}

// ComponentType claims the "helm" component type at the component lowering
// position. build.go registers this rule via RegisterComponentLowering; no
// dispatchable handler exists for "helm".
func (HelmRule) ComponentType() string { return helmType }

// PropertySchema declares the helm component's properties: exactly the keys
// helmProperties decodes plus helmOwnedKeys. A test ties the two.
func (HelmRule) PropertySchema() map[string]oam.PropertySchema {
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	object := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	return map[string]oam.PropertySchema{
		"chart":    str("Chart name within a HelmRepository source, or the chart's path within a GitRepository or Bucket source; required with those three. Refused with an OCIRepository or HelmChart source, which already name the chart."),
		"version":  str("Chart version. For an inline oci:// source it is the OCIRepository's ref.tag, and required under delivery: template. Refused with a referenced OCIRepository or HelmChart source, which already pin it, and with a GitRepository or Bucket source, whose chart Flux reads at the fetched revision, ignoring any version."),
		"delivery": {Type: oam.PropertyTypeString, Default: "flux", Enum: []any{"flux", "template"}, Description: "flux emits a HelmRelease (plus its source for an inline source); template renders the chart client-side at build time, from an inline HelmRepository or OCIRepository URL only."},
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "Chart source: inline (a url, or a Bucket's endpoint and bucketName; the source is generated, and shared by helm components of the document with the same content identity, and optionally named with name), or a reference (name, kind, namespace, and nothing inline) to an existing source CR.",
			Properties: map[string]oam.PropertySchema{
				"url":       str("Inline chart location: an http:// or https:// Helm repository URL, an oci:// URL naming the chart, or with kind GitRepository an http:// or https:// Git repository URL. Not used with kind Bucket."),
				"kind":      {Type: oam.PropertyTypeString, Enum: []any{"HelmRepository", "GitRepository", "Bucket", "OCIRepository", "HelmChart"}, Description: "Source kind. With url it is inferred from the scheme when unset and must agree with it (HelmRepository or OCIRepository); a Git repository URL needs kind GitRepository set. Kind Bucket with endpoint and bucketName generates a Bucket. With name alone (a reference) it is required and may be any of the five."},
				"name":      str("Alone: the name of an existing source CR to reference. Beside an inline source (url, or a Bucket's endpoint and bucketName): the name of the generated source, in place of <document>-source-<digest>, used as written; it must differ from every component name of the document, and components that write the same name share the source only when their source is the same. Not supported under delivery: template."),
				"namespace": str("Namespace of the referenced source CR. Only with a reference: refused beside an inline source, whose generated source is created beside the HelmRelease."),
				"ref": {
					Type:        oam.PropertyTypeObject,
					Description: "Git reference of an inline GitRepository source (its spec.ref): exactly one of branch, tag, semver, name, commit. Required with an inline kind GitRepository, and only valid there.",
					Properties: map[string]oam.PropertySchema{
						"branch": str("Branch to check out."),
						"tag":    str("Tag to check out."),
						"semver": str("SemVer range of tags to check out."),
						"name":   str("Git reference name to check out, e.g. refs/heads/main."),
						"commit": str("Commit SHA to check out."),
					},
				},
				"endpoint":   str("Object storage address of an inline Bucket source (its spec.endpoint). Required with an inline kind Bucket, and only valid there."),
				"bucketName": str("Bucket name of an inline Bucket source. Required with an inline kind Bucket, and only valid there."),
				"provider":   {Type: oam.PropertyTypeString, Enum: []any{"generic", "aws", "gcp", "azure"}, Description: "Provider of an inline Bucket source: generic (Flux's default), aws, gcp or azure. Only with an inline kind Bucket."},
				"region":     str("Region of an inline Bucket source's endpoint. Only with an inline kind Bucket."),
				"prefix":     str("Object prefix of an inline Bucket source, for server-side filtering. Only with an inline kind Bucket."),
			},
		},
		"values":              object("Helm values tree. Must be representable as JSON."),
		"secretValues":        object("Sensitive part of the Helm values tree, kept out of the HelmRelease and of any ConfigMap. Under delivery: flux it is emitted as a Secret (base64-encoded, not encrypted) by a secret trait on the HelmRelease, referenced by a valuesFrom entry placed after the values ConfigMap's and before the authored ones; under delivery: template it is merged over values for the render. A path set in both values and secretValues is refused, and so is a key named global below the top level. An environment policy may forbid it."),
		"valuesMode":          {Type: oam.PropertyTypeString, Enum: []any{"inline", "configMap"}, Description: "How values reach the HelmRelease: inline keeps them in spec.values; configMap moves non-empty values into a ConfigMap emitted by a configmap trait on the HelmRelease, referenced by a valuesFrom entry placed before the authored ones. Unset means inline. configMap is refused under delivery: template."},
		"valuesConfigMapName": str("Name of the ConfigMap valuesMode: configMap generates, in place of <component>-values-<hash>. It is used as written, with no hash: the name then stays the same when the values change. Refused without valuesMode: configMap, with empty values, and under delivery: template."),
		"valuesSecretName":    str("Name of the Secret generated for secretValues, in place of <component>-secret-values-<hash>. It is used as written, with no hash: the name then stays the same when secretValues changes. Refused with empty secretValues and under delivery: template."),
		"interval":            str("HelmRelease spec.interval as a Flux duration (default 60m). The generated source keeps its own default. Refused under delivery: template."),
		"releaseName":         str("Release name. Under delivery: flux, HelmRelease spec.releaseName. Under delivery: template, the render's .Release.Name: a DNS-1123 subdomain of at most 53 characters. Under both it defaults to the component name, shortened as Flux shortens a name over 53 characters."),
		"targetNamespace":     str("HelmRelease spec.targetNamespace. Refused under delivery: template."),
		"driftDetection":      object("HelmRelease spec.driftDetection. Refused under delivery: template."),
		"install":             object("HelmRelease spec.install: Helm install options. Refused under delivery: template."),
		"upgrade":             object("HelmRelease spec.upgrade: Helm upgrade options. Refused under delivery: template."),
		"valuesFrom": {Type: oam.PropertyTypeArray, Description: "HelmRelease spec.valuesFrom: ConfigMaps or Secrets supplying values. Refused under delivery: template.", Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "One values reference (kind, name, valuesKey, targetPath, optional).",
		}},
		scopeOverridesKey: scopeOverridesSchema("Explicit scope entries for kinds the chart renders under delivery: template, taking precedence over kure's own guess (not over a kind the Kubernetes API itself scopes; contradicting a CRD the chart renders is an error). A rendered object of a kind stated Namespaced that carries no namespace gets the application namespace; one of a kind stated Cluster is left as rendered. Refused under delivery: flux, where Helm creates the objects in the cluster."),
	}
}

// helmProperties is what the strict decode checks; helmOwnedKeys are split off
// before it.
type helmProperties struct {
	Chart      string      `json:"chart"`
	Version    string      `json:"version"`
	Delivery   string      `json:"delivery"`
	ValuesMode string      `json:"valuesMode"`
	Source     *helmSource `json:"source"`
	// The authored names of the values ConfigMap and the values Secret; nil when
	// absent or null. A present empty string is an authored name, and refused.
	ValuesConfigMapName *string `json:"valuesConfigMapName"`
	ValuesSecretName    *string `json:"valuesSecretName"`
}

// helmSource is an inline source or a reference to an existing source CR
// (name, kind, optional namespace). Inline, it is a URL with an optional kind
// (and with kind GitRepository a ref), or with kind Bucket an endpoint and
// bucketName (plus provider, region, prefix) and no URL; a name beside either
// names the generated source.
type helmSource struct {
	URL        string      `json:"url"`
	Kind       string      `json:"kind"`
	Name       string      `json:"name"`
	Namespace  string      `json:"namespace"`
	Ref        *helmGitRef `json:"ref"`
	Endpoint   string      `json:"endpoint"`
	BucketName string      `json:"bucketName"`
	Provider   string      `json:"provider"`
	Region     string      `json:"region"`
	Prefix     string      `json:"prefix"`
}

// helmGitRef is an inline GitRepository source's reference, emitted as its
// spec.ref. The rule requires exactly one field.
type helmGitRef struct {
	Branch string `json:"branch,omitempty"`
	Tag    string `json:"tag,omitempty"`
	SemVer string `json:"semver,omitempty"`
	Name   string `json:"name,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// fields lists r's keys, in Flux's GitRepositoryRef order, with their values.
func (r *helmGitRef) fields() [][2]string {
	return [][2]string{{"branch", r.Branch}, {"tag", r.Tag}, {"semver", r.SemVer}, {"name", r.Name}, {"commit", r.Commit}}
}

// inlineBucket reports whether s is read as an inline Bucket: kind Bucket that
// is no plain reference. A reference has a name and locates nothing itself; a
// url is counted as locating, so that it is refused as an inline Bucket's.
func (s *helmSource) inlineBucket() bool {
	return s.Kind == "Bucket" && (s.Name == "" || s.URL != "" || s.Endpoint != "" || s.BucketName != "")
}

// inline reports whether s generates a source: it has a url, or is an inline
// Bucket. Otherwise it is a reference to an existing source, by name.
func (s *helmSource) inline() bool { return s.URL != "" || s.inlineBucket() }

// LowerComponent decodes comp as a helm component and emits its terminal
// components (see HelmRule).
func (HelmRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	props, passthrough, secretValues, err := decodeHelm(comp.Properties)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	switch props.Delivery {
	case "", "flux":
		return lowerHelmFlux(comp, lctx, props, passthrough, secretValues)
	case "template":
		return lowerHelmTemplate(comp, props, passthrough, secretValues)
	default:
		return oam.LoweringResult{}, errors.Errorf("%s: unsupported delivery %q; supported values: flux, template", helmType, props.Delivery)
	}
}

// decodeHelm decodes props strictly and checks what holds for every delivery:
// a source that is inline (a url, or an inline Bucket's endpoint and
// bucketName) or a reference (a name alone), each source key only in the form
// that reads it, and a known valuesMode. The
// passthrough keys come back in their own map under their declared spelling,
// without nulls (absent), and with them scopeOverrides, which each delivery
// takes out of the map before anything is forwarded (lowerHelmFlux refuses it,
// lowerHelmTemplate checks it). secretValues comes back on its own: nil when absent or null,
// refused when it is not an object or has a key named global below its top
// level (refuseNestedGlobal). The strict decode matches keys
// case-insensitively, as encoding/json does, so two spellings of one key, at the
// top level or in source, are refused rather than one silently winning.
func decodeHelm(src map[string]any) (*helmProperties, map[string]any, map[string]any, error) {
	fail := func(err error) (*helmProperties, map[string]any, map[string]any, error) {
		return nil, nil, nil, err
	}
	if err := refuseFoldedKeys("", src); err != nil {
		return fail(err)
	}
	for k, v := range src {
		if s, ok := v.(map[string]any); ok && strings.EqualFold(k, "source") {
			if err := refuseFoldedKeys("source.", s); err != nil {
				return fail(err)
			}
			for rk, rv := range s {
				if r, ok := rv.(map[string]any); ok && strings.EqualFold(rk, "ref") {
					if err := refuseFoldedKeys("source.ref.", r); err != nil {
						return fail(err)
					}
				}
			}
		}
	}
	props, owned, err := builtin.DecodeStrictJSON[helmProperties](src, helmOwnedKeys...)
	if err != nil {
		return fail(errors.Errorf("%s: properties do not decode: %w", helmType, err))
	}
	passthrough := make(map[string]any, len(owned))
	var secretValues map[string]any
	for _, k := range slices.Sorted(maps.Keys(owned)) {
		i := slices.IndexFunc(helmOwnedKeys, func(key string) bool { return strings.EqualFold(key, k) })
		key := helmOwnedKeys[i] // owned holds only keys that fold onto one of these
		v := owned[k]
		if key == scopeOverridesKey {
			// A null, typed or untyped, reads as omission, as it does on the
			// manifests and helmtemplate components (parseScopeOverrides).
			if !isExplicitNull(v) {
				passthrough[key] = v
			}
			continue
		}
		if key != helmSecretValuesKey {
			if v != nil {
				passthrough[key] = v
			}
			continue
		}
		if oam.IsNullValue(v) {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fail(errors.Errorf("%s: %s: must be an object, got %T", helmType, helmSecretValuesKey, v))
		}
		if err := refuseNestedGlobal(helmType, m); err != nil {
			return fail(err)
		}
		secretValues = m
	}
	if props.Source == nil {
		return fail(errors.Errorf("%s: source is required", helmType))
	}
	s := props.Source
	switch {
	case s.inlineBucket():
		if s.URL != "" {
			return fail(errors.Errorf("%s: source.kind Bucket takes source.endpoint and source.bucketName, not source.url", helmType))
		}
		if s.Endpoint == "" || s.BucketName == "" {
			return fail(errors.Errorf("%s: an inline source.kind Bucket requires source.endpoint and source.bucketName", helmType))
		}
		// Only a bare host[:port] or an https:// URL of a host and port is
		// accepted, so user info, a signed query or anything else an endpoint
		// could smuggle is refused. The value is not quoted back.
		if !plainBucketEndpoint(s.Endpoint) {
			return fail(errors.Errorf("%s: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)", helmType))
		}
	case s.URL == "" && s.Name == "":
		return fail(errors.Errorf("%s: source requires either source.url (inline) or source.name (reference)", helmType))
	}
	// A reference alone has a namespace to name. Beside an inline source,
	// source.name names the generated source, which is created beside the
	// HelmRelease whatever the author writes here.
	if s.inline() && s.Namespace != "" {
		return fail(errors.Errorf("%s: source.namespace is only valid with a reference to an existing source (source.name and no inline source); a generated source is created beside the HelmRelease", helmType))
	}
	if !s.inlineBucket() {
		for _, f := range [][2]string{{"endpoint", s.Endpoint}, {"bucketName", s.BucketName}, {"provider", s.Provider}, {"region", s.Region}, {"prefix", s.Prefix}} {
			if f[1] != "" {
				return fail(errors.Errorf("%s: source.%s is only valid with an inline source.kind Bucket", helmType, f[0]))
			}
		}
	}
	if s.Ref != nil && (s.Kind != "GitRepository" || s.URL == "") {
		return fail(errors.Errorf("%s: source.ref is only valid with an inline source.kind GitRepository", helmType))
	}
	switch props.ValuesMode {
	case "", "inline", "configMap":
	default:
		return fail(errors.Errorf("%s: unsupported valuesMode %q; supported values: inline, configMap", helmType, props.ValuesMode))
	}
	return props, passthrough, secretValues, nil
}

// refuseFoldedKeys refuses two keys of m that are equal under Unicode case
// folding (strings.EqualFold, which also folds ſ onto s): the decode would match
// both to one field and keep whichever it read last. The pairwise compare is
// deliberate; lowercasing alone is a weaker fold. A properties map is small.
func refuseFoldedKeys(prefix string, m map[string]any) error {
	keys := slices.Sorted(maps.Keys(m))
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if strings.EqualFold(a, b) {
				return errors.Errorf("%s: %s%s and %s%s are one key given more than once (keys match ignoring case)", helmType, prefix, a, prefix, b)
			}
		}
	}
	return nil
}

// fluxUserinfoRemedy completes refuseURLUserinfo's message under delivery: flux,
// where a credential belongs in an authored source's secretRef.
const fluxUserinfoRemedy = ", which would be written in plain text into the generated source; author a helmrepository or ocirepository with secretRef and reference it with source.name"

// lowerHelmFlux emits the helmrelease component and, for an inline source, the
// source it references (unless another helm component of the document already
// emitted the same one).
func lowerHelmFlux(comp *oam.Component, lctx oam.LoweringContext, props *helmProperties, passthrough, secretValues map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	// Helm creates a HelmRelease's objects in the cluster, so nothing here
	// could apply a stated scope; dropping the property silently would let an
	// author believe it took effect.
	if _, ok := passthrough[scopeOverridesKey]; ok {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: flux does not support %s (only a client-side render reads it)", helmType, scopeOverridesKey)
	}
	if props.ValuesConfigMapName != nil && props.ValuesMode != "configMap" {
		return oam.LoweringResult{}, errors.Errorf("%s: valuesConfigMapName: names the values ConfigMap, and valuesMode is not configMap, so none is generated; remove it, or set valuesMode: configMap", helmType)
	}
	// The names this rule resolves are the component's, also where a caller
	// built the context without it.
	lctx.Component = comp
	release := maps.Clone(passthrough)

	kind := src.Kind
	switch {
	case !src.inline():
		switch kind {
		case "":
			return oam.LoweringResult{}, errors.Errorf("%s: source.kind is required when source.name is set", helmType)
		case "HelmRepository", "GitRepository", "Bucket", "OCIRepository", "HelmChart":
		default:
			return oam.LoweringResult{}, errors.Errorf("%s: source.kind %q is not valid for a source reference; must be HelmRepository, GitRepository, Bucket, OCIRepository, or HelmChart", helmType, kind)
		}
	case kind == "GitRepository":
		if err := checkHelmGitSource(src); err != nil {
			return oam.LoweringResult{}, err
		}
	case kind == "Bucket":
		// decodeHelm required endpoint and bucketName; the bucket terminal
		// checks the rest.
	default:
		if err := refuseURLUserinfo(helmType, src.URL, fluxUserinfoRemedy); err != nil {
			return oam.LoweringResult{}, err
		}
		var err error
		if kind, err = inlineChartSourceKind(helmType, src.URL, src.Kind, props.Chart); err != nil {
			return oam.LoweringResult{}, err
		}
	}
	if helmChartTemplateKind(kind) && props.Chart == "" {
		return oam.LoweringResult{}, errors.Errorf("%s: source.kind %s requires chart to be specified", helmType, kind)
	}
	// Flux reads a GitRepository or Bucket chart at the source's fetched
	// revision and ignores chart.spec.version, so an authored one would be
	// silently dropped.
	if (kind == "GitRepository" || kind == "Bucket") && props.Version != "" {
		return oam.LoweringResult{}, errors.Errorf("%s: version is not used with source.kind %s, whose chart is read at the source's fetched revision", helmType, kind)
	}
	// An OCIRepository or HelmChart source already names the chart, and a
	// chartRef has no version: an inline OCI version becomes the generated
	// source's ref.tag, and a referenced source pins its own.
	if !helmChartTemplateKind(kind) {
		if props.Chart != "" {
			return oam.LoweringResult{}, errors.Errorf("%s: chart is not used with source.kind %s, which already names the chart", helmType, kind)
		}
		if props.Version != "" && src.URL == "" {
			return oam.LoweringResult{}, errors.Errorf("%s: version is not used with a referenced source.kind %s, which already pins the chart version", helmType, kind)
		}
	}

	// The generated valuesFrom entries, in merge order: the values ConfigMap,
	// then the values Secret. Both go ahead of the authored entries. Each of the
	// two names is resolved last, once nothing of its tree is left to refuse.
	traits := comp.Traits
	var generated []any
	secretTrait, secretEntry, err := helmSecretValuesTrait(release["values"], secretValues,
		helmValuesNamer(lctx, comp, oam.NameRoleValuesSecret, "Secret", "secret-values", "valuesSecretName", props.ValuesSecretName))
	if err != nil {
		return oam.LoweringResult{}, err
	}
	if secretTrait == nil && props.ValuesSecretName != nil {
		return oam.LoweringResult{}, errors.Errorf("%s: valuesSecretName: names the values Secret, and %s is empty, so none is generated; remove it, or set %s", helmType, helmSecretValuesKey, helmSecretValuesKey)
	}
	if props.ValuesMode == "configMap" {
		trait, entry, err := helmValuesConfigMap(release,
			helmValuesNamer(lctx, comp, oam.NameRoleValuesConfigMap, "ConfigMap", "values", "valuesConfigMapName", props.ValuesConfigMapName))
		if err != nil {
			return oam.LoweringResult{}, err
		}
		if trait == nil && props.ValuesConfigMapName != nil {
			return oam.LoweringResult{}, errors.Errorf("%s: valuesConfigMapName: names the values ConfigMap, and values is empty, so none is generated; remove it, or set values", helmType)
		}
		if trait != nil {
			traits = append(slices.Clone(traits), *trait)
			generated = append(generated, entry)
		}
	}
	if secretTrait != nil {
		traits = append(slices.Clone(traits), *secretTrait)
		generated = append(generated, secretEntry)
	}
	if err := helmPrependValuesFrom(release, generated); err != nil {
		return oam.LoweringResult{}, err
	}

	// Every refusal is above: the source name is claimed only for a component
	// that lowers.
	var result oam.LoweringResult
	ref := map[string]any{"kind": kind, "name": src.Name}
	if src.Namespace != "" {
		ref["namespace"] = src.Namespace
	}
	if src.inline() {
		source, adopted, err := helmGeneratedSource(lctx, comp, kind, src, props.Version)
		if err != nil {
			return oam.LoweringResult{}, err
		}
		ref["name"] = source.Name
		if !adopted {
			result.Components = append(result.Components, source)
		}
	}
	if helmChartTemplateKind(kind) {
		spec := map[string]any{"chart": props.Chart, "sourceRef": ref}
		if props.Version != "" {
			spec["version"] = props.Version
		}
		// Flux builds a new chart artifact from a GitRepository or Bucket only when
		// the chart's version changes, unless reconcileStrategy is Revision; a chart
		// read from a moving branch or a changed bucket would otherwise never deploy.
		if kind == "GitRepository" || kind == "Bucket" {
			spec["reconcileStrategy"] = "Revision"
		}
		release["chart"] = map[string]any{"spec": spec}
	} else {
		release["chartRef"] = ref
	}

	helmRelease := oam.Component{
		Name:        comp.Name,
		Type:        "helmrelease",
		Properties:  release,
		Traits:      traits,
		Annotations: comp.Annotations,
	}
	// The release is applied after the source this rule generated for it, its
	// own or the one another helm component of the document already emitted. A
	// source the author wrote is the author's to order.
	if src.inline() {
		helmRelease.OrderAfter(ref["name"].(string))
	}
	result.Components = append(result.Components, helmRelease)
	return result, nil
}

// helmValuesKey is the data key the values ConfigMap stores the serialized
// values under, and the valuesKey its valuesFrom entry names. The stored bytes
// are JSON (a YAML subset, which is how Flux reads a valuesFrom value), so the
// key says what they are.
const helmValuesKey = "values.json"

// helmValuesHashLen is how many hex digits of the values digest the values
// ConfigMap's name carries.
const helmValuesHashLen = 10

// helmValuesNamer returns what names one values object of comp from the hex
// digest of its content: the authored name (property, nil for none), else the
// Naming hook's under role, else "<component>-<prefix>-<first 10 digest
// digits>", shortened by the one shortening rule when it would exceed 253
// characters (LoweringContext.ResolveName). Only the component name is cut,
// and the digest of the whole name takes the place of what is cut: a plain
// truncation would give two components whose long names share a prefix the
// same object, one clobbering the other's values. The suffix survives, so the
// default always carries the values hash.
//
// kind is the object's, a core one. The object follows the HelmRelease to a
// Flux namespace, so its name is claimed there (NameSpec.FluxScoped).
func helmValuesNamer(lctx oam.LoweringContext, comp *oam.Component, role oam.NameRole, kind, prefix, property string, authored *string) func(digest string) (string, error) {
	return func(digest string) (string, error) {
		spec := oam.NameSpec{Role: role, Kind: schema.GroupKind{Kind: kind}, FluxScoped: true}
		if authored != nil {
			spec.Property, spec.Authored = property, *authored
		}
		return lctx.ResolveName(comp.Name, prefix+"-"+digest[:helmValuesHashLen], spec)
	}
}

// helmValuesConfigMap implements valuesMode: configMap on release, the
// helmrelease properties lowerHelmFlux builds. It removes values and, when
// they are non-empty, returns the configmap trait that emits a ConfigMap named
// by name (helmValuesNamer), for the helmrelease to carry, and the valuesFrom
// entry naming it. lowerHelmFlux puts the entry ahead of the authored ones
// (helmPrependValuesFrom), so an authored entry still wins on a shared key, as
// Flux merges valuesFrom in order. Absent or empty values return a nil trait
// and no entry.
//
// The trait is the configmap trait as authored documents use it, so the
// ConfigMap follows the HelmRelease to a Flux namespace (it reads the
// ConfigMap through valuesFrom) and is the helmrelease component's object for
// pruning and replacement. Like every configmap trait it is built through the
// configmap kind's own code (go-kure/launcher#741); the values travel as one
// string, so the kind's string-only data typing never refuses them.
//
// The values are serialized once. Those exact bytes are stored in the
// ConfigMap and hashed into its default name, so that name changes whenever
// the content does: the HelmRelease's spec changes with it, which makes Flux
// reconcile a values-only edit at once, and two components with identical
// values carry the same hash. A name from the author or the Naming hook
// carries no hash and does not change with the content: the HelmRelease's
// spec then stays as it was, and Flux picks the new values up at the release's
// next reconciliation (its interval), or at once where the ConfigMap is
// labelled for helm-controller's watch (reconcile.fluxcd.io/watch: Enabled),
// which this rule does not set.
func helmValuesConfigMap(release map[string]any, name func(digest string) (string, error)) (*oam.Trait, map[string]any, error) {
	raw, ok := release["values"]
	delete(release, "values")
	if !ok {
		return nil, nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, errors.Errorf("%s: values is not representable as JSON: %w", helmType, err)
	}
	values, err := helmReleaseValuesMap(&apiextensionsv1.JSON{Raw: encoded})
	if err != nil {
		return nil, nil, err
	}
	if len(values) == 0 {
		return nil, nil, nil
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, nil, errors.Errorf("%s: values is not representable as JSON: %w", helmType, err)
	}
	sum := sha256.Sum256(data)
	cmName, err := name(hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, nil, errors.Wrapf(err, "%s: naming the values ConfigMap", helmType)
	}

	entry := map[string]any{"kind": "ConfigMap", "name": cmName, "valuesKey": helmValuesKey}
	return &oam.Trait{
		Type: "configmap",
		Properties: map[string]any{
			"name": cmName,
			"data": map[string]any{helmValuesKey: string(data)},
		},
	}, entry, nil
}

// helmPrependValuesFrom puts the generated valuesFrom entries, in order, ahead
// of the authored ones on release. With no generated entry release is left as
// it is.
func helmPrependValuesFrom(release map[string]any, generated []any) error {
	if len(generated) == 0 {
		return nil
	}
	switch authored := release["valuesFrom"].(type) {
	case nil:
		release["valuesFrom"] = generated
	case []any:
		release["valuesFrom"] = append(generated, authored...)
	default:
		// A library caller may pass a typed list ([]map[string]any,
		// []helmv2.ValuesReference). The helmrelease decodes valuesFrom through
		// JSON, so normalize through JSON too; anything that is not a JSON array
		// is refused here. The caller's list is strict-decoded first, as the
		// helmrelease would decode it: the round trip into []any keeps only the
		// last of duplicate keys inside a raw entry, which would hide a bad
		// earlier value from the helmrelease's own strict decode.
		var list []any
		encoded, err := json.Marshal(authored)
		if err == nil {
			err = json.Unmarshal(encoded, &list)
		}
		if err != nil {
			return errors.Errorf("%s: valuesFrom must be a list, got %T", helmType, authored)
		}
		if _, _, err := builtin.DecodeStrictJSON[helmValuesFromSpec](map[string]any{"valuesFrom": authored}); err != nil {
			return errors.Errorf("%s: valuesFrom: %w", helmType, err)
		}
		release["valuesFrom"] = append(generated, list...)
	}
	return nil
}

// helmValuesFromSpec is the valuesFrom field of HelmReleaseSpec alone, for
// helmPrependValuesFrom's strict decode of a typed list.
type helmValuesFromSpec struct {
	ValuesFrom []helmv2.ValuesReference `json:"valuesFrom"`
}

// plainSourceURL reports whether raw is exactly an http:// or https:// URL made
// of a host, an optional port in 1-65535 and, when withPath is set, a non-root path (without
// it, at most "/"). The URL is re-assembled from what url.Parse found and must
// match raw, so user info, a query, a fragment or anything else is refused by
// construction rather than by a list of what to look for: an inline source has no
// credential form, and the address is written verbatim into the generated source.
func plainSourceURL(raw string, withPath bool) bool {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") {
		return false
	}
	// url.Parse checks only that a port is digits; 0 or above 65535 would be
	// emitted as written and the source would never become ready.
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	path := u.EscapedPath()
	if withPath != (path != "" && path != "/") {
		return false
	}
	return rest == u.Host+path
}

// plainBucketEndpoint reports whether an inline Bucket endpoint is a bare
// host[:port], or an https:// URL of only a host and port. http:// is refused:
// Flux reaches a non-TLS endpoint only with spec.insecure, which the inline form
// does not take, so the generated bucket could never become ready.
func plainBucketEndpoint(endpoint string) bool {
	if strings.Contains(endpoint, "://") {
		return strings.HasPrefix(endpoint, "https://") && plainSourceURL(endpoint, false)
	}
	return plainSourceURL("https://"+endpoint, false)
}

// checkHelmGitSource checks an inline GitRepository source: an http:// or https://
// URL of a host and repository path only (plainSourceURL), and exactly one
// source.ref field. An ssh:// URL is refused: Flux reads it only with a key from
// spec.secretRef, which the inline form does not take, so the generated source
// could never become ready. A user or token in the URL is refused too: it would be
// written into the generated manifest instead of a Secret. Flux checks out branch master when
// spec.ref is empty and picks one field by precedence when several are set; the
// rule refuses both rather than follow either silently.
func checkHelmGitSource(src *helmSource) error {
	if !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
		return errors.Errorf("%s: source.kind GitRepository requires an http:// or https:// URL; an ssh:// repository needs credentials, so author a gitrepository and reference it", helmType)
	}
	if !plainSourceURL(src.URL, true) {
		return errors.Errorf("%s: source.url of an inline GitRepository must be an http:// or https:// URL of a host, an optional port in 1-65535 and a repository path only; user info, a query or a fragment is not taken inline (author a gitrepository with a secretRef and reference it)", helmType)
	}
	var set []string
	if src.Ref != nil {
		for _, f := range src.Ref.fields() {
			if f[1] != "" {
				set = append(set, f[0])
			}
		}
	}
	switch len(set) {
	case 0:
		return errors.Errorf("%s: an inline source.kind GitRepository requires source.ref with exactly one of branch, tag, semver, name, commit", helmType)
	case 1:
		return nil
	default:
		return errors.Errorf("%s: source.ref sets %s; an inline GitRepository takes exactly one of branch, tag, semver, name, commit", helmType, strings.Join(set, ", "))
	}
}

// helmChartTemplateKind reports whether a source of kind is read through the
// HelmRelease's chart template (chart.spec.sourceRef), whose kinds Flux limits
// to HelmRepository, GitRepository and Bucket. Every other kind is a chartRef.
func helmChartTemplateKind(kind string) bool {
	switch kind {
	case "HelmRepository", "GitRepository", "Bucket":
		return true
	}
	return false
}

// helmGeneratedSource names and builds the source component for the inline
// source of comp. adopted reports that another helm component of the document
// already emitted it: the caller then references it by name without emitting
// it again. The name is source.name when the author wrote one, else the
// document's shared one (see HelmRule).
func helmGeneratedSource(lctx oam.LoweringContext, comp *oam.Component, kind string, src *helmSource, version string) (oam.Component, bool, error) {
	identity, err := helmGeneratedSourceIdentity(kind, src, version)
	if err != nil {
		return oam.Component{}, false, err
	}
	name, adopted, err := generatedSourceName(lctx, helmType, comp, kind, identity, src.Name)
	if err != nil {
		return oam.Component{}, false, err
	}
	source := oam.Component{Name: name}
	url := src.URL
	switch kind {
	case "HelmRepository":
		source.Type = "helmrepository"
		source.Properties = map[string]any{"url": url}
	case "GitRepository":
		ref := map[string]any{}
		for _, f := range src.Ref.fields() {
			if f[1] != "" {
				ref[f[0]] = f[1]
			}
		}
		source.Type = "gitrepository"
		source.Properties = map[string]any{"url": url, "ref": ref}
	case "Bucket":
		source.Type = "bucket"
		source.Properties = map[string]any{"endpoint": src.Endpoint, "bucketName": src.BucketName}
		for _, f := range [][2]string{{"provider", src.Provider}, {"region", src.Region}, {"prefix", src.Prefix}} {
			if f[1] != "" {
				source.Properties[f[0]] = f[1]
			}
		}
	default: // OCIRepository, the only other kind inlineChartSourceKind returns
		// Copy the chart layer as-is: Flux's default extracts the first layer and
		// re-archives it without the files its ignore rules exclude (*.zip,
		// *.png, ...), which a chart may read with .Files.Get.
		source.Type = "ocirepository"
		source.Properties = map[string]any{"url": url, "layerSelector": map[string]any{
			"mediaType": helmChartContentMediaType,
			"operation": "copy",
		}}
		if version != "" {
			source.Properties["ref"] = map[string]any{"tag": version}
		}
	}
	return source, adopted, nil
}

// generatedSourceName names the Flux source of kind a rule generates for comp
// (owner is the rule's component type, for its messages), and claims it for
// identity, the source's content identity
// (LoweringContext.ResolveSharedName, role oam.NameRoleHelmSource). authored
// is the component's own name for it, "" for none: the source is then the
// document's, named <document>-source-<digest of identity> unless the Naming
// hook says otherwise, and shared by every component of the same identity that
// names none either. adopted reports that the source already exists.
//
// The source is a component of the lowered document under this name, and
// lands beside its consumer (NameSpec.FluxScoped). It cannot take comp's own
// name: the two would be one same-name sibling group, and the consumer ordered
// after itself. Nor can it take the name of another component the document
// holds when the rule runs, which the engine would refuse as a duplicate
// component name without saying where the second one comes from. A component
// another rule emits in the same round is still the engine's to refuse.
func generatedSourceName(lctx oam.LoweringContext, owner string, comp *oam.Component, kind, identity, authored string) (name string, adopted bool, err error) {
	spec := oam.NameSpec{Role: oam.NameRoleHelmSource, Kind: fluxSourceKind(kind), FluxScoped: true}
	if authored != "" {
		spec.Property, spec.Authored = "source.name", authored
	}
	// The name is then the default or the Naming hook's answer, and the rule is
	// not told which.
	const unauthoredRemedy = `rename that component, name the source with source.name, or have the Naming hook return another name for role "` + string(oam.NameRoleHelmSource) + `"`
	ownName := func(name string) error {
		if name != comp.Name {
			return nil
		}
		if authored != "" {
			return errors.Errorf("%s: source.name %q is the component's own name; the generated source is a component of the document too, so give it another name", owner, name)
		}
		return errors.Errorf("%s: the generated source would be named %q, the component's own name; the generated source is a component of the document too, so %s", owner, name, unauthoredRemedy)
	}
	// Before the name is claimed, where the author wrote it.
	if authored != "" {
		if err := ownName(authored); err != nil {
			return "", false, err
		}
	}
	sum := sha256.Sum256([]byte(identity))
	digest := hex.EncodeToString(sum[:])[:helmSourceDigestLen]
	lctx.Component = comp
	name, adopted, err = lctx.ResolveSharedName(lctx.Origin.Document, "source-"+digest, identity, spec)
	if err != nil {
		return "", false, errors.Wrapf(err, "%s: naming the generated source", owner)
	}
	if err := ownName(name); err != nil {
		return "", false, err
	}
	// An adopted source may already be in the document, emitted in an earlier
	// round; a new one must not meet a component there.
	if !adopted && lctx.Document != nil {
		for i := range lctx.Document.Spec.Components {
			other := &lctx.Document.Spec.Components[i]
			if other.Name != name {
				continue
			}
			if authored != "" {
				return "", false, errors.Errorf("%s: source.name %q is the name of a %s component of the document; the generated source is a component of the document too, so give it another name", owner, name, other.Type)
			}
			return "", false, errors.Errorf("%s: the generated source would be named %q, the name of a %s component of the document; the generated source is a component of the document too, so %s", owner, name, other.Type, unauthoredRemedy)
		}
	}
	return name, adopted, nil
}

// helmSourceIdentity is a generated source's content identity: every input
// that shapes it: kind and url, plus version for an OCI source.
func helmSourceIdentity(kind, url, version string) string {
	if kind == "OCIRepository" {
		return "oci:" + url + ":" + version
	}
	return "helm:" + url
}

// helmGeneratedSourceIdentity is the content identity of the source an inline
// source generates. A GitRepository's is its URL and ref, a Bucket's every key
// that locates it, each as JSON so that no separator inside a URL or endpoint
// can make two of them collide; a HelmRepository's and an OCIRepository's stay
// helmSourceIdentity's, so their generated names do not change.
func helmGeneratedSourceIdentity(kind string, src *helmSource, version string) (string, error) {
	var prefix string
	var content any
	switch kind {
	case "GitRepository":
		prefix, content = "git:", struct {
			URL string     `json:"url"`
			Ref helmGitRef `json:"ref"`
		}{src.URL, *src.Ref}
	case "Bucket":
		prefix, content = "bucket:", struct {
			Provider   string `json:"provider"`
			Endpoint   string `json:"endpoint"`
			BucketName string `json:"bucketName"`
			Region     string `json:"region"`
			Prefix     string `json:"prefix"`
		}{src.Provider, src.Endpoint, src.BucketName, src.Region, src.Prefix}
	default:
		return helmSourceIdentity(kind, src.URL, version), nil
	}
	b, err := json.Marshal(content)
	if err != nil {
		return "", errors.Wrapf(err, "%s: encoding the generated source identity", helmType)
	}
	return prefix + string(b), nil
}

// lowerHelmTemplate emits the helmtemplate component after refusing what a
// client-side render cannot honour.
func lowerHelmTemplate(comp *oam.Component, props *helmProperties, passthrough, secretValues map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	if src.Name != "" {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: template requires an inline source URL; source.name is not supported (the chart is rendered at build time: no source is generated, and none is read from the cluster)", helmType)
	}
	for _, name := range []struct {
		property string
		authored *string
		object   string
	}{
		{"valuesConfigMapName", props.ValuesConfigMapName, "ConfigMap"},
		{"valuesSecretName", props.ValuesSecretName, "Secret"},
	} {
		if name.authored != nil {
			return oam.LoweringResult{}, errors.Errorf("%s: delivery: template does not support %s (values are baked into the client-side render at build time, so no values %s is generated)", helmType, name.property, name.object)
		}
	}
	if src.Kind == "GitRepository" || src.Kind == "Bucket" {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: template does not support source.kind %s (a client-side render fetches the chart from a HelmRepository or OCIRepository only)", helmType, src.Kind)
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
	// The helmtemplate terminal checks both, and defaults the release name
	// from comp.Name, the name the HelmRelease gets under delivery: flux.
	for _, key := range []string{"values", "releaseName"} {
		if v, ok := passthrough[key]; ok {
			rendered[key] = v
		}
	}
	// The helmtemplate reads scopeOverrides with the same parser; a malformed
	// entry is refused here first, so the message names the component type the
	// author wrote. The authored list is forwarded as written.
	if v, ok := passthrough[scopeOverridesKey]; ok {
		if _, _, err := parseScopeOverrides(map[string]any{scopeOverridesKey: v}); err != nil {
			return oam.LoweringResult{}, errors.Errorf("%s: %w", helmType, err)
		}
		rendered[scopeOverridesKey] = v
	}
	// The helmtemplate merges secretValues over values for the render and
	// refuses a shared path itself; it is refused here first, so the message
	// names the component type the author wrote.
	if len(secretValues) > 0 {
		if values, ok := passthrough["values"].(map[string]any); ok {
			if err := refuseSharedValuePath(helmType, values, secretValues); err != nil {
				return oam.LoweringResult{}, err
			}
		}
		rendered[helmSecretValuesKey] = secretValues
	}
	return oam.LoweringResult{Components: []oam.Component{{
		Name:        comp.Name,
		Type:        helmTemplateType,
		Properties:  rendered,
		Traits:      comp.Traits,
		Annotations: comp.Annotations,
	}}}, nil
}
