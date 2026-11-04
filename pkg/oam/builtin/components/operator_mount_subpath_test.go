package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// operatorMountKind is one of the components that copy volumeMounts to the
// pods of an operator-managed object, with the mount path the operator mounts
// the data volume at in that container.
type operatorMountKind struct {
	typ      string
	handler  oam.ComponentHandler
	dataPath string
}

var operatorMountKinds = []operatorMountKind{
	{"alertmanager", &components.AlertmanagerHandler{}, "/alertmanager"},
	{"prometheus", &components.PrometheusHandler{}, "/prometheus"},
	{"thanosruler", &components.ThanosRulerHandler{}, "/thanos/data"},
}

// operatorMountProps is a component of one emptyDir volume, mounted at path
// with the extra fields of the mount.
func operatorMountProps(path string, extra map[string]any) map[string]any {
	mount := map[string]any{"name": "extra", "mountPath": path}
	for k, v := range extra {
		mount[k] = v
	}
	return map[string]any{
		"volumes":      []any{map[string]any{"name": "extra", "emptyDir": map[string]any{}}},
		"volumeMounts": []any{mount},
	}
}

func operatorMountBuild(k operatorMountKind, props map[string]any) error {
	_, err := policyFreeTransform(k.typ, k.handler, nil, oam.Component{Name: "web", Properties: props})
	return err
}

// TestOperatorMounts_RefuseDescendingPathBreaks: the operator copies an entry
// of volumeMounts to the container as written, and the API refuses a subPath or
// a subPathExpr that is absolute or holds a ".." element
// (validateLocalDescendingPath and validatePathNoBacksteps, ValidateVolumeMounts,
// pkg/apis/core/validation/validation.go:3205-3214 at Kubernetes v1.37.1).
func TestOperatorMounts_RefuseDescendingPathBreaks(t *testing.T) {
	for _, k := range operatorMountKinds {
		for _, field := range []string{"subPath", "subPathExpr"} {
			for _, tc := range []struct{ value, want string }{
				{"/abs", `must be a relative path, got "/abs"`},
				{"a/../b", `must not contain a '..' element, got "a/../b"`},
				{"..", `must not contain a '..' element, got ".."`},
				{"../x", `must not contain a '..' element, got "../x"`},
			} {
				t.Run(k.typ+"/"+field+"/"+tc.value, func(t *testing.T) {
					err := operatorMountBuild(k, operatorMountProps("/extra", map[string]any{field: tc.value}))
					want := "volumeMounts[0]." + field + ": " + tc.want
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want one containing %q", err, want)
					}
				})
			}
		}
	}
}

// TestOperatorMounts_AcceptDescendingPaths: a relative path with no ".."
// element is the API's to accept, whatever else it holds.
func TestOperatorMounts_AcceptDescendingPaths(t *testing.T) {
	for _, k := range operatorMountKinds {
		for _, tc := range []struct {
			name  string
			extra map[string]any
		}{
			{"subPath a/b", map[string]any{"subPath": "a/b"}},
			{"subPath ./a", map[string]any{"subPath": "./a"}},
			{"subPath a..b", map[string]any{"subPath": "a..b"}},
			{"subPath a..b/..c", map[string]any{"subPath": "a..b/..c"}},
			{"subPath empty", map[string]any{"subPath": ""}},
			{"subPathExpr a/b", map[string]any{"subPathExpr": "a/b"}},
			{"subPathExpr a variable", map[string]any{"subPathExpr": "$(POD_NAME)"}},
			{"subPathExpr a..b", map[string]any{"subPathExpr": "a..b"}},
			{"no sub-path", nil},
		} {
			t.Run(k.typ+"/"+tc.name, func(t *testing.T) {
				if err := operatorMountBuild(k, operatorMountProps("/extra", tc.extra)); err != nil {
					t.Errorf("err = %v, want none", err)
				}
			})
		}
	}
}

// TestOperatorMounts_DescendingPathBeforePathClash: an entry that breaks both
// rules is refused for its sub-path, the first thing read of it, as the
// entries of the other volumeMounts are.
func TestOperatorMounts_DescendingPathBeforePathClash(t *testing.T) {
	for _, k := range operatorMountKinds {
		t.Run(k.typ, func(t *testing.T) {
			err := operatorMountBuild(k, operatorMountProps(k.dataPath, map[string]any{"subPath": "/abs"}))
			want := `volumeMounts[0].subPath: must be a relative path, got "/abs"`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one containing %q", err, want)
			}
		})
	}
}

// TestPrometheus_ThanosSidecarSubPathStaysNotCarried: the operator drops a
// sub-path of thanos.volumeMounts, and that refusal is the one a bad sub-path
// gets there.
func TestPrometheus_ThanosSidecarSubPathStaysNotCarried(t *testing.T) {
	k := operatorMountKinds[1]
	for _, field := range []string{"subPath", "subPathExpr"} {
		t.Run(field, func(t *testing.T) {
			props := map[string]any{"thanos": map[string]any{
				"volumeMounts": []any{map[string]any{"name": "extra", "mountPath": "/extra", field: "/abs"}},
			}}
			err := operatorMountBuild(k, props)
			want := "thanos.volumeMounts[0]." + field + ": not carried"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one containing %q", err, want)
			}
		})
	}
}
