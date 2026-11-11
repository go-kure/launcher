package patch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/errors"
)

// YAMLDocument represents a single YAML document with preserved structure
type YAMLDocument struct {
	Node     *yaml.Node
	Resource *unstructured.Unstructured
	Original string // Original YAML content with comments
	Order    int    // Original position in file
}

// YAMLDocumentSet holds multiple YAML documents with preserved order and comments
type YAMLDocumentSet struct {
	Documents []*YAMLDocument
	Separator string // Document separator (usually "---")
}

// LoadResourcesWithStructure loads YAML resources while preserving comments and order.
// A quoted scalar keeps its authored type: "true" and "8080" load as strings, as they
// do through LoadResourcesFromMultiYAML. Only baseObject's normalisation applies, which
// turns an unquoted integer into an int64.
func LoadResourcesWithStructure(r io.Reader) (*YAMLDocumentSet, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML content: %w", err)
	}

	// Split into documents while preserving separators and content
	documents, err := parseYAMLDocuments(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML documents: %w", err)
	}

	set := &YAMLDocumentSet{
		Documents: make([]*YAMLDocument, 0, len(documents)),
		Separator: "---",
	}

	for i, docContent := range documents {
		if strings.TrimSpace(docContent) == "" {
			continue // Skip empty documents
		}

		// Parse with yaml.v3 to preserve structure
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(docContent), &node); err != nil {
			return nil, fmt.Errorf("failed to parse document %d: %w", i, err)
		}

		// Also parse into unstructured for patching. This is a second parse, so that
		// keepTimestampText does not retag the node kept for the write-back: a string tag
		// on a plain timestamp makes the encoder quote it.
		var rawNode yaml.Node
		if err := yaml.Unmarshal([]byte(docContent), &rawNode); err != nil {
			return nil, errors.Wrapf(err, "failed to parse document %d into unstructured", i)
		}
		keepTimestampText(&rawNode)
		if err := refuseEquivalentKeys(&rawNode); err != nil {
			return nil, errors.Wrapf(err, "failed to parse document %d into unstructured", i)
		}
		var raw map[string]any
		if err := rawNode.Decode(&raw); err != nil {
			return nil, errors.Wrapf(err, "failed to parse document %d into unstructured", i)
		}

		if len(raw) == 0 {
			continue // Skip empty documents
		}

		object, err := baseObject(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse document %d into unstructured", i)
		}
		resource := &unstructured.Unstructured{Object: object}

		doc := &YAMLDocument{
			Node:     &node,
			Resource: resource,
			Original: docContent,
			Order:    i,
		}

		set.Documents = append(set.Documents, doc)

		if Debug {
			fmt.Printf("Loaded document %d: kind=%s name=%s\n", i, resource.GetKind(), resource.GetName())
		}
	}

	return set, nil
}

// parseYAMLDocuments splits multi-document YAML while preserving content structure
func parseYAMLDocuments(content string) ([]string, error) {
	var documents []string
	var currentDoc strings.Builder

	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := scanner.Text()

		// Check for document separator
		if strings.TrimSpace(line) == "---" {
			// Save current document if it has content
			if currentDoc.Len() > 0 {
				documents = append(documents, currentDoc.String())
				currentDoc.Reset()
			}
			continue
		}

		// Add line to current document
		if currentDoc.Len() > 0 {
			currentDoc.WriteString("\n")
		}
		currentDoc.WriteString(line)
	}

	// Add final document
	if currentDoc.Len() > 0 {
		documents = append(documents, currentDoc.String())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading YAML content: %w", err)
	}

	return documents, nil
}

// marshalResource marshals a resource object to YAML. yaml.v3 does not know
// json.Number, which a strategic merge patch leaves for an integer beyond the
// int64 range, and would quote it as a string. A copy with a plain scalar in its
// place is marshaled; the object itself is not changed.
func marshalResource(obj map[string]any) ([]byte, error) {
	return yaml.Marshal(yamlNumbers(obj))
}

// yamlNumbers returns v with each json.Number replaced by a plain scalar node
// that holds its own text, so that no digit is rounded.
func yamlNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: t.String()}
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = yamlNumbers(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = yamlNumbers(e)
		}
		return out
	}
	return v
}

// ApplyPatchesToDocument applies patches to a YAML document while preserving structure
func (doc *YAMLDocument) ApplyPatchesToDocument(patches []PatchOp) error {
	// Apply patches to the unstructured resource
	for _, patch := range patches {
		if err := applyPatchOp(doc.Resource.Object, patch); err != nil {
			return fmt.Errorf("failed to apply patch %v: %w", patch, err)
		}
	}

	// Update the YAML node with patched data
	// First, marshal the patched resource to YAML
	patchedYAML, err := marshalResource(doc.Resource.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal patched resource: %w", err)
	}

	// Parse the patched YAML back into a node structure
	var patchedNode yaml.Node
	if err := yaml.Unmarshal(patchedYAML, &patchedNode); err != nil {
		return fmt.Errorf("failed to parse patched YAML: %w", err)
	}

	// Give an untouched timestamp its authored style, then merge to keep the comments
	return doc.mergeBack(patchedYAML, &patchedNode, touchedPaths(patches))
}

