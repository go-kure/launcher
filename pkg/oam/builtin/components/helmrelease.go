package components

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
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
// creates no source: spec.chart or spec.chartRef names an existing one. Values
// in a ConfigMap are the `helm` component's valuesMode: configMap, which
// lowers to a helmrelease plus a configmap trait (go-kure/launcher#702).
type HelmReleaseHandler struct{}

// helmReleaseDurations are HelmReleaseSpec's duration fields, nested ones
// included. All take the same form.
var helmReleaseDurations = []fluxDurationField[helmv2.HelmReleaseSpec]{
	{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration { return &s.Interval }},
	{path: []string{"timeout"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration { return s.Timeout }},
	{path: []string{"chart", "spec", "interval"}, form: fluxduration.Interval, get: func(s *helmv2.HelmReleaseSpec) *metav1.Duration {
		if s.Chart == nil {
			return nil
		}
		return s.Chart.Spec.Interval
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
		"chart":              object("HelmRelease spec.chart: a chart template naming a chart in an existing HelmRepository, GitRepository or Bucket source. Exactly one of chart and chartRef is required."),
		"chartRef":           object("HelmRelease spec.chartRef: a reference to an existing OCIRepository, ExternalArtifact or HelmChart source. Exactly one of chart and chartRef is required."),
		"interval":           str("HelmRelease spec.interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms. Defaults to 60m when unset or zero."),
		"kubeConfig":         object("HelmRelease spec.kubeConfig: a kubeconfig reference for a remote cluster."),
		"suspend":            boolean("HelmRelease spec.suspend: stop reconciling the release."),
		"releaseName":        str("HelmRelease spec.releaseName. Flux's default applies when unset: <targetNamespace>-<name> when targetNamespace is set, else the component name."),
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
// and chartRef, values a JSON object, each valuesFrom entry within the
// ValuesReference CRD's constraints. Every other constraint is left to Flux's
// own CRD admission.
func (h *HelmReleaseHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](component.Properties)
	if err != nil {
		hint := ""
		if strings.Contains(err.Error(), `unknown field "valuesMode"`) {
			hint = "; " + helmReleaseValuesModeHint
		}
		return nil, errors.Errorf("helmrelease: properties do not decode as a HelmReleaseSpec: %w%s", err, hint)
	}
	if err := checkAuthoredFluxDurations("helmrelease", component.Properties, helmReleaseDurations); err != nil {
		return nil, err
	}
	cfg := &HelmReleaseConfig{
		Name:      component.Name,
		Namespace: namespace,
		Spec:      *spec,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// HelmReleaseConfig implements stack.ApplicationConfig for helmrelease
// components.
type HelmReleaseConfig struct {
	// Name is the component name, and the HelmRelease's name.
	Name string
	// Namespace is the application namespace. The HelmRelease lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string

	// Spec is the HelmRelease spec as authored. Generate copies it and
	// applies the interval default and the targetNamespace default under a
	// Flux namespace to that copy.
	Spec helmv2.HelmReleaseSpec

	// fluxNS overrides the HelmRelease's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace. Empty
	// means Namespace.
	fluxNS string
}

// ApplyPolicy is a no-op: a HelmRelease has no resource-limit policy.
func (c *HelmReleaseConfig) ApplyPolicy(_ oam.Policy) error { return nil }

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
	if err := checkFluxDurations("helmrelease", &c.Spec, helmReleaseDurations); err != nil {
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

	hr := fluxcd.CreateHelmRelease(c.Name, c.fluxNamespace())
	// A deep copy, so neither this render nor a later one shares a pointer or
	// slice with the config.
	hr.Spec = *c.Spec.DeepCopy()
	if hr.Spec.Interval.Duration == 0 {
		hr.Spec.Interval = metav1.Duration{Duration: helmReleaseDefaultInterval}
	}
	// Under a Flux namespace the HelmRelease no longer sits in the
	// application namespace, and Flux installs a release into the
	// HelmRelease's own namespace unless targetNamespace says otherwise. So
	// the application namespace becomes the target unless one is authored.
	if c.fluxNS != "" && hr.Spec.TargetNamespace == "" {
		hr.Spec.TargetNamespace = c.Namespace
	}

	obj := client.Object(hr)
	return []*client.Object{&obj}, nil
}
