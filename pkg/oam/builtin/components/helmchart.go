package components

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// HelmChartHandler handles the kind-named `helmchart` component: a 1:1
// projection of Flux's HelmChartSpec (go-kure/launcher#351). It emits one
// HelmChart named after the component. See fluxsource.go for what the source
// components share.
//
// The type name was a role-level composite until go-kure/launcher#350 removed
// it; a document written for that composite uses `helm` instead.
type HelmChartHandler struct{}

// helmChartDurations are HelmChartSpec's duration fields. It has no timeout.
var helmChartDurations = []fluxDurationField[sourcev1.HelmChartSpec]{
	{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *sourcev1.HelmChartSpec) *metav1.Duration { return &s.Interval }},
}

// helmChartCompositeKeys are the top-level keys of the removed helmchart
// composite that HelmChartSpec does not declare. Only these get the pointer to
// `helm`: an unknown key that is not one of them is a plain typo.
var helmChartCompositeKeys = map[string]bool{
	"delivery":        true,
	"releaseName":     true,
	"targetNamespace": true,
	"source":          true,
	"values":          true,
	"valuesMode":      true,
	"driftDetection":  true,
	"install":         true,
	"upgrade":         true,
	"valuesFrom":      true,
}

// CanHandle returns true for the helmchart component type.
func (h *HelmChartHandler) CanHandle(componentType string) bool {
	return componentType == "helmchart"
}

// PropertySchema declares every top-level key of sourcev1.HelmChartSpec. A test
// ties this key set to the struct.
func (h *HelmChartHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"chart":   fluxSourceRequiredString("HelmChart spec.chart: the chart's name, or its path in a GitRepository or Bucket source."),
		"version": fluxSourceString("HelmChart spec.version: a semver expression for the chart version, Flux's default * (latest) when unset. Ignored for a chart from a GitRepository or Bucket source."),
		"sourceRef": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "HelmChart spec.sourceRef: the HelmRepository, GitRepository or Bucket (kind, name, optional apiVersion) the chart is fetched from, in the namespace the HelmChart lands in. kind and name are required.",
		},
		"interval":          fluxSourceString("HelmChart spec.interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms. Defaults to 60m when unset or zero."),
		"reconcileStrategy": fluxSourceString("HelmChart spec.reconcileStrategy: ChartVersion (Flux's default) or Revision."),
		"valuesFiles": {
			Type: oam.PropertyTypeArray, Description: "HelmChart spec.valuesFiles: values files in the source, merged in order, replacing the chart's values.yaml.",
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One values file path, relative to the source."},
		},
		"ignoreMissingValuesFiles": fluxSourceBool("HelmChart spec.ignoreMissingValuesFiles: skip a missing values file instead of failing."),
		"suspend":                  fluxSourceBool("HelmChart spec.suspend: stop reconciling the chart."),
		"verify":                   fluxSourceObject("HelmChart spec.verify: signature verification of an OCI chart (provider, secretRef, matchOIDCIdentity). Flux accepts it only with a HelmRepository source."),
	}
}

// UnsupportedFieldHint returns the pointer to `helm` for a key of the removed
// helmchart composite, and "" for any other key. pkg/oam appends it to the
// unsupported-field error of an authored helmchart component.
func (h *HelmChartHandler) UnsupportedFieldHint(key string) string {
	if !helmChartCompositeKeys[key] {
		return ""
	}
	return `helmchart is now the Flux HelmChart source (go-kure/launcher#351); a document written for the removed helmchart composite uses "helm" (go-kure/launcher#350; migration table in pkg/oam/builtin/components/README.md)`
}

// ToApplicationConfig decodes the component's properties strictly into a
// sourcev1.HelmChartSpec: any key HelmChartSpec does not declare, at any depth,
// and any wrongly typed value is an error. Checks: chart, sourceRef.kind and
// sourceRef.name are set, and verify.provider is not an authored "".
func (h *HelmChartHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[sourcev1.HelmChartSpec](component.Properties)
	if err != nil {
		return nil, errors.Errorf("helmchart: properties do not decode as a HelmChartSpec: %w%s", err, h.decodeHint(component.Properties, err))
	}
	if err := checkAuthoredFluxDurations("helmchart", component.Properties, helmChartDurations); err != nil {
		return nil, err
	}
	if err := refuseEmptyFluxVerifyProvider(component.Properties, "verify"); err != nil {
		return nil, err
	}
	cfg := &HelmChartConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// decodeHint is UnsupportedFieldHint for a decode error, which names the
// unknown key but not its depth: the hint is given when that key is a
// top-level key of props and one of the composite's. It is "" or starts with
// "; ".
func (h *HelmChartHandler) decodeHint(props map[string]any, err error) string {
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if !strings.Contains(err.Error(), "unknown field "+strconv.Quote(k)) {
			continue
		}
		if hint := h.UnsupportedFieldHint(k); hint != "" {
			return "; " + hint
		}
	}
	return ""
}

// HelmChartConfig implements stack.ApplicationConfig for helmchart components.
type HelmChartConfig struct {
	// Name is the component name, and the HelmChart's name unless ObjectName
	// names it.
	Name string
	// ObjectName names the HelmChart (oam.Component.ObjectName). Empty for
	// Name.
	ObjectName string
	// Metadata is the labels and annotations authored for the HelmChart
	// (oam.Component.ObjectMetadata).
	Metadata oam.ObjectMetadata
	// Namespace is the application namespace. The HelmChart lands here unless
	// a Flux namespace is set (SetFluxNamespace).
	Namespace string
	// Spec is the HelmChart spec as authored. Generate copies it and applies
	// the interval default and the verify.provider default to the copy.
	Spec sourcev1.HelmChartSpec

	// fluxNS overrides the HelmChart's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace.
	fluxNS string
}

// validate holds the checks shared by the parse path and Generate, which
// repeats them for a config built directly by a library caller. sourceRef.kind
// and reconcileStrategy are enums the CRD checks.
func (c *HelmChartConfig) validate() error {
	if err := checkFluxDurations("helmchart", &c.Spec, helmChartDurations); err != nil {
		return err
	}
	if c.Spec.Chart == "" {
		return errors.New("helmchart: chart is required")
	}
	if c.Spec.SourceRef.Kind == "" {
		return errors.New("helmchart: sourceRef.kind is required")
	}
	if c.Spec.SourceRef.Name == "" {
		return errors.New("helmchart: sourceRef.name is required")
	}
	return nil
}

// ApplyPolicy checks nothing. A HelmChart names its source and fetches from
// no host of its own; the host is the source's, checked where that source is
// authored, as for a helmrelease's chart.spec.sourceRef.
func (c *HelmChartConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// SetFluxNamespace moves the HelmChart to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *HelmChartConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// Generate emits the HelmChart.
func (c *HelmChartConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	hc := fluxcd.CreateHelmChart(kindObjectName(c.ObjectName, c.Name), fluxSourceNamespace(c.Namespace, c.fluxNS))
	// A deep copy, so no render shares a pointer or slice with the config.
	hc.Spec = *c.Spec.DeepCopy()
	defaultFluxSourceInterval(&hc.Spec.Interval)
	if hc.Spec.Verify != nil {
		fillFluxVerifyProvider(&hc.Spec.Verify.Provider)
	}
	return emitFluxSource("helmchart", hc, nil, c.Metadata)
}
