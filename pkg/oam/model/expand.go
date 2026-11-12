package model

import (
	"sort"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// partRead is one `parts.<part>.<key>` read in a site.
type partRead struct{ part, key string }

// appendPartReads adds each `parts.<part>.<key>` read in ast to reads, once.
func appendPartReads(reads []partRead, ast *cel.Ast) []partRead {
	celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		var op celast.Expr
		var key string
		switch e.Kind() {
		case celast.SelectKind:
			sel := e.AsSelect()
			if sel.IsTestOnly() {
				return
			}
			op, key = sel.Operand(), sel.FieldName()
		case celast.CallKind:
			// An index with a literal key, `parts.db['key']`, reads the same as `parts.db.key`.
			// An index with a computed key fails in CEL when the key is missing.
			call := e.AsCall()
			if call.FunctionName() != "_[_]" || len(call.Args()) != 2 || call.Args()[1].Kind() != celast.LiteralKind {
				return
			}
			k, ok := call.Args()[1].AsLiteral().Value().(string)
			if !ok {
				return
			}
			op, key = call.Args()[0], k
		default:
			return
		}
		// The checker folds the qualified name of a declared variable into one identifier,
		// so `parts.db.key` is a select of `key` on the identifier `parts.db`.
		if op.Kind() != celast.IdentKind || !strings.HasPrefix(op.AsIdent(), "parts.") {
			return
		}
		r := partRead{part: strings.TrimPrefix(op.AsIdent(), "parts."), key: key}
		for _, old := range reads {
			if old == r {
				return
			}
		}
		reads = append(reads, r)
	}))
	return reads
}

// checkPartReads refuses a read of an output key that the part does not declare, and names
// the site, the part, the key and the keys the part does declare. CEL's own failure for a
// missing map key names none of the part, and a read that gave an empty string would put a
// wrong object name into a document.
func (s *site) checkPartReads(path string, parts map[string]map[string]string) error {
	for _, r := range s.reads {
		outs, bound := parts[r.part]
		if !bound {
			return errors.Errorf("model: evaluate %s: parts.%s.%s: the scope holds no outputs of part %q", path, r.part, r.key, r.part)
		}
		if _, ok := outs[r.key]; ok {
			continue
		}
		declared := make([]string, 0, len(outs))
		for k := range outs {
			declared = append(declared, k)
		}
		sort.Strings(declared)
		return errors.Errorf("model: evaluate %s: parts.%s.%s: part %q declares no output %q (it declares: %s)", path, r.part, r.key, r.part, r.key, strings.Join(declared, ", "))
	}
	return nil
}

// Expand evaluates the model in document order into the components its parts emit, and binds
// `parts.<part>.<key>` as it goes: after a part that has no when and no forEach is built, the
// Transformer is asked for its outputs (Transformer.ComponentOutputsNamed, with naming as the
// consumer's Naming hook) and the object name of each is what a later part reads. A part reads
// only an earlier part of that kind (Compile refuses any other read), and only a key the part
// declares (Expand refuses it, naming the site, the part and the key).
//
// The values are evaluated first, from sc.Inputs and sc.Self. sc.Values, sc.Loop and sc.Parts
// are not read. A part whose when is false emits nothing. A part with forEach emits one
// component for each element, and a part without a name takes its id. tr must declare the
// type identities of the parts (registry.DeclareTypes for the built-in types): a part type that
// does not resolve declares no output, so a read of it fails as an undeclared key.
//
// partName turns the name of a part (its evaluated name, or its id) into the name of the
// component. A lowering rule passes the allocator here, so that a collision fails the build and
// the name a later part reads is the one the build uses. A nil partName keeps the name as it is.
// More than MaxParts components fail with ErrLimit, counted after when and forEach.
//
// It does not run the transform and does not check the traits against a capability: the
// components are the input of a later step. A failure returns no component.
func (c *Compiled) Expand(tr *oam.Transformer, sc Scope, naming func(oam.NameRequest) (string, bool), partName func(string) (string, error)) ([]oam.Component, error) {
	sc.Values = make(map[string]any, len(c.values))
	sc.Parts = map[string]map[string]string{}
	sc.Loop = nil
	for i, v := range c.values {
		val, _, err := c.Eval("spec.values["+strconv.Itoa(i)+"].expr", sc)
		if err != nil {
			return nil, err
		}
		sc.Values[v.Name] = val
	}
	var comps []oam.Component
	for i, p := range c.parts {
		base := "spec.parts[" + strconv.Itoa(i) + "]"
		out, err := c.expandPart(tr, base, p, sc, naming, partName, len(comps))
		if err != nil {
			return nil, err
		}
		comps = append(comps, out...)
	}
	if len(comps) == 0 {
		return nil, errors.Errorf("%w: spec.parts: no part emitted a component", ErrInvalid)
	}
	return comps, nil
}

