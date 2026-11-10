package patch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/kure/pkg/logger"

	"github.com/go-kure/launcher/pkg/errors"
)

// patchLogger is a package-level logger for debug messages.
var patchLogger = logger.New(logger.Options{
	Output: os.Stderr,
	Level:  logger.LevelDebug,
	Prefix: "patch",
})

// debugLog logs a debug message when the Debug flag is set.
func debugLog(format string, args ...any) {
	if Debug {
		patchLogger.Debug(format, args...)
	}
}

// RawPatchMap is a mapping of patch paths to values. The loader no longer decodes into it:
// a nested mapping decodes to the type of the map it sits in, so decoding the simple form
// into this named type gave every nested mapping this type. Substitution and the conversion
// to an unstructured value still accept it, for a caller that builds a value of this type.
type RawPatchMap map[string]any

type TargetedPatch struct {
	Target string         `yaml:"target"`
	Type   string         `yaml:"type,omitempty"` // "" (field-level) or "strategic"
	Patch  map[string]any `yaml:"patch"`
}

// PatchSpec ties a parsed PatchOp to an optional explicit target.
// For strategic merge patches, Strategic is non-nil and Patch is zero-value.
type PatchSpec struct {
	Target    string
	Patch     PatchOp         // field-level patch (zero value when Strategic is set)
	Strategic *StrategicPatch // non-nil for strategic merge patches
}

var Debug = os.Getenv("KURE_DEBUG") == "1"

func LoadPatchFile(r io.Reader) ([]PatchSpec, error) {
	return LoadPatchFileWithVariables(r, nil)
}

func LoadPatchFileWithVariables(r io.Reader, varCtx *VariableContext) ([]PatchSpec, error) {
	// Read all content to detect format
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read patch content")
	}

	contentStr := string(content)

	// Detect format and delegate to appropriate parser
	if IsTOMLFormat(contentStr) {
		return LoadTOMLPatchFile(strings.NewReader(contentStr), varCtx)
	} else {
		return LoadYAMLPatchFile(strings.NewReader(contentStr), varCtx)
	}
}

