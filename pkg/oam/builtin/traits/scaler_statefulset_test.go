package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The scaler scales a StatefulSet as well as a Deployment (go-kure/launcher#1268):
// the HorizontalPodAutoscaler's scaleTargetRef takes its kind from the settled
// type of the component the scaler is applied on. These tests run the whole
// transformer with every built-in registered, the way the CLI does, because the
// kind comes from the component type the engine stamps on the trait, and the
// scaler is a sub-application that never sees the component's objects.

const sssNamespace = "default"

// sssApp wraps comps in a document the transformer accepts.
func sssApp(comps ...oam.Component) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: sssNamespace},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
}

// sssComponent is a component of the given built-in type with the properties it
// needs to build, and the given traits.
func sssComponent(name, typ string, extra map[string]any, trs ...oam.Trait) oam.Component {
	props := map[string]any{"image": "ghcr.io/org/app:v1"}
	if typ == "statefulset" {
		props["serviceName"] = "db-headless"
	}
	for k, v := range extra {
		props[k] = v
	}
	return oam.Component{Name: name, Type: typ, Properties: props, Traits: trs}
}

func sssScaler() oam.Trait {
	return scalerTrait(map[string]any{"minReplicas": 2, "maxReplicas": 4})
}

// sssHPAs transforms comps and returns the HorizontalPodAutoscalers they generate.
func sssHPAs(t *testing.T, tr *oam.Transformer, comps ...oam.Component) []*autoscalingv2.HorizontalPodAutoscaler {
	t.Helper()
	cluster, err := tr.Transform(sssApp(comps...), oam.TransformContext{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return generatedHPAs(t, cluster)
}

// sssWantTarget fails the test unless hpas holds exactly one HPA whose
// scaleTargetRef is apps/v1 <kind> <name>.
func sssWantTarget(t *testing.T, hpas []*autoscalingv2.HorizontalPodAutoscaler, kind, name string) {
	t.Helper()
	if len(hpas) != 1 {
		t.Fatalf("generated %d HPAs, want 1", len(hpas))
	}
	want := autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: kind, Name: name}
	if got := hpas[0].Spec.ScaleTargetRef; got != want {
		t.Errorf("scaleTargetRef = %+v, want %+v", got, want)
	}
}

// TestScalerStatefulSet_HPATargetsAStatefulSet: a scaler on a statefulset gives an
// HPA that scales a StatefulSet, under the component's name and under its
// objectName.
func TestScalerStatefulSet_HPATargetsAStatefulSet(t *testing.T) {
	cases := []struct {
		name     string
		extra    map[string]any
		wantName string
	}{
		{"component name", nil, "db"},
		{"objectName", map[string]any{"objectName": "db-primary"}, "db-primary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hpas := sssHPAs(t, disruptionTransformer(), sssComponent("db", "statefulset", tc.extra, sssScaler()))
			sssWantTarget(t, hpas, "StatefulSet", tc.wantName)
		})
	}
}

// TestScalerStatefulSet_DeploymentKindsKeepTheDeploymentTarget is the control:
// the kinds that build a Deployment keep a Deployment target.
func TestScalerStatefulSet_DeploymentKindsKeepTheDeploymentTarget(t *testing.T) {
	for _, typ := range []string{"deployment", "webservice", "worker"} {
		t.Run(typ, func(t *testing.T) {
			hpas := sssHPAs(t, disruptionTransformer(), sssComponent("app", typ, nil, sssScaler()))
			sssWantTarget(t, hpas, "Deployment", "app")
		})
	}
}

