---
title: "Library Quickstart"
weight: 4
---

This page builds the objects of one application from your own Go program, with the same
checks `kurel build` runs, in the same order. The program below is the `Example` in
`pkg/oam` (`ExampleTransformer_Transform`), which the test suite runs, so the code and the
output on this page are tested.

## Set up a module

```bash
mkdir hello && cd hello
go mod init example.com/hello
go get github.com/go-kure/launcher@v0.2.0-beta.5
```

Then copy the `replace` lines of launcher's `go.mod` at that version into your own
`go.mod`, as [Install](../install/#use-launcher-as-a-go-library) explains. Without them the
build fails.

Tested version: the program is the same file at `v0.2.0-beta.5` and at `main`. It needs
`registry.DeclareTypes` and the two-value `ValidateAuthoredPropertiesForTarget`. The
earlier prerelease, `v0.2.0-beta.4`, does not have them. `v0.2.0-beta.5` is a published
prerelease (see [Status words](../../#status-words)). When a newer tag exists, you can pin that tag
instead.

## The program

Save this as `main.go`:

```go
package main

import (
	"fmt"
	"log"
	"sort"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

func main() {
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
}
```

Run it with `go run .`. It prints:

```text
Deployment/web
Service/web
ServiceAccount/web
```

## What each step is for

1. **Register and declare.** `registry` returns every built-in handler and lowering rule.
   `DeclareTypes` declares identities such as `webservice@v1` that a document may author, so
   it runs after the registration.
2. **Parse.** `ParseWithExtraTypes` rejects an unknown field in the document. The
   `LowerableTypes` argument names the types a lowering rule claims.
3. **Evaluate the profile.** The profile's capability choices become the bindings the
   transform uses.
4. **Validate.** `ValidateAuthoredPropertiesForTarget` checks every authored property against
   its type's schema, plus the properties the engine reads itself (a trait's `scope`, for
   example), and returns a normalized copy. Pass that copy to `Transform`. Calling
   `Transform` on the parsed document skips this check. A profile that sets
   `spec.featureGates` needs `ValidateAuthoredPropertiesForTargetWithGates` instead, and
   `TransformContext.TargetVersion` and `FeatureGates` on the `Transform` call, so the
   overrides reach the Service name check too. `ValidateAuthoredPropertiesForTargetWithGates`
   and `FeatureGates` are not in `v0.2.0-beta.5`: pin `main` or a newer tag to use them (see the
   [OAM schemas and contract](../../api-reference/oam/schemas-and-contract/)).
5. **Transform, generate, check.** `Transform` lowers the document into a kure `Cluster`.
   `GenerateApplications` builds the objects, and `CheckInDocumentCollisions` fails if two
   applications of the document generate the same object.

Encoding the objects as YAML, writing a layout, or handing them to a delivery tool is your
program's job; launcher stops at the objects. The
[OAM Model reference](../../api-reference/oam/) documents each call, and
[Concepts](../../concepts/oam-model/) explains the model behind them.
