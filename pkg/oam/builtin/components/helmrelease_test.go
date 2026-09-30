package components_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// hrChart is the smallest valid chart block: a chart in an existing HelmRepository.
func hrChart() map[string]any {
	return map[string]any{"spec": map[string]any{
		"chart":     "podinfo",
		"sourceRef": map[string]any{"kind": "HelmRepository", "name": "podinfo"},
	}}
}

func hrConfig(t *testing.T, name string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(
		&oam.Component{Name: name, Type: "helmrelease", Properties: props}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

// hrGenerate renders cfg, under fluxNS when non-empty, and returns its
// HelmRelease and its values ConfigMap (nil when none is emitted).
func hrGenerate(t *testing.T, cfg stack.ApplicationConfig, fluxNS string) (*helmv2.HelmRelease, *corev1.ConfigMap) {
	t.Helper()
	if fluxNS != "" {
		cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
	}
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var hr *helmv2.HelmRelease
	var cm *corev1.ConfigMap
	for _, o := range objs {
		switch v := (*o).(type) {
		case *helmv2.HelmRelease:
			if hr != nil {
				t.Fatal("more than one HelmRelease emitted")
			}
			hr = v
		case *corev1.ConfigMap:
			if cm != nil {
				t.Fatal("more than one ConfigMap emitted")
			}
			cm = v
		default:
			t.Fatalf("unexpected object %T", v)
		}
	}
	if hr == nil {
		t.Fatal("no HelmRelease emitted")
	}
	return hr, cm
}

func TestHelmReleaseHandler_CanHandle(t *testing.T) {
	h := &components.HelmReleaseHandler{}
	if !h.CanHandle("helmrelease") || h.CanHandle("helmchart") {
		t.Error("CanHandle must accept helmrelease only")
	}
}

// TestHelmReleaseHandler_SchemaMatchesSpec ties the published schema to the
// struct: exactly HelmReleaseSpec's top-level JSON keys plus valuesMode, each
// with the property type its Go field encodes as. An upstream field added or
// removed on a helm-controller bump turns this red.
func TestHelmReleaseHandler_SchemaMatchesSpec(t *testing.T) {
	schema := (&components.HelmReleaseHandler{}).PropertySchema()
	want := map[string]oam.PropertyType{"valuesMode": oam.PropertyTypeString}
	durationType := reflect.TypeFor[metav1.Duration]()
	for f := range reflect.TypeFor[helmv2.HelmReleaseSpec]().Fields() {
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		var pt oam.PropertyType
		switch {
		case ft == durationType, ft.Kind() == reflect.String:
			pt = oam.PropertyTypeString
		case ft.Kind() == reflect.Bool:
			pt = oam.PropertyTypeBoolean
		case ft.Kind() == reflect.Int:
			pt = oam.PropertyTypeInteger
		case ft.Kind() == reflect.Slice:
			pt = oam.PropertyTypeArray
		case ft.Kind() == reflect.Struct:
			pt = oam.PropertyTypeObject
		default:
			t.Fatalf("field %s: unmapped kind %s", f.Name, ft.Kind())
		}
		want[key] = pt
	}
	got := map[string]oam.PropertyType{}
	for k, s := range schema {
		got[k] = s.Type
		if s.Description == "" {
			t.Errorf("property %q has no description", k)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema keys/types = %v\nwant %v", got, want)
	}
}

// TestHelmReleaseHandler_EveryFieldReachable: no HelmReleaseSpec field is
// shadowed by the owned valuesMode key or otherwise unreachable through the
// strict decode. The exclusion list is explicit and must stay empty.
func TestHelmReleaseHandler_EveryFieldReachable(t *testing.T) {
	excluded := []string{}
	got := builtin.UnreachableJSONFields(reflect.TypeFor[helmv2.HelmReleaseSpec](), "valuesMode")
	if !slices.Equal(got, excluded) {
		t.Errorf("unreachable HelmReleaseSpec fields: %v, want %v", got, excluded)
	}
}

// fullHelmReleaseProps sets every top-level HelmReleaseSpec key except
// chartRef (exclusive with chart), with durations in the canonical form
// metav1.Duration re-encodes to, so the emitted spec can be compared with the
// authored map key for key.
const fullHelmReleaseProps = `
chart:
  spec:
    chart: podinfo
    version: 6.x
    sourceRef: {kind: HelmRepository, name: podinfo, namespace: sources}
interval: 10m0s
kubeConfig:
  secretRef: {name: remote, key: value}
suspend: true
releaseName: web
targetNamespace: web-ns
storageNamespace: helm-state
dependsOn:
  - {name: db, namespace: data}
timeout: 5m0s
maxHistory: 3
serviceAccountName: deployer
persistentClient: false
driftDetection:
  mode: warn
install:
  crds: Create
  remediation: {retries: 2}
upgrade:
  crds: CreateReplace
  cleanupOnFail: true
test:
  enable: true
rollback:
  recreate: true
uninstall:
  keepHistory: true
valuesFrom:
  - {kind: Secret, name: creds, valuesKey: v.yaml, targetPath: auth.token, optional: true}
values:
  replicaCount: 2
  big: 9007199254740993
  image: {tag: "1.2"}
commonMetadata:
  labels: {team: web}
postRenderers:
  - kustomize:
      patches:
        - patch: '[{"op":"add","path":"/metadata/labels/x","value":"y"}]'
          target: {kind: Deployment}
postRenderStrategy: combined
waitStrategy:
  name: poller
healthCheckExprs:
  - {apiVersion: example.com/v1, kind: Thing, current: status.ready == true}
`

func yamlProps(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	return m
}

func jsonShape(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestHelmReleaseHandler_ProjectsEveryField: an authored map carrying every
// top-level key comes out as spec unchanged, key for key, big integers exact.
func TestHelmReleaseHandler_ProjectsEveryField(t *testing.T) {
	props := yamlProps(t, fullHelmReleaseProps)
	hr, cm := hrGenerate(t, hrConfig(t, "web", props), "")
	if cm != nil {
		t.Fatal("inline mode emitted a ConfigMap")
	}
	if got, want := jsonShape(t, hr.Spec), jsonShape(t, props); !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("emitted spec differs from authored properties\ngot:  %s\nwant: %s", gj, wj)
	}
	var authoredKeys []string
	for k := range props {
		authoredKeys = append(authoredKeys, k)
	}
	if len(authoredKeys) != len((&components.HelmReleaseHandler{}).PropertySchema())-2 { // minus chartRef and valuesMode
		t.Errorf("fixture sets %d keys; it must set every spec key but chartRef", len(authoredKeys))
	}
}

func TestHelmReleaseHandler_Identity(t *testing.T) {
	hr, _ := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "")
	if hr.Name != "web" || hr.Namespace != "demo" {
		t.Errorf("HelmRelease %s/%s, want demo/web", hr.Namespace, hr.Name)
	}
	if hr.APIVersion != "helm.toolkit.fluxcd.io/v2" || hr.Kind != "HelmRelease" {
		t.Errorf("TypeMeta %s %s", hr.APIVersion, hr.Kind)
	}
	if hr.Spec.TargetNamespace != "" {
		t.Errorf("targetNamespace %q set without a Flux namespace", hr.Spec.TargetNamespace)
	}

	hr, _ = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "flux-system")
	if hr.Namespace != "flux-system" {
		t.Errorf("namespace %q under a Flux namespace, want flux-system", hr.Namespace)
	}
}

// TestHelmReleaseHandler_TargetNamespaceUnderFluxNamespace (C20): with a Flux
// namespace and no authored targetNamespace, the release still installs into
// the application namespace; an authored value wins.
func TestHelmReleaseHandler_TargetNamespaceUnderFluxNamespace(t *testing.T) {
	hr, _ := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "flux-system")
	if hr.Spec.TargetNamespace != "demo" {
		t.Errorf("targetNamespace = %q, want the application namespace demo", hr.Spec.TargetNamespace)
	}
	if got := hr.GetReleaseName(); got != "demo-web" {
		t.Errorf("Flux default release name = %q, want demo-web", got)
	}

	hr, _ = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "targetNamespace": "elsewhere"}), "flux-system")
	if hr.Spec.TargetNamespace != "elsewhere" {
		t.Errorf("authored targetNamespace lost: %q", hr.Spec.TargetNamespace)
	}
}

