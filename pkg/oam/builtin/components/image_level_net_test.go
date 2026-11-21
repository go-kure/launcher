package components_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// The net of go-kure/launcher#1644: every place an image reference is parsed
// holds it to the level of the policy, and to the default level where no policy
// is passed. These tests run each place under three policies: none, `any`
// (which must loosen it) and `digest` (which must tighten it, whatever the
// place implements).

// ilPolicy is a Policy that names the image level and nothing else.
type ilPolicy struct {
	*oam.NoopPolicy
	level oam.ImageReferenceLevel
}

func (p ilPolicy) ImageReferenceLevel() oam.ImageReferenceLevel { return p.level }

// ilNet is the three policies, with what each does to the references the net
// uses: latest, a plain version tag and a digest.
var ilNet = []struct {
	name                       string
	policy                     oam.Policy
	latest, versionTag, pinned bool // accepted?
}{
	{"no policy", nil, false, true, true},
	{"any", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelAny}, true, true, true},
	{"digest", ilPolicy{&oam.NoopPolicy{}, oam.ImageLevelDigest}, false, false, true},
}

const (
	ilLatest = "registry.example/team/app:latest"
	ilTag    = "registry.example/team/app:1.0.0"
	ilPinned = "registry.example/team/app@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// ilTransformer is the transformer a consumer builds from the registry: every
// handler, rule and policy.
func ilTransformer() *oam.Transformer {
	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
	for _, r := range registry.ComponentLoweringRules() {
		t.RegisterBuiltinComponentLowering(r)
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
	return t
}

// ilBuild transforms a one-component document under policy.
func ilBuild(typ string, props map[string]any, policy oam.Policy) error {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "net", Namespace: "test"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{{Name: "web", Type: typ, Properties: props}}},
	}
	_, err := ilTransformer().Transform(app, oam.TransformContext{Namespace: "test", Policy: policy})
	return err
}

// ilWant fails the test unless err is nil exactly when accepted, and a refusal
// names the reference.
func ilWant(t *testing.T, what, reference string, err error, accepted bool) {
	t.Helper()
	switch {
	case accepted && err != nil:
		t.Errorf("%s: %q is refused: %v", what, reference, err)
	case !accepted && err == nil:
		t.Errorf("%s: %q is accepted, want refused", what, reference)
	case !accepted && !strings.Contains(err.Error(), reference):
		t.Errorf("%s: err = %v, want one naming %q", what, err, reference)
	}
}

// ilReferences runs one place over the three policies and the three
// references.
func ilReferences(t *testing.T, place func(reference string, policy oam.Policy) error) {
	t.Helper()
	for _, p := range ilNet {
		t.Run(p.name, func(t *testing.T) {
			ilWant(t, p.name, ilLatest, place(ilLatest, p.policy), p.latest)
			ilWant(t, p.name, ilTag, place(ilTag, p.policy), p.versionTag)
			ilWant(t, p.name, ilPinned, place(ilPinned, p.policy), p.pinned)
		})
	}
}