func LoadYAMLPatchFile(r io.Reader, varCtx *VariableContext) ([]PatchSpec, error) {
	dec := yaml.NewDecoder(r)

	var firstToken yaml.Node
	if err := dec.Decode(&firstToken); err != nil {
		return nil, errors.Wrap(err, "failed to read patch input")
	}
	if firstToken.Kind == yaml.DocumentNode && len(firstToken.Content) > 0 {
		firstToken = *firstToken.Content[0]
	}
	keepTimestampText(&firstToken)

	var patches []PatchSpec

	if firstToken.Kind == yaml.MappingNode {
		if err := refuseEquivalentKeys(&firstToken); err != nil {
			return nil, errors.Wrap(err, "invalid simple patch map")
		}
		var raw map[string]any
		if err := firstToken.Decode(&raw); err != nil {
			return nil, errors.Wrap(err, "invalid simple patch map")
		}
		for k, v := range raw {
			// Apply variable substitution to key
			substitutedKey, err := SubstituteVariables(k, varCtx)
			if err != nil {
				return nil, errors.Wrap(err, "variable substitution failed for key")
			}
			keyStr := fmt.Sprintf("%v", substitutedKey)

			// Apply variable substitution to value based on type
			substitutedValue, err := substituteVariablesInValue(v, varCtx)
			if err != nil {
				return nil, errors.Wrapf(err, "variable substitution failed for key '%s'", keyStr)
			}

			// A YAML value keeps the type YAML gave it: a quoted "8080" stays a string.
			op, err := ParsePatchLine(keyStr, substitutedValue)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid patch line '%s'", keyStr)
			}
			patches = append(patches, PatchSpec{Patch: op})
		}
	} else if firstToken.Kind == yaml.SequenceNode {
		if err := refuseEquivalentPatchKeys(&firstToken); err != nil {
			return nil, errors.Wrap(err, "invalid patch list")
		}
		var list []TargetedPatch
		if err := firstToken.Decode(&list); err != nil {
			return nil, errors.Wrap(err, "invalid patch list")
		}
		for _, entry := range list {
			if entry.Type != "" && entry.Type != "strategic" {
				return nil, errors.Errorf("unknown patch type %q for target %q: must be \"\" or \"strategic\"", entry.Type, entry.Target)
			}
			if entry.Type == "strategic" {
				if len(entry.Patch) == 0 {
					return nil, errors.Errorf("strategic patch targeting '%s' has no patch payload", entry.Target)
				}
				substitutedPatch, err := substituteVariablesInMap(entry.Patch, varCtx)
				if err != nil {
					return nil, errors.Wrapf(err, "variable substitution failed for strategic patch targeting '%s'", entry.Target)
				}
				debugLog("Strategic patch loaded: target=%s", entry.Target)
				patches = append(patches, PatchSpec{
					Target:    entry.Target,
					Strategic: &StrategicPatch{Patch: substitutedPatch},
				})
				continue
			}

			for k, v := range entry.Patch {
				// Apply variable substitution to key
				substitutedKey, err := SubstituteVariables(k, varCtx)
				if err != nil {
					return nil, errors.Wrap(err, "variable substitution failed for key")
				}
				keyStr := fmt.Sprintf("%v", substitutedKey)

				// Apply variable substitution to value based on type
				substitutedValue, err := substituteVariablesInValue(v, varCtx)
				if err != nil {
					return nil, errors.Wrapf(err, "variable substitution failed for key '%s'", keyStr)
				}

				// A YAML value keeps the type YAML gave it: a quoted "8080" stays a string.
				op, err := ParsePatchLine(keyStr, substitutedValue)
				if err != nil {
					return nil, errors.Wrapf(err, "invalid patch line '%s'", keyStr)
				}
				if err := op.NormalizePath(); err != nil {
					return nil, errors.Wrapf(err, "invalid patch path syntax: %s", op.Path)
				}
				debugLog("Targeted patch loaded: target=%s op=%s path=%s value=%v", entry.Target, op.Op, op.Path, substitutedValue)
				patches = append(patches, PatchSpec{Target: entry.Target, Patch: op})
			}
		}
	} else {
		return nil, errors.New("unrecognized patch format")
	}

	return patches, nil
}

func LoadTOMLPatchFile(r io.Reader, varCtx *VariableContext) ([]PatchSpec, error) {
	scanner := bufio.NewScanner(r)
	var patches []PatchSpec
	var currentHeader *TOMLHeader

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for TOML header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			header, err := ParseTOMLHeader(line)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid TOML header at line %d", lineNum)
			}
			currentHeader = header
			continue
		}

		// Parse key-value pair
		if currentHeader == nil {
			return nil, errors.Errorf("patch value without header at line %d: %s", lineNum, line)
		}

		// Split key: value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("invalid patch line format at line %d: %s", lineNum, line)
		}

		key := strings.TrimSpace(parts[0])
		valueStr := strings.TrimSpace(parts[1])

		// Convert TOML header to resource target and field path
		resourceTarget, fieldPath, err := currentHeader.ResolveTOMLPath()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to resolve TOML path for header %s", currentHeader.String())
		}

		// Combine field path with key if we have a field path
		var finalPath string
		if fieldPath != "" {
			finalPath = fieldPath + "." + key
		} else {
			finalPath = key
		}

		// Type the value: quoting, variable substitution and inference are tomlValue's. The field
		// name for the integer test comes from the whole path, so a key of only dots (`.: 3`) takes
		// the name of the field the header names.
		value, err := tomlValue(finalPath, valueStr, varCtx)
		if err != nil {
			return nil, errors.Wrapf(err, "variable substitution failed at line %d", lineNum)
		}

		// Create patch operation
		op, err := ParsePatchLine(finalPath, value)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid patch line '%s' at line %d", finalPath, lineNum)
		}

		if err := op.NormalizePath(); err != nil {
			return nil, errors.Wrapf(err, "invalid patch path syntax: %s", op.Path)
		}

		debugLog("TOML patch loaded: header=%s target=%s op=%s path=%s value=%v",
			currentHeader.String(), resourceTarget, op.Op, op.Path, value)

		patches = append(patches, PatchSpec{Target: resourceTarget, Patch: op})
	}

	if err := scanner.Err(); err != nil {
		return nil, errors.Wrap(err, "error reading patch file")
	}

	return patches, nil
}

