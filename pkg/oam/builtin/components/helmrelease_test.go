package components_test

import (
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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

// hrGenerate renders cfg, under fluxNS when non-empty, and returns its one
// object, the HelmRelease.
func hrGenerate(t *testing.T, cfg stack.ApplicationConfig, fluxNS string) *helmv2.HelmRelease {
	t.Helper()
	if fluxNS != "" {
		cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
	}
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate emitted %d objects, want exactly the HelmRelease", len(objs))
	}
	hr, ok := (*objs[0]).(*helmv2.HelmRelease)
	if !ok {
		t.Fatalf("Generate emitted %T, want a HelmRelease", *objs[0])
	}
	return hr
}

func TestHelmReleaseHandler_CanHandle(t *testing.T) {
	h := &components.HelmReleaseHandler{}
	if !h.CanHandle("helmrelease") || h.CanHandle("helm") {
		t.Error("CanHandle must accept helmrelease only")
	}
}

// TestHelmReleaseHandler_SchemaMatchesSpec ties the published schema to the
// struct: exactly HelmReleaseSpec's top-level JSON keys, each with the
// property type its Go field encodes as. An upstream field added or removed on
// a helm-controller bump turns this red.
func TestHelmReleaseHandler_SchemaMatchesSpec(t *testing.T) {
	schema := (&components.HelmReleaseHandler{}).PropertySchema()
	want := map[string]oam.PropertyType{}
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
// unreachable through the strict decode. The exclusion list is explicit and
// must stay empty.
func TestHelmReleaseHandler_EveryFieldReachable(t *testing.T) {
	excluded := []string{}
	got := builtin.UnreachableJSONFields(reflect.TypeFor[helmv2.HelmReleaseSpec]())
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
	hr := hrGenerate(t, hrConfig(t, "web", props), "")
	if got, want := jsonShape(t, hr.Spec), jsonShape(t, props); !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("emitted spec differs from authored properties\ngot:  %s\nwant: %s", gj, wj)
	}
	var authoredKeys []string
	for k := range props {
		authoredKeys = append(authoredKeys, k)
	}
	if len(authoredKeys) != len((&components.HelmReleaseHandler{}).PropertySchema())-1 { // minus chartRef
		t.Errorf("fixture sets %d keys; it must set every spec key but chartRef", len(authoredKeys))
	}
}

func TestHelmReleaseHandler_Identity(t *testing.T) {
	hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "")
	if hr.Name != "web" || hr.Namespace != "demo" {
		t.Errorf("HelmRelease %s/%s, want demo/web", hr.Namespace, hr.Name)
	}
	if hr.APIVersion != "helm.toolkit.fluxcd.io/v2" || hr.Kind != "HelmRelease" {
		t.Errorf("TypeMeta %s %s", hr.APIVersion, hr.Kind)
	}
	if hr.Spec.TargetNamespace != "" {
		t.Errorf("targetNamespace %q set without a Flux namespace", hr.Spec.TargetNamespace)
	}

	hr = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "flux-system")
	if hr.Namespace != "flux-system" {
		t.Errorf("namespace %q under a Flux namespace, want flux-system", hr.Namespace)
	}
}

// TestHelmReleaseHandler_TargetNamespaceUnderFluxNamespace (C20): with a Flux
// namespace and no authored targetNamespace, the release still installs into
// the application namespace; an authored value wins.
func TestHelmReleaseHandler_TargetNamespaceUnderFluxNamespace(t *testing.T) {
	hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "flux-system")
	if hr.Spec.TargetNamespace != "demo" {
		t.Errorf("targetNamespace = %q, want the application namespace demo", hr.Spec.TargetNamespace)
	}
	if got := hr.GetReleaseName(); got != "demo-web" {
		t.Errorf("Flux default release name = %q, want demo-web", got)
	}

	hr = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "targetNamespace": "elsewhere"}), "flux-system")
	if hr.Spec.TargetNamespace != "elsewhere" {
		t.Errorf("authored targetNamespace lost: %q", hr.Spec.TargetNamespace)
	}
}

