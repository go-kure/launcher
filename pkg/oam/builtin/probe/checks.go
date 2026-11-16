package probe

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-kure/launcher/pkg/oam"
)

// state is one switch position of the policy source.
type state struct {
	name   string
	policy func() oam.Policy
}

var states = []state{
	{"with the probe policy", ProbePolicy},
	{"without a policy", NoPolicy},
}

type entryKey struct {
	component, member, trait string
	traitIndex               int
	path                     string
}

func (k entryKey) String() string {
	owner := strings.TrimRight(k.component+"/"+k.member, "/")
	if k.trait != "" {
		owner += fmt.Sprintf(" trait %s[%d]", k.trait, k.traitIndex)
	}
	return owner + " " + k.path
}

// fillEntries returns the entries of source policy or builtin the report holds,
// by owner and path.
func fillEntries(p *oam.Provenance) map[entryKey]oam.SourceKind {
	out := map[entryKey]oam.SourceKind{}
	for _, e := range p.Entries {
		if e.Source.Kind == oam.SourcePolicy || e.Source.Kind == oam.SourceBuiltin {
			out[entryKey{e.Component, e.Member, e.Trait, e.TraitIndex, e.Path}] = e.Source.Kind
		}
	}
	return out
}

// expected returns the source a fill states for the state at index i.
func (f Fill) expected(i int) oam.SourceKind {
	if i == 0 {
		return f.WithPolicy
	}
	return f.WithoutPolicy
}

func (f Fill) key() entryKey { return entryKey{f.Component, f.Member, f.Trait, f.TraitIndex, f.Path} }

// properties returns the map a check writes the fill's value into: the
// properties of the component, or of its trait at TraitIndex when that trait has
// the fill's type (a throwaway map otherwise, so the value reaches nothing).
func (f Fill) properties(c oam.Component) map[string]any {
	if f.Trait == "" {
		return c.Properties
	}
	if f.TraitIndex < 0 || f.TraitIndex >= len(c.Traits) || c.Traits[f.TraitIndex].Type != f.Trait {
		return map[string]any{}
	}
	if c.Traits[f.TraitIndex].Properties == nil {
		c.Traits[f.TraitIndex].Properties = map[string]any{}
	}
	return c.Traits[f.TraitIndex].Properties
}

func (f Fill) value(i int) any {
	if i == 0 {
		return f.ValueWithPolicy
	}
	return f.ValueWithoutPolicy
}

func (p Probe) refused(i int) string {
	if i == 0 {
		return p.RefusedWithPolicy
	}
	return p.RefusedWithoutPolicy
}

