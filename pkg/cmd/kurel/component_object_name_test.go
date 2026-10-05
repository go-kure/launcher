package kurel

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// noObjectNameTypes are the registered component types that take no
// `objectName`, each with the reason: the type generates no single object named
// after the component. Every other registered type is a kind component and
// declares its object (oam.ComponentObjectProvider).
var noObjectNameTypes = map[string]string{
	"helmtemplate": "renders a chart's objects under the names the chart gives them",
	"manifests":    "emits the authored objects under the names they carry",
	"crd":          "emits the authored CustomResourceDefinitions under the names they carry",
	"passthrough":  "emits one authored object of any kind, which the handler cannot declare",
}

// TestObjectName_EveryComponentTypeChooses: a registered component handler
// either declares its object, and with it takes `objectName` and the name role
// "object", or is listed in noObjectNameTypes with the reason it does not. A
// new kind cannot join the registry without choosing.
func TestObjectName_EveryComponentTypeChooses(t *testing.T) {
	handlers := builtinComponentHandlers()
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		_, declares := handlers[typ].(oam.ComponentObjectProvider)
		_, listed := noObjectNameTypes[typ]
		switch {
		case declares && listed:
			t.Errorf("component type %q declares its object (oam.ComponentObjectProvider) and is listed in noObjectNameTypes; it is one or the other", typ)
		case !declares && !listed:
			t.Errorf("component type %q neither declares its object (oam.ComponentObjectProvider) nor is listed in noObjectNameTypes; a kind component declares it, any other type is listed with the reason", typ)
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(noObjectNameTypes)) {
		if _, registered := handlers[typ]; !registered {
			t.Errorf("noObjectNameTypes lists %q, which is not a registered component type", typ)
		}
	}
}

// objectNameApp is labelInvariantApp with `objectName` set on the component
// when objectName is not empty. props is not mutated.
func objectNameApp(name, typ string, props map[string]any, objectName string) map[string]any {
	if objectName != "" {
		props = maps.Clone(props)
		if props == nil {
			props = map[string]any{}
		}
		props[oam.ObjectNameProperty] = objectName
	}
	return labelInvariantApp(name, typ, props, "", nil)
}