// mergeBack merges the patched node into the document. The document is written from the patched
// resource as it stands, and so loses its comments, in two cases: the merge reports an error, or the
// merged text would not load (an alias key whose anchor now sits after it, after a list is reordered).
// The merge changes the patched node (it gives its keys and items the authored text and comments), so
// the fallback is a second parse of the same bytes, made before the merge: it holds nothing of the
// document, and it loads.
func (doc *YAMLDocument) mergeBack(patchedYAML []byte, patched *yaml.Node, touched [][]string) error {
	var fallback yaml.Node
	if err := yaml.Unmarshal(patchedYAML, &fallback); err != nil {
		return errors.Wrap(err, "failed to parse patched YAML")
	}
	keepAuthoredTimestampStyle(doc.Node, patched, touched)
	keepAuthoredTimestampStyle(doc.Node, &fallback, touched)
	if err := mergeYAMLNodes(doc.Node, patched); err != nil || !loadsAgain(doc.Node) {
		doc.Node = &fallback
	}
	return nil
}

// loadsAgain reports whether the document node, written out, loads again through the loader.
func loadsAgain(node *yaml.Node) bool {
	out, err := yaml.Marshal(node)
	if err != nil {
		return false
	}
	_, err = LoadResourcesWithStructure(bytes.NewReader(out))
	return err == nil
}

// ApplyStrategicPatchesToDocument applies strategic merge patches to a YAML
// document while preserving structure. The resource is mutated in place via
// ApplyStrategicMergePatch, then the YAML node is updated to reflect the
// new state while preserving comments.
func (doc *YAMLDocument) ApplyStrategicPatchesToDocument(patches []StrategicPatch, lookup KindLookup) error {
	for _, smp := range patches {
		if err := ApplyStrategicMergePatch(doc.Resource, smp.Patch, lookup); err != nil {
			return fmt.Errorf("failed to apply strategic patch: %w", err)
		}
	}

	// Re-marshal the patched resource and merge into the existing YAML node
	patchedYAML, err := marshalResource(doc.Resource.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal patched resource: %w", err)
	}

	var patchedNode yaml.Node
	if err := yaml.Unmarshal(patchedYAML, &patchedNode); err != nil {
		return fmt.Errorf("failed to parse patched YAML: %w", err)
	}

	return doc.mergeBack(patchedYAML, &patchedNode, touchedStrategicPaths(patches))
}

// mergeYAMLNodes attempts to merge patched values into original node structure
// This is a best-effort attempt to preserve comments and formatting
func mergeYAMLNodes(original, patched *yaml.Node) error {
	if original.Kind != patched.Kind {
		return fmt.Errorf("node kinds don't match: %v vs %v", original.Kind, patched.Kind)
	}

	switch original.Kind {
	case yaml.DocumentNode:
		if len(original.Content) > 0 && len(patched.Content) > 0 {
			o, p := original.Content[0], patched.Content[0]
			if o.Kind == yaml.MappingNode && p.Kind == yaml.MappingNode {
				return mergeMappingNodesAt(o, p, true)
			}
			return mergeYAMLNodes(o, p)
		}
	case yaml.MappingNode:
		return mergeMappingNodes(original, patched)
	case yaml.SequenceNode:
		return mergeSequenceNodes(original, patched)
	case yaml.ScalarNode:
		// Update scalar value while preserving style and comments
		original.Value = patched.Value
		// Keep original style and comments
	case yaml.AliasNode:
		// Alias nodes reference other nodes; no merge needed
	}

	return nil
}

