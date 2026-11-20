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

// partRead is one read below `parts.<part>` in a site: keys is the select chain after the
// part id (`service`, `name` for `parts.app.service.name`), and empty for a read of the part
// itself.
type partRead struct {
	part string
	keys []string
}

// text is the read as the document wrote it.
func (r partRead) text() string {
	return strings.Join(append([]string{"parts." + r.part}, r.keys...), ".")
}

// partChain reads e as a chain of selects and literal-key indexes on a part: `parts.db.key`,
// `parts.db['key']` and the same below a record. The checker folds the qualified name of a
// declared variable into one identifier, so `parts.db.key` is a select of `key` on the
// identifier `parts.db`. A select that only tests presence is no read, and an index with a
// computed key is not part of a chain: it fails in CEL when the key is missing, so a read of
// its operand stops at the part, which the check refuses.
func partChain(e celast.Expr) (part string, keys []string, ok bool) {
	switch e.Kind() {
	case celast.IdentKind:
		if id := e.AsIdent(); strings.HasPrefix(id, "parts.") {
			return strings.TrimPrefix(id, "parts."), nil, true
		}
	case celast.SelectKind:
		sel := e.AsSelect()
		if sel.IsTestOnly() {
			return "", nil, false
		}
		if p, ks, ok := partChain(sel.Operand()); ok {
			return p, append(ks, sel.FieldName()), true
		}
	case celast.CallKind:
		call := e.AsCall()
		if call.FunctionName() != "_[_]" || len(call.Args()) != 2 || call.Args()[1].Kind() != celast.LiteralKind {
			return "", nil, false
		}
		k, isString := call.Args()[1].AsLiteral().Value().(string)
		if !isString {
			return "", nil, false
		}
		if p, ks, ok := partChain(call.Args()[0]); ok {
			return p, append(ks, k), true
		}
	}
	return "", nil, false
}

// consumeChain marks e and the operands below it that partChain walks, so that the read of a
// chain is recorded once, at its longest.
func consumeChain(e celast.Expr, seen map[int64]bool) {
	for {
		seen[e.ID()] = true
		switch e.Kind() {
		case celast.SelectKind:
			e = e.AsSelect().Operand()
		case celast.CallKind:
			args := e.AsCall().Args()
			if len(args) != 2 {
				return
			}
			e = args[0]
		default:
			return
		}
	}
}

// appendPartReads adds each read below `parts.<part>` in ast to reads, once, as the longest
// chain: `parts.db.app_secret.name` is one read, and not also `parts.db.app_secret`.
func appendPartReads(reads []partRead, ast *cel.Ast) []partRead {
	seen := map[int64]bool{}
	celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if seen[e.ID()] {
			return
		}
		if e.Kind() == celast.SelectKind && e.AsSelect().IsTestOnly() {
			consumeChain(e.AsSelect().Operand(), seen)
			return
		}
		part, keys, ok := partChain(e)
		if !ok {
			return
		}
		consumeChain(e, seen)
		r := partRead{part: part, keys: keys}
		for _, old := range reads {
			if old.text() == r.text() {
				return
			}
		}
		reads = append(reads, r)
	}))
	return reads
}

// recordNameField is the field of a record that a document reads: the name of the object.
const recordNameField = "name"

// isRecordNameRead tells whether ast is exactly one read of the name of a record: the name of
// a part's own object, `parts.<part>.name`, or of one of its other outputs,
// `parts.<part>.<key>.name`. The type of such a read is a string, though the checker types
// everything below a part as dyn.
func isRecordNameRead(ast *cel.Ast) bool {
	_, keys, ok := partChain(ast.NativeRep().Expr())
	return ok && (len(keys) == 1 || len(keys) == 2) && keys[len(keys)-1] == recordNameField
}

// checkPartReads refuses a read that is not the name of a record the part declares, and names
// the site, the read and what the part declares. CEL's own failure for a missing map key names
// none of the part, and a read that gave an empty string would put a wrong object name into
// a document. The part's bound outputs are `name` (the part's own object) and one record,
// `{name}`, under the key of each other output.
func (s *site) checkPartReads(path string, parts map[string]map[string]any) error {
	for _, r := range s.reads {
		bound, ok := parts[r.part]
		if !ok {
			return errors.Errorf("model: evaluate %s: %s: the scope holds no outputs of part %q", path, r.text(), r.part)
		}
		if len(r.keys) == 0 {
			return errors.Errorf("model: evaluate %s: parts.%s: a part is read through its outputs, as parts.%s.%s for its own object or parts.%s.<key>.%s for another output (it declares: %s)",
				path, r.part, r.part, recordNameField, r.part, recordNameField, declaredFields(bound))
		}
		cur, owner := bound, "part "+strconv.Quote(r.part)
		for i, k := range r.keys {
			v, ok := cur[k]
			if !ok {
				return errors.Errorf("model: evaluate %s: %s: %s declares no output %q (it declares: %s)", path, r.text(), owner, k, declaredFields(cur))
			}
			here := strings.Join(append([]string{"parts." + r.part}, r.keys[:i+1]...), ".")
			switch x := v.(type) {
			case string:
				if i != len(r.keys)-1 {
					return errors.Errorf("model: evaluate %s: %s: %s is a name, which has no field %q", path, r.text(), here, r.keys[i+1])
				}
			case map[string]any:
				if i == len(r.keys)-1 {
					return errors.Errorf("model: evaluate %s: %s: %s is the record of an output: read its name, as %s.%s", path, r.text(), here, here, recordNameField)
				}
				cur, owner = x, "output "+strconv.Quote(k)+" of part "+strconv.Quote(r.part)
			}
		}
	}
	return nil
}