// The lowering rules: webservice and worker take an authored image, and parse it
// in three entries (LowerComponent, Outputs, Endpoints) before any handler runs.
func TestImageLevel_LoweringRules(t *testing.T) {
	for _, rule := range []struct {
		typ   string
		props func(image string) map[string]any
	}{
		{"webservice", func(image string) map[string]any { return map[string]any{"image": image, "port": 80} }},
		{"worker", func(image string) map[string]any { return map[string]any{"image": image} }},
	} {
		t.Run(rule.typ+" built by the transformer", func(t *testing.T) {
			ilReferences(t, func(reference string, policy oam.Policy) error {
				return ilBuild(rule.typ, rule.props(reference), policy)
			})
		})
	}

	// Every other place the role kinds author an image: the init containers,
	// the sidecars and an image volume, on the main image pinned by digest so
	// that only the field under test can fail. The role-deployment member each
	// rule lowers to is reached through them, so its handler is covered here.
	for _, typ := range []string{"webservice", "worker"} {
		for _, field := range []struct {
			name  string
			props func(image string) map[string]any
		}{
			{"initContainers", func(image string) map[string]any {
				return map[string]any{"initContainers": []any{map[string]any{"name": "i", "image": image}}}
			}},
			{"sidecars", func(image string) map[string]any {
				return map[string]any{"sidecars": []any{map[string]any{"name": "s", "image": image}}}
			}},
			{"image volume", func(image string) map[string]any {
				return map[string]any{"volumes": []any{map[string]any{
					"name": "v", "type": "image", "mountPath": "/v", "image": map[string]any{"reference": image},
				}}}
			}},
		} {
			t.Run(typ+" "+field.name, func(t *testing.T) {
				ilReferences(t, func(reference string, policy oam.Policy) error {
					props := field.props(reference)
					props["image"] = ilPinned
					if typ == "webservice" {
						props["port"] = 80
					}
					return ilBuild(typ, props, policy)
				})
			})
		}
	}

	comp := func(typ, image string) *oam.Component {
		props := map[string]any{"image": image}
		if typ == "webservice" {
			props["port"] = 80
		}
		return &oam.Component{Name: "web", Type: typ, Properties: props}
	}
	lctx := func() oam.LoweringContext { return oam.LoweringContext{Namer: oam.NewNameAllocator()} }

	for _, typ := range []string{"webservice", "worker"} {
		var rule interface {
			LowerComponent(*oam.Component, oam.LoweringContext) (oam.LoweringResult, error)
			Outputs(*oam.Component, oam.LoweringContext) ([]oam.OutputDecl, error)
		}
		if typ == "webservice" {
			rule = components.WebserviceRule{}
		} else {
			rule = components.WorkerRule{}
		}
		t.Run(typ+" LowerComponent with no context keeps the default level", func(t *testing.T) {
			_, err := rule.LowerComponent(comp(typ, ilLatest), lctx())
			ilWant(t, "LowerComponent", ilLatest, err, false)
			_, err = rule.LowerComponent(comp(typ, ilTag), lctx())
			ilWant(t, "LowerComponent", ilTag, err, true)
		})
		t.Run(typ+" LowerComponent follows the level of its context", func(t *testing.T) {
			_, err := rule.LowerComponent(comp(typ, ilLatest), lctx().WithImageLevel(oam.ImageLevelAny))
			ilWant(t, "LowerComponent at any", ilLatest, err, true)
			_, err = rule.LowerComponent(comp(typ, ilTag), lctx().WithImageLevel(oam.ImageLevelDigest))
			ilWant(t, "LowerComponent at digest", ilTag, err, false)
		})
		t.Run(typ+" Outputs follows the level of its context", func(t *testing.T) {
			_, err := rule.Outputs(comp(typ, ilLatest), lctx())
			ilWant(t, "Outputs at the default", ilLatest, err, false)
			_, err = rule.Outputs(comp(typ, ilLatest), lctx().WithImageLevel(oam.ImageLevelAny))
			ilWant(t, "Outputs at any", ilLatest, err, true)
		})
	}

	// Endpoints reads ports and names and never emits the image: it checks the
	// reference's syntax only, and the build holds it to its level where it is
	// emitted (go-kure/launcher#1644).
	t.Run("webservice Endpoints checks syntax only", func(t *testing.T) {
		_, err := components.WebserviceRule{}.Endpoints(comp("webservice", ilLatest))
		ilWant(t, "Endpoints", ilLatest, err, true)
		if _, err := (components.WebserviceRule{}).Endpoints(comp("webservice", "registry.example/Team/app:1")); err == nil {
			t.Error("Endpoints accepts a reference the parser refuses")
		}
	})
	t.Run("the transformer's endpoint query checks syntax only", func(t *testing.T) {
		_, err := ilTransformer().ComponentEndpoints(comp("webservice", ilLatest))
		ilWant(t, "ComponentEndpoints", ilLatest, err, true)
	})
}

// ilPodSpec is a pod spec whose main image is pinned by digest, so that only the
// field under test can fail, with that field set to image: the main container
// itself, an init container, or an image volume.
func ilPodSpec(field, image string, extra map[string]any) map[string]any {
	spec := map[string]any{"containers": []any{map[string]any{"name": "c", "image": ilPinned}}}
	switch field {
	case "container":
		spec["containers"] = []any{map[string]any{"name": "c", "image": image}}
	case "init container":
		spec["initContainers"] = []any{map[string]any{"name": "i", "image": image}}
	case "image volume":
		spec["volumes"] = []any{map[string]any{"name": "v", "image": map[string]any{"reference": image}}}
	}
	maps.Copy(spec, extra)
	return spec
}

// The pod and the controller kinds hold every image of their pod spec to the
// level: pod, podtemplate, the five controllers, job and cronjob.
func TestImageLevel_PodAndControllerKinds(t *testing.T) {
	labels := map[string]any{"app": "web"}
	template := func(spec map[string]any) map[string]any {
		return map[string]any{"metadata": map[string]any{"labels": labels}, "spec": spec}
	}
	selector := map[string]any{"matchLabels": labels}
	kinds := []struct {
		typ   string
		props func(spec map[string]any) map[string]any
	}{
		{"pod", func(spec map[string]any) map[string]any { return spec }},
		{"podtemplate", func(spec map[string]any) map[string]any { return map[string]any{"template": template(spec)} }},
		{"deployment", func(spec map[string]any) map[string]any {
			return map[string]any{"selector": selector, "template": template(spec)}
		}},
		{"daemonset", func(spec map[string]any) map[string]any {
			return map[string]any{"selector": selector, "template": template(spec)}
		}},
		{"statefulset", func(spec map[string]any) map[string]any {
			return map[string]any{"selector": selector, "template": template(spec)}
		}},
		{"replicaset", func(spec map[string]any) map[string]any {
			return map[string]any{"selector": selector, "template": template(spec)}
		}},
		{"replicationcontroller", func(spec map[string]any) map[string]any {
			return map[string]any{"selector": labels, "template": template(spec)}
		}},
		{"job", func(spec map[string]any) map[string]any {
			spec["restartPolicy"] = "OnFailure"
			return map[string]any{"template": template(spec)}
		}},
		{"cronjob", func(spec map[string]any) map[string]any {
			spec["restartPolicy"] = "OnFailure"
			return map[string]any{"schedule": "* * * * *", "jobTemplate": map[string]any{"spec": map[string]any{"template": template(spec)}}}
		}},
	}
	for _, k := range kinds {
		for _, field := range []string{"container", "init container", "image volume"} {
			t.Run(k.typ+" "+field, func(t *testing.T) {
				ilReferences(t, func(reference string, policy oam.Policy) error {
					return ilBuild(k.typ, k.props(ilPodSpec(field, reference, nil)), policy)
				})
			})
		}
	}
}
