package oam_test

import (
	"fmt"
	"log"
	"sort"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// The library path `kurel build` takes, in order: register the builtins and
// declare their type identities, parse the Application and the ClusterProfile,
// evaluate the profile, validate the authored properties, transform, generate,
// and check the generated objects against each other. A bare Transform skips
// the validation and the collision check. The site's library quickstart quotes
// this function, so change the two together.
func ExampleTransformer_Transform() {
	appYAML := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
`)
	profileYAML := []byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: staging
spec: {}
`)

	// Register every builtin, then declare the versioned type identities.
	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		t.RegisterComponentLowering(r)
	}
	for name, h := range registry.TraitHandlers() {
		t.RegisterBuiltinTrait(name, h)
	}
	for name, h := range registry.PolicyHandlers() {
		t.RegisterPolicy(name, h)
	}
	for _, r := range registry.TraitLoweringRules() {
		t.RegisterBuiltinTraitLowering(r)
	}
	if err := registry.DeclareTypes(t); err != nil {
		log.Fatal(err)
	}

	// Parse the two inputs and evaluate the profile.
	app, err := oam.ParseWithExtraTypes(appYAML, nil, t.LowerableTypes())
	if err != nil {
		log.Fatal(err)
	}
	profile, err := oam.ParseClusterProfile(profileYAML)
	if err != nil {
		log.Fatal(err)
	}
	evaluated, err := t.EvaluateProfile(profile)
	if err != nil {
		log.Fatal(err)
	}

	// Validate the authored properties. Transform takes the copy this returns.
	var target *oam.KubernetesVersion
	if v, ok := evaluated.Spec.TargetVersion(); ok {
		target = &v
	}
	validated, err := t.ValidateAuthoredPropertiesForTarget(app, evaluated.Spec.Capabilities, target)
	if err != nil {
		log.Fatal(err)
	}

	// Transform, generate the objects and check them against each other.
	cluster, err := t.Transform(validated, oam.TransformContext{
		ClusterID:    "local",
		Capabilities: evaluated.Spec.Capabilities,
	})
	if err != nil {
		log.Fatal(err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		log.Fatal(err)
	}
	if err := oam.CheckInDocumentCollisions(apps); err != nil {
		log.Fatal(err)
	}

	var generated []string
	for _, a := range apps {
		for _, o := range a.Objects {
			generated = append(generated, (*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName())
		}
	}
	sort.Strings(generated)
	for _, g := range generated {
		fmt.Println(g)
	}

	// Output:
	// Deployment/web
	// Service/web
	// ServiceAccount/web
}