// declaredFields lists the keys of a bound map, sorted.
func declaredFields(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
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
	exp, err := c.expand(tr, sc, naming, partName)
	if err != nil {
		return nil, err
	}
	return exp.comps, nil
}

// expansion is what one expand gives: the components, the id of the part that gave each, and
// for each part that is read, the name of its component with the outputs the Transformer
// answered for it (published), and what a document reads of them (parts).
type expansion struct {
	comps     []oam.Component
	ids       []string
	parts     map[string]map[string]any
	published map[string]publishedPart
}

// publishedPart is the component a part gave and the outputs it publishes.
type publishedPart struct {
	component string
	outs      []oam.Output
}

// expand is Expand that also returns, for each component, the id of the part that gave it.
func (c *Compiled) expand(tr *oam.Transformer, sc Scope, naming func(oam.NameRequest) (string, bool), partName func(string) (string, error)) (*expansion, error) {
	sc.Values = make(map[string]any, len(c.values))
	sc.Parts = map[string]map[string]any{}
	sc.published = map[string]publishedPart{}
	sc.Loop = nil
	for i, v := range c.values {
		val, _, err := c.Eval("spec.values["+strconv.Itoa(i)+"].expr", sc)
		if err != nil {
			return nil, err
		}
		sc.Values[v.Name] = val
	}
	exp := &expansion{parts: sc.Parts, published: sc.published}
	for i, p := range c.parts {
		base := "spec.parts[" + strconv.Itoa(i) + "]"
		out, err := c.expandPart(tr, base, p, sc, naming, partName, len(exp.comps))
		if err != nil {
			return nil, err
		}
		exp.comps = append(exp.comps, out...)
		for range out {
			exp.ids = append(exp.ids, p.ID)
		}
	}
	if len(exp.comps) == 0 {
		return nil, errors.Errorf("%w: spec.parts: no part emitted a component", ErrInvalid)
	}
	return exp, nil
}

// publish answers the outputs of the model after an expansion: each is the record of a part,
// forwarded under the key of the output, with the kind and the name the Transformer answered
// for that part. A read of a key the part does not declare is refused, naming the output, the
// part and what the part declares (checkPartReads).
func (c *Compiled) publish(exp *expansion) ([]oam.OutputDecl, error) {
	decls := make([]oam.OutputDecl, 0, len(c.outputs))
	for _, o := range c.outputs {
		path := "spec.outputs." + o.key
		read := &site{reads: []partRead{{part: o.part, keys: o.keys}}}
		if err := read.checkPartReads(path, exp.parts); err != nil {
			return nil, err
		}
		p := exp.published[o.part]
		for _, out := range p.outs {
			if out.Key == o.field {
				decls = append(decls, oam.OutputDecl{Key: o.key, Kind: out.Kind, Name: out.Name, ForwardOf: p.component, ForwardKey: o.field})
				break
			}
		}
	}
	return decls, nil
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
	if err := sc.budget.chargeParts(len(instances)); err != nil {
		return nil, errors.Wrapf(err, "model: %s", base)
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
			outs, err := tr.ComponentOutputsNamed(sc.Self.Application, &comp, naming)
			if err != nil {
				return nil, errors.Wrapf(err, "model: %s: part %q", base, p.ID)
			}
			// The part's own object is its `name`, and every other output is a record under
			// its key. The Go API keeps the whole Output, and a document reads the name.
			bound := make(map[string]any, len(outs))
			for _, o := range outs {
				if o.Key == oam.ObjectOutputKey {
					bound[recordNameField] = o.Name
					continue
				}
				bound[o.Key] = map[string]any{recordNameField: o.Name}
			}
			sc.Parts[p.ID] = bound
			sc.published[p.ID] = publishedPart{component: comp.Name, outs: outs}
		}
		comps = append(comps, comp)
	}
	return comps, nil
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