// keyText returns the text a mapping key of the kept node has in the resource. The loader turns
// a key that is not a string into the fmt.Sprint spelling of its decoded value (convertChecked),
// and it reads a plain timestamp as its text (keepTimestampText). So the authored key `0x10`
// reads "16", `.nan` reads "NaN", and `1.0` reads "1". The marshaled resource holds those
// spellings, and a pairing of the kept node with it has to use them. A key that is a string,
// or that does not decode, reads as its own text. An alias key (`*k`) reads as the key it points
// at, as the decoder reads it.
func keyText(n *yaml.Node) string {
	n = resolveAlias(n)
	if n.Kind != yaml.ScalarNode {
		return n.Value
	}
	switch n.ShortTag() {
	case "!!str", "!!timestamp":
		return n.Value
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// rawKeyText returns the text of a key as authored, an alias key being the key it points at. The
// root mapping of a document pairs by it, because the loader keeps the authored text of a root key.
func rawKeyText(n *yaml.Node) string {
	return resolveAlias(n).Value
}

// isScalarKey reports whether a key is a scalar, or an alias of one.
func isScalarKey(n *yaml.Node) bool {
	return resolveAlias(n).Kind == yaml.ScalarNode
}

// mergeMappingNodes merges mapping (object) nodes. An original key pairs with the patched key
// that holds its resource spelling (keyText), and the merge keeps the original key node, so an
// authored key such as `0x10` is written as authored and not beside its converted spelling.
// The root mapping of a document is the exception: the loader decodes it into a string-keyed
// map, so a root key keeps its authored text and pairs by it (root). Two authored keys of one
// mapping never read the same here, because the loaders refuse such a document. One exception
// is a key the patch adds with the text of an authored key (the string "0x10" beside `0x10`): the
// authored key then takes its resource spelling ("16") and the new key is written quoted.
func mergeMappingNodes(original, patched *yaml.Node) error {
	return mergeMappingNodesAt(original, patched, false)
}

// mergeMappingNodesAt is mergeMappingNodes, with root true for the root mapping of a document.
func mergeMappingNodesAt(original, patched *yaml.Node, root bool) error {
	if len(patched.Content)%2 != 0 {
		return fmt.Errorf("invalid mapping node: odd number of content items")
	}
	pairKey := func(n *yaml.Node) string {
		if root {
			return rawKeyText(n)
		}
		return keyText(n)
	}
	// Create a map of keys in the patched node for efficient lookup
	patchedMap := make(map[string]*yaml.Node)
	patchedKeys := make(map[string]*yaml.Node)
	for i := 0; i < len(patched.Content); i += 2 {
		key := patched.Content[i].Value
		value := patched.Content[i+1]
		patchedMap[key] = value
		patchedKeys[key] = patched.Content[i]
	}

	// Update existing keys in original
	for i := 0; i < len(original.Content); i += 2 {
		key := pairKey(original.Content[i])
		if patchedValue, exists := patchedMap[key]; exists {
			// Recursively merge if both are objects/arrays, otherwise replace
			if original.Content[i+1].Kind == patchedValue.Kind &&
				(patchedValue.Kind == yaml.MappingNode || patchedValue.Kind == yaml.SequenceNode) {
				if err := mergeYAMLNodes(original.Content[i+1], patchedValue); err != nil {
					return err
				}
			} else {
				// Replace the value node but keep the key node (preserves comments on key)
				original.Content[i+1] = patchedValue
			}
			delete(patchedMap, key) // Mark as processed
		}
	}

	// Add new keys from patched.
	// A new key whose text an authored key already holds (a string key "0x10" beside the authored
	// key `0x10`) cannot sit beside it: the decoder reads two keys with one text as a duplicate. The
	// authored key then takes its resource spelling ("16") and the new key is written quoted.
	held := make(map[string]*yaml.Node, len(original.Content)/2)
	for i := 0; i+1 < len(original.Content); i += 2 {
		if original.Content[i].Kind == yaml.ScalarNode {
			held[original.Content[i].Value] = original.Content[i]
		}
	}
	for key, value := range patchedMap {
		// A new key keeps the tag and style the marshaler gave it, so a string key that reads as
		// a number (`"16"`) is written quoted, and so does not read as another key.
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: key,
			Tag:   patchedKeys[key].Tag,
			Style: patchedKeys[key].Style,
		}
		if authored, ok := held[key]; ok {
			// The authored key takes its resource spelling. Where another key of the mapping already
			// holds that text, the written mapping holds it twice and does not load. mergeBack finds
			// that when it loads the merged text again, and writes the patched resource as it stands:
			// it loads, and the comments of the document are lost.
			authored.Value, authored.Tag, authored.Style = keyText(authored), "!!str", 0
			keyNode.Tag, keyNode.Style = "!!str", yaml.DoubleQuotedStyle
		}
		original.Content = append(original.Content, keyNode, value)
	}

	return nil
}

// mergeSequenceNodes replaces the original sequence with the patched sequence
// while preserving comments from matched items. This is NOT a true merge:
// items present in the original but absent in the patched set are dropped.
// Callers are expected to provide a patched set that already contains all
// surviving items (e.g. the output of a strategic merge patch).
// When items can be matched by a well-known merge key (e.g. "name"), comments
// and formatting are preserved on existing items. Items without a match key
// fall back to full replacement.
func mergeSequenceNodes(original, patched *yaml.Node) error {
	mergeKey := detectMergeKey(original)
	if mergeKey == "" {
		// No recognizable merge key — replace the entire sequence. The items keep the authored text
		// of their keys where the two lists pair by position (preserveSequence).
		preserveSequence(original, patched, true)
		original.Content = patched.Content
		return nil
	}

	// Build index of original items by merge key value. A value that more than one item holds names
	// no item: the index is only read to copy comments and key text, and neither item can be shown
	// to be the item the resource holds. The same holds for a value that more than one patched item
	// holds: each of them would take the comments of the one original item. The merged items are the
	// patched items either way.
	origIndex := buildUniqueMergeKeyIndex(original, mergeKey)
	repeated := repeatedMergeKeyValues(patched, mergeKey)

	// Process patched items: merge into existing or append
	var merged []*yaml.Node
	seen := make(map[string]bool)

	for _, patchedItem := range patched.Content {
		keyVal := extractMergeKeyValue(patchedItem, mergeKey)
		if keyVal != "" {
			seen[keyVal] = true
			if origItem, exists := origIndex[keyVal]; exists && !repeated[keyVal] {
				// Use patched item as the base (correct field set),
				// but preserve comments from the original item.
				preserveComments(origItem, patchedItem)
				merged = append(merged, patchedItem)
				continue
			}
		}
		merged = append(merged, patchedItem)
	}

	original.Content = merged
	return nil
}

// commonMergeKeys are the well-known keys used by Kubernetes strategic merge
// patch to identify list items.
var commonMergeKeys = []string{
	"name",
	"containerPort",
	"port",
	"mountPath",
	"key",
	"ip",
}

// detectMergeKey examines the first mapping item in a sequence to find a
// recognized merge key. Returns "" if none found.
func detectMergeKey(seq *yaml.Node) string {
	if len(seq.Content) == 0 {
		return ""
	}
	first := seq.Content[0]
	if first.Kind != yaml.MappingNode {
		return ""
	}
	keys := make(map[string]bool)
	for i := 0; i < len(first.Content); i += 2 {
		if first.Content[i].Kind == yaml.ScalarNode {
			keys[first.Content[i].Value] = true
		}
	}
	for _, mk := range commonMergeKeys {
		if keys[mk] {
			return mk
		}
	}
	return ""
}

// buildMergeKeyIndex builds a map from merge-key value to the original node.
func buildMergeKeyIndex(seq *yaml.Node, mergeKey string) map[string]*yaml.Node {
	index := make(map[string]*yaml.Node)
	for _, item := range seq.Content {
		if val := extractMergeKeyValue(item, mergeKey); val != "" {
			index[val] = item
		}
	}
	return index
}

// extractMergeKeyValue extracts the value of the merge key from a mapping node.
func extractMergeKeyValue(node *yaml.Node, mergeKey string) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == mergeKey {
			if node.Content[i+1].Kind == yaml.ScalarNode {
				return node.Content[i+1].Value
			}
		}
	}
	return ""
}

