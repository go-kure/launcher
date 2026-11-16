package probe

import (
	"strconv"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// cloneValue copies the maps and slices of a decoded property value.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}

// cloneComponents copies comps so a run that authors a value leaves the probe as
// it was.
func cloneComponents(comps []oam.Component) []oam.Component {
	out := make([]oam.Component, len(comps))
	for i, c := range comps {
		c.Properties = cloneValue(c.Properties).(map[string]any)
		out[i] = c
	}
	return out
}

// splitPath turns "ports[0].protocol" into its steps: a key, or an index.
func splitPath(path string) ([]any, error) {
	var steps []any
	for _, part := range strings.Split(path, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key != "" {
			steps = append(steps, key)
		}
		for rest != "" {
			idx, tail, ok := strings.Cut(rest, "]")
			if !ok {
				return nil, errors.Errorf("path %q: unclosed index", path)
			}
			n, err := strconv.Atoi(idx)
			if err != nil {
				return nil, errors.Errorf("path %q: index %q is not a number", path, idx)
			}
			steps = append(steps, n)
			rest = strings.TrimPrefix(tail, "[")
		}
	}
	return steps, nil
}

// setAt writes value at path in props, creating the maps on the way. An index
// step must name an element that exists: a probe lists the entry it fills, so a
// missing one is a mistake in the probe, not something to invent.
func setAt(props map[string]any, path string, value any) error {
	steps, err := splitPath(path)
	if err != nil {
		return err
	}
	var cur any = props
	for i, step := range steps {
		last := i == len(steps)-1
		switch s := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return errors.Errorf("path %q: %q is not under an object", path, s)
			}
			if last {
				m[s] = cloneValue(value)
				return nil
			}
			next, ok := m[s]
			if !ok || next == nil {
				// An explicit null reads as omitted, as the pipeline reads it.
				if _, isIndex := steps[i+1].(int); isIndex {
					return errors.Errorf("path %q: no list at %q", path, s)
				}
				next = map[string]any{}
				m[s] = next
			}
			cur = next
		case int:
			l, ok := cur.([]any)
			if !ok || s < 0 || s >= len(l) {
				return errors.Errorf("path %q: no element %d", path, s)
			}
			if last {
				l[s] = cloneValue(value)
				return nil
			}
			cur = l[s]
		}
	}
	return nil
}
