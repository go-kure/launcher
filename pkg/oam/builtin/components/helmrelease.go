package components

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// helmReleaseDefaultInterval is spec.interval when the component leaves it
// unset. Flux requires the field; 60m matches the Flux source components.
const helmReleaseDefaultInterval = 60 * time.Minute

// HelmReleaseHandler handles the kind-named `helmrelease` component: a 1:1
// projection of Flux's HelmReleaseSpec. It emits exactly one HelmRelease and
// no source object: spec.chartRef, or the sourceRef of a chart template of kind
// HelmChart, names an existing one, and for a chart template of kind
// OCIRepository helm-controller generates the OCIRepository itself
// (go-kure/launcher#1072). Values in a ConfigMap are the `helm` component's
// valuesMode: configMap, which lowers to a helmrelease plus a configmap trait
// (go-kure/launcher#702).
type HelmReleaseHandler struct{}

// helmReleaseDurations are HelmReleaseSpec's duration fields, nested ones
// included. All take the form with h but chart.spec.timeout, the timeout of
// the OCIRepository a chart template inlines, which takes no h.
var helmReleaseDurations = []fluxDurationField[helmv2.HelmReleaseSpec]{
	{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration { return &s.Interval }},
	{path: []string{"timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration { return s.Timeout }},
	{path: []string{"chart", "spec", "interval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Chart == nil {
			return nil
		}
		return s.Chart.Spec.Interval
	}},
	{path: []string{"chart", "spec", "timeout"}, form: fluxduration.SourceTimeout, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Chart == nil {
			return nil
		}
		return s.Chart.Spec.Timeout
	}},
	{path: []string{"install", "timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Install == nil {
			return nil
		}
		return s.Install.Timeout
	}},
	{path: []string{"install", "strategy", "retryInterval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Install == nil || s.Install.Strategy == nil {
			return nil
		}
		return s.Install.Strategy.RetryInterval
	}},
	{path: []string{"upgrade", "timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Upgrade == nil {
			return nil
		}
		return s.Upgrade.Timeout
	}},
	{path: []string{"upgrade", "strategy", "retryInterval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Upgrade == nil || s.Upgrade.Strategy == nil {
			return nil
		}
		return s.Upgrade.Strategy.RetryInterval
	}},
	{path: []string{"test", "timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Test == nil {
			return nil
		}
		return s.Test.Timeout
	}},
	{path: []string{"rollback", "timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Rollback == nil {
			return nil
		}
		return s.Rollback.Timeout
	}},
	{path: []string{"uninstall", "timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Uninstall == nil {
			return nil
		}
		return s.Uninstall.Timeout
	}},
}

// CanHandle returns true for the helmrelease component type.
func (h *HelmReleaseHandler) CanHandle(componentType string) bool {
	return componentType == "helmrelease"
}