// TestScalerStatefulSet_OutsideATransformKeepsTheDeploymentTarget: a handler's
// Apply called directly has no component type (oam.Trait.ComponentType is empty
// outside a transform), and the kind falls back to Deployment as before.
func TestScalerStatefulSet_OutsideATransformKeepsTheDeploymentTarget(t *testing.T) {
	trait := sssScaler()
	bundle := newBundle()
	if err := (&traits.ScalerHandler{}).Apply(&trait, newApp("api", sssNamespace), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var hpas []*autoscalingv2.HorizontalPodAutoscaler
	for _, o := range objs {
		if hpa, ok := (*o).(*autoscalingv2.HorizontalPodAutoscaler); ok {
			hpas = append(hpas, hpa)
		}
	}
	sssWantTarget(t, hpas, "Deployment", "api")
}

// scaleTargetKinder is what the scaler config reports for topology-spread to
// tell a Deployment target from a StatefulSet one. The anonymous assertion
// keeps this file compiling before the method exists.
type scaleTargetKinder interface{ ScaleTargetKind() string }

// TestScalerStatefulSet_ScaleTargetKindIsReported: the scaler config reports the
// kind it scales. A config built directly and never passed through Apply reports
// and generates Deployment, so the default does not live only in Apply.
func TestScalerStatefulSet_ScaleTargetKindIsReported(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{{"statefulset", "StatefulSet"}, {"deployment", "Deployment"}} {
		t.Run(tc.typ, func(t *testing.T) {
			cluster, err := disruptionTransformer().Transform(sssApp(sssComponent("db", tc.typ, nil, sssScaler())), oam.TransformContext{})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			cfg := oam.UnwrapConfig(scalerApplication(t, cluster).Config)
			k, ok := cfg.(scaleTargetKinder)
			if !ok {
				t.Fatalf("%T has no ScaleTargetKind() string", cfg)
			}
			if got := k.ScaleTargetKind(); got != tc.want {
				t.Errorf("ScaleTargetKind() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("config built directly", func(t *testing.T) {
		cfg := &traits.ScalerConfig{MinReplicas: 2, MaxReplicas: 4}
		k, ok := any(cfg).(scaleTargetKinder)
		if !ok {
			t.Fatalf("%T has no ScaleTargetKind() string", cfg)
		}
		if got := k.ScaleTargetKind(); got != "Deployment" {
			t.Errorf("ScaleTargetKind() = %q on a config never passed through Apply, want Deployment", got)
		}
		objs, err := cfg.Generate(stack.NewApplication("api", sssNamespace, cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		for _, o := range objs {
			if hpa, ok := (*o).(*autoscalingv2.HorizontalPodAutoscaler); ok {
				if got := hpa.Spec.ScaleTargetRef.Kind; got != "Deployment" {
					t.Errorf("scaleTargetRef.kind = %q on a config built directly, want Deployment", got)
				}
				return
			}
		}
		t.Fatal("Generate produced no HPA")
	})
}

// lowersTo is a component lowering rule that settles its authored type to another
// built-in type, forwarding the traits.
type lowersTo struct{ from, to string }

func (r lowersTo) ComponentType() string { return r.from }

func (r lowersTo) LowerComponent(c *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	props := map[string]any{"image": "ghcr.io/org/app:v1"}
	if r.to == "statefulset" {
		props["serviceName"] = "db-headless"
	}
	return oam.LoweringResult{Components: []oam.Component{{
		Name: c.Name, Type: r.to, Properties: props, Traits: c.Traits,
	}}}, nil
}

// TestScalerStatefulSet_SettledTypeDecidesTheTarget: the kind follows the type the
// component settles to, not the type it was authored as. An authored type that
// lowers to statefulset admits a scaler and gets a StatefulSet target; one that
// lowers to deployment keeps Deployment. A stamp that carried the authored type
// would find no row for either.
func TestScalerStatefulSet_SettledTypeDecidesTheTarget(t *testing.T) {
	for _, tc := range []struct{ to, wantKind string }{{"statefulset", "StatefulSet"}, {"deployment", "Deployment"}} {
		t.Run("lowers to "+tc.to, func(t *testing.T) {
			tr := disruptionTransformer()
			tr.RegisterComponentLowering(lowersTo{from: "settles-" + tc.to, to: tc.to})
			comp := oam.Component{Name: "db", Type: "settles-" + tc.to, Traits: []oam.Trait{sssScaler()}}
			sssWantTarget(t, sssHPAs(t, tr, comp), tc.wantKind, "db")
		})
	}
}

// widgetHandler builds like a deployment but claims only the type "widget", which
// the registration check requires of a handler (Transformer.RegisterComponent)
// and which has no row in the scaler's kind table.
type widgetHandler struct{ components.DeploymentHandler }

func (*widgetHandler) CanHandle(componentType string) bool { return componentType == "widget" }

// TestScalerStatefulSet_UnknownTypeInATransformIsAnError: inside a transform a
// component type with no row in the scaler's kind table is an error naming the
// component and the type, with no Deployment fallback. A transformer that
// registers no lowering rules skips validation, so the scaler's Apply is reached
// with a type admission would have refused.
func TestScalerStatefulSet_UnknownTypeInATransformIsAnError(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"widget": &widgetHandler{}}, nil)
	tr.RegisterBuiltinTrait("scaler", &traits.ScalerHandler{})
	comp := sssComponent("gizmo", "widget", nil, sssScaler())
	_, err := tr.Transform(sssApp(comp), oam.TransformContext{})
	if err == nil {
		t.Fatal("a scaler on a type with no kind row built; want an error, not a Deployment fallback")
	}
	for _, want := range []string{"gizmo", "widget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestScalerStatefulSet_KindTableAgreesWithAdmission: of the terminal kinds with a
// registered handler, exactly deployment and statefulset take a scaler (webservice
// and worker are lowering rules, checked below), and the HPA each builds targets
// its own kind.
func TestScalerStatefulSet_KindTableAgreesWithAdmission(t *testing.T) {
	want := map[string]string{"deployment": "Deployment", "statefulset": "StatefulSet"}
	for typ := range registry.ComponentHandlers() {
		t.Run(typ, func(t *testing.T) {
			comp := sssComponent("x", typ, nil, sssScaler())
			cluster, err := disruptionTransformer().Transform(sssApp(comp), oam.TransformContext{})
			refused := err != nil && strings.Contains(err.Error(), "not supported on component type")
			kind, admitted := want[typ]
			switch {
			case admitted && refused:
				t.Fatalf("a scaler on %s was refused: %v", typ, err)
			case !admitted && !refused:
				t.Fatalf("a scaler on %s was not refused by admission (err = %v); only %v take one", typ, err, want)
			case admitted && err != nil:
				t.Fatalf("transform: %v", err)
			case admitted:
				sssWantTarget(t, generatedHPAs(t, cluster), kind, "x")
			}
		})
	}
}

// TestScalerStatefulSet_TerminalKindsHaveHandlers is the control for the table
// test: the terminal rows have a registered handler, and webservice and worker are
// lowering rules only, never handlers.
func TestScalerStatefulSet_TerminalKindsHaveHandlers(t *testing.T) {
	handlers := registry.ComponentHandlers()
	for _, typ := range []string{"deployment", "statefulset"} {
		if _, ok := handlers[typ]; !ok {
			t.Errorf("no component handler registered for %q", typ)
		}
	}
	rules := registry.ComponentLoweringRules()
	for _, typ := range []string{"webservice", "worker"} {
		if _, ok := rules[typ]; !ok {
			t.Errorf("no component lowering rule registered for %q", typ)
		}
		if _, ok := handlers[typ]; ok {
			t.Errorf("%q is both a lowering rule and a component handler", typ)
		}
	}
}

// sssSpreadTrait is a topology-spread trait.
func sssSpreadTrait() oam.Trait {
	return oam.Trait{Type: "topology-spread", Properties: map[string]any{}}
}

// TestScalerStatefulSet_TopologySpreadIgnoresASameNameStatefulSetScaler: a
// StatefulSet and a Deployment may share an object name in one namespace. The
// Deployment `web`, renamed to `db` by objectName, is not scaled by the scaler of
// the StatefulSet `db`, so it gets no spread count from that scaler's minReplicas.
func TestScalerStatefulSet_TopologySpreadIgnoresASameNameStatefulSetScaler(t *testing.T) {
	scaler := scalerTrait(map[string]any{"minReplicas": 3, "maxReplicas": 5})
	sts := sssComponent("db", "statefulset", nil, scaler)
	dep := sssComponent("web", "deployment", map[string]any{"objectName": "db"}, sssSpreadTrait())
	cluster, err := disruptionTransformer().Transform(sssApp(sts, dep), oam.TransformContext{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var deps []*appsv1.Deployment
	for _, o := range clusterObjects(t, cluster) {
		if d, ok := o.(*appsv1.Deployment); ok {
			deps = append(deps, d)
		}
	}
	if len(deps) != 1 {
		t.Fatalf("generated %d Deployments, want 1", len(deps))
	}
	if got := gotSpread(deps[0].Spec.Template.Spec.TopologySpreadConstraints); len(got) != 0 {
		t.Errorf("Deployment %q got spread constraints %v from the scaler of the same-named StatefulSet, want none (its own count is 1)", deps[0].Name, got)
	}
}

// TestScalerStatefulSet_TopologySpreadOnAStatefulSetIsRefused is the control:
// topology-spread acts on Deployments only, so on a statefulset without a scaler
// it is refused at Generate, as it was before the scaler was admitted.
func TestScalerStatefulSet_TopologySpreadOnAStatefulSetIsRefused(t *testing.T) {
	comp := sssComponent("db", "statefulset", nil, sssSpreadTrait())
	cluster, err := disruptionTransformer().Transform(sssApp(comp), oam.TransformContext{})
	if err == nil {
		err = generateAll(cluster)
	}
	wantRefused(t, err, "generates no Deployment")
}

// TestScalerStatefulSet_TopologySpreadRefusedOnAScaledStatefulSet: with the
// scaler admitted the same refusal is reached with one beside the trait.
func TestScalerStatefulSet_TopologySpreadRefusedOnAScaledStatefulSet(t *testing.T) {
	comp := sssComponent("db", "statefulset", nil, sssScaler(), sssSpreadTrait())
	cluster, err := disruptionTransformer().Transform(sssApp(comp), oam.TransformContext{})
	if err == nil {
		err = generateAll(cluster)
	}
	wantRefused(t, err, "generates no Deployment")
}

// generateAll runs Generate on every application of the cluster and returns the
// first error.
func generateAll(cluster *stack.Cluster) error {
	var walk func(node *stack.Node) error
	walk = func(node *stack.Node) error {
		if node == nil {
			return nil
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				if _, err := a.Generate(); err != nil {
					return err
				}
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(cluster.Node)
}

// sstsGroupRule lowers its type to a same-name sibling group: a statefulset
// carrying the authored traits, and the headless service that governs it.
type sstsGroupRule struct{}

func (sstsGroupRule) ComponentType() string { return "db-pair" }

func (sstsGroupRule) LowerComponent(c *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{
		{Name: c.Name, Type: "statefulset", Properties: map[string]any{
			"image": "ghcr.io/org/db:v1", "serviceName": c.Name,
		}, Traits: c.Traits},
		{Name: c.Name, Type: "service", Properties: map[string]any{
			"clusterIP": "None",
			"selector":  map[string]any{"app": c.Name},
			"ports":     []any{map[string]any{"name": "tcp", "port": 5432}},
		}},
	}}, nil
}

// TestScalerStatefulSet_SiblingGroupMember: a statefulset that is one member of a
// same-name sibling group takes a scaler like any member, and the HPA targets a
// StatefulSet under the group's name.
func TestScalerStatefulSet_SiblingGroupMember(t *testing.T) {
	tr := disruptionTransformer()
	tr.RegisterComponentLowering(sstsGroupRule{})
	comp := oam.Component{Name: "db", Type: "db-pair", Traits: []oam.Trait{sssScaler()}}
	hpas := sssHPAs(t, tr, comp)
	sssWantTarget(t, hpas, "StatefulSet", "db")
	var found bool
	for _, o := range clusterObjects(t, mustTransform(t, tr, comp)) {
		if _, ok := o.(*appsv1.StatefulSet); ok {
			found = true
		}
	}
	if !found {
		t.Error("the group generated no StatefulSet for the HPA to target")
	}
}

func mustTransform(t *testing.T, tr *oam.Transformer, comps ...oam.Component) *stack.Cluster {
	t.Helper()
	cluster, err := tr.Transform(sssApp(comps...), oam.TransformContext{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return cluster
}