// CheckReportedFillsAreReal runs every probe in both states and reports a
// finding where the report and the probe disagree, in either direction:
//
//   - an entry of source policy or builtin that no Fill of the probe lists: add a
//     Fill row for it or fix the report that names it;
//   - a Fill whose stated source is not the one the report holds, or a document
//     refused when the probe says it renders (or the reverse);
//   - a fill an author can write (a Fill value is set): authoring that value must
//     give the same generated objects as leaving it out, and no policy or builtin
//     entry at that path. A report of a fill that does not happen fails here.
//
// A fill no probe document triggers is not seen: the findings say what was
// observed in these probes, nothing wider.
func CheckReportedFillsAreReal(tr *oam.Transformer, probes []Probe) []string {
	var findings []string
	add := func(format string, args ...any) { findings = append(findings, fmt.Sprintf(format, args...)) }
	for _, p := range probes {
		for i, st := range states {
			label := fmt.Sprintf("probe %q %s", p.Name, st.name)
			prov, base, err := RunWith(tr, cloneComponents(p.Components), st.policy(), p.Capabilities)
			if want := p.refused(i); want != "" {
				switch {
				case err == nil:
					add("%s: the probe says the document is refused (%q), and it rendered", label, want)
				case !strings.Contains(err.Error(), want):
					add("%s: refused with %q, which does not contain the stated %q", label, err.Error(), want)
				}
				continue
			}
			if err != nil {
				add("%s: refused, and the probe does not say so: %v", label, err)
				continue
			}
			got := fillEntries(prov)
			listed := map[entryKey]bool{}
			for _, f := range p.Fills {
				k := f.key()
				listed[k] = true
				if want := f.expected(i); got[k] != want {
					add("%s: the report holds source %q at %s, the probe states %q", label, got[k], k, want)
				}
			}
			for _, k := range sortedKeys(got) {
				if !listed[k] {
					add("%s: the report holds a %s entry at %s that no Fill of the probe lists: add a Fill row for it to the probe, or fix the report that names it", label, got[k], k)
				}
			}
			for _, f := range p.Fills {
				v := f.value(i)
				if v != nil && f.expected(i) == "" {
					add("%s: the Fill at %s states a value and no source in this state, so the value is never checked: state the source or drop the value", label, f.Path)
					continue
				}
				if v == nil {
					continue
				}
				comps := cloneComponents(p.Components)
				at := componentIndex(comps, f.Component)
				if at < 0 {
					add("%s: Fill at %s names a component the probe does not have", label, f.Path)
					continue
				}
				if err := setAt(f.properties(comps[at]), f.Path, v); err != nil {
					add("%s: authoring %s: %v", label, f.Path, err)
					continue
				}
				authoredProv, authored, err := RunWith(tr, comps, st.policy(), p.Capabilities)
				if err != nil {
					add("%s: authoring %v at %s is refused: %v", label, v, f.Path, err)
					continue
				}
				if !bytes.Equal(base, authored) {
					add("%s: authoring %v at %s changes the generated objects, so the fill the report names is not what the library does", label, v, f.Path)
				}
				if src, ok := fillEntries(authoredProv)[f.key()]; ok {
					add("%s: the report still holds a %s entry at %s after the author wrote the value", label, src, f.Path)
				}
				if f.Other == nil {
					add("%s: the Fill at %s has a value and no Other, so nothing shows the path reaches a generated object: state an Other value", label, f.Path)
					continue
				}
				otherComps := cloneComponents(p.Components)
				if err := setAt(f.properties(otherComps[at]), f.Path, f.Other); err != nil {
					add("%s: authoring Other at %s: %v", label, f.Path, err)
					continue
				}
				if _, other, err := RunWith(tr, otherComps, st.policy(), p.Capabilities); err != nil {
					add("%s: authoring Other %v at %s is refused: %v", label, f.Other, f.Path, err)
				} else if bytes.Equal(base, other) {
					add("%s: authoring Other %v at %s leaves the generated objects unchanged, so the library never puts %s in an object and the report names a fill nobody sees", label, f.Other, f.Path, f.Path)
				}
			}
		}
	}
	return findings
}

func componentIndex(comps []oam.Component, name string) int {
	for i, c := range comps {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func sortedKeys(m map[entryKey]oam.SourceKind) []entryKey {
	keys := make([]entryKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}

// CheckProbePolicyCoversEveryDefault reports each Default* accessor of
// oam.Policy that p leaves at its zero value. A policy default no probe switches
// on is a default the probes cannot observe.
func CheckProbePolicyCoversEveryDefault(p oam.Policy) []string {
	var findings []string
	pt := reflect.TypeOf((*oam.Policy)(nil)).Elem()
	pv := reflect.ValueOf(p)
	for i := 0; i < pt.NumMethod(); i++ {
		name := pt.Method(i).Name
		if !strings.HasPrefix(name, "Default") {
			continue
		}
		out := pv.MethodByName(name).Call(nil)
		if len(out) == 1 && out[0].IsZero() {
			findings = append(findings, fmt.Sprintf("the probe policy returns no value from %s: set it in the probe policy, and add a probe that shows the default it supplies", name))
		}
	}
	return findings
}
