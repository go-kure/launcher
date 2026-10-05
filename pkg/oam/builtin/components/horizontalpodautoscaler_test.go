package components_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// hpaRich is a HorizontalPodAutoscaler spec that sets every top-level field: a
// metric of each of two types, a quantity written as a number, and scaling
// rules for both directions with an authored window of 0.
const hpaRich = `scaleTargetRef:
  apiVersion: apps/v1
  kind: Deployment
  name: web
minReplicas: 2
maxReplicas: 3
metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Pods
    pods:
      metric:
        name: requests-per-second
        selector:
          matchLabels:
            tier: web
      target:
        type: AverageValue
        averageValue: 100
behavior:
  scaleUp:
    stabilizationWindowSeconds: 0
    selectPolicy: Max
    policies:
      - type: Pods
        value: 4
        periodSeconds: 60
  scaleDown:
    stabilizationWindowSeconds: 300
    tolerance: "0.05"
`

// hpaPlain is the least a horizontalpodautoscaler component may author, with
// the given upper limit.
func hpaPlain(maxReplicas int) map[string]any {
	return map[string]any{
		"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"},
		"maxReplicas":    maxReplicas,
	}
}

// hpaGenerate generates a horizontalpodautoscaler component under the given
// policies and returns the HorizontalPodAutoscaler.
func hpaGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *autoscalingv2.HorizontalPodAutoscaler {
	t.Helper()
	return generateCoreKindUnder(t, &components.HorizontalPodAutoscalerHandler{}, "horizontalpodautoscaler", name, props, policies...).(*autoscalingv2.HorizontalPodAutoscaler)
}

func TestHorizontalPodAutoscalerHandler_CanHandle(t *testing.T) {
	h := &components.HorizontalPodAutoscalerHandler{}
	if !h.CanHandle("horizontalpodautoscaler") {
		t.Error("CanHandle(horizontalpodautoscaler) = false")
	}
	for _, other := range []string{"hpa", "scaler", "poddisruptionbudget"} {
		if h.CanHandle(other) {
			t.Errorf("CanHandle(%s) = true", other)
		}
	}
}

// TestHorizontalPodAutoscalerHandler_EmitsAuthoredSpec: the autoscaler is named
// after the component in the build namespace, unlabelled, and its spec is the
// authored one, field for field, with nothing added. The handler does not
// change its input.
func TestHorizontalPodAutoscalerHandler_EmitsAuthoredSpec(t *testing.T) {
	var want autoscalingv2.HorizontalPodAutoscalerSpec
	if err := yaml.UnmarshalStrict([]byte(hpaRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	props := ptObject(t, hpaRich)
	before, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("marshal the properties: %v", err)
	}
	// Every top-level field of the spec type is in the fixture, so a field a
	// dependency bump adds is built from here too.
	for name := range specJSONFields(t, reflect.TypeFor[autoscalingv2.HorizontalPodAutoscalerSpec]()) {
		if _, ok := props[name]; !ok {
			t.Errorf("the fixture sets no %q, a field of the spec type", name)
		}
	}

	hpa := hpaGenerate(t, "web", props, ptStrictPolicy(), nil)
	if hpa.APIVersion != "autoscaling/v2" || hpa.Kind != "HorizontalPodAutoscaler" {
		t.Errorf("GVK = %s %s, want autoscaling/v2 HorizontalPodAutoscaler", hpa.APIVersion, hpa.Kind)
	}
	if hpa.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", hpa.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(hpa.Spec, want) {
		t.Errorf("spec differs from the authored one:\n got %+v\nwant %+v", hpa.Spec, want)
	}
	if after, err := json.Marshal(props); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the handler changed its input: %s (err %v), was %s", after, err, before)
	}

	// An authored zero is kept, and a quantity written as a number takes its
	// canonical form.
	if w := hpa.Spec.Behavior.ScaleUp.StabilizationWindowSeconds; w == nil || *w != 0 {
		t.Errorf("scaleUp.stabilizationWindowSeconds = %v, want the authored 0", w)
	}
	if q := hpa.Spec.Metrics[1].Pods.Target.AverageValue; q == nil || !q.Equal(resource.MustParse("100")) || q.String() != "100" {
		t.Errorf("pods target averageValue = %v, want 100", q)
	}
	// The object holds nothing beside its identity and the spec: no status the
	// author did not write.
	if !reflect.DeepEqual(hpa.Status, autoscalingv2.HorizontalPodAutoscalerStatus{}) {
		t.Errorf("status = %+v, want none", hpa.Status)
	}
}

