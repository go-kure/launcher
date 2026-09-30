package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// helmReleaseValuesModeKey is the one property the helmrelease component owns
// on top of HelmReleaseSpec's own keys. It is split off before the strict
// decode, so it never reaches the spec.
const helmReleaseValuesModeKey = "valuesMode"

// helmReleaseValuesKey is the data key the generated values ConfigMap stores
// the serialized values under, and the valuesKey its valuesFrom entry names.
// The stored bytes are JSON (a YAML subset, which is how Flux reads a
// valuesFrom value), so the key says what they are.
const helmReleaseValuesKey = "values.json"

// helmReleaseDefaultInterval is spec.interval when the component leaves it
// unset. Flux requires the field; 60m matches the helmchart composite.
const helmReleaseDefaultInterval = 60 * time.Minute

// helmReleaseValuesHashLen is how many hex digits of the values digest the
// generated ConfigMap's name carries.
const helmReleaseValuesHashLen = 10

// HelmReleaseHandler handles the kind-named `helmrelease` component: a 1:1
// projection of Flux's HelmReleaseSpec, plus the launcher-owned valuesMode.
// It emits one HelmRelease and, under valuesMode: configMap with non-empty
// values, the ConfigMap those values are moved into. It creates no source:
// spec.chart or spec.chartRef names an existing one.
type HelmReleaseHandler struct{}

// CanHandle returns true for the helmrelease component type.
func (h *HelmReleaseHandler) CanHandle(componentType string) bool {
	return componentType == "helmrelease"
}

// PropertySchema declares every top-level key of helmv2.HelmReleaseSpec plus
// valuesMode, so authored-property validation admits exactly those keys. The
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
		"interval":           str("HelmRelease spec.interval as a duration (e.g. 10m, 1h30m); defaults to 60m when unset."),
		"kubeConfig":         object("HelmRelease spec.kubeConfig: a kubeconfig reference for a remote cluster."),
		"suspend":            boolean("HelmRelease spec.suspend: stop reconciling the release. true also suppresses the component's auto health check."),
		"releaseName":        str("HelmRelease spec.releaseName. Flux's default applies when unset: <targetNamespace>-<name> when targetNamespace is set, else the component name."),
		"targetNamespace":    str("HelmRelease spec.targetNamespace. When unset and a Flux namespace is configured, it is set to the application namespace."),
		"storageNamespace":   str("HelmRelease spec.storageNamespace: where Helm stores release state."),
		"dependsOn":          objects("HelmRelease spec.dependsOn: releases that must be ready first.", "One dependency reference (name, namespace, readyExpr)."),
		"timeout":            str("HelmRelease spec.timeout for Helm actions, as a duration."),
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
		"valuesMode":         {Type: oam.PropertyTypeString, Default: "inline", Enum: []any{"inline", "configMap"}, Description: "How values reach the HelmRelease: inline keeps them in spec.values; configMap moves non-empty values into a generated ConfigMap, referenced by a valuesFrom entry placed before the authored ones."},
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// helmv2.HelmReleaseSpec. Any key HelmReleaseSpec does not declare, at any
// depth, and any wrongly typed value is an error. Checks: exactly one of chart
// and chartRef, values a JSON object, valuesMode inline or configMap. Every
// other constraint is left to Flux's own CRD admission.
func (h *HelmReleaseHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, owned, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](component.Properties, helmReleaseValuesModeKey)
	if err != nil {
		return nil, errors.Errorf("helmrelease: properties do not decode as a HelmReleaseSpec: %w", err)
	}
	mode, err := helmReleaseValuesMode(owned)
	if err != nil {
		return nil, err
	}
	cfg := &HelmReleaseConfig{
		Name:       component.Name,
		Namespace:  namespace,
		Spec:       *spec,
		ValuesMode: mode,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// helmReleaseValuesMode reads the owned valuesMode key: absent or null means
// inline, and a present value must be the string inline or configMap. The
// owned split matches keys case-insensitively, so a map with two spellings of
// the key is refused rather than one silently winning.
func helmReleaseValuesMode(owned map[string]any) (string, error) {
	if len(owned) > 1 {
		return "", errors.Errorf("helmrelease: %s is given more than once", helmReleaseValuesModeKey)
	}
	for _, v := range owned {
		if v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", errors.Errorf("helmrelease: %s: must be a string, got %T", helmReleaseValuesModeKey, v)
		}
		return s, nil
	}
	return "", nil
}

// HelmReleaseConfig implements stack.ApplicationConfig for helmrelease
// components.
type HelmReleaseConfig struct {
	// Name is the component name, and the HelmRelease's name.
	Name string
	// Namespace is the application namespace. The HelmRelease lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string

	// Spec is the HelmRelease spec as authored. Generate copies it; it
	// applies the interval default, the targetNamespace default under a
	// Flux namespace, and the valuesMode: configMap rewrite to that copy.
	Spec helmv2.HelmReleaseSpec

	// ValuesMode is "inline" or "configMap"; empty means inline.
	ValuesMode string

	// fluxNS overrides the namespace of the HelmRelease and of its values
	// ConfigMap. Set by postProcessFluxNamespace via
	// TransformContext.FluxNamespace. Empty means Namespace.
	fluxNS string
}