// PropertySchema declares every top-level key of helmv2.HelmReleaseSpec, so
// authored-property validation admits exactly those keys. The
// nested Flux shapes stay open objects here: ToApplicationConfig decodes the
// whole map strictly into HelmReleaseSpec, which refuses an unknown or
// wrongly typed key at any depth. A test ties this key set to the struct.
func (h *HelmReleaseHandler) PropertySchema() map[string]oam.PropertySchema {
	object := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	objects := func(desc, item string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeArray, Description: desc, Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: item}}
	}
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
	}
	return map[string]oam.PropertySchema{
		"chart":              object("HelmRelease spec.chart: a chart template; `kind` is the kind of the source object helm-controller generates from it. Kind HelmChart (the default) names a chart (`spec.chart`, required) in an existing HelmRepository, GitRepository or Bucket source (`spec.sourceRef`, required); the OCIRepository fields the template inlines are refused, as helm-controller does not read them for a HelmChart. Kind OCIRepository takes the OCIRepository fields, with the chart's `spec.url` (oci://, required) held to the allowed registries, and helm-controller generates the OCIRepository `<namespace>-<name>` in the HelmRelease's namespace; `spec.chart`, `sourceRef`, `valuesFiles` and `ignoreMissingValuesFiles` are refused, `version` takes only `*` and `reconcileStrategy` only ChartVersion, and `spec.timeout` must be under an hour. Exactly one of chart and chartRef is required."),
		"chartRef":           object("HelmRelease spec.chartRef: a reference to an existing OCIRepository, ExternalArtifact or HelmChart source. Exactly one of chart and chartRef is required."),
		"interval":           str("HelmRelease spec.interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms. Defaults to 60m when unset or zero."),
		"kubeConfig":         object("HelmRelease spec.kubeConfig: a kubeconfig reference for a remote cluster."),
		"suspend":            boolean("HelmRelease spec.suspend: stop reconciling the release."),
		"releaseName":        str("HelmRelease spec.releaseName. When unset it is set to the component name, whatever targetNamespace is; a name over 53 characters is shortened as Flux shortens a release name (its first 40 characters, '-', and 12 hex digits of its SHA-256). An authored value is written as it is."),
		"targetNamespace":    str("HelmRelease spec.targetNamespace. When unset and a Flux namespace is configured, it is set to the application namespace."),
		"storageNamespace":   str("HelmRelease spec.storageNamespace: where Helm stores release state."),
		"dependsOn":          objects("HelmRelease spec.dependsOn: releases that must be ready first.", "One dependency reference (name, namespace, readyExpr)."),
		"timeout":            str("HelmRelease spec.timeout for Helm actions, as a Flux duration: unsigned, units ms, s, m, h; 0s or at least 1ms."),
		"maxHistory":         {Type: oam.PropertyTypeInteger, Description: "HelmRelease spec.maxHistory: release revisions Helm keeps."},
		"serviceAccountName": str("HelmRelease spec.serviceAccountName Helm impersonates."),
		"persistentClient":   boolean("HelmRelease spec.persistentClient."),
		"driftDetection":     object("HelmRelease spec.driftDetection."),
		"install":            object("HelmRelease spec.install: Helm install options."),
		"upgrade":            object("HelmRelease spec.upgrade: Helm upgrade options."),
		"test":               object("HelmRelease spec.test: Helm test options."),
		"rollback":           object("HelmRelease spec.rollback: Helm rollback options."),
		"uninstall":          object("HelmRelease spec.uninstall: Helm uninstall options."),
		"valuesFrom":         objects("HelmRelease spec.valuesFrom: ConfigMaps or Secrets supplying values, merged in order.", "One values reference (kind, name, valuesKey, targetPath, optional)."),
		"values":             object("HelmRelease spec.values: the Helm values tree. Must be representable as JSON."),
		"commonMetadata":     object("HelmRelease spec.commonMetadata: labels and annotations applied to every rendered object."),
		"postRenderers":      objects("HelmRelease spec.postRenderers.", "One post renderer (kustomize)."),
		"postRenderStrategy": str("HelmRelease spec.postRenderStrategy."),
		"waitStrategy":       object("HelmRelease spec.waitStrategy."),
		"healthCheckExprs":   objects("HelmRelease spec.healthCheckExprs: custom CEL health checks.", "One custom health check."),
	}
}

// ExclusiveProperties declares exactly one of chart and chartRef, the rule
// validate enforces.
func (h *HelmReleaseHandler) ExclusiveProperties() []oam.ExclusiveGroup {
	return []oam.ExclusiveGroup{{Keys: []string{"chart", "chartRef"}, Required: true}}
}

// helmReleaseValuesModeHint points a helmrelease document that still sets the
// removed valuesMode key at what replaced it.
const helmReleaseValuesModeHint = `helmrelease no longer takes valuesMode (go-kure/launcher#702); values in a ConfigMap are the helm component's valuesMode: configMap, or a configmap trait plus a valuesFrom entry`

// UnsupportedFieldHint returns the pointer to helm's valuesMode for the
// removed valuesMode key, and "" for any other key. pkg/oam appends it to the
// unsupported-field error of an authored helmrelease component.
func (h *HelmReleaseHandler) UnsupportedFieldHint(key string) string {
	if key != "valuesMode" {
		return ""
	}
	return helmReleaseValuesModeHint
}

