package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// A trait the author writes on a component of a model type is refused, because the rule of a model
// has no part to give it to and would drop it without a word (go-kure/launcher#1528). A trait that
// an outer model writes in its own `traits:` on a part that is itself a model is not authored, and
// still builds.

const innerModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: inner.acme.example
  version: v1
spec:
  celEnv: "1"
  parts:
    - id: web
      type: deployment.apps@v1
      properties:
        image: nginx:1.27
`

const outerModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: outer.acme.example
  version: v1
spec:
  celEnv: "1"
  parts:
    - id: app
      type: inner.acme.example@v1
      properties: {}
      traits:
        - type: topology-spread.launcher.gokure.dev@v1
`

func modelComponent(typ string, traits ...oam.Trait) []oam.Component {
	return []oam.Component{{Name: "orders", Type: typ, Properties: map[string]any{}, Traits: traits}}
}

func requireTraitRefusal(t *testing.T, err error, trait string) {
	t.Helper()
	if err == nil {
		t.Fatal("Transform accepted an authored trait on a model component")
	}
	if !errors.Is(err, model.ErrTraitsUnsupported) {
		t.Errorf("the refusal does not wrap ErrTraitsUnsupported: %v", err)
	}
	for _, want := range []string{`"orders"`, "inner.acme.example@v1", `"` + trait + `"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

func TestModelComponent_RefusesAnAuthoredTrait(t *testing.T) {
	tr, _ := registered(t, innerModel)
	_, err := objects(t, tr, modelComponent("inner.acme.example@v1", oam.Trait{Type: "topology-spread"}), nil)
	requireTraitRefusal(t, err, "topology-spread")
}

// A trait with a property the trait refuses is refused as an authored trait, and the trait's own
// refusal never shows: the build never asks the trait.
func TestModelComponent_RefusesAnAuthoredTraitWithABadProperty(t *testing.T) {
	tr, _ := registered(t, innerModel)
	_, err := objects(t, tr, modelComponent("inner.acme.example@v1",
		oam.Trait{Type: "topology-spread", Properties: map[string]any{"bogus": 1}}), nil)
	requireTraitRefusal(t, err, "topology-spread")
}

func TestModelComponent_WithNoTraitBuilds(t *testing.T) {
	tr, _ := registered(t, innerModel)
	objs := objectsOK(t, tr, modelComponent("inner.acme.example@v1"), nil)
	if _, ok := objs[[2]string{"Deployment", "orders-web"}]; !ok {
		t.Errorf("no Deployment orders-web in %v", objs)
	}
}

// The traits of an outer model's part are the outer author's own, on a component that the outer
// rule emitted. The inner model's rule sees them and must not refuse them.
func TestModelComponent_OuterModelTraitsOnAModelPartStillBuild(t *testing.T) {
	tr, reg := registered(t, innerModel)
	m, err := model.Load([]byte(outerModel))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	objs := objectsOK(t, tr, modelComponent("outer.acme.example@v1"), nil)
	if len(objs) == 0 {
		t.Error("the outer model built no object")
	}
}