func TestHelmReleaseHandler_IntervalDefault(t *testing.T) {
	hr, _ := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "")
	if hr.Spec.Interval.Duration != 60*time.Minute {
		t.Errorf("default interval = %v, want 60m", hr.Spec.Interval.Duration)
	}
	hr, _ = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "interval": "10m"}), "")
	if hr.Spec.Interval.Duration != 10*time.Minute {
		t.Errorf("authored interval = %v, want 10m", hr.Spec.Interval.Duration)
	}
}

func TestHelmReleaseHandler_ChartRef(t *testing.T) {
	ref := map[string]any{"kind": "OCIRepository", "name": "podinfo"}
	hr, _ := hrGenerate(t, hrConfig(t, "web", map[string]any{"chartRef": ref}), "")
	if hr.Spec.ChartRef == nil || hr.Spec.ChartRef.Kind != "OCIRepository" || hr.Spec.ChartRef.Name != "podinfo" || hr.Spec.Chart != nil {
		t.Errorf("chartRef not projected: %+v / %+v", hr.Spec.ChartRef, hr.Spec.Chart)
	}
}

func TestHelmReleaseHandler_Refuses(t *testing.T) {
	ref := map[string]any{"kind": "OCIRepository", "name": "podinfo"}
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"neither chart nor chartRef", map[string]any{}, "exactly one of chart and chartRef"},
		{"both chart and chartRef", map[string]any{"chart": hrChart(), "chartRef": ref}, "exactly one of chart and chartRef"},
		{"unknown top-level key", map[string]any{"chart": hrChart(), "chartt": "x"}, `unknown field "chartt"`},
		{"unknown nested key", map[string]any{"chart": map[string]any{"spec": map[string]any{"chart": "p", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "p"}, "chartVersion": "1"}}}, `unknown field "chartVersion"`},
		{"unknown key in valuesFrom item", map[string]any{"chart": hrChart(), "valuesFrom": []any{map[string]any{"kind": "Secret", "name": "s", "key": "x"}}}, `unknown field "key"`},
		{"wrong type bool", map[string]any{"chart": hrChart(), "suspend": "yes"}, "suspend"},
		{"wrong type integer", map[string]any{"chart": hrChart(), "maxHistory": "3"}, "maxHistory"},
		{"wrong type object", map[string]any{"chart": "podinfo"}, "chart"},
		{"invalid duration", map[string]any{"chart": hrChart(), "interval": "5minutes"}, "5minutes"},
		{"values not an object", map[string]any{"chart": hrChart(), "values": []any{1}}, "values must be a JSON object"},
		{"NaN in values", map[string]any{"chart": hrChart(), "values": map[string]any{"x": math.NaN()}}, "NaN"},
		{"+Inf in values", map[string]any{"chart": hrChart(), "values": map[string]any{"x": map[string]any{"y": math.Inf(1)}}}, "+Inf"},
		{"NaN in values under configMap", map[string]any{"chart": hrChart(), "valuesMode": "configMap", "values": map[string]any{"x": math.NaN()}}, "NaN"},
		{"unknown valuesMode", map[string]any{"chart": hrChart(), "valuesMode": "secret"}, `unsupported valuesMode "secret"`},
		{"wrongly typed valuesMode", map[string]any{"chart": hrChart(), "valuesMode": true}, "valuesMode: must be a string, got bool"},
		{"valuesMode twice", map[string]any{"chart": hrChart(), "valuesMode": "inline", "ValuesMode": "configMap"}, "valuesMode is given more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "helmrelease", Properties: tc.props}, "demo")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestHelmReleaseHandler_ValuesModeDefaultsToInline(t *testing.T) {
	for _, mode := range []any{nil, "inline"} {
		props := map[string]any{"chart": hrChart(), "values": map[string]any{"a": 1}, "valuesMode": mode}
		hr, cm := hrGenerate(t, hrConfig(t, "web", props), "")
		if cm != nil || hr.Spec.Values == nil || string(hr.Spec.Values.Raw) != `{"a":1}` || len(hr.Spec.ValuesFrom) != 0 {
			t.Errorf("valuesMode %v: want inline values, got cm=%v values=%v valuesFrom=%v", mode, cm, hr.Spec.Values, hr.Spec.ValuesFrom)
		}
	}
}

