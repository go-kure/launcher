package probe

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	keys := tr.ComponentLoweringKeys()
	for _, p := range probes {
		prov, base, err := RunWith(tr, cloneComponents(p.Components), NoPolicy(), p.Capabilities)
		if err != nil {
			// A probe that states it is refused without a policy has no document to
			// compare; check B holds that outcome against the statement. Any other
			// refusal is a finding here too, never a silent skip.
			if p.RefusedWithoutPolicy == "" {
				add("probe %q: refused without a policy, and the probe does not say so, so no declared default was checked: %v", p.Name, err)
			}
			continue
		}
		seen := map[string]bool{}
		for _, comp := range p.Components {
			if _, ok := schemas[comp.Type]; !ok {
				add("probe %q: no schema is registered under the component type %q, so no declared default was checked", p.Name, comp.Type)
				continue
			}
			for _, d := range declaredDefaults(comp, schemas[comp.Type]) {
				seen[d.path] = true
				label := fmt.Sprintf("probe %q: the %s schema declares %v as the default of %s", p.Name, d.typ, d.value, d.path)
				comps := cloneComponents(p.Components)
				if err := setAt(comps[componentIndex(comps, d.component)].Properties, d.path, d.value); err != nil {
					add("%s: %v", label, err)
					continue
				}
				_, authored, err := RunWith(tr, comps, NoPolicy(), p.Capabilities)
				want, notAuthorable := p.NotAuthorable[d.path]
				switch {
				case notAuthorable && want == "":
					add("%s, and the probe lists it as not authorable with no text the refusal must contain, so any refusal would pass: state the text", label)
					continue
				case err != nil && notAuthorable && strings.Contains(err.Error(), want):
					continue
				case err != nil && notAuthorable:
					add("%s, and writing it is refused with %q, which does not contain the stated %q", label, err.Error(), want)
					continue
				case err != nil:
					add("%s, and writing it is refused: %v; if the document's own rule refuses it, list the path in the probe's NotAuthorable with the reason", label, err)
					continue
				case notAuthorable:
					add("%s, and the probe lists it as not authorable, yet writing it is accepted: remove the entry from NotAuthorable", label)
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
				reported := reportedAt(prov, d.component, placesOf(keys[d.typ], comp.Properties, d.path))
				_, unreported := p.Unreported[d.path]
				switch {
				case unreported && reported:
					add("%s, and the probe lists it as unreported, yet the report now holds an entry for it: remove the entry from Unreported", label)
				case unreported:
				case !reported:
					add("%s; the handler applies it (the objects are equal) and the report holds no policy, builtin or rendered entry at its place; if that is a known gap, list the path in the probe's Unreported with the reason", label)
				}
			}
		}
		for _, list := range []struct {
			name string
			rows map[string]string
		}{{"LeftToCluster", p.LeftToCluster}, {"Unreported", p.Unreported}, {"NotAuthorable", p.NotAuthorable}} {
			for _, path := range slices.Sorted(maps.Keys(list.rows)) {
				if !seen[path] {
					add("probe %q: %s lists %q, which is no declared default the probe leaves out: remove the entry or fix its path", p.Name, list.name, path)
				}
			}
		}
	}
	return findings
}

// place is one place the report can name a declared default at.
type place struct{ member, trait, path string }

// placesOf returns the places a declared default of a component with the
// authored properties props lands: where the type publishes a lowering key map,
// each place of the key that is live for props (oam.LoweredKey.LiveFor), and the
// authored path itself otherwise.
func placesOf(typeKeys map[string][]oam.LoweredKey, props map[string]any, path string) []place {
	key, _, _ := strings.Cut(path, ".")
	if places, ok := typeKeys[key]; ok {
		out := make([]place, 0, len(places))
		for _, l := range places {
			if l.LiveFor(props) {
				out = append(out, place{l.Member, l.Trait, l.Path})
			}
		}
		return out
	}
	return []place{{"", "", path}}
}

// reportedAt reports whether the report holds an entry of source policy, builtin
// or rendered for the component at every one of the places. An entry below a
// place's path counts for it, because provenance records leaves: a nested default
// under a key the rule builds (affinity) is therefore read as "some leaf below
// affinity", which is weaker than its own leaf, and the findings say so.
//
// Where no entry of the component names a member, a place is matched without its
// member: the report speaks under the plain owner there (oam.LoweredKey.Member).
func reportedAt(p *oam.Provenance, component string, places []place) bool {
	grouped := slices.ContainsFunc(p.Entries, func(e oam.PropertySource) bool {
		return e.Component == component && e.Member != ""
	})
	for _, pl := range places {
		if !grouped {
			pl.member = ""
		}
		if !reportedAtOne(p, component, pl) {
			return false
		}
	}
	return len(places) > 0
}

func reportedAtOne(p *oam.Provenance, component string, pl place) bool {
	for _, e := range p.Entries {
		if e.Component != component || e.Member != pl.member || e.Trait != pl.trait {
			continue
		}
		switch e.Source.Kind {
		case oam.SourcePolicy, oam.SourceBuiltin, oam.SourceRendered:
		default:
			continue
		}
		if pl.path == "" || e.Path == pl.path || strings.HasPrefix(e.Path, pl.path+".") || strings.HasPrefix(e.Path, pl.path+"[") {
			return true
		}
	}
	return false
}
