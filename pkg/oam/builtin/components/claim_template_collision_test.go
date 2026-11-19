package components_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// rawClaimTemplate is one raw `volumeClaimTemplates` entry: an upstream
// PersistentVolumeClaim with a name and a storage request.
func rawClaimTemplate(name string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": "1Gi"}},
		},
	}
}

// TestRawStatefulset_ClaimTemplateNamesCollide: a claim template shares the
// pod's volume namespace with `template.spec.volumes`, and the apiserver does
// not catch an overlap: StatefulSet validation skips the pod template's
// volumes. It surfaces only in the controller, which keys the templates by
// name, so a repeated name keeps one claim and drops the other, and replaces a
// pod volume named like a template with that template's claim. The raw kind
// (go-kure/launcher#1629) refuses both at parse time. A repeated mountPath is
// not checked any more: the API server refuses it when the pod is created.
func TestRawStatefulset_ClaimTemplateNamesCollide(t *testing.T) {
	h := &components.StatefulsetHandler{}
	build := func(claims []any, volumes []any) map[string]any {
		props := rawStatefulsetProps("ghcr.io/org/app:v1")
		props["volumeClaimTemplates"] = claims
		if volumes != nil {
			props["template"].(map[string]any)["spec"].(map[string]any)["volumes"] = volumes
		}
		return props
	}
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"two claim templates with one name",
			build([]any{rawClaimTemplate("data"), rawClaimTemplate("data")}, nil),
			`volumeClaimTemplates: duplicate name "data"`},
		{"claim template and an emptyDir volume",
			build([]any{rawClaimTemplate("data")}, []any{map[string]any{"name": "data", "emptyDir": map[string]any{}}}),
			`template.spec.volumes: volume "data" has the same name as a claim template`},
		{"claim template and a pvc volume",
			build([]any{rawClaimTemplate("data")}, []any{map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "shared-data"}}}),
			`template.spec.volumes: volume "data" has the same name as a claim template`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: "statefulset", Properties: tc.props}, "default")
			wantErrContaining(t, err, tc.wantErr)
		})
	}

	t.Run("distinct names still build", func(t *testing.T) {
		props := build([]any{rawClaimTemplate("data"), rawClaimTemplate("logs")}, []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}})
		objects := buildComponent(t, h, "app", "statefulset", props)
		sts, ok := (*objects[0]).(*appsv1.StatefulSet)
		if !ok {
			t.Fatalf("first object is %T, want a StatefulSet", *objects[0])
		}
		if len(sts.Spec.VolumeClaimTemplates) != 2 || len(sts.Spec.Template.Spec.Volumes) != 1 {
			t.Errorf("got %d claim templates, %d volumes; want 2, 1",
				len(sts.Spec.VolumeClaimTemplates), len(sts.Spec.Template.Spec.Volumes))
		}
	})
}
