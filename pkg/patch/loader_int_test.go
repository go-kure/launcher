package patch

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// intDeployment holds integers at three depths: a map value, a value in a list of maps
// and a value two lists down. yaml.v3 decodes each of them to a Go int.
const intDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  generation: 2
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: myapp
        image: nginx:1.0
        ports:
        - containerPort: 8080
        livenessProbe:
          initialDelaySeconds: 5
`

// requireInt64 reads path as an int64 the way unstructured does, and fails when the
// stored value has another type or another number.
func requireInt64(t *testing.T, obj *unstructured.Unstructured, want int64, path ...string) {
	t.Helper()
	got, found, err := unstructured.NestedInt64(obj.Object, path...)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", strings.Join(path, "."), found, err)
	}
	if got != want {
		t.Errorf("%s = %d, want %d", strings.Join(path, "."), got, want)
	}
}

func requireContainerInts(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireInt64(t, obj, 1, "spec", "replicas")
	requireInt64(t, obj, 2, "metadata", "generation")
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("containers: found=%v err=%v len=%d", found, err, len(containers))
	}
	c, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container is %T", containers[0])
	}
	requireInt64(t, &unstructured.Unstructured{Object: c}, 5, "livenessProbe", "initialDelaySeconds")
	ports, found, err := unstructured.NestedSlice(c, "ports")
	if err != nil || !found || len(ports) != 1 {
		t.Fatalf("ports: found=%v err=%v len=%d", found, err, len(ports))
	}
	p, ok := ports[0].(map[string]any)
	if !ok {
		t.Fatalf("port is %T", ports[0])
	}
	requireInt64(t, &unstructured.Unstructured{Object: p}, 8080, "containerPort")
}

// An integer in a base resource must be an int64, because Unstructured.DeepCopy
// panics on a Go int and Resolve copies each targeted base resource.
func TestLoadResourcesFromMultiYAML_IntegersSurviveResolve(t *testing.T) {
	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(intDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesFromMultiYAML: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("loaded %d resources, want 1", len(resources))
	}
	requireContainerInts(t, resources[0])

	patches, err := LoadPatchFile(strings.NewReader("- target: myapp\n  patch:\n    spec.replicas: 3\n"))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSet(resources, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requireInt64(t, resolved[0].Base, 3, "spec", "replicas")
	requireInt64(t, resolved[0].Base, 2, "metadata", "generation")
}

func TestLoadResourcesWithStructure_IntegersSurviveResolve(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(intDeployment))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	if len(docSet.Documents) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docSet.Documents))
	}
	requireContainerInts(t, docSet.Documents[0].Resource)

	patches, err := LoadPatchFile(strings.NewReader("- target: myapp\n  patch:\n    spec.replicas: 3\n"))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSetWithStructure(docSet, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSetWithStructure: %v", err)
	}
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	requireInt64(t, resolved[0].Base, 3, "spec", "replicas")
}

// stampConfig holds the three timestamp shapes the loaders must hand back as the text the
// author wrote: a date, a time with a fraction and an offset, and (as a control) a quoted
// date that yaml.v3 already decodes as a string. It also holds a timestamp in a list element.
const stampConfig = `apiVersion: v1
kind: ConfigMap
metadata:
  name: stamps
spec:
  dateOnly: 2026-11-07
  fractionOffset: 2026-11-07T12:00:00.5+01:00
  quoted: "2026-11-07"
  list:
  - when: 2026-11-07
`

// keyedConfig holds maps that yaml.v3 decodes to map[interface{}]interface{}: one with
// integer keys (and an integer value) and one with a timestamp key.
const keyedConfig = `apiVersion: v1
kind: ConfigMap
metadata:
  name: stamps
spec:
  numbered:
    123: hello
    456: 7
  keys:
    2026-11-07: dated