// ToApplicationConfig decodes the component's properties strictly into a
// helmv2.HelmReleaseSpec. Any key HelmReleaseSpec does not declare, at any
// depth, and any wrongly typed value is an error. Checks: exactly one of chart
// and chartRef, the chart template's rules (checkHelmReleaseChartTemplate),
// values a JSON object, each valuesFrom entry within the ValuesReference CRD's
// constraints, and chart.spec.verify.provider not an authored "". Every other
// constraint is left to Flux's own CRD admission.
func (h *HelmReleaseHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](component.Properties)
	if err != nil {
		hint := ""
		if strings.Contains(err.Error(), `unknown field "valuesMode"`) {
			hint = "; " + helmReleaseValuesModeHint
		}
		return nil, errors.Errorf("helmrelease: properties do not decode as a HelmReleaseSpec: %w%s", err, hint)
	}
	durations := helmReleaseDurations
	if spec.Chart != nil && isHelmChartTemplateKind(spec.Chart.Kind) {
		// chart.spec.timeout is refused as inert below, whatever its text.
		durations = slices.DeleteFunc(slices.Clone(durations), func(f fluxDurationField[helmv2.HelmReleaseSpec]) bool {
			return f.name() == "chart.spec.timeout"
		})
	}
	if err := checkAuthoredFluxDurations("helmrelease", component.Properties, durations); err != nil {
		return nil, err
	}
	if err := refuseEmptyFluxVerifyProvider(component.Properties, "chart", "spec", "verify"); err != nil {
		return nil, err
	}
	if spec.Chart != nil && isHelmChartTemplateKind(spec.Chart.Kind) {
		if err := refuseAuthoredInertChartFields(component.Properties); err != nil {
			return nil, err
		}
	}
	cfg := &HelmReleaseConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Spec:       *spec,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// HelmReleaseConfig implements stack.ApplicationConfig for helmrelease
// components.
type HelmReleaseConfig struct {
	// Name is the component name, and the HelmRelease's name unless ObjectName
	// names it. The default release name is derived from Name either way.
	Name string
	// ObjectName names the HelmRelease (oam.Component.ObjectName). Empty for
	// Name. It does not move the release name.
	ObjectName string
	// Metadata is the labels and annotations authored for the HelmRelease
	// (oam.Component.ObjectMetadata).
	Metadata oam.ObjectMetadata
	// Namespace is the application namespace. The HelmRelease lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string

	// Spec is the HelmRelease spec as authored. Generate copies it and
	// applies the interval default, the releaseName default, the
	// chart.spec.verify.provider default, and under a Flux namespace the
	// targetNamespace and storageNamespace defaults, to that copy.
	Spec helmv2.HelmReleaseSpec

	// fluxNS overrides the HelmRelease's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace. Empty
	// means Namespace.
	fluxNS string
}

// ApplyPolicy holds the url of a chart template of kind OCIRepository to the
// policy's allowed registries, as the ocirepository component holds its own
// (enforceFluxSourceOCIHost): it is the repository helm-controller generates an
// OCIRepository for. Nothing else of a HelmRelease names a host: a chartRef and
// the sourceRef of a chart template of kind HelmChart name existing sources.
func (c *HelmReleaseConfig) ApplyPolicy(p oam.Policy) error {
	if ch := c.Spec.Chart; ch != nil && ch.Kind == sourcev1.OCIRepositoryKind {
		return enforceFluxSourceOCIHost("helmrelease", "chart.spec.url", ch.Spec.URL, true, p)
	}
	return nil
}

// SetFluxNamespace moves the HelmRelease to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *HelmReleaseConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// fluxNamespace returns the namespace the HelmRelease lands in.
func (c *HelmReleaseConfig) fluxNamespace() string {
	if c.fluxNS != "" {
		return c.fluxNS
	}
	return c.Namespace
}

// validate holds the checks shared by the parse path and Generate. Generate
// repeats them because this type and its fields are exported: a config built
// directly by a library caller never went through ToApplicationConfig.
func (c *HelmReleaseConfig) validate() error {
	if (c.Spec.Chart == nil) == (c.Spec.ChartRef == nil) {
		return errors.New("helmrelease: exactly one of chart and chartRef is required")
	}
	if c.Spec.Chart != nil {
		if err := checkHelmReleaseChartTemplate(c.Spec.Chart); err != nil {
			return err
		}
	}
	if err := checkFluxDurations("helmrelease", &c.Spec, helmReleaseDurations); err != nil {
		return err
	}
	if _, err := c.releaseName(); err != nil {
		return err
	}
	if _, err := helmReleaseValuesMap(c.Spec.Values); err != nil {
		return err
	}
	for i := range c.Spec.ValuesFrom {
		if err := checkHelmReleaseValuesRef(&c.Spec.ValuesFrom[i]); err != nil {
			return errors.Errorf("helmrelease: valuesFrom[%d].%w", i, err)
		}
	}
	return nil
}