// preserveComments copies comments from src (original) to dst (patched) by
// walking both mapping nodes in parallel and matching keys. Only comments on
// keys that exist in dst are transferred. It also gives a key of dst the authored
// text of the key it pairs with, in the mapping and in every list below it.
func preserveComments(src, dst *yaml.Node) {
	preserveAt(src, dst, true)
}

// preserveAt is preserveComments, with comments false to restore the key text of the pairs and
// to copy no comment. A list with no merge key pairs its items by position, and a position is no
// proof that two items are the same item, so a comment never follows it.
func preserveAt(src, dst *yaml.Node, comments bool) {
	if src == nil || dst == nil {
		return
	}

	// Copy top-level comments
	if comments {
		if dst.HeadComment == "" {
			dst.HeadComment = src.HeadComment
		}
		if dst.LineComment == "" {
			dst.LineComment = src.LineComment
		}
		if dst.FootComment == "" {
			dst.FootComment = src.FootComment
		}
	}

	if src.Kind == yaml.SequenceNode && dst.Kind == yaml.SequenceNode {
		preserveSequence(src, dst, comments)
		return
	}
	if src.Kind != yaml.MappingNode || dst.Kind != yaml.MappingNode {
		return
	}

	// Build index of src keys -> (keyNode, valueNode)
	type kvPair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	srcMap := make(map[string]kvPair)
	for i := 0; i < len(src.Content)-1; i += 2 {
		if isScalarKey(src.Content[i]) {
			srcMap[keyText(src.Content[i])] = kvPair{src.Content[i], src.Content[i+1]}
		}
	}

	dstTexts := make(map[string]bool, len(dst.Content)/2)
	for i := 0; i < len(dst.Content)-1; i += 2 {
		if dst.Content[i].Kind == yaml.ScalarNode {
			dstTexts[dst.Content[i].Value] = true
		}
	}

	for i := 0; i < len(dst.Content)-1; i += 2 {
		dstKey := dst.Content[i]
		dstVal := dst.Content[i+1]
		if dstKey.Kind != yaml.ScalarNode {
			continue
		}
		if srcKV, ok := srcMap[dstKey.Value]; ok {
			// A key that is not a string reads differently in the resource (`0x10` reads "16"):
			// the item keeps the authored key text, tag and style. It does not when another key of
			// the item already has that text, because the item would then hold the text twice.
			// An alias key is written as the alias: it is a different kind of node and never
			// repeats the text of a scalar key.
			if srcKV.key.Kind == yaml.AliasNode {
				dstKey.Kind, dstKey.Alias = srcKV.key.Kind, srcKV.key.Alias
				dstKey.Value, dstKey.Tag, dstKey.Style = srcKV.key.Value, srcKV.key.Tag, srcKV.key.Style
			} else if dstKey.Value == srcKV.key.Value {
				// The same text can be written as another type: the authored `16` against the
				// resource's string "16". The key keeps its authored tag and style.
				dstKey.Tag, dstKey.Style = srcKV.key.Tag, srcKV.key.Style
			} else if !dstTexts[srcKV.key.Value] {
				dstKey.Value, dstKey.Tag, dstKey.Style = srcKV.key.Value, srcKV.key.Tag, srcKV.key.Style
			}
			// The key keeps the anchor it declares, so an alias key written elsewhere still has
			// its anchor (`&k 0x10` in one item, `*k` in another).
			if srcKV.key.Anchor != "" {
				dstKey.Anchor = srcKV.key.Anchor
			}
			// Copy comments from source key and value nodes
			if comments {
				if dstKey.HeadComment == "" {
					dstKey.HeadComment = srcKV.key.HeadComment
				}
				if dstKey.LineComment == "" {
					dstKey.LineComment = srcKV.key.LineComment
				}
				if dstKey.FootComment == "" {
					dstKey.FootComment = srcKV.key.FootComment
				}
				if dstVal.HeadComment == "" {
					dstVal.HeadComment = srcKV.value.HeadComment
				}
				if dstVal.LineComment == "" {
					dstVal.LineComment = srcKV.value.LineComment
				}
				if dstVal.FootComment == "" {
					dstVal.FootComment = srcKV.value.FootComment
				}
			}
			// Recurse into nested structures
			preserveAt(srcKV.value, dstVal, comments)
		}
	}
}