// docsOfKind returns the names of the emitted objects of kind.
func docsOfKind(docs []map[string]any, kind string, group string) []string {
	var names []string
	for _, doc := range docs {
		apiVersion, _ := doc["apiVersion"].(string)
		docGroup := ""
		if g, _, versioned := strings.Cut(apiVersion, "/"); versioned {
			docGroup = g
		}
		if doc["kind"] != kind || docGroup != group {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestObjectName_RenamesTheObjectAlone renders every kind component with and
// without `objectName`. Without it the object of the declared kind is named
// after the component. With it that object carries the authored name, no object
// of the kind keeps the component name, nothing is added or dropped, and the
// labels and selectors still carry the component name.
func TestObjectName_RenamesTheObjectAlone(t *testing.T) {
	const component, renamed = "widget", "renamed-widget"
	handlers := builtinComponentHandlers()
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		provider, declares := handlers[typ].(oam.ComponentObjectProvider)
		if !declares {
			continue
		}
		fx, has := componentLabelFixtures[typ]
		if !has {
			t.Errorf("component type %q has no fixture in componentLabelFixtures", typ)
			continue
		}
		t.Run(typ, func(t *testing.T) {
			props := fx.props
			if fx.propsFor != nil {
				props = fx.propsFor(t)
			}
			kind, _ := provider.ComponentObject()

			plain := renderLabelInvariant(t, objectNameApp(component, typ, props, ""))
			if got := docsOfKind(plain, kind.Kind, kind.Group); !slices.Contains(got, component) {
				t.Fatalf("without objectName the %s objects are %v, want one named after the component %q", kind, got, component)
			}

			docs := renderLabelInvariant(t, objectNameApp(component, typ, props, renamed))
			got := docsOfKind(docs, kind.Kind, kind.Group)
			if !slices.Contains(got, renamed) {
				t.Errorf("with objectName the %s objects are %v, want one named %q", kind, got, renamed)
			}
			if slices.Contains(got, component) {
				t.Errorf("with objectName a %s is still named after the component: %v", kind, got)
			}
			if len(docs) != len(plain) {
				t.Errorf("objectName changed the number of emitted objects from %d to %d", len(plain), len(docs))
			}
			n := checkComponentLabelInvariant(t, docs, component)
			if fx.labelled {
				requireAppLabel(t, n, typ+" with objectName")
			}
			if n.selectors < fx.selectors {
				t.Errorf("%s with objectName matched %d pod selectors against its pod template, want at least %d", typ, n.selectors, fx.selectors)
			}
		})
	}
}

// TestObjectName_HandlerDrivenDirectly hands every kind handler `objectName`
// directly, outside a transform, where no engine reads the property off the
// component first. No handler names its object with it. A handler that reads
// the fields it knows passes over the property, and the object keeps the
// component name (`configmap`, `service`, `deployment`); one that decodes its
// properties strictly refuses it as a field it does not know (`namespace`,
// `helmrelease`, `cnpg-cluster`). Both are met among the built-in kinds.
func TestObjectName_HandlerDrivenDirectly(t *testing.T) {
	const component = "widget"
	handlers := builtinComponentHandlers()
	var refuses, passesOver []string
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		if _, declares := handlers[typ].(oam.ComponentObjectProvider); !declares {
			continue
		}
		fx, has := componentLabelFixtures[typ]
		if !has {
			t.Errorf("component type %q has no fixture in componentLabelFixtures", typ)
			continue
		}
		props := fx.props
		if fx.propsFor != nil {
			props = fx.propsFor(t)
		}
		direct := func(props map[string]any) ([]string, error) {
			cfg, err := handlers[typ].ToApplicationConfig(&oam.Component{Name: component, Type: typ, Properties: props}, "default")
			if err != nil {
				return nil, err
			}
			objs, err := cfg.Generate(stack.NewApplication(component, "default", cfg))
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(objs))
			for _, obj := range objs {
				names = append(names, (*obj).GetName())
			}
			return names, nil
		}

		if names, err := direct(maps.Clone(props)); err != nil || !slices.Contains(names, component) {
			t.Errorf("%s driven directly without objectName: objects %v, err %v; want one named %q", typ, names, err, component)
			continue
		}
		withName := maps.Clone(props)
		if withName == nil {
			withName = map[string]any{}
		}
		withName[oam.ObjectNameProperty] = "other"
		names, err := direct(withName)
		switch {
		case err != nil && strings.Contains(err.Error(), oam.ObjectNameProperty):
			refuses = append(refuses, typ)
		case err != nil:
			t.Errorf("%s driven directly with objectName: %v; want the property passed over or refused by name", typ, err)
		case slices.Contains(names, "other") || !slices.Contains(names, component):
			t.Errorf("%s driven directly with objectName names its objects %v; want the component name %q kept", typ, names, component)
		default:
			passesOver = append(passesOver, typ)
		}
	}
	// The three named on each side in the README and in this test's comment.
	for _, typ := range []string{"configmap", "service", "deployment"} {
		if !slices.Contains(passesOver, typ) {
			t.Errorf("%s no longer passes over objectName when driven directly (passing over: %v); the oam README names it", typ, passesOver)
		}
	}
	for _, typ := range []string{"namespace", "helmrelease", "cnpg-cluster"} {
		if !slices.Contains(refuses, typ) {
			t.Errorf("%s no longer refuses objectName when driven directly (refusing: %v); the oam README names it", typ, refuses)
		}
	}
}

