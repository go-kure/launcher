package components

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
)

// TestThanosRulerExcludedGroup_MatchesMarkers holds the group withExcludedGroups
// writes to the markers of the linked module's source: the kubebuilder default
// of ObjectReference.Group, and its enum, which must admit that one value and no
// other. An upstream change to either fails here, so the fill never writes a
// value the API no longer defaults to or admits.
func TestThanosRulerExcludedGroup_MatchesMarkers(t *testing.T) {
	m, read := monitoringFieldMarkers(t)["ObjectReference.Group"]
	if !read {
		t.Fatal("ObjectReference.Group has no field in the module's source")
	}
	if !m.hasDefault || m.def != thanosRulerExcludedGroup {
		t.Errorf("ObjectReference.Group defaults to %q (has a default: %v), want %q", m.def, m.hasDefault, thanosRulerExcludedGroup)
	}
	var enum []string
	found := false
	sourceStructs(t, filepath.Join(linkedModuleDir(t, monitoringModulePath), "v1"), func(typ *ast.TypeSpec, _ *ast.CommentGroup, st *ast.StructType) {
		if typ.Name.Name != "ObjectReference" {
			return
		}
		for _, field := range st.Fields.List {
			if len(field.Names) != 1 || field.Names[0].Name != "Group" || field.Doc == nil {
				continue
			}
			found = true
			for _, comment := range field.Doc.List {
				line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
				if values, ok := strings.CutPrefix(line, "+kubebuilder:validation:Enum="); ok {
					enum = strings.Split(values, ";")
				}
			}
		}
	})
	if !found {
		t.Fatal("the walk did not reach ObjectReference.Group")
	}
	if len(enum) != 1 || enum[0] != thanosRulerExcludedGroup {
		t.Errorf("ObjectReference.Group admits %q, want only %q", enum, thanosRulerExcludedGroup)
	}
}