// baseObject returns the object map of a base resource decoded by yaml.v3, with every
// Go int turned into an int64, including the ints inside a map with non-string keys.
// yaml.v3 decodes a YAML integer to int, and Unstructured.DeepCopy panics on that type.
// It returns an error when two distinct non-string keys of one mapping read the same
// after conversion (`0x10` and `"16"`): loading would drop one of the values.
func baseObject(raw map[string]any) (map[string]any, error) {
	var collision error
	converted, ok := convertChecked(raw, &collision).(map[string]any)
	if collision != nil {
		return nil, collision
	}
	if !ok {
		return raw, nil
	}
	return converted, nil
}

// keepTimestampText tags every timestamp scalar under n as a string, so that decoding
// the node yields the text the author wrote. Without it yaml.v3 decodes `2026-11-07`
// to a time.Time, which Unstructured.DeepCopy panics on and which formatting would
// rewrite. A quoted timestamp already decodes as a string. A scalar tagged
// !!timestamp that is no timestamp keeps its tag, so decoding still rejects it.
func keepTimestampText(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!timestamp" {
		var t time.Time
		if n.Decode(&t) == nil {
			n.Tag = "!!str"
		}
	}
	for _, child := range n.Content {
		keepTimestampText(child)
	}
}

// refuseEquivalentKeys returns an error when two keys of one mapping below top are written
// differently but decode to the same map key: `0x10` and `16`, `1.0` and `1.00`, `true` and
// `True`, `null` and `~`, an anchored key and its alias. The YAML decoder merges such keys into
// one entry before any later check sees them, so the first value would be lost without an error.
//
// The check does not keep a rule list of its own. It decodes every key with the decoder, into
// the key type the decoder gives that mapping, and compares what comes out. top is decoded into
// a string-keyed map, which keeps the text of each key. Any other mapping is decoded into a
// string-keyed map when every own key is a string or the merge key, and into a general map
// otherwise. These choices are yaml.v3 v3.0.1 as pinned in go.mod: isStringMap (decode.go:864),
// the choice in mapping (decode.go:790-795), and a merge source decoded into the map of the
// mapping that names it (merge, decode.go:959-996). The one rule copied by hand is the merge
// precedence: an entry of a merge source whose key the mapping or an earlier source already
// holds is skipped before its value is read (decode.go:828-838 and :960-968), so it loses
// nothing and is not checked. A pair of keys is refused when the decoder reads it as one key
// and does not report it: the decoder reports only a literal repeat, which has the same kind and
// text (decode.go:769-778). Keys of different Go types that read the same after conversion
// (`0x10` and `"16"`) are left to convertChecked, and complex keys are skipped. The target of an
// alias value is followed and checked where the alias is used, because the anchor may sit where the
// decoder never reads it: in a merged entry the mapping overrides, or in a field the patch entry
// does not declare. The decoder decodes the target into the same interface value as the alias
// (decode.go:533-542), so the key type is the one value picks for the target. An anchor that no
// alias reads is not read either, because nothing is lost. Call it after keepTimestampText, so a
// plain timestamp key reads as its text.
func refuseEquivalentKeys(top *yaml.Node) error {
	if top != nil && top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
		top = top.Content[0]
	}
	if top != nil && top.Kind == yaml.AliasNode && top.Alias != nil {
		top = top.Alias
	}
	if top == nil {
		return nil
	}
	c := &keyChecker{active: map[*yaml.Node]bool{}, walked: map[*yaml.Node]bool{}, followed: map[*yaml.Node]bool{}}
	if top.Kind == yaml.MappingNode {
		return c.mapping(top, stringKeys)
	}
	return c.value(top)
}

// destKeys is the key type of the map the decoder fills for a mapping.
type destKeys int

const (
	stringKeys destKeys = iota // map[string]...: a key decodes into its text
	anyKeys                    // map[interface{}]...: a key decodes into its YAML value
)

