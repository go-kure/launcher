package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// A config built at a level keeps it for Generate, which repeats the parse-time
// refusals because the config is exported: a config built from a component at
// `any` with a :latest image generates, and one built from a component with no
// level refuses it (go-kure/launcher#1644).
func TestImageLevel_GenerateKeepsTheLevelOfTheParse(t *testing.T) {
	labels := map[string]any{"app": "web"}
	template := func(spec map[string]any) map[string]any {
		return map[string]any{"metadata": map[string]any{"labels": labels}, "spec": spec}
	}
	selector := map[string]any{"matchLabels": labels}
	spec := func(extra map[string]any) map[string]any { return ilPodSpec("container", ilLatest, extra) }
	cases := []struct {
		typ   string
		props map[string]any
	}{
		{"pod", spec(nil)},
		{"podtemplate", map[string]any{"template": template(spec(nil))}},
		{"deployment", map[string]any{"selector": selector, "template": template(spec(nil))}},
		{"daemonset", map[string]any{"selector": selector, "template": template(spec(nil))}},
		{"statefulset", map[string]any{"selector": selector, "template": template(spec(nil))}},
		{"replicaset", map[string]any{"selector": selector, "template": template(spec(nil))}},
		{"replicationcontroller", map[string]any{"selector": labels, "template": template(spec(nil))}},
		{"job", map[string]any{"template": template(spec(map[string]any{"restartPolicy": "OnFailure"}))}},
		{"cronjob", map[string]any{"schedule": "* * * * *", "jobTemplate": map[string]any{"spec": map[string]any{
			"template": template(spec(map[string]any{"restartPolicy": "OnFailure"}))}}}},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			h := registry.ComponentHandlers()[tc.typ]
			comp := oam.Component{Name: "web", Type: tc.typ, Properties: tc.props}
			if _, err := h.ToApplicationConfig(&comp, "test"); err == nil {
				t.Fatal("a component with no level is built with :latest, want it refused")
			}
			at := comp.WithImageLevel(oam.ImageLevelAny)
			cfg, err := h.ToApplicationConfig(&at, "test")
			if err != nil {
				t.Fatalf("ToApplicationConfig at any: %v", err)
			}
			if _, err := cfg.Generate(&stack.Application{Name: "web", Namespace: "test"}); err != nil {
				t.Errorf("Generate of a config built at any refuses it: %v", err)
			}
		})
	}
}