// preserveSequence pairs the items of two lists and restores what preserveAt restores. Items pair
// by merge key when the authored list has one, and then comments follow as in mergeSequenceNodes.
// A merge-key value that more than one authored item holds pairs nothing: neither item can be shown
// to be the item the resource holds. The same holds for a value that more than one written item
// holds: each of them would take the text and the comments of the one authored item, and at most one
// of them is that item. Without a merge key an item pairs by position only when the two
// lists have the same length and the two items hold the same data. After an add, a delete, a reorder
// or a change of the item, a position can name another item, so no pair is made and each item keeps
// the text the resource gave it. The rule is no pair rather than a wrong pair.
func preserveSequence(src, dst *yaml.Node, comments bool) {
	if mergeKey := detectMergeKey(src); mergeKey != "" {
		index := buildUniqueMergeKeyIndex(src, mergeKey)
		repeated := repeatedMergeKeyValues(dst, mergeKey)
		for _, item := range dst.Content {
			val := extractMergeKeyValue(item, mergeKey)
			if repeated[val] {
				continue
			}
			if match, ok := index[val]; ok {
				// A list below an item that paired by position copies no comment either: a merge key
				// names the child, and cannot show that the parent is the same item.
				preserveAt(match, item, comments)
			}
		}
		return
	}
	if len(src.Content) != len(dst.Content) {
		return
	}
	// The loop is bounded by both lengths, whatever the check above says, so a position never reads
	// past the end of the shorter list.
	for i := range min(len(src.Content), len(dst.Content)) {
		if sameItemData(src.Content[i], dst.Content[i]) {
			preserveAt(src.Content[i], dst.Content[i], false)
		}
	}
}

// buildUniqueMergeKeyIndex is buildMergeKeyIndex without the values that more than one item holds.
func buildUniqueMergeKeyIndex(seq *yaml.Node, mergeKey string) map[string]*yaml.Node {
	index := make(map[string]*yaml.Node)
	seen := make(map[string]bool)
	for _, item := range seq.Content {
		val := extractMergeKeyValue(item, mergeKey)
		if val == "" {
			continue
		}
		if seen[val] {
			delete(index, val)
			continue
		}
		seen[val] = true
		index[val] = item
	}
	return index
}

// repeatedMergeKeyValues returns the merge-key values that more than one item of the list holds.
func repeatedMergeKeyValues(seq *yaml.Node, mergeKey string) map[string]bool {
	count := make(map[string]int)
	repeated := make(map[string]bool)
	for _, item := range seq.Content {
		val := extractMergeKeyValue(item, mergeKey)
		if val == "" {
			continue
		}
		count[val]++
		if count[val] > 1 {
			repeated[val] = true
		}
	}
	return repeated
}

// sameItemData reports whether an authored list item and a written list item hold the same data, as
// the resource reads them: a key compares by its resource spelling (`0x10` and "16" are the same
// key). A merge entry (`<<`) is read as a key of its own and is not
// expanded, so an authored item that uses one differs from the written item, which holds the
// expanded entries, and pairs with nothing.
func sameItemData(a, b *yaml.Node) bool {
	return reflect.DeepEqual(itemData(a), itemData(b))
}

// unreadData stands for what itemData does not read: the value of a merge entry, and a node that
// holds itself through an alias. A written item never holds it, so an item that does equals nothing.
type unreadData struct{}

// nanData stands for a NaN. reflect.DeepEqual never equals two NaNs, and an unchanged `.nan` must
// equal itself.
type nanData struct{}

// itemData reads a node as the data the resource holds: mappings keyed by resource spelling, lists,
// and scalars as their decoded value. A scalar that does not decode reads as null: the loaders refuse
// such a document before a write-back runs. The value of a merge entry is not read, because the
// loader skips a merged entry that the mapping holds as its own, so a merge source can hold
// anything, a cycle included. A cycle through any other alias reads as unreadData, and so no shape
// of document can loop here.
func itemData(node *yaml.Node) any {
	return itemDataAt(node, map[*yaml.Node]bool{})
}