// maxSourceRepeats bounds how often the check walks a merge source it has already walked. Many
// inline sources cost one walk each and are never counted. An alias that reuses a source counts,
// because nested reuse grows exponentially while the decoder still reads the nodes, and the
// decoder rejects that growth later (decode.go:484-490).
const maxSourceRepeats = 100000

type keyChecker struct {
	walked  map[*yaml.Node]bool
	repeats int
	active  map[*yaml.Node]bool
	// followed holds the targets of the alias values value has started on. The result of value
	// depends on the node alone, so a target that many aliases reuse is checked once, and an alias
	// that reaches its own anchor ends here, as the decoder ends it (decode.go:533-537).
	followed map[*yaml.Node]bool
}

// value checks a node the decoder reads into an interface value. An alias reads its target into
// the same interface value, so the target is checked here, where it is used.
func (c *keyChecker) value(n *yaml.Node) error {
	switch n.Kind {
	case yaml.AliasNode:
		if n.Alias == nil || c.followed[n.Alias] {
			return nil
		}
		c.followed[n.Alias] = true
		// The target is read afresh, so no merge of the enclosing walk is in progress for it. Keeping
		// that walk's active sources would skip a source the target reads and leave it unchecked for
		// every later alias, which the followed memo then skips.
		outer := c.active
		c.active = map[*yaml.Node]bool{}
		err := c.value(n.Alias)
		c.active = outer
		return err
	case yaml.MappingNode:
		if isStringMap(n) {
			return c.mapping(n, stringKeys)
		}
		return c.mapping(n, anyKeys)
	case yaml.SequenceNode:
		for _, child := range n.Content {
			if err := c.value(child); err != nil {
				return err
			}
		}
	default:
		// A scalar holds no keys.
	}
	return nil
}

// mapping checks mapping n, which the decoder reads into a map of key type kt, then the merge
// sources it names.
func (c *keyChecker) mapping(n *yaml.Node, kt destKeys) error {
	if err := c.entries(n, kt, nil); err != nil {
		return err
	}
	var taken map[any]bool
	applied := map[*yaml.Node]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMergeKey(n.Content[i]) {
			continue
		}
		if taken == nil {
			taken = ownKeys(n)
		}
		for _, source := range mergeSources(n.Content[i+1]) {
			if err := c.source(source, kt, taken, applied); err != nil {
				return err
			}
		}
	}
	return nil
}

// source checks one merge source of a mapping with key type kt, then the sources it names in
// turn. taken holds the keys the destination already holds. A source that was applied once adds
// nothing the second time, and a source that is being walked is a cycle the decoder rejects.
func (c *keyChecker) source(n *yaml.Node, kt destKeys, taken map[any]bool, applied map[*yaml.Node]bool) error {
	if applied[n] || c.active[n] {
		return nil
	}
	if c.walked[n] {
		c.repeats++
		if c.repeats > maxSourceRepeats {
			return errors.New("document reuses merge sources too often to check")
		}
	}
	c.walked[n] = true
	applied[n] = true
	c.active[n] = true
	defer delete(c.active, n)
	if err := c.entries(n, kt, taken); err != nil {
		return err
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMergeKey(n.Content[i]) {
			continue
		}
		for _, next := range mergeSources(n.Content[i+1]) {
			if err := c.source(next, kt, taken, applied); err != nil {
				return err
			}
		}
	}
	return nil
}

// entries compares the keys of one mapping and checks the value of each entry the decoder reads.
// taken is nil for the mapping itself. For a merge source it holds the keys already held, and an
// entry with such a key is skipped.
func (c *keyChecker) entries(n *yaml.Node, kt destKeys, taken map[any]bool) error {
	type seenKey struct {
		node *yaml.Node
		key  any
	}
	var seen []seenKey
	for i := 0; i+1 < len(n.Content); i += 2 {
		written, value := n.Content[i], n.Content[i+1]
		if isMergeKey(written) {
			continue
		}
		key, ok := decodeKey(written, kt)
		if !ok {
			continue // the later decode reports it
		}
		for _, prior := range seen {
			if sameDecodedKey(prior.key, key) && (prior.node.Kind != written.Kind || prior.node.Value != written.Value) {
				return errors.Errorf("map keys %s and %s both read %q", keyLabel(prior.node), keyLabel(written), fmt.Sprint(key))
			}
		}
		if taken != nil {
			if taken[key] {
				continue
			}
			taken[key] = true
		}
		seen = append(seen, seenKey{written, key})
		if err := c.value(value); err != nil {
			return err
		}
	}
	return nil
}