`

func requireString(t *testing.T, obj map[string]any, want string, path ...string) {
	t.Helper()
	got, found, err := unstructured.NestedString(obj, path...)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", strings.Join(path, "."), found, err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", strings.Join(path, "."), got, want)
	}
}

func requireStampConfig(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireString(t, obj.Object, "2026-11-07", "spec", "dateOnly")
	requireString(t, obj.Object, "2026-11-07T12:00:00.5+01:00", "spec", "fractionOffset")
	requireString(t, obj.Object, "2026-11-07", "spec", "quoted")
	list, found, err := unstructured.NestedSlice(obj.Object, "spec", "list")
	if err != nil || !found || len(list) != 1 {
		t.Fatalf("list: found=%v err=%v len=%d", found, err, len(list))
	}
	item, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("list item is %T", list[0])
	}
	requireString(t, item, "2026-11-07", "when")
}

func requireKeyedConfig(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	requireString(t, obj.Object, "hello", "spec", "numbered", "123")
	requireInt64(t, obj, 7, "spec", "numbered", "456")
	requireString(t, obj.Object, "dated", "spec", "keys", "2026-11-07")
}

// resolveStamps resolves an unrelated-field patch over the set, which copies the targeted base
// resource, and runs check on the resolved base.
func resolveStamps(t *testing.T, set *PatchableAppSet, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	resolved, err := set.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve returned %d resources, want 1", len(resolved))
	}
	if err := resolved[0].Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	check(t, resolved[0].Base)
}

const stampPatch = "- target: stamps\n  patch:\n    metadata.labels.touched: \"yes\"\n"

// A plain timestamp in a base resource must load as the text the author wrote, because
// yaml.v3 decodes it to time.Time, DeepCopy panics on that type and formatting it would
// rewrite what was authored.
func TestLoadResourcesFromMultiYAML_TimestampsKeepAuthoredText(t *testing.T) {
	multiYAMLSurvivesResolve(t, stampConfig, requireStampConfig)
}

// A mapping with non-string keys must load with string keys and int64 values, because
// yaml.v3 decodes it to map[interface{}]interface{}, which DeepCopy panics on.
func TestLoadResourcesFromMultiYAML_NonStringKeyMapsAreConverted(t *testing.T) {
	multiYAMLSurvivesResolve(t, keyedConfig, requireKeyedConfig)
}

func TestLoadResourcesWithStructure_TimestampsKeepAuthoredText(t *testing.T) {
	structureSurvivesResolve(t, stampConfig, requireStampConfig)
}

func TestLoadResourcesWithStructure_NonStringKeyMapsAreConverted(t *testing.T) {
	structureSurvivesResolve(t, keyedConfig, requireKeyedConfig)
}

func multiYAMLSurvivesResolve(t *testing.T, src string, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	resources, err := LoadResourcesFromMultiYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadResourcesFromMultiYAML: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("loaded %d resources, want 1", len(resources))
	}
	check(t, resources[0])

	patches, err := LoadPatchFile(strings.NewReader(stampPatch))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSet(resources, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSet: %v", err)
	}
	resolveStamps(t, set, check)
}

func structureSurvivesResolve(t *testing.T, src string, check func(*testing.T, *unstructured.Unstructured)) {
	t.Helper()
	docSet, err := LoadResourcesWithStructure(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	if len(docSet.Documents) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docSet.Documents))
	}
	check(t, docSet.Documents[0].Resource)

	patches, err := LoadPatchFile(strings.NewReader(stampPatch))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	set, err := NewPatchableAppSetWithStructure(docSet, patches)
	if err != nil {
		t.Fatalf("NewPatchableAppSetWithStructure: %v", err)
	}
	resolveStamps(t, set, check)
}

// The structure path keeps the yaml.Node it writes back from: the loader must not retag
// that node, because a string tag on a plain timestamp makes the encoder quote it.
func TestLoadResourcesWithStructure_WriteBackKeepsPlainTimestamp(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(stampConfig))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	out, err := yaml.Marshal(docSet.Documents[0].Node)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{
		"  dateOnly: 2026-11-07\n",
		"  fractionOffset: 2026-11-07T12:00:00.5+01:00\n",
		"  quoted: \"2026-11-07\"\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("write-back lacks %q:\n%s", want, out)
		}
	}
}

// A timestamp in a patch value must reach the patch as the authored text, in the
// list form and in the simple map form.
func TestLoadPatchFile_TimestampValueKeepsAuthoredText(t *testing.T) {
	for name, src := range map[string]string{
		"list":   "- target: stamps\n  patch:\n    spec.dateOnly: 2026-11-07\n    spec.fractionOffset: 2026-11-07T12:00:00.5+01:00\n    spec.quoted: \"2026-11-07\"\n",
		"simple": "spec.dateOnly: 2026-11-07\nspec.fractionOffset: 2026-11-07T12:00:00.5+01:00\nspec.quoted: \"2026-11-07\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			patches, err := LoadPatchFile(strings.NewReader(src))
			if err != nil {
				t.Fatalf("LoadPatchFile: %v", err)
			}
			if len(patches) != 3 {
				t.Fatalf("loaded %d patches, want 3", len(patches))
			}
			want := map[string]string{
				"spec.dateOnly":       "2026-11-07",
				"spec.fractionOffset": "2026-11-07T12:00:00.5+01:00",
				"spec.quoted":         "2026-11-07",
			}
			for _, p := range patches {
				if got, ok := p.Patch.Value.(string); !ok || got != want[p.Patch.Path] {
					t.Errorf("%s value is %T %v, want string %q", p.Patch.Path, p.Patch.Value, p.Patch.Value, want[p.Patch.Path])
				}
			}
		})
	}
}

// writtenBack returns the YAML text of a document node, as WriteToFile writes it.
func writtenBack(t *testing.T, node *yaml.Node) string {
	t.Helper()
	out, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(out)
}

// requireWrittenLines checks that the write-back of node holds each line, one subtest per
// line, so that a failure names the field it is about.
func requireWrittenLines(t *testing.T, node *yaml.Node, lines map[string]string) {
	t.Helper()
	out := writtenBack(t, node)
	for name, want := range lines {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out, want) {
				t.Errorf("write-back lacks %q:\n%s", want, out)
			}
		})
	}
}

// requirePlainStampsWritten checks the YAML text of a document node after a patch step. The
// resource holds an untouched plain timestamp as a string, and yaml.v3 quotes a string that
// reads as a timestamp, so the write-back must take the authored tag and style back from the
// node it replaces. The quoted control stays quoted.
func requirePlainStampsWritten(t *testing.T, node *yaml.Node) {
	t.Helper()
	requireWrittenLines(t, node, map[string]string{
		"dateOnly":       "dateOnly: 2026-11-07\n",
		"fractionOffset": "fractionOffset: 2026-11-07T12:00:00.5+01:00\n",
		"quoted":         "quoted: \"2026-11-07\"\n",
		"when":           "when: 2026-11-07\n",
	})
}

func loadOneDocument(t *testing.T, src string) *YAMLDocument {
	t.Helper()
	docSet, err := LoadResourcesWithStructure(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	if len(docSet.Documents) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docSet.Documents))
	}
	return docSet.Documents[0]
}

func applyStrategic(t *testing.T, doc *YAMLDocument, patch map[string]any) {
	t.Helper()
	lookup, err := DefaultKindLookup()
	if err != nil {
		t.Fatalf("DefaultKindLookup: %v", err)
	}
	if err := doc.ApplyStrategicPatchesToDocument([]StrategicPatch{{Patch: patch}}, lookup); err != nil {
		t.Fatalf("ApplyStrategicPatchesToDocument: %v", err)
	}
}

// labelsPatch touches only metadata.labels, so every timestamp in the document is untouched.
func labelsPatch() map[string]any {
	return map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}
}

// patchOps loads the patch values of a patch file, so that they carry the types the
// patch loader gives them.
func patchOps(t *testing.T, src string) []PatchOp {
	t.Helper()
	patches, err := LoadPatchFile(strings.NewReader(src))
	if err != nil {
		t.Fatalf("LoadPatchFile: %v", err)
	}
	ops := make([]PatchOp, 0, len(patches))
	for _, p := range patches {
		ops = append(ops, p.Patch)
	}
	return ops
}

// ApplyPatchesToDocument marshals the resource and replaces the document's value nodes
// with the marshaled ones. A timestamp the patch did not touch keeps its authored style.
func TestApplyPatchesToDocument_UntouchedTimestampsKeepAuthoredStyle(t *testing.T) {
	doc := loadOneDocument(t, stampConfig)
	requireStampConfig(t, doc.Resource)
	if err := doc.ApplyPatchesToDocument(nil); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	requirePlainStampsWritten(t, doc.Node)
}

func TestApplyStrategicPatchesToDocument_UntouchedTimestampsKeepAuthoredStyle(t *testing.T) {
	doc := loadOneDocument(t, stampConfig)
	requireStampConfig(t, doc.Resource)
	applyStrategic(t, doc, labelsPatch())
	requirePlainStampsWritten(t, doc.Node)
}

// UpdateDocumentFromResource replaces the whole node and merges nothing, so the authored
// style must be carried over on that path as well.
func TestUpdateDocumentFromResource_UntouchedTimestampsKeepAuthoredStyle(t *testing.T) {
	doc := loadOneDocument(t, stampConfig)
	requireStampConfig(t, doc.Resource)
	doc.Resource.SetLabels(map[string]string{"a": "b"})
	if err := doc.UpdateDocumentFromResource(); err != nil {
		t.Fatalf("UpdateDocumentFromResource: %v", err)
	}
	requirePlainStampsWritten(t, doc.Node)
}

// An explicit !!timestamp tag on an untouched timestamp is written back with the tag, on
// every write-back path.
func TestWriteBack_ExplicitTimestampTagIsKept(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp 2026-11-07\n"
	for name, step := range map[string]func(*testing.T, *YAMLDocument){
		"apply": func(t *testing.T, doc *YAMLDocument) {
			if err := doc.ApplyPatchesToDocument(nil); err != nil {
				t.Fatalf("ApplyPatchesToDocument: %v", err)
			}
		},
		"strategic": func(t *testing.T, doc *YAMLDocument) { applyStrategic(t, doc, labelsPatch()) },
		"update": func(t *testing.T, doc *YAMLDocument) {
			doc.Resource.SetLabels(map[string]string{"a": "b"})
			if err := doc.UpdateDocumentFromResource(); err != nil {
				t.Fatalf("UpdateDocumentFromResource: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			step(t, doc)
			if out, want := writtenBack(t, doc.Node), "stamp: !!timestamp 2026-11-07\n"; !strings.Contains(out, want) {
				t.Errorf("write-back lacks %q:\n%s", want, out)
			}
		})
	}
}

// A list whose items carry a merge key ("name") takes the keyed branch of the sequence
// merge, which replaces each item with its patched copy. An untouched timestamp in such an
// item keeps its authored style.
func TestApplyStrategicPatchesToDocument_KeyedListTimestampKeepsAuthoredStyle(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stamps\nspec:\n  items:\n  - name: a\n    when: 2026-11-07\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, labelsPatch())
	if out, want := writtenBack(t, doc.Node), "when: 2026-11-07\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
}

// Control: a timestamp a patch sets to another date is a new value, written in the form the
// marshaled resource gives it. It discriminates nothing at the base; it pins that only a
// value with the authored text takes the authored style.
func TestApplyPatchesToDocument_PatchedTimestampIsWrittenQuoted(t *testing.T) {
	doc := loadOneDocument(t, stampConfig)
	if err := doc.ApplyPatchesToDocument(patchOps(t, "- target: stamps\n  patch:\n    spec.dateOnly: \"2026-12-01\"\n")); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	if out, want := writtenBack(t, doc.Node), "dateOnly: \"2026-12-01\"\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
}

// Control: a patch that changes a timestamp into an integer keeps its own node. It
// discriminates nothing at the base.
func TestApplyPatchesToDocument_TimestampTypeChangeKeepsItsOwnNode(t *testing.T) {
	doc := loadOneDocument(t, stampConfig)
	if err := doc.ApplyPatchesToDocument(patchOps(t, "- target: stamps\n  patch:\n    spec.dateOnly: 5\n")); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	if out, want := writtenBack(t, doc.Node), "dateOnly: 5\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
}

// Control: a list with no merge key is replaced whole and its items pair by index. The
// patched item at index 0 holds another date than the authored one, so it is written in its
// marshaled, quoted form. It discriminates nothing at the base. The kind is one the lookup
// does not know, so the patch is a JSON merge patch, which replaces the list.
func TestApplyStrategicPatchesToDocument_ReplacedListTimestampIsWrittenQuoted(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  dates:\n  - 2026-11-07\n  - 2026-11-08\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"spec": map[string]any{"dates": []any{"2026-11-08"}}})
	out := writtenBack(t, doc.Node)
	if want := "- \"2026-11-08\"\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "2026-11-07") {
		t.Errorf("write-back still holds the removed item:\n%s", out)
	}
}

// A batch that changes a timestamp into another type and back sets a string the patch wrote,
// so the field is written quoted, with no authored tag. The final text equals the authored
// text, so only the record of which paths the patches touched can tell the two apart.
func TestApplyPatchesToDocument_TypeChangeBackWithinOneBatchIsWrittenQuoted(t *testing.T) {
	for name, stamp := range map[string]string{"plain": "2026-11-07", "explicit_tag": "!!timestamp 2026-11-07"} {
		t.Run(name, func(t *testing.T) {
			doc := loadOneDocument(t, "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  stamp: "+stamp+"\n")
			err := doc.ApplyPatchesToDocument([]PatchOp{
				{Op: "replace", Path: "spec.stamp", Value: int64(5)},
				{Op: "replace", Path: "spec.stamp", Value: "2026-11-07"},
			})
			if err != nil {
				t.Fatalf("ApplyPatchesToDocument: %v", err)
			}
			out := writtenBack(t, doc.Node)
			if want := "stamp: \"2026-11-07\"\n"; !strings.Contains(out, want) {
				t.Errorf("write-back lacks %q:\n%s", want, out)
			}
			if strings.Contains(out, "!!timestamp") {
				t.Errorf("write-back keeps the authored tag on a value a patch set:\n%s", out)
			}
		})
	}
}

// A list item with no merge key, next to an item that has one, is not matched by key. Its
// untouched timestamp keeps the authored style all the same.
func TestApplyPatchesToDocument_UnkeyedItemBesideKeyedItemKeepsAuthoredStyle(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  items:\n  - name: a\n  - when: 2026-11-07\n"
	doc := loadOneDocument(t, src)
	if err := doc.ApplyPatchesToDocument(nil); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	if out, want := writtenBack(t, doc.Node), "when: 2026-11-07\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
}

// Deleting one list item moves the others down. A surviving timestamp is paired with its own
// authored node, not with the node that held its new index.
func TestApplyPatchesToDocument_DeleteFromListKeepsSurvivorStyle(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  dates:\n  - 2026-11-07\n  - 2026-11-08\n"
	doc := loadOneDocument(t, src)
	if err := doc.ApplyPatchesToDocument([]PatchOp{{Op: "delete", Path: "spec.dates", Selector: "0"}}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	if want := "- 2026-11-08\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "2026-11-07") {
		t.Errorf("write-back still holds the deleted item:\n%s", out)
	}
}

// A caller that changes a timestamp in the resource itself, then rebuilds the document, writes
// the new text quoted: the authored style only goes to a scalar that holds the authored text.
func TestUpdateDocumentFromResource_ChangedTimestampTextIsWrittenQuoted(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp 2026-11-07\n"
	doc := loadOneDocument(t, src)
	if err := unstructured.SetNestedField(doc.Resource.Object, "2026-12-01", "spec", "stamp"); err != nil {
		t.Fatalf("SetNestedField: %v", err)
	}
	if err := doc.UpdateDocumentFromResource(); err != nil {
		t.Fatalf("UpdateDocumentFromResource: %v", err)
	}
	out := writtenBack(t, doc.Node)
	if want := "stamp: \"2026-12-01\"\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "!!timestamp") {
		t.Errorf("write-back keeps the authored tag on new text:\n%s", out)
	}
}

// Items of a keyed list pair by their merge key, so deleting the first item leaves the second
// paired with its own authored node and not with the node that held its new index.
func TestApplyPatchesToDocument_DeleteFromKeyedListKeepsSurvivorStyle(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  items:\n  - name: a\n    when: 2026-11-07\n  - name: b\n    when: 2026-11-08\n"
	doc := loadOneDocument(t, src)
	if err := doc.ApplyPatchesToDocument([]PatchOp{{Op: "delete", Path: "spec.items", Selector: "0"}}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	if want := "when: 2026-11-08\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "name: a") {
		t.Errorf("write-back still holds the deleted item:\n%s", out)
	}
}

// The write route of the set applies the patches to the resource, then rebuilds the document
// from it. A patch that writes the same text as an authored timestamp wrote that field, so it is
// quoted and loses the authored tag. A timestamp no patch wrote keeps its authored style.
func TestWritePatchedFilesWithOptions_SameTextTimestampReplacementIsQuoted(t *testing.T) {
	const base = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp 2026-11-07\n  other: 2026-11-08\n"
	docSet, err := LoadResourcesWithStructure(strings.NewReader(base))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	set := &PatchableAppSet{Resources: docSet.GetResources(), DocumentSet: docSet}
	dir := t.TempDir()
	patchFile := dir + "/patch.yaml"
	if err := os.WriteFile(patchFile, []byte("- target: stamps\n  patch:\n    spec.stamp: \"2026-11-07\"\n"), 0o644); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	baseFile := dir + "/base.yaml"
	if err := os.WriteFile(baseFile, []byte(base), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := set.WritePatchedFilesWithOptions(baseFile, []string{patchFile}, dir+"/out", false); err != nil {
		t.Fatalf("WritePatchedFilesWithOptions: %v", err)
	}
	entries, err := os.ReadDir(dir + "/out")
	if err != nil || len(entries) != 1 {
		t.Fatalf("output dir: %d entries, err %v", len(entries), err)
	}
	content, err := os.ReadFile(dir + "/out/" + entries[0].Name())
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := string(content)
	if want := "stamp: \"2026-11-07\"\n"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "!!timestamp") {
		t.Errorf("output keeps the authored tag on a field a patch wrote:\n%s", out)
	}
	if want := "other: 2026-11-08\n"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q (a timestamp no patch wrote):\n%s", want, out)
	}
}

// The production route passes the paths a strategic patch wrote, as well as the field-level ones:
// a list a strategic patch replaced is written as the patch gave it, even where an authored
// timestamp has the same text.
func TestWritePatchedFilesWithOptions_StrategicSameTextTimestampReplacementIsQuoted(t *testing.T) {
	const base = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  dates:\n    - 2026-11-07\n  other: 2026-11-08\n"
	docSet, err := LoadResourcesWithStructure(strings.NewReader(base))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	set := &PatchableAppSet{Resources: docSet.GetResources(), DocumentSet: docSet}
	dir := t.TempDir()
	patchFile := dir + "/patch.yaml"
	if err := os.WriteFile(patchFile, []byte("- target: stamps\n  type: strategic\n  patch:\n    spec:\n      dates:\n        - \"2026-11-07\"\n"), 0o644); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	baseFile := dir + "/base.yaml"
	if err := os.WriteFile(baseFile, []byte(base), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := set.WritePatchedFilesWithOptions(baseFile, []string{patchFile}, dir+"/out", false); err != nil {
		t.Fatalf("WritePatchedFilesWithOptions: %v", err)
	}
	entries, err := os.ReadDir(dir + "/out")
	if err != nil || len(entries) != 1 {
		t.Fatalf("output dir: %d entries, err %v", len(entries), err)
	}
	content, err := os.ReadFile(dir + "/out/" + entries[0].Name())
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := string(content)
	if want := "- \"2026-11-07\"\n"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if want := "other: 2026-11-08\n"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q (a timestamp no patch wrote):\n%s", want, out)
	}
}

// A timestamp reached through an alias is the same timestamp: the copy keeps the style of the
// node the alias points at, on every write-back path.
func TestWriteBack_AliasedTimestampKeepsAuthoredStyle(t *testing.T) {
	const prefix = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n"
	for _, c := range []struct{ name, body, want string }{
		{"mapping_value", "  stamp: &date 2026-11-07\n  copy: *date\n", "copy: 2026-11-07\n"},
		{"scalar_list_item", "  stamp: &date 2026-11-07\n  dates:\n    - *date\n", "dates:\n        - 2026-11-07\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for name, step := range writeBackSteps() {
				t.Run(name, func(t *testing.T) {
					doc := loadOneDocument(t, prefix+c.body)
					step(t, doc)
					if out := writtenBack(t, doc.Node); !strings.Contains(out, c.want) {
						t.Errorf("write-back lacks %q:\n%s", c.want, out)
					}
				})
			}
		})
	}
}

// writeBackSteps are the three write-back paths. Each one writes nothing under spec, so every
// timestamp under spec is untouched.
func writeBackSteps() map[string]func(*testing.T, *YAMLDocument) {
	return map[string]func(*testing.T, *YAMLDocument){
		"apply": func(t *testing.T, doc *YAMLDocument) {
			if err := doc.ApplyPatchesToDocument(nil); err != nil {
				t.Fatalf("ApplyPatchesToDocument: %v", err)
			}
		},
		"strategic": func(t *testing.T, doc *YAMLDocument) { applyStrategic(t, doc, labelsPatch()) },
		"update": func(t *testing.T, doc *YAMLDocument) {
			doc.Resource.SetLabels(map[string]string{"a": "b"})
			if err := doc.UpdateDocumentFromResource(); err != nil {
				t.Fatalf("UpdateDocumentFromResource: %v", err)
			}
		},
	}
}

// A list item that is an alias counts as the scalar it points at, so it keeps that scalar's
// authored style, on every write-back path. The controls pin what must not change: an alias of
// a string, of a quoted string and of a mapping.
func TestWriteBack_AliasedTimestampInList(t *testing.T) {
	const prefix = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n"
	for _, c := range []struct{ name, body, want string }{
		{"B_alias_then_plain_item", "  stamp: &date 2026-11-07\n  dates:\n    - *date\n    - 2026-11-08\n", "dates:\n        - 2026-11-07\n        - 2026-11-08\n"},
		{"C_two_aliases_of_one_node", "  stamp: &date 2026-11-07\n  dates:\n    - *date\n    - *date\n", "dates:\n        - 2026-11-07\n        - 2026-11-07\n"},
		{"E_alias_of_a_tagged_timestamp", "  stamp: &date !!timestamp 2026-11-07\n  dates:\n    - *date\n", "dates:\n        - !!timestamp 2026-11-07\n"},
		{"H_anchor_item_then_its_alias", "  dates:\n    - &d 2026-11-07\n    - *d\n", "dates:\n        - 2026-11-07\n        - 2026-11-07\n"},
		{"D_control_alias_of_a_string", "  name: &n hello\n  names:\n    - *n\n", "names:\n        - hello\n"},
		{"G_control_alias_of_a_quoted_string", "  q: &q \"2026-11-07\"\n  dates:\n    - *q\n", "dates:\n        - \"2026-11-07\"\n"},
		{"F_control_alias_of_a_mapping", "  base: &b {name: x}\n  items:\n    - *b\n", "items:\n        - name: x\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for name, step := range writeBackSteps() {
				t.Run(name, func(t *testing.T) {
					doc := loadOneDocument(t, prefix+c.body)
					step(t, doc)
					if out := writtenBack(t, doc.Node); !strings.Contains(out, c.want) {
						t.Errorf("write-back lacks %q:\n%s", c.want, out)
					}
				})
			}
		})
	}
}

// A field a patch writes keeps its own node, so a patch that writes the list quotes its
// timestamp, while a patch that writes another field leaves the list item as authored.
func TestWriteBack_AliasedTimestampInList_Touched(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  stamp: &date 2026-11-07\n  dates:\n    - *date\n  other: x\n"
	for _, c := range []struct {
		name  string
		patch PatchOp
		want  string
	}{
		{"T1_control_patch_writes_the_list", PatchOp{Op: "replace", Path: "spec.dates", Value: []any{"2026-11-07"}}, "dates:\n        - \"2026-11-07\"\n"},
		{"T2_patch_writes_another_field", PatchOp{Op: "replace", Path: "spec.other", Value: "y"}, "dates:\n        - 2026-11-07\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			if err := doc.ApplyPatchesToDocument([]PatchOp{c.patch}); err != nil {
				t.Fatalf("ApplyPatchesToDocument: %v", err)
			}
			if out := writtenBack(t, doc.Node); !strings.Contains(out, c.want) {
				t.Errorf("write-back lacks %q:\n%s", c.want, out)
			}
		})
	}
}

// An empty mapping in a strategic patch writes nothing under it: the existing keys of that
// mapping are untouched, so a timestamp inside keeps its authored style.
func TestApplyStrategicPatchesToDocument_EmptyMapKeepsTimestampStyle(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp 2026-11-07\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"spec": map[string]any{}})
	if out, want := writtenBack(t, doc.Node), "stamp: !!timestamp 2026-11-07\n"; !strings.Contains(out, want) {
		t.Errorf("write-back lacks %q:\n%s", want, out)
	}
}

// A field name with literal brackets is written by a patch whose path names it literally, so
// the touched path must name it the same way, not cut at the bracket. That holds for a field
// below the root and for a root key that starts with the bracket, where the cut path is empty.
func TestApplyPatchesToDocument_LiteralBracketKeyIsTouched(t *testing.T) {
	for _, c := range []struct{ name, src, path string }{
		{"nested", "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\nspec:\n  \"stamp[raw]\": !!timestamp 2026-11-07\n", "spec.stamp[raw]"},
		{"root", "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: stamps\n\"[raw]\": !!timestamp 2026-11-07\n", "[raw]"},
	} {
		doc := loadOneDocument(t, c.src)
		if err := doc.ApplyPatchesToDocument([]PatchOp{{Op: "replace", Path: c.path, Value: "2026-11-07"}}); err != nil {
			t.Fatalf("%s: ApplyPatchesToDocument: %v", c.name, err)
		}
		out := writtenBack(t, doc.Node)
		if strings.Contains(out, "!!timestamp") {
			t.Errorf("%s: write-back keeps the authored tag on a field a patch wrote:\n%s", c.name, out)
		}
		if want := "\"2026-11-07\"\n"; !strings.Contains(out, want) {
			t.Errorf("%s: write-back lacks %q:\n%s", c.name, want, out)
		}
	}
}

// Two distinct keys of one mapping that read the same after conversion (`0x10` is the
// integer 16, `"16"` is a string) must be refused by both loaders, because loading would
// keep one of the two values, chosen by map order, and drop the other.
func TestLoaders_RefuseNonStringKeysThatReadTheSame(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\nspec:\n  keys:\n    0x10: hexadecimal\n    \"16\": string\n"
	for name, load := range map[string]func() error{
		"multi": func() error {
			_, err := LoadResourcesFromMultiYAML(strings.NewReader(src))
			return err
		},
		"structure": func() error {
			_, err := LoadResourcesWithStructure(strings.NewReader(src))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := load()
			if err == nil {
				t.Fatal("loader accepted keys 0x10 and \"16\"")
			}
			if want := `both read "16"`; !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		})
	}
}

// An explicit !!timestamp tag on a valid timestamp keeps the authored text, like a plain
// one. On a value that is no timestamp it must still fail as yaml.v3 fails it, so the
// retag cannot turn a bad document into a string.
func TestLoaders_ExplicitTimestampTag(t *testing.T) {
	const valid = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp 2026-11-07\n"
	const invalid = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stamps\nspec:\n  stamp: !!timestamp not-a-date\n"

	multi, err := LoadResourcesFromMultiYAML(strings.NewReader(valid))
	if err != nil || len(multi) != 1 {
		t.Fatalf("LoadResourcesFromMultiYAML(valid): n=%d err=%v", len(multi), err)
	}
	requireString(t, multi[0].Object, "2026-11-07", "spec", "stamp")
	docSet, err := LoadResourcesWithStructure(strings.NewReader(valid))
	if err != nil || len(docSet.Documents) != 1 {
		t.Fatalf("LoadResourcesWithStructure(valid): err=%v", err)
	}
	requireString(t, docSet.Documents[0].Resource.Object, "2026-11-07", "spec", "stamp")
	patches, err := LoadPatchFile(strings.NewReader("- target: stamps\n  patch:\n    spec.stamp: !!timestamp 2026-11-07\n"))
	if err != nil || len(patches) != 1 {
		t.Fatalf("LoadPatchFile(valid): n=%d err=%v", len(patches), err)
	}
	if got, ok := patches[0].Patch.Value.(string); !ok || got != "2026-11-07" {
		t.Errorf("patch value is %T %v, want string \"2026-11-07\"", patches[0].Patch.Value, patches[0].Patch.Value)
	}

	if _, err := LoadResourcesFromMultiYAML(strings.NewReader(invalid)); err == nil {
		t.Error("LoadResourcesFromMultiYAML accepted !!timestamp not-a-date")
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(invalid)); err == nil {
		t.Error("LoadResourcesWithStructure accepted !!timestamp not-a-date")
	}
	if _, err := LoadPatchFile(strings.NewReader("- target: stamps\n  patch:\n    spec.stamp: !!timestamp not-a-date\n")); err == nil {
		t.Error("LoadPatchFile accepted !!timestamp not-a-date")
	}
}

// A map with a timestamp key holds its keys as strings now, so it is an ordinary
// string-keyed map and a quoted "true" under it stays the string "true", as it does under
// every other key (the sibling key). Before keys were kept, such a map could not be
// resolved at all.
func TestLoadResourcesWithStructure_TimestampKeyMapLoadsLikeStringKeyedMap(t *testing.T) {
	docSet, err := LoadResourcesWithStructure(strings.NewReader(
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stamps\ndata:\n  2026-11-07: \"true\"\n  enabled: \"true\"\n"))
	if err != nil {
		t.Fatalf("LoadResourcesWithStructure: %v", err)
	}
	obj := docSet.Documents[0].Resource.Object
	for _, key := range []string{"2026-11-07", "enabled"} {
		got, found, err := unstructured.NestedFieldNoCopy(obj, "data", key)
		if err != nil || !found {
			t.Fatalf("data.%s: found=%v err=%v", key, found, err)
		}
		if got != "true" {
			t.Errorf("data.%s = %v (%T), want string \"true\"", key, got, got)
		}
	}
}

// A NaN key must keep its value, because NaN never equals itself: a conversion that looks the
// value up again by key finds nothing and loads `NaN: null` with no error. Both loaders and
// the patch-value path are checked; the patch path is where the value was lost first.
func TestNonStringKeysKeepTheValueOfANaNKey(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\nspec:\n  keys:\n    .nan: keep-me\n"
	t.Run("multi", func(t *testing.T) {
		resources, err := LoadResourcesFromMultiYAML(strings.NewReader(src))
		if err != nil || len(resources) != 1 {
			t.Fatalf("LoadResourcesFromMultiYAML: n=%d err=%v", len(resources), err)
		}
		requireString(t, resources[0].Object, "keep-me", "spec", "keys", "NaN")
	})
	t.Run("structure", func(t *testing.T) {
		docSet, err := LoadResourcesWithStructure(strings.NewReader(src))
		if err != nil || len(docSet.Documents) != 1 {
			t.Fatalf("LoadResourcesWithStructure: err=%v", err)
		}
		requireString(t, docSet.Documents[0].Resource.Object, "keep-me", "spec", "keys", "NaN")
	})
	t.Run("patch value", func(t *testing.T) {
		patches, err := LoadPatchFile(strings.NewReader("- target: keys\n  patch:\n    spec.keys:\n      .nan: keep-me\n"))
		if err != nil || len(patches) != 1 {
			t.Fatalf("LoadPatchFile: n=%d err=%v", len(patches), err)
		}
		obj := map[string]any{"spec": map[string]any{"keys": map[string]any{}}}
		if err := applyPatchOp(obj, patches[0].Patch); err != nil {
			t.Fatalf("applyPatchOp: %v", err)
		}
		requireString(t, obj, "keep-me", "spec", "keys", "NaN")
	})
}

// A NaN key next to the string key "NaN" is the same collision as 0x10 and "16".
func TestLoaders_RefuseANaNKeyNextToItsStringSpelling(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\nspec:\n  keys:\n    .nan: float\n    \"NaN\": string\n"
	if _, err := LoadResourcesFromMultiYAML(strings.NewReader(src)); err == nil {
		t.Error("LoadResourcesFromMultiYAML accepted keys .nan and \"NaN\"")
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(src)); err == nil {
		t.Error("LoadResourcesWithStructure accepted keys .nan and \"NaN\"")
	}
}