// expandPart builds the components of one part and, for a part that is read, binds its outputs
// in sc.Parts. have is the number of components the earlier parts gave.
func (c *Compiled) expandPart(tr *oam.Transformer, base string, p Part, sc Scope, naming func(oam.NameRequest) (string, bool), partName func(string) (string, error), have int) ([]oam.Component, error) {
	if p.When != "" {
		on, _, err := c.Eval(base+".when", sc)
		if err != nil {
			return nil, err
		}
		if b, _ := on.(bool); !b {
			return nil, nil
		}
	}
	instances := []any{nil}
	if p.ForEach != nil {
		in, _, err := c.Eval(base+".forEach.in", sc)
		if err != nil {
			return nil, err
		}
		list, _ := in.([]any)
		instances = list
	}
	if have+len(instances) > MaxParts {
		return nil, errors.Errorf("%w: %s gives more than %d parts in one render", ErrLimit, base, MaxParts)
	}
	var comps []oam.Component
	for _, el := range instances {
		is := sc
		is.Loop = el
		comp, err := c.buildComponent(base, p, is)
		if err != nil {
			return nil, err
		}
		if partName != nil {
			name, err := partName(comp.Name)
			if err != nil {
				return nil, errors.Wrapf(err, "model: %s: part %q", base, p.ID)
			}
			comp.Name = name
		}
		if p.ForEach == nil && p.When == "" {
			outs, err := tr.ComponentOutputsNamed(sc.Self.Application, &comp, objectNameUnhooked(naming))
			if err != nil {
				return nil, errors.Wrapf(err, "model: %s: part %q", base, p.ID)
			}
			bound := make(map[string]string, len(outs))
			for _, o := range outs {
				bound[o.Key] = o.Name
			}
			sc.Parts[p.ID] = bound
		}
		comps = append(comps, comp)
	}
	return comps, nil
}

// objectNameUnhooked is naming without its answers for the role object. The build names the
// object of a kind part (a Secret, a ConfigMap) after its component and never asks the hook,
// because a lowering rule emitted it, so the query must not ask either.
func objectNameUnhooked(naming func(oam.NameRequest) (string, bool)) func(oam.NameRequest) (string, bool) {
	if naming == nil {
		return nil
	}
	return func(r oam.NameRequest) (string, bool) {
		if r.Role == oam.NameRoleObject {
			return "", false
		}
		return naming(r)
	}
}

// buildComponent evaluates the name, the properties, the traits and the annotations of p in sc.
func (c *Compiled) buildComponent(base string, p Part, sc Scope) (oam.Component, error) {
	comp := oam.Component{Name: p.ID, Type: p.Type}
	if p.Name != "" {
		name, _, err := c.Eval(base+".name", sc)
		if err != nil {
			return comp, err
		}
		s, ok := name.(string)
		if !ok {
			return comp, errors.Errorf("%w: %s.name: must give a string, got %T", ErrInvalid, base, name)
		}
		comp.Name = s
	}
	props, err := c.evalMap(base+".properties", p.Properties, sc)
	if err != nil {
		return comp, err
	}
	comp.Properties = props
	for j, t := range p.Traits {
		tp, err := c.evalMap(base+".traits["+strconv.Itoa(j)+"].properties", t.Properties, sc)
		if err != nil {
			return comp, err
		}
		comp.Traits = append(comp.Traits, oam.Trait{Type: t.Type, Properties: tp})
	}
	if len(p.Annotations) > 0 {
		comp.Annotations = make(map[string]string, len(p.Annotations))
		for k := range p.Annotations {
			val, _, err := c.Eval(base+".annotations."+k, sc)
			if err != nil {
				return comp, err
			}
			s, ok := val.(string)
			if !ok {
				return comp, errors.Errorf("%w: %s.annotations.%s: must give a string, got %T", ErrInvalid, base, k, val)
			}
			comp.Annotations[k] = s
		}
	}
	return comp, nil
}

// evalMap evaluates every string below m, at the site path the compile gave it.
func (c *Compiled) evalMap(path string, m map[string]any, sc Scope) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		r, err := c.evalTree(path+"."+k, v, sc)
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}

// evalTree is walk of eval.go for a value: a string is its site, a map or a list is its
// elements, and any other value is itself.
func (c *Compiled) evalTree(path string, v any, sc Scope) (any, error) {
	switch x := v.(type) {
	case string:
		r, _, err := c.Eval(path, sc)
		return r, err
	case map[string]any:
		return c.evalMap(path, x, sc)
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[keyText(k)] = e
		}
		return c.evalMap(path, m, sc)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := c.evalTree(path+"["+strconv.Itoa(i)+"]", e, sc)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}