func itemDataAt(node *yaml.Node, onPath map[*yaml.Node]bool) any {
	n := resolveAlias(node)
	if onPath[n] {
		return unreadData{}
	}
	switch n.Kind {
	case yaml.MappingNode:
		onPath[n] = true
		defer delete(onPath, n)
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].ShortTag() == "!!merge" {
				out[keyText(n.Content[i])] = unreadData{}
				continue
			}
			out[keyText(n.Content[i])] = itemDataAt(n.Content[i+1], onPath)
		}
		return out
	case yaml.SequenceNode:
		onPath[n] = true
		defer delete(onPath, n)
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, itemDataAt(c, onPath))
		}
		return out
	case yaml.ScalarNode:
		var v any
		_ = n.Decode(&v)
		return comparableScalar(v)
	default:
		// resolveAlias leaves no alias node, and a document node never reaches here.
		return nil
	}
}

// comparableScalar gives a decoded scalar the form the marshaler's output decodes to. The marshaler
// writes the float 1.0 as `1`, which decodes as an int, so a whole float reads as that int on both
// sides, and a NaN reads as one value that equals itself.
func comparableScalar(v any) any {
	f, ok := v.(float64)
	if !ok {
		return v
	}
	if math.IsNaN(f) {
		return nanData{}
	}
	if f > -(1<<53) && f < 1<<53 && f == math.Trunc(f) {
		return int(f)
	}
	return f
}

// keepAuthoredTimestampStyle gives each scalar under next the tag and style of the authored
// scalar it replaces, when the authored scalar is a timestamp and next holds the same text as
// a string. The loader hands a timestamp to the resource as its text, so the marshaled resource
// holds a string, and yaml.v3 quotes a string that reads as a timestamp. With the authored tag
// and style an untouched timestamp is written as it was authored. A value with other text or
// another type keeps its own node, so a timestamp a patch sets is still written quoted.
//
// It runs on the patched node before the merge, so it covers every way the merge keeps or
// replaces a node. A path in touched, or under one, is a value a patch wrote: it keeps its own
// node even when its text equals the authored text, as in a batch that changes a timestamp to
// another type and back. Mappings pair entries by scalar key. A sequence pairs a scalar item
// with the first unpaired authored scalar of the same text, so a deletion that moves items
// down does not break the pairs. An authored item that is an alias counts as the scalar it
// points at. A mapping or list item is paired by merge key, else by index. The pairing takes
// time linear in the list length.
//
// Limits, all of them quoting style and never a value: a patch that addresses a list item by
// index or by key leaves every timestamp in that list quoted; after a delete, items that pair
// by index (mapping items with no merge key, lists inside a list) may stay quoted; and where
// one list holds equal text in two styles (`"2026-11-07"` and a timestamp), a delete can leave
// the survivor in the style of the item that was removed.
func keepAuthoredTimestampStyle(authored, next *yaml.Node, touched [][]string) {
	keepAuthoredStyleAt(authored, next, nil, touched)
}

// touchedPaths lists the field paths that patches write. A path stops at its first selector, so
// a patch that addresses a list item by index or by key touches the whole list. The path as
// written is recorded as well, for a field name that holds literal brackets. A delete writes
// nothing: the items it leaves keep their authored style.
func touchedPaths(patches []PatchOp) [][]string {
	var out [][]string
	for _, patch := range patches {
		if patch.Op == "delete" {
			continue
		}
		var path []string
		literal := parsePath(patch.Path)
		for _, segment := range literal {
			i := strings.Index(segment, "[")
			if i < 0 {
				path = append(path, segment)
				continue
			}
			if i > 0 {
				path = append(path, segment[:i])
			}
			break
		}
		if len(path) > 0 {
			out = append(out, path)
		}
		// A root key that starts with a bracket cuts to an empty path, but the literal one still names it.
		if len(literal) > 0 && !slices.Equal(path, literal) {
			out = append(out, literal)
		}
	}
	return out
}

// touchedStrategicPaths lists the field paths that strategic patches write: every key of a
// patch map down to its first non-mapping value. A list in a patch touches the whole list. An
// empty mapping writes nothing, so it touches nothing.
func touchedStrategicPaths(patches []StrategicPatch) [][]string {
	var out [][]string
	var walk func(path []string, value map[string]any)
	walk = func(path []string, value map[string]any) {
		for key, v := range value {
			p := append(slices.Clone(path), key)
			if m, ok := v.(map[string]any); ok {
				walk(p, m)
				continue
			}
			out = append(out, p)
		}
	}
	for _, patch := range patches {
		walk(nil, patch.Patch)
	}
	return out
}

// isTouched reports whether path equals a touched path or lies under one.
func isTouched(path []string, touched [][]string) bool {
	for _, t := range touched {
		if len(t) <= len(path) && slices.Equal(t, path[:len(t)]) {
			return true
		}
	}
	return false
}