// helmReleaseChartInertFields are the chart.spec fields of a chart template
// that helm-controller does not carry into the HelmChart it generates. The
// template inlines an OCIRepositorySpec, which a template of kind
// OCIRepository reads and one of kind HelmChart does not: helm-controller
// v1.7.1 maps chart, version, interval, reconcileStrategy, valuesFiles,
// ignoreMissingValuesFiles, sourceRef and verify's provider and secretRef
// (internal/reconcile/helmchart_template.go:225-258), and the API refuses url
// without kind OCIRepository (api/v2/helmrelease_types.go:373). The two
// fields of the inlined spec that are read, interval and verify, are not
// listed. Each field maps to whether a spec sets it, for a config built
// directly in Go; an authored one is refused whatever its value
// (refuseAuthoredInertChartFields).
// TestHelmReleaseChartInertFields_CoverInlinedSpec holds the list to the
// inlined type.
var helmReleaseChartInertFields = map[string]func(*helmv2.HelmChartTemplateSpec) bool{
	"url":                func(s *helmv2.HelmChartTemplateSpec) bool { return s.URL != "" },
	"ref":                func(s *helmv2.HelmChartTemplateSpec) bool { return s.Reference != nil },
	"layerSelector":      func(s *helmv2.HelmChartTemplateSpec) bool { return s.LayerSelector != nil },
	"provider":           func(s *helmv2.HelmChartTemplateSpec) bool { return s.Provider != "" },
	"secretRef":          func(s *helmv2.HelmChartTemplateSpec) bool { return s.SecretRef != nil },
	"serviceAccountName": func(s *helmv2.HelmChartTemplateSpec) bool { return s.ServiceAccountName != "" },
	"certSecretRef":      func(s *helmv2.HelmChartTemplateSpec) bool { return s.CertSecretRef != nil },
	"proxySecretRef":     func(s *helmv2.HelmChartTemplateSpec) bool { return s.ProxySecretRef != nil },
	"timeout":            func(s *helmv2.HelmChartTemplateSpec) bool { return s.Timeout != nil },
	"ignore":             func(s *helmv2.HelmChartTemplateSpec) bool { return s.Ignore != nil },
	"insecure":           func(s *helmv2.HelmChartTemplateSpec) bool { return s.Insecure },
	"suspend":            func(s *helmv2.HelmChartTemplateSpec) bool { return s.Suspend },
}

// helmReleaseChartVerifyInertFields are the chart.spec.verify fields
// helm-controller does not carry into the generated HelmChart, held as
// helmReleaseChartInertFields are.
var helmReleaseChartVerifyInertFields = map[string]func(*sourcev1.OCIRepositoryVerification) bool{
	"matchOIDCIdentity":    func(v *sourcev1.OCIRepositoryVerification) bool { return v.MatchOIDCIdentity != nil },
	"trustedRootSecretRef": func(v *sourcev1.OCIRepositoryVerification) bool { return v.TrustedRootSecretRef != nil },
}

// helmReleaseChartInertRefusal is the refusal of an inert chart template field
// at path.
func helmReleaseChartInertRefusal(path string) error {
	return errors.Errorf("%s: not read for a chart template of kind HelmChart (the default); helm-controller does not carry it into the HelmChart it generates. It is a field of the OCIRepository a template of chart.kind OCIRepository generates; for a HelmChart, a source's own settings go on the source sourceRef names", path)
}

// isHelmChartTemplateKind reports whether a chart template of kind generates
// a HelmChart: kind HelmChart, or none, its default.
func isHelmChartTemplateKind(kind string) bool {
	return kind == "" || kind == sourcev1.HelmChartKind
}