// decodeKey decodes a key node into the key type of its map, as the decoder does. It reports
// false for a key the decoder cannot use as a map key.
func decodeKey(key *yaml.Node, kt destKeys) (any, bool) {
	if key.Kind != yaml.ScalarNode && key.Kind != yaml.AliasNode {
		return nil, false
	}
	if kt == stringKeys {
		return decodeText(key)
	}
	var value any
	if key.Decode(&value) != nil || !hashable(value) {
		return nil, false
	}
	return value, true
}

// decodeText decodes a key node into a string, as the decoder does for a string-keyed map and for
// a struct field name. A null key does not decode into a string: unmarshal returns false with no
// error (decode.go:554-562), and the decoder then skips the entry without holding its name
// (decode.go:832 and :911). Node.Decode drops that false result, so the null is tested here.
func decodeText(key *yaml.Node) (string, bool) {
	target := key
	if target.Kind == yaml.AliasNode && target.Alias != nil {
		target = target.Alias
	}
	if target.Kind == yaml.ScalarNode && target.ShortTag() == "!!null" {
		return "", false
	}
	var text string
	if key.Decode(&text) != nil {
		return "", false
	}
	return text, true
}

// ownKeys is the set the decoder starts a merge with: every own key of n decoded into an
// interface value (merge, decode.go:960-968).
func ownKeys(n *yaml.Node) map[any]bool {
	taken := map[any]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var value any
		if n.Content[i].Decode(&value) == nil && hashable(value) {
			taken[value] = true
		}
	}
	return taken
}

func hashable(v any) bool {
	t := reflect.TypeOf(v)
	return t == nil || t.Comparable()
}

// isMergeKey reports whether key is the merge key `<<`, which names sources and is no entry.
// It is yaml.v3's isMerge (decode.go:998) as pinned in go.mod: a quoted "<<" or a !!binary key
// that reads "<<" is an ordinary key.
func isMergeKey(key *yaml.Node) bool {
	return key.Kind == yaml.ScalarNode && key.Value == "<<" &&
		(key.Tag == "" || key.Tag == "!" || key.Tag == "!!merge")
}

// isStringMap is yaml.v3's isStringMap (decode.go:864), the test mapping (decode.go:790-795)
// uses to choose a string-keyed map for a mapping it reads into an interface value: every own
// key is a string or the merge key.
func isStringMap(n *yaml.Node) bool {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if tag := n.Content[i].ShortTag(); tag != "!!str" && tag != "!!merge" {
			return false
		}
	}
	return true
}

// mergeSources returns the mappings that the value of a merge key names: a mapping, an alias of
// one, or a sequence of those (merge, decode.go:959-996). Anything else is the decoder's error.
func mergeSources(value *yaml.Node) []*yaml.Node {
	switch value.Kind {
	case yaml.MappingNode:
		return []*yaml.Node{value}
	case yaml.AliasNode:
		if value.Alias != nil && value.Alias.Kind == yaml.MappingNode {
			return []*yaml.Node{value.Alias}
		}
	case yaml.SequenceNode:
		var sources []*yaml.Node
		for _, child := range value.Content {
			sources = append(sources, mergeSources(child)...)
		}
		return sources
	default:
		// A scalar names no source.
	}
	return nil
}

// keyLabel is the text a user wrote for a key: an alias reads as `*name`.
func keyLabel(key *yaml.Node) string {
	if key.Kind == yaml.AliasNode {
		return "*" + key.Value
	}
	return key.Value
}

// sameDecodedKey reports whether two decoded keys are one map key. Different Go types are
// never equal, and a NaN never equals itself, which is how the decoder treats them.
func sameDecodedKey(a, b any) bool {
	return hashable(a) && hashable(b) && a == b
}