// resolveAlias returns the node an alias points at, and any other node as it is. An alias with
// no target is returned as it is.
func resolveAlias(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}

func keepAuthoredStyleAt(authored, next *yaml.Node, path []string, touched [][]string) {
	if authored != nil {
		authored = resolveAlias(authored) // an alias is the node it points at
	}
	if authored == nil || next == nil || authored.Kind != next.Kind || isTouched(path, touched) {
		return
	}
	switch authored.Kind {
	case yaml.AliasNode:
		// an alias with no target (not resolved above): nothing to keep
	case yaml.ScalarNode:
		if authored.ShortTag() != "!!timestamp" || next.ShortTag() != "!!str" || next.Value != authored.Value {
			return
		}
		var t time.Time
		if authored.Decode(&t) != nil {
			return
		}
		next.Tag = authored.Tag
		next.Style = authored.Style
	case yaml.MappingNode:
		authoredValues := make(map[string]*yaml.Node, len(authored.Content)/2)
		for i := 0; i+1 < len(authored.Content); i += 2 {
			if isScalarKey(authored.Content[i]) {
				// The root mapping keeps its authored key text in the resource (mergeMappingNodesAt).
				if len(path) == 0 {
					authoredValues[rawKeyText(authored.Content[i])] = authored.Content[i+1]
				} else {
					authoredValues[keyText(authored.Content[i])] = authored.Content[i+1]
				}
			}
		}
		for i := 0; i+1 < len(next.Content); i += 2 {
			if next.Content[i].Kind != yaml.ScalarNode {
				continue
			}
			key := next.Content[i].Value
			if value, ok := authoredValues[key]; ok {
				keepAuthoredStyleAt(value, next.Content[i+1], append(slices.Clone(path), key), touched)
			}
		}
	case yaml.DocumentNode:
		for i := 0; i < len(authored.Content) && i < len(next.Content); i++ {
			keepAuthoredStyleAt(authored.Content[i], next.Content[i], path, touched)
		}
	case yaml.SequenceNode:
		mergeKey := detectMergeKey(authored)
		var byKey map[string]*yaml.Node
		if mergeKey != "" {
			byKey = buildMergeKeyIndex(authored, mergeKey)
		}
		paired := make(map[*yaml.Node]bool, len(authored.Content))
		// The authored scalar items by text, in list order, built once on the first scalar item.
		// resume holds, for each text, the first index that can still be unpaired: an item before
		// it is paired, and an item is never unpaired. So each authored item is passed at most
		// once and the pairing takes time linear in the list length.
		var byText map[string][]*yaml.Node
		var resume map[string]int
		for i, item := range next.Content {
			var match *yaml.Node
			switch {
			case item.Kind == yaml.ScalarNode:
				if byText == nil {
					byText = make(map[string][]*yaml.Node)
					resume = make(map[string]int)
					for _, candidate := range authored.Content {
						if target := resolveAlias(candidate); target.Kind == yaml.ScalarNode {
							byText[target.Value] = append(byText[target.Value], candidate)
						}
					}
				}
				candidates := byText[item.Value]
				j := resume[item.Value]
				for j < len(candidates) && paired[candidates[j]] {
					j++
				}
				resume[item.Value] = j
				if j < len(candidates) {
					match = candidates[j]
				}
			default:
				if mergeKey != "" {
					if candidate, ok := byKey[extractMergeKeyValue(item, mergeKey)]; ok && !paired[candidate] {
						match = candidate
					}
				}
				if match == nil && i < len(authored.Content) && !paired[authored.Content[i]] {
					match = authored.Content[i]
				}
			}
			if match != nil {
				paired[match] = true
				keepAuthoredStyleAt(match, item, path, touched)
			}
		}
	}
}

// WriteToFile writes the document set to a file with preserved structure
func (set *YAMLDocumentSet) WriteToFile(filename string) error {
	var buf bytes.Buffer

	for i, doc := range set.Documents {
		if i > 0 {
			buf.WriteString(set.Separator + "\n")
		}

		// Marshal the updated node back to YAML
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)

		if err := encoder.Encode(doc.Node); err != nil {
			return fmt.Errorf("failed to encode document %d: %w", i, err)
		}

		_ = encoder.Close()
	}

	// Write to file
	content := buf.String()
	// Clean up extra newlines at the end
	content = strings.TrimSuffix(content, "\n") + "\n"

	if err := writeFile(filename, []byte(content)); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filename, err)
	}

	return nil
}

// writeFile is a helper to write content to a file
func writeFile(filename string, content []byte) error {
	return os.WriteFile(filename, content, 0644)
}

// GetResources returns the unstructured resources in order
func (set *YAMLDocumentSet) GetResources() []*unstructured.Unstructured {
	resources := make([]*unstructured.Unstructured, len(set.Documents))
	for i, doc := range set.Documents {
		resources[i] = doc.Resource
	}
	return resources
}

// FindDocumentByName finds a document by resource name
func (set *YAMLDocumentSet) FindDocumentByName(name string) *YAMLDocument {
	for _, doc := range set.Documents {
		if doc.Resource.GetName() == name {
			return doc
		}
	}
	return nil
}