func configMapModeProps(values map[string]any) map[string]any {
	return map[string]any{
		"chart":      hrChart(),
		"valuesMode": "configMap",
		"values":     values,
		"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "creds"}, map[string]any{"kind": "ConfigMap", "name": "extra"}},
	}
}

// assertValuesConfigMap checks the invariants every generated values ConfigMap
// holds, and returns its name.
func assertValuesConfigMap(t *testing.T, hr *helmv2.HelmRelease, cm *corev1.ConfigMap) string {
	t.Helper()
	if cm == nil {
		t.Fatal("no values ConfigMap emitted")
	}
	if hr.Spec.Values != nil {
		t.Errorf("spec.values not cleared: %s", hr.Spec.Values.Raw)
	}
	if cm.Namespace != hr.Namespace {
		t.Errorf("ConfigMap in %q, HelmRelease in %q", cm.Namespace, hr.Namespace)
	}
	if cm.APIVersion != "v1" || cm.Kind != "ConfigMap" {
		t.Errorf("ConfigMap TypeMeta %q %q", cm.APIVersion, cm.Kind)
	}
	// The app label is written only when the component name is a legal label
	// value; a longer name leaves the ConfigMap unlabelled.
	var wantLabels map[string]string
	if len(validation.IsValidLabelValue(hr.Name)) == 0 {
		wantLabels = map[string]string{"app": hr.Name}
	}
	if !reflect.DeepEqual(cm.Labels, wantLabels) || cm.Annotations != nil {
		t.Errorf("ConfigMap metadata labels=%v annotations=%v, want labels=%v", cm.Labels, cm.Annotations, wantLabels)
	}
	if len(cm.Data) != 1 {
		t.Fatalf("ConfigMap data has %d keys, want 1", len(cm.Data))
	}
	var key, data string
	for k, v := range cm.Data {
		key, data = k, v
	}
	if len(hr.Spec.ValuesFrom) == 0 {
		t.Fatal("no valuesFrom entry")
	}
	ref := hr.Spec.ValuesFrom[0]
	if ref.Kind != "ConfigMap" || ref.Name != cm.Name || ref.ValuesKey != key {
		t.Errorf("valuesFrom[0] = %+v, want ConfigMap %s key %s", ref, cm.Name, key)
	}
	if errs := validation.IsDNS1123Subdomain(cm.Name); len(errs) != 0 {
		t.Errorf("ConfigMap name %q is not a DNS-1123 subdomain: %v", cm.Name, errs)
	}
	sum := sha256.Sum256([]byte(data))
	if !strings.HasSuffix(cm.Name, "-values-"+hex.EncodeToString(sum[:])[:10]) {
		t.Errorf("ConfigMap name %q does not end in the digest of its stored bytes", cm.Name)
	}
	return cm.Name
}