// refuseEquivalentPatchKeys applies refuseEquivalentKeys to the patch field of every entry of
// the list form of a patch file. The decoder reads that field into a string-keyed map. An entry
// can be an alias, and can take its patch field from a merge source, so both are followed.
func refuseEquivalentPatchKeys(list *yaml.Node) error {
	for _, entry := range list.Content {
		for _, patch := range patchFields(entry) {
			if err := refuseEquivalentKeys(patch); err != nil {
				return err
			}
		}
	}
	return nil
}

// patchFields returns the value of the patch field of a list entry. The decoder reads an entry
// into a struct, decoding each key into a string (mappingStruct, decode.go:878-953), so a key
// written as `!!binary cGF0Y2g=` or as an alias of `patch` names the field too. A field in a
// merge source counts unless the entry or an earlier source holds that name (decode.go:915-920).
func patchFields(entry *yaml.Node) []*yaml.Node {
	if entry.Kind == yaml.AliasNode && entry.Alias != nil {
		entry = entry.Alias
	}
	if entry.Kind != yaml.MappingNode {
		return nil
	}
	var fields []*yaml.Node
	collectPatchFields(entry, ownKeys(entry), true, map[*yaml.Node]bool{}, &fields)
	return fields
}

func collectPatchFields(n *yaml.Node, taken map[any]bool, own bool, applied map[*yaml.Node]bool, fields *[]*yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if isMergeKey(n.Content[i]) {
			continue
		}
		name, ok := decodeText(n.Content[i])
		if !ok {
			continue
		}
		if !own {
			if taken[name] {
				continue
			}
			taken[name] = true
		}
		if name == "patch" {
			*fields = append(*fields, n.Content[i+1])
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMergeKey(n.Content[i]) {
			continue
		}
		for _, source := range mergeSources(n.Content[i+1]) {
			if !applied[source] {
				applied[source] = true
				collectPatchFields(source, taken, false, applied, fields)
			}
		}
	}
}

func LoadResourcesFromMultiYAML(r io.Reader) ([]*unstructured.Unstructured, error) {
	dec := yaml.NewDecoder(r)
	var resources []*unstructured.Unstructured
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, errors.Wrap(err, "failed to decode resource document")
		}
		keepTimestampText(&node)
		if err := refuseEquivalentKeys(&node); err != nil {
			return nil, errors.Wrap(err, "failed to decode resource document")
		}
		var raw map[string]any
		if err := node.Decode(&raw); err != nil {
			return nil, errors.Wrap(err, "failed to decode resource document")
		}
		if len(raw) > 0 {
			object, err := baseObject(raw)
			if err != nil {
				return nil, errors.Wrap(err, "failed to decode resource document")
			}
			u := &unstructured.Unstructured{Object: object}
			debugLog("Loaded resource: kind=%s name=%s", u.GetKind(), u.GetName())
			resources = append(resources, u)
		}
	}
	return resources, nil
}

func LoadPatchableAppSet(resourceReaders []io.Reader, patchReader io.Reader) (*PatchableAppSet, error) {
	var resources []*unstructured.Unstructured
	for _, r := range resourceReaders {
		rs, err := LoadResourcesFromMultiYAML(r)
		if err != nil {
			return nil, err
		}
		resources = append(resources, rs...)
	}

	patches, err := LoadPatchFile(patchReader)
	if err != nil {
		return nil, err
	}

	return NewPatchableAppSet(resources, patches)
}

func resolvePatchTarget(resources []*unstructured.Unstructured, path string) (string, string) {
	pathParts := parsePath(path)
	if len(pathParts) == 0 {
		return "", ""
	}
	first := strings.ToLower(pathParts[0])
	for _, r := range resources {
		name := strings.ToLower(r.GetName())
		kind := strings.ToLower(r.GetKind())
		if first == name || first == fmt.Sprintf("%s.%s", kind, name) {
			trimmed := strings.Join(pathParts[1:], ".")
			return r.GetName(), trimmed
		}
	}
	return "", ""
}

// CanonicalResourceKey returns the unique key for a resource.
// For namespaced resources: "namespace/kind.name"
// For cluster-scoped resources: "kind.name"
func CanonicalResourceKey(r *unstructured.Unstructured) string {
	kindName := fmt.Sprintf("%s.%s", strings.ToLower(r.GetKind()), r.GetName())
	if ns := r.GetNamespace(); ns != "" {
		return fmt.Sprintf("%s/%s", ns, kindName)
	}
	return kindName
}