// FindDocumentByKindAndName finds a document by resource kind and name
func (set *YAMLDocumentSet) FindDocumentByKindAndName(kind, name string) *YAMLDocument {
	for _, doc := range set.Documents {
		if strings.EqualFold(doc.Resource.GetKind(), kind) &&
			doc.Resource.GetName() == name {
			return doc
		}
	}
	return nil
}

// FindDocumentByKindNameAndNamespace finds a document by kind, name, and namespace.
// All three must match for a result.
func (set *YAMLDocumentSet) FindDocumentByKindNameAndNamespace(kind, name, namespace string) *YAMLDocument {
	for _, doc := range set.Documents {
		if strings.EqualFold(doc.Resource.GetKind(), kind) &&
			doc.Resource.GetName() == name &&
			doc.Resource.GetNamespace() == namespace {
			return doc
		}
	}
	return nil
}

// UpdateDocumentFromResource updates a document's YAML node from its resource. It cannot tell
// which fields a patch wrote, so a timestamp whose text is unchanged keeps its authored style.
func (doc *YAMLDocument) UpdateDocumentFromResource() error {
	return doc.updateDocumentFromResource(nil)
}

// updateDocumentFromResource is UpdateDocumentFromResource for a caller that applied patches to
// the resource and knows the paths they wrote: those fields keep their own node.
func (doc *YAMLDocument) updateDocumentFromResource(touched [][]string) error {
	// Marshal the updated resource to YAML
	updatedYAML, err := marshalResource(doc.Resource.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal updated resource: %w", err)
	}

	// Parse the updated YAML back into a node structure
	var updatedNode yaml.Node
	if err := yaml.Unmarshal(updatedYAML, &updatedNode); err != nil {
		return fmt.Errorf("failed to parse updated YAML: %w", err)
	}

	// For now, use the updated node directly to ensure correctness
	// Comment preservation will be improved in a future iteration
	keepAuthoredTimestampStyle(doc.Node, &updatedNode, touched)
	doc.Node = &updatedNode

	return nil
}

// GenerateOutputFilename creates the output filename based on the pattern
// <outputDir>/<originalname>-patch-<patchname>.yaml
func GenerateOutputFilename(originalPath, patchPath, outputDir string) string {
	// Extract base names without extensions
	originalBase := extractBaseName(originalPath)
	patchBase := extractBaseName(patchPath)

	if outputDir == "" {
		outputDir = "." // Default to current directory
	}

	return fmt.Sprintf("%s/%s-patch-%s.yaml", outputDir, originalBase, patchBase)
}

// Copy creates a deep copy of the YAMLDocumentSet
func (set *YAMLDocumentSet) Copy() (*YAMLDocumentSet, error) {
	copiedSet := &YAMLDocumentSet{
		Documents: make([]*YAMLDocument, len(set.Documents)),
		Separator: set.Separator,
	}

	for i, doc := range set.Documents {
		// Deep copy the YAML node
		copiedNode, err := copyYAMLNode(doc.Node)
		if err != nil {
			return nil, fmt.Errorf("failed to copy YAML node for document %d: %w", i, err)
		}

		// Deep copy the unstructured resource
		copiedResource := doc.Resource.DeepCopy()

		copiedSet.Documents[i] = &YAMLDocument{
			Node:     copiedNode,
			Resource: copiedResource,
			Original: doc.Original, // String is immutable, safe to share
			Order:    doc.Order,
		}
	}

	return copiedSet, nil
}

// copyYAMLNode creates a deep copy of a yaml.Node
func copyYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	if node == nil {
		return nil, nil
	}

	copied := &yaml.Node{
		Kind:        node.Kind,
		Style:       node.Style,
		Tag:         node.Tag,
		Value:       node.Value,
		Anchor:      node.Anchor,
		Alias:       node.Alias,
		Content:     make([]*yaml.Node, len(node.Content)),
		HeadComment: node.HeadComment,
		LineComment: node.LineComment,
		FootComment: node.FootComment,
		Line:        node.Line,
		Column:      node.Column,
	}

	// Recursively copy content nodes
	for i, child := range node.Content {
		copiedChild, err := copyYAMLNode(child)
		if err != nil {
			return nil, fmt.Errorf("failed to copy child node %d: %w", i, err)
		}
		copied.Content[i] = copiedChild
	}

	return copied, nil
}

// extractBaseName extracts the base name without extension from a file path
func extractBaseName(filePath string) string {
	// Find the last slash to get filename
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash == -1 {
		lastSlash = strings.LastIndex(filePath, "\\") // Windows path
	}

	filename := filePath
	if lastSlash >= 0 {
		filename = filePath[lastSlash+1:]
	}

	// Remove extension
	if dotIndex := strings.LastIndex(filename, "."); dotIndex > 0 {
		filename = filename[:dotIndex]
	}

	// Clean up filename for use in output (remove invalid characters)
	re := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	return re.ReplaceAllString(filename, "-")
}