func TestHelmReleaseHandler_ConfigMapMode(t *testing.T) {
	values := map[string]any{"replicaCount": 2, "image": map[string]any{"tag": "1.2"}, "big": uint64(18446744073709551615)}
	hr, cm := hrGenerate(t, hrConfig(t, "web", configMapModeProps(values)), "")
	assertValuesConfigMap(t, hr, cm)

	// Authored entries keep their order, after the generated one.
	if len(hr.Spec.ValuesFrom) != 3 || hr.Spec.ValuesFrom[1].Name != "creds" || hr.Spec.ValuesFrom[2].Name != "extra" {
		t.Errorf("valuesFrom = %+v, want generated, creds, extra", hr.Spec.ValuesFrom)
	}
	// The stored bytes decode back to the authored values, numbers exact.
	var stored map[string]any
	dec := json.NewDecoder(strings.NewReader(cm.Data["values.json"]))
	dec.UseNumber()
	if err := dec.Decode(&stored); err != nil {
		t.Fatalf("stored values: %v", err)
	}
	if !reflect.DeepEqual(stored, jsonShape(t, values)) {
		t.Errorf("stored values %v, want %v", stored, values)
	}
}

// TestHelmReleaseHandler_ConfigMapNameTracksValues: the name and the
// valuesFrom entry move together when the values change, and stay equal.
func TestHelmReleaseHandler_ConfigMapNameTracksValues(t *testing.T) {
	hr1, cm1 := hrGenerate(t, hrConfig(t, "web", configMapModeProps(map[string]any{"a": 1})), "")
	hr2, cm2 := hrGenerate(t, hrConfig(t, "web", configMapModeProps(map[string]any{"a": 2})), "")
	n1, n2 := assertValuesConfigMap(t, hr1, cm1), assertValuesConfigMap(t, hr2, cm2)
	if n1 == n2 {
		t.Errorf("different values share ConfigMap name %q", n1)
	}
	if hr1.Spec.ValuesFrom[0].Name != n1 || hr2.Spec.ValuesFrom[0].Name != n2 {
		t.Error("valuesFrom entry did not move with the ConfigMap name")
	}
}

