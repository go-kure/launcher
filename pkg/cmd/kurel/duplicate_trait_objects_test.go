package kurel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin go-kure/launcher#757 through kurel's own transformer: one
// component's application and its trait sub-applications must not generate two
// objects of one group, kind, namespace and name.

// collisionCheck transforms appYAML with kurel's builtin transformer, generates
// it once and returns the in-document collision check's verdict, as runBuild
// reaches it.
func collisionCheck(t *testing.T, appYAML string) ([]oam.GeneratedApplication, error) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	return apps, oam.CheckInDocumentCollisions(apps)
}

// transformRefusal transforms appYAML with kurel's builtin transformer and
// returns the transform's error: a name two authored traits claim is refused
// there (go-kure/launcher#787), before the in-document collision check.
func transformRefusal(t *testing.T, appYAML string) error {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	_, err = transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
	return err
}

// duplicateShapes are the cluster shapes: nothing ordered (flat), groups from
// placement policies, and groups from a dependency policy.
var duplicateShapes = []struct {
	name, components, policies string
}{
	{name: "flat"},
	{name: "placement", components: `
    - name: agent
      type: daemonset
      properties:
        image: ghcr.io/example/agent:v1.0.0
`, policies: `
  policies:
    - name: agent-first
      type: placement
      properties:
        component: agent
        tier: infra
    - name: web-last
      type: placement
      properties:
        component: web
        tier: apps
`},
	{name: "dependency", components: `
    - name: other
      type: webservice
      properties:
        image: ghcr.io/example/other:v1.0.0
        port: 8080
`, policies: `
  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: other
            dependsOn: [web]
`},
}

func duplicateApp(webComponent, shapeComponents, policies string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
` + webComponent + shapeComponents + policies
}

const webTwoConfigMaps = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: configmap
          properties:
            name: dup
            data:
              a: "1"
        - type: configmap
          properties:
            name: dup
            data:
              b: "2"
`

const webConfigMapAndClaim = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: configmap
          properties:
            name: dup
            data:
              a: "1"
        - type: pvc
          properties:
            name: dup
            size: 1Gi
`

func helmWeb(traits string) string {
	return `    - name: web
      type: helm
      properties:
        chart: web
        version: 1.0.0
        source:
          url: https://charts.example.com
        valuesMode: configMap
        values:
          replicaCount: 2
` + traits
}

func TestDuplicateTraitObjects_TwoAuthoredTraits(t *testing.T) {
	for _, shape := range duplicateShapes {
		t.Run(shape.name, func(t *testing.T) {
			err := transformRefusal(t, duplicateApp(webTwoConfigMaps, shape.components, shape.policies))
			want := `name collision: ConfigMap "default/dup" is named by component "web" traits[0] "configmap" (its own object, set by name) and by component "web" traits[1] "configmap" (its own object, set by name)`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
	}
}

func TestDuplicateTraitObjects_HelmValuesAgainstAuthoredTrait(t *testing.T) {
	for _, shape := range duplicateShapes {
		t.Run(shape.name, func(t *testing.T) {
			apps, err := collisionCheck(t, duplicateApp(helmWeb(""), shape.components, shape.policies))
			if err != nil {
				t.Fatalf("baseline: %v", err)
			}
			var valuesName string
			for _, a := range apps {
				for _, p := range a.Objects {
					if p == nil {
						continue
					}
					obj := *p
					if obj.GetObjectKind().GroupVersionKind().Kind == "ConfigMap" && strings.Contains(obj.GetName(), "-values-") {
						valuesName = obj.GetName()
					}
				}
			}
			if valuesName == "" {
				t.Fatal("baseline generated no helm values ConfigMap")
			}
			traits := `      traits:
        - type: configmap
          properties:
            name: ` + valuesName + `
            data:
              a: "1"
`
			err = transformRefusal(t, duplicateApp(helmWeb(traits), shape.components, shape.policies))
			// The helm rule claimed the values ConfigMap's name under its role; the
			// authored trait's claim of it is the second.
			want := fmt.Sprintf(`name collision: ConfigMap "default/%s" is named by component "web" (role "values-configmap", its default) and by component "web" traits[0] "configmap" (its own object, set by name)`,
				valuesName)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
	}
}

// A ConfigMap and a PersistentVolumeClaim both named dup are two objects, but
// each trait names its sub-application after its object, and two applications
// of one name in one bundle are refused (go-kure/launcher#787).
func TestDuplicateTraitObjects_SameNameOtherKindRefusedByApplicationName(t *testing.T) {
	for _, shape := range duplicateShapes {
		t.Run(shape.name, func(t *testing.T) {
			err := transformRefusal(t, duplicateApp(webConfigMapAndClaim, shape.components, shape.policies))
			want := `name collision: application "dup" is named by component "web" traits[0] "configmap" (role "sub-application", its default) and by ` +
				`component "web" traits[1] "pvc" (role "sub-application", its default)`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
	}
}