// ResolveTargetKey resolves a patch target to its canonical resource key.
// Accepts short names ("my-app"), kind-qualified names ("deployment.my-app"),
// and namespace-qualified names ("staging/deployment.my-app").
// Returns an error if the target matches no resource or is ambiguous.
func ResolveTargetKey(resources []*unstructured.Unstructured, target string) (string, error) {
	// Check for namespace/kind.name format
	if idx := strings.Index(target, "/"); idx > 0 {
		ns := target[:idx]
		rest := target[idx+1:]
		lowRest := strings.ToLower(rest)
		for _, r := range resources {
			kindName := fmt.Sprintf("%s.%s", strings.ToLower(r.GetKind()), r.GetName())
			if lowRest == kindName && r.GetNamespace() == ns {
				return CanonicalResourceKey(r), nil
			}
		}
		return "", errors.Errorf("target %q not found in base resources", target)
	}

	// Try kind.name match first (case-insensitive)
	lowTarget := strings.ToLower(target)
	var kindNameMatches []*unstructured.Unstructured
	for _, r := range resources {
		kindName := fmt.Sprintf("%s.%s", strings.ToLower(r.GetKind()), r.GetName())
		if lowTarget == kindName {
			kindNameMatches = append(kindNameMatches, r)
		}
	}
	if len(kindNameMatches) == 1 {
		return CanonicalResourceKey(kindNameMatches[0]), nil
	}
	if len(kindNameMatches) > 1 {
		return "", errors.Errorf("target %q is ambiguous, matches %d resources; use namespace/kind.name to disambiguate",
			target, len(kindNameMatches))
	}

	// Try short name match
	var shortMatches []*unstructured.Unstructured
	for _, r := range resources {
		if r.GetName() == target {
			shortMatches = append(shortMatches, r)
		}
	}

	switch len(shortMatches) {
	case 0:
		return "", errors.Errorf("target %q not found in base resources", target)
	case 1:
		return CanonicalResourceKey(shortMatches[0]), nil
	default:
		names := make([]string, len(shortMatches))
		for i, r := range shortMatches {
			names[i] = fmt.Sprintf("%s.%s", strings.ToLower(r.GetKind()), r.GetName())
		}
		return "", errors.Errorf("target %q is ambiguous, matches: %s; use kind.name format",
			target, strings.Join(names, ", "))
	}
}

// smartTarget attempts to match a patch to a resource based on field presence.
func smartTarget(resources []*unstructured.Unstructured, p PatchOp) []string {
	var matches []string
	for _, r := range resources {
		if err := p.ValidateAgainst(r); err == nil {
			matches = append(matches, r.GetName())
		}
	}
	return matches
}

// resolvedPatch is the internal type used by resolvePatches.
type resolvedPatch struct {
	Patch     PatchOp
	Strategic *StrategicPatch
	Target    string
}

// resolvePatches is the shared resolution logic for NewPatchableAppSet and
// NewPatchableAppSetWithStructure. It normalizes paths, resolves targets,
// and returns wrapped patches ready for inclusion in a PatchableAppSet.
func resolvePatches(resources []*unstructured.Unstructured, patches []PatchSpec) ([]resolvedPatch, error) {
	var wrapped []resolvedPatch

	for _, spec := range patches {
		// Handle strategic merge patches
		if spec.Strategic != nil {
			target := spec.Target
			if target == "" {
				return nil, errors.New("strategic merge patch requires an explicit target")
			}
			resolved, err := ResolveTargetKey(resources, target)
			if err != nil {
				return nil, errors.Wrap(err, "strategic merge patch")
			}
			target = resolved

			debugLog("Strategic patch resolved: target=%s", target)
			wrapped = append(wrapped, resolvedPatch{Target: target, Strategic: spec.Strategic})
			continue
		}

		p := spec.Patch
		if err := p.NormalizePath(); err != nil {
			return nil, errors.Wrapf(err, "invalid patch path syntax: %s", p.Path)
		}

		var target string
		var trimmed string
		if spec.Target != "" {
			resolved, err := ResolveTargetKey(resources, spec.Target)
			if err != nil {
				if strings.Contains(err.Error(), "not found in base resources") {
					return nil, errors.Errorf("explicit target not found: %s", spec.Target)
				}
				return nil, errors.Wrapf(err, "explicit target %q", spec.Target)
			}
			target = resolved
		} else {
			target, trimmed = resolvePatchTarget(resources, p.Path)
			if target == "" {
				cands := smartTarget(resources, p)
				if len(cands) == 1 {
					target = cands[0]
				}
			}
		}

		if target == "" {
			return nil, errors.Errorf("could not determine target resource for patch path: %s", p.Path)
		}

		if trimmed != "" {
			p.Path = trimmed
		}

		debugLog("Patch resolved: target=%s op=%s path=%s value=%v", target, p.Op, p.Path, p.Value)
		wrapped = append(wrapped, resolvedPatch{Target: target, Patch: p})
	}

	return wrapped, nil
}

