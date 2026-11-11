package patch

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"gopkg.in/yaml.v3"
)

// failingMarshaler is a resource value that yaml cannot marshal.
type failingMarshaler struct{}

func (failingMarshaler) MarshalYAML() (any, error) { return nil, errors.New("boom") }

// TestPatchErrorText pins the text of the errors that conflict.go, set.go, toml_parser.go and
// yaml_preserve.go build with pkg/errors. The rows pass on the code that built them with
// fmt.Errorf as well: the change moves the call and keeps the text. A row holds the exact text,
// or its prefix where the tail comes from another package.
//
// Sites with no row here, each with the reason no public call reaches it: copyYAMLNode and Copy fail
// only on an error from a child copy, which has none at the base; the merge in mergeYAMLNodes returns
// its errors to callers that discard them; and a resource that holds a value yaml cannot marshal
// panics in the deep copy that WritePatchedFilesWithOptions makes before it updates a document. A
// resource value that yaml marshals badly or parses badly reaches the marshal and parse sites through
// a document method called directly, and the rows above pin them. The pull request lists the rest.
func TestPatchErrorText(t *testing.T) {
	dir := t.TempDir()
	missingPatch := filepath.Join(dir, "missing.yaml")
	badPatch := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badPatch, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingOut := filepath.Join(dir, "no", "such", "out.yaml")
	long := strings.Repeat("x", 70000)

	emptySet := func() *PatchableAppSet {
		return &PatchableAppSet{DocumentSet: &YAMLDocumentSet{}}
	}

	// A patch nested deeper than maxDepth fails on the strategic path, with no KindLookup.
	tooDeepPatches := []StrategicPatch{{Patch: map[string]any{"data": deepMap(maxDepth + 1)}}}
	// writeConfigMap runs PatchableAppSet.WriteToFile on the ConfigMap with one patch of either kind.
	writeConfigMap := func(op *PatchOp, smp *StrategicPatch) error {
		docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
		if err != nil {
			return err
		}
		set := &PatchableAppSet{Resources: docSet.GetResources(), DocumentSet: docSet}
		entry := struct {
			Target    string
			Patch     PatchOp
			Strategic *StrategicPatch
		}{Target: "cm", Strategic: smp}
		if op != nil {
			entry.Patch = *op
		}
		set.Patches = append(set.Patches, entry)
		return set.WriteToFile(filepath.Join(dir, "out.yaml"))
	}

	// writePatchFile writes a patch file with the given content and returns its path.
	writePatchFile := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// cmSet is a set that holds the ConfigMap, for WritePatchedFilesWithOptions.
	cmSet := func() (*PatchableAppSet, error) {
		docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
		if err != nil {
			return nil, err
		}
		return &PatchableAppSet{Resources: docSet.GetResources(), DocumentSet: docSet}, nil
	}
	// writePatched runs WritePatchedFilesWithOptions on the ConfigMap with one patch file.
	writePatched := func(patchFile, outputDir string) error {
		set, err := cmSet()
		if err != nil {
			return err
		}
		return set.WritePatchedFilesWithOptions("a.yaml", []string{patchFile}, outputDir, false)
	}
	// shortNamePatch names a ConfigMap by its short name; two ConfigMaps of that name make it ambiguous.
	shortNamePatch := writePatchFile("short-name.yaml", "cm.data.x: y\n")
	twoConfigMaps := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: a\n---\n" +
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: b\n"
	// unmarshalableConfigMap loads the ConfigMap and puts a value into its resource that yaml cannot marshal.
	unmarshalableConfigMap := func() (*YAMLDocument, error) {
		docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
		if err != nil {
			return nil, err
		}
		doc := docSet.Documents[0]
		doc.Resource.Object["data"].(map[string]any)["bad"] = failingMarshaler{}
		return doc, nil
	}
	// danglingAliasConfigMap loads the ConfigMap and puts an alias node into its resource: it marshals as
	// "*missing", and parsing that text back fails on the undefined anchor.
	danglingAliasConfigMap := func() (*YAMLDocument, error) {
		docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
		if err != nil {
			return nil, err
		}
		doc := docSet.Documents[0]
		doc.Resource.Object["data"].(map[string]any)["bad"] = &yaml.Node{Kind: yaml.AliasNode, Value: "missing"}
		return doc, nil
	}
	noTargetPatch := writePatchFile("no-target.yaml", "- type: strategic\n  patch:\n    data:\n      x: y\n")
	badListPatch := writePatchFile("bad-list.yaml", "- target: cm\n  patch:\n    \"data.items[-]\": x\n")
	emptyPatch := writePatchFile("empty.yaml", "- target: cm\n  patch: {}\n")
	outputAFile := writePatchFile("output-is-a-file", "x")
	// The output file name is built from the original and patch names: a directory there fails the write.
	blockedDir := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blockedDir, "a-patch-empty.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	rows := []loaderErrorRow{
		{name: "toml header format", call: func() error {
			_, err := ParseTOMLHeader("x")
			return err
		}, want: "invalid TOML header format: x"},
		{name: "toml header empty", call: func() error {
			_, err := ParseTOMLHeader("[]")
			return err
		}, want: "empty TOML header"},
		{name: "toml header bracketed without name", call: func() error {
			_, err := ParseTOMLHeader("[a[b]]")
			return err
		}, want: "TOML header must have at least kind.name: [a[b]]"},
		{name: "toml header without name", call: func() error {
			_, err := ParseTOMLHeader("[x]")
			return err
		}, want: "TOML header must have at least kind.name: [x]"},
		{name: "write to file without document set", call: func() error {
			return (&PatchableAppSet{}).WriteToFile("unused.yaml")
		}, want: "no document set available for structure preservation"},
		{name: "write patched files without document set", call: func() error {
			return (&PatchableAppSet{}).WritePatchedFilesWithOptions("a.yaml", []string{"p.yaml"}, dir, false)
		}, want: "no document set available for structure preservation"},
		{name: "write to file resolve", call: func() error {
			set := emptySet()
			set.Patches = []struct {
				Target    string
				Patch     PatchOp
				Strategic *StrategicPatch
			}{{Target: "none"}}
			return set.WriteToFile("unused.yaml")
		}, prefix: "failed to resolve patches: "},
		{name: "open patch file", call: func() error {
			return emptySet().WritePatchedFilesWithOptions("a.yaml", []string{missingPatch}, dir, false)
		}, want: "failed to open patch file " + missingPatch + ": open " + missingPatch + ": no such file or directory"},
		{name: "load patch file", call: func() error {
			return emptySet().WritePatchedFilesWithOptions("a.yaml", []string{badPatch}, dir, false)
		}, want: "failed to load patches from " + badPatch + ": unrecognized patch format"},
		{name: "read yaml content", call: func() error {
			_, err := LoadResourcesWithStructure(iotest.ErrReader(errors.New("boom")))
			return err
		}, want: "failed to read YAML content: boom"},
		{name: "parse document", call: func() error {
			_, err := LoadResourcesWithStructure(strings.NewReader("a: [\n"))
			return err
		}, prefix: "failed to parse document 0: yaml: "},
		{name: "scan yaml documents", call: func() error {
			_, err := LoadResourcesWithStructure(strings.NewReader("a: " + long + "\n"))
			return err
		}, want: "failed to parse YAML documents: error reading YAML content: bufio.Scanner: token too long"},
		{name: "write file", call: func() error {
			return (&YAMLDocumentSet{}).WriteToFile(missingOut)
		}, want: "failed to write file " + missingOut + ": open " + missingOut + ": no such file or directory"},
		{name: "apply patch to document", call: func() error {
			docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
			if err != nil {
				return err
			}
			return docSet.Documents[0].ApplyPatchesToDocument([]PatchOp{appendToMissingList})
		}, prefix: "failed to apply patch {append data.items "},
		{name: "apply strategic patch to document", call: func() error {
			docSet, err := LoadResourcesWithStructure(strings.NewReader(patchErrorConfigMap))
			if err != nil {
				return err
			}
			return docSet.Documents[0].ApplyStrategicPatchesToDocument(tooDeepPatches, nil)
		}, prefix: "failed to apply strategic patch: "},
		{name: "write to file field patch", call: func() error {
			return writeConfigMap(&appendToMissingList, nil)
		}, prefix: "failed to apply patches to document configmap.cm: failed to apply patch {append data.items "},
		{name: "write to file strategic patch", call: func() error {
			return writeConfigMap(nil, &tooDeepPatches[0])
		}, prefix: "failed to apply strategic patches to document configmap.cm: failed to apply strategic patch: "},
		{name: "write patched files create set", call: func() error {
			return writePatched(noTargetPatch, dir)
		}, prefix: "failed to create patchable set for " + noTargetPatch + ": "},
		{name: "write patched files resolve", call: func() error {
			docSet, err := LoadResourcesWithStructure(strings.NewReader(twoConfigMaps))
			if err != nil {
				return err
			}
			set := &PatchableAppSet{Resources: docSet.GetResources(), DocumentSet: docSet}
			return set.WritePatchedFilesWithOptions("a.yaml", []string{shortNamePatch}, dir, false)
		}, prefix: "failed to resolve patches from " + shortNamePatch + ": "},
		{name: "write patched files apply", call: func() error {
			return writePatched(badListPatch, dir)
		}, prefix: "failed to apply patches to resource cm: "},
		{name: "write patched files output directory", call: func() error {
			return writePatched(emptyPatch, outputAFile)
		}, prefix: "failed to create output directory " + outputAFile + ": "},
		{name: "write patched files output file", call: func() error {
			return writePatched(emptyPatch, blockedDir)
		}, prefix: "failed to write patched file " + blockedDir + "/a-patch-empty.yaml: "},
		{name: "resolve with conflict check", call: func() error {
			set, err := cmSet()
			if err != nil {
				return err
			}
			set.KindLookup, err = DefaultKindLookup()
			if err != nil {
				return err
			}
			bogus := &StrategicPatch{Patch: map[string]any{"bogus": map[string]any{}}}
			for range 2 {
				set.Patches = append(set.Patches, struct {
					Target    string
					Patch     PatchOp
					Strategic *StrategicPatch
				}{Target: "cm", Strategic: bogus})
			}
			_, _, err = set.ResolveWithConflictCheck()
			return err
		}, prefix: "conflict detection failed for resource cm: conflict detection failed between patch 0 and 1: "},
		{name: "apply patch to document marshal", call: func() error {
			doc, err := unmarshalableConfigMap()
			if err != nil {
				return err
			}
			return doc.ApplyPatchesToDocument([]PatchOp{{Op: "replace", Path: "data.a", Value: "2"}})
		}, want: "failed to marshal patched resource: boom"},
		{name: "update document from resource marshal", call: func() error {
			doc, err := unmarshalableConfigMap()
			if err != nil {
				return err
			}
			return doc.UpdateDocumentFromResource()
		}, want: "failed to marshal updated resource: boom"},
		{name: "apply strategic patch to document marshal", call: func() error {
			doc, err := unmarshalableConfigMap()
			if err != nil {
				return err
			}
			// An empty patch list skips the JSON round trip, so the value reaches the marshal.
			return doc.ApplyStrategicPatchesToDocument(nil, nil)
		}, want: "failed to marshal patched resource: boom"},
		{name: "apply patch to document parse", call: func() error {
			doc, err := danglingAliasConfigMap()
			if err != nil {
				return err
			}
			return doc.ApplyPatchesToDocument(nil)
		}, prefix: "failed to parse patched YAML: "},
		{name: "apply strategic patch to document parse", call: func() error {
			doc, err := danglingAliasConfigMap()
			if err != nil {
				return err
			}
			return doc.ApplyStrategicPatchesToDocument(nil, nil)
		}, prefix: "failed to parse patched YAML: "},
		{name: "update document from resource parse", call: func() error {
			doc, err := danglingAliasConfigMap()
			if err != nil {
				return err
			}
			return doc.UpdateDocumentFromResource()
		}, prefix: "failed to parse updated YAML: "},
		{name: "encode document", call: func() error {
			set := &YAMLDocumentSet{Documents: []*YAMLDocument{{Node: &yaml.Node{Kind: 999}}}}
			return set.WriteToFile(filepath.Join(dir, "encoded.yaml"))
		}, prefix: "failed to encode document 0: "},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := row.call()
			if err == nil {
				t.Fatalf("no error, want %q%q", row.want, row.prefix)
			}
			got := err.Error()
			if row.prefix != "" {
				if !strings.HasPrefix(got, row.prefix) {
					t.Fatalf("error text %q does not start with %q", got, row.prefix)
				}
				return
			}
			if got != row.want {
				t.Fatalf("error text %q, want %q", got, row.want)
			}
		})
	}
}

// TestPatchErrorChain pins that a wrapping site keeps its cause on the unwrap chain, for sites
// built with Wrap and with Wrapf.
func TestPatchErrorChain(t *testing.T) {
	boom := errors.New("boom")
	if _, err := LoadResourcesWithStructure(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("Wrap site: the read error is not on the chain: %v", err)
	}

	_, err := LoadResourcesWithStructure(strings.NewReader("a: " + strings.Repeat("x", 70000) + "\n"))
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("Wrap site: the scanner error is not on the chain: %v", err)
	}

	_, err = LoadResourcesWithStructure(strings.NewReader("a: [\n"))
	if err == nil || errors.Unwrap(err) == nil {
		t.Fatalf("Wrapf site: no cause on the chain: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "no", "out.yaml")
	err = (&YAMLDocumentSet{}).WriteToFile(missing)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Wrapf site: the file error is not on the chain: %v", err)
	}
}
