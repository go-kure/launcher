package oam

import (
	"maps"
	"slices"
	"strings"
)

// DeclareBounds returns schema with PropertySchema.BoundedBy set on the
// properties bounds names. A key of bounds is a path from the top of schema,
// dotted, with "[]" after the key of an array to step into its items
// (`volumes[].type`), as PropertySchema.GatedPaths writes one. The classes join
// any the property already declares, and are published in RefusalClasses order;
// a class outside that set follows, by name.
//
// It is how a schema declaration states its own bounds over fragments it shares
// with other declarations: the schema handed in is left as it was at every
// depth, so a bound never reaches a declaration that did not state it. A path
// the schema does not declare is declared nowhere.
func DeclareBounds(schema map[string]PropertySchema, bounds map[string][]RefusalClass) map[string]PropertySchema {
	out := maps.Clone(schema)
	for path, classes := range bounds {
		declareBound(out, strings.Split(path, "."), classes)
	}
	return out
}

// declareBound declares classes at the path segs below props, a map the caller
// owns, and reports whether the path exists. A node on the way is copied
// before it is changed, and only once the path is known to exist.
func declareBound(props map[string]PropertySchema, segs []string, classes []RefusalClass) bool {
	key, items := strings.CutSuffix(segs[0], "[]")
	node, ok := props[key]
	if !ok {
		return false
	}
	if items {
		if node.Items == nil {
			return false
		}
		item, ok := boundNode(*node.Items, segs[1:], classes)
		if !ok {
			return false
		}
		node.Items = &item
	} else if node, ok = boundNode(node, segs[1:], classes); !ok {
		return false
	}
	props[key] = node
	return true
}

// boundNode returns node with classes declared at the path rest below it, and
// whether that path exists.
func boundNode(node PropertySchema, rest []string, classes []RefusalClass) (PropertySchema, bool) {
	if len(rest) == 0 {
		node.BoundedBy = joinRefusalClasses(node.BoundedBy, classes)
		return node, true
	}
	props := maps.Clone(node.Properties)
	if !declareBound(props, rest, classes) {
		return node, false
	}
	node.Properties = props
	return node, true
}

// joinRefusalClasses returns the classes of both lists, each once: the
// library's own in RefusalClasses order, then any other by name.
func joinRefusalClasses(a, b []RefusalClass) []RefusalClass {
	all := slices.Concat(a, b)
	var out []RefusalClass
	for _, c := range RefusalClasses() {
		if slices.Contains(all, c) {
			out = append(out, c)
		}
	}
	own := len(out)
	for _, c := range all {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out[own:])
	return out
}