func TestHelmReleaseHandler_IntervalDefault(t *testing.T) {
	hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), "")
	if hr.Spec.Interval.Duration != 60*time.Minute {
		t.Errorf("default interval = %v, want 60m", hr.Spec.Interval.Duration)
	}
	hr = hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "interval": "10m"}), "")
	if hr.Spec.Interval.Duration != 10*time.Minute {
		t.Errorf("authored interval = %v, want 10m", hr.Spec.Interval.Duration)
	}
}

func TestHelmReleaseHandler_ChartRef(t *testing.T) {
	ref := map[string]any{"kind": "OCIRepository", "name": "podinfo"}
	hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chartRef": ref}), "")
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
		// valuesMode moved to the helm component (go-kure/launcher#702): any
		// value is an unknown key, with a pointer to what replaced it.
		{"valuesMode configMap", map[string]any{"chart": hrChart(), "valuesMode": "configMap", "values": map[string]any{"a": 1}}, `unknown field "valuesMode"; helmrelease no longer takes valuesMode (go-kure/launcher#702)`},
		{"valuesMode inline", map[string]any{"chart": hrChart(), "valuesMode": "inline"}, `unknown field "valuesMode"; helmrelease no longer takes valuesMode`},
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

// TestHelmReleaseHandler_ValuesModeHint: the authored-property check appends
// the pointer to helm's valuesMode for the removed valuesMode key only.
func TestHelmReleaseHandler_ValuesModeHint(t *testing.T) {
	h := &components.HelmReleaseHandler{}
	if got := h.UnsupportedFieldHint("valuesMode"); !strings.Contains(got, "helm component's valuesMode: configMap") {
		t.Errorf("valuesMode hint = %q", got)
	}
	for _, key := range []string{"valuesmode", "chartt", "values"} {
		if got := h.UnsupportedFieldHint(key); got != "" {
			t.Errorf("hint for %q = %q, want none", key, got)
		}
	}
}

// TestHelmReleaseHandler_GenerateDoesNotAlias: rendering twice gives equal,
// independent output, and editing one render leaves the config untouched.
func TestHelmReleaseHandler_GenerateDoesNotAlias(t *testing.T) {
	cfg := hrConfig(t, "web", map[string]any{
		"chart":      hrChart(),
		"values":     map[string]any{"a": 1},
		"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "creds"}, map[string]any{"kind": "ConfigMap", "name": "extra"}},
	})
	hr1 := hrGenerate(t, cfg, "")
	hr1.Spec.ValuesFrom[0].Name = "mutated"
	hr1.Spec.Chart.Spec.Chart = "mutated"
	hr2 := hrGenerate(t, cfg, "")
	if hr2.Spec.ValuesFrom[0].Name != "creds" || hr2.Spec.Chart.Spec.Chart != "podinfo" {
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
		"values not JSON":  {Name: "web", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`{"a":`)}}},
		"values a string":  {Name: "web", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`"s"`)}}},
		"trailing content": {Name: "web", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Values: &apiextensionsv1.JSON{Raw: []byte(`{"a":1} {}`)}}},
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
// Flux namespace: the HelmRelease lands in the Flux namespace, the release
// targets the application namespace, and the auto health check references the
// HelmRelease where it lands.
func TestTransform_HelmRelease_FluxNamespace(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmrelease": &components.HelmReleaseHandler{}}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "shop", Namespace: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "helmrelease",
			Properties: map[string]any{"chart": hrChart(), "values": map[string]any{"a": 1}},
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
				hr := hrGenerate(t, a.Config, "")
				if hr.Namespace != "flux-system" || hr.Spec.TargetNamespace != "shop" {
					t.Errorf("HelmRelease %s, targetNamespace %q", hr.Namespace, hr.Spec.TargetNamespace)
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
						hr := hrGenerate(t, a.Config, "")
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
