package traits_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// A sub-application's name is not its object's name (go-kure/launcher#787): a
// Naming hook that renames only sub-applications changes no generated object.
// These traits name their object apart from the sub-application; four of them
// used to name it after the sub-application.
func TestNamingHook_SubApplicationRenameLeavesObjectNames(t *testing.T) {
	app := func() *oam.Application {
		return &oam.Application{
			APIVersion: oam.SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
			Spec: oam.ApplicationSpec{Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{
					{Type: "configmap", Properties: map[string]any{"name": "web-config", "data": map[string]any{"a": "1"}}},
					{Type: "ingress", Properties: map[string]any{
						"rules": []any{map[string]any{"host": "example.com", "paths": []any{map[string]any{"path": "/"}}}},
					}},
					{Type: "httproute", Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules":      []any{map[string]any{}},
					}},
					{Type: "volsync", Properties: map[string]any{"sourcePVC": "data", "schedule": "@daily", "copyMethod": "Clone"}},
					{Type: "secret", Properties: map[string]any{"name": "web-creds", "stringData": map[string]any{"a": "1"}}},
				},
			}}},
		}
	}
	run := func(hook func(oam.NameRequest) (string, bool)) (subApps, objects []string) {
		t.Helper()
		tr := oam.NewTransformer(nil, nil)
		registerWebservice(tr)
		tr.RegisterBuiltinTrait("configmap", &traits.ConfigMapHandler{})
		tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
		tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
		tr.RegisterBuiltinTrait("volsync", &traits.VolSyncHandler{})
		tr.RegisterBuiltinTrait("secret", &traits.SecretHandler{})
		cluster, err := tr.Transform(app(), oam.TransformContext{
			Namespace: "default",
			Naming:    hook,
			Capabilities: map[string]oam.CapabilityBinding{
				"volsync": {Rendering: map[string]any{"storageClassName": "fast"}},
			},
		})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		generated, err := oam.GenerateApplications(cluster)
		if err != nil {
			t.Fatalf("GenerateApplications: %v", err)
		}
		for _, a := range generated {
			subApps = append(subApps, a.Name)
			for _, p := range a.Objects {
				if p == nil {
					continue
				}
				obj := *p
				objects = append(objects, fmt.Sprintf("%s %s/%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName()))
			}
		}
		return subApps, objects
	}

	plainApps, plainObjects := run(nil)
	renamedApps, renamedObjects := run(func(req oam.NameRequest) (string, bool) {
		if req.Role != oam.NameRoleSubApplication {
			return "", false
		}
		return "renamed-" + req.Default, true
	})

	for _, kind := range []string{"ConfigMap", "Ingress", "HTTPRoute", "ReplicationSource", "Secret"} {
		if !slices.ContainsFunc(plainObjects, func(o string) bool { return strings.HasPrefix(o, kind+" ") }) {
			t.Fatalf("no %s generated: the test covers nothing for it; objects: %v", kind, plainObjects)
		}
	}
	if !slices.Equal(plainObjects, renamedObjects) {
		t.Errorf("renaming the sub-applications changed the objects:\nwithout: %v\nwith:    %v", plainObjects, renamedObjects)
	}
	var renamed int
	for _, name := range renamedApps {
		if strings.HasPrefix(name, "renamed-") {
			renamed++
		}
	}
	if renamed != 5 {
		t.Errorf("%d sub-applications carry the hook's name, want the 5 traits': %v (without the hook: %v)", renamed, renamedApps, plainApps)
	}
}