// TestHelmReleaseHandler_IdenticalValuesHashAlike: two components with the
// same values carry the same values hash; a reordered map, or the same JSON
// written with another key order and spacing, yields the identical name.
func TestHelmReleaseHandler_IdenticalValuesHashAlike(t *testing.T) {
	values := func() map[string]any { return map[string]any{"b": 2, "a": map[string]any{"y": true, "x": "s"}} }
	_, cmA := hrGenerate(t, hrConfig(t, "alpha", configMapModeProps(values())), "")
	_, cmB := hrGenerate(t, hrConfig(t, "beta", configMapModeProps(values())), "")
	suffix := func(name string) string { return name[strings.LastIndex(name, "-values-"):] }
	if suffix(cmA.Name) != suffix(cmB.Name) || cmA.Data["values.json"] != cmB.Data["values.json"] {
		t.Errorf("identical values hash differently: %q vs %q", cmA.Name, cmB.Name)
	}

	name := func(raw string) string {
		cfg := &components.HelmReleaseConfig{
			Name: "alpha", Namespace: "demo", ValuesMode: "configMap",
			Spec: helmv2.HelmReleaseSpec{
				ChartRef: &helmv2.CrossNamespaceSourceReference{Kind: "OCIRepository", Name: "p"},
				Values:   &apiextensionsv1.JSON{Raw: []byte(raw)},
			},
		}
		hr, cm := hrGenerate(t, cfg, "")
		return assertValuesConfigMap(t, hr, cm)
	}
	n1 := name(`{"a":{"x":"s","y":true},"b":2}`)
	n2 := name(`{ "b": 2, "a": { "y": true, "x": "s" } }`)
	if n1 != n2 || n1 != cmA.Name {
		t.Errorf("reordered values changed the name: %q, %q, %q", n1, n2, cmA.Name)
	}
}

