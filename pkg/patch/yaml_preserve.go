package patch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	keepAuthoredTimestampStyle(doc.Node, &patchedNode, touchedPaths(patches))
	if err := mergeYAMLNodes(doc.Node, &patchedNode); err != nil {
		// If merging fails, fall back to using the patched node
		doc.Node = &patchedNode
	}

	return nil
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

	keepAuthoredTimestampStyle(doc.Node, &patchedNode, touchedStrategicPaths(patches))
	if err := mergeYAMLNodes(doc.Node, &patchedNode); err != nil {
		doc.Node = &patchedNode
	}

	return nil
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
			return mergeYAMLNodes(original.Content[0], patched.Content[0])
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

// mergeMappingNodes merges mapping (object) nodes
func mergeMappingNodes(original, patched *yaml.Node) error {
	if len(patched.Content)%2 != 0 {
		return fmt.Errorf("invalid mapping node: odd number of content items")
	}

	// Create a map of keys in the patched node for efficient lookup
	patchedMap := make(map[string]*yaml.Node)
	for i := 0; i < len(patched.Content); i += 2 {
		key := patched.Content[i].Value
		value := patched.Content[i+1]
		patchedMap[key] = value
	}

	// Update existing keys in original
	for i := 0; i < len(original.Content); i += 2 {
		key := original.Content[i].Value
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

	// Add new keys from patched
	for key, value := range patchedMap {
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: key,
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
		// No recognizable merge key — replace the entire sequence
		original.Content = patched.Content
		return nil
	}

	// Build index of original items by merge key value
	origIndex := buildMergeKeyIndex(original, mergeKey)

	// Process patched items: merge into existing or append
	var merged []*yaml.Node
	seen := make(map[string]bool)

	for _, patchedItem := range patched.Content {
		keyVal := extractMergeKeyValue(patchedItem, mergeKey)
		if keyVal != "" {
			seen[keyVal] = true
			if origItem, exists := origIndex[keyVal]; exists {
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
// keys that exist in dst are transferred.
func preserveComments(src, dst *yaml.Node) {
	if src == nil || dst == nil {
		return
	}

	// Copy top-level comments
	if dst.HeadComment == "" {
		dst.HeadComment = src.HeadComment
	}
	if dst.LineComment == "" {
		dst.LineComment = src.LineComment
	}
	if dst.FootComment == "" {
		dst.FootComment = src.FootComment
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
		if src.Content[i].Kind == yaml.ScalarNode {
			srcMap[src.Content[i].Value] = kvPair{src.Content[i], src.Content[i+1]}
		}
	}

	for i := 0; i < len(dst.Content)-1; i += 2 {
		dstKey := dst.Content[i]
		dstVal := dst.Content[i+1]
		if dstKey.Kind != yaml.ScalarNode {
			continue
		}
		if srcKV, ok := srcMap[dstKey.Value]; ok {
			// Copy comments from source key and value nodes
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
			// Recurse into nested structures
			preserveComments(srcKV.value, dstVal)
		}
	}
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
// points at. A mapping or list item is paired by merge key, else by index.
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
			if authored.Content[i].Kind == yaml.ScalarNode {
				authoredValues[authored.Content[i].Value] = authored.Content[i+1]
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
		for i, item := range next.Content {
			var match *yaml.Node
			switch {
			case item.Kind == yaml.ScalarNode:
				for _, candidate := range authored.Content {
					target := resolveAlias(candidate)
					if !paired[candidate] && target.Kind == yaml.ScalarNode && target.Value == item.Value {
						match = candidate
						break
					}
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
