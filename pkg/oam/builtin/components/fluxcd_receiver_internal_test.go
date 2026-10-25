package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestReceiverDefaultAPIVersionKinds_PinsTheControllerDefaults holds the list
// to the kinds notification-controller v1.9.4 defaults an apiVersion for
// (defaultFluxAPIVersions in internal/server/receiver_handlers.go), so that a
// kind dropped or misspelled in the list fails here. This second copy is
// deliberate: a Flux upgrade that changes the defaults edits both.
func TestReceiverDefaultAPIVersionKinds_PinsTheControllerDefaults(t *testing.T) {
	want := []string{"Bucket", "GitRepository", "HelmChart", "HelmRepository", "OCIRepository", "ImageRepository", "ArtifactGenerator"}
	if !slices.Equal(receiverDefaultAPIVersionKinds, want) {
		t.Errorf("receiverDefaultAPIVersionKinds = %v, want %v", receiverDefaultAPIVersionKinds, want)
	}
}

// TestReceiverDefaultAPIVersionKinds_AcceptedUnversioned holds every kind of
// receiverDefaultAPIVersionKinds to being accepted without an apiVersion, so
// that a kind added to the list is exercised with no second edit
// (go-kure/launcher#984).
func TestReceiverDefaultAPIVersionKinds_AcceptedUnversioned(t *testing.T) {
	h := &FluxcdReceiverHandler{}
	for _, kind := range receiverDefaultAPIVersionKinds {
		t.Run(kind, func(t *testing.T) {
			props := map[string]any{
				"type":      "github",
				"resources": []any{map[string]any{"kind": kind, "name": "*"}},
			}
			if _, err := h.ToApplicationConfig(&oam.Component{Name: "hook", Type: fluxcdReceiverType, Properties: props}, "default"); err != nil {
				t.Errorf("ToApplicationConfig = %v, want no refusal for an unversioned %s", err, kind)
			}
		})
	}
}

// TestReceiverKindAPIVersions_CoverTheEnum holds receiverKindAPIVersions to
// the kinds the Receiver API admits and the notification controller has no
// default version for: the `kind` enum marker of CrossNamespaceObjectReference
// in the pinned notification-controller API, less
// receiverDefaultAPIVersionKinds (go-kure/launcher#1010). A Flux upgrade that
// adds a kind to the enum fails here until the refusal can suggest its
// version.
func TestReceiverKindAPIVersions_CoverTheEnum(t *testing.T) {
	enum := receiverResourceKindEnum(t)
	var want []string
	for _, kind := range enum {
		if !slices.Contains(receiverDefaultAPIVersionKinds, kind) {
			want = append(want, kind)
		}
	}
	got := slices.Sorted(maps.Keys(receiverKindAPIVersions))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("receiverKindAPIVersions kinds = %v, want the enum %v less the default kinds", got, want)
	}
	for _, kind := range receiverDefaultAPIVersionKinds {
		if !slices.Contains(enum, kind) {
			t.Errorf("default kind %s is not in the enum %v", kind, enum)
		}
	}
}

// receiverResourceKindEnum reads the `+kubebuilder:validation:Enum=` marker of
// the Kind field of CrossNamespaceObjectReference in the v1 API package of the
// notification-controller module go.mod pins.
func receiverResourceKindEnum(t *testing.T) []string {
	t.Helper()
	file := filepath.Join(linkedModuleDir(t, "github.com/fluxcd/notification-controller/api"), "v1", "reference_types.go")
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var enum []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "CrossNamespaceObjectReference" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range st.Fields.List {
			if len(field.Names) != 1 || field.Names[0].Name != "Kind" || field.Doc == nil {
				continue
			}
			for _, c := range field.Doc.List {
				line := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if v, ok := strings.CutPrefix(line, "+kubebuilder:validation:Enum="); ok {
					enum = strings.Split(v, ";")
				}
			}
		}
		return false
	})
	if len(enum) == 0 {
		t.Fatalf("%s: no Enum marker on CrossNamespaceObjectReference.Kind", file)
	}
	return enum
}