// checkHelmReleaseChartTemplate holds a chart template to what helm-controller
// generates from it, by its kind, which takes the API's two values
// (api/v2/helmrelease_types.go:393): a HelmChart (checkHelmChartTemplate) or
// an OCIRepository (checkOCIRepositoryChartTemplate).
func checkHelmReleaseChartTemplate(ch *helmv2.HelmChartTemplate) error {
	switch {
	case isHelmChartTemplateKind(ch.Kind):
		return checkHelmChartTemplate(&ch.Spec)
	case ch.Kind == sourcev1.OCIRepositoryKind:
		return checkOCIRepositoryChartTemplate(&ch.Spec)
	}
	return errors.Errorf("chart.kind %q: the Flux API takes HelmChart (the default) or OCIRepository", ch.Kind)
}

// helmReleaseChartOCIMaxTimeout bounds chart.spec.timeout under kind
// OCIRepository: helm-controller copies the template into a typed
// OCIRepository and converts it with DefaultUnstructuredConverter before it
// applies it (helm-controller v1.7.1
// internal/reconcile/helmchart_template.go:130,152), which writes a
// metav1.Duration as Duration.String(). An hour or more then carries an h,
// which the OCIRepository's timeout pattern refuses
// (source-controller api v1.10.1 v1/ocirepository_types.go:132-134), whatever
// the component writes.
const helmReleaseChartOCIMaxTimeout = time.Hour

// checkOCIRepositoryChartTemplate holds a chart template of kind OCIRepository
// to the API's rules for that kind (api/v2/helmrelease_types.go:372-380): a url,
// starting with oci://, and no chart, sourceRef, valuesFiles or
// ignoreMissingValuesFiles, a version only of "*" and a reconcileStrategy only
// of ChartVersion. The rules are read on the decoded spec, so a value the type
// leaves out of the object, an ignoreMissingValuesFiles: false or an empty
// valuesFiles, is unauthored, as it is in the object. Last, a timeout must be
// under an hour (helmReleaseChartOCIMaxTimeout).
func checkOCIRepositoryChartTemplate(s *helmv2.HelmChartTemplateSpec) error {
	if err := checkFluxSourceURL("helmrelease", "chart.spec.url", s.URL, "oci://"); err != nil {
		return errors.Errorf("%w under chart.kind OCIRepository", err)
	}
	refused := func(field string) error {
		return errors.Errorf("chart.spec.%s: not taken under chart.kind OCIRepository, which generates an OCIRepository for the chart artifact chart.spec.url names (the Flux API refuses it)", field)
	}
	switch {
	case s.Chart != "":
		return refused("chart")
	case s.SourceRef != nil:
		return refused("sourceRef")
	case len(s.ValuesFiles) > 0:
		return refused("valuesFiles")
	case s.IgnoreMissingValuesFiles:
		return refused("ignoreMissingValuesFiles")
	case s.Version != "" && s.Version != "*":
		return errors.Errorf("chart.spec.version %q: under chart.kind OCIRepository only \"*\" is taken; the version is the tag or digest chart.spec.ref names (the Flux API refuses another)", s.Version)
	case s.ReconcileStrategy != "" && s.ReconcileStrategy != sourcev1.ReconcileStrategyChartVersion:
		return errors.Errorf("chart.spec.reconcileStrategy %q: under chart.kind OCIRepository only %s is taken (the Flux API refuses another)", s.ReconcileStrategy, sourcev1.ReconcileStrategyChartVersion)
	}
	if t := s.Timeout; t != nil && t.Duration >= helmReleaseChartOCIMaxTimeout {
		return errors.Errorf("chart.spec.timeout %s: under chart.kind OCIRepository it must be under an hour: helm-controller writes the OCIRepository it generates with the timeout in hours, which the OCIRepository's timeout does not take. For a longer timeout, use an ocirepository component and a chartRef to it", fluxduration.SourceTimeout.Format(t.Duration))
	}
	return nil
}

// checkHelmChartTemplate holds a chart template of kind HelmChart to what
// helm-controller generates a HelmChart from: a chart and a source reference
// with its kind, which the API requires for that kind
// (api/v2/helmrelease_types.go:374) and the type leaves out of the object when
// empty, and none of the inert fields set. A kind does not choose the source's
// kind for the author.
func checkHelmChartTemplate(s *helmv2.HelmChartTemplateSpec) error {
	if s.SourceRef == nil {
		return errors.New("chart.spec.sourceRef: required (the HelmRepository, GitRepository or Bucket the chart is in)")
	}
	if s.SourceRef.Kind == "" {
		return errors.New("chart.spec.sourceRef.kind: required (the Flux API takes HelmRepository, GitRepository or Bucket)")
	}
	if s.Chart == "" {
		return errors.New("chart.spec.chart: required (the name or path of the chart in its source)")
	}
	for _, field := range slices.Sorted(maps.Keys(helmReleaseChartInertFields)) {
		if helmReleaseChartInertFields[field](s) {
			return helmReleaseChartInertRefusal("chart.spec." + field)
		}
	}
	if s.Verify != nil {
		for _, field := range slices.Sorted(maps.Keys(helmReleaseChartVerifyInertFields)) {
			if helmReleaseChartVerifyInertFields[field](s.Verify) {
				return helmReleaseChartInertRefusal("chart.spec.verify." + field)
			}
		}
	}
	return nil
}

