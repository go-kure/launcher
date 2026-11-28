package definition_test

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-kure/launcher/pkg/oam/definition"
)

// exampleFS holds the example's definition and CRD as an in-memory tree, the definition at
// def and the CRD where the definition's own path names it, relative to def's directory.
func exampleFS(t *testing.T, def, crd string) fstest.MapFS {
	t.Helper()
	read := func(name string) []byte {
		data, err := os.ReadFile(exampleDefinitions + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return fstest.MapFS{
		def: {Data: read("batchjob.yaml")},
		crd: {Data: read("crds/batchjob.yaml")},
	}
}

// TestLoadFS loads a definition and its CRD from a file system, as the shipped ones are,
// the CRD read relative to the definition's own directory.
func TestLoadFS(t *testing.T) {
	for _, tt := range []struct{ name, def, crd string }{
		{"root", "batchjob.yaml", "crds/batchjob.yaml"},
		{"subdirectory", "shipped/batchjob.yaml", "shipped/crds/batchjob.yaml"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, err := definition.LoadFS(exampleFS(t, tt.def, tt.crd), tt.def)
			if err != nil {
				t.Fatal(err)
			}
			if h.TypeName() != "batch-job" {
				t.Errorf("TypeName = %q", h.TypeName())
			}
		})
	}
}

// TestLoadFS_Refusals: the checks of Load hold for a file system, the digest first.
func TestLoadFS_Refusals(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(fsys fstest.MapFS)
		wants []string
	}{
		{"changed CRD", func(f fstest.MapFS) { f["crds/batchjob.yaml"].Data = append(f["crds/batchjob.yaml"].Data, '#') },
			[]string{"sha256", `"batchjob.yaml"`}},
		{"missing CRD", func(f fstest.MapFS) { delete(f, "crds/batchjob.yaml") },
			[]string{"reading the pinned CRD"}},
		{"CRD path that leaves the directory", func(f fstest.MapFS) {
			f["batchjob.yaml"].Data = []byte(strings.Replace(string(f["batchjob.yaml"].Data), "crd: crds/batchjob.yaml", "crd: ../crds/batchjob.yaml", 1))
		}, []string{"must be a relative path inside the definition's directory"}},
		{"missing definition", func(f fstest.MapFS) { delete(f, "batchjob.yaml") },
			[]string{"reading component definition"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := exampleFS(t, "batchjob.yaml", "crds/batchjob.yaml")
			tt.edit(fsys)
			_, err := definition.LoadFS(fsys, "batchjob.yaml")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			for _, w := range tt.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal does not hold %q: %v", w, err)
				}
			}
		})
	}
}