// assertLegalMetadata checks that every object cfg emits has a legal
// DNS-1123 subdomain name and only legal label keys and values, the checks
// the API server applies on create.
func assertLegalMetadata(t *testing.T, cfg stack.ApplicationConfig) {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objs {
		obj := *o
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		if errs := validation.IsDNS1123Subdomain(obj.GetName()); len(errs) != 0 {
			t.Errorf("%s name %q is not a DNS-1123 subdomain: %v", kind, obj.GetName(), errs)
		}
		for k, v := range obj.GetLabels() {
			if errs := validation.IsQualifiedName(k); len(errs) != 0 {
				t.Errorf("%s label key %q is invalid: %v", kind, k, errs)
			}
			if errs := validation.IsValidLabelValue(v); len(errs) != 0 {
				t.Errorf("%s label %s value %q is invalid: %v", kind, k, v, errs)
			}
		}
	}
}

// TestHelmReleaseHandler_MaxLengthNameStaysLegal: a 253-byte component name,
// the longest validate.go admits, still yields a legal ConfigMap name that
// carries the values hash, the valuesFrom entry names it, and no emitted
// object carries an illegal label: the name is too long to be a label value,
// so the ConfigMap gets no app label.
func TestHelmReleaseHandler_MaxLengthNameStaysLegal(t *testing.T) {
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 70)
	if len(long) != 253 || len(validation.IsDNS1123Subdomain(long)) != 0 {
		t.Fatalf("fixture name is %d bytes or invalid", len(long))
	}
	cfg := hrConfig(t, long, configMapModeProps(map[string]any{"a": 1}))
	assertLegalMetadata(t, cfg)
	hr, cm := hrGenerate(t, cfg, "")
	n := assertValuesConfigMap(t, hr, cm)
	if len(n) > 253 {
		t.Errorf("ConfigMap name is %d bytes", len(n))
	}
	if cm.Labels != nil {
		t.Errorf("ConfigMap of a %d-byte name carries labels %v, want none", len(long), cm.Labels)
	}
	// Two long names sharing the kept prefix still differ.
	other := long[:252] + "e"
	hr2, cm2 := hrGenerate(t, hrConfig(t, other, configMapModeProps(map[string]any{"a": 1})), "")
	if n2 := assertValuesConfigMap(t, hr2, cm2); n2 == n {
		t.Errorf("distinct long names collided on %q", n)
	}
}

// TestHelmReleaseHandler_ConfigMapAppLabelBoundary: the ConfigMap's app label
// is the component name exactly when that name is a legal label value (at
// most 63 characters), and absent past that limit.
func TestHelmReleaseHandler_ConfigMapAppLabelBoundary(t *testing.T) {
	cases := []struct {
		name  string
		label bool
	}{
		{"web", true},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 30) + "." + strings.Repeat("b", 32), true},
		{strings.Repeat("a", 64), false},
	}
	for _, tc := range cases {
		cfg := hrConfig(t, tc.name, configMapModeProps(map[string]any{"a": 1}))
		assertLegalMetadata(t, cfg)
		_, cm := hrGenerate(t, cfg, "")
		got, ok := cm.Labels["app"]
		if ok != tc.label || (ok && got != tc.name) || len(cm.Labels) > 1 {
			t.Errorf("%d-byte name: ConfigMap labels %v, want app label %v", len(tc.name), cm.Labels, tc.label)
		}
	}
}

// TestHelmReleaseHandler_ConfigMapFollowsFluxNamespace: under a Flux
// namespace the ConfigMap lands with the HelmRelease, where Flux resolves
// valuesFrom.
func TestHelmReleaseHandler_ConfigMapFollowsFluxNamespace(t *testing.T) {
	hr, cm := hrGenerate(t, hrConfig(t, "web", configMapModeProps(map[string]any{"a": 1})), "flux-system")
	assertValuesConfigMap(t, hr, cm)
	if hr.Namespace != "flux-system" || cm.Namespace != "flux-system" {
		t.Errorf("HelmRelease in %q, ConfigMap in %q, want both in flux-system", hr.Namespace, cm.Namespace)
	}
}