// refuseAuthoredInertChartFields refuses an inert chart template field an
// author wrote, whatever its value: a false, a "" or a null the typed spec
// cannot tell from an unauthored field is still not the component's to read.
// Keys match case-insensitively, as the decode's do. The properties are read
// in their JSON form, as the decode reads them (numbers kept exact, so a value
// float64 cannot hold is not refused here).
func refuseAuthoredInertChartFields(authored map[string]any) error {
	raw, err := json.Marshal(authored)
	if err != nil {
		return errors.Wrap(err, "helmrelease: properties do not encode as JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var props map[string]any
	if err := dec.Decode(&props); err != nil {
		return errors.Wrap(err, "helmrelease: properties do not read as a JSON object")
	}
	node := props
	var at []string
	for _, key := range []string{"chart", "spec"} {
		k, err := foldedKey(node, key, at)
		if err != nil || k == "" {
			return err
		}
		next, ok := node[k].(map[string]any)
		if !ok {
			return nil
		}
		node = next
		at = append(at, k)
	}
	if err := refuseInertChartKeys(node, "chart.spec.", slices.Sorted(maps.Keys(helmReleaseChartInertFields))); err != nil {
		return err
	}
	k, err := foldedKey(node, "verify", at)
	if err != nil || k == "" {
		return err
	}
	verify, ok := node[k].(map[string]any)
	if !ok {
		return nil
	}
	return refuseInertChartKeys(verify, "chart.spec.verify.", slices.Sorted(maps.Keys(helmReleaseChartVerifyInertFields)))
}

// refuseInertChartKeys refuses the first key of node, in key order, that matches
// one of fields case-insensitively (helmReleaseChartInertRefusal, at prefix
// and the field's json name).
func refuseInertChartKeys(node map[string]any, prefix string, fields []string) error {
	for _, k := range slices.Sorted(maps.Keys(node)) {
		for _, field := range fields {
			if strings.EqualFold(k, field) {
				return helmReleaseChartInertRefusal(prefix + field)
			}
		}
	}
	return nil
}

// releaseName returns spec.releaseName as Generate writes it: the authored
// value as it is, else the default every Helm release of the component gets
// (defaultHelmReleaseName), which is the name delivery: template renders the
// same component under (go-kure/launcher#785). Flux derives an unset release
// name from the HelmRelease's name and, when one is set, its target namespace
// ("<targetNamespace>-<name>"); writing the default makes the release name the
// component's with and without a target namespace. A config built directly
// with neither a Name nor a releaseName has nothing to derive a default from
// and is refused, as the helmtemplate terminal refuses it.
func (c *HelmReleaseConfig) releaseName() (string, error) {
	if c.Spec.ReleaseName != "" {
		return c.Spec.ReleaseName, nil
	}
	return defaultHelmReleaseName("helmrelease", c.Name)
}

// helmReleaseValuesFromKinds are the kinds Flux's ValuesReference admits (its
// CRD enum on kind).
var helmReleaseValuesFromKinds = []string{"Secret", "ConfigMap"}

// The ValuesReference CRD's limits and patterns on name, valuesKey and
// targetPath (github.com/fluxcd/pkg/apis/meta). The patterns are copied
// verbatim.
const (
	helmReleaseValuesNameMax       = 253
	helmReleaseValuesKeyMax        = 253
	helmReleaseValuesTargetPathMax = 250
)

var (
	helmReleaseValuesKeyPattern        = regexp.MustCompile(`^[\-._a-zA-Z0-9]+$`)
	helmReleaseValuesTargetPathPattern = regexp.MustCompile(`^([a-zA-Z0-9_\-.\\\/]|\[[0-9]{1,5}\])+$`)
)

// checkHelmReleaseValuesRef checks one spec.valuesFrom entry against the
// ValuesReference constraints of Flux's CRD, so a bad entry fails the build
// instead of the apply. Lengths count characters, as the CRD's maxLength
// does. The error starts with the field name; validate prefixes the entry's
// index.
func checkHelmReleaseValuesRef(ref *helmv2.ValuesReference) error {
	kinds := strings.Join(helmReleaseValuesFromKinds, ", ")
	if ref.Kind == "" {
		return errors.Errorf("kind is required: one of %s", kinds)
	}
	if !slices.Contains(helmReleaseValuesFromKinds, ref.Kind) {
		return errors.Errorf("kind %q is not one of %s", ref.Kind, kinds)
	}
	if ref.Name == "" {
		return errors.New("name is required")
	}
	if n := utf8.RuneCountInString(ref.Name); n > helmReleaseValuesNameMax {
		return errors.Errorf("name is %d characters, more than %d", n, helmReleaseValuesNameMax)
	}
	if err := checkHelmReleaseValuesPattern("valuesKey", ref.ValuesKey, helmReleaseValuesKeyMax, helmReleaseValuesKeyPattern); err != nil {
		return err
	}
	return checkHelmReleaseValuesPattern("targetPath", ref.TargetPath, helmReleaseValuesTargetPathMax, helmReleaseValuesTargetPathPattern)
}

// checkHelmReleaseValuesPattern checks an optional ValuesReference field: unset
// is fine, a set value is at most limit characters and matches pattern.
func checkHelmReleaseValuesPattern(field, value string, limit int, pattern *regexp.Regexp) error {
	if value == "" {
		return nil
	}
	if n := utf8.RuneCountInString(value); n > limit {
		return errors.Errorf("%s is %d characters, more than %d", field, n, limit)
	}
	if !pattern.MatchString(value) {
		return errors.Errorf("%s %q does not match %s", field, value, pattern)
	}
	return nil
}

// helmReleaseValuesMap parses spec.values into a map. Absent values yield a
// nil map. Numbers stay exact (json.Number), so re-serializing does not round
// them.
func helmReleaseValuesMap(v *apiextensionsv1.JSON) (map[string]any, error) {
	if v == nil || len(v.Raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(v.Raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, errors.Errorf("helmrelease: values must be a JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("helmrelease: values must be a single JSON object")
	}
	return m, nil
}

// Generate emits the HelmRelease, in the Flux namespace when one is set, else
// in the application namespace.
func (c *HelmReleaseConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}

	hr := fluxcd.CreateHelmRelease(kindObjectName(c.ObjectName, c.Name), c.fluxNamespace())
	// A deep copy, so neither this render nor a later one shares a pointer or
	// slice with the config.
	hr.Spec = *c.Spec.DeepCopy()
	if hr.Spec.Interval.Duration == 0 {
		hr.Spec.Interval = metav1.Duration{Duration: helmReleaseDefaultInterval}
	}
	// validate has already accepted it.
	hr.Spec.ReleaseName, _ = c.releaseName()
	if hr.Spec.Chart != nil && hr.Spec.Chart.Spec.Verify != nil {
		fillFluxVerifyProvider(&hr.Spec.Chart.Spec.Verify.Provider)
	}
	// Under a Flux namespace the HelmRelease no longer sits in the
	// application namespace, and Flux installs a release into the
	// HelmRelease's own namespace unless targetNamespace says otherwise. So
	// the application namespace becomes the target unless one is authored.
	// helm-controller likewise keeps the release's Helm storage (the
	// sh.helm.release.v1.* Secrets, with the rendered manifests and the
	// merged values) in the HelmRelease's own namespace unless
	// storageNamespace says otherwise, so an unauthored storageNamespace
	// follows the effective target, authored or defaulted
	// (go-kure/launcher#988).
	if c.fluxNS != "" {
		if hr.Spec.TargetNamespace == "" {
			hr.Spec.TargetNamespace = c.Namespace
		}
		if hr.Spec.StorageNamespace == "" {
			hr.Spec.StorageNamespace = hr.Spec.TargetNamespace
		}
	}

	return kindObject(hr, c.Metadata)
}
