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
// reaches a generated source. values is forwarded under delivery: template
// too; every other one is refused there (helmFluxOnlyKeys).
var helmPassthroughKeys = []string{"values", "interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

// helmFluxOnlyKeys are the passthrough keys only a HelmRelease reads. delivery:
// template refuses each one, in this order, naming the key.
var helmFluxOnlyKeys = []string{"interval", "releaseName", "targetNamespace", "driftDetection", "install", "upgrade", "valuesFrom"}

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
//     name, traits and annotations, with the URL inline. No source is emitted:
//     the chart is rendered at build time.
//
// A generated source is named <document>-source-<digest>, the digest taken over
// the source's content identity (helmSourceIdentity), and claimed through
// NameAllocator.NameOrAdopt: helm components of one document that share a
// source identity (the URL, with the version for OCI and the ref for Git, or a
// Bucket's location) share one source, and the second one only references it. Two documents never share or collide on a generated source,
// because the document name is part of its name. The source uses its terminal's
// own interval default, never the release interval, so the identity says
// everything about its content.
//
// With source.name the release references an existing source, optionally in
// source.namespace, and nothing else is emitted: a HelmRepository, GitRepository
// or Bucket through chart.spec.sourceRef, with chart naming the chart (its path
// in a GitRepository or Bucket artifact), or an OCIRepository or HelmChart
// through chartRef.
//
// The properties decode strictly: a key the schema does not declare, at any
// depth of source, is refused. delivery: template refuses every key only a
// HelmRelease reads (helmFluxOnlyKeys), a source reference, valuesMode:
// configMap, and an OCI source without a version, each with a helm: message
// naming what the author wrote rather than the terminal it would reach.
// valuesMode has no registration-time default and is never forwarded: under
// configMap with non-empty values the rule moves the values into a configmap
// trait on the helmrelease and prepends a valuesFrom entry for it
// (helmValuesConfigMap).
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
		"chart":    str("Chart name within a HelmRepository source, or the chart's path within a GitRepository or Bucket source; required with those three. Refused with an OCIRepository or HelmChart source, which already name the chart."),
		"version":  str("Chart version. For an inline oci:// source it is the OCIRepository's ref.tag, and required under delivery: template. Refused with a referenced OCIRepository or HelmChart source, which already pin it, and with a GitRepository or Bucket source, whose chart Flux reads at the fetched revision, ignoring any version."),
		"delivery": {Type: oam.PropertyTypeString, Default: "flux", Enum: []any{"flux", "template"}, Description: "flux emits a HelmRelease (plus its source for an inline source); template renders the chart client-side at build time, from an inline HelmRepository or OCIRepository URL only."},
		"source": {
			Type:        oam.PropertyTypeObject,
			Required:    true,
			Description: "Chart source: inline (a url, or a Bucket's endpoint and bucketName; the source is generated, and shared by helm components of the document with the same content identity), or a reference (name, kind, namespace) to an existing source CR.",
			Properties: map[string]oam.PropertySchema{
				"url":       str("Inline chart location: an http:// or https:// Helm repository URL, an oci:// URL naming the chart, or with kind GitRepository an http:// or https:// Git repository URL. Mutually exclusive with name; not used with kind Bucket."),
				"kind":      {Type: oam.PropertyTypeString, Enum: []any{"HelmRepository", "GitRepository", "Bucket", "OCIRepository", "HelmChart"}, Description: "Source kind. With url it is inferred from the scheme when unset and must agree with it (HelmRepository or OCIRepository); a Git repository URL needs kind GitRepository set. Without url or name, kind Bucket generates a Bucket from endpoint and bucketName. With name it is required and may be any of the five."},
				"name":      str("Name of an existing source CR to reference. Mutually exclusive with url; not supported under delivery: template."),
				"namespace": str("Namespace of the referenced source CR. Only with name."),
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
		"values":          object("Helm values tree. Must be representable as JSON."),
		"valuesMode":      {Type: oam.PropertyTypeString, Enum: []any{"inline", "configMap"}, Description: "How values reach the HelmRelease: inline keeps them in spec.values; configMap moves non-empty values into a ConfigMap emitted by a configmap trait on the HelmRelease, referenced by a valuesFrom entry placed before the authored ones. Unset means inline. configMap is refused under delivery: template."},
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

// helmSource is an inline source or a reference to an existing source CR
// (name, kind, optional namespace). Inline, it is a URL with an optional kind
// (and with kind GitRepository a ref), or with kind Bucket an endpoint and
// bucketName (plus provider, region, prefix) and no URL.
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

// inlineBucket reports whether s generates a Bucket: kind Bucket without a
// reference name.
func (s *helmSource) inlineBucket() bool { return s.Kind == "Bucket" && s.Name == "" }

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
// a source with exactly one of url and name (an inline Bucket has neither, but
// an endpoint and bucketName), each source key only in the form that reads it,
// and a known valuesMode. The
// passthrough keys come back in their own map under their declared spelling,
// without nulls (absent). The strict decode matches keys case-insensitively, as
// encoding/json does, so two spellings of one key, at the top level or in
// source, are refused rather than one silently winning.
func decodeHelm(src map[string]any) (*helmProperties, map[string]any, error) {
	if err := refuseFoldedKeys("", src); err != nil {
		return nil, nil, err
	}
	for k, v := range src {
		if s, ok := v.(map[string]any); ok && strings.EqualFold(k, "source") {
			if err := refuseFoldedKeys("source.", s); err != nil {
				return nil, nil, err
			}
			for rk, rv := range s {
				if r, ok := rv.(map[string]any); ok && strings.EqualFold(rk, "ref") {
					if err := refuseFoldedKeys("source.ref.", r); err != nil {
						return nil, nil, err
					}
				}
			}
		}
	}
	props, owned, err := builtin.DecodeStrictJSON[helmProperties](src, helmPassthroughKeys...)
	if err != nil {
		return nil, nil, errors.Errorf("%s: properties do not decode: %w", helmType, err)
	}
	passthrough := make(map[string]any, len(owned))
	for _, k := range slices.Sorted(maps.Keys(owned)) {
		i := slices.IndexFunc(helmPassthroughKeys, func(key string) bool { return strings.EqualFold(key, k) })
		key := helmPassthroughKeys[i] // owned holds only keys that fold onto one of these
		if v := owned[k]; v != nil {
			passthrough[key] = v
		}
	}
	if props.Source == nil {
		return nil, nil, errors.Errorf("%s: source is required", helmType)
	}
	s := props.Source
	switch {
	case s.URL != "" && s.Name != "":
		return nil, nil, errors.Errorf("%s: source.url and source.name are mutually exclusive", helmType)
	case s.inlineBucket():
		if s.URL != "" {
			return nil, nil, errors.Errorf("%s: source.kind Bucket takes source.endpoint and source.bucketName, not source.url", helmType)
		}
		if s.Endpoint == "" || s.BucketName == "" {
			return nil, nil, errors.Errorf("%s: an inline source.kind Bucket requires source.endpoint and source.bucketName", helmType)
		}
		// Only a bare host[:port] or an https:// URL of a host and port is
		// accepted, so user info, a signed query or anything else an endpoint
		// could smuggle is refused. The value is not quoted back.
		if !plainBucketEndpoint(s.Endpoint) {
			return nil, nil, errors.Errorf("%s: source.endpoint of an inline Bucket must be a host[:port], or an https:// URL of only a host and an optional port, any port in 1-65535; user info, a path, a query, a fragment or http:// is not taken inline (author a bucket, with a secretRef or insecure: true, and reference it)", helmType)
		}
	case s.URL == "" && s.Name == "":
		return nil, nil, errors.Errorf("%s: source requires either source.url (inline) or source.name (reference)", helmType)
	}
	if s.Name == "" && s.Namespace != "" {
		return nil, nil, errors.Errorf("%s: source.namespace is only valid with source.name", helmType)
	}
	if !s.inlineBucket() {
		for _, f := range [][2]string{{"endpoint", s.Endpoint}, {"bucketName", s.BucketName}, {"provider", s.Provider}, {"region", s.Region}, {"prefix", s.Prefix}} {
			if f[1] != "" {
				return nil, nil, errors.Errorf("%s: source.%s is only valid with an inline source.kind Bucket", helmType, f[0])
			}
		}
	}
	if s.Ref != nil && (s.Kind != "GitRepository" || s.URL == "") {
		return nil, nil, errors.Errorf("%s: source.ref is only valid with an inline source.kind GitRepository", helmType)
	}
	switch props.ValuesMode {
	case "", "inline", "configMap":
	default:
		return nil, nil, errors.Errorf("%s: unsupported valuesMode %q; supported values: inline, configMap", helmType, props.ValuesMode)
	}
	return props, passthrough, nil
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
func lowerHelmFlux(comp *oam.Component, lctx oam.LoweringContext, props *helmProperties, passthrough map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	release := maps.Clone(passthrough)
	traits := comp.Traits
	if props.ValuesMode == "configMap" {
		trait, err := helmValuesConfigMap(comp.Name, release)
		if err != nil {
			return oam.LoweringResult{}, err
		}
		if trait != nil {
			traits = append(slices.Clone(comp.Traits), *trait)
		}
	}

	kind := src.Kind
	switch {
	case src.Name != "":
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

	// Every refusal is above: the source name is claimed only for a component
	// that lowers.
	var result oam.LoweringResult
	ref := map[string]any{"kind": kind, "name": src.Name}
	if src.Namespace != "" {
		ref["namespace"] = src.Namespace
	}
	if src.Name == "" {
		source, adopted, err := helmGeneratedSource(lctx, kind, src, props.Version)
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

	result.Components = append(result.Components, oam.Component{
		Name:        comp.Name,
		Type:        "helmrelease",
		Properties:  release,
		Traits:      traits,
		Annotations: comp.Annotations,
	})
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

// helmValuesConfigMap implements valuesMode: configMap on release, the
// helmrelease properties lowerHelmFlux builds. It removes values and, when
// they are non-empty, prepends a valuesFrom entry for a ConfigMap named
// helmValuesConfigMapName and returns the configmap trait that emits it, for
// the helmrelease to carry. The entry goes ahead of the authored ones, so an
// authored entry still wins on a shared key, as Flux merges valuesFrom in
// order. Absent or empty values return a nil trait and no entry.
//
// The trait is the configmap trait as authored documents use it, so the
// ConfigMap follows the HelmRelease to a Flux namespace (it reads the
// ConfigMap through valuesFrom) and is the helmrelease component's object for
// pruning and replacement. Like every configmap trait it is built through the
// configmap kind's own code (go-kure/launcher#741); the values travel as one
// string, so the kind's string-only data typing never refuses them.
//
// The values are serialized once. Those exact bytes are stored in the
// ConfigMap and hashed into its name, so the name changes whenever the
// content does: the HelmRelease's spec changes with it, which makes Flux
// reconcile a values-only edit, and two components with identical values
// carry the same hash.
func helmValuesConfigMap(name string, release map[string]any) (*oam.Trait, error) {
	raw, ok := release["values"]
	delete(release, "values")
	if !ok {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.Errorf("%s: values is not representable as JSON: %w", helmType, err)
	}
	values, err := helmReleaseValuesMap(&apiextensionsv1.JSON{Raw: encoded})
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, errors.Errorf("%s: values is not representable as JSON: %w", helmType, err)
	}
	sum := sha256.Sum256(data)
	cmName := helmValuesConfigMapName(name, hex.EncodeToString(sum[:]))

	entry := map[string]any{"kind": "ConfigMap", "name": cmName, "valuesKey": helmValuesKey}
	switch authored := release["valuesFrom"].(type) {
	case nil:
		release["valuesFrom"] = []any{entry}
	case []any:
		release["valuesFrom"] = append([]any{entry}, authored...)
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
			return nil, errors.Errorf("%s: valuesFrom must be a list, got %T", helmType, authored)
		}
		if _, _, err := builtin.DecodeStrictJSON[helmValuesFromSpec](map[string]any{"valuesFrom": authored}); err != nil {
			return nil, errors.Errorf("%s: valuesFrom: %w", helmType, err)
		}
		release["valuesFrom"] = append([]any{entry}, list...)
	}
	return &oam.Trait{
		Type: "configmap",
		Properties: map[string]any{
			"name": cmName,
			"data": map[string]any{helmValuesKey: string(data)},
		},
	}, nil
}

// helmValuesFromSpec is the valuesFrom field of HelmReleaseSpec alone, for
// helmValuesConfigMap's strict decode of a typed list.
type helmValuesFromSpec struct {
	ValuesFrom []helmv2.ValuesReference `json:"valuesFrom"`
}

// helmValuesConfigMapName names the values ConfigMap of component name whose
// serialized values have the hex digest valuesDigest: boundedResourceName
// with the suffix "-values-<first 10 digest digits>". The suffix survives
// truncation, so the name always carries the values hash and is always a legal
// DNS-1123 subdomain within 253 bytes.
func helmValuesConfigMapName(name, valuesDigest string) string {
	return boundedResourceName(name, "-values-"+valuesDigest[:helmValuesHashLen])
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

// helmGeneratedSource names and builds the source component for an inline
// source. adopted reports that another helm component of the document already
// emitted it: the caller then references it by name without emitting it again.
func helmGeneratedSource(lctx oam.LoweringContext, kind string, src *helmSource, version string) (oam.Component, bool, error) {
	identity, err := helmGeneratedSourceIdentity(kind, src, version)
	if err != nil {
		return oam.Component{}, false, err
	}
	sum := sha256.Sum256([]byte(identity))
	digest := hex.EncodeToString(sum[:])[:helmSourceDigestLen]
	name, adopted, err := lctx.Namer.NameOrAdopt(lctx.Origin.Document, "source-"+digest, identity, lctx.Origin)
	if err != nil {
		return oam.Component{}, false, errors.Wrapf(err, "%s: naming the generated source", helmType)
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
func lowerHelmTemplate(comp *oam.Component, props *helmProperties, passthrough map[string]any) (oam.LoweringResult, error) {
	src := props.Source
	if src.Name != "" {
		return oam.LoweringResult{}, errors.Errorf("%s: delivery: template requires an inline source URL; source.name is not supported", helmType)
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