// ApplyPolicy is a no-op: a HelmRelease has no resource-limit policy.
func (c *HelmReleaseConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// SetFluxNamespace moves the HelmRelease, and its values ConfigMap, to ns.
// Satisfies pkg/oam.fluxNamespaceSettable.
func (c *HelmReleaseConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// EmitsAutoHealthCheck vetoes the auto health check when the document sets
// `suspend: true`, the same shape as deployment's veto for `paused: true` and
// job's for `suspend: true`. HelmReleaseSpec.Suspend tells helm-controller to
// suspend reconciliation of the release, and the Ready condition the check
// reads is written by a reconciliation, so while the document keeps the
// release suspended that condition cannot report on it — a newly created
// suspended release never acquires one. Waiting on it is not a health signal:
// either it blocks the enclosing Kustomization until it times out, on a state
// the document asked for, or it passes without observing anything. Nothing
// here claims which of the two a given controller version does; the reason
// stands either way. The HelmRelease is still emitted and applied; only the
// readiness gate on it is skipped, so `suspend: true` stays a usable way to
// stage a release. Satisfies pkg/oam.autoHealthCheckEmitter.
func (c *HelmReleaseConfig) EmitsAutoHealthCheck() bool {
	return !c.Spec.Suspend
}

// fluxNamespace returns the namespace the HelmRelease and its values
// ConfigMap land in.
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
	switch c.ValuesMode {
	case "", "inline", "configMap":
	default:
		return errors.Errorf("helmrelease: unsupported %s %q; supported values: inline, configMap", helmReleaseValuesModeKey, c.ValuesMode)
	}
	if (c.Spec.Chart == nil) == (c.Spec.ChartRef == nil) {
		return errors.New("helmrelease: exactly one of chart and chartRef is required")
	}
	if _, err := helmReleaseValuesMap(c.Spec.Values); err != nil {
		return err
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

// Generate emits the HelmRelease and, under valuesMode: configMap with
// non-empty values, the ConfigMap those values are moved into. Both land in
// the Flux namespace when one is set, else in the application namespace.
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

	var objects []*client.Object
	if c.ValuesMode == "configMap" {
		cm, err := c.valuesConfigMap(hr)
		if err != nil {
			return nil, err
		}
		if cm != nil {
			obj := client.Object(cm)
			objects = append(objects, &obj)
		}
	}
	obj := client.Object(hr)
	objects = append(objects, &obj)
	return objects, nil
}

// valuesConfigMap moves hr's values into a ConfigMap in hr's namespace. It
// clears spec.values and places a valuesFrom entry for the ConfigMap ahead of
// the authored entries, so an authored entry still wins on a shared key, as
// under the helmchart composite. Empty or absent values generate nothing and
// return nil.
//
// The values are serialized once. Those exact bytes are stored in the
// ConfigMap and hashed into its name, so the name changes whenever the
// content does: the HelmRelease's spec changes with it, which makes Flux
// reconcile a values-only edit, and two components with identical values
// carry the same hash.
func (c *HelmReleaseConfig) valuesConfigMap(hr *helmv2.HelmRelease) (*corev1.ConfigMap, error) {
	values, err := helmReleaseValuesMap(hr.Spec.Values)
	if err != nil {
		return nil, err
	}
	hr.Spec.Values = nil
	if len(values) == 0 {
		return nil, nil
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, errors.Errorf("helmrelease %q: values is not representable as JSON: %w", c.Name, err)
	}
	sum := sha256.Sum256(data)
	name := helmReleaseValuesConfigMapName(c.Name, hex.EncodeToString(sum[:]))

	cm := kubernetes.CreateConfigMap(name, hr.Namespace)
	// The label is the component's label value (appLabels: the component
	// name, projected when it exceeds 63 characters, go-kure/launcher#572),
	// as on the helmchart composite's values ConfigMap. A component name can
	// be a 253-byte DNS-1123 subdomain, so the raw name would not always be a
	// legal label value.
	cm.Labels = appLabels(c.Name)
	kubernetes.AddConfigMapData(cm, helmReleaseValuesKey, string(data))

	hr.Spec.ValuesFrom = append([]helmv2.ValuesReference{{
		Kind:      "ConfigMap",
		Name:      name,
		ValuesKey: helmReleaseValuesKey,
	}}, hr.Spec.ValuesFrom...)
	return cm, nil
}

// helmReleaseValuesConfigMapName names the values ConfigMap of component name
// whose serialized values have the hex digest valuesDigest: the composite's
// valuesConfigMapName scheme (boundedResourceName) with the suffix
// "-values-<first 10 digest digits>". The suffix survives truncation, so the
// name always carries the values hash and is always a legal DNS-1123
// subdomain within 253 bytes.
func helmReleaseValuesConfigMapName(name, valuesDigest string) string {
	return boundedResourceName(name, "-values-"+valuesDigest[:helmReleaseValuesHashLen])
}
