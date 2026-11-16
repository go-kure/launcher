package probe

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// declared is one default a handler schema declares for a property the probe
// document leaves out.
type declared struct {
	component, typ, path string
	value                any
}

// declaredDefaults walks a document and its schema together and lists the
// declared defaults of the properties the document does not write, at the top
// level and inside every object and list entry the document does write.
func declaredDefaults(comp oam.Component, schema map[string]oam.PropertySchema) []declared {
	var out []declared
	var walk func(props map[string]any, s map[string]oam.PropertySchema, prefix string)
	walk = func(props map[string]any, s map[string]oam.PropertySchema, prefix string) {
		for _, name := range sortedNames(s) {
			ps := s[name]
			v, written := props[name]
			if !written || v == nil {
				// An explicit null reads as omitted, as the pipeline reads it.
				if ps.Default != nil {
					out = append(out, declared{comp.Name, comp.Type, prefix + name, ps.Default})
				}
				continue
			}
			switch t := v.(type) {
			case map[string]any:
				if len(ps.Properties) > 0 {
					walk(t, ps.Properties, prefix+name+".")
				}
			case []any:
				if ps.Items == nil || len(ps.Items.Properties) == 0 {
					continue
				}
				for i, e := range t {
					if m, ok := e.(map[string]any); ok {
						walk(m, ps.Items.Properties, fmt.Sprintf("%s%s[%d].", prefix, name, i))
					}
				}
			}
		}
	}
	walk(comp.Properties, schema, "")
	return out
}

func sortedNames(m map[string]oam.PropertySchema) []string {
	return slices.Sorted(maps.Keys(m))
}

// CheckDeclaredDefaults takes every default a handler schema declares for a
// property a probe document leaves out. Writing the default must give the same
// generated objects as leaving it out, and the report must then hold an entry
// at that path: a default the library applies and does not report is a finding,
// and so is a declared default the library does not apply. It runs without a
// policy, so a builtin fill is not hidden behind a policy default.
//
// It reads the schema declarations only. A fill no schema declares (the resource
// requests, the token automount) is not seen here; the probes' own Fill rows and
// check B carry those.
func CheckDeclaredDefaults(tr *oam.Transformer, probes []Probe) []string {
	var findings []string
	add := func(format string, args ...any) { findings = append(findings, fmt.Sprintf(format, args...)) }
	schemas := tr.HandlerSchemas().Components
	for _, p := range probes {
		prov, base, err := Run(tr, cloneComponents(p.Components), NoPolicy())
		if err != nil {
			// A probe that states it is refused without a policy has no document to
			// compare; check B holds that outcome against the statement. Any other
			// refusal is a finding here too, never a silent skip.
			if p.RefusedWithoutPolicy == "" {
				add("probe %q: refused without a policy, and the probe does not say so, so no declared default was checked: %v", p.Name, err)
			}
			continue
		}
		for _, comp := range p.Components {
			if _, ok := schemas[comp.Type]; !ok {
				add("probe %q: no schema is registered under the component type %q, so no declared default was checked", p.Name, comp.Type)
				continue
			}
			for _, d := range declaredDefaults(comp, schemas[comp.Type]) {
				label := fmt.Sprintf("probe %q: the %s schema declares %v as the default of %s", p.Name, d.typ, d.value, d.path)
				comps := cloneComponents(p.Components)
				if err := setAt(comps[componentIndex(comps, d.component)].Properties, d.path, d.value); err != nil {
					add("%s: %v", label, err)
					continue
				}
				_, authored, err := Run(tr, comps, NoPolicy())
				if err != nil {
					add("%s, and writing it is refused: %v", label, err)
					continue
				}
				_, left := p.LeftToCluster[d.path]
				switch {
				case left && bytes.Equal(base, authored):
					add("%s, and the probe lists it as left to the cluster, yet writing it changes nothing: remove the entry from LeftToCluster", label)
					continue
				case left:
					continue
				case !bytes.Equal(base, authored):
					add("%s, and writing it changes the generated objects: the default is not what the handler applies; if the cluster applies it, list the path in the probe's LeftToCluster with the reason", label)
					continue
				}
				if !reportsPath(prov, d.component, d.path) {
					add("%s; the handler applies it (the objects are equal) and the report holds no policy or builtin entry at that path", label)
				}
			}
		}
	}
	return findings
}

// reportsPath reports whether the report holds a policy or builtin entry of the
// component, or of one of its members, at path.
func reportsPath(p *oam.Provenance, component, path string) bool {
	for _, e := range p.Entries {
		if e.Component == component && e.Path == path && e.Trait == "" &&
			(e.Source.Kind == oam.SourcePolicy || e.Source.Kind == oam.SourceBuiltin) {
			return true
		}
	}
	return false
}
