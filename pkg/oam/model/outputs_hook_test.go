package model_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// hookedRule is a lowering rule, not a kind component, that names an object for the role object
// through ResolveName, so the Naming hook is asked for it in its lowering and in its query.
type hookedRule struct{}

var hookedKind = schema.GroupKind{Kind: "ConfigMap"}

func (hookedRule) ComponentType() string { return "hookeddb" }

func (hookedRule) LowerComponent(c *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	if _, err := lctx.ResolveName(c.Name, "s", oam.NameSpec{Role: oam.NameRoleObject, Kind: hookedKind}); err != nil {
		return oam.LoweringResult{}, err
	}
	return oam.LoweringResult{Components: []oam.Component{{Name: c.Name + "-cfg", Type: "configmap@v1", Properties: map[string]any{}}}}, nil
}

func (hookedRule) Outputs(c *oam.Component, lctx oam.LoweringContext) ([]oam.OutputDecl, error) {
	name, err := lctx.ResolveName(c.Name, "s", oam.NameSpec{Role: oam.NameRoleObject, Kind: hookedKind})
	if err != nil {
		return nil, err
	}
	return []oam.OutputDecl{{Key: "s", Role: oam.NameRoleObject, Kind: hookedKind, Default: c.Name + "-s", Name: name}}, nil
}

// TestRule_AModelPublishesTheRoleObjectOfARulePartUnderTheHook: a part that is itself a lowering
// rule gets the whole Naming hook, the role object included, so a model that reads and publishes
// the name that rule resolves gives the name the build gives, and the build holds the two equal.
func TestRule_AModelPublishesTheRoleObjectOfARulePartUnderTheHook(t *testing.T) {
	tr := expandTransformer(t)
	tr.RegisterComponentLowering(hookedRule{})
	if err := tr.DeclareComponentTypes(map[string]oam.TypeID{"hookeddb": {Name: "hookeddb", Version: "v1"}}); err != nil {
		t.Fatalf("DeclareComponentTypes: %v", err)
	}
	reg := model.NewRegistry(tr)
	for _, doc := range []string{
		outputsDoc("webapp.acme.example", "  outputs:\n    s: \"${ parts.x.s.name }\"\n", `
    - id: x
      type: hookeddb@v1
      properties: {}
    - id: cfg
      type: configmap@v1
      properties:
        data:
          s: "${ parts.x.s.name }"
`),
	} {
		m, err := model.Load([]byte(doc))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	objs := objectsOK(t, tr, []oam.Component{{Name: "orders", Type: webappType}}, hookedNaming(oam.NameRoleObject))

	cms := names(objs, "ConfigMap")
	var read any
	for _, n := range cms {
		if data, ok := objs[[2]string{"ConfigMap", n}]["data"].(map[string]any); ok {
			if v, ok := data["s"]; ok {
				read = v
			}
		}
	}
	if read != "h-orders-x-s" {
		t.Fatalf("the ConfigMap reads %v, want the name the hook gave the rule part, h-orders-x-s (ConfigMaps: %v)", read, cms)
	}
}