func TestHelmReleaseHandler_EmptyValuesGenerateNoConfigMap(t *testing.T) {
	for _, values := range []map[string]any{nil, {}} {
		props := configMapModeProps(values)
		if values == nil {
			delete(props, "values")
		}
		hr, cm := hrGenerate(t, hrConfig(t, "web", props), "")
		if cm != nil {
			t.Errorf("values %v: ConfigMap emitted", values)
		}
		if hr.Spec.Values != nil || len(hr.Spec.ValuesFrom) != 2 || hr.Spec.ValuesFrom[0].Name != "creds" {
			t.Errorf("values %v: values=%v valuesFrom=%+v", values, hr.Spec.Values, hr.Spec.ValuesFrom)
		}
	}
}

// TestHelmReleaseHandler_GenerateDoesNotAlias: rendering twice gives equal,
// independent output, and editing one render leaves the config untouched.
func TestHelmReleaseHandler_GenerateDoesNotAlias(t *testing.T) {
	cfg := hrConfig(t, "web", configMapModeProps(map[string]any{"a": 1}))
	hr1, cm1 := hrGenerate(t, cfg, "")
	hr1.Spec.ValuesFrom[1].Name = "mutated"
	hr1.Spec.Chart.Spec.Chart = "mutated"
	hr2, cm2 := hrGenerate(t, cfg, "")
	if hr2.Spec.ValuesFrom[1].Name != "creds" || hr2.Spec.Chart.Spec.Chart != "podinfo" || cm1.Name != cm2.Name {
		t.Errorf("second render saw the first render's edits: %+v", hr2.Spec)
	}
	spec := cfg.(*components.HelmReleaseConfig).Spec
	if spec.Values == nil || len(spec.ValuesFrom) != 2 || spec.Interval.Duration != 0 || spec.TargetNamespace != "" {
		t.Errorf("Generate mutated the config's spec: %+v", spec)
	}
}

// TestHelmReleaseConfig_GenerateValidatesDirectConfig: a config built directly
// is checked at the emission boundary as the parse path checks it.
func TestHelmReleaseConfig_GenerateValidatesDirectConfig(t *testing.T) {
	chartRef := &helmv2.CrossNamespaceSourceReference{Kind: "OCIRepository", Name: "p"}
	cases := map[string]*components.HelmReleaseConfig{
		"no chart":         {Name: "web"},
		"bad valuesMode":   {Name: "web", ValuesMode: "yaml", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef}},
		"values not JSON":  {Name: "web", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`{"a":`)}}},
		"values a string":  {Name: "web", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`"s"`)}}},
		"trailing content": {Name: "web", ValuesMode: "configMap", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`{"a":1} {}`)}}},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := cases[n].Generate(nil); err == nil {
			t.Errorf("%s: Generate accepted an invalid config", n)
		}
	}
}