// TestHorizontalPodAutoscalerHandler_Minimal: with the two required fields
// alone the spec holds those and nothing else: minReplicas, metrics and
// behavior stay unset for the API server to default.
func TestHorizontalPodAutoscalerHandler_Minimal(t *testing.T) {
	hpa := hpaGenerate(t, "web", hpaPlain(3), ptStrictPolicy(), nil)
	want := autoscalingv2.HorizontalPodAutoscalerSpec{
		ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"},
		MaxReplicas:    3,
	}
	if !reflect.DeepEqual(hpa.Spec, want) {
		t.Errorf("spec = %+v, want %+v", hpa.Spec, want)
	}
}

// hpaScalerDefaults is a policy that sets the scaler trait's replica defaults,
// which stubPolicy leaves unset.
type hpaScalerDefaults struct {
	*stubPolicy
	min, max *int32
}

func (p hpaScalerDefaults) DefaultScalerMinReplicas() *int32 { return p.min }
func (p hpaScalerDefaults) DefaultScalerMaxReplicas() *int32 { return p.max }

// TestHorizontalPodAutoscalerHandler_FillsNoPolicyDefault: the kind takes no
// default from the policy, the scaler trait's limits included: the authored
// maxReplicas stays, and minReplicas stays unset.
func TestHorizontalPodAutoscalerHandler_FillsNoPolicyDefault(t *testing.T) {
	defaults := hpaScalerDefaults{stubPolicy: &stubPolicy{defaultReplicas: int32ptr(2)}, min: int32ptr(2), max: int32ptr(9)}
	hpa := hpaGenerate(t, "web", hpaPlain(4), defaults)
	if hpa.Spec.MaxReplicas != 4 || hpa.Spec.MinReplicas != nil {
		t.Errorf("maxReplicas = %d, minReplicas = %v; want the authored 4 and none", hpa.Spec.MaxReplicas, hpa.Spec.MinReplicas)
	}
}