// TestObjectName_TwoKindsShareAName: two components cannot share a component
// name, and their objects can share an object name where their kinds differ: a
// Service named like its StatefulSet is a `service` component given the
// StatefulSet's name as its `objectName`.
func TestObjectName_TwoKindsShareAName(t *testing.T) {
	fx := componentLabelFixtures["statefulset"]
	props := fx.props
	if fx.propsFor != nil {
		props = fx.propsFor(t)
	}
	app := labelInvariantApp("db", "statefulset", props, "", nil)
	spec := app["spec"].(map[string]any)
	spec["components"] = append(spec["components"].([]any), map[string]any{
		"name": "db-svc",
		"type": "service",
		"properties": map[string]any{
			oam.ObjectNameProperty: "db",
			"selector":             map[string]any{"app": "db"},
			"ports":                []any{map[string]any{"port": 5432}},
		},
	})
	docs := renderLabelInvariant(t, app)
	for kind, group := range map[string]string{"StatefulSet": "apps", "Service": ""} {
		if got := docsOfKind(docs, kind, group); !slices.Contains(got, "db") {
			t.Errorf("the %s objects are %v, want one named \"db\"", kind, got)
		}
	}
}

// TestObjectName_ScalerTraitAndScalingKindsShareOneNameSpace: the `scaler`
// trait's HorizontalPodAutoscaler and PodDisruptionBudget are claimed as the
// kinds the `horizontalpodautoscaler` and `poddisruptionbudget` components
// declare, so one name given to both in one namespace is refused. The default
// names (`<component>-hpa`, `<component>-pdb`) keep the two apart, which the
// last case builds.
func TestObjectName_ScalerTraitAndScalingKindsShareOneNameSpace(t *testing.T) {
	web := componentLabelFixtures["webservice"]
	scaler := map[string]any{"minReplicas": 2, "maxReplicas": 3, "enablePDB": true}
	cases := []struct {
		name, typ, objectName string
		trait                 map[string]any
		want                  string
	}{
		{name: "autoscaler", typ: "horizontalpodautoscaler", objectName: "shared",
			trait: map[string]any{"hpaName": "shared"},
			want:  `name collision: HorizontalPodAutoscaler.autoscaling "default/shared"`},
		{name: "budget", typ: "poddisruptionbudget", objectName: "shared",
			trait: map[string]any{"pdbName": "shared"},
			want:  `name collision: PodDisruptionBudget.policy "default/shared"`},
		{name: "autoscaler named like the trait's default", typ: "horizontalpodautoscaler", objectName: "web-hpa",
			want: `name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa"`},
		{name: "default names", typ: "horizontalpodautoscaler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			traitProps := maps.Clone(scaler)
			maps.Copy(traitProps, tc.trait)
			app := labelInvariantApp("web", "webservice", web.props, "scaler", traitProps)
			spec := app["spec"].(map[string]any)
			kind := objectNameApp("scaling", tc.typ, componentLabelFixtures[tc.typ].props, tc.objectName)
			spec["components"] = append(spec["components"].([]any), kind["spec"].(map[string]any)["components"].([]any)...)
			docs, err := renderLabelInvariantErr(t, app)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("kurel build failed: %v", err)
				}
				got := docsOfKind(docs, "HorizontalPodAutoscaler", "autoscaling")
				if want := []string{"scaling", "web-hpa"}; !slices.Equal(got, want) {
					t.Errorf("the HorizontalPodAutoscaler objects are %v, want %v", got, want)
				}
				return
			}
			if err == nil {
				t.Fatalf("a %s component and a scaler trait built with one object name; want the collision refused", tc.typ)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused with %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestObjectName_RefusedWhereNoObjectIsDeclared: a component type that declares
// no object refuses `objectName`, naming the property.
func TestObjectName_RefusedWhereNoObjectIsDeclared(t *testing.T) {
	for _, typ := range slices.Sorted(maps.Keys(noObjectNameTypes)) {
		t.Run(typ, func(t *testing.T) {
			fx := componentLabelFixtures[typ]
			props := fx.props
			if fx.propsFor != nil {
				props = fx.propsFor(t)
			}
			_, err := renderLabelInvariantErr(t, objectNameApp("widget", typ, props, "renamed-widget"))
			if err == nil {
				t.Fatalf("%s accepted objectName; want it refused", typ)
			}
			if !strings.Contains(err.Error(), oam.ObjectNameProperty) {
				t.Errorf("%s refused objectName with %q, want the property named", typ, err)
			}
		})
	}
}