// toAppSetPatches converts resolvedPatch slice to the PatchableAppSet.Patches type.
func toAppSetPatches(resolved []resolvedPatch) []struct {
	Target    string
	Patch     PatchOp
	Strategic *StrategicPatch
} {
	out := make([]struct {
		Target    string
		Patch     PatchOp
		Strategic *StrategicPatch
	}, len(resolved))
	for i, r := range resolved {
		out[i] = struct {
			Target    string
			Patch     PatchOp
			Strategic *StrategicPatch
		}{Target: r.Target, Patch: r.Patch, Strategic: r.Strategic}
	}
	return out
}

// NewPatchableAppSet constructs a PatchableAppSet from already loaded resources
// and parsed patch specifications.
func NewPatchableAppSet(resources []*unstructured.Unstructured, patches []PatchSpec) (*PatchableAppSet, error) {
	resolved, err := resolvePatches(resources, patches)
	if err != nil {
		return nil, err
	}
	return &PatchableAppSet{
		Resources: resources,
		Patches:   toAppSetPatches(resolved),
	}, nil
}

// NewPatchableAppSetWithStructure constructs a PatchableAppSet with YAML structure preservation.
func NewPatchableAppSetWithStructure(documentSet *YAMLDocumentSet, patches []PatchSpec) (*PatchableAppSet, error) {
	resources := documentSet.GetResources()
	resolved, err := resolvePatches(resources, patches)
	if err != nil {
		return nil, err
	}
	return &PatchableAppSet{
		Resources:   resources,
		DocumentSet: documentSet,
		Patches:     toAppSetPatches(resolved),
	}, nil
}

// substituteVariablesInMap recursively applies variable substitution to all
// string leaf values in a map[string]interface{}, and in the mappings with non-string keys inside it.
func substituteVariablesInMap(m map[string]any, ctx *VariableContext) (map[string]any, error) {
	if ctx == nil {
		return m, nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		substituted, err := substituteVariablesInValue(v, ctx)
		if err != nil {
			return nil, err
		}
		result[k] = substituted
	}
	return result, nil
}

func substituteVariablesInValue(v any, ctx *VariableContext) (any, error) {
	switch val := v.(type) {
	case string:
		return substituteValue(val, ctx)
	case map[string]any:
		return substituteVariablesInMap(val, ctx)
	case RawPatchMap:
		return substituteVariablesInMap(val, ctx)
	case map[interface{}]interface{}:
		// yaml.v3 decodes a mapping with a non-string key (`1: x`) to this type. Its keys stay as
		// authored: Apply turns them into strings. The entries come from the range and are not looked
		// up again by key, because a NaN key never equals itself.
		if ctx == nil {
			return val, nil
		}
		result := make(map[interface{}]interface{}, len(val))
		for k, item := range val {
			substituted, err := substituteVariablesInValue(item, ctx)
			if err != nil {
				return nil, err
			}
			result[k] = substituted
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			s, err := substituteVariablesInValue(item, ctx)
			if err != nil {
				return nil, err
			}
			result[i] = s
		}
		return result, nil
	default:
		return v, nil
	}
}
