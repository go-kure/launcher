package patch

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
)

// The README section "Error Handling" says which call paths return a
// *errors.PatchError that errors.As can match, and which do not. These tests pin both
// halves: the field-patch path keeps the chain of applyPatchOp, also through
// PatchableAppSet.WriteToFile, and the strategic path builds no PatchError.

const patchErrorConfigMap = `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
data:
  a: "1"
`

// appendToMissingList is a field patch that applyPatchOp refuses with a PatchError:
// the list it appends to does not exist.
var appendToMissingList = PatchOp{Op: "append", Path: "data.items", Value: "x"}

func requireAppendPatchError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error for an append to a missing list")
	}
	var pe *errors.PatchError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As did not find a *errors.PatchError in %T: %v", err, err)
	}
	if pe.Operation != "append" {
		t.Errorf("PatchError.Operation = %q, want %q", pe.Operation, "append")
	}
}

func TestApplyPatchesToDocument_KeepsThePatchErrorChain(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	err = docSet.Documents[0].ApplyPatchesToDocument([]PatchOp{appendToMissingList})
	requireAppendPatchError(t, err)
}

func TestWriteToFile_KeepsThePatchErrorChain(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	pas := &PatchableAppSet{
		Resources:   docSet.GetResources(),
		DocumentSet: docSet,
		Patches: []struct {
			Target    string
			Patch     PatchOp
			Strategic *StrategicPatch
		}{
			{Target: "cm", Patch: appendToMissingList},
		},
	}
	err = pas.WriteToFile(t.TempDir() + "/out.yaml")
	requireAppendPatchError(t, err)
}

// ApplyStrategicPatchesToDocument fails with a plain wrapped error: the strategic path
// builds no PatchError, so a caller must not expect errors.As to match one.
func TestApplyStrategicPatchesToDocument_BuildsNoPatchError(t *testing.T) {
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	tooDeep := []StrategicPatch{{Patch: map[string]any{"data": deepMap(maxDepth + 1)}}}

	for name, kl := range map[string]KindLookup{
		"known kind":     lookup,
		"merge fallback": nil,
	} {
		t.Run(name, func(t *testing.T) {
			docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
			if err != nil {
				t.Fatalf("LoadResourcesWithStructure: %v", err)
			}
			err = docSet.Documents[0].ApplyStrategicPatchesToDocument(tooDeep, kl)
			if err == nil {
				t.Fatal("want an error for a patch nested deeper than maxDepth")
			}
			var pe *errors.PatchError
			if errors.As(err, &pe) {
				t.Errorf("errors.As found a *errors.PatchError (%v) on the strategic path; the README says it builds none", pe)
			}
		})
	}
}