// TestTransform_HelmRelease_FluxNamespace runs the transform pipeline with a
// Flux namespace: the HelmRelease and its values ConfigMap land together in
// the Flux namespace, the release targets the application namespace, and the
// auto health check references the HelmRelease where it lands.
func TestTransform_HelmRelease_FluxNamespace(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmrelease": &components.HelmReleaseHandler{}}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "shop", Namespace: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "helmrelease",
			Properties: map[string]any{"chart": hrChart(), "valuesMode": "configMap", "values": map[string]any{"a": 1}},
		}}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var found bool
	var walk func(*stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				if a.Name != "web" {
					continue
				}
				found = true
				hr, cm := hrGenerate(t, a.Config, "")
				assertValuesConfigMap(t, hr, cm)
				if hr.Namespace != "flux-system" || cm.Namespace != "flux-system" || hr.Spec.TargetNamespace != "shop" {
					t.Errorf("HelmRelease %s, ConfigMap %s, targetNamespace %q", hr.Namespace, cm.Namespace, hr.Spec.TargetNamespace)
				}
				want := stack.HealthCheck{APIVersion: "helm.toolkit.fluxcd.io/v2", Kind: "HelmRelease", Name: "web", Namespace: "flux-system"}
				if !slices.Contains(n.Bundle.HealthChecks, want) {
					t.Errorf("health checks %+v lack %+v", n.Bundle.HealthChecks, want)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	if !found {
		t.Fatal("helmrelease application not found")
	}
}

// TestHelmReleaseConfig_SuspendVetoesAutoHealthCheck pins the config half of
// the suspend veto: `suspend: true` stops helm-controller reconciling the
// release, so the Ready condition the synthesized check reads cannot report on
// it, and the config declines the check. An unsuspended release, authored or
// by omission (including an explicit null), keeps it.
func TestHelmReleaseConfig_SuspendVetoesAutoHealthCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suspend any
		set     bool
		want    bool
	}{
		{"suspend true vetoes the check", true, true, false},
		{"suspend false keeps it", false, true, true},
		{"suspend null keeps it", nil, true, true},
		{"suspend unauthored keeps it", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"chart": hrChart()}
			if tc.set {
				props["suspend"] = tc.suspend
			}
			e, ok := hrConfig(t, "web", props).(interface{ EmitsAutoHealthCheck() bool })
			if !ok {
				t.Fatal("HelmReleaseConfig does not satisfy the autoHealthCheckEmitter shape the transform asserts on")
			}
			if got := e.EmitsAutoHealthCheck(); got != tc.want {
				t.Errorf("EmitsAutoHealthCheck() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTransform_HelmRelease_SuspendSkipsAutoHealthCheck runs the transform
// pipeline end to end: a suspended helmrelease component still emits its
// HelmRelease but gets no synthesized HelmRelease health check, while an
// unsuspended one does. The prune-protection case proves the veto survives a
// trait decorator, which must forward it.
func TestTransform_HelmRelease_SuspendSkipsAutoHealthCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suspend bool
		traits  []oam.Trait
		wantHC  bool
	}{
		{"unsuspended keeps the check", false, nil, true},
		{"suspended skips the check", true, nil, false},
		{"unsuspended under prune-protection keeps the check", false, []oam.Trait{{Type: "prune-protection"}}, true},
		{"suspended under prune-protection skips the check", true, []oam.Trait{{Type: "prune-protection"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := oam.NewTransformer(nil, nil)
			tr.RegisterComponent("helmrelease", &components.HelmReleaseHandler{})
			tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
			props := map[string]any{"chart": hrChart()}
			if tc.suspend {
				props["suspend"] = true
			}
			app := &oam.Application{
				Metadata: oam.Metadata{Name: "shop", Namespace: "shop"},
				Spec: oam.ApplicationSpec{Components: []oam.Component{{
					Name: "web", Type: "helmrelease", Properties: props, Traits: tc.traits,
				}}},
			}
			cluster, err := tr.Transform(app, oam.TransformContext{FluxNamespace: "flux-system"})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			var found bool
			var checks []stack.HealthCheck
			var walk func(*stack.Node)
			walk = func(n *stack.Node) {
				if n == nil {
					return
				}
				if n.Bundle != nil {
					checks = append(checks, n.Bundle.HealthChecks...)
					for _, a := range n.Bundle.Applications {
						if a.Name != "web" {
							continue
						}
						found = true
						hr, _ := hrGenerate(t, a.Config, "")
						if hr.Spec.Suspend != tc.suspend {
							t.Errorf("emitted HelmRelease suspend = %v, want %v", hr.Spec.Suspend, tc.suspend)
						}
					}
				}
				for _, c := range n.Children {
					walk(c)
				}
			}
			walk(cluster.Node)
			if !found {
				t.Fatal("helmrelease application not found")
			}
			want := stack.HealthCheck{APIVersion: "helm.toolkit.fluxcd.io/v2", Kind: "HelmRelease", Name: "web", Namespace: "flux-system"}
			if got := slices.Contains(checks, want); got != tc.wantHC {
				t.Errorf("health checks %+v: contains %+v = %v, want %v", checks, want, got, tc.wantHC)
			}
			if !tc.wantHC && len(checks) != 0 {
				t.Errorf("suspended release: health checks %+v, want none", checks)
			}
		})
	}
}