// TestHorizontalPodAutoscalerHandler_Refusals: what the component refuses when
// it is read, with no policy involved: a property that is not a spec field, at
// any depth, and an unauthored scaleTargetRef or maxReplicas.
func TestHorizontalPodAutoscalerHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a autoscaling/v2 HorizontalPodAutoscalerSpec"
	with := func(extra map[string]any) map[string]any {
		props := hpaPlain(3)
		maps.Copy(props, extra)
		return props
	}
	without := func(name string) map[string]any {
		props := hpaPlain(3)
		delete(props, name)
		return props
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":          {nil, "scaleTargetRef: required"},
		"no scaleTargetRef":      {without("scaleTargetRef"), "scaleTargetRef: required"},
		"null scaleTargetRef":    {with(map[string]any{"scaleTargetRef": nil}), "scaleTargetRef: required"},
		"empty scaleTargetRef":   {with(map[string]any{"scaleTargetRef": map[string]any{}}), "scaleTargetRef: required"},
		"no maxReplicas":         {without("maxReplicas"), "maxReplicas: required"},
		"null maxReplicas":       {with(map[string]any{"maxReplicas": nil}), "maxReplicas: required"},
		"maxReplicas of 0":       {with(map[string]any{"maxReplicas": 0}), "maxReplicas: required"},
		"the scaler's key":       {with(map[string]any{"targetCPUUtilization": 70}), notASpec},
		"the object's spec":      {map[string]any{"spec": hpaPlain(3)}, notASpec},
		"metadata":               {with(map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}), notASpec},
		"maxReplicas a string":   {with(map[string]any{"maxReplicas": "three"}), notASpec},
		"maxReplicas a fraction": {with(map[string]any{"maxReplicas": 2.5}), notASpec},
		"target sub-key":         {with(map[string]any{"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "web", "namespace": "other"}}), notASpec},
		"metric sub-key":         {with(map[string]any{"metrics": []any{map[string]any{"type": "Resource", "resources": map[string]any{}}}}), notASpec},
		"behavior sub-key":       {with(map[string]any{"behavior": map[string]any{"scaleUp": map[string]any{"window": 0}}}), notASpec},
		"bad quantity":           {with(map[string]any{"behavior": map[string]any{"scaleDown": map[string]any{"tolerance": "a lot"}}}), notASpec},
		"metrics a map":          {with(map[string]any{"metrics": map[string]any{"type": "Resource"}}), notASpec},
		"null metric":            {with(map[string]any{"metrics": []any{nil}}), "metrics[0]"},
		"two spellings":          {with(map[string]any{"MaxReplicas": 4}), "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.HorizontalPodAutoscalerHandler{}, "horizontalpodautoscaler", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestHorizontalPodAutoscalerConfig_GenerateRepeatsTheRefusals: the config is
// exported, so a spec built in code is held to the same refusals before the
// autoscaler is emitted.
func TestHorizontalPodAutoscalerConfig_GenerateRepeatsTheRefusals(t *testing.T) {
	target := autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"}
	for name, tc := range map[string]struct {
		spec autoscalingv2.HorizontalPodAutoscalerSpec
		want string
	}{
		"no scaleTargetRef": {autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 3}, "scaleTargetRef: required"},
		"no maxReplicas":    {autoscalingv2.HorizontalPodAutoscalerSpec{ScaleTargetRef: target}, "maxReplicas: required"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &components.HorizontalPodAutoscalerConfig{Name: "web", Namespace: coreKindNamespace, Spec: tc.spec}
			_, err := cfg.Generate(stack.NewApplication("web", coreKindNamespace, cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestHorizontalPodAutoscalerConfig_GenerateCopies: each Generate returns an
// autoscaler of its own, sharing no slice or pointer with the next or with the
// config, so what is written on one does not reach another build.
func TestHorizontalPodAutoscalerConfig_GenerateCopies(t *testing.T) {
	cfg, err := (&components.HorizontalPodAutoscalerHandler{}).ToApplicationConfig(
		&oam.Component{Name: "web", Type: "horizontalpodautoscaler", Properties: ptObject(t, hpaRich)}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	var objs [2]reflect.Value
	for i := range objs {
		generated, err := cfg.Generate(stack.NewApplication("web", coreKindNamespace, cfg))
		if err != nil || len(generated) != 1 {
			t.Fatalf("Generate: %d objects, err %v", len(generated), err)
		}
		objs[i] = reflect.ValueOf(*generated[0])
	}
	if !reflect.DeepEqual(objs[0].Interface(), objs[1].Interface()) {
		t.Fatalf("two builds differ: %+v and %+v", objs[0].Interface(), objs[1].Interface())
	}
	if shared := sharedReferences(objs[0], objs[1], ""); len(shared) != 0 {
		t.Errorf("two builds share %v", shared)
	}
	held := reflect.ValueOf(&cfg.(*components.HorizontalPodAutoscalerConfig).Spec)
	if shared := sharedReferences(objs[0].Elem().FieldByName("Spec").Addr(), held, ".Spec"); len(shared) != 0 {
		t.Errorf("a build shares %v with the config", shared)
	}
	// Vacuity guard: compared with itself, an object shares every reference
	// it holds, so the walk must report the ones the fixture sets.
	self := sharedReferences(objs[0], objs[0], "")
	for _, path := range []string{
		".Spec.MinReplicas", ".Spec.Metrics", ".Spec.Metrics[0].Resource", ".Spec.Metrics[0].Resource.Target.AverageUtilization",
		".Spec.Metrics[1].Pods.Metric.Selector.MatchLabels", ".Spec.Behavior", ".Spec.Behavior.ScaleUp.Policies",
		".Spec.Behavior.ScaleUp.StabilizationWindowSeconds", ".Spec.Behavior.ScaleDown.Tolerance",
	} {
		if !slices.Contains(self, path) {
			t.Errorf("the walk did not reach %s; it found %v", path, self)
		}
	}
}

// TestHorizontalPodAutoscalerPolicy_MaxReplicas: maxReplicas is held to the
// policy's replica maximum, and the violation names the component and the
// field. minReplicas is not held: it cannot exceed maxReplicas on a valid
// autoscaler.
func TestHorizontalPodAutoscalerPolicy_MaxReplicas(t *testing.T) {
	for name, tc := range map[string]struct {
		maxReplicas int
		policy      *stubPolicy
		want        string // "" when the component builds
	}{
		"over the maximum":             {4, ptStrictPolicy(), "maxReplicas: replicas 4 exceeds enforced maximum 3"},
		"at the maximum":               {3, ptStrictPolicy(), ""},
		"under the maximum":            {1, ptStrictPolicy(), ""},
		"over it with no maximum set":  {50, &stubPolicy{}, ""},
		"over it with no policy given": {50, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			objs, err := pvTransform("horizontalpodautoscaler", &components.HorizontalPodAutoscalerHandler{}, hpaPlain(tc.maxReplicas), pvPolicy(tc.policy))
			if tc.want != "" {
				htWantViolation(t, err, `component "web": `+tc.want)
				return
			}
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "HorizontalPodAutoscaler" {
				t.Fatalf("generated %v, want the one HorizontalPodAutoscaler", objs)
			}
			obj := objs[0]
			if obj.GetName() != "web" || obj.GetNamespace() != "demo" {
				t.Errorf("identity = %q/%q, want demo/web", obj.GetNamespace(), obj.GetName())
			}
			wantLabels := map[string]string{oam.ComponentLabelKeyForDomain(""): "web"}
			if !reflect.DeepEqual(obj.GetLabels(), wantLabels) || len(obj.GetAnnotations()) != 0 {
				t.Errorf("labels = %v, annotations = %v; want labels %v and no annotation", obj.GetLabels(), obj.GetAnnotations(), wantLabels)
			}
		})
	}
}

// TestHorizontalPodAutoscalerConfig_ApplyPolicy_NilIsANoOp: a nil policy
// checks nothing, as on every other config.
func TestHorizontalPodAutoscalerConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.HorizontalPodAutoscalerConfig{Name: "web", Namespace: coreKindNamespace,
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 50}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}

// TestHorizontalPodAutoscalerHandler_DeclaresItsObject: the handler declares
// the object it emits, by group and kind, as namespaced, so the type takes
// `objectName` and the engine claims the name in the object's namespace.
func TestHorizontalPodAutoscalerHandler_DeclaresItsObject(t *testing.T) {
	var h oam.ComponentHandler = &components.HorizontalPodAutoscalerHandler{}
	provider, declares := h.(oam.ComponentObjectProvider)
	if !declares {
		t.Fatalf("%T declares no object (oam.ComponentObjectProvider)", h)
	}
	want := schema.GroupKind{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}
	if got, scope := provider.ComponentObject(); got != want || scope != oam.ObjectScopeNamespaced {
		t.Errorf("ComponentObject() = %s, scope %d; want %s, namespaced (%d)", got, scope, want, oam.ObjectScopeNamespaced)
	}
}

// TestHorizontalPodAutoscalerHandler_ObjectName: through the transform, the
// author's `objectName` names the autoscaler, and without one the Naming
// hook's answer for role "object" does; the author's wins over the hook's. It
// names the object and nothing else: the namespace, the component label and
// the spec are those of the autoscaler built under the component name.
func TestHorizontalPodAutoscalerHandler_ObjectName(t *testing.T) {
	const component, authored = "web", "web-autoscaler"
	h := &components.HorizontalPodAutoscalerHandler{}
	var asked []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		if req.Role != oam.NameRoleObject {
			return "", false
		}
		asked = append(asked, req)
		return "hooked-" + req.Default, true
	}
	build := func(t *testing.T, objectName string, naming func(oam.NameRequest) (string, bool)) client.Object {
		t.Helper()
		props := ptObject(t, hpaRich)
		if objectName != "" {
			props[oam.ObjectNameProperty] = objectName
		}
		objs, err := policyFreeTransform("horizontalpodautoscaler", h, naming, oam.Component{Name: component, Properties: props})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(objs) != 1 {
			t.Fatalf("generated %d objects, want one", len(objs))
		}
		return objs[0]
	}
	for name, tc := range map[string]struct {
		objectName string
		naming     func(oam.NameRequest) (string, bool)
		want       string
	}{
		"objectName":               {authored, nil, authored},
		"the Naming hook":          {"", hook, "hooked-" + component},
		"objectName over the hook": {authored, hook, authored},
	} {
		t.Run(name, func(t *testing.T) {
			plain := build(t, "", nil)
			if plain.GetName() != component {
				t.Fatalf("without objectName the object is named %q, want the component's %q", plain.GetName(), component)
			}
			asked = nil
			obj := build(t, tc.objectName, tc.naming)
			if obj.GetName() != tc.want || obj.GetNamespace() != "demo" {
				t.Errorf("identity = %q/%q, want demo/%s", obj.GetNamespace(), obj.GetName(), tc.want)
			}
			wantLabels := map[string]string{oam.ComponentLabelKeyForDomain(""): component}
			if !reflect.DeepEqual(obj.GetLabels(), wantLabels) {
				t.Errorf("labels = %v, want %v: the component label keeps the component's name", obj.GetLabels(), wantLabels)
			}
			obj.SetName(plain.GetName())
			if got, want := policyFreeJSON(t, obj), policyFreeJSON(t, plain); !reflect.DeepEqual(got, want) {
				t.Errorf("but for its name the object differs from the one built under the component name:\n got %v\nwant %v", got, want)
			}
			switch {
			case tc.naming == nil || tc.objectName != "":
				if len(asked) != 0 {
					t.Errorf("the hook was asked %+v, want it not asked for an authored name", asked)
				}
			case len(asked) == 0:
				t.Error("the hook was not asked for the object's name")
			default:
				for _, req := range asked {
					if req.Kind != "HorizontalPodAutoscaler.autoscaling" || req.Component != component || req.Default != component {
						t.Errorf("the hook was asked %+v, want kind HorizontalPodAutoscaler.autoscaling, component and default %q", req, component)
					}
				}
			}
		})
	}
}

// TestHorizontalPodAutoscalerHandler_ObjectNameIsClaimedInItsNamespace: two
// components given one object name are refused, and the refusal names the
// autoscaler by its namespace and name.
func TestHorizontalPodAutoscalerHandler_ObjectNameIsClaimedInItsNamespace(t *testing.T) {
	named := func(name string) oam.Component {
		props := hpaPlain(3)
		props[oam.ObjectNameProperty] = "shared"
		return oam.Component{Name: name, Properties: props}
	}
	_, err := policyFreeTransform("horizontalpodautoscaler", &components.HorizontalPodAutoscalerHandler{}, nil, named("a"), named("b"))
	want := fmt.Sprintf("name collision: HorizontalPodAutoscaler.autoscaling %q is named by component %q", "demo/shared", "a")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}
